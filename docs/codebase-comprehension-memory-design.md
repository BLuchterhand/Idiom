# Codebase Comprehension Memory Design

## Objective

Make idiom a stronger planner for local and smaller models by giving them a
multi-resolution, evidence-linked model of the codebase without flooding the
prompt with raw files.

The target is not for the harness to decide the model's strategy. The target is
for the harness to maintain trustworthy repository facts, summaries, graph
relationships, and packet-ready context slices that the model can use when it
plans.

North star: the model owns strategy; the harness owns contracts. See
[decisioning-north-star.md](decisioning-north-star.md).

## Current Capability

idiom already has several important pieces:

- Architect packets externalize task context before a model writes a plan,
  critique, or executor brief. They are task-scoped envelopes containing task
  text, constraints, explicit files, tests, anchors, risks, unknowns, profile
  rules, and optional collector output.
- `discovery_query` runs deterministic layered probes for impact analysis,
  tracing, architecture boundaries, security review, and history context. It
  persists evidence packets under `.idiom/discovery/queries`.
- `.idiom/discovery/context.md`, `modules.jsonl`, `manifest.json`, and related
  artifacts provide a cheap project digest and top-level map.
- Language tools expose symbol lookup, file outlines, references, definitions,
  calls, diagnostics, and related-file probes where the language backend
  supports them.
- Workflow state, task ledger, typed resume context, target-role packets, and
  context compaction preserve active task state across longer runs.

These are already aligned with the desired direction. The missing layer is not
"more context packets." It is a persistent comprehension substrate that can
generate better packets and answer graph-shaped questions at the right
resolution.

## Current Vs Proposed

| Area | Current idiom capability | Proposed capability |
| --- | --- | --- |
| Planning envelope | Architect packets carry task-scoped context. | Architect packets can be populated from reusable graph views. |
| Discovery | `discovery_query` runs a live deterministic recipe. | `discovery_query` can reuse a persistent graph and probe only gaps. |
| Project memory | `.idiom/discovery/context.md` gives a cheap top-level digest. | `.idiom/comprehension/graph.sqlite` stores the active graph; `.idiom/comprehension/graph.json` remains an inspectable export. |
| Resolution | File listings, outlines, symbols, and evidence excerpts. | Repository, subsystem, package, file, symbol, and line/range views. |
| Ranking | Mostly implicit in tool choice and search results. | Transparent graph-ranked localization with signal explanations. |
| Freshness | Query artifacts include generated time and git-sensitive IDs. | Each node, edge, view, and summary has freshness and confidence metadata. |
| Runtime behavior | Verification output and logs are available as separate evidence. | Runtime traces become graph evidence linked to static nodes. |

## Gap

Current context packets mostly package known or freshly probed evidence. They do
not maintain a reusable, queryable, multi-resolution model of the codebase.

The practical gaps are:

- No canonical symbol/file/module/subsystem graph persisted across turns.
- No stable node IDs that let a model zoom from architecture summary to exact
  symbol body without rediscovering paths.
- No hierarchical summaries that roll up from symbol to file to package to
  subsystem with provenance and freshness.
- No graph-ranked localization that can auto-populate packet files, anchors,
  impact surfaces, candidate tests, and open gaps.
- No first-class representation of confidence, staleness, static-analysis
  uncertainty, or missing backend coverage at each abstraction level.
- Runtime evidence, test traces, and failing execution slices are not yet part
  of the same comprehension model as static code structure.

The result is that small models still spend too many calls reconstructing the
same orientation, and context packets often depend on explicit user-provided
files or ad hoc collectors instead of a reusable repository memory.

## Design Principle

Separate three concepts that are easy to conflate:

- **Index:** durable repository facts computed by the harness.
- **View:** bounded, resolution-specific context returned to the model.
- **Packet:** task-scoped planning or execution envelope containing selected
  views, constraints, and validation contracts.

The index should be broad and persistent. Views should be narrow and cheap.
Packets should be auditable and task-specific.

## Non-Goals

- Do not synthesize final answers in the harness.
- Do not force the model to use a specific next tool because a graph says so.
- Do not present LLM-generated summaries as facts unless they are labeled as
  summaries with provenance.
- Do not build a single giant repository summary and inject it everywhere.
- Do not require heavyweight language infrastructure before the first useful
  version works.
- Do not replace existing language tools, `discovery_query`, architect packets,
  or workflow state. This design feeds them.

## Target Architecture

Add a new codebase comprehension subsystem with four layers:

1. **Collectors**
   Gather raw facts from existing local surfaces: file listings, tree-sitter or
   syntax indexes, language tools, dependency extraction, git history, tests,
   diagnostics, and optional runtime traces.

2. **Repository Graph**
   Store normalized nodes and edges in a local persistent database under
   `.idiom/comprehension/`.

3. **Abstraction Builder**
   Build deterministic and optionally model-assisted summaries at multiple
   resolutions. Every generated summary must keep provenance back to graph nodes,
   files, line ranges, commands, and source commit/fingerprint.

4. **Context Views And Packet Integration**
   Expose task-shaped tools and CLI commands that return bounded context views
   and can populate architect packets.

The architecture should sit next to `internal/workspace` and `internal/architect`
rather than inside the runner. The runner consumes views and packets; it should
not own indexing policy.

## Language Enrichment Layer

The graph now has an explicit enrichment seam:

- Fast enrichment records language backend availability, build/project
  manifests, and the tools/capabilities that can deepen each indexed language.
- Deep enrichment can run bounded live workspace-tool probes, including LSP
  backed diagnostics, references, call hierarchy, and implementation queries
  when those tools are installed.
- Every enrichment emits collector reports and graph nodes/edges with
  provenance. Missing LSPs are reported as unavailable capability, not as a hard
  indexing failure.

This keeps the capability language-agnostic. Go, C/C++, Python,
TypeScript/JavaScript, Rust, Java, and GDScript/Godot all use the same graph
vocabulary for backend facts, project manifests, diagnostic evidence, and live
tool evidence. The exact source can differ by language: syntax indexes,
dedicated project collectors, LSPs, compilers, package managers, or generic
fallbacks all write observations into the same durable model.

The graph still supplements discovery tools rather than replacing them. Use the
graph to map and package what is already known; use language-aware tools,
`discovery_query`, exact reads, or runtime traces to verify stale or source-level
claims.

## Related Work

This design deliberately borrows proven ideas without cloning any one project:

- [Aider repo maps](https://aider.chat/2023/10/22/repomap.html):
  tree-sitter symbol extraction plus graph ranking to fit useful repository
  context into a small token budget.
- [RepoGraph](https://proceedings.iclr.cc/paper_files/paper/2025/hash/4a4a3c197deac042461c677219efd36c-Abstract-Conference.html):
  repository-level code graph as a plug-in navigation layer for AI software
  engineering.
- [CodePlan](https://www.microsoft.com/en-us/research/publication/codeplan-repository-level-coding-using-llms-and-planning-2/):
  repository-level coding framed as planning over dependency and may-impact
  analysis.
- [Codebase-Memory MCP](https://arxiv.org/abs/2603.27277): persistent
  tree-sitter knowledge graph exposed to agents through typed tools.
- [Joern code property graphs](https://docs.joern.io/code-property-graph/):
  AST, control-flow, and data-flow relationships in one queryable
  representation.
- [Kythe](https://kythe.io/docs/kythe-overview.html) and
  [Sourcegraph SCIP](https://sourcegraph.com/docs/code-navigation/precise-code-navigation):
  language-agnostic indexing formats for definitions, references, type metadata,
  and cross-reference navigation.
- [SWE-agent](https://proceedings.neurips.cc/paper_files/paper/2024/hash/5a7c947568c1b1328ccc5230172e1e7c-Abstract-Conference.html):
  agent-computer interface design as a model-performance lever.
- [Gistify/debug-gym](https://microsoft.github.io/debug-gym/blog/2025/10/gistify/):
  runtime execution and traces as necessary evidence for codebase-level
  understanding.

The idiom-specific adaptation is the north-star split: these systems inspire the
evidence substrate, but the model still owns strategy.

## Data Model

Start with a simple property graph. The active implementation stores normalized
facts in SQLite and keeps a portable JSON export so the workspace tool and CLI
surface remain easy to inspect. The node/edge shape should stay stable as
incremental refresh, saved views, and larger language coverage need more indexed
queries.

### Nodes

Minimum node kinds:

- `repo`
- `directory`
- `file`
- `package`
- `module`
- `symbol`
- `type`
- `interface`
- `method`
- `function`
- `test`
- `command`
- `config`
- `route`
- `external_dependency`
- `decision`
- `summary`
- `runtime_trace`
- `diagnostic`

Each node should include:

- stable ID
- kind
- path when applicable
- language when applicable
- qualified name when applicable
- line range when applicable
- content fingerprint
- git head or dirty-worktree fingerprint
- source collector
- confidence tier
- created/updated timestamp

Stable IDs should be deterministic where possible:

```text
repo://<root-fingerprint>
file://<path>
symbol://<path>#<qualified-name>:<range-fingerprint>
summary://<scope-id>/<resolution>/<source-fingerprint>
```

IDs must tolerate line drift. For symbols, prefer path plus qualified name plus
body fingerprint over raw line numbers.

### Edges

Minimum edge kinds:

- `contains`
- `defines`
- `imports`
- `references`
- `calls`
- `implements`
- `overrides`
- `uses_type`
- `tests`
- `configures`
- `routes_to`
- `depends_on`
- `changed_with`
- `summarizes`
- `evidenced_by`
- `generated_from`
- `may_impact`
- `runtime_calls`
- `runtime_reads`
- `runtime_writes`

Edges should carry:

- collector
- confidence tier
- evidence location or command
- freshness fingerprint
- optional reason text for inferred edges

### Confidence Tiers

Use explicit confidence labels:

- `exact`: compiler/LSP/test/runtime-backed fact.
- `syntax`: parser or tree-sitter structural fact.
- `heuristic`: regex/import convention/path inference.
- `summary`: model-generated or compressed explanation.
- `unknown`: intentionally missing or unsupported.

The model can reason with all tiers, but packet and final-answer contracts should
make tier differences visible.

## Resolution Levels

The index should support zooming across levels:

1. **Repository**
   Purpose, active languages, entrypoints, major subsystems, build/test surface,
   important conventions, known gaps.

2. **Subsystem**
   Directory or graph-community summary, owned concepts, public APIs, dependencies,
   volatility, primary tests.

3. **Package/Module**
   Imports, exported symbols, internal collaborators, test coverage, common
   change patterns.

4. **File**
   Outline, responsibilities, important symbols, incoming/outgoing dependencies,
   related tests.

5. **Symbol**
   Signature, body range, callers, callees, references, implemented interfaces,
   tests, diagnostics.

6. **Line/Range**
   Exact source excerpt, blame/history, diagnostics, runtime hit evidence.

A context view should be able to return any level with handles that let the model
request the next deeper level. The model chooses whether to zoom.

## Tool And CLI Surface

Add a small set of high-level tools. Names are tentative.

### `context_index`

Build or refresh the comprehension index.

Inputs:

- `path`
- `language`
- `mode`: `quick`, `standard`, `deep`
- `include_runtime`: boolean
- `include_history`: boolean
- `max_files`

Output:

- index ID
- root fingerprint
- files indexed
- languages
- collector coverage
- skipped paths
- stale nodes removed
- errors and unsupported features

This is a harness operation. It should not prescribe what the model does next.

### `context_status`

Report index freshness and coverage.

Output:

- current git head and dirty status
- index fingerprint
- stale scopes
- available collectors
- missing optional tools
- last successful refresh

### `context_query`

Ask graph-shaped questions without dumping source files.

Examples:

- "what calls this symbol?"
- "what tests cover this package?"
- "what files usually change with this one?"
- "what public APIs does this subsystem expose?"
- "what would likely be impacted by changing this type?"

Inputs:

- `query`
- `path`
- `symbol`
- `scope`
- `resolution`
- `max_results`
- `include_evidence`

Output:

- claims
- graph nodes and handles
- evidence paths/ranges
- confidence tiers
- unresolved gaps

The tool result must be observational: claims, evidence, uncertainty. It must
not say "now edit X" or "call Y next."

### `context_zoom`

Return a deeper or broader view for a handle.

Inputs:

- `handle`
- `resolution`
- `budget_tokens`
- `include_source`

Output:

- bounded view
- child handles
- parent handles
- provenance

### `context_pack`

Generate packet-ready context for a task.

Inputs:

- `task`
- `profile`
- `scope`
- `mode`: `plan`, `critique`, `brief`, `implement`
- `budget_tokens`
- `required_tests`
- `forbidden_paths`

Output:

- candidate files with roles: editable, read-only evidence, analogy, forbidden
- candidate symbols and anchors
- graph-ranked rationale
- relevant tests and commands
- risks
- unknowns
- evidence entries suitable for architect packets
- gaps that need one exact probe

This does not replace `idiom architect packet`. It supplies better evidence and
defaults for it.

### `context_trace_pack`

Attach runtime evidence from a command or test.

Inputs:

- `command`
- `path`
- `max_events`
- `include_files`

Output:

- executed files and ranges
- runtime call edges when available
- stdout/stderr excerpt
- failing assertion or panic anchor
- environment limitations

This must stay language-agnostic at the contract layer. Collector-specific
parsers such as Go test JSON, Go coverprofiles, LCOV, Python tracebacks,
JavaScript/TypeScript stack frames, JVM stack frames, C/C++ file-line output, or
GDScript `res://` paths should all write the same runtime node/edge vocabulary.
The first version runs a command, saves stdout/stderr excerpts, parses generic
stack frames, Go test JSON, Go coverprofiles, and LCOV, and stores trace
artifacts under `.idiom/comprehension/traces/`.

## Future Expansion Of Context Tools

The context tools should grow as a layered comprehension system, not as a
replacement for exact discovery tools. The intended split stays stable:

- `context_index` builds durable, language-agnostic facts.
- `context_status` reports freshness, coverage, backend availability, and gaps.
- `context_query` answers graph-shaped orientation questions without raw source
  flooding.
- `context_zoom` expands one handle to the next useful abstraction level.
- `context_pack` packages task-specific evidence for planning and bounded
  execution.
- `context_trace_pack` adds runtime evidence without overwriting static facts.

Current hardening should continue to make this tool set more reliable for small
local models:

1. **Richer collector contracts.**
   Keep every language on the same node/edge vocabulary while allowing
   collector-specific detail in properties. Standard mode records deterministic
   collector facts; deep mode can add bounded live language-tool/LSP
   relationships. A missing relationship should appear as an explicit gap or
   lower-confidence edge, not as silent absence.

2. **Typed coverage reporting.**
   `context_status` reports collector coverage, backend/toolchain availability,
   collector versions, storage compatibility, stale files, stale symbols, and
   bounded samples. Continue expanding this toward per-language support for
   files, symbols, imports/includes, calls, references, tests, diagnostics,
   runtime traces, and history. This lets a model know whether a query result is
   weak because the relationship does not exist or because the collector cannot
   prove it yet.

3. **Incremental refresh.**
   Scope-limited refresh now merges fresh per-path graph facts with reusable
   out-of-scope facts and expands the refresh scope across directly related
   dependency files. Continue improving this with build-config invalidation,
   language backend availability changes, and finer package/module dependency
   closure for large repos.

4. **Query intent routing.**
   `context_query` should recognize common intents such as "callers", "callees",
   "tests for", "imports", "blast radius", "entrypoints", and "ownership",
   then run bounded graph traversals. The output still reports evidence and
   uncertainty; it does not prescribe the next tool call.

5. **Zoomable views.**
   `context_zoom` should support repository, subsystem, package, file, symbol,
   and range views with stable handles. Views should include parent/child
   handles, relevant tests, incoming/outgoing relationships, and known stale
   edges within the requested token budget.

6. **Packet-ready packaging.**
   `context_pack` is the bridge from reusable comprehension to task-specific
   planning. It ranks candidate files and symbols, labels evidence, includes
   tests and risks, and names unresolved gaps. It should remain the first
   consumer of stricter freshness policies.

7. **Runtime and history overlays.**
   `context_trace_pack`, blame/history collectors, and coverage data should add
   separate runtime/history nodes and edges. Static, runtime, and history facts
   should remain distinguishable so a model cannot confuse "called in this test"
   with "always calls".

8. **Discovery tool backing.**
   Existing discovery tools should use the context graph as a cache and
   orientation layer when it is fresh. They should still perform exact language,
   structural, history, or source reads when the graph is stale, incomplete, or
   too coarse for the claim being made.

Language-specific expansion should proceed opportunistically:

- **Go:** add `go/packages`/`go/types` support for import resolution,
  interfaces, method sets, build tags, generated files, and coverage-backed
  test-to-symbol edges.
- **C++:** use `compile_commands.json` and clangd when available for
  namespaces, overloads, templates, declarations versus definitions, references,
  diagnostics, and translation-unit blast radius. The deterministic collector
  should keep covering includes, declarations/definitions, local calls, template
  declarations, template call sites, and ambiguous include gaps.
- **GDScript/Godot:** add a parser or language-server backend when practical.
  The current Godot-aware collector covers scripts, scenes, resources,
  autoloads, exported/onready properties, signals, class names, extends, and
  `res://` dependencies; future work should deepen scene node trees, signal
  connections, groups, project settings, and additional Godot 3/4 resource
  differences.
- **Generic languages:** add Tree-sitter or existing semantic helpers for
  TypeScript, Python, Rust, Java, and other source families while keeping the
  generic fallback for unknown languages.

The expansion order should be driven by behavior, not code coverage: add the
next fact type when it lets a small model avoid broad reads, avoid path/symbol
guessing, produce a better packet, or make stale/unsupported evidence explicit.

## Architect Packet Integration

Architect packets remain the planning envelope.

Add optional packet fields or evidence kinds:

```json
{
  "kind": "context_view",
  "label": "task localization",
  "path": ".idiom/comprehension/views/<id>.json",
  "content": "bounded JSON excerpt or summary"
}
```

Potential new architect packet fields:

- `context_index_id`
- `context_views`
- `candidate_files`
- `candidate_symbols`
- `evidence_gaps`
- `confidence`

Keep backward compatibility by representing new data as `Evidence` first. Add
typed packet fields only after the shape stabilizes.

Packet rendering should preserve the current rule: planners use only the packet.
The difference is that the packet can now include better harness-computed views.

## Discovery Query Integration

`discovery_query` should not disappear. It should become a bridge:

- If no index exists, run the current recipe.
- If an index exists, use it for the orientation layers and add live probes only
  for missing or stale claims.
- Persist the same evidence packet shape, plus the context index/view IDs that
  contributed to it.

This keeps existing tests and user expectations intact while reducing repeated
exploration.

## Runtime Evidence

Static code graphs are not enough for behavior-heavy tasks. Runtime evidence
should be modeled as first-class but optional.

Initial runtime sources:

- failing test output
- stack traces
- coverage reports
- `go test -run ... -json`
- explicit command logs
- debug log slices for idiom's own harness behavior

Later runtime sources:

- language-specific tracing hooks
- debugger-backed call traces
- HTTP route exercise traces
- integration-test execution slices

Runtime edges must be marked separately from static edges. A static call edge and
a runtime-observed call edge are different evidence types.

## Summary Generation

Use deterministic summaries first:

- file outline from syntax/language tools
- package import/export summaries
- call/reference counts
- test mapping
- graph communities and centrality

Use model-assisted summaries only as compression over already-collected facts.

Model-assisted summary contract:

- input facts are listed and fingerprinted
- output is labeled `summary`
- output contains no new file paths or symbols not present in input facts
- summary stores source fact IDs
- summary expires when source facts change

This lets small models benefit from natural-language compression without turning
summaries into unverified facts.

## Ranking And Localization

Ranking should help choose what fits in a budget; it should not choose the
strategy.

Signals:

- lexical match to task
- symbol/name match
- dependency distance from named anchors
- call/reference centrality
- test proximity
- recent change co-occurrence
- diagnostics/failing stack trace proximity
- explicit user-provided files or constraints

Outputs must expose why an item was ranked:

```json
{
  "path": "internal/workspace/discovery_query.go",
  "role": "read_only_evidence",
  "score": 0.82,
  "signals": ["symbol_match", "dependency_neighbor", "recent_query_artifact"],
  "confidence": "syntax"
}
```

Do not hide ranking as an authoritative edit plan.

## Storage

Use `.idiom/comprehension/`:

```text
.idiom/comprehension/
  manifest.json
  graph.sqlite        # active indexed backend
  graph.json          # compatibility/export copy
  views/
    <view-id>.json
  summaries/
    <summary-id>.json
  traces/
    <trace-id>.json
```

Like `.idiom/discovery/`, this directory should be ignored by git.

`manifest.json` should include:

- schema version
- root
- git head
- dirty status fingerprint
- active languages
- collector versions
- optional tool availability
- last refresh times

## Active SQLite Store With JSON Export

The active local store is SQLite, with a JSON export kept for compatibility and
inspection:

- `.idiom/comprehension/graph.sqlite` contains normalized node, edge, metadata,
  and view tables with `store_kind: "sqlite-graph-v1"`.
- `.idiom/comprehension/graph.json` contains an export copy with `nodes`,
  `edges`, root metadata, git state, languages, generated time, and
  `store_kind: "json-graph-v1"` so humans and older tooling can still inspect
  the graph shape.
- `.idiom/comprehension/manifest.json` contains the active SQLite graph ID,
  artifact path, indexed counts, git state, generated time, and storage backend
  compatibility reports.
- File nodes store content fingerprints; symbol nodes inherit the containing
  file fingerprint so `context_status` can report stale symbols when a file
  changes or disappears.
- `context_index` rewrites the graph from available language collectors. Go,
  C++, Python, TypeScript/JavaScript, Rust, and Java use native semantic
  indexes; GDScript uses a dedicated Godot-aware collector; unregistered source
  families still use the generic file/symbol/import/test/call collector. This
  keeps the graph language-agnostic while letting supported languages carry
  richer facts.
- Go collector facts include packages, files, imports, declarations,
  package-scoped call edges, local-module import call edges, external call
  symbols, and test-to-package/test-to-symbol heuristics.
- C++ collector facts include files, packages, declarations, local and system
  include edges, definition-preferred call edges, test-file heuristics, and
  confidence-preserving external dependencies when includes are ambiguous.
- GDScript collector facts include `.gd` scripts, `.tscn` scenes, `.tres`
  resources, `project.godot` autoloads, class names, extends, signals,
  functions, variables, and resource/script dependency edges.
- Python, TypeScript/JavaScript, Rust, and Java collector facts include
  file/package/symbol/import facts, local import resolution when paths can be
  proven from the workspace, class/type-to-method containment, heuristic call
  edges, test symbol/package edges, and language-specific properties such as
  async/export/trait/annotation metadata.
- Deep enrichment keeps bounded diagnostic/tool evidence nodes and also
  normalizes parseable live references, incoming/outgoing call hierarchy, and
  implementation results into graph edges. Unparseable output remains evidence
  text instead of being promoted into an ungrounded relationship.
- `context_status` reports stale files, stale symbol counts, and bounded stale
  symbol samples. `context_index` reports refreshed stale files and symbol
  counts after rebuilding.

## SQLite Schema

SQLite uses boring, migration-friendly tables:

```sql
CREATE TABLE graph_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE graph_nodes (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  path TEXT,
  language TEXT,
  qualified_name TEXT,
  start_line INTEGER,
  end_line INTEGER,
  fingerprint TEXT,
  git_head TEXT,
  collector TEXT NOT NULL,
  confidence TEXT NOT NULL,
  properties_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE graph_edges (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  collector TEXT NOT NULL,
  confidence TEXT NOT NULL,
  evidence_json TEXT,
  properties_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE graph_views (
  id TEXT PRIMARY KEY,
  root_node_id TEXT,
  resolution TEXT NOT NULL,
  query_json TEXT NOT NULL,
  output_json TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  stale INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
```

Indexes:

```sql
CREATE INDEX graph_nodes_path_idx ON graph_nodes(path);
CREATE INDEX graph_nodes_kind_path_idx ON graph_nodes(kind, path);
CREATE INDEX graph_nodes_qualified_name_idx ON graph_nodes(qualified_name);
CREATE INDEX graph_edges_source_kind_idx ON graph_edges(source_id, kind);
CREATE INDEX graph_edges_target_kind_idx ON graph_edges(target_id, kind);
```

This can evolve into a richer graph store later. The first goal is stable local
facts and fast relationship queries, not graph-database sophistication.

`graph.sqlite` uses a separate metadata contract so idiom can detect absent,
unversioned, compatible, migration-ready, or incompatible SQLite stores before
trusting them:

```json
{
  "store_kind": "sqlite-graph-v1",
  "schema_version": 1,
  "source_index_id": "optional-json-index-id",
  "created_at": "2026-05-07T00:00:00Z",
  "updated_at": "2026-05-07T00:00:00Z"
}
```

`context_status` reports the active SQLite graph and the JSON export. A future
SQLite schema must be reported as incompatible rather than silently read by
older idiom binaries. Malformed current-schema SQLite files are reported as
invalid, and `context_index` is allowed to replace invalid or incompatible
local graph artifacts during refresh.

## Invalidation

Freshness must be explicit.

Invalidate or mark stale when:

- file fingerprint changes
- git head changes
- relevant config/build files change
- collector version changes
- optional language backend availability changes

Do not block all queries on stale data. Return stale markers and let the model or
caller decide whether to refresh, unless a command explicitly requires fresh
context.

Freshness policies:

- `allow_stale`: return stale data with warnings.
- `refresh_if_stale`: refresh affected scope first.
- `require_fresh`: fail if refresh cannot complete.

Default interactive behavior should be `allow_stale` for orientation and
`refresh_if_stale` for packet generation.

## Internal Package Shape

Proposed packages:

```text
internal/comprehension/
  graph.go
  store.go
  indexer.go
  collectors.go
  summaries.go
  ranking.go
  views.go
  packet.go
  trace.go
```

Responsibilities:

- `internal/comprehension` owns graph schema, storage, indexing, ranking, view
  construction, summary provenance, and packet-ready evidence.
- `internal/workspace` exposes tools that call comprehension APIs.
- `internal/architect` accepts context evidence but does not know how to index
  repositories.
- `internal/agent` consumes tools and packets. It should not own graph policy.
- `cmd/idiom` exposes CLI commands for build/status/query/pack.

This keeps durable repository comprehension separate from runner strategy.

## CLI Shape

Proposed commands:

```bash
./idiom context build --path . --mode standard
./idiom context status
./idiom context query --task-file /tmp/task.md --resolution module
./idiom context zoom --handle symbol://internal/foo.go#Bar --resolution symbol
./idiom context pack --task-file /tmp/task.md --output /tmp/context-view.json
./idiom architect packet --task-file /tmp/task.md --context-view /tmp/context-view.json --output /tmp/packet.json
```

The CLI should work without starting an inference session.

## Rollout Plan

### Phase 0: Design And Fixtures

- Add this design document.
- Add small fixture repositories for Go and one dynamic language.
- Define graph schema and golden JSON fixtures.
- Add eval scenarios that measure tool-call and token reduction for orientation
  tasks.

Acceptance:

- Design accepted.
- Fixtures cover symbol lookup, imports, tests, and one stale-file scenario.

### Phase 1: Static Graph MVP

- Implement the SQLite graph store and keep the JSON export compatible with the
  node/edge model.
- Index files, directories, packages, symbols, imports, and tests through
  language-agnostic graph contracts. Go and C++ use existing semantic helpers;
  GDScript has a dedicated Godot-aware collector for scripts, scenes,
  resources, and autoloads; other recognized source families get generic
  file/symbol/import coverage.
- Add `context_index`, `context_status`, `context_query`, and `context_zoom`.
- Add git/fingerprint invalidation.
- Add small generated fixtures in focused tests, including at least one non-Go
  source family.
- Keep the runner unchanged.

Acceptance:

- `context_query` can answer package/file/symbol relationship questions with
  evidence handles.
- Existing workspace tools continue to pass.
- No runner behavior changes are required.
- Stale file changes are reported by `context_status`.
- Query results include confidence tiers and evidence handles.

Verification:

```bash
go test ./internal/comprehension/... ./internal/workspace/...
go test ./...
```

### Phase 2: Packet-Ready Views

- Add `context_pack`.
- Let `idiom architect packet` accept context-view evidence.
- Add packet lint checks for stale or missing context views when required.
- Update docs for architect packet flow.

Acceptance:

- A task can generate a packet with candidate files, symbols, tests, risks, and
  unknowns without user-provided file flags.
- Planner prompt still says to use only the packet.
- Packet evidence distinguishes exact, syntax, heuristic, and summary facts.

Verification:

```bash
go test ./internal/comprehension/... ./internal/architect/... ./cmd/idiom/...
go test ./...
```

### Phase 3: Hierarchical Summaries

- Build deterministic repository, subsystem, package, file, and symbol views.
- Add model-assisted summary generation through `idiom artifact` or a bounded
  local model path only after deterministic views are stable.
- Add summary invalidation and provenance checks.

Acceptance:

- A model can ask for repository, subsystem, file, and symbol views without raw
  file flooding.
- Summary output cannot introduce unobserved files or symbols.
- Stale summaries are marked or refreshed.

### Phase 4: Discovery Query Backing

- Teach `discovery_query` to use the comprehension index when available.
- Preserve existing output shape and artifact path behavior.
- Add context index/view IDs to full query artifacts.

Acceptance:

- Existing `discovery_query` tests remain valid.
- Repeated broad discovery on the same repo uses fewer live probes when the index
  is fresh.

### Phase 5: Runtime Trace Packs

- Add `context_trace_pack` for Go test JSON, stack traces, and coverage.
- Represent runtime traces as graph nodes and runtime edges.
- Integrate trace evidence into `context_pack` for failing-test tasks.
- Keep the public trace schema language-agnostic; language-specific parsers are
  collectors behind that schema.

Acceptance:

- Failing-test tasks can produce packet evidence with executed files, failing
  anchors, and likely impacted symbols.
- Runtime facts are clearly labeled and do not overwrite static facts.

### Phase 6: Multi-Language Expansion

- Add collectors for TypeScript, Python, Rust, Java, and C/C++ by reusing
  existing language tool surfaces.
- Keep feature parity opportunistic; do not pretend every language has exact
  calls/dataflow.
- Add per-language coverage reporting.

Acceptance:

- `context_status` makes language/backend coverage visible.
- Unsupported relationships return explicit gaps, not empty confidence.

## Evaluation

Add eval metrics for:

- orientation tool calls before first useful answer
- token volume in discovery/tool results
- path hallucination rate
- packet completeness: files, symbols, tests, risks, unknowns
- stale context detection
- final-answer evidence grounding
- model retry loops after failed symbol/path probes

Add scenario families:

- "Explain subsystem"
- "Locate implementation"
- "Plan refactor"
- "Impact analysis"
- "Failing test localization"
- "Architecture boundary review"

Compare:

- baseline direct tools
- current `discovery_query`
- `context_pack` generated packet
- `context_pack` plus runtime trace when applicable

Success criteria should be practical:

- fewer broad reads
- fewer repeated searches
- better packet grounding
- no increase in unsafe writes or strategy forcing
- no regression in existing `go test ./...`

## Development Checklist

- [x] Create `internal/comprehension` package with schema, store, and graph
  types.
- [x] Add bootstrap store/version handling for
  `.idiom/comprehension/graph.json`.
- [x] Activate `.idiom/comprehension/graph.sqlite` as the primary graph store
  while preserving `.idiom/comprehension/graph.json` as an export.
- [x] Add rich Go, C++, Python, TypeScript/JavaScript, Rust, Java, and
  GDScript/Godot file/package/symbol/import/test collectors, plus a generic
  cross-language fallback collector.
- [x] Add fingerprint and staleness detection.
- [x] Expose `context_index` and `context_status`.
- [x] Expose `context_query` for file, package, symbol, import, and test
  relationships.
- [x] Expose `context_zoom` for repository, file, and symbol handles.
- [x] Add CLI equivalents under `idiom context`.
- [x] Update harness prompts so the model can use context graph tools
  effectively.
- [x] Add `context_pack` after the graph MVP is stable.
- [x] Integrate context-view evidence with architect packets.
- [x] Add packet lint/freshness checks.
- [x] Teach `discovery_query` to use the index opportunistically.
- [x] Add migration/version handling for `.idiom/comprehension/graph.sqlite`
  when incremental refresh or saved views require it.
- [x] Add runtime trace packs.
- [x] Expand collectors across language families.
- [x] Add structured live-tool/LSP relationship payloads so deep-mode evidence
  can be normalized without parsing prose-only output.
- [x] Add dependency-aware scoped refresh and reusable out-of-scope graph merge.
- [x] Add deterministic summary quality/provenance metadata.
- [x] Add audit/perf telemetry ingestion for collector versions, incremental
  refresh, dependency refresh files, summary nodes, and normalized
  relationship edges.
- [x] Add eval scenarios for token/tool-call reduction and grounding quality.

## North-Star Alignment

This design follows the north star in these ways:

- The model still chooses whether to query, zoom, plan, edit, verify, or answer.
- The harness only computes and exposes repository facts, confidence, freshness,
  and packet contracts.
- Tool outputs report observations, evidence paths, counts, handles, gaps, and
  uncertainty. They do not prescribe the next tool call.
- Hard enforcement is reserved for invariants: path permissions, write roles,
  stale-context requirements when explicitly requested, packet lint gates, and
  verification contracts.
- Ranking is advisory and transparent. It helps fit evidence into a token budget
  but does not become an edit command.
- Model-assisted summaries are labeled as summaries and remain tied to source
  facts.
- Context packets remain auditable and bounded.

## Risks

- **False confidence from summaries.** Mitigation: provenance, confidence tiers,
  summary invalidation, and no new facts in summaries.
- **Stale graph facts.** Mitigation: fingerprints, stale markers, refresh
  policies, and packet lint.
- **Heavy indexing cost.** Mitigation: quick/standard/deep modes, incremental
  updates, and scope-limited indexing.
- **Language backend inconsistency.** Mitigation: coverage reporting and explicit
  `unknown` gaps.
- **Strategy leakage in tool output.** Mitigation: review tool summaries against
  the north-star rule that evidence tools do not prescribe next actions.
- **Overbuilding before value.** Mitigation: ship graph MVP, packet views, and
  evals before runtime tracing or model summaries.

## Open Questions

- Which SQLite-backed views should be materialized first: task packets,
  subsystem summaries, or partial-refresh invalidation sets?
- Should tree-sitter be introduced directly, or should phase 1 rely entirely on
  existing language tool/index surfaces?
- What is the minimum useful test mapping for Go: package-level tests, symbol
  name heuristics, coverage, or all three?
- Should `context_pack` be a workspace tool first, a CLI command first, or both
  together?
- How much model-assisted summarization is acceptable for default local-model
  runs, given token and latency constraints?

## Immediate Next Step

With the implementation checklist complete, the next expansion should harden the
eval fixtures and run live comparisons: direct tools versus `discovery_query`,
`context_pack`, and `context_pack` plus runtime trace evidence on orientation,
localization, planning, and failing-test tasks.
