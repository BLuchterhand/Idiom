[2m  ▎ thinking[0m
  · 17151 tok · in 17055 (13568 cached / 3487 new) · out 96 (29 reasoning) · ¥0.0040
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-implementation/review-packet.md"}
[2m  ▎ thinking[0m
  · 26202 tok · in 26008 (13568 cached / 12440 new) · out 194 (60 reasoning) · ¥0.0131 · cache prefix changed: tools
  -> grep {"pattern": "five-file.*benchmark|DeepSeek.*cache.*benchmark|prefetch.*benchmark|benchmark.*criteria", "path": "docs/evi...
  -> ls {"path": "docs/evidence/read-only-prefetch-proposal"}
[2m  ▎ thinking[0m
  · 29008 tok · in 28817 (26112 cached / 2705 new) · out 191 (74 reasoning) · ¥0.0036
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-implementation/benchmark-readiness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
  · 31051 tok · in 30847 (28928 cached / 1919 new) · out 204 (68 reasoning) · ¥0.0029
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md", "offset": 60, "limit": 15}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
[2m  ▎ thinking[0m
  · 34207 tok · in 34013 (30976 cached / 3037 new) · out 194 (59 reasoning) · ¥0.0040
  -> grep {"path": "internal/runtime/agent", "pattern": "readOnlyPrefetchEvent|readOnlyNamedPathPrefetchEvent|EstimatedRoundTripsS...
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch_test.go"}
[2m  ▎ thinking[0m
  · 37420 tok · in 37344 (34176 cached / 3168 new) · out 76 (22 reasoning) · ¥0.0040
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
  · 39150 tok · in 38956 (37376 cached / 1580 new) · out 194 (38 reasoning) · ¥0.0027
  -> grep {"path": ".", "pattern": "read_only_named_path_prefetch|prefetch.*summary|summary.*prefetch", "glob": "**/*.go"}
  -> grep {"path": "docs/evidence", "pattern": "five-file.*DeepSeek|DeepSeek.*cache.*benchmark"}
[2m  ▎ thinking[0m
  · 42365 tok · in 42159 (39040 cached / 3119 new) · out 206 (33 reasoning) · ¥0.0043
  -> grep {"path": ".", "pattern": "summary\\.json|benchmark.*aggregat|eval.*prefetch", "glob": "**/*.go"}
  -> grep {"path": ".", "pattern": "summary\\.json", "glob": "**/*.md", "limit": 30}
  · tool output truncated: 5921 of 38689 bytes elided
[2m  ▎ thinking[0m
  · 59838 tok · in 59686 (42240 cached / 17446 new) · out 152 (25 reasoning) · ¥0.0186
  -> read_file {"path": ".idiom/benchmark/20260611-154037-large-fixcheck/summary.json", "limit": 80}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/runtime-review-focused.md"}
[2m  ▎ thinking[0m
  · 63946 tok · in 63702 (59776 cached / 3926 new) · out 244 (73 reasoning) · ¥0.0056
  -> read_file {"limit": 60, "offset": 60, "path": ".idiom/benchmark/20260611-154037-large-fixcheck/summary.json"}
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "limit": 50, "offset": 225}
[2m  ▎ thinking[0m
Now I have a thorough view of the implementation, the telemetry, the event schema, the benchmark summary structure, and the stated criteria. Here is my formal review.

---

## Benchmark Readiness Review: Prefetch Excerpt Implementation

### Verified Facts

| Layer | What exists | Evidence |
|---|---|---|
| **Prefetch event** | `readOnlyPrefetchEvent` (role `read_only_named_path_prefetch`) with 11 fields including `eligible`, `selected_paths`, `bytes`, `truncated_files`, `estimated_round_trips_saved` | `read_only_prefetch.go:15-27` |
| **Event routing** | Deferred `emitEvent` call fires on every attempt (eligible or not), writing to the conversation JSONL log | `read_only_prefetch.go:31-35` |
| **Benchmark summary** | `summary.json` aggregates `model_calls`, `tool_calls`, `prompt_tokens`, `peak_prompt_tokens`, `completion_tokens`, `duration_ms` per runner | `.idiom/benchmark/20260611-154037-large-fixcheck/summary.json` |
| **Answer recording** | Full answer text is recorded in the benchmark summary | Same file, lines 114–115 |
| **Baseline reference** | The existing benchmark (without prefetch) produced 340,538 prompt tokens total, 15 model calls, 14 tool calls, 61,878 first-call prompt tokens | `model-runtime-config.md` and baseline summaries |
| **Rollout Step 4** | "Add benchmark summary fields for prefetch hits, skipped paths, prefetched bytes, dedup count, and estimated eliminated round trips" — **not implemented** | `scope-notes.md:43`, `runtime-review-focused.md:62-64` |
| **Per-tool breakdown** | `summary.json` has total `tool_calls` but no per-tool-name counts | Verified in the summary.json schema |

---

### Evaluation Against the Four Stated Criteria

#### Criterion 1: "first-call prompt tokens drop by at least 40% versus the full-prefetch rerun (61,878)"

**Gap: partially measurable.** The benchmark summary tracks `peak_prompt_tokens` (largest single call) and `prompt_tokens` (total across all calls), but not "first call tokens" as a named field. The first model_call's prompt token count *can* be extracted from the conversation JSONL, but requires a post-hoc parsing step. There is no summary-level assertion that compares first-call tokens against the baseline.

A proxy approach using `peak_prompt_tokens` is possible but ambiguous: with prefetch, the first call now carries all evidence inline, so `peak_prompt_tokens` is likely the first call — but this is an inference, not a guaranteed alignment.

#### Criterion 2: "model calls remain at one or increase to at most three"

**✅ Directly measurable.** `summary.json` aggregates `model_calls` with min/median/max. This is a zero-effort comparison against the baseline of 15.

#### Criterion 3: "the answer still names [the five required concepts]"

**⚠️ Measurable but manual.** The full answer text is recorded in each trial's JSON entry in the benchmark summary. A human or automated script could grep for the required concepts. However, there is no automated scoring pipeline or oracle in the benchmark infrastructure that asserts semantic coverage of these five concepts. The existing `TestRunnerTruncatedPrefetchCanAnswerWithoutReadFileFollowup` shows the pattern for a single-file oracle, but this is a unit test, not a benchmark-eval integration.

#### Criterion 4: "any follow-up `read_file` calls are bounded and fewer than the five serial reads that prefetch replaced"

**⚠️ Indirectly measurable.** `summary.json` tracks `tool_calls` but does not break them down by tool name. A human/agent would need to parse the conversation JSONL to count `read_file` invocations specifically. The total `tool_calls` count (baseline: 14) can serve as a coarse proxy: if tool calls drop from 14 to, say, 5-8, it is reasonable to infer that the serial `read_file` sequence was replaced by prefetch. But this conflates `read_file`, `code_navigate`, and `search_content` calls.

---

### Metric Gaps

| # | Gap | Impact | Severity |
|---|---|---|---|
| **G1** | **No prefetch-layer fields in `summary.json`** — Rollout Step 4 is deferred. No hit count, skipped count, bytes, dedup count, or `estimated_round_trips_saved` appears in the benchmark aggregate. | An observer comparing two benchmark runs cannot distinguish "prefetch improved efficiency" from "model behaved differently" without parsing raw conversation JSONL. Contradicts the proposal's drift-check requirement: "Telemetry/audit metadata is sufficient to RCA future benchmark deltas without reading raw transcripts." | **High** |
| **G2** | **No "first call prompt tokens" field** — Criterion 1 requires this specific value. The existing `peak_prompt_tokens` is a reasonable proxy when prefetch flattens the model's evidence acquisition into a single call, but this is not guaranteed (e.g., if the model still does 1-2 `read_file` follow-ups, the peak could be a later call). | Criterion 1 cannot be validated from `summary.json` alone. Requires conversation-log parsing. | **Medium** |
| **G3** | **No per-tool breakdown** — Criterion 4 requires counting `read_file` calls separately. `summary.json` only has total `tool_calls`. | Criterion 4 cannot be validated from the aggregate. Requires conversation-log parsing. | **Medium** |
| **G4** | **No answer-content scoring pipeline** — Criterion 3 asserts five specific semantic concepts. The infrastructure records the answer text but has no automated oracle that checks for their presence. | Manual or subagent-driven review required for each benchmark run. Low risk if this is acceptable for a one-time validation, but not scalable. | **Low** (for a one-shot benchmark; **Medium** if repeated across trials) |

---

### What the Benchmark *Can* Validate from `summary.json` Alone

| Fact | How | Certainty |
|---|---|---|
| Model calls decreased (from 15) | `model_calls` field | ✅ Direct |
| Total tool calls decreased | `tool_calls` field | ✅ Direct |
| Total prompt tokens decreased | `prompt_tokens` field | ✅ Direct |
| Total completion tokens comparable | `completion_tokens` field | ✅ Direct |
| Peak prompt token effect | `peak_prompt_tokens` (first-call proxy) | ⚠️ Proxy |
| Answer success | `answer_succeeded` field | ✅ Direct |
| Answer text available for human review | `answer` field in trials | ✅ Direct |

### What Requires Conversation-Log Parsing

| Fact | Why |
|---|---|
| First-call prompt tokens vs 61,878 baseline | No dedicated field |
| Number of `read_file` follow-up calls | No per-tool breakdown |
| Prefetch hit/skip/bytes/dedup/round-trips | Rollout Step 4 not implemented |
| Whether prefetch event was eligible or a miss | Recorded in telemetry, not in summary |

---

### What the Final-Arcitecture Review Found (March Review)

The preceding benchmark-readiness review (`final-architecture-expansion/benchmark-readiness.md`) found that a benchmark rerun would "produce valid results for the new behavior" but flagged **one critical finding**: if the benchmark objective does not contain explicit file paths, prefetch is a no-op. The existing benchmark does contain the five explicit paths (`model-runtime-config.md:101`), so this finding is resolved for this specific benchmark.

---

### Verdict

**APPROVE — but only with explicit understanding of the metric gaps.**

The implementation is correct, well-tested, safe, and appropriately scoped. The five-file DeepSeek cache benchmark can validate **2 of 4 criteria directly** from `summary.json` (model calls ≤ 3, answer success/availability). The remaining two criteria (first-call prompt tokens, bounded `read_file` follow-ups) and the prefetch-layer efficiency metrics require parsing the conversation JSONL because Rollout Step 4 (benchmark summary aggregation) was deferred as out-of-scope.

**If the intent is a one-shot validation** (run the benchmark once, inspect logs, declare success or failure for the criteria), this is sufficient: a motivated reviewer can extract first-call prompt tokens and `read_file` counts from the conversation log, and check the answer text for the five required concepts.

**If the intent is ongoing benchmark regression detection** (compare prefetch metrics across runs without raw logs), the G1 gap is blocking — prefetch fields must be wired into `summary.json` before drift analysis can work without raw transcripts.

**Recommendation:** Before declaring the benchmark validated, for this specific run, ensure the conversation log is preserved alongside the summary, and prepare a small script or manual checklist to extract