package util

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONAndReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.json")
	input := map[string]any{"key": "value", "num": 42}

	if err := WriteJSON(path, input); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty file")
	}
	if data[len(data)-1] != '\n' {
		t.Error("no trailing newline")
	}

	var got map[string]any
	if err := ReadJSON(path, &got); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if got["key"] != "value" || int(got["num"].(float64)) != 42 {
		t.Errorf("unexpected content: %v", got)
	}
}

func TestReadJSON_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := ReadJSON(path, &v); err == nil {
		t.Error("expected error for empty file")
	}
}

func TestWriteJSON_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	ch := make(chan int)
	if err := WriteJSON(path, ch); err == nil {
		t.Error("expected error for non-marshalable value")
	}
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var v json.RawMessage
	if err := ReadJSON(path, &v); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
