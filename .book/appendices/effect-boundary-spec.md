# Appendix F: Effect-Boundary Specification

Use this before an AI workflow can access tools, communicate outside draft
assistance, or move beyond discovery only.

| Surface | Discovery Only | Draft Assistance | Reviewed Proposal | Bounded Action |
| --- | --- | --- | --- | --- |
| Source access | Approved sources for inspection | Approved low-risk context only | Required sources for claims | Required sources plus action logs |
| Communication | None outside the user/team | Internal draft only | Internal reviewer packet | Approved customer/system message |
| Record mutation | None | None | None or draft note | Only permitted fields |
| Money / credit / entitlement | None | None | Recommendation only | Within threshold after acceptance |
| Tool use | Read-only or sandboxed | Tools limited to drafting | Route packet / request missing source | Act within explicit permission |
| Failure state | Stop / mark missing | Ask user / mark missing | Return incomplete / escalate | Stop, rollback, or incident path |

The boundary is real only if the workflow can enforce it.
