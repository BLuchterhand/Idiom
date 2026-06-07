package workspace

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
