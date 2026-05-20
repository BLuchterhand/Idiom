package workerruntime

import (
	"testing"
)

func TestRuntimeContractForTaskFamilyRecognizesPlanThenExecutePlanner(t *testing.T) {
	spec := RuntimeContractForTaskFamily("plan_then_execute_planner")
	if spec.TaskFamily != "plan_then_execute_planner" {
		t.Fatalf("expected family preserved, got %q (catalog probably fell back to direct_answer — register the family explicitly)", spec.TaskFamily)
	}
	if spec.Contract != "implementation_result" {
		t.Fatalf("planner phase should carry implementation_result contract, got %q", spec.Contract)
	}
	if spec.PermissionProfile != "guarded_workspace_write" {
		t.Fatalf("planner phase needs guarded_workspace_write so the runner doesn't block reads/submit_plan, got %q", spec.PermissionProfile)
	}
	if spec.ToolProfile != CapabilityPlanSubmissionPlanner {
		t.Fatalf("planner phase should expose the plan-submission profile, got %q", spec.ToolProfile)
	}
	if !planThenExecuteToolListContains(spec.RequiredTools, "submit_plan") {
		t.Fatalf("planner phase must require submit_plan in RequiredTools, got %v", spec.RequiredTools)
	}
	if !planThenExecuteToolListContains(spec.SearchPolicy, "required_existing_paths_need_source_body_before_submit_plan") {
		t.Fatalf("planner phase search policy must name source-body-before-submit_plan, got %v", spec.SearchPolicy)
	}
	if planThenExecuteToolListContains(spec.SearchPolicy, "trust_preloaded_current_state_anchors") {
		t.Fatalf("planner phase search policy must not imply anchors satisfy source-body evidence, got %v", spec.SearchPolicy)
	}
}

func TestRuntimeBriefForPlanThenExecutePlannerKeepsSubmitPlanInAllowedTools(t *testing.T) {
	available := []string{
		"read_file", "search_content", "code_navigate", "shell", "git_diff",
		"submit_plan", "edit_file", "apply_patch",
		"task", "task_ledger",
		"gdscript_trace",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_planner", "main", available)
	if !planThenExecuteToolListContains(brief.AllowedTools, "submit_plan") {
		t.Fatalf("planner brief must surface submit_plan in AllowedTools, got %v", brief.AllowedTools)
	}
	if !planThenExecuteToolListContains(brief.AllowedTools, "read_file") {
		t.Fatalf("planner brief must surface read_file for evidence gathering, got %v", brief.AllowedTools)
	}
	if !planThenExecuteToolListContains(brief.AllowedTools, "gdscript_trace") {
		t.Fatalf("planner brief should preserve project discovery externals, got %v", brief.AllowedTools)
	}
	for _, forbidden := range []string{"edit_file", "apply_patch", "shell", "git_diff", "task", "task_ledger"} {
		if planThenExecuteToolListContains(brief.AllowedTools, forbidden) {
			t.Fatalf("planner brief must not advertise %s; got %v", forbidden, brief.AllowedTools)
		}
	}
}

func TestRuntimeContractForPlanThenExecuteReviewRepairIsReviewShaped(t *testing.T) {
	spec := RuntimeContractForTaskFamily("plan_then_execute_review_repair")
	if spec.TaskFamily != "plan_then_execute_review_repair" {
		t.Fatalf("expected review-repair family preserved, got %q", spec.TaskFamily)
	}
	if spec.Contract != "review_findings" {
		t.Fatalf("review-repair phase should carry review_findings contract, got %q", spec.Contract)
	}
	if spec.PermissionProfile != "plan_submission_review" {
		t.Fatalf("review-repair phase should not be generic guarded_workspace_write, got %q", spec.PermissionProfile)
	}
	if spec.ToolProfile != CapabilityPlanSubmissionReview {
		t.Fatalf("review-repair phase should expose read tools plus submit_plan, got %q", spec.ToolProfile)
	}
	if planThenExecuteToolListContains(spec.RequiredTools, "submit_plan") {
		t.Fatalf("review-repair must not require submit_plan when a checklist-backed pass is valid, got %v", spec.RequiredTools)
	}
	if len(spec.RequiredTools) != 0 {
		t.Fatalf("review-repair must not require unavailable decision tools, got %v", spec.RequiredTools)
	}
}

func TestRuntimeBriefForPlanThenExecuteReviewRepairKeepsSubmitPlanWithoutWrites(t *testing.T) {
	available := []string{
		"read_file", "search_content", "code_navigate",
		"submit_plan", "edit_file", "apply_patch",
		"task", "task_ledger",
		"ask", "gdscript_trace", "mcp:question/ask",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_review_repair", "review", available)
	if !planThenExecuteToolListContains(brief.AllowedTools, "submit_plan") {
		t.Fatalf("review-repair brief must surface submit_plan for corrective plans, got %v", brief.AllowedTools)
	}
	if !planThenExecuteToolListContains(brief.AllowedTools, "read_file") {
		t.Fatalf("review-repair brief must keep read evidence tools, got %v", brief.AllowedTools)
	}
	if planThenExecuteToolListContains(brief.AllowedTools, "edit_file") || planThenExecuteToolListContains(brief.AllowedTools, "apply_patch") {
		t.Fatalf("review-repair brief must not surface per-edit writes, got %v", brief.AllowedTools)
	}
	if planThenExecuteToolListContains(brief.AllowedTools, "task") {
		t.Fatalf("review-repair brief must not surface broad delegation, got %v", brief.AllowedTools)
	}
	for _, forbidden := range []string{"ask", "gdscript_trace", "mcp:question/ask"} {
		if planThenExecuteToolListContains(brief.AllowedTools, forbidden) {
			t.Fatalf("review-repair brief must not surface external tool %s; got %v", forbidden, brief.AllowedTools)
		}
	}
}

func TestBuiltInToolNamesDoNotLookExternal(t *testing.T) {
	for _, tool := range []string{
		"code_navigate",
		"read_only_capability",
		"source_sanity",
		"harness_feedback",
		"context_impact",
		"submit_plan",
		"delegate_edit",
	} {
		if isExternalToolName(tool) {
			t.Fatalf("built-in tool %q must not be classified as a project external", tool)
		}
	}
}

func planThenExecuteToolListContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
