# Rust tooling — integration snippets

This file carries the exact text to splice into the four centralized
files so the new Rust surface becomes discoverable. All of the Rust
tool implementations, the LSP client, and the semantic index already
live in their own files — these snippets just wire the registry,
dispatcher, schemas, and manifest to them.

Apply the four edits in any order; none depend on the others.

## language_registry.go

Inside `var languageRegistry = []LanguageSupport{ ... }` — append a
third entry after the closing brace of the `cpp` entry (line 135 in the
current file, immediately before the `}` that closes the slice literal):

```go
	{
		Name:       "rust",
		Extensions: []string{".rs"},
		Tools: map[Capability]string{
			CapFileOutline:     "rust_file_outline",
			CapFindSymbol:      "rust_find_symbol",
			CapWorkspaceSymbol: "rust_workspace_symbol",
			CapDefinition:      "rust_definition",
			CapReferences:      "rust_references",
			CapCallsFrom:       "rust_calls_from",
			CapCallsInto:       "rust_calls_into",
			CapReadSymbol:      "rust_read_symbol",
			CapRelatedFiles:    "rust_related_files",
			CapImplementations: "rust_implementations",
			CapDiagnostics:     "rust_diagnostics",
			CapOverview:        "rust_overview",
			CapEntrypoints:     "rust_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Rust decision guide — prefer deterministic tools over raw shell/grep:
- Orient a .rs file: rust_file_outline (path)
- Find a symbol anywhere: rust_workspace_symbol (pattern)
- Jump to a definition: rust_definition (pattern, path scope)
- Who calls this / who uses this: rust_references, rust_calls_into
- What does this call: rust_calls_from
- Read one symbol body: rust_read_symbol (pattern, path)
- Trait impls across the crate: rust_implementations (trait name)
- Files related to a concept: rust_related_files (pattern, path)
- Build errors: rust_diagnostics (path, runs cargo check)
Fallback to read_file with an explicit start_line/end_line range only when a semantic tool returns nothing.
`),
		Detect: func(root string) bool {
			if info, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil && !info.IsDir() {
				return true
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					name := d.Name()
					if _, skip := exploreSkippedDirNames[name]; skip && path != root {
						return filepath.SkipDir
					}
					if name == "target" && path != root {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if strings.EqualFold(filepath.Ext(path), ".rs") {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
		},
	},
```

The anchor line just above this new block is the closing `},` of the
`cpp` LanguageSupport entry (line 135 in the current file).

## manager.go

### 1) Add the Rust tool dispatch cases

Inside `Manager.Execute`, immediately after the last `cpp_*` case — the
current file ends the cpp block at line 399 with `case
"cpp_file_outline": return m.cppFileOutline(req)` — insert:

```go
	case "rust_overview",
		"rust_entrypoints",
		"rust_find_symbol",
		"rust_workspace_symbol",
		"rust_read_symbol",
		"rust_related_files",
		"rust_file_outline",
		"rust_definition",
		"rust_references",
		"rust_calls_from",
		"rust_calls_into",
		"rust_implementations",
		"rust_diagnostics":
		return m.ExecuteRustTool(ctx, req)
```

`ExecuteRustTool` is defined in `internal/capabilities/workspace/rust_tools.go` and
handles every `rust_*` name — adding or removing a tool only edits that
file, not this switch.

### 2) Wire the manifest flag (optional but matches the Go/cpp pattern)

In `Manager.toolManifestTools` (line 308 in the current file), update
the struct literal to also pass `IncludeRust`:

```go
func (m *Manager) toolManifestTools(readOnly bool) []string {
	return ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: !readOnly,
		IncludeTask:       true,
		IncludeWriteTools: !readOnly,
		IncludeGo:         m.isGoWorkspace(),
		IncludeCpp:        m.isCppWorkspace(),
		IncludeRust:       m.isRustWorkspace(),
	})
}
```

`isRustWorkspace` is defined in `internal/capabilities/workspace/rust_tools.go`.

## tool_schemas.go

Inside `baseToolDefinitions()`, append the following block just before
the closing `}` of the slice literal (line 350 in the current file,
immediately after the `go_move_declarations` entry):

```go
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
```

The anchor is the closing `},` of the existing `go_move_declarations`
entry (line 349).

## tool_manifest.go

### 1) Add the flag

At the top of `tool_manifest.go`, extend `ToolManifestToolOptions`:

```go
type ToolManifestToolOptions struct {
	IncludeTaskLedger bool
	IncludeTask       bool
	IncludeWriteTools bool
	IncludeGo         bool
	IncludeCpp        bool
	IncludeRust       bool
}
```

### 2) Emit the Rust tool names

Inside `ToolManifestTools`, append a new branch after the cpp block
(current line 90, right before the closing `return tools`):

```go
	if opts.IncludeRust {
		tools = append(tools, RustToolNames()...)
	}
```

`RustToolNames()` is defined in `internal/capabilities/workspace/rust_tools.go` so
the list lives next to the implementations.

### 3) Teach the model about the tools (manifest entries)

Inside `BuildToolManifest`, append these entries to the `entries`
slice — they can go immediately after the last `cpp_file_outline`
entry (line 163 in the current file, just before the `mkdir` entry):

```go
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
```

### 4) Decision-guide prose (optional)

In `RenderSemanticToolRules`, to match Go/C++ parity, add:

```go
	hasRust := manifestHasPrefix(manifest, "rust_")
```

...alongside the existing `hasGo`/`hasCpp` probes, extend the no-op guard:

```go
	if !hasGo && !hasCpp && !hasRust {
		return ""
	}
```

...and append after the C/C++ block:

```go
	if hasRust {
		lines = append(lines,
			"Rust semantic-tool rules:",
			"- Prefer Rust semantic tools over raw shell/grep for symbol, trait-impl, call-graph, and diagnostics questions.",
			"- Semantic lookups return paths, symbol kinds, and handles for follow-up inspection.",
			"- rust-analyzer backs the precise queries when installed; fallbacks use the syntactic index.",
		)
	}
```
