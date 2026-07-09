package app

import (
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
)

func TestSetForcedTaskFamilyProjectsPlanThenExecuteToolSurface(t *testing.T) {
	// End-to-end: setting the forced task family on the Model must make the
	// prompt/runtime brief use the plan-submission tool surface so the planner
	// phase exposes submit_plan without storing phase state on workspace.Manager.
	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Workspace.EnableWrites = true
	cfg.Chat.AttachInstructionFiles = false

	model := New(cfg)
	defer model.Close()

	// Default: edit_file in the manifest, submit_plan absent.
	defaultTools := model.workspaceAllowedToolsForTaskFamily(model.forcedTaskFamily)
	if !appPlanModeContains(defaultTools, "edit_file") {
		t.Fatalf("default workspace must include edit_file, got %v", defaultTools)
	}

	if appPlanModeContains(defaultTools, "submit_plan") {
		t.Fatalf("default workspace must NOT include submit_plan, got %v", defaultTools)
	}

	// Forcing the canonical plan-then-execute task family projects the
	// plan-submission planner surface.
	model.SetForcedTaskFamily("plan_then_execute_planner")

	plannerTools := model.workspaceAllowedToolsForTaskFamily(model.forcedTaskFamily)
	if !appPlanModeContains(plannerTools, "submit_plan") {
		t.Fatalf("planner-profile manifest must include submit_plan, got %v", plannerTools)
	}

	for _, write := range []string{"edit_file", "apply_patch", "delegate_edit"} {
		if appPlanModeContains(plannerTools, write) {
			t.Fatalf("planner-profile manifest must hide %s, got %v", write, plannerTools)
		}
	}

	// Switching to a different family returns to the default workspace surface.
	model.SetForcedTaskFamily("implementation")

	restored := model.workspaceAllowedToolsForTaskFamily(model.forcedTaskFamily)
	if !appPlanModeContains(restored, "edit_file") {
		t.Fatalf("restored manifest must include edit_file, got %v", restored)
	}

	if appPlanModeContains(restored, "submit_plan") {
		t.Fatalf("restored manifest must NOT include submit_plan, got %v", restored)
	}

	model.SetForcedTaskFamily("plan_then_execute_review_repair")

	reviewTools := model.workspaceAllowedToolsForTaskFamily(model.forcedTaskFamily)
	if !appPlanModeContains(reviewTools, "submit_plan") {
		t.Fatalf("review-repair profile must include submit_plan, got %v", reviewTools)
	}

	if appPlanModeContains(reviewTools, "edit_file") {
		t.Fatalf("review-repair profile must hide per-edit writes, got %v", reviewTools)
	}
}

func appPlanModeContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

func TestNormalizeTurnRoutePlanRecognizesPlanThenExecutePlanner(t *testing.T) {
	// Without this case, normalizeTurnRoutePlan falls through to the
	// default fallback and produces a direct_answer / conversation_only
	// route — which strips the brief's allowed_tools to zero. The first
	// two plan-execute Colony runs failed with `allowed_tools_count: 0`
	// for exactly this reason.
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: "plan_then_execute_planner",
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	if plan.Family != "plan_then_execute_planner" {
		t.Fatalf("normalizer must preserve the family, got %q (probably hit the default fallbackTurnRoute branch)", plan.Family)
	}

	if plan.InitialLane != "implement" {
		t.Fatalf("planner phase should run on the implement lane (write-capable contract), got %q", plan.InitialLane)
	}

	if plan.Contract != "implementation_result" {
		t.Fatalf("planner phase should carry implementation_result contract, got %q", plan.Contract)
	}
}

func TestNormalizeTurnRoutePlanRecognizesPlanThenExecuteReviewRepair(t *testing.T) {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: "plan_then_execute_review_repair",
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	if plan.Family != "plan_then_execute_review_repair" {
		t.Fatalf("normalizer must preserve review-repair family, got %q", plan.Family)
	}

	if plan.InitialLane != "review" {
		t.Fatalf("review-repair phase should run on review lane, got %q", plan.InitialLane)
	}

	if plan.Contract != "review_findings" {
		t.Fatalf("review-repair phase should carry review_findings contract, got %q", plan.Contract)
	}
}

func TestNormalizeTurnRoutePlanRecognizesArtifactFinalAnswer(t *testing.T) {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: workerruntime.ArtifactFinalAnswerTaskFamily,
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	if plan.Family != workerruntime.ArtifactFinalAnswerTaskFamily {
		t.Fatalf("normalizer must preserve artifact family, got %q", plan.Family)
	}

	if plan.InitialLane != "main" {
		t.Fatalf("artifact final-answer phase should run on main lane, got %q", plan.InitialLane)
	}

	if plan.Contract != "artifact_final_answer" {
		t.Fatalf("artifact phase should carry artifact_final_answer contract, got %q", plan.Contract)
	}
}

func TestRuntimeProfileMutationCapableIncludesPlanThenExecutePlanner(t *testing.T) {
	// The runtime brief copies several constraints onto implementation-
	// shaped lanes (planner-owned verification, exact-anchor handling,
	// etc.). Plan-then-execute IS implementation-shaped — without this
	// classification the planner phase is treated like a non-write lane
	// and the brief's contract guidance drifts off.
	if !workerruntime.TaskFamilyMutationCapable("plan_then_execute_planner") {
		t.Fatalf("plan_then_execute_planner must be mutation-capable")
	}
}
