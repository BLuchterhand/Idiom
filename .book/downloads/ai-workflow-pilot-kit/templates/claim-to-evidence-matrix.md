# Claim-To-Evidence Matrix

Use this when an AI-generated answer combines several claims into one
recommendation. Fill one row for each claim that matters to the decision.

## Workflow

| Field | Answer |
| --- | --- |
| Workflow name |  |
| Decision or proposed action |  |
| Packet ID |  |
| Reviewer |  |
| Review date |  |

## Matrix

| Claim ID | Claim | Decision Relevance | Authority-Bearing Source | Source Existence | Source Authority | Source Type | Source Owner | Source Date / Version | Reviewed Date | Freshness Rule | Verification Method | Source Reference / Citation | Exact Claim Supported | Caveat / Limit | Unsupported Stronger Claim | State |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |

Allowed values:

- Decision relevance: required / supporting / context only.
- Source existence: missing / attached source / illustrative placeholder.
- Source authority: authority-bearing / supporting / background context / not authority-bearing.
- Source type: primary / official / regulator-court / academic / journalism / internal / vendor / background.
- State: missing / stale / supported / conflicted / verified.

## State Definitions

| State | Meaning | Allowed Decision Behavior |
| --- | --- | --- |
| missing | No source is attached for the claim. | Return incomplete or block. |
| stale | A source exists but fails the freshness rule. | Return incomplete, block, or escalate. |
| supported | The source appears relevant but has not been checked. | Do not accept yet. |
| conflicted | Two sources or owners disagree. | Block or escalate. |
| verified | The checker confirmed the source supports the claim. | May be accepted if other required claims pass. |

## Citation Honesty Check

Before acceptance, verify:

- every citation is attached to a specific claim;
- every required claim has an authority-bearing source, not only background
  context;
- illustrative placeholders are labeled as illustrative;
- source dates or versions are visible;
- the packet distinguishes evidence from explanation;
- no citation is used to support a stronger claim than the source actually
  supports.

## Good And Bad Citation Use

| Use | Example | Decision Behavior |
| --- | --- | --- |
| Good | Contract clause supports the exact credit limit for this customer on the incident date. | May verify the credit-limit claim if current. |
| Good | Incident record supports the affected service and time window. | May verify incident qualification if account matches. |
| Bad | Status page says an outage occurred, so the customer is eligible for credit. | Treat as context only unless policy and contract also support eligibility. |
| Bad | Vendor eval says retrieval is grounded, so all citations are trustworthy. | Require claim-level source, date, owner, and verification. |

If a source is useful background but not authority-bearing, mark the decision
relevance as `context only`.
