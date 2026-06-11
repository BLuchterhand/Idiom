[2m  ▎ thinking[0m
  · 16946 tok · in 16828 (16640 cached / 188 new) · out 118 (64 reasoning) · ¥0.0008
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## Review Verdict: **PASS**

**Prior risk #1 — Streaming note** ✅ **Resolved.**
The design explicitly scopes streaming out (lines 108–109, 158–161): it documents that only `CompleteChatMessage` is covered, that future streaming chunks may need a separate pass, and that the implementation must not claim streaming coverage. This is a clean model-agnostic boundary.

**Prior risk #2 — Provider-specific extensibility** ✅ **Resolved.**
The design addresses this at three points:
- **Non-Goals** state cache metrics are not mandatory for non-DeepSeek backends.
- **Risks** (lines 240–242) prescribe "additive optional fields or a typed extension map at the runtime edge; do not overload DeepSeek fields for different provider semantics."
- **Parsing** (lines 104–105) confirms OpenAI Responses parse compatibly and zero out cache fields until/unless that backend exposes equivalents.

The JSONL field names (`prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`) are provider-generic, not DeepSeek-specific. The one fuzzy spot is that "typed extension map" is stated as a principle but not designed yet — acceptable for an initial atom that only targets DeepSeek.

**Prior risk #3 — `cache_adjusted` semantics** ✅ **Resolved.**
There is no `cache_adjusted` field in the runtime contract. The design is emphatic:
- Runner `addUsage` must not synthesize hit/miss from prompt tokens (lines 129–130).
- Pricing and currency estimates live **only** in benchmark/reporting scripts (lines 39–41, 143–146, 192–203).
- `prompt_cache_accounting_known` cleanly separates known-zero from unknown accounting via JSON field presence, not numeric values.
- `prompt_cache_hit_rate` is emitted only when accounting is known and `hit + miss > 0`.

All three risks are addressed with clear, enforceable contracts. The design is aligned for implementation of atoms A5.1–A5.4.
  · 22906 tok · in 21297 (17152 cached / 4145 new) · out 1609 (1156 reasoning) · ¥0.0077 · cache prefix changed: tools
