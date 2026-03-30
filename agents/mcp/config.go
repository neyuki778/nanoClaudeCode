package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	DefaultConfigPath        = ".agents/mcp/config.json"
	DefaultConfigVersion     = 1
	DefaultTransport         = "stdio"
	DefaultStartupTimeoutSec = 10
	DefaultToolTimeoutSec    = 30
)

var serverNamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

type Config struct {
	Version int                     `json:"version"`
	Servers map[string]ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Enabled           bool              `json:"enabled"`
	Transport         string            `json:"transport"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	Cwd               string            `json:"cwd"`
	Env               map[string]string `json:"env"`
	StartupTimeoutSec int               `json:"startup_timeout_sec"`
	ToolTimeoutSec    int               `json:"tool_timeout_sec"`
}

func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read mcp config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse mcp config %s: %w", path, err)
	}

	if err := normalizeAndValidate(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid mcp config %s: %w", path, err)
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Version: DefaultConfigVersion,
		Servers: map[string]ServerConfig{},
	}
}

func normalizeAndValidate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}

	if cfg.Version == 0 {
		cfg.Version = DefaultConfigVersion
	}
	if cfg.Version != DefaultConfigVersion {
		return fmt.Errorf("unsupported version: %d", cfg.Version)
	}

	if cfg.Servers == nil {
		cfg.Servers = map[string]ServerConfig{}
		return nil
	}

	normalized := make(map[string]ServerConfig, len(cfg.Servers))
	for rawName, server := range cfg.Servers {
		name := normalizeServerName(rawName)
		if name == "" {
			return fmt.Errorf("empty server name")
		}
		if !serverNamePattern.MatchString(name) {
			return fmt.Errorf("invalid server name %q", rawName)
		}
		if _, exists := normalized[name]; exists {
			return fmt.Errorf("duplicate server name %q", name)
		}

		server.Transport = normalizeTransport(server.Transport)
		if server.Transport != DefaultTransport {
			return fmt.Errorf("server %q: unsupported transport %q", name, server.Transport)
		}

		server.Command = strings.TrimSpace(server.Command)
		server.Cwd = normalizeCwd(server.Cwd)
		server.Env = normalizeEnv(server.Env)
		server.StartupTimeoutSec = normalizePositiveInt(server.StartupTimeoutSec, DefaultStartupTimeoutSec)
		server.ToolTimeoutSec = normalizePositiveInt(server.ToolTimeoutSec, DefaultToolTimeoutSec)
		server.Args = normalizeArgs(server.Args)

		if server.Enabled && server.Command == "" {
			return fmt.Errorf("server %q: empty command", name)
		}
		normalized[name] = server
	}

	cfg.Servers = normalized
	return nil
}

func normalizeServerName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func normalizeTransport(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return DefaultTransport
	}
	return v
}

func normalizeCwd(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "."
	}
	return filepath.Clean(v)
}

func normalizeEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = os.ExpandEnv(strings.TrimSpace(value))
	}
	return out
}

func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func normalizePositiveInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func (c Config) EnabledServers() []string {
	names := make([]string, 0, len(c.Servers))
	for name, server := range c.Servers {
		if server.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
