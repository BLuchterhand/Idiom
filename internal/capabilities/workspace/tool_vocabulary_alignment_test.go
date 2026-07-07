package workspace_test

import (
	"slices"
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestPlanExecutePlannerVocabularyAlignsWithWorkspacePolicyMetadata(t *testing.T) {
	export, err := workspace.BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport: %v", err)
	}

	workspacePolicy := make(map[string]workspace.ToolSchemaPolicyExport, len(export.Tools))
	for _, tool := range export.Tools {
		workspacePolicy[tool.Name] = tool.Policy
	}

	for _, entry := range workspace.PlanExecutePlannerToolMetadataEntries() {
		if !entry.PlannerAllowed {
			continue
		}

		policy, ok := workspacePolicy[entry.Tool]
		if !ok {
			t.Fatalf("planner vocabulary tool %q missing from workspace schema export", entry.Tool)
		}

		if len(entry.Capabilities) != 1 {
			t.Fatalf("planner vocabulary tool %q must keep one primary capability for contract projection, got %#v", entry.Tool, entry.Capabilities)
		}

		if !slices.Contains(policy.Capabilities, entry.Capabilities[0]) {
			t.Fatalf("planner vocabulary tool %q capability = %q, workspace policy capabilities = %#v", entry.Tool, entry.Capabilities[0], policy.Capabilities)
		}

		if !slices.Contains(policy.SideEffects, entry.SideEffect) {
			t.Fatalf("planner vocabulary tool %q side effect = %q, workspace policy side effects = %#v", entry.Tool, entry.SideEffect, policy.SideEffects)
		}
	}

	for _, tool := range export.Tools {
		if !slices.Contains(tool.Profiles, "plan_then_execute") {
			continue
		}

		entry, ok := workspace.LookupPlanExecutePlannerToolMetadata(tool.Name)
		if !ok {
			t.Fatalf("workspace planner-profile tool %q missing from workspace planner metadata", tool.Name)
		}
		if !entry.PlannerAllowed {
			t.Fatalf("workspace planner-profile tool %q is not planner-allowed in workspace metadata: %#v", tool.Name, entry)
		}
	}
}
