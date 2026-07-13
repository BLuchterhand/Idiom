package app

import (
	"context"
	"fmt"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("set forced task family projects plan then execute tool surface", func() {
	t := ginkgo.GinkgoT()
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
	session := newDetachedPromptExecutionSession(context.Background(), model)
	defaultTools := session.workspaceAllowedToolsForTaskFamily(session.forcedTaskFamily)
	gomega.Expect(appPlanModeContains(defaultTools, "edit_file")).To(gomega.BeTrue(), fmt.Sprintf("default workspace must include edit_file, got %v", defaultTools))

	gomega.Expect(appPlanModeContains(defaultTools, "submit_plan")).To(gomega.BeFalse(), fmt.Sprintf("default workspace must NOT include submit_plan, got %v", defaultTools))

	// Forcing the canonical plan-then-execute task family projects the
	// plan-submission planner surface.
	model.SetForcedTaskFamily("plan_then_execute_planner")

	session = newDetachedPromptExecutionSession(context.Background(), model)
	plannerTools := session.workspaceAllowedToolsForTaskFamily(session.forcedTaskFamily)
	gomega.Expect(appPlanModeContains(plannerTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner-profile manifest must include submit_plan, got %v", plannerTools))

	for _, write := range []string{"edit_file", "apply_patch"} {
		gomega.Expect(appPlanModeContains(plannerTools, write)).To(gomega.BeFalse(), fmt.Sprintf("planner-profile manifest must hide %s, got %v", write, plannerTools))
	}

	// Switching to a different family returns to the default workspace surface.
	model.SetForcedTaskFamily("implementation")

	session = newDetachedPromptExecutionSession(context.Background(), model)
	restored := session.workspaceAllowedToolsForTaskFamily(session.forcedTaskFamily)
	gomega.Expect(appPlanModeContains(restored, "edit_file")).To(gomega.BeTrue(), fmt.Sprintf("restored manifest must include edit_file, got %v", restored))

	gomega.Expect(appPlanModeContains(restored, "submit_plan")).To(gomega.BeFalse(), fmt.Sprintf("restored manifest must NOT include submit_plan, got %v", restored))

	model.SetForcedTaskFamily("plan_then_execute_review_repair")

	session = newDetachedPromptExecutionSession(context.Background(), model)
	reviewTools := session.workspaceAllowedToolsForTaskFamily(session.forcedTaskFamily)
	gomega.Expect(appPlanModeContains(reviewTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("review-repair profile must include submit_plan, got %v", reviewTools))

	gomega.Expect(appPlanModeContains(reviewTools, "edit_file")).To(gomega.BeFalse(), fmt.Sprintf("review-repair profile must hide per-edit writes, got %v", reviewTools))
})

func appPlanModeContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("normalize turn route plan recognizes plan then execute planner", func() {
	// Without this case, normalizeTurnRoutePlan falls through to the
	// default fallback and produces a direct_answer / conversation_only
	// route — which strips the brief's allowed_tools to zero. The first
	// two plan-execute Colony runs failed with `allowed_tools_count: 0`
	// for exactly this reason.
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: "plan_then_execute_planner",
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	gomega.Expect(plan.Family).To(gomega.Equal("plan_then_execute_planner"), fmt.Sprintf("normalizer must preserve the family, got %q (probably hit the default fallbackTurnRoute branch)", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("planner phase should run on the implement lane (write-capable contract), got %q", plan.InitialLane))

	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("planner phase should carry implementation_result contract, got %q", plan.Contract))
})

var _ = ginkgo.It("normalize turn route plan recognizes plan then execute review repair", func() {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: "plan_then_execute_review_repair",
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	gomega.Expect(plan.Family).To(gomega.Equal("plan_then_execute_review_repair"), fmt.Sprintf("normalizer must preserve review-repair family, got %q", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("review"), fmt.Sprintf("review-repair phase should run on review lane, got %q", plan.InitialLane))

	gomega.Expect(plan.Contract).To(gomega.Equal("review_findings"), fmt.Sprintf("review-repair phase should carry review_findings contract, got %q", plan.Contract))
})

var _ = ginkgo.It("normalize turn route plan recognizes artifact final answer", func() {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family: workerruntime.ArtifactFinalAnswerTaskFamily,
		Reason: "explicit_task_family",
	}, modepolicy.ModeOrDefault(""))
	gomega.Expect(plan.Family).To(gomega.Equal(workerruntime.ArtifactFinalAnswerTaskFamily), fmt.Sprintf("normalizer must preserve artifact family, got %q", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("artifact final-answer phase should run on main lane, got %q", plan.InitialLane))

	gomega.Expect(plan.Contract).To(gomega.Equal("artifact_final_answer"), fmt.Sprintf("artifact phase should carry artifact_final_answer contract, got %q", plan.Contract))
})

var _ = ginkgo.It("runtime profile mutation capable includes plan then execute planner", func() {
	// The runtime brief copies several constraints onto implementation-
	// shaped lanes (planner-owned verification, exact-anchor handling,
	// etc.). Plan-then-execute IS implementation-shaped — without this
	// classification the planner phase is treated like a non-write lane
	// and the brief's contract guidance drifts off.
	gomega.Expect(workerruntime.TaskFamilyMutationCapable("plan_then_execute_planner")).To(gomega.BeTrue(), fmt.Sprintf("plan_then_execute_planner must be mutation-capable"))
})
