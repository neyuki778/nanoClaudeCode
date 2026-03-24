package main

import (
	"context"
	"encoding/json"
	"fmt"
	"nanocc/agents/skills"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

const (
	todoPhaseEmpty     = "empty"
	todoPhaseActive    = "active"
	todoPhaseCompleted = "completed"
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

type subAgentSpawnArgs struct {
	TaskSummary string `json:"task_summary"`
	TimeoutSec  int    `json:"timeout_sec"`
	Retries     int    `json:"retries"`
}

type subAgentWaitArgs struct {
	AgentIDs []string `json:"agent_ids"`
}

type skillNameArgs struct {
	Name string `json:"name"`
}

type todoTask struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type todoSnapshot struct {
	tasks       []todoTask
	lastID      string
	lastVersion int
}

type todoStateStore struct {
	// 进程内 TODO 状态。
	mu            sync.RWMutex
	tasks         []todoTask
	currentID     string
	version       int
	phase         string
	lastCompleted *todoSnapshot
}

func newTodoStateStore() *todoStateStore {
	return &todoStateStore{phase: todoPhaseEmpty}
}

func (s *todoStateStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = nil
	s.currentID = ""
	s.version = 0
	s.phase = todoPhaseEmpty
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
	if isTodoCompletedState(s.tasks, s.currentID) {
		s.phase = todoPhaseCompleted
		completedTasks := make([]todoTask, len(s.tasks))
		copy(completedTasks, s.tasks)
		s.lastCompleted = &todoSnapshot{
			tasks:       completedTasks,
			lastID:      s.currentID,
			lastVersion: s.version,
		}
	} else {
		s.phase = todoPhaseActive
	}
	return s.version, nil
}

func (s *todoStateStore) snapshot() ([]todoTask, string, int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]todoTask, 0, len(s.tasks))
	tasks = append(tasks, s.tasks...)
	return tasks, s.currentID, s.version, s.phase
}

func (s *todoStateStore) ContextMessage() string {
	// 注入给模型的最新 TODO 摘要（每轮请求都会附带）。
	tasks, currentID, version, phase := s.snapshot()
	if len(tasks) == 0 {
		return "Current TODO status: (empty). For simple single-turn Q&A, reply directly without TODO. Only call `todo_set` when the task is non-trivial and truly multi-step."
	}
	if phase == todoPhaseCompleted {
		var b strings.Builder
		fmt.Fprintf(&b, "Current TODO status (v%d): COMPLETED.\n", version)
		for _, task := range tasks {
			fmt.Fprintf(&b, "- [x] %s: %s\n", task.ID, task.Text)
		}
		b.WriteString("Current task: (none)\n")
		b.WriteString("The TODO is already complete for this user request. Reply directly to the user now, and do not call `todo_set` again.")
		return b.String()
	}

	var b strings.Builder
	// if phase == todoPhaseCompleted {
	// 	fmt.Fprintf(&b, "Current TODO status: (empty). Last TODO status: ")
	// 	for _, task := range tasks {
	// 		fmt.Fprintf(&b, )
	// 	}
	// }
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
	tasks, currentID, version, _ := s.snapshot()
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

func isTodoCompletedState(tasks []todoTask, currentID string) bool {
	if len(tasks) == 0 || strings.TrimSpace(currentID) != "" {
		return false
	}
	for _, task := range tasks {
		if !task.Done {
			return false
		}
	}
	return true
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

func baseToolSpecs(todo *todoStateStore) []toolSpec {
	// 所有 agent 都可用的基础工具集合。
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

func parentToolSpecs(
	todo *todoStateStore,
	manager *subAgentManager,
	runner subAgentRunner,
	skillState *skills.State,
	skillRegistry *skills.Registry,
) []toolSpec {
	specs := append([]toolSpec{}, baseToolSpecs(todo)...)
	specs = append(specs,
		toolSpec{
			Name:        "skill_list",
			Description: "List all skills discovered from .skills directory and show active flags.",
			Parameters:  skillListSchema(),
			Handler:     skillListHandler(skillRegistry, skillState),
		},
		toolSpec{
			Name:        "skill_load",
			Description: "Activate one skill by name for subsequent turns.",
			Parameters:  skillNameSchema("Skill name to activate."),
			Handler:     skillLoadHandler(skillRegistry, skillState),
		},
		toolSpec{
			Name:        "skill_unload",
			Description: "Deactivate one skill by name.",
			Parameters:  skillNameSchema("Skill name to deactivate."),
			Handler:     skillUnloadHandler(skillState),
		},
		toolSpec{
			Name:        "subagent_spawn",
			Description: "Create and start one sub-agent job asynchronously.",
			Parameters:  subAgentSpawnSchema(),
			Handler:     subAgentSpawnHandler(manager, runner),
		},
		toolSpec{
			Name:        "subagent_wait",
			Description: "Wait for specific sub-agent jobs (or all when omitted) and return summaries.",
			Parameters:  subAgentWaitSchema(),
			Handler:     subAgentWaitHandler(manager),
		},
	)
	return specs
}

func childToolSpecs(todo *todoStateStore) []toolSpec {
	// 子代理禁止使用 subagent_* 工具（不可递归创建子代理）。
	return append([]toolSpec{}, baseToolSpecs(todo)...)
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

func subAgentSpawnSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_summary": map[string]any{
				"type":        "string",
				"description": "Task summary for the sub-agent to execute.",
			},
			"timeout_sec": map[string]any{
				"type":        "integer",
				"description": "Per-attempt timeout in seconds.",
			},
			"retries": map[string]any{
				"type":        "integer",
				"description": "Retry count after failure. Max 2.",
			},
		},
		"required": []string{"task_summary", "timeout_sec"},
	}
}

func subAgentWaitSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"agent_ids": map[string]any{
				"type":        "array",
				"description": "Target sub-agent IDs. Empty means wait all.",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}
}

func skillListSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

func skillNameSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": description,
			},
		},
		"required": []string{"name"},
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

func parseSubAgentSpawnArgs(arguments string) (subAgentSpawnArgs, error) {
	var args subAgentSpawnArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return subAgentSpawnArgs{}, err
	}
	args.TaskSummary = strings.TrimSpace(args.TaskSummary)
	if args.TaskSummary == "" {
		return subAgentSpawnArgs{}, fmt.Errorf("empty task_summary")
	}
	if args.TimeoutSec <= 0 {
		return subAgentSpawnArgs{}, fmt.Errorf("timeout_sec must be positive")
	}
	if args.TimeoutSec > 3600 {
		return subAgentSpawnArgs{}, fmt.Errorf("timeout_sec too large: max 3600")
	}
	if args.Retries < 0 {
		return subAgentSpawnArgs{}, fmt.Errorf("retries must be >= 0")
	}
	return args, nil
}

func parseSubAgentWaitArgs(arguments string) (subAgentWaitArgs, error) {
	if strings.TrimSpace(arguments) == "" {
		return subAgentWaitArgs{}, nil
	}

	var args subAgentWaitArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return subAgentWaitArgs{}, err
	}
	normalized := make([]string, 0, len(args.AgentIDs))
	seen := make(map[string]struct{}, len(args.AgentIDs))
	for _, id := range args.AgentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	args.AgentIDs = normalized
	return args, nil
}

func parseSkillNameArgs(arguments string) (skillNameArgs, error) {
	var args skillNameArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return skillNameArgs{}, err
	}
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" {
		return skillNameArgs{}, fmt.Errorf("empty name")
	}
	return args, nil
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

func subAgentSpawnHandler(manager *subAgentManager, runner subAgentRunner) toolHandler {
	return func(arguments string) string {
		if manager == nil {
			return "error: subagent manager is not configured"
		}
		args, err := parseSubAgentSpawnArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}
		if args.Retries == 0 {
			args.Retries = 2
		}

		jobID, err := manager.Spawn(
			args.TaskSummary,
			time.Duration(args.TimeoutSec)*time.Second,
			args.Retries,
			runner,
		)
		if err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("subagent spawned: %s", jobID)
	}
}

func subAgentWaitHandler(manager *subAgentManager) toolHandler {
	return func(arguments string) string {
		if manager == nil {
			return "error: subagent manager is not configured"
		}
		args, err := parseSubAgentWaitArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		jobs := manager.Wait(args.AgentIDs)
		return formatWaitResult(jobs)
	}
}

func skillListHandler(registry *skills.Registry, state *skills.State) toolHandler {
	return func(arguments string) string {
		if strings.TrimSpace(arguments) != "" && strings.TrimSpace(arguments) != "{}" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
				return "invalid args: " + err.Error()
			}
		}
		if registry == nil || registry.Count() == 0 {
			return "skills: none found under .skills"
		}

		activeSet := make(map[string]struct{})
		if state != nil {
			for _, name := range state.ActiveNames() {
				activeSet[name] = struct{}{}
			}
		}

		defs := registry.List()
		var b strings.Builder
		fmt.Fprintf(&b, "skills found: %d\n", len(defs))
		for _, def := range defs {
			flag := "inactive"
			if _, ok := activeSet[def.Name]; ok {
				flag = "active"
			}
			fmt.Fprintf(&b, "- %s [%s]: %s\n", def.Name, flag, def.Description)
		}
		return strings.TrimSpace(b.String())
	}
}

func skillLoadHandler(registry *skills.Registry, state *skills.State) toolHandler {
	return func(arguments string) string {
		if registry == nil || state == nil {
			return "error: skills runtime is not configured"
		}
		args, err := parseSkillNameArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		def, loaded, err := state.Load(args.Name, registry)
		if err != nil {
			return "error: " + err.Error()
		}
		if !loaded {
			return fmt.Sprintf("skill already active: %s", def.Name)
		}
		return fmt.Sprintf("skill loaded: %s", def.Name)
	}
}

func skillUnloadHandler(state *skills.State) toolHandler {
	return func(arguments string) string {
		if state == nil {
			return "error: skills runtime is not configured"
		}
		args, err := parseSkillNameArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		name := skills.NormalizeName(args.Name)
		if !state.Unload(name) {
			return fmt.Sprintf("skill not active: %s", name)
		}
		return fmt.Sprintf("skill unloaded: %s", name)
	}
}

func formatWaitResult(jobs []subAgentJob) string {
	if len(jobs) == 0 {
		return "no subagent jobs found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "subagent wait finished: %d job(s)\n", len(jobs))
	for _, job := range jobs {
		fmt.Fprintf(&b, "- %s [%s] attempt=%d", job.ID, job.Status, job.Attempt)
		if job.Status == subAgentJobSucceeded && strings.TrimSpace(job.ResultText) != "" {
			fmt.Fprintf(&b, "\n  result: %s", strings.TrimSpace(job.ResultText))
		}
		if job.Status != subAgentJobSucceeded && strings.TrimSpace(job.ErrorText) != "" {
			fmt.Fprintf(&b, "\n  error: %s", strings.TrimSpace(job.ErrorText))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
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
