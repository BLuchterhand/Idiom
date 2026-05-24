# Contract Authority Migration Plan

Status: accepted implementation migration plan
Evidence snapshot: `context_pack` `e0a303010755de53`; `docs/decisioning-north-star.md`;
`docs/design-contract-first-local-autonomy.md`;
`docs/design-turn-phase-contract.md`;
`docs/archive/2026-05-historical/plan-execute-overhaul-proposal.md`;
`docs/live-contract-suite.md`

## Objective

Move contract authority out of scattered prompts, route strings, guard-local
state, recovery prose, and live-suite assumptions into one typed migration path.
The end state is not a larger prompt. It is a staged runner pipeline where route,
runtime policy, tool intent, path roles, obligations, recovery, verification,
and completion all reference the same autonomy contract and phase projection.

North star: the model owns strategy; the harness owns contracts. This migration
must preserve valid model choice while making hard invariants executable,
auditable, replayable, and testable.

## Current Authority Problem

Recent plan-execute and live-contract work shows that the harness already has
most of the necessary pieces: `AutonomyContract`, `TurnPhaseContract`,
`ToolIntent` events, typed planning state, runtime transport policy, obligation
events, completion gates, scripted contracts, and live rollback oracles. The
remaining problem is ownership.

Authority is still split across:

- route and lane selection,
- runtime brief construction,
- model-call transport policy,
- tool manifest filtering,
- textual/native tool parsing,
- candidate-source and required-path guards,
- autonomy obligation recording,
- recovery nudges,
- completion gates,
- live and scripted eval expectations.

The migration below makes each surface a projection of typed state before it is
allowed to enforce behavior.

## Migration Principles

- Preserve behavior first. New contracts shadow existing gates until replay and
  parity prove equivalence for the covered phase.
- Promote one invariant at a time. A guard can move only when its old decision,
  new typed decision, recovery text, and audit event agree.
- Keep candidate scope, required coverage, source evidence, selected edit
  paths, edit-set obligations, and verification obligations separate.
- Treat context packets as evidence envelopes. They may localize work, but do
  not grant write permission or mandatory coverage without typed route input.
- Prefer low-value-action nudges over hard rejections for ordinary exploration.
  Keep hard rejection for permissions, protected paths, invalid tool protocol,
  invalid plan artifacts, exhausted budgets, and completion with open required
  obligations.
- Do not use prose phrases as contract triggers. Compatibility string parsing
  may feed shadow diagnostics, but durable authority must be route metadata,
  command mode, runtime brief fields, progress state, or autonomy contract
  fields.

## Packet Sequencing

Every runner path should converge on this packet order. Later packets may narrow
or resolve earlier state, but must not invent broader authority.

1. `context_pack`: localization evidence, freshness, candidate files, packet
   id, unresolved gaps, and provenance. It is read/write neutral by default.
2. `typed_route_contract`: lane, task family, workflow kind, workflow phase,
   command mode, actor role, required coverage paths, allowed candidate paths,
   explicit forbidden paths, and terminal outcome family.
3. `runtime_policy`: backend kind, model alias, native/textual transport,
   response format, parser contract, tool choice, thinking policy, token budget,
   wall-clock budget, and live timeout.
4. `AutonomyContract`: immutable phase contract compiled from route, runtime
   brief, workspace config, packet evidence, permission profile, tool rules,
   approval policy, current runner state, and eval expectations.
5. `TurnPhaseContract`: compact per-model-call projection over the autonomy
   contract, visible tools, action classes, expressible terminal outcomes,
   hidden tools, runtime-call compatibility, evidence state, and enforcement
   mode.
6. `ToolIntent`: one normalized action request from native calls, textual tool
   syntax, or terminal output; includes action class, capability, parser result,
   confidence, source transport, and unsafe-inference status.
7. `path_roles_resolved`: normalized workspace targets, roles, freshness, and
   source of authority for every path-bearing action.
8. `tool_policy_decision`: allow, ask, nudge, reject, stop, or observe-only,
   with invariant id, recovery id, and old-guard parity state.
9. `tool_policy_result`: execution outcome, changed paths, evidence paths,
   parser/runtime facts, and obligations opened or resolved.
10. `obligation_state`: unified open/resolved/waived obligations for evidence,
    write, snippet, edit-set, verification, approval, final answer, and recovery.
11. `completion_gate_decision`: final acceptance, source-satisfied, blocker,
    review pass, or rejection, based on the current obligation state and runtime
    phase.
12. `replay_summary`: compact audit reconstruction linking route, contract,
    intents, decisions, obligations, recovery, verification, and final outcome.

The planner/executor handoff must preserve packet identity: planner packet id
to `submit_plan`, `PlanArtifact.PackID`, executor inherited packet event, and
executor autonomy contract. If a packet is missing or stale, the executor should
emit a typed miss/stale event rather than silently rediscovering scope.

## Stage 0: Inventory And Replay Baseline

Goal: know the existing behavior before moving authority.

Deliverables:

- Inventory all guard and prompt authority that affects route, allowed tools,
  next action, invalid action, recovery, verification, and completion.
- Map each guard to a stable invariant id and the autonomy/phase field that
  should eventually own it.
- Add a replay report for representative historical logs that reconstructs:
  route contract, runtime policy, turn phase, tool intents, policy decisions,
  obligations, recovery decisions, and completion gates.
- Mark each reconstructed field as `typed`, `derived_from_legacy`, or
  `prose_only`.

Acceptance criteria:

- Replay can explain at least one direct question, one implement run, one
  required-path plan-execute run, one open-candidate plan-execute run, one
  review-repair terminal run, and one product acceptance closeout.
- The report identifies every prose-only authority source that remains in the
  covered runs.
- No runtime behavior changes.

## Stage 1: Staged Replay And Parity Harness

Goal: make old and new decisions comparable before enforcement changes.

Deliverables:

- Run the new typed reducer and projection in `observe_only` for covered phases.
- For every model action, emit old decision, new typed decision, action class,
  invariant id, and divergence reason.
- Add parity fixtures for high-risk phases:
  required-source, submit-ready, stale-anchor refresh, malformed submit-plan
  recovery, review-repair terminal, implementation write-ready, verification
  ready, product acceptance closeout, and budget/loop exhaustion.
- Teach `harness_audit` to summarize false accepts, false rejects, missing
  packet sequence fields, and contradiction clusters by session and step.

Acceptance criteria:

- For covered phases, old and new decisions match for valid actions and hard
  invalid actions.
- Divergences are classified as intentional future behavior, missing typed
  input, stale packet, unsupported parser shape, or legacy prompt authority.
- No guard is removed while parity is incomplete.

## Stage 2: `ToolIntent` Normalization

Goal: runner policy consumes one typed action request regardless of native or
textual transport.

Deliverables:

- Define `ToolIntent` as the boundary object for native tool calls, textual
  tool syntax, JSON terminal forms, and visible terminal outcomes.
- Normalize safe shapes through one parser path:
  native call, fenced JSON object, bare JSON object, explicit XML/tool envelope,
  and known key aliases when safe.
- Emit parser outcomes:
  `normalized`, `missing_tool_inferred`, `unsupported_shape`,
  `unsafe_to_infer`, `requires_terminal_action`, and `transport_mismatch`.
- Preserve native tool semantics. The normalizer must not loosen native calls
  by inferring unsafe actions from narrative prose.

Acceptance criteria:

- Equivalent safe native and textual requests produce equivalent `ToolIntent`
  capability/action-class fields.
- Unsafe narrative text, mismatched tool envelopes, missing required arguments,
  and unsupported shapes are rejected or routed to typed recovery without
  executing.
- Existing `tool_intent_classified` events carry the normalized intent id and
  parser outcome.

## Stage 3: Typed Route Contract

Goal: route authority is explicit before prompt or runner text exists.

Deliverables:

- Introduce or complete a `typed_route_contract` input with lane, task family,
  workflow kind, phase, actor role, command mode, and route source.
- Carry required coverage paths, allowed candidate scope, evidence paths,
  forbidden paths, exact snippets, edit-set obligations, and terminal outcomes
  as separate fields.
- Stop promoting context-pack candidates, prompt headings, or retry prose into
  required coverage unless a typed route source says so.
- For TUI activation, record whether the route came from explicit command,
  classifier, durable artifact dispatch, or conservative conversation fallback.

Acceptance criteria:

- Plan-execute, implement, review-repair, verification, locate, and
  conversation routes can be represented without parsing magic phrases.
- Tests prove typed scope wins over prompt wording when both are present.
- Candidate scope cannot accidentally become required edit coverage.

## Stage 4: Runtime Policy

Goal: terminal contracts are paired with a model call that can express them.

Deliverables:

- Centralize runtime policy selection for native/textual tools, response format,
  parser contract, tool choice, thinking policy, max tokens, timeout, and live
  watchdog budget.
- Record backend kind, safe model alias, finish reason, token counts,
  response-format support, native/textual mode, thinking flags, and effective
  timeout in bounded telemetry.
- Make terminal plan-execute states choose one explicit expressible shape:
  native `submit_plan`, textual JSON `submit_plan`, textual correction,
  source-satisfied terminal, or typed blocker terminal.

Acceptance criteria:

- Audit flags any phase that allows `submit_plan` while the runtime call exposes
  neither native `submit_plan` nor the textual submit-plan parser.
- Length stops, watchdog timeouts, parser failures, unsupported response format,
  and loop-budget exhaustion are classified differently.
- Live timeout policy is visible in resolved config and replay output.

## Stage 5: Runner Action Pipeline

Goal: every action goes through the same host-owned policy path.

Pipeline:

1. derive `TurnPhaseContract`;
2. present prompt/tool surface from the projection;
3. normalize response into `ToolIntent` or terminal intent;
4. resolve path roles and side-effect class;
5. evaluate typed policy;
6. execute only allowed/approved actions;
7. update obligations and readiness;
8. rederive the phase contract when state changes;
9. gate completion from obligations, not prose.

Deliverables:

- Move policy calls before execution for reads, writes, shell, plan submission,
  verification, external/MCP/app tools, and terminal answers.
- Keep existing guards as backstops during migration, but log when a backstop
  fires after typed policy allowed an action.
- Convert the first simple guards only after parity:
  planner per-edit write rejection, review-repair discovery rejection,
  submit-ready discovery rejection, and stale-anchor immediate resubmit
  rejection.

Acceptance criteria:

- Every executed tool has prior `ToolIntent`, optional path roles, and
  `tool_policy_decision` events.
- Backstop-only rejections trend to zero for migrated phases.
- No valid CLI or TUI route loses an expressible terminal outcome.

## Stage 6: `AutonomyContract` Invariant Migration

Goal: hard invariants live in `AutonomyContract` and are referenced everywhere.

Deliverables:

- Move invariant ids into the autonomy contract:
  protected path, forbidden path, required coverage, planner-submit-only,
  approval required, verification after write, completion with open obligations,
  exhausted budget, and invalid protocol.
- Convert control rails, `TurnPhaseContract`, policy decisions, recovery text,
  and audit summaries to reference autonomy contract id, contract version, and
  invariant ids.
- Recompile the autonomy contract only on phase boundaries or meaningful state
  changes: successful write, verification result, approval result, route
  transition, user instruction update, context freshness change, delegated
  result, or recovery state transition.

Acceptance criteria:

- A final answer, rejection, nudge, or stop can name the invariant that allowed
  or blocked it.
- `control_rails` and `turn_phase_contract` reference the same valid
  `contract_id`.
- Hard policy decisions are reconstructable from contract fields, not prompt
  strings.

## Stage 7: Unified Obligations

Goal: readiness and completion read one obligation ledger.

Deliverables:

- Represent source-body evidence, required writes, exact snippets, edit-set
  requirements, verification, approval, final answer shape, recovery, and
  delegated handoff as typed obligations.
- Open verification obligations after successful writes unless the active
  contract explicitly skips verification.
- Resolve obligations only from source-aware evidence, valid writes, accepted
  plan artifacts, post-write verification, approved waivers, or typed blockers.
- Make source-satisfied closeout resolve required coverage without creating fake
  edits.

Acceptance criteria:

- Completion gates reject mutable-task final answers with unresolved required
  obligations.
- Skipped verification is visible as waived or blocked, not silently absent.
- Required edit paths, source-body obligations, and `submit_plan.edits[]`
  coverage appear as separate obligation kinds.

## Stage 8: Typed Recovery

Goal: recovery is a state transition, not another prompt reminder.

Deliverables:

- Define recovery states for malformed tool request, unsupported parser shape,
  invalid submit-plan artifact, stale/missing anchor, low-value action,
  protected path, approval unavailable, verification failed, completion blocked,
  and budget exhausted.
- Each recovery state must carry allowed next action classes, terminal outcomes,
  hidden tools, remaining obligations, and loop/budget counters.
- Replace broad recovery menus with state-specific contract projections:
  corrected submit, bounded source refresh, source-satisfied, typed blocker, or
  verification retry as applicable.

Acceptance criteria:

- Recovery text and tool visibility are generated from the same phase contract.
- Expected low-value exploration produces nudges and budget accounting, not hard
  runner rejections, until a loop fuse or budget requires typed blocker.
- Invalid protocol and protected-path attempts remain hard invariant failures.

## Stage 9: Scripted Replay And Live Smoke Policy

Goal: use deterministic scripted coverage for contract mechanics and live smoke
only for model-dependent behavior.

Scripted policy:

- Add or update scripted contracts for every migrated invariant before
  enforcement changes ship.
- Prefer scripted contracts for parser normalization, route contracts, runtime
  policy selection, tool visibility, path roles, obligation closure, recovery
  state, completion gates, rollback proof, and audit reconstruction.
- Scripted fixtures may assert exact event fields because the fake model is
  deterministic.

Live policy:

- Keep live contracts small and expensive. Use them for actual local-model
  prompt following, strategy, terminal generation pressure, executor handoff,
  verification behavior, and rollback under real binary/model wiring.
- Do not weaken implementation-required live fixtures to accept typed blockers.
  Add separate blocker/recovery fixtures when blocker quality is the behavior
  under test.
- Every live case must report resolved runtime policy, model/backend facts,
  effective timeout, rollback status, changed-before-reset evidence, and absence
  or presence of hard runner rejections according to its oracle.

Acceptance criteria:

- `make scripted-contracts` covers every migrated hard invariant and recovery
  state before live enforcement is enabled.
- `make live-contracts` remains a smoke bed for representative model behavior,
  not the only proof of contract mechanics.
- Replay output can explain scripted and live failures without raw transcript
  reconstruction.

## Enforcement Gates

No stage may move from shadow to enforcement until these gates pass:

- Unit parity proves old and new decisions match for the phase.
- Scripted contract proves the intended invariant and the nearest valid action.
- Audit can reconstruct the decision with contract id, invariant id, intent id,
  obligations, and recovery state.
- CLI and TUI use the same typed route/runtime fields.
- The failure message or recovery nudge does not introduce a new action class
  absent from the contract.
- Reviewer sign-off confirms the migrated rule is a harness invariant, not a
  model strategy preference.

## Rollout Order

1. Replay and parity only.
2. `ToolIntent` normalization in observe-only.
3. Typed route contract fields for plan-execute and implement.
4. Runtime policy centralization for terminal plan-execute.
5. Runner action pipeline for plan submission and write-capable tools.
6. Autonomy invariant ids for planner-only writes and required coverage.
7. Unified obligations for write, verification, final answer, and
   source-satisfied closure.
8. Typed recovery for malformed submit-plan and stale-anchor refresh.
9. Extend to external/MCP/app side effects and approval policy.
10. Broaden scripted contracts, then run the live smoke subset.

## Non-Goals

- Do not edit product workflow behavior as part of this migration.
- Do not replace OS sandboxing or imply harness checks are filesystem isolation.
- Do not introduce staged plan chunks in this migration; they remain future work
  unless separately designed.
- Do not make exact prompt wording an acceptance criterion.
- Do not remove existing guards before parity and replay prove equivalence.
- Do not expand live contracts to cover every unit-level policy branch.

## Done Criteria

The migration is complete when:

- every tool-capable turn emits a typed route contract, runtime policy,
  autonomy contract, turn phase contract, tool intent, policy decision, and
  obligation/completion events where applicable;
- every hard rejection names a contract id and invariant id;
- every final answer for a mutable task is allowed by obligation state or closes
  with an explicit blocker/waiver;
- every migrated phase has scripted regression coverage and replay coverage;
- live smoke covers direct question, explicit implement, short plan-execute,
  product-workflow implementation acceptance, and at least one typed recovery or
  blocker fixture;
- `harness_audit` summarizes contract coverage, replay gaps, unresolved
  obligations, low-value loops, hard rejections, and runtime-policy mismatch
  without parsing raw prompt prose.
