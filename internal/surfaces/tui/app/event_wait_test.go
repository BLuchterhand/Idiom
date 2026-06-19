package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
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

func TestHeadlessAgentTurnDoesNotBlockOnFullUIEventBufferAfterToolResult(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	m.SetNonInteractive(true)
	m.events = make(chan tea.Msg, 1)
	m.cfg.Chat.MaxToolSteps = 1
	m.client = &scriptedLLMClient{responses: []string{
		`{"tool":"read_file","path":"README.md"}`,
		"README.md says hello.",
	}}

	if err := os.WriteFile(filepath.Join(m.workspace.Root(), "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	type runResult struct {
		answer string
		err    error
	}

	done := make(chan runResult, 1)
	go func() {
		answer, err := m.runAgentTurn(
			context.Background(),
			"read README",
			[]llama.ChatMessage{{Role: "user", Content: "read README"}},
			llama.ChatOptions{},
			promptExecutionPlan{
				mode: modepolicy.ModeOrDefault("build"),
				route: turnRoutePlan{
					Family:   "headless_test",
					Contract: "ui_event_delivery",
				},
				runtimeBrief: workerruntime.WorkerRuntimeBrief{
					TaskFamily: "conversation",
					Contract:   "direct_answer",
					Objective:  "read README",
				},
			},
		)
		done <- runResult{answer: answer, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("runAgentTurn: %v", result.err)
		}

		if !strings.Contains(result.answer, "README.md") {
			t.Fatalf("expected final answer from forced closeout, got %q", result.answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("headless runAgentTurn blocked on UI event delivery")
	}
}

func TestImplementationDispatchUsesHeadlessSafeUIEventQueue(t *testing.T) {
	body, err := os.ReadFile("implementation_dispatch.go")
	if err != nil {
		t.Fatalf("read implementation_dispatch.go: %v", err)
	}

	if strings.Contains(string(body), "m.events <-") {
		t.Fatal("implementation dispatch must route UI messages through queueUIEvent so headless plan-execute dispatch cannot block")
	}
}
