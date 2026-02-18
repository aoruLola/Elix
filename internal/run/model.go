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
	ID          string          `json:"run_id"`
	WorkspaceID string          `json:"workspace_id"`
	Workspace   string          `json:"workspace_path,omitempty"`
	Backend     string          `json:"backend"`
	Prompt      string          `json:"prompt"`
	Context     map[string]any  `json:"context,omitempty"`
	Options     RunOptions      `json:"options,omitempty"`
	Attachments []RunAttachment `json:"attachments,omitempty"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Terminal    TerminalInfo    `json:"terminal"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type TerminalInfo struct {
	IsTerminal bool   `json:"is_terminal"`
	Outcome    string `json:"outcome,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
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

type RunAttachment struct {
	FileID    string `json:"file_id"`
	Alias     string `json:"alias"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type TokenUsageTotals struct {
	RunCount     int64 `json:"run_count"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type TokenUsageByBackend struct {
	Backend string `json:"backend"`
	TokenUsageTotals
}

type TokenUsageSummary struct {
	From      time.Time             `json:"from"`
	To        time.Time             `json:"to"`
	Totals    TokenUsageTotals      `json:"totals"`
	ByBackend []TokenUsageByBackend `json:"by_backend"`
}

type TokenQuotaItem struct {
	Backend         string    `json:"backend"`
	WindowFrom      time.Time `json:"window_from"`
	WindowTo        time.Time `json:"window_to"`
	Configured      bool      `json:"configured"`
	QuotaTokens     int64     `json:"quota_tokens"`
	UsedTokens      int64     `json:"used_tokens"`
	RemainingTokens int64     `json:"remaining_tokens"`
	Exceeded        bool      `json:"exceeded"`
}

type EmergencyState struct {
	Active    bool      `json:"active"`
	Reason    string    `json:"reason,omitempty"`
	Activated time.Time `json:"activated_at,omitempty"`
}
