package workspace

import (
	"strings"
)

type ToolManifestEntry struct {
	Tool    string `json:"tool"`
	Request string `json:"request"`
	Purpose string `json:"purpose,omitempty"`
}

type ToolManifestToolOptions struct {
	IncludeTaskLedger bool
	IncludeTask       bool
	IncludeWriteTools bool
	IncludeGo         bool
	IncludeCpp        bool
}

func ToolManifestTools(opts ToolManifestToolOptions) []string {
	var tools []string
	if opts.IncludeTaskLedger {
		tools = append(tools, "task_ledger")
	}
	if opts.IncludeTask {
		tools = append(tools, "task")
	}
	tools = append(tools,
		"pwd",
		"list_files",
		"search_files",
		"search_content",
		"read_file",
		"git_status",
		"git_diff",
		"shell",
	)
	if opts.IncludeWriteTools {
		tools = append(tools, "mkdir", "edit_file", "write_file", "append_file", "apply_patch")
	}
	if opts.IncludeGo {
		tools = append(tools,
			"go_overview",
			"go_entrypoints",
			"go_find_symbol",
			"go_find_function",
			"go_find_type",
			"go_find_const",
			"go_find_package",
			"go_related_files",
			"go_workspace_symbol",
			"go_references",
			"go_definition",
			"go_implementations",
			"go_calls_from",
			"go_calls_into",
			"go_read_symbol",
			"go_extract_body",
			"go_importers",
			"go_diagnostics",
			"go_file_outline",
		)
		if opts.IncludeWriteTools {
			tools = append(tools, "go_move_declarations")
		}
	}
	if opts.IncludeCpp {
		tools = append(tools,
			"cpp_overview",
			"cpp_entrypoints",
			"cpp_find_symbol",
			"cpp_read_symbol",
			"cpp_related_files",
			"cpp_references",
			"cpp_definition",
			"cpp_declaration",
			"cpp_implementations",
			"cpp_calls_from",
			"cpp_calls_into",
			"cpp_extract_body",
			"cpp_includes",
			"cpp_who_includes",
			"cpp_blast_radius",
			"cpp_dead_symbol",
			"cpp_workspace_symbol",
			"cpp_diagnostics",
		)
	}
	return tools
}

func WorkspaceWriteToolNames() []string {
	return []string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "go_move_declarations"}
}

func IsWorkspaceWriteTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	for _, candidate := range WorkspaceWriteToolNames() {
		if tool == candidate {
			return true
		}
	}
	return false
}

func BuildToolManifest(allowedTools []string) []ToolManifestEntry {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	includeAll := len(allowed) == 0
	entries := []ToolManifestEntry{
		{Tool: "task_ledger", Request: `{"tool":"task_ledger","todos":[{"id":"inspect","content":"Inspect target files","status":"in_progress"}]}`, Purpose: "track complex multi-step work"},
		{Tool: "task", Request: `{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect one focused question and summarize evidence."}`, Purpose: "delegate bounded read-only evidence gathering"},
		{Tool: "pwd", Request: `{"tool":"pwd"}`, Purpose: "show current workspace directory"},
		{Tool: "list_files", Request: `{"tool":"list_files","path":".","recursive":true,"max_depth":2}`, Purpose: "bounded file discovery"},
		{Tool: "search_files", Request: `{"tool":"search_files","pattern":"name","path":"."}`, Purpose: "find files by name"},
		{Tool: "search_content", Request: `{"tool":"search_content","pattern":"symbol|text","path":".","glob":"*.go"}`, Purpose: "find text or symbols"},
		{Tool: "read_file", Request: `{"tool":"read_file","path":"relative/file.go","start_line":1,"end_line":120}`, Purpose: "read exact file ranges"},
		{Tool: "go_overview", Request: `{"tool":"go_overview"}`, Purpose: "summarize Go workspace"},
		{Tool: "go_entrypoints", Request: `{"tool":"go_entrypoints"}`, Purpose: "find Go entrypoints"},
		{Tool: "go_find_symbol", Request: `{"tool":"go_find_symbol","pattern":"Name"}`, Purpose: "find Go symbols"},
		{Tool: "go_find_function", Request: `{"tool":"go_find_function","pattern":"FuncName"}`, Purpose: "find Go functions"},
		{Tool: "go_find_type", Request: `{"tool":"go_find_type","pattern":"TypeName"}`, Purpose: "find Go types"},
		{Tool: "go_find_const", Request: `{"tool":"go_find_const","pattern":"ConstName"}`, Purpose: "find Go constants"},
		{Tool: "go_find_package", Request: `{"tool":"go_find_package","pattern":"internal/app"}`, Purpose: "find Go packages"},
		{Tool: "go_related_files", Request: `{"tool":"go_related_files","pattern":"routing","path":"internal/app"}`, Purpose: "find related Go files"},
		{Tool: "go_workspace_symbol", Request: `{"tool":"go_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Go symbol lookup"},
		{Tool: "go_references", Request: `{"tool":"go_references","pattern":"Name","path":"."}`, Purpose: "find Go references"},
		{Tool: "go_definition", Request: `{"tool":"go_definition","pattern":"Name","path":"."}`, Purpose: "find Go definitions"},
		{Tool: "go_implementations", Request: `{"tool":"go_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find Go implementations"},
		{Tool: "go_calls_from", Request: `{"tool":"go_calls_from","pattern":"FuncName","path":"."}`, Purpose: "find outgoing Go calls"},
		{Tool: "go_calls_into", Request: `{"tool":"go_calls_into","pattern":"FuncName","path":"."}`, Purpose: "find incoming Go calls"},
		{Tool: "go_read_symbol", Request: `{"tool":"go_read_symbol","pattern":"Name","path":"relative/file.go"}`, Purpose: "read a Go symbol body"},
		{Tool: "go_extract_body", Request: `{"tool":"go_extract_body","pattern":"FuncName","path":"relative/file.go"}`, Purpose: "extract a Go body"},
		{Tool: "go_importers", Request: `{"tool":"go_importers","pattern":"internal/app"}`, Purpose: "find Go importer packages"},
		{Tool: "go_diagnostics", Request: `{"tool":"go_diagnostics","path":"relative/file.go"}`, Purpose: "run Go diagnostics"},
		{Tool: "go_file_outline", Request: `{"tool":"go_file_outline","path":"relative/file.go"}`, Purpose: "inspect declaration boundaries for Go refactors"},
		{Tool: "go_move_declarations", Request: `{"tool":"go_move_declarations","path":"source.go","destination":"target.go","pattern":"tag:responsibility"}`, Purpose: "mechanically move Go declarations"},
		{Tool: "cpp_overview", Request: `{"tool":"cpp_overview"}`, Purpose: "summarize C/C++ workspace"},
		{Tool: "cpp_entrypoints", Request: `{"tool":"cpp_entrypoints"}`, Purpose: "find C/C++ entrypoints"},
		{Tool: "cpp_find_symbol", Request: `{"tool":"cpp_find_symbol","pattern":"Name"}`, Purpose: "find C/C++ symbols"},
		{Tool: "cpp_read_symbol", Request: `{"tool":"cpp_read_symbol","pattern":"Name","path":"src/file.cpp"}`, Purpose: "read a C/C++ symbol body"},
		{Tool: "cpp_related_files", Request: `{"tool":"cpp_related_files","pattern":"Name","path":"src"}`, Purpose: "find related C/C++ files"},
		{Tool: "cpp_references", Request: `{"tool":"cpp_references","pattern":"Name","path":"."}`, Purpose: "find C/C++ references"},
		{Tool: "cpp_definition", Request: `{"tool":"cpp_definition","pattern":"Name","path":"."}`, Purpose: "find C/C++ definitions"},
		{Tool: "cpp_declaration", Request: `{"tool":"cpp_declaration","pattern":"Name","path":"."}`, Purpose: "find C/C++ declarations"},
		{Tool: "cpp_implementations", Request: `{"tool":"cpp_implementations","pattern":"Name","path":"."}`, Purpose: "find C/C++ implementations"},
		{Tool: "cpp_calls_from", Request: `{"tool":"cpp_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing C/C++ calls"},
		{Tool: "cpp_calls_into", Request: `{"tool":"cpp_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming C/C++ calls"},
		{Tool: "cpp_extract_body", Request: `{"tool":"cpp_extract_body","pattern":"Name","path":"src/file.cpp"}`, Purpose: "extract a C/C++ body"},
		{Tool: "cpp_includes", Request: `{"tool":"cpp_includes","path":"src/file.cpp"}`, Purpose: "inspect includes"},
		{Tool: "cpp_who_includes", Request: `{"tool":"cpp_who_includes","path":"include/file.h"}`, Purpose: "find include users"},
		{Tool: "cpp_blast_radius", Request: `{"tool":"cpp_blast_radius","pattern":"Name"}`, Purpose: "estimate C/C++ blast radius"},
		{Tool: "cpp_dead_symbol", Request: `{"tool":"cpp_dead_symbol","pattern":"Name","path":"."}`, Purpose: "check for dead C/C++ symbols"},
		{Tool: "cpp_workspace_symbol", Request: `{"tool":"cpp_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide C/C++ symbol lookup"},
		{Tool: "cpp_diagnostics", Request: `{"tool":"cpp_diagnostics","path":"src/file.cpp"}`, Purpose: "run C/C++ diagnostics"},
		{Tool: "mkdir", Request: `{"tool":"mkdir","path":"relative/dir"}`, Purpose: "create a directory"},
		{Tool: "edit_file", Request: `{"tool":"edit_file","path":"relative/file","start_line":10,"end_line":12,"content":"replacement\n"}`, Purpose: "localized edit"},
		{Tool: "write_file", Request: `{"tool":"write_file","path":"relative/file","content":"full file contents"}`, Purpose: "write full file content"},
		{Tool: "append_file", Request: `{"tool":"append_file","path":"relative/file","content":"text to append"}`, Purpose: "append text"},
		{Tool: "apply_patch", Request: `{"tool":"apply_patch","content":"*** Begin Patch\n*** Update File: relative/file\n@@\n-old\n+new\n*** End Patch"}`, Purpose: "multi-file patch envelope"},
		{Tool: "git_status", Request: `{"tool":"git_status","workdir":"."}`, Purpose: "inspect git state"},
		{Tool: "git_diff", Request: `{"tool":"git_diff","workdir":".","path":"optional/path"}`, Purpose: "inspect git diff"},
		{Tool: "shell", Request: `{"tool":"shell","command":"go test ./...","workdir":"."}`, Purpose: "run an allowed shell command"},
	}
	out := make([]ToolManifestEntry, 0, len(entries))
	for _, entry := range entries {
		if includeAll {
			out = append(out, entry)
			continue
		}
		if _, ok := allowed[entry.Tool]; ok {
			out = append(out, entry)
		}
	}
	return out
}

func RenderToolManifestInstructions(allowedTools []string) string {
	manifest := BuildToolManifest(allowedTools)
	if len(manifest) == 0 {
		return ""
	}
	lines := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		line := "- " + entry.Tool + " " + entry.Request
		if entry.Purpose != "" {
			line += " (" + entry.Purpose + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func RenderSemanticToolRules(allowedTools []string, readOnly bool) string {
	manifest := BuildToolManifest(allowedTools)
	hasGo := manifestHasPrefix(manifest, "go_")
	hasCpp := manifestHasPrefix(manifest, "cpp_")
	if !hasGo && !hasCpp {
		return ""
	}

	var lines []string
	if hasGo {
		lines = append(lines,
			"Go semantic-tool rules:",
			"- Prefer Go semantic tools over raw shell/grep for package, symbol, entrypoint, reference, and diagnostics questions.",
			"- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
		)
		if !readOnly && manifestHasTool(manifest, "go_move_declarations") {
			lines = append(lines,
				"- For large Go refactors, inspect declaration boundaries with go_file_outline, then use go_move_declarations for model-chosen responsibility batches instead of rewriting giant files by hand.",
			)
		}
	}
	if hasCpp {
		lines = append(lines,
			"C/C++ semantic-tool rules:",
			"- Prefer C/C++ semantic tools over raw shell/grep for symbol, include-graph, call-graph, blast-radius, and diagnostics questions.",
			"- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
		)
	}
	return strings.Join(lines, "\n")
}

func manifestHasPrefix(manifest []ToolManifestEntry, prefix string) bool {
	for _, entry := range manifest {
		if strings.HasPrefix(entry.Tool, prefix) {
			return true
		}
	}
	return false
}

func manifestHasTool(manifest []ToolManifestEntry, tool string) bool {
	for _, entry := range manifest {
		if entry.Tool == tool {
			return true
		}
	}
	return false
}
