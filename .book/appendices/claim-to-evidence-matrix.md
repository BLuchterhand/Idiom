# Appendix B: Claim-To-Evidence Matrix

Use this when a fluent AI-generated proposal bundles several claims into one
recommendation.

| Claim | Authority-Bearing Source | Freshness Rule | Verification Method | State |
| --- | --- | --- | --- | --- |
|  |  |  |  |  |
|  |  |  |  |  |
|  |  |  |  |  |

Allowed state values: missing / stale / supported / conflicted / verified.

Rules:

- Context helps the model understand the situation.
- Evidence supports or rejects a specific claim.
- Freshness decides whether the source is current enough for the decision.
- Verification checks a claim or condition against a standard.
- Acceptance decides whether the output may be used.

In the support-credit workflow, the ticket, policy, contract, billing ledger,
outage evidence, and compliance notes answer different claims. A proposal that
cites one of them can still be missing the source that controls the decision.
