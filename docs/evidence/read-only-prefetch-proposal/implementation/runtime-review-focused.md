[2m  ▎ thinking[0m
  · 17065 tok · in 16905 (16768 cached / 137 new) · out 160 (10 reasoning) · ¥0.0008
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scope-notes.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scoped.diff"}
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
[2m  ▎ thinking[0m
Let me examine the actual implementation files more closely to understand what metadata and events are produced.
  · 29198 tok · in 28644 (13312 cached / 15332 new) · out 554 (360 reasoning) · ¥0.0167 · cache prefix changed: tools
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch_test.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_answerability.go"}
  · tool output truncated: 15834 of 48602 bytes elided
[2m  ▎ thinking[0m
Now let me check the benchmark summary code to see if there's any prefetch integration:
  · 46031 tok · in 43628 (29184 cached / 14444 new) · out 2403 (2316 reasoning) · ¥0.0198
  -> grep {"pattern": "prefetch|Prefetch|EstimatedRoundTripsSaved", "path": "internal"}
[2m  ▎ thinking[0m
Let me check whether benchmark summaries consume the prefetch event:
  · 51833 tok · in 51656 (45952 cached / 5704 new) · out 177 (31 reasoning) · ¥0.0070
  -> grep {"path": "internal", "pattern": "EstimatedRoundTripsSaved|prefetch_hit|prefetch_byte|prefetch_skip"}
  -> grep {"path": "internal/runtime/agent", "pattern": "emitCompletionStatus|completionStatus"}
[2m  ▎ thinking[0m
Now I have all the evidence needed. Here is my formal review.

---

## Review: Read-Only Named-Path Evidence Prefetch

### Verified Facts

1. **Event payload**: The implementation emits a `readOnlyPrefetchEvent` (role `read_only_named_path_prefetch`) with these fields: `schema_version`, `eligible`, `miss_reason`, `requested_paths`, `selected_paths`, `skipped_paths` (path + reason), `duplicate_paths`, `bytes`, `lines`, `truncated_files`, `estimated_round_trips_saved`. All numeric fields use `omitempty`.

2. **Source attribution**: `readOnlyEvidenceItem.Source` distinguishes `"prefetch"` from `"read_file"` in the evidence ledger (`read_only_answerability.go:285-291`). The item-level `Source` field is preserved through dedup: a prefetched path and a later model-initiated `read_file` of the same path both appear in the ledger with their respective sources.

3. **Evidence pipeline integration**: `concreteSourceEvidenceResult` (`runner.go:13228-13231`) treats `ToolNamePrefetch` identically to `read_file`. The `plan_then_execute_guard.go:1763` does the same.

4. **Policy parity**: `prefetchReadFile` (read_prefetch.go:112-172) shares `resolveReadTarget` with normal `read_file` for path resolution, protected-path checks, symlink handling. Exclusion categories: `resolve_failed`, `excluded_logs`, `excluded_generated`, `directory`, `stat_failed`, `too_large`, `binary`, `read_failed`, `max_files_exceeded`, `max_bytes_exceeded`.

5. **Route guard**: Prefetch fires only on read-only direct-answer lanes, with checks for: existing evidence present, env kill switch, direct-answer surface availability, and surface capability advertisement. For write lanes a metadata event is still emitted with `eligible=false`, `miss_reason="not_read_only_direct_answer"`.

6. **Caps**: Defaults of 6 files, 160KB total, 64KB per file — all configurable via `PrefetchReadOptions`.

7. **Dedup**: Duplicate paths are counted (`DuplicatePaths` counter) but only one result is produced.

8. **EstimatedRoundTripsSaved formula**: `len(report.Results) - 1`, floor clamped to 0. Conservative: a single prefetched file reports 0 saved; 3 files report 2 saved.

9. **Tests**: Cover inclusion, exclusion (log/binary/large), policy parity, write lanes blocked with metadata, dedup with source preservation, surface-gate eligibility.

---

### Findings

**Q1: Does the implementation provide enough metadata to evaluate benchmark drift without raw transcripts?**

**Partially yes, with a gap.**

The `readOnlyPrefetchEvent` is structurally rich enough to answer the prefetch-layer questions: "Did prefetch fire? What did it fetch? What was skipped and why? How many round trips did the formula estimate? How many bytes and lines were injected?" The event flows through the standard `RunEvent` / telemetry pipeline, so it is recorded in conversation JSONL logs.

However, the event alone **cannot** answer the cross-layer drift question: "Did prefetch actually reduce model round trips / latency / token usage, and by how much?" That requires correlating the prefetch event with the run's completion status event (which records model call count, tool call count, prompt/completion tokens, wall time). The prefetch event does not embed or reference these sibling metrics. An observer parsing telemetry logs must cross-reference two event types to answer the drift question — possible, but it's a join operation, not a self-contained record.

Furthermore, none of this data is wired into **benchmark summary aggregation** (e.g., `summary.json`). The proposal's Rollout step 4 calls for "benchmark summary fields for prefetch hit count, skipped count, prefetched bytes, dedup count, and estimated eliminated round trips." These fields do not appear in any benchmark pipeline in the scoped diff or the wider codebase. A future observer must parse raw telemetry logs to extract prefetch metrics — partially defeating the "without raw transcripts" clause.

The scope-notes.md does not list benchmark integration as in-scope, so this is a **proposal-level drift-expectation gap** rather than an implementation-scope gap. The metadata exists; the aggregation layer does not yet consume it.

**Q2: Should it reduce hosted DeepSeek model round trips for explicit named-file prompts?**

**Yes, the mechanism is sound and should reduce round trips.**

The causal chain is:
1. `pathparse.MentionedWorkspacePaths` extracts explicit file references from the objective.
2. `Workspace.PrefetchReadFiles` resolves each path through the same `resolveReadTarget` policy as `read_file`.
3. Synthetic `ToolResult` entries (with `Tool: "prefetch"`) are injected into the collected results before the first model turn.
4. The model sees the prefetched file content in its initial context, removing the need to emit a `read_file` tool call for those files.

For the benchmark case in the proposal (9 `read_file` calls in serial), this eliminates the first N model round trips where N is the number of explicitly named files within the cap. The `EstimatedRoundTripsSaved` formula (`len(results) - 1`) is conservative — it counts N-1 for N prefetched files, whereas the actual savings could equal N (if the model answers in one turn instead of N reads + 1 answer). Under-counting is acceptable for a metric labeled "estimated."

**Additional finding: `EstimatedRoundTripsSaved` derivation is undocumented.**

The formula `len(results) - 1` is not explained in comments or the event schema. A future reader comparing a run with 3 prefetched files and seeing `estimated_round_trips_saved: 2` may misinterpret the 1-file gap. The formula is conservative, but its derivation should be documented — either inline or in the schema version changelog — so drift analysts know whether to treat the number as a lower bound, a median expectation, or a formula-based estimate.

**Additional finding: No `actual_model_calls` or run reference in the event.**

The event cannot be matched to the run's model call count without cross-referencing the timeline. Adding a `run_model_calls` or embedding the prefetch event's position relative to the completion status event would make drift analysis a single-query operation on the telemetry log rather than a cross-referencing exercise.

---

### Required Fixes

These are not blocking for the scope defined in scope-notes.md, but they are necessary before the proposal's drift-evaluation expectations (final bullet of Drift Checks) can be satisfied:

1. **Document `EstimatedRoundTripsSaved` derivation** — Add a comment in `read_prefetch.go` or the event struct explaining the formula (`len(results) - 1`), that it is conservative, and under what conditions actual savings exceed the estimate.

2. **(Follow-up, not in scope)** Wire prefetch event fields into benchmark summary aggregation — The proposal's Rollout step 4 is not implemented. The raw data exists but is not surfaced in `summary.json` or equivalent.

---

### Benchmark Expectations

When the proposal's integration checks are run against this implementation:

| Metric | Expected | How to observe |
|---|---|---|
| Model call count | Decreased for explicit named-file prompts | Compare against baseline without prefetch |
| Tool call count | Decreased (fewer serial `read_file` calls) | Compare against baseline |
| Prompt tokens | Slightly increased (prefetched content bytes) | Telemetry; prefetch event records `bytes` |
| Wall time | Decreased for read-heavy prompts | Completion status event |
| Answer correctness | Unchanged (prefetch is transport-only) | Standard answer-quality eval |
| Prefetch hit/skip/bytes | Present in conversation telemetry | `read_only_named_path_prefetch` event |
| Benchmark summary fields | **Not present** — not yet wired | — |
| `harness_audit` prefetch event | Present, without raw file bodies | `harness_audit` output |
| No `answer_objective_unmet` regressions | Expected | Standard eval |
| No `Selected-log scope enforced` regressions | Expected | Standard eval |

Key gap: benchmark summaries will **not** contain prefetch fields until the follow-up integration is done.

---

### Final Verdict

**approve-with-changes**

**Rationale:**
- The implementation correctly solves the core problem: injecting explicit-named-path evidence before the first model turn, using shared workspace policy, with clean source attribution and dedup.
- The metadata event (`readOnlyPrefetchEvent`) is well-structured and sufficient for evaluating prefetch-layer behavior from telemetry logs.
- The mechanism is isolated to read-only direct-answer lanes, with appropriate guards and exclusions.
- The proposal's drift expectation — "Telemetry/audit metadata is sufficient to RCA future benchmark deltas without reading raw transcripts" — is met at the raw telemetry level but **not yet** at the benchmark-summary aggregation level. That is a separate integration step.
- The `EstimatedRoundTripsSaved` formula should be documented inline to prevent misinterpretation by future drift analysts.

**Changes required before the commit ships (non-blocking but strongly recommended):**
1. Add an inline comment explaining the `EstimatedRoundTripsSaved = len(results) - 1` derivation (conservative lower bound).
2. Explicitly note in scope-notes.md or commit body that benchmark summary integration (proposal Rollout step 4) is deferred as a follow-up.

The implementation is otherwise sound. I would not block the commit on these items — they are documentation and scope-clarity concerns, not correctness or safety issues.
  · 57556 tok · in 53951 (51712 cached / 2239 new) · out 3605 (1491 reasoning) · ¥0.0105
