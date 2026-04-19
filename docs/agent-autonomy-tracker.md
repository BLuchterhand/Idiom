# Agent Autonomy Tracker

## Goal

Move the harness from fixed-step completion to model-decided completion with harness-verified readiness. The agent should keep working until it returns a no-tool final answer that satisfies the lane contract, or until a safety control stops a stalled or risky run.

## External Baseline

- OpenCode allows agent `steps` to be unset, in which case the agent continues until the model chooses to stop or the user interrupts.
- Claude Code's public Agent SDK loop repeats model decisions and tool results until the model produces a response with no tool calls. Turn and cost budgets are optional controls, not the definition of done.
- Both systems separate autonomy from safety through permissions, loop/stall controls, task tracking, and context compaction.

## Design Principles

- Completion is model-owned: a no-tool answer is the agent's claim that it is done.
- Acceptance is harness-owned: the lane done gate decides whether that final answer satisfies the task contract.
- Step limits are safety fuses, not task sizing. Large implementation work can run without a fixed step cap while still respecting no-progress, permission, timeout, and context controls.
- Progress is measured by useful evidence, edits, verification, or narrower state. Repeated failures and repeated identical actions count against a stall budget.
- Complex tasks should maintain an explicit task ledger and should not finish while required work remains pending.

## Implementation Plan

- [x] Add a runner policy where `MaxToolSteps <= 0` means no fixed step cap.
- [x] Add a no-progress safety fuse independent of total step count.
- [x] Add a generic completion gate so lanes can reject premature final prose and force the model to continue.
- [x] Wire implementation mode to model-decided completion for large structural requests.
- [x] Reject implementation final answers when no edit/no-op/blocker evidence exists.
- [ ] Add a first-class task ledger tool for complex multi-step work.
- [ ] Add context compaction for long-running implementation turns.
- [ ] Add cost/token budget accounting once token usage is available from the local backend.
- [ ] Add UI/debug snapshots for progress, done-gate rejection, and stall fuse decisions.

## Acceptance Criteria

- Small implementation tasks still finish quickly and keep bounded defaults.
- Large structural tasks can continue without a hard tool-count cap.
- Premature prose after planning/discovery is rejected unless it includes a real no-op or blocker.
- Repeated identical actions are rejected and converted into verification or a concrete blocker.
- Full test suite passes after every harness change.
- Runtime smoke scenarios prove the model can decide when to finish after real edits and verification.
