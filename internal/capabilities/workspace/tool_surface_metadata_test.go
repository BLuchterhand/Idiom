package workspace

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/toolcatalog"
	"idiom/internal/contracts/autonomy"
)

func TestBuiltInToolSurfaceCatalogLookupReturnsClonedMetadata(t *testing.T) {
	first, ok := builtInToolSurfaceCatalog.Lookup(ToolNameReadFile)
	if !ok {
		t.Fatalf("catalog lookup missed %s", ToolNameReadFile)
	}

	first.EvidenceCapabilities = append(first.EvidenceCapabilities, "mutated")
	first.PolicyCapabilities = append(first.PolicyCapabilities, autonomy.CapabilitySourceEdit)
	first.SideEffects = append(first.SideEffects, autonomy.SideEffectDestructive)

	second, ok := builtInToolSurfaceCatalog.Lookup(ToolNameReadFile)
	if !ok {
		t.Fatalf("second catalog lookup missed %s", ToolNameReadFile)
	}

	if slices.Contains(second.EvidenceCapabilities, "mutated") ||
		slices.Contains(second.PolicyCapabilities, autonomy.CapabilitySourceEdit) ||
		slices.Contains(second.SideEffects, autonomy.SideEffectDestructive) {
		t.Fatalf("catalog lookup leaked mutable slices: %#v", second)
	}
}

func TestWorkspaceToolSurfaceListsProjectCatalog(t *testing.T) {
	if !slices.Equal(defaultToolManifestTools(), projectedCatalogTools(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.DefaultManifest
	})) {
		t.Fatalf("default manifest tools no longer project catalog metadata")
	}

	if !slices.Equal(WorkspaceWriteToolNames(), projectedCatalogTools(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return ToolCategory(spec.Category) == CategoryWrite
	})) {
		t.Fatalf("workspace write tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeSourceInspectionToolNames(), projectedCatalogTools(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeSourceInspection
	})) {
		t.Fatalf("runtime source inspection tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeContextDiscoveryToolNames(), projectedCatalogTools(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeContextDiscovery
	})) {
		t.Fatalf("runtime context discovery tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeHistoryInspectionToolNames(), projectedCatalogTools(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeHistoryInspection
	})) {
		t.Fatalf("runtime history inspection tools no longer project catalog metadata")
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

		spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
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

func TestWorkspaceToolSurfaceMetadataKeepsKeySemanticFlags(t *testing.T) {
	readFile, ok := builtInToolSurfaceCatalog.Lookup(ToolNameReadFile)
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

	submitPlan, ok := builtInToolSurfaceCatalog.Lookup(ToolNameSubmitPlan)
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

	harnessAudit, ok := builtInToolSurfaceCatalog.Lookup(ToolNameHarnessAudit)
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

func projectedCatalogTools(match func(toolcatalog.ToolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range builtInToolSurfaceCatalog.Specs() {
		if match(spec) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}
