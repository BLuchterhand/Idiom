package workspace_test

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
)

var _ = ginkgo.Describe("tool vocabulary alignment", func() {
	ginkgo.It("plan execute planner vocabulary aligns with workspace policy metadata", func() {
		export, err := workspace.BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport: %v", err))

		workspacePolicy := make(map[string]workspace.ToolSchemaPolicyExport, len(export.Tools))
		for _, tool := range export.Tools {
			workspacePolicy[tool.Name] = tool.Policy
		}

		for _, entry := range workspace.PlanExecutePlannerToolMetadataEntries() {
			if !entry.PlannerAllowed {
				continue
			}

			policy, ok := workspacePolicy[entry.Tool]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("planner vocabulary tool %q missing from workspace schema export", entry.Tool))
			gomega.Expect(entry.Capabilities).To(gomega.HaveLen(1), fmt.Sprintf("planner vocabulary tool %q must keep one primary capability for contract projection, got %#v", entry.Tool, entry.Capabilities))
			gomega.Expect(slices.Contains(policy.Capabilities, entry.Capabilities[0])).To(gomega.BeTrue(), fmt.Sprintf("planner vocabulary tool %q capability = %q, workspace policy capabilities = %#v", entry.Tool, entry.Capabilities[0], policy.Capabilities))
			gomega.Expect(slices.Contains(policy.SideEffects, entry.SideEffect)).To(gomega.BeTrue(), fmt.Sprintf("planner vocabulary tool %q side effect = %q, workspace policy side effects = %#v", entry.Tool, entry.SideEffect, policy.SideEffects))

		}

		for _, tool := range export.Tools {
			if !slices.Contains(tool.Profiles, "plan_then_execute") {
				continue
			}

			entry, ok := workspace.LookupPlanExecutePlannerToolMetadata(tool.Name)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("workspace planner-profile tool %q missing from workspace planner metadata", tool.Name))
			gomega.Expect(entry.PlannerAllowed).To(gomega.BeTrue(), fmt.Sprintf("workspace planner-profile tool %q is not planner-allowed in workspace metadata: %#v", tool.Name, entry))

		}
	})
})
