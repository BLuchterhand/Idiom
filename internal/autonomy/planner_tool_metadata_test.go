package autonomy

import (
	"slices"
	"testing"
)

func TestPlanExecutePlannerToolMetadataNamesAllowedSurface(t *testing.T) {
	allowed := PlanExecutePlannerToolNames()
	for _, tool := range []string{
		"search_files",
		"search_content",
		"read_file",
		"file_stats",
		"code_navigate",
		"harness_feedback",
		"recovery_inspect",
		"recovery_status",
		"product_efforts",
		"product_status",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"history_range",
		"history_symbol",
		"blame_range",
		"submit_plan",
	} {
		if !slices.Contains(allowed, tool) {
			t.Fatalf("planner allowed tool names missing %q: %#v", tool, allowed)
		}
		entry, ok := LookupPlanExecutePlannerToolMetadata(tool)
		if !ok {
			t.Fatalf("planner metadata missing allowed tool %q", tool)
		}
		if !entry.PlannerAllowed {
			t.Fatalf("planner metadata marks allowed tool %q as forbidden: %#v", tool, entry)
		}
		if entry.SideEffect != SideEffectReadOnly {
			t.Fatalf("planner allowed tool %q side effect = %q, want read_only", tool, entry.SideEffect)
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
