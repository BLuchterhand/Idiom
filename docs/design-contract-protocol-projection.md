# Contract Protocol Projection

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: phase 1-2 implemented and verified 2026-05-24; gate consumption and
static prompt pruning remain staged

## Purpose

Define the dynamic model-facing protocol projection for an active compiled
contract. This is the prompt/tool/parser-facing surface that lets the model
understand what the harness will enforce without turning prompts into authority.

The projection answers one question for the current model call:

What can the model legally do now, what terminal shapes are valid, and what
safe alternatives exist if the attempted action would violate the contract?

## Problem

Idiom already has runtime briefs, prompt layers, tool manifests, parser
contracts, recovery nudges, completion gates, and audit events. Recent history
shows those surfaces can drift:

- a recovery nudge may teach one terminal state while the gate enforces another
- a visible tool menu may imply discovery is still useful after readiness
- prompt prose may collapse required edit paths, source-body evidence, and plan
  coverage
- parser mode or backend transport may not match the expected final action
- changing a prompt can appear to change behavior without changing the actual
  typed contract

The fix is not more static prompt text. The fix is a generated projection from
the active contract, with a stable identity that all surfaces cite.

## Design Decision

Compile every active contract into a `ModelProtocolProjection` before model
dispatch. Runtime briefs, tool visibility, parser settings, recovery cards,
completion gates, and friction events must cite the projection's
`protocol_hash` in covered lanes.

The projection is model-readable and compact. It explains contract semantics,
but it does not own authority. Authority remains in the compiled contract and
policy decisions.

## Projection Shape

Schema fields:

```json
{
  "contract_id": "contract-123",
  "contract_version": "idiom.autonomy.contract.v1",
  "protocol_hash": "protocol_...",
  "phase_id": "plan_execute.required_source",
  "parser_mode": "source_refresh",
  "tool_transport_mode": "textual",
  "parser_transport_mode": "textual_json_terminal",
  "parser_contract": "source_refresh",
  "response_format": "json|text|tool_call",
  "expected_visible_output": "visible_text_tool_request",
  "visible_tools": ["read_file", "code_navigate", "submit_plan"],
  "callable_tools": ["read_file", "code_navigate"],
  "callable_action_classes": ["read", "source_satisfied", "blocker"],
  "invalid_action_classes": ["submit_plan", "per_edit_write", "search"],
  "terminal_response_shapes": ["Decision: source-satisfied", "<<QUESTION>>:"],
  "source_body_obligations": ["internal/example.go"],
  "required_edit_coverage": ["internal/example.go"],
  "path_role_summary": ["required_edit:internal/example.go"],
  "verification_obligations": ["command:go test ./internal/example"],
  "malformed_output_policy": "retry_same_protocol_hash",
  "recovery_budget": 2,
  "safe_alternatives": ["Decision: source-satisfied", "<<QUESTION>>:"]
}
```

The exact implementation can use Go structs rather than JSON as the internal
representation. The JSON above defines field vocabulary; values are an
illustrative plan-execute required-source fixture, not global defaults.
`safe_alternatives`, terminal shapes, invalid action classes, and path roles are
derived from the active phase contract.

## Model-Facing Rendering

Render the projection into a compact protocol card near the top of the
phase-specific runtime brief:

```text
[active contract protocol]
Active contract: contract-123 / protocol_hash=protocol_...
Phase: plan_execute.required_source
Visible tools: read_file, code_navigate, submit_plan
Callable tools now: read_file, code_navigate
Valid terminal outcomes: Decision: source-satisfied; <<QUESTION>>:
Invalid now: submit_plan, per_edit_write, search
Path roles: required_edit_paths=[internal/example.go]; source_body_obligations=[internal/example.go]; roles=[required_edit:internal/example.go]
Verification: command:go test ./internal/example
Safe alternatives: Decision: source-satisfied; <<QUESTION>>:
```

Rendering rules:

- Use the same vocabulary as gates and audit.
- Put current executable protocol before generic working-style guidance.
- Do not include irrelevant tools or terminal outcomes.
- Do not include raw source bodies.
- Do not prescribe a strategy step when multiple legal actions remain.

## Surface Parity Rules

In covered lanes, these surfaces must cite or derive from the same
`protocol_hash`:

- runtime brief protocol card
- visible tool manifest
- parser/response-format configuration
- legal action surface / readiness state
- failure feedback
- recovery cards
- completion gate decisions
- guard-friction events
- audit reconstruction

If a surface cannot cite the hash yet, it should emit a parity gap event rather
than silently reconstructing authority.

## Prompt Ownership

Static prompts remain useful for durable norms:

- engineering style
- evidence discipline
- generic repository navigation preference
- communication style
- explanation of stable vocabulary

Static prompts should not carry per-run authority:

- active required edit paths
- current terminal outcomes
- visible tool restrictions
- current verification obligations
- retry budgets
- parser response shape
- phase-specific recovery alternatives

Those belong in the projection.

## Rollout

### Phase 1: Model-Visible Projection, Gate-Report-Only

Implementation status: complete for plan-execute planner calls. The runner
compiles a deterministic `ModelProtocolProjection`, inserts one active protocol
card into the model request before the frontier step instruction, emits
turn-phase lifecycle events with the same protocol hash, and keeps gate
enforcement driven by existing typed contract/runtime state.

Implementation note: `contract_id` in the model protocol projection is the
active runner contract id when the compiler receives a parent contract id. The
compiled passive child contract can still exist for report-only evidence, but
the model-visible card and protocol hash bind to the active contract identity.

Generate the projection for plan-execute planner calls and log
`contract_presented` events. The compact protocol card is model-visible, so this
phase is not prompt-neutral. It is gate-report-only: deterministic gates,
tool visibility, parser settings, and recovery decisions remain driven by the
compiled contract and existing runtime state.

Acceptance:

- projection is deterministic for equivalent contract input
- projection carries contract id, phase id, and hash
- runtime brief includes the hash
- existing gate outcomes are unchanged for valid and invalid fixtures
- prompt/card wording changes are reviewed as model-visible behavior changes

### Phase 2: Parity Checks

Implementation status: started and covered for the highest-risk surfaces.
Audit reconciliation now compares model protocol, next-action state,
completion-gate, recovery-card, and turn-phase summaries by contract id, phase
id, model call id, protocol hash, callable tools, terminal outcomes, action
classes, and parser/runtime fields. Contract-id drift is a typed parity issue.

Compare the projection against visible tools, parser config, completion gates,
and recovery text.

Acceptance:

- audit reports missing or mismatched protocol hashes
- turn-phase contradiction findings include projection parity details
- tests cover at least one mismatch and one clean projection

### Phase 3: Gate Contract Consumption

Move one gate at a time to consume compiled contract fields, obligation state,
and invariant IDs instead of reconstructing the same invariant from runner state
and prose. Gates may cite the projection hash for audit parity, but must not
consume model-facing projection rendering as enforcement authority.

Acceptance:

- behavior is unchanged for valid and invalid fixtures
- gate decisions cite invariant id and protocol hash
- projection-card-only changes cannot alter gate behavior unless the underlying
  compiled contract changes

### Phase 4: Static Prompt Pruning

Remove duplicated per-phase authority from static prompts once the projection
covers it.

Acceptance:

- prompt token count decreases or stays stable
- replay pass rate does not regress
- friction audit does not show increased `contract_not_visible`

## Tests

Required test types:

- golden projection tests for representative lane/phase contracts
- parity tests between projection and visible tools
- parser/response-format compatibility tests
- recovery card safe-alternative tests
- completion-gate event tests
- prompt rendering tests that assert no duplicate contradictory terminal states

## Success Criteria

- Covered model calls have exactly one protocol hash.
- Runtime brief, visible tools, parser config, and completion gate agree on
  valid action classes.
- Guard-friction events can say whether the model violated a visible protocol
  or the harness failed to present one.
- Static prompt updates no longer need to carry phase-specific authority.

## Non-Goals

- Do not make the projection choose the model's next strategic step.
- Do not remove deterministic guards.
- Do not turn every tool schema field into prompt text.
- Do not expose internal scoring or adapter implementation details unless they
  affect the model's legal action surface.
