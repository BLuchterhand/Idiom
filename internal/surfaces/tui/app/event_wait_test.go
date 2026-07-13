package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("event waiter is single flight", func() {
	m := &Model{
		events: make(chan tea.Msg, 1),
		input:  textarea.New(),
	}

	first := m.waitForEventCmd()
	gomega.Expect(first).NotTo(gomega.BeNil(), fmt.Sprint("first event waiter should arm"))

	{
		second := m.waitForEventCmd()
		gomega.Expect(second).To(gomega.BeNil(), fmt.Sprint("second event waiter should be suppressed while one is armed"))
	}

	m.events <- infoMsg{text: "ready"}

	msg := first()
	wrapped, ok := msg.(eventMsg)

	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("event waiter returned %T, want eventMsg", msg))

	{
		_, ok := wrapped.msg.(infoMsg)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("wrapped message = %T, want infoMsg", wrapped.msg))
	}

	_, cmd := m.Update(msg)
	gomega.Expect(cmd).NotTo(gomega.BeNil(), fmt.Sprint("handling an event should rearm the event waiter"))

	gomega.Expect(m.eventWaitArmed).To(gomega.BeTrue(), fmt.Sprint("event waiter should be armed after handling an event"))

	{
		extra := m.waitForEventCmd()
		gomega.Expect(extra).To(gomega.BeNil(), fmt.Sprint("manual rearm should be suppressed while the event waiter is armed"))
	}
})

var _ = ginkgo.It("spinner ticks do not spawn event waiters", func() {
	m := &Model{
		events:    make(chan tea.Msg, 1),
		input:     textarea.New(),
		spinner:   spinner.New(),
		streaming: true,
	}
	{
		cmd := m.waitForEventCmd()
		gomega.Expect(cmd).NotTo(gomega.BeNil(), fmt.Sprint("event waiter should arm before spinner ticks"))
	}

	for i := 0; i < 5; i++ {
		_, _ = m.Update(spinner.TickMsg{})
	}

	gomega.Expect(m.eventWaitArmed).To(gomega.BeTrue(), fmt.Sprint("spinner ticks should leave the existing waiter armed"))

	{
		extra := m.waitForEventCmd()
		gomega.Expect(extra).To(gomega.BeNil(), fmt.Sprint("spinner ticks should not create another event waiter"))
	}
})

var _ = ginkgo.It("headless agent turn does not block on full ui event buffer after tool result", ginkgo.SpecTimeout(2*time.Second), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	m.SetNonInteractive(true)
	m.events = make(chan tea.Msg, 1)
	m.cfg.Chat.MaxToolSteps = 1
	client := &scriptedLLMRecorder{responses: []string{
		`{"tool":"read_file","path":"README.md"}`,
		"README.md says hello.",
	}}
	m.client = client.Mock(t)
	session := newPromptExecutionSession(ctx, m, m.events, false)

	{
		err := os.WriteFile(filepath.Join(m.workspace.Root(), "README.md"), []byte("hello\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write README: %v", err))
	}

	type runResult struct {
		answer string
		err    error
	}

	done := make(chan runResult, 1)
	go func() {
		answer, err := session.runAgentTurn(
			ctx,
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

	var result runResult
	gomega.Eventually(ctx, done).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runAgentTurn: %v", result.err))
	gomega.Expect(strings.Contains(result.answer, "README.md")).To(gomega.BeTrue(), fmt.Sprintf("expected final answer from forced closeout, got %q", result.answer))
})

var _ = ginkgo.It("implementation dispatch uses headless safe ui event queue", func() {
	_, sourceFile, _, ok := runtime.Caller(0)
	gomega.Expect(ok).To(gomega.BeTrue(), "resolve event_wait_test.go source path")
	body, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "implementation_dispatch.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read implementation_dispatch.go: %v", err))

	gomega.Expect(strings.Contains(string(body), "m.events <-")).To(gomega.BeFalse(), fmt.Sprint("implementation dispatch must route UI messages through queueUIEvent so headless plan-execute dispatch cannot block"))
})
