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
- Strategy is model-owned: when multiple safe next steps are available, the harness should nudge rather than choose the exact step for the model.
- Force is harness-owned only for invariants: permissions, safety boundaries, verification contracts, completion contracts, protocol correctness, and exhausted loop/budget fuses.
- Step limits are safety fuses, not task sizing. Large implementation work can run without a fixed step cap while still respecting no-progress, permission, timeout, and context controls.
- Progress is measured by useful evidence, edits, verification, or narrower state. Repeated failures and repeated identical actions count against a stall budget.
- Complex tasks should maintain an explicit task ledger and should not finish while required work remains pending.

See [decisioning-north-star.md](decisioning-north-star.md) for the repo-wide decisioning standard.

## Implementation Plan

Historical design for the earlier frontier capability plan lives in
[archive/2026-05-historical/frontier-agent-implementation-plan.md](archive/2026-05-historical/frontier-agent-implementation-plan.md).

- [x] Add a structured intent classifier snapshot for every prompt execution plan.
- [x] Add a selective model-backed intent verifier for ambiguous, risky, boundary-constrained, or conflicting route decisions.
- [x] Add host-owned reconciliation so verifier output can refine semantics but cannot bypass safety vetoes.
- [x] Log verifier and reconciliation snapshots for auditability.
- [x] Use deterministic control-plane vetoes for plan-only, read-only, verification-only, and user-boundary constraints.
- [x] Carry intent, risk, confidence, signals, vetoes, and user boundaries into route/control-rail logs.
- [x] Enforce conversation boundaries such as "don't push" before release-oriented shell actions.
- [x] Convert control rails into declarative runner permission policies where practical.
- [x] Add a runner policy where `MaxToolSteps <= 0` means no fixed step cap.
- [x] Add a no-progress safety fuse independent of total step count.
- [x] Add a generic completion gate so lanes can reject premature final prose and force the model to continue.
- [x] Wire implementation mode to model-decided completion for large structural requests.
- [x] Reject implementation final answers when no edit/no-op/blocker evidence exists.
- [x] Add a first-class task ledger tool for complex multi-step work.
- [x] Add context compaction for long-running implementation turns.
- [x] Add dynamic budget and stop accounting for runner model calls, tool calls, progress, compactions, and stop reason.
- [x] Add isolated child-workbranch primitives for branch/worktree execution.
- [x] Add declarative permission decisions, checkpoint events, and runner safety hooks.
- [x] Add typed task-resume context so persisted plan memory carries approved edit targets, read-only evidence paths, forbidden paths, open gaps, and verification intent without re-parsing prose.
- [x] Split implementation target packets into editable primary paths, read-only evidence paths, analogy paths, and forbidden paths.
- [x] Enforce implementation target roles in the live write guard so evidence and analogy files remain readable but not editable.
- [x] Add deterministic Go structural-refactor fallback from `go_file_outline` recommendations to `go_move_declarations` when the model tries to read or re-outline instead of acting.
- [x] Add classifier health debounce and recovery logging so a down local route-classifier does not spam the UI while still surfacing degraded routing.
- [x] Add on-demand local route-classifier auto-start with dedicated binary/model/port/log/start-timeout settings and managed shutdown.
- [x] Add first-run classifier preflight and schema-first JSON prompt coverage so missing local models fail clearly and the small classifier sees explicit route examples.
- [x] Add explicit agent modes (`auto`, `plan`, `build`, `review`, `explore`) with route-plan, intent, and control-rail visibility.
- [x] Preserve explicit user read-only and verification-only boundaries even when build mode is selected.
- [x] Add workflow command templates for latest-log debugging, durable-plan implementation, verification-only checks, frontier audits, and OpenCode-style audits.
- [x] Add dynamic tool-policy snapshots that expose allowed and forbidden tools derived from deterministic control rails.
- [x] Add a read-only `task` child-runner tool with bounded child budgets, declarative read-only permissions, and nested-task prevention.
- [x] Add durable plan approval-scope schema with editable targets, read-only evidence, allowed new destinations, forbidden paths, verification expectations, and plan-exit handoff.
- [x] Carry plan-approved new destinations into typed resume context and implementation target packets.
- [x] Add regression coverage for the three-turn optimization workflow: discover optimization opportunities -> plan a refactor for a chosen file -> perform the approved refactor.
- [x] Expand plan-only and build-approval language so "plan a refactor" remains read-only and "perform the refactor" resumes the durable plan instead of starting an ungrounded new route.
- [x] Add a persisted typed workflow-state machine for optimization/refactor workflows with explicit `discovered`, `planning`, `awaiting_approval`, `building`, and `complete` phases.
- [x] Route workflow approval turns through typed workflow state, including editable targets, read-only evidence, allowed destinations, forbidden paths, and verification expectations.
- [x] Expose workflow kind/phase in route-plan and control-rail logs so reviewers can diagnose state-machine decisions.
- [x] Include active workflow state in route-classifier memory so the small classifier can classify terse approval turns with the same context as the host.
- [x] Extend eval log parsing with `workflow_state` assertions for phase, target, evidence, and verification coverage.
- [x] Gate workflow completion on a new execution journal with changed paths and passing verification instead of any nil implementation error.
- [x] Fingerprint workflow-state targets and clear stale or legacy un-fingerprinted workflows before resuming build-mode execution.
- [x] Preserve generic durable-plan workflow kind so non-refactor durable plans do not inherit optimization-refactor approval behavior.
- [x] Add token budget accounting when token usage is available from the local backend; budget ledgers now preserve prompt, completion, and total token counts. Cost accounting remains configuration-dependent because local backends do not expose pricing.
- [x] Add UI/debug snapshots for progress, done-gate rejection, and stall fuse decisions.

## Plan Mode Parity

OpenCode-style plan mode needs to be durable, explicit, and auditable. This tracker records the plan-only workflow requirements separately from the larger frontier capability list so the docs can stay aligned with implementation progress.

- [x] Document the durable plan artifact shape in the frontier implementation plan.
- [x] Add plan-exit/approval semantics to the autonomy tracker.
- [x] Add a build-switch reminder requirement to the plan-mode design.
- [x] Add an explore-before-plan phase requirement to the plan-mode design.
- [x] Add a verification section requirement to the plan-mode design.
- [x] Implement durable plan artifact storage through persisted long-horizon task memory.
- [x] Implement explicit plan-exit/approval handling in execution flow.
- [x] Implement build-switch reminders at the plan-to-build handoff.
- [x] Implement explore-before-plan execution ordering through architecture evidence gathering before durable plan persistence.
- [x] Require a verification section for structural plan artifacts.
- [x] Persist a machine-readable approval scope and plan-exit contract inside durable plan artifacts.
- [x] Resume plan-approved new destinations without widening read-only evidence into editable targets.
- [x] Add tests that prove plan-only prompts remain read-only until approved.
- [x] Persist plan/build workflow state separately from prose conversation history so a three-turn workflow can advance deterministically without target drift.
- [x] Validate persisted workflow targets against file fingerprints and require replanning when targets changed after approval.
- [x] Keep incomplete implementation attempts resumable when verification or control-rail acceptance does not prove completion.

## Acceptance Criteria

- Small implementation tasks still finish quickly and keep bounded defaults.
- Large structural tasks can continue without a hard tool-count cap.
- Premature prose after planning/discovery is rejected unless it includes a real no-op or blocker.
- Repeated identical actions are rejected and converted into verification or a concrete blocker.
- Plan-mode parity remains visible as a tracked workflow with clear documentation status and separate implementation status.
- Full test suite passes after every harness change.
- Runtime smoke scenarios prove the model can decide when to finish after real edits and verification.
