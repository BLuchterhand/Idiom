package app

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"idiom/internal/agent"
	"idiom/internal/transcript"
	"idiom/internal/workspace"
)

func decodeJSONEvents(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode JSONL %q: %v", line, err)
		}
		out = append(out, record)
	}
	return out
}

func TestEmitJSONMessageOnlyKnownRoles(t *testing.T) {
	var buf bytes.Buffer
	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")

	model.emitJSONMessage("user", "hello")
	model.emitJSONMessage("assistant", "hi")
	model.emitJSONMessage("internal_kind", "should be filtered")

	events := decodeJSONEvents(t, buf.String())
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d (%v)", len(events), events)
	}
	for _, e := range events {
		if e["session_id"] != "sess-1" {
			t.Errorf("session_id missing on event: %v", e)
		}
		if e["type"] != "message" {
			t.Errorf("expected type=message, got %v", e["type"])
		}
	}
	if events[0]["role"] != "user" || events[1]["role"] != "assistant" {
		t.Errorf("roles wrong: %v", events)
	}
}

func TestEmitJSONToolRequestAndResult(t *testing.T) {
	var buf bytes.Buffer
	model := &Model{}
	model.SetJSONEventStream(&buf, "")

	model.emitJSONToolRequest(agent.ToolRequestEvent{
		Step: 3, Agent: "main",
		Request: workspace.ToolRequest{Tool: "read_file", Path: "foo.go"},
		Action:  "narrow",
	})
	model.emitJSONToolResult(agent.ToolResultEvent{
		Step: 3, Agent: "main",
		Result: workspace.ToolResult{Tool: "read_file", Summary: "Read foo.go", Error: ""},
	})
	model.emitJSONToolResult(agent.ToolResultEvent{
		Step: 4, Agent: "main",
		Result: workspace.ToolResult{Tool: "shell", Summary: "Ran cmd", Error: "boom"},
	})

	events := decodeJSONEvents(t, buf.String())
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0]["type"] != "tool_request" || events[0]["tool"] != "read_file" || events[0]["path"] != "foo.go" {
		t.Errorf("bad tool_request event: %v", events[0])
	}
	if events[1]["type"] != "tool_result" || events[1]["summary"] != "Read foo.go" {
		t.Errorf("bad tool_result event: %v", events[1])
	}
	if _, hasErr := events[1]["error"]; hasErr {
		t.Errorf("successful result should omit error key: %v", events[1])
	}
	if events[2]["error"] != "boom" {
		t.Errorf("expected error=boom: %v", events[2])
	}
}

func TestEmitJSONFinalAndError(t *testing.T) {
	var buf bytes.Buffer
	model := &Model{}
	model.SetJSONEventStream(&buf, "")

	model.emitJSONStart("hello prompt")
	model.emitJSONError("server_not_ready", "down")
	model.emitJSONFinal("done text")

	events := decodeJSONEvents(t, buf.String())
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0]["type"] != "start" || events[0]["prompt"] != "hello prompt" {
		t.Errorf("bad start event: %v", events[0])
	}
	if events[1]["type"] != "error" || events[1]["kind"] != "server_not_ready" || events[1]["message"] != "down" {
		t.Errorf("bad error event: %v", events[1])
	}
	if events[2]["type"] != "final" || events[2]["content"] != "done text" {
		t.Errorf("bad final event: %v", events[2])
	}
}

func TestLogRunEventEmitsCompletionStatusJSON(t *testing.T) {
	var buf bytes.Buffer
	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")

	model.logRunEvent(agent.RunEvent{
		Role:    "completion_status",
		Content: `{"lane":"main","contract":"implementation_result","status":"blocked","blocker_kind":"required_edit_paths_remaining","edit_attempted":false}`,
	})

	events := decodeJSONEvents(t, buf.String())
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d (%v)", len(events), events)
	}
	event := events[0]
	if event["type"] != "completion_status" || event["status"] != "blocked" || event["contract"] != "implementation_result" {
		t.Fatalf("bad completion_status event: %v", event)
	}
	if event["blocker_kind"] != "required_edit_paths_remaining" || event["edit_attempted"] != false {
		t.Fatalf("missing blocker metadata: %v", event)
	}
	if event["session_id"] != "sess-1" {
		t.Fatalf("session_id missing on completion_status event: %v", event)
	}
}

func TestLogRunEventStoresInternalRecoveryAsRunEvent(t *testing.T) {
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: transcript.DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	model := &Model{debugLogger: logger}
	model.logRunEvent(agent.RunEvent{
		Role:    "completion_gate_recovery",
		Content: "internal recovery prompt",
	})

	body, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	if strings.Contains(string(body), `"type":"message"`) {
		t.Fatalf("internal recovery event must not be written as a message record:\n%s", body)
	}
	if !strings.Contains(string(body), `"type":"run_event"`) || !strings.Contains(string(body), `"role":"completion_gate_recovery"`) {
		t.Fatalf("expected run_event record, got:\n%s", body)
	}
}

func TestSetJSONEventStreamNilDisables(t *testing.T) {
	var buf bytes.Buffer
	model := &Model{}
	model.SetJSONEventStream(&buf, "")
	model.emitJSONFinal("a")
	model.SetJSONEventStream(nil, "")
	model.emitJSONFinal("b")
	events := decodeJSONEvents(t, buf.String())
	if len(events) != 1 {
		t.Fatalf("expected 1 event after nil disable, got %d", len(events))
	}
	if events[0]["content"] != "a" {
		t.Errorf("expected only first event, got %v", events[0])
	}
}
