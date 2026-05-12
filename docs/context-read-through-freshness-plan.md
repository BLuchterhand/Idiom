# Context Read-Through Freshness Plan

## Purpose

Make idiom's context cache trustworthy by moving freshness from a caller
discipline into an idiom-owned read-path contract.

North star: the model owns strategy; the harness owns contracts. Freshness is a
contract. A cache-backed context tool must either return graph facts proven fresh
for the requested scope or return a freshness blocker. It must not quietly serve
stale derived facts and ask the model to notice.

## Current Verified State

- Source files are the source of truth.
- `.idiom/comprehension/graph.sqlite`, `graph.json`, `manifest.json`, and
  `graph_views` are derived data.
- `context_query`, `context_pack`, and `context_impact` already call a workspace
  auto-refresh helper before reading graph facts.
- Current auto-refresh works well for bounded `path` scopes and for small
  stale/missing sets through `context_status --refresh`.
- Unbounded stale graph reads can still return stale-marked graph output or
  skip graph backing, depending on the tool.
- Cache validity is split across several places: `Status`, workspace
  `contextAutoRefresh`, `Pack`, `LoadView`, `LoadViewByID`,
  `discovery_query`, and trace overlay freshness.

The gap is not "add another prompt rule." The gap is a single freshness
authority that every cache-backed read path must call.

## Required Guarantee

For static graph facts:

> Any cache-backed context read returns only facts derived from current source
> fingerprints for the relevant scope, or it returns a typed freshness blocker.

For runtime trace evidence:

> Idiom cannot make old command output fresh without rerunning the command.
> Stale trace evidence must be excluded from fresh graph claims by default or
> clearly labeled as stale evidence, never promoted into a fresh fact.

This gives callers a simple contract: `fresh_for_scope=true` means safe to rely
on as current derived context. `freshness_blocker` means run/allow a rebuild or
use live source/language tools.

## Design

### 1. Add A Central Freshness Gate

Create a shared internal API in `internal/comprehension`, not just
`internal/workspace`:

```go
type FreshnessPolicy string

const (
    FreshnessReportOnly FreshnessPolicy = "report_only"
    FreshnessReadThrough FreshnessPolicy = "read_through"
    FreshnessRequire FreshnessPolicy = "require_fresh"
    FreshnessAllowStale FreshnessPolicy = "allow_stale" // debug/inspection only
)

type FreshnessRequest struct {
    Root       string
    Operation  string // context_query, context_pack, context_zoom, load_view, etc.
    Policy     FreshnessPolicy
    Path       string
    Handle     string
    Query      string
    ReferencedPaths []string
    Language   string
    Mode       string
    MaxFiles   int
    MaxDuration time.Duration
}

type FreshnessDecision struct {
    Policy        FreshnessPolicy
    Operation     string
    FreshForScope bool
    GlobalFresh   bool
    Scope         string
    Reason        string
    Blocker       string
    Refresh       *RefreshReport
    CheckedPaths  []string
    StalePaths    []string
    MissingPaths  []string
}
```

`EnsureFresh(ctx, FreshnessRequest) (FreshnessDecision, error)` owns:

- status lookup
- scope inference
- stale/missing path detection
- collector-version and backend mismatch handling
- bounded incremental refresh
- full rebuild when the operation is unbounded and policy allows it
- fail-closed blocker creation when freshness cannot be proven

The workspace manager should call this API; it should not implement freshness
policy itself.

### 2. Define Freshness Policies By Operation

Default policies:

| Operation | Default policy | Behavior |
| --- | --- | --- |
| `context_status` | `report_only` | Report status. With `--refresh`, call `read_through`. |
| `context_query` | `read_through` | Refresh relevant scope before graph query; blocker if unbounded stale graph cannot be refreshed inside budget. |
| `context_zoom` | `read_through` | Infer scope from handle/path, refresh that scope, then zoom. |
| `context_pack` | `read_through` | Must return fresh packet evidence or blocker. |
| `context_impact` | `read_through` | Same as pack. |
| `context_trace_pack` | source/runtime | Runs live command; then marks trace facts fresh/stale against current files. Does not refresh static graph by itself unless it adds graph overlay. |
| `LoadView` / `LoadViewByID` | `require_fresh` | Cached view can be served only if referenced paths are current. Otherwise refresh/recompute or miss/block. |
| `discovery_query` graph layer | `read_through` | Use graph layer only when freshness succeeds; otherwise run live discovery layers and include the blocker as a gap. |

`allow_stale` should exist for debug commands and audits, not normal model-facing
context reads.

### 3. Infer The Smallest Safe Scope

`EnsureFresh` should avoid whole-repo rebuilds when it can prove a smaller read
scope:

- explicit `Path` wins.
- handle-derived path wins for `context_zoom` and cached view rehydration.
- `ReferencedPaths` wins for `LoadViewByID`.
- query/path terms can infer a bounded directory only when the query includes a
  path-like token that exists in the workspace.
- no bounded scope plus any stale/missing path means the operation is
  repo-wide.

For bounded scope:

1. Find stale/missing paths in scope.
2. Expand through the existing dependency refresh closure for imports/calls/tests.
3. Refresh the common bounded scope.
4. Re-run status.
5. Proceed only if the requested scope is fresh, even if unrelated repo paths
   remain stale.

For repo-wide reads:

1. If there is no index or collector versions mismatch, run an intentional root
   rebuild when within budget.
2. If the stale set is broad, either run root rebuild or return a blocker. Do
   not return stale graph facts under `read_through` or `require_fresh`.

This preserves token efficiency without weakening correctness.

### 4. Make View Cache Freshness Load-Bearing

Strengthen `graph_views` so cached view reads can validate and recover without
guessing:

- add or encode `task`, `mode`, `index_id`, `workspace_state_fingerprint`,
  `referenced_paths_json`, and `source_fingerprints_json` for each row.
- use `referenced_paths_json` instead of decoding `output_json` for invalidation
  where practical.
- `LoadView` should call `EnsureFresh` for the row's referenced paths before
  returning a hit.
- `LoadViewByID` should either:
  - return the cached row only when referenced paths are fresh, or
  - reconstruct the original request from stored metadata and recompute the
    view, or
  - return a typed `stale_cached_view` miss/blocker.

Do not rely only on `stale=0`. That flag is an optimization. The read path must
still prove the row's source fingerprints are current.

### 5. Separate Static Freshness From Runtime Trace Freshness

Runtime trace artifacts are evidence, not source truth. A static graph refresh
does not make an old failed command current.

Rules:

- `context_pack` should include fresh trace overlays by default.
- stale trace overlays should be excluded from fresh packet evidence unless the
  caller explicitly asks for stale/debug evidence.
- stale trace artifacts may still appear under `risks` or `unresolved_gaps`.
- `context_trace_pack` is the only operation that can make runtime evidence
  fresh, because it reruns the command.

### 6. Add Freshness Metadata To Every Context Result

Every context tool result should include a consistent `freshness` block:

```json
{
  "freshness": {
    "policy": "read_through",
    "fresh_for_scope": true,
    "global_fresh": false,
    "scope": "internal/comprehension",
    "checked_paths": ["internal/comprehension/graph.go"],
    "stale_paths": [],
    "refresh": { "...": "existing RefreshReport" }
  }
}
```

`fresh` can remain for compatibility, but `freshness.fresh_for_scope` becomes
the precise contract. A scoped result may be fresh for its requested scope while
the wider repo still has unrelated stale files.

### 7. Fail Closed

For normal context reads, failure to refresh should produce a typed blocker,
not stale output:

```json
{
  "error": "freshness_blocker",
  "freshness": {
    "policy": "read_through",
    "fresh_for_scope": false,
    "reason": "stale_unbounded_graph",
    "blocker": "context graph has 431 stale/missing files; run context_index --path . or provide a bounded path"
  }
}
```

This is aligned with idiom's north star: the harness enforces the contract but
does not decide the model's strategy. The model can choose to narrow the path,
ask for a rebuild, use live source tools, or report the blocker.

## Implementation Phases

### Phase 1: Freshness Authority

- Add `EnsureFresh` and the types above in `internal/comprehension`.
- Move the core logic from workspace `contextAutoRefresh` and
  `contextStatusAutoRefresh` into this shared API.
- Keep the existing workspace helper as a thin adapter during migration.
- Preserve existing caps as defaults, but make them request/config fields.

Tests:

- no-op fresh status returns no refresh.
- bounded stale path refreshes and returns `FreshForScope=true`.
- stale paths outside the bounded scope do not block a scoped query.
- unbounded stale graph returns blocker when full rebuild is over budget.
- missing index and collector mismatch run root rebuild when policy allows.

### Phase 2: Wire Read Paths

- `context_query`, `context_zoom`, `context_pack`, and `context_impact` call
  `EnsureFresh` and stop if `FreshForScope=false`.
- `discovery_query` uses graph backing only after `EnsureFresh` succeeds.
- `context_status --refresh` calls `EnsureFresh`; plain `context_status`
  remains report-only.

Tests:

- `context_query` without a path does not return stale graph facts after a file
  edit; it either rebuilds or reports `freshness_blocker`.
- `context_pack` after a scoped edit returns non-stale candidates.
- `context_zoom` refreshes the handle's file before returning related nodes.
- `discovery_query` records the freshness blocker and falls back to live layers.

### Phase 3: Harden `graph_views`

- Persist view metadata needed to validate and recompute cached rows.
- Add referenced-path and source-fingerprint metadata.
- Make `LoadView` and `LoadViewByID` freshness-aware.
- Treat `stale=0` as a fast path, not the final proof.

Tests:

- cached pack hit remains valid across no-op rebuild.
- editing a referenced path invalidates or refreshes before a cached hit.
- editing an unrelated path does not invalidate a scoped cached view.
- `LoadViewByID` refuses stale planner packets instead of rehydrating stale
  evidence.

### Phase 4: Runtime Trace Policy

- Exclude stale trace evidence from fresh packet evidence by default.
- Add an opt-in debug/include-stale flag for audits.
- Keep stale traces visible as risks/gaps when they explain why a packet is not
  fully current.

Tests:

- stale trace artifact does not mark a fresh static packet as fresh runtime
  evidence.
- rerunning `context_trace_pack` makes runtime evidence current again.

### Phase 5: Observability And Evals

- Add telemetry fields for freshness policy, attempted refreshes, blockers,
  stale prevented, cache hit after freshness check, cache miss due to stale row,
  and root rebuilds.
- Extend `harness_audit` with freshness health:
  - read-through attempts
  - blockers by reason
  - stale cached view preventions
  - refresh failures
  - root rebuild frequency
- Add eval fixtures comparing old stale-read behavior to read-through behavior.

## Acceptance Criteria

- No normal cache-backed context tool returns `fresh=true` when any returned
  static graph fact depends on stale or missing source files.
- A stale cached `graph_views` row cannot be returned through `LoadView` or
  `LoadViewByID`.
- Scoped stale edits are refreshed before `context_query`, `context_pack`,
  `context_zoom`, and `context_impact` return graph evidence.
- Unbounded stale graph reads either rebuild the root graph within budget or
  return a typed freshness blocker.
- Stale runtime trace artifacts are not promoted into fresh packet evidence.
- The model-facing output remains observational: evidence, freshness, gaps, and
  blockers. It does not prescribe a next tool call.

## Deliberate Non-Goals

- Do not add prompt reminders as the primary fix.
- Do not run a full repo rebuild before every query when a bounded scope can be
  proven fresh.
- Do not hide refresh failures by falling back to stale graph facts.
- Do not treat old runtime command output as current without rerunning it.
- Do not make the graph the source of truth; it remains a repairable derived
  index over source files.

## Reviewer Notes (Idiom-side review, 2026-05-11)

Reviewed against the patterns currently shipped on the refine pipeline
(commits `9c94fcf` → `ec5f8b0`) and the project's north-star principles
(harness owns contracts, model owns strategy, loud-failure preferred,
no-bandaid budgets, generic-first). The design is north-star aligned and
architecturally sound; concerns below are clarifications and follow-up
details, not architectural disagreements. Numbered for cross-reference.

### Clarifications worth folding in before implementation starts

**1. Budget-exceeded contract is under-specified.** §3 mentions `MaxFiles` /
`MaxDuration` but doesn't say what happens when exceeded. Suggest making it
explicit in the design:

- Under `read_through` or `require_fresh`: budget exceeded → typed
  `freshness_blocker` with reason `budget_exceeded`. Caller decides whether
  to retry with a larger budget, narrow the scope, or pivot to live tools.
- Under `allow_stale`: return stale facts annotated with
  `freshness.budget_exceeded=true` so audit consumers see the degraded path.
- Under `report_only`: budget is moot; the operation doesn't refresh.

Without this, callers can't predict whether to retry-with-bigger-budget or
pivot to live tools.

**2. The `report_only` row in §2's policy table conflicts with Phase 2's
statement.** Phase 2 says "`context_status --refresh` calls `EnsureFresh`;
plain `context_status` remains report-only." The table currently reads as if
`context_status` is ALWAYS `report_only`. Suggest clarifying the row:

| Operation | Default policy | Behavior |
| --- | --- | --- |
| `context_status` (no flag) | `report_only` | Report status only. |
| `context_status --refresh` | `read_through` | Refresh-on-demand within budget; emit blocker on failure. |

Same pattern: any operation that supports a `--refresh` (or similar) opt-in
should appear as two rows, not one. Saves the reader from reconciling the
table against the prose.

**3. Live-tool fallback contract.** When `EnsureFresh` returns a blocker, the
model is told to "use live source/language tools" — but those tools don't
have the same freshness contract because they read source directly. Worth a
one-paragraph statement near §7:

> **Live language tools (`go_read_symbol`, `cpp_read_symbol`, `python_read_symbol`,
> raw `read_file`, etc.) bypass the graph cache entirely and read from source
> on every call. They are inherently `fresh_for_scope=true` for the requested
> path. When `EnsureFresh` returns a blocker, the model can pivot to these
> tools with no further freshness concerns — the cost is that they don't
> compose into packets the way graph reads do.**

Without this, the model has no rule for "which fallback tool is safe to
trust" — it has to derive that the harness's promise differs across tool
classes, which is exactly the kind of strategy work the harness should
shoulder.

### Implementation discovery items (verify during Phase 1, don't block design)

**4. `LoadViewByID` recompute path is ambitious.** §4 says cached rows can
"reconstruct the original request from stored metadata and recompute the
view." This requires `task` / `mode` / `referenced_paths_json` / etc. to be
sufficient inputs to fully recompute. Worth verifying that existing
`graph_views` rows have enough columns — if not, recompute becomes a Phase
3.5 work item and Phase 3's actual ship is the simpler path: return a
`stale_cached_view` miss/blocker and let the caller request a fresh pack.

Suggest adding to §Phase 3 tests: "current `graph_views` rows can be
reconstructed into a valid `FreshnessRequest` for recompute, OR Phase 3 ships
miss-only behavior." Whichever lands first.

**5. Backward-compatibility migration scope.** §Phase 1 says "Keep the
existing workspace helper as a thin adapter during migration." Worth a
one-line non-goal entry naming WHO migrates:

> **Existing callers of `workspace.contextAutoRefresh` and
> `workspace.contextStatusAutoRefresh` must migrate to `EnsureFresh` calls
> with explicit policy. The thin-adapter shim exists for the migration
> window only and should be removed before Phase 5 telemetry lands — leaving
> two freshness paths in production permanently is the silent-failure
> footgun §6.1 Gap 3 of `docs/refine-end-to-end-loop.md` warned about (project
> tags that never fire because the harness emits canonical-only).**

Then list the current callers so the migration surface is visible:
`Status`, `Pack`, `LoadView`, `LoadViewByID`, `discovery_query`, plus any
trace-overlay-freshness callers per §1.

### Cross-cuts with shipped refine work

**6. Interaction with refine's per-iter `<iterDir>/lint.json`.** Refine now
persists derived data per iteration (`metrics.json` + `lint.json` per
`docs/refine-end-to-end-loop.md` §6.1 Gap 5 Colony shipped path). These are
also derived facts — if a future orchestrator reads them as cross-iteration
evidence, do they need a parallel freshness contract? Lean: **no, they're
scoped-snapshots** (immutable once written for a given iter), but worth a
one-line cross-reference so the reader knows the distinction:

> **Refine iter artifacts (`<iterDir>/lint.json`, `<iterDir>/metrics.json`,
> `<iterDir>/draft.md`) are scoped snapshots, not cache rows — they capture
> the state of one iteration and are not re-derived from source. Freshness
> is per-iter (always fresh-at-emission, never re-refreshed). The
> `EnsureFresh` contract here applies only to graph-derived facts that CAN
> be recomputed from current source.**

This keeps the boundary clean — `EnsureFresh` doesn't accidentally extend
into per-iter scratchpad territory.

**7. `allow_stale` audit footprint.** §2 says `allow_stale` exists "for debug
commands and audits, not normal model-facing context reads." Worth adding a
telemetry counter on `allow_stale` invocations (§Phase 5 already lists
freshness telemetry; add this one):

- `allow_stale_invocations` — incremented every time a tool dispatches under
  `allow_stale`. Should be zero in normal operation; a non-zero count in
  production telemetry signals a caller silently weakening the contract.

Cheap signal, prevents silent drift if a regression sneaks the policy onto a
production path.

### Test fixture / sequencing nits

**8. Eval fixture determinism (§Phase 5).** Testing freshness needs
controlled file mtimes / content fingerprints. Wall-clock manipulation is
flaky in CI. Worth a note in §Phase 5:

> **Eval fixtures must control source fingerprints deterministically. Use
> git-tree-based fixtures (commit the "old" state, commit the "new" state,
> reset between cases) OR in-memory filesystem control — not wall-clock
> mtime manipulation. The freshness-contract regressions are subtle enough
> that flaky fixtures will mask real bugs.**

**9. Phase 1 / Phase 2 boundary tweak.** Phase 1 ships `EnsureFresh` as an
adapter behind the existing `contextAutoRefresh`. The tests in §Phase 1
verify the API in isolation. Phase 2 wires read paths through it. Suggest
moving one test category from Phase 2 into Phase 1: a **contract test**
asserting that `EnsureFresh` returns `Refresh != nil` + `FreshForScope=true`
⟺ a corresponding source-fingerprint match. This is the canonical
invariant; catching drift between the central authority and its consumers
early is cheaper than catching it after each consumer migrates.

### Bottom line

Ship-ready as a design. Items 1-3 fold cleanly inline (~10 minutes of edit
time); 4-5 are Phase 1 implementation discovery; 6-7 are Phase 5 follow-ons;
8-9 are test-shape nits. None blocks starting Phase 1.

The core insight — "freshness as a contract, not caller discipline" — is the
same ratchet refine got with `idiom brief lint --json` + per-iter
`lint.json`: convert a class of "model has to remember to check" bugs into a
class of "harness either guarantees or names the blocker" contracts.
Architecturally consistent with the project's direction; a strong addition
to the harness-side surface.

