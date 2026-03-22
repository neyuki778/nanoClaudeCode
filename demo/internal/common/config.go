package common

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

func LoadConfig() Config {
	_ = godotenv.Overload(".env", "../.env", "../../.env")

	return Config{
		BaseURL: normalizeBaseURL(getenv("OPENAI_BASE_URL", "http://localhost:11434/v1")),
		APIKey:  normalizeAPIKey(getenv("OPENAI_API_KEY", "")),
		Model:   getenv("OPENAI_MODEL", "gpt-4o"),
	}
}

func NewClient(cfg Config) openai.Client {
	return openai.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)
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
