package app

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestEventWaiterIsSingleFlight(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 1),
		input:  textarea.New(),
	}

	first := m.waitForEventCmd()
	if first == nil {
		t.Fatal("first event waiter should arm")
	}
	if second := m.waitForEventCmd(); second != nil {
		t.Fatal("second event waiter should be suppressed while one is armed")
	}

	m.events <- infoMsg{text: "ready"}
	msg := first()
	wrapped, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("event waiter returned %T, want eventMsg", msg)
	}
	if _, ok := wrapped.msg.(infoMsg); !ok {
		t.Fatalf("wrapped message = %T, want infoMsg", wrapped.msg)
	}

	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("handling an event should rearm the event waiter")
	}
	if !m.eventWaitArmed {
		t.Fatal("event waiter should be armed after handling an event")
	}
	if extra := m.waitForEventCmd(); extra != nil {
		t.Fatal("manual rearm should be suppressed while the event waiter is armed")
	}
}

func TestSpinnerTicksDoNotSpawnEventWaiters(t *testing.T) {
	m := &Model{
		events:    make(chan tea.Msg, 1),
		input:     textarea.New(),
		spinner:   spinner.New(),
		streaming: true,
	}
	if cmd := m.waitForEventCmd(); cmd == nil {
		t.Fatal("event waiter should arm before spinner ticks")
	}

	for i := 0; i < 5; i++ {
		_, _ = m.Update(spinner.TickMsg{})
	}
	if !m.eventWaitArmed {
		t.Fatal("spinner ticks should leave the existing waiter armed")
	}
	if extra := m.waitForEventCmd(); extra != nil {
		t.Fatal("spinner ticks should not create another event waiter")
	}
}
