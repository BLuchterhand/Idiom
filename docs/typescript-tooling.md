# TypeScript / JavaScript tooling

idiom exposes a `ts_*` toolset to the model whenever the workspace looks
like a TypeScript/JavaScript project. The same toolset covers `.ts`,
`.tsx`, `.mts`, `.cts`, `.js`, `.jsx`, `.mjs`, `.cjs`, and `.d.ts` —
`typescript-language-server` handles both languages from a single
process, so splitting them would just duplicate code.

## Install

The language server is **not bundled**. Install it yourself once:

```bash
npm install -g typescript-language-server typescript
```

Verify:

```bash
which typescript-language-server
typescript-language-server --version
```

If it's missing, tree-sitter-backed tools (outline, find-symbol,
read-symbol, related-files, entrypoints, overview, file outline) still
work against the semantic index. LSP-backed tools (workspace-symbol,
definition, references, implementations, call hierarchy, diagnostics)
degrade gracefully with a message pointing you at this install command.

## Detection

A workspace is considered TS/JS when **any** of these is true:

- `tsconfig.json`, `jsconfig.json`, or `package.json` sits at the root
- a walk from the root finds any `.ts / .tsx / .mts / .cts / .js / .jsx /
  .mjs / .cjs` file (skipping `node_modules`, `.next`, `dist`, `build`,
  `out`, `coverage`)

## Capabilities

Thirteen canonical capabilities, all prefixed `ts_`:

| Capability       | Tool                    | Backed by                        |
| ---------------- | ----------------------- | -------------------------------- |
| file_outline     | `ts_file_outline`       | tree-sitter-style semantic index |
| find_symbol      | `ts_find_symbol`        | semantic index                   |
| workspace_symbol | `ts_workspace_symbol`   | LSP -> semantic fallback         |
| definition       | `ts_definition`         | LSP -> semantic fallback         |
| references       | `ts_references`         | LSP -> rg fallback               |
| calls_from       | `ts_calls_from`         | LSP call hierarchy -> semantic   |
| calls_into       | `ts_calls_into`         | LSP call hierarchy -> semantic   |
| read_symbol      | `ts_read_symbol`        | semantic index                   |
| related_files    | `ts_related_files`      | semantic index                   |
| implementations  | `ts_implementations`    | LSP -> semantic fallback         |
| diagnostics      | `ts_diagnostics`        | LSP (publishDiagnostics)         |
| overview         | `ts_overview`           | semantic index                   |
| entrypoints      | `ts_entrypoints`        | semantic index                   |

## Depth: LSP vs. tree-sitter

- The **tree-sitter-style index** is regex-driven, cheap to build, and
  answers orientation questions without any external process. It
  understands top-level functions, classes, methods, interfaces, type
  aliases, enums, namespaces, arrow-function-bound declarations, ES
  imports, CommonJS `require`, and dynamic `import(...)`. It does **not**
  resolve types, follow complex JSX generics, or understand
  module-resolution paths beyond relative specifiers with the usual TS
  extension fallbacks.
- The **LSP path** (`typescript-language-server --stdio`) delivers full
  type-aware answers for workspace symbols, definitions, references,
  implementations, call hierarchy, and diagnostics. It's slower to start
  — the model should prefer the index-backed tools for orientation and
  only reach for LSP tools when a type-aware answer is required.

## Usage examples

```
{"tool":"ts_overview"}
{"tool":"ts_file_outline","path":"src/index.ts"}
{"tool":"ts_find_symbol","pattern":"Shouter"}
{"tool":"ts_workspace_symbol","pattern":"Shouter"}
{"tool":"ts_definition","pattern":"greet","path":"src"}
{"tool":"ts_references","pattern":"greet","path":"."}
{"tool":"ts_read_symbol","pattern":"Shouter.shout","path":"src/greeter.ts"}
{"tool":"ts_calls_from","pattern":"main"}
{"tool":"ts_calls_into","pattern":"greet"}
{"tool":"ts_diagnostics","path":"src/index.ts"}
```
