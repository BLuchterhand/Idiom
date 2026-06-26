# Source And Freshness Map

Use this before a claim-to-evidence matrix when the team has not agreed which
sources are authoritative enough for the decision.

| Claim Type | Authority-Bearing Source | Source Existence | Source Authority | Source Type | Source Owner | Freshness Rule | Conflict Rule | Not Enough By Itself |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |  |

Allowed values:

- Source existence: missing / attached source / illustrative placeholder.
- Source authority: authority-bearing / supporting / background context / not authority-bearing.
- Source type: primary / official / regulator-court / academic / journalism / internal / vendor / background.

## Rules

- Background context can help the model understand the situation, but it does
  not control the decision unless the source owner also controls the claim.
- Illustrative placeholders are allowed in examples, but they must be labeled
  so readers do not mistake them for documented sources.
- A stale official source may still be official, but it cannot verify a current
  decision until the freshness rule passes.
- When sources conflict, the packet should block or escalate. Do not let the AI
  choose the convenient source silently.
- The map should name sources that are not enough by themselves, such as status
  pages, generic vendor evals, stale knowledge-base pages, or prior similar
  approvals.
