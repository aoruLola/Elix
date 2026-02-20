package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWorkspaceBasicCases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	p := New([]string{root})

	if err := p.ValidateWorkspace(""); err == nil {
		t.Fatalf("expected error for empty workspace path")
	}

	inside := filepath.Join(root, "project")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("mkdir inside: %v", err)
	}
	if err := p.ValidateWorkspace(inside); err != nil {
		t.Fatalf("expected inside path to be accepted, got: %v", err)
	}

	outside := t.TempDir()
	if err := p.ValidateWorkspace(outside); err == nil {
		t.Fatalf("expected outside path to be rejected")
	}
}

func TestValidateWorkspaceRejectsEscapingSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link-out")

	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported in current environment: %v", err)
	}

	p := New([]string{root})
	if err := p.ValidateWorkspace(link); err == nil {
		t.Fatalf("expected symlink escaping root to be rejected")
	}
}

func TestValidateRunOptions(t *testing.T) {
	t.Parallel()

	p := New(nil)
	valid := RunOptions{
		Model:         "gpt-5",
		Profile:       "default.profile",
		Sandbox:       "workspace-write",
		SchemaVersion: "v2",
	}
	if err := p.ValidateRunOptions(valid); err != nil {
		t.Fatalf("expected valid options to pass, got: %v", err)
	}

	cases := []struct {
		name string
		opts RunOptions
		msg  string
	}{
		{
			name: "invalid model",
			opts: RunOptions{Model: "bad model"},
			msg:  "model",
		},
		{
			name: "invalid profile",
			opts: RunOptions{Profile: "bad profile"},
			msg:  "profile",
		},
		{
			name: "invalid sandbox",
			opts: RunOptions{Sandbox: "root"},
			msg:  "sandbox",
		},
		{
			name: "invalid schema version",
			opts: RunOptions{SchemaVersion: "v3"},
			msg:  "schema_version",
		},
		{
			name: "model too long",
			opts: RunOptions{Model: strings.Repeat("a", 129)},
			msg:  "model",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := p.ValidateRunOptions(tc.opts)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("expected error to contain %q, got %q", tc.msg, err.Error())
			}
		})
	}
}
