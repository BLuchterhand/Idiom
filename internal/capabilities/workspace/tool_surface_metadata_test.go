package workspace

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestBuiltInToolSurfaceCatalogLookupReturnsClonedMetadata(t *testing.T) {
	first, ok := builtInToolSurfaceCatalog.lookup(ToolNameReadFile)
	if !ok {
		t.Fatalf("catalog lookup missed %s", ToolNameReadFile)
	}

	first.EvidenceCapabilities = append(first.EvidenceCapabilities, "mutated")
	first.PolicyCapabilities = append(first.PolicyCapabilities, autonomy.CapabilitySourceEdit)
	first.SideEffects = append(first.SideEffects, autonomy.SideEffectDestructive)
	first.Facets = append(first.Facets, toolSurfaceFacetBroadDiscovery)

	second, ok := builtInToolSurfaceCatalog.lookup(ToolNameReadFile)
	if !ok {
		t.Fatalf("second catalog lookup missed %s", ToolNameReadFile)
	}

	if slices.Contains(second.EvidenceCapabilities, "mutated") ||
		slices.Contains(second.PolicyCapabilities, autonomy.CapabilitySourceEdit) ||
		slices.Contains(second.SideEffects, autonomy.SideEffectDestructive) ||
		slices.Contains(second.Facets, toolSurfaceFacetBroadDiscovery) {
		t.Fatalf("catalog lookup leaked mutable slices: %#v", second)
	}
}

func TestWorkspaceToolSurfaceListsProjectCatalog(t *testing.T) {
	if !slices.Equal(defaultToolManifestTools(), projectedCatalogTools(func(spec toolSurfaceSpec) bool {
		return spec.DefaultManifest
	})) {
		t.Fatalf("default manifest tools no longer project catalog metadata")
	}

	if !slices.Equal(WorkspaceWriteToolNames(), projectedCatalogTools(func(spec toolSurfaceSpec) bool {
		return ToolCategory(spec.Category) == CategoryWrite
	})) {
		t.Fatalf("workspace write tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeSourceInspectionToolNames(), projectedCatalogTools(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeSourceInspection
	})) {
		t.Fatalf("runtime source inspection tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeContextDiscoveryToolNames(), projectedCatalogTools(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeContextDiscovery
	})) {
		t.Fatalf("runtime context discovery tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeHistoryInspectionToolNames(), projectedCatalogTools(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeHistoryInspection
	})) {
		t.Fatalf("runtime history inspection tools no longer project catalog metadata")
	}

	broadDiscoveryBuiltIns := builtInToolSurfaceToolsByFacet(toolSurfaceFacetBroadDiscovery)
	for _, tool := range broadDiscoveryBuiltIns {
		if !slices.Contains(BroadDiscoveryToolNames(), tool) {
			t.Fatalf("broad discovery built-ins no longer project catalog facet metadata: missing %s", tool)
		}
	}
}

func TestWorkspacePerEditWriteToolNamesOwnsDirectWriteAdapters(t *testing.T) {
	tools := WorkspacePerEditWriteToolNames()
	want := []string{ToolNameEditFile, ToolNameWriteFile, ToolNameAppendFile, ToolNameApplyPatch}
	if !slices.Equal(tools, want) {
		t.Fatalf("per-edit write tools = %#v, want %#v", tools, want)
	}

	for _, tool := range tools {
		if !IsWorkspacePerEditWriteTool(tool) {
			t.Fatalf("per-edit write predicate rejected %q", tool)
		}
		if !IsWorkspaceWriteTool(tool) {
			t.Fatalf("per-edit write tool %q is not in the workspace write catalog", tool)
		}
	}

	for _, broaderWrite := range []string{ToolNameMkdir, ToolNameDelegateEdit, ToolNameGoMoveDeclarations} {
		if IsWorkspacePerEditWriteTool(broaderWrite) {
			t.Fatalf("broader write tool %q must not satisfy per-edit write predicate", broaderWrite)
		}
	}
}

func TestExploreModeWriteBlockUsesWorkspaceWriteCatalog(t *testing.T) {
	writeTools := WorkspaceWriteToolNames()
	if len(writeTools) == 0 {
		t.Fatal("workspace write tool catalog is empty")
	}

	for _, tool := range writeTools {
		reason, blocked := exploreModeWriteBlocked(tool, ModeExplore)
		if !blocked {
			t.Fatalf("explore mode did not block cataloged write tool %q", tool)
		}
		if !strings.Contains(reason, "explore mode is read-only") {
			t.Fatalf("explore mode rejection for %q lost read-only reason: %q", tool, reason)
		}
	}

	for _, tool := range []string{ToolNameReadFile, ToolNameSearchContent, ToolNameSubmitPlan} {
		if reason, blocked := exploreModeWriteBlocked(tool, ModeExplore); blocked {
			t.Fatalf("explore mode should not block non-workspace-write tool %q at dispatch gate: %q", tool, reason)
		}
	}
}

func TestRuntimeReadOnlyInvestigationToolNamesKeepsGeneralLaneDiagnostics(t *testing.T) {
	tools := RuntimeReadOnlyInvestigationToolNames()
	for _, want := range []string{
		ToolNameReadFile,
		ToolNameSearchFiles,
		ToolNameSearchContent,
		ToolNameReadOnlyCapability,
		ToolNameHarnessAudit,
		ToolNameContextStatus,
		ToolNameContextQuery,
		ToolNameContextZoom,
		ToolNameStructuralSearch,
		ToolNameHistoryRange,
		ToolNameHistorySymbol,
		ToolNameBlameRange,
		ToolNameSystemSearchAccess,
		ToolNameTask,
		ToolNameMCPWebFetchExa,
		ToolNameMCPWebSearchExa,
	} {
		if !slices.Contains(tools, want) {
			t.Fatalf("read-only investigation projection missing %s: %#v", want, tools)
		}
	}

	for _, forbidden := range []string{
		ToolNameSourceSanity,
		ToolNameContextPack,
		ToolNameContextImpact,
		ToolNameContextTracePack,
		ToolNameDiscoveryQuery,
		ToolNameSubmitPlan,
		ToolNameShell,
		ToolNameEditFile,
		ToolNameWriteFile,
		ToolNameApplyPatch,
	} {
		if slices.Contains(tools, forbidden) {
			t.Fatalf("read-only investigation projection exposed non-investigation tool %s: %#v", forbidden, tools)
		}
	}
}

func TestRuntimeReadOnlyInvestigationToolActionClassesMatchAutonomy(t *testing.T) {
	for _, tool := range RuntimeReadOnlyInvestigationToolNames() {
		if strings.HasPrefix(tool, "mcp:") {
			continue
		}

		switch got := autonomy.TurnPhaseActionClassForToolClass(tool); got {
		case autonomy.ActionClassRead, autonomy.ActionClassSearch, autonomy.ActionClassGenericProbe:
		default:
			t.Fatalf("%s is in read-only investigation projection with non-investigation action class %q", tool, got)
		}
	}
}

func TestRuntimeReadOnlyInvestigationToolNamesAllowsOnlyReadApprovalAdditiveEffect(t *testing.T) {
	for _, tool := range RuntimeReadOnlyInvestigationToolNames() {
		if strings.HasPrefix(tool, "mcp:") || tool == ToolNameTask {
			continue
		}

		spec, ok := builtInToolSurfaceCatalog.lookup(tool)
		if !ok {
			t.Fatalf("missing metadata for %s", tool)
		}
		if slices.Equal(spec.SideEffects, sideEffectReadOnly) {
			continue
		}
		if tool == ToolNameSystemSearchAccess && slices.Equal(spec.SideEffects, sideEffectReadOnlyAdditive) {
			continue
		}
		t.Fatalf("read-only investigation projection exposed unexpected side effect for %s: %#v", tool, spec.SideEffects)
	}
}

func TestToolPolicyCapabilitiesUsesWorkspaceCatalog(t *testing.T) {
	readFile := ToolPolicyCapabilities(ToolNameReadFile)
	if !slices.Equal(readFile, []autonomy.Capability{autonomy.CapabilityBoundedRead}) {
		t.Fatalf("read_file capabilities = %#v, want bounded_read", readFile)
	}

	rg := ToolPolicyCapabilities(ToolNameRg)
	if !slices.Equal(rg, []autonomy.Capability{autonomy.CapabilityLiteralSearch}) {
		t.Fatalf("rg capabilities = %#v, want literal_search", rg)
	}

	languageTool := ToolPolicyCapabilities("go_read_symbol")
	if !slices.Equal(languageTool, []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}) {
		t.Fatalf("language tool capabilities = %#v, want semantic_discovery", languageTool)
	}

	readFile[0] = autonomy.CapabilitySourceEdit
	if again := ToolPolicyCapabilities(ToolNameReadFile); slices.Contains(again, autonomy.CapabilitySourceEdit) {
		t.Fatalf("ToolPolicyCapabilities leaked mutable catalog slice: %#v", again)
	}
}

func TestPlanExecutePlannerToolMetadataNamesAllowedSurface(t *testing.T) {
	allowed := PlanExecutePlannerToolNames()
	for _, tc := range []struct {
		tool       string
		capability autonomy.Capability
		sideEffect autonomy.SideEffectClass
	}{
		{tool: ToolNameSearchFiles, capability: autonomy.CapabilityLiteralSearch, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameSearchContent, capability: autonomy.CapabilityLiteralSearch, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameReadFile, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameFileStats, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameCodeNavigate, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameHarnessFeedback, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectAdditive},
		{tool: ToolNameRecoveryInspect, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameRecoveryStatus, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameProductEfforts, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameProductStatus, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameContextStatus, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameContextQuery, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameContextZoom, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameContextPack, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameContextImpact, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameDiscoveryQuery, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameStructuralSearch, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameHistoryRange, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameHistorySymbol, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameBlameRange, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
		{tool: ToolNameSystemSearchAccess, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectAdditive},
		{tool: ToolNameSubmitPlan, capability: autonomy.CapabilityPlanSubmission, sideEffect: autonomy.SideEffectAdditive},
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

		if !slices.Equal(entry.Capabilities, []autonomy.Capability{tc.capability}) {
			t.Fatalf("planner allowed tool %q capabilities = %#v, want %q", tc.tool, entry.Capabilities, tc.capability)
		}

		if entry.SideEffect != tc.sideEffect {
			t.Fatalf("planner allowed tool %q side effect = %q, want %q", tc.tool, entry.SideEffect, tc.sideEffect)
		}
	}
}

func TestPlanExecutePlannerToolMetadataMarksRejectedPlannerTools(t *testing.T) {
	for _, tool := range []string{
		ToolNameApplyPatch,
		ToolNameEditFile,
		ToolNameWriteFile,
		ToolNameAppendFile,
		ToolNameDelegateEdit,
		ToolNameMkdir,
		ToolNameGoMoveDeclarations,
		ToolNameShell,
		ToolNameSourceSanity,
		ToolNameContextTracePack,
		ToolNameTask,
		ToolNameTaskLedger,
		ToolNameRecoveryDecompose,
		ToolNameGitDiff,
		ToolNameHarnessAudit,
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

func TestPlanExecutePlannerProjectionToolsOwnsAllowedFilter(t *testing.T) {
	got := PlanExecutePlannerProjectionTools(
		[]string{ToolNameReadFile, ToolNameEditFile, "", ToolNameSubmitPlan},
		[]string{ToolNameReadFile, ToolNameShell, ToolNameHistoryRange},
	)
	want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameHistoryRange}
	if !slices.Equal(got, want) {
		t.Fatalf("planner projection tools = %#v, want %#v", got, want)
	}
}

func TestPlanExecuteKnownPlannerToolNamesPreservesKnownRejectedTools(t *testing.T) {
	got := PlanExecuteKnownPlannerToolNames([]string{
		ToolNameEditFile,
		"not_a_tool",
		ToolNameSubmitPlan,
		ToolNameEditFile,
	})
	want := []string{ToolNameEditFile, ToolNameSubmitPlan}
	if !slices.Equal(got, want) {
		t.Fatalf("known planner tools = %#v, want %#v", got, want)
	}
}

func TestPlanExecutePlannerToolMetadataReturnsDefensiveCopies(t *testing.T) {
	entries := PlanExecutePlannerToolMetadataEntries()
	if len(entries) == 0 || len(entries[0].Capabilities) == 0 {
		t.Fatalf("planner metadata fixture unexpectedly empty: %#v", entries)
	}

	submitPlanIndex := slices.IndexFunc(entries, func(entry PlanExecutePlannerToolMetadata) bool {
		return entry.Tool == ToolNameSubmitPlan
	})
	if submitPlanIndex < 0 {
		t.Fatalf("planner metadata missing submit_plan: %#v", entries)
	}

	entries[submitPlanIndex].Tool = "mutated"
	entries[submitPlanIndex].Capabilities[0] = autonomy.CapabilitySourceEdit

	freshEntryFromEntries := PlanExecutePlannerToolMetadataEntries()[submitPlanIndex]
	if freshEntryFromEntries.Tool == "mutated" || freshEntryFromEntries.Capabilities[0] != autonomy.CapabilityPlanSubmission {
		t.Fatalf("planner metadata entries were mutated through caller-owned slice: %#v", freshEntryFromEntries)
	}

	entry, ok := LookupPlanExecutePlannerToolMetadata(ToolNameSubmitPlan)
	if !ok || len(entry.Capabilities) == 0 {
		t.Fatalf("submit_plan metadata missing: %#v", entry)
	}

	entry.Capabilities[0] = autonomy.CapabilitySourceEdit

	freshEntry, ok := LookupPlanExecutePlannerToolMetadata(ToolNameSubmitPlan)
	if !ok {
		t.Fatal("submit_plan metadata missing after mutation")
	}

	if freshEntry.Capabilities[0] != autonomy.CapabilityPlanSubmission {
		t.Fatalf("lookup metadata was mutated through caller-owned slice: %#v", freshEntry)
	}
}

func TestWorkspaceToolSurfaceMetadataKeepsKeySemanticFlags(t *testing.T) {
	readFile, ok := builtInToolSurfaceCatalog.lookup(ToolNameReadFile)
	if !ok {
		t.Fatalf("missing metadata for %s", ToolNameReadFile)
	}
	if ToolCategory(readFile.Category) != CategoryRead || !readFile.DefaultManifest || !readFile.RuntimeSourceInspection {
		t.Fatalf("read_file semantic flags drifted: %#v", readFile)
	}
	if !slices.Equal(readFile.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityBoundedRead}) {
		t.Fatalf("read_file policy capabilities drifted: %#v", readFile.PolicyCapabilities)
	}
	if !slices.Equal(readFile.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
		t.Fatalf("read_file side effects drifted: %#v", readFile.SideEffects)
	}

	submitPlan, ok := builtInToolSurfaceCatalog.lookup(ToolNameSubmitPlan)
	if !ok {
		t.Fatalf("missing metadata for %s", ToolNameSubmitPlan)
	}
	if ToolCategory(submitPlan.Category) != CategoryVerification || submitPlan.CapabilityClassification != CapabilityClassWriteSideEffect {
		t.Fatalf("submit_plan semantic flags drifted: %#v", submitPlan)
	}
	if !slices.Equal(submitPlan.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityPlanSubmission}) {
		t.Fatalf("submit_plan policy capabilities drifted: %#v", submitPlan.PolicyCapabilities)
	}
	if !slices.Equal(submitPlan.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectAdditive}) {
		t.Fatalf("submit_plan side effects drifted: %#v", submitPlan.SideEffects)
	}

	harnessAudit, ok := builtInToolSurfaceCatalog.lookup(ToolNameHarnessAudit)
	if !ok {
		t.Fatalf("missing metadata for %s", ToolNameHarnessAudit)
	}
	if ToolCategory(harnessAudit.Category) != CategoryRead || harnessAudit.CapabilityClassification != CapabilityClassRouted {
		t.Fatalf("harness_audit semantic flags drifted: %#v", harnessAudit)
	}
	if !slices.Equal(harnessAudit.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}) {
		t.Fatalf("harness_audit policy capabilities drifted: %#v", harnessAudit.PolicyCapabilities)
	}
	if !slices.Equal(harnessAudit.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
		t.Fatalf("harness_audit side effects drifted: %#v", harnessAudit.SideEffects)
	}

}

func projectedCatalogTools(match func(toolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range builtInToolSurfaceCatalog.specsSnapshot() {
		if match(spec) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}
