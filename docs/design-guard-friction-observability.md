# Guard Friction Observability

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: phase 1-2 implemented and verified 2026-05-24; replay baseline and
regression promotion remain staged

## Purpose

Make guard friction measurable before changing guard behavior. A guard hit is
useful only when it proves an invariant was protected. If the model was missing
the active contract, saw contradictory guidance, or could not express a legal
action through the visible tool surface, the guard hit is also evidence of a
harness alignment gap.

This effort gives audit, eval, and future implementation atoms a shared event
model for nudges, rejections, recoveries, stops, and completion-gate failures.

## Problem

Current audit findings can report important symptoms such as
`turn_phase_contract_contradiction`, `same_tool_repeat`,
`named_gap_read_walk`, `rejection_retry`, and
`completion_gate_reopened_discovery`. Those findings are enough to prioritize
work, but not enough to answer the first-cause question consistently:

- Did the model see the contract before the invalid action?
- Were safe alternatives visible and expressible?
- Did another prompt, recovery card, or tool description contradict the gate?
- Did retry succeed after one nudge?
- Was the event a real invariant block or avoidable contract friction?

Without those answers, the project risks adding another prompt reminder or
another guard when the right fix is contract projection, tool-surface parity, or
runtime-state logging.

## Design Decision

Introduce a bounded `guard_friction` event stream for consequential guard
interactions. The event should be emitted when the harness nudges, rejects,
stops, recovers, blocks, or reopens a model action because of an active
contract, policy, completion, verification, loop, parser, or tool-surface rule.

The event is diagnostic. It does not decide behavior. Existing guards remain
authoritative until later implementation atoms explicitly migrate them.

## Event Shape

Minimum event fields:

```json
{
  "schema": "idiom.guard_friction.v1",
  "session_id": "20260524-example",
  "step": 4,
  "lane": "plan-execute",
  "phase": "planner",
  "contract_id": "contract-123",
  "contract_version": 2,
  "protocol_hash": "protocol-abc",
  "invariant_id": "completion.submit_plan_or_terminal",
  "obligation_id": "required_edit_paths.coverage",
  "subject": "tool_call|final_answer|malformed_tool_call|route_transition",
  "decision": "nudge|reject|stop|recover|block",
  "guard_surface": "completion_gate|tool_policy|parser|recovery|runner_loop",
  "first_invalid_action": "search_content",
  "visible_action_classes": ["submit_plan", "source_satisfied", "blocker"],
  "visible_tools": ["submit_plan"],
  "safe_alternatives": ["submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"],
  "model_contract_visible": true,
  "contradictory_surface_seen": false,
  "retry_number": 0,
  "retry_outcome": "not_applicable|success|same_failure|different_failure",
  "avoidability": "unknown|real_invariant|contract_not_visible|contradictory_contract|tool_surface_gap|parser_surface_gap|model_ignored_visible_contract",
  "evidence_refs": ["conversation:record-id", "telemetry:event-id"]
}
```

The event should avoid raw prompt, transcript, file-body, secret, or customer
content. Evidence refs, event ids, paths, counts, action classes, and contract
ids are sufficient for audit.

## Avoidability Taxonomy

Use stable machine-readable categories:

- `real_invariant`: the model violated a visible and expressible invariant.
- `contract_not_visible`: the gate enforced an invariant not present in the
  last model-facing protocol projection.
- `contradictory_contract`: another visible surface advertised an action class
  the gate rejected.
- `tool_surface_gap`: a legal action existed conceptually but was not visible or
  callable.
- `parser_surface_gap`: the expected structured response shape was not aligned
  with backend/tool transport/parser mode.
- `runtime_state_gap`: selected model, thinking policy, token budget, response
  format, or tool transport did not match the phase contract.
- `evidence_timing_gap`: the harness had evidence or anchors that were not
  available to the model at the decision point.
- `model_ignored_visible_contract`: the visible protocol and alternatives were
  clear, but the model selected an invalid action.
- `unknown`: the event lacks enough structured evidence and needs audit
  improvement before a fix is designed.

The first rollout can leave avoidability as `unknown` for many events. The
important contract is that the event carries enough state for audit to reduce
`unknown` over time.

## Audit Integration

Extend `harness_audit` behavior output with:

- guard friction totals by lane and phase
- avoidability counts
- first invalid action counts
- retry outcome counts
- guard surfaces by event volume
- top contracts by avoidable friction
- visible safe-alternative coverage
- contradiction examples with redacted evidence refs

Add an `all_clear` distinction:

- `guards_fired`: normal count of guard decisions
- `avoidable_friction`: events where the guard likely corrected a harness
  presentation gap
- `unclassified_friction`: events missing enough fields for diagnosis

`all_clear` should not hide guard activity. A run can pass behaviorally while
still producing useful friction evidence.

## Replay And Eval

Create a small replay corpus before changing enforcement:

- include the highest-scoring recent offenders from `harness_audit`
- include one clean successful run per covered lane
- include one source-satisfied short-circuit
- include one malformed-output recovery
- include one completion-gate repeated rejection
- include one capability/read-loop failure

Each replay should assert:

- friction events have contract id and protocol hash when expected
- avoidability classification is present or deliberately `unknown`
- safe alternatives are recorded for nudges and rejections
- no raw prompt or file body appears in friction event payloads

## Implementation Phases

### Phase 1: Passive Events

Implementation status: complete for the covered runner gate and completion
surfaces. The runner emits passive `guard_friction` autonomy events for
rejected, nudged, and stopped guard decisions. The event carries stable
contract, phase, invariant, retry, safe-alternative, and model-visible protocol
fields without changing enforcement behavior.

Emit `guard_friction` events from existing rejection/nudge/stop sites without
changing behavior.

Candidate surfaces:

- completion gates
- plan-execute submit-plan guard
- tool policy rejection
- parser/malformed-output recovery
- same-tool repeat and loop fuses
- write-only recovery
- post-readiness discovery rejection

### Phase 2: Audit Grouping

Implementation status: complete for current behavior audit output.
`harness_audit` groups guard friction by contract, phase, invariant, guard
surface, first invalid action, retry outcome, avoidability, visible action
classes, visible tools, and safe alternatives. Guard activity is diagnostic and
does not by itself fail `all_clear`.

Teach `harness_audit` to group friction by contract, phase, invariant, surface,
and avoidability. Keep redaction strict.

### Phase 3: Baseline Report

Implementation status: staged. The current behavior audit can name historical
offenders, but the fresh replay corpus has not yet been selected.

Generate a baseline report for the named replay corpus and recent live logs.
This baseline becomes the comparison point for protocol projection and
turn-phase reconciliation work.

### Phase 4: Regression Promotion

Implementation status: started through focused unit and scripted-contract
coverage for repeated completion rejection and write-description repair
friction. Broader replay promotion remains staged until the fresh corpus is
named.

Promote recurring avoidable friction patterns into focused scripted or replay
tests. Do not promote every raw event; group by invariant and first invalid
transition.

## Success Criteria

- At least 95 percent of guard rejections in covered lanes include
  `contract_id`, `protocol_hash`, `invariant_id`, and `guard_surface`.
- Audit can distinguish real invariant blocks from at least three avoidable
  categories on the replay corpus.
- Repeated rejection findings include retry outcome and first invalid action.
- No friction event contains raw prompt, raw transcript, file body, or secret
  material.
- A future contract/protocol change can prove improvement against the baseline
  without relying on anecdotal live-run memory.

## Non-Goals

- Do not change enforcement behavior in this effort.
- Do not add new model-facing reminders.
- Do not store raw transcripts or prompt excerpts in friction events.
- Do not make avoidability depend on model self-report.
- Do not score model quality from a single guard hit. Score the contract
  presentation and retry path first.
