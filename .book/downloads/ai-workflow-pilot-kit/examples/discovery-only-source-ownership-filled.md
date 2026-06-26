# Filled Example: Discovery Only Source Ownership Failure

This example shows a workflow that should not enter a reviewed proposal pilot
yet because the team cannot name the source owner for the highest-risk claim.
It is illustrative and does not document a real vendor-risk workflow.

## Decision Acceptance Contract

| Field | Answer |
| --- | --- |
| Workflow name | Vendor-risk exception triage |
| Contract owner | Procurement operations lead |
| Business owner after acceptance | Not assigned |
| Exact decision | Whether a vendor exception may proceed without the standard security questionnaire. |
| Initial authority mode | discovery only |
| What AI may do before acceptance | Summarize candidate sources and list missing owners. |
| What AI must not do | Recommend approval, route the exception, notify the vendor, or mark security review complete. |

## Why It Stays In Discovery

| Issue | Finding | Required Next Step |
| --- | --- | --- |
| Highest-risk claim | Vendor meets minimum security requirements. | Name the security source that controls the claim. |
| Source owner | Procurement has a spreadsheet, security owns policy, legal owns contract terms. | Assign one owner for the acceptance rule or define escalation. |
| Freshness | Spreadsheet has no reviewed date. | Add source date/version and freshness rule. |
| Reviewer capacity | No funded reviewer time. | Estimate review queue before pilot. |
| Authority conflict | Sales wants exception fast; security has veto but no SLA. | Define escalation owner and SLA. |

## Allowed Output

```text
Discovery note only:
- candidate sources found;
- missing source owners;
- unresolved authority conflicts;
- recommendation to stay out of reviewed proposal mode.
```

## Stop/Go Decision

```text
Stop before pilot. The workflow may use AI for discovery notes, but it cannot
generate reviewed proposals until source ownership, freshness, reviewer
capacity, and escalation authority are named.
```
