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
		"the next visible action must be exactly one terminal outcome",
		"`Decision: source-satisfied`",
		"`<<QUESTION>>:`",
		"Plan artifact rules",
		"`edits` is an array, never a quoted JSON string",
		"Prefer the smallest executable edit body",
		"use exact `old_text`/`new_text` for small unique replacements",
		"`insert_after_line` inserts small inline non-empty `content` after an existing 1-based line",
		"needs a large insertion anchored to unchanged source lines",
		"unchanged anchor line(s) plus inserted content",
		"Each edit body must be executable final content",
		"Do not submit placeholders, ellipses, `TODO` markers, `existing code` comments",
		"Line-range `content` is the complete replacement for the inclusive range",
		"Canonical `submit_plan` shape",
		`"tool":"submit_plan"`,
		`"insert_after_line":20`,
		`"content":"complete inserted lines\n"`,
		"never placeholders",
		"Required `submit_plan` coverage for this run: 2 required path(s)",
		"After every existing required path has satisfied its source-body obligation, your first successful `submit_plan.edits` array must include at least one real edit for every path above",
		"Do not treat a source read as plan coverage",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("planner addendum missing %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{
		"Prefer numeric line-range edits for existing source/test changes",
		"line-range shape) for non-trivial existing-path edits",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("planner addendum retained stale wording %q:\n%s", stale, got)
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
		"primary evidence ledger",
		"`context_status` (`refresh=true`)",
		"`context_query`",
		"`context_zoom`",
		"`code_navigate`",
		"Do not sweep large files",
		"one bounded `read_file` range",
		"Corrective `submit_plan.edits[]` entries must be executable final content",
		"not review prose",
		"no placeholders, ellipses",
		"`insert_after_line`+`content` only for small inline insertions",
		"large additions",
		"unchanged anchor line(s) plus inserted content",
		"Use `old_text` only for tiny exact snippets",
		"not a quoted JSON string",
		"## Terminal Output Templates",
		"Do not add prose before or after the chosen shape",
		"Pass shape:",
		"Corrective plan shape:",
		"Blocker shape:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("review-repair prompt missing %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{
		"Prefer line-range `content` for non-trivial source/test repairs",
		"Line-range `content` for non-trivial source/test repairs",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("review-repair prompt retained stale wording %q:\n%s", stale, got)
		}
	}
}
