[2m  ▎ thinking[0m
  · 16916 tok · in 16823 (16768 cached / 55 new) · out 93 (27 reasoning) · ¥0.0006
  -> read_file {"path": "/workspace/idiom/docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## North-Star / Architecture Review: `design-hosted-cache-cost-metrics.md`

**Verdict: PASS**

The document respects model-vs-harness authority, Clean Architecture boundaries, and generic provider facts. It explicitly avoids embedding Reasonix-specific logic in runtime code. Below are the findings per lens.

---

### Model‑vs‑Harness Authority

Pricing policy lives outside the provider client and the runner. The document is unambiguous:

- *Non-Goals* (line 40): *"Do not hardcode current DeepSeek pricing into `internal/runtime/llama`."*
- *Goals* (line 33–35): *"Keep pricing policy outside the provider client. The client owns provider facts; benchmark/reporting code owns estimates and must label the pricing snapshot."*
- *Risks* (line 225): *"Keep metrics generic provider facts. Do not reference Reasonix in runtime code."*

The dataflow in the mermaid diagram (lines 67–74) correctly shows benchmark/report as a downstream consumer rather than a runtime authority — it receives facts and computes cost externally.

**No issues.**

---

### Clean Architecture Boundaries

The design follows a strict layered flow:

```
Provider response → llama.Usage (gateway/interface layer)
                 → runnerMetrics (application/use-case aggregation)
                 → JSONL events + TUI state + Transcript (presentation/logging)
                 → Benchmark scripts (external)
```

Each layer has a defined responsibility:
- `llama.Usage` — holds provider facts, no pricing logic (section 1).
- `runnerMetrics` / `runnerBudgetLedger` — sums additive fields without synthesizing hit/miss; "must not synthesize hit/miss values from prompt tokens" (line 113–114).
- Session summaries include derived fields (`cache_adjusted_prompt_tokens`, `prompt_cache_hit_rate`) but the document flags these as accounting conveniences, not provider bills (lines 126–134).
- Benchmark scripts compute cost estimates with explicit pricing-snapshot labeling (section 5).

Implementation atoms are organized by architectural layer (A5.1 gateway, A5.2 application, A5.3 presentation, A5.4 external scripts), and each has scoped acceptance criteria.

**Minor observation (not a blocker):** `cache_adjusted_prompt_tokens` and `prompt_cache_hit_rate` are derivations, not provider facts. Placing them in the runner/session-summary layer is **acceptable** because they are pure functions of known provider fields and the document explicitly says they are not billing values. The Review Questions section (line 322) already asks whether they should move to benchmark reports — appropriate self-critique.

**No architecture violations.**

---

### Generic Provider Facts

The new fields (`PromptCacheHitTokens`, `PromptCacheMissTokens`, `ReasoningTokens`) are named generically — no `DeepSeek*` prefix. The struct serves as the union of all known provider fact fields across backends:

- DeepSeek parsing fills them from `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` / `completion_tokens_details.reasoning_tokens`.
- OpenAI Responses parsing "remains compatible and zeroes cache fields unless that backend later exposes equivalent fields" (line 94–95).
- Absent fields stay zero (line 84, 113–114), so "unknown" and "known zero" remain distinct.

The handling is backend-agnostic by design. No provider-specific type or branching leaks into the public contract.

**No issues.**

---

### Reasonix‑Specific Runtime Code

The Problem section (line 10) references the Reasonix-vs-Idiom benchmark *as motivation* — appropriate for a design document. The runtime code itself stays clean:

- The Risks section (line 224–225) explicitly prohibits Reasonix references in runtime code.
- None of the implementation atoms (A5.1–A5.4) reference Reasonix in any file path, function name, struct field, or logic branch.
- The only Reasonix mention in implementation material is the verification step (line 315): `reasonix run --model deepseek-flash`. This is an external CLI invocation for a benchmark comparison, not runtime code. Acceptable.

**No issues.**

---

### Summary

| Lens | Verdict |
|---|---|
| Model‑vs‑harness authority | **Clean** — pricing policy explicitly gated to benchmark scripts |
| Clean Architecture | **Clean** — layered, single-responsibility, with clear acceptance per atom |
| Generic provider facts | **Clean** — generic names, zero-absent handling, backend-agnostic |
| No Reasonix in runtime code | **Clean** — mentioned only as motivation and as an external verification CLI |

**PASS.** The document is ready for implementation. Consider moving `cache_adjusted_prompt_tokens` and `prompt_cache_hit_rate` out of session-summary JSONL and into benchmark/reporting scripts if the review-team consensus falls on the strict side of "accounting convenience vs. provider fact" — but this is a style preference, not an architecture defect.
  · 23274 tok · in 20863 (17152 cached / 3711 new) · out 2411 (1331 reasoning) · ¥0.0089 · cache prefix changed: tools
