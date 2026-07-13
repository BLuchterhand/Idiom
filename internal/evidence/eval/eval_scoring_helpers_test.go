package eval

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
)

var _ = ginkgo.Describe("write tool inference from summaries", func() {
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
		tt := tt
		ginkgo.It(tt.summary, func() {
			{
				got := writeToolFromSummary(tt.summary)
				gomega.Expect(got).To(gomega.BeEquivalentTo(tt.want), fmt.Sprintf("writeToolFromSummary(%q) = %q, want %q", tt.summary, got, tt.want))
			}
		})
	}
})
var _ = ginkgo.It("autonomy event from data normalizes structured event", func() {
	event, ok := autonomyEventFromData(map[string]any{
		"type":        string(autonomy.EventContractCompiled),
		"event_id":    "event-1",
		"contract_id": "contract-1",
	})
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected structured autonomy event"))

	gomega.Expect(event.Type).To(gomega.BeEquivalentTo(autonomy.EventContractCompiled), fmt.Sprintf("unexpected normalized event: %#v", event))
	gomega.Expect(event.SchemaVersion).To(gomega.BeEquivalentTo(autonomy.EventSchemaVersion), fmt.Sprintf("unexpected normalized event: %#v", event))

	{
		_, ok := autonomyEventFromData(map[string]any{"event_id": "missing-type"})
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprint("event without type should not be accepted"))
	}
})
var _ = ginkgo.It("merge string int maps trims and sums positive counts", func() {
	got := mergeStringIntMaps(
		map[string]int{" pack-a ": 1, "ignored": 0},
		map[string]int{"pack-a": 2, "pack-b": 3, "negative": -1},
	)
	gomega.Expect(got["pack-a"]).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected merged map: %#v", got))
	gomega.Expect(got["pack-b"]).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected merged map: %#v", got))
	gomega.Expect(got).To(gomega.HaveLen(2), fmt.Sprintf("unexpected merged map: %#v", got))
})
var _ = ginkgo.It("plan execution packet not used detection", func() {
	handoffs := []PlanExecutionHandoffSnapshot{
		{PacketFreshness: " stale "},
		{PacketFreshness: " not_used_by_design "},
	}
	gomega.Expect(planExecutionHasPacketNotUsed(handoffs)).To(gomega.BeTrue(), fmt.Sprintf("expected normalized not-used packet freshness to count: %#v", handoffs))

	gomega.Expect(planExecutionHasPacketNotUsed([]PlanExecutionHandoffSnapshot{{PacketNotUsed: true}})).To(gomega.BeTrue(), fmt.Sprint("expected explicit PacketNotUsed flag to count"))
})
var _ = ginkgo.It("route plan reason from writer route reason", func() {
	{
		got := routePlanReasonFromWriterRouteReason(" route.multi-file-plan-execute ")
		gomega.Expect(got).To(gomega.BeEquivalentTo("multi_file_plan_execute"), fmt.Sprintf("routePlanReasonFromWriterRouteReason = %q, want multi_file_plan_execute", got))
	}
})
var _ = ginkgo.It("workflow state matches expected fields", func() {
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

	gomega.Expect(workflowStateMatches(actual, WorkflowStateExpectation{
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
	})).To(gomega.BeTrue(), fmt.Sprintf("expected workflow state to match"))

	gomega.Expect(workflowStateMatches(actual, WorkflowStateExpectation{RequireEditable: []string{"missing.go"}})).To(gomega.BeFalse(), fmt.Sprint("workflow state should reject missing required editable target"))
})
