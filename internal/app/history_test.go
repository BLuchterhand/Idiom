package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/agent"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func newHistoryPanelModel() *Model {
	return &Model{
		history: historyState{
			panelOpen: true,
			viewport:  viewport.New(72, 20),
		},
		panelTab: panelTabHistory,
		events:   make(chan tea.Msg, 2),
	}
}

func TestLiveAgentHistoryEntryUpdatesToolCallInPlace(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect the app", nil)
	m.beginAgentHistoryTurn()

	if len(m.history.entries) != 2 {
		t.Fatalf("expected user entry plus live agent entry, got %d", len(m.history.entries))
	}
	agentEntry := &m.history.entries[1]
	if !agentEntry.inProgress {
		t.Fatal("agent history entry should be marked in progress")
	}
	if agentEntry.summary != "working…" {
		t.Fatalf("placeholder summary = %q, want working…", agentEntry.summary)
	}

	m.recordToolTraceInHistory(1, "read_file | running internal/app/history.go", "inspect the history model", true, "read", "", nil)
	if len(agentEntry.toolCalls) != 1 {
		t.Fatalf("expected one live tool line, got %d", len(agentEntry.toolCalls))
	}
	if !agentEntry.toolCalls[0].running {
		t.Fatal("tool line should be marked running")
	}
	if !strings.Contains(m.history.viewport.View(), "read_file | running internal/app/history.go") {
		t.Fatalf("history viewport did not render running tool line:\n%s", m.history.viewport.View())
	}
	if !strings.Contains(m.history.viewport.View(), "reason: inspect the history model") {
		t.Fatalf("history viewport did not render tool intent:\n%s", m.history.viewport.View())
	}

	m.recordToolTraceInHistory(1, "read_file | Read internal/app/history.go lines 1-40", "", false, "", "ok: read 40 lines", nil)
	if len(agentEntry.toolCalls) != 1 {
		t.Fatalf("tool result should update existing step, got %d lines", len(agentEntry.toolCalls))
	}
	if agentEntry.toolCalls[0].running {
		t.Fatal("tool line should no longer be marked running")
	}
	if strings.Contains(agentEntry.toolCalls[0].line, "running") {
		t.Fatalf("tool line was not replaced with result: %q", agentEntry.toolCalls[0].line)
	}
	if agentEntry.toolCalls[0].intent != "inspect the history model" {
		t.Fatalf("tool result should preserve intent, got %q", agentEntry.toolCalls[0].intent)
	}
	if agentEntry.toolCalls[0].kind != "read" || agentEntry.toolCalls[0].outcome != "ok: read 40 lines" {
		t.Fatalf("tool audit metadata not preserved: %#v", agentEntry.toolCalls[0])
	}
}

func TestRecordAgentTurnFinalizesLiveHistoryEntry(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "fix the panel", nil)
	m.beginAgentHistoryTurn()
	m.recordToolTraceInHistory(1, "search_content | Found 2 matching lines", "locate references before editing", false, "discovery / text_search", "ok: found matches", nil)
	m.messages = []llama.ChatMessage{{Role: "assistant", Content: "Updated the history panel."}}

	m.recordAgentTurn()

	if len(m.history.entries) != 2 {
		t.Fatalf("expected two history entries, got %d", len(m.history.entries))
	}
	agentEntry := m.history.entries[1]
	if agentEntry.inProgress {
		t.Fatal("agent history entry should be finalized")
	}
	if m.currentTurnHistoryActive {
		t.Fatal("current turn history should be inactive after finalization")
	}
	if len(agentEntry.toolCalls) != 1 {
		t.Fatalf("expected one retained tool line, got %d", len(agentEntry.toolCalls))
	}
	if len(agentEntry.contents) != 1 || agentEntry.contents[0] != "Updated the history panel." {
		t.Fatalf("final assistant response not attached to live entry: %#v", agentEntry.contents)
	}
}

func TestToolRequestIntentUsesDescriptionWhenAvailable(t *testing.T) {
	got := toolRequestIntent(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:        "edit_file",
			Path:        "internal/app/history.go",
			Description: "Add inferred tool intent to the live history panel.",
		},
	})
	want := "Add inferred tool intent to the live history panel."
	if got != want {
		t.Fatalf("toolRequestIntent = %q, want %q", got, want)
	}
}

func TestToolRequestIntentInfersSearchPurpose(t *testing.T) {
	got := toolRequestIntent(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "recordToolTraceInHistory",
			Glob:    "internal/app/*.go",
		},
	})
	want := "locate references to recordToolTraceInHistory in internal/app/*.go"
	if got != want {
		t.Fatalf("toolRequestIntent = %q, want %q", got, want)
	}
}

func TestHistoryRendersAuditEventsAndToolMetadata(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect audit trail", nil)
	m.beginAgentHistoryTurn()

	m.handleHistoryAudit(historyAuditMsg{event: historyAuditEvent{
		kind:   "lane",
		label:  "route selected",
		detail: "initial=explore followup=implement contract=implementation_result",
		status: "explore",
	}})
	m.recordToolTraceInHistory(
		1,
		"context_pack | ranked 8 files",
		"build a ranked context packet before choosing exact reads",
		false,
		"discovery / context",
		"ok: ranked 8 file(s)",
		[]historyAuditEvent{{kind: "cache", label: "context freshness refresh indexed 12 file(s)", detail: "reason=stale_unbounded_graph", status: "refresh"}},
	)

	view := m.history.viewport.View()
	for _, want := range []string{
		"lane [explore]: route selected",
		"cache [refresh]: context freshness refresh indexed 12 file(s)",
		"type=discovery / context",
		"outcome=ok: ranked 8 file(s)",
		"reason: build a ranked context packet before choosing exact reads",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("history viewport missing %q:\n%s", want, view)
		}
	}
}

func TestHistoryAuditEventFromRunEventSummarizesContractSignals(t *testing.T) {
	event, ok := historyAuditEventFromRunEvent(agent.RunEvent{
		Role:    "completion_status",
		Content: `{"lane":"implement","contract":"implementation_result","status":"blocked","blocker_kind":"required_edit_paths_remaining","edit_attempted":false}`,
	})
	if !ok {
		t.Fatal("expected completion_status to produce a history audit event")
	}
	if event.kind != "contract" || event.label != "contract blocked: implementation_result" || !strings.Contains(event.detail, "blocker=required_edit_paths_remaining") {
		t.Fatalf("unexpected completion audit event: %#v", event)
	}
}

func TestHistoryAuditEventFromAutonomyObligation(t *testing.T) {
	event, ok := historyAuditEventFromRunEvent(agent.RunEvent{
		Role:    "autonomy_event",
		Content: `{"type":"obligation_resolved","contract_id":"contract_123","obligation":{"obligation_id":"source-body","kind":"source_evidence","status":"resolved","target_ref":"internal/app/history.go","resolution":"read_file"}}`,
	})
	if !ok {
		t.Fatal("expected autonomy event to produce a history audit event")
	}
	if event.kind != "obligation" || event.status != "resolved" || !strings.Contains(event.auditLine(), "source_evidence") {
		t.Fatalf("unexpected autonomy audit event: %#v", event)
	}
}

func TestHistoryAuditEventFromChildToolEvents(t *testing.T) {
	requestPayload := mustMarshalJSON(t, agent.ToolRequestEvent{
		Step:  2,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "historyAuditEvent",
			Path:    "internal/app",
		},
	})
	requestEvent, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: "child_tool_request", Content: requestPayload})
	if !ok {
		t.Fatal("expected child tool request audit event")
	}
	if requestEvent.kind != "delegation" || requestEvent.status != "requested" || !strings.Contains(requestEvent.detail, "reason=locate references") {
		t.Fatalf("unexpected child request audit event: %#v", requestEvent)
	}

	resultPayload := mustMarshalJSON(t, agent.ToolResultEvent{
		Step:  2,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool: "search_content",
		},
		Result: workspace.ToolResult{Tool: "search_content", Summary: "Found 3 lines", Output: "a\nb\nc\n"},
	})
	resultEvent, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: "child_tool_result", Content: resultPayload})
	if !ok {
		t.Fatal("expected child tool result audit event")
	}
	if resultEvent.kind != "delegation" || resultEvent.status != "ok" || !strings.Contains(resultEvent.detail, "3 line(s)") {
		t.Fatalf("unexpected child result audit event: %#v", resultEvent)
	}
}

func TestFailAgentHistoryTurnFinalizesPlaceholder(t *testing.T) {
	m := newHistoryPanelModel()
	m.beginAgentHistoryTurn()

	m.failAgentHistoryTurn(errors.New("server unavailable"))

	if len(m.history.entries) != 1 {
		t.Fatalf("expected failed placeholder to remain as history entry, got %d", len(m.history.entries))
	}
	entry := m.history.entries[0]
	if entry.inProgress {
		t.Fatal("failed history entry should not remain in progress")
	}
	if !strings.Contains(entry.summary, "Chat failed: server unavailable") {
		t.Fatalf("failure summary missing error: %q", entry.summary)
	}
}

func TestRecordToolTraceInHistoryThrottlesLiveHistoryRender(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect the app", nil)
	m.beginAgentHistoryTurn()
	m.streaming = true
	m.history.focused = false
	m.history.renderLast = time.Now()

	m.recordToolTraceInHistory(1, "read_file | running internal/app/history.go", "inspect the history model", true, "read", "", nil)

	if !m.history.renderPending {
		t.Fatal("expected live history render to be throttled while streaming")
	}
	if strings.Contains(m.history.viewport.View(), "read_file | running internal/app/history.go") {
		t.Fatalf("expected throttled render to defer viewport update, got:\n%s", m.history.viewport.View())
	}
}

func TestCtrlBOpensHistoryTabAndSetsStatusWhileStreaming(t *testing.T) {
	m := &Model{
		ready:     true,
		width:     120,
		height:    40,
		viewport:  viewport.New(80, 20),
		input:     textarea.New(),
		panelTab:  panelTabSessions,
		streaming: true,
		events:    make(chan tea.Msg, 2),
	}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	updated := model.(*Model)

	if !updated.history.panelOpen {
		t.Fatal("expected Ctrl+B to open the panel")
	}
	if updated.panelTab != panelTabHistory {
		t.Fatalf("expected Ctrl+B to switch to history tab, got %d", updated.panelTab)
	}
	if !strings.Contains(updated.status, "History panel open") {
		t.Fatalf("expected status to acknowledge panel open, got %q", updated.status)
	}
}
