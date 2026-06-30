package workspace

import (
	"strings"

	"idiom/internal/contracts/autonomy"
)

// ToolCategory classifies workspace tools into broad capability groups
// for gating, recovery-card rendering, and autonomy recording.
type ToolCategory int

const (
	CategoryUnknown ToolCategory = iota
	CategoryRead
	CategoryWrite
	CategorySearch
	CategoryDiscovery
	CategoryVerification
	CategoryShell
	CategoryMCP
)

// ClassifyTool returns the ToolCategory for a given tool name.
func ClassifyTool(name string) ToolCategory {
	if metadata, ok := BuiltInToolSurfaceMetadata(name); ok && metadata.Category != CategoryUnknown {
		return metadata.Category
	}

	if isLanguageToolName(name) {
		return CategoryRead
	}

	if len(name) > 4 && name[:4] == "mcp:" {
		return CategoryMCP
	}

	return CategoryUnknown
}

// IsReadTool reports whether name is a read, search, or discovery tool.
func IsReadTool(name string) bool {
	c := ClassifyTool(name)
	return c == CategoryRead || c == CategorySearch || c == CategoryDiscovery
}

// IsReadClassToolName reports whether name is a read-class tool that narrows
// workspace evidence after readiness. This is intentionally narrower than
// IsReadTool: broad context-pack/status tools are discovery setup, while this
// class covers follow-up reads/searches that must explain the exact remaining
// evidence gap.
func IsReadClassToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameReadFile, ToolNameSearchContent, ToolNameSearchFiles, ToolNameListFiles,
		ToolNameHarnessAudit, ToolNameDiscoveryQuery, ToolNameStructuralSearch, ToolNameCtagsSymbols,
		ToolNameDependencyGraph, ToolNameHistoryRange, ToolNameHistorySymbol, ToolNameBlameRange,
		ToolNameSemgrepScan, ToolNameJoernScan:
		return true
	default:
		return IsLanguageReadClassToolName(name)
	}
}

func IsLanguageReadClassToolName(name string) bool {
	return LanguageToolHasAnyCapability(name,
		CapReadSymbol,
		CapFileOutline,
		CapExtractBody,
		CapWorkspaceSymbol,
		CapReferences,
		CapDefinition,
		CapCallsFrom,
		CapCallsInto,
		CapRelatedFiles,
	)
}

// IsRuntimeInspectionRequest reports whether a request gathers or narrows
// workspace evidence rather than verifying, mutating, or updating harness state.
func IsRuntimeInspectionRequest(req ToolRequest) bool {
	if IsBroadInspectionRequest(req) {
		return true
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameShell || tool == ToolNameListFiles {
		return IsNarrowingRequest(req)
	}

	metadata, ok := BuiltInToolSurfaceMetadata(tool)
	if !ok {
		return isLanguageToolName(tool)
	}

	actionClass := autonomy.CanonicalActionClass(autonomy.ActionClass(metadata.ActionClass))
	if metadata.Category == CategoryWrite ||
		metadata.Category == CategoryVerification ||
		actionClass == autonomy.ActionClassVerify ||
		actionClass == autonomy.ActionClassPerEditWrite ||
		actionClass == autonomy.ActionClassSubmitPlan {

		return false
	}

	if metadata.RuntimeSourceInspection ||
		metadata.RuntimeContextDiscovery ||
		metadata.RuntimeHistoryInspection {

		return true
	}

	switch actionClass {
	case autonomy.ActionClassRead,
		autonomy.ActionClassSearch,
		autonomy.ActionClassGenericProbe:
		return true
	default:
		return false
	}
}
