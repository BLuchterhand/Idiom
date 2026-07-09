package featureplanner

import (
	"fmt"
	"strings"
	"testing"

	plancontract "idiom/internal/contracts/plan"
)

func TestPlanExecuteCompletionTerminalDecisionClass(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		want   plancontract.PlanExecuteTerminalDecisionClass
	}{
		{
			name:   "completed status",
			fields: map[string]any{"status": "completed"},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
		{
			name:   "skipped review repair",
			fields: map[string]any{"status": "skipped", "review_repair_skipped": true},
			want:   plancontract.PlanExecuteTerminalDecisionCompleted,
		},
		{
			name:   "source satisfied status",
			fields: map[string]any{"status": "source-satisfied", "outcome": "source_satisfied"},
			want:   plancontract.PlanExecuteTerminalDecisionCompleted,
		},
		{
			name:   "failure details override degraded status",
			fields: map[string]any{"status": "degraded", "failure_kind": "review_model_error"},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
		{
			name:   "failure details override success terminal class",
			fields: map[string]any{"terminal_class": "success", "status": "completed", "failure_kind": "review_model_error"},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
		{
			name:   "degraded outcome overrides success terminal class",
			fields: map[string]any{"terminal_class": "success", "status": "completed", "outcome": "review_degraded"},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
		{
			name:   "nonzero exit code blocks completed terminal class",
			fields: map[string]any{"terminal_class": "success", "status": "completed", "exit_code": 1},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
		{
			name:   "ambiguous status",
			fields: map[string]any{"status": "ambiguous"},
			want:   plancontract.PlanExecuteTerminalDecisionBlocked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanExecuteCompletionTerminalDecision(tt.fields).Class; got != tt.want {
				t.Fatalf("PlanExecuteCompletionTerminalClass = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanExecuteCompletionTerminalDecisionReasons(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]any
		wantClass  plancontract.PlanExecuteTerminalDecisionClass
		wantReason string
	}{
		{
			name:       "missing status",
			fields:     map[string]any{},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: "missing required nested plan-execute status",
		},
		{
			name:       "skipped without explicit marker",
			fields:     map[string]any{"status": "skipped"},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design",
		},
		{
			name:       "running phase blocks completed status",
			fields:     map[string]any{"status": "completed", "phase": "running"},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: `nested plan-execute terminal record invalid: invalid plan-execute terminal completion phase "running"`,
		},
		{
			name:       "failure details block success terminal class",
			fields:     map[string]any{"terminal_class": "success", "status": "completed", "failure_reason": "review repair failed"},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: "nested plan-execute reported failure details",
		},
		{
			name:       "failed review blocks completed status",
			fields:     map[string]any{"phase": "complete", "status": "completed", "review_status": "failed"},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: "nested plan-execute status is not success-shaped",
		},
		{
			name:       "skipped status blocks success terminal class without marker",
			fields:     map[string]any{"terminal_class": "success", "status": "skipped"},
			wantClass:  plancontract.PlanExecuteTerminalDecisionBlocked,
			wantReason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design",
		},
		{
			name:      "skipped review with explicit marker succeeds",
			fields:    map[string]any{"status": "completed", "review_status": "skipped", "review_repair_skipped": true},
			wantClass: plancontract.PlanExecuteTerminalDecisionCompleted,
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

func TestPlanExecuteCompletionFieldsPreservesExitCodeForTerminalDecision(t *testing.T) {
	output := `{"type":"idiom.plan_execute.terminal_completion","terminal_class":"success","status":"completed","exit_code":1}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := completionFieldInt(fields, "exit_code"); got != 1 {
		t.Fatalf("exit_code = %#v, want 1; fields=%+v", got, fields)
	}
	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want %q; fields=%+v", got, plancontract.PlanExecuteTerminalDecisionBlocked, fields)
	}
}

func TestPlanExecuteCompletionFieldsUsesTerminalRecordAsSerializationSource(t *testing.T) {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:        plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:  plancontract.PlanExecuteReviewStatusPassed,
		TerminalClass: plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeCompleted,
		ChangedPaths:  []string{"a.go"},
	})
	summary := PlanExecuteCompletionSummary{
		Status:         "failed",
		FailureKind:    "stale_summary_should_not_win",
		terminalRecord: &record,
	}

	fields := summary.Fields()

	if got := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatus); got != string(plancontract.PlanExecuteTerminalStatusCompleted) {
		t.Fatalf("status = %q, want terminal record status; fields=%#v", got, fields)
	}
	if got := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldFailureKind); got != "" {
		t.Fatalf("failure_kind = %q, want terminal record to suppress stale summary failure; fields=%#v", got, fields)
	}
	if got := completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldChangedPaths); len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("changed_paths = %#v, want terminal record paths; fields=%#v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsIgnoresUnknownPlanExecuteEventName(t *testing.T) {
	output := `{"type":"idiom.plan_execute.not_real","terminal_class":"success","status":"completed","phase":"complete","review_status":"passed"}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := CompletionFieldString(fields, "status"); got != "" {
		t.Fatalf("status = %q, want unknown event ignored; fields=%+v", got, fields)
	}
	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("decision = %q, want blocked for ignored unknown event; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsBlocksInvalidPlanExecuteEventName(t *testing.T) {
	output := fmt.Sprintf(`{"type":%q,"terminal_class":"success","status":"completed","phase":"complete","review_status":"passed"}`,
		plancontract.PlanExecuteInvalidEventName(plancontract.PlanExecuteEventVerification))

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("decision = %q, want blocked for invalid event; fields=%+v", got, fields)
	}
	if got := completionFieldStringSlice(fields, "validation_failures"); len(got) != 1 || got[0] != "invalid_plan_execute_event:verification" {
		t.Fatalf("validation_failures = %#v, want invalid event marker; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsTreatsSourceSatisfiedEventAsEvidenceOnly(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.source_satisfied","source_satisfied_evidence_paths":["pkg/feature.go"],"reason":"planner reported requested final state already present"}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := CompletionFieldString(fields, "status"); got != "" {
		t.Fatalf("status = %q, want no terminal status from intermediate source_satisfied event; fields=%+v", got, fields)
	}

	if got := CompletionFieldString(fields, "outcome"); got != "" {
		t.Fatalf("outcome = %q, want no terminal outcome from intermediate source_satisfied event; fields=%+v", got, fields)
	}

	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want blocked without terminal_completion record; fields=%+v", got, fields)
	}

	if got := completionFieldStringSlice(fields, "source_satisfied_evidence_paths"); len(got) != 1 || got[0] != "pkg/feature.go" {
		t.Fatalf("source satisfied evidence paths = %#v", got)
	}
}

func TestPlanExecuteCompletionFieldsPrefersLaterConcreteReviewRepairState(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","ts":"2026-06-07T12:00:00Z","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact"}`,
		`{"type":"idiom.plan_execute.review_repair_blocked","ts":"2026-06-07T12:01:00Z","state_path":".idiom/plan-execute/sessions/active.json","phase":"review_repair","status":"blocked","review_status":"blocked","failure_kind":"review_repair_blocked","failure_reason":"review repair remained blocked","changed_paths":["internal/surfaces/tui/app/feature.go"]}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

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

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

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
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","exit_code":1,"failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact","failure_id":"failure-123","record_path":".idiom/recovery/failures/failure-123.json","packet_path":".idiom/recovery/failure-123/packet.json","next_valid_action":"review_decomposition","validation_failures":["exit_code_nonzero"],"source_sanity":[{"kind":"source_sanity","path":"pkg/feature.go","status":"failed","error":"compile failed"}],"verification_rejects":[{"command":"go test ./pkg","reason":"unsupported_verification_command"}]}`,
		`{"type":"idiom.plan_execute.completed","state_path":".idiom/plan-execute/sessions/active.json","phase":"complete","status":"completed","review_status":"skipped","review_repair_skipped":true}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := fmt.Sprint(fields["status"]); got != "completed" {
		t.Fatalf("status = %q, want completed; fields=%+v", got, fields)
	}

	if got := CompletionFieldString(fields, "failure_kind"); got != "" {
		t.Fatalf("failure_kind = %q, want cleared earlier planning failure; fields=%+v", got, fields)
	}

	if got, ok := fields["exit_code"]; ok {
		t.Fatalf("exit_code = %#v, want cleared earlier planning failure; fields=%+v", got, fields)
	}

	if got, ok := fields["validation_failures"]; ok {
		t.Fatalf("validation_failures = %#v, want cleared earlier planning failure; fields=%+v", got, fields)
	}

	if got, ok := fields["verification_rejects"]; ok {
		t.Fatalf("verification_rejects = %#v, want cleared earlier planning failure; fields=%+v", got, fields)
	}

	if got, ok := fields["source_sanity"]; ok {
		t.Fatalf("source_sanity = %#v, want cleared earlier planning failure; fields=%+v", got, fields)
	}

	for _, key := range []string{"failure_id", "record_path", "packet_path", "next_valid_action"} {
		if got, ok := fields[key]; ok {
			t.Fatalf("%s = %#v, want cleared earlier planning failure; fields=%+v", key, got, fields)
		}
	}

	if got := fmt.Sprint(fields["progress_path"]); got != ".idiom/plan-execute/sessions/active.json" {
		t.Fatalf("progress_path = %q, want active progress path; fields=%+v", got, fields)
	}

	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionCompleted {
		t.Fatalf("terminal class = %q, want completed; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsKeepsTerminalCompletionSourceSatisfied(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.completed","status":"completed","phase":"review_repair"}`,
		`{"type":"idiom.plan_execute.terminal_completion","terminal_class":"success","status":"source-satisfied","outcome":"source_satisfied","phase":"complete","required_edit_paths":["pkg/feature.go"],"source_satisfied_evidence_paths":["pkg/feature.go"],"source_satisfied_evidence":[{"path":"pkg/feature.go","kind":"harness_observed_source"}]}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := CompletionFieldString(fields, "status"); got != "source-satisfied" {
		t.Fatalf("status = %q, want source-satisfied; fields=%+v", got, fields)
	}

	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionCompleted {
		t.Fatalf("terminal class = %q, want completed; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsPreservesScopeContractWithoutGenericAlias(t *testing.T) {
	output := `{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","scope_contract_id":"scope-123","scope_contract_hash":"scope-hash-123"}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

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
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","contract_id":"parent-contract","contract_hash":"snapshot-hash-1"}`).Fields()

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
	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{
			"scope_contract_id":   "scope-overlay",
			"scope_contract_hash": "scope-overlay-hash",
		}),
	).Fields()

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

func TestPlanExecuteCompletionMergeAndOverlayPreserveNonzeroExitCode(t *testing.T) {
	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummary{
			Status:        "completed",
			Phase:         "complete",
			TerminalClass: "success",
		},
		PlanExecuteCompletionSummary{
			ExitCode: 1,
		},
	).Fields()

	if got := merged["exit_code"]; got != 1 {
		t.Fatalf("merged exit_code = %#v, want 1; fields=%+v", got, merged)
	}
	if got := PlanExecuteCompletionTerminalDecision(merged).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("merged terminal class = %q, want blocked; fields=%+v", got, merged)
	}

	fields := map[string]any{
		"status":         "completed",
		"phase":          "complete",
		"terminal_class": "success",
	}
	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{"exit_code": 1}),
	).Fields()

	if got := fields["exit_code"]; got != 1 {
		t.Fatalf("overlaid exit_code = %#v, want 1; fields=%+v", got, fields)
	}
	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("overlaid terminal class = %q, want blocked; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionMergePreservesSparseValidationFailure(t *testing.T) {
	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummary{
			Status:        "completed",
			Phase:         "complete",
			TerminalClass: "success",
		},
		PlanExecuteCompletionSummary{
			ValidationFailures: []string{"exit_code_nonzero"},
		},
	).Fields()

	if got := completionFieldStringSlice(merged, "validation_failures"); len(got) != 1 || got[0] != "exit_code_nonzero" {
		t.Fatalf("validation_failures = %#v, want sparse failure preserved; fields=%+v", got, merged)
	}
	if got := PlanExecuteCompletionTerminalDecision(merged).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want blocked; fields=%+v", got, merged)
	}
}

func TestPlanExecuteCompletionMergePreservesSparseVerificationReject(t *testing.T) {
	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummary{
			Status:        "completed",
			Phase:         "complete",
			TerminalClass: "success",
		},
		PlanExecuteCompletionSummary{
			VerificationRejects: []plancontract.VerificationReject{{
				Command: "go test ./pkg",
				Reason:  plancontract.VerificationRejectUnsupportedCmd,
			}},
		},
	).Fields()

	rejects, _ := merged["verification_rejects"].([]plancontract.VerificationReject)
	if len(rejects) != 1 || rejects[0].Command != "go test ./pkg" {
		t.Fatalf("verification_rejects = %#v, want sparse reject preserved; fields=%+v", merged["verification_rejects"], merged)
	}
	if got := PlanExecuteCompletionTerminalDecision(merged).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want blocked; fields=%+v", got, merged)
	}
}

func TestPlanExecuteCompletionMergePreservesSparseSourceSanityFailure(t *testing.T) {
	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummary{
			Status:        "completed",
			Phase:         "complete",
			TerminalClass: "success",
		},
		PlanExecuteCompletionSummary{
			SourceSanity: []plancontract.PhaseEvidenceCheck{{
				Kind:   "source_sanity",
				Path:   "pkg/feature.go",
				Status: "failed",
				Error:  "compile failed",
			}},
		},
	).Fields()

	checks, _ := merged["source_sanity"].([]plancontract.PhaseEvidenceCheck)
	if len(checks) != 1 || checks[0].Path != "pkg/feature.go" {
		t.Fatalf("source_sanity = %#v, want sparse failure preserved; fields=%+v", merged["source_sanity"], merged)
	}
	if got := PlanExecuteCompletionTerminalDecision(merged).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want blocked; fields=%+v", got, merged)
	}
}

func TestPlanExecuteCompletionFieldsTrustsTerminalAcceptanceEvidence(t *testing.T) {
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.terminal_completion","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`).Fields()

	if got := fields["acceptance_evidence_trusted"]; got != true {
		t.Fatalf("acceptance_evidence_trusted = %#v, want true; fields=%+v", got, fields)
	}
}

func TestTrustObservedPlanExecuteCompletionFieldsMarksProducerTrust(t *testing.T) {
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

}

func TestPlanExecuteCompletionFieldsDoesNotTrustLegacyAcceptanceEvidence(t *testing.T) {
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`).Fields()

	if got := fields["acceptance_evidence_trusted"]; got == true {
		t.Fatalf("legacy completion should not trust acceptance evidence; fields=%+v", fields)
	}
}

func TestPlanExecuteCompletionSummaryMergeReplacesSourceSanityEvidence(t *testing.T) {
	fields := map[string]any{
		"status": "running",
		"source_sanity": []any{
			map[string]any{"kind": "source_sanity", "path": "pkg/stale.go", "status": "ok"},
		},
	}

	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{
			"status":        "completed",
			"phase":         "complete",
			"changed_paths": []any{"pkg/feature.go"},
			"source_sanity": []any{
				map[string]any{"kind": "source_sanity", "path": "pkg/feature.go", "status": "ok"},
			},
		}),
	).Fields()

	checks, ok := fields["source_sanity"].([]plancontract.PhaseEvidenceCheck)
	if !ok || len(checks) != 1 || checks[0].Path != "pkg/feature.go" {
		t.Fatalf("source_sanity overlay = %#v, want only current terminal evidence; fields=%+v", fields["source_sanity"], fields)
	}
}

func TestPlanExecuteCompletionSummaryMergeReplacesAcceptanceEvidence(t *testing.T) {
	fields := map[string]any{
		"status":                      "running",
		"acceptance_evidence_ids":     []any{"stale-acceptance"},
		"acceptance_evidence_trusted": true,
	}

	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{
			"status": "completed",
			"phase":  "complete",
		}),
	).Fields()

	if _, ok := fields["acceptance_evidence_ids"]; ok {
		t.Fatalf("stale acceptance_evidence_ids survived overlay: fields=%+v", fields)
	}

	if _, ok := fields["acceptance_evidence_trusted"]; ok {
		t.Fatalf("stale acceptance_evidence_trusted survived overlay: fields=%+v", fields)
	}
}

func TestPlanExecuteCompletionSummaryMergeReplacesWriteAllowedPaths(t *testing.T) {
	fields := map[string]any{
		"status":              "running",
		"write_allowed_paths": []any{"pkg/stale.go"},
	}

	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{
			"status":              "completed",
			"phase":               "complete",
			"write_allowed_paths": []any{"pkg/feature.go"},
		}),
	).Fields()

	paths, ok := fields["write_allowed_paths"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "pkg/feature.go" {
		t.Fatalf("write_allowed_paths overlay = %#v, want only current terminal envelope; fields=%+v", fields["write_allowed_paths"], fields)
	}
}

func TestPlanExecuteCompletionSummaryMergeNestedFailureOverridesWrapperCompleted(t *testing.T) {
	fields := map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"progress_path": ".idiom/plan-execute/sessions/wrapper.json",
	}
	fields = MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(map[string]any{
			"status":         "failed",
			"phase":          "planning",
			"failure_kind":   "plan_artifact_missing",
			"failure_reason": "nested plan-execute failed before handoff",
			"progress_path":  ".idiom/plan-execute/sessions/nested.json",
		}),
	).Fields()

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

	if got := PlanExecuteCompletionTerminalDecision(fields).Class; got != plancontract.PlanExecuteTerminalDecisionBlocked {
		t.Fatalf("terminal class = %q, want blocked; fields=%+v", got, fields)
	}
}

func TestPlanExecuteCompletionFieldsDegradedInheritsEarlierProgressPath(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/active.json","phase":"executor","status":"running","changed_paths":["internal/surfaces/tui/app/feature.go"]}`,
		`{"type":"idiom.plan_execute.review_repair_degraded","phase":"review_repair","status":"degraded","review_status":"degraded","failure_kind":"review_model_error","failure_reason":"review model returned invalid JSON"}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

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

func TestPlanExecuteCompletionFieldsPreserveOwnerTerminalProjection(t *testing.T) {
	summary := PlanExecuteCompletionSummaryFromFields(map[string]any{
		"type":                             plancontract.PlanExecuteTerminalCompletionRecordType,
		"status":                           "failed",
		"workflow_id":                      "plan_execute",
		"workflow_terminal_state":          "blocked",
		"dispatchable":                     false,
		"required_edit_paths":              []any{"pkg/a.go"},
		"changed_paths":                    []any{"pkg/a.go"},
		"required_path_coverage":           []any{map[string]any{"path": "pkg/a.go", "status": "covered", "evidence": "read_file"}},
		"required_acceptance_evidence_ids": []any{"eval-1"},
		"acceptance_evidence_claims":       []any{map[string]any{"id": "eval-1", "status": "satisfied"}},
		"failure_invariants":               []any{map[string]any{"code": "recovery_record_missing", "status": "failed", "reason": "record missing"}},
	})

	fields := summary.Fields()
	for _, key := range []string{
		"workflow_id",
		"workflow_terminal_state",
		"required_path_coverage",
		"required_acceptance_evidence_ids",
		"acceptance_evidence_claims",
		"failure_invariants",
	} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("owner terminal field %q was dropped: %#v", key, fields)
		}
	}
}

func TestPlanExecuteRecoveryRetryPromptBuildsAnchorRefreshAddendum(t *testing.T) {
	prompt, ok := PlanExecuteRecoveryRetryPromptForSummary("implement atom", PlanExecuteCompletionSummaryFromFields(map[string]any{
		"failure_kind":      "planner_blocked",
		"failure_reason":    "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h",
		"next_valid_action": "retry_plan_execute",
		"required_edit_paths": []any{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
	}))
	if !ok {
		t.Fatal("expected retry prompt")
	}

	for _, want := range []string{
		"implement atom",
		"## Plan-execute Recovery Addendum",
		"native/src/simulation/world/collapse_event.h",
		"First read source for `native/src/simulation/world/collapse_event.h`",
		"then produce one valid `submit_plan`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("retry prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteRecoveryRetryPromptIgnoresHumanBlocker(t *testing.T) {
	if prompt, ok := PlanExecuteRecoveryRetryPromptForSummary("implement atom", PlanExecuteCompletionSummaryFromFields(map[string]any{
		"failure_kind":        "planner_blocked",
		"failure_reason":      "<<QUESTION>>: product owner must clarify scope",
		"next_valid_action":   "answer_blocker",
		"required_edit_paths": []any{"docs/feature.md"},
	})); ok {
		t.Fatalf("unexpected retry prompt for human blocker:\n%s", prompt)
	}
}
