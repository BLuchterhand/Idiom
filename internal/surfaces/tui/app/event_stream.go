package app

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"idiom/internal/runtime/agent"
)

const JSONEventSchemaVersion = "idiom.headless.event.v1"

type jsonEventStream struct {
	mu        sync.Mutex
	w         io.Writer
	sessionID string
}

func newJSONEventStream(w io.Writer, sessionID string) *jsonEventStream {
	return &jsonEventStream{w: w, sessionID: sessionID}
}

func (s *jsonEventStream) emit(record map[string]any) {
	if s == nil || s.w == nil {
		return
	}

	if record == nil {
		return
	}

	if _, ok := record["schema_version"]; !ok {
		record["schema_version"] = JSONEventSchemaVersion
	}

	if _, ok := record["ts"]; !ok {
		record["ts"] = time.Now().Format(time.RFC3339Nano)
	}

	if s.sessionID != "" {
		record["session_id"] = s.sessionID
	}

	data, err := json.Marshal(record)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.w.Write(append(data, '\n'))
}

// SetJSONEventStream wires a JSONL event sink onto the Model. Callers (typically
// headless `idiom --prompt --json`) should pass os.Stdout. Pass nil to
// disable. The sessionID is included in every emitted record so consumers can
// correlate events with the matching debug log file.
func (m *Model) SetJSONEventStream(w io.Writer, sessionID string) {
	if m == nil {
		return
	}

	if w == nil {
		m.eventStream = nil
		return
	}

	m.eventStream = newJSONEventStream(w, sessionID)
}

// DebugLogPath returns the JSONL debug-log path for the current session, or ""
// if no logger is configured.
func (m *Model) DebugLogPath() string {
	if m == nil {
		return ""
	}

	return m.debugLogPath
}

func (m *Model) emitJSONEvent(record map[string]any) {
	if m == nil || m.eventStream == nil {
		return
	}

	m.eventStream.emit(record)
}

func (m *Model) emitJSONMessage(role, content string) {
	switch role {
	case "user", "assistant", "route_plan":
	default:
		return
	}

	m.emitJSONEvent(map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	})
}

func (m *Model) emitJSONToolRequest(event agent.ToolRequestEvent) {
	record := map[string]any{
		"type":      "tool_request",
		"step":      event.Step,
		"agent":     event.Agent,
		"tool":      event.Request.Tool,
		"path":      event.Request.Path,
		"workdir":   event.Request.Workdir,
		"action":    event.Action,
		"broad":     event.Broad,
		"narrowing": event.Narrowing,
	}
	if event.DiscoveryClass != "" {
		record["discovery_class"] = event.DiscoveryClass
	}

	if event.DiscoveryScope != "" {
		record["discovery_scope"] = event.DiscoveryScope
	}

	if event.DiscoveryCost != "" {
		record["discovery_cost"] = event.DiscoveryCost
	}

	m.emitJSONEvent(record)
}

func (m *Model) emitJSONToolResult(event agent.ToolResultEvent) {
	record := map[string]any{
		"type":    "tool_result",
		"step":    event.Step,
		"agent":   event.Agent,
		"tool":    event.Result.Tool,
		"summary": event.Result.Summary,
	}
	if event.Result.Error != "" {
		record["error"] = event.Result.Error
	}

	m.emitJSONEvent(record)
}

func (m *Model) emitJSONError(kind, message string) {
	m.emitJSONEvent(map[string]any{
		"type":    "error",
		"kind":    kind,
		"message": message,
	})
}

func (m *Model) emitJSONFinal(reply string) {
	m.emitJSONEvent(map[string]any{
		"type":    "final",
		"content": reply,
	})
}

func (m *Model) emitJSONStart(prompt string) {
	record := map[string]any{
		"type":   "start",
		"prompt": prompt,
	}
	if m.workspace != nil {
		record["workspace_root"] = m.workspace.Root()
		record["workspace_mode"] = string(m.workspace.Mode())
	}

	record["agent_mode"] = m.currentMode().Name
	m.emitJSONEvent(record)
}
