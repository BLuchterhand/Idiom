package workspace

import (
	"reflect"
	"slices"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestBuiltInToolSurfaceSpecsProjectCatalogUnchanged(t *testing.T) {
	catalogSpecs := builtInToolSurfaceCatalog.Specs()
	if len(builtInToolSurfaceSpecs) != len(catalogSpecs) {
		t.Fatalf("workspace projection count = %d, catalog count = %d", len(builtInToolSurfaceSpecs), len(catalogSpecs))
	}

	for i, catalogSpec := range catalogSpecs {
		projected := builtInToolSurfaceSpecFromCatalog(catalogSpec)
		if !reflect.DeepEqual(builtInToolSurfaceSpecs[i], projected) {
			t.Fatalf("workspace projection[%d] for %s drifted:\ngot  %#v\nwant %#v", i, catalogSpec.Tool, builtInToolSurfaceSpecs[i], projected)
		}

		lookup, ok := BuiltInToolSurfaceMetadata(catalogSpec.Tool)
		if !ok {
			t.Fatalf("catalog-backed lookup missed %s", catalogSpec.Tool)
		}
		if !reflect.DeepEqual(lookup, projected) {
			t.Fatalf("lookup projection for %s drifted:\ngot  %#v\nwant %#v", catalogSpec.Tool, lookup, projected)
		}
	}
}

func TestWorkspaceToolSurfaceListsProjectCatalog(t *testing.T) {
	if !slices.Equal(defaultToolManifestTools(), projectedCatalogTools(func(spec builtInToolSurfaceSpec) bool {
		return spec.DefaultManifest
	})) {
		t.Fatalf("default manifest tools no longer project catalog metadata")
	}

	if !slices.Equal(WorkspaceWriteToolNames(), projectedCatalogTools(func(spec builtInToolSurfaceSpec) bool {
		return spec.Category == CategoryWrite
	})) {
		t.Fatalf("workspace write tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeSourceInspectionToolNames(), projectedCatalogTools(func(spec builtInToolSurfaceSpec) bool {
		return spec.RuntimeSourceInspection
	})) {
		t.Fatalf("runtime source inspection tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeContextDiscoveryToolNames(), projectedCatalogTools(func(spec builtInToolSurfaceSpec) bool {
		return spec.RuntimeContextDiscovery
	})) {
		t.Fatalf("runtime context discovery tools no longer project catalog metadata")
	}

	if !slices.Equal(RuntimeHistoryInspectionToolNames(), projectedCatalogTools(func(spec builtInToolSurfaceSpec) bool {
		return spec.RuntimeHistoryInspection
	})) {
		t.Fatalf("runtime history inspection tools no longer project catalog metadata")
	}
}

func TestWorkspaceToolSurfaceMetadataKeepsKeySemanticFlags(t *testing.T) {
	readFile, ok := BuiltInToolSurfaceMetadata(ToolNameReadFile)
	if !ok {
		t.Fatalf("missing metadata for %s", ToolNameReadFile)
	}
	if readFile.Category != CategoryRead || !readFile.DefaultManifest || !readFile.RuntimeSourceInspection || !readFile.PlannerAllowed || readFile.ActionClass != "read" {
		t.Fatalf("read_file semantic flags drifted: %#v", readFile)
	}
	if !slices.Equal(readFile.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityBoundedRead}) {
		t.Fatalf("read_file policy capabilities drifted: %#v", readFile.PolicyCapabilities)
	}
	if !slices.Equal(readFile.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
		t.Fatalf("read_file side effects drifted: %#v", readFile.SideEffects)
	}

	submitPlan, ok := BuiltInToolSurfaceMetadata(ToolNameSubmitPlan)
	if !ok {
		t.Fatalf("missing metadata for %s", ToolNameSubmitPlan)
	}
	if submitPlan.Category != CategoryVerification || submitPlan.CapabilityClassification != CapabilityClassWriteSideEffect || !submitPlan.PlannerAllowed || submitPlan.ActionClass != "submit_plan" {
		t.Fatalf("submit_plan semantic flags drifted: %#v", submitPlan)
	}
	if !slices.Equal(submitPlan.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityPlanSubmission}) {
		t.Fatalf("submit_plan policy capabilities drifted: %#v", submitPlan.PolicyCapabilities)
	}
	if !slices.Equal(submitPlan.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectAdditive}) {
		t.Fatalf("submit_plan side effects drifted: %#v", submitPlan.SideEffects)
	}

}

func projectedCatalogTools(match func(builtInToolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range builtInToolSurfaceSpecs {
		if match(spec) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}
