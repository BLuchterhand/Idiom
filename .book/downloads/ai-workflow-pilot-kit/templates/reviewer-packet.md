# Reviewer Packet

Use this as the packet a reviewer sees. The reviewer should not need to
reconstruct source collection before deciding.

## Packet Summary

| Field | Answer |
| --- | --- |
| Packet ID |  |
| Workflow |  |
| Proposed action |  |
| Consequence if accepted |  |
| Submitted by |  |
| Reviewer |  |
| Due date |  |

Allowed submitted-by values: AI / human / mixed.

## Proposed Action

State exactly what acceptance would allow:

```text

```

## Claims And Evidence

| Claim ID | Claim | Source / Citation | Source Existence | Source Authority | Source Type | Freshness | State | Caveat / Limit | Reviewer Finding |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |  |

Allowed values:

- Source existence: missing / attached source / illustrative placeholder.
- Source authority: authority-bearing / supporting / background context / not authority-bearing.
- Source type: primary / official / regulator-court / academic / journalism / internal / vendor / background.
- Freshness: current / stale / unknown.
- State: missing / supported / conflicted / verified.

## Missing Or Conflicted Evidence

| Issue | Required Action | Owner |
| --- | --- | --- |
|  |  |  |

Allowed required actions: return incomplete / block / escalate.

## Reviewer Decision

Packet decision:

```text
[ ] Accept packet as review input
[ ] Reject
[ ] Return incomplete
[ ] Escalate
[ ] Block workflow
```

Decision or effect authorization:

```text
[ ] No decision accepted
[ ] Accept proposed decision
[ ] Authorize permitted effect
[ ] Require named owner approval before effect
```

Reviewer note:

```text

```

## Audit Note

Record the packet decision, accepted decision if any, authorized effect if any,
rejected claim, missing evidence, escalation, or blocked state. Include source
versions, reviewer identity, and owner after acceptance.

```text

```

## Override Rule

An informal business request cannot override a rejected, incomplete, conflicted,
or stale packet. Override requires the named escalation owner, a new audit note,
and a decision to accept the risk explicitly.

Accepting the packet as useful review input does not authorize the workflow to
change customer, money, record, production, legal, or other authority-bearing
state.
