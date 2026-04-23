package workspace

// ToolDefinition is the neutral form of an OpenAI-function-calling tool
// spec. The agent layer converts this to llama.Tool before sending.
// ParametersJSON is a raw JSON schema string; we keep it as text so
// hand-authored schemas read naturally and so we don't pull a JSON
// schema library in here.
type ToolDefinition struct {
	Name           string
	Description    string
	ParametersJSON string
}

// BuildToolDefinitions returns the OpenAI-shaped function specs for each
// allowed tool. Pass an empty slice to get the full set. Tool names not
// in this map are silently skipped — keep the name list here in sync
// with ToolManifestTools in tool_manifest.go.
//
// Schemas are hand-tuned per tool. Kept terse: the model's training has
// strong priors on the native tool-call channel, so minimal fields +
// good descriptions beat an exhaustive parameter list.
func BuildToolDefinitions(allowedTools []string) []ToolDefinition {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	includeAll := len(allowed) == 0
	defs := baseToolDefinitions()
	out := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if !includeAll {
			if _, ok := allowed[def.Name]; !ok {
				continue
			}
		}
		out = append(out, def)
	}
	return out
}

// schemaStr is a tiny helper to keep the parameter JSON readable.
func schemaObj(required []string, properties string) string {
	req := ""
	if len(required) > 0 {
		req = `,"required":[`
		for i, name := range required {
			if i > 0 {
				req += ","
			}
			req += `"` + name + `"`
		}
		req += `]`
	}
	return `{"type":"object","properties":{` + properties + `}` + req + `}`
}

func strProp(desc string) string {
	return `{"type":"string","description":"` + desc + `"}`
}
func intProp(desc string) string {
	return `{"type":"integer","description":"` + desc + `"}`
}
func boolProp(desc string) string {
	return `{"type":"boolean","description":"` + desc + `"}`
}

func baseToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "task_ledger",
			Description: "Track complex multi-step work. Keep at most one item in_progress. When an item will touch files, list them in `paths` so the plan panel attaches the completed write to this item instead of adding a duplicate line.",
			ParametersJSON: schemaObj(
				[]string{"todos"},
				`"todos":{"type":"array","items":{"type":"object","properties":{"id":`+strProp("stable id")+`,"content":`+strProp("what needs doing")+`,"status":{"type":"string","enum":["pending","in_progress","completed","blocked","cancelled"]},"paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative files this item will touch"}},"required":["id","content","status"]}}`,
			),
		},
		{
			Name:        "task",
			Description: "Delegate a bounded read-only evidence-gathering question to a sub-agent.",
			ParametersJSON: schemaObj(
				[]string{"agent", "prompt"},
				`"agent":`+strProp("agent name, e.g. explorer")+`,"mode":{"type":"string","enum":["read_only","write"]},"prompt":`+strProp("focused question"),
			),
		},
		{
			Name:        "pwd",
			Description: "Show the current working directory.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "list_files",
			Description: "List files in a directory. Use once for orientation, then narrow with search_files/search_content or read_file on discovered handles.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("relative or absolute workspace path")+`,"recursive":`+boolProp("walk subdirectories")+`,"max_depth":`+intProp("recursion depth limit"),
			),
		},
		{
			Name:        "search_files",
			Description: "Find files by name pattern (glob-like).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("name pattern, e.g. *.go")+`,"path":`+strProp("root to search under"),
			),
		},
		{
			Name:        "search_content",
			Description: "Find text matches in files (prose, config, comments, strings). For symbol discovery in code, prefer the active language's workspace_symbol / find_symbol tools — they return file, line, kind, and qualified name.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("regex or plain text")+`,"path":`+strProp("root to search under")+`,"glob":`+strProp("file-name glob filter"),
			),
		},
		{
			Name:        "read_file",
			Description: "Read a file or a line range. For code in a supported language, prefer file_outline + read_symbol over reading whole files; use read_file with start_line/end_line only when a range a single declaration cannot cover (imports, headers, cross-declaration spans). Prefer the handle form {id:\"fN\"} after a search.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("relative or absolute path")+`,"id":`+strProp("discovered-file handle like f3")+`,"start_line":`+intProp("1-based start")+`,"end_line":`+intProp("1-based end"),
			),
		},
		{
			Name:        "git_status",
			Description: "Inspect the git working tree status.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory, defaults to workspace root"),
			),
		},
		{
			Name:        "git_diff",
			Description: "Inspect the git diff for the working tree or a path.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory")+`,"path":`+strProp("optional path to scope the diff"),
			),
		},
		{
			Name:        "shell",
			Description: "Run a shell command in the workspace. Same OS permissions as this process.",
			ParametersJSON: schemaObj(
				[]string{"command"},
				`"command":`+strProp("the command line")+`,"workdir":`+strProp("working directory"),
			),
		},
		{
			Name:        "mkdir",
			Description: "Create a directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("directory to create"),
			),
		},
		{
			Name:        "edit_file",
			Description: "Replace a line range (or old_text) in an existing file. Preferred for localized edits. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start")+`,"end_line":`+intProp("1-based end")+`,"old_text":`+strProp("exact text to replace")+`,"new_text":`+strProp("replacement")+`,"content":`+strProp("replacement content for the line range")+`,"description":`+strProp("one sentence on what this change is and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "write_file",
			Description: "Overwrite a file with exact contents. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("full file contents")+`,"description":`+strProp("one sentence on what this file is for and why it's being written")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "append_file",
			Description: "Append text to an existing file. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("text to append")+`,"description":`+strProp("one sentence on what's being appended and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "apply_patch",
			Description: "Apply a multi-file patch envelope (*** Begin Patch / *** End Patch). Requires a one-sentence description of the patch's intent.",
			ParametersJSON: schemaObj(
				[]string{"content", "description"},
				`"content":`+strProp("patch envelope")+`,"description":`+strProp("one sentence covering the patch's overall intent")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this patch fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:           "go_overview",
			Description:    "Summarize the Go workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "go_entrypoints",
			Description:    "List Go program entrypoints.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "go_find_symbol",
			Description: "Find Go symbols matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "go_find_function",
			Description: "Find Go functions matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name or regex"),
			),
		},
		{
			Name:        "go_find_type",
			Description: "Find Go types matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("type name or regex"),
			),
		},
		{
			Name:        "go_find_const",
			Description: "Find Go constants matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("constant name or regex"),
			),
		},
		{
			Name:        "go_find_package",
			Description: "Find Go packages matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("package import path or regex"),
			),
		},
		{
			Name:        "go_related_files",
			Description: "Find Go files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_workspace_symbol",
			Description: "Workspace-wide Go symbol lookup.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "go_references",
			Description: "Find references to a Go symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_definition",
			Description: "Find a Go symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_implementations",
			Description: "Find implementations of a Go interface.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_calls_from",
			Description: "Find outgoing calls from a Go function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_calls_into",
			Description: "Find incoming calls to a Go function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_read_symbol",
			Description: "Read the body of a Go symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_extract_body",
			Description: "Extract the body of a Go function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_importers",
			Description: "Find packages that import a given Go package.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("package path"),
			),
		},
		{
			Name:        "go_diagnostics",
			Description: "Run Go diagnostics (build/vet/errors) on a file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "cpp_file_outline",
			Description: "List declarations with line ranges in a C/C++ file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_file_outline",
			Description: "Inspect declaration boundaries for Go refactors.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_move_declarations",
			Description: "Mechanically move Go declarations between files by tag or explicit list.",
			ParametersJSON: schemaObj(
				[]string{"path", "destination"},
				`"path":`+strProp("source file")+`,"destination":`+strProp("destination file")+`,"pattern":`+strProp("tag:responsibility or explicit list"),
			),
		},
		{
			Name:           "python_overview",
			Description:    "Summarize the Python workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "python_entrypoints",
			Description:    "List likely Python entrypoint files (main blocks, app.py, cli.py).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "python_find_symbol",
			Description: "Find Python symbols (functions, classes, methods) matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "python_workspace_symbol",
			Description: "Workspace-wide Python symbol lookup (uses pyright/pylsp when available).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "python_definition",
			Description: "Find a Python symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_references",
			Description: "Find references to a Python symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_implementations",
			Description: "Find subclass implementations of a Python base class.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("base class name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_calls_from",
			Description: "Find outgoing calls from a Python function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_calls_into",
			Description: "Find incoming calls to a Python function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_read_symbol",
			Description: "Read the body of a Python symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "python_related_files",
			Description: "Find Python files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_diagnostics",
			Description: "Run Python diagnostics (requires pyright or pylsp on PATH).",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "python_file_outline",
			Description: "List declarations with line ranges in a Python file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:           "ts_overview",
			Description:    "Summarize the TypeScript/JavaScript workspace (likely anchors, counts).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "ts_entrypoints",
			Description:    "List likely TypeScript/JavaScript entrypoint files.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "ts_find_symbol",
			Description: "Find TypeScript/JavaScript symbols by name.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_read_symbol",
			Description: "Read the body of a TypeScript/JavaScript symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol, e.g. ClassName.method")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "ts_related_files",
			Description: "Find TypeScript/JavaScript files related to a concept or symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("keyword or symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_definition",
			Description: "Find a TypeScript/JavaScript symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_implementations",
			Description: "Find implementations of a TypeScript interface or overriding methods.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_references",
			Description: "Find TypeScript/JavaScript references to a symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_calls_from",
			Description: "Find outgoing calls from a TypeScript/JavaScript function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_calls_into",
			Description: "Find incoming calls to a TypeScript/JavaScript function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_workspace_symbol",
			Description: "Workspace-wide TypeScript/JavaScript symbol lookup via typescript-language-server when available.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol pattern")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_diagnostics",
			Description: "Run TypeScript diagnostics (type errors, lints) on a file or directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file or directory path"),
			),
		},
		{
			Name:        "ts_file_outline",
			Description: "List declarations with line ranges in a TypeScript/JavaScript file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:           "rust_overview",
			Description:    "Summarize the Rust workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "rust_entrypoints",
			Description:    "List Rust entrypoints (main, lib root, #[test], #[bench]).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "rust_find_symbol",
			Description: "Find Rust symbols (fn, struct, enum, trait, impl, macro) matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_workspace_symbol",
			Description: "Workspace-wide Rust symbol lookup (uses rust-analyzer when available).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "rust_read_symbol",
			Description: "Read the body of a Rust symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "rust_related_files",
			Description: "Find Rust files related to a pattern. Follows the mod-tree from the current file.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_file_outline",
			Description: "List declarations with line ranges in a Rust file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "rust_definition",
			Description: "Find a Rust symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_references",
			Description: "Find references to a Rust symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_calls_from",
			Description: "Find outgoing calls from a Rust function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_calls_into",
			Description: "Find incoming calls to a Rust function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_implementations",
			Description: "Find implementations of a Rust trait.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("trait name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_diagnostics",
			Description: "Run Rust diagnostics via cargo check.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("optional path to scope the check"),
			),
		},
		{
			Name:           "java_overview",
			Description:    "Summarize the Java workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "java_entrypoints",
			Description:    "List Java program entrypoints and test classes.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "java_find_symbol",
			Description: "Find Java symbols matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "java_read_symbol",
			Description: "Read the body of a Java symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "java_related_files",
			Description: "Find Java files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_workspace_symbol",
			Description: "Workspace-wide Java symbol lookup.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "java_definition",
			Description: "Find a Java symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_references",
			Description: "Find references to a Java symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_implementations",
			Description: "Find implementations of a Java interface or class.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface or class name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_calls_from",
			Description: "Find outgoing calls from a Java method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_calls_into",
			Description: "Find incoming calls to a Java method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_diagnostics",
			Description: "Run Java diagnostics (mvn/gradle/javac) on a file or directory.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory path"),
			),
		},
		{
			Name:        "java_file_outline",
			Description: "List declarations with line ranges in a Java file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
	}
}
