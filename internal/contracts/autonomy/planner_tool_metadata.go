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
	ToolNameBlameRange:         plannerToolMetadata(ToolNameBlameRange, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameCodeNavigate:       plannerToolMetadata(ToolNameCodeNavigate, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameCodingRulesCheck:   plannerToolMetadata(ToolNameCodingRulesCheck, CapabilityShellVerification, SideEffectReadOnly, false),
	ToolNameContextImpact:      plannerToolMetadata(ToolNameContextImpact, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameContextPack:        plannerToolMetadata(ToolNameContextPack, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameContextQuery:       plannerToolMetadata(ToolNameContextQuery, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameContextStatus:      plannerToolMetadata(ToolNameContextStatus, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameContextZoom:        plannerToolMetadata(ToolNameContextZoom, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameDiscoveryQuery:     plannerToolMetadata(ToolNameDiscoveryQuery, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameFileStats:          plannerToolMetadata(ToolNameFileStats, CapabilityBoundedRead, SideEffectReadOnly, true),
	ToolNameHarnessFeedback:    plannerToolMetadata(ToolNameHarnessFeedback, CapabilitySemanticDiscovery, SideEffectAdditive, true),
	ToolNameHistoryRange:       plannerToolMetadata(ToolNameHistoryRange, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameHistorySymbol:      plannerToolMetadata(ToolNameHistorySymbol, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameProductEfforts:     plannerToolMetadata(ToolNameProductEfforts, CapabilityBoundedRead, SideEffectReadOnly, true),
	ToolNameProductStatus:      plannerToolMetadata(ToolNameProductStatus, CapabilityBoundedRead, SideEffectReadOnly, true),
	ToolNameReadFile:           plannerToolMetadata(ToolNameReadFile, CapabilityBoundedRead, SideEffectReadOnly, true),
	ToolNameRecoveryInspect:    plannerToolMetadata(ToolNameRecoveryInspect, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameRecoveryStatus:     plannerToolMetadata(ToolNameRecoveryStatus, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameSystemSearchAccess: plannerToolMetadata(ToolNameSystemSearchAccess, CapabilityBoundedRead, SideEffectAdditive, true),
	ToolNameSearchContent:      plannerToolMetadata(ToolNameSearchContent, CapabilityLiteralSearch, SideEffectReadOnly, true),
	ToolNameSearchFiles:        plannerToolMetadata(ToolNameSearchFiles, CapabilityLiteralSearch, SideEffectReadOnly, true),
	ToolNameStructuralSearch:   plannerToolMetadata(ToolNameStructuralSearch, CapabilitySemanticDiscovery, SideEffectReadOnly, true),
	ToolNameSubmitPlan:         plannerToolMetadata(ToolNameSubmitPlan, CapabilityPlanSubmission, SideEffectAdditive, true),

	ToolNameAppendFile:         plannerToolMetadata(ToolNameAppendFile, CapabilitySourceEdit, SideEffectAdditive, false),
	ToolNameApplyPatch:         plannerToolMetadata(ToolNameApplyPatch, CapabilityApplyPatch, SideEffectAdditive, false),
	ToolNameContextTracePack:   plannerToolMetadata(ToolNameContextTracePack, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameCppDiagnostics:     plannerToolMetadata(ToolNameCppDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameDelegateEdit:       plannerToolMetadata(ToolNameDelegateEdit, CapabilityDelegation, SideEffectAdditive, false),
	ToolNameEditFile:           plannerToolMetadata(ToolNameEditFile, CapabilitySourceEdit, SideEffectAdditive, false),
	ToolNameGoDiagnostics:      plannerToolMetadata(ToolNameGoDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameGoMoveDeclarations: plannerToolMetadata(ToolNameGoMoveDeclarations, CapabilitySourceEdit, SideEffectAdditive, false),
	ToolNameJavaDiagnostics:    plannerToolMetadata(ToolNameJavaDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameMkdir:              plannerToolMetadata(ToolNameMkdir, CapabilitySourceEdit, SideEffectAdditive, false),
	ToolNamePythonDiagnostics:  plannerToolMetadata(ToolNamePythonDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameRecoveryDecompose:  plannerToolMetadata(ToolNameRecoveryDecompose, CapabilityExternalWrite, SideEffectAdditive, false),
	ToolNameRustDiagnostics:    plannerToolMetadata(ToolNameRustDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameShell:              plannerToolMetadata(ToolNameShell, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameSourceSanity:       plannerToolMetadata(ToolNameSourceSanity, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameTask:               plannerToolMetadata(ToolNameTask, CapabilityDelegation, SideEffectAdditive, false),
	ToolNameTaskLedger:         plannerToolMetadata(ToolNameTaskLedger, CapabilityExternalWrite, SideEffectAdditive, false),
	ToolNameTsDiagnostics:      plannerToolMetadata(ToolNameTsDiagnostics, CapabilityShellVerification, SideEffectUnknown, false),
	ToolNameWriteFile:          plannerToolMetadata(ToolNameWriteFile, CapabilitySourceEdit, SideEffectAdditive, false),
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
