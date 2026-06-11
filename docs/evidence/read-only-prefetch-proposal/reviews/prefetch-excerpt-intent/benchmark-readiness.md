[2m  ▎ thinking[0m
  · 16992 tok · in 16848 (16640 cached / 208 new) · out 144 (41 reasoning) · ¥0.0008
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
  -> glob {"pattern": "docs/evidence/read-only-prefetch-proposal/**"}
[2m  ▎ thinking[0m
  · 19629 tok · in 19378 (17152 cached / 2226 new) · out 251 (17 reasoning) · ¥0.0031 · cache prefix changed: tools
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/benchmark-readiness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/benchmark-readiness.stderr.log"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/final-architecture-expansion/benchmark-readiness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/final-architecture-expansion/benchmark-readiness.stderr.log"...
[2m  ▎ thinking[0m
  · 25194 tok · in 24899 (19584 cached / 5315 new) · out 295 (43 reasoning) · ¥0.0063
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/drift-risk.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/harness-correctness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/north-star.md"}
  -> grep {"pattern": "61,878|61878|prompt.tokens|prompt_tokens|five full files|5 full files", "path": "docs/evidence/read-only-pr...
[2m  ▎ thinking[0m
Let me also look at the current prefetch implementation and the metrics files to understand the actual benchmark data.
  · 27701 tok · in 27519 (25088 cached / 2431 new) · out 182 (29 reasoning) · ¥0.0033
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> glob {"pattern": ".idiom/benchmark/**/summary.json"}
[2m  ▎ thinking[0m
  · 31777 tok · in 31583 (27648 cached / 3935 new) · out 194 (37 reasoning) · ¥0.0049
  -> read_file {"path": ".idiom/benchmark/20260611-141654/summary.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-152410-large/summary.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-154037-large-fixcheck/summary.json"}
[2m  ▎ thinking[0m
  · 39725 tok · in 39063 (31744 cached / 7319 new) · out 662 (556 reasoning) · ¥0.0093
  -> read_file {"path": ".idiom/benchmark/20260611-142351/summary.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-145729/summary.json"}
[2m  ▎ thinking[0m
---

## Review: Capped Prefetch Excerpts — Benchmark/Readiness Assessment

**Document:** `docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md`

**Benchmark context verified from** `.idiom/benchmark/` summaries:

| Benchmark | Scenario | Model calls | Tool calls | Total prompt tokens | Answer |
|-----------|----------|-------------|------------|-------------------|--------|
| `20260611-152410-large` | No prefetch, 5 explicit files | 5–7 | 9–11 | 59k–86k | ✅ (trial 1 only) |
| `20260611-154037-large-fixcheck` | Same 5 files, deeper run | 15 | 14 | **340k** (cumulative) | ✅ |
| Reasonix baseline (same task) | Single bundled evidence call | 1 | — | ~53k | ✅ |

The gap is clear: serial `read_file` loops inflate cumulative prompt tokens by 2–6× (59k → 340k in the pathological case). Full-file prefetch would collapse this to ~62k in one call. **Capped excerpts would reduce it further while preserving the safety valve of follow-up `read_file`.**

---

### Verdict: APPROVE — valid quick win, with binding measurement requirements

The intent document's safety argument is sound: the change is scoped to synthetic `prefetch` output only, `read_file` remains unbounded, truncation metadata is explicit, and no routing/lane/child-agent behavior changes. This is considerably lower risk than global tool-output truncation or benchmark-only behavior.

---

### What Must Be Measured

These measurements must be run on the same 5-file benchmark task used in the latest benchmarks (`docs/design-hosted-cache-cost-metrics.md`, `internal/runtime/llama/client.go`, `internal/runtime/agent/runner_state.go`, `internal/surfaces/tui/app/model_state.go`, `internal/evidence/transcript/debuglog.go`) with prefetch active:

**1. Per-file excerpt utilization** — How many lines/bytes of each file are actually in the prefetched output, vs the full file? This determines whether the excerpt cap is actually binding. If most files are ≤100 lines already (e.g. `README.md`, design docs), the cap is a no-op — and the token savings come entirely from truncating the Go source files (which are 200–600 lines).

**2. Model call count with excerpts** — The key metric. Hypotheses:
| Call count | Verdict |
|-----------|---------|
| 1 (same as full-file prefetch) | ✅ Excerpt was sufficient |
| 2–3 | ✅ Still a net win vs 5–7 / 15 |
| 4+ | ⚠️ Excerpt too aggressive or follow-up affordance unclear |

**3. Follow-up `read_file` calls** — How many additional reads does the model issue after seeing the excerpt? If the model consistently requests the same middle/lower sections of the same files, the excerpt cap is hitting the wrong boundary. Expected: 0–2 follow-ups total (for specific function bodies the excerpt clipped).

**4. Answer correctness (same-task comparison)** — Compare excerpt-prefetch answer against full-file-prefetch answer and the non-prefetch 15-call baseline (which produced a correct, detailed answer). Critical check: does any fact present in the full-file answer disappear in the excerpt answer? This is the hardest measurement and the most important for north-star alignment (evidence completeness before answer).

**5. `TruncatedFiles` and `EstimatedRoundTripsSaved` telemetry** — The intent doc adds `TruncatedFiles` to the existing `read_only_named_path_prefetch` event. Validate it fires correctly and the estimated savings reflect actual round-trip reduction.

**6. False negative audit** — Manually review the excerpt output for each file and flag whether any semantically important block (exported type definition, critical function body, key comment block) was clipped. This is a one-time manual check across the benchmark fixture. If ≥1 file's excerpt drops information the model needed to answer correctly, the excerpt limit is too aggressive or should be adaptive.

---

### Risk Assessment

| Risk | Likelihood | Mitigation in intent doc |
|------|-----------|--------------------------|
| Model doesn't realize excerpt is truncated and produces shallow answer | Low–Medium | Truncation metadata + explicit "bounded `read_file` follow-up available" note |
| Follow-up `read_file` calls erase token savings | Low | Even 2 follow-ups = 3 total calls vs 5–15 without prefetch |
| Excerpt cap is tuned wrong (too generous = no savings; too tight = many follow-ups) | Medium | Should be configurable; start at 100 lines / 6KB per file and measure |
| No regression test for excerpt truncation flagging | Low (process) | Should be added alongside; not in current intent doc but follows from the safety argument |

---

### Recommended Excerpt Cap Starting Point

Based on the benchmark fixture's file profiles (Go source files with key types and exported functions in the first ~60–100 lines, design docs under 200 lines):

- **Per-file excerpt limit:** 8 KB (~150–200 lines)
- **Total prefetch excerpt budget:** 48 KB (~1,000 lines across 6 files)

This would reduce the prefetch payload from ~62k tokens to an estimated **~18–25k tokens** per first call — comparable to the Reasonix baseline — while being generous enough that the model rarely needs a follow-up for architectural understanding. Function body details that fall beyond line 150 in a 400-line Go file would still require a `read_file` call, but those are *targeted* reads (6–15 lines each, not full-file serial re-reads).

---

### Summary

Capped prefetch excerpts are a **valid quick win** — the architecture change is scoped, the safety argument is sound, and the token-saving potential (from ~62k to ~18–25k per first call) is substantial. The binding condition is measurement #4 (answer correctness): if excerpt answers are factually complete for the same task, ship it. If not, the excerpt boundary needs adjustment or the model-facing note needs strengthening, but the approach itself is correct.
  · 48698 tok · in 45230 (39680 cached / 5550 new) · out 3468 (2116 reasoning) · ¥0.0133
