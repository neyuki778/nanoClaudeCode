package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

type TurnItem struct {
	Role string
	Text string
}

func main() {
	loadDotEnv()

	baseURL := normalizeBaseURL(getenv("OPENAI_BASE_URL", "http://localhost:11434/v1"))
	apiKey := normalizeAPIKey(getenv("OPENAI_API_KEY", ""))
	model := getenv("OPENAI_MODEL", "gpt-4o")
	if apiKey == "" {
		panic("OPENAI_API_KEY is empty")
	}

	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
	)

	fmt.Printf("Chatbot started. base_url=%s model=%s\n", baseURL, model)
	fmt.Println("Type your message. Commands: /reset, /exit")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	history := make([]TurnItem, 0, 24)

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
			history = history[:0]
			fmt.Println("context reset")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		params := responses.ResponseNewParams{
			Model: openai.ResponsesModel(model),
			Input: responses.ResponseNewParamsInputUnion{
				OfString: openai.String(buildPrompt(history, text, 12)),
			},
		}

		resp, err := client.Responses.New(ctx, params)
		cancel()
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		answer := strings.TrimSpace(resp.OutputText())
		fmt.Println(answer)
		history = append(history, TurnItem{Role: "user", Text: text}, TurnItem{Role: "assistant", Text: answer})
		if len(history) > 24 {
			history = history[len(history)-24:]
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("input error: %v\n", err)
	}
}

func loadDotEnv() {
	_ = godotenv.Overload(".env", "../.env", "../../.env")
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func normalizeAPIKey(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "Bearer ")
	return strings.TrimSpace(v)
}

func normalizeBaseURL(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimRight(v, "/")
	v = strings.TrimSuffix(v, "/chat/completions")
	v = strings.TrimSuffix(v, "/responses")
	return v
}

func buildPrompt(history []TurnItem, input string, maxTurns int) string {
	if len(history) == 0 || maxTurns <= 0 {
		return input
	}
	start := len(history) - maxTurns*2
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	b.WriteString("Use the following recent conversation as context.\n")
	for _, t := range history[start:] {
		b.WriteString(roleLabel(t.Role))
		b.WriteString(": ")
		b.WriteString(t.Text)
		b.WriteByte('\n')
	}
	b.WriteString("User: ")
	b.WriteString(input)
	b.WriteString("\nReply naturally.")
	return b.String()
}

func roleLabel(role string) string {
	if role == "assistant" {
		return "Assistant"
	}
	return "User"
}
