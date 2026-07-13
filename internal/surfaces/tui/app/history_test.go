package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
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

var _ = ginkgo.It("live agent history entry updates tool call in place", func() {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect the app", nil)
	m.beginAgentHistoryTurn()

	gomega.Expect(len(m.history.entries)).To(gomega.Equal(2), fmt.Sprintf("expected user entry plus live agent entry, got %d", len(m.history.entries)))

	agentEntry := &m.history.entries[1]
	gomega.Expect(agentEntry.inProgress).To(gomega.BeTrue(), fmt.Sprint("agent history entry should be marked in progress"))

	gomega.Expect(agentEntry.summary).To(gomega.Equal("working…"), fmt.Sprintf("placeholder summary = %q, want working…", agentEntry.summary))

	m.recordToolTraceInHistory(1, "read_file | running internal/surfaces/tui/app/history.go", "inspect the history model", true, "read", "", nil)

	gomega.Expect(len(agentEntry.toolCalls)).To(gomega.Equal(1), fmt.Sprintf("expected one live tool line, got %d", len(agentEntry.toolCalls)))

	gomega.Expect(agentEntry.toolCalls[0].running).To(gomega.BeTrue(), fmt.Sprint("tool line should be marked running"))

	gomega.Expect(strings.Contains(m.history.viewport.View(), "Tools: 1 call(s) (1 read); 1 running")).To(gomega.BeTrue(), fmt.Sprintf("history viewport did not render compact tool summary:\n%s", m.history.viewport.View()))

	gomega.Expect(strings.Contains(m.history.viewport.View(), "Latest: read_file running - inspect the history model")).To(gomega.BeTrue(), fmt.Sprintf("history viewport did not render latest tool intent:\n%s", m.history.viewport.View()))

	m.recordToolTraceInHistory(1, "read_file | Read internal/surfaces/tui/app/history.go lines 1-40", "", false, "", "ok: read 40 lines", nil)

	gomega.Expect(len(agentEntry.toolCalls)).To(gomega.Equal(1), fmt.Sprintf("tool result should update existing step, got %d lines", len(agentEntry.toolCalls)))

	gomega.Expect(agentEntry.toolCalls[0].running).To(gomega.BeFalse(), fmt.Sprint("tool line should no longer be marked running"))

	gomega.Expect(strings.Contains(agentEntry.toolCalls[0].line, "running")).To(gomega.BeFalse(), fmt.Sprintf("tool line was not replaced with result: %q", agentEntry.toolCalls[0].line))

	gomega.Expect(agentEntry.toolCalls[0].intent).To(gomega.Equal("inspect the history model"), fmt.Sprintf("tool result should preserve intent, got %q", agentEntry.toolCalls[0].intent))

	gomega.Expect(agentEntry.toolCalls[0].kind).To(gomega.Equal("read"), fmt.Sprintf("tool audit metadata not preserved: %#v", agentEntry.toolCalls[0]))
	gomega.Expect(agentEntry.toolCalls[0].outcome).To(gomega.Equal("ok: read 40 lines"), fmt.Sprintf("tool audit metadata not preserved: %#v", agentEntry.toolCalls[0]))
})

var _ = ginkgo.It("record agent turn finalizes live history entry", func() {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "fix the panel", nil)
	m.beginAgentHistoryTurn()
	m.recordToolTraceInHistory(1, "search_content | Found 2 matching lines", "locate references before editing", false, "discovery / text_search", "ok: found matches", nil)

	m.messages = []llama.ChatMessage{{Role: "assistant", Content: "Updated the history panel."}}

	m.recordAgentTurn()

	gomega.Expect(len(m.history.entries)).To(gomega.Equal(2), fmt.Sprintf("expected two history entries, got %d", len(m.history.entries)))

	agentEntry := m.history.entries[1]
	gomega.Expect(agentEntry.inProgress).To(gomega.BeFalse(), fmt.Sprint("agent history entry should be finalized"))

	gomega.Expect(m.currentTurnHistoryActive).To(gomega.BeFalse(), fmt.Sprint("current turn history should be inactive after finalization"))

	gomega.Expect(len(agentEntry.toolCalls)).To(gomega.Equal(1), fmt.Sprintf("expected one retained tool line, got %d", len(agentEntry.toolCalls)))

	gomega.Expect(len(agentEntry.contents)).To(gomega.Equal(1), fmt.Sprintf("final assistant response not attached to live entry: %#v", agentEntry.contents))
	gomega.Expect(agentEntry.contents[0]).To(gomega.Equal("Updated the history panel."), fmt.Sprintf("final assistant response not attached to live entry: %#v", agentEntry.contents))
})

var _ = ginkgo.It("tool request intent uses description when available", func() {
	got := toolRequestIntent(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:        "edit_file",
			Path:        "internal/surfaces/tui/app/history.go",
			Description: "Add inferred tool intent to the live history panel.",
		},
	})
	want := "Add inferred tool intent to the live history panel."

	gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("toolRequestIntent = %q, want %q", got, want))
})

var _ = ginkgo.It("tool request intent infers search purpose", func() {
	got := toolRequestIntent(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "recordToolTraceInHistory",
			Glob:    "internal/surfaces/tui/app/*.go",
		},
	})
	want := "locate references to recordToolTraceInHistory in internal/surfaces/tui/app/*.go"

	gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("toolRequestIntent = %q, want %q", got, want))
})

var _ = ginkgo.It("history renders compact audit digest", func() {
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

	view := collapseWhitespace(m.history.viewport.View())
	for _, want := range []string{
		"Route: explore -> implement (implementation_result)",
		"Tools: 1 call(s) (1 discovery)",
		"Latest: context_pack ok - build a ranked context packet before choosing exact reads",
		"Signals: cache refreshed",
	} {
		gomega.Expect(strings.Contains(view, want)).To(gomega.BeTrue(), fmt.Sprintf("history viewport missing %q:\n%s", want, view))
	}

	for _, noisy := range []string{
		"lane [explore]: route selected",
		"type=discovery / context",
		"outcome=ok: ranked 8 file(s)",
		"reason: build a ranked context packet before choosing exact reads",
	} {
		gomega.Expect(strings.Contains(view, noisy)).To(gomega.BeFalse(), fmt.Sprintf("compact history should not render raw detail %q:\n%s", noisy, view))
	}
})

var _ = ginkgo.It("history trace detail renders raw audit events and tool metadata", func() {
	m := newHistoryPanelModel()

	m.traceMode = traceDisplayDetail
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

	view := collapseWhitespace(m.history.viewport.View())
	for _, want := range []string{
		"Details:",
		"lane [explore]: route selected",
		"cache [refresh]: context freshness refresh indexed 12 file(s)",
		"type=discovery / context",
		"outcome=ok: ranked 8 file(s)",
		"reason: build a ranked context packet before choosing exact reads",
	} {
		gomega.Expect(strings.Contains(view, want)).To(gomega.BeTrue(), fmt.Sprintf("detail history viewport missing %q:\n%s", want, view))
	}
})

var _ = ginkgo.It("history audit event from run event summarizes contract signals", func() {
	event, ok := historyAuditEventFromRunEvent(agent.RunEvent{
		Role:    "completion_status",
		Content: `{"lane":"implement","contract":"implementation_result","status":"blocked","blocker_kind":"required_edit_paths_remaining","edit_attempted":false}`,
	})
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected completion_status to produce a history audit event"))

	gomega.Expect(event.kind).To(gomega.Equal("contract"), fmt.Sprintf("unexpected completion audit event: %#v", event))
	gomega.Expect(event.label).To(gomega.Equal("contract blocked: implementation_result"), fmt.Sprintf("unexpected completion audit event: %#v", event))
	gomega.Expect(event.detail).To(gomega.ContainSubstring("blocker=required_edit_paths_remaining"), fmt.Sprintf("unexpected completion audit event: %#v", event))
})

var _ = ginkgo.It("history audit event from autonomy obligation", func() {
	event, ok := historyAuditEventFromRunEvent(agent.RunEvent{
		Role:    "autonomy_event",
		Content: `{"type":"obligation_resolved","contract_id":"contract_123","obligation":{"obligation_id":"source-body","kind":"source_evidence","status":"resolved","target_ref":"internal/surfaces/tui/app/history.go","resolution":"read_file"}}`,
	})
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected autonomy event to produce a history audit event"))

	gomega.Expect(event.kind).To(gomega.Equal("obligation"), fmt.Sprintf("unexpected autonomy audit event: %#v", event))
	gomega.Expect(event.status).To(gomega.Equal("resolved"), fmt.Sprintf("unexpected autonomy audit event: %#v", event))
	gomega.Expect(event.auditLine()).To(gomega.ContainSubstring("source_evidence"), fmt.Sprintf("unexpected autonomy audit event: %#v", event))
})

var _ = ginkgo.It("history audit event from child tool events", func() {
	t := ginkgo.GinkgoT()
	requestPayload := mustMarshalJSON(t, agent.ToolRequestEvent{
		Step:  2,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "historyAuditEvent",
			Path:    "internal/surfaces/tui/app",
		},
	})
	requestEvent, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: "child_tool_request", Content: requestPayload})

	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected child tool request audit event"))

	gomega.Expect(requestEvent.kind).To(gomega.Equal("delegation"), fmt.Sprintf("unexpected child request audit event: %#v", requestEvent))
	gomega.Expect(requestEvent.status).To(gomega.Equal("requested"), fmt.Sprintf("unexpected child request audit event: %#v", requestEvent))
	gomega.Expect(requestEvent.detail).To(gomega.ContainSubstring("reason=locate references"), fmt.Sprintf("unexpected child request audit event: %#v", requestEvent))

	resultPayload := mustMarshalJSON(t, agent.ToolResultEvent{
		Step:  2,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool: "search_content",
		},
		Result: workspace.ToolResult{Tool: "search_content", Summary: "Found 3 lines", Output: "a\nb\nc\n"},
	})
	resultEvent, ok := historyAuditEventFromRunEvent(agent.RunEvent{Role: "child_tool_result", Content: resultPayload})

	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected child tool result audit event"))

	gomega.Expect(resultEvent.kind).To(gomega.Equal("delegation"), fmt.Sprintf("unexpected child result audit event: %#v", resultEvent))
	gomega.Expect(resultEvent.status).To(gomega.Equal("ok"), fmt.Sprintf("unexpected child result audit event: %#v", resultEvent))
	gomega.Expect(resultEvent.detail).To(gomega.ContainSubstring("3 line(s)"), fmt.Sprintf("unexpected child result audit event: %#v", resultEvent))
})

var _ = ginkgo.It("fail agent history turn finalizes placeholder", func() {
	m := newHistoryPanelModel()
	m.beginAgentHistoryTurn()

	m.failAgentHistoryTurn(errors.New("server unavailable"))

	gomega.Expect(len(m.history.entries)).To(gomega.Equal(1), fmt.Sprintf("expected failed placeholder to remain as history entry, got %d", len(m.history.entries)))

	entry := m.history.entries[0]
	gomega.Expect(entry.inProgress).To(gomega.BeFalse(), fmt.Sprint("failed history entry should not remain in progress"))

	gomega.Expect(strings.Contains(entry.summary, "Chat failed: server unavailable")).To(gomega.BeTrue(), fmt.Sprintf("failure summary missing error: %q", entry.summary))
})

var _ = ginkgo.It("record tool trace in history throttles live history render", func() {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect the app", nil)
	m.beginAgentHistoryTurn()

	m.streaming = true
	m.history.focused = false
	m.history.renderLast = time.Now()

	m.recordToolTraceInHistory(1, "read_file | running internal/surfaces/tui/app/history.go", "inspect the history model", true, "read", "", nil)

	gomega.Expect(m.history.renderPending).To(gomega.BeTrue(), fmt.Sprint("expected live history render to be throttled while streaming"))

	gomega.Expect(strings.Contains(m.history.viewport.View(), "read_file | running internal/surfaces/tui/app/history.go")).To(gomega.BeFalse(), fmt.Sprintf("expected throttled render to defer viewport update, got:\n%s", m.history.viewport.View()))
})

var _ = ginkgo.It("ctrl b opens history tab and sets status while streaming", func() {
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

	gomega.Expect(updated.history.panelOpen).To(gomega.BeTrue(), fmt.Sprint("expected Ctrl+B to open the panel"))

	gomega.Expect(updated.panelTab).To(gomega.Equal(panelTabHistory), fmt.Sprintf("expected Ctrl+B to switch to history tab, got %d", updated.panelTab))

	gomega.Expect(strings.Contains(updated.status, "History panel open")).To(gomega.BeTrue(), fmt.Sprintf("expected status to acknowledge panel open, got %q", updated.status))
})
