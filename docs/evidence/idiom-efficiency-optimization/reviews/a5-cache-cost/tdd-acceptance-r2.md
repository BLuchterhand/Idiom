[2m  ▎ thinking[0m
  · 16920 tok · in 16826 (16640 cached / 186 new) · out 94 (40 reasoning) · ¥0.0007
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
## TDD / Acceptance Review: `docs/design-hosted-cache-cost-metrics.md`

**Rating: PASS_WITH_RISKS**

---

### Prior Risk 1: Missing full-pipeline DeepSeek response-to-summary assertion

**RESOLVED.** A5.2 now explicitly requires:

> *"A runner-level test feeds a DeepSeek-shaped response through the full path and asserts cache fields survive into final JSONL summary output."*

This is the correct invariant: a response parsed at the provider boundary survives `llama.Usage` → `addUsage` → `runnerMetrics` → `session_summary` JSONL. The atom boundary is right — it tests the runtime's end-to-end contract without dragging in TUI rendering.

### Prior Risk 2: Unclear benchmark acceptance

**RESOLVED.** A5.4 acceptance is now concrete and testable:

| Assertion | Verifiable? |
|---|---|
| "Benchmark report prints raw tokens, cache hit/miss, hit rate, and estimated cost when a pricing snapshot is supplied" | Yes — exact fields listed |
| "Report labels pricing date and model" | Yes |
| "Report clearly says when provider cache fields are unknown" | Yes — forces a distinct output shape for unknown vs. known-zero |

The cost estimation formula is explicit and the pricing-snapshot contract (provider, model, date, rates, known flag) is fully specified.

---

### Remaining Risks (why not a clean PASS)

**1. A5.2 test scope underspecified for the known-zero case**
The runner-level test acceptance says "feeds a DeepSeek-shaped response … asserts cache fields survive." It should also assert a second variant: a response with `prompt_cache_hit_tokens=0` and `prompt_cache_miss_tokens=0` *where the provider explicitly sent those fields* sets `prompt_cache_accounting_known=true`. The design makes this distinction critical (lines 91-94, 115), but the acceptance in A5.2 doesn't force both test paths.

**2. No backward-compatible parsing test for downstream readers**
Line 174 states *"Existing readers must not fail on added fields"* and A5.3 tests for explain/post-analyze, but there is no explicit test that feeds an old-format JSONL (without cache fields) through the updated parsers and asserts zero/nil defaults. Without this, a future parser change that accidentally requires the new fields won't be caught until a user runs an old log through the new binary. A cheap fixture-based test in A5.3 would close this.

**3. A5.4 fixture strategy is ambiguous**
The acceptance says *"Use captured JSONL fixtures or rerun the DeepSeek Flash benchmark after A5.1-A5.3 pass."* An integration test should pick one: "Use captured JSONL fixtures" is deterministic and CI-safe; "rerun the benchmark" is live and manual. The design should commit to fixtures for the automated acceptance and treat the live rerun as a separate validation step (which the Verification Plan step 4 already covers). Right now a reviewer could argue the benchmark acceptance is not automated.

**4. No cross-atom integration test**
The four atoms each have focused tests, and the combined `go test -run` in Verification step 2 runs them in the same invocation — but that's four separate test binaries, not one test that walks the full chain (provider response → parse → runner aggregate → event → TUI aggregation → transcript). A single integration test spanning packages would catch type-mismatch or field-name skew between layers. The current plan only catches those at build time or live-run time.

---

### Summary

| Dimension | Assessment |
|---|---|
| Prior risk 1 (pipeline assertion) | Addressed |
| Prior risk 2 (acceptance clarity) | Addressed |
| Acceptance criteria per atom | Clear and testable |
| Scope boundaries | Well-drawn |
| Non-regression coverage | Good (existing tests + raw token assertions) |
| Known-zero vs unknown distinction | Correctly designed; under-tested |
| Downstream reader backward compat | Implicit, not enforced by a test |
| Cross-atom integration | None — relies on build + manual run |

**Verdict: PASS_WITH_RISKS** — the design is aligned and implementable, but I recommend tracking the four risks above as pre-merge checklist items (or at minimum as clearly deferred to a follow-up atom) rather than closing them as implicit. The most impactful fix would be adding the old-fixture backward-compat test in A5.3 and the known-zero runner test in A5.2.
  · 23613 tok · in 21271 (17152 cached / 4119 new) · out 2342 (1360 reasoning) · ¥0.0091 · cache prefix changed: tools
