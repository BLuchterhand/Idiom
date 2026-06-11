[2m  ▎ thinking[0m
  · 16928 tok · in 16826 (16768 cached / 58 new) · out 102 (48 reasoning) · ¥0.0006
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## Review: docs/design-hosted-cache-cost-metrics.md

**Verdict: PASS_WITH_RISKS**

---

### What the document gets right (deepest invariant)

The document correctly identifies the **contract gap** as the root cause: `llama.Usage` (and its JSONL serialization) lacks cache-breakdown fields, so every downstream consumer — runner metrics, TUI summaries, benchmark reports — inherits the same opaque view. This is not a benchmark tooling bug or a prompt-layout problem; it is a **provider-facts contract** gap in  `internal/runtime/llama/client.go` → `llama.Usage`. The document traces that gap end-to-end:

- **Broken invariant:** `llama.Usage` does not carry `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, or `reasoning_tokens` from the provider response.
- **Boundary that owns it:** The provider client in `internal/runtime/llama` (parsing) and the runner aggregation in `internal/runtime/agent` (propagation).
- **Broader class of failures prevented:** Any comparison across provider pricing regimes, not just the Reasonix benchmark. Cache-friendliness analysis, cost regression tracking, and provider-switching studies all depend on the same missing fields, and the document's fix covers them all generically.

The **separation of concerns** (Section 1 = provider facts, Section 2 = aggregation + JSONL, Section 5 = cost estimation outside the runtime) is the right architecture. Specifically:

- Lines 33-34: _"Keep pricing policy outside the provider client. The client owns provider facts; benchmark/reporting code owns estimates."_
- Lines 113-114: _"must not synthesize hit/miss values from prompt tokens. That keeps 'unknown' distinct from 'known zero'."_
- Lines 223-224: _"Keep metrics generic provider facts. Do not reference Reasonix in runtime code."_

These are north-star aligned: the harness owns contracts, not strategy.

**Evidence/verification obligations** are well-structured: each of the four implementation atoms (A5.1–A5.4) lists exact files, test commands, and acceptance criteria. The combined verification plan (lines 306-315) ties them together end-to-end.

---

### Risks (why not a clean PASS)

**R1 — `cache_adjusted_prompt_tokens` is a derived field that does not mean what it says (lines 124-134)**

The field equals `prompt_cache_miss_tokens + prompt_cache_hit_tokens` when cache is known — which is simply `prompt_tokens` restated. It adds zero information beyond the sum of its own constituents and the existing `prompt_tokens` field. Worse, the name "cache_adjusted" suggests a cache-discounted value, which is exactly what the benchmark layer should compute. This is a mild **boundary leak**: a derived convenience field that lives in the runtime JSONL but serves only the benchmark consumer. The document's own review question (line 321-322) asks whether it should exist. The correct answer for Incident-Root-Cause discipline is: **remove it**. Let benchmark scripts compute `hit + miss` from the raw fields; if they want a restatement of `prompt_tokens` they already have `prompt_tokens`.

**R2 — No invariant-enforcing test for propagation completeness (lines 197-207)**

The JSON Field Contract table lists which events carry which fields, but the verification plan only tests *specific* combinations of fields being present. There is no structural test that enforces "every field in `llama.Usage` appears in `model_call` JSONL, and every field in `model_call` appears in the TUI `sessionAgg`." A future developer could add a field to `llama.Usage` and propagate it to `model_call` but forget `sessionAgg`, and the current test suite would not catch the divergence. The document's risk section (line 219-221) names this risk but mitigates with "focused tests" rather than a structural equivalence contract.

**R3 — The root cause is framed as a missing-feature gap, not a broken invariant (lines 15-23)**

The Problem section reads as a list of things Idiom doesn't emit, rather than stating the invariant failure: "The `llama.Usage` type and its JSONL contract do not require cache-breakdown fields, so the runtime cannot distinguish provider billing categories." This framing matters for Incident Root-Cause — future readers debugging a benchmark accusation will scan for "why was the system wrong" not "what fields were missing." The design implicitly corrects this, but the problem statement itself could be sharper.

---

### Summary

| Criterion | Assessment |
|-----------|-----------|
| Deepest invariant named | ✅ Yes — `llama.Usage` contract gap traced end-to-end |
| Symptom-only hacks avoided | ✅ Yes — separate concerns, no pricing in runtime, no cache inference |
| Evidence/verification obligations | ⚠️ Strong per-atom tests, but no propagation-completeness contract |
| North-star alignment | ✅ Yes — contract boundary enforcement, no strategy leakage into runtime |
| Non-goals prevent scope creep | ✅ Yes — explicit about what is *not* changing |

**Recommendation:** Address R1 by removing `cache_adjusted_prompt_tokens` from the runtime JSONL contract (leave it to benchmark scripts to compute). Address R2 by adding a structural test that asserts field parity across the `llama.Usage` → `model_call` → `session_summary` chain — a simple map-key comparison would suffice. R3 is cosmetic but worth tightening the Problem statement.
  · 23569 tok · in 20875 (17152 cached / 3723 new) · out 2694 (1482 reasoning) · ¥0.0095 · cache prefix changed: tools
