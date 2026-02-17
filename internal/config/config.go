package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr         string
	AuthToken        string
	SQLitePath       string
	WorkspaceRoots   []string
	RunTimeout       time.Duration
	MaxOutputBytes   int64
	MaxConcurrentRun int

	CodexAdapter  AdapterConfig
	GeminiAdapter AdapterConfig
	ClaudeAdapter AdapterConfig
}

type AdapterConfig struct {
	Enabled    bool
	GRPCAddr   string
	BinaryPath string
}

func Load() Config {
	timeoutSec := envInt("RUN_TIMEOUT_SECONDS", 1800)
	return Config{
		HTTPAddr:         env("BRIDGE_HTTP_ADDR", ":8765"),
		AuthToken:        env("BRIDGE_AUTH_TOKEN", "echohelix-dev-token"),
		SQLitePath:       env("BRIDGE_SQLITE_PATH", "./bridge.db"),
		WorkspaceRoots:   splitCSV(env("WORKSPACE_ROOTS", "/tmp")),
		RunTimeout:       time.Duration(timeoutSec) * time.Second,
		MaxOutputBytes:   int64(envInt("RUN_MAX_OUTPUT_BYTES", 4*1024*1024)),
		MaxConcurrentRun: envInt("MAX_CONCURRENT_RUNS", 32),
		CodexAdapter: AdapterConfig{
			Enabled:    envBool("CODEX_ADAPTER_ENABLED", true),
			GRPCAddr:   env("CODEX_ADAPTER_ADDR", "127.0.0.1:50051"),
			BinaryPath: env("CODEX_ADAPTER_BIN", "./codex-adapter"),
		},
		GeminiAdapter: AdapterConfig{
			Enabled:    envBool("GEMINI_ADAPTER_ENABLED", true),
			GRPCAddr:   env("GEMINI_ADAPTER_ADDR", "127.0.0.1:50052"),
			BinaryPath: env("GEMINI_ADAPTER_BIN", "./gemini-adapter"),
		},
		ClaudeAdapter: AdapterConfig{
			Enabled:    envBool("CLAUDE_ADAPTER_ENABLED", false),
			GRPCAddr:   env("CLAUDE_ADAPTER_ADDR", "127.0.0.1:50053"),
			BinaryPath: env("CLAUDE_ADAPTER_BIN", "./claude-adapter"),
		},
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(k string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
