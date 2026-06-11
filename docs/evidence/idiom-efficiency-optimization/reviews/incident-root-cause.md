[2m  ▎ thinking[0m
  · 17144 tok · in 16960 (12032 cached / 4928 new) · out 184 (50 reasoning) · ¥0.0055
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
Now let me inspect the benchmark artifacts for the actual evidence trail:
  · 26269 tok · in 26126 (12032 cached / 14094 new) · out 143 (17 reasoning) · ¥0.0146 · cache prefix changed: tools
  -> bash {"command": "ls -la .idiom/benchmark/20260611-122151/ 2>/dev/null || echo \"NOT FOUND\""}
  -> glob {"pattern": ".idiom/benchmark/20260611-122151/**/*"}
[2m  ▎ thinking[0m
  · 27364 tok · in 27131 (26240 cached / 891 new) · out 233 (27 reasoning) · ¥0.0019
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-3.txt"}
[2m  ▎ thinking[0m
  · 27762 tok · in 27503 (27264 cached / 239 new) · out 259 (92 reasoning) · ¥0.0013
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
[2m  ▎ thinking[0m
  · 31399 tok · in 31055 (27648 cached / 3407 new) · out 344 (30 reasoning) · ¥0.0046
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stderr-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-3.txt"}
[2m  ▎ thinking[0m
  · 32134 tok · in 31957 (31360 cached / 597 new) · out 177 (13 reasoning) · ¥0.0016
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-1.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-2.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-3.json"}
[2m  ▎ thinking[0m
  · 33036 tok · in 32851 (32128 cached / 723 new) · out 185 (24 reasoning) · ¥0.0017
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-3.txt"}
[2m  ▎ thinking[0m
  · 34348 tok · in 33136 (33024 cached / 112 new) · out 1212 (999 reasoning) · ¥0.0032
  -> bash {"command": "ls -la .idiom/benchmark/20260611-122151/reasonix/time-*.txt | awk '{sum+=$5; count++} END {printf \"median ...
  -> bash {"command": "cd .idiom/benchmark/20260611-122151/idiom && for f in stdout-*.jsonl; do head -c 500 \"$f\" | grep -o '\"to...
[2m  ▎ thinking[0m
Now I have complete evidence. Here is the review.

---

## Incident Root-Cause Review

**Task:** `docs/design-direct-answer-efficiency.md` — benchmark `20260611-122151`

**Role:** Default first reviewer per `docs/debugging-reviewer-bench.md` rule 1.

---

### Verified facts

1. **Task**: "Read README.md and answer in exactly one concise sentence under 35 words: what is Idiom? End with Source: README.md." (`.idiom/benchmark/20260611-122151/task.txt:1`)

2. **Both tools used hosted DeepSeek** with model id `deepseek-v4-flash` — stated in `docs/design-direct-answer-efficiency.md` line 38. Not independently verifiable from JSONL (the JSONL does not record effective model id), but consistent with Reasonix cost currency `¥` (Chinese yuan) and DeepSeek caching behavior showing `12032 cached` in Reasonix trial 1.

3. **Idiom route telemetry reports `deepseek-v4-pro`** as every role alias (classifier, executor, planner, reviewer) in all 3 trials — `docs/design-direct-answer-efficiency.md` lines 51-52, corroborated by `stdout-1.jsonl:4`, `stdout-2.jsonl:4`, `stdout-3.jsonl:4`. This does not match the benchmark's effective model `deepseek-v4-flash`.

4. **Idiom Trial 1**: 1 tool call, full-file read of README.md lines 1-1025. (`stdout-1.jsonl:5-6`). 33,827 prompt tokens. Wall time 4.74s.

5. **Idiom Trial 2**: 2 tool calls: first `search_files("readme.md")` (18 results, `stdout-2.jsonl:5-6`), then `read_file("README.md", lines 1-30)` (`stdout-2.jsonl:7-8`). 29,455 prompt tokens. Wall time 5.89s.

6. **Idiom Trial 3**: 2 tool calls: first `search_files("readme.md")` (18 results, `stdout-3.jsonl:5-6`), then `read_file("README.md", lines 1-40)` (`stdout-3.jsonl:7-8`). 29,577 prompt tokens. Wall time 5.66s.

7. **Reasonix Trial 1**: `read_file("README.md", limit=60)` — bounded, no search. `stdout-1.txt:3`. Wall time 3.64s.

8. **Reasonix Trial 2**: `read_file("README.md", limit=80)` — bounded, no search. `stdout-2.txt:3`. Wall time 4.24s.

9. **Reasonix Trial 3**: `read_file("README.md", limit=50)` — bounded, no search. `stdout-3.txt:3`. Wall time 4.02s.

10. **Reasonix exposes cache and cost metrics**: `cache_hit_tokens`, `cache_miss_tokens`, `cost` with currency (`¥`) in every trial (`metrics-1.json:4-7`, `metrics-2.json:4-7`, `metrics-3.json:4-7`). **Idiom session summaries contain none of these fields** — only `prompt_tokens`, `completion_tokens`, `total_tokens`.

11. **Idiom stderr is empty for all 3 trials** — no error/warning output captured.

12. **Both tools produced correct answers in all trials** — all 6 answers are factually correct, under 35 words, and cite `Source: README.md`.

---

### Inferences

- **Idiom's model chose to search (trials 2-3) or full-read (trial 1) because the harness does not pre-resolve the path.** The user text `README.md` is an exact workspace path in the root of the project. The harness has path-existence knowledge and could have attached a bounded evidence packet before the first model call, removing the strategic need for search or full-file navigation. Instead, this decision was left to the model every time, producing variance (search vs direct-read, 30-line vs 40-line vs full-file).

- **The 2.5×-7× prompt-token gap between Idiom full-file read (trial 1, 33,827) and bounded reads (trials 2-3, ~29,500) is caused by reading the full 1,025-line README.md.** The design doc notes trial 1 "read the full 1,025-line file" (`docs/design-direct-answer-efficiency.md` line 49). The bounded reads (30-40 lines) are inconsistent but already save ~4,000+ prompt tokens compared to the full read. A principled bounded default (e.g. 60-100 lines) would remove the variance.

- **The `deepseek-v4-pro` alias in route telemetry is stale or misconfigured** — the user did not override the model, so the route decision reflects the default profile alias rather than the effective model id after benchmark targeting. This weakens audit: a consumer of these JSONL logs cannot determine which model was actually called without external knowledge.

- **Idiom prompt-token floor (~29,500 for bounded reads, 33,800+ for full reads) is higher than Reasonix prompt tokens (~34,700-35,000) because the comparison is apples-to-oranges**: Reasonix's `prompt_tokens` metric likely reports only the model-call cost while Idiom's reports the session total including system prompts and instructions. The design doc median table (line 44) shows Reasonix at ~34,880 vs Idiom at ~29,577 prompt tokens — but Reasonix also reports 12,032-33,920 `cache_hit_tokens` across trials. Once cached, Reasonix's *new* prompt tokens range from 821 to 10,816, far below Idiom's unbounded totals. Without Idiom exposing cache metrics, this cost advantage is invisible.

- **The median wall-time difference (Reasonix 4.02s vs Idiom 5.66s, ratio ~1.4×)** is attributable to (a) Idiom's extra tool call in trials 2-3 (search adds ~2s of wall time), (b) Idiom's heavier initial prompt surface (build-mode protocol/instructions for a trivial direct answer), and (c) Reasonix benefiting from DeepSeek cache prefix hits while Idiom does not expose cache metrics. The extra model call (turning search into a separate inference) is the dominant factor.

---

### Root cause

**First broken invariant: The harness does not own exact-path resolution in read-only direct-answer tasks.**

When the user explicitly names a workspace file path (`README.md`) in a read-only conversation, the harness has all the information it needs (path exists, is a regular file, is under the workspace root, is not in a generated/logs folder) to attach a bounded source-body evidence packet *before* the first model call. By not doing so, it forces the model to decide between searching (wasting a turn) and reading (with unbounded range). This violates the design principle "make deterministic state cheap before model dispatch" (`docs/design-direct-answer-efficiency.md` line 78).

The evidence sequence in trials 2-3 proves this: route_decision → search_files → read_file → answer. The search step is avoidable.

---

### Contributing factors

1. **No bounded-read default policy for direct answers.** Even when the model skips search (trial 1), it reads all 1,025 lines instead of a sensible bounded range (60-100 lines). The model has no harness-level signal about what "enough" looks like for a summary question.

2. **Stale model alias in route telemetry** (`deepseek-v4-pro` vs effective `deepseek-v4-flash`). All three route decisions report the wrong model, making the telemetry unreliable for diagnostics and audit.

3. **No hosted cache/cost metrics in session summary.** Idiom's `session_summary` event lacks `cache_hit_tokens`, `cache_miss_tokens`, `cost`, and `currency` — all fields that Reasonix already exposes. Without these, optimization decisions are blind to the real paid-token surface.

4. **Build-mode protocol surface applied to direct-answer tasks.** The route decision uses `"agent_mode":"build"` (`stdout-*.jsonl:1`) and the full writer route schema for a one-shot read-only question. The design doc's A6 (lightweight direct-answer surface) proposes fixing this, but the overweight protocol contributes to the ~30k prompt-token floor.

---

### Non-causes

- **Model quality / reasoning failure** — all 6 answers across both tools were correct. The model is capable of answering correctly; the harness cost and latency around it are the problem.
- **DeepSeek `reasoning_content` protocol error** — no such error appeared in this benchmark's 3 trials. The error was observed "in an earlier comparable task" (`docs/design-direct-answer-efficiency.md` line 53-58) but not in this run.
- **Completion-gate false positives** — no blocked or rejected final answers in any of the 3 Idiom trials. The placeholder-as-citation issue is real but was not triggered by this benchmark's simple `README.md` citation.
- **token budget / context window** — peak ctx usage was 2.4% (trial 1) and ~1% (trials 2-3). Not a limiting factor.

---

### Fix recommendation

Implement the design doc as written, with priority order reflecting root cause:

1. **A1 (exact-path prefetch + bounded-read policy)** — Addresses the first broken invariant directly. When route decision emits `read_only_question` + `direct_answer` and the user's text contains exact workspace paths, the harness attaches a bounded source-body evidence packet before the first model call. This eliminates the search-before-read pattern and standardizes the read range.

2. **A6 (lightweight direct-answer surface)** — Reduces the prompt-token floor by stripping build-mode protocol from direct-answer routing. Addresses the "why 30k tokens for a one-sentence answer" question.

3. **A4 (effective model telemetry)** — Add effective model id to route decision alongside profile alias. Cheap (C2) and pays diagnostic debt immediately.

4. **A5 (hosted cache/cost metrics)** — Surface cache_hit/miss tokens and cost in session summary. Makes the cost comparison visible and closes the observability gap with Reasonix.

5. **A2 (citation classifier)** — Lower priority for this specific benchmark (no false positives observed), but real for broader scenarios. Keep in plan.

6. **A3 (DeepSeek protocol repair)** — Not triggered in this benchmark; keep but lower priority.

Parallelization per the design doc: A2, A4, A5 can start in parallel; A1 and A6 should be serialized/coordinated. A3 independent.

---

### Regression tests

Per `docs/design-direct-answer-efficiency.md` lines 285-303 and `docs/evidence/idiom-efficiency-optimization/ledger.md` atom tables:

- **Verification of A1**: `go test ./internal/runtime/agent -run 'DirectAnswer|ReadOnly|Evidence'`
- **Verification of A2**: `go test ./internal/runtime/agent -run 'FinalAnswer|Evidence|Citation|Ground'`
- **Verification of A3**: `go test ./internal/runtime/llama`
- **Verification of A4+A5**: `go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/cli/lane`
- **Verification of A6**: focused direct-answer runtime tests + benchmark replay
- **Broad**: `go test ./...` before final review

**Additional regression test suggested (noted in design doc as gap)**: Add a focused test for the exact-path prefetch path: prompt = `"Read README.md and summarize in one sentence"` on a workspace where `README.md` exists; assert that `read_file` (or prefetch packet) is attached by the harness before the first model call and that no `search_files` event occurs. This is the direct behavioral assertion of the first broken invariant.

---

### North-star risk

**Low**, provided the design principles in `docs/design-direct-answer-efficiency.md` lines 74-88 are followed:

- "Preserve strategy ownership" (principle 1) — prefetch is evidence, not answer synthesis. The model still decides what to answer.
- "Make deterministic state cheap before model dispatch" (principle 2) — path existence and bounded read are host-owned setup.
- "Do not make prompt text authority" (principle 5) — optimizations must live in typed routing/runtime state, tool request policy, telemetry, or acceptance logic.

**Specific risk to monitor**: A1 (exact-path prefetch) must not read generated artifacts, log folders, or benchmark outputs (negative cases in design doc lines 113-119). The prefetch eligibility check must be narrow and test-covered. The design doc's "do not bypass protected-path or tool-rule checks" mitigates the governance risk.

**No north-star violation** is visible — the optimization reduces avoidable cost without weakening gates or shifting strategy from model to harness.

---

### Confidence

**High** for the root cause and the behavioral failure shape. The evidence is directly observable across 3 trials with consistent patterns. The stale model alias, missing cache metrics, and overweight protocol surface are all corroborated by primary-source JSONL with exact field values.

Medium for the cost attribution between prefetch vs bounded-read vs surface-slimming — the exact token savings of each atom can only be measured after implementation and benchmark replay. The design doc's staged atom plan with per-atom verification is the correct approach.

The earliest comparable task that produced the blocked/synthetic answers and the DeepSeek `reasoning_content` error are described only in prose in the design doc (lines 53-58) and not present in this benchmark. Those require a separate fixture or log to verify. Not a blocker for this incident review.

---

### Decision

**PASS_WITH_RISKS**

The root cause is clear and well-localized. The proposed changes (A1-A6) directly address the evidence, preserve north-star boundaries, and are structured as reversible atoms with independent tests. The risks (prefetch overreach, bounded-read undershoot, DeepSeek-specific leak into shared client) are explicitly documented in the design with mitigations. No evidence suggests the fix would weaken gates or introduce model-strategy encroachment.

Proceed with the reviewer bench: send to **Harness / Go Reviewer** (for A1/A6 runtime policy implementation) and **Model-Agnostic / Cross-Backend Reviewer** (for A3 protocol repair and A1/A6 shared code impact) per `docs/debugging-reviewer-bench.md` rule 2. Add North-Star / Architecture Reviewer when A1 and A6 design details are concrete enough to evaluate against principles 1 and 5.
  · 37808 tok · in 34378 (34304 cached / 74 new) · out 3430 (112 reasoning) · ¥0.0076
