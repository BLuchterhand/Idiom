# Frontier Worker Runtime Follow-Up Design

This document defines the validation surface for qwen-bubbles as a frontier-style coding-agent harness. The goal is to make each worker call informed by a compact, typed view of the current run instead of relying on static prompt text and scattered session memories.

## Scope

The runtime covers seven feature families:

1. Unified run state
2. Prompt compilation
3. Per-step instruction packets
4. Typed recovery planning
5. Typed tool manifests
6. Verification repair loops
7. Child-worker contracts

Follow-up parity additions now also cover:

- Frontier role specs for main, implementation, repair, read-only child, and isolated write child workers.
- Token usage propagation into runner budget ledgers when the backend returns usage.
- Live SWE-suite execution hooks for generating logs before scoring.
- Failed-log-to-regression-suite generation.
- Verification failure classification for targeted repair.

These features apply to every worker lane: `main`, `locate`, `explore`, `review`, `architecture`, and `implement`.

## Feature 1: Unified Run State

### Purpose

Workers should receive one authoritative state packet that summarizes the current task. This prevents stale or conflicting instructions from workflow state, task memory, structural refactor state, execution journals, target packets, and failure feedback.

### Required Fields

- `lane`: current worker lane.
- `phase`: planning, evidence, editing, verification, repair, blocked, or final.
- `objective`: user-visible task objective.
- `step`: current model/tool step number.
- `target_refs`: current editable or read-only target references.
- `evidence_paths`: paths validated by reads, searches, semantic tools, child tasks, or deterministic bootstrap.
- `changed_paths`: workspace paths already changed.
- `constraints`: active hard constraints such as guarded writes, no declaration moves, read-only lane, or verification-only lane.
- `allowed_tools`: active tool allowlist derived from control rails.
- `forbidden_tools`: active tool denylist derived from control rails and user boundaries.
- `tool_manifest`: canonical request schemas for the allowed tool set.
- `required_evidence`: evidence the answer must have before completion.
- `readiness_gate`: deterministic completion gates for the lane.
- `verification_contract`: verification expectation for the lane.
- `verification_plan`: expected verification commands when known.
- `target_packet`: compact target-packet lines for implementation workers.
- `budget`: model/tool/context budget for the worker.
- `last_tool`: most recent tool.
- `last_result`: summary/error shape of the most recent tool result.
- `failure_class`: typed failure category if the last step failed.
- `next_actions`: allowed next action classes.
- `invalid_actions`: action classes that must not be repeated.

### Validation

- A frontier unit test must render a non-empty run-state packet with lane, step, evidence, constraints, and next actions.
- Runner tests must prove later model calls receive a current-state or step-instruction packet after a tool result.
- The packet must be compact and deterministic so log diffs are stable.
- The first model call must receive an initial runtime packet before any tool request.
- Context compaction must preserve the runtime packet, target refs, evidence paths, changed paths, and budget.

## Feature 2: Prompt Compilation

### Purpose

Worker prompts should be compiled from lane contracts rather than assembled as bespoke strings in `internal/app`. Static instructions are still allowed, but their source must be frontier-owned.

### Required Prompt Sections

- Base system prompt, if configured.
- Workspace instructions, if present.
- Lane declaration.
- Task family and answer contract.
- Authority and permission profile.
- Required output shape.
- Tool-use rules.
- Recovery and stop conditions.
- Current request.

### Lane Requirements

`explore`
- Read-only.
- Must cite inspected evidence.
- Must return likely files, finding or hypothesis, confidence, and next step.

`review`
- Read-only.
- Must prefer one concrete finding over a broad weak list.
- Must cite exact candidate paths.

`architecture`
- Read-only.
- Must identify structural shape, hotspot paths, pressure points, and bounded refactor direction.

`implement`
- Must prefer smallest correct change.
- Must consume target packets before broad discovery.
- Must keep task ledger current for complex work.
- Must use verification result from harness before claiming success.
- Must obey no-move and structural-stage constraints.
- If verification feedback is supplied, must repair the failing verification before reporting success.

### Validation

- Tests must assert compiled prompts include lane, answer contract, required output, and key tool constraints.
- Existing app prompt builder tests must pass through frontier prompt compilation.

## Feature 3: Per-Step Instruction Packets

### Purpose

After each material event, the next model call should receive a compact step packet. This gives the worker immediate, current instructions instead of requiring it to infer from a long transcript.

### Required Packet Sections

- Current state summary.
- Evidence paths.
- Last failure or last result.
- Active constraints.
- Invalid next actions.
- Valid next actions.

### Validation

- Runner tests must show a step instruction packet appears in the retry/model-call history after a failed or successful tool result.
- The step packet must include the latest evidence path when available.
- The step packet must not tell the worker to repeat a failed action class.
- Step packets must merge live evidence and changed paths with the original runtime brief.

## Feature 4: Typed Recovery Planning

### Purpose

Failure handling should be classified before it is rendered to the model. The runner should not rely only on ad hoc string prose.

### Failure Classes

- `tool_parse_error`
- `permission_boundary`
- `guard_rejection`
- `completion_gate`
- `verification_unresolved`
- `repeated_action`
- `broad_inspection_loop`
- `target_scope_violation`
- `post_edit_focus`
- `child_task_failure`
- `checkpoint_failure`
- `tool_failure`
- `unknown`

### Recovery Action Shape

- `failure_class`
- `user_visible_blocker`
- `do_not_repeat`
- `required_next`
- `invalid_next`
- `valid_next`
- `state_summary`

### Validation

- Unit tests must classify common runner failures deterministically.
- Runner failure feedback packets must carry `failure_class`.
- Completion-gate failures must require satisfying the gate before final answer.
- Permission and approval failures must be marked user-visible.

## Feature 5: Typed Tool Manifests

### Purpose

Workers should see a canonical schema for the currently allowed tools. The source of truth lives in `internal/workspace/tool_manifest.go`; frontier runtime consumes that source when rendering runtime packets.

### Required Behavior

- Workspace instructions render available tools from the shared manifest.
- Runtime packets include a filtered `tool_manifest` for the active `allowed_tools`.
- Explore and child workers receive read-only tool manifests.
- The manifest includes native workspace tools, Go semantic tools, C/C++ semantic tools, git inspection tools, shell, task delegation, and task ledger.

### Validation

- Unit tests must prove runtime packets include typed tool manifest entries.
- Workspace and frontier must not maintain divergent tool schemas.

## Feature 6: Verification Repair Loops

### Purpose

Implementation workers should not stop after post-edit verification fails. The harness must feed failing verification output back into a bounded repair worker before finalizing the task.

### Required Behavior

- Post-edit verification failure is rendered as repair context.
- The repair worker receives the same target packet, control rails, tool manifest, failed commands, and failing tool results.
- Repair completion requires either a repair edit or an explicit blocker tied to the failing verification output.
- A successful repair edit triggers verification again before final summary.
- The loop is bounded to one repair worker pass per implementation run.

### Validation

- Completion-gate tests must reject repair finals without a repair edit or explicit blocker.
- Implementation tests must cover edit -> failed verification -> repair worker -> verification.

## Feature 7: Child-Worker Contracts

### Purpose

Delegated workers should not be raw prompt fragments. A child task is a bounded read-only worker with its own runtime brief, tool manifest, event stream, budget, and answer contract.

### Required Behavior

- Child workers receive a `delegated_read_only` runtime brief.
- Isolated write children receive a `delegated_isolated_implementation` runtime brief and run in a temporary git worktree.
- Child workers inherit parent context limits where configured.
- Child workers deny writes and nested tasks.
- Child tool requests/results are surfaced through parent runner events.
- Child output must answer the delegated question with concise evidence.
- Write-child output must include changed paths, verification status, confidence, and a diff for parent review; the parent workspace is not mutated by the child.

### Validation

- Runner tests must assert read-only child policy denies writes and nested tasks.
- Event tests should cover child tool request/result forwarding.

## Future Validation Matrix

| Capability | Required validation |
| --- | --- |
| Unified run state | Unit render test plus runner integration test |
| Prompt compiler | Lane prompt tests for explore, review, architecture, implement |
| Step instruction | Runner second-call history includes current-state packet |
| Recovery planner | Unit tests for parse, permission, gate, repeated, target-scope failures |
| Typed tool manifest | Runtime brief includes filtered tool schemas from workspace source |
| Verification repair | Failed post-edit verification enters bounded repair loop |
| Child workers | Child tasks receive read-only runtime brief and emit parent-visible events |
| Session hygiene | Workflow/task/resume render one coherent state packet |
| Worker quality | SWE prompt suite includes route, target, edit, recovery, verification cases |
| Performance | Eval reports track model calls, tool calls, compactions, recovery count, and stop reason |

## Completion Criteria

This follow-up is complete when:

- Frontier owns the prompt compiler, run-state packet shape, step-instruction rendering, run policy, completion policy, and recovery planner.
- App prompt builders are adapters into frontier.
- Runner emits typed recovery feedback, initial runtime briefs, and current-state step packets.
- Workspace owns the tool manifest consumed by both workspace instructions and frontier runtime.
- Implementation verification failures run through a bounded repair worker before finalization.
- Child workers use the same runtime-brief contract as top-level workers.
- Focused and full Go test suites pass.
- A self-review confirms no newly added worker policy lives only in app or agent.
