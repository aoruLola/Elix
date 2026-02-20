package codec

import (
	"testing"

	"google.golang.org/grpc/encoding"
)

func TestJSONCodecMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()

	c := JSONCodec{}
	in := map[string]any{
		"run_id": "run-1",
		"ok":     true,
	}

	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := c.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out["run_id"] != "run-1" {
		t.Fatalf("unexpected run_id: %#v", out["run_id"])
	}
	if out["ok"] != true {
		t.Fatalf("unexpected ok: %#v", out["ok"])
	}
}

func TestJSONCodecUnmarshalInvalidJSON(t *testing.T) {
	t.Parallel()

	c := JSONCodec{}
	var out map[string]any
	if err := c.Unmarshal([]byte("{"), &out); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}

func TestJSONCodecNameAndRegister(t *testing.T) {
	t.Parallel()

	c := JSONCodec{}
	if got := c.Name(); got != Name {
		t.Fatalf("name mismatch: got %q want %q", got, Name)
	}
	Register()
	if got := encoding.GetCodec(Name); got == nil {
		t.Fatalf("expected codec %q to be registered", Name)
	}
}
