package app

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
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
		t.Fatalf("consecutive duplicate submissions should collapse, got %d entries", got)
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
