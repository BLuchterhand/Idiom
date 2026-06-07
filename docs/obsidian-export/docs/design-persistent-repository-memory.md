# Persistent Repository Memory And Context Graph

Status: design guidance

This is one pillar in the coordinated design map:
[harness-design-map.md](harness-design-map.md). It supplies the evidence
substrate for contract-first autonomy, capability routing, auditable planning,
local-model context budgets, eval/ranker learning, and sidecar APIs.

## Purpose

Persistent repository memory is one of Agent Harness's defining pillars. It is the
substrate that lets a local or smaller model work in a real repository without
reconstructing the same map through repeated `rg`, raw file reads, and prompt
stuffing.

The target is not an autonomous graph planner. The target is a local,
inspectable, evidence-linked repository memory that can answer graph-shaped
questions, generate bounded views, feed architect packets, inform verification,
and learn from completed runs while preserving Agent Harness's north star:

- The model owns strategy.
- The harness owns contracts.

This document sits above
[codebase-comprehension-memory-design.md](codebase-comprehension-memory-design.md)
and [architect-packets.md](architect-packets.md). Those documents describe
implementation mechanics and packet flow. This document defines the product
pillar: why the memory exists, how it differs from competitive baselines, which
contracts it must expose, and how Agent Harness will measure whether the pillar is
working.

## Pillar Claim

Agent Harness should make repository context durable, fresh, queryable, and auditable.
The same graph facts should be usable by:

- an Agent Harness lane before the first model call
- `context_pack` during planning and implementation
- `context_query` and `context_zoom` during model-directed exploration
- `context_trace_pack` after a failing command or runtime trace
- architect packets and deterministic plan audit
- `harness_audit`, evals, and ranker learning after the run
- LLM agents through `harness tool`

The differentiator is not "Agent Harness also parses symbols." LLM agent does that
well. The differentiator is that repository knowledge becomes a durable local
contract surface: source-linked, freshness-aware, confidence-labeled,
ranker-improvable, trace-integrated, and replayable from logs.

## Competitive Baseline

### LLM Agent Repo Maps

LLM-agent repo maps are the baseline Agent Harness must respect. Mature
repo maps send the model important classes, functions, types, signatures, and
critical definition lines from the whole git repository, then use graph ranking
to select the most relevant map content inside the active token budget. Modern
implementations use syntax-aware indexing so they can extract definitions and
references across many languages.

LLM agents also have practical product features around ask/code/architect
modes, lint/test repair loops, and broad language support.

Implication for Agent Harness: a token-budgeted repo map is table stakes. Agent Harness should
not claim differentiation merely because it can list symbols or rank files.
Agent Harness must go further by persisting graph facts across runs, exposing freshness
and confidence, connecting context to contracts, and measuring localization
quality in evals.

### RepoGraph

RepoGraph's 2024 arXiv preprint models repository structure as a line-level
graph over definitions and references, then exposes subgraph retrieval as a
plug-in for procedural and agent frameworks (<https://arxiv.org/abs/2410.14684>).
Its SWE-bench-Lite experiments show consistent gains when plugged into RAG, Agentless,
AutoCodeRover, and SWE-agent: for example, Agentless improves from 27.33% to
29.67% resolve rate, AutoCodeRover from 19.00% to 21.33%, and SWE-agent from
18.33% to 20.33% in the reported table
(<https://ar5iv.labs.arxiv.org/html/2410.14684v2>, an HTML rendering of the
same arXiv preprint). RepoGraph also improves file/function/line localization
coverage across the same baselines.

Implication for Agent Harness: graph retrieval should be available as an optional model
action and as deterministic packet context. The graph should help localization,
but it must not become a hidden edit plan.

### Codebase-Memory MCP

Codebase-Memory is the closest product signal cited in this draft for
persistent codebase memory exposed to agents. The 2026 preprint reports a
persistent tree-sitter knowledge graph over 66 languages, served through MCP,
evaluated across 31 repositories with 83% answer quality versus 92% for a
file-exploration agent, at 10x fewer tokens and 2.1x fewer tool calls
(<https://arxiv.org/abs/2603.27277>). The linked GitHub project documentation is
mutable and must be re-checked in the release-evidence bundle before any
product claim should treat it only as provisional evidence
for operational claims about a SQLite-backed graph, auto-sync, CLI invocation of
every MCP tool, graph schema introspection, call-path tracing, change-impact
mapping, runtime trace ingestion, and a 99.2% token-reduction benchmark for five
structural queries compared with file-by-file grep exploration
(<https://github.com/DeusData/codebase-memory-mcp>).

Implication for Agent Harness: persistent graph tools are becoming a product category.
Agent Harness should not compete as a generic MCP server alone. Its graph must be bound
to Agent Harness-specific contracts: target roles, packet freshness, verification,
audit logs, local-model budgets, and ranker feedback.

### CodePlan

CodePlan frames repository-level coding as planning over repository
dependencies, change may-impact analysis, previous edits, and task instructions.
It targets multi-file tasks such as package migration and temporal edits where
the correct change spans 2 to 97 files. Microsoft reports that CodePlan gets
5 of 6 repositories to pass validity checks while baselines with the same
context type get none
(<https://www.microsoft.com/en-us/research/publication/codeplan-repository-level-coding-using-llms-and-planning/>,
<https://arxiv.org/abs/2309.12499>).

Implication for Agent Harness: memory should support multi-step planning and blast
radius analysis, but Agent Harness should keep the final plan model-owned and the
dependency/may-impact facts harness-owned.

### Code Property Graphs, Kythe, And SCIP

Joern's code property graph is a directed, edge-labeled, attributed multigraph
that merges syntax, control-flow, and data-flow representations into one
queryable structure (<https://docs.joern.io/code-property-graph/>,
<https://cpg.joern.io/>). That is the right mental model for deep semantic
analysis, but it is too heavy to make mandatory for Agent Harness's first-class local
experience.

Kythe provides a language-agnostic graph for source-code facts, cross-references,
type information, and build/compiler metadata. Two Kythe principles are directly
applicable: tools should degrade gracefully with incomplete data, and incomplete
data is preferable to incorrect data (<https://kythe.io/docs/kythe-overview.html>).
Kythe's schema also separates file anchors, semantic nodes, facts, and directed
edges, which maps well to Agent Harness's handle/provenance needs
(<https://kythe.io/docs/schema-overview.html>).

Sourcegraph precise code navigation uses the SCIP Code Intelligence Protocol as
a language-agnostic index format and falls back to search-based navigation when
precise indexes are unavailable
(<https://sourcegraph.com/docs/code-navigation/precise-code-navigation>). That
fallback story is important: Agent Harness should use exact language indexes when
available and make lower-confidence fallback explicit when they are not.

Implication for Agent Harness: use a graph vocabulary that can absorb richer
CPG, Kythe, SCIP, LSP, and compiler facts without pretending all relationships
are equally exact.

## Research Findings To Carry Forward

- Repository context is a performance lever. SWE-agent's 2024 arXiv preprint
  argues that the
  agent-computer interface materially affects agents' ability to navigate repos,
  edit files, and run tests (<https://arxiv.org/abs/2405.15793>).
- Compact repo maps are expected. An LLM agent's default `--map-tokens` value
  is 1k tokens, with dynamic expansion when no files are in chat
 .
- Graph context improves localization and resolve rates, but can increase token
  use if agents over-call it. RepoGraph's arXiv-preprint table reports higher
  average token use after adding graph context for all four tested baselines,
  even while resolve rates improve
  (<https://ar5iv.labs.arxiv.org/html/2410.14684v2>).
- Persistent graph memory can reduce exploration cost dramatically. The
  Codebase-Memory paper reports 10x fewer tokens and 2.1x fewer tool calls at a
  modest answer-quality loss against a file-exploration agent
  (<https://arxiv.org/abs/2603.27277>).
- Planning needs may-impact analysis, not only retrieval. CodePlan's 2023 arXiv
  preprint result on multi-file repository edits supports treating dependency
  and impact facts as planning inputs (<https://arxiv.org/abs/2309.12499>).
- Functional correctness alone is not enough. Needle in the Repo's 2026 arXiv
  preprint reports that 13.3% of evaluated outcomes passed functional tests but
  failed a structural oracle, and that architectural pressures such as
  dependency control remain hard (<https://arxiv.org/abs/2603.27745>).
- CI repair is a distinct repository-level benchmark shape. CI-Repair-Bench's
  2026 arXiv preprint contains 567 CI failure instances from 103 repositories
  and evaluates repairs by full original CI workflow re-execution; its best
  reported LLM reaches only 18.9% repair success
  (<https://arxiv.org/abs/2604.27148>).

The practical lesson is that Agent Harness's memory cannot be judged only by whether a
single answer sounds right. It must be evaluated on localization, token/tool
cost, stale-context prevention, required-test discovery, structural correctness,
and verification outcomes.

## Agent Harness Target Architecture

The persistent memory pillar has six layers:

```text
source workspace
  -> collectors
  -> repository graph store
  -> view builders
  -> packets and tools
  -> lanes, audits, evals, and LLM agents
  -> ranker and feedback loop
```

### 1. Collectors

Collectors observe facts and record provenance. They do not choose the next
model action.

Collector families:

- file and directory discovery
- language syntax extraction
- semantic language backends and LSPs
- build and package manifests
- imports, includes, dependency edges, routes, config, and generated artifacts
- tests and test-to-code heuristics
- diagnostics and source sanity results
- git history, blame, co-change, and ownership signals
- runtime traces, stack traces, coverage, and command output
- Agent Harness conversation logs, telemetry, and `harness_audit` findings for harness
  behavior tasks

Collectors must emit confidence and coverage. Unsupported relationships should
show up as explicit gaps, not as silent empty results.

### 2. Repository Graph Store

The store is the durable local index under `.harness/comprehension/`,
centered on `graph.sqlite` with `graph.json` as an inspectable export. It owns:

- graph metadata and schema compatibility
- stable node and edge identifiers
- content fingerprints and git/worktree fingerprints
- collector versions and language capability reports
- normalized properties and evidence records
- materialized `graph_views` cache rows
- stale markers for nodes, edges, summaries, views, and traces

SQLite is the right default. It is local, inspectable, easy to define, and good
enough for Agent Harness graph queries. The schema should remain
migration-friendly rather than optimize prematurely for a graph database.

### 3. View Builders

View builders translate the broad index into bounded model-facing context:

- repository view
- subsystem view
- package/module view
- file view
- symbol view
- range/excerpt view
- impact view
- trace view
- task localization view

Views are not prompts. They are structured observations with handles, evidence,
confidence, freshness, and gaps. A model may ask to zoom, query, edit, verify,
or answer after reading a view; the view itself must not prescribe that choice.

### 4. Packets

Packets are task-scoped envelopes that combine selected views with contracts:

- allowed and forbidden paths
- target roles: editable, read-only evidence, analogy, forbidden
- candidate files, symbols, tests, and anchors
- constraints and profile rules
- freshness requirements
- unresolved gaps
- verification commands
- handoff state such as `pack_id`

Architect packets remain the planning boundary. `context_pack` supplies better
evidence; `harness architect packet` owns the packet schema and gates.

### 5. Lanes, Audits, And Evals

The runner consumes tools and packets. It should not own graph policy.

The graph feeds:

- `harness lane implement` and `plan-execute` preloaded `context_pack`
- deterministic `architect lint`, `budget`, `plan-audit`, `packet-compare`, and
  `replay-compare`
- `discovery_query` orientation and cache reuse
- `harness_audit` behavior findings
- eval suites that compare direct exploration, discovery, context packs, and
  trace-backed packs

### 6. Ranker And Feedback Loop

Ranker learning updates localization weights from observed outcomes, but ranking
remains advisory. Ranker output helps fit evidence into a budget; it never
becomes an edit command.

## Index, View, Packet Boundary

These three terms must stay distinct.

| Concept | Scope | Source of truth | Model-facing role | Must include |
| --- | --- | --- | --- | --- |
| Index | Broad repository memory | `.harness/comprehension/graph.sqlite` | Not normally injected directly | facts, fingerprints, collector provenance, confidence, freshness |
| View | Bounded resolution-specific slice | `graph_views` row plus exported JSON | Observation the model can read or zoom from | handles, claims, evidence, uncertainty, stale state, token budget |
| Packet | Task-scoped planning/execution envelope | architect packet artifact | Contract the model plans or executes within | selected views, target roles, constraints, validation, gates, handoff |

Failure modes when the boundary blurs:

- If the index is injected directly, context becomes bulky and stale semantics
  disappear.
- If a view contains strategy, an evidence tool starts steering the model's next
  action.
- If a packet is only evidence, it cannot enforce target roles, freshness, or
  verification.
- If ranker scores are hidden, the model and auditor cannot distinguish a strong
  candidate from a token-budget compromise.

## Graph Schema

Agent Harness's graph should stay simple enough to inspect and broad enough to absorb
future precision. The canonical shape is a property graph with typed nodes,
typed edges, JSON properties, evidence records, confidence tiers, and
fingerprints.

### Node Kinds

Core node kinds:

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
- `audit_finding`

Each node should carry:

- stable ID
- kind
- path when applicable
- language when applicable
- qualified name when applicable
- line or range anchors when applicable
- content fingerprint
- git head or dirty-worktree fingerprint
- collector and collector version
- confidence tier
- created and updated timestamps
- properties JSON

Stable IDs should favor semantic stability over raw line numbers:

```text
repo://<root-fingerprint>
file://<path>
symbol://<path>#<qualified-name>:<body-fingerprint>
summary://<scope-id>/<resolution>/<source-fingerprint>
trace://<command-fingerprint>/<time-or-run-id>
```

Line numbers are evidence, not identity. If a symbol moves without changing
name/body, its handle should survive.

### Edge Kinds

Core edge kinds:

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
- `fails_at`
- `covered_by`
- `diagnoses`

Edges should carry:

- source and target node IDs
- kind
- collector and collector version
- confidence tier
- evidence path/range or command
- freshness fingerprint
- optional reason text for inferred edges
- properties JSON

Static and runtime edges must remain separate. A static `calls` edge and a
runtime-observed `runtime_calls` edge answer different questions.

### Confidence Tiers

Use a small vocabulary everywhere:

- `exact`: compiler, LSP, test, runtime, or precise-index backed
- `syntax`: parser or tree-sitter backed
- `heuristic`: convention, regex, import-path inference, or co-change
- `summary`: generated compression over listed facts
- `unknown`: unsupported, stale, missing, or intentionally not collected

Packet gates may require a minimum confidence tier. Orientation views may return
lower-confidence facts with warnings.

## View Contract

Every view returned by `context_query`, `context_zoom`, `context_pack`, or
`context_trace_pack` should include:

```json
{
  "id": "view://...",
  "index_id": "graph-content-addressed-id",
  "resolution": "file|symbol|package|repo|task|trace",
  "query": {},
  "freshness": {
    "fresh": true,
    "policy": "allow_stale|refresh_if_stale|require_fresh",
    "stale_paths": [],
    "workspace_state_fingerprint": "..."
  },
  "claims": [],
  "handles": [],
  "evidence": [],
  "ranked_items": [],
  "gaps": [],
  "budget": {
    "requested_tokens": 8000,
    "estimated_tokens": 4200,
    "truncated": false
  }
}
```

View outputs should prefer concrete statements:

- "No Go collector was available for this path."
- "This call edge is heuristic because it came from syntax matching."
- "The view is stale because `internal/foo.go` changed after indexing."
- "The top candidate is high because of symbol match, test proximity, and
  recent co-change."

View outputs should avoid strategy:

- Do not say "edit this file next."
- Do not say "run this tool next."
- Do not say "the fix is probably X" unless that is an observed diagnostic,
  trace, or source claim with evidence.

## Freshness Model

Freshness is a first-class contract. The graph is useful only if callers can
tell whether it matches the active workspace.

Invalidate or mark stale when any of these change:

- file content fingerprint
- file existence or path
- git head
- dirty-worktree fingerprint
- build or package config
- collector version
- optional backend availability
- ranker weights
- runtime trace directory state
- summary source-fact fingerprint
- packet/view referenced path set

Freshness policies:

- `allow_stale`: return stale facts with warnings. Default for orientation.
- `refresh_if_stale`: refresh affected scope before returning if possible.
  Default for packet generation.
- `require_fresh`: fail if the graph or referenced view cannot be made fresh.
  Required for gated architect packets and high-confidence verification claims.

The active `graph_views` cache contract should remain content-addressed over
the active graph ID, canonical query inputs, mode, budget, scope, required tests,
forbidden paths, intent, ranker workspace-state fingerprint, and trace state.
The exported `views/<pack_id>.json` file is an inspection artifact; SQLite is
the source of truth.

Freshness must be visible at use time. A model should not need to call
`context_status` separately to learn that a packet is stale.

## Ranker Learning

The ranker is Agent Harness's memory-improvement loop. It should learn which graph facts
were useful without collapsing into a self-fulfilling popularity contest.

### Signals

Positive signals:

- user-provided explicit file, symbol, or test
- successful verified edit touched a ranked file
- verification failure trace matched a ranked candidate
- model zoomed into a candidate and then edited or verified successfully
- architect plan referenced candidates that survived plan-audit
- tests suggested by the pack were run and relevant
- repeated successful co-change between files

Negative signals:

- ranked item led to zero-hit source reads or searches
- ranked item was repeatedly explored in failed wrong-scope sessions
- stale view was served and then rejected by packet lint
- candidate was outside allowed target roles
- candidate behaved like a hub and inflated rankings without task evidence
- a required changed path was absent from top-k

### Update Contract

Every learning update should be written to
`.harness/ranker/learning_log.jsonl` with:

- timestamp
- source event type
- graph index ID
- task or eval ID
- affected node or path
- old and new weight
- reason
- verification outcome
- whether the signal is training, eval, or manual

Manual controls such as `harness ranker boost` and `harness ranker penalize` should
stay available, but automatic updates should be gated by verified outcomes or
explicit eval labels.

### Guardrails

- Do not train on held-out eval gold during the measured run.
- Decay old weights by graph epoch and repository churn.
- Cap hub centrality so common utility files do not dominate every pack.
- Keep per-task lexical/symbol evidence stronger than global popularity.
- Recompute or invalidate cached views when ranker weights change.
- Report ranker reasons in the view so users can inspect why a file surfaced.

## Trace Integration

Static graphs help orientation. Runtime traces help explain behavior.

`context_trace_pack` should accept a command, capture bounded output, parse known
trace formats, and write runtime facts without overwriting static facts.

Initial trace sources:

- command stdout/stderr excerpts
- Go test JSON
- Go coverprofiles
- LCOV
- generic stack frames with file:line anchors
- Python, JavaScript/TypeScript, JVM, C/C++, and GDScript path formats where
  parseable
- Agent Harness conversation JSONL and telemetry JSONL for harness behavior debugging

Trace nodes and edges:

- `runtime_trace` node for the command/run
- `command` node for the executed command
- `fails_at` edge to source range or test node
- `covered_by` edge from symbol/file to trace when coverage is available
- `runtime_calls`, `runtime_reads`, `runtime_writes` when observed
- `diagnoses` edge from an audit finding or diagnostic to evidence

Trace facts must carry:

- command
- working directory
- exit status
- start/end time
- environment limitations
- redaction status
- parser used
- confidence tier
- raw artifact path
- bounded excerpts used in packet views

Trace-backed `context_pack` should be favored for failing-test tasks. A failing
stack frame is often a stronger localization signal than lexical similarity, but
the packet should label it as runtime evidence from one command, not as a proof
that no other path is involved.

## CLI And Tool API

The CLI and workspace tool API should expose the same capability vocabulary.
Every command must work without starting an inference session.

### Core Commands

```bash
harness context build --path . --mode standard
harness context status --json
harness context query --query "what calls LoadView?" --resolution symbol --json
harness context zoom --handle symbol://internal/repository/comprehension/views.go#LoadView --resolution symbol --json
harness context pack --task-file /tmp/task.md --mode plan --output /tmp/context-view.json
harness context impact --path internal/repository/comprehension/views.go --json
harness context trace-pack --command "go test ./internal/repository/comprehension/..." --path . --json
harness architect packet --task-file /tmp/task.md --schema planning_v2 --context-view /tmp/context-view.json --output /tmp/packet.json
```

### LLM Agent API

LLM agents should be able to call the same functions through `harness tool`:

```bash
harness tool context_pack --input '{"task":"localize graph view cache invalidation","mode":"plan","limit":8}'
harness tool context_query --input '{"query":"tests for internal/repository/comprehension/views.go","resolution":"symbol"}'
harness tool context_zoom --input '{"handle":"file://internal/repository/comprehension/views.go","resolution":"file"}'
harness tool context_trace_pack --input '{"command":"go test ./internal/repository/comprehension/...","path":"."}'
harness tool harness_audit --input '{"purpose":"behavior"}'
```

### Result Contract

Tool results should be shaped for machines and readable by models:

```json
{
  "tool": "context_pack",
  "summary": "8 candidates, 3 tests, fresh graph, 1 unsupported relationship gap",
  "output": {
    "pack_id": "ctxp_...",
    "index_id": "idx_...",
    "fresh": true,
    "candidate_files": [],
    "candidate_symbols": [],
    "tests": [],
    "risks": [],
    "gaps": []
  },
  "error": ""
}
```

Errors should distinguish:

- no index
- stale and refresh failed
- unsupported language/backend
- malformed handle
- query too broad
- budget truncation
- incompatible store schema
- permission or protected-path rejection

## Eval Benchmark Design

The memory pillar needs its own benchmark, not only end-to-end SWE pass rates.
The benchmark should answer whether persistent graph memory makes Agent Harness more
reliable, cheaper, and more auditable.

### Scenario Families

- Explain subsystem.
- Locate implementation.
- Plan refactor.
- Impact analysis.
- Required-test discovery.
- Failing-test localization.
- Runtime trace triage.
- Architecture boundary review.
- CI failure triage.
- Harness behavior debugging from conversation and telemetry logs.

### Data Sources

- Recent Agent Harness conversation logs with known changed paths and verification
  outcomes.
- `.harness/comprehension` graph snapshots and view cache rows.
- Existing Agent Harness eval fixtures.
- Small synthetic fixture repos with known graph relationships.
- Real internal tasks replayed at fixed commits.
- Selected public benchmark shapes inspired by SWE-bench, CodePlan,
  NITR, and CI-Repair-Bench.

Each benchmark record should include:

- task ID
- repository and commit
- dirty-worktree setup if any
- task text
- allowed/protected paths
- gold changed paths
- gold relevant tests
- expected symbols or graph claims when known
- required validation command
- baseline graph ID and collector coverage
- trace artifact if the scenario is trace-backed
- ranker state fingerprint
- model and run options

### Conditions To Compare

- raw search/read tools only
- `discovery_query`
- `context_pack`
- `context_pack` plus `context_zoom`
- `context_pack` plus `context_trace_pack`
- `context_pack` with learned ranker weights
- architect packet v1 versus `planning_v2`

### Metrics

Localization:

- top-1, top-3, top-5 changed-path hit rate
- top-k required-test hit rate
- function/symbol hit rate where gold exists
- false-positive hub rate
- missing protected-path or forbidden-path violations

Cost:

- tool calls before first useful edit or answer
- broad raw reads before first edit
- repeated zero-hit searches
- input/output token estimate for tool observations
- graph cache hit/miss/stale ratios
- context pack build latency

Correctness and contracts:

- post-write verification rate
- pass/fail/blocker status
- stale-context prevention
- packet lint failures
- final-answer grounding
- structural oracle pass rate for maintainability scenarios
- CI workflow repair success for CI scenarios

Learning:

- ranker weight update count by reason
- improvement after training on training split
- no improvement regression on held-out split
- hub inflation trend
- stale cache invalidation precision

Auditability:

- every result links to pack ID, graph ID, trace ID, and verification artifact
- every failed scenario has a deterministic reason bucket
- replay can reproduce candidate rankings from stored state

### Acceptance Thresholds For Promotion

Before persistent memory is treated as a default pillar, it should meet these
thresholds on a fixed benchmark slice:

- Top-5 changed-path localization improves by at least 15 percentage points over
  raw search/read and does not regress versus active `discovery_query`.
- Broad raw file reads before first edit drop by at least 30%.
- Tool-observation token volume drops by at least 40% for orientation tasks.
- Required-test top-5 hit rate reaches at least 80% on tasks with known tests.
- Stale packet use is zero when `require_fresh` is enabled.
- `graph_views` cache hit rate is at least 70% for repeated no-op
  plan-execute workflows.
- Trace-backed packs beat non-trace packs on failing-test localization.
- No benchmark condition increases protected-path violations or final answers
  without required verification.

The exact thresholds can be revised after the first benchmark run, but they
should remain quantitative. "Feels better" is not a pillar acceptance criterion.

## Implementation Plan

### Phase 0: Reality Baseline

- Record active `context_pack`, `context_query`, `context_zoom`,
  `context_trace_pack`, `harness_audit`, and ranker behavior.
- Build a 30 to 50 task localization slice from recent logs.
- Add a `memory_benchmark` report that can compare raw tools, discovery, packs,
  and trace-backed packs.
- Document active gaps as facts, not design map optimism.

Exit criteria:

- Benchmark records include gold changed paths, tests where known, pack IDs,
  graph IDs, cache metrics, and verification outcomes.
- A no-op run can reproduce the same context pack from cached graph state.

### Phase 1: Freshness And Store Contract

- Keep SQLite as source of truth and JSON exports as inspection artifacts.
- Harden store compatibility and migration checks.
- Make `context_status` report per-language collector coverage, stale paths,
  stale symbols, stale views, ranker fingerprint, and trace fingerprint.
- Ensure view freshness is reported inside every view result.
- Add regression tests for scoped view invalidation.

Exit criteria:

- Changing a referenced file marks dependent views stale.
- Changing ranker weights or adding traces busts relevant cached packs.
- Incompatible stores are reported explicitly and never read silently.

### Phase 2: View And Packet Discipline

- Normalize view output schemas across query, zoom, pack, impact, and trace.
- Make all views report confidence tiers and evidence handles.
- Keep strategy language out of tool outputs.
- Extend architect packet lint for stale views, unresolved handles, missing
  required tests, and target-role violations.
- Ensure planner/executor handoff preserves `pack_id`.

Exit criteria:

- `planning_v2` packets can be audited deterministically for freshness,
  candidate scope, tests, and constraints.
- Existing packet v1 callers remain compatible.

### Phase 3: Ranker Learning

- Define and persist learning events from verified edits, evals, manual nudges,
  zero-hit searches, and stale-context rejections.
- Add held-out eval protection so measured scenarios do not train on their own
  answer keys.
- Surface ranker explanations in every `context_pack` ranked item.
- Add hub dampening and decay.

Exit criteria:

- A successful verified edit produces an inspectable positive learning event.
- A stale or wrong-scope failed run produces an inspectable negative event.
- Re-running the benchmark can show before/after localization deltas.

### Phase 4: Runtime And Audit Trace Integration

- Expand `context_trace_pack` parsers and trace schema.
- Feed trace evidence into `context_pack` for failing-test and runtime-error
  tasks.
- Treat conversation logs, telemetry, and `harness_audit` as trace sources for
  behavior-debugging tasks.
- Add redaction and retention controls for trace artifacts.

Exit criteria:

- A failing test can produce a trace-backed packet with failing anchors,
  executed files, candidate tests, and uncertainty.
- Runtime edges are visibly separate from static edges.
- Behavior-debugging packets can cite conversation and telemetry evidence
  without hand-reconstructing raw JSONL.

### Phase 5: LLM Agent Sidecar

- Stabilize `harness tool` JSON schemas for context, trace, audit, and packet
  commands.
- Add examples for LLM-agent workflows.
- Expose machine-readable capability discovery: languages, collectors, graph
  freshness, contracts, supported trace parsers, and recommended verification.
- Keep sidecar behavior local-first and inspectable.

Exit criteria:

- An LLM agent can call `context_pack`, zoom a handle, run trace pack, and
  run `harness_audit` without starting an Agent Harness model session.
- The sidecar workflow preserves Agent Harness contracts instead of becoming a generic
  graph search daemon.

## Risks

- False confidence from summaries. Mitigation: summaries are `summary`
  confidence, source facts are fingerprinted, and generated summaries cannot
  introduce new paths or symbols.
- Stale graph facts. Mitigation: content fingerprints, per-view stale markers,
  refresh policies, packet lint, and source/test verification as final authority.
- Strategy leakage. Mitigation: view outputs report claims and uncertainty, not
  next actions.
- Overweight indexing. Mitigation: quick/standard/deep modes, scoped refresh,
  optional deep collectors, and benchmarked latency budgets.
- Language inconsistency. Mitigation: typed collector coverage and `unknown`
  gaps rather than silent absence.
- Ranker feedback loops. Mitigation: held-out evals, weight decay, hub caps,
  and explicit learning logs.
- Privacy and log retention. Mitigation: local storage, redaction status,
  configurable retention, and no cloud graph service as a default.
- Binary graph artifacts. Mitigation: keep `.harness/comprehension` ignored by
  default; if shared artifacts are ever introduced, make them opt-in and
  integrity-checked.
- CPG overreach. Mitigation: use Joern/CPG-style depth as optional enrichment,
  not as a baseline dependency.
- Competitive drift. Mitigation: benchmark against LLM-agent-style maps,
  graph-memory MCP tools, and direct exploration instead of comparing against a
  stale mental model.

## Non-Goals

- Do not replace source reads, tests, compilers, or runtime verification.
- Do not make the graph choose the model's next step.
- Do not build a monolithic natural-language repository summary and inject it
  everywhere.
- Do not claim exact semantic relationships when only syntax or heuristics are
  available.
- Do not require Joern, Kythe, SCIP, or every LSP before the memory is useful.
- Do not centralize repository memory in a hosted service.
- Do not commit `.harness/comprehension` artifacts by default.
- Do not optimize for maximum graph complexity before evals prove value.

## Acceptance Criteria

The persistent repository memory pillar is accepted when all of these are true:

- `context_pack` is the default localization source for implement and
  plan-execute lanes, and its packet IDs survive planner/executor handoff.
- Every context view includes graph ID, freshness, confidence, evidence handles,
  ranker reasons, and unresolved gaps.
- `context_status` can explain collector coverage and stale state without raw
  source spelunking.
- `graph_views` cache rows are content-addressed, reused on no-op refreshes, and
  invalidated precisely for changed referenced paths, ranker weights, and trace
  state.
- `context_trace_pack` can attach failing-test or runtime evidence to graph
  nodes without merging static and runtime relationships.
- Ranker learning writes auditable positive and negative events tied to
  verification, eval, or explicit manual input.
- Architect packet lint can reject stale or malformed context views when a gate
  requires fresh context.
- The memory benchmark reports localization, cost, freshness, cache, trace,
  verification, and auditability metrics.
- A fixed benchmark slice shows lower raw exploration cost and better
  localization without increasing unsafe writes or unverified final answers.
- LLM agents can use `harness tool` to call context, trace, and audit
  commands without starting an Agent Harness inference session.

If these criteria are met, persistent repository memory becomes more than a repo
map. It becomes Agent Harness's local evidence substrate: the graph remembers, packets
bound the work, traces explain behavior, evals measure progress, and the model
keeps strategy where it belongs.
