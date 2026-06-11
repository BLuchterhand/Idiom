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
	if metadata, ok := builtInToolSurfaceMetadata(name); ok && metadata.Category != CategoryUnknown {
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
	metadata, ok := builtInToolSurfaceMetadata(tool)
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
