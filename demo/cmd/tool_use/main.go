package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nanocc/demo/internal/common"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

var (
	developerMessage = "You are a coding agent. Use tools `bash`, `read_file`, and `write_file` when needed. When task is complete, reply directly."
)

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
