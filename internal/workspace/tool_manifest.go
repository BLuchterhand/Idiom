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
	IncludePython     bool
	IncludeTypescript bool
	IncludeRust       bool
	IncludeJava       bool
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
			"cpp_file_outline",
		)
	}
	if opts.IncludePython {
		tools = append(tools,
			"python_overview",
			"python_entrypoints",
			"python_find_symbol",
			"python_read_symbol",
			"python_related_files",
			"python_references",
			"python_definition",
			"python_implementations",
			"python_calls_from",
			"python_calls_into",
			"python_workspace_symbol",
			"python_diagnostics",
			"python_file_outline",
		)
	}
	if opts.IncludeTypescript {
		tools = append(tools,
			"ts_overview",
			"ts_entrypoints",
			"ts_find_symbol",
			"ts_read_symbol",
			"ts_related_files",
			"ts_definition",
			"ts_implementations",
			"ts_references",
			"ts_calls_from",
			"ts_calls_into",
			"ts_workspace_symbol",
			"ts_diagnostics",
			"ts_file_outline",
		)
	}
	if opts.IncludeRust {
		tools = append(tools, RustToolNames()...)
	}
	if opts.IncludeJava {
		tools = append(tools,
			"java_overview",
			"java_entrypoints",
			"java_find_symbol",
			"java_read_symbol",
			"java_related_files",
			"java_workspace_symbol",
			"java_definition",
			"java_references",
			"java_implementations",
			"java_calls_from",
			"java_calls_into",
			"java_diagnostics",
			"java_file_outline",
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
		{Tool: "cpp_file_outline", Request: `{"tool":"cpp_file_outline","path":"src/file.cpp"}`, Purpose: "list C/C++ declarations with line ranges"},
		{Tool: "python_overview", Request: `{"tool":"python_overview"}`, Purpose: "summarize Python workspace"},
		{Tool: "python_entrypoints", Request: `{"tool":"python_entrypoints"}`, Purpose: "find Python entrypoints"},
		{Tool: "python_find_symbol", Request: `{"tool":"python_find_symbol","pattern":"Name"}`, Purpose: "find Python symbols"},
		{Tool: "python_read_symbol", Request: `{"tool":"python_read_symbol","pattern":"Name","path":"src/pkg/file.py"}`, Purpose: "read a Python symbol body"},
		{Tool: "python_related_files", Request: `{"tool":"python_related_files","pattern":"term","path":"src"}`, Purpose: "find related Python files"},
		{Tool: "python_references", Request: `{"tool":"python_references","pattern":"Name","path":"."}`, Purpose: "find Python references"},
		{Tool: "python_definition", Request: `{"tool":"python_definition","pattern":"Name","path":"."}`, Purpose: "find Python definitions"},
		{Tool: "python_implementations", Request: `{"tool":"python_implementations","pattern":"BaseClass","path":"."}`, Purpose: "find Python subclasses"},
		{Tool: "python_calls_from", Request: `{"tool":"python_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing Python calls"},
		{Tool: "python_calls_into", Request: `{"tool":"python_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming Python calls"},
		{Tool: "python_workspace_symbol", Request: `{"tool":"python_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Python symbol lookup"},
		{Tool: "python_diagnostics", Request: `{"tool":"python_diagnostics","path":"src/pkg/file.py"}`, Purpose: "run Python diagnostics (needs pyright/pylsp)"},
		{Tool: "python_file_outline", Request: `{"tool":"python_file_outline","path":"src/pkg/file.py"}`, Purpose: "list Python declarations with line ranges"},
		{Tool: "ts_overview", Request: `{"tool":"ts_overview"}`, Purpose: "summarize TypeScript/JavaScript workspace"},
		{Tool: "ts_entrypoints", Request: `{"tool":"ts_entrypoints"}`, Purpose: "find TypeScript/JavaScript entrypoints"},
		{Tool: "ts_find_symbol", Request: `{"tool":"ts_find_symbol","pattern":"Name"}`, Purpose: "find TypeScript/JavaScript symbols"},
		{Tool: "ts_read_symbol", Request: `{"tool":"ts_read_symbol","pattern":"ClassName.method","path":"src/file.ts"}`, Purpose: "read a TypeScript/JavaScript symbol body"},
		{Tool: "ts_related_files", Request: `{"tool":"ts_related_files","pattern":"Name","path":"src"}`, Purpose: "find related TypeScript/JavaScript files"},
		{Tool: "ts_definition", Request: `{"tool":"ts_definition","pattern":"Name","path":"."}`, Purpose: "find TypeScript/JavaScript definitions"},
		{Tool: "ts_implementations", Request: `{"tool":"ts_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find TypeScript/JavaScript implementations"},
		{Tool: "ts_references", Request: `{"tool":"ts_references","pattern":"Name","path":"."}`, Purpose: "find TypeScript/JavaScript references"},
		{Tool: "ts_calls_from", Request: `{"tool":"ts_calls_from","pattern":"funcName","path":"."}`, Purpose: "find outgoing TypeScript/JavaScript calls"},
		{Tool: "ts_calls_into", Request: `{"tool":"ts_calls_into","pattern":"funcName","path":"."}`, Purpose: "find incoming TypeScript/JavaScript calls"},
		{Tool: "ts_workspace_symbol", Request: `{"tool":"ts_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide TypeScript/JavaScript symbol lookup"},
		{Tool: "ts_diagnostics", Request: `{"tool":"ts_diagnostics","path":"src/file.ts"}`, Purpose: "run TypeScript diagnostics"},
		{Tool: "ts_file_outline", Request: `{"tool":"ts_file_outline","path":"src/file.ts"}`, Purpose: "list TypeScript/JavaScript declarations with line ranges"},
		{Tool: "rust_overview", Request: `{"tool":"rust_overview"}`, Purpose: "summarize Rust workspace"},
		{Tool: "rust_entrypoints", Request: `{"tool":"rust_entrypoints"}`, Purpose: "find Rust entrypoints (main, lib, test, bench)"},
		{Tool: "rust_find_symbol", Request: `{"tool":"rust_find_symbol","pattern":"Name"}`, Purpose: "find Rust symbols"},
		{Tool: "rust_workspace_symbol", Request: `{"tool":"rust_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Rust symbol lookup"},
		{Tool: "rust_read_symbol", Request: `{"tool":"rust_read_symbol","pattern":"Name","path":"src/file.rs"}`, Purpose: "read a Rust symbol body"},
		{Tool: "rust_related_files", Request: `{"tool":"rust_related_files","pattern":"Name","path":"src"}`, Purpose: "find related Rust files (follows mod tree)"},
		{Tool: "rust_file_outline", Request: `{"tool":"rust_file_outline","path":"src/file.rs"}`, Purpose: "list Rust declarations with line ranges"},
		{Tool: "rust_definition", Request: `{"tool":"rust_definition","pattern":"Name","path":"."}`, Purpose: "find Rust definitions"},
		{Tool: "rust_references", Request: `{"tool":"rust_references","pattern":"Name","path":"."}`, Purpose: "find Rust references"},
		{Tool: "rust_calls_from", Request: `{"tool":"rust_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing Rust calls"},
		{Tool: "rust_calls_into", Request: `{"tool":"rust_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming Rust calls"},
		{Tool: "rust_implementations", Request: `{"tool":"rust_implementations","pattern":"TraitName","path":"."}`, Purpose: "find Rust trait impls"},
		{Tool: "rust_diagnostics", Request: `{"tool":"rust_diagnostics"}`, Purpose: "run cargo check"},
		{Tool: "java_overview", Request: `{"tool":"java_overview"}`, Purpose: "summarize Java workspace"},
		{Tool: "java_entrypoints", Request: `{"tool":"java_entrypoints"}`, Purpose: "find Java entrypoints and test classes"},
		{Tool: "java_find_symbol", Request: `{"tool":"java_find_symbol","pattern":"Name"}`, Purpose: "find Java symbols"},
		{Tool: "java_read_symbol", Request: `{"tool":"java_read_symbol","pattern":"Name","path":"src/main/java/com/example/File.java"}`, Purpose: "read a Java symbol body"},
		{Tool: "java_related_files", Request: `{"tool":"java_related_files","pattern":"service","path":"src/main/java"}`, Purpose: "find related Java files"},
		{Tool: "java_workspace_symbol", Request: `{"tool":"java_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Java symbol lookup"},
		{Tool: "java_definition", Request: `{"tool":"java_definition","pattern":"Name","path":"."}`, Purpose: "find Java definitions"},
		{Tool: "java_references", Request: `{"tool":"java_references","pattern":"Name","path":"."}`, Purpose: "find Java references"},
		{Tool: "java_implementations", Request: `{"tool":"java_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find Java implementations"},
		{Tool: "java_calls_from", Request: `{"tool":"java_calls_from","pattern":"methodName","path":"."}`, Purpose: "find outgoing Java calls"},
		{Tool: "java_calls_into", Request: `{"tool":"java_calls_into","pattern":"methodName","path":"."}`, Purpose: "find incoming Java calls"},
		{Tool: "java_diagnostics", Request: `{"tool":"java_diagnostics","path":"src/main/java/com/example/File.java"}`, Purpose: "run Java diagnostics"},
		{Tool: "java_file_outline", Request: `{"tool":"java_file_outline","path":"src/main/java/com/example/File.java"}`, Purpose: "list Java declarations with line ranges"},
		{Tool: "mkdir", Request: `{"tool":"mkdir","path":"relative/dir"}`, Purpose: "create a directory"},
		{Tool: "edit_file", Request: `{"tool":"edit_file","path":"relative/file","old_text":"exact substring from file","new_text":"replacement text"}`, Purpose: "edit by exact substring match (preferred shape; old_text must appear verbatim in the file)"},
		{Tool: "edit_file", Request: `{"tool":"edit_file","path":"relative/file","start_line":10,"end_line":12,"content":"replacement\n"}`, Purpose: "edit by line range (alternative shape; use when you know the exact lines). Must provide content — NOT old_text/new_text with this shape."},
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
	hasPython := manifestHasPrefix(manifest, "python_")
	hasTs := manifestHasPrefix(manifest, "ts_")
	hasRust := manifestHasPrefix(manifest, "rust_")
	hasJava := manifestHasPrefix(manifest, "java_")
	if !hasGo && !hasCpp && !hasPython && !hasTs && !hasRust && !hasJava {
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
	if hasPython {
		lines = append(lines,
			"Python semantic-tool rules:",
			"- Prefer Python semantic tools over raw shell/grep for module, symbol, entrypoint, reference, and diagnostics questions.",
			"- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
			"- python_diagnostics requires pyright or pylsp; if neither is installed, fall back to shell (`python -m py_compile`, `ruff check`) explicitly.",
		)
	}
	if hasTs {
		lines = append(lines,
			"TypeScript/JavaScript semantic-tool rules:",
			"- Prefer TypeScript/JavaScript semantic tools over raw shell/grep for symbol, definition, reference, call-graph, and diagnostics questions.",
			"- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
			"- The same toolset covers .ts/.tsx and .js/.jsx/.mjs/.cjs — typescript-language-server handles both from one process.",
		)
	}
	if hasRust {
		lines = append(lines,
			"Rust semantic-tool rules:",
			"- Prefer Rust semantic tools over raw shell/grep for symbol, trait-impl, call-graph, and diagnostics questions.",
			"- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
			"- rust-analyzer backs the precise queries when installed; fallbacks use the syntactic index.",
		)
	}
	if hasJava {
		lines = append(lines,
			"Java semantic-tool rules:",
			"- Prefer Java semantic tools over raw shell/grep for package, symbol, reference, implementation, and diagnostics questions.",
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
