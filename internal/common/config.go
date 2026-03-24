package common

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Config struct {
	BaseURL       string
	APIKey        string
	Model         string
	SubAgentModel string
	DebugHTTP     bool
}

func LoadConfig() Config {
	_ = godotenv.Overload(".env", "../.env", "../../.env")
	model := getenv("OPENAI_MODEL", "gpt-4o")

	return Config{
		BaseURL:       normalizeBaseURL(getenv("OPENAI_BASE_URL", "http://localhost:11434/v1")),
		APIKey:        normalizeAPIKey(getenv("OPENAI_API_KEY", "")),
		Model:         model,
		SubAgentModel: getenv("SUBAGENT_MODEL", model),
		DebugHTTP:     getenvBool("DEBUG_HTTP", false),
	}
}

func NewClient(cfg Config) openai.Client {
	opts := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.DebugHTTP {
		opts = append(opts, option.WithDebugLog(log.New(os.Stderr, "[openai] ", log.LstdFlags|log.Lmicroseconds)))
	}
	return openai.NewClient(opts...)
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
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
