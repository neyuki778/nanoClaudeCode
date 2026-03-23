package main

import (
	"context"
	"fmt"
	"time"

	"nanocc/demo/internal/common"

	"github.com/openai/openai-go/v3"
)

func main() {
	cfg := common.LoadConfig()
	fmt.Printf("Using OPENAI_BASE_URL=%s, OPENAI_MODEL=%s, DEBUG_HTTP=%t\n", cfg.BaseURL, cfg.Model, cfg.DebugHTTP)
	client := common.NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("你好，请用一句话回复：自定义 BaseURL 调用成功。"),
		},
		Model: openai.ChatModel(cfg.Model),
	})
	if err != nil {
		panic(err)
	}

	if len(resp.Choices) == 0 {
		fmt.Println("no choices returned")
		return
	}

	fmt.Println(resp.Choices[0].Message.Content)
}
