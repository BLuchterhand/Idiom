package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestClassifyTUIRequestDetectsImplementationBeforeModelWork(t *testing.T) {
	got := classifyTUIRequest("Implement persistent input history.")
	if got.Class != tuiRequestClassImplementation {
		t.Fatalf("class = %q, want implementation", got.Class)
	}
	if !got.RequiresConfirmation || got.Command != "/implement" {
		t.Fatalf("implementation classification should require /implement confirmation: %#v", got)
	}
}

func TestClassifyTUIRequestHonorsReadOnlyConstraint(t *testing.T) {
	got := classifyTUIRequest("Just explain how we would implement persistent input history; do not edit files.")
	if got.RequiresConfirmation {
		t.Fatalf("read-only request should not require mutation confirmation: %#v", got)
	}
	if got.Class != tuiRequestClassConversation {
		t.Fatalf("class = %q, want conversation", got.Class)
	}
}

func TestClassifyTUIRequestDetectsPlanExecuteIntent(t *testing.T) {
	got := classifyTUIRequest("Send this through plan-execute and implement the atom.")
	if got.Class != tuiRequestClassPlanExecute {
		t.Fatalf("class = %q, want plan-execute", got.Class)
	}
	if !got.RequiresConfirmation || got.Command != "/plan-execute" {
		t.Fatalf("plan-execute classification should require /plan-execute confirmation: %#v", got)
	}
}

func TestHandleTUIRequestPreflightConfirmsMutationWithoutStartingStream(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.input.SetValue("Implement persistent input history.")

	m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	if m.streaming {
		t.Fatal("preflight confirmation should not start a model stream")
	}
	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want user + local assistant confirmation", len(m.messages))
	}
	if got := m.messages[0].Role; got != "user" {
		t.Fatalf("first message role = %q, want user", got)
	}
	if got := m.messages[1].Role; got != "assistant" {
		t.Fatalf("second message role = %q, want assistant", got)
	}
	if !strings.Contains(m.messages[1].Content, "`/implement <objective>`") {
		t.Fatalf("assistant confirmation should name /implement command, got %q", m.messages[1].Content)
	}
	if !strings.Contains(m.status, "Request classified: implementation") {
		t.Fatalf("status = %q", m.status)
	}
}
