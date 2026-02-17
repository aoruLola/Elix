package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestNormalizeAndValidateEvent(t *testing.T) {
	ev := Event{
		RunID:   "r1",
		Seq:     1,
		TS:      time.Now().UTC(),
		Type:    TypeToken,
		Payload: map[string]any{"text": "hello"},
		Backend: "codex",
	}
	NormalizeEvent(&ev)
	if err := ValidateEvent(ev); err != nil {
		t.Fatalf("expected valid event, got err=%v", err)
	}
	if ev.SchemaVersion != SchemaVersionV2 {
		t.Fatalf("expected schema_version v2, got %s", ev.SchemaVersion)
	}
	if ev.Compat == nil || ev.Compat.Text != "hello" {
		t.Fatalf("expected compat text, got %#v", ev.Compat)
	}
}

func TestValidateEventRejectsInvalidEnum(t *testing.T) {
	ev := Event{
		RunID:   "r1",
		Seq:     1,
		TS:      time.Now().UTC(),
		Type:    "unknown",
		Channel: ChannelFinal,
		Format:  FormatMarkdown,
		Role:    RoleAssistant,
		Backend: "codex",
	}
	if err := ValidateEvent(ev); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestSchemaEnumsMatchCode(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	schemaPath := filepath.Join(root, "schema", "event.v2.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing")
	}

	assertEnumMatch(t, props, "type", AllowedTypes())
	assertEnumMatch(t, props, "schema_version", AllowedSchemaVersions())
	assertEnumMatch(t, props, "channel", AllowedChannels())
	assertEnumMatch(t, props, "format", AllowedFormats())
	assertEnumMatch(t, props, "role", AllowedRoles())
}

func assertEnumMatch(t *testing.T, props map[string]any, key string, want []string) {
	t.Helper()
	prop, ok := props[key].(map[string]any)
	if !ok {
		t.Fatalf("missing property %s", key)
	}
	rawEnum, ok := prop["enum"].([]any)
	if !ok {
		t.Fatalf("missing enum for %s", key)
	}
	got := make([]string, 0, len(rawEnum))
	for _, v := range rawEnum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("invalid enum type for %s", key)
		}
		got = append(got, s)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enum mismatch for %s: got=%v want=%v", key, got, want)
	}
}
