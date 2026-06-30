package app

import (
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/workflows/planexecute"
)

func TestSlashApproveInvalidArgsDoNotResolvePlanApproval(t *testing.T) {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	quit := m.slashApprove("/approve nope", []string{"/approve", "nope"})
	if quit {
		t.Fatal("slashApprove should not quit")
	}

	if m.status != "Usage: /approve [all]" {
		t.Fatalf("status = %q, want usage", m.status)
	}
	if m.currentPlanApproval() != pending {
		t.Fatal("invalid approve args should leave pending plan approval unresolved")
	}

	select {
	case decision := <-pending.response:
		t.Fatalf("invalid approve args resolved approval: %#v", decision)
	default:
	}
}

func TestSlashApproveAllWithoutPlanApprovalDoesNotResolveGenericApproval(t *testing.T) {
	m := &Model{}
	pending := &pendingApproval{
		request:  workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: "date"},
		response: make(chan approvalResponse, 1),
	}
	m.approvalPending = pending

	quit := m.slashApprove("/approve all", []string{"/approve", "all"})
	if quit {
		t.Fatal("slashApprove should not quit")
	}

	if m.status != "No pending plan approval." {
		t.Fatalf("status = %q, want no pending plan approval", m.status)
	}
	if m.currentApproval() != pending {
		t.Fatal("/approve all should leave generic approval unresolved")
	}

	select {
	case decision := <-pending.response:
		t.Fatalf("/approve all resolved generic approval: %#v", decision)
	default:
	}
}

func TestSlashApproveAllResolvesSystemSearchApprovalAsContents(t *testing.T) {
	m := &Model{}
	pending := &pendingApproval{
		request:  workspace.ToolRequest{Tool: workspace.ToolNameSystemSearchAccess, Path: "../idiom"},
		mode:     systemSearchApprovalModeName,
		response: make(chan approvalResponse, 1),
	}
	m.approvalPending = pending

	quit := m.slashApprove("/approve all", []string{"/approve", "all"})
	if quit {
		t.Fatal("slashApprove should not quit")
	}

	if m.status != "Approved folder contents: request_system_search_access: ../idiom" {
		t.Fatalf("status = %q", m.status)
	}
	if m.currentApproval() != nil {
		t.Fatal("/approve all should resolve system-search approval")
	}

	select {
	case decision := <-pending.response:
		if !decision.allow || decision.scope != workspace.SystemSearchAccessScopeContents {
			t.Fatalf("decision = %#v, want contents approval", decision)
		}
	default:
		t.Fatal("approval response was not delivered")
	}
}

func TestDirectPlanApprovalConsumesBeginImplementationInput(t *testing.T) {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"src/pages/products.astro"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	if !m.directPlanExecuteApprovalShouldConsumeOperatorInput("please begin on your implementation") {
		t.Fatal("begin implementation input should consume pending plan approval")
	}

	answer, err := m.runDirectPlanExecuteApprovalAnswerOnce("please begin on your implementation")
	if err != nil {
		t.Fatalf("runDirectPlanExecuteApprovalAnswerOnce: %v", err)
	}
	if answer == "" || m.currentPlanApproval() != nil {
		t.Fatalf("approval should resolve, answer=%q pending=%#v", answer, m.currentPlanApproval())
	}

	select {
	case decision := <-pending.response:
		if !decision.allow || decision.blanket {
			t.Fatalf("decision = %#v, want current approval", decision)
		}
	default:
		t.Fatal("approval response was not delivered")
	}
}

func TestPlanExecuteApprovalDecisionFromOperatorInput(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			allow, blanket, ok := planExecuteApprovalDecisionFromOperatorInput(tt.input)
			if allow != tt.wantAllow || blanket != tt.wantBlanket || ok != tt.wantOK {
				t.Fatalf("decision = allow:%t blanket:%t ok:%t, want allow:%t blanket:%t ok:%t", allow, blanket, ok, tt.wantAllow, tt.wantBlanket, tt.wantOK)
			}
		})
	}
}

func TestDirectPlanApprovalConsumesApproveAllInput(t *testing.T) {
	m := &Model{}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go", "b.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	answer, err := m.runDirectPlanExecuteApprovalAnswerOnce("approve all edits")
	if err != nil {
		t.Fatalf("runDirectPlanExecuteApprovalAnswerOnce: %v", err)
	}
	if !m.planExecuteApproveAllEdits || answer == "" {
		t.Fatalf("blanket approval should be recorded, answer=%q blanket=%t", answer, m.planExecuteApproveAllEdits)
	}

	select {
	case decision := <-pending.response:
		if !decision.allow || !decision.blanket {
			t.Fatalf("decision = %#v, want blanket approval", decision)
		}
	default:
		t.Fatal("approval response was not delivered")
	}
}

func TestPlanExecuteApprovalSessionKeepsBlanketApproval(t *testing.T) {
	m := &Model{planExecuteApproveAllEdits: true}
	pending := &pendingPlanApproval{
		request:  planexecute.PlanApprovalRequest{Paths: []string{"a.go"}},
		response: make(chan planApprovalResponse, 1),
	}
	m.planApprovalPending = pending

	m.beginPlanExecuteApprovalSession()
	if m.currentPlanApproval() != nil {
		t.Fatal("begin session should clear stale pending approval")
	}
	if !m.planExecuteApproveAllEdits {
		t.Fatal("begin session should preserve blanket approval")
	}

	m.planApprovalPending = pending
	m.endPlanExecuteApprovalSession()
	if m.currentPlanApproval() != nil {
		t.Fatal("end session should clear pending approval")
	}
	if !m.planExecuteApproveAllEdits {
		t.Fatal("end session should preserve blanket approval")
	}
}
