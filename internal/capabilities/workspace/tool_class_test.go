package workspace

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("tool class", func() {
	ginkgo.It("classify tool uses language registry for read tools", func() {
		for _, lang := range semanticlanguage.LanguageRegistry() {
			for capability, tool := range lang.Tools {
				if got := ClassifyTool(tool); got != CategoryRead {
					ginkgo.Fail(fmt.Sprintf("%s tool %s for %s category = %v, want %v", lang.Name, tool, capability, got, CategoryRead))
				}
				gomega.Expect(IsReadTool(tool)).To(gomega.BeTrue(), fmt.Sprintf("%s tool %s for %s should be read-capable", lang.Name, tool, capability))

			}
		}
	})
	ginkgo.It("classify tool keeps legacy language helpers", func() {
		for _, spec := range languagetools.ToolCatalog() {
			if !spec.Supplemental {
				continue
			}

			tool := spec.Tool
			if got := ClassifyTool(tool); got != CategoryRead {
				ginkgo.Fail(fmt.Sprintf("supplemental language tool %s category = %v, want %v", tool, got, CategoryRead))
			}
		}
	})
	ginkgo.It("search tool name includes workspace search and legacy grep", func() {
		for _, name := range []string{ToolNameSearchFiles, ToolNameSearchContent, ToolNameRg, "grep"} {
			gomega.Expect(IsSearchToolName(name)).To(gomega.BeTrue(), fmt.Sprintf("IsSearchToolName(%q) = false, want true", name))

		}

		for _, name := range []string{ToolNameReadFile, ToolNameContextQuery, ToolNameCodeNavigate, ToolNameShell, "go_workspace_symbol"} {
			gomega.Expect(IsSearchToolName(name)).To(gomega.BeFalse(), fmt.Sprintf("IsSearchToolName(%q) = true, want false", name))

		}
	})
	ginkgo.It("implementation attempt tool uses workspace action classes", func() {
		for _, tool := range []string{
			ToolNameEditFile,
			ToolNameWriteFile,
			ToolNameAppendFile,
			ToolNameApplyPatch,
			ToolNameMkdir,
			ToolNameGoMoveDeclarations,
			ToolNameSubmitPlan,
		} {
			gomega.Expect(IsImplementationAttemptTool(tool)).To(gomega.BeTrue(), fmt.Sprintf("implementation attempt predicate rejected %q with action class %q", tool, ToolActionClass(tool)))

		}

		for _, tool := range []string{
			ToolNameReadFile,
			ToolNameSearchContent,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameTask,
			ToolNameRecoveryDecompose,
			ToolNameGoDiagnostics,
		} {
			gomega.Expect(IsImplementationAttemptTool(tool)).To(gomega.BeFalse(), fmt.Sprintf("implementation attempt predicate accepted %q with action class %q", tool, ToolActionClass(tool)))

		}

		if got := ToolActionClass(ToolNameSubmitPlan); got != autonomy.ActionClassSubmitPlan {
			ginkgo.Fail(fmt.Sprintf("submit_plan action class = %q, want %q", got, autonomy.ActionClassSubmitPlan))
		}
	})
	ginkgo.It("is read class tool name uses post readiness evidence policy", func() {
		for _, name := range []string{
			ToolNameReadFile,
			ToolNameSearchContent,
			ToolNameDiscoveryQuery,
			"go_read_symbol",
			"java_references",
			ToolNameHistoryRange,
			ToolNameSemgrepScan,
		} {
			gomega.Expect(IsReadClassToolName(name)).To(gomega.BeTrue(), fmt.Sprintf("IsReadClassToolName(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameContextPack,
			ToolNameContextStatus,
			ToolNameWriteFile,
			ToolNameSubmitPlan,
			"mcp:exa/web_search_exa",
		} {
			gomega.Expect(IsReadClassToolName(name)).To(gomega.BeFalse(), fmt.Sprintf("IsReadClassToolName(%q) = true, want false", name))

		}
	})
	ginkgo.It("runtime content inspection tool uses workspace tool surface", func() {
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
			gomega.Expect(IsRuntimeContentInspectionTool(name)).To(gomega.BeTrue(), fmt.Sprintf("IsRuntimeContentInspectionTool(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameListFiles,
			ToolNameContextStatus,
			ToolNameContextPack,
			ToolNameSubmitPlan,
			ToolNameWriteFile,
			"mcp:exa/web_search_exa",
		} {
			gomega.Expect(IsRuntimeContentInspectionTool(name)).To(gomega.BeFalse(), fmt.Sprintf("IsRuntimeContentInspectionTool(%q) = true, want false", name))

		}
	})
	ginkgo.It("runtime guidance tool families use workspace taxonomy", func() {
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
			gomega.Expect(IsRuntimeSearchGuidanceTool(name)).To(gomega.BeTrue(), fmt.Sprintf("IsRuntimeSearchGuidanceTool(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameReadFile,
			ToolNameCodeNavigate,
			"go_read_symbol",
			"python_diagnostics",
			ToolNameSubmitPlan,
			ToolNameEditFile,
		} {
			gomega.Expect(IsRuntimeSearchGuidanceTool(name)).To(gomega.BeFalse(), fmt.Sprintf("IsRuntimeSearchGuidanceTool(%q) = true, want false", name))

		}

		for _, name := range []string{
			ToolNameListFiles,
			ToolNameSearchContent,
			ToolNameSearchFiles,
			ToolNameReadFile,
			ToolNameRg,
		} {
			gomega.Expect(IsRuntimeOrientationGuidanceTool(name)).To(gomega.BeTrue(), fmt.Sprintf("IsRuntimeOrientationGuidanceTool(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameCodeNavigate,
			"go_references",
			ToolNameDiscoveryQuery,
			ToolNameEditFile,
		} {
			gomega.Expect(IsRuntimeOrientationGuidanceTool(name)).To(gomega.BeFalse(), fmt.Sprintf("IsRuntimeOrientationGuidanceTool(%q) = true, want false", name))

		}
	})
	ginkgo.It("runtime narrow tool family uses language catalog", func() {
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
				ginkgo.Fail(fmt.Sprintf("RuntimeNarrowToolFamily(%q) = %q, want %q", tc.tool, got, tc.want))
			}
		}
	})
	ginkgo.It("source body inspection tool uses language catalog", func() {
		for _, name := range []string{
			ToolNameReadFile,
			"go_read_symbol",
			"go_file_outline",
			"cpp_extract_body",
			"rust_file_outline",
			"java_read_symbol",
		} {
			gomega.Expect(IsSourceBodyInspectionTool(name)).To(gomega.BeTrue(), fmt.Sprintf("IsSourceBodyInspectionTool(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameSearchContent,
			"go_references",
			"cpp_declaration",
			ToolNameContextPack,
			ToolNameWriteFile,
		} {
			gomega.Expect(IsSourceBodyInspectionTool(name)).To(gomega.BeFalse(), fmt.Sprintf("IsSourceBodyInspectionTool(%q) = true, want false", name))

		}
	})
	ginkgo.It("language source body tool uses language catalog", func() {
		for _, name := range []string{
			"go_read_symbol",
			"go_extract_body",
			"cpp_read_symbol",
			"cpp_extract_body",
			"java_read_symbol",
		} {
			gomega.Expect(IsLanguageSourceBodyTool(name)).To(gomega.BeTrue(), fmt.Sprintf("IsLanguageSourceBodyTool(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameReadFile,
			"go_file_outline",
			"rust_file_outline",
			"go_references",
			"python_extract_body",
		} {
			gomega.Expect(IsLanguageSourceBodyTool(name)).To(gomega.BeFalse(), fmt.Sprintf("IsLanguageSourceBodyTool(%q) = true, want false", name))

		}
	})
	ginkgo.It("task ledger read evidence tool uses workspace taxonomy", func() {
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
			gomega.Expect(TaskLedgerReadEvidenceTool(name)).To(gomega.BeTrue(), fmt.Sprintf("TaskLedgerReadEvidenceTool(%q) = false, want true", name))

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
			gomega.Expect(TaskLedgerReadEvidenceTool(name)).To(gomega.BeFalse(), fmt.Sprintf("TaskLedgerReadEvidenceTool(%q) = true, want false", name))

		}
	})
	ginkgo.It("tool request path attaches instructions uses language catalog", func() {
		for _, name := range []string{
			ToolNameReadFile,
			ToolNameEditFile,
			ToolNameCodeNavigate,
			"go_read_symbol",
			"cpp_extract_body",
			"python_diagnostics",
		} {
			gomega.Expect(ToolRequestPathAttachesInstructions(name)).To(gomega.BeTrue(), fmt.Sprintf("ToolRequestPathAttachesInstructions(%q) = false, want true", name))

		}

		for _, name := range []string{
			ToolNameSearchContent,
			ToolNameListFiles,
			"go_references",
			ToolNameSubmitPlan,
		} {
			gomega.Expect(ToolRequestPathAttachesInstructions(name)).To(gomega.BeFalse(), fmt.Sprintf("ToolRequestPathAttachesInstructions(%q) = true, want false", name))

		}
	})
	ginkgo.It("broad discovery tool names uses language catalog", func() {
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
			gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("BroadDiscoveryToolNames missing %q from %#v", want, got))

		}

		for _, notBroad := range []string{
			ToolNameReadFile,
			"go_read_symbol",
			"cpp_references",
			ToolNameHistoryRange,
		} {
			gomega.Expect(slices.Contains(got, notBroad)).To(gomega.BeFalse(), fmt.Sprintf("BroadDiscoveryToolNames includes targeted tool %q in %#v", notBroad, got))

		}
	})
})
