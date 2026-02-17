package driver

import (
	"context"

	"echohelix/internal/events"
)

type StartRequest struct {
	RunID         string
	WorkspaceID   string
	WorkspacePath string
	Prompt        string
	Context       map[string]any
	Options       RunOptions
}

type RunOptions struct {
	Model         string
	Profile       string
	Sandbox       string
	SchemaVersion string
}

type Stream struct {
	Events <-chan events.Event
	Done   <-chan error
}

type Health struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type CapabilitySet struct {
	Backend                string   `json:"backend"`
	EventTypes             []string `json:"event_types"`
	SupportsCancel         bool     `json:"supports_cancel"`
	SupportsPTY            bool     `json:"supports_pty"`
	SchemaVersions         []string `json:"schema_versions,omitempty"`
	PreferredSchemaVersion string   `json:"preferred_schema_version,omitempty"`
	CompatFields           []string `json:"compat_fields,omitempty"`
}

type Driver interface {
	Name() string
	StartRun(ctx context.Context, req StartRequest) (*Stream, error)
	Cancel(ctx context.Context, runID string) error
	Health(ctx context.Context) (Health, error)
	Capabilities(ctx context.Context) (CapabilitySet, error)
}
