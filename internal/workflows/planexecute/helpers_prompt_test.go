package planexecute

import (
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
)

var _ = ginkgo.It("plan execute planner system addendum frontloads planner protocol", func() {
	got := PlanExecutePlannerSystemAddendumWithObligations([]string{"a.go", "b_test.go"}, nil, nil)
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
		"`insert_after_line` with inline content",
		"real required or selected path",
		"never placeholders",
		"Required path coverage for this run: 2 required path(s)",
		"After every existing required path has satisfied its source-body obligation, do not perform additional source reads",
		"history evidence is present for every selected existing edit path",
		"Do not treat a source read or history read as plan coverage",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("planner addendum missing %q:\n%s", want, got))

	}

	for _, stale := range []string{
		"Prefer numeric line-range edits for existing source/test changes",
		"line-range shape) for non-trivial existing-path edits",
		"path/from/required_edit_paths",
		"After any required history evidence is present",
	} {
		gomega.Expect(strings.Contains(got, stale)).To(gomega.BeFalse(), fmt.Sprintf("planner addendum retained stale wording %q:\n%s", stale, got))

	}
})

var _ = ginkgo.It("plan execute review repair prompt carries submit plan quality bar", func() {
	got := PlanExecuteReviewRepairPrompt(
		"Implement behavior.",
		planartifact.PlanArtifact{
			Rationale: "change behavior",
			Edits:     []planartifact.PlanEdit{{Path: "a.go", Purpose: "update behavior"}},
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
		"`replace_all:true` with empty `old_text` and complete `new_text` only for missing/new files",
		"existing source/test files must use bounded anchored edits",
		"not a quoted JSON string",
		"## Terminal Output Templates",
		"Do not add prose before or after the chosen shape",
		"Pass shape:",
		"Corrective plan shape:",
		"Blocker shape:",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("review-repair prompt missing %q:\n%s", want, got))

	}

	for _, stale := range []string{
		"Prefer line-range `content` for non-trivial source/test repairs",
		"Line-range `content` for non-trivial source/test repairs",
		"Whole-file/new-file",
	} {
		gomega.Expect(strings.Contains(got, stale)).To(gomega.BeFalse(), fmt.Sprintf("review-repair prompt retained stale wording %q:\n%s", stale, got))

	}
})

var _ = ginkgo.It("review answer pass contradiction rejects placeholder checklist", func() {
	answer := strings.Join([]string{
		"Decision: pass",
		"",
		"Verification Checklist:",
		"- Required behavior: cite a checked fact from Phase/Diff Evidence.",
	}, "\n")
	if reason := ReviewAnswerPassContradictionReason(answer); reason != "placeholder_checklist_item" {
		ginkgo.Fail(fmt.Sprintf("placeholder checklist should be contradictory, got %q", reason))
	}
})
