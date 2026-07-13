package workerruntime

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
)

var _ = ginkgo.It("runtime contract for task family recognizes plan then execute planner", func() {
	spec := RuntimeContractForTaskFamily("plan_then_execute_planner")
	gomega.Expect(spec.TaskFamily != "plan_then_execute_planner").To(gomega.BeFalse(), fmt.Sprintf("expected family preserved, got %q (catalog probably fell back to direct_answer — register the family explicitly)", spec.TaskFamily))

	gomega.Expect(spec.Contract != "implementation_result").To(gomega.BeFalse(), fmt.Sprintf("planner phase should carry implementation_result contract, got %q", spec.Contract))

	gomega.Expect(spec.PermissionProfile != "guarded_workspace_write").To(gomega.BeFalse(), fmt.Sprintf("planner phase needs guarded_workspace_write so the runner doesn't block reads/submit_plan, got %q", spec.PermissionProfile))

	gomega.Expect(spec.ToolProfile != workspace.ToolSurfaceProfilePlanSubmissionPlanner).To(gomega.BeFalse(), fmt.Sprintf("planner phase should expose the plan-submission profile, got %q", spec.ToolProfile))

	gomega.Expect(planThenExecuteToolListContains(spec.RequiredTools, workspace.ToolNameSubmitPlan)).To(gomega.BeTrue(), fmt.Sprintf("planner phase must require submit_plan in RequiredTools, got %v", spec.RequiredTools))

	gomega.Expect(planThenExecuteToolListContains(spec.RequiredTools, workspace.RequiredToolGitHistoryLookup)).To(gomega.BeTrue(), fmt.Sprintf("planner phase must require git history lookup before submit_plan, got %v", spec.RequiredTools))

	gomega.Expect(planThenExecuteToolListContains(spec.RequiredEvidence, string(RequiredEvidenceGitHistoryRationale))).To(gomega.BeTrue(), fmt.Sprintf("planner phase must require git history rationale evidence, got %v", spec.RequiredEvidence))

	gomega.Expect(planThenExecuteToolListContains(spec.SearchPolicy, "required_existing_paths_need_source_body_before_submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner phase search policy must name source-body-before-submit_plan, got %v", spec.SearchPolicy))

	gomega.Expect(planThenExecuteToolListContains(spec.SearchPolicy, "history_required_before_submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner phase search policy must name history-before-submit_plan, got %v", spec.SearchPolicy))

	gomega.Expect(planThenExecuteToolListContains(spec.SearchPolicy, "submit_plan_requires_executable_final_edits")).To(gomega.BeTrue(), fmt.Sprintf("planner phase search policy must name executable submit_plan quality bar, got %v", spec.SearchPolicy))

	gomega.Expect(planThenExecuteToolListContains(spec.SearchPolicy, "trust_preloaded_current_state_anchors")).To(gomega.BeFalse(), fmt.Sprintf("planner phase search policy must not imply anchors satisfy source-body evidence, got %v", spec.SearchPolicy))
})

var _ = ginkgo.It("runtime brief for plan then execute planner keeps submit plan in allowed tools", func() {
	available := []string{
		"read_file", "search_content", "code_navigate", "shell", "git_diff",
		"history_range", "history_symbol", "blame_range",
		"submit_plan", "edit_file", "apply_patch",
		"task", "task_ledger",
		"recovery_inspect", "recovery_status", "recovery_decompose",
		"product_efforts", "product_status",
		"gdscript_trace",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_planner", "main", available)

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner brief must surface submit_plan in AllowedTools, got %v", brief.AllowedTools))

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("planner brief must surface read_file for evidence gathering, got %v", brief.AllowedTools))

	for _, expected := range []string{"history_range", "history_symbol", "blame_range"} {
		gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("planner brief must surface %s for pre-submit history evidence, got %v", expected, brief.AllowedTools))
	}

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "gdscript_trace")).To(gomega.BeTrue(), fmt.Sprintf("planner brief should preserve project discovery externals, got %v", brief.AllowedTools))

	for _, expected := range []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status"} {
		gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("planner brief must surface %s for read-only workflow/status evidence, got %v", expected, brief.AllowedTools))
	}

	for _, forbidden := range []string{"edit_file", "apply_patch", "shell", "git_diff", "task", "task_ledger", "recovery_decompose"} {
		gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("planner brief must not advertise %s; got %v", forbidden, brief.AllowedTools))
	}
})

var _ = ginkgo.It("plan submission planner profile uses workspace planner metadata", func() {
	canonicalTools := workspace.PlanExecutePlannerToolNames()
	manifestTools := workspace.ManifestToolsForSurfaceProfile(workspace.ToolSurfaceProfilePlanSubmissionPlanner)
	for _, tool := range canonicalTools {
		gomega.Expect(planThenExecuteToolListContains(manifestTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("planner manifest missing workspace planner tool %q: manifest=%#v canonical=%#v", tool, manifestTools, canonicalTools))
	}

	for _, tool := range manifestTools {
		gomega.Expect(slices.Contains(canonicalTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("planner manifest exposes non-planner tool %q: manifest=%#v canonical=%#v", tool, manifestTools, canonicalTools))
	}
})

var _ = ginkgo.It("runtime brief for plan then execute planner uses workspace profile completion", func() {
	available := []string{
		"read_file", "search_content", "code_navigate",
		"product_efforts", "product_status",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_planner", "main", available)

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner brief must include workspace-profile-required submit_plan, got %v", brief.AllowedTools))

	for _, expected := range []string{"product_efforts", "product_status"} {
		gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("planner brief lost workspace-profile tool %s, got %v", expected, brief.AllowedTools))
	}
})

var _ = ginkgo.It("runtime contract for plan then execute review repair is review shaped", func() {
	spec := RuntimeContractForTaskFamily("plan_then_execute_review_repair")
	gomega.Expect(spec.TaskFamily != "plan_then_execute_review_repair").To(gomega.BeFalse(), fmt.Sprintf("expected review-repair family preserved, got %q", spec.TaskFamily))

	gomega.Expect(spec.Contract != "review_findings").To(gomega.BeFalse(), fmt.Sprintf("review-repair phase should carry review_findings contract, got %q", spec.Contract))

	gomega.Expect(spec.PermissionProfile != "plan_submission_review").To(gomega.BeFalse(), fmt.Sprintf("review-repair phase should not be generic guarded_workspace_write, got %q", spec.PermissionProfile))

	gomega.Expect(spec.ToolProfile != workspace.ToolSurfaceProfilePlanSubmissionReview).To(gomega.BeFalse(), fmt.Sprintf("review-repair phase should expose read tools plus submit_plan, got %q", spec.ToolProfile))

	gomega.Expect(planThenExecuteToolListContains(spec.RequiredTools, workspace.ToolNameSubmitPlan)).To(gomega.BeFalse(), fmt.Sprintf("review-repair must not require submit_plan when a checklist-backed pass is valid, got %v", spec.RequiredTools))

	gomega.Expect(len(spec.RequiredTools) != 0).To(gomega.BeFalse(), fmt.Sprintf("review-repair must not require unavailable decision tools, got %v", spec.RequiredTools))

	gomega.Expect(planThenExecuteToolListContains(spec.SearchPolicy, "corrective_submit_plan_requires_executable_final_edits")).To(gomega.BeTrue(), fmt.Sprintf("review-repair search policy must name corrective submit_plan quality bar, got %v", spec.SearchPolicy))
})

var _ = ginkgo.It("runtime brief for plan then execute review repair keeps submit plan without writes", func() {
	available := []string{
		"read_file", "search_content", "code_navigate",
		"submit_plan", "edit_file", "apply_patch",
		"task", "task_ledger",
		"ask", "gdscript_trace", "mcp:question/ask",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_review_repair", "review", available)

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("review-repair brief must surface submit_plan for corrective plans, got %v", brief.AllowedTools))

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("review-repair brief must keep read evidence tools, got %v", brief.AllowedTools))

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "edit_file")).To(gomega.BeFalse(), fmt.Sprintf("review-repair brief must not surface per-edit writes, got %v", brief.AllowedTools))
	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "apply_patch")).To(gomega.BeFalse(), fmt.Sprintf("review-repair brief must not surface per-edit writes, got %v", brief.AllowedTools))

	gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, "task")).To(gomega.BeFalse(), fmt.Sprintf("review-repair brief must not surface broad delegation, got %v", brief.AllowedTools))

	for _, forbidden := range []string{"ask", "gdscript_trace", "mcp:question/ask"} {
		gomega.Expect(planThenExecuteToolListContains(brief.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("review-repair brief must not surface external tool %s; got %v", forbidden, brief.AllowedTools))
	}
})

func planThenExecuteToolListContains[T ~string](haystack []T, needle string) bool {
	for _, s := range haystack {
		if string(s) == needle {
			return true
		}
	}

	return false
}
