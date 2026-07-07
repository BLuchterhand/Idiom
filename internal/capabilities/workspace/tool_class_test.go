package workspace

import (
	"slices"
	"testing"

	semanticlanguage "idiom/internal/repository/semantic/language"
)

func TestClassifyToolUsesLanguageRegistryForReadTools(t *testing.T) {
	for _, lang := range semanticlanguage.LanguageRegistry() {
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

func TestRuntimeContentInspectionToolUsesWorkspaceToolSurface(t *testing.T) {
	for _, name := range []string{
		ToolNameReadFile,
		ToolNameSearchContent,
		ToolNameCodeNavigate,
		ToolNameReadOnlyCapability,
		"go_read_symbol",
		"go_find_function",
		"cpp_declaration",
		"cpp_who_includes",
		"rust_references",
		"python_diagnostics",
	} {
		if !IsRuntimeContentInspectionTool(name) {
			t.Fatalf("IsRuntimeContentInspectionTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameListFiles,
		ToolNameContextStatus,
		ToolNameContextPack,
		ToolNameSubmitPlan,
		ToolNameWriteFile,
		"mcp:exa/web_search_exa",
	} {
		if IsRuntimeContentInspectionTool(name) {
			t.Fatalf("IsRuntimeContentInspectionTool(%q) = true, want false", name)
		}
	}
}

func TestRuntimeGuidanceToolFamiliesUseWorkspaceTaxonomy(t *testing.T) {
	for _, name := range []string{
		ToolNameSearchContent,
		ToolNameSearchFiles,
		ToolNameRg,
		"go_find_symbol",
		"go_find_type",
		"cpp_declaration",
		"python_definition",
		"ts_references",
		"rust_calls_into",
		"java_related_files",
	} {
		if !IsRuntimeSearchGuidanceTool(name) {
			t.Fatalf("IsRuntimeSearchGuidanceTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameReadFile,
		ToolNameCodeNavigate,
		"go_read_symbol",
		"python_diagnostics",
		ToolNameSubmitPlan,
		ToolNameEditFile,
	} {
		if IsRuntimeSearchGuidanceTool(name) {
			t.Fatalf("IsRuntimeSearchGuidanceTool(%q) = true, want false", name)
		}
	}

	for _, name := range []string{
		ToolNameListFiles,
		ToolNameSearchContent,
		ToolNameSearchFiles,
		ToolNameReadFile,
		ToolNameRg,
	} {
		if !IsRuntimeOrientationGuidanceTool(name) {
			t.Fatalf("IsRuntimeOrientationGuidanceTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameCodeNavigate,
		"go_references",
		ToolNameDiscoveryQuery,
		ToolNameEditFile,
	} {
		if IsRuntimeOrientationGuidanceTool(name) {
			t.Fatalf("IsRuntimeOrientationGuidanceTool(%q) = true, want false", name)
		}
	}
}

func TestRuntimeNarrowToolFamilyUsesLanguageCatalog(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{tool: ToolNameCodeNavigate, want: "code_navigate"},
		{tool: "go_read_symbol", want: "code_navigate"},
		{tool: "go_find_package", want: "code_navigate"},
		{tool: "cpp_declaration", want: "code_navigate"},
		{tool: "rust_references", want: "code_navigate"},
		{tool: ToolNameReadFile, want: "read_file"},
		{tool: ToolNameSearchContent, want: "search"},
		{tool: ToolNameShell, want: "shell"},
		{tool: ToolNameContextQuery, want: ""},
		{tool: ToolNameSubmitPlan, want: ""},
	}

	for _, tc := range tests {
		if got := RuntimeNarrowToolFamily(tc.tool); got != tc.want {
			t.Fatalf("RuntimeNarrowToolFamily(%q) = %q, want %q", tc.tool, got, tc.want)
		}
	}
}

func TestLanguageToolPartsUsesCatalog(t *testing.T) {
	language, suffix, ok := LanguageToolParts("ts_file_outline")
	if !ok {
		t.Fatal("LanguageToolParts(ts_file_outline) did not find catalog entry")
	}
	if language != "typescript" || suffix != "file_outline" {
		t.Fatalf("LanguageToolParts(ts_file_outline) = %q, %q; want typescript, file_outline", language, suffix)
	}

	if _, _, ok := LanguageToolParts("go_fake_tool"); ok {
		t.Fatal("LanguageToolParts(go_fake_tool) found non-catalog tool")
	}
}

func TestSourceBodyInspectionToolUsesLanguageCatalog(t *testing.T) {
	for _, name := range []string{
		ToolNameReadFile,
		"go_read_symbol",
		"go_file_outline",
		"cpp_extract_body",
		"rust_file_outline",
		"java_read_symbol",
	} {
		if !IsSourceBodyInspectionTool(name) {
			t.Fatalf("IsSourceBodyInspectionTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameSearchContent,
		"go_references",
		"cpp_declaration",
		ToolNameContextPack,
		ToolNameWriteFile,
	} {
		if IsSourceBodyInspectionTool(name) {
			t.Fatalf("IsSourceBodyInspectionTool(%q) = true, want false", name)
		}
	}
}

func TestLanguageSourceBodyToolUsesLanguageCatalog(t *testing.T) {
	for _, name := range []string{
		"go_read_symbol",
		"go_extract_body",
		"cpp_read_symbol",
		"cpp_extract_body",
		"java_read_symbol",
	} {
		if !IsLanguageSourceBodyTool(name) {
			t.Fatalf("IsLanguageSourceBodyTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameReadFile,
		"go_file_outline",
		"rust_file_outline",
		"go_references",
		"python_extract_body",
	} {
		if IsLanguageSourceBodyTool(name) {
			t.Fatalf("IsLanguageSourceBodyTool(%q) = true, want false", name)
		}
	}
}

func TestTaskLedgerReadEvidenceToolUsesWorkspaceTaxonomy(t *testing.T) {
	for _, name := range []string{
		ToolNameReadFile,
		ToolNameCodeNavigate,
		ToolNameContextPack,
		ToolNameContextQuery,
		ToolNameDiscoveryQuery,
		ToolNameSearchContent,
		"go_read_symbol",
		"go_file_outline",
		"cpp_extract_body",
		"rust_file_outline",
		"java_read_symbol",
	} {
		if !TaskLedgerReadEvidenceTool(name) {
			t.Fatalf("TaskLedgerReadEvidenceTool(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameSearchFiles,
		ToolNameFileStats,
		ToolNameContextStatus,
		ToolNameHistoryRange,
		ToolNameSourceSanity,
		ToolNameSubmitPlan,
		ToolNameWriteFile,
		"go_references",
		"python_diagnostics",
	} {
		if TaskLedgerReadEvidenceTool(name) {
			t.Fatalf("TaskLedgerReadEvidenceTool(%q) = true, want false", name)
		}
	}
}

func TestToolRequestPathAttachesInstructionsUsesLanguageCatalog(t *testing.T) {
	for _, name := range []string{
		ToolNameReadFile,
		ToolNameEditFile,
		ToolNameCodeNavigate,
		"go_read_symbol",
		"cpp_extract_body",
		"python_diagnostics",
	} {
		if !ToolRequestPathAttachesInstructions(name) {
			t.Fatalf("ToolRequestPathAttachesInstructions(%q) = false, want true", name)
		}
	}

	for _, name := range []string{
		ToolNameSearchContent,
		ToolNameListFiles,
		"go_references",
		ToolNameSubmitPlan,
	} {
		if ToolRequestPathAttachesInstructions(name) {
			t.Fatalf("ToolRequestPathAttachesInstructions(%q) = true, want false", name)
		}
	}
}

func TestBroadDiscoveryToolNamesUsesLanguageCatalog(t *testing.T) {
	got := BroadDiscoveryToolNames()
	for _, want := range []string{
		ToolNameListFiles,
		ToolNameDiscoveryQuery,
		ToolNameDependencyGraph,
		"go_overview",
		"cpp_entrypoints",
		"ts_overview",
		"rust_entrypoints",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("BroadDiscoveryToolNames missing %q from %#v", want, got)
		}
	}

	for _, notBroad := range []string{
		ToolNameReadFile,
		"go_read_symbol",
		"cpp_references",
		ToolNameHistoryRange,
	} {
		if slices.Contains(got, notBroad) {
			t.Fatalf("BroadDiscoveryToolNames includes targeted tool %q in %#v", notBroad, got)
		}
	}
}
