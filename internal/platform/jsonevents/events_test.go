package jsonevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestEmitKeepsReservedFieldsAuthoritative(t *testing.T) {
	var stdout bytes.Buffer

	if err := Emit(&stdout, true, "owned.event", map[string]any{
		"type": "caller.event",
		"ts":   "not-a-timestamp",
		"x":    float64(1),
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got := payload["type"]; got != "owned.event" {
		t.Fatalf("type = %v, want owned.event", got)
	}
	if got := payload["ts"]; got == "not-a-timestamp" || got == "" {
		t.Fatalf("ts = %v, want emitter timestamp", got)
	}
	if got := payload["x"]; got != float64(1) {
		t.Fatalf("x = %v, want 1", got)
	}
}

func TestEmitReturnsMarshalError(t *testing.T) {
	var stdout bytes.Buffer
	err := Emit(&stdout, true, "owned.event", map[string]any{
		"bad": func() {},
	})
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on marshal failure", stdout.String())
	}
}

func TestEmitReturnsWriterError(t *testing.T) {
	want := errors.New("writer closed")
	err := Emit(failingWriter{err: want}, true, "owned.event", nil)
	if !errors.Is(err, want) {
		t.Fatalf("Emit error = %v, want %v", err, want)
	}
}

func TestEmitReturnsNilWriterErrorWhenEnabled(t *testing.T) {
	if err := Emit(nil, true, "owned.event", nil); err == nil {
		t.Fatal("expected nil writer error")
	}
}

func TestEmitReturnsEmptyEventNameErrorWhenEnabled(t *testing.T) {
	var stdout bytes.Buffer
	if err := Emit(&stdout, true, " ", nil); err == nil {
		t.Fatal("expected empty event name error")
	}
}

func TestEmitAllowsInvalidInputsWhenDisabled(t *testing.T) {
	if err := Emit(nil, false, "", map[string]any{"bad": func() {}}); err != nil {
		t.Fatalf("Emit disabled error = %v, want nil", err)
	}
}
