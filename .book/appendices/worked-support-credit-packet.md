# Appendix M: Worked Support-Credit Packet

This appendix shows how the book's templates fit together for the running
support-credit workflow. The example remains a composite design scenario. It is
not a documented deployment.

## Workflow

```text
Workflow:
AI-assisted service-credit recommendation after a service incident

Authority mode:
Reviewed proposal

What the output may do before acceptance:
Internal packet preparation and reviewer routing

What the output must not do before acceptance:
- customer commitment
- final ticket decision
- CRM/account status update implying approval
- credit application
- finance ledger mutation

Accepted state:
Support may recommend the credit to the account team only after current policy,
executed contract terms, billing history, customer-specific outage evidence,
and required finance/compliance checks are complete.
```

The AI assistant can summarize the support ticket, identify claims, find
candidate sources, draft customer language, and prepare the packet. It cannot
make the credit decision. It cannot make a customer promise. It cannot update
the account record with likely approval.

## Support-Credit Minimum Viable Contract

| Field | Support-Credit Example |
| --- | --- |
| One decision | Should support recommend a service credit for this outage-related customer request? |
| Controlling source map | Current approved exception policy plus executed customer contract for eligibility. |
| Action limit before acceptance | Internal reviewed proposal only. |
| One checker | Finance for credit threshold; compliance/legal for policy or contract conflict. |
| One stop condition | Missing source, stale source, contract-policy conflict, or review queue above threshold. |
| One owner | Support manager after the packet is accepted by the required owners. |

This is intentionally small. It does not solve all support work. It controls
one decision.

## Claim-To-Evidence Matrix

| Claim | Authority-Bearing Source | Freshness Rule | Verification Method | State |
| --- | --- | --- | --- | --- |
| Customer requested a credit | Current support ticket | Current ticket thread | Ticket link inspected | Supported |
| Outage affected this customer | Customer-specific outage impact evidence | Approved incident impact note or current telemetry | Incident record checked | Missing in some packets |
| Credit is allowed under policy | Current approved exception policy | Approved current version, not stale KB page | Policy owner/version checked | Supported or stale |
| Contract permits the remedy | Executed contract and amendments | Current executed agreement | Contract clause inspected | Blocked if unavailable |
| Credit amount is within authority | Billing ledger and finance threshold | Current account standing | Finance reviewer checks threshold | Missing in some packets |
| Customer can be told approval is likely | Accepted recommendation and communication owner | Only after acceptance | Account/support owner approval | Not permitted before acceptance |

The AI-generated proposal compresses these claims into one recommendation. The
packet separates them again so review can inspect the actual decision.

## Responsibility And Incentive Map

| Role | Owns | Does Not Own | Veto / Escalation |
| --- | --- | --- | --- |
| Support manager | Customer relationship, packet assembly, final support recommendation after acceptance | Finance threshold, legal contract meaning, policy ownership | Can return incomplete packet; can escalate source conflict |
| Finance reviewer | Credit threshold, billing history, financial approval | Customer relationship, incident severity | Can reject or require amount change |
| Compliance/legal | Policy interpretation, consistency, contract conflict | Renewal pressure | Can block customer commitment until conflict is resolved |
| Incident operations | Outage timeline and customer-specific impact evidence | Credit remedy | Can mark evidence preliminary or approved |
| Executive sponsor | Pilot scope, funded review capacity, stop/go decision | Claim-level verification | Can fund capacity or narrow scope; should not override missing evidence informally |

The support manager is accountable for the customer response only after the
workflow gives them usable evidence and records the recommendation as accepted.
Naming the manager without source access, rejection authority, or escalation
cover does not create a control.

## Reviewer Packet

```text
Proposed action:
Recommend discretionary service credit.

Allowed reviewer actions:
- accept packet for low-risk recommendation;
- reject as unsupported;
- return incomplete for source collection;
- escalate contract-policy conflict;
- block customer commitment.

Rejection authority:
If the contract clause, current policy, finance threshold, or customer-specific
outage evidence is missing, the reviewer may reject or return the packet
without informal override through Slack or the account team.

Audit note:
Record accepted, rejected, escalated, or missing evidence state with source
links and reviewer identity.
```

The packet protects the reviewer by making the missing work visible. If the
reviewer has to reconstruct the evidence manually, the workflow has moved the
cost of AI speed into reviewer time.

## Review-Capacity Example

| Measure | Pilot Assumption | Threshold / Action |
| --- | ---: | --- |
| Expected proposals per week | 47 | Scope must narrow if volume rises without capacity. |
| Complete-packet rate | 70 percent at start | Target 85 percent before expanding. |
| Funded reviewer-hours per week | 6 | Capacity is a product constraint. |
| Average review time | 15 minutes | Re-estimate weekly. |
| Maximum well-reviewed packets | 24 | Queue above 24 open proposals triggers throttle. |
| Review debt | 9 complete packets over capacity | Stop new proposals or add reviewer time. |
| Escaped unsupported decisions | 0 tolerated | Rollback or stop the pilot. |

These are scenario assumptions, not claims about typical support operations.
Their job is to show the decision math. If the workflow can generate 47
recommendations and review 24 well, review capacity is the limiting constraint.

## Final Pilot Decision

At the end of the three-week pilot, the team should be able to say one of five
things:

| Decision | Meaning |
| --- | --- |
| Continue | Low-risk reviewed proposals meet evidence and capacity thresholds. |
| Narrow | The workflow is useful only for a smaller class of cases. |
| Throttle | AI proposal volume exceeds funded review capacity. |
| Escalate | Source ownership, contract conflicts, or policy authority need executive resolution. |
| Stop | Unsupported decisions escaped, stale evidence passed, or review capacity failed. |

The most important decision may be rejecting direct credit application. That
does not mean the pilot failed. It means the workflow learned that bounded
action requires stronger permissions, post-action verification, audit sampling,
rollback paths, and duplicate-credit safeguards before authority increases.
