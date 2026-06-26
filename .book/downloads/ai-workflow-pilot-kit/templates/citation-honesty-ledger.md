# Citation Honesty Ledger

Use this ledger when a packet, example, vendor evaluation, or case uses source
references. Its job is to prevent a reference from carrying more authority than
the source supports.

## Ledger

| Reference ID / Citation | Source Existence | Source Authority | Source Type | Source Owner | Reviewed Date | Exact Claim Supported | Caveat / Limit | What This Does Not Prove | Decision Use |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |  |

Allowed values:

- Source existence: missing / attached source / illustrative placeholder.
- Source authority: authority-bearing / supporting / background context / not authority-bearing.
- Source type: primary / official / regulator-court / academic / journalism / internal / vendor / background.
- Decision use: verify / support / context only / do not use.

## Rules

- Label illustrative placeholders. Do not let example IDs look like real
  citations.
- A source can support one claim without supporting the stronger decision.
- A vendor assertion about grounding, safety, or governance is not evidence that
  a specific workflow claim is true.
- A source can be official and stale at the same time.
- Background context may help the model reason, but it should not verify a
  required decision claim.
- If a source does not show its owner, reviewed date, or version, treat the
  claim as unsupported until the owner supplies it.

## Example

| Reference ID / Citation | Source Existence | Source Authority | Source Type | Source Owner | Reviewed Date | Exact Claim Supported | Caveat / Limit | What This Does Not Prove | Decision Use |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| incident/INC-4412 | illustrative placeholder | supporting | internal primary | SRE incident manager | 2026-06-18 | A qualifying incident affected the relevant service window. | Composite example; not a documented public incident. | Customer credit eligibility or approved amount. | support |
| contract/ACME-v7 | illustrative placeholder | authority-bearing | internal official | Legal / finance policy owner | 2026-06-18 | Contract category allows a credit type in the example. | Composite example; not a real customer contract. | Finance approval or actual entitlement. | support |
