package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"idiom/internal/config"
	"idiom/internal/transcript"
)

func TestInputHistoryNavigationUsesSubmittedPrompts(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("first prompt")
	m.recordInputHistory("second prompt")

	m.input.SetValue("draft prompt")
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("expected up arrow to navigate input history")
	}
	if got := m.input.Value(); got != "second prompt" {
		t.Fatalf("first up should show newest prompt, got %q", got)
	}
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("expected second up arrow to navigate input history")
	}
	if got := m.input.Value(); got != "first prompt" {
		t.Fatalf("second up should show older prompt, got %q", got)
	}
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("expected down arrow to navigate input history")
	}
	if got := m.input.Value(); got != "second prompt" {
		t.Fatalf("down should move toward newer prompt, got %q", got)
	}
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("expected down arrow to restore draft after newest prompt")
	}
	if got := m.input.Value(); got != "draft prompt" {
		t.Fatalf("down past newest should restore draft, got %q", got)
	}
}

func TestInputHistoryDoesNotOverrideFocusedPanelsOrSlashSuggestions(t *testing.T) {
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             textarea.New(),
		inputHistory:      []string{"prior prompt"},
		inputHistoryIndex: -1,
		slashSuggestions: slashSuggestionsState{
			visible:  true,
			selected: 1,
			matches:  []slashSuggestionEntry{{command: "/one"}, {command: "/two"}},
		},
	}
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("slash suggestion up arrow should be handled")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("slash suggestion navigation should not alter input history value, got %q", got)
	}
	if m.slashSuggestions.selected != 0 {
		t.Fatalf("slash suggestion selection should move, got %d", m.slashSuggestions.selected)
	}

	m.slashSuggestions = slashSuggestionsState{}
	m.history.panelOpen = true
	m.history.focused = true
	m.history.entries = []historyEntry{{sender: "user"}, {sender: "agent"}}
	m.history.selected = 1
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("focused history panel up arrow should be handled")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("focused panel navigation should not alter input history value, got %q", got)
	}
	if m.history.selected != 0 {
		t.Fatalf("history panel selection should move, got %d", m.history.selected)
	}
}

func TestInputHistoryRecordsSubmissionsAndResetsOnManualEdit(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("repeat")
	m.recordInputHistory("repeat")
	m.recordInputHistory("next")
	if got := len(m.inputHistory); got != 2 {
		t.Fatalf("duplicate submissions should collapse, got %d entries", got)
	}
	if got := m.inputHistory[1]; got != "next" {
		t.Fatalf("newest history entry = %q, want next", got)
	}

	m.recordInputHistory("repeat")
	if got := strings.Join(m.inputHistory, "|"); got != "next|repeat" {
		t.Fatalf("re-submitted duplicate should move to newest slot, got %q", got)
	}

	m.inputHistoryIndex = 1
	m.inputHistoryDraft = "draft"
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); handled {
		t.Fatal("manual edit should pass through to textarea handling")
	}
	if m.inputHistoryIndex != -1 || m.inputHistoryDraft != "" {
		t.Fatalf("manual edit should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft)
	}
}

func TestInputHistoryCapsAtFiftyMostRecentUniqueEntries(t *testing.T) {
	m := &Model{}
	for i := 0; i < maxInputHistoryEntries+5; i++ {
		m.recordInputHistory(fmt.Sprintf("prompt-%02d", i))
	}
	if got := len(m.inputHistory); got != maxInputHistoryEntries {
		t.Fatalf("history length = %d, want %d", got, maxInputHistoryEntries)
	}
	if got := m.inputHistory[0]; got != "prompt-05" {
		t.Fatalf("oldest retained prompt = %q, want prompt-05", got)
	}
	if got := m.inputHistory[len(m.inputHistory)-1]; got != "prompt-54" {
		t.Fatalf("newest retained prompt = %q, want prompt-54", got)
	}
}

func TestInputHistoryLoadsFromRecordedSessionsOldestToNewestDeduped(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	writeSessionLog(t, dir, "20260418-055543.504.jsonl", []string{
		`{"type":"message","role":"user","content":"older prompt"}`,
		`{"type":"message","role":"assistant","content":"ignored"}`,
		`{"type":"message","role":"user","content":"repeat me"}`,
	})
	writeSessionLog(t, dir, "20260419-100000.000.jsonl", []string{
		`{"type":"message","role":"user","content":"newer prompt"}`,
		`{"type":"message","role":"user","content":"repeat me"}`,
	})
	writeSessionLog(t, dir, "notes.txt", []string{
		`{"type":"message","role":"user","content":"not a session"}`,
	})

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}
	want := []string{"older prompt", "newer prompt", "repeat me"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestLoadHistoryFromSessionSeedsInputHistory(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"message","role":"user","content":"first prompt"}`,
		`{"type":"message","role":"assistant","content":"first reply"}`,
		`{"type":"message","role":"user","content":"second prompt"}`,
	})

	model := &Model{}
	if err := model.LoadHistoryFromSession(path); err != nil {
		t.Fatalf("LoadHistoryFromSession: %v", err)
	}
	if got := strings.Join(model.inputHistory, "|"); got != "first prompt|second prompt" {
		t.Fatalf("input history = %q", got)
	}
}

func TestInputHistoryLoadsSubmittedInputBeforeLaneNormalizedUserMessages(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  dir,
		Mode: transcript.DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}

	model := &Model{debugLogger: logger}
	model.recordInputSubmission("/plan-execute fix the up arrow history")
	if err := logger.LogMessage("user", "fix the up arrow history"); err != nil {
		t.Fatalf("LogMessage: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}
	want := []string{"/plan-execute fix the up arrow history"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestInputHistoryFromRecordedSessionsMissingDir(t *testing.T) {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: filepath.Join(t.TempDir(), "missing")}}
	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty history for missing dir, got %#v", got)
	}
}
