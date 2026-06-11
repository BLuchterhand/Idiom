[2m  ▎ thinking[0m
  · 16907 tok · in 16818 (16640 cached / 178 new) · out 89 (26 reasoning) · ¥0.0007
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
## Re-review: Prefetch Model-Facing Excerpt Intent

I read `docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md`. The document is well-structured with clear objectives, contracted behavior, and explicit non-goals. However, I found **several actionable gaps** across all three drift-risk dimensions.

---

### Scope Creep

1. **`MaxExcerptBytesPerFile` as a new config option** — The proposal adds a new workspace-layer configuration knob. The core change (truncated excerpt instead of `too_large` skip) could be achieved with a fixed ratio of `MaxBytesPerFile` or a simple constant. Adding a user-facing option expands the config surface and introduces cross-product questions (what is the default? does `MaxExcerptBytesPerFile > MaxBytesPerFile` cause confusion? where is it exposed?). This should be defended or replaced with a derived value.

2. **"Single truncation regime"** (line 27) is ambiguous — it could mean "there is exactly one truncation rule for prefetch output" (descriptive) or "consolidate N existing truncation code paths into one" (prescriptive, hidden work). If the latter is intended, that work is not scoped or estimated. If merely descriptive, the phrasing could cause implementers to take on unstated refactoring.

---

### Hidden Behavior Changes

3. **Model receives partial file content instead of `too_large`** — The spec describes this as the intended change, but the downstream effect is non-trivial. Previously the model saw a clear signal: "this file is too large, use `read_file`." After the change, it sees the preamble (imports, headers, license blocks) of a large file, which may contain no actionable logic. The model could plausibly:
   - Proceed with incomplete context (no follow-up `read_file`),
   - Issue wrong `read_file` requests (wrong ranges, wrong offset),
   - Or successfully request the missing range.

   The only acceptance test covering this (#5, line 65) uses the phrase "without gratuitous follow-up," which is too vague to operationalize as a deterministic pass/fail.

4. **The "neutral model-facing footer" is still an instruction** (line 32). The spec says it is "not a strategy instruction," but embedding language like "omitted ranges can be requested with a bounded `read_file` call if needed" is a model-facing assertion embedded in tool output. Models treat tool output text as actionable. At minimum, the footer should be verified across model versions that it does not produce spurious follow-ups or confabulated file-reading loops.

5. **`TruncatedFiles` field going live** — If any downstream telemetry consumer (dashboards, alerts, eval harness) has an invariant like `TruncatedFiles == 0` on the prefetch event, making the field live after it was inert is a breaking schema change. The intent does not mention checking for or communicating this.

6. **Missing safeguard ordering** — The proposal keeps "binary safeguards" and "generated/log" safeguards but doesn't state the order of checks. Does the excerpt budget apply before or after the binary sniff? If a binary file is also over the excerpt budget, does it get a binary-truncated excerpt or a full binary skip? The literal ordering determines actual behavior and should be specified.

---

### Missing Test Criteria

7. **No telemetry acceptance test** — The proposal makes `TruncatedFiles` live (line 34-35) but has no test that the counter increments correctly when truncation occurs.

8. **No sub-budget file test** — No acceptance test for files *below* the excerpt budget. The prefetch should return the full file unchanged. (A negative regression test.)

9. **No multi-file prefetch test** — If two files in one prefetch are both oversized, do both get truncated? Does the first consume all budget? The spec mentions "original byte count" per file in the summary but doesn't test the interaction.

10. **No error-recovery test** — What happens if the excerpt computation panics or encounters an unreadable file? Graceful degradation (fall back to `too_large`?) or propagate the error? Not specified.

11. **No zero/edge-file test** — Empty files, whitespace-only files, or files with exactly one line over the budget. The line-numbered prefix format needs these boundaries tested.

12. **No `MaxExcerptBytesPerFile` boundary test** — What if the excerpt budget exceeds `MaxBytesPerFile`? What if it's set to 0 (disables truncation entirely, or breaks something)?

---

### Verdict

**Findings — do not APPROVE yet.** The core contract is sound and the non-goals are well-drawn, but the gaps above are actionable and should be resolved before implementation. Specifically:

- **Must fix (blocking)**: #3 (vague "gratuitous" test criterion — replace with a concrete oracle like "answer contains the expected symbol evidence without reaching a `read_file` call"), #6 (specify safeguard ordering), #7 (add telemetry test), #8 (add sub-budget test).
- **Should fix**: #1 (defend or replace `MaxExcerptBytesPerFile`), #4 (footer neutrality validation), #9 (multi-file test), #10 (error recovery test).
- **Clarify (not blocking)**: #2 (what "single truncation regime" means), #5 (telemetry downstream check).
  · 21389 tok · in 18120 (17152 cached / 968 new) · out 3269 (2091 reasoning) · ¥0.0078 · cache prefix changed: tools
