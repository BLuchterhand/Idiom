package autonomy

import (
	"cmp"
	"slices"
	"strings"
)

// PlanExecutePlannerToolMetadata describes planner-facing capabilities
// for a tool. Metadata is derived from the built-in tool surface spec.
type PlanExecutePlannerToolMetadata struct {
	Tool           string
	Capabilities   []Capability
	SideEffect     SideEffectClass
	PlannerAllowed bool
}

var planExecutePlannerToolMetadata = map[string]PlanExecutePlannerToolMetadata{
	"blame_range":       plannerToolMetadata("blame_range", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"code_navigate":     plannerToolMetadata("code_navigate", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"context_impact":    plannerToolMetadata("context_impact", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"context_pack":      plannerToolMetadata("context_pack", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"context_query":     plannerToolMetadata("context_query", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"context_status":    plannerToolMetadata("context_status", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"context_zoom":      plannerToolMetadata("context_zoom", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"discovery_query":   plannerToolMetadata("discovery_query", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"file_stats":        plannerToolMetadata("file_stats", CapabilityBoundedRead, SideEffectReadOnly, true),
	"harness_feedback":  plannerToolMetadata("harness_feedback", CapabilitySemanticDiscovery, SideEffectAdditive, true),
	"history_range":     plannerToolMetadata("history_range", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"history_symbol":    plannerToolMetadata("history_symbol", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"product_efforts":   plannerToolMetadata("product_efforts", CapabilityBoundedRead, SideEffectReadOnly, true),
	"product_status":    plannerToolMetadata("product_status", CapabilityBoundedRead, SideEffectReadOnly, true),
	"read_file":         plannerToolMetadata("read_file", CapabilityBoundedRead, SideEffectReadOnly, true),
	"recovery_inspect":  plannerToolMetadata("recovery_inspect", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"recovery_status":   plannerToolMetadata("recovery_status", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"search_content":    plannerToolMetadata("search_content", CapabilityLiteralSearch, SideEffectReadOnly, true),
	"search_files":      plannerToolMetadata("search_files", CapabilityLiteralSearch, SideEffectReadOnly, true),
	"structural_search": plannerToolMetadata("structural_search", CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	"submit_plan":       plannerToolMetadata("submit_plan", CapabilityPlanSubmission, SideEffectAdditive, true),

	"append_file":          plannerToolMetadata("append_file", CapabilitySourceEdit, SideEffectAdditive, false),
	"apply_patch":          plannerToolMetadata("apply_patch", CapabilityApplyPatch, SideEffectAdditive, false),
	"context_trace_pack":   plannerToolMetadata("context_trace_pack", CapabilityShellVerification, SideEffectUnknown, false),
	"cpp_diagnostics":      plannerToolMetadata("cpp_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"delegate_edit":        plannerToolMetadata("delegate_edit", CapabilityDelegation, SideEffectAdditive, false),
	"edit_file":            plannerToolMetadata("edit_file", CapabilitySourceEdit, SideEffectAdditive, false),
	"go_diagnostics":       plannerToolMetadata("go_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"go_move_declarations": plannerToolMetadata("go_move_declarations", CapabilitySourceEdit, SideEffectAdditive, false),
	"java_diagnostics":     plannerToolMetadata("java_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"mkdir":                plannerToolMetadata("mkdir", CapabilitySourceEdit, SideEffectAdditive, false),
	"python_diagnostics":   plannerToolMetadata("python_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"recovery_decompose":   plannerToolMetadata("recovery_decompose", CapabilityExternalWrite, SideEffectAdditive, false),
	"rust_diagnostics":     plannerToolMetadata("rust_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"shell":                plannerToolMetadata("shell", CapabilityShellVerification, SideEffectUnknown, false),
	"source_sanity":        plannerToolMetadata("source_sanity", CapabilityShellVerification, SideEffectUnknown, false),
	"task":                 plannerToolMetadata("task", CapabilityDelegation, SideEffectAdditive, false),
	"task_ledger":          plannerToolMetadata("task_ledger", CapabilityExternalWrite, SideEffectAdditive, false),
	"ts_diagnostics":       plannerToolMetadata("ts_diagnostics", CapabilityShellVerification, SideEffectUnknown, false),
	"write_file":           plannerToolMetadata("write_file", CapabilitySourceEdit, SideEffectAdditive, false),
}

func plannerToolMetadata(tool string, capability Capability, sideEffect SideEffectClass, plannerAllowed bool) PlanExecutePlannerToolMetadata {
	return PlanExecutePlannerToolMetadata{
		Tool:           tool,
		Capabilities:   []Capability{capability},
		SideEffect:     sideEffect,
		PlannerAllowed: plannerAllowed,
	}
}

func PlanExecutePlannerToolMetadataEntries() []PlanExecutePlannerToolMetadata {
	entries := make([]PlanExecutePlannerToolMetadata, 0, len(planExecutePlannerToolMetadata))
	for _, entry := range planExecutePlannerToolMetadata {
		entries = append(entries, copyPlanExecutePlannerToolMetadata(entry))
	}
	slices.SortFunc(entries, func(a, b PlanExecutePlannerToolMetadata) int {
		return cmp.Compare(a.Tool, b.Tool)
	})
	return entries
}

func PlanExecutePlannerToolNames() []string {
	var tools []string
	for _, entry := range PlanExecutePlannerToolMetadataEntries() {
		if entry.PlannerAllowed {
			tools = append(tools, entry.Tool)
		}
	}
	return tools
}

func LookupPlanExecutePlannerToolMetadata(tool string) (PlanExecutePlannerToolMetadata, bool) {
	entry, ok := planExecutePlannerToolMetadata[strings.TrimSpace(tool)]
	if !ok {
		return PlanExecutePlannerToolMetadata{}, false
	}
	return copyPlanExecutePlannerToolMetadata(entry), true
}

func IsPlanExecutePlannerToolAllowed(tool string) bool {
	entry, ok := LookupPlanExecutePlannerToolMetadata(tool)
	return ok && entry.PlannerAllowed
}

func copyPlanExecutePlannerToolMetadata(entry PlanExecutePlannerToolMetadata) PlanExecutePlannerToolMetadata {
	entry.Capabilities = append([]Capability(nil), entry.Capabilities...)
	return entry
}
