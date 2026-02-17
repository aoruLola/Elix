package events

import "time"

const (
	TypeToken      = "token"
	TypeToolCall   = "tool_call"
	TypeToolResult = "tool_result"
	TypePatch      = "patch"
	TypeStatus     = "status"
	TypeDone       = "done"
	TypeError      = "error"
)

const (
	SchemaVersionV1 = "v1"
	SchemaVersionV2 = "v2"
)

type CompatFields struct {
	Text    string `json:"text,omitempty"`
	Status  string `json:"status,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

type Event struct {
	RunID         string         `json:"run_id"`
	Seq           int64          `json:"seq"`
	TS            time.Time      `json:"ts"`
	SchemaVersion string         `json:"schema_version,omitempty"`
	Type          string         `json:"type"`
	Channel       string         `json:"channel,omitempty"`
	Format        string         `json:"format,omitempty"`
	Role          string         `json:"role,omitempty"`
	Compat        *CompatFields  `json:"compat,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
	Backend       string         `json:"backend"`
	Source        string         `json:"source,omitempty"`
}
