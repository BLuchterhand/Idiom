package workspace

import "idiom/internal/contracts/autonomy"

// ToolName constants for all built-in workspace tools.
// Keep this list synchronized with builtInToolSurfaceSpecs below.
const (
	ToolNameAppendFile         = autonomy.ToolNameAppendFile
	ToolNameApplyPatch         = autonomy.ToolNameApplyPatch
	ToolNameBlameRange         = autonomy.ToolNameBlameRange
	ToolNameCodeNavigate       = autonomy.ToolNameCodeNavigate
	ToolNameCodingRulesCheck   = autonomy.ToolNameCodingRulesCheck
	ToolNameContextImpact      = autonomy.ToolNameContextImpact
	ToolNameContextIndex       = autonomy.ToolNameContextIndex
	ToolNameContextPack        = autonomy.ToolNameContextPack
	ToolNameContextQuery       = autonomy.ToolNameContextQuery
	ToolNameContextStatus      = autonomy.ToolNameContextStatus
	ToolNameContextTracePack   = autonomy.ToolNameContextTracePack
	ToolNameContextZoom        = autonomy.ToolNameContextZoom
	ToolNameCtagsSymbols       = autonomy.ToolNameCtagsSymbols
	ToolNameDelegateEdit       = autonomy.ToolNameDelegateEdit
	ToolNameDependencyGraph    = autonomy.ToolNameDependencyGraph
	ToolNameDiscoveryQuery     = autonomy.ToolNameDiscoveryQuery
	ToolNameEditFile           = autonomy.ToolNameEditFile
	ToolNameFileStats          = autonomy.ToolNameFileStats
	ToolNameGitDiff            = autonomy.ToolNameGitDiff
	ToolNameGitStatus          = autonomy.ToolNameGitStatus
	ToolNameHarnessAudit       = autonomy.ToolNameHarnessAudit
	ToolNameHarnessFeedback    = autonomy.ToolNameHarnessFeedback
	ToolNameHistoryRange       = autonomy.ToolNameHistoryRange
	ToolNameHistorySymbol      = autonomy.ToolNameHistorySymbol
	ToolNameJoernScan          = autonomy.ToolNameJoernScan
	ToolNameListFiles          = autonomy.ToolNameListFiles
	ToolNameMkdir              = autonomy.ToolNameMkdir
	ToolNameProductEfforts     = autonomy.ToolNameProductEfforts
	ToolNameProductStatus      = autonomy.ToolNameProductStatus
	ToolNamePwd                = autonomy.ToolNamePwd
	ToolNameReadFile           = autonomy.ToolNameReadFile
	ToolNameRecoveryDecompose  = autonomy.ToolNameRecoveryDecompose
	ToolNameRecoveryInspect    = autonomy.ToolNameRecoveryInspect
	ToolNameRecoveryStatus     = autonomy.ToolNameRecoveryStatus
	ToolNameRg                 = autonomy.ToolNameRg
	ToolNameSearchContent      = autonomy.ToolNameSearchContent
	ToolNameSearchFiles        = autonomy.ToolNameSearchFiles
	ToolNameSemgrepScan        = autonomy.ToolNameSemgrepScan
	ToolNameShell              = autonomy.ToolNameShell
	ToolNameSourceSanity       = autonomy.ToolNameSourceSanity
	ToolNameSystemSearchAccess = autonomy.ToolNameSystemSearchAccess
	ToolNameStructuralSearch   = autonomy.ToolNameStructuralSearch
	ToolNameSubmitPlan         = autonomy.ToolNameSubmitPlan
	ToolNameTask               = autonomy.ToolNameTask
	ToolNameTaskLedger         = autonomy.ToolNameTaskLedger
	ToolNameWriteFile          = autonomy.ToolNameWriteFile

	// Read-only capability adapter tool.
	ToolNameReadOnlyCapability = "read_only_capability"

	// Language-specific built-in tools (planner metadata only — no builtInToolSurfaceSpec entry).
	ToolNameGoMoveDeclarations = autonomy.ToolNameGoMoveDeclarations

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

	// Language diagnostic tools (planner metadata only).
	ToolNameGoDiagnostics     = autonomy.ToolNameGoDiagnostics
	ToolNameCppDiagnostics    = autonomy.ToolNameCppDiagnostics
	ToolNamePythonDiagnostics = autonomy.ToolNamePythonDiagnostics
	ToolNameRustDiagnostics   = autonomy.ToolNameRustDiagnostics
	ToolNameJavaDiagnostics   = autonomy.ToolNameJavaDiagnostics
	ToolNameTsDiagnostics     = autonomy.ToolNameTsDiagnostics

	// MCP-prefixed external tools.
	ToolNameMCPWebFetchExa  = "mcp:exa/web_fetch_exa"
	ToolNameMCPWebSearchExa = "mcp:exa/web_search_exa"
)
