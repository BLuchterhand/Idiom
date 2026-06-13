package recovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildPlanExecuteFailureRecordClassifiesPlannerBlocked(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-1",
		ProgressStatePath: ".idiom/plan-execute/sessions/session-1.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason:     "<<QUESTION>>: docs need a human answer",
		RequiredEditPaths: []string{"docs/design.md"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.FailureID != "plan-execute-session-1" {
		t.Fatalf("failure id=%q", record.FailureID)
	}

	if record.Failure.Class != FailureClassPlannerBlocked {
		t.Fatalf("failure class=%q", record.Failure.Class)
	}

	if record.NextAction != NextActionAnswerBlocker {
		t.Fatalf("next action=%q", record.NextAction)
	}

	if len(record.Failure.EvidenceRefs) == 0 || record.Failure.EvidenceRefs[0].Kind != "progress_state" {
		t.Fatalf("expected progress state evidence, got %#v", record.Failure.EvidenceRefs)
	}
}

func TestBuildPlanExecuteFailureRecordRetriesRequiredSubmitPlanSurfaceMismatch(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "atom-1-run-1",
		ProgressStatePath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason: "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.\n\n" +
			"Blocker:\n- Tool not available in current runtime contract: submit_plan is not in the current allowed tool surface. Available tools: read_file, code_navigate.",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}, time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.Failure.Class != FailureClassPlannerBlocked {
		t.Fatalf("failure class=%q", record.Failure.Class)
	}

	if record.NextAction != NextActionRetryPlanExecute {
		t.Fatalf("next action=%q, want %q", record.NextAction, NextActionRetryPlanExecute)
	}
}

func TestBuildPlanExecuteFailureRecordClassifiesAnchorRefreshAsRetry(t *testing.T) {
	reason := strings.Join([]string{
		"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
		"",
		"Remaining bounded refresh paths:",
		"- native/src/core/colony_sim_tick.cpp",
		"Next valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path.",
	}, "\n")
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-anchor-refresh",
		ProgressStatePath: ".idiom/plan-execute/sessions/session-anchor-refresh.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason:     reason,
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
		},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.Failure.Class != FailureClassPlannerBlocked {
		t.Fatalf("failure class=%q", record.Failure.Class)
	}

	if record.NextAction != NextActionRetryPlanExecute {
		t.Fatalf("next action=%q, want %q", record.NextAction, NextActionRetryPlanExecute)
	}

	refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", reason, []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
	})
	if !ok {
		t.Fatal("expected typed source-refresh result")
	}

	if refresh.ActionClass != NextActionRetryPlanExecute || refresh.Reason != "anchor_refresh" || refresh.Path != "native/src/core/colony_sim_tick.cpp" {
		t.Fatalf("unexpected refresh result: %+v", refresh)
	}

	if len(refresh.Paths) != 1 || refresh.Paths[0] != "native/src/core/colony_sim_tick.cpp" {
		t.Fatalf("refresh paths = %#v", refresh.Paths)
	}
}

func TestBuildPlanExecuteFailureRecordClassifiesLiveBoundedAnchorRefreshAsRetry(t *testing.T) {
	reason := strings.Join([]string{
		"<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.",
		"",
		"Blocker:",
		"- Bounded submit_plan anchor refresh required: the previous `submit_plan` was rejected before it became a valid plan artifact, and the corrected plan still needs bounded source-body refresh for: native/src/core/colony_sim_tick.cpp.",
		"Do not call `submit_plan` until the remaining anchor refresh returns.",
	}, "\n")

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-live-anchor-refresh",
		ProgressStatePath: ".idiom/plan-execute/sessions/session-live-anchor-refresh.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason:     reason,
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}, time.Date(2026, 6, 13, 9, 35, 32, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.NextAction != NextActionRetryPlanExecute {
		t.Fatalf("next action=%q, want %q", record.NextAction, NextActionRetryPlanExecute)
	}
	refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", reason, record.SourceRun.RequiredEditPaths)
	if !ok {
		t.Fatal("expected live bounded anchor refresh text to be retryable")
	}
	if refresh.Reason != "anchor_refresh" || refresh.Path != "native/src/core/colony_sim_tick.cpp" {
		t.Fatalf("unexpected refresh result: %+v", refresh)
	}
}

func TestBuildPlanExecuteFailureRecordClassifiesTruncatedAnchorRefreshAsRetry(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-anchor-refresh-truncated",
		ProgressStatePath: ".idiom/plan-execute/sessions/session-anchor-refresh-truncated.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
		},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.NextAction != NextActionRetryPlanExecute {
		t.Fatalf("next action=%q, want %q", record.NextAction, NextActionRetryPlanExecute)
	}

	refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", record.Failure.Summary, record.SourceRun.RequiredEditPaths)
	if !ok {
		t.Fatal("expected truncated internal anchor-refresh marker to remain machine-actionable")
	}

	if refresh.Path != "native/src/core/colony_sim_tick.cpp" || refresh.Reason != "anchor_refresh" {
		t.Fatalf("unexpected refresh result: %+v", refresh)
	}

	if !stringSlicesEqual(refresh.Paths, []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
	}) {
		t.Fatalf("refresh paths = %#v", refresh.Paths)
	}
}

func TestPlanExecutePlannerBlockedRetryRefreshUsesTypedNextActionFallback(t *testing.T) {
	refresh, ok := PlanExecutePlannerBlockedRetryRefreshResult(
		"planner_blocked",
		"<<QUESTION>>: source refresh needed",
		[]string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
		NextActionRetryPlanExecute,
	)
	if !ok {
		t.Fatal("expected typed retry action to produce refresh result")
	}

	if refresh.Path != "native/src/core/colony_sim_tick.cpp" || refresh.Reason != "retry_action" {
		t.Fatalf("unexpected refresh result: %+v", refresh)
	}

	got := PlanExecuteSourceRefreshRetryInstruction(refresh, []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"})
	if !strings.Contains(got, "First refresh source-body evidence for each candidate anchor path") ||
		!strings.Contains(got, "native/src/core/colony_sim_tick.cpp, native/src/simulation/world/collapse_event.h") ||
		!strings.Contains(got, "then satisfy runtime-required git history evidence") ||
		!strings.Contains(got, "then produce one valid `submit_plan`") {

		t.Fatalf("unexpected retry instruction:\n%s", got)
	}
}

func TestBuildPlanExecuteFailureRecordCarriesLockedContractAndInvariantFailures(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-contract-context",
		FailureKind:       "plan_artifact_invalid",
		FailureReason:     "submit_plan violated a contract invariant",
		RequiredEditPaths: []string{"pkg/feature.go"},
		AllowedEditPaths:  []string{"pkg/feature_test.go"},
		LockedContract: &ContractSnapshot{
			ContractID:           "contract_plan_execute_123",
			ContractHash:         "sha256:contract",
			SnapshotID:           "snapshot-123",
			SnapshotPath:         ".idiom/contracts/snapshot.json",
			RuntimeContractID:    "runtime-123",
			ProtocolHash:         "sha256:protocol",
			WriteAllowedPaths:    []string{"pkg/feature.go"},
			WriteAllowedExplicit: true,
		},
		InvariantFailures: []InvariantFailure{{
			InvariantID:            "contract.required_edit_coverage",
			FieldPath:              "submit_plan.edits[0].path",
			Path:                   "pkg/feature.go",
			Expected:               "path inside locked required_edit_paths",
			Actual:                 "path omitted from submitted edits",
			RequiredRepairEvidence: []string{"source-body evidence for pkg/feature.go", "raw_prompt: hidden"},
		}},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.LockedContract == nil {
		t.Fatalf("expected locked contract snapshot")
	}

	if record.LockedContract.ContractID != "contract_plan_execute_123" ||
		record.LockedContract.ContractHash != "sha256:contract" ||
		record.LockedContract.SnapshotID != "snapshot-123" ||
		record.LockedContract.SnapshotPath != ".idiom/contracts/snapshot.json" {

		t.Fatalf("locked contract not preserved: %#v", record.LockedContract)
	}

	if !stringSlicesEqual(record.LockedContract.RequiredEditPaths, []string{"pkg/feature.go"}) ||
		!stringSlicesEqual(record.LockedContract.AllowedEditPaths, []string{"pkg/feature_test.go"}) ||
		!stringSlicesEqual(record.LockedContract.WriteAllowedPaths, []string{"pkg/feature.go"}) ||
		!record.LockedContract.WriteAllowedExplicit {

		t.Fatalf("locked contract scope not populated from source run: %#v", record.LockedContract)
	}

	if len(record.Failure.InvariantFailures) != 1 {
		t.Fatalf("invariant failures=%#v", record.Failure.InvariantFailures)
	}

	failure := record.Failure.InvariantFailures[0]
	if failure.InvariantID != "contract.required_edit_coverage" ||
		failure.FieldPath != "submit_plan.edits[0].path" ||
		failure.Path != "pkg/feature.go" ||
		!strings.Contains(failure.RequiredRepairEvidence[1], "redacted") {

		t.Fatalf("invariant failure not normalized/redacted: %#v", failure)
	}
}

func TestWritePlanExecuteFailureRecordPreservesCreatedAt(t *testing.T) {
	root := t.TempDir()
	input := PlanExecuteFailureInput{
		SessionID:     "session-2",
		Phase:         "planning",
		Status:        "failed",
		FailureKind:   "plan_artifact_missing",
		FailureReason: "no plan artifact",
	}
	firstTime := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	first, path, err := WritePlanExecuteFailureRecord(root, input, firstTime)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	if path != filepath.ToSlash(filepath.Join(".idiom", "recovery", "plan-execute-session-2", "failure.json")) {
		t.Fatalf("record path=%q", path)
	}

	secondTime := firstTime.Add(time.Hour)

	second, _, err := WritePlanExecuteFailureRecord(root, input, secondTime)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if second.CreatedAt != first.CreatedAt {
		t.Fatalf("created_at changed from %q to %q", first.CreatedAt, second.CreatedAt)
	}

	if second.UpdatedAt == first.UpdatedAt {
		t.Fatalf("updated_at did not change")
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
		t.Fatalf("stat failure record: %v", err)
	}
}

func TestWritePlanExecuteFailureRecordRejectsSymlinkedRecoveryDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "recovery")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, _, err := WritePlanExecuteFailureRecord(root, PlanExecuteFailureInput{
		SessionID:   "session-symlink-recovery",
		FailureKind: "planner_failed",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "symlinked directories") {
		t.Fatalf("expected symlinked recovery write rejection, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(outside, "plan-execute-session-symlink-recovery", "failure.json")); !os.IsNotExist(err) {
		t.Fatalf("failure record should not be written through symlinked recovery dir, stat err=%v", err)
	}
}

func TestBuildFailureRecordImplementDefaultsToInspectFailure(t *testing.T) {
	record, err := BuildFailureRecord(FailureRecordInput{
		FailureID: recoveryIDForTest("implement", "session-implement"),
		SourceRun: SourceRun{
			Lane:              SourceLaneImplement,
			SessionID:         "session-implement",
			PromptFile:        "atoms/brief.md",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
		},
		FailureKind:    "implement_process_failed",
		FailureSummary: "implement process failed: exit_code=1 final_state=failed stop_reason=model_error degradations=queue_degraded",
		EvidenceRefs: []EvidenceRef{
			{Kind: "lane_history", Path: ".idiom/lane-history.jsonl"},
			{Kind: "session_summary", ID: "session-implement", Summary: "stop_reason=model_error"},
		},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildFailureRecord: %v", err)
	}

	if record.FailureID != "implement-session-implement" {
		t.Fatalf("failure id=%q", record.FailureID)
	}

	if record.SourceRun.Lane != SourceLaneImplement {
		t.Fatalf("source lane=%q", record.SourceRun.Lane)
	}

	if record.Failure.Class != FailureClassUnknown {
		t.Fatalf("failure class=%q", record.Failure.Class)
	}

	if record.NextAction != NextActionInspectFailure {
		t.Fatalf("next action=%q", record.NextAction)
	}

	if record.SourceRun.PromptFile != "atoms/brief.md" {
		t.Fatalf("prompt file=%q", record.SourceRun.PromptFile)
	}

	if len(record.SourceRun.RequiredEditPaths) != 1 || record.SourceRun.RequiredEditPaths[0] != "internal/workflows/recovery/record.go" {
		t.Fatalf("required paths=%#v", record.SourceRun.RequiredEditPaths)
	}
}

func TestBuildFailureRecordRedactsGenericDetails(t *testing.T) {
	record, err := BuildFailureRecord(FailureRecordInput{
		FailureID: "implement-session-redact",
		SourceRun: SourceRun{
			Lane:      SourceLaneImplement,
			SessionID: "session-redact",
		},
		FailureKind:    "implement_process_failed",
		FailureSummary: "exit_code=1\nraw_prompt: do the secret thing\nOPENAI_API_KEY=sk-live-secret",
		EvidenceRefs: []EvidenceRef{
			{Kind: "session_summary", ID: "session-redact", Summary: "transcript: hidden\nbearer abc123"},
		},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildFailureRecord: %v", err)
	}

	rendered := record.Failure.Summary + "\n" + record.Failure.EvidenceRefs[0].Summary
	for _, forbidden := range []string{"raw_prompt", "OPENAI_API_KEY", "sk-live-secret", "transcript", "bearer abc123"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("generic failure record leaked %q in %q", forbidden, rendered)
		}
	}

	if !strings.Contains(rendered, "[redacted sensitive recovery detail]") {
		t.Fatalf("expected redaction marker in %q", rendered)
	}
}

func TestBuildFailureRecordRejectsUnknownContractValues(t *testing.T) {
	base := FailureRecordInput{
		FailureID: "implement-session-contract",
		SourceRun: SourceRun{
			Lane:      SourceLaneImplement,
			SessionID: "session-contract",
		},
		FailureClass: FailureClassExecutorEdit,
	}
	if _, err := BuildFailureRecord(FailureRecordInput{
		FailureID:    "implement-session-bad-class",
		SourceRun:    base.SourceRun,
		FailureClass: "made_up_class",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "failure class") {
		t.Fatalf("expected invalid failure class error, got %v", err)
	}

	base.NextAction = "made_up_action"
	if _, err := BuildFailureRecord(base, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "next action") {
		t.Fatalf("expected invalid next action error, got %v", err)
	}
}

func TestPlanExecuteFailureWrappersPreserveFailureIDs(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:   "plan-execute-existing",
		FailureKind: "planner_failed",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	if record.FailureID != "plan-execute-existing" {
		t.Fatalf("prefixed session id should not be double-prefixed, got %q", record.FailureID)
	}

	root := t.TempDir()

	written, path, err := WritePlanExecuteFailureRecord(root, PlanExecuteFailureInput{
		SessionID:   "session-wrapper",
		FailureKind: "planner_failed",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WritePlanExecuteFailureRecord: %v", err)
	}

	if written.FailureID != "plan-execute-session-wrapper" {
		t.Fatalf("written failure id=%q", written.FailureID)
	}

	if path != filepath.ToSlash(filepath.Join(".idiom", "recovery", "plan-execute-session-wrapper", "failure.json")) {
		t.Fatalf("record path=%q", path)
	}
}

func TestTryLoadFailureRecordForSessionFindsImplementRecord(t *testing.T) {
	root := t.TempDir()

	written, _, err := WriteFailureRecord(root, FailureRecordInput{
		FailureID: ImplementFailureID("session-implement-load"),
		SourceRun: SourceRun{
			Lane:      SourceLaneImplement,
			SessionID: "session-implement-load",
		},
		FailureClass: FailureClassExecutorEdit,
		FailureKind:  "implement_process_failed",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WriteFailureRecord: %v", err)
	}

	loaded, path, found, err := TryLoadFailureRecordForSession(root, "session-implement-load")
	if err != nil || !found {
		t.Fatalf("TryLoadFailureRecordForSession found=%v err=%v", found, err)
	}

	if loaded.FailureID != written.FailureID || path != filepath.ToSlash(filepath.Join(".idiom", "recovery", written.FailureID, "failure.json")) {
		t.Fatalf("loaded wrong record/path: %#v path=%s", loaded, path)
	}
}

func TestClassifyPlanExecuteFailureRecognizesBudgetExhaustion(t *testing.T) {
	got := ClassifyPlanExecuteFailure("planner_failed", "completion reached max_tokens before submit_plan", "")
	if got != FailureClassBudgetExhausted {
		t.Fatalf("class=%q", got)
	}
}

func TestClassifyPlanExecuteFailureRecognizesMissingPathAuthorization(t *testing.T) {
	got := ClassifyPlanExecuteFailure("missing_edit_path_without_new_file_authorization", "plan would create missing edit path without new-file authorization", "")
	if got != FailureClassPlanInvalid {
		t.Fatalf("class=%q", got)
	}

	if action := nextActionForFailureClass(got); action != NextActionReviewDecomposition {
		t.Fatalf("next action=%q", action)
	}
}

func TestClassifyPlanExecuteFailureKeepsRepeatedInvalidSubmitPlanAsPlannerBlocked(t *testing.T) {
	got := ClassifyPlanExecuteFailure("planner_blocked", "<<QUESTION>>: Plan-execute submit correction is blocked because repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for every remaining required path.", "")
	if got != FailureClassPlannerBlocked {
		t.Fatalf("class=%q", got)
	}

	if action := nextActionForFailureClass(got); action != NextActionAnswerBlocker {
		t.Fatalf("next action=%q", action)
	}
}

func TestClassifyPlanExecuteFailureRecognizesTerminalContractFailure(t *testing.T) {
	got := ClassifyPlanExecuteFailure("terminal_completion_contract_failed", "terminal completion success rejected: changed_path_outside_write_envelope:pkg/feature.go", "")
	if got != FailureClassPlanInvalid {
		t.Fatalf("class=%q", got)
	}

	if action := nextActionForFailureClass(got); action != NextActionReviewDecomposition {
		t.Fatalf("next action=%q", action)
	}
}

func TestClassifyPlanExecuteFailureRecognizesReviewRepairScopeContractMismatch(t *testing.T) {
	got := ClassifyPlanExecuteFailure("review_repair_blocked", "<<QUESTION>>: required header changes are outside the allowed repair scope.", "")
	if got != FailureClassPlanInvalid {
		t.Fatalf("class=%q", got)
	}

	if action := nextActionForFailureClass(got); action != NextActionReviewDecomposition {
		t.Fatalf("next action=%q", action)
	}
}

func TestClassifyPlanExecuteFailureRecognizesScopeTooLarge(t *testing.T) {
	tests := []struct {
		name              string
		kind              string
		reason            string
		reviewReasonClass string
	}{
		{name: "explicit_kind", kind: "scope_too_large"},
		{name: "planner_reason", kind: "planner_failed", reason: "brief lint atom-size violation: scope too large for one atom"},
		{name: "executor_reason", kind: "executor_failed", reason: "too many required paths for a bounded implementation"},
		{name: "review_reason_class", kind: "review_repair_failed", reviewReasonClass: "scope_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPlanExecuteFailure(tt.kind, tt.reason, tt.reviewReasonClass)
			if got != FailureClassScopeTooLarge {
				t.Fatalf("class=%q", got)
			}
		})
	}
}

func TestClassifyPlanExecuteFailureRecognizesVerificationFailed(t *testing.T) {
	tests := []struct {
		name              string
		kind              string
		reason            string
		reviewReasonClass string
	}{
		{name: "explicit_kind", kind: "verification_failed"},
		{name: "source_sanity_kind", kind: "source_sanity_failed"},
		{name: "source_sanity_reason", kind: "executor_failed", reason: "source_sanity failed for pkg/feature_test.go: go test compile failed"},
		{name: "compile_reason", kind: "executor_failed", reason: "cmd/idiom: go test compile failed: undefined: missingHelper"},
		{name: "executor_reason", kind: "executor_failed", reason: "required tests failed: go test ./internal/workflows/recovery"},
		{name: "review_reason_class", kind: "review_repair_failed", reviewReasonClass: "verification_failed"},
		{name: "fallback_reason", kind: "unknown", reason: "verification command failed: failing package ./cmd/idiom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPlanExecuteFailure(tt.kind, tt.reason, tt.reviewReasonClass)
			if got != FailureClassVerification {
				t.Fatalf("class=%q", got)
			}
		})
	}
}

func TestBuildPlanExecuteFailureRecordRedactsSensitiveDetails(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:     "session-3",
		FailureKind:   "executor_failed",
		FailureReason: "executor failed\nOPENAI_API_KEY=sk-live-secret",
		ExecutorFailures: []string{
			"request failed with bearer abc123",
		},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	for _, value := range []string{record.Failure.Summary, record.Failure.EvidenceRefs[0].Summary} {
		if value == "" {
			t.Fatalf("expected redacted value")
		}

		if containsAny(value, []string{"OPENAI_API_KEY", "sk-live-secret", "bearer abc123"}) {
			t.Fatalf("sensitive value was not redacted: %q", value)
		}
	}

	if !containsAny(record.Failure.Summary, []string{"[redacted sensitive recovery detail]"}) {
		t.Fatalf("summary did not include redaction marker: %q", record.Failure.Summary)
	}
}

func TestRedactRecoveryTextRemovesTranscriptAndFileBodyShapes(t *testing.T) {
	redacted := RedactRecoveryText("raw_prompt: operator text\nfile_body:\n```text\ncustomer source\npassword=secret\n```\ntranscript: hidden")
	for _, forbidden := range []string{"raw_prompt", "file_body", "customer source", "password=secret", "transcript"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redaction leaked %q in %q", forbidden, redacted)
		}
	}

	if !strings.Contains(redacted, "redacted") {
		t.Fatalf("expected redaction marker: %q", redacted)
	}
}

func TestPartialStateForDirtyPathsClassifiesOutOfScope(t *testing.T) {
	partial := PartialStateForDirtyPaths(
		[]string{"unexpected.go"},
		[]string{"allowed.go"},
		[]string{"allowed.go"},
		nil,
		1,
	)
	if partial.DirtyStatus != DirtyStatusDirtyOutScope {
		t.Fatalf("dirty status=%q", partial.DirtyStatus)
	}

	if partial.ScopeStatus != ScopeStatusOutOfScope {
		t.Fatalf("scope status=%q", partial.ScopeStatus)
	}
}

func recoveryIDForTest(prefix, sessionID string) string {
	return prefix + "-" + sessionID
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}

	return false
}
