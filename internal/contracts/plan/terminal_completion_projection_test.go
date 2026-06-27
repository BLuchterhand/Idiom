package plan

import (
	"testing"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
)

func TestPlanExecuteTerminalWorkflowProjectionDoesNotMaskInvalidTuple(t *testing.T) {
	workflowID, terminalState, dispatchable := planExecuteTerminalWorkflowProjection(PlanExecuteTerminalCompletionRecord{
		Phase:         PlanExecuteProgressPhasePlanning,
		Status:        PlanExecuteTerminalStatusCompleted,
		TerminalClass: PlanExecuteTerminalClassSuccess,
		Outcome:       PlanExecuteTerminalOutcomeCompleted,
	})

	if workflowID != workflowstate.WorkflowPlanExecutePlanner ||
		terminalState != workflowstate.TerminalStateComplete ||
		dispatchable {

		t.Fatalf("projection was masked to workflow=%q terminal=%q dispatchable=%v", workflowID, terminalState, dispatchable)
	}

	if err := workflowstate.ValidateTerminalProjection(workflowID, terminalState); err == nil {
		t.Fatalf("expected planner/complete terminal projection to remain invalid")
	}
}

func TestPlanExecuteTerminalWorkflowProjectionDoesNotDeriveDirtyScopeFromPlannerProse(t *testing.T) {
	workflowID, terminalState, dispatchable := planExecuteTerminalWorkflowProjection(PlanExecuteTerminalCompletionRecord{
		Phase:         PlanExecuteProgressPhasePlanning,
		Status:        PlanExecuteTerminalStatusFailed,
		TerminalClass: PlanExecuteTerminalClassFailed,
		FailureKind:   PlanExecuteFailurePlannerBlocked,
		FailureReason: "workspace has dirty files",
	})

	if workflowID != workflowstate.WorkflowPlanExecutePlanner ||
		terminalState != workflowstate.TerminalStateBlocked ||
		dispatchable {

		t.Fatalf("projection = (%q, %q, %v), want (%q, %q, false)", workflowID, terminalState, dispatchable, workflowstate.WorkflowPlanExecutePlanner, workflowstate.TerminalStateBlocked)
	}

	if err := workflowstate.ValidateTerminalProjection(workflowID, terminalState); err != nil {
		t.Fatalf("planner blocked terminal projection should validate: %v", err)
	}
}

func TestPlanExecuteTerminalVocabularyAliasesStayCentralized(t *testing.T) {
	tests := []struct {
		name          string
		record        PlanExecuteTerminalCompletionRecord
		wantClass     PlanExecuteTerminalClass
		wantOutcome   PlanExecuteTerminalOutcome
		wantWorkflow  workflowstate.WorkflowID
		wantTerminal  workflowstate.TerminalState
		wantKnown     []string
		wantTerminals []string
	}{
		{
			name: "completed status",
			record: PlanExecuteTerminalCompletionRecord{
				Phase:  PlanExecuteProgressPhaseComplete,
				Status: PlanExecuteTerminalStatusCompleted,
			},
			wantClass:     PlanExecuteTerminalClassSuccess,
			wantOutcome:   PlanExecuteTerminalOutcomeCompleted,
			wantWorkflow:  workflowstate.WorkflowPlanExecuteExecutor,
			wantTerminal:  workflowstate.TerminalStateComplete,
			wantKnown:     []string{string(PlanExecuteTerminalStatusCompleted), planExecuteTerminalStatusComplete},
			wantTerminals: []string{string(PlanExecuteTerminalStatusCompleted), planExecuteTerminalStatusSuccess, planExecuteTerminalStatusSucceeded},
		},
		{
			name: "source satisfied alias",
			record: PlanExecuteTerminalCompletionRecord{
				Phase:  PlanExecuteProgressPhaseComplete,
				Status: PlanExecuteTerminalStatusSourceSatisfied,
			},
			wantClass:     PlanExecuteTerminalClassSuccess,
			wantOutcome:   PlanExecuteTerminalOutcomeSourceSatisfied,
			wantWorkflow:  workflowstate.WorkflowPlanExecutePlanner,
			wantTerminal:  workflowstate.TerminalStateSourceSatisfied,
			wantKnown:     []string{string(PlanExecuteTerminalStatusSourceSatisfied)},
			wantTerminals: []string{string(PlanExecuteTerminalStatusSourceSatisfied)},
		},
		{
			name: "failure alias",
			record: PlanExecuteTerminalCompletionRecord{
				Phase:  PlanExecuteProgressPhaseComplete,
				Status: planExecuteTerminalStatusFailure,
			},
			wantClass:     PlanExecuteTerminalClassFailed,
			wantOutcome:   PlanExecuteTerminalOutcomeFailed,
			wantWorkflow:  workflowstate.WorkflowPlanExecuteExecutor,
			wantTerminal:  workflowstate.TerminalStateBlocked,
			wantKnown:     []string{string(PlanExecuteTerminalStatusFailed), planExecuteTerminalStatusFailure},
			wantTerminals: []string{string(PlanExecuteTerminalStatusFailed), planExecuteTerminalStatusFailure},
		},
		{
			name: "failed review overrides completed status",
			record: PlanExecuteTerminalCompletionRecord{
				Phase:        PlanExecuteProgressPhaseComplete,
				Status:       PlanExecuteTerminalStatusCompleted,
				ReviewStatus: PlanExecuteReviewStatusFailed,
			},
			wantClass:     PlanExecuteTerminalClassFailed,
			wantOutcome:   PlanExecuteTerminalOutcomeFailed,
			wantWorkflow:  workflowstate.WorkflowPlanExecuteExecutor,
			wantTerminal:  workflowstate.TerminalStateBlocked,
			wantKnown:     []string{string(PlanExecuteTerminalStatusCompleted)},
			wantTerminals: []string{string(PlanExecuteTerminalStatusCompleted)},
		},
		{
			name: "running blocks",
			record: PlanExecuteTerminalCompletionRecord{
				Phase:  PlanExecuteProgressPhase(planExecuteTerminalStatusRunning),
				Status: PlanExecuteTerminalStatus(planExecuteTerminalStatusRunning),
			},
			wantClass:    PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome:  PlanExecuteTerminalOutcomeBlocked,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantTerminal: workflowstate.TerminalStateBlocked,
			wantKnown:    []string{planExecuteTerminalStatusRunning},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotClass, gotOutcome := ClassifyPlanExecuteTerminalCompletion(tc.record)
			if gotClass != tc.wantClass || gotOutcome != tc.wantOutcome {
				t.Fatalf("classification = (%q, %q), want (%q, %q)", gotClass, gotOutcome, tc.wantClass, tc.wantOutcome)
			}

			gotWorkflow, gotTerminal, dispatchable := planExecuteTerminalWorkflowProjection(tc.record)
			if gotWorkflow != tc.wantWorkflow || gotTerminal != tc.wantTerminal || dispatchable {
				t.Fatalf("projection = (%q, %q, %v), want (%q, %q, false)", gotWorkflow, gotTerminal, dispatchable, tc.wantWorkflow, tc.wantTerminal)
			}

			for _, status := range tc.wantKnown {
				if !knownPlanExecuteTerminalStatus(status) {
					t.Fatalf("knownPlanExecuteTerminalStatus rejected %q", status)
				}
			}

			for _, status := range tc.wantTerminals {
				if !PlanExecuteTerminalStatusIsTerminal(status) {
					t.Fatalf("PlanExecuteTerminalStatusIsTerminal rejected %q", status)
				}
			}
		})
	}
}

func TestPlanExecuteTerminalDecisionRejectsFailedReviewWithCompletedStatus(t *testing.T) {
	decision := DecidePlanExecuteTerminalCompletion(PlanExecuteTerminalCompletionRecord{
		Phase:        PlanExecuteProgressPhaseComplete,
		Status:       PlanExecuteTerminalStatusCompleted,
		ReviewStatus: PlanExecuteReviewStatusFailed,
	}, nil)

	if decision.Class != PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("decision class = %q, want blocked", decision.Class)
	}
	if decision.Reason == "" {
		t.Fatal("blocked failed-review decision should explain why")
	}
}

func TestPlanExecuteTerminalNextActionUsesRecoveryContractVocabulary(t *testing.T) {
	for _, action := range recoverycontract.NextActions() {
		if !knownPlanExecuteTerminalNextAction(action) {
			t.Fatalf("plan terminal validation rejected recovery next action %q", action)
		}
	}

	if knownPlanExecuteTerminalNextAction(recoverycontract.NextAction("retry-plan-typo")) {
		t.Fatal("plan terminal validation accepted unknown recovery next action")
	}
}

func TestPhaseEvidenceKindVocabularyCoversTerminalProducers(t *testing.T) {
	for _, kind := range []PhaseEvidenceKind{
		PhaseEvidenceKindSourceSanity,
		PhaseEvidenceKindVerification,
		PhaseEvidenceKindPatchHygiene,
		PhaseEvidenceKindSubmittedEdit,
	} {
		parsed, ok := ParsePhaseEvidenceKind(string(kind))
		if !ok || parsed != kind {
			t.Fatalf("ParsePhaseEvidenceKind(%q) = %q %v", kind, parsed, ok)
		}
		if !KnownPhaseEvidenceKind(kind) {
			t.Fatalf("KnownPhaseEvidenceKind rejected %q", kind)
		}
	}

	if parsed, ok := ParsePhaseEvidenceKind("git_diff"); ok {
		t.Fatalf("ParsePhaseEvidenceKind accepted unknown kind %q", parsed)
	}
}

func TestPhaseEvidenceStatusVocabularyCoversTerminalProducers(t *testing.T) {
	for _, status := range []PhaseEvidenceStatus{
		PhaseEvidenceStatusOK,
		PhaseEvidenceStatusPassed,
		PhaseEvidenceStatusFailed,
		PhaseEvidenceStatusFailure,
		PhaseEvidenceStatusError,
		PhaseEvidenceStatusMissing,
		PhaseEvidenceStatusSkipped,
		PhaseEvidenceStatusDegraded,
		PhaseEvidenceStatusBlocked,
		PhaseEvidenceStatusUnavailable,
		PhaseEvidenceStatusUnknown,
		PhaseEvidenceStatusApplied,
		PhaseEvidenceStatusPending,
	} {
		parsed, ok := ParsePhaseEvidenceStatus(string(status))
		if !ok || parsed != status {
			t.Fatalf("ParsePhaseEvidenceStatus(%q) = %q %v", status, parsed, ok)
		}
		if !KnownPhaseEvidenceStatus(status) {
			t.Fatalf("KnownPhaseEvidenceStatus rejected %q", status)
		}
	}

	if parsed, ok := ParsePhaseEvidenceStatus("ready"); ok {
		t.Fatalf("ParsePhaseEvidenceStatus accepted unknown status %q", parsed)
	}
}

func TestRequiredPathCoverageStatusVocabulary(t *testing.T) {
	for _, status := range []PlanExecuteRequiredPathCoverageStatus{
		PlanExecuteRequiredPathCoverageMissing,
		PlanExecuteRequiredPathCoverageChanged,
		PlanExecuteRequiredPathCoverageSourceSatisfied,
	} {
		parsed, ok := ParsePlanExecuteRequiredPathCoverageStatus(string(status))
		if !ok || parsed != status {
			t.Fatalf("ParsePlanExecuteRequiredPathCoverageStatus(%q) = %q %v", status, parsed, ok)
		}
		if !KnownPlanExecuteRequiredPathCoverageStatus(status) {
			t.Fatalf("KnownPlanExecuteRequiredPathCoverageStatus rejected %q", status)
		}
	}

	if parsed, ok := ParsePlanExecuteRequiredPathCoverageStatus("covered"); ok {
		t.Fatalf("ParsePlanExecuteRequiredPathCoverageStatus accepted unknown status %q", parsed)
	}
}
