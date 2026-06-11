[2m  ▎ thinking[0m
I'll start by reading the required documents in parallel.
  · 17168 tok · in 16968 (12032 cached / 4936 new) · out 200 (56 reasoning) · ¥0.0056
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
Now let me check for the benchmark artifacts and the `reasoning_content` evidence.
  · 26387 tok · in 26150 (12032 cached / 14118 new) · out 237 (64 reasoning) · ¥0.0148 · cache prefix changed: tools
  -> glob {"pattern": ".idiom/benchmark/20260611-122151/**"}
  -> grep {"pattern": "reasoning_content", "path": ".idiom"}
  -> grep {"pattern": "deepseek-v4", "path": ".idiom"}
[2m  ▎ thinking[0m
  · 27771 tok · in 27428 (26368 cached / 1060 new) · out 343 (95 reasoning) · ¥0.0023
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> read_file {"path": ".idiom/backends.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
[2m  ▎ thinking[0m
  · 34707 tok · in 34214 (27648 cached / 6566 new) · out 493 (31 reasoning) · ¥0.0081
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-1.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-2.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-3.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-1.txt"}
[2m  ▎ thinking[0m
  · 37634 tok · in 35695 (34688 cached / 1007 new) · out 1939 (1479 reasoning) · ¥0.0056
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stderr-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stderr-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stderr-3.txt"}
[2m  ▎ thinking[0m
Now I have all the evidence needed. Let me verify one more critical claim — whether there's any CLI override mechanism or profile for `deepseek-v4-flash` that could explain the attribution gap.
  · 39972 tok · in 38143 (37632 cached / 511 new) · out 1829 (1621 reasoning) · ¥0.0049
  -> grep {"path": ".idiom", "pattern": "deepseek.*flash|flash.*deepseek"}
  -> grep {"path": ".idiom", "pattern": "v4-flash"}
  -> grep {"path": ".", "pattern": "reasoning_content"}
[2m  ▎ thinking[0m
All evidence collected. Here is my review.

---

## Model Runtime / Config Reviewer — `idiom-efficiency-optimization`

**Reviewer scope:** DeepSeek Flash setup, `reasoning_content`, token/cache/cost observability, model attribution.

---

### Verified facts

1. **No `deepseek-v4-flash` profile exists in `.idiom/backends.json`.** The only DeepSeek profile is `deepseek-v4-pro`, mapping `deepseek-chat` (the full model, not Flash). Source: `.idiom/backends.json` lines 30-55.

2. **All 3 Idiom benchmark trials report route telemetry with role aliases `deepseek-v4-pro`** — classifier, executor, planner, and reviewer all as `deepseek-v4-pro`. No trial mentions `deepseek-v4-flash`. Source: `.idiom/benchmark/20260611-122151/idiom/stdout-{1,2,3}.jsonl`, each `writer_route_decision` event.

3. **No `reasoning_content` error evidence exists in this benchmark artifact.** All three Idiom stderr files are empty. The `reasoning_content` protocol error described in the design doc (lines 57-58) is from an unattested separate run. Source: `.idiom/benchmark/20260611-122151/idiom/stderr-{1,2,3}.txt`.

4. **Idiom session summaries report only prompt/completion/total tokens.** No cache hit/miss, cost, currency, or reasoning token fields. Source: each trial's `session_summary` JSONL event.

5. **Reasonix exposes cache_hit_tokens, cache_miss_tokens, cost (¥), and reasoning token counts** (13-74 reasoning tokens per final answer call). Source: `.idiom/benchmark/20260611-122151/reasonix/metrics-{1,2,3}.json` and `stdout-{1,2,3}.txt` thinking lines.

6. **Idiom already parses `reasoning_content`** in `internal/runtime/llama/client.go:36` (chat message struct field), `client.go:165-203` (response parsing), and **hosted-chat tests explicitly strip it** from follow-up payloads (`client_test.go:326-327`).

7. **Reasonix uses bounded reads pervasively** — `limit: 50`, `limit: 60`, `limit: 80` in successive trials. Idiom trial 1 read all 1025 lines of README.md; trials 2-3 read only lines 1-30 and 1-40 after a search step.

---

### Inferences

- **The benchmark may not have run against `deepseek-v4-flash` at all.** The backends config has no such profile, and every trial's route telemetry says `deepseek-v4-pro`. If a CLI `-model` override was used, the telemetry did not capture it. The design doc's claim "Both tools used the hosted DeepSeek API with model id `deepseek-v4-flash`" is unverifiable from the provided artifact and contradicted by the route telemetry.

- **Reasoning tokens are a missing dimension in the comparison.** Reasonix emits 13-74 reasoning tokens per final answer call; Idiom may be producing reasoning tokens internally (it parses `reasoning_content`) but does not expose them in session summaries or the benchmark table. If the comparison cares about token efficiency, reasoning tokens should be counted separately from visible completion tokens.

- **The existing `reasoning_content` handling in `client.go` strips it from hosted-chat follow-up payloads.** This is the *opposite* of what DeepSeek hosted-chat requires for thinking-mode continuity (it demands the prior message's `reasoning_content` be passed back). This explains the reported error. The fix direction in the design doc (A3) is correct.

- **Prompt token comparison is asymmetric.** Idiom's 29,577 median prompt tokens vs Reasonix's 34,880 looks like Idiom is more efficient, but Reasonix's cache_hit (24k-34k per trial) means its actual *new* tokens were only 821-10,816. Without cache-adjusted metrics from Idiom, the comparison is incomplete.

---

### Root cause

**Model attribution gap:** The route telemetry pipeline records the profile alias (`deepseek-v4-pro`) as the effective model, not the actual client model id. When a model override is passed (e.g., `-model deepseek-v4-flash`), the override is used at the API layer but not reflected in route/session telemetry. This makes the benchmark artifact ambiguous as to which model served each call.

---

### Contributing factors

1. **No `deepseek-v4-flash` backend profile exists** — any Flash run must use an ad-hoc CLI override, which side-steps the profile config and creates an untracked configuration path.
2. **`reasoning_content` handling is reverse for hosted DeepSeek** — `client.go` strips it from follow-up payloads, but DeepSeek hosted-chat requires it preserved for thinking-mode continuity. The existing tests (`client_test.go:326-327`) explicitly assert the stripping behavior, which is correct for local backends but wrong for DeepSeek.
3. **Usage schema does not include cache/cost/reasoning token fields** — the `session_summary` event has only `prompt_tokens`, `completion_tokens`, `total_tokens`.
4. **Reasonix stdout output embeds token and cost telemetry in the visible agent trace** — Idiom has no equivalent inline token visibility for operator inspection.

---

### Non-causes

- **Not a "model quality" issue.** The benchmark task is too simple (single-sentence answer from README.md) to assess output quality differences between `deepseek-chat` and `deepseek-v4-flash`.
- **Not a tool-call transport issue.** All 3 Idiom trials completed without errors, recovery nudges, or malformed tool calls.
- **Not a harness state or gate failure.** The read-only direct-answer path routed correctly, completed, and produced acceptable answers in all 3 trials.

---

### Fix recommendation

Split the concerns across design atoms as proposed, with these runtime-specific notes:

1. **A3 (protocol repair):** The fix must distinguish **"preserve reasoning_content for hosted DeepSeek"** from **"strip reasoning_content for local backends."** The existing `client.go` already has the `ReasoningContent` parsing infrastructure — the repair is in the history-construction path, not the response-parsing path. Add a backend capability check (`requiresReasoningContentPreservation()`) at the client boundary. Do not touch the response-parsing code. Update `client_test.go` to add a DeepSeek-hosted variant that asserts preservation.

2. **A4 (model telemetry):** The route decision schema must carry both `profile_alias` (e.g., `deepseek-v4-pro`) and `effective_model` (e.g., `deepseek-v4-flash` or `deepseek-chat`). The `session_summary` should also report `effective_model` so benchmark JSONL can unambiguously answer "which model ran this call."

3. **A5 (cache/cost metrics):** The session summary schema needs additive optional fields: `cache_hit_tokens`, `cache_miss_tokens`, `reasoning_tokens`, `cost`, `currency`. Provider-specific mapping should live in a `usage_adapter` at the client layer, not in every consumer. Reasonix's structure is a good reference.

4. **Benchmark reproducibility:** Before implementation, either (a) add a `deepseek-v4-flash` profile to `backends.json` and re-run the baseline, or (b) capture the exact `-model` CLI invocation so the artifact is reproducible. Without this, post-fix comparison against a different model undermines the success metric.

---

### Regression tests

- `go test ./internal/runtime/llama` — must pass after reasoning_content history repair (DeepSeek variant vs local-backend variant).
- `go test ./internal/runtime/agent -run 'Reasoning'` — existing reasoning salvage tests must not regress.
- New focused tests:
  - `TestHostedDeepSeekPreservesReasoningContent` — follow-up request includes prior `reasoning_content`.
  - `TestLocalBackendStripsReasoningContent` — existing behavior unchanged.
  - `TestSessionSummaryCacheCostFields` — optional fields parse and aggregate, missing fields are zero/unknown.
  - `TestRouteTelemetryEffectiveModel` — profile alias and effective model id are both recorded.

---

### North-star risk

**Low.** The design is north-star aligned: it preserves strategy ownership (prefetch is host-owned setup, not answer synthesis), keeps gates as invariants (citation classifier distinguishes evidence from examples), and avoids prompt-text authority (optimizations live in typed runtime state, telemetry, and acceptance logic).

The main risk is **A6 (lightweight direct-answer surface)** — it could accidentally narrow the tool surface so far that legitimate read-only investigation breaks. The mitigation (keep full tooling available for broad questions) is correct but needs a clear typed trigger rather than heuristic string matching.

---

### Confidence

**High** on the model attribution gap (verified in all 3 stdout JSONL and backends.json).
**High** on the cache/cost gap (verified in session_summary vs Reasonix metrics).
**Medium** on the `reasoning_content` error claim — the error is plausible and consistent with known DeepSeek API behavior, but no artifact for the failing trial exists in this benchmark. The existing code behavior (stripping reasoning_content) confirms the failure mode is real.
**Medium** on whether the benchmark actually ran against Flash — no evidence confirms or refutes the CLI override claim.

---

### Decision

**PASS_WITH_RISKS**

**Risks to record:**
1. **Model attribution in the benchmark is unresolved** — the design doc's claim ("both used `deepseek-v4-flash`") is not supported by the provided artifact. If the pre-fix baseline used `deepseek-chat` and the post-fix replay uses `deepseek-v4-flash`, any token/cost improvement could be model-differences, not harness-fix improvements. **Mitigation:** Pin the profile before starting A3/A4 implementation, or document the ambiguity explicitly in the benchmark artifact.
2. **Existing reasoning_content stripping is correct for local backends but wrong for DeepSeek** — the A3 fix must not break the local-backend path. The current test (`client_test.go:326-327`) must remain as the local-backend assertion, with a new DeepSeek-variant added.
3. **No automated benchmark replay fixture exists** — the success metrics (A4: "benchmark JSONL reports deepseek-v4-flash") depend on re-running the benchmark, which is currently a manual step. Consider making it a `go test` replayable fixture.
  · 45138 tok · in 42205 (39936 cached / 2269 new) · out 2933 (552 reasoning) · ¥0.0089
