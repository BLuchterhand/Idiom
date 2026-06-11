[2m  ▎ thinking[0m
  · 17201 tok · in 16945 (16640 cached / 305 new) · out 256 (17 reasoning) · ¥0.0011
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
  -> read_file {"path": ".idiom/benchmark/.idiom/benchmark/conversations/20260611-154038.422.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-154037-large-fixcheck/summary.json"}
  -> read_file {"path": "docs/decisioning-north-star.md"}
  -> read_file {"path": "docs/idiom-maintenance-best-practices.md"}
  · tool output truncated: 326202 of 358970 bytes elided
[2m  ▎ thinking[0m
# Incident Root-Cause Review: Read-Only Named-Path Evidence Prefetch Proposal

## Verified facts

1. **Idiom's run was correct but slow**: The conversation log `20260611-154038.422.jsonl` and `summary.json` both confirm Idiom produced a valid, grounded answer with `answer_succeeded: true`, 15 model calls, 14 tool calls, 44.47s wall time, and 340,538 prompt tokens. The tool sequence was serial: 9 `read_file` + 4 `code_navigate` + 1 `search_content` — essentially one model round trip per evidence step.

2. **Reasonix's run was faster but failed**: Summary.json shows Reasonix at 19.43s with 53,053 prompt tokens, but critically **`answer_succeeded: false`**. The proposal cites Reasonix's tokens and time without acknowledging this failure.

3. **No diagnostic-log misclassification in this run**: The conversation log shows the user prompt names the file `internal/evidence/transcript/debuglog.go` — a literal path, not natural-language "debug logs". The classifier correctly routed `read_only_question` → `direct_answer` with `writer_mode: read_only`. No `selected_log` contract, no `answer_objective_unmet`, no `Selected-log scope enforced` events appear anywhere in the 260-event log. The proposal's "first issue" has **no evidence in the provided primary source**.

4. **The user prompt explicitly named 5‑6 files**: The benchmark task was "Read these files: docs/design-hosted-cache-cost-metrics.md, internal/runtime/llama/client.go, internal/runtime/agent/runner_state.go, internal/surfaces/tui/app/model_state.go, and internal/evidence/transcript/debuglog.go. In exactly 6 concise bullets, explain…" — an unusually explicit evidence set.

5. **Prompts and base.md failed to load**: Event #2 records `error_kind: "context"` with `"load baseline prompt .idiom/benchmark/prompts/base.md: open .idiom/benchmark/prompts/base.md: no such file or directory"`. The run recovered but this means the baseline prompt was missing, possibly affecting the model's initial behavior.

6. **Peak prompt token count (31,586) on final answer only**: The proposal states the final-answer call alone used a 31,586-token prompt and accumulated ~71k characters of tool output. This is consistent with the model accumulating all prior read results into each subsequent call's context.

7. **Evidence accounting was clean**: Final-answer acceptance (event #253) and final-answer critic (event #254) both show `accept` decisions with 6 evidence paths, correctly linking to source-body reads the model performed.

## Inferences

**The serial transport inefficiency is real.** The model read one file per round trip because the harness provided no mechanism to batch-inject explicitly named, permission-safe file content before the first model turn. Each read result then accumulated into every subsequent prompt, compounding context growth.

**The Reasonix comparison is misleading without the failure caveat.** Reasonix's `answer_succeeded: false` means its 19.43s may partly reflect early termination, incomplete coverage, or a failed answer — not simply superior transport. The proposal presents the numbers as a clean efficiency comparison.

**The diagnostic-log misclassification claim is unsupported by the provided logs.** The proposal separates it as "first issue, already fixed" but the primary evidence file shows no such failure in the current run. This may have occurred in a different benchmark iteration or a different prompt configuration; the proposal does not cite its source.

**The baseline-prompt load failure is a confounding factor.** The missing `base.md` may have caused the model to fall back to default or partial instructions, potentially influencing how conservatively it gathered evidence (more serial reads rather than batching). The proposal does not mention this error.

## Root cause

The proposal's stated root cause is reasonable but **descriptive rather than diagnostic**. Rephrasing for precision:

> The harness required one model round trip per file read when (a) the set of files to read was already explicit in the user prompt, (b) the route was permission-safe read-only, and (c) normal workspace read policy would have allowed the same reads. The harness had no mechanism to inject explicitly named, permission-verified file evidence into the initial model context, forcing serial tool-driven acquisition and compounding token growth across turns.

This is a **transport architecture gap**, not a contract failure, gate failure, or model strategy failure. The invariant that was violated is: *manifestly known evidence paths should not require N sequential model turns to transport when no safety or strategy reason prevents pre-loading.*

## Contributing factors

1. **Serial read tool surface**: The model had only single-file `read_file` and `code_navigate` — no batch-read tool. Even if the model wanted to read multiple files in one turn, the tools don't support it.
2. **Context compounding**: Each read result accumulated into subsequent prompt contexts, so 9 reads × growing prior context = ballooning tokens in later turns.
3. **Missing baseline prompt** (`base.md` not found at `.idiom/benchmark/prompts/base.md`): May have degraded initial model guidance, potentially making the model more conservative/sequential in its reading strategy.
4. **Prompt structure**: The user asked to "Read these files" then "explain" — the model interpreted this as an instruction to perform reads first, then synthesize, rather than using context tools to batch-gather evidence.

## Non-causes

- **Not a completion-gate or loop problem**: Verified — `stop_reason: model_final`, no completion-gate rejections, `budget_exhausted: false`.
- **Not a model refusal or strategy failure**: The model correctly identified its task and executed it — just serially.
- **Not a route misclassification**: The classifier correctly picked `read_only_question` → `direct_answer`.
- **Not an evidence-accounting gap**: Final-answer acceptance correctly tracked all 6 evidence paths.
- **Not a permission violation**: All reads were within workspace policy.

## Fix recommendation

**Approve with changes.** The serial prefetch proposal is the right direction — it's route-local, north-star aligned (harness owns transport, model owns sufficiency), and the safety rules are conservative. Required changes:

1. **Acknowledge the asymmetric comparison**: State that Reasonix's `answer_succeeded` was `false` and discuss the implications for comparing latency numbers. The gap between 44.47s and 19.43s is real but narrower in the read/transport dimension than the raw numbers suggest because Reasonix failed to complete the task.

2. **Tighten the root cause statement**: Replace the currently descriptive form with the invariant-level statement above: the harness had no mechanism to inject known, permission-safe file evidence before the first model turn.

3. **Add the baseline-prompt failure as a confounding variable**: Note the `base.md` load error and consider whether the fix should include a resilient fallback for missing prompts before relying on prefetch for correctness.

4. **Consider a cap interaction issue**: If the prefetch reads, say, 5 files into context, and the model then requests its own reads of the same files, does the evidence ledger double-count? The proposal says "same ledger path" but should define dedup semantics explicitly.

5. **Add a "thin answer" regression check**: Confirm that prefetched evidence does not cause the model to short-circuit its own verification — the proposal's current test plan ensures the model *can* request follow-ups but doesn't check whether the model *typically does* when prefetch is active.

## Regression tests

The proposal's test plan is reasonably comprehensive. Additions needed:

1. **Positive oracle**: A test where the model should read MORE files than prefetched (to confirm it can and does). The current tests only check that prefetch doesn't block reads, not that the model continues normal evidence gathering.
2. **Dedup test**: Prefetched paths + same paths read by model → ledger shows one evidence entry, not two.
3. **Baseline-missing scenario**: Ensure prefetch still fires correctly when the baseline prompt is missing (as in this run).
4. **Serial-behavior benchmark regression**: The proposed benchmark re-run should verify that model call count drops from 15 to ~6-8 (1 prefetch + 5-7 model-directed) and prompt tokens drop commensurately, without answer correctness regression.
5. **Negative oracle**: A mutation/plan-execute/recovery route should never receive a prefetch packet, even if the prompt names files. The proposal has this but should verify it explicitly in integration.

## North-star risk

**Low to moderate.** The proposal correctly keeps the split: harness prefetches evidence (transport), model decides sufficiency (strategy). The specific risks:

- **Risk of nudging model toward early answer**: Presenting pre-read content could implicitly signal "this is enough." Mitigation: the proposal forces no "answer now" gate after prefetch and keeps all tools available.
- **Risk of over-extraction**: A path like `internal/evidence/transcript/debuglog.go` might be prefetched when the model only needed its name for citation. Mitigation: conservative caps (6 files, bounded bytes).
- **Good model-harness boundary**: The prefetch never writes files, synthesizes answers, marks objectives complete, or blocks follow-up tools. This is clean.

The proposal avoids the north-star antipatterns listed in `docs/decisioning-north-star.md`: it doesn't use prompt magic, doesn't change strategy lanes, doesn't hide tools, and doesn't make the harness declare evidence sufficiency.

## Confidence

**Moderate-high (7/10).** The core diagnosis (serial transport inefficiency) is well-supported by the log evidence. Confidence is reduced by:

- The unsupported diagnostic-log claim (no evidence in provided source)
- The asymmetric Reasonix comparison (failed run vs successful run)
- The confounding base.md load failure not addressed
- Only one trial exists in the benchmark — no evidence this is a stable pattern

The proposal would benefit from running the benchmark again with the baseline prompt fixed and confirming the serial-read pattern persists before committing to implementation.

---

## Final verdict: approve-with-changes

**The proposal identifies a real invariant failure and the fix is north-star aligned.** The serial evidence transport gap is the correct problem to solve. The proposed prefetch stage is well-scoped, route-local, and includes appropriate safety guards.

**Required changes before implementation:**

1. Acknowledge Reasonix's `answer_succeeded: false` and discuss the asymmetric comparison.
2. Tighten the root cause to the invariant level ("no mechanism to inject known permission-safe evidence before first model turn").
3. Note the baseline-prompt load failure as a confounding factor and address resilient fallback.
4. Add dedup semantics to the evidence ledger integration.
5. Add a "thin answer" regression test (model should still gather evidence beyond prefetch).
6. Remove or provide separate evidence for the diagnostic-log misclassification claim — the provided primary source does not support it.
  · 50798 tok · in 45509 (17152 cached / 28357 new) · out 5289 (2915 reasoning) · ¥0.0393 · cache prefix changed: tools
