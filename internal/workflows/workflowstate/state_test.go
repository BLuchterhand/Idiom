package workflowstate

import "testing"

func TestClassifyPlanExecuteTerminalStates(t *testing.T) {
	tests := []struct {
		name         string
		input        PlanExecuteTerminalInput
		wantWorkflow WorkflowID
		wantState    TerminalState
	}{
		{
			name:         "complete executor",
			input:        PlanExecuteTerminalInput{Phase: "complete", Status: "completed", TerminalClass: "success", Outcome: "completed"},
			wantWorkflow: WorkflowPlanExecuteExecutor,
			wantState:    TerminalStateComplete,
		},
		{
			name:         "complete executor with explicit no next action",
			input:        PlanExecuteTerminalInput{Phase: "complete", Status: "completed", TerminalClass: "success", Outcome: "completed", NextAction: "none"},
			wantWorkflow: WorkflowPlanExecuteExecutor,
			wantState:    TerminalStateComplete,
		},
		{
			name:         "source satisfied planner",
			input:        PlanExecuteTerminalInput{Phase: "planning", Status: "source-satisfied", TerminalClass: "success", Outcome: "source_satisfied"},
			wantWorkflow: WorkflowPlanExecutePlanner,
			wantState:    TerminalStateSourceSatisfied,
		},
		{
			name:         "planner blocked",
			input:        PlanExecuteTerminalInput{Phase: "planning", Status: "blocked", TerminalClass: "recovery_required", Outcome: "blocked", FailureKind: "planner_blocked", NextAction: "recover status"},
			wantWorkflow: WorkflowPlanExecutePlanner,
			wantState:    TerminalStateRecoveryOpen,
		},
		{
			name:         "stale anchor",
			input:        PlanExecuteTerminalInput{Phase: "planning", Status: "failed", TerminalClass: "failed", FailureKind: "planner_blocked", FailureReason: "old_text not found in current source"},
			wantWorkflow: WorkflowPlanExecutePlanner,
			wantState:    TerminalStateStaleNeedsRebase,
		},
		{
			name:         "missing scope",
			input:        PlanExecuteTerminalInput{Phase: "executor", Status: "failed", TerminalClass: "failed", FailureKind: "missing_edit_path_without_new_file_authorization"},
			wantWorkflow: WorkflowPlanExecutePlanner,
			wantState:    TerminalStateMissingScope,
		},
		{
			name:         "verification failure",
			input:        PlanExecuteTerminalInput{Phase: "executor", Status: "failed", TerminalClass: "failed", FailureKind: "verification_failed"},
			wantWorkflow: WorkflowPlanExecuteExecutor,
			wantState:    TerminalStateVerificationFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPlanExecuteTerminal(tt.input)
			if got.WorkflowID != tt.wantWorkflow || got.TerminalState != tt.wantState {
				t.Fatalf("classification = (%q, %q), want (%q, %q): %#v", got.WorkflowID, got.TerminalState, tt.wantWorkflow, tt.wantState, got)
			}
			if got.Dispatchable {
				t.Fatalf("terminal plan-execute states must not be dispatchable: %#v", got)
			}
			if !got.Terminal {
				t.Fatalf("terminal plan-execute classification should be terminal: %#v", got)
			}
		})
	}
}

func TestClassifyPlanExecuteTerminalHardFailureOutranksSourceSatisfied(t *testing.T) {
	got := ClassifyPlanExecuteTerminal(PlanExecuteTerminalInput{
		Phase:       "planning",
		Status:      "source-satisfied",
		Outcome:     "source_satisfied",
		ExitCode:    1,
		FailureKind: "terminal_completion_contract_failed",
	})

	if got.TerminalState == TerminalStateSourceSatisfied || got.Dispatchable {
		t.Fatalf("hard failure must not classify as source-satisfied/dispatchable: %#v", got)
	}
	if got.TerminalState != TerminalStateBlocked {
		t.Fatalf("TerminalState = %q, want blocked: %#v", got.TerminalState, got)
	}
}

func TestClassifyPlanExecuteTerminalTypedFieldsOverrideLegacyStrings(t *testing.T) {
	got := ClassifyPlanExecuteTerminal(PlanExecuteTerminalInput{
		Phase:          "executor",
		Status:         "failed",
		TerminalClass:  "failed",
		FailureKind:    "executor_failed",
		FailureKindID:  PlanExecuteFailureMissingEditPathWithoutNewFileAuth,
		ReviewStatus:   "passed",
		ReviewStatusID: ReviewStatusBlocked,
		NextAction:     "none",
		NextActionID:   NextActionReviewDecomposition,
	})

	if got.WorkflowID != WorkflowPlanExecutePlanner || got.TerminalState != TerminalStateMissingScope {
		t.Fatalf("typed failure kind should own workflow/state over legacy strings, got %#v", got)
	}
}
