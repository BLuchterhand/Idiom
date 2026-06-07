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
	for _, spec := range supplementalLanguageToolSpecs {
		tool := spec.Tool
		if got := ClassifyTool(tool); got != CategoryRead {
			t.Fatalf("legacy language helper %s category = %v, want %v", tool, got, CategoryRead)
		}
	}
}
