package planexecute

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

func TestPlanExecuteTerminalCompletionRecordClassifiesProgressStateBranches(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		state        ProgressState
		input        PlanExecuteTerminalCompletionInput
		wantClass    plancontract.PlanExecuteTerminalClass
		wantOutcome  plancontract.PlanExecuteTerminalOutcome
		wantWorkflow workflowstate.WorkflowID
		wantState    workflowstate.TerminalState
	}{
		{
			name: "completed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = plancontract.PlanExecuteProgressPhaseComplete
				state.Status = plancontract.PlanExecuteProgressStatusCompleted
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeCompleted,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateComplete,
		},
		{
			name: "source satisfied explicit input",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = plancontract.PlanExecuteProgressPhaseComplete
				state.Status = plancontract.PlanExecuteProgressStatusCompleted
			}),
			input: PlanExecuteTerminalCompletionInput{
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: []string{"pkg/feature.go", "pkg/feature.go", " "},
				CompletedAt:                  now,
			},
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateSourceSatisfied,
		},
		{
			name: "review skipped by design",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkSkippedReview()
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeReviewSkippedByDesign,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateComplete,
		},
		{
			name: "review degraded",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusDegraded, StatusDetail: "review unavailable"}, nil)
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeReviewDegraded,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateRecoveryOpen,
		},
		{
			name: "blocked",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusBlocked, StatusDetail: "missing approval"}, nil)
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeBlocked,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateRecoveryOpen,
		},
		{
			name: "failed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed("planner_failed", "planner model unavailable")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
		{
			name: "missing requested test edit stays planner owned",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed(string(plancontract.PlanExecuteFailureMissingRequestedTestEdit), "missing requested test edit")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
		{
			name: "missing requested implementation edit stays planner owned",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit), "missing requested implementation edit")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := NewPlanExecuteTerminalCompletionRecord(tt.state, tt.input)
			if record.TerminalClass != tt.wantClass || record.Outcome != tt.wantOutcome {
				t.Fatalf("terminal classification = class %q outcome %q, want class %q outcome %q; record=%#v", record.TerminalClass, record.Outcome, tt.wantClass, tt.wantOutcome, record)
			}

			if record.WorkflowID != tt.wantWorkflow || record.WorkflowTerminalState != tt.wantState || record.Dispatchable {
				t.Fatalf("workflow classification = workflow %q state %q dispatchable=%v, want workflow %q state %q dispatchable=false; record=%#v", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable, tt.wantWorkflow, tt.wantState, record)
			}

			if record.SchemaVersion != plancontract.PlanExecuteTerminalCompletionSchemaVersion || record.Type != plancontract.PlanExecuteTerminalCompletionRecordType {
				t.Fatalf("record schema/type missing: %#v", record)
			}
		})
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordDefaultsAndTrims(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SessionID:                    " session-1 ",
		Status:                       " complete ",
		SourceSatisfiedEvidencePaths: []string{" a.go ", "", "a.go", "../escape.go"},
		ChangedPaths:                 []string{` b.go `, `b.go`, `pkg\feature.go`, "pkg/./bad.go"},
	})

	if record.SchemaVersion != plancontract.PlanExecuteTerminalCompletionSchemaVersion || record.Type != plancontract.PlanExecuteTerminalCompletionRecordType {
		t.Fatalf("normalizer should fill schema/type, got %#v", record)
	}

	if record.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want trimmed session-1", record.SessionID)
	}

	if record.TerminalClass != plancontract.PlanExecuteTerminalClassSuccess || record.Outcome != plancontract.PlanExecuteTerminalOutcomeCompleted {
		t.Fatalf("classification = class %q outcome %q, want completed/completed", record.TerminalClass, record.Outcome)
	}

	if record.WorkflowID != workflowstate.WorkflowPlanExecuteExecutor || record.WorkflowTerminalState != workflowstate.TerminalStateComplete || record.Dispatchable {
		t.Fatalf("workflow classification = workflow %q state %q dispatchable=%v, want executor complete false", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable)
	}

	if len(record.SourceSatisfiedEvidencePaths) != 1 || record.SourceSatisfiedEvidencePaths[0] != "a.go" {
		t.Fatalf("source evidence paths not normalized: %#v", record.SourceSatisfiedEvidencePaths)
	}

	if len(record.ChangedPaths) != 2 || record.ChangedPaths[0] != "b.go" || record.ChangedPaths[1] != "pkg/feature.go" {
		t.Fatalf("changed paths not normalized: %#v", record.ChangedPaths)
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordReclassifiesContradictions(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		TerminalClass: plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeCompleted,
		Status:        plancontract.PlanExecuteTerminalStatusCompleted,
		FailureKind:   "review_model_error",
		FailureReason: "review model returned invalid JSON",
	})

	if record.TerminalClass != plancontract.PlanExecuteTerminalClassFailed || record.Outcome != plancontract.PlanExecuteTerminalOutcomeFailed {
		t.Fatalf("classification = class %q outcome %q, want failed/failed", record.TerminalClass, record.Outcome)
	}

	if record.WorkflowTerminalState != workflowstate.TerminalStateBlocked {
		t.Fatalf("workflow terminal state = %q, want blocked", record.WorkflowTerminalState)
	}
}

func TestPlanExecuteWorkflowClassificationUsesTypedFailureKindMapping(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Phase:         plancontract.PlanExecuteProgressPhaseExecutor,
		Status:        plancontract.PlanExecuteTerminalStatusFailed,
		TerminalClass: plancontract.PlanExecuteTerminalClassFailed,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeFailed,
		FailureKind:   plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth,
	})

	if record.WorkflowID != workflowstate.WorkflowPlanExecutePlanner ||
		record.WorkflowTerminalState != workflowstate.TerminalStateMissingScope {

		t.Fatalf("typed workflow classification = workflow %q state %q, want planner missing-scope: %#v", record.WorkflowID, record.WorkflowTerminalState, record)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromFieldsNormalizesStatePath(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"state_path":            ".idiom/plan-execute/sessions/field-test.json",
		"status":                "completed",
		"review_status":         "skipped",
		"review_repair_skipped": true,
		"changed_paths":         []any{"pkg/feature.go", "pkg/feature.go"},
	}))

	if record.ProgressPath != ".idiom/plan-execute/sessions/field-test.json" {
		t.Fatalf("ProgressPath = %q, want state_path fallback", record.ProgressPath)
	}

	if record.TerminalClass != plancontract.PlanExecuteTerminalClassSuccess || record.Outcome != plancontract.PlanExecuteTerminalOutcomeReviewSkippedByDesign {
		t.Fatalf("classification = class %q outcome %q, want skipped-by-design success", record.TerminalClass, record.Outcome)
	}

	if len(record.ChangedPaths) != 1 || record.ChangedPaths[0] != "pkg/feature.go" {
		t.Fatalf("ChangedPaths = %#v, want deduped path", record.ChangedPaths)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromFieldsPreservesParentAuthority(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"contract_id":   "parent-contract",
		"snapshot_hash": "parent-snapshot",
		"contract_hash": "legacy-snapshot",
	}))

	if record.ContractID != "parent-contract" {
		t.Fatalf("ContractID = %q, want parent-contract", record.ContractID)
	}

	if record.SnapshotHash != "parent-snapshot" {
		t.Fatalf("SnapshotHash = %q, want parent-snapshot", record.SnapshotHash)
	}

	if record.ContractHash != "legacy-snapshot" {
		t.Fatalf("ContractHash = %q, want legacy-snapshot diagnostic preserved", record.ContractHash)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromProgressStateRequiresTerminalState(t *testing.T) {
	running := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = plancontract.PlanExecuteProgressStatusRunning
		state.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	})
	if _, ok := PlanExecuteTerminalCompletionRecordFromProgressState(running, PlanExecuteTerminalCompletionInput{}); ok {
		t.Fatalf("running progress must not synthesize terminal completion")
	}

	completed := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = plancontract.PlanExecuteProgressStatusCompleted
		state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	})
	record, ok := PlanExecuteTerminalCompletionRecordFromProgressState(completed, PlanExecuteTerminalCompletionInput{ProgressPath: "progress.json"})

	if !ok {
		t.Fatalf("completed progress should synthesize terminal completion")
	}

	if record.ProgressPath != "progress.json" || record.TerminalClass != plancontract.PlanExecuteTerminalClassSuccess {
		t.Fatalf("record = %#v, want terminal success with progress path", record)
	}
}

func TestTerminalCompletionRecordIncludesMechanicalEvidence(t *testing.T) {
	state := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.MarkSkippedReview()

		state.RequiredEditPaths = []string{"pkg/feature.go"}
		state.ScopeContractID = "scope-123"
		state.ScopeContractHash = "scope-123"
		state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "ok",
		}}
		state.Executor.VerificationCommands = []plancontract.VerificationCommand{{
			Command: "go test ./pkg",
			Argv:    []string{"go", "test", "./pkg"},
			Source:  plancontract.VerificationCommandSourceInferred,
		}}
		state.Executor.Verification = []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "go test ./pkg",
			Status:  "ok",
		}}
		state.Executor.VerificationSkips = []plancontract.VerificationSkip{{Path: "README.md", Reason: plancontract.VerificationSkipNonGoPath}}
		state.Executor.VerificationRejects = []plancontract.VerificationReject{{Command: "go test ./pkg; rm -rf /", Reason: plancontract.VerificationRejectUnsupportedCmd}}
	})

	record := NewPlanExecuteTerminalCompletionRecord(state, PlanExecuteTerminalCompletionInput{})

	if record.ScopeContractID != "scope-123" || record.ScopeContractHash != "scope-123" {
		t.Fatalf("scope contract metadata missing: %#v", record)
	}

	if record.ProtocolHash != "scope-123" {
		t.Fatalf("protocol hash missing: %#v", record)
	}

	if len(record.SourceSanity) != 1 || len(record.Verification) != 1 || len(record.VerificationCommands) != 1 {
		t.Fatalf("mechanical evidence missing: source=%#v commands=%#v verification=%#v", record.SourceSanity, record.VerificationCommands, record.Verification)
	}

	if len(record.VerificationSkips) != 1 || len(record.VerificationRejects) != 1 {
		t.Fatalf("verification skips/rejects missing: skips=%#v rejects=%#v", record.VerificationSkips, record.VerificationRejects)
	}

	if len(record.RequiredPathCoverage) != 1 || record.RequiredPathCoverage[0].Status != plancontract.PlanExecuteRequiredPathCoverageChanged {
		t.Fatalf("required path coverage missing: %#v", record.RequiredPathCoverage)
	}

	if len(record.FailureInvariants) == 0 {
		t.Fatalf("failure invariants must be recorded")
	}

	if !planExecuteTerminalStringSliceContains(record.ValidationFailures, "verification_rejected:go test ./pkg; rm -rf /") {
		t.Fatalf("verification reject must prevent success, got %#v", record.ValidationFailures)
	}
}

func TestTerminalCompletionUsesChangedPathsAsLegacyEnvelopeOnlyWithoutDeclaredScope(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		Verification:        []plancontract.PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}},
		ReviewRepairSkipped: true,
	})

	if planExecuteTerminalStringSliceContains(record.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go") {
		t.Fatalf("legacy no-scope completion should use changed paths as runtime envelope, got %#v", record.ValidationFailures)
	}

	scoped := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		RequiredEditPaths:   []string{"pkg/expected.go"},
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		ReviewRepairSkipped: true,
	})

	if !planExecuteTerminalStringSliceContains(scoped.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go") {
		t.Fatalf("declared scope should still reject out-of-envelope changed path, got %#v", scoped.ValidationFailures)
	}
}

func TestFinalizeTerminalCompletionRejectsStatusOnlySuccess(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("status_only_success", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/status_only_success.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.FailureKind != string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed) {
		t.Fatalf("FailureKind = %q, want %q", result.FailureKind, string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed))
	}

	if result.Completion == nil || result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not downgraded to failed: %#v", result.Completion)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "changed_paths_missing") {
		t.Fatalf("validation failures missing changed_paths_missing: %#v", result.Completion.ValidationFailures)
	}
}

func TestFinalizeTerminalCompletionSuccessDoesNotCarryStaleRecoveryMetadata(t *testing.T) {
	root := t.TempDir()
	sessionID := "stale_recovery_success"
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		FailureKind:       string(plancontract.PlanExecuteFailurePlannerBlocked),
		FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n- pkg/feature.go",
		RequiredEditPaths: []string{"pkg/feature.go"},
	}, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("write stale recovery record: %v", err)
	}

	state := NewProgressState(sessionID, "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}
	state.Executor.Verification = []plancontract.PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/stale_recovery_success.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want success; failure=%s %s", result.ExitCode, result.FailureKind, result.FailureReason)
	}

	if result.Completion == nil {
		t.Fatal("expected terminal completion")
	}

	if result.Completion.NextAction != "" || result.Completion.FailureID != "" || result.Completion.RecordPath != "" || result.Completion.PacketPath != "" {
		t.Fatalf("success completion carried stale recovery metadata: %#v", result.Completion)
	}
}

func TestFinalizeTerminalCompletionRejectsMissingSourceSanityForSourceChange(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("missing_source_sanity", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.FailureKind != string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed) {
		t.Fatalf("FailureKind = %q, want %q", result.FailureKind, plancontract.PlanExecuteFailureTerminalCompletionContractFailed)
	}

	if result.Completion == nil || result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not downgraded to failed: %#v", result.Completion)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "source_sanity_missing:pkg/feature.go") {
		t.Fatalf("validation failures missing source_sanity_missing: %#v", result.Completion.ValidationFailures)
	}

	loaded, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "missing_source_sanity")
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err)
	}
	if !planExecuteTerminalStringSliceContains(loaded.ValidationFailures, "source_sanity_missing:pkg/feature.go") {
		t.Fatalf("saved terminal completion lost validation failure: %#v", loaded.ValidationFailures)
	}

	fields := plancontract.PlanExecuteTerminalCompletionEventFields(*result.Completion, result.CompletionPath)
	fieldFailures, ok := fields["validation_failures"].([]any)
	if !ok || !planExecuteTerminalAnySliceContains(fieldFailures, "source_sanity_missing:pkg/feature.go") {
		t.Fatalf("terminal completion event lost validation failure: %#v", fields["validation_failures"])
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("downgraded terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionRejectsMissingCppSourceSanity(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("missing_cpp_source_sanity", "prompt", "", []string{"native/src/machine.cpp"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"native/src/machine.cpp"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_cpp_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.Completion == nil || result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not downgraded to failed: %#v", result.Completion)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "source_sanity_missing:native/src/machine.cpp") {
		t.Fatalf("validation failures missing source_sanity_missing for C++ path: %#v", result.Completion.ValidationFailures)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("downgraded terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionCarriesAcceptanceEvidenceIDs(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1", "Req-1", "adr-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want success; completion=%#v", result.ExitCode, result.Completion)
	}

	if result.Completion == nil {
		t.Fatalf("completion missing")
	}

	want := []string{"adr-1", "req-1"}
	if !planExecuteTerminalStringSlicesEqual(result.Completion.AcceptanceEvidenceIDs, want) {
		t.Fatalf("AcceptanceEvidenceIDs = %#v, want %#v", result.Completion.AcceptanceEvidenceIDs, want)
	}

	if !planExecuteTerminalStringSlicesEqual(result.Completion.RequiredAcceptanceEvidenceIDs, want) {
		t.Fatalf("RequiredAcceptanceEvidenceIDs = %#v, want %#v", result.Completion.RequiredAcceptanceEvidenceIDs, want)
	}
}

func TestTerminalCompletionCarriesAcceptanceEvidenceClaimsIntoValidation(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SessionID:         "acceptance_claims",
		Phase:             plancontract.PlanExecuteProgressPhaseComplete,
		Status:            plancontract.PlanExecuteTerminalStatusCompleted,
		ReviewStatus:      plancontract.PlanExecuteReviewStatusSkipped,
		ExitCode:          0,
		RequiredEditPaths: []string{"pkg/feature.go"},
		WriteAllowedPaths: []string{"pkg/feature.go"},
		ChangedPaths:      []string{"pkg/feature.go"},
		SourceSanity:      []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		RequiredAcceptanceEvidence: []plancontract.AcceptanceEvidenceRequired{{
			ID:        "coverage-threshold",
			Required:  true,
			Threshold: ">=80%",
			Source:    "test",
		}},
		AcceptanceEvidenceClaims: []plancontract.AcceptanceEvidenceClaim{{
			ID:    "coverage-threshold",
			Value: "72%",
			Met:   true,
		}},
	})

	if !planExecuteTerminalStringSliceContains(record.RequiredAcceptanceEvidenceIDs, "coverage-threshold") {
		t.Fatalf("required threshold obligation ID was not mirrored into required IDs: %#v", record.RequiredAcceptanceEvidenceIDs)
	}

	if len(record.AcceptanceEvidenceClaims) != 1 || record.AcceptanceEvidenceClaims[0].ID != "coverage-threshold" {
		t.Fatalf("acceptance evidence claims not normalized on record: %#v", record.AcceptanceEvidenceClaims)
	}

	if !planExecuteTerminalStringSliceContains(record.ValidationFailures, "acceptance_evidence_claim_met_mismatch") ||
		!planExecuteTerminalStringSliceContains(record.ValidationFailures, "acceptance_evidence_threshold_not_met") {
		t.Fatalf("terminal validation did not evaluate threshold claim: %#v", record.ValidationFailures)
	}

	fields := plancontract.PlanExecuteTerminalCompletionEventFields(record, ".idiom/plan-execute/terminal/acceptance_claims.json")
	if _, ok := fields["acceptance_evidence_claims"]; !ok {
		t.Fatalf("terminal event fields omitted acceptance evidence claims: %#v", fields)
	}
}

func TestFinalizeTerminalCompletionRejectsMissingRequiredAcceptanceIDs(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("required_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure; completion=%#v", result.ExitCode, result.Completion)
	}

	if result.Completion == nil {
		t.Fatalf("completion missing")
	}

	if result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed {
		t.Fatalf("TerminalClass = %q, want failed", result.Completion.TerminalClass)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "acceptance_evidence_missing") {
		t.Fatalf("validation failures missing acceptance_evidence_missing: %#v", result.Completion.ValidationFailures)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("RequiredAcceptanceEvidenceIDs alone must not populate AcceptanceEvidenceIDs: got %#v", result.Completion.AcceptanceEvidenceIDs)
	}

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("LoadProgressState: %v", err)
	}

	if len(loaded.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("downgraded progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionDoesNotMapAcceptanceIDsWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("required_acceptance_without_evidence", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_without_evidence.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.Completion != nil && len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("status-only completion should not map acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionClearsAcceptanceIDsOnDirectFailure(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("direct_failure_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/direct_failure_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 2, FailureKind: "executor_failed", FailureReason: "command failed"})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want direct failure exit", result.ExitCode)
	}

	if result.Completion == nil || result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not failed: %#v", result.Completion)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("failed terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("LoadProgressState: %v", err)
	}

	if len(loaded.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("failed progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs)
	}

	if loaded.Status != plancontract.PlanExecuteProgressStatusFailed {
		t.Fatalf("failed progress state status = %q, want failed", loaded.Status)
	}

	if loaded.FailureKind != "executor_failed" {
		t.Fatalf("failed progress state failure kind = %q, want executor_failed", loaded.FailureKind)
	}
}

func TestTerminalCompletionRejectsDegradedCppSourceSanity(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		ChangedPaths:        []string{"native/src/machine.cpp"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "native/src/machine.cpp", Status: "ok", Summary: "Source sanity passed for 1 file(s) (1 generic fallback)"}},
		ReviewRepairSkipped: true,
	})

	if !planExecuteTerminalStringSliceContains(record.ValidationFailures, "source_sanity_degraded:native/src/machine.cpp") {
		t.Fatalf("degraded C++ source_sanity should reject terminal success, got %#v", record.ValidationFailures)
	}
}

func TestRecoveryInvariantFailuresIncludeValidationFailurePaths(t *testing.T) {
	failures := planExecuteRecoveryInvariantFailures(plancontract.PlanExecuteTerminalCompletionRecord{
		FailureInvariants: []plancontract.PlanExecuteTerminalInvariant{{
			Code:   "required_path_covered:pkg/feature.go",
			Status: "failed",
			Reason: "missing",
		}},
		ValidationFailures: []string{
			"changed_path_outside_write_envelope:pkg/feature.go",
			"verification_failed:go test ./pkg",
			"source_sanity_failed:(unknown)",
		},
	})

	if len(failures) != 4 {
		t.Fatalf("failures=%#v", failures)
	}

	if failures[0].Path != "pkg/feature.go" || failures[1].Path != "pkg/feature.go" {
		t.Fatalf("expected path-qualified invariant failures, got %#v", failures)
	}

	if failures[2].Path != "" || failures[3].Path != "" {
		t.Fatalf("non-path invariant suffixes must not become recovery paths: %#v", failures)
	}
}

func planExecuteTerminalStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func planExecuteTerminalAnySliceContains(values []any, target string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == target {
			return true
		}
	}

	return false
}

func planExecuteTerminalStringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func TestNormalizePlanExecuteTerminalCompletionRecordKeepsDegradedOutcomeIdempotent(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:       plancontract.PlanExecuteTerminalStatusDegraded,
		ReviewStatus: plancontract.PlanExecuteReviewStatusDegraded,
		FailureKind:  plancontract.PlanExecuteFailureReviewRepairDegraded,
	})

	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)

	if record.TerminalClass != plancontract.PlanExecuteTerminalClassRecoveryRequired || record.Outcome != plancontract.PlanExecuteTerminalOutcomeReviewDegraded {
		t.Fatalf("degraded terminal record was not idempotent: %#v", record)
	}
}

func TestSavePlanExecuteTerminalCompletionRecordRoundTrips(t *testing.T) {
	root := t.TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
			state.Status = plancontract.PlanExecuteProgressStatusBlocked
			state.FailureKind = "review_repair_blocked"
			state.FailureReason = "review repair remained blocked"
			state.SetWriteAllowedPaths([]string{"logs/telemetry/run.jsonl", "logs/telemetry/run.jsonl"})
		}),
		PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/terminal_completion_test.json",
			ExitCode:     1,
			FailureID:    "failure-123",
			RecordPath:   ".idiom/recovery/failures/failure-123.json",
			PacketPath:   ".idiom/recovery/packets/failure-123.json",
			NextAction:   "review_decomposition",
		},
	)

	path, err := SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	if want := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", "terminal_completion_test.terminal.json")); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	loaded, loadedPath, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "terminal_completion_test")
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err)
	}

	if loadedPath != path {
		t.Fatalf("loaded path = %q, want %q", loadedPath, path)
	}

	if loaded.TerminalClass != plancontract.PlanExecuteTerminalClassRecoveryRequired || loaded.Outcome != plancontract.PlanExecuteTerminalOutcomeBlocked {
		t.Fatalf("loaded classification = class %q outcome %q", loaded.TerminalClass, loaded.Outcome)
	}

	if loaded.FailureID != "failure-123" || loaded.PacketPath == "" || loaded.NextAction != "review_decomposition" {
		t.Fatalf("loaded recovery metadata incomplete: %#v", loaded)
	}

	if len(loaded.WriteAllowedPaths) != 1 || loaded.WriteAllowedPaths[0] != "logs/telemetry/run.jsonl" {
		t.Fatalf("write allowed paths not normalized: %#v", loaded.WriteAllowedPaths)
	}
}

func TestLoadPlanExecuteTerminalCompletionRejectsInvalidLifecycleFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*plancontract.PlanExecuteTerminalCompletionRecord)
		wantErr string
	}{
		{
			name: "status",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.Status = "typo"
			},
			wantErr: "invalid plan-execute terminal completion status",
		},
		{
			name: "phase status mismatch",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.Phase = plancontract.PlanExecuteProgressPhasePlanning
				record.Status = plancontract.PlanExecuteTerminalStatusCompleted
			},
			wantErr: "invalid plan-execute terminal completion lifecycle",
		},
		{
			name: "failure kind",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.FailureKind = "review_repair_typo"
			},
			wantErr: "invalid plan-execute terminal completion failure kind",
		},
		{
			name: "next action",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.NextAction = "retry_typo"
			},
			wantErr: "invalid plan-execute terminal completion next action",
		},
		{
			name: "evidence status",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "mystery"}}
			},
			wantErr: "invalid plan-execute terminal completion evidence status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			record := NewPlanExecuteTerminalCompletionRecord(
				progressStateForTerminalCompletionTest(func(state *ProgressState) {
					state.Phase = plancontract.PlanExecuteProgressPhaseComplete
					state.Status = plancontract.PlanExecuteProgressStatusCompleted
					state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
				}),
				PlanExecuteTerminalCompletionInput{},
			)
			tt.mutate(&record)
			writeTerminalCompletionRecordFixture(t, root, record)

			_, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, record.SessionID)
			if err == nil || found {
				t.Fatalf("expected invalid terminal completion error and found=false, found=%v err=%v", found, err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSavePlanExecuteTerminalCompletionRecordRejectsInvalidLifecycle(t *testing.T) {
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Phase = plancontract.PlanExecuteProgressPhasePlanning
			state.Status = plancontract.PlanExecuteProgressStatusFailed
		}),
		PlanExecuteTerminalCompletionInput{},
	)
	record.Status = "typo"

	if _, err := SavePlanExecuteTerminalCompletionRecord(t.TempDir(), record); err == nil || !strings.Contains(err.Error(), "invalid plan-execute terminal completion status") {
		t.Fatalf("expected invalid terminal completion error, got %v", err)
	}
}

func TestSavePlanExecuteTerminalCompletionRecordDoesNotFollowPredictableTempSymlink(t *testing.T) {
	root := t.TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.SessionID = "tmp_symlink_terminal"
			state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
			state.Status = plancontract.PlanExecuteProgressStatusBlocked
		}),
		PlanExecuteTerminalCompletionInput{ExitCode: 1},
	)
	fullPath, _, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	if err != nil {
		t.Fatalf("planExecuteTerminalCompletionPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir terminal dir: %v", err)
	}
	outside := filepath.Join(root, "outside-terminal.txt")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	if err := os.Symlink(outside, fullPath+".tmp"); err != nil {
		t.Fatalf("symlink predictable temp: %v", err)
	}

	if _, err := SavePlanExecuteTerminalCompletionRecord(root, record); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}
	body, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside: %v", err)
	}
	if string(body) != "sentinel" {
		t.Fatalf("predictable temp symlink target was modified: %q", string(body))
	}
	if _, err := os.Lstat(fullPath + ".tmp"); err != nil {
		t.Fatalf("predictable temp symlink should remain untouched: %v", err)
	}
}

func TestLoadPlanExecuteTerminalCompletionRejectsSessionMismatch(t *testing.T) {
	root := t.TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {}),
		PlanExecuteTerminalCompletionInput{},
	)
	record.SessionID = "other-session"

	fullPath, _, err := planExecuteTerminalCompletionPath(root, "terminal_completion_test")
	if err != nil {
		t.Fatalf("planExecuteTerminalCompletionPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "terminal_completion_test")
	if err == nil || found {
		t.Fatalf("expected session mismatch error and found=false, found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "does not match requested session") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeTerminalCompletionRecordFixture(t *testing.T, root string, record plancontract.PlanExecuteTerminalCompletionRecord) {
	t.Helper()

	fullPath, _, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	if err != nil {
		t.Fatalf("planExecuteTerminalCompletionPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func progressStateForTerminalCompletionTest(mutator func(*ProgressState)) ProgressState {
	state := NewProgressState("terminal_completion_test", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.ChangedPaths = []string{"pkg/feature.go"}
	mutator(&state)

	return state
}
