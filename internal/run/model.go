package run

import "time"

const (
	StatusQueued     = "queued"
	StatusRunning    = "running"
	StatusStreaming  = "streaming"
	StatusCancelling = "cancelling"
	StatusCancelled  = "cancelled"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

type Run struct {
	ID          string         `json:"run_id"`
	WorkspaceID string         `json:"workspace_id"`
	Workspace   string         `json:"workspace_path,omitempty"`
	Backend     string         `json:"backend"`
	Prompt      string         `json:"prompt"`
	Context     map[string]any `json:"context,omitempty"`
	Options     RunOptions     `json:"options,omitempty"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type SubmitRequest struct {
	WorkspaceID   string         `json:"workspace_id"`
	WorkspacePath string         `json:"workspace_path"`
	Backend       string         `json:"backend"`
	Prompt        string         `json:"prompt"`
	Context       map[string]any `json:"context,omitempty"`
	Options       RunOptions     `json:"options,omitempty"`
}

type RunOptions struct {
	Model         string `json:"model,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Sandbox       string `json:"sandbox,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
}
