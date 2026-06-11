[2m  ▎ thinking[0m
  · 16947 tok · in 16834 (16640 cached / 194 new) · out 113 (59 reasoning) · ¥0.0008
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
**Review verdict: PASS_WITH_RISKS**

Prior risks are resolved; the design is aligned for implementation with two minor caveats that should be closed during implementation code review rather than blocking the atom.

## Prior-risk resolution

| Prior risk | Status | Evidence |
|---|---|---|
| **Ambiguous `cache_adjusted_prompt_tokens`** | ✅ Resolved | Term is absent. Separation is explicit: runtime owns provider facts, benchmark owns estimates. The JSONL contract emits raw breakdown fields; "adjusted" and currency live only in reporting. |
| **Missing `cache_accounting_known`** | ✅ Resolved | `PromptCacheTokensKnown`/`prompt_cache_accounting_known` threaded through `llama.Usage`, runner, JSONL, TUI, transcript. Semantics are clean: set by field presence, not by numeric value. |
| **Weak invariant framing** | ✅ Resolved | The design opens with a named broken invariant ("telemetry authority") and the implementation is structured as a contract fix, not a feature. The `addUsage` ban on synthesis is a strong guard. |
| **Structural test scope** | ✅ Resolved | Four well-bounded atoms with explicit file lists, focused test patterns, acceptance criteria, and a combined verification command. No "run all tests" shotgun scoping. |

## Strengths

- **Provider-fact purity**: The boundary between parsing (client.go), aggregation (runner_state.go), emission (runner.go), and downstream readers is clean. Pricing is kept out of the runtime library.
- **Known/unknown invariant**: boolean is present on the struct, per-call event, session summary, and debug transcript. This was the core weakness in the prior draft.
- **Non-goals are crisp**: No inference, no mandatory metrics for non-DeepSeek, no route/prompt changes, no reverted A1/A2 work.
- **Streaming limitation documented**: Explicitly scoped out; forward-looking note about provider final-chunk usage.

## Caveats (risk, not blocker)

1. **`PromptCacheTokensKnown` granularity for partial fields**: The doc says "field presence, not on the numeric values" but the struct only has one boolean — it can't distinguish "both cache fields present" from "only one present". If DeepSeek ever sends one cache field without the other (e.g. `prompt_cache_miss_tokens: 500` but no `prompt_cache_hit_tokens`), the current design would set `known=true` but the aggregator would see a breakdown that doesn't sum to `prompt_tokens`. Recommend clarifying in code comments or the doc that known=true requires presence of *one or both* cache-detail fields, and that aggregators should not assert `hit + miss == prompt_tokens` as an invariant for current DeepSeek behavior.

2. **`addUsage` overflow**: The contract says "sum non-negative values" but doesn't cover overflow. For benchmark totals this is harmless noise; leave a one-line comment at the implementation site (`runner_state.go`) that overflow is unhandled by design for this use case.

Neither caveat blocks implementation — they are code-review-level clarifications. The design is ready for A5.1–A5.4 to proceed.
  · 23081 tok · in 21298 (17152 cached / 4146 new) · out 1783 (1097 reasoning) · ¥0.0081 · cache prefix changed: tools
