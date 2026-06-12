package autonomy

import (
	"slices"
	"testing"
)

func TestPlanExecutePlannerToolMetadataNamesAllowedSurface(t *testing.T) {
	allowed := PlanExecutePlannerToolNames()
	for _, tc := range []struct {
		tool       string
		capability Capability
		sideEffect SideEffectClass
	}{
		{tool: "search_files", capability: CapabilityLiteralSearch, sideEffect: SideEffectReadOnly},
		{tool: "search_content", capability: CapabilityLiteralSearch, sideEffect: SideEffectReadOnly},
		{tool: "read_file", capability: CapabilityBoundedRead, sideEffect: SideEffectReadOnly},
		{tool: "file_stats", capability: CapabilityBoundedRead, sideEffect: SideEffectReadOnly},
		{tool: "code_navigate", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "harness_feedback", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectAdditive},
		{tool: "recovery_inspect", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "recovery_status", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "product_efforts", capability: CapabilityBoundedRead, sideEffect: SideEffectReadOnly},
		{tool: "product_status", capability: CapabilityBoundedRead, sideEffect: SideEffectReadOnly},
		{tool: "context_status", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "context_query", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "context_zoom", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "context_pack", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "context_impact", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "discovery_query", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "structural_search", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "history_range", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "history_symbol", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "blame_range", capability: CapabilitySemanticDiscovery, sideEffect: SideEffectReadOnly},
		{tool: "submit_plan", capability: CapabilityPlanSubmission, sideEffect: SideEffectAdditive},
	} {
		if !slices.Contains(allowed, tc.tool) {
			t.Fatalf("planner allowed tool names missing %q: %#v", tc.tool, allowed)
		}

		entry, ok := LookupPlanExecutePlannerToolMetadata(tc.tool)
		if !ok {
			t.Fatalf("planner metadata missing allowed tool %q", tc.tool)
		}

		if !entry.PlannerAllowed {
			t.Fatalf("planner metadata marks allowed tool %q as forbidden: %#v", tc.tool, entry)
		}

		if !slices.Equal(entry.Capabilities, []Capability{tc.capability}) {
			t.Fatalf("planner allowed tool %q capabilities = %#v, want %q", tc.tool, entry.Capabilities, tc.capability)
		}

		if entry.SideEffect != tc.sideEffect {
			t.Fatalf("planner allowed tool %q side effect = %q, want %q", tc.tool, entry.SideEffect, tc.sideEffect)
		}
	}
}

func TestPlanExecutePlannerToolMetadataMarksRejectedPlannerTools(t *testing.T) {
	for _, tool := range []string{
		"apply_patch",
		"edit_file",
		"write_file",
		"append_file",
		"delegate_edit",
		"mkdir",
		"go_move_declarations",
		"shell",
		"source_sanity",
		"context_trace_pack",
		"task",
		"task_ledger",
		"recovery_decompose",
	} {
		entry, ok := LookupPlanExecutePlannerToolMetadata(tool)
		if !ok {
			t.Fatalf("planner metadata missing rejected tool %q", tool)
		}

		if entry.PlannerAllowed {
			t.Fatalf("planner metadata allows rejected tool %q: %#v", tool, entry)
		}

		if slices.Contains(PlanExecutePlannerToolNames(), tool) {
			t.Fatalf("planner allowed tool names contain rejected tool %q", tool)
		}
	}
}

func TestPlanExecutePlannerToolMetadataReturnsDefensiveCopies(t *testing.T) {
	entries := PlanExecutePlannerToolMetadataEntries()
	if len(entries) == 0 || len(entries[0].Capabilities) == 0 {
		t.Fatalf("planner metadata fixture unexpectedly empty: %#v", entries)
	}

	submitPlanIndex := slices.IndexFunc(entries, func(entry PlanExecutePlannerToolMetadata) bool {
		return entry.Tool == "submit_plan"
	})
	if submitPlanIndex < 0 {
		t.Fatalf("planner metadata missing submit_plan: %#v", entries)
	}

	entries[submitPlanIndex].Tool = "mutated"
	entries[submitPlanIndex].Capabilities[0] = CapabilitySourceEdit

	freshEntryFromEntries := PlanExecutePlannerToolMetadataEntries()[submitPlanIndex]
	if freshEntryFromEntries.Tool == "mutated" || freshEntryFromEntries.Capabilities[0] != CapabilityPlanSubmission {
		t.Fatalf("planner metadata entries were mutated through caller-owned slice: %#v", freshEntryFromEntries)
	}

	entry, ok := LookupPlanExecutePlannerToolMetadata("submit_plan")
	if !ok || len(entry.Capabilities) == 0 {
		t.Fatalf("submit_plan metadata missing: %#v", entry)
	}

	entry.Capabilities[0] = CapabilitySourceEdit

	freshEntry, ok := LookupPlanExecutePlannerToolMetadata("submit_plan")

	if !ok {
		t.Fatal("submit_plan metadata missing after mutation")
	}

	if freshEntry.Capabilities[0] != CapabilityPlanSubmission {
		t.Fatalf("lookup metadata was mutated through caller-owned slice: %#v", freshEntry)
	}
}
