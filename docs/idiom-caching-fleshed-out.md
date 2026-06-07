# Idiom Caching: Fleshed-Out Concept

> Synthesized from the short analysis, verified against the codebase at
> `internal/repository/comprehension/`, `internal/workflows/planexecute/`,
> `internal/capabilities/workspace/`, `internal/runtime/backends/`, and
> `docs/idiom-strategic-thesis.md`.

---

## 1. What We Have (Verified)

### 1.1 Backend prompt/prefix cache — EXISTS, WORKS

`internal/runtime/backends/launcher.go` (lines 48–92) emits `--prompt-cache-size`,
`--prompt-cache-bytes`, `--prefix-cache-size`, `--prefix-cache-max-bytes`
flags to local launchers. Hosted models get separate cache semantics. This
reduces inference recompute latency. It does **not** reduce prompt-bloat token
costs for the model side of the wire; that is a prompt-structure problem.

### 1.2 Repository/context cache (`graph_views`) — EXISTS, STRONG SUBSTRATE

The `graph_views` SQLite table in `.idiom/comprehension/graph.sqlite` is the
Idiom-native cache for `context_pack` results.

**Key properties (verified in code):**

| Property | Location | Status |
|---|---|---|
| Content-addressed cache key | `pack_cache.go:25` `viewKeyFromOptions()` — hashes IndexID + Task + Mode + all Query inputs (BudgetTokens, RequiredPaths, fingerprints, etc.) | ✅ |
| Deterministic 16-char hex `pack_id` | `views.go:56` `ViewKey.ID()` — SHA256 of canonical fields | ✅ |
| Freshness validation on load | `views.go:159` `LoadView()` — validates referenced paths' fingerprints against current index | ✅ |
| `LoadViewByIDStatus` for rehydration | `views.go:225` — returns fresh/stale/missing for a `pack_id` without reconstructing the key | ✅ |
| Stale-row prevention | `views.go:193-196` — stale rows are treated as cache misses and counted as `stalePreventions` | ✅ |
| Workspace-state fingerprint | `views.go:1218` `ComprehensionStateFingerprint()` — composite of ranker weights + runtime traces | ✅ |
| Component fingerprints | `views.go:1232` — `RankerWeightsFingerprint` + `RuntimeTracesFingerprint` kept separate for attribution | ✅ |
| Cache telemetry for audit | `context_telemetry.go:365` `addContextCacheTelemetry()` — extracts cache state/source/reason/invalidation from pack output | ✅ |
| Audit cache panel | `harness_audit.go:3540` — reports stale ratio, contract gaps, unresolved gaps, low hit rate | ✅ |
| `pack_id` round-trip planner→executor | `helpers.go:734` `applyExecutorHandoffView()` — executor rehydrates from planner's `PackID`, emits `executor_inherits_pack` or `executor_pack_miss` | ✅ |
| Contract attachment | `result_metadata.go:106` `AttachPackResultContract()` — schema version, confidence, freshness, cache state, unresolved gaps | ✅ |
| JSON artifact mirror | `pack_cache.go:51` `savePack()` — writes `.idiom/comprehension/views/<pack_id>.json` for inspection | ✅ |

---

## 2. Verified Gaps (The Opportunity)

### 2.1 `Quality` is assessed but never rendered to the model

**Evidence:**
- `pack_quality.go:16` `assessPackQuality()` computes `SourceOwnerFound`, `LocalizationConfidence` (high/medium/low), and `EvidencePaths`.
- `pack.go:67` `PackResult.Quality` carries the field.
- **But** `context_pack.go:30` `promptContextPackView` struct **omits** the `Quality` field entirely.
- `context_pack.go:226-229` `writePromptContextFiles/Symbols/Tests/Gaps` render files, symbols, tests, and gaps — never quality.

**Impact:** The model receives a candidate list but no signal about whether the ranker
thinks it found a source owner. A `source_owner_found=false` packet looks identical
to a high-confidence packet in the prompt. This likely contributes to the broad
`read_file`/`search_content` churn observed.

### 2.2 `BudgetTokens` is carried but not enforced in rendering

**Evidence:**
- `pack_cache.go:34` `BudgetTokens` is part of the content-addressed cache key.
- `pack.go:413` `BudgetTokens` is stored on `PackResult`.
- **But** `buildPackResult` (line 396) uses `ctx.limit` (not `BudgetTokens`) to
  bound candidate lists. `BudgetTokens` is never used to truncate the output JSON
  or the rendered prompt section.

**Impact:** `BudgetTokens` can signal intent but doesn't actually constrain the
output size. The token budget between the client and the cache is not honored.

### 2.3 `context_query` and `context_zoom` are NOT cached in `graph_views`

**Evidence:**
- `context_tools.go:124` `contextQuery()` calls `comprehension.Query()` — no
  `graph_views` read or write.
- `context_tools.go:177` `contextZoom()` calls `comprehension.Zoom()` — same.
- Compare to `context_tools.go:213` `contextPack()` which calls `comprehension.Pack()`
  → reads from and writes to `graph_views`.

**Impact:** Repeated graph-shaped questions (e.g. "what calls X") re-execute
the query against the graph each time, even though the graph is durable. The
model re-requests and re-processes the same relationship data.

### 2.4 Ranker learning triggers are defined but "not yet wired"

**Evidence:**
`harness_audit.go:3173` `rankerLearningKnownTriggers` — three of five triggers
are explicitly marked "not yet wired":

| Trigger | Component | Status |
|---|---|---|
| `eval` | `internal/evidence/eval/swe_suite.go` | Wired |
| `cli` | `internal/surfaces/cli/ranker/ranker.go` | Wired |
| `verified_edit` | `internal/runtime/agent/runner.go` | **not yet wired** |
| `harness_feedback` | `internal/capabilities/workspace/harness_feedback.go` | **not yet wired** |
| `audit_watch` | `internal/auditcmd/audit.go` | **not yet wired** |
| `recovery_reconciliation` | `internal/recovercmd/packet.go` | Wired (evidence-only) |

### 2.5 Prompt prefix structure not optimized for backend caches

**Evidence:**
- `launcher.go` emits cache flags to the backend, but the prompt assembly in
  the runner/rails determines whether the prefix is stable enough for the KV
  cache to be reusable across sessions.
- The agent's system prompt, tool schemas, and protocol text are the stable
  prefix; volatile task context should always be appended after.

---

## 3. Fleshed-Out Implementation Plan

### Phase A: Quick Wins (1–3 days each)

#### A1. Render `Quality` into the context_pack prompt section

**What:** Add `quality` to `promptContextPackView` and render it in
`BuildPromptContextPackSection`.

**How:**

1. Add to `promptContextPackView` struct (in `context_pack.go`):
   ```go
   Quality *promptContextPackQuality `json:"quality,omitempty"`
   ```
   with:
   ```go
   type promptContextPackQuality struct {
       LocalizationConfidence string `json:"localization_confidence"`
       SourceOwnerFound       bool   `json:"source_owner_found"`
   }
   ```

2. In `RenderPromptContextPackSectionWithOptions`, after the Freshness line,
   emit:
   ```
   - Quality: localization=<high|medium|low> source_owner_found=<bool>
   ```

3. When `SourceOwnerFound == false`, add a nudge line:
   ```
   - ⚠ No source owner identified — use [context_zoom] or bounded [search_files] before writing
   ```

**State impact:** Zero new infrastructure. The `PackResult.Quality` is already
computed; it's just not deserialized into the view or rendered.

**Expected impact:** Model stops treating low-confidence packets as definitive
and avoids broad-search thrash when the ranker itself flagged weak localization.

---

#### A2. Enforce `BudgetTokens` in `buildPackResult`

**What:** Use `BudgetTokens` (when non-zero) to cap the size of candidate lists
and evidence in the pack output, not just `Limit`.

**How:**

1. In `buildPackResult` (`pack.go:396`), compute a token-aware cap:
   ```go
   effectiveLimit := ctx.limit
   if ctx.opts.BudgetTokens > 0 {
       effectiveLimit = min(effectiveLimit, budgetTokensLimit(ctx.opts.BudgetTokens))
   }
   ```

2. Apply `effectiveLimit` to `candidateFiles`, `candidateSymbols`, and `tests`.

3. Add `truncated_by_budget` to `PackQuality.Reasons` when truncation occurs.

**State impact:** Changes `buildPackResult` only. The cache key already includes
`BudgetTokens`, so different budgets produce different cache entries.

**Note:** This is inherently approximate — we don't know exact token counts
without running the tokenizer. A byte count ÷ 3 heuristic is reasonable.

---

#### A3. Add a "cache savings" audit panel

**What:** Extend `harness_audit` to report estimated savings from cache reuse.

**How:**

1. In `context_telemetry.go`, track `read_file`, `search_content`, `search_files`,
   `code_navigate` calls that were **avoided** because `context_pack` surfaced
   the relevant paths directly. This is a counterfactual, so the simplest signal:
   - Track sessions where `context_pack` was called first and the model's next
     tool was a narrow read (e.g. `read_symbol`, `extract_body`, bounded `read_file`)
     instead of a broad search.
   - Count these as "localized hits."

2. Add to the audit panel (`harness_audit.go:3540` area):
   - `broad_search_avoided_estimate` — sessions where first post-pack tool was narrow
   - `tokens_to_first_edit` — median prompt tokens before first `edit_file`/`submit_plan`
   - `context_pack_hit_rate` (already exists: `cache_hits`/`cache_misses`)

3. Expose these through `harness_audit` output in the findings list.

**State impact:** New counters in telemetry, new audit findings. Does not
change agent behavior.

---

### Phase B: Structural Wins (3–7 days each)

#### B1. Make `context_pack` compact-by-default for model-facing tool output

**What:** The full JSON artifact (candidates + relationships + evidence + contract)
is ~KB-scale. The model doesn't need all of it in the tool result — it needs the
`pack_id`, top files/symbols/tests, freshness, quality, and gaps. Keep the full
artifact behind `context_zoom` or a dedicated view.

**How:**

1. In `context_tools.go:204` `contextPack()`, add a `compact` mode (default `true`
   for model-facing calls, `false` for CLI `idiom tool context_pack`).

2. In compact mode, the `ToolResult.Output` includes only:
   - `pack_id`, `index_id`, `fresh`/`freshness`
   - `quality` (localization_confidence, source_owner_found)
   - Top 8 `candidate_files` (path, role, confidence, score only)
   - Top 8 `candidate_symbols` (id, path, qualified_name, kind)
   - Top 4 `tests`
   - `unresolved_gaps`
   - A hint: "Full artifact at `.idiom/comprehension/views/<pack_id>.json`; use
     `context_zoom` to drill into candidates."

3. Full JSON artifact is still written to `graph_views` and the JSON file.

**State impact:** Changes `context_pack` tool output shape for model-facing
calls. The plan-execute preload already builds its own rendered section
(`BuildPromptContextPackSection`), which is separately addressed by A1.

**Note:** The plan-execute prompt preload is already compact (it renders a
custom markdown section, not the raw JSON). This change is for when the model
invokes `context_pack` directly as a tool during a session.

---

#### B2. Cache `context_query` and `context_zoom` in `graph_views`

**What:** Extend the `graph_views` cache layer to cover `Query` and `Zoom`
results, not just `Pack`.

**How:**

1. Define `ViewKey` variants for query and zoom operations. A query key hashes:
   `IndexID + Query + Path + Symbol + Scope + Resolution + Limit + IncludeEvidence`.
   A zoom key hashes: `IndexID + Handle + Resolution + Limit + IncludeSource`.

2. Add `loadCachedQuery`/`saveQuery` and `loadCachedZoom`/`saveZoom` functions
   parallel to `LoadView`/`SaveView`.

3. In `contextQuery()` and `contextZoom()` (`context_tools.go`), check cache
   before executing the graph operation, and persist results on miss.

4. Use the same freshness validation path as `LoadView`: validate that
   referenced paths haven't changed fingerprints.

**State impact:** New rows in `graph_views` with different resolution values
(`"query"` / `"zoom"`). Reuses existing schema, freshness, and invalidation
infrastructure.

**Risk:** Query and Zoom outputs can be large (edge lists). Apply a
`BudgetTokens`-style cap and note truncation in the cache metadata.

---

#### B3. Wire `verified_edit` ranker learning

**What:** When the executor successfully applies a verified edit to a file,
boost that file's score in the ranker so it surfaces higher in future
`context_pack` calls.

**How:**

1. In `internal/runtime/agent/runner.go` (where verified edits are confirmed),
   after a verified edit succeeds:
   - Call `comprehension.RecordLearningEvent(root, LearningEvent{Source: "verified_edit", Path: changedFile, MultiplierDelta: +0.15, Reason: "verified_edit_succeeded"})`.
   - Persist to `.idiom/ranker/learning_log.jsonl` and update `weights.json`.

2. Similarly, when a `context_pack` session results in zero hits (no files
   from the packet were edited) or wrong-scope edits:
   - Penalize the surfaced files by a small negative delta.

3. Ensure the ranker's learned weights are included in `ComprehensionStateFingerprint`
   (they already are — `rankerWeightsStateFingerprint` in `views.go:1259`).

**State impact:** Changes `runner.go` to emit learning events. The trigger is
already defined in `harness_audit.go:3184` but marked "not yet wired." The
`.idiom/ranker/` persistence path already exists and is used by the `eval` and
`cli` triggers.

---

#### B4. Stabilize prompt prefixes for backend KV caches

**What:** Reorder the prompt assembly so static system/tool/protocol text always
appears before volatile task/context text.

**How:**

1. Audit `internal/runtime/agent/` prompt assembly — identify the order in which
   system prompt, tool manifests, lane instructions, and task brief are
   concatenated.

2. Enforce invariant: static text (system prompt, tool schemas, protocol rules)
   → lane-specific instructions → task brief → dynamic context (context_pack
   output, runtime evidence).

3. Document this ordering contract so future prompt changes don't accidentally
   interleave volatile text early.

**State impact:** Changes prompt assembly order in the runner. No cache-key
changes. Benefits both local `--prompt-cache` launchers and hosted backends with
automatic prefix caching (Anthropic, OpenAI prompt caching).

---

### Phase C: Bigger Wins (1–2 weeks each)

#### C1. Add hierarchical summaries with provenance

**What:** Generate deterministic or provenance-linked summaries at repo →
package → file → symbol levels, cached independently, so the model can get
high-level orientation without a giant prompt.

**How:**

1. Build a summary generator that runs during `context_index` and produces
   markdown summary nodes stored in the graph.
2. Summaries are content-addressed (SHA256 of the source they summarize) so
   they auto-invalidate on change.
3. `context_pack` can include a `summary_level` option: `"repo"`, `"package"`,
   `"file"`, `"symbol"`.
4. Summaries are cached in `graph_views` with their own resolution type.

**State impact:** New graph node types for summaries. Extends `context_index`
and `context_pack`. Lines up with `docs/idiom-strategic-thesis.md` line 368:
"make `context_pack` and graph views demonstrably better than raw search."

---

#### C2. Make cache freshness visible at the point of use

**What:** When the model reads a file surfaced by a stale `context_pack` packet,
surface the staleness in the tool result so the model knows to verify.

**How:**

1. In `code_navigate`, `read_file`, `read_symbol`, and `extract_body`, check
   whether the requested path is referenced by a stale `graph_views` row.

2. If so, append a freshness note to the tool result:
   ```
   ⚠ This file was surfaced by a stale context packet (pack: <id>, stale since <reason>).
   Consider running [context_status refresh=true] or re-running [context_pack].
   ```

3. This requires the tool surface to have access to the `graph_views` cache
   state, which can be passed via the workspace manager or a lightweight
   sidecar lookup.

**State impact:** Cross-cutting change — every read tool needs cache awareness.
Could start with `code_navigate` and `read_file` as the highest-volume tools.

---

## 4. Priority / Sequencing

| # | Item | Effort | Impact | Dependency |
|---|---|---|---|---|
| A1 | Render Quality in context_pack prompt | Low | High | None |
| A2 | Enforce BudgetTokens in pack output | Low | Medium | None |
| A3 | Cache-savings audit panel | Low | Medium | None |
| B1 | Compact context_pack tool output | Medium | High | A1 (for fallback hint) |
| B2 | Cache context_query/context_zoom | Medium | High | None |
| B3 | Wire verified_edit ranker learning | Medium | High | None |
| B4 | Stabilize prompt prefix order | Low | Medium | None |
| C1 | Hierarchical summaries | High | High | B2 (summary caching) |
| C2 | Stale-context visibility at point of use | High | Medium | B2 |

**Recommended first sprint:** A1 + A2 + B4 (all low-effort, no new infrastructure).
**Second sprint:** B1 + B2 (structural caching wins).
**Third sprint:** B3 + A3 (learning loop + measurement).

---

## 5. Corrections to the Original Analysis

The original analysis was strong. Two minor corrections from code verification:

1. **"BudgetTokens is carried into the result/cache key, but the pack builder
   still mostly uses limit and emits relationships/candidates independently"**
   — Confirmed. `buildPackResult` at `pack.go:396` uses `ctx.limit` exclusively.
   `BudgetTokens` is a cache-key component but **never** bounds output size.

2. **"context_telemetry.go (line 365)"** — The actual path is
   `internal/capabilities/workspace/context_telemetry.go:365`, not
   `internal/repository/comprehension/context_telemetry.go`. The telemetry
   extraction function `addContextCacheTelemetry` does correctly extract
   cache state/source/reason/fingerprints from pack output for audit
   consumption.

3. **Cache key for `context_query`/`context_zoom`** — The original analysis
   correctly notes these are not cached. The `graph_views` table schema
   (with its `resolution` column already supporting `"plan"` and `"implement"`)
   can naturally extend to `"query"` and `"zoom"` resolutions.

---

## 6. Metrics to Track

From `docs/idiom-strategic-thesis.md` line 394–402, extended with specifics:

| Metric | Current state | Target |
|---|---|---|
| Top-5 localization hit rate | Not measured | Benchmarked |
| Average broad reads before first edit | High (audit saw 414 `code_navigate`, 150 `search_files`, 130 `search_content`) | Decreasing |
| `context_pack` cache hit rate | Already tracked in `ViewStats` | >0.5 for repeated workflows |
| Stale-row prevention rate | Already tracked (`stalePreventions`) | Monitored, not targeted |
| Pack quality `source_owner_found` rate | Computed but not surfaced | >0.7 for plan-mode packs |
| Verified-edit → ranker feedback loop | Not wired | Wired with learning log |
| `context_query`/`zoom` cache reuse | N/A (not cached) | Hit rate >0.3 |
