[2m  ▎ thinking[0m
  · 16910 tok · in 16827 (16768 cached / 59 new) · out 83 (29 reasoning) · ¥0.0006
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## Review: `docs/design-hosted-cache-cost-metrics.md`

**Verdict: PASS_WITH_RISKS**

---

### ✅ Strong points (agnostic-friendly)

- Section 2 ("Runner Aggregation") explicitly states *"It must not synthesize hit/miss values from prompt tokens. That keeps 'unknown' distinct from 'known zero'."* — correct cross-backend hygiene.
- Section 4 ("Downstream Readers") requires backwards compat with existing fields. Good.
- Section 1 ("Provider Usage Contract") flatly zeroes absent fields rather than erroring. Necessary for any multi-provider wire-up.
- The pricing-policy boundary (lines 33–36, lines 215–216: "Do not reference Reasonix in runtime code") is clear and portable.

---

### ⚠️ Risk 1: Metric names are verbatim DeepSeek wire names — not portable

The three core provider-fact fields are:

| Proposed name | DeepSeek wire name | More portable alternative |
|---|---|---|
| `prompt_cache_hit_tokens` | `usage.prompt_cache_hit_tokens` | `input_tokens_cached` or `cache_read_tokens` |
| `prompt_cache_miss_tokens` | `usage.prompt_cache_miss_tokens` | `input_tokens_uncached` or `cache_miss_tokens` |
| `reasoning_tokens` | `usage.completion_tokens_details.reasoning_tokens` | `reasoning_tokens` (OK — used by OpenAI too) |

**Anthropic's cache model has *three* categories**, not two: `cache_creation_input_tokens` (writes), `cache_read_input_tokens` (hits), and uncached `input_text_tokens`. The design's binary hit/miss cannot represent Anthropic faithfully without either:

1. mapping `cache_read_input_tokens → prompt_cache_hit_tokens` and `cache_creation_input_tokens → prompt_cache_miss_tokens` (losing the creation-vs-uncached distinction), or
2. adding a third top-level field later, which proliferates provider-prefixed names.

**Recommendation:** Name the fields after the *accounting concept* rather than the provider wire name. E.g. `input_tokens_cached`, `input_tokens_uncached`, and leave room for an `input_tokens_cache_write` field at zero for providers that lack it. See line 82 — this is the right place to change before implementation.

---

### ⚠️ Risk 2: `cache_adjusted_prompt_tokens` is information-free

Per line 130–133:

```
cache_adjusted_prompt_tokens = prompt_cache_miss_tokens + prompt_cache_hit_tokens
                               when known, else prompt_tokens
```

For DeepSeek, `hit + miss = prompt_tokens` by contract. For unknown backends, it falls back to `prompt_tokens`. So this field **always equals `prompt_tokens`**. It carries no independent information. A reader who sees `cache_adjusted_prompt_tokens = 500` and `prompt_tokens = 500` cannot tell whether cache was known or not — the true signal is whether `prompt_cache_hit_tokens` is populated.

Either drop it (let benchmark scripts compute it), or change its semantics to a boolean `cache_accounting_known` alongside the raw fields. The doc partially acknowledges this in the "Risks" section ("Confusing 'adjusted tokens' with billable currency") but doesn't resolve the redundancy.

---

### ⚠️ Risk 3: No mention of streaming vs. non-streaming cache behavior

Some providers (OpenAI, Anthropic) expose different or no cache fields during streaming responses. The design should at minimum note that cache fields may arrive on the **final streaming chunk** rather than every chunk, and that aggregators must handle partial vs. final `model_call` events. Without this, future streaming backends could produce zero-filled cache fields that look like "no cache" rather than "streaming chunk before final usage".

---

### ⚠️ Risk 4: No extensibility mechanism for provider-specific sub-fields

The design adds three flat top-level fields. A future provider with a genuinely novel metric (e.g. Gemini's `cached_content_token_count` at the *request* level rather than *response* level, or Azure's token-throttle counters) would need another top-level addition. Consider whether a `usage_provider_metadata` JSON object (opaque to aggregation, passed through to JSONL) would reduce field churn and keep `llama.Usage` stable longer.

---

### ✅ What makes this pass despite the risks

- The separation of provider facts (in `llama.Usage`) from pricing estimates (benchmark scripts) is correct and reusable.
- Zeroing absent fields rather than erroring is the right cross-backend stance.
- Backwards compatibility in "Risks and Mitigations" and "Downstream Readers" is thorough.
- The implementation atoms are concrete and testable.
- The three proposed fields do cover DeepSeek and OpenAI o-series today. The risks are about *tomorrow*.

---

### Exact references

| Risk | Lines | Doc section |
|---|---|---|
| Field names are DeepSeek wire names, not portable to Anthropic/Google | 80–83, 197–205 | 1. Provider Usage Contract, JSON Field Contract |
| `cache_adjusted_prompt_tokens` is always `prompt_tokens` in value | 129–133 | 2. Runner Aggregation |
| No streaming-cache caveat | 46–61 (source facts), 89–95 (parsing changes) | Source Facts, 1. Provider Usage Contract |
| No extensibility for provider-specific metadata | 78–86 | 1. Provider Usage Contract |
  · 23766 tok · in 20857 (17152 cached / 3705 new) · out 2909 (1663 reasoning) · ¥0.0099 · cache prefix changed: tools
