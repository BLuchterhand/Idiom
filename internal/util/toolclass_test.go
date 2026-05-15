package util

import "testing"

func TestClassifyToolExplicit(t *testing.T) {
	readTools := []string{
		"list_files", "read_file", "file_stats", "pwd", "code_navigate",
		"source_sanity", "read_only_capability", "harness_audit",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_function",
		"go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_file_outline",
		"cpp_read_symbol", "cpp_related_files", "cpp_definition", "cpp_declaration",
		"cpp_implementations", "cpp_references", "cpp_calls_from", "cpp_calls_into",
		"cpp_extract_body", "cpp_includes", "cpp_who_includes",
		"cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics",
		"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol",
		"python_related_files", "python_definition", "python_references",
		"python_implementations", "python_calls_from", "python_calls_into",
		"python_workspace_symbol", "python_diagnostics", "python_file_outline",
		"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol",
		"ts_related_files", "ts_definition", "ts_implementations", "ts_references",
		"ts_calls_from", "ts_calls_into", "ts_workspace_symbol", "ts_diagnostics",
		"ts_file_outline",
		"rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_workspace_symbol",
		"rust_read_symbol", "rust_related_files", "rust_file_outline",
		"rust_definition", "rust_references", "rust_calls_from", "rust_calls_into",
		"rust_implementations", "rust_diagnostics",
		"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol",
		"java_related_files", "java_workspace_symbol", "java_definition",
		"java_references", "java_implementations", "java_calls_from",
		"java_calls_into", "java_diagnostics", "java_file_outline",
	}
	for _, name := range readTools {
		if got := ClassifyTool(name); got != CategoryRead {
			t.Errorf("ClassifyTool(%q) = %v, want CategoryRead", name, got)
		}
	}

	searchTools := []string{"search_files", "search_content", "rg", "grep"}
	for _, name := range searchTools {
		if got := ClassifyTool(name); got != CategorySearch {
			t.Errorf("ClassifyTool(%q) = %v, want CategorySearch", name, got)
		}
	}

	discoveryTools := []string{
		"context_pack", "context_query", "context_zoom", "context_status",
		"context_index", "context_build", "context_impact", "context_trace_pack",
		"discovery_query", "structural_search", "dependency_graph",
		"ctags_symbols", "semgrep_scan", "joern_scan",
		"history_range", "history_symbol", "blame_range",
	}
	for _, name := range discoveryTools {
		if got := ClassifyTool(name); got != CategoryDiscovery {
			t.Errorf("ClassifyTool(%q) = %v, want CategoryDiscovery", name, got)
		}
	}

	writeTools := []string{
		"edit_file", "write_file", "append_file", "apply_patch",
		"mkdir", "go_move_declarations", "delegate_edit",
	}
	for _, name := range writeTools {
		if got := ClassifyTool(name); got != CategoryWrite {
			t.Errorf("ClassifyTool(%q) = %v, want CategoryWrite", name, got)
		}
	}

	verificationTools := []string{"harness_feedback", "idiom_feedback", "submit_plan"}
	for _, name := range verificationTools {
		if got := ClassifyTool(name); got != CategoryVerification {
			t.Errorf("ClassifyTool(%q) = %v, want CategoryVerification", name, got)
		}
	}

	shellTools := []string{"shell", "git_status", "git_diff"}
	for _, name := range shellTools {
		if got := ClassifyTool(name); got != CategoryShell {
			t.Errorf("ClassifyTool(%q) = %v, want CategoryShell", name, got)
		}
	}
}

func TestClassifyToolUnknown(t *testing.T) {
	if got := ClassifyTool("nonexistent_tool"); got != CategoryUnknown {
		t.Errorf("ClassifyTool(nonexistent_tool) = %v, want CategoryUnknown", got)
	}
	if got := ClassifyTool(""); got != CategoryUnknown {
		t.Errorf("ClassifyTool(empty) = %v, want CategoryUnknown", got)
	}
	if got := ClassifyTool("task"); got != CategoryUnknown {
		t.Errorf("ClassifyTool(task) = %v, want CategoryUnknown", got)
	}
	if got := ClassifyTool("task_ledger"); got != CategoryUnknown {
		t.Errorf("ClassifyTool(task_ledger) = %v, want CategoryUnknown", got)
	}
}

func TestClassifyToolMCP(t *testing.T) {
	if got := ClassifyTool("mcp:some_server"); got != CategoryMCP {
		t.Errorf("ClassifyTool(mcp:some_server) = %v, want CategoryMCP", got)
	}
	if got := ClassifyTool("mcp:"); got != CategoryUnknown {
		t.Errorf("ClassifyTool(mcp:) = %v, want CategoryUnknown (no server name)", got)
	}
}

func TestIsReadTool(t *testing.T) {
	readTools := []string{
		"read_file", "list_files", "file_stats", "go_read_symbol",
		"cpp_overview", "python_definition", "ts_workspace_symbol",
		"rust_diagnostics", "java_entrypoints",
		"harness_audit", "code_navigate", "source_sanity",
	}
	for _, name := range readTools {
		if !IsReadTool(name) {
			t.Errorf("IsReadTool(%q) = false, want true", name)
		}
	}
	searchTools := []string{"search_files", "search_content", "rg", "grep"}
	for _, name := range searchTools {
		if !IsReadTool(name) {
			t.Errorf("IsReadTool(%q) = false, want true (search is read-class)", name)
		}
	}
	discoveryTools := []string{
		"context_pack", "discovery_query", "context_status",
		"dependency_graph", "history_range", "ctags_symbols",
	}
	for _, name := range discoveryTools {
		if !IsReadTool(name) {
			t.Errorf("IsReadTool(%q) = false, want true (discovery is read-class)", name)
		}
	}
}

func TestIsReadToolFalsePositives(t *testing.T) {
	notReadTools := []string{
		"shell", "git_status", "git_diff",
		"edit_file", "write_file", "mkdir",
		"harness_feedback", "submit_plan",
		"mcp:server", "", "task", "task_ledger",
	}
	for _, name := range notReadTools {
		if IsReadTool(name) {
			t.Errorf("IsReadTool(%q) = true, want false", name)
		}
	}
}

func TestIsWriteTool(t *testing.T) {
	writeTools := []string{"edit_file", "write_file", "append_file", "apply_patch", "mkdir", "go_move_declarations"}
	for _, name := range writeTools {
		if !IsWriteTool(name) {
			t.Errorf("IsWriteTool(%q) = false, want true", name)
		}
	}
	notWrite := []string{"read_file", "search_content", "shell", "harness_feedback"}
	for _, name := range notWrite {
		if IsWriteTool(name) {
			t.Errorf("IsWriteTool(%q) = true, want false", name)
		}
	}
}

func TestIsSearchTool(t *testing.T) {
	searchTools := []string{"search_files", "search_content", "rg", "grep"}
	for _, name := range searchTools {
		if !IsSearchTool(name) {
			t.Errorf("IsSearchTool(%q) = false, want true", name)
		}
	}
	notSearch := []string{"read_file", "context_pack", "edit_file", "shell"}
	for _, name := range notSearch {
		if IsSearchTool(name) {
			t.Errorf("IsSearchTool(%q) = true, want false", name)
		}
	}
}

func TestIsDiscoveryTool(t *testing.T) {
	discoveryTools := []string{
		"context_pack", "context_query", "context_zoom", "context_status",
		"context_index", "context_impact", "context_trace_pack",
		"discovery_query", "structural_search", "dependency_graph",
		"ctags_symbols", "semgrep_scan", "joern_scan",
		"history_range", "history_symbol", "blame_range",
	}
	for _, name := range discoveryTools {
		if !IsDiscoveryTool(name) {
			t.Errorf("IsDiscoveryTool(%q) = false, want true", name)
		}
	}
	notDiscovery := []string{"read_file", "search_content", "edit_file", "shell"}
	for _, name := range notDiscovery {
		if IsDiscoveryTool(name) {
			t.Errorf("IsDiscoveryTool(%q) = true, want false", name)
		}
	}
}
