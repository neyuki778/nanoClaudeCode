package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"nanocc/demo/internal/common"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

func main() {
	cfg := common.LoadConfig()
	if cfg.APIKey == "" {
		panic("OPENAI_API_KEY is empty")
	}

	client := common.NewClient(cfg)

	tools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        "bash",
			Description: openai.String("Execute a shell command in current workspace and return stdout/stderr."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to run",
					},
				},
				"required": []string{"command"},
			},
		}),
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.DeveloperMessage("You are a coding agent. Use tool `bash` when execution is needed. When task is complete, reply directly."),
	}

	fmt.Printf("Tool-use agent started. base_url=%s model=%s\n", cfg.BaseURL, cfg.Model)
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
			messages = []openai.ChatCompletionMessageParamUnion{
				openai.DeveloperMessage("You are a coding agent. Use tool `bash` when execution is needed. When task is complete, reply directly."),
			}
			fmt.Println("context reset")
			continue
		}

		messages = append(messages, openai.UserMessage(text))
		answer, err := runToolLoop(client, cfg.Model, tools, messages)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		// Persist assistant final text into history.
		messages = append(messages, openai.AssistantMessage(answer))
		fmt.Print(">> ")
		fmt.Println(answer)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("input error: %v\n", err)
	}
}

func runToolLoop(
	client openai.Client,
	model string,
	tools []openai.ChatCompletionToolUnionParam,
	messages []openai.ChatCompletionMessageParamUnion,
) (string, error) {
	local := append([]openai.ChatCompletionMessageParamUnion{}, messages...)

	for step := 0; step < 20; step++ {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(model),
			Messages: local,
			Tools:    tools,
		})
		cancel()
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("no choices returned")
		}

		msg := resp.Choices[0].Message
		local = append(local, msg.ToParam())

		if len(msg.ToolCalls) == 0 {
			return strings.TrimSpace(msg.Content), nil
		}

		for _, call := range msg.ToolCalls {
			fc := call.Function
			if call.Type != "function" || fc.Name != "bash" {
				local = append(local, openai.ToolMessage("unsupported tool", call.ID))
				continue
			}

			cmd, err := parseCommand(fc.Arguments)
			if err != nil {
				local = append(local, openai.ToolMessage("invalid args: "+err.Error(), call.ID))
				continue
			}

			out := runBash(cmd)
			local = append(local, openai.ToolMessage(out, call.ID))
		}
	}

	return "", fmt.Errorf("tool loop exceeded max steps")
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
