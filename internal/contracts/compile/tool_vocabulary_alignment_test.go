package contractcompile_test

import (
	"slices"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/compile"
)

func TestPlanExecutePlannerVocabularyAlignsWithAutonomyMetadata(t *testing.T) {
	canonicalTools := autonomy.PlanExecutePlannerToolNames()
	compilerTools := contractcompile.PlanExecutePlannerToolNames()

	if !slices.Equal(compilerTools, canonicalTools) {
		t.Fatalf("compiler planner tool names = %#v, want autonomy metadata names %#v", compilerTools, canonicalTools)
	}

	for _, entry := range autonomy.PlanExecutePlannerToolMetadataEntries() {
		if got := contractcompile.IsPlanExecutePlannerToolAllowed(entry.Tool); got != entry.PlannerAllowed {
			t.Fatalf("compiler planner allowed(%q) = %v, want autonomy metadata %v", entry.Tool, got, entry.PlannerAllowed)
		}
	}
}

func TestPlanExecutePlannerVocabularyAlignsWithWorkspacePolicyMetadata(t *testing.T) {
	export, err := workspace.BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport: %v", err)
	}

	workspacePolicy := make(map[string]workspace.ToolSchemaPolicyExport, len(export.Tools))
	for _, tool := range export.Tools {
		workspacePolicy[tool.Name] = tool.Policy
	}

	for _, entry := range autonomy.PlanExecutePlannerToolMetadataEntries() {
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
}
