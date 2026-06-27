package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/jsonl"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
)

type sessionReplay struct {
	messages []llama.ChatMessage
	history  []historyEntry
}

// LoadHistoryFromSession replays the user/assistant message records from a
// prior session log into the model's conversation history. Tool results are not
// replayed; the model just sees the prior conversation as plain chat history.
// Durable workflow pointers are restored only when the log names a resumable
// per-workspace state artifact, such as an open direct feature-planner session.
func (m *Model) LoadHistoryFromSession(path string) error {
	if m == nil {
		return fmt.Errorf("model is nil")
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("session path is required")
	}

	replay, err := replaySessionLog(path)
	if err != nil {
		return err
	}

	if len(replay.messages) == 0 {
		return fmt.Errorf("session log %q contained no user/assistant messages", path)
	}

	inputHistory, err := inputHistoryFromSessionLog(path, maxInputHistoryEntries)
	if err != nil {
		return err
	}

	m.messages = append(m.messages, replay.messages...)
	m.inputHistory = inputHistory
	m.resetInputHistoryNavigation()

	m.history.entries = append(m.history.entries, replay.history...)
	if len(m.history.entries) > 0 {
		m.history.selected = len(m.history.entries) - 1
	}

	m.requestHistoryPanelRender(true, true)
	if strings.TrimSpace(m.directFeaturePlannerSessionID) == "" {
		m.restoreDirectFeaturePlannerSessionFromSessionLog(path)
	}

	return nil
}

func replaySessionLog(path string) (sessionReplay, error) {
	var replay sessionReplay

	currentAgent := -1

	ensureAgent := func() *historyEntry {
		if currentAgent >= 0 && currentAgent < len(replay.history) {
			entry := &replay.history[currentAgent]
			if entry.sender == "agent" && entry.inProgress {
				return entry
			}
		}

		replay.history = append(replay.history, historyEntry{sender: "agent", inProgress: true})
		currentAgent = len(replay.history) - 1

		return &replay.history[currentAgent]
	}
	finalizeAgent := func() {
		if currentAgent < 0 || currentAgent >= len(replay.history) {
			currentAgent = -1
			return
		}

		entry := &replay.history[currentAgent]

		entry.inProgress = false

		if len(entry.contents) == 0 && len(entry.toolCalls) == 0 && len(entry.auditEvents) == 0 {
			replay.history = append(replay.history[:currentAgent], replay.history[currentAgent+1:]...)
		} else {
			entry.summary = localFallbackSummary(entry)
		}

		currentAgent = -1
	}
	appendAudit := func(event historyAuditEvent) {
		event = cleanHistoryAuditEvent(event)
		if event.label == "" {
			return
		}

		if currentAgent < 0 && len(replay.history) > 0 && replay.history[len(replay.history)-1].sender == "agent" {
			entry := &replay.history[len(replay.history)-1]

			entry.auditEvents = append(entry.auditEvents, event)
			entry.summary = localFallbackSummary(entry)

			return
		}

		entry := ensureAgent()

		entry.auditEvents = append(entry.auditEvents, event)
		entry.summary = localFallbackSummary(entry)
	}

	err := jsonl.ScanFile(path, func(raw json.RawMessage) error {
		var record map[string]any

		if err := json.Unmarshal(raw, &record); err != nil {
			return nil
		}

		recordType := sessionStringField(record, "type")
		switch recordType {
		case "message":
			role := sessionStringField(record, "role")
			content := strings.TrimSpace(sessionStringField(record, "content"))

			if content == "" {
				return nil
			}

			switch role {
			case "user":
				finalizeAgent()

				replay.messages = append(replay.messages, llama.ChatMessage{Role: role, Content: content})

				entry := historyEntry{sender: "user", contents: []string{content}}

				entry.summary = localFallbackSummary(&entry)
				replay.history = append(replay.history, entry)
			case "assistant":
				replay.messages = append(replay.messages, llama.ChatMessage{Role: role, Content: content})

				entry := ensureAgent()

				entry.contents = append(entry.contents, content)

				finalizeAgent()
			default:
				if audit, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: role, Content: content}); ok {
					appendAudit(audit)
				}
			}
		case "tool_request":
			entry := ensureAgent()
			event := sessionToolRequestEvent(record)
			line := sessionToolLine(event.Request.Tool, textutil.FirstNonEmpty(event.Request.Description, sessionStringField(record, "action"), sessionStringField(record, "raw")))
			entry.upsertToolCall(event.Step, line, toolRequestIntent(event), true, historyToolAuditKind(event), "")

			entry.summary = localFallbackSummary(entry)
		case "tool_result":
			entry := ensureAgent()
			step := sessionIntField(record, "step")
			tool := sessionStringField(record, "tool")
			summary := sessionStringField(record, "summary")
			line := sessionToolLine(tool, textutil.FirstNonEmpty(summary, sessionStringField(record, "path"), "completed"))
			entry.upsertToolCall(step, line, "", false, "", sessionToolResultOutcome(record))

			entry.auditEvents = append(entry.auditEvents, sessionContextAuditEvents(record)...)
			entry.summary = localFallbackSummary(entry)
		case "run_event":
			role := sessionStringField(record, "role")
			content := sessionStringField(record, "content")

			if audit, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: role, Content: content}); ok {
				appendAudit(audit)
			}
		case "model_call":
			if audit, ok := historyAuditModelCall(sessionPayloadJSON(record)); ok {
				appendAudit(audit)
			}
		case "phase":
			if audit, ok := historyAuditPhase(sessionPayloadJSON(record)); ok {
				appendAudit(audit)
			}
		case "turn_summary":
			if audit, ok := historyAuditBudget(sessionPayloadJSON(record)); ok {
				appendAudit(audit)
			}
		case "session_summary":
			appendAudit(historyAuditEvent{
				kind:   "session",
				label:  "session summary loaded",
				detail: sessionSummaryDetail(record),
				status: "loaded",
			})
		}

		return nil
	})
	if err != nil {
		return sessionReplay{}, fmt.Errorf("read session log: %w", err)
	}

	finalizeAgent()

	for i := range replay.history {
		replay.history[i].inProgress = false
		replay.history[i].summary = localFallbackSummary(&replay.history[i])
	}

	return replay, nil
}

func sessionToolRequestEvent(record map[string]any) agent.ToolRequestEvent {
	action := sessionStringField(record, "action")
	if action == "" {
		action = sessionStringField(record, "raw")
	}

	return agent.ToolRequestEvent{
		Step:           sessionIntField(record, "step"),
		Agent:          sessionStringField(record, "agent"),
		DiscoveryClass: sessionStringField(record, "discovery_class"),
		DiscoveryScope: sessionStringField(record, "discovery_scope"),
		DiscoveryCost:  sessionStringField(record, "discovery_cost"),
		Request: workspace.ToolRequest{
			Tool:        sessionStringField(record, "tool"),
			Description: action,
			Command:     action,
		},
	}
}

func sessionToolLine(tool, detail string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "tool"
	}

	detail = truncateRunes(collapseWhitespace(strings.TrimSpace(detail)), 140)
	if detail == "" {
		return tool
	}

	return tool + " | " + detail
}

func sessionToolResultOutcome(record map[string]any) string {
	if errText := strings.TrimSpace(sessionStringField(record, "error")); errText != "" {
		return "failed: " + truncateRunes(collapseWhitespace(errText), 120)
	}

	summary := strings.TrimSpace(sessionStringField(record, "summary"))
	if summary == "" {
		summary = "completed"
	}

	outcome := "ok: " + truncateRunes(collapseWhitespace(summary), 100)
	lines := sessionIntField(record, "output_lines")
	chars := sessionIntField(record, "output_chars")

	if lines > 0 || chars > 0 {
		outcome += fmt.Sprintf(" (%d line(s), %d char(s))", lines, chars)
	}

	return outcome
}

func sessionContextAuditEvents(record map[string]any) []historyAuditEvent {
	var events []historyAuditEvent

	if state := sessionStringField(record, "context_cache_state"); state != "" {
		detail := joinNonEmpty("; ",
			keyValue("source", sessionStringField(record, "context_cache_source")),
			keyValue("reason", sessionStringField(record, "context_cache_reason")),
			keyValue("pack", sessionStringField(record, "context_cache_pack_id")),
		)

		events = append(events, historyAuditEvent{kind: "cache", label: "context cache " + state, detail: detail, status: state})
	}

	if sessionBoolField(record, "context_auto_refresh_attempted") || sessionBoolField(record, "context_freshness_refresh_attempted") {
		files := sessionIntField(record, "context_auto_refresh_files_indexed")
		if files == 0 {
			files = sessionIntField(record, "context_freshness_refresh_files_indexed")
		}

		reason := textutil.FirstNonEmpty(sessionStringField(record, "context_auto_refresh_reason"), sessionStringField(record, "context_freshness_reason"))
		label := "context freshness refresh"

		if files > 0 {
			label = fmt.Sprintf("context freshness refresh indexed %d file(s)", files)
		}

		events = append(events, historyAuditEvent{kind: "cache", label: label, detail: keyValue("reason", reason), status: "refresh"})
	}

	if state := sessionStringField(record, "context_freshness_state"); state != "" {
		detail := joinNonEmpty("; ",
			keyValue("policy", sessionStringField(record, "context_freshness_policy")),
			keyValue("reason", sessionStringField(record, "context_freshness_reason")),
		)

		events = append(events, historyAuditEvent{kind: "freshness", label: "context evidence " + state, detail: detail, status: state})
	}

	return events
}

func sessionSummaryDetail(record map[string]any) string {
	return joinNonEmpty("; ",
		intKeyValue("turns", sessionIntField(record, "turns")),
		intKeyValue("model_calls", sessionIntField(record, "model_calls")),
		intKeyValue("tool_calls", sessionIntField(record, "tool_calls")),
		intKeyValue("tokens", sessionIntField(record, "total_tokens")),
	)
}

func sessionPayloadJSON(record map[string]any) string {
	payload := make(map[string]any, len(record))
	for key, value := range record {
		if key == "ts" || key == "type" {
			continue
		}

		payload[key] = value
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}

	return string(body)
}

func sessionStringField(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return strings.TrimSpace(value)
}

func sessionBoolField(record map[string]any, key string) bool {
	value, _ := record[key].(bool)
	return value
}

func sessionIntField(record map[string]any, key string) int {
	switch value := record[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}
