package toolcatalog

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestCatalogLookupAndSpecsReturnClones(t *testing.T) {
	catalog := NewToolSurfaceCatalog([]ToolSurfaceSpec{
		{
			Tool:                 "read_file",
			EvidenceCapabilities: []string{"inspect_file"},
			PolicyCapabilities:   []autonomy.Capability{autonomy.CapabilityBoundedRead},
			SideEffects:          []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		},
	})

	spec, ok := catalog.Lookup("read_file")
	if !ok {
		t.Fatal("expected catalog lookup to find read_file")
	}

	spec.EvidenceCapabilities[0] = "mutated"
	spec.PolicyCapabilities[0] = autonomy.CapabilitySourceEdit
	spec.SideEffects[0] = autonomy.SideEffectDestructive

	next, ok := catalog.Lookup("read_file")
	if !ok {
		t.Fatal("expected second catalog lookup to find read_file")
	}
	if !slices.Equal(next.EvidenceCapabilities, []string{"inspect_file"}) {
		t.Fatalf("lookup evidence capabilities were mutated: %#v", next.EvidenceCapabilities)
	}
	if !slices.Equal(next.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityBoundedRead}) {
		t.Fatalf("lookup policy capabilities were mutated: %#v", next.PolicyCapabilities)
	}
	if !slices.Equal(next.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
		t.Fatalf("lookup side effects were mutated: %#v", next.SideEffects)
	}

	specs := catalog.Specs()
	specs[0].EvidenceCapabilities[0] = "mutated"

	next, _ = catalog.Lookup("read_file")
	if !slices.Equal(next.EvidenceCapabilities, []string{"inspect_file"}) {
		t.Fatalf("spec list mutation leaked into catalog: %#v", next.EvidenceCapabilities)
	}
}

func TestCatalogToolsWherePreservesSeedOrder(t *testing.T) {
	catalog := NewToolSurfaceCatalog([]ToolSurfaceSpec{
		{Tool: "pwd", DefaultManifest: true},
		{Tool: "edit_file"},
		{Tool: "read_file", DefaultManifest: true},
	})

	got := catalog.ToolsWhere(func(spec ToolSurfaceSpec) bool {
		return spec.DefaultManifest
	})

	if !slices.Equal(got, []string{"pwd", "read_file"}) {
		t.Fatalf("ToolsWhere order = %#v", got)
	}
}

func TestCatalogValidateRejectsMissingAndDuplicateTools(t *testing.T) {
	catalog := NewToolSurfaceCatalog([]ToolSurfaceSpec{
		{Tool: "read_file"},
		{Tool: "  "},
		{Tool: "read_file"},
	})

	err := catalog.Validate()
	if err == nil {
		t.Fatal("expected invalid catalog to fail validation")
	}

	message := err.Error()
	for _, want := range []string{
		"tool_surface_specs[1].tool is required",
		`tool_surface_specs[2].tool duplicates tool_surface_specs[0].tool "read_file"`,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected validation error to contain %q, got %q", want, message)
		}
	}
}

func TestMustNewToolSurfaceCatalogPanicsOnInvalidCatalog(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected invalid catalog seed to panic")
		}
	}()

	MustNewToolSurfaceCatalog([]ToolSurfaceSpec{{Tool: ""}})
}
