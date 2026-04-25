# Python tooling — integration snippets

This file contains the exact text to add to the four centralized files
that wire Python into the existing Go/C++ registry, dispatch, schema,
and manifest layers. The files are:

- `internal/workspace/language_registry.go`
- `internal/workspace/manager.go`
- `internal/workspace/tool_schemas.go`
- `internal/workspace/tool_manifest.go`

Each section shows the surrounding anchor line(s) so you know where to
paste.

---

## language_registry.go

Add this entry to the `languageRegistry = []LanguageSupport{…}` slice,
after the existing `cpp` block.

Anchor (the line immediately above where the new entry goes):

```go
			return found
		},
	},
```

Append this new `LanguageSupport` literal right after the `cpp` entry's
closing `},`:

```go
	{
		Name:       "python",
		Extensions: []string{".py", ".pyi"},
		Tools: map[Capability]string{
			CapFileOutline:     "python_file_outline",
			CapFindSymbol:      "python_find_symbol",
			CapWorkspaceSymbol: "python_workspace_symbol",
			CapDefinition:      "python_definition",
			CapReferences:      "python_references",
			CapCallsFrom:       "python_calls_from",
			CapCallsInto:       "python_calls_into",
			CapReadSymbol:      "python_read_symbol",
			CapRelatedFiles:    "python_related_files",
			CapImplementations: "python_implementations",
			CapDiagnostics:     "python_diagnostics",
			CapOverview:        "python_overview",
			CapEntrypoints:     "python_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Python decision guide — prefer deterministic tools over raw shell/grep:
- Orient a .py file: python_file_outline (path)
- Find a symbol anywhere: python_workspace_symbol (pattern)
- Jump to a definition: python_definition (pattern, path scope)
- Who calls this / who uses this: python_references, python_calls_into
- What does this call: python_calls_from
- Read one symbol body: python_read_symbol (pattern, path)
- Files related to a concept: python_related_files (pattern, path)
- Subclasses of a base class: python_implementations (pattern)
- Type / lint errors: python_diagnostics (path) — requires pyright or pylsp
Fallback to read_file with an explicit start_line/end_line range only when a semantic tool returns nothing.
`),
		Detect: func(root string) bool {
			markers := []string{
				"pyproject.toml",
				"setup.py",
				"setup.cfg",
				"requirements.txt",
				"Pipfile",
				"poetry.lock",
			}
			for _, marker := range markers {
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
					switch d.Name() {
					case ".venv", "venv", "__pycache__", ".mypy_cache",
						".pytest_cache", ".tox", "site-packages", ".eggs":
						if path != root {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				switch strings.ToLower(filepath.Ext(path)) {
				case ".py", ".pyi":
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
		},
	},
```

---

## manager.go

Add new `case` arms to the tool-dispatch `switch` inside
`RunTool` (or whatever the request-routing method is called — look for
the existing `case "cpp_overview":` block around line 362).

Anchor (the existing cpp block, shown for orientation):

```go
		case "cpp_overview":
			return m.cppOverview()
		case "cpp_entrypoints":
			return m.cppEntrypoints(req)
		// … the rest of the cpp_ cases …
		case "cpp_file_outline":
			return m.cppFileOutline(req)
```

Add these 13 new arms immediately after the final `cpp_file_outline`
case, before the `go_find_package` block (or wherever the Go cases
begin):

```go
		case "python_overview":
			return m.pythonOverview()
		case "python_entrypoints":
			return m.pythonEntrypoints(req)
		case "python_find_symbol":
			return m.pythonFindSymbol(req)
		case "python_read_symbol":
			return m.pythonReadSymbol(req)
		case "python_related_files":
			return m.pythonRelatedFiles(req)
		case "python_definition":
			return m.pythonDefinition(ctx, req)
		case "python_implementations":
			return m.pythonImplementations(ctx, req)
		case "python_references":
			return m.pythonReferences(ctx, req)
		case "python_calls_from":
			return m.pythonCallsFrom(ctx, req)
		case "python_calls_into":
			return m.pythonCallsInto(ctx, req)
		case "python_workspace_symbol":
			return m.pythonWorkspaceSymbol(ctx, req)
		case "python_diagnostics":
			return m.pythonDiagnostics(ctx, req)
		case "python_file_outline":
			return m.pythonFileOutline(req)
```

No other changes to `manager.go` are required. In particular:

- No new `Manager` fields are needed — `internal/workspace/python_tools.go`
  rebuilds the Python index on demand. (See the comment at
  `getPythonIndex` for rationale and a follow-up note about adding
  `pythonIndex` / `pythonIndexToken` fields if profiling ever shows the
  rebuild cost.)
- No new imports — `internal/workspace/python_tools.go` and
  `internal/workspace/pylsp_lsp.go` import `idiom/internal/semantic`
  themselves.

---

## tool_schemas.go

Add these definitions to the slice returned by `baseToolDefinitions()`.
Recommended placement: right after the `cpp_file_outline` entry, before
the `go_file_outline` entry.

Anchor:

```go
		{
			Name:        "cpp_file_outline",
			Description: "List declarations with line ranges in a C/C++ file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
```

Append these 13 entries:

```go
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
```

---

## tool_manifest.go

### Step 1 — add an `IncludePython` option

Anchor:

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
	IncludePython     bool
}
```

### Step 2 — extend `ToolManifestTools` to emit the Python names

Anchor (the final `if opts.IncludeCpp {` block):

```go
	if opts.IncludeCpp {
		tools = append(tools,
			"cpp_overview",
			…
			"cpp_file_outline",
		)
	}
	return tools
}
```

Insert this block immediately before `return tools`:

```go
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
```

### Step 3 — add manifest entries to `BuildToolManifest`

Anchor (end of the `cpp_*` manifest entries, right before the `mkdir`
entry):

```go
		{Tool: "cpp_file_outline", Request: `{"tool":"cpp_file_outline","path":"src/file.cpp"}`, Purpose: "list C/C++ declarations with line ranges"},
		{Tool: "mkdir", Request: `{"tool":"mkdir","path":"relative/dir"}`, Purpose: "create a directory"},
```

Insert these rows between `cpp_file_outline` and `mkdir`:

```go
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
```

### Step 4 — extend `RenderSemanticToolRules` to mention Python

Anchor:

```go
func RenderSemanticToolRules(allowedTools []string, readOnly bool) string {
	manifest := BuildToolManifest(allowedTools)
	hasGo := manifestHasPrefix(manifest, "go_")
	hasCpp := manifestHasPrefix(manifest, "cpp_")
	if !hasGo && !hasCpp {
		return ""
	}
```

Replace with:

```go
func RenderSemanticToolRules(allowedTools []string, readOnly bool) string {
	manifest := BuildToolManifest(allowedTools)
	hasGo := manifestHasPrefix(manifest, "go_")
	hasCpp := manifestHasPrefix(manifest, "cpp_")
	hasPython := manifestHasPrefix(manifest, "python_")
	if !hasGo && !hasCpp && !hasPython {
		return ""
	}
```

Then add a new block after the existing `hasCpp` block, before the final
`return`:

```go
	if hasPython {
		lines = append(lines,
			"Python semantic-tool rules:",
			"- Prefer Python semantic tools over raw shell/grep for module, symbol, entrypoint, reference, and diagnostics questions.",
			"- Semantic lookups return paths, symbol kinds, and handles for follow-up inspection.",
			"- python_diagnostics requires pyright or pylsp; if neither is installed, fall back to shell (`python -m py_compile`, `ruff check`) explicitly.",
		)
	}
```

---

That's the full set. After pasting all four blocks, `go build ./...` and
`go test ./internal/workspace/...` should both pass (assuming the
unrelated pre-existing Rust/Java/TypeScript stubs in the workspace
package are either completed or dropped).
