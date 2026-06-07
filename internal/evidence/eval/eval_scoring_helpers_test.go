package eval

import (
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestWriteToolFromSummary(t *testing.T) {
	cases := []struct {
		summary string
		want    string
	}{
		{summary: "Wrote README.md", want: "write_file"},
		{summary: "Edited internal/evidence/eval/eval.go", want: "edit_file"},
		{summary: "Appended CHANGELOG.md", want: "append_file"},
		{summary: "Read README.md", want: ""},
	}
	for _, tt := range cases {
		t.Run(tt.summary, func(t *testing.T) {
			if got := writeToolFromSummary(tt.summary); got != tt.want {
				t.Fatalf("writeToolFromSummary(%q) = %q, want %q", tt.summary, got, tt.want)
			}
		})
	}
}

func TestAutonomyEventFromDataNormalizesStructuredEvent(t *testing.T) {
	event, ok := autonomyEventFromData(map[string]any{
		"type":        string(autonomy.EventContractCompiled),
		"event_id":    "event-1",
		"contract_id": "contract-1",
	})
	if !ok {
		t.Fatal("expected structured autonomy event")
	}
	if event.Type != autonomy.EventContractCompiled || event.SchemaVersion != autonomy.EventSchemaVersion {
		t.Fatalf("unexpected normalized event: %#v", event)
	}

	if _, ok := autonomyEventFromData(map[string]any{"event_id": "missing-type"}); ok {
		t.Fatal("event without type should not be accepted")
	}
}

func TestMergeStringIntMapsTrimsAndSumsPositiveCounts(t *testing.T) {
	got := mergeStringIntMaps(
		map[string]int{" pack-a ": 1, "ignored": 0},
		map[string]int{"pack-a": 2, "pack-b": 3, "negative": -1},
	)
	if got["pack-a"] != 3 || got["pack-b"] != 3 || len(got) != 2 {
		t.Fatalf("unexpected merged map: %#v", got)
	}
}

func TestPlanExecutionPacketNotUsedDetection(t *testing.T) {
	handoffs := []PlanExecutionHandoffSnapshot{
		{PacketFreshness: " stale "},
		{PacketFreshness: " not_used_by_design "},
	}
	if !planExecutionHasPacketNotUsed(handoffs) {
		t.Fatalf("expected normalized not-used packet freshness to count: %#v", handoffs)
	}
	if !planExecutionHasPacketNotUsed([]PlanExecutionHandoffSnapshot{{PacketNotUsed: true}}) {
		t.Fatal("expected explicit PacketNotUsed flag to count")
	}
}

func TestRoutePlanReasonFromWriterRouteReason(t *testing.T) {
	if got := routePlanReasonFromWriterRouteReason(" route.multi-file-plan-execute "); got != "multi_file_plan_execute" {
		t.Fatalf("routePlanReasonFromWriterRouteReason = %q, want multi_file_plan_execute", got)
	}
}

func TestWorkflowStateMatchesExpectedFields(t *testing.T) {
	actual := WorkflowStateSnapshot{
		Action:              "persist",
		Kind:                "feature",
		Phase:               "awaiting_approval",
		SourceLane:          "architecture",
		TargetRefs:          []string{"internal/evidence/eval/eval.go"},
		EditableTargets:     []string{"internal/evidence/eval/eval.go"},
		ReadOnlyEvidence:    []string{"README.md"},
		EvidencePaths:       []string{"internal/evidence/eval/eval.go"},
		AllowedDestinations: []string{"implement"},
		Verification:        []string{"go test ./internal/evidence/eval"},
		NextActions:         []string{"wait for approval"},
		Resumable:           true,
	}
	resumable := true
	if !workflowStateMatches(actual, WorkflowStateExpectation{
		Action:              "persist",
		Kind:                "feature",
		Phase:               "awaiting_approval",
		SourceLane:          "architecture",
		RequireTargetRefs:   []string{"internal/evidence/eval/eval.go"},
		RequireEditable:     []string{"internal/evidence/eval/eval.go"},
		RequireReadOnly:     []string{"README.md"},
		RequireEvidence:     []string{"internal/evidence/eval/eval.go"},
		RequireDestinations: []string{"implement"},
		RequireVerification: []string{"go test ./internal/evidence/eval"},
		RequireNextActions:  []string{"wait for approval"},
		Resumable:           &resumable,
	}) {
		t.Fatalf("expected workflow state to match")
	}
	if workflowStateMatches(actual, WorkflowStateExpectation{RequireEditable: []string{"missing.go"}}) {
		t.Fatal("workflow state should reject missing required editable target")
	}
}
