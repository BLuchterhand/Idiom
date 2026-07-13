package featureplanner

import (
	"fmt"
	"strings"

	plancontract "idiom/internal/contracts/plan"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("plan execute terminal contract decision class", func() {
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
		ginkgo.It(tt.name, func() {
			{
				got := plancontract.DecidePlanExecuteTerminalCompletionFields(tt.fields, nil).Class
				gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("PlanExecuteCompletionTerminalClass = %q, want %q", got, tt.want))
			}

		})
	}
})
var _ = ginkgo.Describe("plan execute terminal contract decision reasons", func() {
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
		ginkgo.It(tt.name, func() {
			got := plancontract.DecidePlanExecuteTerminalCompletionFields(tt.fields, nil)
			gomega.Expect(got.Class).To(gomega.Equal(tt.wantClass), fmt.Sprintf("decision = %#v, want class=%q reason=%q", got, tt.wantClass, tt.wantReason))
			gomega.Expect(got.Reason).To(gomega.Equal(tt.wantReason), fmt.Sprintf("decision = %#v, want class=%q reason=%q", got, tt.wantClass, tt.wantReason))

		})
	}
})
var _ = ginkgo.It("plan execute completion fields preserves exit code for terminal decision", func() {
	output := `{"type":"idiom.plan_execute.terminal_completion","terminal_class":"success","status":"completed","exit_code":1}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := completionFieldInt(fields, "exit_code")
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("exit_code = %#v, want 1; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want %q; fields=%+v", got, plancontract.PlanExecuteTerminalDecisionBlocked, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields uses terminal record as serialization source", func() {
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
	{

		got := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatus)
		gomega.Expect(got).To(gomega.Equal(string(plancontract.PlanExecuteTerminalStatusCompleted)), fmt.Sprintf("status = %q, want terminal record status; fields=%#v", got, fields))
	}
	{

		got := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldFailureKind)
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("failure_kind = %q, want terminal record to suppress stale summary failure; fields=%#v", got, fields))
	}
	{

		got := completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldChangedPaths)
		gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("changed_paths = %#v, want terminal record paths; fields=%#v", got, fields))
		gomega.Expect(got[0]).To(gomega.Equal("a.go"), fmt.Sprintf("changed_paths = %#v, want terminal record paths; fields=%#v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields ignores unknown plan execute event name", func() {
	output := `{"type":"idiom.plan_execute.not_real","terminal_class":"success","status":"completed","phase":"complete","review_status":"passed"}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := CompletionFieldString(fields, "status")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("status = %q, want unknown event ignored; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("decision = %q, want blocked for ignored unknown event; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields blocks invalid plan execute event name", func() {
	output := fmt.Sprintf(`{"type":%q,"terminal_class":"success","status":"completed","phase":"complete","review_status":"passed"}`,
		plancontract.PlanExecuteInvalidEventName(plancontract.PlanExecuteEventVerification))

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("decision = %q, want blocked for invalid event; fields=%+v", got, fields))
	}
	{

		got := completionFieldStringSlice(fields, "validation_failures")
		gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("validation_failures = %#v, want invalid event marker; fields=%+v", got, fields))
		gomega.Expect(got[0]).To(gomega.Equal("invalid_plan_execute_event:verification"), fmt.Sprintf("validation_failures = %#v, want invalid event marker; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields treats source satisfied event as evidence only", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.source_satisfied","source_satisfied_evidence_paths":["pkg/feature.go"],"reason":"planner reported requested final state already present"}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := CompletionFieldString(fields, "status")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("status = %q, want no terminal status from intermediate source_satisfied event; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "outcome")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("outcome = %q, want no terminal outcome from intermediate source_satisfied event; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked without terminal_completion record; fields=%+v", got, fields))
	}
	{

		got := completionFieldStringSlice(fields, "source_satisfied_evidence_paths")
		gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("source satisfied evidence paths = %#v", got))
		gomega.Expect(got[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("source satisfied evidence paths = %#v", got))
	}

})
var _ = ginkgo.It("plan execute completion fields prefers later concrete review repair state", func() {
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
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q; fields=%+v", key, got, want, fields))
		}

	}

	changed, _ := fields["changed_paths"].([]string)
	gomega.Expect(changed).To(gomega.HaveLen(1), fmt.Sprintf("changed_paths = %#v, want active changed path; fields=%+v", fields["changed_paths"], fields))
	gomega.Expect(changed[0]).To(gomega.Equal("internal/surfaces/tui/app/feature.go"), fmt.Sprintf("changed_paths = %#v, want active changed path; fields=%+v", fields["changed_paths"], fields))

})
var _ = ginkgo.It("plan execute completion fields preserves recovery sidecar metadata", func() {
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
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q; fields=%+v", key, got, want, fields))
		}

	}
})
var _ = ginkgo.It("plan execute completion fields later completed state clears earlier failure", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","exit_code":1,"failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact","failure_id":"failure-123","record_path":".idiom/recovery/failures/failure-123.json","packet_path":".idiom/recovery/failure-123/packet.json","next_valid_action":"review_decomposition","validation_failures":["exit_code_nonzero"],"source_sanity":[{"kind":"source_sanity","path":"pkg/feature.go","status":"failed","error":"compile failed"}],"verification_rejects":[{"command":"go test ./pkg","reason":"unsupported_verification_command"}]}`,
		`{"type":"idiom.plan_execute.completed","state_path":".idiom/plan-execute/sessions/active.json","phase":"complete","status":"completed","review_status":"skipped","review_repair_skipped":true}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := fmt.Sprint(fields["status"])
		gomega.Expect(got).To(gomega.Equal("completed"), fmt.Sprintf("status = %q, want completed; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "failure_kind")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("failure_kind = %q, want cleared earlier planning failure; fields=%+v", got, fields))
	}
	{

		got, ok := fields["exit_code"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("exit_code = %#v, want cleared earlier planning failure; fields=%+v", got, fields))
	}
	{

		got, ok := fields["validation_failures"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("validation_failures = %#v, want cleared earlier planning failure; fields=%+v", got, fields))
	}
	{

		got, ok := fields["verification_rejects"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("verification_rejects = %#v, want cleared earlier planning failure; fields=%+v", got, fields))
	}
	{

		got, ok := fields["source_sanity"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("source_sanity = %#v, want cleared earlier planning failure; fields=%+v", got, fields))
	}

	for _, key := range []string{"failure_id", "record_path", "packet_path", "next_valid_action"} {
		{
			got, ok := fields[key]
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("%s = %#v, want cleared earlier planning failure; fields=%+v", key, got, fields))
		}

	}
	{

		got := fmt.Sprint(fields["progress_path"])
		gomega.Expect(got).To(gomega.Equal(".idiom/plan-execute/sessions/active.json"), fmt.Sprintf("progress_path = %q, want active progress path; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionCompleted), fmt.Sprintf("terminal class = %q, want completed; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields keeps terminal completion source satisfied", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.completed","status":"completed","phase":"review_repair"}`,
		`{"type":"idiom.plan_execute.terminal_completion","terminal_class":"success","status":"source-satisfied","outcome":"source_satisfied","phase":"complete","required_edit_paths":["pkg/feature.go"],"source_satisfied_evidence_paths":["pkg/feature.go"],"source_satisfied_evidence":[{"path":"pkg/feature.go","kind":"harness_observed_source"}]}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := CompletionFieldString(fields, "status")
		gomega.Expect(got).To(gomega.Equal("source-satisfied"), fmt.Sprintf("status = %q, want source-satisfied; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionCompleted), fmt.Sprintf("terminal class = %q, want completed; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields preserves scope contract without generic alias", func() {
	output := `{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","scope_contract_id":"scope-123","scope_contract_hash":"scope-hash-123"}`

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()

	for key, want := range map[string]string{
		"scope_contract_id":   "scope-123",
		"scope_contract_hash": "scope-hash-123",
	} {
		{
			got := CompletionFieldString(fields, key)
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q; fields=%+v", key, got, want, fields))
		}

	}
	{

		got := CompletionFieldString(fields, "contract_id")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("contract_id = %q, want empty generic contract id for scope-only record; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "contract_hash")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("contract_hash = %q, want empty generic contract hash for scope-only record; fields=%+v", got, fields))
	}

	fromFields := PlanExecuteCompletionSummaryFromFields(map[string]any{
		"status":              "completed",
		"scope_contract_id":   "scope-fields",
		"scope_contract_hash": "scope-fields-hash",
	}).Fields()
	{
		got := CompletionFieldString(fromFields, "contract_id")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("contract_id = %q, want empty generic contract id for scope-only fields; fields=%+v", got, fromFields))
	}
	{

		got := CompletionFieldString(fromFields, "contract_hash")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("contract_hash = %q, want empty generic contract hash for scope-only fields; fields=%+v", got, fromFields))
	}
	{

		got := CompletionFieldString(fromFields, "scope_contract_id")
		gomega.Expect(got).To(gomega.Equal("scope-fields"), fmt.Sprintf("scope_contract_id = %q, want scope-fields; fields=%+v", got, fromFields))
	}
	{

		got := CompletionFieldString(fromFields, "scope_contract_hash")
		gomega.Expect(got).To(gomega.Equal("scope-fields-hash"), fmt.Sprintf("scope_contract_hash = %q, want scope-fields-hash; fields=%+v", got, fromFields))
	}

})
var _ = ginkgo.It("plan execute completion fields normalizes legacy contract hash to snapshot", func() {
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","contract_id":"parent-contract","contract_hash":"snapshot-hash-1"}`).Fields()
	{

		got := CompletionFieldString(fields, "contract_id")
		gomega.Expect(got).To(gomega.Equal("parent-contract"), fmt.Sprintf("contract_id = %q, want parent-contract; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "snapshot_hash")
		gomega.Expect(got).To(gomega.Equal("snapshot-hash-1"), fmt.Sprintf("snapshot_hash = %q, want legacy contract_hash normalized; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "contract_hash")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("contract_hash = %q, want no parallel contract_hash authority; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion merge and overlay preserve scope contract fields", func() {
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
		{
			got := CompletionFieldString(merged, key)
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("merged %s = %q, want %q; fields=%+v", key, got, want, merged))
		}

	}
	{

		got := CompletionFieldString(merged, "contract_id")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("merged contract_id = %q, want empty generic contract id; fields=%+v", got, merged))
	}
	{

		got := CompletionFieldString(merged, "contract_hash")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("merged contract_hash = %q, want empty generic contract hash; fields=%+v", got, merged))
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
	{

		got := CompletionFieldString(fields, "scope_contract_id")
		gomega.Expect(got).To(gomega.Equal("scope-overlay"), fmt.Sprintf("overlaid scope_contract_id = %q, want scope-overlay; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "scope_contract_hash")
		gomega.Expect(got).To(gomega.Equal("scope-overlay-hash"), fmt.Sprintf("overlaid scope_contract_hash = %q, want scope-overlay-hash; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "contract_id")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("overlaid contract_id = %q, want empty generic contract id; fields=%+v", got, fields))
	}
	{

		got := CompletionFieldString(fields, "contract_hash")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("overlaid contract_hash = %q, want empty generic contract hash; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion merge and overlay preserve nonzero exit code", func() {
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
	{

		got := merged["exit_code"]
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("merged exit_code = %#v, want 1; fields=%+v", got, merged))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(merged, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("merged terminal class = %q, want blocked; fields=%+v", got, merged))
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
	{

		got := fields["exit_code"]
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("overlaid exit_code = %#v, want 1; fields=%+v", got, fields))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("overlaid terminal class = %q, want blocked; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion merge preserves sparse validation failure", func() {
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
	{

		got := completionFieldStringSlice(merged, "validation_failures")
		gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("validation_failures = %#v, want sparse failure preserved; fields=%+v", got, merged))
		gomega.Expect(got[0]).To(gomega.Equal("exit_code_nonzero"), fmt.Sprintf("validation_failures = %#v, want sparse failure preserved; fields=%+v", got, merged))
	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(merged, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked; fields=%+v", got, merged))
	}

})
var _ = ginkgo.It("plan execute completion merge preserves sparse verification reject", func() {
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
	gomega.Expect(rejects).To(gomega.HaveLen(1), fmt.Sprintf("verification_rejects = %#v, want sparse reject preserved; fields=%+v", merged["verification_rejects"], merged))
	gomega.Expect(rejects[0].Command).To(gomega.Equal("go test ./pkg"), fmt.Sprintf("verification_rejects = %#v, want sparse reject preserved; fields=%+v", merged["verification_rejects"], merged))
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(merged, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked; fields=%+v", got, merged))
	}

})
var _ = ginkgo.It("plan execute completion merge preserves sparse source sanity failure", func() {
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
	gomega.Expect(checks).To(gomega.HaveLen(1), fmt.Sprintf("source_sanity = %#v, want sparse failure preserved; fields=%+v", merged["source_sanity"], merged))
	gomega.Expect(checks[0].Path).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("source_sanity = %#v, want sparse failure preserved; fields=%+v", merged["source_sanity"], merged))
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(merged, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked; fields=%+v", got, merged))
	}

})
var _ = ginkgo.It("plan execute completion fields trusts terminal acceptance evidence", func() {
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.terminal_completion","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`).Fields()
	{

		got := fields["acceptance_evidence_trusted"]
		gomega.Expect(got).To(gomega.Equal(true), fmt.Sprintf("acceptance_evidence_trusted = %#v, want true; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("trust observed plan execute completion fields marks producer trust", func() {
	fields := map[string]any{
		"status":                  "completed",
		"terminal_class":          "completed",
		"completion_source":       "tui_plan_execute_returned",
		"acceptance_evidence_ids": []string{"req-1"},
	}

	TrustObservedPlanExecuteCompletionFields(fields)
	{

		got := fields["acceptance_evidence_trusted"]
		gomega.Expect(got).To(gomega.Equal(true), fmt.Sprintf("observed terminal completion should trust acceptance evidence; fields=%+v", fields))
	}

})
var _ = ginkgo.It("trust observed plan execute completion fields does not register failed terminal", func() {
	fields := map[string]any{
		"status":                  "failed",
		"terminal_class":          "failed",
		"acceptance_evidence_ids": []string{"req-1"},
	}

	TrustObservedPlanExecuteCompletionFields(fields)
	{

		got := fields["acceptance_evidence_trusted"]
		gomega.Expect(got).NotTo(gomega.Equal(true), fmt.Sprintf("failed terminal completion should not trust acceptance evidence; fields=%+v", fields))
	}

})
var _ = ginkgo.It("plan execute completion fields does not trust legacy acceptance evidence", func() {
	fields := PlanExecuteCompletionSummaryFromOutput(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","acceptance_evidence_ids":["req-1"]}`).Fields()
	{

		got := fields["acceptance_evidence_trusted"]
		gomega.Expect(got).NotTo(gomega.Equal(true), fmt.Sprintf("legacy completion should not trust acceptance evidence; fields=%+v", fields))
	}

})
var _ = ginkgo.It("plan execute completion summary merge replaces source sanity evidence", func() {
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
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("source_sanity overlay = %#v, want only current terminal evidence; fields=%+v", fields["source_sanity"], fields))
	gomega.Expect(checks).To(gomega.HaveLen(1), fmt.Sprintf("source_sanity overlay = %#v, want only current terminal evidence; fields=%+v", fields["source_sanity"], fields))
	gomega.Expect(checks[0].Path).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("source_sanity overlay = %#v, want only current terminal evidence; fields=%+v", fields["source_sanity"], fields))

})
var _ = ginkgo.It("plan execute completion summary merge replaces acceptance evidence", func() {
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
	{

		_, ok := fields["acceptance_evidence_ids"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("stale acceptance_evidence_ids survived overlay: fields=%+v", fields))
	}
	{

		_, ok := fields["acceptance_evidence_trusted"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("stale acceptance_evidence_trusted survived overlay: fields=%+v", fields))
	}

})
var _ = ginkgo.It("plan execute completion summary merge replaces write allowed paths", func() {
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
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("write_allowed_paths overlay = %#v, want only current terminal envelope; fields=%+v", fields["write_allowed_paths"], fields))
	gomega.Expect(paths).To(gomega.HaveLen(1), fmt.Sprintf("write_allowed_paths overlay = %#v, want only current terminal envelope; fields=%+v", fields["write_allowed_paths"], fields))
	gomega.Expect(paths[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("write_allowed_paths overlay = %#v, want only current terminal envelope; fields=%+v", fields["write_allowed_paths"], fields))

})
var _ = ginkgo.It("plan execute completion summary merge nested failure overrides wrapper completed", func() {
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
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q; fields=%+v", key, got, want, fields))
		}

	}
	{

		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields degraded inherits earlier progress path", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/active.json","phase":"executor","status":"running","changed_paths":["internal/surfaces/tui/app/feature.go"]}`,
		`{"type":"idiom.plan_execute.review_repair_degraded","phase":"review_repair","status":"degraded","review_status":"degraded","failure_kind":"review_model_error","failure_reason":"review model returned invalid JSON"}`,
	}, "\n")

	fields := PlanExecuteCompletionSummaryFromOutput(output).Fields()
	{

		got := fmt.Sprint(fields["progress_path"])
		gomega.Expect(got).To(gomega.Equal(".idiom/plan-execute/sessions/active.json"), fmt.Sprintf("progress_path = %q, want inherited path; fields=%+v", got, fields))
	}

	changed, _ := fields["changed_paths"].([]string)
	gomega.Expect(changed).To(gomega.HaveLen(1), fmt.Sprintf("changed_paths = %#v, want inherited changed path; fields=%+v", fields["changed_paths"], fields))
	gomega.Expect(changed[0]).To(gomega.Equal("internal/surfaces/tui/app/feature.go"), fmt.Sprintf("changed_paths = %#v, want inherited changed path; fields=%+v", fields["changed_paths"], fields))
	{

		got := fmt.Sprint(fields["failure_kind"])
		gomega.Expect(got).To(gomega.Equal("review_model_error"), fmt.Sprintf("failure_kind = %q, want review_model_error; fields=%+v", got, fields))
	}

})
var _ = ginkgo.It("plan execute completion fields preserve owner terminal projection", func() {
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
		{
			_, ok := fields[key]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("owner terminal field %q was dropped: %#v", key, fields))
		}

	}
})
var _ = ginkgo.It("plan execute recovery retry prompt builds anchor refresh addendum", func() {
	prompt, ok := PlanExecuteRecoveryRetryPromptForSummary("implement atom", PlanExecuteCompletionSummaryFromFields(map[string]any{
		"failure_kind":      "planner_blocked",
		"failure_reason":    "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h",
		"next_valid_action": "retry_plan_execute",
		"required_edit_paths": []any{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
	}))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected retry prompt"))

	for _, want := range []string{
		"implement atom",
		"## Plan-execute Recovery Addendum",
		"native/src/simulation/world/collapse_event.h",
		"First read source for `native/src/simulation/world/collapse_event.h`",
		"then produce one valid `submit_plan`",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("retry prompt missing %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("plan execute recovery retry prompt ignores human blocker", func() {
	{
		prompt, ok := PlanExecuteRecoveryRetryPromptForSummary("implement atom", PlanExecuteCompletionSummaryFromFields(map[string]any{
			"failure_kind":        "planner_blocked",
			"failure_reason":      "<<QUESTION>>: product owner must clarify scope",
			"next_valid_action":   "answer_blocker",
			"required_edit_paths": []any{"docs/feature.md"},
		}))
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("unexpected retry prompt for human blocker:\n%s", prompt))
	}

})
