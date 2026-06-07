# Context / Token Efficiency Design

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: design reviewed (seven-role bench, in-process), 2026-06-03

This document defines the optimization program for reducing avoidable
prompt/tool-token waste and repeated discovery in Idiom by routing all
model-facing context_pack projections through one shared, budget-enforced,
quality-aware compact renderer. It is a focused design under the
[Harness Optimization Program](design-harness-optimization-program.md), advancing
the Persistent Repository Memory, Local-Model Operations, and Eval/Audit/Learning
pillars.

## 1. Problem Statement

### 1.1 Root Cause

**Split model-facing context projection ownership.** Idiom currently has at least
four separate code paths that render PackResult for model consumption, each with
different fields, different compaction rules, and different handling of quality
and budget:

| Projection | Owner | Includes Quality? | Enforces BudgetTokens? |
|---|---|---|---|
| Direct `context_pack` tool result (model-facing) | `workspace/manager.go` `compactContextPackOutputForModel` | Yes | No |
| Plan-execute preload `## Context Pack` section | `planexecute/context_pack.go` `promptContextPackView` | **No** | No |
| Executor handoff packet | `planexecute/types.go` `ExecutorHandoffPacket` | **No** (only file/symbol/test counts) | No |
| Phase evidence packet | `planexecute/types.go` `PhaseEvidencePacket` | **No** | No |
| Sidecar/CLI `idiom tool context_pack` | Full JSON marshal | Included in raw JSON | No |
| Telemetry / audit | `harnessAuditContextCache` | Not rendered | N/A |
| Resume / recovery | Re-derives from `LoadView` | Varies | No |

The `compactContextPackView` in `manager.go` _does_ include quality and is
content-addressed, but the plan-execute preload struct (`promptContextPackView`)
— the highest-traffic model-facing surface — omits `Quality` entirely. The
`BudgetTokens` field exists on `PackResult`, `PackOptions`, `ViewQueryInputs`,
and as a cache-key component, but it is never used to cap rendered output:
`buildPackResult` uses only `ctx.limit` to bound candidate list length.

The result is that:
- A model receiving a plan-execute brief cannot see localization confidence or
  source-owner status, making it harder to judge when to trust the packet vs.
  when to do targeted follow-up discovery.
- No single budget cap governs how many tokens the packet consumes in the prompt.
- The four rendering paths cannot be compared for correctness without reading the
  source of each.

### 1.2 Reviewer Consensus

The design intent was reviewed by all seven reviewer roles (see Section 7). The
consensus is:

> Idiom should **not** shrink full `PackResult` artifacts. Keep full
> `context_pack` artifacts and `graph_views` complete. Add one canonical,
> budget-enforced, quality-aware model-facing compact packet projection over full
> `PackResult`, shared by direct tool results, plan-execute/lane preloads, phase
> evidence, review/repair, resume/recovery, sidecar, telemetry, and audit.
> Render `PackResult.Quality` in preloads and phase evidence. Enforce
> `budget_tokens` on the compact projection only. Preserve `pack_id` as typed
> evidence identity. Add cache-savings metrics. Wire verified-edit ranker
> learning only after deterministic post-write verification. Defer
> `context_query`/`context_zoom` caching, hierarchical summaries, and
> prompt-prefix stabilization until the shared projection contract is stable.

### 1.3 Risks Carried Forward From Review

1. **Do not weaken freshness, source-body, protected path, verification, or
   completion gates.** The compact projection is evidence, not authority.
2. **Do not claim backend prompt-cache savings** unless backend telemetry
   supports it. Hosted/native runs are separate from local prompt-cache flags.
3. **Compact packets are localization evidence, not source-body evidence or
   hidden plans.** The model must still verify exact anchors before writes.
4. **`context_query`/`context_zoom` caching is deferred** — separate key,
   freshness, and provenance contracts are needed.
5. **Ranker learning must reject unverified/no-op/protected/generated-path
   evidence** — this guard partially exists (the `unverified_evidence` rejection
   in `ranker_learning.go`), but must be strengthened to wire only
   post-write-verification confirmed edits.

## 2. Goals and Non-Goals

### 2.1 Goals

1. **One canonical shared compact projection** over full `PackResult`,
   accessible from all model-facing and audit/telemetry surfaces.
2. **Budget enforcement** on the compact projection: `BudgetTokens` becomes an
   active rendering cap (truncation with omitted-item counts), not just
   cache-key metadata.
3. **Quality rendered everywhere**: `PackResult.Quality` present in plan-execute
   preloads, executor handoff, phase evidence, and recovery/resume surfaces.
4. **`pack_id` propagated as typed evidence identity** across planner, executor,
   review, resume, recovery, telemetry, audit, and sidecar surfaces.
5. **Cache-savings metrics** in `harness_audit` that distinguish:
   - Model-facing rendered token savings (tokens not sent because of compact
     projection vs. full JSON)
   - Avoided rediscovery (cache hits × estimated recomputation cost in tokens /
     tool calls)
   - Backend prefix/prefill cache reuse (only when backend telemetry supports it)
6. **Verified-edit ranker learning** wired only after deterministic post-write
   verification confirms the edit was applied and passes tests, with explicit
   rejection of no-op, protected, generated-path, and unverified sources.
7. **Full artifacts retained** at `.idiom/comprehension/views/<pack_id>.json`
   and in `graph_views` SQLite rows, complete and freshness-validated.

### 2.2 Non-Goals

1. **Do not reduce full `PackResult` artifacts.** Compact projection is
   additive, not replacing.
2. **Do not remove fields from `graph_views`** rows.
3. **Do not weaken freshness, source-body, protected path, verification, or
   completion gates.**
4. **Do not add backend-specific prompt-cache flags** without backend telemetry
   support.
5. **Do not cache `context_query` or `context_zoom`** — deferred to separate
   design.
6. **Do not add hierarchical summaries or prompt-prefix stabilization** — deferred
   until the shared projection contract is stable.
7. **Do not change `PackResult` schema** beyond additive fields.
8. **Do not change ranker learning for non-verified sources.** Recovery
   reconciliation remains evidence-only (`RecordVerifiedRankerEvidence` with
   `source:"recovery_reconciliation"`); only post-write verified edits become
   weight-mutating.
9. **Do not make compact packets source-body evidence.** They remain localization
   evidence only; the model must still use `code_navigate`, `read_symbol`,
   `extract_body`, or bounded `read_file` for source-body obligations.
10. **Do not claim prompt-cache savings for hosted/remote backends** unless
    backend telemetry actually reports prefix hit/miss.

## 3. Current-State Evidence

### 3.1 Source Locations (Verified)

| Item | File | Lines | Finding |
|---|---|---|---|
| `PackResult.BudgetTokens` | `internal/repository/comprehension/pack.go` | 66 | Field exists, JSON-tagged, stored from opts |
| `PackResult.Quality` | `internal/repository/comprehension/pack.go` | 67 | Field exists, populated by `assessPackQuality` (line 399) |
| `buildPackResult` uses `ctx.limit` only | `internal/repository/comprehension/pack.go` | 396-427 | `BudgetTokens` stored but never used to truncate; `ctx.limit` bounds candidates (line 397) |
| `compactContextPackView` (direct tool result) | `internal/capabilities/workspace/manager.go` | 2232-2272 | Includes `Quality` field (line 2236); renders `packet_quality:` section |
| `compactContextPackOutputForModel` | `internal/capabilities/workspace/manager.go` | 2274-2300 | Triggered by `formatToolResultOutputForModel` (line 2222); deserializes full JSON → compact struct → text |
| `promptContextPackView` (plan-execute preload) | `internal/workflows/planexecute/context_pack.go` | 30-38 | **No Quality field**; renders `## Context Pack` without quality |
| `ExecutorHandoffPacket` | `internal/workflows/planexecute/types.go` | 86-110 | Only `CandidateFiles`, `CandidateSymbols`, `Tests` counts; **no Quality** |
| `PhaseEvidencePacket` | `internal/workflows/planexecute/types.go` | 249-261 | **No Quality field**; `Confidence` is `GraphResultConfidence` string, not `PackQuality` |
| `applyExecutorHandoffView` | `internal/workflows/planexecute/helpers.go` | 733-747 | Extracts `PackID`, `IndexID`, `Fresh`, counts from `PackResult`; **skips Quality** |
| `PhaseEvidencePacketFromView` | `internal/workflows/planexecute/helpers.go` | 3056-3084 | Builds from `LoadViewByID` result; **does not carry Quality** |
| `graph_views` schema | `internal/repository/comprehension/views.go` | 1113-1129 | Content-addressed by `ViewKey.ID()`; stores full `output_json`; scoped staleness via `referenced_paths_json` |
| `ViewQueryInputs.BudgetTokens` | `internal/repository/comprehension/views.go` | 28 | In cache key; used for dedup, not enforcement |
| `ViewStats` / cache counters | `internal/repository/comprehension/views.go` | 94-102, 874-904 | Atomic counters: hits, misses, writes, freshness hits, stale preventions; surfaced in `harness_audit` |
| `HarnessAuditContextCache` | `internal/runtime/agent/harness_audit.go` | 57-90 | Reports rows, fresh/stale, hit/miss, hit rate, stale ratio, bytes stored; **no token/cost savings metrics** |
| `RankerLearning.Verified` guard | `internal/repository/comprehension/ranker_learning.go` | 241 | Rejects `!verified` with `unverified_evidence`; but no source-verification linkage (any caller can set `Verified: true`) |
| `Recovery reconciliation` | `internal/repository/comprehension/ranker_learning.go` | 293-333 | `RecordVerifiedRankerEvidence` records without mutating weights (`action:"no_op"`) |
| `PlanArtifact.PackID` | `internal/capabilities/workspace/submit_plan.go` | 122 | PackID round-trips planner → executor via `submit_plan` |
| `classifyExecutorHandoffPacket` | `internal/workflows/planexecute/helpers.go` | 618-727 | Resolves effective PackID, calls `LoadViewByIDStatus`, classifies as inherited/stale/missing |
| `emitExecutorHandoffPacketEvents` | `internal/workflows/planexecute/helpers.go` | 753-770 | Emits `executor_inherits_pack` / `executor_pack_miss` / `executor_packet_stale` / `executor_packet_overridden` JSON events |

### 3.2 Audit Findings

- **No token/cost savings metrics** exist anywhere. `grep -r "token.*sav\|cost.*sav\|savings"` returns zero matches in `.go` files.
- **`harness_audit` context cache** reports `HitRate`, `StaleRatio`, `BytesStored`, but no estimated token impact.
- **`BudgetTokens`** is dead metadata: stored, cached on, never enforced.
- **Quality** is computed (via `assessPackQuality`) but only rendered in one of four model-facing surfaces.
- **Ranker learning** has an `unverified_evidence` guard but no post-write-verification linkage — the `Verified` boolean is caller-asserted.

## 4. Proposed Architecture

### 4.1 Owners and Data Flow

```
                          Pack() → graph_views row (full)
                          PackResult (complete)
                               │
                  ┌────────────┼────────────┐
                  │            │            │
             savePack()   CompactProjection  views/<pack_id>.json
             (full JSON)    (new shared)     (inspectable export)
                  │            │
                  │     ┌──────┼──────┬──────────┬──────────┐
                  │     │      │      │          │          │
                  │   Direct  Plan-  Executor  Phase     Sidecar/
                  │   tool    Exec   handoff   evidence   audit
                  │   result  preload          / resume
                  │     │      │      │          │          │
                  │     └──────┴──────┴──────────┴──────────┘
                  │            │
                  │     All share one CompactPackProjection type
                  │     and one budget-enforcing renderer
                  │
            Full artifact retained as source of truth.
            Compact projection is model-facing evidence,
            not source-body authority.
```

**Canonical owner:** A new shared `CompactPackProjection` type and
`RenderCompactPackProjection(projection CompactPackProjection, budgetTokens int) string`
function, owned by `internal/repository/comprehension/compact_projection.go`
(alongside `pack.go` and `views.go`).

**Consumers (all call the shared renderer):**
- `workspace/manager.go` — direct `context_pack` tool result formatting
- `planexecute/context_pack.go` — plan-execute preload `## Context Pack` section
- `planexecute/helpers.go` — executor handoff and phase evidence rendering
- Recovery / resume (via `LoadView` + shared renderer)
- Sidecar / `idiom tool` (model-facing output path; CLI JSON path retains full marshal)
- `harness_audit` — cache-savings metrics computation
- Telemetry — compact event fields

### 4.2 Typed Contracts

#### 4.2.1 `CompactPackProjection` (new type)

```go
// CompactPackProjection is the canonical model-facing projection of a
// PackResult. It is derived from the full PackResult by the shared
// compact-projection builder. All model-facing surfaces MUST use this
// type and the shared RenderCompactPackProjection renderer.
type CompactPackProjection struct {
    PackID           string                    `json:"pack_id"`
    Fresh            bool                      `json:"fresh"`
    Quality          CompactPackQuality        `json:"quality"`
    CandidateFiles   []CompactPackFile         `json:"candidate_files"`
    CandidateSymbols []CompactPackSymbol       `json:"candidate_symbols"`
    Tests            []CompactPackTest         `json:"tests"`
    UnresolvedGaps   []string                  `json:"unresolved_gaps"`
    // Budget tracking (set by builder, not from PackResult)
    BudgetTokens     int                       `json:"budget_tokens,omitempty"`
    TotalCandidates  int                       `json:"total_candidates"`
    OmittedFiles     int                       `json:"omitted_files"`
    OmittedSymbols   int                       `json:"omitted_symbols"`
    OmittedTests     int                       `json:"omitted_tests"`
}

type CompactPackQuality struct {
    LocalizationConfidence string   `json:"localization_confidence"`
    SourceOwnerFound       bool     `json:"source_owner_found"`
    EvidencePaths          []string `json:"evidence_paths,omitempty"`
    Reasons                []string `json:"reasons,omitempty"`
}
```

This is intentionally similar to the existing `compactContextPackView` +
`compactContextPackQuality` in `manager.go` (lines 2232-2272), but lifted to
the `comprehension` package as the single shared type. The existing
`compactContextPackView` becomes a thin caller of the shared builder.

#### 4.2.2 Builder: `NewCompactPackProjection`

```go
func NewCompactPackProjection(result PackResult) CompactPackProjection
```

Extracts all fields from a full `PackResult`. At this stage, no budget
enforcement occurs — the projection carries `TotalCandidates` and all lists
uncapped.

#### 4.2.3 Renderer: `RenderCompactPackProjection`

```go
func RenderCompactPackProjection(p CompactPackProjection, budgetTokens int) string
```

Enforces `budgetTokens` by progressive truncation:

1. Render the fixed header (pack_id, freshness, quality).
2. Measure token count.
3. Render candidate files, symbols, tests, gaps in priority order.
4. After each section, measure tokens; if over budget, stop rendering further
   sections and append an omitted-count summary line.
5. Within a section, if rendering all entries exceeds remaining budget, render
   the top N entries and append e.g. `(+M more files omitted, see full artifact)`.

Token counting uses a conservative estimator (character count / 3.5 for
English-heavy text, with a floor; or `tiktoken`-compatible cl100k_base
encoding if the Go port is acceptable as a dependency). The exact estimator
is an implementation detail; the contract is that the budget is enforced as a
hard cap on rendered tokens.

The renderer MUST NOT silently drop `pack_id` or `quality` — those are required
header fields.

#### 4.2.4 Contract invariants

| Invariant | Enforcement |
|---|---|
| Full `PackResult` always saved to `graph_views` and `views/<pack_id>.json` | Existing `SaveView` + `savePack` calls unchanged |
| `pack_id` present in every compact projection | `NewCompactPackProjection` copies `result.PackID` |
| `Quality` present in every compact projection | `NewCompactPackProjection` copies `result.Quality` |
| Budget enforced on rendered output | `RenderCompactPackProjection` applies budget before returning |
| Omitted-item counts present when truncation occurs | Omitted fields populated by renderer |
| Compact projection is localization evidence only | All surface documentation states this; no source-body gate accepts compact projection as satisfaction |

### 4.3 Consumer Migration

Each current consumer migrates to the shared type and renderer:

1. **`formatToolResultOutputForModel` / `compactContextPackOutputForModel`**
   (`workspace/manager.go:2222-2324`): Replace `compactContextPackView` struct
   and `writeCompactContextPackQuality` with calls to
   `NewCompactPackProjection` + `RenderCompactPackProjection`. The trigger
   logic in `formatToolResultOutputForModel` stays; only the rendering delegate
   changes.

2. **`promptContextPackView`** (`planexecute/context_pack.go:30-38`): Replace
   with `CompactPackProjection`. Update `RenderPromptContextPackSectionWithOptions`
   to call the shared renderer. **Add `Quality` rendering** to the `## Context Pack`
   section (currently absent).

3. **`ExecutorHandoffPacket`** (`planexecute/types.go:86-110`): Add
   `Quality CompactPackQuality` and `PackID` fields (PackID already present).
   Populate from the shared projection.

4. **`PhaseEvidencePacket`** (`planexecute/types.go:249-261`): Add
   `Quality CompactPackQuality`. Populate from the shared projection.

5. **Resume / recovery**: Use `LoadViewByID` + `NewCompactPackProjection` +
   `RenderCompactPackProjection` for model-facing context restoration.

6. **Sidecar / `idiom tool`**: The human-readable text output path uses the
   shared renderer. The `--json` / machine path retains full `PackResult`
   JSON.

7. **`harness_audit`**: Add cache-savings metrics (see 4.5).

### 4.4 `pack_id` Propagation Contract

`pack_id` is already present in:
- `PackResult.PackID` → `graph_views` row key
- `PlanArtifact.PackID` → round-trips through `submit_plan`
- `ExecutorHandoffPacket.PackID` → executor event emission
- `ToolRequest.PackID` (optional, for sidecar/CLI)

The contract addition:
- `CompactPackProjection.PackID` MUST equal `PackResult.PackID`.
- Every model-facing surface that renders a compact projection MUST include
  `pack_id` in the rendered output.
- Telemetry and audit events that reference a context packet MUST include
  `pack_id` as a top-level field.
- `pack_id` is the content-addressed cache key (`ViewKey.ID()`), making it a
  stable identity for cross-referencing between logs, artifacts, audit, and
  replay.

### 4.5 Cache-Savings Metrics

Add three metric categories to `harnessAuditContextCache`:

```go
type harnessAuditContextCache struct {
    // ... existing fields ...

    // Model-facing token savings
    EstimatedFullTokensSaved   int64 `json:"estimated_full_tokens_saved"`
    // Estimated tokens avoided by serving compact projection instead of
    // full PackResult JSON. Computed as: sum over all model-facing
    // render calls of (len(fullJSON)/3.5 - renderedTokens).

    // Avoided rediscovery
    EstimatedRediscoveryTokensAvoided int64 `json:"estimated_rediscovery_tokens_avoided"`
    // Estimated tokens avoided by cache hits. Computed as:
    // session_hits × avg_recompute_cost_tokens. avg_recompute_cost_tokens
    // is a running estimate from actual recomputation passes.

    // Backend prefix/prefill cache (conditional)
    BackendPrefixCacheHits   int64 `json:"backend_prefix_cache_hits,omitempty"`
    BackendPrefixCacheMisses int64 `json:"backend_prefix_cache_misses,omitempty"`
    // Only populated when the active backend exposes prefix-cache telemetry
    // (e.g., llama.cpp server /v1/metrics, vLLM prometheus metrics, or
    // MLX runtime stats). Absent field = "not available", not "zero hits".
}
```

Metric collection:
- `EstimatedFullTokensSaved`: increment atomically in `RenderCompactPackProjection`
  each time it renders; compare against the size of the equivalent full JSON
  output.
- `EstimatedRediscoveryTokensAvoided`: increment atomically on cache hit in
  `LoadView` / `LoadViewByID`; use the size of the cached `output_json` divided
  by the character-to-token ratio as the avoided-cost estimate.
- `BackendPrefixCache*`: gated on backend capability detection. The
  `backend_telemetry` collection path (separate from this design) provides the
  raw prefix-cache counters; the audit reads and surfaces them.

### 4.6 Ranker Learning: Post-Write Verification Gate

Current state: `ApplyVerifiedRankerLearning` (ranker_learning.go:198) accepts a
`Verified bool` parameter. The guard at line 241 rejects `!verified`, but any
caller can pass `Verified: true`. There is no linkage to actual post-write
verification evidence.

Required change:
1. Add a `VerifiedRankerLearningRequest.VerificationEvidence` field of type
   `*VerificationEvidence` containing at minimum:
   - `EditPaths []string` — paths that were edited
   - `VerificationCommand string` — the command run (e.g., `go test ./...`)
   - `VerificationPassed bool` — whether the command succeeded
   - `VerificationOutput string` — bounded sanitized output (no file bodies)
2. Before accepting `Verified: true`, `ApplyVerifiedRankerLearning` MUST check:
   - `VerificationEvidence != nil`
   - `len(VerificationEvidence.EditPaths) > 0`
   - `VerificationEvidence.VerificationPassed == true`
   - No edit path is in `.idiom/`, generated, or protected (path prefix check)
   - At least one edit path overlaps with a requested `Files` entry
3. If any check fails, reject with reason `verification_evidence_missing` or
   `edit_path_mismatch`.
4. Recovery reconciliation (`source:"recovery_reconciliation"`) continues to
   use `RecordVerifiedRankerEvidence` (evidence-only, no weight mutation).

### 4.7 Cross-Backend Considerations

- The compact projection renderer is backend-agnostic: it produces text,
  consumed identically by MLX/Qwen, llama.cpp, DeepSeek, hosted
  OpenAI-compatible, and sidecar agents.
- `BudgetTokens` enforcement uses a conservative token estimator that works
  without backend-specific tokenizers. The estimator is documented as
  approximate; the contract is "budget is a cap, not a precise byte-for-byte
  guarantee."
- Backend prefix-cache metrics are conditional: they only populate when the
  active backend exposes them. No backend-specific code in the shared
  projection path.
- The shared projection type is JSON-serializable and language-agnostic; no Go
  type assumptions leak into the rendered text.

## 5. Sequenced Implementation Plan

Each atom is independently verifiable and can be shipped as a focused PR.

### Atom 1: Shared Compact Projection Type and Builder

**Scope:** `internal/repository/comprehension/compact_projection.go` (new file)

- Define `CompactPackProjection`, `CompactPackQuality`, `CompactPackFile`,
  `CompactPackSymbol`, `CompactPackTest` types.
- Implement `NewCompactPackProjection(result PackResult) CompactPackProjection`.
- Implement `RenderCompactPackProjection(p CompactPackProjection, budgetTokens int) string`
  with budget enforcement.
- Add a conservative token estimator (`estimateTokens(text string) int`).
- Unit tests: round-trip from real `PackResult` fixtures, budget truncation with
  omitted counts, quality always present, pack_id always present.

**Verification:** `go test ./internal/repository/comprehension/...`

### Atom 2: Migrate Direct Tool Result Rendering

**Scope:** `internal/capabilities/workspace/manager.go`

- Replace `compactContextPackView` struct usage with `CompactPackProjection`.
- Replace `compactContextPackOutputForModel` body with delegate to
  `NewCompactPackProjection` + `RenderCompactPackProjection`.
- Remove `writeCompactContextPackQuality` (logic moves to shared renderer).
- Keep trigger logic in `formatToolResultOutputForModel` intact.

**Verification:** `go test ./internal/capabilities/workspace/...` and manual
`idiom tool context_pack --input '{"task":"test","mode":"plan"}'`

### Atom 3: Add Quality to Plan-Execute Preload

**Scope:** `internal/workflows/planexecute/context_pack.go`, `types.go`

- Replace `promptContextPackView` with `CompactPackProjection`.
- Update `RenderPromptContextPackSectionWithOptions` to call shared renderer.
- Quality now renders in `## Context Pack` section.
- Update `promptContextPackView` struct → remove; consumers use
  `comprehension.CompactPackProjection`.

**Verification:** `go test ./internal/workflows/planexecute/...` and manual
`idiom lane plan-execute` with a task that triggers context_pack

### Atom 4: Add Quality to Executor Handoff and Phase Evidence

**Scope:** `internal/workflows/planexecute/types.go`, `helpers.go`

- Add `Quality comprehensions.CompactPackQuality` to `ExecutorHandoffPacket`.
- Add `Quality comprehensions.CompactPackQuality` to `PhaseEvidencePacket`.
- Update `applyExecutorHandoffView` and `PhaseEvidencePacketFromView` to
  populate Quality from `LoadView` result via `NewCompactPackProjection`.
- Update `emitExecutorHandoffPacketEvents` to include quality fields.

**Verification:** `go test ./internal/workflows/planexecute/...` and inspect
`executor_inherits_pack` JSON events for quality fields

### Atom 5: Budget Enforcement Wiring

**Scope:** `internal/repository/comprehension/pack.go`, `compact_projection.go`

- In `Pack()`, after building `PackResult`, call
  `RenderCompactPackProjection` with the requested `BudgetTokens` from
  `PackOptions`.
- The existing `ctx.limit` still bounds candidate list length within
  `buildPackResult`; budget enforcement is an additional cap on rendered output.
- Model-facing surfaces already use the shared renderer (Atoms 2-4), so budget
  enforcement automatically applies everywhere.

**Verification:** `go test ./internal/repository/comprehension/...` with
budget-constrained PackOptions; verify output is under budget and includes
omitted counts

### Atom 6: Cache-Savings Metrics

**Scope:** `internal/repository/comprehension/views.go`, `compact_projection.go`,
`internal/runtime/agent/harness_audit.go`

- Add atomic counters: `estimatedFullTokensSaved`, `estimatedRediscoveryTokensAvoided`.
- Increment `estimatedFullTokensSaved` in `RenderCompactPackProjection`.
- Increment `estimatedRediscoveryTokensAvoided` on cache hit in `LoadView` /
  `LoadViewByID`.
- Export via `ViewStats`.
- Surface in `harnessAuditContextCache` as `EstimatedFullTokensSaved` and
  `EstimatedRediscoveryTokensAvoided`.
- Add `BackendPrefixCacheHits` / `BackendPrefixCacheMisses` fields (gated,
  populate only when backend telemetry available — can be wired in a follow-up
  atom if the backend telemetry surface doesn't yet exist).

**Verification:** `go test ./internal/repository/comprehension/...` and
`idiom tool harness_audit --input '{"purpose":"behavior"}'`

### Atom 7: Ranker Learning Post-Write Verification Gate

**Scope:** `internal/repository/comprehension/ranker_learning.go`

- Add `VerificationEvidence` struct.
- Add field to `VerifiedRankerLearningRequest`.
- Strengthen `ApplyVerifiedRankerLearning` to require:
  - Non-nil verification evidence
  - Non-empty edit paths
  - Verification passed
  - No protected/generated paths in edit set
  - Overlap between edit paths and requested files
- Add explicit rejection reasons: `verification_evidence_missing`,
  `no_edit_paths`, `verification_not_passed`, `protected_path_in_edits`,
  `edit_path_mismatch`.
- Update SWE eval learning trigger (`swe_suite.go:1220`) to provide
  verification evidence.
- Update product workflow learning trigger to provide verification evidence.
- Recovery reconciliation unchanged (still uses `RecordVerifiedRankerEvidence`
  with `action:"no_op"`).

**Verification:** `go test ./internal/repository/comprehension/...` and SWE eval
run that triggers ranker learning

### Atom 8: Docs, Audit, and Regression Tests

**Scope:** docs, tests

- Update `docs/codebase-comprehension-memory-design.md` to reference this design
  for compact projection and budget enforcement.
- Add regression tests:
  - `compact_projection_test.go`: budget enforcement with omitted counts
  - `compact_projection_test.go`: quality always present
  - `compact_projection_test.go`: pack_id always present
  - `ranker_learning_test.go`: rejects unverified, no-edit, protected-path,
    and verification-failed evidence
  - `harness_audit_test.go` or integration: cache-savings fields present
  - Plan-execute integration: quality renders in `## Context Pack`
- Update `harness_audit` context-cache findings to report savings metrics.

**Verification:** `go test ./...` and `make build`

## 6. Acceptance Criteria and Regression Tests

### 6.1 Acceptance Gates

| Gate | How verified |
|---|---|
| Compact projection under budget with omitted counts | Unit test: render with small budget, verify output ≤ budget tokens, omitted counts > 0 |
| Full artifact retained in graph_views and views/<pack_id>.json | Integration test: after Pack(), verify both graph_views row and flat file exist with complete data |
| Quality rendered in plan-execute preload | Integration test: capture `## Context Pack` section, assert `packet_quality` present with `localization_confidence` and `source_owner_found` |
| Quality rendered in executor handoff and phase evidence | Integration test: inspect `executor_inherits_pack` JSON event, assert `quality` field present |
| pack_id visible across planner/executor/review/resume/recovery/telemetry/audit/sidecar | Audit test: assert all seven surfaces emit or accept pack_id |
| Hasharness_audit reports token/tool savings plus correctness metrics | Audit test: run `harness_audit`, assert `estimated_full_tokens_saved` and `estimated_rediscovery_tokens_avoided` fields present with numeric values |
| Ranker learning rejects unverified/no-op/protected/generated-path evidence | Unit test: call `ApplyVerifiedRankerLearning` with each bad input, assert rejection and correct reason |
| BudgetTokens enforced as rendering cap | Unit test: Pack() with BudgetTokens=500, assert rendered output under 500 tokens |
| No regression in existing tests | `go test ./...` passes |
| No change to full PackResult artifact shape | Diff `PackResult` JSON before/after; only additive field changes allowed |

### 6.2 Regression Tests (Behavior-Focused)

1. **Budget truncation**: Pack with `BudgetTokens=100`, verify:
   - Output is ≤ ~100 tokens
   - `omitted_files` > 0 or `omitted_symbols` > 0
   - `pack_id` and `quality` still present in output
   - Full artifact at `views/<pack_id>.json` contains all candidates

2. **Quality in plan-execute preload**: Run `idiom lane plan-execute` with a
   known task, capture stderr "context_pack preloaded", verify the rendered
   `## Context Pack` includes `packet_quality:` section.

3. **Quality in executor handoff**: After `submit_plan`, inspect
   `executor_inherits_pack` JSON event for `quality.localization_confidence`
   and `quality.source_owner_found`.

4. **Ranker learning — rejects unverified**: Call
   `ApplyVerifiedRankerLearning` with `Verified: false`, assert rejection
   reason `unverified_evidence`.

5. **Ranker learning — rejects no edit paths**: Call with
   `VerificationEvidence.EditPaths = nil`, assert rejection.

6. **Ranker learning — rejects protected paths**: Call with edit path in
   `.idiom/`, assert rejection reason `protected_path_in_edits`.

7. **Ranker learning — accepts valid**: Call with real verification evidence
   (post-`go test` pass), assert weights mutated and learning log appended.

8. **Cache savings counters**: Run `Pack()` twice with same task, verify
   `viewCacheHits` > 0 and `estimatedRediscoveryTokensAvoided` > 0.

9. **No quality regression in direct tool result**: `idiom tool context_pack`
   output still includes `packet_quality:` section with correct values.

10. **BudgetTokens=0 means no budget enforcement**: When `BudgetTokens` is 0
    (default), output is not truncated (existing behavior preserved for
    unbounded calls).

## 7. Reviewer Bench (Seven-Role Review)

Per [debugging-reviewer-bench.md](debugging-reviewer-bench.md), a full bench is
warranted because this is a broad rearchitecture affecting shared harness
behavior across all lanes, prompts, tools, audit, and learning.

The following review was performed in-process by Reasonix (the local harness
running this session). Each role examined the design against the reviewer's
primary questions. Evidence was drawn from source files listed in Section 3,
existing design docs, and the consensus recorded in the task brief.

---

### 7.1 Incident Root-Cause Reviewer

**Verified facts:**
- Four distinct code paths render context_pack output for models.
- `promptContextPackView` (the highest-traffic path) omits `Quality`.
- `BudgetTokens` is stored on `PackResult` and used as a cache key but never
  enforces a rendering cap.
- `buildPackResult` uses only `ctx.limit` to bound candidates (pack.go:397).
- No token/cost savings metrics exist in the codebase.

**Inferences:**
- The split ownership is not intentional design; it is incremental accretion
  across `workspace`, `planexecute`, and audit code over ~6 months.
- Quality was added to `PackResult` and the direct tool result path, but the
  plan-execute preload was never updated because `promptContextPackView` was
  defined before `PackQuality` existed.

**Root cause:** `CompactPackProjection` (or equivalent) was never defined as a
shared type. Each consumer defined its own struct with the fields it needed at
the time.

**Contributing factors:**
- `PackResult.Quality` was added after `promptContextPackView` was already in
  use.
- `BudgetTokens` was designed as cache-key metadata, never promoted to
  enforcement.
- No integration test verifies that plan-execute preloads and direct tool
  results carry the same fields.

**Non-causes:**
- Not a freshness or staleness bug.
- Not a model behavior bug.
- Not a permissions or protected-path issue.

**Fix recommendation:** Shared projection type and renderer as proposed.

**Regression tests:** Budget enforcement, quality in plan-execute preload,
quality in executor handoff, pack_id in all surfaces.

**North-star risk:** Low. This is a harness contract improvement (unifying
projection) that does not touch model strategy.

**Confidence:** High. All facts verified from source.

---

### 7.2 Model Runtime / Config Reviewer

**Verified facts:**
- The compact projection renderer is text-based; no backend-specific
  tokenization required.
- `BudgetTokens` enforcement uses a conservative estimator (chars / 3.5).
- No backend configuration changes needed.

**Inferences:**
- The design is backend-agnostic by construction.
- The conservative token estimator may slightly over- or under-count for
  non-English or code-heavy text, but the contract says "budget is an
  approximate cap," not a precise guarantee.

**Root cause:** N/A (no runtime/config issue).

**Contributing factors:** N/A.

**Non-causes:** The token estimator choice does not affect model capability.

**Fix recommendation:** Document the estimator's approximation in the contract
so callers don't expect byte-exact budgets.

**Regression tests:** Budget enforcement tests with known text sizes.

**North-star risk:** Low. The design does not change model runtime or config.

**Confidence:** High.

---

### 7.3 Harness / Go Reviewer

**Verified facts:**
- `CompactPackProjection` is a new type in `internal/repository/comprehension/`,
  alongside existing `PackResult`, `ViewKey`, `ViewQueryInputs` types.
- The shared renderer is a pure function (no state, no concurrency).
- Atomic counters for savings metrics use existing `sync/atomic` pattern from
  `views.go:94-102`.
- Consumer migration replaces local structs with the shared type; no new
  goroutines or channels.

**Inferences:**
- No concurrency or lifecycle bugs introduced.
- The migration is mechanical: replace struct definition + call site.

**Root cause:** N/A.

**Contributing factors:** The existing `compactContextPackView` in `manager.go`
could have been the shared type from the start, but was scoped to the
workspace package for simplicity.

**Non-causes:** Not a state-machine or routing bug.

**Fix recommendation:**
- Keep the `CompactPackProjection` in `comprehension` (not `workspace`) so
  `planexecute` can import it without a circular dependency.
- Verify import graph before landing: `planexecute` → `comprehension` is
  already established (uses `LoadView`, `LoadViewByID`).

**Regression tests:** `go test ./...` and `go vet ./...`.

**North-star risk:** Low. The change is additive and typed.

**Confidence:** High.

---

### 7.4 Prompt / Protocol Reviewer

**Verified facts:**
- The `## Context Pack` section in plan-execute preloads currently lacks
  quality information.
- The design adds `packet_quality:` to all model-facing projections.
- The renderer includes an `evidence_note: context_pack is localization
  evidence, not source-body evidence` line (existing in `manager.go`; will
  be in the shared renderer).

**Inferences:**
- Adding quality to plan-execute preloads will help models decide when to trust
  the packet vs. when to do targeted follow-up reads.
- The risk of models treating localization evidence as source-body evidence is
  mitigated by the explicit evidence_note.

**Root cause:** Missing quality in plan-execute preload weakens the model's
ability to assess packet reliability before acting.

**Contributing factors:** The `promptContextPackView` struct was defined before
`PackQuality` existed.

**Non-causes:** Prompt text is not the issue; the issue is missing structured
fields in the preload.

**Fix recommendation:** Add quality to preloads as proposed. Consider adding
a short guidance line when `localization_confidence == "low"`:
"Low localization confidence — verify exact anchors with targeted reads
before submitting a plan."

**Regression tests:** Scripted TUI test that verifies the `## Context Pack`
section includes `packet_quality:` and the evidence_note.

**North-star risk:** Low. The change improves model-visible evidence without
prescribing strategy.

**Confidence:** High.

---

### 7.5 TDD / Acceptance Reviewer

**Verified facts:**
- Acceptance gates are defined in Section 6.1 with concrete verification
  methods.
- Regression tests are specified in Section 6.2 with 10 concrete scenarios.
- Each atom has a verification command.

**Inferences:**
- The acceptance criteria are testable and measurable.
- The "no regression in existing tests" gate depends on current test coverage
  of context_pack and plan-execute paths.

**Root cause:** N/A.

**Contributing factors:** The acceptance gates are conservative — they require
all seven surfaces to carry pack_id and quality, which is stronger than the
current state.

**Non-causes:** Not a TDD process gap.

**Fix recommendation:** Add an integration test that exercises the full
round-trip: `context_pack` → `submit_plan` with PackID → executor inherits
pack → phase evidence includes quality. This is the canonical smoke test for
the design.

**Regression tests:** The 10 scenarios in Section 6.2 are sufficient.

**North-star risk:** Low. Acceptance criteria align with contract-first design.

**Confidence:** High.

---

### 7.6 North-Star / Architecture Reviewer

**Verified facts:**
- The model still owns strategy: compact projection is evidence, not commands.
- The harness owns contracts: budget enforcement, freshness, pack_id identity.
- No permission, protected-path, verification, or completion gate is weakened.
- The design adds a typed contract (`CompactPackProjection`) where previously
  four ad-hoc structs existed.

**Inferences:**
- This design is strongly north-star aligned: it consolidates split ownership
  into one typed contract without prescribing model strategy.
- The "compact packets are localization evidence, not source-body evidence"
  invariant preserves the model's obligation to verify exact anchors.
- Budget enforcement is a harness contract (resource bound), not a strategy
  choice.

**Root cause:** Split projection ownership is a classic "missing typed
contract" failure mode, exactly the pattern the north star warns against.

**Contributing factors:** Incremental feature addition without a shared
projection owner.

**Non-causes:** Not a model-strategy-vs-harness-contract boundary issue.

**Fix recommendation:** The shared projection is the correct architectural
fix. Consider whether other split surfaces (tool manifests, recovery cards,
next-action state) have the same pattern and would benefit from similar
consolidation — but that is out of scope for this design.

**North-star risk:** Very low. This design is a textbook application of the
north-star rule: "one obligation gets one canonical owner."

**Confidence:** High.

---

### 7.7 Model-Agnostic / Cross-Backend Reviewer

**Verified facts:**
- The compact projection renderer produces plain text.
- No backend-specific tokenizer is required.
- No tool-call transport, parser extraction, or tool injection is changed.
- Backend prefix-cache metrics are gated on backend telemetry availability.

**Inferences:**
- MLX/Qwen, llama.cpp, DeepSeek, and hosted OpenAI-compatible backends will
  all see identical compact projection text.
- The conservative token estimator (chars/3.5) is backend-agnostic by design.
- DeepSeek's native tool-call transport is unaffected — the compact projection
  is prompt text, not tool-call metadata.

**Root cause:** N/A (no cross-backend issue).

**Contributing factors:** N/A.

**Non-causes:** N/A.

**Fix recommendation:** Document the token estimator's backend-agnostic
approximation. If future work requires per-backend tokenization, make it a
backend capability (like prefix-cache metrics) rather than assuming one
tokenizer.

**Regression tests:** Run the same context_pack call through MLX/Qwen and
llama.cpp paths; verify the compact projection is identical.

**North-star risk:** Low. The design is backend-agnostic by construction.

**Confidence:** High.

---

### 7.8 Review Summary

| Role | Verdict | Risk | Key finding |
|---|---|---|---|
| Incident Root-Cause | Root cause confirmed | Low | Split projection ownership is the core issue |
| Model Runtime / Config | No runtime concern | Low | Token estimator is approximate; document it |
| Harness / Go | No state-machine concern | Low | Verify import graph before landing |
| Prompt / Protocol | Missing quality weakens model decision | Low | Add quality + evidence_note to preloads |
| TDD / Acceptance | Acceptance gates are testable | Low | Add full round-trip integration test |
| North-Star / Architecture | Strongly north-star aligned | Very low | Textbook "one obligation, one owner" fix |
| Model-Agnostic / Cross-Backend | Backend-agnostic | Low | Identical text across all backends |

**Overall confidence:** High. No reviewer identified a blocking risk. All
recommended changes are incorporated into the design (token estimator
documentation, integration test, evidence_note preservation, import graph
verification).

## 8. Open Questions and Deferred Work

### 8.1 Deferred (Explicitly Out of Scope)

| Item | Reason | Target design |
|---|---|---|
| `context_query` / `context_zoom` caching | Separate key, freshness, and provenance contracts needed | Future design doc |
| Hierarchical summaries | Requires graph-node summary invalidation contracts first | `codebase-comprehension-memory-design.md` |
| Prompt-prefix stabilization | Wait until shared projection contract is stable and benchmarked | Future design doc |
| Backend prefix-cache wiring | Requires backend telemetry surface (llama.cpp /v1/metrics, vLLM metrics) | Future atom in this program |
| `executor smart-skip` (skip reads based on planner's ranked files) | Already noted as "intentional first-cut scope" in comprehension design; needs separate design | `codebase-comprehension-memory-design.md` §Known cache limitations |

### 8.2 Open Questions

1. **Token estimator choice:** Should the conservative `chars / 3.5` estimator
   be replaced with a `tiktoken`-compatible Go port (e.g., `github.com/pkoukk/tiktoken-go`)?
   - Pro: more accurate for English + code.
   - Con: adds a dependency, cl100k_base encoding may not match local model
     tokenizers.
   - **Resolution for now:** Use `chars / 3.5` as default; make the estimator
     function swappable via an internal interface so a tiktoken port can be
     plugged in later.

2. **Budget enforcement granularity:** Should budget enforcement happen at
   `Pack()` time or at render time?
   - Current design: budget enforced at render time (`RenderCompactPackProjection`).
   - Alternative: enforce at `Pack()` time by passing `BudgetTokens` to
     `buildPackResult` and capping candidates there.
   - **Recommendation:** Enforce at render time so the full artifact remains
     complete in `graph_views` for replay/audit, and budget is a presentation
     concern. This matches the invariant "full artifact retained, compact
     projection budgeted."

3. **Should `BudgetTokens=0` mean "no budget" or "use a default"?**
   - Current behavior: `BudgetTokens=0` means the field is omitted from JSON
     and no budget is enforced.
   - **Recommendation:** Keep this behavior. Explicit `BudgetTokens=0` = no cap.
     Explicit `BudgetTokens > 0` = enforced cap.

4. **Should `harness_audit` findings fire on low cache hit rates or high token
   waste?**
   - Current audit already fires `context_cache_low_hit_rate` and
     `context_cache_mostly_stale`.
   - **Recommendation:** Add `context_cache_high_token_waste` when
     `EstimatedRediscoveryTokensAvoided / (EstimatedRediscoveryTokensAvoided + actual tokens consumed)` is below a threshold. Defer threshold tuning to
     a follow-up atom after baseline data is collected.

5. **Are there existing `.idiom/comprehension/views/*.json` files that need
   migration?**
   - No. The `graph_views` SQLite rows are the source of truth; flat files
     are inspectable exports. No migration needed. New compact projection
     is derived from `PackResult` on read, not stored separately.

## References

- [Debugging Reviewer Bench](debugging-reviewer-bench.md)
- [Harness Optimization Program](design-harness-optimization-program.md)
- [Codebase Comprehension Memory Design](codebase-comprehension-memory-design.md)
- [Decisioning North Star](decisioning-north-star.md)
- [Persistent Repository Memory Pillar](design-persistent-repository-memory.md)
- [Eval/Audit/Learning Flywheel](design-eval-audit-learning-flywheel.md)
- [Idiom Maintenance Best Practices](idiom-maintenance-best-practices.md)
