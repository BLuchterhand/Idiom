# 90-Minute AI Workflow Pilot Workshop

Use this workshop before a team starts an AI workflow pilot.

## Required Attendees

- Workflow owner: owns the business outcome.
- Engineering or platform owner: owns system behavior and integration.
- Reviewer or reviewer manager: owns review capacity and rejection workflow.
- Source owner: owns the highest-risk source or policy.
- Risk, compliance, security, or legal partner when the workflow affects
  customers, money, regulated records, production systems, or restricted data.

## Inputs

- One candidate workflow.
- One recent example or ticket.
- Current source or policy documents.
- Current reviewer queue or capacity estimate.
- Known vendor or internal AI tool constraints.

## Agenda

| Time | Activity | Output |
| ---: | --- | --- |
| 0-10 min | Name the workflow and decision. | One decision statement |
| 10-20 min | Classify permitted and prohibited effects. | Discovery only / draft assistance / reviewed proposal / bounded action mode |
| 20-35 min | Split the output into claims. | Claim list |
| 35-50 min | Map claims to sources and freshness rules. | Claim-to-evidence matrix |
| 50-60 min | Define reviewer actions. | Accept packet, accept decision, authorize effect, reject, return incomplete, escalate, or block |
| 60-70 min | Estimate review capacity. | Proposal threshold and review-debt stop condition |
| 70-80 min | Identify bypass pressure and sanctioned lower-authority path. | Bypass check |
| 80-90 min | Make the Week 0 decision. | Continue, discovery only, narrow, or stop |

## Facilitator Prompts

| Activity | Ask This | Write This | Do Not Accept |
| --- | --- | --- | --- |
| Name the workflow and decision | What exact work state changes if this succeeds? | One sentence naming the decision or state. | "Improve productivity" or "use AI for support." |
| Classify effects | What can this output change, send, update, route, apply, trigger, or approve? | Allowed and prohibited effects before acceptance. | "A human will check it" without a prohibited effect. |
| Split into claims | What statements must be true before the recommendation can be used? | Claim list with required/supporting/context labels. | One broad recommendation treated as one claim. |
| Map claims to sources | Which source controls each required claim, and how current must it be? | Source owner, source date/version, freshness rule. | A citation that is background context only. |
| Define reviewer actions | What can the reviewer do besides approve? | Accept packet, accept decision, authorize effect, reject, return incomplete, escalate, block. | Review without rejection or return-incomplete authority. |
| Estimate capacity | How many complete packets can reviewers inspect with funded time? | Review-capacity scorecard and review-debt threshold. | Hope-based capacity or unpaid overtime. |
| Identify bypass pressure | Where will people go if the official path is too slow? | Bypass risk and sanctioned lower-authority path. | Treating bypass as user misconduct only. |
| Make Week 0 decision | What must be true before launch? | Continue, stay discovery only, narrow, or stop. | Launching with unresolved source ownership. |

Example good answer:

```text
The workflow may draft a credit recommendation and reviewer packet. It may not
issue credit, update billing, send the customer message, or mark the exception
approved before finance verifies the amount and the support manager accepts the
decision.
```

## Facilitation Rules

- Keep the discussion about one workflow.
- Write down disagreements as source conflicts or authority conflicts.
- Do not let "human review" stand unless the reviewer has evidence, time,
  authority, and a protected no.
- Do not let "the vendor has governance" stand unless a feature can block,
  route, log, throttle, escalate, recover, or stop work in this workflow.
- Convert every "we should" into a field, owner, threshold, or stop condition.

## Workshop Exit Criteria

The team may start a reviewed proposal pilot only if it has:

- a named decision;
- a prohibited effect before acceptance;
- a source map for the highest-risk claims;
- reviewer actions that include rejection and return-incomplete;
- a funded review-capacity estimate;
- a stop condition for stale source, missing source, review debt, and escaped unsupported decision;
- an owner after acceptance.

If any item is missing, the output may still be useful, but the workflow should
stay in discovery or draft assistance.
