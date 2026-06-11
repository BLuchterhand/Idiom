[2m  ▎ thinking[0m
  · 16934 tok · in 16822 (16640 cached / 182 new) · out 112 (58 reasoning) · ¥0.0007
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
Let me verify a few referenced files to ground the review.
  · 22610 tok · in 21285 (17152 cached / 4133 new) · out 1325 (1138 reasoning) · ¥0.0071 · cache prefix changed: tools
  -> grep {"pattern": "llama\\.Usage", "path": "internal/runtime/llama"}
  -> grep {"pattern": "runnerMetrics", "path": "internal/runtime/agent/runner_state.go"}
  -> grep {"pattern": "runnerBudgetLedger", "path": "internal/runtime/agent"}
[2m  ▎ thinking[0m
  · 23285 tok · in 23129 (22528 cached / 601 new) · out 156 (19 reasoning) · ¥0.0014
  -> read_file {"path": "internal/runtime/agent/runner_state.go", "offset": 200, "limit": 80}
  -> grep {"path": "internal/runtime/llama", "pattern": "type Usage struct"}
[2m  ▎ thinking[0m
  · 24547 tok · in 24392 (23168 cached / 1224 new) · out 155 (14 reasoning) · ¥0.0020
  -> read_file {"offset": 105, "limit": 50, "path": "internal/runtime/llama/client.go"}
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "emitModelCallTraceWithOptions"}
[2m  ▎ thinking[0m
  · 25769 tok · in 25656 (24448 cached / 1208 new) · out 113 (29 reasoning) · ¥0.0019
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 6087, "limit": 100}
[2m  ▎ thinking[0m
Now let me check how the TUI aggregates and what the transcript code looks like for the session summary.
  · 27375 tok · in 27180 (25728 cached / 1452 new) · out 195 (35 reasoning) · ¥0.0024
  -> grep {"path": "internal/surfaces/tui/app/model_state.go", "pattern": "model_call|session_summary|modelCall"}
  -> grep {"path": "internal/evidence/transcript/debuglog.go", "pattern": "SessionSummary|BudgetLedger|ModelCall"}
[2m  ▎ thinking[0m
  · 28332 tok · in 28159 (27264 cached / 895 new) · out 173 (17 reasoning) · ¥0.0018
  -> read_file {"offset": 109, "limit": 40, "path": "internal/evidence/transcript/debuglog.go"}
  -> read_file {"offset": 144, "limit": 30, "path": "internal/evidence/transcript/debuglog.go"}
[2m  ▎ thinking[0m
I've now reviewed the full document and verified the relevant codebase context. Here is my assessment.

---

## Review Result: **PASS_WITH_RISKS**

### Prior Risk 1: Nested Reasoning Parse Test — MITIGATED

The doc explicitly names the JSON path `usage.completion_tokens_details.reasoning_tokens`, calls out the exact product-level response fields (`prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, `completion_tokens_details.reasoning_tokens`), and specifies asymmetric test coverage: known-present, known-zero, omitted, and unchanged-legacy. Dedicated test file `deepseek_test.go` is named. No further design work needed; ready for implementation.

### Prior Risk 2: Aggregation Shape — MOSTLY CLEAN (minor gaps)

The data flow `Usage → runnerMetrics.addUsage() → model_call event → TUI/transcript` is correct and matches the code at `runner.go:6109-6111` and `model_state.go:1172`. The separation of provider facts (Usage) from interpretation (pricing estimates in benchmark scripts) is the strongest part of the design.

Two gaps for the implementer:

1. **`runnerBudgetLedger` semantics are underspecified.** The doc says "extend with the same fields" but doesn't state whether cache fields participate in budget enforcement or are just passthrough storage. The ledger currently tracks consumption against budget limits; giving it cache fields without stating whether cache-aware cost forces a different budget ceiling is ambiguous. Recommend: the implementation should clarify (likely passthrough-only for this atom).

2. **Debug transcript has three types, but the doc clusters them.** `ModelCall`, `TurnSummary`, and `SessionSummary` in `debuglog.go` all need new fields. The doc references "BudgetLedger" and "SessionSummary" but doesn't call out `TurnSummary` explicitly. The implementer needs to extend all three.

### Prior Risk 3: Clumsy Parameter Growth — ACKNOWLEDGED, NOT ABSTRACTED

The count is real: the new fields appear in **6 struct types** (`llama.Usage`, `runnerMetrics`, `runnerBudgetLedger`, debug `ModelCall`, debug `TurnSummary`, debug `SessionSummary`) plus the TUI internal `sessionAgg` and inline JSONL map payloads. That's ~24 field declarations plus serialization tags spread across the codebase.

The doc does not propose a shared `CacheAccounting` struct to reduce this. It mentions "additive optional fields or a typed extension map" for *future* providers but doesn't apply that principle to the current expansion. This is **pragmatic for a first pass** but the implementer should consider extracting a shared struct before A5.3 to avoid copying identical fields into three debug types.

### Additional Finding

**Naming inconsistency**: `PromptCacheTokensKnown` in `llama.Usage` vs `PromptCacheAccountingKnown` in runnerMetrics/session summary. One uses "CacheTokens", the other "CacheAccounting". These will drift during implementation unless unified. Recommend standardizing on `PromptCacheAccountingKnown` (the JSONL field name) or `CacheAccountingKnown` for all types.
  · 30610 tok · in 29002 (28288 cached / 714 new) · out 1608 (932 reasoning) · ¥0.0045
