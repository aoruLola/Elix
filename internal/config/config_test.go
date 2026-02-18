package config

import (
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
	abs := filepath.FromSlash("/usr/local/bin/codex-adapter")
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
