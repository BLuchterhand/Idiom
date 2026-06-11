[2m  ▎ thinking[0m
  · 16901 tok · in 16819 (16640 cached / 179 new) · out 82 (19 reasoning) · ¥0.0007
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
## APPROVE with Findings

The proposal is well-scoped, the safety boundary is tight (prefetch-only, normal `read_file` unchanged), and the telemetry reuse avoids new instrumentation. Low-risk quick-win framing is sound. However, before committing to implementation I have three concrete findings.

---

### Finding 1: Acceptance test #5 is an underspecified behavioral oracle

> "A runner-level truncated-prefetch fixture can answer without gratuitous follow-up when the excerpt is sufficient."

This is the most important test (it validates the model actually *works* with excerpts), but is not actionable as written:

- **"Gratuitous"** is not defined. One extra `read_file` call when the excerpt is genuinely sufficient? Any `read_file` at all on the truncated ranges? How many false positives would slip through?
- **Is it automated or manual?** It's listed alongside four clearly automatable unit-style tests, which implies it should also be automated, but the success predicate is vague.
- **What counts as "sufficient"?** The fixture needs a concrete evidence requirement: e.g. "the excerpt contains the symbol definition needed to answer the prompt."

**Recommendation:** Tighten to an eval-style fixture with a measurable oracle:

> *"A runner-level fixture with a large fixture file and a prompt answerable from the first N lines of that file produces a correct answer (asserted by eval oracle) without calling `read_file` on the truncated range."*

The threshold is: zero unnecessary `read_file` calls on truncated portions when the excerpt already provides the answer.

---

### Finding 2: No benchmark metric → the "quick win" cannot be measured

The proposal's motivation is cost reduction, but there is no target, no baseline, and no acceptance criterion for the win itself.

**What's missing:**
- Baseline: current token cost of prefetch per session (or per file) from today's logs/telemetry.
- Target: what reduction counts as success — e.g. "prefetch body tokens drop ≥60% on prefetch-heavy sessions" or "P95 prefetch bytes per session ≤ X".
- Regression guard: a non-goal metric — e.g. "correctness evals do not regress on fixtures that need >1 read_file follow-up to answer from a truncated prefetch."

Without these, you'll ship the change, see *some* cost drop, and won't know whether you hit the quick-win bar or just moved the work to follow-up `read_file` calls.

**Recommendation:** Add 1–2 concrete benchmark criteria before the acceptance-test section. For a quick win they don't need to be elaborate — a stable eval subset and a before/after token-count comparison on the existing `read_only_named_path_prefetch` telemetry field would suffice.

---

### Finding 3: Missing acceptance test for unchanged paths

Three behaviors that should not change are not explicitly tested:

1. **Small files still return full content via prefetch** (not truncated). Current behavior preserved, but not tested.
2. **`read_file` with an oversized requested range still returns a `too_large` / boundary error.** The proposal says "normal `read_file` behavior remains unchanged" — but a test confirming the error path is distinct from the new excerpt path is important for regression safety.
3. **The truncation summary format is not specified** — test #2 requires "exact, stable format" but the format is left undefined. For the test to pass deterministically, the format string needs to be part of the intent (even if deferred to implementation, the fields and order should be baked).

**Recommendation:** Add these three tests and specify the truncation summary field order in the "Proposed Contract" section (or as a design note).

---

### Summary

| Area | Verdict |
|---|---|
| Contract scope & safety boundary | ✅ Clean |
| Non-goals | ✅ Disciplined |
| Acceptance tests 1–4 | ✅ Concrete, automatable |
| Acceptance test #5 | ⚠️ Needs tightening to measurable oracle |
| Benchmark/metrics for the "quick win" | ❌ Missing — cannot verify the win |
| Correctness regression coverage | ⚠️ 3 missing tests |

**Bottom line:** APPROVE with findings 1 and 2 as pre-implementation blockers (specify acceptance test #5's oracle and add benchmark criteria); finding 3 as should-fix before first commit. The contract is solid — these are completeness gaps, not design flaws.
  · 19962 tok · in 18114 (17152 cached / 962 new) · out 1848 (909 reasoning) · ¥0.0050 · cache prefix changed: tools
