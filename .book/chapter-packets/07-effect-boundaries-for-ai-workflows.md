# Chapter 7 Packet: Effect Boundaries For AI Workflows

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft.

Security and governance sources support the control. The Cruise
pedestrian-dragging incident gives the chapter a probabilistic physical-action
case where a post-collision classifier still had authority to select a maneuver
when the safe state should have removed action authority.

## Chapter Claim

Useful rails do not micromanage the model's reasoning. They constrain what the
workflow may access, what claims may be accepted or communicated, what records
may change, and what actions require approval, retry, or escalation.

## Reader Starting Point

The reader may think rails are better prompts, polite refusals, or generic
guardrails.

## First Visible Moment

An employee can draft a payment request but cannot approve, release, and
reconcile the payment alone.

Scene beats:

1. The employee's draft is useful work and should remain easy.
2. The approval button is unavailable because the amount crosses a threshold.
3. The release path requires a separate authority and leaves an audit trail.
4. Reconciliation checks whether the released action matched the approved one.
5. The AI analogue is allowed to draft, but customer communication, credit
   application, and record mutation require accepted conditions.

## Workplace Behavior Parallel

Separation of duties preserves useful work while preventing one actor from
creating, approving, and executing consequential effects.

## Human Fallibility Pattern

People with excessive authority can make mistakes, bypass review, or normalize
exceptions under pressure.

## Existing Workplace Contract

Permission boundaries, approval thresholds, logs, exception owners, and
separation of duties constrain effects without eliminating judgment.

## AI Analogue

AI can draft freely but should not approve, apply, communicate, retry, or mutate
records unless the workflow's evidence and authority conditions are met.

## Analogy Break

AI tool permissions can turn language into effects quickly. Prompt compliance is
not enough when tools can change records or communicate externally.

## Operator Artifact

Permission and effect-boundary specification:

| Action | AI Allowed? | Required Condition | Failure State |
| --- | --- | --- | --- |
| Summarize ticket | Yes | Data class allowed | Redact/block |
| Draft recommendation | Yes | Evidence status shown | Proposal only |
| Communicate to customer | No by default | Accepted decision and owner | Not permitted |
| Apply credit | No by default | Finance approval and audit | Blocked |
| Retry after blocker | Limited | New evidence supplied | Escalate |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | Security/governance owns tool permissions; domain owners own approval thresholds. |
| Source authority | Data-class policy, approval rules, audit requirements, and incident obligations. |
| Update cadence | Review after new tool integrations, new data classes, or escaped effects. |
| Failure signal | A prompt instruction is the only thing preventing a consequential action. |
| Operational hook | Enforce access, block effects, require approval, log action, or trigger post-action verification. |
| Escalation/retirement | Remove access or reduce what the output may do before acceptance until enforceable conditions exist. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use security, governance, and separation-of-duties sources for permission and effect-boundary claims. |
| Normative control standard | Tool-mediated effects should require enforceable conditions, not only instructions. |
| Composite scenario | Support-credit authority examples are illustrative until sourced. |

## Evidence Status

Sources: **G1**, **G2**, **G3**, **X1**, **X2**, **X3**, **A10**, **S1**,
**S2**, **S5**, **RC8**.

Gaps: support-specific action-authority cases remain illustrative.

## Main Sections

1. **Drafting versus releasing payment**: open with separation of duties.
2. **Rails as effect boundaries**: access, communication, mutation, approval.
3. **Machine-checkable and human controls**: what systems enforce and what
   owners decide.
4. **Typed blockers**: missing source, missing permission, review capacity.
5. **Audit hooks and kill switches**: rails must leave operational traces.

## Practical Takeaway

For each AI workflow, list allowed actions, prohibited actions, required
conditions, and valid failure states.

## Possible Objections

- "This limits AI too much": answer with a concrete low-risk drafting path and
  a stricter path for customer, money, record, or system effects.
- "The model can follow instructions": tool permissions should not depend on
  obedience alone.
- "Humans can approve later": customer communication and system changes may
  already create effects.

## Bridge

Chapter 7 sets boundaries. Chapter 8 asks whether the organization can verify
and review the resulting proposal volume.
