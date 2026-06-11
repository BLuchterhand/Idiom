[2m  ▎ thinking[0m
  · 17026 tok · in 16850 (16640 cached / 210 new) · out 176 (32 reasoning) · ¥0.0009
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
## REVIEW: Prefetch Model-Facing Excerpt Intent

### Summary: APPROVE with findings

The proposal is **north-star aligned** in its core design: the harness owns the excerpt contract (cap, truncation reporting, follow-up note, telemetry), and the model retains full strategy freedom through an unchanged `read_file` tool. No contract creep into routing, gates, child-agent exclusion, or search tools. The scope boundary (synthetic prefetch only) is clean.

---

### Finding 1 — "too_large" skip vs. excerpt intent (unresolved design tension)

The proposal says:

> emit a line-numbered excerpt capped by a *new* model-facing prefetch limit

But the current `read_prefetch.go` (line 142–143) **skips files entirely** when `info.Size() > limit` — where `limit == maxBytesPerFile` (default 64KB). The "too_large" skip is a pre-filter, not a truncation. If the intent is to replace this skip with a read-and-truncate-to-excerpt, it should say so. If the intent is to keep the skip, then files > `maxBytesPerFile` get **zero** prefetch evidence — the model can't even see there's a file to follow up on. That's weaker than the current approach in some cases and should be explicitly justified or remediated (e.g., always attempt an excerpt, with the skip only for truly pathological files beyond a hard cap).

**Recommendation**: Resolve in the proposal: does the new model-facing limit replace the "too_large" skip (read up to `maxBytesPerFile`, show only the excerpt) or sit on top of it (keep the skip, only truncate the narrow window between the new limit and `maxBytesPerFile`)?

---

### Finding 2 — Missing explicit `read_file` follow-up note

The proposal requires:

> append an explicit model-facing note that a bounded `read_file` follow-up is available for omitted ranges

This is a **north-star positive** — it surfaces a model-controlled tool — but the current implementation has no such note in the summary (lines 168–172 of `read_prefetch.go`). The proposal should make this a hard acceptance criterion for any implementation. Without it, the model may silently treat a truncated excerpt as complete, defeating the strategy-preservation goal.

**Recommendation**: Require the follow-up note in the `ToolResult.Summary` (or as an appended paragraph) before this is considered implemented.

---

### Finding 3 — Relationship between the two limits is underspecified

The current code has one per-file byte limit (`PrefetchMaxBytesPerFile`, 64KB). The proposal introduces a second, smaller "model-facing prefetch limit." Three scenarios exist and should be stated:

| File size | Current behavior | Proposed behavior? |
|---|---|---|
| < new limit | Full content, no truncation | Same |
| new limit – `maxBytesPerFile` | Full content, no truncation | **Read fully, show only excerpt** |
| > `maxBytesPerFile` | Skipped ("too_large") | Depends on Finding 1 |

Without specifying the second limit's value and its relationship to `maxBytesPerFile`, implementors don't know whether to (a) add a new config field alongside the existing ones, or (b) lower the existing per-file byte budget for prefetch specifically. The proposal should name the new constant or config key and state whether it replaces or supplements the existing per-file limit.

---

### Finding 4 — No guard against silent-excerpt-as-complete (minor)

The truncation metadata says `" (truncated)"` in the summary, but the model could treat a truncated excerpt as the complete file. Finding 2's follow-up note is the primary mitigation. Consider also including original total line/byte counts *in the output itself* (not just the summary) when truncated, so the model sees concrete evidence of omission even if it ignores the summary line.

**Recommendation**: Add original total lines to the output footer when truncated (not just the summary), e.g. a trailing line like `-- file has N total lines; truncated to M --`.

---

### Actor-boundary verdict

**Clean.** The harness decides:
- When to prefetch (read-only lanes, non-child agents, no existing evidence)
- The excerpt cap
- Truncation reporting
- The follow-up note
- Telemetry schema

The model receives cheaper initial evidence and freely decides whether to follow up with `read_file`. No harness judgment is substituted for model strategy. No contract reaches into routing, gates, or other actor boundaries.

**Overall: APPROVE with the four findings above resolved before or during implementation.**
  · 25333 tok · in 21622 (17152 cached / 4470 new) · out 3711 (2717 reasoning) · ¥0.0122 · cache prefix changed: tools
