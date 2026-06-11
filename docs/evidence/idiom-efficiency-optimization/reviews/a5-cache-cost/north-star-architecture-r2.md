[2m  ▎ thinking[0m
  · 16961 tok · in 16817 (16640 cached / 177 new) · out 144 (90 reasoning) · ¥0.0008
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
**Verdict: PASS**

No blockers. The revised document cleanly satisfies all three North-Star criteria. Detailed findings:

**1. Provider facts remain generic** ✅

- The few references to "DeepSeek Flash" / "Reasonix-vs-Idiom" appear only in the Problem section (lines 10–14) as concrete context for *why* the gap matters. The design itself uses *generic* field names (`PromptCacheHitTokens`, `PromptCacheMissTokens`, `PromptCacheTokensKnown`, `ReasoningTokens`) and frames them as provider facts that any backend can supply.
- Parsing instructions say "Hosted chat-completions response struct accepts …" with DeepSeek as one example, then explicitly adds "OpenAI Responses parsing remains compatible and zeroes cache fields" (line 104–105).
- Risks section (line 241–242) proactively says "do not overload DeepSeek fields for different provider semantics" — the design anticipates provider diversity, not DeepSeek lock-in.

**2. Runtime code avoids Reasonix-specific behavior** ✅

- Every action item (A5.1–A5.4) targets files inside the Idiom codebase (`internal/runtime/llama`, `internal/runtime/agent`, `internal/surfaces/…`). Nothing instructs runtime code to reference Reasonix, call Reasonix APIs, or behave differently when Reasonix is present.
- Risks section (line 239) explicitly states: "Do not reference Reasonix in runtime code."
- The single `reasonix run …` occurrence (line 336) is in the Verification Plan — it's a test/benchmark invocation *outside* runtime code, not runtime behavior. Acceptable.

**3. Pricing stays outside runtime** ✅

- Non-Goals (line 46): "Do not hardcode current DeepSeek pricing into `internal/runtime/llama`."
- Section 2 (lines 144–146): "Derived values such as 'cache-adjusted prompt tokens' or currency estimates belong in benchmark/reporting scripts, **not runtime JSONL**, because they are interpretations of provider facts."
- Section 5 (Benchmark Cost Estimation) is placed entirely in benchmark artifacts/scripts, with the explicit rule that "if no pricing table is configured, print cache hit/miss and hit rate without a currency estimate" (lines 202–203).
- Risks (line 229): "Name currency fields only in benchmark/reporting artifacts, not in `llama.Usage`."

**One observation (not a FAIL or PASS_WITH_RISKS):** The doc reuses `internal/runtime/llama` (the existing provider-interface package) for cache-field parsing, which makes sense architecturally — that's where `llama.Usage` already lives — but the package name is a historical misnomer now that it handles DeepSeek hosted APIs. Not this doc's problem to fix, and the design correctly routes through the existing boundary.

**Conclusion:** PASS. The document is generic about providers, clean about runtime boundaries, and keeps pricing in the benchmark/reporting layer where it belongs. Ready to implement.
  · 23062 tok · in 21312 (17152 cached / 4160 new) · out 1750 (1091 reasoning) · ¥0.0080 · cache prefix changed: tools
