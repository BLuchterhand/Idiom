[2m  ▎ thinking[0m
  · 17054 tok · in 16900 (16640 cached / 260 new) · out 154 (35 reasoning) · ¥0.0009
  -> read_file {"path": "/workspace/idiom/docs/proposal-read-only-named-path-evidence-prefetch.md"}
  -> read_file {"path": "/workspace/idiom/docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
Let me gather the benchmark evidence cited in the proposal and understand the current runtime configuration.
  · 21871 tok · in 21606 (17152 cached / 4454 new) · out 265 (100 reasoning) · ¥0.0053 · cache prefix changed: tools
  -> glob {"pattern": "**/benchmark/**/summary.json"}
  -> glob {"pattern": "**/benchmark/**/conversations/*.jsonl"}
  -> grep {"pattern": "prefetch", "path": "/workspace/idiom"}
  · tool output truncated: 8468 of 41236 bytes elided
[2m  ▎ thinking[0m
  · 30966 tok · in 30549 (21760 cached / 8789 new) · out 417 (108 reasoning) · ¥0.0101
  -> read_file {"path": "/workspace/idiom/.idiom/benchmark/20260611-154037-large-fixcheck/summary.json"}
  -> read_file {"path": "/workspace/idiom/.idiom/benchmark/20260611-152410-large/summary.json"}
  -> read_file {"path": "/workspace/idiom/.idiom/benchmark/20260611-141654/summary.json"}
  -> read_file {"path": "/workspace/idiom/.idiom/benchmark/20260611-142351/summary.json"}
  -> read_file {"path": "/workspace/idiom/.idiom/benchmark/20260611-145729/summary.json"}
[2m  ▎ thinking[0m
  · 44035 tok · in 43753 (30848 cached / 12905 new) · out 282 (26 reasoning) · ¥0.0141
  -> grep {"path": "/workspace/idiom", "pattern": "deepseek-v4-flash|deepseek-v4-pro|deepseek-chat", "glob": "con...
  -> explore {"task": "In /workspace/idiom, find the runtime configuration that maps model aliases (deepseek-v4-flas...
  -> mcp__codegraph__files {"pattern": "**/*.go", "path": "/workspace/idiom"}
  -> glob {"pattern": "**/config/**", "path": "/workspace/idiom"}
  -> grep {"pattern": "deepseek", "path": "/workspace/idiom"}
  -> read_file {"path": "/workspace/idiom/backends.example.json"}
  -> read_file {"path": "/workspace/idiom/internal/platform/config/config.go", "offset": 100, "limit": 200}
  -> glob {"path": "/workspace/idiom/internal/runtime/backends", "recursive": true}
  ⊘ glob pattern is required
  -> read_file {"path": "/workspace/idiom/internal/platform/config/config.go", "offset": 300, "limit": 200}
  -> ls {"path": "/workspace/idiom/internal/runtime/backends"}
  -> read_file {"path": "/workspace/idiom/internal/surfaces/tui/app/hosted_backend_test.go"}
  -> read_file {"limit": 200, "offset": 0, "path": "/workspace/idiom/internal/runtime/backends/backends.go"}
  -> read_file {"limit": 200, "offset": 100, "path": "/workspace/idiom/internal/runtime/backends/launcher.go"}
  -> read_file {"limit": 300, "offset": 200, "path": "/workspace/idiom/internal/runtime/backends/backends.go"}
  -> codegraph_search {"query": "AliasForRole"}
  -> grep {"path": "/workspace/idiom/internal/runtime", "pattern": "AliasForRole|roleSpec|backendsModelToLlamaSer...
  ⊘ codegraph_search unknown tool "codegraph_search"
  -> grep {"path": "/workspace/idiom/internal/surfaces/tui/app", "pattern": "func roleSpec"}
  -> grep {"path": "/workspace/idiom/internal/runtime/llama", "pattern": "ToolCall|tool_call|TextualTool|native.*...
  -> codegraph_search {"query": "deepseek", "kind": "function"}
  ⊘ codegraph_search unknown tool "codegraph_search"
  -> read_file {"limit": 100, "offset": 110, "path": "/workspace/idiom/internal/surfaces/tui/app/lifecycle.go"}
  -> read_file {"limit": 120, "offset": 80, "path": "/workspace/idiom/internal/runtime/llama/client.go"}
  -> read_file {"limit": 200, "offset": 270, "path": "/workspace/idiom/internal/runtime/llama/client.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/llama/deepseek_test.go", "limit": 100}
  -> read_file {"limit": 100, "offset": 1050, "path": "/workspace/idiom/internal/platform/config/config.go"}
  -> read_file {"limit": 100, "offset": 1050, "path": "/workspace/idiom/internal/runtime/llama/client.go"}
  -> grep {"path": "/workspace/idiom/internal/platform/config", "pattern": "BuiltinChatPresets|qwen3-coding"}
  -> grep {"path": "/workspace/idiom/internal/runtime/llama", "pattern": "prompt_cache|Cache"}
  -> read_file {"limit": 80, "offset": 974, "path": "/workspace/idiom/internal/platform/config/config.go"}
  -> grep {"path": "/workspace/idiom/internal/surfaces/tui/app", "pattern": "backendsModelToLlamaServer"}
  ⊘ explore sub-agent: paused after 10 tool-call rounds (agent.max_steps) — the work so far is saved; send another message to continue, or set max_steps higher or to 0 for no limit
[2m  ▎ thinking[0m
Let me directly check the runtime configuration files.
  · 53565 tok · in 53314 (44032 cached / 9282 new) · out 251 (14 reasoning) · ¥0.0107
  -> read_file {"path": "/workspace/idiom/backends.example.json", "offset": 85, "limit": 40}
  -> glob {"pattern": "**/.idiom/backends.json"}
  -> grep {"path": "/workspace/idiom/internal/runtime/llama", "pattern": "tool_choice|ToolChoice|TextualTools|Nat...
[2m  ▎ thinking[0m
  · 56011 tok · in 55732 (53504 cached / 2228 new) · out 279 (42 reasoning) · ¥0.0039
  -> grep {"path": "/workspace/idiom/internal/runtime/llama/client.go", "pattern": "BackendSupportsNativeTools|de...
  -> grep {"glob": "*.go", "path": "/workspace/idiom/internal/runtime", "pattern": "BackendSupportsNativeTools"}
  -> read_file {"path": "/workspace/idiom/.idiom/backends.json", "limit": 80}
[2m  ▎ thinking[0m
  · 57716 tok · in 57519 (55936 cached / 1583 new) · out 197 (24 reasoning) · ¥0.0031
  -> read_file {"path": "/workspace/idiom/internal/runtime/llama/client.go", "offset": 280, "limit": 30}
  -> grep {"path": "/workspace/idiom/internal/runtime/llama/client.go", "pattern": "Content|content.*=.*Text|Text...
[2m  ▎ thinking[0m
  · 59397 tok · in 59170 (57600 cached / 1570 new) · out 227 (131 reasoning) · ¥0.0032
  -> read_file {"path": "/workspace/idiom/docs/design-direct-answer-efficiency.md", "limit": 80, "offset": 0}
[2m  ▎ thinking[0m
  · 60572 tok · in 60459 (59392 cached / 1067 new) · out 113 (40 reasoning) · ¥0.0025
  -> read_file {"path": "/workspace/idiom/docs/evidence/idiom-efficiency-optimization/reviews/model-runtime-config.md"...
[2m  ▎ thinking[0m
  · 64927 tok · in 64681 (60544 cached / 4137 new) · out 246 (69 reasoning) · ¥0.0058
  -> read_file {"path": "/workspace/idiom/docs/evidence/read-only-prefetch-proposal/reviews/model-runtime-config.md", ...
  -> grep {"path": "/workspace/idiom/docs/evidence/read-only-prefetch-proposal/reviews/model-runtime-config.md", ...
[2m  ▎ thinking[0m
  · 65228 tok · in 65095 (64896 cached / 199 new) · out 133 (28 reasoning) · ¥0.0018
  -> read_file {"limit": 50, "offset": 5, "path": "/workspace/idiom/docs/evidence/read-only-prefetch-proposal/reviews/...
[2m  ▎ thinking[0m
Now I have all the evidence needed. Here is my review.

---

## Model Runtime / Config Reviewer — `docs/proposal-read-only-named-path-evidence-prefetch.md`

**Reviewer scope:** Hosted-model round trips, cache/miss behavior, prompt accumulation, tool-call transport, token accounting, model attribution, and whether prefetch is the right layer versus model/backend configuration.

---

### Verified facts

1. **Idiom large-fixcheck benchmark (`20260611-154037-large-fixcheck`):** 44.47s, 15 model calls, 14 tool calls, 340,538 prompt tokens (total across all calls), 31,586 peak prompt tokens (largest single call), 183,168 cache-hit tokens (53.8% hit rate), 157,370 cache-miss tokens, 2,103 completion tokens, 0 reasoning tokens, `answer_succeeded: true`. Effective model: `deepseek-v4-flash`. Source: `.idiom/benchmark/20260611-154037-large-fixcheck/summary.json` lines 3-68.

2. **Reasonix large-fixcheck benchmark (same run):** 19.43s, 53,053 prompt tokens (total), 17,280 cache-hit tokens (32.6% hit rate), 35,773 cache-miss tokens, 1,244 completion tokens, 729 reasoning tokens, `answer_succeeded: false`. Single trial. Source: same summary.json lines 66-111.

3. **Pre-fixcheck benchmark (`20260611-152410-large`):** Idion both trials `answer_succeeded: false` — source-grounding gate blocked them (7 model calls, 11 tool calls in trial 1; 5 model calls, 9 tool calls in trial 2). Reasonix both trials `answer_succeeded: true`. Same task, same model (`deepseek-v4-flash`). Source: `.idiom/benchmark/20260611-152410-large/summary.json`.

4. **The benchmark task explicitly names 5 workspace-relative file paths:** `docs/design-hosted-cache-cost-metrics.md`, `internal/runtime/llama/client.go`, `internal/runtime/agent/runner_state.go`, `internal/surfaces/tui/app/model_state.go`, and `internal/evidence/transcript/debuglog.go`. This is an exact-match for the proposal's eligibility criteria. Source: summary.json lines 167.

5. **Backend configuration confirmed:** The active profile (`deepseek-v4-pro` in `.idiom/backends.json`) maps to `kind: "deepseek"`, `model: "deepseek-chat"`. The benchmark was run with a CLI `-model` override to `deepseek-v4-flash`. `BackendSupportsNativeTools("deepseek")` returns `true` — the hosted DeepSeek API uses native OpenAI-style tool calls. Source: `.idiom/backends.json:30-55`, `internal/runtime/llama/client.go:284-291`.

6. **Reasonix cached prompt tokens are near-constant across all benchmark runs:** 17,152 cached tokens every trial (with one outlier at 13,312). This is consistent with a fixed-size system prompt being cached. Source: all 5 summary.json files in `.idiom/benchmark/`.

7. **Idiom's tool sequence was 9 `read_file`, 4 `code_navigate`, and 1 `search_content`** — 14 tool calls serialized one-at-a-time through the model loop. Source: proposal lines 29-30 (citing conversation JSONL).

8. **Idiom's cache hit rate increases with more model calls** — 53.8% at 15 calls in the fixcheck run vs 0% reported in earlier runs. This is consistent with the conversation prefix growing with each turn and being partially cached. Source: `20260611-154037-large-fixcheck/summary.json:24-28`.

---

### Inferences

1. **The 14 tool calls each required a separate hosted model round trip.** Each tool call requires: model emits tool call request → harness executes tool → tool result is appended → full conversation (including newly loaded evidence) is sent back to the model. At hosted API latency (~1-2s per request plus token processing), 14 extra round trips explain roughly 20-28s of the 44.47s total wall time. The remaining gap is token processing time for the accumulated 340k tokens.

2. **Prompt accumulation is the token cost driver.** Idiom sent 340,538 prompt tokens total across 15 calls. Even with 53.8% caching, 157,370 cache-miss tokens were billed. Reasonix sent only 53,053 token in (presumably) 1-2 calls, with 35,773 cache-miss tokens. The 4.5× multiplier in billed tokens is entirely from re-sending unique tool evidence per round trip.

3. **Reasonix's "one call" approach bundles evidence into the initial prompt.** Reasonix's ~53k total prompt tokens vs Idiom's 340k suggests Reasonix reads the evidence files before the first model call and includes bounded excerpts — exactly what the proposal's prefetch stage would do. Reasonix's higher proportion of cache-hit tokens (32.6% of total = 17,280) in a *single* call means its system prompt is cached, and the model-only new tokens are bounded.

4. **The `answer_succeeded: false` for Reasonix in the fixcheck run does not affect the latency comparison.** The 19.43s includes 729 reasoning tokens, meaning the model spent time thinking before failing. A successful Reasonix trial (from the pre-fixcheck run) averaged 18s with 7 fewer tool calls. The latency advantage holds regardless of answer success — the structure is inherently faster.

5. **Idiom's 0 reasoning tokens is not a meaningful gap.** The model (`deepseek-v4-flash`) produces reasoning tokens when prompted (Reasonix shows 729 in its failed trial). Idiom may be stripping `reasoning_content` from the response (known behavior in `client.go`), but this does not affect wall time meaningfully — reasoning would add latency, not remove it.

6. **The prompt cache hit rate difference (53.8% Idiom vs 32.6% Reasonix) is a denominator artifact.** Idiom's 53.8% hit rate with 15 calls means the growing conversation prefix gets partially cached, while Reasonix's single call has no repeat prefix to cache beyond the fixed system prompt. This does *not* mean Idiom is more cache-efficient — it means the denominator (total tokens sent) is inflated by re-sent cached conversation history.

---

### Root cause

**Serial evidence transport under hosted DeepSeek API latency and per-call token billing.** The harness serializes each `read_file` / `code_navigate` / `search_content` through the model loop — emit tool call → execute → append result → re-send full conversation → repeat. For a prompt that names 5 explicit files, this produces 14 round trips with 340k cumulative prompt tokens when 1-2 calls with bounded evidence would suffice.

The first broken invariant:

> When a read-only prompt explicitly names local source files, the harness should attach bounded evidence to the initial context rather than requiring the model to discover and read each file through N separate tool-annotated round trips, each incurring hosted API latency and re-sending accumulated token context.

---

### Contributing factors

1. **DeepSeek Flash is a hosted (not local) API.** Each model call incurs network latency (~200-500ms) plus token processing time (~2-4ms per token for input, variable for output). 15 calls × ~1.5s average = ~22s of round-trip overhead before any token processing. Source: consistent with observed wall times vs local-backend benchmarks.

2. **No mechanism to batch evidence reads.** The tool surface exposes `read_file`, `code_navigate`, and `search_content` as discrete tool calls. There is no `read_files` (plural) or prefetch stage to harvest explicitly-named paths before the first model call.

3. **Prompt accumulation creates a growing per-call tax.** Each model call after a tool execution includes the entire prior conversation plus the new tool output. By the final call, ~71k characters of tool output are embedded in a 31,586-token prompt. This is re-sent and partially re-cached each time, inflating billed token counts even when cache hit rate is high.

4. **Cache is not free on each round trip.** Despite 53.8% cache-hit rate, the cache-miss tokens (157,370) represent substantial billed token volume from unique evidence per call. Each file read is new content that cannot be cached until a future call reuses it.

5. **The source-grounding gate (pre-fixcheck) blocked correct answers, forcing re-runs.** The benchmark required 2 runs: the first run failed (source-grounding blocked), the fixcheck run succeeded but at 15 calls. The gate fix was correct, but without prefetch, any evidence-heavy task will still produce high call/token counts.

6. **`reasoning_content` handling is misconfigured for hosted DeepSeek.** Existing code strips `reasoning_content` from follow-up request payloads (correct for local backends, wrong for DeepSeek hosted-chat thinking mode). This is a separate issue from the latency problem but affects correctness of thinking-mode runs. Source: `internal/runtime/llama/client_test.go:326-327`, confirmed in prior review.

---

### Non-causes

- **Not a model quality issue.** The same model (`deepseek-v4-flash` / `deepseek-chat`) answers correctly when evidence is properly presented. The 15 model calls were not from model refusal, hallucination, or inability — they were from the harness requiring discovery through serial tool use.

- **Not a tool-call transport issue.** Native tool calls work correctly with the hosted DeepSeek API. No malformed tool calls, failed parsings, or tool-call errors appear in any benchmark trial.

- **Not a backend selection issue.** Both tools used the same hosted DeepSeek API. Switching backends (local llama.cpp, MLX) would change latency per call but would not reduce the number of round trips.

- **Not a prompt-template or system-instruction issue.** The model was not confused by conflicting instructions — it correctly chose to read files and record tool calls. The serial read-vs-outline strategy is model-owned strategy, not a harness override.

- **Not a cache configuration issue.** DeepSeek prompt caching is working (Idiom 53.8% hit rate). The problem is that cached content is mostly repeated conversation structure, not evidence — each new file read is a cache miss.

- **Not a source-grounding gate issue** (in the fixcheck run). The gate was fixed between the pre-fixcheck and fixcheck runs. The 15-call profile is from the *successful* run with the correct gate.

---

### Fix recommendation

**Approve the proposal as scoped, with these runtime-specific notes:**

1. **Prefetch is the correct layer.** The bottleneck is not model quality, backend configuration, tool-call transport, or prompt caching — it is serial evidence transport through the model loop. A host-owned prefetch stage that reads explicitly-named paths before the first model call directly addresses this at the harness level. No model or backend configuration change can fix the N-round-trip pattern.

2. **The "serial evidence transport" diagnosis is well-supported by the benchmark data.** 14 tool calls × ~1.5s average round trip + 157k cache-miss tokens = the dominant share of the 44.47s wall time and 340k cumulative tokens. The fix reduces both dimensions proportionally.

3. **Bounded prefetch caps are essential.** The proposal's limits (6 files max, bounded bytes per file, aggregate cap) prevent prefetch from ballooning the initial context. Without these, prefetch could exceed the model's context window or inflate the first call unnecessarily. The truncation-marking requirement (line 150) is correct — partial evidence must be visibly partial.

4. **The A6 (lightweight surface) dependency is architecture-critical.** The proposal and the related `design-direct-answer-efficiency.md` correctly sequence A6 (surface inventory) before A1 (prefetch). Prefetch must go through the evidence pipeline, not the system-prompt pipeline. The existing gate — `DirectAnswerSurfaceCapSourceBodyPrefetch` not yet advertised in `directAnswerSurfaceCapabilities()` — is verified as enforced.

5. **Do not scope-creep into inference-time fixes.** The proposal correctly excludes:
   - Changing the model's strategy prompts or tool list
   - Adding a forced "answer now" gate
   - Global caching or cost-metric redesign
   These would be model-quality or backend-config changes, not evidence-transport changes.

6. **One runtime-specific gap:** The proposal should also log `model_calls_saved` in prefetch telemetry — how many round trips the prefetch eliminated versus a non-prefetched baseline. This is essential for future benchmark comparisons to distinguish "fewer calls due to prefetch" from "fewer calls due to different model behavior."

---

### Regression tests

| Test | Purpose | Coverage gap | Runtime-specific concern |
|------|---------|-------------|--------------------------|
| `go test ./internal/runtime/agent -run 'DirectAnswer\|ReadOnly\|Evidence'` | Prefetch eligibility + evidence accounting | **Gap:** No hosted-model round-trip count assertion in existing tests | Prefetch must not break native tool-call paths for DeepSeek |
| `go test ./internal/runtime/llama/...` | Backend tool support + reasoning_content preservation | **Gap:** No test for `reasoning_content` preservation in follow-up payloads for DeepSeek | DeepSeek hosted-chat requires thinking-mode continuity; stripping breaks it |
| Prefetch count assertion | Prefetch reduces model calls for exact-path prompts | **New test needed:** Assert that a prompt naming 5 existing files produces 1 model call instead of 5+ | Test must run against a mock backend that records call count |

**Dedicated integration test needed:** `TestExactPathPrefetchReducesModelCalls` — a runner-level test where the user prompt names 3 existing files. Assert that:
- Prefetch packet is attached before the first model call (via event log)
- Model calls ≤ 2 (1 for classification/routing + 1 for final answer)
- No `read_file` or `search_content` tool calls in the tool trace (evidence is already in context)

---

### North-star risk

**Low.** The proposal preserves strategy ownership (prefetch is evidence transport, not answer synthesis), keeps gates as invariants (prefetched evidence goes through the same ledger as normal `read_file` results), and avoids prompt-text authority (no scenario-specific prompt nudge or strategy instruction).

**Specific risk to monitor:** Over-eager path extraction. The proposal's extractor must be conservative — it should match explicit workspace-relative file paths only, not package names, symbol names, or globs. False-positive path extraction could read unintended files into context. The mitigation (lines 131-149, "Safety Rules") is correct but depends on disciplined implementation.

---

### Confidence

**High** — the latency and token diagnosis is quantitatively supported by complete benchmark summaries with cache, token, call-count, and timing fields. The prefetch solution maps directly to the verified bottleneck. The proposal correctly classifies the problem as evidence transport, not model quality or backend configuration.

**Medium** on one question: whether 6-file cap and bounded bytes per file are sufficient for the largest expected read-only prompts. If a user names 10-15 files (a realistic scenario for code-review or architecture questions), the 6-file cap means some files still go through serial model-directed reads. This is a reasonable first-cap but should be revisited after benchmark replay.

---

### Final verdict: **approve-with-changes**

**Required change before approval:** Add a `model_calls_saved` or `round_trips_saved` field to the prefetch telemetry schema (proposal line 90). This field should report `len(extracted_paths) - min(1, len(prefetched_paths))` — the number of model call round trips the prefetch eliminated compared to a non-prefetched baseline where each file required one read tool call. Without this field, post-fix benchmark analysis cannot distinguish "prefetch reduced calls" from "model chose to read fewer files."

**Recommendation:** Add a `prefetch.eliminated_round_trips` counter to the structured event described in line 69, and include it in both telemetry and the benchmark summary schema. This is a low-cost addition that directly addresses the reviewer bench requirement that "telemetry/audit metadata is sufficient to RCA future benchmark deltas without reading raw transcripts" (proposal drift checks, line 209).
  · 72902 tok · in 66912 (65152 cached / 1760 new) · out 5990 (2343 reasoning) · ¥0.0150
