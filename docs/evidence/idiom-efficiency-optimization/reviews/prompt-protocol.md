[2m  ▎ thinking[0m
  · 17110 tok · in 16958 (12032 cached / 4926 new) · out 152 (18 reasoning) · ¥0.0055
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
[2m  ▎ thinking[0m
  · 26173 tok · in 26092 (12032 cached / 14060 new) · out 81 (26 reasoning) · ¥0.0145 · cache prefix changed: tools
  -> ls {"path": ".idiom/benchmark/20260611-122151"}
[2m  ▎ thinking[0m
  · 26358 tok · in 26196 (26112 cached / 84 new) · out 162 (15 reasoning) · ¥0.0009
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> ls {"path": ".idiom/benchmark/20260611-122151/idiom"}
  -> ls {"path": ".idiom/benchmark/20260611-122151/reasonix"}
[2m  ▎ thinking[0m
  · 27055 tok · in 26582 (26240 cached / 342 new) · out 473 (12 reasoning) · ¥0.0018
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-1.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-2.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-3.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-3.txt"}
[2m  ▎ thinking[0m
  · 28717 tok · in 28392 (27008 cached / 1384 new) · out 325 (11 reasoning) · ¥0.0026
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-3.txt"}
[2m  ▎ thinking[0m
Now I have all the evidence I need. Let me synthesize the Prompt / Protocol review.

---

**Prompt / Protocol Reviewer — Efficiency Optimization Design**

## Verified facts

1. **Route-decision schema** (`idiom.writer_route.v1`) in all three Idiom trials reports every role alias as `deepseek-v4-pro` — but the benchmark's active config targeted `deepseek-v4-flash`. The protocol schema has no field for the *effective* resolved model; it only carries the profile-level alias.
   - Source: `.idiom/benchmark/20260611-122151/idiom/stdout-{1,2,3}.jsonl` line 4 (writer_route_decision), compared against `docs/design-direct-answer-efficiency.md` § Evidence Snapshot ("model id `deepseek-v4-flash`").

2. **Contract and writer mode are correct**: all three trials emit `contract=direct_answer`, `task_family=conversation`, `writer_mode=read_only`. The route correctly classifies this as `route.read_only_question`. The model receives the right surface category.
   - Source: same route-decision events.

3. **Tool-sequence variance**: Trial 1 reads `README.md` directly (full 1025 lines); trials 2 and 3 call `search_files` first, then read bounded ranges (lines 1-30 and 1-40). The model chose `search_files` in 2/3 trials even though the user named the exact path.
   - Source: tool_request events in `stdout-{2,3}.jsonl` step 1 = `search_files`, step 2 = `read_file`.

4. **Session summary protocol lacks cache/cost/reasoning fields**: Idiom's `session_summary` emits `prompt_tokens`, `completion_tokens`, `total_tokens`, `model_calls`, `tool_calls`, `duration_ms`, `ctx_size`, `peak_ctx_pct`. It does **not** emit `cache_hit_tokens`, `cache_miss_tokens`, `reasoning_tokens`, or `cost`. Reasonix's metrics include all of these plus currency.
   - Source: session_summary lines in all three `stdout-*.jsonl` vs Reasonix `metrics-*.json` from `.idiom/benchmark/20260611-122151/reasonix/`.

5. **No completion-gate false positives in this benchmark**: All three trials produced grounded answers with correct `Source: README.md` citations. The earlier false-positive scenario (command/example paths treating `path/to/brief.md` as unsupported evidence) is not present in these runs.
   - Source: `stdout-{1,2,3}.jsonl` final messages; design doc § Proposed Changes 4 refers to an earlier comparable task not present in this benchmark.

## Inferences

1. The model's choice to `search_files` in 2/3 trials is not a protocol defect *per se* — the protocol correctly offers both `search_files` and `read_file` tools. However, the protocol surface does not give the model a reason to prefer direct read over search for exact user-named paths. **A host-owned prefetch (A1) is the protocol-level fix**, not a prompt nudge.
2. The missing `effective_model` field in the route schema is a protocol contract gap. Downstream consumers (benchmark, audit, telemetry) cannot unambiguously answer which model served the call without cross-referencing external config. The schema `idiom.writer_route.v1` should carry an `effective_model` alongside the existing role aliases.
3. The `build` agent_mode with full tool surface (including edit/write tools even when `writer_mode=read_only`) is the likely cause of the ~30k prompt-token floor. The protocol does not strip mutation tools or protocol text for direct-answer tasks — this is A6's scope.

## Root cause

**The model-facing protocol for direct-answer read-only tasks runs the same full `build`-mode surface, with the same tool schema, route schema, and protocol text as mutation lanes.** The only differentiation is the `writer_mode=read_only` and `contract=direct_answer` labels. Without a compiled lightweight surface, the model still sees edit/write tools, planner/executor protocol text, and mutation-lane terminal state definitions — inflating the prompt-token floor unnecessarily. Separately, the route-decision schema conflates profile alias with effective model identity.

## Contributing factors

- **No exact-path prefetch (A1 gap)**: The harness passes the user's exact path mention to the model as a prose instruction, but does not pre-resolve the evidence. The model spends a tool call on `search_files` or full-file reads that the harness could have short-circuited.
- **No bounded-read defaults (A1 gap)**: The model in Trial 1 reads all 1025 lines. A bounded-read default for "what is Idiom? / summary / definition" prompts would match the 60-100 line policy in the design doc.
- **Lack of cache/cost fields in session_summary**: The protocol does not expose provider-level efficiency metrics, making benchmark cost/cache analysis impossible without external instrumentation.
- **Profile alias vs effective model drift in route schema**: The route schema was presumably populated from a profile named after `deepseek-v4-pro` even though the actual model resolved to `deepseek-v4-flash`. The protocol has no place to record this resolution.

## Non-causes

- The answer quality is good. Not a model-capability problem.
- No completion-gate false positives in this benchmark — the acceptance protocol works correctly for real evidence citations.
- No routing misclassification: the route correctly chose `read_only` and `direct_answer`.
- No DeepSeek `reasoning_content` protocol error in these three clean trials.

## Fix recommendation

**From the protocol surface perspective, the priority ordering should be:**

1. **A6 first (lightweight direct-answer surface)**, then **A1 (prefetch + bounded reads)** — because A6 defines the protocol that A1 populates. The compiled runtime surface for direct-answer should:
   - Expose only read-only tools (read_file, search, grep, code_navigate) — strip edit_file/write_file/submit_plan/plan-execute protocol text.
   - Emit a distinct `route_plan` contract type (e.g. `direct_answer` with a lightweight surface version).
   - Use a compact completion gate that only checks source evidence, not mutation obligations.
   - Remove planner/executor/reviewer role protocol from the model-facing instructions.

2. **Route schema amendment**: Add `effective_model` (the resolved model id after profile override) to `idiom.writer_route.v1` (or a new `idiom.direct_answer_route.v1` if A6 creates one). The profile alias stays for attribution tracing; the effective field is the truth for which model ran.

3. **Session summary schema addition**: Add optional `cache_hit_tokens`, `cache_miss_tokens`, `cost` (with `currency`), and `reasoning_tokens` to the session_summary event. Make them 0/nil when the provider doesn't supply them — never fail on missing.

4. **The exact-path prefetch (A1)** should be a host-side evidence preparation step *after* the route decision but *before* the first model call. This is not a protocol change — it's a runtime precondition that the protocol (the surface defined in A6) can rely on.

## Regression tests

- **Direct-answer protocol projection**: test that the model-facing instruction block for `direct_answer` does not contain `submit_plan`, `edit_file`, `write_file`, `plan_execute`, or any mutation-lane terminal state names.
- **Tool surface isolation**: test that `read_file` and `search_files` are available in direct-answer mode; `edit_file`, `write_file`, `submit_plan` are not.
- **Route schema effective_model**: test that route telemetry with a model override reports both the original alias and the effective model id.
- **Session summary cache/cost**: test that hosted provider responses with `cache_hit_tokens` / `cache_miss_tokens` / cost fields are parsed and present in the summary; missing fields result in zero values, not errors.
- **Full-file vs bounded read**: test that a `"what is X?"` prompt on a multi-hundred-line doc triggers the bounded-read policy (≤100 lines) and not a full-file request.
- **Exact-path prefetch protocol binding**: test that a direct-answer task with an explicit user-named path emits `prefetch_evidence` as a source-body event before any model tool call.

## North-star risk

Medium. A6 (lightweight surface) directly supports the north-star principle of "keep contracts precise" — stripping mutation protocol from a read-only path is more contract-correct than the current shared surface. The risk is over-slimming: if the surface is too stripped, a legitimate read-only investigation that needs a `read_file` with `limit=0` (full file) or a `grep` would fail. Mitigation: the design doc already requires full tooling available when the question is broad (§ Risks And Non-Goals, Mitigations). The test for "read-only investigation remains possible" in A6's TDD obligations must be more than a symbolic check — it should cover the actual discovery sequence.

The route schema gap (alias vs effective model) is a small but real north-star concern: "audit and evals" is part of the north star, and ambiguous model attribution undermines audit trust.

## Confidence

High (8/10). The evidence from benchmark logs and session summaries directly shows all claimed protocol behaviors. The one thin area is the "earlier comparable task" with acceptance-gate false positives — no logs are available in this benchmark to verify that failure shape. The fix recommendations follow the design doc's atom plan and north-star principles.

## Decision

**PASS_WITH_RISKS**

Primary risk: A6 slimming could remove or restrict a tool the model needs for broad read-only investigation. Mitigation is adequate if A6's TDD obligations include a concrete test for non-trivial read-only investigation sequences (e.g. grep → filter → read_file, or trace_code → follow symbol). The design doc should make this test mandatory, not optional. Secondary risk: A1 prefetch and A6 surface must be coordinated (the design doc already flags this as serial).

**Recommended reviewer ordering**: A6 → A1 → A2/A3/A4/A5 (parallel), with final integration after all atoms. The protocol surface (A6) is the foundation that A1 populates, A2 classifies against, and A4/A5 report through. Doing A3 (DeepSeek protocol repair) in parallel is safe since its boundary is `internal/runtime/llama/client.go`, disjoint from the agent-level protocol surface.
  · 35666 tok · in 32106 (28672 cached / 3434 new) · out 3560 (1209 reasoning) · ¥0.0111
