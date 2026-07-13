package workspace

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("manager plan then execute", func() {
	ginkgo.It("manager plan then execute planner profile projects manifest", func() {
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir(), EnableWrites: true})

		// Default state: per-edit write tools present, submit_plan absent.
		defaultTools := mgr.ToolSurfaceSnapshot().AllowedTools()
		gomega.Expect(planModeManifestContains(defaultTools, "edit_file")).To(gomega.BeTrue(), fmt.Sprintf("default manifest must include edit_file, got %v", defaultTools))
		gomega.Expect(planModeManifestContains(defaultTools, "submit_plan")).To(gomega.BeFalse(), fmt.Sprintf("default manifest must NOT include submit_plan, got %v", defaultTools))

		// Planner profile: submit_plan present, per-edit writes hidden.
		plannerTools := mgr.ToolSurfaceSnapshotForProfile(ToolSurfaceProfilePlanSubmissionPlanner).AllowedTools()
		gomega.Expect(planModeManifestContains(plannerTools, "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("planner-profile manifest must include submit_plan, got %v", plannerTools))

		for _, write := range []string{"edit_file", "write_file", "append_file", "apply_patch", "mkdir", "go_move_declarations"} {
			gomega.Expect(planModeManifestContains(plannerTools, write)).To(gomega.BeFalse(), fmt.Sprintf("planner-profile manifest must hide %s, got %v", write, plannerTools))

		}

		for _, forbidden := range []string{"task", "task_ledger", "pwd", "source_sanity", "git_status", "git_diff", "shell", "context_trace_pack"} {
			gomega.Expect(planModeManifestContains(plannerTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("planner-profile manifest must not advertise %s because the planner guard rejects it, got %v", forbidden, plannerTools))

		}

		// Read-class tools should still be present so the planner can
		// gather evidence before submitting the plan.
		for _, read := range []string{"read_file", "search_content", "code_navigate", "context_pack", "harness_feedback", "recovery_inspect", "recovery_status", "product_efforts", "product_status", "history_range", "history_symbol", "blame_range", "submit_plan"} {
			gomega.Expect(planModeManifestContains(plannerTools, read)).To(gomega.BeTrue(), fmt.Sprintf("planner-profile manifest must keep %s, got %v", read, plannerTools))

		}
		gomega.Expect(planModeManifestContains(plannerTools, "recovery_decompose")).To(gomega.BeFalse(), fmt.Sprintf("planner-profile manifest must not expose recovery_decompose because it writes recovery packets, got %v", plannerTools))

		// Explicit profile projection must not mutate the default surface.
		defaultAgain := mgr.ToolSurfaceSnapshot().AllowedTools()
		gomega.Expect(planModeManifestContains(defaultAgain, "edit_file")).To(gomega.BeTrue(), fmt.Sprintf("planner profile projection must leave default manifest unchanged, got %v", defaultAgain))
		gomega.Expect(planModeManifestContains(defaultAgain, "submit_plan")).To(gomega.BeFalse(), fmt.Sprintf("planner profile projection must leave default manifest unchanged, got %v", defaultAgain))

	})
	ginkgo.It("plan then execute planner manifest uses workspace planner metadata", func() {
		got := ToolManifestTools(ToolManifestToolOptions{IncludePlanThenExecute: true, PlanThenExecutePlannerSurface: true})
		want := PlanExecutePlannerToolNames()
		slices.Sort(got)
		slices.Sort(want)
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("workspace planner manifest names = %#v, want planner metadata names %#v", got, want))

	})
})

func planModeManifestContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}
