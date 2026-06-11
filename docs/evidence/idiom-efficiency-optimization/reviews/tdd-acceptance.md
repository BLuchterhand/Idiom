[2m  ▎ thinking[0m
  · 17154 tok · in 16962 (12032 cached / 4930 new) · out 192 (58 reasoning) · ¥0.0056
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
  · 26220 tok · in 26136 (12032 cached / 14104 new) · out 84 (12 reasoning) · ¥0.0145 · cache prefix changed: tools
  -> ls {"path": ".idiom/benchmark/20260611-122151", "recursive": true}
[2m  ▎ thinking[0m
  · 26822 tok · in 26453 (26112 cached / 341 new) · out 369 (16 reasoning) · ¥0.0016
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-3.txt"}
[2m  ▎ thinking[0m
  · 27537 tok · in 27057 (26752 cached / 305 new) · out 480 (13 reasoning) · ¥0.0018
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-1.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-2.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-3.json"}
[2m  ▎ thinking[0m
Now let me verify one exact source claim — whether `final_answer_acceptance.go` exists as stated.
  · 34714 tok · in 32067 (27520 cached / 4547 new) · out 2647 (2576 reasoning) · ¥0.0104
  -> glob {"pattern": "**/final_answer_acceptance.go"}
[2m  ▎ thinking[0m
Good — file exists. Now I have sufficient evidence. Here is the TDD/Acceptance review:

---

## TDD / Acceptance Reviewer — `idiom-efficiency-optimization`

**Source design:** `docs/design-direct-answer-efficiency.md`
**Ledger:** `docs/evidence/idiom-efficiency-optimization/ledger.md` (status: pre-implementation, all reviewers pending)
**Benchmark:** `.idiom/benchmark/20260611-122151/`

---

### Verified facts

1. **Benchmark median-per-metric values in the design doc table match raw data exactly.**
   - Idiom wall 5.66s = median of [4.74, 5.89, 5.66] → correct.
   - Idiom prompt 29,577 = median of [33,827, 29,455, 29,577] → correct.
   - Idiom total 29,735 = median of [33,912, 29,619, 29,735] → correct.
   - Reasonix prompt 34,880 = median of [34,880, 35,090, 34,741] → correct.
   - Reasonix total 35,009 = median of [35,009, 35,235, 34,927] → correct.
   - Source: `idiom/stdout-[123].jsonl` lines 9 and 11 (session_summary events), `reasonix/stdout-[123].txt` lines showing token counts.

2. **Idiom trial 1 read the full 1,025-line README.md without search.** All 3 Idiom trials show `deepseek-v4-pro` in route telemetry despite the benchmark targeting DeepSeek Flash. Source: `idiom/stdout-1.jsonl` lines 5-6 (read_file 1-1025); all three `idiom/stdout-*.jsonl` line 4 (`writer_route_decision` contains `classifier_model: "deepseek-v4-pro"`).

3. **Idiom trials 2 and 3 searched first (`search_files`), then read bounded ranges (1-30 and 1-40).** This is the avoidable-search pattern the design targets. Source: `idiom/stdout-2.jsonl` lines 5-7; `idiom/stdout-3.jsonl` lines 5-7.

4. **Reasonix exposes `cache_hit_tokens`, `cache_miss_tokens`, `cost`, and `currency`; Idiom does not.** Reasonix metrics JSON includes all four fields per trial. Idiom session summaries report only `prompt_tokens`, `completion_tokens`, `total_tokens`, and `model_calls`. Source: `reasonix/metrics-[123].json` vs. `idiom/stdout-[123].jsonl` line 11.

5. **The `reasoning_content` error and the command-placeholder citation failure are referenced as "earlier comparable task" evidence** — no artifacts for those failures exist in `.idiom/benchmark/20260611-122151/`. The design doc acknowledges this: "not a product-quality eval suite."

6. **`final_answer_acceptance.go` exists** at `internal/runtime/agent/final_answer_acceptance.go`, consistent with the context packet's localization.

### Inferences

- The avoidable-search pattern (trials 2 and 3) and full-file read (trial 1) are **not** flukes — they occur across 3/3 trials (one full-read, two search-then-bounded-read). The model is not reliably routing "read this exact path" to a direct `read_file` call without first searching.
- The stale telemetry alias (`deepseek-v4-pro` vs. the effective model) appears in **all** 3 trials, meaning the model override path is not propagating to route telemetry at all. This is likely a single missing field assignment, not a race or intermittent issue.
- Reasonix's 2-3× cache hit ratio (trial 3: 33,920 cached / 821 missed) vs. Idiom's lack of cache metrics suggests the design doc's proposal (A5) is justified but the magnitude of savings from cache alone may vary by provider state and prompt shape.

### Root cause

The design does not define a failure that has *already occurred in production* — this is a pre-implementation review for a planned optimization program, not a post-mortem. The closest root cause for the observed benchmark inefficiencies:

- **Idiom's direct-answer evidence path has no host-owned exact-path pre-resolution.** The model must discover `README.md` via tool calls (search or read) before it can read evidence, adding 1-2 unnecessary turns.
- **Route telemetry does not capture the effective model id after `-model` override**, leaving `deepseek-v4-pro` as the stale default in all benchmark runs.
- **Reasonix's hosted usage schema captures cache and cost at the API boundary; Idiom's does not**, making cost-per-benchmark comparisons impossible without external billing data.

### Contributing factors

- The benchmark is small (3 trials) and not a statistically rigorous eval suite — the design doc is honest about this.
- The "earlier comparable task" failures (reasoning_content, placeholder citations) lack replay artifacts in this benchmark, so their reproducibility cannot be independently verified from this evidence.
- The design splits work into 6 atoms but A1 (prefetch+bounded read) and A6 (lightweight surface) are tightly coupled because both modify the direct-answer runtime surface. The ledger correctly flags this as serialized/coordinated, but the `expected_files` overlap: A1 lists `internal/runtime/agent/*read_only*` and A6 lists `internal/runtime/agent/**` — guaranteed collision.

### Non-causes

- Not a model quality issue — both tools used the same DeepSeek Flash endpoint. The benchmark shows Idiom uses *fewer* total tokens (29,735 vs 35,009 median) but takes longer wall time (5.66s vs 4.02s), suggesting the extra tool calls and full-file read dominate wall time, not model latency.
- Not a prompt-guidance problem per se — the model *can* read README.md directly (trial 1), it just doesn't do so reliably. Adding more prompt reminders would be the wrong fix per north-star principle 5.
- Not a completion-gate false positive — all 3 trials produced valid final answers with correct source citation. The placeholder-citation failure is from an earlier, separate run.

### Fix recommendation

Accept the design as a pre-implementation plan with these TDD-specific observations:

1. **A1 acceptance criteria need a sharper success threshold.** "One model call plus zero or one tool call equivalent" is the right target. But the negative cases should include a fixture where the path does not exist — ensure the prefetch does not block or stall on missing paths.

2. **A2 (citation classifier) is the most likely atom to regress at acceptance time** because distinguishing "evidence citation" from "command example" requires parsing path-like strings in prose. The ledger requires this check in the *same* file as the existing acceptance logic (`final_answer_acceptance.go`), which is correct — do not split it into a separate utility that can drift.

3. **A1 and A6 must share a test fixture for benchmark replay** because both affect the direct-answer runtime surface. The ledger's "serialized or coordinated" note is correct but insufficient — recommend a shared *integration* replay test that runs both atoms' changes together before either is considered complete.

4. **A3 (DeepSeek protocol repair) has no replay artifact in this benchmark.** The design should specify a synthetic fixture that reproduces the `reasoning_content` error, or at least document the expected DeepSeek API behavior and the condition under which the error fires. Without a reproducible fixture, this atom has no acceptance oracle.

5. **A5 (cache/cost metrics) should share a test with A4 (model telemetry)** because the session summary JSONL is the single output both atoms extend. The ledger has them as separate required test commands but the actual changes likely touch overlapping structs in the usage/aggregation path.

### Regression tests

Per atom (`go test` commands match the ledger):

| Atom | Primary test command | Gap |
|------|-------------------|-----|
| A1 | `go test ./internal/runtime/agent -run 'DirectAnswer\|ReadOnly\|Evidence'` | Should add a negative-case test for non-existent/excluded paths |
| A2 | `go test ./internal/runtime/agent -run 'FinalAnswer\|Evidence\|Citation\|Ground'` | Should test that a command example like `path/to/brief.md` in README text does not fail a grounded answer |
| A3 | `go test ./internal/runtime/llama` | Needs a synthetic fixture reproducing `reasoning_content` error — not present in current benchmark |
| A4 | focused route/runtime telemetry tests | Test that `-model` override value appears in `writer_route_decision.effective_model` (or equivalent) |
| A5 | `go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/cli/lane` | Should verify session_summary aggregates cache/cost fields when present, omits gracefully when absent |
| A6 | focused direct-answer runtime tests + benchmark replay | The benchmark replay must compare against `.idiom/benchmark/20260611-122151` per the design doc |

Broad regression: `go test ./...` before final review (same as design doc).

### North-star risk

**Low to moderate.** The design explicitly follows north-star principles:
- Principle 1 (preserve strategy ownership) — prefetch is host-owned setup, not answer synthesis. ✓
- Principle 3 (gates are invariants, not friction) — citation classifier distinguishes real from spurious. ✓
- Principle 5 (no prompt text authority) — all optimizations are typed runtime/routing. ✓

The risk is primarily executional: **A1 overreach** (prefetching generated artifacts or outside the workspace) could violate the "harness owns contracts" boundary. The design's negative cases are well-stated but depend on the implementation being as disciplined as the spec.

### Confidence

**High for the benchmark evidence — 95%.** The raw data matches the design doc's summary, the patterns (avoidable search, stale telemetry, missing cache fields) are reproducible across all 3 trials, and the source path `final_answer_acceptance.go` exists where the context packet predicted it.

**Moderate for the "earlier comparable task" claims — 60%.** The `reasoning_content` error and placeholder-citation failure are not present in the benchmark artifact and cannot be independently replayed without separate evidence.

**Moderate for atom readiness — 70%.** The atoms are well-defined but A1↔A6 overlap is under-resolved, A3 lacks a replay fixture, and the required test commands are broad package patterns that may catch integration issues late.

---

### Decision: PASS_WITH_RISKS

**Rationale:** The design is evidence-grounded, north-star aligned, and the atom decomposition is sensible. Pass with three conditions:

1. **A1 and A6 must share an integration replay test** before either is marked complete — the direct-answer runtime surface is one system.
2. **A3 must define a synthetic fixture** for the `reasoning_content` protocol error, or explicitly accept that verification depends on live DeepSeek API behavior (and document that acceptance gap).
3. **The ledger's `pre-implementation review pending` status must advance** to reflect this review and the other pending reviewers before any atom moves to implementation.
  · 37431 tok · in 34749 (34688 cached / 61 new) · out 2682 (153 reasoning) · ¥0.0061
