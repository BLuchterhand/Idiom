# Chapter 11 Packet: Evals, Incidents, And Workflow Governance

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat for governance-theater claims.

Standards can support what governance should include. Organizational failure
claims should be framed as operating risks unless a source directly supports
the stronger claim.

## Chapter Claim

This chapter argues for treating AI governance as operationally useful when it
changes what the workflow can accept, stop, escalate, measure, or learn. Claims
that governance loses operational value without those effects should be framed
as the book's operating test unless stronger organizational sources are added.

## Reader Starting Point

The reader may equate governance with policies, model approvals, dashboards, or
periodic reviews.

## First Visible Moment

A near miss is discovered during a sampled audit. A credit recommendation was
accepted with stale policy evidence because the reviewer saw a polished summary
but not the source age. The team can update a policy document, or it can change
the workflow that accepted the bad packet: freshness gate, reviewer packet,
queue throttle, regression case, and escalation rule.

Scene beats:

1. The near miss is uncomfortable because the visible policy appeared to be
   followed.
2. The audit shows the accepted packet lacked a current-source check.
3. A policy update would be easy and insufficient.
4. The useful response changes the workflow: freshness gate, regression case,
   sampled review, and owner assignment.
5. One old control is retired because it never changed acceptance.

## Workplace Behavior Parallel

Incident review in operations is useful when it changes monitors, runbooks,
tests, owners, or deployment gates. The book's operating test treats a review
as weak when it only records blame without changing the workflow.

## Human Fallibility Pattern

Organizations may fall back to paperwork when operational change is costly,
political, or cross-functional.

## Existing Workplace Contract

Postmortems, regression tests, sampled audits, calibration, and control
retirement turn failures into system changes.

## AI Analogue

AI evals and governance should test the workflow, not only the model. They must
surface stale sources, escaped unsupported decisions, reviewer overload, bypass,
and audit gaps.

## Analogy Break

AI systems can drift through model, vendor, retrieval, prompt, and tool changes;
governance must monitor more than one component.

## Operator Artifact

Control validation loop:

| Signal | Review Action | Workflow Change |
| --- | --- | --- |
| Accepted decision based on stale evidence | Stale-source drill | Freshness gate |
| Rejected incomplete packets | Queue review | Source owner fix |
| Reviewer overload | Capacity check | Throttle/WIP limit |
| Bypass use | Bypass postmortem | Sanctioned fast path |
| Escaped unsupported decision | Incident review | Regression case |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | Governance owner maintains the validation loop; workflow owners implement changes. |
| Source authority | Incident records, sampled decisions, eval results, audit logs, reviewer calibration, and bypass reports. |
| Update cadence | Monthly in steady state; immediately after high-consequence escapes. |
| Failure signal | Governance artifacts record risk but do not change acceptance, permission, evidence, escalation, logging, throttling, or retirement. |
| Operational hook | Add regression cases, change gates, sample accepted decisions, throttle, or retire ineffective controls. |
| Escalation/retirement | Retire controls that never change decisions; add regression cases for escaped failures. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use governance, incident, eval, and audit sources for concrete mechanisms. |
| Normative control standard | Governance should validate and change acceptance conditions as an operating standard; avoid broad claims about governance theater unless sourced. |
| Hypothesis/composite scenario | Claims about governance theater, metric gaming, and incentive failure need careful framing unless sourced. |

## Evidence Status

Sources: **G1**, **G2**, **G3**, **G5**, **G6**, **G8a**, **G8b**, **G9**,
**S1**, **S3**, **S6**, **A5**, **A10**, **D3**, **D7**.

Gaps: audit theater, metric gaming, incentive misalignment, control validation
practice evidence.

## Main Sections

1. **The near miss**: policy update or workflow change.
2. **Evaluate the workflow**: model, sources, tools, reviewers, acceptance.
3. **Turn failures into regression cases**: stale-source and escaped-error
   drills.
4. **Audit accepted decisions and control failures**: accepted decisions,
   blockers, overrides, escaped errors, and bypass findings.
5. **Retire useless controls**: controls must change acceptance or stop work.

## Practical Takeaway

For every governance artifact, ask what it changes in the workflow: acceptance,
permission, evidence, escalation, logging, throttling, or retirement.

## Possible Objections

- "Governance is required anyway": yes, but required artifacts still need
  operational hooks.
- "Evals are enough": evals need incident feedback and workflow context.
- "Control retirement is risky": keeping dead controls can hide real
  bottlenecks when those controls never change acceptance.

## Bridge

Chapter 11 creates the learning loop. Chapter 12 turns the full argument into a
30-day workflow design.
