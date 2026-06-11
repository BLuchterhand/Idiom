[2m  ▎ thinking[0m
  · 16917 tok · in 16829 (16640 cached / 189 new) · out 88 (34 reasoning) · ¥0.0007
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## Review: `docs/design-hosted-cache-cost-metrics.md`

### Verdict: **PASS_WITH_RISKS**

The revised design resolves all three prior risks cleanly. Below are the findings per risk, plus one new concern.

---

### 1. Session emission policy conflict — **RESOLVED**

The policy is well-defined and internally consistent:

- **Per-call** (`model_call`): cache fields emitted "only when non-zero or known-present" — the `known-present` clause correctly handles the known-zero case.
- **Session summary**: stable numeric fields plus a boolean `prompt_cache_accounting_known`, with `prompt_cache_hit_rate` conditioned on known+non-zero denominator.
- **No conflict** with existing contracts: "Existing fields remain authoritative for raw token volume" and "Preserve existing token fields and readers for backwards compatibility."
- The distinction between provider facts (runtime JSONL) and derived estimates (benchmark scripts) is explicitly drawn and enforced by artifact boundaries.

One phrasing nit: "only when non-zero or known-present" could be read as an exclusive-or. If `hit=0, miss=0, known=true`, the fields should be emitted (the known-present branch). That is the correct behavior, but the reader has to pause to confirm. Consider sharpening to "only when `prompt_cache_accounting_known` is true or at least one cache-token field is non-zero" — though this is an editorial clarity note, not a contract defect.

---

### 2. `cache_adjusted` naming — **RESOLVED**

No `cache_adjusted` naming appears anywhere in runtime or aggregation layer:

- Provider facts use `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`.
- The only computed field is `prompt_cache_hit_rate` (a ratio, clearly labeled).
- The Risks section explicitly names the trap ("Confusing 'adjusted tokens' with billable currency") and enforces the boundary: "Name currency fields only in benchmark/reporting artifacts, not in `llama.Usage`."
- Derived values like "cache-adjusted prompt tokens" are explicitly excluded from runtime JSONL.

This is well-guarded.

---

### 3. `prompt_cache_accounting_known` in contract — **RESOLVED**

Present at every layer with consistent semantics:

| Layer | Name | Meaning |
|---|---|---|
| `llama.Usage` (Go) | `PromptCacheTokensKnown` | Provider explicitly reported cache hit/miss |
| runnerMetrics (Go) | `PromptCacheAccountingKnown` | Same concept, runner-level aggregation |
| `model_call` JSONL | `prompt_cache_accounting_known` | Per-call boolean |
| `session_summary` JSONL | `prompt_cache_accounting_known` | True if ≥1 call had known accounting |

Validation requires:
- Known-zero → `known=true`
- Omitted → `known=false`, numeric fields zero
- Parsing is based on JSON field *presence*, not numeric value.

This fully closes the prior gap.

---

### New concern (minor, naming consistency)

The Go struct field `PromptCacheTokensKnown` (section 1, `llama.Usage`) and the runner struct field `PromptCacheAccountingKnown` (section 2, `runnerMetrics`) use different names for the same logical concept. While each struct can legitimately choose its own Go identifier, this divergence creates a small implementation trap — a developer touching both layers may mentally model them as slightly different concepts. The JSONL field is the stable contract surface, so the runtime names are cosmetic, but it's worth picking one canonical term (`Accounting`, `Tokens`, or `CacheAccounting`) and using it across all Go identifiers so the relationship is obvious at a glance.

---

### Summary

| Criterion | Status |
|---|---|
| Session emission policy | Clear and conflict-free |
| `cache_adjusted` naming | Absent from runtime; properly scoped to benchmark |
| `prompt_cache_accounting_known` | Present at every level with correct semantics |
| Backwards compatibility | Explicit and preserved |
| Separation of provider facts vs. estimates | Strongly enforced |
| Validation coverage | Well-specified per atom |

**PASS_WITH_RISKS** — the design is aligned for implementation. The only outstanding risk is the `PromptCacheTokensKnown` vs `PromptCacheAccountingKnown` Go identifier mismatch, which is low severity but worth resolving before A5.1 code lands to avoid future confusion when a developer reads both structs side by side.
  · 23787 tok · in 21268 (17152 cached / 4116 new) · out 2519 (1567 reasoning) · ¥0.0095 · cache prefix changed: tools
