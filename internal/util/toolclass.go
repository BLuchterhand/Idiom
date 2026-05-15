package util

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

var toolCategories = map[string]ToolCategory{
	"list_files":           CategoryRead,
	"read_file":            CategoryRead,
	"file_stats":           CategoryRead,
	"pwd":                  CategoryRead,
	"code_navigate":        CategoryRead,
	"source_sanity":        CategoryRead,
	"read_only_capability": CategoryRead,
	"harness_audit":        CategoryRead,

	"search_files":   CategorySearch,
	"search_content": CategorySearch,
	"rg":             CategorySearch,
	"grep":           CategorySearch,

	"context_pack":       CategoryDiscovery,
	"context_query":      CategoryDiscovery,
	"context_zoom":       CategoryDiscovery,
	"context_status":     CategoryDiscovery,
	"context_index":      CategoryDiscovery,
	"context_build":      CategoryDiscovery,
	"context_impact":     CategoryDiscovery,
	"context_trace_pack": CategoryDiscovery,
	"discovery_query":    CategoryDiscovery,
	"structural_search":  CategoryDiscovery,
	"dependency_graph":   CategoryDiscovery,
	"ctags_symbols":      CategoryDiscovery,
	"semgrep_scan":       CategoryDiscovery,
	"joern_scan":         CategoryDiscovery,
	"history_range":      CategoryDiscovery,
	"history_symbol":     CategoryDiscovery,
	"blame_range":        CategoryDiscovery,

	"edit_file":           CategoryWrite,
	"write_file":          CategoryWrite,
	"append_file":         CategoryWrite,
	"apply_patch":         CategoryWrite,
	"mkdir":               CategoryWrite,
	"go_move_declarations": CategoryWrite,
	"delegate_edit":       CategoryWrite,

	"harness_feedback": CategoryVerification,
	"idiom_feedback":   CategoryVerification,
	"submit_plan":      CategoryVerification,

	"shell":      CategoryShell,
	"git_status": CategoryShell,
	"git_diff":   CategoryShell,

	"go_overview":         CategoryRead,
	"go_entrypoints":      CategoryRead,
	"go_find_symbol":      CategoryRead,
	"go_find_function":    CategoryRead,
	"go_find_type":        CategoryRead,
	"go_find_const":       CategoryRead,
	"go_file_outline":     CategoryRead,
	"go_read_symbol":      CategoryRead,
	"go_find_package":     CategoryRead,
	"go_related_files":    CategoryRead,
	"go_importers":        CategoryRead,
	"go_workspace_symbol": CategoryRead,
	"go_definition":       CategoryRead,
	"go_references":       CategoryRead,
	"go_implementations":  CategoryRead,
	"go_calls_from":       CategoryRead,
	"go_calls_into":       CategoryRead,
	"go_extract_body":     CategoryRead,
	"go_diagnostics":      CategoryRead,

	"cpp_overview":         CategoryRead,
	"cpp_entrypoints":      CategoryRead,
	"cpp_find_symbol":      CategoryRead,
	"cpp_file_outline":     CategoryRead,
	"cpp_read_symbol":      CategoryRead,
	"cpp_related_files":    CategoryRead,
	"cpp_definition":       CategoryRead,
	"cpp_declaration":      CategoryRead,
	"cpp_implementations":  CategoryRead,
	"cpp_references":       CategoryRead,
	"cpp_calls_from":       CategoryRead,
	"cpp_calls_into":       CategoryRead,
	"cpp_extract_body":     CategoryRead,
	"cpp_includes":         CategoryRead,
	"cpp_who_includes":     CategoryRead,
	"cpp_blast_radius":     CategoryRead,
	"cpp_dead_symbol":      CategoryRead,
	"cpp_workspace_symbol": CategoryRead,
	"cpp_diagnostics":      CategoryRead,

	"python_overview":         CategoryRead,
	"python_entrypoints":      CategoryRead,
	"python_find_symbol":      CategoryRead,
	"python_read_symbol":      CategoryRead,
	"python_related_files":    CategoryRead,
	"python_definition":       CategoryRead,
	"python_implementations":  CategoryRead,
	"python_references":       CategoryRead,
	"python_calls_from":       CategoryRead,
	"python_calls_into":       CategoryRead,
	"python_workspace_symbol": CategoryRead,
	"python_diagnostics":      CategoryRead,
	"python_file_outline":     CategoryRead,

	"ts_overview":         CategoryRead,
	"ts_entrypoints":      CategoryRead,
	"ts_find_symbol":      CategoryRead,
	"ts_read_symbol":      CategoryRead,
	"ts_related_files":    CategoryRead,
	"ts_definition":       CategoryRead,
	"ts_implementations":  CategoryRead,
	"ts_references":       CategoryRead,
	"ts_calls_from":       CategoryRead,
	"ts_calls_into":       CategoryRead,
	"ts_workspace_symbol": CategoryRead,
	"ts_diagnostics":      CategoryRead,
	"ts_file_outline":     CategoryRead,

	"rust_overview":         CategoryRead,
	"rust_entrypoints":      CategoryRead,
	"rust_find_symbol":      CategoryRead,
	"rust_workspace_symbol": CategoryRead,
	"rust_read_symbol":      CategoryRead,
	"rust_related_files":    CategoryRead,
	"rust_file_outline":     CategoryRead,
	"rust_definition":       CategoryRead,
	"rust_references":       CategoryRead,
	"rust_calls_from":       CategoryRead,
	"rust_calls_into":       CategoryRead,
	"rust_implementations":  CategoryRead,
	"rust_diagnostics":      CategoryRead,

	"java_overview":         CategoryRead,
	"java_entrypoints":      CategoryRead,
	"java_find_symbol":      CategoryRead,
	"java_read_symbol":      CategoryRead,
	"java_related_files":    CategoryRead,
	"java_workspace_symbol": CategoryRead,
	"java_definition":       CategoryRead,
	"java_references":       CategoryRead,
	"java_implementations":  CategoryRead,
	"java_calls_from":       CategoryRead,
	"java_calls_into":       CategoryRead,
	"java_diagnostics":      CategoryRead,
	"java_file_outline":     CategoryRead,
}

func ClassifyTool(name string) ToolCategory {
	if c, ok := toolCategories[name]; ok {
		return c
	}
	if len(name) > 4 && name[:4] == "mcp:" {
		return CategoryMCP
	}
	return CategoryUnknown
}

func IsReadTool(name string) bool {
	c := ClassifyTool(name)
	return c == CategoryRead || c == CategorySearch || c == CategoryDiscovery
}

func IsWriteTool(name string) bool {
	return ClassifyTool(name) == CategoryWrite
}

func IsSearchTool(name string) bool {
	return ClassifyTool(name) == CategorySearch
}

func IsDiscoveryTool(name string) bool {
	return ClassifyTool(name) == CategoryDiscovery
}
