# Java tooling

idiom's Java support follows the same Capability-keyed shape used by
Go and C/C++: a per-language `LanguageSupport` entry in
`internal/semantic/language/registry.go`, a tree-walking semantic
index at `internal/semantic/java_index.go`, and a set of `java_*`
tools implemented under `internal/semantic/languagetools` and exposed
through the workspace manager adapter.

## Capabilities

All 13 canonical capabilities are implemented:

| Capability | Tool name |
|---|---|
| `file_outline` | `java_file_outline` |
| `find_symbol` | `java_find_symbol` |
| `workspace_symbol` | `java_workspace_symbol` |
| `definition` | `java_definition` |
| `references` | `java_references` |
| `calls_from` | `java_calls_from` |
| `calls_into` | `java_calls_into` |
| `read_symbol` | `java_read_symbol` |
| `related_files` | `java_related_files` |
| `implementations` | `java_implementations` |
| `diagnostics` | `java_diagnostics` |
| `overview` | `java_overview` |
| `entrypoints` | `java_entrypoints` |

## Workspace detection

A directory is treated as a Java workspace if any of the following
is true:

1. It contains `pom.xml`, `build.gradle`, `build.gradle.kts`, or
   `settings.gradle` at the top level.
2. It contains at least one `.java` file under a non-skipped
   subdirectory. Skipped dirs: `target`, `build`, `.gradle`, `.mvn`,
   `node_modules`, and the shared exploration skip list.

## LSP: Eclipse JDT-LS (jdtls)

Deep semantic operations (`java_definition`, `java_references`,
`java_implementations`, `java_calls_from`, `java_calls_into`, the
LSP-backed path of `java_workspace_symbol`) prefer jdtls when
available. The client lives at `internal/semantic/languagetools/jdtls_lsp.go`
and mirrors `clangd_lsp.go` — plain JSON-RPC over stdio with a
longer initialize timeout (`jdtlsInitializeTimeout = 30s`) and a
short post-initialize settle to account for project import.

**jdtls is not bundled.** Install it yourself:

- macOS: `brew install jdtls`
- Linux/manual: download from
  [https://download.eclipse.org/jdtls/snapshots/](https://download.eclipse.org/jdtls/snapshots/)
  and put the launcher on `PATH`.
- **Requires Java 17+ on `PATH`.** jdtls refuses to start on older JDKs.

`exec.LookPath("jdtls")` is consulted at request time; if jdtls is
absent the LSP-backed tools fall back cleanly to the tree-sitter-free
semantic index at `internal/semantic/java_index.go`. Tree-sitter-backed
tools (`java_file_outline`, `java_find_symbol`, `java_read_symbol`,
`java_related_files`, `java_overview`, `java_entrypoints`, and the
`java_calls_from/java_calls_into` semantic fallback) work without
jdtls.

## Tree-sitter vs LSP depth

This drop does not use `github.com/smacker/go-tree-sitter` for Java.
The semantic index is regex-based, matching the approach already used
for C/C++ (`internal/semantic/cpp_index.go`). This keeps the Java
build cgo-free and avoids adding another large transitive dep. The
parser handles the subset needed for the 13 capabilities:

- package / import extraction
- class / interface / enum / record / @interface headers with
  `extends` / `implements` / `permits` clauses
- method + constructor signatures, including modifiers and annotations
- inner / nested / static nested classes, indented under their
  enclosing class in `java_file_outline`
- per-method call-site extraction for the semantic `calls_from` /
  `calls_into` fallback

Deeper resolution — generic erasure, overload resolution, cross-jar
type lookup, inference — is the job of jdtls.

## Java-specific capability notes

- **`related_files`**: follows `package` and `import` edges. Files in
  the same package are treated as siblings; wildcard imports
  (`import foo.bar.*;`) pull in every indexed file in that package.
- **`implementations`**: especially important for Java. The semantic
  fallback walks every indexed class/record/enum and emits those
  whose `extends` or `implements` clause names the target interface
  or class. jdtls `textDocument/implementation` takes precedence when
  available.
- **`entrypoints`**: three kinds are flagged:
  - classes with `public static void main(String[])`
  - methods annotated with `@Test` (JUnit 4 `org.junit.Test` and
    JUnit 5 `org.junit.jupiter.api.Test`)
  - classes annotated with `@SpringBootApplication`
- **`diagnostics`**: picks the most authoritative signal available:
  - `pom.xml` present → `mvn -q -o compile -DskipTests`
  - `build.gradle` present → `gradle --offline --quiet compileJava`
  - otherwise → `javac -Xlint:all -d $TMPDIR <file>` per target
  jdtls-pushed `publishDiagnostics` is intentionally **not** used —
  collecting push diagnostics requires keeping the LSP client alive
  across request boundaries, which doesn't fit the single-tool-call
  harness. This is a known LSP-depth gap.

## Caching

Per-language index caching lives on `internal/semantic/languagetools.Manager`
and is invalidated by the same git HEAD/dirty-status token used by the
other language caches. `internal/workspace` owns dispatch, request/response
adaptation, and path/file host services, not language index state.

## Files

- `internal/semantic/java_index.go` — index build + parsing
- `internal/semantic/java_query.go` — symbol queries, call queries,
  implementations, dependency closures
- `internal/semantic/java_index_test.go` — index unit tests
- `internal/semantic/languagetools/java_tools.go` — `java_*` tool
  implementations and per-manager caching
- `internal/semantic/languagetools/jdtls_lsp.go` — jdtls JSON-RPC client
- `internal/workspace/language_tools_adapter.go` — workspace host adapter
  and compatibility dispatch facade
- `internal/semantic/languagetools/java_tools_test.go` — workspace-facade
  integration tests (LSP-gated tests skip via `t.Skip()` if jdtls is absent)

## Integration points

Java entries are already wired into the centralized registry, dispatcher,
schemas, and manifest. The historical handoff snippets are archived at
[`archive/2026-05-historical/java-tooling-integration.md`](./archive/2026-05-historical/java-tooling-integration.md).
