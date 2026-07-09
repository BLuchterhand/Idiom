package languagetools

import (
	"slices"
	"testing"

	"idiom/internal/repository/semantic/language"
)

func TestToolCatalogOwnsLanguageToolAndCodeNavigationCapabilities(t *testing.T) {
	catalog := ToolCatalog()
	if len(catalog) == 0 {
		t.Fatal("language tool catalog should not be empty")
	}

	for _, tc := range []struct {
		tool         string
		language     string
		capability   language.Capability
		supplemental bool
	}{
		{tool: "go_find_function", language: "go", capability: language.CapFindSymbol, supplemental: true},
		{tool: "go_extract_body", language: "go", capability: language.CapExtractBody},
		{tool: "cpp_declaration", language: "cpp", capability: language.CapDefinition, supplemental: true},
		{tool: "rust_file_outline", language: "rust", capability: language.CapFileOutline},
	} {
		spec, ok := ToolSpecForTool(tc.tool)
		if !ok {
			t.Fatalf("%s missing from language tool catalog", tc.tool)
		}
		if spec.Language != tc.language || spec.Capability != tc.capability || spec.Supplemental != tc.supplemental {
			t.Fatalf("%s spec = %#v, want language=%q capability=%q supplemental=%v", tc.tool, spec, tc.language, tc.capability, tc.supplemental)
		}
	}

	for _, nav := range codeNavigationSpecs {
		if nav.Capability == "" {
			t.Fatalf("code navigation spec %q has no semantic capability", nav.Name)
		}

		for _, lang := range language.LanguageRegistry() {
			tool := lang.Tool(nav.Capability)
			if tool == "" {
				continue
			}

			spec, ok := ToolSpecForTool(tool)
			if !ok {
				t.Fatalf("code navigation capability %s routes %s, but catalog has no matching tool", nav.Name, tool)
			}
			if spec.Capability != nav.Capability {
				t.Fatalf("%s capability = %q, want %q from code navigation spec %s", tool, spec.Capability, nav.Capability, nav.Name)
			}
		}
	}

	var names []string
	for _, nav := range codeNavigationSpecs {
		names = append(names, nav.Name)
	}
	if !slices.Equal(CodeNavigationCapabilityNames(), names) {
		t.Fatalf("code navigation capability names = %#v, want catalog names %#v", CodeNavigationCapabilityNames(), names)
	}
}

func TestToolParts(t *testing.T) {
	languageName, suffix, ok := ToolParts("ts_file_outline")
	if !ok {
		t.Fatal("ToolParts(ts_file_outline) did not find catalog entry")
	}
	if languageName != "typescript" || suffix != "file_outline" {
		t.Fatalf("ToolParts(ts_file_outline) = %q, %q; want typescript, file_outline", languageName, suffix)
	}

	if _, _, ok := ToolParts("go_fake_tool"); ok {
		t.Fatal("ToolParts(go_fake_tool) found non-catalog tool")
	}
}
