# Chapter 9 Packet: Conditions For Useful Human Review

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat for modern GenAI review claims.

Use direct GenAI examples for narrow claims and review-condition language for
the broader design standard.

## Chapter Claim

Human review can fail when the reviewer lacks evidence, time, independence,
expertise, authority to reject, or a practical interface for judgment. This
chapter treats those conditions as a design standard for reviewable AI work,
not as a sourced universal law.

## Reader Starting Point

The reader may believe "human in the loop" is a sufficient safety claim.

## First Visible Moment

The reviewer screen shows the AI recommendation, claim list, source links,
stale-source warning, missing evidence, proposed action, reject/escalate
controls, and the rejection rule.

Scene beats:

1. The reviewer sees the recommendation and the claims that must be checked.
2. One source is stale, one claim is missing evidence, and the proposed action
   exceeds support authority.
3. The interface makes reject and escalate as easy as accept.
4. The reviewer uses a rejection rule instead of negotiating in Slack.
5. The AI output remains useful because the review packet turns it into a
   focused judgment task.

## Workplace Behavior Parallel

Reviewers in mature workflows need packets, criteria, time, and authority.
Otherwise review becomes a ritual.

## Human Fallibility Pattern

Reviewers can drift toward review without enough time or evidence when
overloaded, underinformed, politically pressured, or unable to reject safely.

## Existing Workplace Contract

Review packets, acceptance criteria, independence, rejection authority,
escalation routes, and calibration make review meaningful.

## AI Analogue

AI-generated recommendations can be more polished and numerous than human
reviewers expect. The review process must preserve evidence and rejection power.

## Analogy Break

AI can make unsupported output look more complete than it is, increasing
automation bias and review overload.

## Operator Artifact

Reviewer packet:

| Field | Purpose |
| --- | --- |
| Proposed action | What AI recommends |
| Claim list | What must be checked |
| Source links | Evidence path |
| Freshness status | Stale/current/unknown |
| Missing evidence | What blocks acceptance |
| Risk consequence | What happens if wrong |
| Reviewer action | accept/reject/escalate |
| Rejection authority | Policy backing rejection |

Does not own: capacity economics or incentive mapping. This chapter owns what
the reviewer sees, can check, and can do in the moment of review.

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | The review owner maintains packet fields and rejection/escalation actions. |
| Source authority | Claim list, source links, freshness status, risk tier, and acceptance criteria. |
| Update cadence | Review after calibration misses, reviewer complaints, or escaped unsupported decisions. |
| Failure signal | Reviewers reconstruct evidence manually, cannot reject, or accept without checking claims. |
| Operational hook | Block acceptance, escalate, or sample decisions when the reviewer cannot perform the required check. |
| Escalation/retirement | Simplify packet fields that do not affect judgment; block acceptance when required fields are absent. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use human factors, automation bias, and domain review sources only where directly supported. |
| Normative control standard | The draft may recommend evidence, time, independence, expertise, authority, and a practical interface as design conditions; do not state that all human review is invalid without them unless sourced. |
| Hypothesis/composite scenario | GenAI-specific overload scenes are plausible patterns unless sourced. |

## Evidence Status

Current publication prose cites the Uber/NTSB automation-oversight case through
`RC10` and uses review-condition language. **H1**, **H2**, **H3**, **H4**,
**H5**, **H6**, **H7**, **S4**, and **G2** are candidate/source-pool planning
sources for future strengthening unless they are promoted into the source ledger
for this chapter and cited in the prose.

Gaps: LLM-specific automation bias, hallucination acceptance, legal/medical AI
review studies, and reviewer overload in AI-assisted workflows remain
source-pool or public-launch/formal-pitch opportunities, not current chapter
citations.

## Main Sections

1. **The review screen**: show a usable packet.
2. **Why presence is not oversight**: evidence, time, independence.
3. **Review ergonomics**: what the reviewer sees and can do.
4. **Rejection authority**: review fails if rejection is punished.
5. **Calibration**: rejected and accepted decisions feed learning.

## Practical Takeaway

Before claiming human oversight, verify the reviewer can see the evidence,
understand the standard, reject the output, and survive the consequences.

## Possible Objections

- "Experts know what to check": experts still need evidence and time.
- "Review slows the workflow": bad review creates rework and escaped risk.
- "AI can explain itself": explanations are inputs to review, not acceptance.

## Bridge

Chapter 9 designs review. Chapter 10 handles the pressure that appears when the
official workflow is too slow and teams look for bypass paths.
