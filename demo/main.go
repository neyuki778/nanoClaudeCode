package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	loadDotEnv()

	baseURL := getenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	apiKey := getenv("OPENAI_API_KEY", "your-api-key")
	model := getenv("OPENAI_MODEL", "gpt-4o")
	fmt.Printf("Using OPENAI_BASE_URL=%s, OPENAI_MODEL=%s\n", baseURL, model)

	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("你好，请用一句话回复：自定义 BaseURL 调用成功。"),
		},
		Model: openai.ChatModel(model),
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

func loadDotEnv() {
	// Try current directory first, then repository root when running from demo/.
	_ = godotenv.Load(".env", "../.env")
}

func getenv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
