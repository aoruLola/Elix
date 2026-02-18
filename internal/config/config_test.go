package config

import (
	"reflect"
	"path/filepath"
	"testing"
)

func TestEnvPathResolvesRelativeToBaseDir(t *testing.T) {
	t.Setenv("ECHOHELIX_TEST_PATH_1", "")
	base := filepath.FromSlash("/opt/echohelix/bin")
	got := envPath("ECHOHELIX_TEST_PATH_1", "./codex-adapter", base)
	want := filepath.Join(base, "./codex-adapter")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEnvPathKeepsAbsolutePath(t *testing.T) {
	t.Setenv("ECHOHELIX_TEST_PATH_2", "")
	base := filepath.FromSlash("/opt/echohelix/bin")
	abs := filepath.Join(t.TempDir(), "codex-adapter")
	got := envPath("ECHOHELIX_TEST_PATH_2", abs, base)
	if got != abs {
		t.Fatalf("expected absolute path preserved, got %q", got)
	}
}

func TestExecutableDirNotEmpty(t *testing.T) {
	if d := executableDir(); d == "" {
		t.Fatalf("executableDir should not be empty")
	}
}

func TestParseKVInt64CSV(t *testing.T) {
	got := parseKVInt64CSV("codex:1000, gemini:2000, invalid, claude:-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 valid entries, got %d", len(got))
	}
	if got["codex"] != 1000 {
		t.Fatalf("expected codex quota=1000, got %d", got["codex"])
	}
	if got["gemini"] != 2000 {
		t.Fatalf("expected gemini quota=2000, got %d", got["gemini"])
	}
}

func TestLoadSessionBackendDefaults(t *testing.T) {
	t.Setenv("GEMINI_CLI_BIN", "")
	t.Setenv("GEMINI_SESSION_ARGS", "")
	t.Setenv("CLAUDE_CLI_BIN", "")
	t.Setenv("CLAUDE_SESSION_ARGS", "")

	cfg := Load()
	if cfg.GeminiSessionBin != "gemini" {
		t.Fatalf("expected default GeminiSessionBin=gemini, got %q", cfg.GeminiSessionBin)
	}
	if len(cfg.GeminiSessionArgs) != 0 {
		t.Fatalf("expected default GeminiSessionArgs empty, got %#v", cfg.GeminiSessionArgs)
	}
	if cfg.ClaudeSessionBin != "claude" {
		t.Fatalf("expected default ClaudeSessionBin=claude, got %q", cfg.ClaudeSessionBin)
	}
	if len(cfg.ClaudeSessionArgs) != 0 {
		t.Fatalf("expected default ClaudeSessionArgs empty, got %#v", cfg.ClaudeSessionArgs)
	}
}

func TestLoadSessionBackendEnvOverrides(t *testing.T) {
	t.Setenv("GEMINI_CLI_BIN", "/opt/tools/gemini")
	t.Setenv("GEMINI_SESSION_ARGS", "--output-format stream-json --model gemini-2.5-pro")
	t.Setenv("CLAUDE_CLI_BIN", "/opt/tools/claude")
	t.Setenv("CLAUDE_SESSION_ARGS", "--print --verbose")

	cfg := Load()
	if cfg.GeminiSessionBin != "/opt/tools/gemini" {
		t.Fatalf("expected GeminiSessionBin override, got %q", cfg.GeminiSessionBin)
	}
	if !reflect.DeepEqual(cfg.GeminiSessionArgs, []string{"--output-format", "stream-json", "--model", "gemini-2.5-pro"}) {
		t.Fatalf("unexpected GeminiSessionArgs: %#v", cfg.GeminiSessionArgs)
	}
	if cfg.ClaudeSessionBin != "/opt/tools/claude" {
		t.Fatalf("expected ClaudeSessionBin override, got %q", cfg.ClaudeSessionBin)
	}
	if !reflect.DeepEqual(cfg.ClaudeSessionArgs, []string{"--print", "--verbose"}) {
		t.Fatalf("unexpected ClaudeSessionArgs: %#v", cfg.ClaudeSessionArgs)
	}
}
