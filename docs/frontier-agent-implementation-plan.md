# Frontier Agent Implementation Plan

## Objective

Bring idiom closer to frontier coding-agent harness behavior: the model decides how to work and when it is done, while the harness verifies readiness, preserves state across long runs, enforces safety, and produces auditable logs.

This plan is grounded in the current repo state and the public behavior patterns documented by OpenCode and Claude Code:

- OpenCode supports agents with role-specific prompts, models, permissions, subagents, automatic compaction, and optional max steps. If steps are unset, the agent continues until the model stops or the user interrupts.
- Claude Code's Agent SDK describes an agent loop that streams assistant turns, tool results, and a final result containing success/limit state and usage. Its todo tracking is automatically used for complex multi-step work and user-provided task lists.

The implementation target is not to clone either system. The target is the same frontier property: open-ended model decisioning constrained by explicit, observable harness contracts.

## The Capabilities

### 0. Hybrid Intent Classifier And Control Plane

Problem: A frontier harness should not make fuzzy task-routing decisions solely through brittle keyword precedence, and it should not leave safety-critical actions to the main model's judgment. The correct split is model-style semantic intent for task understanding, with deterministic host rails as vetoes, fallbacks, and audit records.

Implementation:

- Add a structured intent decision attached to every prompt execution plan.
- Record intent, selected lane, follow-up lane, edit intent, plan-only intent, verification intent, risk, confidence, semantic signals, vetoes, user-stated boundaries, and fallback guidance.
- Add a selective model-backed intent verifier for ambiguous, low-confidence, high-risk, boundary-constrained, or internally conflicting host decisions.
- Reconcile verifier output through a host-owned safety gate: the verifier can improve semantic lane choice, but it cannot bypass plan-only, read-only, verification-only, or user-boundary vetoes.
- Treat plan-only, read-only, verification-only, and user-boundary signals as control-plane vetoes against accidental mutation.
- Carry the structured intent into route ambiguity logs and deterministic control rails.
- Carry classifier `target_refs` through reconciliation, architecture evidence bootstrap, and final-answer rails so follow-up prompts such as "this file" cannot drift to unrelated hotspots.
- Log `intent_verifier` and `intent_reconciliation` records so every second-opinion model call is auditable.
- Enforce user boundaries such as "don't push" and "wait for review" before shell release actions.
- Convert rails into a declarative permission policy for runner tool decisions where practical, while retaining specialized pre-execute guards for path and mutation semantics.

Tests:

- Plan-only refactor prompts expose a plan-only veto and do not route to implementation.
- Mixed failure-fix prompts trigger the verifier and keep implementation semantics when the user explicitly asks to fix.
- Verifier attempts to override a plan-only veto are rejected by reconciliation.
- Verifier-added user boundaries become enforced rails.
- User no-push boundaries are captured in rails and block `git push`.
- Direct conversation rails deny shell execution through declarative permission policy.
- Log/debug route snapshots include intent, risk, confidence, and vetoes for post-run diagnosis.
- Conversation-coherence evals can require every user turn to produce a logged assistant turn.
- Architecture follow-ups preserve classifier target refs and fail rails if the answer/evidence drifts away from the target.

Acceptance:

- Determinism is used for safety and auditability, not as the only source of task intelligence.
- Ambiguous semantic routing has an inspectable structured decision instead of a hidden keyword outcome.
- User-stated boundaries become enforceable action blocks until explicitly lifted.
- Captured logs that omit assistant replies or lose a follow-up target become regression failures instead of subjective review findings.

### 1. Task Ledger And Done Gate

Problem: The runner can reject premature final prose, but it does not yet give the model a durable work plan that the harness can inspect. For complex requests, "I am done" should be checked against a model-maintained ledger, not just changed paths.

Implementation:

- Add a runner-handled `task_ledger` tool.
- Support Claude-style `todos` payloads with `pending`, `in_progress`, `completed`, `blocked`, and `cancelled` states.
- Keep the ledger in runner state, not in workspace files.
- Require at most one `in_progress` item for a single runner.
- Treat unfinished `pending` and `in_progress` items as completion blockers.
- Allow `blocked` only when the final answer explicitly reports the blocker.
- For large structural implementation requests, require the model to create a ledger before finalizing.
- Emit ledger updates through normal tool-result logging so debug logs and replay tooling can see the state transitions.

Tests:

- Runner accepts final only after required ledger items are completed.
- Runner rejects final when required ledger is missing for complex work.
- Runner rejects malformed ledger payloads and multiple active items.
- Implementation prompt includes task-ledger policy for structural requests.

Acceptance:

- A complex refactor cannot answer "done" while its own required work items remain unfinished.
- A true no-op or blocker can still terminate with a clear terminal reason.
- Small implementation tasks are not forced to use a ledger.

### 2. Context Compaction Inside Long Runs

Problem: App-level chat history compaction exists, but the runner loop still grows raw tool history during a long implementation turn. Long structural work needs compaction that preserves the active objective, ledger, changed paths, verification evidence, and recent blockers.

Implementation:

- Add `RunPolicy.MaxContextMessages` and `RunPolicy.MaxContextChars`.
- When thresholds are crossed, compact older runner messages into one system summary.
- Pin the immutable system prompt, original user request, active task ledger, changed paths, successful verification results, and recent errors.
- Keep the last few assistant/tool exchanges verbatim for local continuity.
- Emit `context_compaction` snapshots with retained count, dropped count, pinned paths, ledger status, and strategy.

Tests:

- Long runner histories are compacted before the next model call.
- The compacted prompt contains objective, ledger, changed paths, and recent blocker.
- The latest tool exchanges remain verbatim.
- Compaction is not triggered for normal short turns.

Acceptance:

- Long implementation runs remain within practical context without losing state.
- A model cannot forget pending work solely because old messages were dropped.

### 3. Dynamic Budget And Stop Accounting

Problem: We removed the hard step cap for structural work, but stop reasons are still partial. Frontier harnesses need explicit time/model-call/token/tool budgets, with "limit hit" distinguished from "model chose done".

Implementation:

- Extend `RunPolicy` with optional `MaxModelCalls`, `MaxDuration`, and eventually `MaxInputTokens`/`MaxOutputTokens` once backend usage is available.
- Track model calls, tool calls, successful progress turns, rejected actions, compactions, and final stop reason.
- Emit a runner-level `budget_ledger` snapshot at completion.
- Use limits as fuses, not sizing defaults: large implementation work remains model-decided unless a fuse trips.
- If a fuse trips, the final prompt must force explicit budget exhaustion and unresolved gaps.

Tests:

- Budget ledger records model calls, tool calls, progress count, completion-gate rejections, and stop reason.
- Unbounded structural policy stops because the model finalized when gates pass.
- Duration/model-call fuses stop with budget-exhausted language.
- No-progress fuse still overrides unbounded loops.

Acceptance:

- Logs can explain why a run stopped.
- The user can tell the difference between "done", "blocked", and "budget exhausted".

### 4. Isolated Child Work And Workbranches

Problem: OpenCode and Claude Code can delegate isolated work. idiom has analysis lanes, but implementation is still mostly a single runner. Large tasks need child work that can explore, validate, or patch in isolation without corrupting the main workspace.

Implementation:

- Add an agent-role registry: `implement`, `explore`, `review`, `verify`, and later project-defined roles.
- Add a child-runner API that can run read-only or write-enabled work with separate prompts, tool policy, ledger subset, and debug session id.
- Implement the first read-only `task` tool with delegated prompt, child agent label, read-only mode, bounded child budgets, and nested-task prevention.
- Deny write tools in read-only children through declarative permission policy rather than relying on prompt text.
- For write-enabled child work, create a git worktree under `/tmp` or configured scratch space.
- Require child outputs to return a patch summary, changed files, verification result, and confidence.
- Main runner decides whether to apply or reject child diffs.
- For parallelism, start with sequential child runs in the app architecture, then add concurrent execution only after state and log isolation are solid.

Tests:

- Read-only child cannot write.
- Read-only child can complete a bounded delegated task and return evidence to the parent runner.
- Nested `task` calls are denied inside child runners.
- Write child runs in an isolated worktree and does not mutate the main tree until accepted.
- Child result includes changed paths and verification evidence.
- Main runner can reject a child patch without changing the workspace.

Acceptance:

- Large refactors can use branch/worktree isolation.
- The model remains responsible for choosing when child work is useful.
- The harness preserves mergeability and auditability.

### 5. Declarative Safety, Checkpoints, And Hooks

Problem: Write guards and route policies exist, but safety is scattered. Frontier agents need declarative permission modes, checkpoints before risky edits, hooks for verification, and replayable decisions.

Implementation:

- Introduce a policy object that maps tools and path patterns to `allow`, `ask`, or `deny`.
- Replace ad hoc write-guard rules with policy evaluation where practical.
- Create a checkpoint before the first write in an implementation run, recording git head and dirty status.
- Add hook points: before tool, after tool, before final, after final, before verification, after verification.
- Emit debug records for policy decisions, checkpoint creation, done-gate rejection, stall fuse, and verification hooks.
- Add a replay harness that can assert policy outcomes from captured logs.

Tests:

- Permission patterns allow, ask, and deny expected tool/path combinations.
- Checkpoint metadata is captured before first write.
- Hook events are emitted in order.
- Done-gate and policy decisions appear in eval metrics.

Acceptance:

- Safety decisions are inspectable, not hidden in scattered conditionals.
- Risky work has a recovery point.
- Scenario evals can prove the agent obeyed the frontier controls.

### 6. Durable Plan Mode Parity

Problem: OpenCode-style planning is not just a routing choice. It is a durable artifact with an explicit exit path, a build handoff, and a visible verification section. That makes planning inspectable across turns instead of being reduced to a transient "think first" instruction.

Implementation:

- Add a durable plan artifact that records the objective, constraints, assumptions, open questions, proposed phases, verification section, and build-switch reminder.
- Add a machine-readable plan schema with `approval_scope` and `plan_exit` fields so the host can recover editable targets, read-only evidence, allowed new destinations, forbidden paths, and verification expectations without prose guessing.
- Treat plan mode as a read-only workflow with explicit exit or approval semantics before build-mode execution begins.
- Preserve plan artifacts in logs and tracker updates so later turns can reuse the same intent rather than reconstructing it from scratch.
- Require an explore-before-plan phase for ambiguous requests: gather evidence first, then write the plan artifact, then proceed to build only if the plan is approved.
- Include a verification section in the plan artifact that names expected checks, evidence requirements, and completion criteria.
- Emit a build-switch reminder when the agent transitions from planning into implementation so the model restates the objective and the acceptance checks before writing.
- Keep the plan artifact compatible with replay and postmortem review so the plan-to-build handoff remains auditable.

Tests:

- Plan-only requests produce a durable plan artifact and no writes.
- Durable plan artifacts include a parseable approval scope and allowed-destination list.
- Plan exit requires explicit approval or an equivalent exit signal before build mode.
- Build handoff emits a reminder and keeps the plan artifact visible in the run record.
- Explore-before-plan prompts surface exploration output before the durable plan artifact.
- Structural plans include a verification section with concrete checks and evidence.

Acceptance:

- Planning is a first-class artifact, not just a transient prompt pattern.
- The build handoff is explicit and reviewable.
- Verification is part of the plan, not an afterthought.

### 7. Typed Resume Context And Target-Role Packets

Problem: Persisted plans and long-horizon memory are useful only if the implementation handoff preserves intent precisely. Reconstructing approved edit targets, evidence files, forbidden paths, gaps, and verification expectations from prose makes the harness vulnerable to target widening and accidental edits to files that were only inspected as evidence.

Implementation:

- Add a typed resume context derived from persisted task memory.
- Carry approved edit targets separately from read-only evidence paths, allowed new destinations, forbidden paths, attempted searches, open gaps, and verification plan entries.
- Apply typed resume context directly to implementation target packets before deterministic enrichment.
- Render target packets with explicit editable primary paths, read-only evidence files, plan-approved new destinations, analogy files, forbidden files, and strategy/verification hints.
- Prime implementation evidence from editable and read-only paths without widening editable scope.
- Keep prose resume prompts for model continuity, but treat the typed context as the authoritative host contract.

Tests:

- Durable plan resumption keeps only approved targets editable.
- Plan-approved new destinations can be written without converting read-only evidence into editable source files.
- Evidence paths from prior searches remain available for read-only inspection.
- Verification intent from memory becomes an implementation strategy hint.
- Basename and symbol enrichment do not widen explicit primary targets.

Acceptance:

- A prior plan can be resumed without converting every inspected file into an editable target.
- The harness has a first-class packet it can enforce, not just text it hopes the model follows.

### 8. Target-Role Enforcement In Live Guards

Problem: It is not enough for the prompt to say a file is read-only evidence. Frontier-grade harnesses must enforce target roles at tool time so a model cannot accidentally mutate support files, analogy examples, or forbidden paths after initial target-consumption checks have passed.

Implementation:

- Add path-role helpers for readable, editable, and forbidden path checks.
- Require write tools to target editable primary paths.
- Allow read/inspection tools to target editable primary paths, read-only evidence paths, analogy paths, or allowed discovery paths.
- Block edits to read-only evidence and analogy paths.
- Block `go_move_declarations` when the source is not editable or the destination is forbidden/read-only evidence/analogy.
- Run role checks before the guard short-circuits on previously consumed targets.

Tests:

- `edit_file` against a read-only evidence path is blocked.
- `edit_file` against an analogy path is blocked.
- `edit_file` against an approved primary path is allowed.
- `read_file` against a read-only evidence path is allowed.

Acceptance:

- Target role labels are enforced structurally, not merely logged.
- The implementation lane can inspect broad supporting context without inheriting broad write authority.

### 9. Deterministic Structural-Refactor Fallback

Problem: Go outline tooling can already identify declaration batches and recommended move commands. If the model repeatedly reads or re-outlines after receiving those recommendations, the harness should convert the obvious next mechanical action instead of wasting turns or stopping with weak progress.

Implementation:

- Parse recommended `go_move_declarations` tool calls from `go_file_outline` output.
- When a full file read or repeated outline request is rejected after outline evidence, attempt a deterministic fallback to the first compatible move recommendation.
- Emit a `deterministic_tool_fallback` event before executing the synthesized tool request.
- Preserve normal permission, guard, checkpoint, execution, and result handling for the synthesized request.

Tests:

- Recommended move requests are parsed from outline output.
- A runner that asks for `read_file` after a Go outline recommendation executes the deterministic `go_move_declarations` fallback.
- The fallback creates the expected destination file and lets the model produce a final answer.

Acceptance:

- The harness can make bounded deterministic progress on mechanical refactor steps when the model stalls on an already-derived action.
- The fallback remains auditable and still passes through normal safety controls.

### 10. Route-Classifier Health And Degraded-Mode Debounce

Problem: A local small-model classifier is useful only when its operational state is visible and non-disruptive. If the classifier server is down, the harness should report degraded routing once, continue with host routing, and log recovery when the classifier becomes healthy again.

Implementation:

- Record classifier failure only when a route-classifier base URL is configured.
- Emit a `route_classifier_health` debug record for failures and recoveries.
- Send a single user-visible degraded-routing notification while the classifier remains offline.
- Clear the offline flag and emit recovery when a later classifier call succeeds.
- Auto-start a configured local classifier server on demand when the classifier endpoint is unavailable.
- Keep classifier launch settings separate from the main chat model: binary, local GGUF model, host, port, context size, thread count, GPU layers, log file, extra args, and startup timeout.
- Treat the auto-started classifier as a managed helper process and stop it during app shutdown.
- Preflight the local classifier model path before spawning so first-run setup errors are clear and no useless helper process is left behind.
- Use a schema-first JSON-only classifier prompt with enums and concrete examples for debug-log inspection, log-fix implementation, plan-only architecture, durable-plan approval, verification-only requests, and user-boundary capture.

Tests:

- Repeated classifier failures produce one UI notification.
- Classifier recovery clears the degraded state.
- Health logging does not affect deterministic host safety vetoes.
- Auto-start starts a configured local helper process, waits for model readiness, and then completes the classifier call.
- Config validation rejects auto-start without a local classifier model path.
- Missing local classifier model paths fail fast with an actionable classifier-health error before process spawn.
- Prompt tests assert the small-model classifier receives strict JSON schema, enum, and example guidance.

Acceptance:

- Local classifier outages are observable without spamming the user or blocking work.
- A configured local classifier can be launched by the harness without a separate `make classifier-server` step.
- First-run failures point at the missing model/binary/config issue instead of appearing as generic route-classifier silence.
- Routing remains safe because deterministic rails stay host-owned.

### 11. Explicit Mode And Tool-Policy Control Plane

Problem: Frontier harnesses expose deterministic control surfaces for user intent while keeping the model responsible for task execution. A user should be able to force plan/build/review/explore behavior, and logs should show which tools were available under that route rather than requiring a reviewer to infer policy from prompts.

Implementation:

- Add explicit agent modes: `auto`, `plan`, `build`, `review`, and `explore`.
- Keep `auto` as the default host/classifier route, but let `/agent plan|build|review|explore` impose a visible mode override.
- Preserve safety boundaries: build mode must not override explicit verification-only, plan-only, or read-only user instructions.
- Carry selected agent mode through route-plan logs, intent snapshots, control rails, and eval route expectations.
- Add workflow command templates for recurring high-value flows: latest-log debugging, implement-plan, verification, frontier audit, and OpenCode-style audit.
- Emit a dynamic tool-policy snapshot with allowed and forbidden tool sets derived from deterministic control rails.
- Extend eval assertions so scenarios can require agent mode and allowed-tool visibility.

Tests:

- `/agent plan` forces a read-only architecture plan route and blocks edit intent.
- `/agent build` does not override explicit verification-only/read-only prompts.
- Workflow templates resolve to concrete prompts.
- Control rails and route-plan eval parsing understand `agent_mode`/`mode`.
- Tool-policy snapshots include allowed tools and forbidden tools for the selected route.

Acceptance:

- Deterministic mode is observable, testable, and subordinate to explicit user safety boundaries.
- Recurring workflows do not depend on ad hoc prompt wording.
- Tool availability is an auditable contract, not merely an instruction hidden in the system prompt.

### 12. Typed Workflow State Machine

Problem: The harness was still too phrase-sensitive across multi-turn work. A sequence like "Discovery optimization opportunities" -> "Plan a refactor for this file" -> "Perform the refactor" should not be reconstructed from recent prose or scattered keyword checks every turn. It needs an explicit workflow object with a phase, target roles, verification expectations, and allowed transitions.

Implementation:

- Add a persisted workflow-state artifact under the app state directory.
- Track workflow kind, phase, objective, last prompt, source lane, target refs, evidence paths, editable targets, read-only evidence, allowed destinations, forbidden paths, verification expectations, and next actions.
- Start an `optimization_refactor` workflow from architecture/optimization discovery prompts.
- Preserve a generic `durable_plan` workflow kind for non-refactor durable plans.
- Advance the workflow from `discovered` to `planning` for plan-only refactor follow-ups.
- Convert durable plan artifacts into `awaiting_approval` workflow state with typed editable/read-only roles.
- Persist file fingerprints for editable targets and read-only evidence, and reject stale or legacy un-fingerprinted workflow state before build-mode resume.
- Resume approved plans through typed workflow state rather than re-parsing the prior answer.
- Route approval turns such as "perform the refactor" to implementation with workflow kind/phase visible in route-plan and control-rail logs.
- Keep negative approval language such as "do not perform yet" read-only.
- Pass workflow state to the small route-classifier prompt so semantic verification sees the active workflow context.
- Mark a resumed workflow complete only after a new execution journal records changed paths and passed verification; otherwise keep the workflow resumable with an incomplete-run next action.
- Add eval parsing and assertions for `workflow_state` log records so captured logs can verify phase transitions.

Tests:

- Discovery prompts persist a typed `optimization_refactor` workflow in the `discovered` phase.
- Plan-only refactor follow-ups remain read-only and expose workflow `planning` in route plans.
- Durable plan persistence advances workflow state to `awaiting_approval` with editable targets and read-only evidence separated.
- Approval prompts route to implementation and carry workflow `building` metadata.
- Negative approval prompts do not resume the workflow.
- Workflow state persists and reloads for the same workspace.
- Workflow state rejects stale targets and legacy state without target fingerprints.
- Generic durable plans keep `durable_plan` kind instead of being over-classified as optimization/refactor workflows.
- Completion acceptance rejects old journals, missing verification, failed verification, and control-rail gap summaries.
- Classifier routing memory includes active workflow state.
- Eval log analysis extracts workflow-state metrics and can require workflow phase, targets, and verification.

Acceptance:

- Multi-turn plan/build workflows are state-machine driven rather than merely phrase-driven.
- The implementation handoff consumes a typed workflow contract with target roles and verification requirements.
- Logs can prove whether a workflow was discovered, planned, approved, resumed, or completed.

## Implementation Order

0. Hybrid intent classifier and deterministic control plane.
1. Task ledger and done gate.
2. Durable plan mode parity.
3. Runner context compaction.
4. Budget ledger and stop accounting.
5. Child runners and workbranch isolation.
6. Declarative safety, checkpoints, hooks, and replay assertions.
7. Typed resume context and target-role packets.
8. Live target-role enforcement.
9. Deterministic structural-refactor fallback.
10. Route-classifier health and degraded-mode debounce.
11. Explicit mode and tool-policy control plane.
12. Typed workflow state machine.

This order is intentional: the ledger gives the model durable intent, compaction preserves that intent, budgets explain termination, child work expands capability, and declarative safety makes the broader autonomy acceptable.

## Quality Bar

- Every capability lands with focused unit tests and at least one integration-style runner test.
- Plan-mode parity lands as a durable artifact with explicit exit/approval semantics, a build-switch reminder, an explore-before-plan phase, and a verification section.
- All public prompts that expose a control must include the tool contract and failure behavior.
- Logs must support post-run diagnosis without reading model internals.
- The model decides actions and completion; deterministic hooks validate and preserve state rather than replacing reasoning.
- `go test ./...` must pass after each slice.
