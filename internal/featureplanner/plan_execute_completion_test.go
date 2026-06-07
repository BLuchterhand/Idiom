package featureplanner

import (
	"fmt"
	"strings"
	"testing"
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
		`{"type":"idiom.plan_execute.review_repair_blocked","ts":"2026-06-07T12:01:00Z","state_path":".idiom/plan-execute/sessions/active.json","phase":"review_repair","status":"blocked","review_status":"blocked","failure_kind":"review_repair_blocked","failure_reason":"review repair remained blocked","changed_paths":["internal/app/feature.go"]}`,
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
	if len(changed) != 1 || changed[0] != "internal/app/feature.go" {
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
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/active.json","phase":"executor","status":"running","changed_paths":["internal/app/feature.go"]}`,
		`{"type":"idiom.plan_execute.review_repair_degraded","phase":"review_repair","status":"degraded","review_status":"degraded","failure_kind":"review_model_error","failure_reason":"review model returned invalid JSON"}`,
	}, "\n")

	fields := PlanExecuteCompletionFields(output)

	if got := fmt.Sprint(fields["progress_path"]); got != ".idiom/plan-execute/sessions/active.json" {
		t.Fatalf("progress_path = %q, want inherited path; fields=%+v", got, fields)
	}
	changed, _ := fields["changed_paths"].([]string)
	if len(changed) != 1 || changed[0] != "internal/app/feature.go" {
		t.Fatalf("changed_paths = %#v, want inherited changed path; fields=%+v", fields["changed_paths"], fields)
	}
	if got := fmt.Sprint(fields["failure_kind"]); got != "review_model_error" {
		t.Fatalf("failure_kind = %q, want review_model_error; fields=%+v", got, fields)
	}
}
