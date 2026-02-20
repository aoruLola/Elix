package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRunningMissingBinary(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Name:       "test",
		BinaryPath: filepath.Join(t.TempDir(), "missing-binary"),
		GRPCAddr:   "127.0.0.1:50051",
	})

	err := s.EnsureRunning(context.Background())
	if err == nil {
		t.Fatalf("expected missing binary error")
	}
	if !strings.Contains(err.Error(), "adapter binary missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopWithoutProcessIsNoop(t *testing.T) {
	t.Parallel()

	s := New(Config{})
	if err := s.Stop(); err != nil {
		t.Fatalf("expected no-op stop, got: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("expected repeat no-op stop, got: %v", err)
	}
}

func TestEnsureRunningReturnsErrorWhenProcessExitsImmediately(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current executable: %v", err)
	}
	s := New(Config{
		Name:       "quick-exit",
		BinaryPath: exe,
		GRPCAddr:   "127.0.0.1:50051",
	})

	err = s.EnsureRunning(context.Background())
	if err == nil {
		t.Fatalf("expected ensure running to fail when process exits immediately")
	}
	if !strings.Contains(err.Error(), "adapter process exited early") {
		t.Fatalf("unexpected error: %v", err)
	}
}
