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
		historyPanelOpen: true,
		historyViewport:  viewport.New(72, 20),
		panelTab:         panelTabHistory,
		events:           make(chan tea.Msg, 2),
	}
}

func TestLiveAgentHistoryEntryUpdatesToolCallInPlace(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "inspect the app", nil)
	m.beginAgentHistoryTurn()

	if len(m.historyEntries) != 2 {
		t.Fatalf("expected user entry plus live agent entry, got %d", len(m.historyEntries))
	}
	agentEntry := &m.historyEntries[1]
	if !agentEntry.inProgress {
		t.Fatal("agent history entry should be marked in progress")
	}
	if agentEntry.summary != "working…" {
		t.Fatalf("placeholder summary = %q, want working…", agentEntry.summary)
	}

	m.recordToolTraceInHistory(1, "read_file | running internal/app/history.go", "inspect the history model", true)
	if len(agentEntry.toolCalls) != 1 {
		t.Fatalf("expected one live tool line, got %d", len(agentEntry.toolCalls))
	}
	if !agentEntry.toolCalls[0].running {
		t.Fatal("tool line should be marked running")
	}
	if !strings.Contains(m.historyViewport.View(), "read_file | running internal/app/history.go") {
		t.Fatalf("history viewport did not render running tool line:\n%s", m.historyViewport.View())
	}
	if !strings.Contains(m.historyViewport.View(), "why: inspect the history model") {
		t.Fatalf("history viewport did not render tool intent:\n%s", m.historyViewport.View())
	}

	m.recordToolTraceInHistory(1, "read_file | Read internal/app/history.go lines 1-40", "", false)
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
}

func TestRecordAgentTurnFinalizesLiveHistoryEntry(t *testing.T) {
	m := newHistoryPanelModel()
	m.appendHistoryMessage("user", "fix the panel", nil)
	m.beginAgentHistoryTurn()
	m.recordToolTraceInHistory(1, "search_content | Found 2 matching lines", "locate references before editing", false)
	m.messages = []llama.ChatMessage{{Role: "assistant", Content: "Updated the history panel."}}

	m.recordAgentTurn()

	if len(m.historyEntries) != 2 {
		t.Fatalf("expected two history entries, got %d", len(m.historyEntries))
	}
	agentEntry := m.historyEntries[1]
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

func TestFailAgentHistoryTurnFinalizesPlaceholder(t *testing.T) {
	m := newHistoryPanelModel()
	m.beginAgentHistoryTurn()

	m.failAgentHistoryTurn(errors.New("server unavailable"))

	if len(m.historyEntries) != 1 {
		t.Fatalf("expected failed placeholder to remain as history entry, got %d", len(m.historyEntries))
	}
	entry := m.historyEntries[0]
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
	m.historyFocused = false
	m.historyRenderLast = time.Now()

	m.recordToolTraceInHistory(1, "read_file | running internal/app/history.go", "inspect the history model", true)

	if !m.historyRenderPending {
		t.Fatal("expected live history render to be throttled while streaming")
	}
	if strings.Contains(m.historyViewport.View(), "read_file | running internal/app/history.go") {
		t.Fatalf("expected throttled render to defer viewport update, got:\n%s", m.historyViewport.View())
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

	if !updated.historyPanelOpen {
		t.Fatal("expected Ctrl+B to open the panel")
	}
	if updated.panelTab != panelTabHistory {
		t.Fatalf("expected Ctrl+B to switch to history tab, got %d", updated.panelTab)
	}
	if !strings.Contains(updated.status, "History panel open") {
		t.Fatalf("expected status to acknowledge panel open, got %q", updated.status)
	}
}
