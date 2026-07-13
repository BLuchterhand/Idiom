package app

import (
	"context"
	"fmt"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/workflows/planexecute"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("slash approve invalid args do not resolve plan approval", func() {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	quit := m.slashApprove("/approve nope", []string{"/approve", "nope"})
	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("slashApprove should not quit"))

	gomega.Expect(m.status).To(gomega.Equal("Usage: /approve [all]"), fmt.Sprintf("status = %q, want usage", m.status))
	gomega.Expect(m.currentPlanApproval()).To(gomega.Equal(pending), fmt.Sprint("invalid approve args should leave pending plan approval unresolved"))

	gomega.Expect(pending.response).NotTo(gomega.Receive(), "invalid approve args must not resolve approval")
})

var _ = ginkgo.It("slash approve all without plan approval does not resolve generic approval", func() {
	m := &Model{}
	pending := &pendingApproval{
		request:  workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: "date"},
		response: make(chan approvalResponse, 1),
	}
	m.approvalPending = pending

	quit := m.slashApprove("/approve all", []string{"/approve", "all"})
	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("slashApprove should not quit"))

	gomega.Expect(m.status).To(gomega.Equal("No pending plan approval."), fmt.Sprintf("status = %q, want no pending plan approval", m.status))
	gomega.Expect(m.currentApproval()).To(gomega.Equal(pending), fmt.Sprint("/approve all should leave generic approval unresolved"))

	gomega.Expect(pending.response).NotTo(gomega.Receive(), "/approve all must not resolve generic approval")
})

var _ = ginkgo.It("slash approve all resolves system search approval as contents", func() {
	m := &Model{}
	pending := &pendingApproval{
		request:  workspace.ToolRequest{Tool: workspace.ToolNameSystemSearchAccess, Path: "../idiom"},
		mode:     systemSearchApprovalModeName,
		response: make(chan approvalResponse, 1),
	}
	m.approvalPending = pending

	quit := m.slashApprove("/approve all", []string{"/approve", "all"})
	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("slashApprove should not quit"))

	gomega.Expect(m.status).To(gomega.Equal("Approved folder contents: request_system_search_access: ../idiom"), fmt.Sprintf("status = %q", m.status))
	gomega.Expect(m.currentApproval()).To(gomega.BeNil(), fmt.Sprint("/approve all should resolve system-search approval"))

	var decision approvalResponse
	gomega.Expect(pending.response).To(gomega.Receive(&decision), "approval response was not delivered")
	gomega.Expect(decision.allow).To(gomega.BeTrue())
	gomega.Expect(decision.scope).To(gomega.Equal(workspace.SystemSearchAccessScopeContents))
})

var _ = ginkgo.It("direct plan approval consumes begin implementation input", func() {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"src/pages/products.astro"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	gomega.Expect(m.directPlanExecuteApprovalShouldConsumeOperatorInput("please begin on your implementation")).To(gomega.BeTrue(), fmt.Sprint("begin implementation input should consume pending plan approval"))

	answer, err := m.runDirectPlanExecuteApprovalAnswerOnce("please begin on your implementation")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runDirectPlanExecuteApprovalAnswerOnce: %v", err))
	gomega.Expect(answer).To(gomega.Not(gomega.Equal("")), fmt.Sprintf("approval should resolve, answer=%q pending=%#v", answer, m.currentPlanApproval()))
	gomega.Expect(m.currentPlanApproval()).To(gomega.BeNil(), fmt.Sprintf("approval should resolve, answer=%q pending=%#v", answer, m.currentPlanApproval()))

	var decision planApprovalResponse
	gomega.Expect(pending.response).To(gomega.Receive(&decision), "approval response was not delivered")
	gomega.Expect(decision.allow).To(gomega.BeTrue())
	gomega.Expect(decision.blanket).To(gomega.BeFalse())
})

var _ = ginkgo.It("plan execute approval decision from operator input", func() {
	tests := []struct {
		name        string
		input       string
		wantAllow   bool
		wantBlanket bool
		wantOK      bool
	}{
		{
			name:      "approve current",
			input:     "approve planned edits",
			wantAllow: true,
			wantOK:    true,
		},
		{
			name:      "begin implementation",
			input:     "please begin on your implementation",
			wantAllow: true,
			wantOK:    true,
		},
		{
			name:        "approve all",
			input:       "approve all edits",
			wantAllow:   true,
			wantBlanket: true,
			wantOK:      true,
		},
		{
			name:   "deny",
			input:  "deny this plan",
			wantOK: true,
		},
		{
			name:   "negated positive",
			input:  "do not start yet",
			wantOK: true,
		},
		{
			name:   "contracted negated positive",
			input:  "don't proceed with implementation",
			wantOK: true,
		},
		{
			name:  "unrelated chat",
			input: "what files are in the plan?",
		},
	}

	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			allow, blanket, ok := planExecuteApprovalDecisionFromOperatorInput(tt.input)
			gomega.Expect(allow).To(gomega.Equal(tt.wantAllow), fmt.Sprintf("decision = allow:%t blanket:%t ok:%t, want allow:%t blanket:%t ok:%t", allow, blanket, ok, tt.wantAllow, tt.wantBlanket, tt.wantOK))
			gomega.Expect(blanket).To(gomega.Equal(tt.wantBlanket), fmt.Sprintf("decision = allow:%t blanket:%t ok:%t, want allow:%t blanket:%t ok:%t", allow, blanket, ok, tt.wantAllow, tt.wantBlanket, tt.wantOK))
			gomega.Expect(ok).To(gomega.Equal(tt.wantOK), fmt.Sprintf("decision = allow:%t blanket:%t ok:%t, want allow:%t blanket:%t ok:%t", allow, blanket, ok, tt.wantAllow, tt.wantBlanket, tt.wantOK))
		})
	}
})

var _ = ginkgo.It("direct plan approval consumes approve all input", func() {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go", "b.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	answer, err := m.runDirectPlanExecuteApprovalAnswerOnce("approve all edits")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runDirectPlanExecuteApprovalAnswerOnce: %v", err))
	gomega.Expect(m.planExecuteApproveAllEdits).To(gomega.BeTrue(), fmt.Sprintf("blanket approval should be recorded, answer=%q blanket=%t", answer, m.planExecuteApproveAllEdits))
	gomega.Expect(answer).To(gomega.Not(gomega.Equal("")), fmt.Sprintf("blanket approval should be recorded, answer=%q blanket=%t", answer, m.planExecuteApproveAllEdits))

	var decision planApprovalResponse
	gomega.Expect(pending.response).To(gomega.Receive(&decision), "approval response was not delivered")
	gomega.Expect(decision.allow).To(gomega.BeTrue())
	gomega.Expect(decision.blanket).To(gomega.BeTrue())
})

var _ = ginkgo.It("accepted feature authority approves only the exact initial authorized-new plan paths", func() {
	request := planexecute.PlanApprovalRequest{
		Phase: planexecute.PlanApprovalPhaseInitial,
		Paths: []string{"internal/new/feature.go", "internal/new/feature_test.go"},
	}
	decision, covered := planExecuteAuthorizedNewFileApproval(request, []string{
		"./internal/new/feature.go", "internal/new/feature_test.go",
	})
	gomega.Expect(covered).To(gomega.BeTrue())
	gomega.Expect(decision.Approved).To(gomega.BeTrue())
	gomega.Expect(decision.ApprovedPaths).To(gomega.Equal(request.Paths))

	request.Paths = append(request.Paths, "internal/outside.go")
	_, covered = planExecuteAuthorizedNewFileApproval(request, []string{"internal/new/feature.go", "internal/new/feature_test.go"})
	gomega.Expect(covered).To(gomega.BeFalse(), "one unrelated path must send the complete plan through ordinary approval")

	request.Paths = request.Paths[:2]
	request.Phase = planexecute.PlanApprovalPhaseReviewRepair
	_, covered = planExecuteAuthorizedNewFileApproval(request, request.Paths)
	gomega.Expect(covered).To(gomega.BeFalse(), "review-repair remains a distinct approval decision")
})

var _ = ginkgo.It("canceling plan execute approval clears pending ownership and keeps blanket approval", func() {
	m := &Model{promptRuntimeState: promptRuntimeState{
		promptRuntimeCompletionState: promptRuntimeCompletionState{planExecuteApproveAllEdits: true},
	}, events: make(chan tea.Msg, 1)}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	m.startPromptExecution(func(ctx context.Context, _ *promptExecutionSession) (string, error) {
		<-ctx.Done()

		return "", ctx.Err()
	})
	m.cancelActivePromptExecution()
	gomega.Expect(m.currentPlanApproval()).To(gomega.BeNil(), fmt.Sprint("canceling the active prompt should clear pending approval ownership"))
	gomega.Expect(m.planExecuteApproveAllEdits).To(gomega.BeTrue(), fmt.Sprint("canceling the active prompt should preserve blanket approval"))
})
