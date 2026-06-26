# Pilot Runbook

Use this runbook to move from workshop output to a live pilot. It defines who
owns each artifact, where packets live, how review states change, and how
accepted work hands off to the business owner.

## Operating Setup

| Item | Decision |
| --- | --- |
| Workflow name |  |
| Pilot owner |  |
| Business owner after acceptance |  |
| Reviewer manager |  |
| Escalation owner |  |
| Packet storage location |  |
| Pilot log location |  |
| Weekly review time |  |
| Review SLA |  |
| Stop/go decision date |  |

## Artifact Ownership

| Artifact | Owner | Due | Storage Location | Required Before Launch? |
| --- | --- | --- | --- | --- |
| Decision acceptance contract | Pilot owner |  |  | yes |
| Claim-to-evidence matrix | Source owner and pilot owner |  |  | yes |
| Reviewer packet | Reviewer manager |  |  | yes |
| Effect-boundary spec | Platform or workflow owner |  |  | yes |
| Review-capacity scorecard | Reviewer manager |  |  | yes |
| Pilot log | Pilot owner |  |  | yes |
| Control-validation loop | Pilot owner |  |  | yes |
| Authority-pace checklist | Stop/go owner |  |  | before authority increase |

## Packet Lifecycle

Use one state per packet.

```text
generated
  -> incomplete
  -> ready for review
  -> accepted as review input
  -> accepted decision
  -> effect authorized

ready for review
  -> rejected
  -> returned incomplete
  -> escalated
  -> blocked
```

State meanings:

| State | Meaning | Next Owner |
| --- | --- | --- |
| generated | AI or a human has created a draft packet. | Packet preparer |
| incomplete | Required source, freshness, owner, or claim field is missing. | Packet preparer |
| ready for review | Required packet fields are present. | Reviewer |
| accepted as review input | Reviewer accepts the packet as useful evidence, not as final authority. | Reviewer |
| accepted decision | Named owner accepts the proposed decision. | Business owner |
| effect authorized | Named owner authorizes the permitted effect. | Workflow/tool owner |
| rejected | Reviewer rejects the claim, decision, or evidence. | Pilot owner |
| returned incomplete | Reviewer sends packet back for missing evidence. | Packet preparer |
| escalated | Authority, source conflict, or risk owner must decide. | Escalation owner |
| blocked | Workflow cannot proceed under the current contract. | Stop/go owner |

## Daily Operating Rules

- Do not review packets that lack required sources, source dates or versions,
  reviewer action choices, and prohibited-effect status.
- Do not let a reviewer reconstruct missing evidence manually. Return the
  packet incomplete and fix source collection.
- Do not treat packet acceptance as effect authorization.
- Log every generated, incomplete, accepted, rejected, returned, escalated, and
  blocked packet in `spreadsheets/pilot-log.csv`.
- Escalate source conflicts and authority conflicts. Do not hide them in notes.
- Stop or roll back if unsupported work reaches customer, money, record,
  production, legal, or other authority-bearing state.

## Weekly Meeting Agenda

| Time | Topic | Decision |
| ---: | --- | --- |
| 0-10 min | Review generated, complete, accepted, rejected, returned, escalated, and blocked packets. | Continue / investigate |
| 10-20 min | Check review debt, reviewer time, and queue age. | Continue / throttle / add capacity |
| 20-30 min | Sample accepted decisions for evidence, freshness, and owner handoff. | Continue / repair control |
| 30-40 min | Review stale-source drills, bypass incidents, and escaped unsupported decisions. | Continue / narrow / stop |
| 40-50 min | Decide changes before next week. | Owner and due date |

## Acceptance Handoff

Before a decision can become an authorized effect, record:

| Field | Answer |
| --- | --- |
| Packet ID |  |
| Accepted decision |  |
| Effect authorized |  |
| Owner authorizing effect |  |
| Source versions reviewed |  |
| Reviewer action |  |
| Business owner after acceptance |  |
| Correction or rollback path |  |

## Launch Checklist

```text
[ ] Packet storage location exists.
[ ] Pilot log exists and is writable.
[ ] Review SLA is named.
[ ] Escalation owner has agreed to own conflicts.
[ ] Source owners have agreed to freshness rules.
[ ] Prohibited effects are denied, sandboxed, or monitored.
[ ] Review-capacity threshold is set.
[ ] Stop conditions are accepted by the pilot sponsor.
```
