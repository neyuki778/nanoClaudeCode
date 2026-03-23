package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type toolHandler func(arguments string) string

type toolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     toolHandler
}

type toolField struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type todoTask struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type todoStateStore struct {
	// 进程内 TODO 状态。
	mu        sync.RWMutex
	tasks     []todoTask
	currentID string
	version   int
}

func newTodoStateStore() *todoStateStore {
	return &todoStateStore{}
}

func (s *todoStateStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = nil
	s.currentID = ""
	s.version = 0
}

func (s *todoStateStore) Set(tasks []todoTask, currentID string) (int, error) {
	// 采用“整包覆盖”策略：模型每次提交完整最新状态，避免增量更新错乱。
	if err := validateTodo(tasks, currentID); err != nil {
		return 0, err
	}

	copied := make([]todoTask, 0, len(tasks))
	for _, task := range tasks {
		copied = append(copied, todoTask{
			ID:   strings.TrimSpace(task.ID),
			Text: strings.TrimSpace(task.Text),
			Done: task.Done,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = copied
	s.currentID = strings.TrimSpace(currentID)
	s.version++
	return s.version, nil
}

func (s *todoStateStore) snapshot() ([]todoTask, string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]todoTask, 0, len(s.tasks))
	tasks = append(tasks, s.tasks...)
	return tasks, s.currentID, s.version
}

func (s *todoStateStore) ContextMessage() string {
	// 注入给模型的最新 TODO 摘要（每轮请求都会附带）。
	tasks, currentID, version := s.snapshot()
	if len(tasks) == 0 {
		return "Current TODO status: (empty). Use `todo_set` to initialize and maintain task progress."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Current TODO status (v%d):\n", version)
	for _, task := range tasks {
		state := " "
		if task.Done {
			state = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", state, task.ID, task.Text)
	}
	if currentID == "" {
		b.WriteString("Current task: (none)\n")
	} else {
		fmt.Fprintf(&b, "Current task: %s\n", currentID)
	}
	b.WriteString("When progress changes, call `todo_set` with full latest state.")
	return b.String()
}

func (s *todoStateStore) RenderForUser() string {
	// 展示给终端用户的可读格式。
	tasks, currentID, version := s.snapshot()
	if len(tasks) == 0 {
		return "TODO v0: (empty)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "TODO v%d\n", version)
	for _, task := range tasks {
		state := " "
		if task.Done {
			state = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", state, task.ID, task.Text)
	}
	if currentID == "" {
		b.WriteString("Current: (none)")
	} else {
		fmt.Fprintf(&b, "Current: %s", currentID)
	}
	return b.String()
}

func validateTodo(tasks []todoTask, currentID string) error {
	// MVP 一致性约束：
	// - id 唯一且非空
	// - text 非空
	// - current_id 要么为空，要么指向一个未完成任务
	if len(tasks) > 50 {
		return fmt.Errorf("too many tasks: max 50")
	}
	trimmedCurrent := strings.TrimSpace(currentID)
	if len(tasks) == 0 && trimmedCurrent != "" {
		return fmt.Errorf("current_id must be empty when tasks is empty")
	}

	// 判断 task 是否重复的 set
	seen := make(map[string]struct{}, len(tasks))
	hasCurrent := trimmedCurrent == ""
	for index, task := range tasks {
		id := strings.TrimSpace(task.ID)
		text := strings.TrimSpace(task.Text)
		if id == "" {
			return fmt.Errorf("tasks[%d].id is empty", index)
		}
		if text == "" {
			return fmt.Errorf("tasks[%d].text is empty", index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate task id: %s", id)
		}
		seen[id] = struct{}{}
		if id == trimmedCurrent {
			hasCurrent = true
			if task.Done {
				return fmt.Errorf("current_id task is marked done: %s", id)
			}
		}
	}
	if !hasCurrent {
		return fmt.Errorf("current_id not found in tasks: %s", trimmedCurrent)
	}
	return nil
}

func defaultToolSpecs(todo *todoStateStore) []toolSpec {
	// 工具注册中心：新增工具时在这里挂 spec。
	return []toolSpec{
		newTool(
			"bash",
			"Execute a shell command in current workspace and return stdout/stderr.",
			bashHandler,
			reqString("command", "Shell command to run"),
		),
		newTool(
			"read_file",
			"Read file contents from workspace.",
			readFileHandler,
			reqString("path", "Relative file path to read"),
			optInteger("limit", "Max bytes to return (optional)"),
		),
		newTool(
			"write_file",
			"Write file contents into workspace.",
			writeFileHandler,
			reqString("path", "Relative file path to write"),
			reqString("content", "File content to write"),
		),
		{
			Name:        "todo_set",
			Description: "Replace TODO state with latest full list and current task.",
			// todo_set 使用专用 schema，要求一次提交完整状态。
			Parameters: todoSetSchema(),
			Handler:    todoSetHandler(todo),
		},
	}
}

func todoSetSchema() map[string]any {
	// 通过 additionalProperties=false 约束模型只输出定义过的字段。
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "Full task list.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Stable task id, e.g. t1.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "Task description.",
						},
						"done": map[string]any{
							"type":        "boolean",
							"description": "Whether task is completed.",
						},
					},
					"required": []string{"id", "text", "done"},
				},
			},
			"current_id": map[string]any{
				"type":        "string",
				"description": "Current in-progress task id; empty means none.",
			},
		},
		"required": []string{"tasks", "current_id"},
	}
}

func newTool(name, description string, handler toolHandler, fields ...toolField) toolSpec {
	return toolSpec{
		Name:        name,
		Description: description,
		Parameters:  objectSchemaFromFields(fields...),
		Handler:     handler,
	}
}

func reqString(name, description string) toolField {
	return toolField{Name: name, Type: "string", Description: description, Required: true}
}

func optInteger(name, description string) toolField {
	return toolField{Name: name, Type: "integer", Description: description, Required: false}
}

func buildTools(specs []toolSpec) []responses.ToolUnionParam {
	tools := make([]responses.ToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        spec.Name,
				Description: openai.String(spec.Description),
				Parameters:  spec.Parameters,
			},
		})
	}
	return tools
}

func buildToolHandlers(specs []toolSpec) map[string]toolHandler {
	handlers := make(map[string]toolHandler, len(specs))
	for _, spec := range specs {
		handlers[spec.Name] = spec.Handler
	}
	return handlers
}

func objectSchemaFromFields(fields ...toolField) map[string]any {
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		prop := map[string]any{
			"type": field.Type,
		}
		if field.Description != "" {
			prop["description"] = field.Description
		}
		properties[field.Name] = prop
		if field.Required {
			required = append(required, field.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func parseCommand(arguments string) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return "", fmt.Errorf("empty command")
	}
	return args.Command, nil
}

func parseReadFileArgs(arguments string) (string, int, error) {
	var args struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", 0, err
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return "", 0, fmt.Errorf("empty path")
	}
	return args.Path, args.Limit, nil
}

func parseWriteFileArgs(arguments string) (string, string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", "", err
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return "", "", fmt.Errorf("empty path")
	}
	return args.Path, args.Content, nil
}

func parseTodoSetArgs(arguments string) ([]todoTask, string, error) {
	var args struct {
		Tasks     []todoTask `json:"tasks"`
		CurrentID string     `json:"current_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, "", err
	}

	normalized := make([]todoTask, 0, len(args.Tasks))
	for _, task := range args.Tasks {
		normalized = append(normalized, todoTask{
			ID:   strings.TrimSpace(task.ID),
			Text: strings.TrimSpace(task.Text),
			Done: task.Done,
		})
	}
	currentID := strings.TrimSpace(args.CurrentID)
	return normalized, currentID, nil
}

func bashHandler(arguments string) string {
	cmd, err := parseCommand(arguments)
	if err != nil {
		return "invalid args: " + err.Error()
	}
	fmt.Printf("Tool use: bash %s\n", cmd)
	return runBash(cmd)
}

func readFileHandler(arguments string) string {
	path, limit, err := parseReadFileArgs(arguments)
	if err != nil {
		return "invalid args: " + err.Error()
	}
	safe, err := safeWorkspacePath(path)
	if err != nil {
		return "invalid path: " + err.Error()
	}
	data, err := os.ReadFile(safe)
	if err != nil {
		return "error: " + err.Error()
	}
	if limit <= 0 {
		limit = 10000
	}
	if limit > 50000 {
		limit = 50000
	}
	if len(data) > limit {
		return string(data[:limit]) + fmt.Sprintf("\n\n(truncated: %d/%d bytes)", limit, len(data))
	}
	return string(data)
}

func writeFileHandler(arguments string) string {
	path, content, err := parseWriteFileArgs(arguments)
	if err != nil {
		return "invalid args: " + err.Error()
	}
	safe, err := safeWorkspacePath(path)
	if err != nil {
		return "invalid path: " + err.Error()
	}
	dir := filepath.Dir(safe)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "error: " + err.Error()
		}
	}
	if err := os.WriteFile(safe, []byte(content), 0o644); err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("ok: wrote %d bytes to %s", len(content), path)
}

func todoSetHandler(todo *todoStateStore) toolHandler {
	return func(arguments string) string {
		tasks, currentID, err := parseTodoSetArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		version, err := todo.Set(tasks, currentID)
		if err != nil {
			return "invalid todo: " + err.Error()
		}

		doneCount := 0
		for _, task := range tasks {
			if task.Done {
				doneCount++
			}
		}
		// 返回可读摘要，便于模型和用户同时确认当前 TODO 状态。
		return fmt.Sprintf("todo updated v%d: %d total, %d done\n%s", version, len(tasks), doneCount, todo.RenderForUser())
	}
}

func safeWorkspacePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path is not allowed")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid path")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return clean, nil
}

func runBash(command string) string {
	blocked := []string{"rm -rf /", "shutdown", "reboot", "mkfs", ":(){:|:&};:"}
	for _, b := range blocked {
		if strings.Contains(command, b) {
			return "blocked: dangerous command"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "zsh", "-lc", command)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "error: command timeout (30s)"
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = "(no output)"
	}
	if err != nil {
		return "error: " + err.Error() + "\n" + text
	}
	if len(text) > 50000 {
		return text[:50000]
	}
	return text
}
