package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONEventStreamAddsSchemaVersion(t *testing.T) {
	var out bytes.Buffer
	stream := newJSONEventStream(&out, "session-1")
	stream.emit(map[string]any{"type": "final", "content": "done"})

	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event["schema_version"] != JSONEventSchemaVersion {
		t.Fatalf("schema_version = %v, want %s", event["schema_version"], JSONEventSchemaVersion)
	}
	if event["session_id"] != "session-1" {
		t.Fatalf("session_id = %v", event["session_id"])
	}
}
