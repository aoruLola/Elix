package session

import "testing"

func TestToCodexSandbox(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "read only kebab", input: "read-only", want: "read-only"},
		{name: "read only camel", input: "readOnly", want: "read-only"},
		{name: "workspace write kebab", input: "workspace-write", want: "workspace-write"},
		{name: "workspace write camel", input: "workspaceWrite", want: "workspace-write"},
		{name: "danger full access kebab", input: "danger-full-access", want: "danger-full-access"},
		{name: "danger full access camel", input: "dangerFullAccess", want: "danger-full-access"},
		{name: "unknown passthrough", input: "custom", want: "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toCodexSandbox(tt.input)
			if got != tt.want {
				t.Fatalf("toCodexSandbox(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
