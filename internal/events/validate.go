package events

import "fmt"

const (
	ChannelFinal   = "final"
	ChannelWorking = "working"
	ChannelSystem  = "system"
)

const (
	FormatMarkdown = "markdown"
	FormatPlain    = "plain"
	FormatJSON     = "json"
	FormatDiff     = "diff"
)

const (
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

var allowedTypes = map[string]struct{}{
	TypeToken:      {},
	TypeToolCall:   {},
	TypeToolResult: {},
	TypePatch:      {},
	TypeStatus:     {},
	TypeDone:       {},
	TypeError:      {},
}

var allowedChannels = map[string]struct{}{
	ChannelFinal:   {},
	ChannelWorking: {},
	ChannelSystem:  {},
}

var allowedFormats = map[string]struct{}{
	FormatMarkdown: {},
	FormatPlain:    {},
	FormatJSON:     {},
	FormatDiff:     {},
}

var allowedRoles = map[string]struct{}{
	RoleAssistant: {},
	RoleSystem:    {},
}

var allowedSchemaVersions = map[string]struct{}{
	SchemaVersionV1: {},
	SchemaVersionV2: {},
}

func AllowedTypes() []string {
	return []string{
		TypeToken,
		TypeToolCall,
		TypeToolResult,
		TypePatch,
		TypeStatus,
		TypeDone,
		TypeError,
	}
}

func AllowedChannels() []string {
	return []string{
		ChannelFinal,
		ChannelWorking,
		ChannelSystem,
	}
}

func AllowedFormats() []string {
	return []string{
		FormatMarkdown,
		FormatPlain,
		FormatJSON,
		FormatDiff,
	}
}

func AllowedRoles() []string {
	return []string{
		RoleAssistant,
		RoleSystem,
	}
}

func AllowedSchemaVersions() []string {
	return []string{
		SchemaVersionV1,
		SchemaVersionV2,
	}
}

func NormalizeEvent(e *Event) {
	if e.SchemaVersion == "" {
		e.SchemaVersion = SchemaVersionV2
	}
	if e.Type == "" {
		e.Type = TypeToken
	}
	if e.Channel == "" {
		switch e.Type {
		case TypeToken:
			e.Channel = ChannelWorking
		case TypeToolCall, TypeToolResult, TypePatch:
			e.Channel = ChannelWorking
		default:
			e.Channel = ChannelSystem
		}
	}
	if e.Format == "" {
		switch e.Type {
		case TypeToken:
			e.Format = FormatPlain
		case TypePatch:
			e.Format = FormatDiff
		default:
			e.Format = FormatJSON
		}
	}
	if e.Role == "" {
		if e.Type == TypeToken || e.Type == TypeToolCall || e.Type == TypeToolResult || e.Type == TypePatch {
			e.Role = RoleAssistant
		} else {
			e.Role = RoleSystem
		}
	}
	applyCompat(e)
}

func ValidateEvent(e Event) error {
	if e.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if e.Seq <= 0 {
		return fmt.Errorf("seq must be > 0")
	}
	if e.SchemaVersion == "" {
		return fmt.Errorf("schema_version is required")
	}
	if _, ok := allowedSchemaVersions[e.SchemaVersion]; !ok {
		return fmt.Errorf("invalid schema_version: %s", e.SchemaVersion)
	}
	if e.Type == "" {
		return fmt.Errorf("type is required")
	}
	if _, ok := allowedTypes[e.Type]; !ok {
		return fmt.Errorf("invalid type: %s", e.Type)
	}
	if e.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	if _, ok := allowedChannels[e.Channel]; !ok {
		return fmt.Errorf("invalid channel: %s", e.Channel)
	}
	if e.Format == "" {
		return fmt.Errorf("format is required")
	}
	if _, ok := allowedFormats[e.Format]; !ok {
		return fmt.Errorf("invalid format: %s", e.Format)
	}
	if e.Role == "" {
		return fmt.Errorf("role is required")
	}
	if _, ok := allowedRoles[e.Role]; !ok {
		return fmt.Errorf("invalid role: %s", e.Role)
	}
	return nil
}

func applyCompat(e *Event) {
	if e.Compat == nil {
		e.Compat = &CompatFields{}
	}
	switch e.Type {
	case TypeToken:
		if e.Compat.Text == "" {
			if v, ok := payloadString(e.Payload, "text"); ok {
				e.Compat.Text = v
			} else if v, ok := payloadString(e.Payload, "stderr"); ok {
				e.Compat.Text = v
			} else if v, ok := payloadString(e.Payload, "message"); ok {
				e.Compat.Text = v
			}
		}
	case TypeStatus, TypeDone:
		if e.Compat.Status == "" {
			if v, ok := payloadString(e.Payload, "status"); ok {
				e.Compat.Status = v
			}
		}
	case TypeError:
		e.Compat.IsError = true
		if e.Compat.Text == "" {
			if v, ok := payloadString(e.Payload, "message"); ok {
				e.Compat.Text = v
			}
		}
	}
	if e.Type == TypeDone && e.Compat.Status == "" {
		e.Compat.Status = "completed"
	}
}

func payloadString(payload map[string]any, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	v, ok := payload[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}
