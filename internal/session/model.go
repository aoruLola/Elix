package session

import "time"

const (
	BackendCodex = "codex"

	StatusStarting = "starting"
	StatusReady    = "ready"
	StatusClosed   = "closed"
	StatusFailed   = "failed"
)

type Session struct {
	ID            string    `json:"session_id"`
	Backend       string    `json:"backend"`
	WorkspaceID   string    `json:"workspace_id,omitempty"`
	WorkspacePath string    `json:"workspace_path"`
	ThreadID      string    `json:"thread_id,omitempty"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Event struct {
	SessionID string         `json:"session_id"`
	Seq       int64          `json:"seq"`
	TS        time.Time      `json:"ts"`
	Type      string         `json:"type"`
	Method    string         `json:"method,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type PendingRequest struct {
	RequestID  string         `json:"request_id"`
	Method     string         `json:"method"`
	Kind       string         `json:"kind"`
	Params     map[string]any `json:"params,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	ResolvedAt time.Time      `json:"resolved_at,omitempty"`
	Resolved   bool           `json:"resolved"`
}

type Approval struct {
	RequestID string         `json:"request_id"`
	Method    string         `json:"method"`
	ThreadID  string         `json:"thread_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	ItemID    string         `json:"item_id,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Command   string         `json:"command,omitempty"`
	Cwd       string         `json:"cwd,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Resolved  bool           `json:"resolved"`
}

type CreateRequest struct {
	WorkspaceID   string         `json:"workspace_id,omitempty"`
	WorkspacePath string         `json:"workspace_path"`
	Backend       string         `json:"backend,omitempty"`
	ThreadID      string         `json:"thread_id,omitempty"`
	Model         string         `json:"model,omitempty"`
	Approval      string         `json:"approval_policy,omitempty"`
	Sandbox       string         `json:"sandbox,omitempty"`
	Config        map[string]any `json:"config,omitempty"`
}

type StartTurnRequest struct {
	Prompt         string           `json:"prompt,omitempty"`
	Input          []map[string]any `json:"input,omitempty"`
	Model          string           `json:"model,omitempty"`
	Approval       string           `json:"approval_policy,omitempty"`
	Sandbox        string           `json:"sandbox,omitempty"`
	OutputSchema   map[string]any   `json:"output_schema,omitempty"`
	ExpectedTurnID string           `json:"expected_turn_id,omitempty"`
	Steer          bool             `json:"steer,omitempty"`
}

type StartTurnResult struct {
	SessionID string `json:"session_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type BackendStatus struct {
	SessionID string    `json:"session_id"`
	Backend   string    `json:"backend"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Result    any       `json:"result"`
	FetchedAt time.Time `json:"fetched_at"`
}

type BackendCallRequest struct {
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type BackendCallResult struct {
	SessionID string    `json:"session_id"`
	Backend   string    `json:"backend"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Method    string    `json:"method"`
	Result    any       `json:"result"`
	CalledAt  time.Time `json:"called_at"`
}

type ResolveRequestInput struct {
	Result map[string]any `json:"result,omitempty"`
	Error  *ResolveError  `json:"error,omitempty"`
}

type ResolveError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type ApprovalDecision struct {
	Decision   string `json:"decision"`
	ForSession bool   `json:"for_session,omitempty"`
}
