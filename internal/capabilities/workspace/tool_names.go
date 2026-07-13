package workspace

// ToolName constants for all built-in workspace tools.
// Keep this list synchronized with builtInToolSurfaceCatalog().
const (
	ToolNameAppendFile         = "append_file"
	ToolNameApplyPatch         = "apply_patch"
	ToolNameBlameRange         = "blame_range"
	ToolNameCodeNavigate       = "code_navigate"
	ToolNameCodingRulesCheck   = "coding_rules_check"
	ToolNameContextImpact      = "context_impact"
	ToolNameContextIndex       = "context_index"
	ToolNameContextPack        = "context_pack"
	ToolNameContextQuery       = "context_query"
	ToolNameContextStatus      = "context_status"
	ToolNameContextTracePack   = "context_trace_pack"
	ToolNameContextZoom        = "context_zoom"
	ToolNameCtagsSymbols       = "ctags_symbols"
	ToolNameDependencyGraph    = "dependency_graph"
	ToolNameDiscoveryQuery     = "discovery_query"
	ToolNameEditFile           = "edit_file"
	ToolNameFileStats          = "file_stats"
	ToolNameGitDiff            = "git_diff"
	ToolNameGitStatus          = "git_status"
	ToolNameHarnessAudit       = "harness_audit"
	ToolNameHarnessFeedback    = "harness_feedback"
	ToolNameHistoryRange       = "history_range"
	ToolNameHistorySymbol      = "history_symbol"
	ToolNameJoernScan          = "joern_scan"
	ToolNameListFiles          = "list_files"
	ToolNameMkdir              = "mkdir"
	ToolNameNote               = "note"
	ToolNameProductEfforts     = "product_efforts"
	ToolNameProductStatus      = "product_status"
	ToolNamePwd                = "pwd"
	ToolNameReadFile           = "read_file"
	ToolNameRecoveryDecompose  = "recovery_decompose"
	ToolNameRecoveryInspect    = "recovery_inspect"
	ToolNameRecoveryStatus     = "recovery_status"
	ToolNameRg                 = "rg"
	ToolNameSearchContent      = "search_content"
	ToolNameSearchFiles        = "search_files"
	ToolNameSemgrepScan        = "semgrep_scan"
	ToolNameShell              = "shell"
	ToolNameSourceSanity       = "source_sanity"
	ToolNameSystemSearchAccess = "request_system_search_access"
	ToolNameStructuralSearch   = "structural_search"
	ToolNameSubmitPlan         = "submit_plan"
	ToolNameTask               = "task"
	ToolNameTaskLedger         = "task_ledger"
	ToolNameWriteFile          = "write_file"

	// Read-only capability adapter tool.
	ToolNameReadOnlyCapability = "read_only_capability"

	// Language-specific built-in write tools.
	ToolNameGoMoveDeclarations = "go_move_declarations"

	// Supplemental language tools registered via language_tool_catalog.go.
	ToolNameGoFindFunction = "go_find_function"
	ToolNameGoFindType     = "go_find_type"
	ToolNameGoFindConst    = "go_find_const"
	ToolNameGoFindPackage  = "go_find_package"
	ToolNameGoImporters    = "go_importers"
	ToolNameCppDeclaration = "cpp_declaration"
	ToolNameCppIncludes    = "cpp_includes"
	ToolNameCppWhoIncludes = "cpp_who_includes"
	ToolNameCppBlastRadius = "cpp_blast_radius"
	ToolNameCppDeadSymbol  = "cpp_dead_symbol"

	// Language diagnostic tools.
	ToolNameGoDiagnostics     = "go_diagnostics"
	ToolNameCppDiagnostics    = "cpp_diagnostics"
	ToolNamePythonDiagnostics = "python_diagnostics"
	ToolNameRustDiagnostics   = "rust_diagnostics"
	ToolNameJavaDiagnostics   = "java_diagnostics"
	ToolNameTsDiagnostics     = "ts_diagnostics"

	// MCP-prefixed external tools.
	ToolNameMCPWebFetchExa  = "mcp:exa/web_fetch_exa"
	ToolNameMCPWebSearchExa = "mcp:exa/web_search_exa"
)
