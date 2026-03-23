package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
