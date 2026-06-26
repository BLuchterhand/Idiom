# Filled Example: Bounded Action After Acceptance

This example shows bounded action only after evidence, review, permission,
logging, and rollback are in place.
It is illustrative and does not document a specific support system deployment.
All source names in this example are illustrative placeholders, not public
citations or real internal records.

## Decision Acceptance Contract

| Field | Answer |
| --- | --- |
| Workflow name | Low-risk duplicate ticket closure |
| Contract owner | Support operations manager |
| Business owner after acceptance | Support queue owner |
| Exact decision | Close a duplicate internal support ticket when it matches an already-open parent ticket. |
| Initial authority mode | bounded action |
| What AI may do before acceptance | Propose duplicate relationship and prepare closure note. |
| What AI must not do | Close customer escalations, close tickets with billing/legal/security labels, or send external messages. |

## Required Claims

| Claim ID | Claim | Authority-Bearing Source | Source Existence | Source Authority | Source Type | Freshness Rule | Verification Method | State |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| C1 | Candidate ticket and parent ticket are for the same customer account. | Support ticket system | illustrative placeholder | authority-bearing | internal primary | Current ticket state | Match account ID. | verified |
| C2 | Parent ticket is still open. | Support ticket system | illustrative placeholder | authority-bearing | internal primary | Current ticket state | Read parent status before closure. | verified |
| C3 | Candidate ticket has no billing, legal, security, or customer-escalation label. | Support ticket labels | illustrative placeholder | authority-bearing | internal primary | Current ticket state | Check labels immediately before action. | verified |
| C4 | Closure note links to parent ticket. | Generated closure note | illustrative placeholder | supporting | workflow output | Current packet | Reviewer verifies note before action. | verified |

## Effect Boundary

| Surface | Allowed Before Acceptance | Allowed After Acceptance | Prohibited | Enforcement |
| --- | --- | --- | --- | --- |
| Ticket read | Read candidate and parent ticket. | Same. | Reading unrelated queues. | Queue-scoped token. |
| Ticket mutation | None. | Close duplicate ticket and add internal note. | Closing excluded labels or parent ticket. | Permission and label guard. |
| Communication | Internal note only. | Internal note only. | External customer message. | No external-send permission. |
| Audit | Draft packet log. | Action log with packet ID and rollback owner. | Unlogged closure. | Action denied without log destination. |

## Bounded Action Controls

| Control | Requirement |
| --- | --- |
| Permission | Tool token can close only tickets in the duplicate-candidate queue. |
| Pre-action check | Re-read labels and parent status immediately before closure. |
| Post-action verification | Confirm candidate ticket is closed and parent link is visible. |
| Rollback | Reopen ticket and remove duplicate label within one business day. |
| Incident path | Any excluded-label closure stops the workflow until reviewed. |
| Sampling | Sample 20% of closures weekly, minimum 10 packets. |

## Decision

```text
Approve bounded action only for duplicate-candidate tickets that pass all
pre-action checks. Any stale parent status, excluded label, missing audit log,
or unsupported closure attempt denies the action and creates an incident note.
```
