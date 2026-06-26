# Filled Example: Support Credit Reviewed Proposal

This example shows a reviewed proposal workflow for customer support credits.
It is a composite design scenario, not a documented deployment or a claim about
typical support operations. Use it as a calibration sample, not as a universal
policy.

The source references below are illustrative internal IDs for the composite
scenario. They are not public citations and do not document a real customer,
incident, contract, or billing record.

## Decision Acceptance Contract

| Field | Answer |
| --- | --- |
| Workflow name | Support credit exception review |
| Contract owner | Support operations manager |
| Business owner after acceptance | Finance approver for credits above policy threshold; support manager below threshold |
| Exact decision | Whether to recommend a one-time service credit for a named customer incident pending required approval |
| Initial authority mode | reviewed proposal |
| What AI may do before acceptance | Draft a reviewer packet, list claims, attach source links, and recommend accept/reject/escalate. |
| What AI must not do | Issue the credit, update billing, send a customer message, change entitlement, or approve an exception. |

## Claim-To-Evidence Matrix

| Claim ID | Claim | Decision Relevance | Authority-Bearing Source | Source Existence | Source Authority | Source Type | Source Owner | Source Date / Version | Reviewed Date | Freshness Rule | Verification Method | Source Reference / Citation | Exact Claim Supported | Caveat / Limit | Unsupported Stronger Claim | State |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| C1 | Customer experienced a qualifying service incident. | required | Incident record | illustrative placeholder | authority-bearing | internal primary | SRE incident manager | Incident post-incident record v3 | 2026-06-18 | Current incident record or final postmortem | Match account, time window, affected service, and severity. | incident/INC-4412 | A qualifying incident affected the service in the relevant window. | Status page alone is context. | Customer is credit-eligible. | verified |
| C2 | Contract allows this credit type. | required | Customer contract and current approved exception/service-credit policy | illustrative placeholder | authority-bearing | internal official | Legal / finance policy owner | Contract version active on incident date | 2026-06-18 | Active terms for incident date | Confirm clause, limit, and approval threshold. | contract/ACME-v7 | Contract and policy allow this credit category. | Escalate if contract and policy conflict. | Proposed amount is approved. | verified |
| C3 | Proposed amount is within threshold. | required | Billing ledger and credit policy | illustrative placeholder | authority-bearing | internal primary | Finance | Current billing cycle ledger | 2026-06-18 | Ledger current to review date | Compare proposed amount to eligible charges and approval limit. | billing/account-772 | Proposed amount can be checked against eligible charges. | Finance has not completed verification. | Credit may be issued. | supported |
| C4 | Customer has not already received overlapping credit. | required | Billing ledger and prior exception log | illustrative placeholder | authority-bearing | internal primary | Finance / support ops | Current ledger plus exception log | 2026-06-18 | Current to review date | Search account credits for incident period. | exceptions/account-772 | No overlapping credit is recorded for the incident period. | Does not prove future entitlement. | Customer should receive credit. | verified |
| C5 | Customer-facing explanation is accurate and noncommittal. | supporting | Approved incident language and support response policy | illustrative placeholder | supporting | internal official | Support comms | Approved language v2 | 2026-06-18 | Current approved language | Compare draft text to approved wording. | comms/incident-language-v2 | Draft wording matches approved noncommittal incident language. | AI may draft, not send. | Message may be sent without approval. | supported |

## Reviewer Packet

| Field | Answer |
| --- | --- |
| Packet ID | SC-2026-0142 |
| Proposed action | Recommend a one-time credit of USD 2,400 pending finance approval. |
| Consequence if accepted | Billing record changes and customer communication after approval. |
| Reviewer | Support operations manager, then finance approver |
| Allowed decisions | accept packet as review input, accept proposed decision, authorize effect, return incomplete, escalate to finance, reject |

### Missing Or Conflicted Evidence

| Issue | Required Action | Owner |
| --- | --- | --- |
| Finance has not verified C3. | Escalate before acceptance. | Finance approver |

### Reviewer Decision

```text
Packet accepted as useful review input. Return incomplete for decision
acceptance until finance verifies the proposed amount. AI-generated customer
message remains an internal draft, and no billing effect is authorized.
```

## Effect Boundary

| Surface | Allowed Before Acceptance | Allowed After Acceptance | Prohibited | Enforcement |
| --- | --- | --- | --- | --- |
| Source access | Read incident, contract, ledger summary, policy, exception log. | Same. | Reading unrelated accounts. | Account-scoped access. |
| Communication | Draft internal packet and draft customer note. | Send approved message through support tool. | Direct customer send before approval. | Send permission disabled. |
| Record mutation | None. | Apply credit only through billing approval path. | Direct ledger edit. | Billing role separation. |
| Money / credit | Recommendation only. | Credit within approved threshold. | Auto-issuing credit. | Finance approval required. |

## Weekly Control Review

| Measure | Current | Threshold | Decision |
| --- | ---: | ---: | --- |
| Generated packets | 28 | 40 | Continue. |
| Complete packets | 19 | 30 | Fix source collection; do not raise volume. |
| Accepted verified packets | 13 | 20 | Continue at reviewed proposal only. |
| Returned incomplete | 7 | 8 | Watch source quality. |
| Escaped unsupported decisions | 0 | 0 | Required to continue. |
| Review debt | 3 | 0 | Throttle until backlog clears. |

Scale decision: stay in reviewed proposal mode. Do not move to bounded action
until finance verification is consistently present in the packet and review debt
is zero for two review periods.
