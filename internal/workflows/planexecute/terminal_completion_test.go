package planexecute

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"idiom/internal/workflows/recovery"
)

func TestPlanExecuteTerminalCompletionRecordClassifiesProgressStateBranches(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		state       ProgressState
		input       PlanExecuteTerminalCompletionInput
		wantClass   PlanExecuteTerminalClass
		wantOutcome PlanExecuteTerminalOutcome
	}{
		{
			name: "completed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = ProgressPhaseComplete
				state.Status = ProgressStatusCompleted
			}),
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeCompleted,
		},
		{
			name: "source satisfied explicit input",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = ProgressPhaseComplete
				state.Status = ProgressStatusCompleted
			}),
			input: PlanExecuteTerminalCompletionInput{
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: []string{"pkg/feature.go", "pkg/feature.go", " "},
				CompletedAt:                  now,
			},
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeSourceSatisfied,
		},
		{
			name: "review skipped by design",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkSkippedReview()
			}),
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeReviewSkippedByDesign,
		},
		{
			name: "review degraded",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: "degraded (review unavailable)"}, nil)
			}),
			wantClass:   PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome: PlanExecuteTerminalOutcomeReviewDegraded,
		},
		{
			name: "blocked",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: "blocked: missing approval"}, nil)
			}),
			wantClass:   PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome: PlanExecuteTerminalOutcomeBlocked,
		},
		{
			name: "failed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed("planner_failed", "planner model unavailable")
			}),
			wantClass:   PlanExecuteTerminalClassFailed,
			wantOutcome: PlanExecuteTerminalOutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := NewPlanExecuteTerminalCompletionRecord(tt.state, tt.input)
			if record.TerminalClass != tt.wantClass || record.Outcome != tt.wantOutcome {
				t.Fatalf("terminal classification = class %q outcome %q, want class %q outcome %q; record=%#v", record.TerminalClass, record.Outcome, tt.wantClass, tt.wantOutcome, record)
			}

			if record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion || record.Type != PlanExecuteTerminalCompletionRecordType {
				t.Fatalf("record schema/type missing: %#v", record)
			}
		})
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordDefaultsAndTrims(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		SessionID:                    " session-1 ",
		Status:                       " complete ",
		SourceSatisfiedEvidencePaths: []string{" a.go ", "", "a.go", "../escape.go"},
		ChangedPaths:                 []string{` b.go `, `b.go`, `pkg\feature.go`, "pkg/./bad.go"},
	})

	if record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion || record.Type != PlanExecuteTerminalCompletionRecordType {
		t.Fatalf("normalizer should fill schema/type, got %#v", record)
	}

	if record.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want trimmed session-1", record.SessionID)
	}

	if record.TerminalClass != PlanExecuteTerminalClassSuccess || record.Outcome != PlanExecuteTerminalOutcomeCompleted {
		t.Fatalf("classification = class %q outcome %q, want completed/completed", record.TerminalClass, record.Outcome)
	}

	if len(record.SourceSatisfiedEvidencePaths) != 1 || record.SourceSatisfiedEvidencePaths[0] != "a.go" {
		t.Fatalf("source evidence paths not normalized: %#v", record.SourceSatisfiedEvidencePaths)
	}

	if len(record.ChangedPaths) != 2 || record.ChangedPaths[0] != "b.go" || record.ChangedPaths[1] != "pkg/feature.go" {
		t.Fatalf("changed paths not normalized: %#v", record.ChangedPaths)
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordReclassifiesContradictions(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		TerminalClass: PlanExecuteTerminalClassSuccess,
		Outcome:       PlanExecuteTerminalOutcomeCompleted,
		Status:        ProgressStatusCompleted,
		FailureKind:   "review_model_error",
		FailureReason: "review model returned invalid JSON",
	})

	if record.TerminalClass != PlanExecuteTerminalClassFailed || record.Outcome != PlanExecuteTerminalOutcomeFailed {
		t.Fatalf("classification = class %q outcome %q, want failed/failed", record.TerminalClass, record.Outcome)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromFieldsNormalizesStatePath(t *testing.T) {
	record := PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"state_path":            ".idiom/plan-execute/sessions/field-test.json",
		"status":                "completed",
		"review_status":         "skipped",
		"review_repair_skipped": true,
		"changed_paths":         []any{"pkg/feature.go", "pkg/feature.go"},
	})

	if record.ProgressPath != ".idiom/plan-execute/sessions/field-test.json" {
		t.Fatalf("ProgressPath = %q, want state_path fallback", record.ProgressPath)
	}

	if record.TerminalClass != PlanExecuteTerminalClassSuccess || record.Outcome != PlanExecuteTerminalOutcomeReviewSkippedByDesign {
		t.Fatalf("classification = class %q outcome %q, want skipped-by-design success", record.TerminalClass, record.Outcome)
	}

	if len(record.ChangedPaths) != 1 || record.ChangedPaths[0] != "pkg/feature.go" {
		t.Fatalf("ChangedPaths = %#v, want deduped path", record.ChangedPaths)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromFieldsPreservesParentAuthority(t *testing.T) {
	record := PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"contract_id":   "parent-contract",
		"snapshot_hash": "parent-snapshot",
		"contract_hash": "legacy-snapshot",
	})

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
		state.Status = ProgressStatusRunning
		state.Phase = ProgressPhaseExecutor
	})
	if _, ok := PlanExecuteTerminalCompletionRecordFromProgressState(running, PlanExecuteTerminalCompletionInput{}); ok {
		t.Fatalf("running progress must not synthesize terminal completion")
	}

	completed := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = ProgressStatusCompleted
		state.Phase = ProgressPhaseComplete
	})
	record, ok := PlanExecuteTerminalCompletionRecordFromProgressState(completed, PlanExecuteTerminalCompletionInput{ProgressPath: "progress.json"})

	if !ok {
		t.Fatalf("completed progress should synthesize terminal completion")
	}

	if record.ProgressPath != "progress.json" || record.TerminalClass != PlanExecuteTerminalClassSuccess {
		t.Fatalf("record = %#v, want terminal success with progress path", record)
	}
}

func TestTerminalCompletionRecordIncludesMechanicalEvidence(t *testing.T) {
	state := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.MarkSkippedReview()

		state.RequiredEditPaths = []string{"pkg/feature.go"}
		state.ScopeContractID = "scope-123"
		state.ScopeContractHash = "scope-123"
		state.Executor.SourceSanity = []PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "ok",
		}}
		state.Executor.VerificationCommands = []VerificationCommand{{
			Command: "go test ./pkg",
			Argv:    []string{"go", "test", "./pkg"},
			Source:  VerificationCommandSourceInferred,
		}}
		state.Executor.Verification = []PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "go test ./pkg",
			Status:  "ok",
		}}
		state.Executor.VerificationSkips = []VerificationSkip{{Path: "README.md", Reason: VerificationSkipNonGoPath}}
		state.Executor.VerificationRejects = []VerificationReject{{Command: "go test ./pkg; rm -rf /", Reason: VerificationRejectUnsupportedCmd}}
	})

	record := NewPlanExecuteTerminalCompletionRecord(state, PlanExecuteTerminalCompletionInput{})

	if record.ScopeContractID != "scope-123" || record.ScopeContractHash != "scope-123" {
		t.Fatalf("scope contract metadata missing: %#v", record)
	}

	if len(record.SourceSanity) != 1 || len(record.Verification) != 1 || len(record.VerificationCommands) != 1 {
		t.Fatalf("mechanical evidence missing: source=%#v commands=%#v verification=%#v", record.SourceSanity, record.VerificationCommands, record.Verification)
	}

	if len(record.VerificationSkips) != 1 || len(record.VerificationRejects) != 1 {
		t.Fatalf("verification skips/rejects missing: skips=%#v rejects=%#v", record.VerificationSkips, record.VerificationRejects)
	}

	if len(record.RequiredPathCoverage) != 1 || record.RequiredPathCoverage[0].Status != "changed" {
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
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		Status:              ProgressStatusCompleted,
		Phase:               ProgressPhaseComplete,
		ReviewStatus:        ProgressStatusSkipped,
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		Verification:        []PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}},
		ReviewRepairSkipped: true,
	})

	if planExecuteTerminalStringSliceContains(record.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go") {
		t.Fatalf("legacy no-scope completion should use changed paths as runtime envelope, got %#v", record.ValidationFailures)
	}

	scoped := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		Status:              ProgressStatusCompleted,
		Phase:               ProgressPhaseComplete,
		ReviewStatus:        ProgressStatusSkipped,
		RequiredEditPaths:   []string{"pkg/expected.go"},
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		ReviewRepairSkipped: true,
	})

	if !planExecuteTerminalStringSliceContains(scoped.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go") {
		t.Fatalf("declared scope should still reject out-of-envelope changed path, got %#v", scoped.ValidationFailures)
	}
}

func TestFinalizeTerminalCompletionRejectsStatusOnlySuccess(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("status_only_success", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/status_only_success.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.FailureKind != RunFailureTerminalCompletionContractFailed {
		t.Fatalf("FailureKind = %q, want %q", result.FailureKind, RunFailureTerminalCompletionContractFailed)
	}

	if result.Completion == nil || result.Completion.TerminalClass != PlanExecuteTerminalClassFailed {
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
		FailureKind:       RunFailurePlannerBlocked,
		FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n- pkg/feature.go",
		RequiredEditPaths: []string{"pkg/feature.go"},
	}, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("write stale recovery record: %v", err)
	}

	state := NewProgressState(sessionID, "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Executor.SourceSanity = []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}
	state.Executor.Verification = []PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}}
	state.Review.Status = ProgressStatusSkipped

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

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = ProgressStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.FailureKind != RunFailureTerminalCompletionContractFailed {
		t.Fatalf("FailureKind = %q, want %q", result.FailureKind, RunFailureTerminalCompletionContractFailed)
	}

	if result.Completion == nil || result.Completion.TerminalClass != PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not downgraded to failed: %#v", result.Completion)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "source_sanity_missing:pkg/feature.go") {
		t.Fatalf("validation failures missing source_sanity_missing: %#v", result.Completion.ValidationFailures)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("downgraded terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionRejectsMissingCppSourceSanity(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("missing_cpp_source_sanity", "prompt", "", []string{"native/src/machine.cpp"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"native/src/machine.cpp"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = ProgressStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_cpp_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure", result.ExitCode)
	}

	if result.Completion == nil || result.Completion.TerminalClass != PlanExecuteTerminalClassFailed {
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

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1", "Req-1", "adr-1"}
	state.Review.Status = ProgressStatusSkipped
	state.Executor.SourceSanity = []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

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

func TestFinalizeTerminalCompletionRejectsMissingRequiredAcceptanceIDs(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("required_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.Review.Status = ProgressStatusSkipped
	state.Executor.SourceSanity = []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want terminal contract failure; completion=%#v", result.ExitCode, result.Completion)
	}

	if result.Completion == nil {
		t.Fatalf("completion missing")
	}

	if result.Completion.TerminalClass != PlanExecuteTerminalClassFailed {
		t.Fatalf("TerminalClass = %q, want failed", result.Completion.TerminalClass)
	}

	if !planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "acceptance_evidence_missing") {
		t.Fatalf("validation failures missing acceptance_evidence_missing: %#v", result.Completion.ValidationFailures)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("RequiredAcceptanceEvidenceIDs alone must not populate AcceptanceEvidenceIDs: got %#v", result.Completion.AcceptanceEvidenceIDs)
	}

	loaded, _, err := LoadProgressState(root, state.SessionID)
	if err != nil {
		t.Fatalf("LoadProgressState: %v", err)
	}

	if len(loaded.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("downgraded progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionDoesNotMapAcceptanceIDsWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("required_acceptance_without_evidence", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_without_evidence.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})

	if result.Completion != nil && len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("status-only completion should not map acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}
}

func TestFinalizeTerminalCompletionClearsAcceptanceIDsOnDirectFailure(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("direct_failure_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = ProgressPhaseComplete
	state.Status = ProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = ProgressStatusSkipped
	state.Executor.SourceSanity = []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/direct_failure_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 2, FailureKind: "executor_failed", FailureReason: "command failed"})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want direct failure exit", result.ExitCode)
	}

	if result.Completion == nil || result.Completion.TerminalClass != PlanExecuteTerminalClassFailed {
		t.Fatalf("completion was not failed: %#v", result.Completion)
	}

	if len(result.Completion.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("failed terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs)
	}

	loaded, _, err := LoadProgressState(root, state.SessionID)
	if err != nil {
		t.Fatalf("LoadProgressState: %v", err)
	}

	if len(loaded.AcceptanceEvidenceIDs) > 0 {
		t.Fatalf("failed progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs)
	}

	if loaded.Status != ProgressStatusFailed {
		t.Fatalf("failed progress state status = %q, want failed", loaded.Status)
	}

	if loaded.FailureKind != "executor_failed" {
		t.Fatalf("failed progress state failure kind = %q, want executor_failed", loaded.FailureKind)
	}
}

func TestTerminalCompletionRejectsDegradedCppSourceSanity(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		Status:              ProgressStatusCompleted,
		Phase:               ProgressPhaseComplete,
		ReviewStatus:        ProgressStatusSkipped,
		ChangedPaths:        []string{"native/src/machine.cpp"},
		SourceSanity:        []PhaseEvidenceCheck{{Kind: "source_sanity", Path: "native/src/machine.cpp", Status: "ok", Summary: "Source sanity passed for 1 file(s) (1 generic fallback)"}},
		ReviewRepairSkipped: true,
	})

	if !planExecuteTerminalStringSliceContains(record.ValidationFailures, "source_sanity_degraded:native/src/machine.cpp") {
		t.Fatalf("degraded C++ source_sanity should reject terminal success, got %#v", record.ValidationFailures)
	}
}

func TestRecoveryInvariantFailuresIncludeValidationFailurePaths(t *testing.T) {
	failures := planExecuteRecoveryInvariantFailures(PlanExecuteTerminalCompletionRecord{
		FailureInvariants: []PlanExecuteTerminalInvariant{{
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
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		Status:       ProgressStatusDegraded,
		ReviewStatus: "degraded (review unavailable)",
		FailureKind:  "review_repair_degraded",
	})

	record = NormalizePlanExecuteTerminalCompletionRecord(record)

	if record.TerminalClass != PlanExecuteTerminalClassRecoveryRequired || record.Outcome != PlanExecuteTerminalOutcomeReviewDegraded {
		t.Fatalf("degraded terminal record was not idempotent: %#v", record)
	}
}

func TestSavePlanExecuteTerminalCompletionRecordRoundTrips(t *testing.T) {
	root := t.TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Status = ProgressStatusBlocked
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

	if loaded.TerminalClass != PlanExecuteTerminalClassRecoveryRequired || loaded.Outcome != PlanExecuteTerminalOutcomeBlocked {
		t.Fatalf("loaded classification = class %q outcome %q", loaded.TerminalClass, loaded.Outcome)
	}

	if loaded.FailureID != "failure-123" || loaded.PacketPath == "" || loaded.NextAction != "review_decomposition" {
		t.Fatalf("loaded recovery metadata incomplete: %#v", loaded)
	}

	if len(loaded.WriteAllowedPaths) != 1 || loaded.WriteAllowedPaths[0] != "logs/telemetry/run.jsonl" {
		t.Fatalf("write allowed paths not normalized: %#v", loaded.WriteAllowedPaths)
	}
}

func progressStateForTerminalCompletionTest(mutator func(*ProgressState)) ProgressState {
	state := NewProgressState("terminal_completion_test", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.ChangedPaths = []string{"pkg/feature.go"}
	mutator(&state)

	return state
}
