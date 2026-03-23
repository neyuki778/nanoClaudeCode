package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nanocc/demo/internal/common"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

var (
	developerMessage = "You are a coding agent. Use tools `bash`, `read_file`, and `write_file` when needed. When task is complete, reply directly."
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

func main() {
	cfg := common.LoadConfig()
	if cfg.APIKey == "" {
		panic("OPENAI_API_KEY is empty")
	}

	client := common.NewClient(cfg)

	specs := defaultToolSpecs()
	tools := buildTools(specs)
	handlers := buildToolHandlers(specs)

	messages := []responses.ResponseInputItemUnionParam{
		responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
	}

	fmt.Printf("Tool-use agent started. base_url=%s model=%s debug_http=%t\n", cfg.BaseURL, cfg.Model, cfg.DebugHTTP)
	fmt.Println("Type your message. Commands: /reset, /exit")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			fmt.Println("bye")
			return
		}
		if text == "/reset" {
			messages = []responses.ResponseInputItemUnionParam{
				responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
			}
			fmt.Println("context reset")
			continue
		}

		messages = append(messages, responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleUser))
		answer, err := runToolLoop(client, cfg.Model, tools, handlers, messages)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		// Persist assistant final text into history.
		messages = append(messages, responses.ResponseInputItemParamOfMessage(answer, responses.EasyInputMessageRoleAssistant))
		fmt.Print(">>> ")
		fmt.Println(answer)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("input error: %v\n", err)
	}
}

func runToolLoop(
	client openai.Client,
	model string,
	tools []responses.ToolUnionParam,
	handlers map[string]toolHandler,
	messages []responses.ResponseInputItemUnionParam,
) (string, error) {
	inputItems := append([]responses.ResponseInputItemUnionParam{}, messages...)

	for step := 0; step < 20; step++ {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		params := responses.ResponseNewParams{
			Model: openai.ResponsesModel(model),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: inputItems,
			},
			Tools: tools,
		}
		resp, err := client.Responses.New(ctx, params)
		cancel()
		if err != nil {
			return "", err
		}

		followUpItems := make([]responses.ResponseInputItemUnionParam, 0, len(resp.Output)*2)
		for _, item := range resp.Output {
			if item.Type != "function_call" {
				continue
			}
			// 显式回放 function_call，便于不支持 previous_response_id tool state 的服务端匹配 call_id
			followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCall(item.Arguments, item.CallID, item.Name))

			handler, ok := handlers[item.Name]
			if !ok {
				followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, "unsupported tool"))
				continue
			}

			out := handler(item.Arguments)
			fmt.Printf("Tool use output: %s\n", out)
			followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, out))
		}

		if len(followUpItems) == 0 {
			return strings.TrimSpace(resp.OutputText()), nil
		}

		inputItems = append(inputItems, followUpItems...)
	}

	return "", fmt.Errorf("tool loop exceeded max steps")
}

func defaultToolSpecs() []toolSpec {
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
