# Capability-Oriented Tool Routing

Status: design guidance

This is one pillar in the coordinated design map:
[harness-design-map.md](harness-design-map.md). It is the interface layer
between model intent, contract enforcement, repository memory, language
backends, verification, audit, and sidecar clients.

This document defines Agent Harness's capability-oriented tool routing pillar: the model
should ask for the capability it needs in task language, and the harness should
resolve that request to the best concrete language, graph, runtime, or generic
tool available for the target.

The design extends the existing direction in
[decisioning-north-star.md](decisioning-north-star.md): the model owns strategy;
the harness owns contracts. It also builds on
[codebase-comprehension-memory-design.md](codebase-comprehension-memory-design.md),
which gives Agent Harness a persistent graph, context views, freshness, and
confidence tiers. The goal is not fewer precise tools. The goal is less
model-visible tool-menu complexity while preserving precise backend evidence.

## External Signals

Agent systems point toward the same split:

- MCP tools are model-controlled schema-backed operations with `inputSchema`,
  optional `outputSchema`, structured results, tool annotations, error
  semantics, and audit/security expectations. Release review must re-check
  that selector before product claims. It also distinguishes
  structured `Tool Execution Errors` that can help a model self-correct from
  protocol errors that are less likely to be repairable:
  https://modelcontextprotocol.io/specification/.
- MCP annotations are useful as risk and UX hints, not as hard guarantees.
  The MCP tool-annotations post is explicit that annotations should feed policy
  engines while enforcement belongs in authorization, transport, or runtime
  controls: https://blog.modelcontextprotocol.io/.
- LLM-agent permission systems expose `ask`, `allow`, and `deny`, support
  per-agent permission overrides, and apply rules to built-ins, custom tools,
  and MCP tools.
- LLM-agent hooks expose lifecycle events around prompt submission, tool
  use, permissions, subagents, compaction, and stop conditions. These hooks can
  allow, deny, ask, or defer a tool call, and can inspect files or tool output
  before returning a structured decision.
- LLM-agent repo maps send ranked, token-budgeted file and symbol context
  with each change request, then let the model request specific files when it
  needs more code. Mature agents can also infer related files, watch IDE
  annotations, and run changed-file lint/test repair loops from configured
  commands. SWE-agent frames the agent-computer interface itself as a
  performance lever:
  its 2024 arXiv preprint says a custom ACI improves an agent's ability to edit
  code, navigate repositories, execute tests, and repair software tasks:
  https://arxiv.org/abs/2405.15793. Its ACI docs call out bounded file viewing,
  concise repository search, edit-time syntax checks, and explicit empty-output
  handling as interface choices that help agents:
  https://github.com/SWE-agent/SWE-agent/blob/main/docs/background/aci.md.
- The Language Server Protocol standardizes a capability vocabulary for
  code navigation: definition, implementation, references, call hierarchy,
  document symbols, workspace symbols, diagnostics, and related features are
  exchanged behind capability flags:
  https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/.
- Sourcegraph's precise code navigation uses language-specific indexes through
  the language-agnostic SCIP protocol and automatically falls back to
  search-based navigation when precise navigation is unavailable:
  https://sourcegraph.com/docs/code-navigation/precise-code-navigation. Kythe
  uses a language-agnostic graph for build metadata, definitions/usages, type
  information, and cross-language associations:
  https://www.kythe.io/docs/kythe-overview.html.

The shared lesson is narrow: good agent performance depends on the interface,
permissions, context substrate, tool result shape, and recovery semantics. For
Agent Harness, capability routing is the interface layer that makes those pieces legible
to smaller local models without taking strategy away from them.

## Problem

A greenfield implementation should provide useful high-precision tools, but the model-visible surface is
too language-shaped:

- `go_definition`, `cpp_definition`, `python_definition`, `ts_definition`,
  `rust_definition`, and `java_definition` are different tool names for the
  same strategic need.
- Similar duplication exists for references, implementations, callers, callees,
  file outlines, read-symbol operations, diagnostics, and related-file probes.
- `code_navigate` and `source_sanity` provide generic routes, but they
  coexist with the full language-prefixed menu rather than serving as the
  canonical contract surface.
- Prompt and recovery text can accidentally teach the model to reason about
  language tool selection instead of evidence needs.
- Tool results do not consistently report the route chosen, the precision tier,
  backend coverage, staleness, fallback use, or whether zero results mean
  "proved absent" versus "not found by this backend."
- Audit output cannot reliably distinguish a model that ignored a precise
  capability from a resolver that had no precise backend available.

The practical failure modes are visible:

- broad `read_file` and `search_content` loops after enough repository evidence
  is available
- repeated failed symbol probes because the model guesses the wrong
  language-prefixed tool or calls read-symbol without an anchor
- source verification that silently downgrades to weak checks instead of
  returning a concrete blocker
- prompt bloat from enumerating language-specific affordances
- hard-to-diagnose behavior logs because the harness sees tool names but not
  capability intent

The design must preserve precision. Language-specific tools encode real
knowledge: Go packages, C++ compile commands and includes, Python LSP behavior,
TypeScript project boundaries, Rust cargo checks, Java build tools, and Godot
resource relationships. Hiding that precision behind a generic request is only
acceptable if the result reports what actually happened.

## Design Goals

1. Let the model ask in task terms: "find references," "inspect this symbol,"
   "check source sanity," "localize this change," "trace this failing test."
2. Keep the harness responsible for route resolution, backend availability,
   permission enforcement, freshness policy, and result normalization.
3. Preserve precise language backends as adapters, not as the primary strategy
   menu in prompts.
4. Make fallback explicit: the result must say which adapter ran, which
   precision tier was achieved, which fallback was used, and what remains
   unproven.
5. Make audit and eval possible: every capability request should emit telemetry
   that connects intent, route, precision, fallback, and task outcome.
6. Keep LLM-agent compatibility through `harness tool` and existing
   language-prefixed tools during rollout.

## Non-Goals

- Do not remove language-prefixed tools in the first rollout.
- Do not replace LSPs, compilers, tree-sitter, ctags, Semgrep, Joern, or the
  comprehension graph with a generic approximation.
- Do not turn ranking into an edit plan. Ranked files and routes are evidence,
  not instructions.
- Do not make the harness choose the model's next strategic step. The resolver
  can choose an adapter for a declared capability; it should not decide whether
  the model should inspect, edit, verify, or answer.
- Do not rely on annotations or prompts for security. Permissions, protected
  paths, write scopes, and verification contracts remain deterministic gates.
- Do not make a single giant "do anything" tool that hides cost, precision, or
  side effects.

## Capability Vocabulary

Capabilities are stable contracts. Tool names, language adapters, and backend
implementations may change underneath them.

| Capability | Intent | Required Target | Typical Adapters | Result Contract |
| --- | --- | --- | --- | --- |
| `localize_change` | Rank task-relevant files, symbols, tests, risks, and gaps. | task text, optional path/scope | `context_pack`, `context_impact`, `discovery_query` | candidate files/symbols/tests, scores/signals, freshness, gaps |
| `query_context` | Ask graph-shaped orientation questions without raw source flooding. | query, optional handle/path | `context_query`, `context_zoom` | graph nodes, handles, evidence paths, confidence, staleness |
| `inspect_file` | Get a file outline or bounded source range. | path or file handle | language file-outline tools, `context_zoom`, `read_file` fallback | declarations/ranges or exact range with confidence |
| `inspect_symbol` | Read a symbol body or definition-range snippet after anchoring. | symbol plus path/handle | language read-symbol tools, LSP definition plus read range, ctags/AST fallback | resolved symbol, body/range, provenance |
| `find_symbol` | Locate matching symbols in a file, package, or workspace. | pattern/query, optional scope | workspace/file symbol adapters, context graph, ctags | matches with paths/ranges/kinds |
| `find_definition` | Resolve the definition for a symbol or position. | symbol/query plus path/range | LSP/native definition tools, graph, ctags/text fallback | definition locations and absence confidence |
| `find_references` | Find uses of a symbol. | symbol plus scope/path | LSP/native references, graph edges, text fallback | reference locations, precision, truncation |
| `find_implementations` | Find concrete implementations or overrides. | interface/trait/base symbol plus scope | LSP/native implementations, graph, language heuristics | implementation locations with confidence |
| `calls_from` | Find outgoing calls from a function/method. | symbol plus path/scope | LSP call hierarchy, native language indexes, graph | callees, external calls, confidence |
| `calls_into` | Find incoming calls to a function/method. | symbol plus path/scope | LSP call hierarchy, native references, graph | callers, confidence, truncation |
| `explain_dependency` | Explain imports/includes/dependencies and likely blast radius. | path/package/module/dependency | `dependency_graph`, C++ include tools, context graph, native importers | dependency edges, affected candidates, gaps |
| `related_files` | Find tests, peers, generated files, configs, or companion source. | path/pattern | language related-file tools, graph test/import edges, path heuristics | related paths with relationship kinds |
| `source_sanity` | Check changed source files for syntax/type/build diagnostics. | file or narrow directory | language diagnostics, compiler/typechecker/LSP, generic syntax sanity | diagnostics, precision, blocker if source-aware check unavailable |
| `trace_runtime_failure` | Attach failing runtime/test evidence to source handles. | command/log/path | `context_trace_pack`, test parsers, stack parsers | trace artifact, failing anchors, executed files |
| `verify_edit` | Run the verification appropriate to changed files and task risk. | changed paths plus requested checks | `source_sanity`, focused tests/builds, `git diff --check` for prose/config | verification status, commands, diagnostics, blockers |
| `audit_behavior` | Audit harness/tool/prompt behavior. | optional log path/scope | `harness_audit`, `harness_feedback` | findings, counts, evidence paths, uncertainty |

Write capabilities (`edit_file`, `write_file`, `append_file`, `apply_patch`,
`submit_plan`, `delegate_edit`) remain separate because they carry side effects.
The routing pillar can expose write permission state and target roles, but it
should not blur read/navigation capabilities with mutation capabilities.

## Resolver Architecture

Capability routing adds a resolver between model-facing tool requests and
concrete adapters.

```text
model request
  -> capability schema validation
  -> permission and target-role gate
  -> target resolution and language detection
  -> capability registry lookup
  -> adapter candidate scoring
  -> primary adapter execution
  -> bounded fallback execution, if allowed
  -> normalized capability result
  -> telemetry/audit event
```

### Core Components

1. **Capability Registry**
   Defines stable capability IDs, required inputs, optional inputs, precision
   requirements, fallback policy, risk level, write/read effect, and aliases.
   The registry is an inner contract: adapters implement it, prompts describe
   it, and audits score it.

2. **Adapter Registry**
   Registers concrete adapters by capability:
   language-prefixed workspace tools, LSP operations, comprehension graph
   queries, structural search, dependency graph, runtime trace parsing, git
   history, generic reads/searches, and shell-backed verification commands.

3. **Workspace Capability Snapshot**
   A typed snapshot computed per turn or per tool manifest:
   indexed languages, backend availability, fresh/stale graph state, available
   LSPs, known project manifests, permission mode, target roles, protected
   paths, changed files, and active verification obligations.

4. **Resolver**
   Chooses the adapter for a declared capability. Resolution is deterministic
   and evidence-shaped: infer language from target path, prefer exact semantic
   backends when available, honor freshness and permission policies, then
   degrade to narrower fallbacks only when the capability allows it.

5. **Result Normalizer**
   Converts adapter-specific output into a common result envelope with
   precision, confidence, route, fallback, observations, handles, diagnostics,
   artifacts, gaps, and error semantics.

6. **Telemetry and Audit Sink**
   Emits one event per request with `capability`, `target`, `adapter`, precision
   tier, fallback details, stale state, result counts, truncation, errors, and
   whether the result was later followed by broad generic search or successful
   verification.

### Adapter Scoring

The resolver should score candidate adapters with boring rules:

- target match: exact file/symbol/handle beats inferred workspace scope
- language match: path-derived language beats user-provided language override
  when they conflict
- precision: exact/semantic beats syntax, syntax beats graph heuristic, graph
  heuristic beats raw text
- freshness: fresh graph or live backend beats stale graph
- cost: bounded symbol queries beat broad workspace search
- permission: read-only adapters are allowed where write adapters are hidden
- contract: verification capabilities require source-aware or command evidence;
  generic formatting checks do not satisfy source-code verification

The resolver can log all candidate adapters and why they lost. It should return
only useful diagnostics to the model, not an internal scoring dump.

### Boundary Placement

Following the repository's architecture rules, capability contracts should sit
at the application/workspace boundary, not inside the runner:

- `internal/capabilities/workspace` exposes capability tools and maps them to concrete
  workspace operations.
- `internal/repository/comprehension` remains the graph/index/view owner.
- `internal/runtime/agent` consumes the tool manifest, runtime brief, and normalized
  results; it enforces run contracts but does not own language routing policy.
- `cmd/harness` exposes CLI equivalents through `harness tool` and future
  `harness capability` commands.
- Existing language packages and LSP integrations stay replaceable adapters.

## Public Tool Shape

There are two viable public-surface paths. The rollout should start with the
first and graduate to the second only when audit data shows that a broader
facade improves behavior.

### Path A: Strengthen Existing Generic Tools

Make the existing generic tools canonical:

- `code_navigate` for navigation capabilities:
  `file_outline`, `find_symbol`, `workspace_symbol`, `read_symbol`,
  `definition`, `references`, `calls_from`, `calls_into`,
  `implementations`, and `related_files`.
- `source_sanity` for source-aware diagnostics.
- `context_pack`, `context_query`, `context_zoom`, `context_impact`, and
  `context_trace_pack` for repository memory and runtime evidence.
- `harness_audit` for harness behavior.

Language-prefixed tools remain callable, but prompt/runtime guidance treats
them as adapter implementation details.

### Path B: Add A General `capability_request`

After the result envelope and resolver telemetry stabilize, add a single
facade for most read-only capabilities:

```json
{
  "capability": "find_references",
  "target": {
    "path": "internal/capabilities/workspace/tool_schemas.go",
    "symbol": "BuildToolDefinitions",
    "handle": "symbol://internal/capabilities/workspace/tool_schemas.go#BuildToolDefinitions",
    "start_line": 1,
    "end_line": 80
  },
  "scope": {
    "path": "internal/capabilities/workspace",
    "language": "go"
  },
  "precision_min": "semantic",
  "fallback": "bounded",
  "freshness": "refresh_if_stale",
  "limit": 50,
  "named_gap": "which callers depend on the active tool schema shape",
  "why_not_answering": "the schema impact is not localized"
}
```

`capability_request` should be read-only at first. Mutating tools remain
separate because permissions and user approval are clearer when write actions
have explicit names.

### Capability Status

Add a `capability_status` tool or extend `context_status` with a
capability-focused section:

```json
{
  "capabilities": [
    {
      "id": "find_definition",
      "state": "available",
      "languages": {
        "go": { "precision": "exact", "adapters": ["go_definition", "gopls"] },
        "cpp": { "precision": "semantic", "adapters": ["cpp_definition", "clangd"] },
        "unknown": { "precision": "text", "adapters": ["ctags_symbols", "search_content"] }
      }
    }
  ],
  "freshness": {
    "context_graph": "fresh",
    "stale_paths": []
  },
  "permissions": {
    "write": "available",
    "protected_paths": []
  }
}
```

This should inform the runtime brief and audit. It should not force the model to
call a capability next.

## Schema And Tool-Result Changes

The active `ToolDefinition` has `Name`, `Description`, and `ParametersJSON`.
That is enough for Agent platformtyle function calling but not enough for
capability-level audit, MCP export, or structured result validation. Add
optional fields without breaking active consumers:

```go
type ToolDefinition struct {
    Name             string
    Description      string
    ParametersJSON   string
    OutputSchemaJSON string
    AnnotationsJSON  string
    CapabilityIDs    []string
    Visibility        string // public, adapter, hidden, deprecated
}
```

For MCP export, map these to MCP `inputSchema`, `outputSchema`, and
`annotations`. For the native local harness, use the same data to render compact
tool menus, validate structured results, and emit audit metadata.

Normalized capability results should include both human-readable text and
structured JSON. The MCP spec recommends structured content with an output
schema while retaining serialized text for compatibility:
https://modelcontextprotocol.io/specification/.

```json
{
  "capability": "find_references",
  "target": {
    "path": "internal/capabilities/workspace/tool_schemas.go",
    "symbol": "BuildToolDefinitions"
  },
  "resolved_target": {
    "handle": "symbol://internal/capabilities/workspace/tool_schemas.go#BuildToolDefinitions",
    "path": "internal/capabilities/workspace/tool_schemas.go",
    "range": { "start_line": 15, "end_line": 32 },
    "language": "go"
  },
  "route": {
    "primary_adapter": "go_references",
    "adapter_chain": ["go_references"],
    "fallback_used": false,
    "fallback_reason": "",
    "candidate_adapters": [
      { "name": "go_references", "state": "selected", "precision": "semantic" },
      { "name": "context_query", "state": "available", "precision": "syntax" },
      { "name": "search_content", "state": "fallback", "precision": "text" }
    ]
  },
  "quality": {
    "precision": "semantic",
    "confidence": "exact",
    "freshness": "live",
    "absence_confidence": "not_applicable",
    "truncated": false
  },
  "observations": [
    {
      "kind": "reference",
      "path": "internal/runtime/agent/runner.go",
      "range": { "start_line": 100, "end_line": 103 },
      "handle": "range://internal/runtime/agent/runner.go:100-103",
      "confidence": "exact"
    }
  ],
  "diagnostics": [],
  "artifacts": [],
  "gaps": [],
  "available_followups": ["inspect_symbol", "calls_into", "source_sanity"],
  "error": null
}
```

`available_followups` is a capability menu for the same target, not a
prescribed next step. Recovery packets may surface it only when the model has
not reached answer/write/verify readiness.

### Error Taxonomy

Use stable machine-readable error codes:

- `capability_unavailable`: no adapter supports the requested capability.
- `backend_missing`: a known adapter exists but required binary/index/LSP is
  absent.
- `target_ambiguous`: multiple symbols or files match and the request needs a
  narrower target.
- `target_not_found`: the target could not be found by the selected precision
  tier.
- `not_proven_absent`: fallback found nothing, but the backend cannot prove
  absence.
- `stale_context`: required freshness could not be satisfied.
- `permission_denied`: run contract forbids the requested capability or target.
- `fallback_exhausted`: bounded fallback ran and still could not answer.
- `truncated`: result limit prevented complete evidence.
- `adapter_error`: concrete adapter failed; include adapter name and stderr or
  diagnostic summary where safe.

These errors should be tool-execution errors, not protocol errors, whenever the
model can reasonably self-correct by narrowing scope, naming a symbol, lowering
precision, refreshing context, or declaring a blocker.

## Precision And Fallback Semantics

Precision tiers must be explicit and conservative.

| Tier | Meaning | Examples | Absence Claim Allowed |
| --- | --- | --- | --- |
| `exact` | Compiler, typechecker, LSP, test, runtime, or build-backed fact with precise location. | gopls references, clangd diagnostics, cargo check, test trace | Usually yes within declared scope |
| `semantic` | Language-aware index or parser result with symbol identity but not necessarily full project compilation. | native Go/C++/Python/TS/Rust/Java indexes, SCIP-like index | Sometimes, if scope is complete |
| `syntax` | AST/tree-sitter/ctags-style structure without full semantic resolution. | file outline, declarations, local calls | No, except for local syntax claims |
| `graph` | Cached comprehension graph relationship with freshness and confidence metadata. | `context_query`, `context_pack` edges | Only if graph coverage says complete |
| `heuristic` | Convention, filename, import text, or relationship inference. | related tests by name, path peers | No |
| `text` | Raw content search or bounded file read. | `search_content`, `read_file` | No |
| `unavailable` | No useful backend ran. | missing LSP, stale required graph | No |

Fallback policy is per capability:

- Orientation capabilities default to `bounded` fallback. If semantic routing
  misses, the resolver can try graph, syntax, or text fallback within the
  requested scope.
- Verification capabilities default to `source_aware_or_blocker`. A generic
  `git diff --check` style check can satisfy prose/config formatting but must
  not satisfy source-code sanity for code edits.
- Exact-anchor implementation contracts can set `fallback = none` for broad
  rediscovery while still allowing target-anchored reads for named edit-safety
  gaps.
- Absence claims require a backend that can prove completeness for the scope.
  Otherwise the result must say `not_proven_absent`.
- Stale graph data may be used for orientation with warnings. Packet generation
  and verification should default to `refresh_if_stale`, and hard contracts can
  require fresh data.

Fallback must never be silent. A result that used `search_content` after a
failed LSP request is still useful, but it is a different class of evidence.

## Prompt And Recovery Changes

Prompting should describe capability contracts, not language menus.

### Runtime Brief

The runtime brief should include a compact capability snapshot:

```text
Available capability routes:
- localize_change: context_pack/context_impact, graph fresh
- code navigation: definition/references/callers/callees via language adapters;
  fallback reports precision
- source_sanity: source-aware diagnostics for Go/C++/Python/TS/Rust/Java when
  backend is available; otherwise returns blocker for source edits
- verification: run project commands through shell when appropriate and allowed
- writes: edit_file/write_file/apply_patch within allowed scope
```

The brief should not enumerate every `go_*`, `cpp_*`, `python_*`, `ts_*`,
`rust_*`, and `java_*` tool unless the session is explicitly debugging tool
adapters.

### Tool Descriptions

Descriptions should use capability terms:

- Good: "Find references for a symbol; the harness routes to the best
  language-aware backend and reports fallback."
- Bad: "Use `go_references` for Go, `ts_references` for TypeScript,
  `rust_references` for Rust..."

Adapter tools can still have precise descriptions for direct CLI use and tests,
but the model's main menu should point to the generic route.

### Recovery Packets

Recovery should name the violated contract and safe capability choices:

- "The previous result was text fallback, so it cannot prove absence. Either
  answer with that uncertainty, request `find_references` with a narrower
  symbol/path, or name a blocker."
- "Source verification is required after editing `internal/capabilities/workspace/*.go`.
  Use `source_sanity` or a focused `go test` command, or report why source-aware
  verification is unavailable."
- "You have enough evidence to answer. Continue only if you can name one exact
  missing claim and one exact capability request that would resolve it."

Recovery should avoid hard-picking `go_diagnostics`, `cpp_diagnostics`, or other
language-prefixed tools except when the task is explicitly adapter debugging.

### Subagents And Capability Profiles

Subagents should receive capability profiles instead of raw full tool menus:

- `explorer`: `localize_change`, `query_context`, `inspect_file`,
  `inspect_symbol`, `find_*`, no writes.
- `reviewer`: read/navigation/audit plus `source_sanity`, no writes.
- `executor`: exact requested write tools, `source_sanity`, focused
  verification, narrow inspect capability for edit-safety gaps.

This follows the same shape as an LLM agent's subagent-specific tool access and
An LLM agent's per-agent permissions, while preserving Agent Harness's target-role and
verification contracts.

## Audit Metrics

Add capability-routing fields to conversation logs, telemetry, and
`harness_audit`.

### Per-Request Fields

- `capability`
- `target_path`, `target_symbol`, `target_handle`
- `requested_precision_min`
- `selected_adapter`
- `candidate_adapters`
- `precision_tier`
- `confidence_tier`
- `freshness_policy`
- `freshness_state`
- `fallback_used`
- `fallback_reason`
- `result_count`
- `truncated`
- `absence_confidence`
- `error_code`
- `permission_state`
- `duration_ms`
- `result_artifact`

### Behavior Metrics

- prompt-visible tool count and token cost
- language-prefixed calls as a share of all navigation calls
- generic capability calls as a share of navigation calls
- broad `read_file`/`search_content` calls after a successful capability result
- fallback rate by capability and language
- `capability_unavailable` and `backend_missing` rates by language
- `not_proven_absent` follow-up behavior: answer with uncertainty, narrow retry,
  or broad loop
- same-capability same-target repeat count
- zero-result retries without narrower target
- source edits ending without source-aware verification or concrete blocker
- `context_pack` top-k hit rate against changed paths
- reads/searches before first edit in implementation sessions
- stale graph usage and refresh success/failure rates

### Audit Findings

`harness_audit` should surface:

- capability menu drift: prompts mention language menus while generic routes are
  available
- adapter coverage gaps: a language has direct tools but no capability mapping
- silent fallback: result evidence downgraded without structured `fallback_used`
- false verification: source edits accepted with only prose/config checks
- fallback loops: repeated text searches after semantic/graph evidence was
  sufficient or had clearly failed
- unavailable ambiguity: empty result returned without `backend_missing`,
  `target_not_found`, or `not_proven_absent`

## Compatibility With Existing Language-Prefixed Tools

Compatibility is mandatory.

- Keep `go_*`, `cpp_*`, `python_*`, `ts_*`, `rust_*`, and `java_*` tools
  callable through `harness tool` and internal tests.
- Treat language-prefixed tools as adapter-level APIs. They remain useful for
  direct debugging, CLI workflows, and LLM agents that know exactly which
  backend they want.
- Add capability metadata to every language-prefixed tool so audits can detect
  unmapped adapters.
- Let the resolver record `selected_adapter` with the underlying tool name, so
  result precision remains debuggable.
- Do not remove language-prefixed tools from manifests until evals show the
  generic route is stable and external consumers have a migration path.
- If a direct language tool is hidden from the prompt-visible menu, keep it in
  an adapter manifest so the resolver can still call it.
- Existing tests for language tools should continue to assert adapter behavior.
  New tests should assert that generic capability routes reach the same adapter
  and normalize the same evidence.

The deprecation target is prompt visibility, not implementation existence.

## Rollout Plan

### Phase 0: Inventory And Contract Freeze

- Inventory all existing tools and map them to capability IDs.
- Add a static audit that fails on language tools with no capability metadata.
- Document which capabilities have generic routes:
  `code_navigate`, `source_sanity`, `context_*`, and `harness_audit`.
- Decide the initial precision labels and error taxonomy.

Acceptance:

- Every existing language-prefixed navigation/diagnostic tool has a capability
  mapping.
- `harness_audit` can report unmapped tools.
- No prompt behavior changes yet.

### Phase 1: Normalize Existing Generic Results

- Add a common result envelope to `code_navigate`, `source_sanity`,
  `context_query`, `context_zoom`, `context_pack`, `context_impact`, and
  `context_trace_pack`.
- Add `OutputSchemaJSON` where useful and validate structured output in tests.
- Emit per-request telemetry with capability, adapter, precision, fallback, and
  freshness fields.
- Preserve active human-readable summaries.

Acceptance:

- Existing callers still work.
- New structured fields identify primary adapter and fallback state.
- `source_sanity` reports source-aware evidence or a concrete blocker.

### Phase 2: Build The Resolver Registry

- Introduce a `CapabilityRegistry` and adapter registry in `internal/capabilities/workspace`.
- Route `code_navigate` through the registry instead of hand-coded language
  branching where practical.
- Add adapter candidates for language tools, graph tools, ctags, structural
  search, and bounded text fallback.
- Add tests for Go plus at least two non-Go languages with fixture repos.

Acceptance:

- Generic `definition`, `references`, `calls_from`, `calls_into`, and
  `file_outline` requests select the expected adapter for known file types.
- Missing backends return `backend_missing` or downgraded fallback with explicit
  precision.
- No broad fallback runs when `fallback = none`.

### Phase 3: Prompt And Recovery Migration

- Update base/profile/runtime guidance to say "ask for capabilities" and shrink
  language-specific menus.
- Update recovery packets to report violated capability contracts and available
  capability routes.
- Keep direct language tools hidden or deprioritized in normal coding profiles,
  but visible in tool-adapter debugging profiles.
- Add prompt-surface overlap checks to `harness_audit`.

Acceptance:

- Prompts no longer teach a language-specific strategy menu for normal coding
  work.
- Recovery text names capabilities and contracts, not one-off language tools.
- Behavior tests cover at least one post-readiness recovery and one post-write
  verification recovery.

### Phase 4: Capability Status And Optional Facade

- Add `capability_status` or extend `context_status` with capability coverage.
- If telemetry shows the existing generic routes are still too fragmented, add
  read-only `capability_request`.
- Export capability schemas through `harness tool list` and MCP-compatible
  metadata where available.
- Add docs for LLM agents using Agent Harness as a sidecar capability provider.

Acceptance:

- An LLM agent can inspect available capabilities, request localization,
  zoom to a symbol, run source sanity, and audit behavior without knowing the
  language-prefixed tools.
- Existing direct `harness tool go_*` calls still work.

### Phase 5: Eval And Audit Loop

- Compare direct language tools, generic capability routes, and generic routes
  plus graph/runtime evidence on orientation and implementation scenarios.
- Track reads/searches before first edit, fallback rates, verification quality,
  localization top-k hit rate, and behavior-audit findings.
- Feed repeated misses into ranker learning, adapter coverage, prompt
  migration, or resolver policy.

Acceptance:

- Generic routes reduce broad reads/searches in the benchmark without reducing
  successful verification.
- Audit can explain every fallback and every unavailable capability.
- No increase in unsafe writes, hidden verification gaps, or strategy-forcing
  recoveries.

## Risks And Mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Generic facade becomes ambiguous. | The model may provide weak targets or ask vague capabilities. | Require target fields per capability; return `target_ambiguous` with candidate handles. |
| Precision silently degrades. | The model overtrusts text fallback. | Mandatory `precision`, `fallback_used`, and `absence_confidence` fields. |
| Resolver chooses strategy. | Harness violates the north star. | Resolver only maps declared capability to adapter; recovery offers capability menus, not forced next steps. |
| Language adapters diverge. | Generic results become inconsistent by language. | Shared result envelope, per-language fixtures, adapter coverage audit. |
| Tool menu hides useful direct tools. | Expert users and LLM agents lose control. | Keep direct tools callable through `harness tool`; hide only from normal prompt-visible menus. |
| More schemas increase maintenance. | Tool definitions drift from implementations. | Generate capability audits from the registry and fail tests on unmapped adapters. |
| Fallback costs grow. | Local models waste time and context. | Bounded fallback, result limits, truncation markers, and telemetry on fallback duration. |
| Stale graph evidence looks authoritative. | Plans use old repository facts. | Freshness policies, stale markers, refresh-if-stale for packets/verification, and `not_proven_absent`. |
| Annotations are mistaken for security. | Unsafe tools may be auto-approved incorrectly. | Treat annotations as hints only; deterministic permissions remain authoritative. |

## Acceptance Criteria

This pillar is complete enough for default use when all of the following hold:

1. Normal coding prompts expose capability-oriented guidance and no longer
   require the model to choose between language-prefixed navigation tools.
2. `code_navigate` or `capability_request` can route definition, references,
   implementations, callers, callees, file outline, symbol lookup, read-symbol,
   and related-files requests for supported languages.
3. Every routed result reports selected adapter, precision tier, confidence,
   freshness, fallback state, truncation, and absence confidence.
4. Source edits cannot satisfy verification with weak generic checks when
   source-aware diagnostics or focused tests are required; missing backends
   return concrete blockers.
5. `harness_audit` reports capability coverage, unmapped adapters, fallback
   rates, language-prefixed call share, broad-search-after-capability patterns,
   and verification gaps.
6. Existing language-prefixed tools remain callable through `harness tool` and
   their active focused tests continue to pass.
7. Fixture tests prove that generic requests reach the same precise adapters as
   direct language tools for Go and at least two non-Go languages.
8. A localization/eval suite shows fewer broad reads/searches before first edit
   without reducing verified task completion.
9. LLM agents can inspect capability status and call the generic routes
   without starting an Agent Harness model session.
10. Recovery packets describe violated contracts and available capability
    choices without prescribing one exact next tool unless a hard invariant is
    being enforced.

## Open Questions

- Should `capability_request` define, or should Agent Harness keep a small set of
  capability-specific generic tools (`code_navigate`, `source_sanity`,
  `context_*`, `harness_audit`) and rely on better metadata?
- How much adapter detail should be visible in the prompt-visible tool menu
  versus only in structured results and audit output?
- Should `precision_min` be model-controlled, harness-derived from task phase,
  or both with harness-enforced floors for verification?
- Should capability status be a new tool or an extension of `context_status`?
- Which LLM-agent compatibility shape should be first: native
  `harness tool` JSON, MCP export, or both?

## North-Star Check

This design aligns with Agent Harness's decisioning policy:

- The model still decides whether to localize, inspect, edit, verify, or answer.
- The harness resolves a declared capability to the best safe adapter and
  reports what happened.
- Permissions, write scope, protected paths, verification, freshness
  requirements, and loop fuses remain deterministic host contracts.
- Tool results provide evidence, paths, symbols, counts, confidence, staleness,
  and uncertainty. They do not prescribe the next tool call.
- Fallback is bounded and explicit. Weak evidence stays useful but cannot pass
  as strong evidence.
- Language-specific precision is preserved behind a stable capability contract.
