package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/platform/config"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("tui read approval decider auto denies in headless mode", ginkgo.SpecTimeout(500*time.Millisecond), func(ctx ginkgo.SpecContext) {
	model := &Model{}

	var buf bytes.Buffer
	model.SetJSONEventStream(&buf, "test-session")
	session := newDetachedPromptExecutionSession(workspace.WithExecutionSessionID(ctx, "test-session"), model)

	decider := newTuiReadApprovalDecider(session)

	type readResult struct {
		decision readapproval.Decision
		err      error
	}
	done := make(chan readResult, 1)
	go func() {
		decision, err := decider.DecideRead(session.promptExecutionContext, readapproval.Request{
			SessionID: "test-session",
			Path:      "docs/design-intent-loop-mutation-cycle.md",
			Tool:      "read_file",
			Lane:      "plan-then-execute-planner",
		})
		done <- readResult{decision: decision, err: err}
	}()

	var result readResult
	gomega.Eventually(ctx, done).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	decision := result.decision
	gomega.Expect(decision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied), fmt.Sprintf("expected headless mode to auto-deny, got %v", decision.Outcome))
	gomega.Expect(decision.Path).To(gomega.Equal("docs/design-intent-loop-mutation-cycle.md"), fmt.Sprintf("expected decision to carry the requested path, got %q", decision.Path))

	emitted := buf.String()
	gomega.Expect(strings.Contains(emitted, "read_approval_requested")).To(gomega.BeTrue(), fmt.Sprintf("expected read_approval_requested event in JSON stream, got: %s", emitted))
	gomega.Expect(strings.Contains(emitted, "read_approval_decision")).To(gomega.BeFalse(), fmt.Sprintf("decider should not emit read_approval_decision; gate owns decision provenance, got: %s", emitted))
	gomega.Expect(strings.Contains(emitted, `"path":"docs/design-intent-loop-mutation-cycle.md"`)).To(gomega.BeTrue(), fmt.Sprintf("event should name the requested path, got: %s", emitted))
})

var _ = ginkgo.It("tui read approval decider handles nil model", func() {
	decider := newTuiReadApprovalDecider(nil)

	decision, err := decider.DecideRead(context.Background(), readapproval.Request{
		SessionID: "s",
		Path:      "docs/foo.md",
	})
	gomega.Expect(err).To(gomega.Equal(readapproval.ErrNoDecider), fmt.Sprintf("expected ErrNoDecider, got %v", err))
	gomega.Expect(decision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied), fmt.Sprintf("expected OutcomeDenied, got %v", decision.Outcome))
	gomega.Expect(decision.Reason).To(gomega.Equal(readapproval.ReasonNoDeciderWired), fmt.Sprintf("expected ReasonNoDeciderWired, got %q", decision.Reason))
})

var _ = ginkgo.It("binds read approval to the exact nonempty prompt session", ginkgo.Label("integration", "race-contract"), ginkgo.SpecTimeout(time.Second), func(ctx ginkgo.SpecContext) {
	type readResult struct {
		decision readapproval.Decision
		err      error
	}
	newSession := func(sessionID string) *promptExecutionSession {
		root := ginkgo.GinkgoT().TempDir()
		model := &Model{
			cfg:       config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
			workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		}
		return newPromptExecutionSession(workspace.WithExecutionSessionID(ctx, sessionID), model, make(chan tea.Msg, 8), true)
	}
	receivePending := func(events <-chan tea.Msg) *pendingApproval {
		for {
			var msg tea.Msg
			gomega.Eventually(ctx, events).Should(gomega.Receive(&msg))
			if pendingMsg, ok := msg.(approvalPendingMsg); ok {
				return pendingMsg.pending
			}
		}
	}

	sessionA := newSession("session-a")
	deciderA := newTuiReadApprovalDecider(sessionA)
	wrongDecision, wrongErr := deciderA.DecideRead(workspace.WithExecutionSessionID(ctx, "session-b"), readapproval.Request{
		SessionID: "session-b", Tool: workspace.ToolNameReadFile, Path: "wrong.txt",
	})
	gomega.Expect(wrongErr).To(gomega.MatchError(gomega.ContainSubstring("does not match the prompt session")))
	gomega.Expect(wrongDecision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
	gomega.Expect(sessionA.events).To(gomega.HaveLen(0), "wrong-session read must fail before reaching the UI")

	emptySession := newSession("")
	emptyDecision, emptyErr := newTuiReadApprovalDecider(emptySession).DecideRead(emptySession.promptExecutionContext, readapproval.Request{
		Tool: workspace.ToolNameReadFile, Path: "empty.txt",
	})
	gomega.Expect(emptyErr).To(gomega.HaveOccurred())
	gomega.Expect(emptyDecision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
	gomega.Expect(emptySession.events).To(gomega.HaveLen(0), "explicit-empty identity must fail before reaching the UI")

	sessionB := newSession("session-b")
	deciderB := newTuiReadApprovalDecider(sessionB)
	resultA := make(chan readResult, 1)
	resultB := make(chan readResult, 1)
	go func() {
		decision, err := deciderA.DecideRead(sessionA.promptExecutionContext, readapproval.Request{
			SessionID: "session-a", Tool: workspace.ToolNameReadFile, Path: "a.txt",
		})
		resultA <- readResult{decision: decision, err: err}
	}()
	go func() {
		decision, err := deciderB.DecideRead(sessionB.promptExecutionContext, readapproval.Request{
			SessionID: "session-b", Tool: workspace.ToolNameReadFile, Path: "b.txt",
		})
		resultB <- readResult{decision: decision, err: err}
	}()
	pendingA := receivePending(sessionA.events)
	pendingB := receivePending(sessionB.events)
	pendingA.response <- approvalResponse{allow: true}
	var approved readResult
	gomega.Eventually(ctx, resultA).Should(gomega.Receive(&approved))
	gomega.Expect(approved.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(approved.decision.Outcome).To(gomega.Equal(readapproval.OutcomeApproved))
	gomega.Expect(resultB).To(gomega.HaveLen(0), "session A approval must not release session B")
	pendingB.response <- approvalResponse{}
	var denied readResult
	gomega.Eventually(ctx, resultB).Should(gomega.Receive(&denied))
	gomega.Expect(denied.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(denied.decision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
})

var _ = ginkgo.It("cancels a pending read approval without accepting a late decision", ginkgo.Label("integration", "race-contract"), ginkgo.SpecTimeout(time.Second), func(ctx ginkgo.SpecContext) {
	root := ginkgo.GinkgoT().TempDir()
	model := &Model{
		cfg:       config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	decisionCtx, cancel := context.WithCancel(workspace.WithExecutionSessionID(ctx, "session-cancel"))
	session := newPromptExecutionSession(decisionCtx, model, make(chan tea.Msg, 8), true)
	decider := newTuiReadApprovalDecider(session)
	type readResult struct {
		decision readapproval.Decision
		err      error
	}
	done := make(chan readResult, 1)
	go func() {
		decision, err := decider.DecideRead(decisionCtx, readapproval.Request{
			SessionID: "session-cancel", Tool: workspace.ToolNameReadFile, Path: "cancel.txt",
		})
		done <- readResult{decision: decision, err: err}
	}()
	var pending *pendingApproval
	for pending == nil {
		var msg tea.Msg
		gomega.Eventually(ctx, session.events).Should(gomega.Receive(&msg))
		if pendingMsg, ok := msg.(approvalPendingMsg); ok {
			pending = pendingMsg.pending
		}
	}
	cancel()
	pending.response <- approvalResponse{allow: true}
	var result readResult
	gomega.Eventually(ctx, done).Should(gomega.Receive(&result))
	gomega.Expect(result.err).To(gomega.MatchError(context.Canceled))
	gomega.Expect(result.decision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
})

var _ = ginkgo.It("new read approval gate returns nil for nil model", func() {
	{
		got := (*promptExecutionSession)(nil).newReadApprovalGate()
		gomega.Expect(got).To(gomega.BeNil(), fmt.Sprintf("expected nil gate for nil model, got %+v", got))
	}
})

var _ = ginkgo.It("new read approval gate wires decider and cache", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	gate := session.newReadApprovalGate()
	gomega.Expect(gate).NotTo(gomega.BeNil(), fmt.Sprint("expected non-nil gate for non-nil model"))
	gomega.Expect(gate.Decider).NotTo(gomega.BeNil(), fmt.Sprint("expected gate to wire a Decider"))
	gomega.Expect(gate.Cache).NotTo(gomega.BeNil(), fmt.Sprint("expected gate to wire a Cache when workspace is available"))
})
