package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr                       string
	AuthToken                      string
	SQLitePath                     string
	WorkspaceRoots                 []string
	RunTimeout                     time.Duration
	AccessTokenTTL                 time.Duration
	RefreshTokenTTL                time.Duration
	PairCodeTTL                    time.Duration
	PairStartRateLimit             int
	PairStartRateWindow            time.Duration
	RefreshFailAlertThreshold      int
	RefreshFailAlertWindow         time.Duration
	AuthFailAlertThreshold         int
	AuthFailAlertWindow            time.Duration
	PairCompleteFailAlertThreshold int
	PairCompleteFailAlertWindow    time.Duration
	TrustedProxyCIDRs              []string
	MaxOutputBytes                 int64
	MaxConcurrentRun               int
	DailyTokenQuota                map[string]int64
	FileStoreDir                   string
	MaxUploadBytes                 int64
	CodexSessionEnabled            bool
	CodexAppServerBin              string
	CodexAppServerArgs             []string
	GeminiSessionBin               string
	GeminiSessionArgs              []string
	ClaudeSessionBin               string
	ClaudeSessionArgs              []string
	CodexSessionStartTimeout       time.Duration
	CodexSessionRequestTimeout     time.Duration
	SessionRetention               time.Duration
	SessionCleanupPeriod           time.Duration
	BackendCallReadMethods         []string
	BackendCallCancelMethods       []string
	BackendCallBlockedMethods      []string

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
	accessTokenTTLSec := envInt("AUTH_ACCESS_TOKEN_TTL_SECONDS", 900)
	refreshTokenTTLSec := envInt("AUTH_REFRESH_TOKEN_TTL_SECONDS", 86400)
	pairCodeTTLSec := envInt("AUTH_PAIR_CODE_TTL_SECONDS", 60)
	pairStartRateLimit := envInt("AUTH_PAIR_START_RATE_LIMIT", 6)
	pairStartRateWindowSec := envInt("AUTH_PAIR_START_RATE_WINDOW_SECONDS", 60)
	refreshFailAlertThreshold := envInt("AUTH_REFRESH_FAIL_ALERT_THRESHOLD", 5)
	refreshFailAlertWindowSec := envInt("AUTH_REFRESH_FAIL_ALERT_WINDOW_SECONDS", 120)
	authFailAlertThreshold := envInt("AUTH_AUTH_FAIL_ALERT_THRESHOLD", 8)
	authFailAlertWindowSec := envInt("AUTH_AUTH_FAIL_ALERT_WINDOW_SECONDS", 120)
	pairCompleteFailAlertThreshold := envInt("AUTH_PAIR_COMPLETE_FAIL_ALERT_THRESHOLD", 5)
	pairCompleteFailAlertWindowSec := envInt("AUTH_PAIR_COMPLETE_FAIL_ALERT_WINDOW_SECONDS", 120)
	codexSessionStartTimeoutSec := envInt("CODEX_SESSION_START_TIMEOUT_SECONDS", 20)
	codexSessionRequestTimeoutSec := envInt("CODEX_SESSION_REQUEST_TIMEOUT_SECONDS", 30)
	sessionRetentionSec := envInt("SESSION_RETENTION_SECONDS", 21600)
	sessionCleanupSec := envInt("SESSION_CLEANUP_INTERVAL_SECONDS", 300)
	baseDir := executableDir()
	codexBin := env("CODEX_CLI_BIN", "codex")
	return Config{
		HTTPAddr:                       env("BRIDGE_HTTP_ADDR", ":8765"),
		AuthToken:                      env("BRIDGE_AUTH_TOKEN", "echohelix-dev-token"),
		SQLitePath:                     envPath("BRIDGE_SQLITE_PATH", filepath.Join(baseDir, "bridge.db"), baseDir),
		WorkspaceRoots:                 splitCSV(env("WORKSPACE_ROOTS", "/tmp")),
		RunTimeout:                     time.Duration(timeoutSec) * time.Second,
		AccessTokenTTL:                 time.Duration(accessTokenTTLSec) * time.Second,
		RefreshTokenTTL:                time.Duration(refreshTokenTTLSec) * time.Second,
		PairCodeTTL:                    time.Duration(pairCodeTTLSec) * time.Second,
		PairStartRateLimit:             pairStartRateLimit,
		PairStartRateWindow:            time.Duration(pairStartRateWindowSec) * time.Second,
		RefreshFailAlertThreshold:      refreshFailAlertThreshold,
		RefreshFailAlertWindow:         time.Duration(refreshFailAlertWindowSec) * time.Second,
		AuthFailAlertThreshold:         authFailAlertThreshold,
		AuthFailAlertWindow:            time.Duration(authFailAlertWindowSec) * time.Second,
		PairCompleteFailAlertThreshold: pairCompleteFailAlertThreshold,
		PairCompleteFailAlertWindow:    time.Duration(pairCompleteFailAlertWindowSec) * time.Second,
		TrustedProxyCIDRs:              splitCSV(env("TRUSTED_PROXY_CIDRS", "")),
		MaxOutputBytes:                 int64(envInt("RUN_MAX_OUTPUT_BYTES", 4*1024*1024)),
		MaxConcurrentRun:               envInt("MAX_CONCURRENT_RUNS", 32),
		DailyTokenQuota:                parseKVInt64CSV(env("DAILY_TOKEN_QUOTA", "")),
		FileStoreDir:                   envPath("BRIDGE_FILE_STORE_DIR", filepath.Join(baseDir, "files"), baseDir),
		MaxUploadBytes:                 int64(envInt("BRIDGE_MAX_UPLOAD_BYTES", 20*1024*1024)),
		CodexSessionEnabled:            envBool("CODEX_SESSION_ENABLED", true),
		CodexAppServerBin:              codexBin,
		CodexAppServerArgs:             strings.Fields(env("CODEX_APP_SERVER_ARGS", "")),
		GeminiSessionBin:               env("GEMINI_CLI_BIN", "gemini"),
		GeminiSessionArgs:              strings.Fields(env("GEMINI_SESSION_ARGS", "")),
		ClaudeSessionBin:               env("CLAUDE_CLI_BIN", "claude"),
		ClaudeSessionArgs:              strings.Fields(env("CLAUDE_SESSION_ARGS", "")),
		CodexSessionStartTimeout:       time.Duration(codexSessionStartTimeoutSec) * time.Second,
		CodexSessionRequestTimeout:     time.Duration(codexSessionRequestTimeoutSec) * time.Second,
		SessionRetention:               time.Duration(sessionRetentionSec) * time.Second,
		SessionCleanupPeriod:           time.Duration(sessionCleanupSec) * time.Second,
		BackendCallReadMethods:         splitCSV(env("BACKEND_CALL_READ_METHODS", "status")),
		BackendCallCancelMethods:       splitCSV(env("BACKEND_CALL_CANCEL_METHODS", "turn/interrupt")),
		BackendCallBlockedMethods:      splitCSV(env("BACKEND_CALL_BLOCKED_METHODS", "initialize,initialized")),
		CodexAdapter: AdapterConfig{
			Enabled:    envBool("CODEX_ADAPTER_ENABLED", true),
			GRPCAddr:   env("CODEX_ADAPTER_ADDR", "127.0.0.1:50051"),
			BinaryPath: envPath("CODEX_ADAPTER_BIN", filepath.Join(baseDir, "codex-adapter"), baseDir),
		},
		GeminiAdapter: AdapterConfig{
			Enabled:    envBool("GEMINI_ADAPTER_ENABLED", true),
			GRPCAddr:   env("GEMINI_ADAPTER_ADDR", "127.0.0.1:50052"),
			BinaryPath: envPath("GEMINI_ADAPTER_BIN", filepath.Join(baseDir, "gemini-adapter"), baseDir),
		},
		ClaudeAdapter: AdapterConfig{
			Enabled:    envBool("CLAUDE_ADAPTER_ENABLED", false),
			GRPCAddr:   env("CLAUDE_ADAPTER_ADDR", "127.0.0.1:50053"),
			BinaryPath: envPath("CLAUDE_ADAPTER_BIN", filepath.Join(baseDir, "claude-adapter"), baseDir),
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

func envPath(k, def, baseDir string) string {
	v := env(k, def)
	if v == "" {
		return v
	}
	if filepath.IsAbs(v) {
		return v
	}
	if baseDir == "" {
		return v
	}
	return filepath.Join(baseDir, v)
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
		exe = real
	}
	dir := filepath.Dir(exe)
	if dir == "" {
		return "."
	}
	return dir
}

func parseKVInt64CSV(v string) map[string]int64 {
	out := map[string]int64{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		if k == "" {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		out[k] = n
	}
	return out
}
