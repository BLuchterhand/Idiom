# Java tooling — centralized-file integration

The Java tooling (`internal/semantic/java_*`, `internal/workspace/java_tools.go`,
`internal/workspace/jdtls_lsp.go`) is self-contained except for four
hooks into centralized files. Paste the snippets below into the named
files. Each snippet quotes the existing anchor line that identifies
where it goes.

## language_registry.go

Add a third entry to the `languageRegistry` slice, after the `cpp`
entry. Anchor — the closing brace of the cpp entry and the `}` that
closes `languageRegistry`:

```go
        // ...cpp entry...
        Detect: func(root string) bool {
            // ...existing cpp Detect...
        },
    },
    {
        Name:       "java",
        Extensions: []string{".java"},
        Tools: map[Capability]string{
            CapFileOutline:     "java_file_outline",
            CapFindSymbol:      "java_find_symbol",
            CapWorkspaceSymbol: "java_workspace_symbol",
            CapDefinition:      "java_definition",
            CapReferences:      "java_references",
            CapCallsFrom:       "java_calls_from",
            CapCallsInto:       "java_calls_into",
            CapReadSymbol:      "java_read_symbol",
            CapRelatedFiles:    "java_related_files",
            CapImplementations: "java_implementations",
            CapDiagnostics:     "java_diagnostics",
            CapOverview:        "java_overview",
            CapEntrypoints:     "java_entrypoints",
        },
        DecisionGuide: strings.TrimSpace(`
Java decision guide — prefer deterministic tools over raw shell/grep:
- Orient a .java file: java_file_outline (path)
- Find a symbol anywhere: java_workspace_symbol (pattern)
- Jump to a definition: java_definition (pattern, path scope)
- Who calls this / who uses this: java_references, java_calls_into
- What does this call: java_calls_from
- Read one symbol body: java_read_symbol (pattern, path)
- Interface implementers: java_implementations (pattern)
- Files related to a concept: java_related_files (pattern, path)
- Build/compile errors: java_diagnostics (path)
Fallback to read_file with an explicit start_line/end_line range only when a semantic tool returns nothing.
`),
        Detect: func(root string) bool {
            for _, manifest := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"} {
                if info, err := os.Stat(filepath.Join(root, manifest)); err == nil && !info.IsDir() {
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
                    case "target", "build", ".gradle", ".mvn":
                        if path != root {
                            return filepath.SkipDir
                        }
                    }
                    return nil
                }
                if strings.HasPrefix(filepath.Base(path), ".") {
                    return nil
                }
                if strings.EqualFold(filepath.Ext(path), ".java") {
                    found = true
                    return filepath.SkipAll
                }
                return nil
            })
            return found
        },
    },
}
```

## manager.go

Two edits here.

### 1. Dispatch cases — in the tool-dispatch `switch tool { ... }` block

Anchor — the existing `case "cpp_file_outline":` block. Add these
`case` entries directly after it (order within the switch doesn't
matter, but keeping languages grouped reads better):

```go
    case "java_overview":
        return m.javaOverview()
    case "java_entrypoints":
        return m.javaEntrypoints(req)
    case "java_find_symbol":
        return m.javaFindSymbol(req)
    case "java_read_symbol":
        return m.javaReadSymbol(req)
    case "java_related_files":
        return m.javaRelatedFiles(req)
    case "java_workspace_symbol":
        return m.javaWorkspaceSymbol(ctx, req)
    case "java_definition":
        return m.javaDefinition(ctx, req)
    case "java_references":
        return m.javaReferences(ctx, req)
    case "java_implementations":
        return m.javaImplementations(ctx, req)
    case "java_calls_from":
        return m.javaCallsFrom(ctx, req)
    case "java_calls_into":
        return m.javaCallsInto(ctx, req)
    case "java_diagnostics":
        return m.javaDiagnostics(ctx, req)
    case "java_file_outline":
        return m.javaFileOutline(req)
```

### 2. IsNarrowingRequest + ShouldClearBroadInspectionLock

Both functions have a large `case` covering every language's semantic
tools. Anchor — the `cpp_file_outline` tail of each list. Add the
Java names as a new line in both lists:

In `IsNarrowingRequest`:

```go
    case "search_files", "search_content", "rg", "read_file",
        // ...go_*...
        // ...cpp_*...
        "cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics",
        "java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files",
        "java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into",
        "java_workspace_symbol", "java_diagnostics", "java_file_outline":
        return true
```

In `ShouldClearBroadInspectionLock` (same structure, same additions):

```go
    case "search_files", "search_content", "rg",
        // ...go_*...
        // ...cpp_*...
        "cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics",
        "java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files",
        "java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into",
        "java_workspace_symbol", "java_diagnostics", "java_file_outline":
        return true
```

## tool_schemas.go

Add these `ToolDefinition` literals to the slice returned by
`baseToolDefinitions()`. Anchor — the existing `cpp_file_outline`
entry near the end of the slice:

```go
        {
            Name:        "java_file_outline",
            Description: "List declarations with line ranges in a Java file.",
            ParametersJSON: schemaObj(
                []string{"path"},
                `"path":`+strProp("file path"),
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
```

## tool_manifest.go

Three edits here.

### 1. `ToolManifestToolOptions` — add an `IncludeJava` flag

Anchor — the existing `IncludeCpp bool` line:

```go
type ToolManifestToolOptions struct {
    IncludeTaskLedger bool
    IncludeTask       bool
    IncludeWriteTools bool
    IncludeGo         bool
    IncludeCpp        bool
    IncludeJava       bool
}
```

### 2. `ToolManifestTools` — emit the Java names when `IncludeJava` is set

Anchor — the existing `if opts.IncludeCpp { ... }` block. Add a
sibling block right after it:

```go
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
```

### 3. `BuildToolManifest` — add manifest entries

Anchor — the existing `{Tool: "cpp_file_outline", ...}` entry. Insert
these entries right after it:

```go
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
```

### 4. Optional — `RenderSemanticToolRules` Java block

Anchor — the existing `if hasCpp { ... }` block. Add a sibling block:

```go
    hasJava := manifestHasPrefix(manifest, "java_")
    if hasJava {
        lines = append(lines,
            "Java semantic-tool rules:",
            "- Prefer Java semantic tools over raw shell/grep for package, symbol, reference, implementation, and diagnostics questions.",
            "- Use returned handles with read_file when you need surrounding source after a semantic lookup.",
        )
    }
```

Also add the `hasJava` check to the early-return guard at the top of
the function so the Java-only case still produces output:

```go
    if !hasGo && !hasCpp && !hasJava {
        return ""
    }
```

## Callers to update

The four files above are the structural hooks. Callers that construct
`ToolManifestToolOptions` (e.g. in `internal/agent/modes.go`) should
opt in by setting `IncludeJava: true` where appropriate; that's a
trivial follow-up and not part of this task.
