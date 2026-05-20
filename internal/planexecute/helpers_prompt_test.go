package planexecute

import (
	"strings"
	"testing"

	"idiom/internal/workspace"
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
		"Each edit body must be executable final content",
		"Do not submit placeholders, ellipses, `TODO` markers, `existing code` comments",
		"Line-range `content` is the complete replacement for the inclusive range",
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

func TestPlanExecuteReviewRepairPromptCarriesSubmitPlanQualityBar(t *testing.T) {
	got := planExecuteReviewRepairPrompt(
		"Implement behavior.",
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits:     []workspace.PlanEdit{{Path: "a.go", Purpose: "update behavior"}},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		ReviewRepairEvidence{},
	)
	for _, want := range []string{
		"Corrective `submit_plan.edits[]` entries must be executable final content",
		"not review prose",
		"no placeholders, ellipses",
		"`old_text` is exact current text",
		"not a quoted JSON string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("review-repair prompt missing %q:\n%s", want, got)
		}
	}
}
