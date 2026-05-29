## Turn Evidence Panel: Pre-Implementation Reviewer Report

### Reviewers

| Reviewer | Role | Verdict |
|---|---|---|
| Curie | Incident Root-Cause Reviewer | Pass with three gaps identified |
| Dalton | Harness / Go Reviewer | Pass with cache-integration gap identified |
| Pasteur | North-Star / Architecture Reviewer | Pass with three cautions |

### Key Findings

1. **Render cache staleness (Dalton):** The `renderedMessageBody()` full-body
   cache key does not include evidence container state. Fix: include
   `turnEvidenceFingerprint()` in the cache key. Incorporated in Iteration 1.

2. **Budget data routing gap (Curie):** `budget_ledger` run events are
   fire-and-forget into `sessionAgg`. Fix: emit `budgetLedgerMsg` from
   `logRunEvent`. Incorporated in Iteration 1.

3. **Audit kind mapping implicit (Curie):** The evidence group taxonomy
   remapped `historyAuditKind` without defining the mapping function.
   Fix: explicit `evidenceGroupForAuditKind()` function. Incorporated.

4. **Duplicated authority risk (Dalton):** `turnEvidence` and
   `historyEntry.toolCalls` feed from separate code paths. Fix: both feed
   from `recordToolTraceInHistory()`. Incorporated.

5. **Tool-name information loss (Pasteur):** Generic display names lose
   backend routing visibility. Fix: add language-tag suffixes. Incorporated.

6. **Detail-mode redundancy (Pasteur):** Expanded evidence rows would
   duplicate inline tool messages. Fix: collapsed summary only in detail
   mode. Incorporated.

### Resolved Design Questions

| Question | Resolution |
|---|---|
| #4: source_sanity as verification? | Yes, resolved: source_sanity counts as verification evidence |

### North-Star Assessment

All three reviewers agree: north-star risk is low. The design is a pure
viewport concern. No prompt text, model-visible contracts, lane behavior
changes, or runner modifications. It makes the harness more observable without
changing what it enforces.

The design explicitly distinguishes observation ("the harness reports facts")
from strategy narration ("the model did the talking") and uses contract
language rather than chat language.

### Verdict

Design approved for implementation. All three reviewers pass. Six concrete
findings identified and addressed in Iteration 1. No blocking issues remain.

### Second-Pass Boundary Review (2026-05-28)

Conducted a line-by-line boundary audit of the Iteration 1 design against the
actual codebase. Ten gaps found, all closed in Iteration 2.

| # | Gap | Severity | Status |
|---|---|---|---|
| 1 | Splice delimiter fragility — splitting glamour-rendered body on ANSI-styled `"Qwen"` label would fail on code blocks containing "Qwen" | High | Fixed: Replaced with iterative message-by-message rendering, no post-hoc string splitting |
| 2 | Past-turn freeze impossible with current architecture — `turnEvidence` cleared means no containers render on next `renderMessages` call | High | Fixed: Added `pastTurnContainers` slice on Model storing frozen container strings by turn index |
| 3 | Contradiction: Integration Points says `recordToolTraceInHistory()` "unchanged" but Drift Prevention says it "gains a side effect" | Medium | Fixed: Integration Points updated |
| 4 | Clearing point incomplete — only mentions `handleKeyPress` Enter, not `/plan-execute`, `/implement`, `/feature-planner` | Medium | Fixed: Clearing now at top of `handleKeyPress` before any dispatch |
| 5 | `budgetLedgerMsg` arrival timing — may arrive after `streamDoneMsg` | Medium | Fixed: Summary shows `—` when budget unavailable; `handleBudgetLedger` updates cached container |
| 6 | Glyph resolution unspecified — no mapping from `outcome` string to `✔ ✘ ◌ …` | Medium | Fixed: Added `resolveEvidenceGlyph()` function |
| 7 | Verification tool list unspecified — implementor doesn't know which tools count | Low | Fixed: Explicit list: `shell` (verified) + `source_sanity` |
| 8 | Lane detection unspecified — implementor doesn't know which field determines lane type | Low | Fixed: `m.planExecuteActive` + `m.forcedTaskFamily` |
| 9 | Tab-order and focus model underspecified | Low | Deferred: Container is viewport element, not focusable component in MVP. Expand/collapse via viewport-position-aware Enter |
| 10 | Open question #4 listed as unresolved while iteration log says resolved | Low | Fixed: Removed from questions, moved to resolved in iteration log |

### Verdict Post Iteration 2

Design is now implementation-ready. Every boundary gap is closed. An implementor
reading only this document can build the feature without guessing at behavior at
any event, state, rendering, or mode boundary.
