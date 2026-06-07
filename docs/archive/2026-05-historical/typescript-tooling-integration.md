# TypeScript tooling — integration snippets

The TS tool implementations, LSP client, and semantic index all live in
new files that compile on their own. To wire them into the discovery
redirect, the Manager dispatch, the LLM-facing tool schemas, and the
tool manifest, paste the snippets below into the four centralized files.

---

## language_registry.go

Anchor: the `languageRegistry` slice literal — add a third
`LanguageSupport` entry after the existing `"cpp"` entry. The closing
`},` of the C++ entry is the insertion point.

```go
	{
		Name:       "typescript",
		Extensions: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".d.ts"},
		Tools: map[Capability]string{
			CapFileOutline:     "ts_file_outline",
			CapFindSymbol:      "ts_find_symbol",
			CapWorkspaceSymbol: "ts_workspace_symbol",
			CapDefinition:      "ts_definition",
			CapReferences:      "ts_references",
			CapCallsFrom:       "ts_calls_from",
			CapCallsInto:       "ts_calls_into",
			CapReadSymbol:      "ts_read_symbol",
			CapRelatedFiles:    "ts_related_files",
			CapImplementations: "ts_implementations",
			CapDiagnostics:     "ts_diagnostics",
			CapOverview:        "ts_overview",
			CapEntrypoints:     "ts_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
TypeScript/JavaScript decision guide — prefer deterministic tools over raw shell/grep:
- Orient a .ts/.tsx/.js/.jsx file: ts_file_outline (path)
- Find a symbol anywhere: ts_workspace_symbol (pattern)
- Jump to a definition: ts_definition (pattern, path scope)
- Who calls this / who uses this: ts_references, ts_calls_into
- What does this call: ts_calls_from
- Read one symbol body: ts_read_symbol (pattern, path)
- Files related to a concept: ts_related_files (pattern, path)
- Type/compile errors: ts_diagnostics (path)
The same toolset covers .js/.jsx/.mjs/.cjs — typescript-language-server handles both TS and JS from one process. Fallback to read_file with an explicit start_line/end_line range only when a semantic tool returns nothing.
`),
		Detect: func(root string) bool {
			for _, marker := range []string{"tsconfig.json", "jsconfig.json", "package.json"} {
				if info, err := os.Stat(filepath.Join(root, marker)); err == nil && !info.IsDir() {
					return true
				}
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if _, skip := exploreSkippedDirNames[d.Name()]; skip && path != root {
						return filepath.SkipDir
					}
					if name := d.Name(); name == ".next" || name == "node_modules" {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if isTsExtension(filepath.Ext(path)) {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
		},
	},
```

`isTsExtension` is already defined in `internal/capabilities/workspace/ts_tools.go`,
so the registry doesn't need a new helper.

---

## manager.go

### 1. Add a dispatch group for `ts_*` tools

Anchor: the existing `case "cpp_file_outline":` block in the
`Execute(ctx, req)` switch. Immediately after its closing
`return m.cppFileOutline(req)` line (before the `case "go_find_package":`
block), insert:

```go
	case "ts_overview":
		return m.tsOverview()
	case "ts_entrypoints":
		return m.tsEntrypoints(req)
	case "ts_find_symbol":
		return m.tsFindSymbol(req)
	case "ts_read_symbol":
		return m.tsReadSymbol(req)
	case "ts_related_files":
		return m.tsRelatedFiles(req)
	case "ts_definition":
		return m.tsDefinition(ctx, req)
	case "ts_implementations":
		return m.tsImplementations(ctx, req)
	case "ts_references":
		return m.tsReferences(ctx, req)
	case "ts_calls_from":
		return m.tsCallsFrom(ctx, req)
	case "ts_calls_into":
		return m.tsCallsInto(ctx, req)
	case "ts_workspace_symbol":
		return m.tsWorkspaceSymbol(ctx, req)
	case "ts_diagnostics":
		return m.tsDiagnostics(ctx, req)
	case "ts_file_outline":
		return m.tsFileOutline(req)
```

### 2. Pass `IncludeTypescript` into the tool manifest

Anchor: the `toolManifestTools` helper currently reads:

```go
func (m *Manager) toolManifestTools(readOnly bool) []string {
	return ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: !readOnly,
		IncludeTask:       true,
		IncludeWriteTools: !readOnly,
		IncludeGo:         m.isGoWorkspace(),
		IncludeCpp:        m.isCppWorkspace(),
	})
}
```

Add a sixth option matching the new tool_manifest field:

```go
func (m *Manager) toolManifestTools(readOnly bool) []string {
	return ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger:  !readOnly,
		IncludeTask:        true,
		IncludeWriteTools:  !readOnly,
		IncludeGo:          m.isGoWorkspace(),
		IncludeCpp:         m.isCppWorkspace(),
		IncludeTypescript:  m.isTsWorkspace(),
	})
}
```

### 3. (Optional) Include `ts_*` in the broad/narrowing inspection sets

If you want `ts_*` tools to participate in the broad-inspection-lock
heuristics the same way `cpp_*` tools do, extend the two `switch`
blocks at `IsNarrowingRequest` / `ShouldClearBroadInspectionLock` — both
currently list the cpp prefix set. Append these cases to each block:

```go
	"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol", "ts_related_files",
	"ts_definition", "ts_implementations", "ts_references", "ts_calls_from", "ts_calls_into",
	"ts_workspace_symbol", "ts_diagnostics", "ts_file_outline":
```

---

## tool_schemas.go

Anchor: the final entry in `baseToolDefinitions()` is the
`go_move_declarations` definition followed by the closing `}` of the
returned slice. Insert these entries **before** that closing `}`:

```go
	{
		Name:        "ts_overview",
		Description: "Summarize the TypeScript/JavaScript workspace (likely anchors, counts).",
		ParametersJSON: `{"type":"object","properties":{}}`,
	},
	{
		Name:        "ts_entrypoints",
		Description: "List likely TypeScript/JavaScript entrypoint files.",
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
```

---

## tool_manifest.go

### 1. Add `IncludeTypescript` to `ToolManifestToolOptions`

Anchor: the existing struct literal:

```go
type ToolManifestToolOptions struct {
	IncludeTaskLedger bool
	IncludeTask       bool
	IncludeWriteTools bool
	IncludeGo         bool
	IncludeCpp        bool
}
```

Replace with:

```go
type ToolManifestToolOptions struct {
	IncludeTaskLedger bool
	IncludeTask       bool
	IncludeWriteTools bool
	IncludeGo         bool
	IncludeCpp        bool
	IncludeTypescript bool
}
```

### 2. Append the ts_* block inside `ToolManifestTools`

Anchor: inside `ToolManifestTools`, directly after the existing
`if opts.IncludeCpp { ... }` block and before the final `return tools`
statement, add:

```go
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
```

### 3. Add manifest entries (examples the model sees)

Anchor: in `BuildToolManifest`, the large `entries := []ToolManifestEntry{...}` slice
literal. Append these entries before the final `}` of the slice (after
the existing `cpp_file_outline` entry is a natural spot):

```go
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
```

### 4. Add a TS block to `RenderSemanticToolRules`

Anchor: the existing two-branch block that renders Go rules and C++
rules. After the `if hasCpp { ... }` block, before the final
`return strings.Join(lines, "\n")`, add:

```go
	hasTs := manifestHasPrefix(manifest, "ts_")
	if hasTs {
		lines = append(lines,
			"TypeScript/JavaScript semantic-tool rules:",
			"- Prefer TypeScript/JavaScript semantic tools over raw shell/grep for symbol, definition, reference, call-graph, and diagnostics questions.",
			"- Semantic lookups return paths, symbol kinds, and handles for follow-up inspection.",
			"- The same toolset covers .ts/.tsx and .js/.jsx/.mjs/.cjs — typescript-language-server handles both from one process.",
		)
	}
```

You'll also need to change the early return guard from
`if !hasGo && !hasCpp { return "" }` to
`if !hasGo && !hasCpp && !hasTs { return "" }` (hoist `hasTs :=` above
that guard if you prefer — functionally equivalent).
