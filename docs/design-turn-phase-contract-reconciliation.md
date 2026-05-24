# Turn-Phase Contract Reconciliation

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: phase 2 implemented and verified 2026-05-24; fresh replay baseline,
projection rendering migration, and parity enforcement remain staged

## Purpose

Reduce the `turn_phase_contract_contradiction` finding by reconciling every
model-facing and gate-facing surface against the existing
[design-turn-phase-contract.md](design-turn-phase-contract.md) projection.

This is a targeted follow-up, not a replacement design. The existing design
defines the turn contract shape. This effort defines how to drive the current
contradiction count down safely.

## Problem

The current behavior audit reports turn-phase contract contradictions as a
high-severity finding. That class means the model may receive one surface saying
an action is valid while another surface rejects it.

Common contradiction shapes:

- visible tools include discovery after the phase has narrowed to terminal
  closeout
- next-action state suggests a probe when completion gate requires answer,
  `submit_plan`, source-satisfied, or blocker
- recovery text says "try again" without the parser-readable terminal shape
- prompt text calls a planner failure an implementation failure
- required edit paths, evidence paths, and plan coverage are described with one
  overloaded phrase
- review-repair says `Decision: pass` while failed obligations remain

These contradictions create avoidable guard friction and make local models look
weaker than they are.

## Design Decision

Create a reconciliation pass that compares the active `TurnPhaseContract` or
model protocol projection against all model-facing surfaces generated for the
turn. In report-only mode, the pass emits parity findings. Once stable, covered
surfaces should render from the projection instead of reconstructing phase
authority.

The reconciliation pass checks consistency. It does not decide the model's
strategy and does not weaken guards.

## Surfaces To Reconcile

Covered surfaces:

- phase-specific runtime brief
- base/profile prompt additions that are attached after the brief
- visible tool list
- native or textual parser contract
- response format and tool-choice configuration
- next-action state
- failure feedback packet
- recovery card
- completion gate reason
- final-answer decorator constraints
- audit event reconstruction

For each surface, record:

- `surface_id`
- `contract_id`
- `protocol_hash`
- action classes advertised
- callable tools advertised
- terminal outcomes advertised
- invalid actions advertised
- obligation ids referenced

## Reconciliation Checks

### Tool Visibility

Every callable tool shown to the model must be allowed by the phase contract.
Every required callable action class must have either a visible tool or a
terminal response shape.

Failure examples:

- `search_content` visible during submit-plan-only correction
- `submit_plan` required but not visible
- source-satisfied terminal outcome allowed but not rendered

### Terminal Outcomes

All terminal outcomes must be listed together for the phase.

Failure examples:

- prompt allows final summary while gate accepts only `submit_plan`
- recovery mentions blocker but omits source-satisfied closeout
- review-repair can say `Decision: pass` while failed obligations remain

### Obligation Vocabulary

The surface must keep these concepts distinct:

- `required_edit_paths`: required mutation coverage
- `candidate_edit_paths`: safe optional write scope
- source-body obligations: required current-source evidence
- `submit_plan.edits[]` coverage: valid plan entry or terminal source-satisfied
  evidence
- evidence paths: observed facts, not write authority

Failure examples:

- a read path treated as a required edit path
- context packet candidates treated as mandatory coverage
- evidence paths used to authorize writes

### Parser And Transport Compatibility

The parser/transport mode must be able to express the expected action shape.

Failure examples:

- textual local backend receives native-tool-only history
- phase expects visible terminal text but response format forces hidden or
  malformed structured output
- malformed-output recovery asks for `submit_plan` while only no-tool text is
  accepted

### Recovery Consistency

Recovery text should be a restatement of the phase contract plus the specific
failed invariant. It should not introduce new authority or broaden discovery.

Failure examples:

- repeated recovery nudge teaches basic protocol that the initial lead-in did
  not state
- retry adds edit-set obligation only in prose and loses it on resume
- rejected `submit_plan` treated as consumed plan artifact

## Implementation Phases

### Phase 1: Inventory And Baseline

Implementation status: partially satisfied by current `harness_audit` behavior
output and guard-friction grouping. A fresh replay corpus is still required
before claiming contradiction-count improvement over historical logs.

Use existing audit logs and the new guard-friction baseline to group
contradictions by phase and surface pair.

Acceptance:

- top contradiction shapes are listed with redacted evidence refs
- fresh baseline corpus is named
- no code behavior changes are made

### Phase 2: Report-Only Reconciler

Implementation status: complete for current covered surfaces. The reconciler is
pure/report-only and compares turn-phase contracts with surface summaries from
the turn-phase lifecycle event, model protocol projection, next-action state,
completion gate, and recovery cards. It reports mismatches by surface pair and
invariant id without raw prompt or transcript content.

Add a pure reconciliation function that compares projection fields with
surface summaries.

Acceptance:

- tests cover clean parity and each contradiction family above
- audit can report contradiction by surface pair and invariant id
- output contains no raw prompt or transcript body

### Phase 3: Render From Projection

Implementation status: started. Model protocol cards now render explicit
visible and callable tool lists from the compiled protocol. Additional recovery
card and next-action-state rendering from the projection remains staged.

Move the most failure-prone surfaces to render from the protocol projection:
first recovery card and next-action state, then runtime brief tool summary, then
completion-gate explanation text.

Acceptance:

- behavior fixtures still pass
- contradiction count drops on replay corpus
- prompt token count does not grow materially

### Phase 4: Enforce Parity In Covered Lanes

For the pilot lane, reject or fail fast when a surface tries to advertise an
action class outside the active projection.

Acceptance:

- parity failure is a harness bug with a typed event, not a model failure
- valid surfaces remain dispatchable
- TUI, CLI, and sidecar paths agree for covered phases

## Tests

Required tests:

- plan-execute planner: `submit_plan`, source-satisfied, blocker all visible
- plan-execute planner: broad discovery hidden after readiness
- review-repair: pass blocked when failed obligations remain
- source-satisfied: no placeholder edit required
- malformed-output recovery: corrected terminal shape visible
- candidate paths do not become required paths
- parser mode and expected output shape are compatible

## Metrics

Primary:

- `turn_phase_contract_contradiction` count on fresh replay corpus
- contradiction count by surface pair
- avoidable guard friction caused by `contradictory_contract`
- repeated rejection count after recovery

Secondary:

- prompt token impact
- tool-call count after readiness
- source-satisfied short-circuit success rate
- malformed-output recovery success rate

## Non-Goals

- Do not weaken completion gates.
- Do not make recovery text choose strategy beyond safe alternatives.
- Do not migrate every surface in one patch.
- Do not treat historical contradiction count as proof of current failure
  after recent fixes; establish a fresh replay baseline first.
