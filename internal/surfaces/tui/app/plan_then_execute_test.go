package app

import (
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/workerruntime"
)

func TestSetForcedTaskFamilyTogglesPlanThenExecutePlannerMode(t *testing.T) {
	// End-to-end: setting the forced task family on the Model must
	// propagate to the workspace.Manager so the manifest swaps
	// per-edit writes for submit_plan. Without this wiring the
	// planner phase has no submit_plan tool to call (the failure
	// mode the first plan-execute Colony run hit on 2026-05-09).
	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Workspace.EnableWrites = true
	cfg.Chat.AttachInstructionFiles = false

	model := New(cfg)
	defer model.Close()

	// Default: planner mode off, edit_file in the manifest, submit_plan absent.
	defaultTools := model.workspace.AllowedTools()
	if !appPlanModeContains(defaultTools, "edit_file") {
		t.Fatalf("default workspace must include edit_file, got %v", defaultTools)
	}
	if appPlanModeContains(defaultTools, "submit_plan") {
		t.Fatalf("default workspace must NOT include submit_plan, got %v", defaultTools)
	}
	if model.workspace.PlanThenExecutePlannerMode() {
		t.Fatalf("planner mode must default to off")
	}

	// Forcing the canonical plan-then-execute task family flips the
	// workspace flag.
	model.SetForcedTaskFamily("plan_then_execute_planner")
	if !model.workspace.PlanThenExecutePlannerMode() {
		t.Fatalf("SetForcedTaskFamily(plan_then_execute_planner) must enable workspace planner mode")
	}
	plannerTools := model.workspace.AllowedTools()
	if !appPlanModeContains(plannerTools, "submit_plan") {
		t.Fatalf("planner-mode manifest must include submit_plan, got %v", plannerTools)
	}
	for _, write := range []string{"edit_file", "apply_patch", "delegate_edit"} {
		if appPlanModeContains(plannerTools, write) {
			t.Fatalf("planner-mode manifest must hide %s, got %v", write, plannerTools)
		}
	}

	// Switching to a different family flips the flag back off.
	model.SetForcedTaskFamily("implementation")
	if model.workspace.PlanThenExecutePlannerMode() {
		t.Fatalf("non-plan-execute family must restore default workspace mode")
	}
	restored := model.workspace.AllowedTools()
	if !appPlanModeContains(restored, "edit_file") {
		t.Fatalf("restored manifest must include edit_file, got %v", restored)
	}
	if appPlanModeContains(restored, "submit_plan") {
		t.Fatalf("restored manifest must NOT include submit_plan, got %v", restored)
	}

	model.SetForcedTaskFamily("plan_then_execute_review_repair")
	if !model.workspace.PlanThenExecutePlannerMode() {
		t.Fatalf("SetForcedTaskFamily(plan_then_execute_review_repair) must enable submit_plan surface")
	}
	reviewTools := model.workspace.AllowedTools()
	if !appPlanModeContains(reviewTools, "submit_plan") {
		t.Fatalf("review-repair manifest must include submit_plan, got %v", reviewTools)
	}
	if appPlanModeContains(reviewTools, "edit_file") {
		t.Fatalf("review-repair manifest must hide per-edit writes, got %v", reviewTools)
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
	}, agent.ModeOrDefault(""))
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
	}, agent.ModeOrDefault(""))
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
	}, agent.ModeOrDefault(""))
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

func TestIsImplementationLikeFamilyIncludesPlanThenExecutePlanner(t *testing.T) {
	// The runtime brief copies several constraints onto implementation-
	// shaped lanes (planner-owned verification, exact-anchor handling,
	// etc.). Plan-then-execute IS implementation-shaped — without this
	// classification the planner phase is treated like a non-write lane
	// and the brief's contract guidance drifts off.
	if !isImplementationLikeFamily("plan_then_execute_planner") {
		t.Fatalf("plan_then_execute_planner must be classified as implementation-like")
	}
}
