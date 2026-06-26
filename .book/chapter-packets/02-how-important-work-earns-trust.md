# Chapter 2 Packet: How Important Work Earns Trust

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat.

Software and operations examples are reasonably supported. Aviation, medicine,
finance, law, and journalism should remain sourced sidebars until stronger
domain-specific sources are added.

## Chapter Claim

High-consequence work earns trust when expertise enters a reviewable process.
AI adoption should inherit that lesson while acknowledging that human processes
also fail.

## Reader Starting Point

The reader may see process as an AI-specific burden or assume that adding a
human reviewer is enough.

## First Visible Moment

A competent expert submits work that still enters review: a pull request before
release, an incident timeline before a postmortem, or a policy exception before
customer communication.

Scene beats:

1. The expert is respected; the scene should not make the person look careless.
2. The work still pauses at a gate because the organization cares about the
   effect, not only the person's competence.
3. The reviewer checks evidence, criteria, and consequences rather than
   redoing the whole job.
4. The process catches one ordinary omission and turns it into a better packet.
5. The AI analogue inherits this pattern and adds scale, opacity, and speed.

## Workplace Behavior Parallel

Code review, tests, incident reviews, approval limits, runbooks, logs, and audit
records exist because even expert human work is fallible.

## Human Fallibility Pattern

People are tired, rushed, biased, incomplete, incentivized, stale, or politically
constrained. Expertise reduces some errors but does not remove fallibility.

## Existing Workplace Contract

High-consequence workflows use evidence, review, separation of duties, approvals,
logs, tests, incident learning, and accountable ownership.

## AI Analogue

AI is another fallible contributor to work. It changes generation speed,
coverage, and opacity; it does not remove the need for accepted work.

## Analogy Break

AI can produce far more proposals than reviewers can inspect, can propagate
through tools quickly, and cannot be accountable. The process needs additional
controls for scale, permission, and auditability.

## Operator Artifact

Process evidence check:

| Work Type | Evidence Required | Reviewer | Acceptance Hook | Failure State |
| --- | --- | --- | --- | --- |
| Pull request | Tests, diff, review | Maintainer | Merge blocked until approval | Failing checks |
| Incident report | Timeline, logs, owners | Incident lead | Action items assigned | Missing evidence |
| Policy exception | Current policy, contract | Domain owner | Decision held until accepted | Escalate |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | The process owner names the reviewer and acceptance rule. |
| Source authority | Each work type lists the evidence required before acceptance. |
| Update cadence | Review after escaped errors, repeated delays, or changed risk. |
| Failure signal | Review happens but cannot change acceptance. |
| Operational hook | Block, escalate, log, or assign corrective work when required process evidence is missing. |
| Escalation/retirement | Remove fields that never block or escalate; add hooks where review is only ceremonial. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use domain sources only for specific control examples such as code review, incident review, audit, or safety practice. |
| Normative control standard | Important AI workflows should inherit reviewable-process discipline. |
| Composite scenario | Cross-domain analogies remain examples, not proof that all mature domains work identically. |

## Evidence Status

Sources: **S1**, **S2**, **S3**, **S4**, **S5**, **S6**, **H1**, **H2**,
**H3**, **H4**, **G1**, **G3**, **G4**.

Gaps: domain-specific sidebars and evidence on control degradation under
pressure.

## Main Sections

1. **Competence still enters review**: open with a concrete work artifact.
2. **Why expertise alone was never the control**: show human fallibility.
3. **Where process fails**: overload, weak rejection authority, ritualized audits.
4. **What AI changes**: speed, scale, opacity, and tool reach.
5. **The process evidence check**: add evidence, reviewer, hook, and failure
   state fields.

## Practical Takeaway

Do not ask only whether a human is "in the loop." Ask what evidence, review,
approval, log, or test changes whether the work can be accepted.

Drafting boundary: Chapter 2 normalizes reviewable process for fallible expert
work. Save reviewer-interface mechanics, rejection authority, and detailed
review packets for Chapter 9.

## Possible Objections

- "This is bureaucracy": answer by distinguishing hooks from paperwork.
- "Human process fails too": agree and make control validation central.
- "AI should reduce process": answer that low-risk work gets lighter controls.

## Bridge

Chapter 2 normalizes process. Chapter 3 shows why artifact fixes are
insufficient when they improve the packet without controlling the decision.
