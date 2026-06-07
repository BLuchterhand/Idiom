# Capability-First Model Interface

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: implemented and verified 2026-05-24

## Purpose

Reduce model friction by making the model-visible read, navigation, and
verification interface capability-shaped instead of language-tool-shaped.

The model should ask for the evidence it needs in task terms. The harness
should resolve that request to the best available adapter for the target
language, graph, runtime trace, shell command, or fallback path.

This document is a focused follow-up to
[design-capability-oriented-tooling.md](design-capability-oriented-tooling.md).

## Problem

Language-specific tools are valuable, but large model-visible menus create
avoidable friction:

- the model must choose between many language-prefixed tools for the same
  strategic need
- wrong-tool attempts produce avoidable retries
- broad `read_file` and `search_content` can look simpler than precise
  navigation
- zero-result searches do not always distinguish "proved absent" from "not
  found by this backend"
- audit sees tool names but not the model's intended capability

The current behavior audit still shows broad reads/searches, same-tool repeats,
named-gap read walks, zero-hit searches, truncated search content, and redirect
storms. A capability-first interface should reduce those patterns without
removing precise adapters.

## Design Decision

Strengthen the existing model-facing capability surface for read-only evidence
actions and add capability metadata for verification-oriented tools. Keep
mutating tools explicit.

The codebase already has a partial `read_only_capability` facade. The first
rollout should extend and expose that existing contract alongside generic tools
(`code_navigate`, `context_pack`, `context_query`, `context_zoom`,
`source_sanity`, `context_trace_pack`, and `harness_audit`) instead of adding a
second generic `capability_request` facade.

Language-prefixed tools remain available as compatibility adapters and sidecar
tools. Prompt/runtime guidance should teach capability vocabulary, not adapter
names, unless the session is debugging an adapter.

Capability metadata belongs in the workspace tool manifest/schema export and
runtime capability summary. Resolver behavior stays in `internal/capabilities/workspace`;
runtime profile/brief presentation stays in `internal/runtime/workerruntime`;
readiness, write, and verification gates stay outside the resolver.

## Initial Capability Set

Pilot capabilities:

- `localize_change`: rank files, symbols, tests, risks, and gaps
- `inspect_file`: file outline or bounded source range
- `inspect_symbol`: exact symbol body or definition-range read
- `find_definition`: resolve declaration or definition
- `find_references`: find uses of a symbol
- `calls_from`: outgoing call relationships
- `calls_into`: incoming call relationships
- `related_files`: tests, peers, generated files, configs
- `source_diagnostics`: source-aware diagnostics
- `verify_edit`: verification appropriate to changed files and task risk
- `audit_behavior`: harness behavior audit

Write capabilities are deliberately excluded from this facade:

- `edit_file`
- `write_file`
- `append_file`
- `apply_patch`
- `submit_plan`

Those carry side effects and should remain explicit.

## Capability Result Contract

Every routed read-only capability result should report:

- requested capability
- resolved target
- selected adapter
- adapter candidates considered
- precision tier
- fallback used or refused
- freshness state
- result counts
- absence confidence
- truncation state
- unresolved gaps
- available follow-up capabilities for the same target

Precision tiers:

- `exact`: compiler, typechecker, LSP, test, runtime, or build-backed fact
- `semantic`: language-aware symbol identity without full project compilation
- `syntax`: parser or ctags-style structure
- `graph`: cached comprehension graph with freshness/confidence metadata
- `heuristic`: convention or path/name relationship
- `text`: raw content search or bounded file read
- `unavailable`: no useful backend ran

Absence claims require a backend that can prove completeness for the requested
scope. Text fallback should usually return `not_proven_absent`.

## Runtime Brief Changes

The dynamic runtime brief should include a compact capability summary:

```text
Available evidence capabilities:
- localize_change: context graph packet with freshness and gaps
- inspect_symbol / find_references / calls_*: routed through language adapters
  when available; fallback reports precision
- source_diagnostics / verify_edit: source-aware checks or typed blocker when a code
  verification contract cannot be satisfied
- broad reads/searches: fallback only for exact strings, prose/config ranges,
  or named gaps not covered by structured capabilities
```

The brief should avoid enumerating every language-prefixed adapter. The result
will still report which adapter actually ran.

## Resolver Rules

The resolver should choose adapters with conservative, deterministic rules:

- exact handle beats inferred path
- path-derived language beats user-provided language override when they
  conflict
- live semantic backend beats stale graph
- fresh graph beats raw search for orientation
- bounded symbol/range probes beat full-file reads when a symbol is known
- verification capabilities require source-aware or command evidence
- fallback must be visible in the result
- broad fallback is unavailable after readiness unless the named gap justifies
  it

The resolver may return available follow-ups, but should not prescribe the next
strategy step.

## Rollout

The initial implementation slice covers Phase 1, Phase 2, and the read-only
navigation portion of Phase 3. It deliberately does not implement Phase 4 or add
the optional `capability_request` facade.

### Phase 1: Capability Telemetry Over Existing Tools

Annotate existing generic and language-prefixed read tools with capability ids,
precision tier, and fallback state in tool manifests and audit output.

Acceptance:

- audit can group tool use by capability
- unmapped read adapters are classified as routed, compatibility, deferred, or
  write-side-effect
- no model-facing behavior changes are required

Implementation notes:

- tool manifest and schema export entries carry `evidence_capabilities`
- tool manifest and schema export entries carry `capability_classification`
- tool-surface audit reports capability and classification counts

### Phase 2: Prompt And Runtime Brief Parity

Update runtime brief rendering to describe capability routes first and adapter
names second.

Acceptance:

- protocol projection and runtime brief agree on available capabilities
- adapter names are still visible where required by the actual tool API
- tests cover compact brief rendering

Implementation notes:

- runtime briefs expose `available_evidence_capabilities`
- read-only conversation and architecture profiles include
  `read_only_capability`
- verification-only profiles do not expose the read-only capability facade

### Phase 3: Capability Resolver Pilot

Broaden the existing read-only resolver for symbol inspection/navigation:
`inspect_symbol`, `find_definition`, and `find_references`.

Acceptance:

- result envelope reports selected adapter, precision, freshness, fallback, and
  gaps
- Go path uses Go adapter when available
- missing backend returns typed capability error, not broad silent fallback
- raw search fallback is bounded and visible

Implementation notes:

- `inspect-symbol`, `calls-from`, and `calls-into` are routed read-only
  capability IDs
- design vocabulary aliases such as `inspect_symbol` and `find_definition`
  normalize to stable registry IDs
- path-derived language wins over a conflicting user-provided language override

### Phase 4: Verification Capability

Route `source_diagnostics` and `verify_edit` through capability results that can
return a typed blocker when source-aware verification is unavailable.

Acceptance:

- code-edit verification cannot be satisfied by formatting-only checks
- verification result records commands or diagnostics
- completion gates can cite verification capability evidence

### Phase 5: Optional `capability_request`

Only after telemetry shows benefit, add a single read-only facade that accepts
capability id, target, precision minimum, fallback policy, freshness, limit, and
named gap.

Acceptance:

- facade is read-only
- mutating tools remain explicit
- sidecar compatibility with existing tools is preserved

## Metrics

Primary:

- broad `read_file` and `search_content` share after context readiness
- same-tool repeats
- zero-hit search count
- named-gap read walks
- wrong-tool rejection count
- capability fallback rate
- capability unavailable rate

Secondary:

- edit-target recall in `context_pack`
- first relevant file rank
- verification blocker accuracy
- prompt token cost of tool/capability descriptions

## Tests

Required tests:

- capability id appears in tool manifest/export
- Go symbol inspection routes to Go adapter when available
- fallback reports precision and absence confidence
- missing backend returns typed error
- broad raw search fallback is refused when contract forbids discovery
- verification capability distinguishes source-aware checks from formatting
  checks
- audit groups behavior findings by capability

## Non-Goals

- Do not remove language-prefixed tools in the first rollout.
- Do not expose a generic write facade.
- Do not hide precision loss behind a friendly result.
- Do not let capability results prescribe strategy.
- Do not treat heuristic related-file guesses as required edit paths.
