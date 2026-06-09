package featureplanner

import (
	"fmt"
	"strings"
	"testing"

	coreplanexecute "idiom/internal/workflows/planexecute"
)

func TestPlanExecuteCompletionTerminalClass(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		want   PlanExecuteTerminalClass
	}{
		{
			name:   "completed status",
			fields: map[string]any{"status": "completed"},
			want:   PlanExecuteTerminalCompleted,
		},
		{
			name:   "skipped review repair",
			fields: map[string]any{"status": "skipped", "review_repair_skipped": true},
			want:   PlanExecuteTerminalCompleted,
		},
		{
			name:   "source satisfied status",
			fields: map[string]any{"status": "source-satisfied", "outcome": "source_satisfied"},
			want:   PlanExecuteTerminalCompleted,
		},
		{
			name:   "failure details override degraded status",
			fields: map[string]any{"status": "degraded", "failure_kind": "review_model_error"},
			want:   PlanExecuteTerminalBlocked,
		},
		{
			name:   "failure details override success terminal class",
			fields: map[string]any{"terminal_class": "success", "status": "completed", "failure_kind": "review_model_error"},
			want:   PlanExecuteTerminalBlocked,
		},
		{
			name:   "degraded outcome overrides success terminal class",
			fields: map[string]any{"terminal_class": "success", "status": "completed", "outcome": "review_degraded"},
			want:   PlanExecuteTerminalBlocked,
		},
		{
			name:   "ambiguous status",
			fields: map[string]any{"status": "ambiguous"},
			want:   PlanExecuteTerminalBlocked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanExecuteCompletionTerminalClass(tt.fields); got != tt.want {
				t.Fatalf("PlanExecuteCompletionTerminalClass = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanExecuteCompletionTerminalDecisionReasons(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]any
		wantClass  PlanExecuteTerminalClass
		wantReason string
	}{
		{
			name:       "missing status",
			fields:     map[string]any{},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "missing required nested plan-execute status",
		},
		{
			name:       "skipped without explicit marker",
			fields:     map[string]any{"status": "skipped"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design",
		},
		{
			name:       "running phase blocks completed status",
			fields:     map[string]any{"status": "completed", "phase": "running"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute status is not success-shaped",
		},
		{
			name:       "failure details block success terminal class",
			fields:     map[string]any{"terminal_class": "success", "status": "completed", "failure_reason": "review repair failed"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute reported failure details",
		},
		{
			name:       "skipped status blocks success terminal class without marker",
			fields:     map[string]any{"terminal_class": "success", "status": "skipped"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design",
		},
		{
			name:      "skipped review with explicit marker succeeds",
			fields:    map[string]any{"status": "completed", "review_status": "skipped", "review_repair_skipped": true},
			wantClass: PlanExecuteTerminalCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanExecuteCompletionTerminalDecision(tt.fields)
			if got.Class != tt.wantClass || got.Reason != tt.wantReason {
				t.Fatalf("decision = %#v, want class=%q reason=%q", got, tt.wantClass, tt.wantReason)
			}
		})
	}
}

func TestPlanExecuteCompletionFieldsDerivesSourceSatisfiedOutcome(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/source/state.json","phase":"complete","status":"completed"}`,
		`{"type":"idiom.plan_execute.source_satisfied","source_satisfied_evidence_paths":["pkg/feature.go"],"reason":"planner reported requested final state already present"}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	if got := CompletionFieldString(fields, "status"); got != "source-satisfied" {
		t.Fatalf("status = %q, want source-satisfied; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "outcome"); got != "source_satisfied" {
		t.Fatalf("outcome = %q, want source_satisfied; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsPrefersLaterConcreteReviewRepairState(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","ts":"2026-06-07T12:00:00Z","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact"}`,
		`{"type":"idiom.plan_execute.review_repair_blocked","ts":"2026-06-07T12:01:00Z","state_path":".idiom/plan-execute/sessions/active.json","phase":"review_repair","status":"blocked","review_status":"blocked","failure_kind":"review_repair_blocked","failure_reason":"review repair remained blocked","changed_paths":["internal/surfaces/tui/app/feature.go"]}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	for key, want := range map[string]string{
		"status":         "blocked",
		"phase":          "review_repair",
		"review_status":  "blocked",
		"failure_kind":   "review_repair_blocked",
		"failure_reason": "review repair remained blocked",
		"progress_path":  ".idiom/plan-execute/sessions/active.json",
	} {
		if got := fmt.Sprint(fields[key]); got != want {
			t.Fatalf("%s = %q, want %q; fields=%+v", key, got, want, fields)
		}
	}
	changed, _ := fields["changed_paths"].([]string)
	if len(changed) != 1 || changed[0] != "internal/surfaces/tui/app/feature.go" {
		t.Fatalf("changed_paths = %#v, want active changed path; fields=%+v", fields["changed_paths"], fields)
	}
}

func TestPlanExecuteCompletionFieldsPreservesRecoverySidecarMetadata(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","ts":"2026-06-07T12:00:00Z","state_path":".idiom/plan-execute/sessions/pe.json","phase":"planning","status":"failed","failure_kind":"missing_edit_path_without_new_file_authorization","failure_reason":"plan would create missing edit path without new-file authorization"}`,
		`{"type":"idiom.plan_execute.recovery_failure_record_written","ts":"2026-06-07T12:00:01Z","failure_id":"failure-123","failure_kind":"missing_edit_path_without_new_file_authorization","record_path":".idiom/recovery/failures/failure-123.json","next_valid_action":"review_decomposition"}`,
		`{"type":"idiom.plan_execute.recovery_decomposition_packet_written","ts":"2026-06-07T12:00:02Z","failure_id":"failure-123","packet_path":".idiom/recovery/packets/failure-123.json","next_valid_action":"review_decomposition"}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	for key, want := range map[string]string{
		"status":            "failed",
		"failure_kind":      "missing_edit_path_without_new_file_authorization",
		"failure_id":        "failure-123",
		"record_path":       ".idiom/recovery/failures/failure-123.json",
		"packet_path":       ".idiom/recovery/packets/failure-123.json",
		"next_valid_action": "review_decomposition",
	} {
		if got := fmt.Sprint(fields[key]); got != want {
			t.Fatalf("%s = %q, want %q; fields=%+v", key, got, want, fields)
		}
	}
}

func TestPlanExecuteCompletionFieldsLaterCompletedStateClearsEarlierFailure(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact"}`,
		`{"type":"idiom.plan_execute.completed","state_path":".idiom/plan-execute/sessions/active.json","phase":"complete","status":"completed","review_status":"skipped","review_repair_skipped":true}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	if got := fmt.Sprint(fields["status"]); got != "completed" {
		t.Fatalf("status = %q, want completed; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "failure_kind"); got != "" {
		t.Fatalf("failure_kind = %q, want cleared earlier planning failure; fields=%+v", got, fields)
	}
	if got := fmt.Sprint(fields["progress_path"]); got != ".idiom/plan-execute/sessions/active.json" {
		t.Fatalf("progress_path = %q, want active progress path; fields=%+v", got, fields)
	}
	if got := PlanExecuteCompletionTerminalClass(fields); got != PlanExecuteTerminalCompleted {
		t.Fatalf("terminal class = %q, want completed; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsKeepsNestedSourceSatisfiedTerminal(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.completed","status":"completed","phase":"review_repair"}`,
		`{"type":"idiom.plan_execute.source_satisfied","status":"source-satisfied","phase":"complete"}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	if got := CompletionFieldString(fields, "status"); got != "source-satisfied" {
		t.Fatalf("status = %q, want source-satisfied; fields=%+v", got, fields)
	}
	if got := PlanExecuteCompletionTerminalClass(fields); got != PlanExecuteTerminalCompleted {
		t.Fatalf("terminal class = %q, want completed; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsPreservesScopeContractWithoutGenericAlias(t *testing.T) {
	output := `{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","scope_contract_id":"scope-123","scope_contract_hash":"scope-hash-123"}`

	fields := PlanExecuteCompletionFields(output)

	for key, want := range map[string]string{
		"scope_contract_id":   "scope-123",
		"scope_contract_hash": "scope-hash-123",
	} {
		if got := CompletionFieldString(fields, key); got != want {
			t.Fatalf("%s = %q, want %q; fields=%+v", key, got, want, fields)
		}
	}
	if got := CompletionFieldString(fields, "contract_id"); got != "" {
		t.Fatalf("contract_id = %q, want empty generic contract id for scope-only record; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "contract_hash"); got != "" {
		t.Fatalf("contract_hash = %q, want empty generic contract hash for scope-only record; fields=%+v", got, fields)
	}

	fromFields := PlanExecuteCompletionSummaryFromFields(map[string]any{
		"status":              "completed",
		"scope_contract_id":   "scope-fields",
		"scope_contract_hash": "scope-fields-hash",
	}).Fields()
	if got := CompletionFieldString(fromFields, "contract_id"); got != "" {
		t.Fatalf("contract_id = %q, want empty generic contract id for scope-only fields; fields=%+v", got, fromFields)
	}
	if got := CompletionFieldString(fromFields, "contract_hash"); got != "" {
		t.Fatalf("contract_hash = %q, want empty generic contract hash for scope-only fields; fields=%+v", got, fromFields)
	}
	if got := CompletionFieldString(fromFields, "scope_contract_id"); got != "scope-fields" {
		t.Fatalf("scope_contract_id = %q, want scope-fields; fields=%+v", got, fromFields)
	}
	if got := CompletionFieldString(fromFields, "scope_contract_hash"); got != "scope-fields-hash" {
		t.Fatalf("scope_contract_hash = %q, want scope-fields-hash; fields=%+v", got, fromFields)
	}
}

func TestPlanExecuteCompletionFieldsNormalizesLegacyContractHashToSnapshot(t *testing.T) {
	fields := PlanExecuteCompletionFields(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","contract_id":"parent-contract","contract_hash":"snapshot-hash-1"}`)

	if got := CompletionFieldString(fields, "contract_id"); got != "parent-contract" {
		t.Fatalf("contract_id = %q, want parent-contract; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "snapshot_hash"); got != "snapshot-hash-1" {
		t.Fatalf("snapshot_hash = %q, want legacy contract_hash normalized; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "contract_hash"); got != "" {
		t.Fatalf("contract_hash = %q, want no parallel contract_hash authority; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionMergeAndOverlayPreserveScopeContractFields(t *testing.T) {
	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummary{
			Status:            "running",
			ScopeContractID:   "scope-123",
			ScopeContractHash: "scope-hash-123",
		},
		PlanExecuteCompletionSummary{
			Status: "completed",
			Phase:  "complete",
		},
	).Fields()

	for key, want := range map[string]string{
		"scope_contract_id":   "scope-123",
		"scope_contract_hash": "scope-hash-123",
	} {
		if got := CompletionFieldString(merged, key); got != want {
			t.Fatalf("merged %s = %q, want %q; fields=%+v", key, got, want, merged)
		}
	}
	if got := CompletionFieldString(merged, "contract_id"); got != "" {
		t.Fatalf("merged contract_id = %q, want empty generic contract id; fields=%+v", got, merged)
	}
	if got := CompletionFieldString(merged, "contract_hash"); got != "" {
		t.Fatalf("merged contract_hash = %q, want empty generic contract hash; fields=%+v", got, merged)
	}

	fields := map[string]any{
		"status": "completed",
		"phase":  "complete",
	}
	OverlayPlanExecuteCompletionFields(fields, map[string]any{
		"scope_contract_id":   "scope-overlay",
		"scope_contract_hash": "scope-overlay-hash",
	})
	if got := CompletionFieldString(fields, "scope_contract_id"); got != "scope-overlay" {
		t.Fatalf("overlaid scope_contract_id = %q, want scope-overlay; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "scope_contract_hash"); got != "scope-overlay-hash" {
		t.Fatalf("overlaid scope_contract_hash = %q, want scope-overlay-hash; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "contract_id"); got != "" {
		t.Fatalf("overlaid contract_id = %q, want empty generic contract id; fields=%+v", got, fields)
	}
	if got := CompletionFieldString(fields, "contract_hash"); got != "" {
		t.Fatalf("overlaid contract_hash = %q, want empty generic contract hash; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsTrustsTerminalAcceptanceEvidence(t *testing.T) {
	fields := PlanExecuteCompletionFields(`{"type":"idiom.plan_execute.terminal_completion","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`)

	if got := fields["acceptance_evidence_trusted"]; got != true {
		t.Fatalf("acceptance_evidence_trusted = %#v, want true; fields=%+v", got, fields)
	}
}

func TestTrustPlanExecuteCompletionFieldsDoesNotMintTrustForHarnessReturnSource(t *testing.T) {
	fields := map[string]any{
		"status":                  "completed",
		"terminal_class":          "completed",
		"completion_source":       "tui_plan_execute_returned",
		"acceptance_evidence_ids": []string{"req-1"},
	}

	TrustPlanExecuteCompletionFields(fields)

	if got := fields["acceptance_evidence_trusted"]; got == true {
		t.Fatalf("consumer-side trust should not be minted from completion_source alone; fields=%+v", fields)
	}
	if planExecuteAcceptanceEvidenceTrustRegistered(fields) {
		t.Fatalf("completion_source-only trust should not be registered; fields=%+v", fields)
	}
}

func TestTrustObservedPlanExecuteCompletionFieldsRegistersProducerTrust(t *testing.T) {
	fields := map[string]any{
		"status":                  "completed",
		"terminal_class":          "completed",
		"completion_source":       "tui_plan_execute_returned",
		"acceptance_evidence_ids": []string{"req-1"},
	}

	TrustObservedPlanExecuteCompletionFields(fields)

	if got := fields["acceptance_evidence_trusted"]; got != true {
		t.Fatalf("observed terminal completion should trust acceptance evidence; fields=%+v", fields)
	}
	if !planExecuteAcceptanceEvidenceTrustRegistered(fields) {
		t.Fatalf("observed terminal completion was not registered; fields=%+v", fields)
	}
}

func TestTrustObservedPlanExecuteCompletionFieldsDoesNotRegisterFailedTerminal(t *testing.T) {
	fields := map[string]any{
		"status":                  "failed",
		"terminal_class":          "failed",
		"acceptance_evidence_ids": []string{"req-1"},
	}

	TrustObservedPlanExecuteCompletionFields(fields)

	if got := fields["acceptance_evidence_trusted"]; got == true {
		t.Fatalf("failed terminal completion should not trust acceptance evidence; fields=%+v", fields)
	}
	if planExecuteAcceptanceEvidenceTrustRegistered(fields) {
		t.Fatalf("failed terminal completion should not be registered; fields=%+v", fields)
	}
}

func TestPlanExecuteCompletionFieldsDoesNotTrustLegacyAcceptanceEvidence(t *testing.T) {
	fields := PlanExecuteCompletionFields(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`)

	if got := fields["acceptance_evidence_trusted"]; got == true {
		t.Fatalf("legacy completion should not trust acceptance evidence; fields=%+v", fields)
	}
}

func TestOverlayPlanExecuteCompletionFieldsReplacesSourceSanityEvidence(t *testing.T) {
	fields := map[string]any{
		"status": "running",
		"source_sanity": []any{
			map[string]any{"kind": "source_sanity", "path": "pkg/stale.go", "status": "ok"},
		},
	}

	OverlayPlanExecuteCompletionFields(fields, map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"changed_paths": []any{"pkg/feature.go"},
		"source_sanity": []any{
			map[string]any{"kind": "source_sanity", "path": "pkg/feature.go", "status": "ok"},
		},
	})

	checks, ok := fields["source_sanity"].([]coreplanexecute.PhaseEvidenceCheck)
	if !ok || len(checks) != 1 || checks[0].Path != "pkg/feature.go" {
		t.Fatalf("source_sanity overlay = %#v, want only current terminal evidence; fields=%+v", fields["source_sanity"], fields)
	}
}

func TestOverlayPlanExecuteCompletionFieldsReplacesAcceptanceEvidence(t *testing.T) {
	fields := map[string]any{
		"status":                      "running",
		"acceptance_evidence_ids":     []any{"stale-acceptance"},
		"acceptance_evidence_trusted": true,
	}

	OverlayPlanExecuteCompletionFields(fields, map[string]any{
		"status": "completed",
		"phase":  "complete",
	})

	if _, ok := fields["acceptance_evidence_ids"]; ok {
		t.Fatalf("stale acceptance_evidence_ids survived overlay: fields=%+v", fields)
	}
	if _, ok := fields["acceptance_evidence_trusted"]; ok {
		t.Fatalf("stale acceptance_evidence_trusted survived overlay: fields=%+v", fields)
	}
}

func TestOverlayPlanExecuteCompletionFieldsNestedFailureOverridesWrapperCompleted(t *testing.T) {
	fields := map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"progress_path": ".idiom/plan-execute/sessions/wrapper.json",
	}
	OverlayPlanExecuteCompletionFields(fields, map[string]any{
		"status":         "failed",
		"phase":          "planning",
		"failure_kind":   "plan_artifact_missing",
		"failure_reason": "nested plan-execute failed before handoff",
		"progress_path":  ".idiom/plan-execute/sessions/nested.json",
	})

	for key, want := range map[string]string{
		"status":         "failed",
		"phase":          "planning",
		"failure_kind":   "plan_artifact_missing",
		"failure_reason": "nested plan-execute failed before handoff",
		"progress_path":  ".idiom/plan-execute/sessions/nested.json",
	} {
		if got := fmt.Sprint(fields[key]); got != want {
			t.Fatalf("%s = %q, want %q; fields=%+v", key, got, want, fields)
		}
	}
	if got := PlanExecuteCompletionTerminalClass(fields); got != PlanExecuteTerminalBlocked {
		t.Fatalf("terminal class = %q, want blocked; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsDegradedInheritsEarlierProgressPath(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/active.json","phase":"executor","status":"running","changed_paths":["internal/surfaces/tui/app/feature.go"]}`,
		`{"type":"idiom.plan_execute.review_repair_degraded","phase":"review_repair","status":"degraded","review_status":"degraded","failure_kind":"review_model_error","failure_reason":"review model returned invalid JSON"}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	if got := fmt.Sprint(fields["progress_path"]); got != ".idiom/plan-execute/sessions/active.json" {
		t.Fatalf("progress_path = %q, want inherited path; fields=%+v", got, fields)
	}
	changed, _ := fields["changed_paths"].([]string)
	if len(changed) != 1 || changed[0] != "internal/surfaces/tui/app/feature.go" {
		t.Fatalf("changed_paths = %#v, want inherited changed path; fields=%+v", fields["changed_paths"], fields)
	}
	if got := fmt.Sprint(fields["failure_kind"]); got != "review_model_error" {
		t.Fatalf("failure_kind = %q, want review_model_error; fields=%+v", got, fields)
	}
}
