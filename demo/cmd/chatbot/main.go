package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nanocc/internal/common"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type TurnItem struct {
	Role string
	Text string
}

func main() {
	cfg := common.LoadConfig()
	if cfg.APIKey == "" {
		panic("OPENAI_API_KEY is empty")
	}

	client := common.NewClient(cfg)

	fmt.Printf("Chatbot started. base_url=%s model=%s debug_http=%t\n", cfg.BaseURL, cfg.Model, cfg.DebugHTTP)
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
			Model: openai.ResponsesModel(cfg.Model),
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
