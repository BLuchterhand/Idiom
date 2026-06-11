[2m  ▎ thinking[0m
  · 16895 tok · in 16818 (16768 cached / 50 new) · out 77 (23 reasoning) · ¥0.0005
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## Review: `docs/design-hosted-cache-cost-metrics.md`

**Role:** Prompt / Protocol reviewer
**Focus:** provider protocol facts vs harness policy, JSONL event contracts, prompt/route/behavior drift
**Verdict:** **PASS_WITH_RISKS**

---

### ✅ Strong Points

1. **Provider facts vs pricing policy boundary is explicit and enforced.**
   - §25–36 (Goals) and §40–44 (Non-Goals) cleanly split: `llama.Usage` owns provider facts; benchmark/reporting owns currency estimates.
   - §40: "Do not hardcode current DeepSeek pricing into `internal/runtime/llama`." — this is a durable contract, not just a prose preference.
   - §86: "do not add pricing or policy logic here [in `llama.Usage`]."

2. **No prompt, route, or answer-behavior drift.**
   - §43: "Do not change route selection, prompts, or direct-answer behavior."
   - The data flow is a one-way read → aggregate → emit pipeline (diagram lines 68–74). No feedback path into prompt assembly, gate logic, or actor selection. Safe.

3. **Backwards-compatible additive schema.**
   - §36: "Preserve existing token fields and readers."
   - §207: "Existing fields remain authoritative for raw token volume."
   - §160: "Existing readers must not fail on added fields." — correct JSONL evolution practice.

---

### ⚠️ Risks (contractual ambiguities)

**Risk 1 — "Known-present" is underspecified.**
> §117: "Per-call `model_call` JSONL should include cache fields only when non-zero or **known-present**."

The document uses `known-present` without defining when the runtime can *know* a field is present vs merely assumed absent. Three distinct cases exist:

| Case | Provider returned cache fields? | Should field appear in JSONL? |
|------|-------------------------------|-------------------------------|
| Backend is DeepSeek, fields present (≥0) | Yes | Always |
| Backend is DeepSeek, fields omitted from response | No | Omit (or `null`?) |
| Backend is OpenAI/other, never returns cache | No | Omit |

The second and third cases look identical in the JSONL output but mean different things to a downstream benchmark parser. The document's per-call policy makes them indistinguishable.

*Recommendation:* Define `known-present` explicitly: a boolean `prompt_cache_accounting_known` per `model_call` event (already hinted in §218 as a risk mitigation). Add it to the JSONL contract table in §196–207 so downstream parsers have an authoritative signal.

---

**Risk 2 — Session-summary emission policy conflicts with per-call policy.**
> §118–119: "Session summaries should include **stable numeric fields** so benchmark parsers can rely on them."

"Stable" implies always-present (even if zero). But the per-call policy (§117) omits cache fields when zero/unknown. A session_summary that always emits `prompt_cache_hit_tokens: 0` could mean "all calls had zero hits" (known) or "cache fields were never available" (unknown). The parser cannot tell which.

The document's own Review Questions (§320) flags this: "Should session summaries always include zero cache fields, or only when known?" — good that it's identified, but it's an open design question, not a settled contract. Settle it before implementation.

---

**Risk 3 — `cache_adjusted_prompt_tokens` naming creates a semantic trap.**
> §129–134: defined as `prompt_cache_miss_tokens + prompt_cache_hit_tokens` when known, else `prompt_tokens`.

The prose correctly says "accounting convenience, not a provider bill." But the field name `cache_adjusted_prompt_tokens` sounds like "what you effectively pay for." A benchmark script that uses this field as a cost input without reading the pricing-rate prose will get subtly wrong estimates (because `prompt_tokens` is the pre-cache total, not a post-cache cost basis). Strongly consider renaming to `cache_known_prompt_tokens` or similar to encode the field's semantics (known/unknown, not adjusted/effective).

---

**Risk 4 — `prompt_cache_accounting_known` is relegated to prose, absent from the contract table.**
> §218: "optionally emit `prompt_cache_accounting_known=false` or a diagnostic if a future test finds mismatch."

This field would solve Risk 1 and Risk 2 simultaneously. It should be in the canonical JSONL contract (lines 196–207) with defined emission rules, not mentioned as an optional diagnostic in the Risks section.

---

### Summary

| Criterion | Status | Key reference |
|-----------|--------|---------------|
| Provider facts vs harness policy | ✅ Clean separation | §30–36, §40, §86 |
| Prompt/route/behavior drift | ✅ None introduced | §43 |
| Backwards-compatible JSONL | ✅ Additive, no breaking changes | §36, §160, §207 |
| Field emission rules precise enough for implementation | ⚠️ "Known-present" underspecified | §117 |
| Session vs per-call consistency | ⚠️ Open question (acknowledged) | §118–119 vs §117, §320 |
| Field names encode correct semantics | ⚠️ `cache_adjusted_prompt_tokens` misleading | §129–134 |
| Contract table complete | ⚠️ Missing `prompt_cache_accounting_known` | §196–207 vs §218 |

**PASS_WITH_RISKS** — the architecture is sound and the boundary between provider facts and policy is correctly drawn. Tighten the JSONL contract table (add `prompt_cache_accounting_known`, settle session-summary emission policy, and rename `cache_adjusted_prompt_tokens` to avoid the cost-implication trap) before A5.1 implementation begins.
  · 23648 tok · in 20842 (17152 cached / 3690 new) · out 2806 (1505 reasoning) · ¥0.0096 · cache prefix changed: tools
