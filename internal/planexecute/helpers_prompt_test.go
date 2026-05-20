package planexecute

import (
	"strings"
	"testing"
)

func TestPlanExecutePlannerSystemAddendumFrontloadsPlannerProtocol(t *testing.T) {
	got := planExecutePlannerSystemAddendum([]string{"a.go", "b_test.go"}, nil)
	for _, want := range []string{
		"executable plan artifact, not a narrative implementation answer",
		"Visible response protocol",
		"call exactly one source-read tool",
		"Once every existing required_edit_path has satisfied its source-body obligation, call `submit_plan` as the next visible action",
		"Do not answer in prose first",
		"Plan artifact rules",
		"`edits` is an array, never a quoted JSON string",
		"Canonical `submit_plan` shape",
		`"tool":"submit_plan"`,
		"never placeholders",
		"Required `submit_plan` coverage for this run: 2 required path(s)",
		"After every existing required path has satisfied its source-body obligation, your first successful `submit_plan.edits` array must include at least one real edit for every path above",
		"Do not treat a source read as plan coverage",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("planner addendum missing %q:\n%s", want, got)
		}
	}
}
