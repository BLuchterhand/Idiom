[2m  ▎ thinking[0m
I'll start by reading the key documents to understand the review scope.
  · 17211 tok · in 16968 (12032 cached / 4936 new) · out 243 (64 reasoning) · ¥0.0057
  -> read_file {"limit": 200, "path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"limit": 200, "path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
Let me check the benchmark artifacts for cross-backend evidence.
  · 24499 tok · in 24391 (13312 cached / 11079 new) · out 108 (25 reasoning) · ¥0.0116 · cache prefix changed: tools
  -> ls {"path": ".idiom/benchmark/20260611-122151", "recursive": true}
[2m  ▎ thinking[0m
  · 25030 tok · in 24732 (24448 cached / 284 new) · out 298 (38 reasoning) · ¥0.0014
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> read_file {"limit": 60, "path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"limit": 60, "path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"limit": 60, "path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
[2m  ▎ thinking[0m
Let me inspect the specific implementation files that are referenced in the design doc as likely change areas, to check for existing backend coupling.
  · 29089 tok · in 28362 (24960 cached / 3402 new) · out 727 (579 reasoning) · ¥0.0054
  -> mcp__codegraph__context {"task": "How does internal/runtime/llama/client.go handle backend-specific fields like reasoning_content? How does inte...
[2m  ▎ thinking[0m
  · 30240 tok · in 30077 (29056 cached / 1021 new) · out 163 (45 reasoning) · ¥0.0019
  -> mcp__codegraph__node {"includeCode": true, "symbol": "Tool"}
  -> mcp__codegraph__node {"includeCode": true, "symbol": "Runner"}
[2m  ▎ thinking[0m
Let me check the final answer acceptance and the source grounding logic in more detail.
  · 34667 tok · in 34531 (30208 cached / 4323 new) · out 136 (50 reasoning) · ¥0.0052
  -> read_file {"limit": 100, "path": "internal/runtime/agent/final_answer_acceptance.go"}
[2m  ▎ thinking[0m
  · 36378 tok · in 36065 (34560 cached / 1505 new) · out 313 (34 reasoning) · ¥0.0028
  -> read_file {"limit": 150, "offset": 100, "path": "internal/runtime/agent/final_answer_acceptance.go"}
  -> explore {"task": "In the Idiom repo at /Users/ben/Documents/GitHub/idiom, find and show me:\n1. The method `sourceGroundingRejec...
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go"}
  -> grep {"pattern": "acceptedAnswerEvidencePaths", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> grep {"pattern": "reasoning_content", "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "pattern": "func.*sourceGroundingRejectio...
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "pattern": "func.*emitModelCallTraceWithO...
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "offset": 19572, "limit": 80}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go", "offset": 150, "limit": 100}
  -> read_file {"offset": 8645, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "limit": 80}
  -> read_file {"offset": 6074, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "limit": 120}
  -> read_file {"offset": 1, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/final_answer_acceptance.go", "limit": 80...
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go", "pattern": "func.*parseChatResponse|func....
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go", "pattern": "type ChatMessage struct"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "pattern": "func safeModelAliasForTelemet...
  -> read_file {"limit": 30, "offset": 30, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go"}
  -> read_file {"limit": 60, "offset": 1040, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go"}
  -> read_file {"limit": 25, "offset": 6195, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go"}
  -> read_file {"limit": 30, "offset": 590, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/client.go"}
[2m  ▎ thinking[0m
  · 40191 tok · in 39261 (36352 cached / 2909 new) · out 930 (781 reasoning) · ¥0.0055
  -> explore {"task": "In the Idiom repo at /Users/ben/Documents/GitHub/idiom, find how the writer_route_decision event gets its mode...
  -> mcp__codegraph__search {"query": "writer_route_decision", "kind": "variable"}
  -> grep {"pattern": "writer_route_decision", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/workflows/planexecute/run.go", "offset": 115, "limit": 30}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/lane/lane.go", "offset": 285, "limit": 30}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/implement/implement.go", "offset": 70, "limit": 30}
  -> mcp__codegraph__search {"query": "planExecuteWriterRouteDecision"}
  -> mcp__codegraph__search {"query": "buildLaneWriterRouteDecision"}
  -> mcp__codegraph__search {"query": "BuildDecision"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/workflows/planexecute/writer_route.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/lane/lane.go", "offset": 1260, "limit": 40}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/route/writer_route.go"}
  -> mcp__codegraph__search {"kind": "type", "query": "writerroute.Decision"}
  -> mcp__codegraph__search {"kind": "function", "query": "NewDecisionFromParams"}
  -> mcp__codegraph__search {"kind": "method", "query": "EventFields", "limit": 10}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/contracts/writerroute/decision.go"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/lane", "pattern": "func cliWriterRouteRoleAliases"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/lane", "pattern": "func buildCLIWriterRouteDecision"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/lane/adapters.go"}
  ⊘ explore sub-agent: paused after 8 tool-call rounds (agent.max_steps) — the work so far is saved; send another message to continue, or set max_steps higher or to 0 for no limit
[2m  ▎ thinking[0m
  · 40335 tok · in 40250 (40064 cached / 186 new) · out 85 (21 reasoning) · ¥0.0012
  -> grep {"path": "internal", "pattern": "writer_route_decision"}
[2m  ▎ thinking[0m
  · 41824 tok · in 41744 (40320 cached / 1424 new) · out 80 (25 reasoning) · ¥0.0024
  -> read_file {"path": "internal/surfaces/tui/app/writer_route.go"}
[2m  ▎ thinking[0m
  · 43936 tok · in 43353 (41728 cached / 1625 new) · out 583 (507 reasoning) · ¥0.0036
  -> grep {"path": ".idiom/benchmark/20260611-122151/idiom", "pattern": "model_call"}
[2m  ▎ thinking[0m
  · 44603 tok · in 44412 (43904 cached / 508 new) · out 191 (116 reasoning) · ¥0.0018
  -> grep {"path": ".idiom/benchmark/20260611-122151/idiom", "pattern": "model"}
[2m  ▎ thinking[0m
Now I have sufficient evidence across all six atoms. Here is my review.

---

## Model-Agnostic / Cross-Backend Reviewer

**Design doc:** `docs/design-direct-answer-efficiency.md`
**Ledger:** `docs/evidence/idiom-efficiency-optimization/ledger.md`
**Benchmark:** `.idiom/benchmark/20260611-122151/`
**Source inspected:** `internal/runtime/llama/client.go`, `internal/runtime/agent/final_answer_acceptance.go`, `internal/runtime/agent/runner.go`, `internal/surfaces/tui/app/writer_route.go`

---

**Verified facts:**

1. **Benchmark route decision reports stale alias**. All three trials emit `writer_route_decision` with `executor_model: "deepseek-v4-pro"` and all `role_aliases` mapping to `deepseek-v4-pro`, yet the design doc states the benchmark targeted `deepseek-v4-flash`. The alias comes from `appWriterRouteRoleAliases()` (`writer_route.go:76-95`) which reads the config profile alias; `writerRouteModelForRole()` (`writer_route.go:97-119`) returns that alias directly if set, without carrying the effective API model name.

2. **No model-call trace events exist in the benchmark output**. The three trials produce only 6-7 event types each (start, message, route_decision, tool_request, tool_result, final, session_summary). The `model_calls: 3` counter is aggregated in the summary but individual `model_call` trace events — which would have carried `model_alias` — were not emitted or were filtered out. This means the benchmark cannot prove which model name was actually sent to the DeepSeek API in each call.

3. **`reasoning_content` is already parsed and stored** in `ChatMessage` (`client.go:35`, struct field `ReasoningContent`). Response parsing has a fallback chain: `reasoning_content` → `reasoning` (MLX alias). The `hostedChatMessages()` sanitizer at `client.go:598-608` **zeros out `ReasoningContent`** before sending history to hosted backends.

4. **Zeroing `ReasoningContent` in hosted chat history** is the likely trigger for the DeepSeek "The reasoning_content in the thinking mode must be passed back to the API" error. DeepSeek's hosted API contract appears to require the previous assistant's `reasoning_content` to be echoed in follow-up requests. The current code unconditionally cleans it, which would break any multi-turn DeepSeek session that produced thinking content.

5. **Source-grounding rejection** (`sourceGroundingRejectionReason`, `runner.go:8645-8710`) applies a single `sourceGroundingMentionedPaths()` extraction to all answer text. It does not distinguish evidence citations from command/example/placeholder paths. This is per the design's §4 diagnosis.

6. **No cache/cost fields** appear in any `session_summary` event or tool result in the benchmark output. The `session_summary` only carries `prompt_tokens`, `completion_tokens`, `ctx_size`, `duration_ms`, `compactions`, `model_calls`, `tool_calls`, `turns`. The DeepSeek API does supply cache hit/miss and cost metadata; Idiom simply doesn't parse or emit them.

7. **Exact-path prefetch is backend-agnostic** because it happens before any model call — it's a host-owned evidence preparation step in the runner pipeline, not a model or transport concern.

---

**Inferences:**

- The alias `deepseek-v4-pro` in the route decision is the configured profile alias from a local config or backend file, not the API model name. The actual model name `deepseek-v4-flash` was passed through the `llama.ChatOptions.Model` field to the API call. The discrepancy means anyone reading the benchmark artifact cannot determine which model was actually invoked without cross-referencing the configuration.

- The `reasoning_content` protocol error likely reproduces on *any* multi-turn DeepSeek hosted-chat session where the first response includes thinking content, because `hostedChatMessages()` unconditionally strips it before the second turn. This is not a one-off flake — it's a systematic incompatibility with DeepSeek's hosted API contract.

---

**Root cause (cross-backend perspective):**

The shared transport layer (`client.go:598-608`) applies a backend-hostile policy — stripping `ReasoningContent` — uniformly to all hosted backends instead of only to backends that require it, and without a mechanism to preserve it when the backend contract demands echoing it back.

---

**Contributing factors:**

- The telemetry layer has two incomplete model-name representations: the route decision carries the alias (which may differ from the API model), and the model-call trace (when emitted) carries `model_alias` from `opts.Model` after alias resolution. The benchmark output lacks model-call traces entirely, so neither representation is verifiable from the recorded artifact.
- The `session_summary` aggregation counter `model_calls: 3` reveals nothing about which model was called or whether it was a single model repeatedly or different overrides.
- Cache/cost metrics are simply unparsed — no provider-specific usage field extraction exists in the client response handling.

---

**Non-causes:**

- Not a model quality or prompt-quality issue — the answers themselves are correct across all three trials.
- Not a tool-transport problem (native vs textual) — all three trials completed with proper tool calls and a valid final answer.
- Not a read/write permission or lane-routing issue — all tasks were correctly classified as `read_only_question` → `conversation` → `direct_answer`.
- The full-file read in trial 1 (1025 lines), while wasteful, is a policy/scope issue specific to direct-answer prefetch defaults, not a cross-backend concern.

---

**Fix recommendation (cross-backend constraints):**

1. **A3 (Hosted DeepSeek reasoning protocol) — Conditionally preserve `ReasoningContent` instead of unconditionally stripping it.**
   The current approach (`client.go:598-608`) zeroes `ReasoningContent` for all hosted backends. Change to:
   - Preserve `ReasoningContent` in the history message when the backend is DeepSeek and the previous response included it.
   - Still strip it for backends that don't use it (local llama.cpp, MLX).
   - The shared `hostedChatMessages()` function must *not* inspect model identity — instead, the fix should pass a policy flag (e.g., `preserveReasoning bool`) from the caller, sourced from the `Policy` or `ChatOptions` layer.
   - TDD obligation "non-DeepSeek hosted/local behavior remains unchanged" must be enforced by unit tests that exercise both paths. The existing test in `client.go` area should get two new cases: DeepSeek-with-reasoning preserves it, llama.cpp-without-reasoning still strips it.

2. **A4 (Effective-model telemetry) — Add `effective_model` alongside `model_alias` in model-call traces, and ensure `session_summary` carries it.**
   The route decision (`writer_route.go`) should either:
   - Add an `effective_executor_model` / `effective_planner_model` field alongside the existing alias-based fields; or
   - Rename the current `executor_model` to `executor_alias` and add a new `executor_model` with the resolved API model name. Either way, both values must be present in the same event.
   Cross-backend constraint: this must work for every backend — local (alias == effective), hosted (alias may differ), and override (alias differs by definition).

3. **A5 (Hosted cache/cost metrics) — Parse optional usage fields from the API response, add to existing telemetry.**
   The current `chatResponse` struct (`client.go:173-188`) captures `usage` fields. Extend the usage parsing to extract optional fields like `cache_creation_input_tokens`, `cache_read_input_tokens`, provider-specific cost. Aggregate into `session_summary`. Cross-backend constraint: missing fields must be zero/omitted, not an error. The existing test suite must confirm that a mock response without these fields produces the same session summary structure it does today.

4. **A1 (Exact-path prefetch) and A2 (Citation classifier) and A6 (Lightweight surface) — No cross-backend concerns identified.**
   A1 is entirely pre-model-call host-owned logic. A2 is text processing in `final_answer_acceptance.go` — it runs after the model responds and does not vary by backend. A6 is a compiled protocol projection; must be verified for both native-tool and textual-tool backends during implementation but contains no backend-specific assumptions in the design.

---

**Regression tests:**

| Atom | Required test | Cross-backend guard |
|------|--------------|---------------------|
| A3 | `go test ./internal/runtime/llama -run 'Reasoning|Hosted|DeepSeek'` | Two cases: DeepSeek-with-reasoning preserves; llama.cpp/MLX strips |
| A4 | Focused route/runtime telemetry test | Assert both alias and effective model appear in JSON, not just alias |
| A5 | `go test ./internal/runtime/llama ./internal/runtime/agent` | Mock response without cache/cost fields → no parse error, zero values |
| A1+A2+A6 | Existing DirectAnswer/Evidence/Citation tests | Already backend-neutral; just confirm no regressions |

---

**North-star risk:**

**Low to moderate.** The six atoms cleanly separate backend-specific concerns (A3, A5) from backend-neutral ones (A1, A2, A6), with A4 straddling the boundary. The design's cross-backend language ("non-DeepSeek behavior unchanged", "missing fields remain zero") is present in the TDD obligations of atoms A3 and A5. No atom proposes diverging the shared client interface or prompt construction by backend identity string. The main risk is in implementation: the A3 fix must add `preserveReasoning` as a typed policy field, not a model-name string comparison buried in `hostedChatMessages()`, or the shared code will accumulate backend-specific string branches.

---

**Confidence:**

**High** for the factual findings (benchmark telemetry, reasoning_content handling, missing cache/cost parsing).
**Moderate** for the inference that zeroing `ReasoningContent` is the exact cause of the protocol error — I cannot reproduce the error from the available evidence, but the error message ("must be passed back") combined with the code's unconditional stripping makes this the most probable cause.

---

**Decision:** PASS_WITH_RISKS

**Risks accepted:**

1. **A3 implementation must add `preserveReasoning` as a typed policy flag, not a model-name string check.** A string check (*if model == "deepseek-v4-flash"*) would be a north-star violation and would require updating every time a new DeepSeek model ID appears. The fix belongs in `Policy` (`policy.go:43`) as a boolean or in `ChatOptions` as a per-call signal.

2. **A4 must carry both alias and effective model, not just add the effective one and drop the alias.** Downstream consumers (eval scoring, telemetry dashboards) may depend on the alias field name. Adding a new field is safe; renaming or removing the old one is not. The design says "profile alias and effective model id are both represented" — this must be verified during implementation.

3. **The benchmark artifact has no model-call traces**, so the current A4 baseline is incomplete. After A4, re-running the same benchmark should produce events that let a future reviewer verify *which model was called* without guessing. The A4 test must emit a `model_call` trace during the benchmark, or the `session_summary` must carry the API model name.

4. **A5 (cache/cost) is optional but the design does not specify a schema for the new fields.** The parser will need to decide field names and units (tokens vs dollars). These should match what DeepSeek/OpenAI actually return, and should be surfaced in a consistent schema across providers rather than a pass-through of provider-specific names.
  · 49446 tok · in 46371 (44544 cached / 1827 new) · out 3075 (401 reasoning) · ¥0.0089
