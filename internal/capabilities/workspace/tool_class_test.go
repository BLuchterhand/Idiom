package workspace

import "testing"

func TestClassifyToolUsesLanguageRegistryForReadTools(t *testing.T) {
	for _, lang := range LanguageRegistry() {
		for capability, tool := range lang.Tools {
			if got := ClassifyTool(tool); got != CategoryRead {
				t.Fatalf("%s tool %s for %s category = %v, want %v", lang.Name, tool, capability, got, CategoryRead)
			}

			if !IsReadTool(tool) {
				t.Fatalf("%s tool %s for %s should be read-capable", lang.Name, tool, capability)
			}
		}
	}
}

func TestClassifyToolKeepsLegacyLanguageHelpers(t *testing.T) {
	for _, specs := range supplementalLanguageToolSpecs {
		for _, spec := range specs {
			tool := spec.Tool
			if got := ClassifyTool(tool); got != CategoryRead {
				t.Fatalf("legacy language helper %s category = %v, want %v", tool, got, CategoryRead)
			}
		}
	}
}

func TestIsReadClassToolNameUsesPostReadinessEvidencePolicy(t *testing.T) {
	for _, name := range []string{
		ToolNameReadFile,
		ToolNameSearchContent,
		ToolNameDiscoveryQuery,
		"go_read_symbol",
		"java_references",
		ToolNameHistoryRange,
		ToolNameSemgrepScan,
	} {
		if !IsReadClassToolName(name) {
			t.Fatalf("IsReadClassToolName(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameContextPack,
		ToolNameContextStatus,
		ToolNameWriteFile,
		ToolNameSubmitPlan,
		"mcp:exa/web_search_exa",
	} {
		if IsReadClassToolName(name) {
			t.Fatalf("IsReadClassToolName(%q) = true, want false", name)
		}
	}
}
