# Chapter 3 Packet: When Artifact Fixes Do Not Control The Decision

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat.

The chapter must treat artifact fixes as useful improvements, not strawmen.

## Chapter Claim

Prompts, RAG, better models, evals, policies, and human-in-the-loop labels can
help, but none is sufficient if unsupported output can still become authority.

## Reader Starting Point

The reader may believe the adoption problem can be solved by better tooling,
better prompting, stricter policy, or adding a reviewer.

## First Visible Moment

The team improves the prompt, adds retrieval, displays a review label, and
publishes a policy. The recommendation still reaches Slack and CRM before the
decision rule exists.

Scene beats:

1. Each improvement is reasonable and visibly helps part of the workflow.
2. The prompt produces cleaner language; retrieval adds plausible sources.
3. The review label makes risk visible but does not stop sharing.
4. The policy says what should happen but is not attached to the runtime path.
5. The recommendation still becomes an expectation before the workflow can
   accept or reject it.

## Workplace Behavior Parallel

A release checklist improves discipline but fails if nobody can stop the
release when a required check is missing.

## Human Fallibility Pattern

Teams mistake ceremony for control, especially when speed pressure rewards
visible progress.

## Existing Workplace Contract

A real control changes decision behavior, escalation, logging, blocking,
throttling, or permissions. A checklist that cannot stop anything is only a
reminder.

## AI Analogue

AI fixes become real controls only when they change the authority boundary:
what can be accepted, communicated, applied, or escalated.

## Analogy Break

AI outputs can spread through connected tools faster than human checklist
failures, making artifact fixes feel complete while authority still leaks.

## Operator Artifact

Authority-boundary test:

| Fix | What It Improves | What It Still Cannot Stop |
| --- | --- | --- |
| Better prompt | Output shape | Unsupported decision |
| RAG | Candidate context | Stale or wrong authority |
| Review label | Awareness | Review without evidence, time, or rejection authority |
| Eval dashboard | Aggregate quality | Single high-risk escape |
| Policy | Expected behavior | Runtime action |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | The workflow owner records which fix controls a decision and which only improves inputs. |
| Source authority | Each fix names the source, gate, or runtime condition it depends on. |
| Update cadence | Revisit after new tools, model changes, or escaped unsupported decisions. |
| Failure signal | A fix improves output quality but unsupported work can still become a decision. |
| Operational hook | Convert the fix into a block, escalation, log, throttle, permission, or decision rule. |
| Escalation/retirement | Promote useful reminders into hooks or remove them from the core contract. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use AI, RAG, eval, and governance sources for specific claims about technique limits. |
| Normative control standard | A control should change decision, escalation, logging, blocking, throttling, or permission behavior. |
| Composite scenario | The sequence of fixes is a composite adoption pattern, not a documented incident. |

## Evidence Status

Sources: **A6**, **A7**, **A8**, **A9**, **A10**, **G1**, **G2**, **G3**,
**H3**, **H5**, **H6**, **X1**, **X2**.

Gaps: concrete cases where evals, policies, RAG, or HITL labels failed because
authority boundaries remained weak.

## Main Sections

1. **A sequence of artifact improvements**: show each fix helping something.
2. **The decision rule**: name what still leaks.
3. **Proposal, reviewed proposal, bounded action**: introduce workflow
   classification.
4. **How artifact fixes become real controls**: connect to hooks.
5. **The classification lens**: add chapter artifact.

## Practical Takeaway

For each proposed AI fix, ask: what can this fix block, escalate, log, throttle,
or prevent from becoming an accepted decision?

## Possible Objections

- "Are you saying RAG is useless?": no, it is useful context infrastructure.
- "Policies matter": yes, when encoded into workflow behavior.
- "Better models matter": yes, but model quality and controls are coupled.

## Bridge

Chapter 3 separates artifact fixes from authority controls. Chapter 4 asks which
tasks should enter the workflow in the first place.
