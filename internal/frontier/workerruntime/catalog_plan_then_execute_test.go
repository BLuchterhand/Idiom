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
	if spec.ToolProfile != CapabilityGuardedWrite {
		t.Fatalf("planner phase should expose write-capable tool profile (so submit_plan flows through), got %q", spec.ToolProfile)
	}
	if !planThenExecuteToolListContains(spec.RequiredTools, "submit_plan") {
		t.Fatalf("planner phase must require submit_plan in RequiredTools, got %v", spec.RequiredTools)
	}
}

func TestRuntimeBriefForPlanThenExecutePlannerKeepsSubmitPlanInAllowedTools(t *testing.T) {
	available := []string{
		"read_file", "search_content", "code_navigate",
		"submit_plan", "edit_file", "apply_patch",
		"task", "task_ledger",
	}
	brief := RuntimeBriefForTaskFamily("plan_then_execute_planner", "main", available)
	if !planThenExecuteToolListContains(brief.AllowedTools, "submit_plan") {
		t.Fatalf("planner brief must surface submit_plan in AllowedTools, got %v", brief.AllowedTools)
	}
	if !planThenExecuteToolListContains(brief.AllowedTools, "read_file") {
		t.Fatalf("planner brief must surface read_file for evidence gathering, got %v", brief.AllowedTools)
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
