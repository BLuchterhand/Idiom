# Opening Packet: Workflow Teardown

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat.

Use the support exception as a composite design scenario until support-specific
sources are added. The opening should not claim that this exact incident
happened inside a named company or industry.

## Opening Job

Show the reader the whole adoption failure before naming the framework. The
opening should make the problem visible: useful AI output escapes into work
before the organization has established evidence, permission, review capacity,
or accountable acceptance.

## First Visible Moment

It is late Friday afternoon. A strategic customer wants a service credit after
an outage. Support asks the AI assistant for a recommendation. The answer is
fluent enough to forward: it summarizes the ticket, names a policy clause,
proposes a credit, and drafts language for the account team.

Before anyone has checked the current policy, contract cap, prior credits,
customer-specific outage evidence, or finance authority, the recommendation is
already in Slack. Sales treats it as the likely answer. Support updates the
ticket note. Finance sees the thread after the expectation has formed.

The failure is not that the AI was useless. The failure is that the workflow had
no accepted state between "generated" and "organizationally relied upon."

## Scene Beats

1. The AI draft is genuinely useful: concise summary, plausible decision,
   professional customer language.
2. The visible pressure is real: renewal risk, customer frustration, Friday
   deadline, executive visibility.
3. The first leak is social, not technical: a Slack message turns a proposal
   into an expectation.
4. The second leak is operational: the ticket note records the recommendation
   before evidence review.
5. The third leak is political: the support manager is named accountable but
   lacks finance and compliance authority.
6. The sources disagree: stale knowledge-base policy, current approved policy,
   customer contract, billing ledger, and outage timeline answer different
   questions.
7. The first repair is modest: label the output as proposal, identify missing
   evidence, and stop customer communication until acceptance.
8. The opening ends by naming the practical question: what has to be true before
   AI output can affect customers, money, records, or system state?

## Workplace Behavior Parallel

The same failure can happen without AI. A confident analyst, a hurried manager,
or a persuasive account lead can turn a draft into a commitment before review.
Organizations use approval limits, review packets, evidence trails, and
separation of duties because human work also becomes dangerous when it moves
too quickly from proposal to authority.

## Human Fallibility Pattern

People tend to trust fluent work that arrives at the moment they need relief.
They also tend to defer hard checks when the output appears coherent and the
political cost of delay is visible.

## Existing Workplace Contract

Before important work changes customer expectations, financial exposure, or
system records, mature workflows usually ask:

- What decision is being made?
- Which source can answer each material claim?
- Who has authority to accept, reject, or escalate?
- What may the output do before acceptance?
- What happens when evidence is missing or reviewers are overloaded?

## AI Analogue

AI makes the old failure faster and more polished. It can draft a plausible
packet before the workflow knows whether the packet is current, permitted,
reviewable, affordable, or accepted.

## Analogy Break

AI can produce many such proposals at once and can route them through connected
tools. A human draft may leak through conversation; an AI-assisted workflow can
leak through Slack, CRM, ticketing systems, emails, automation rules, and copied
side channels.

## Operator Artifact

Initial decision-state label:

| Work Item | Current State | Allowed Effect | Required Next Check |
| --- | --- | --- | --- |
| AI recommendation | Proposal | Internal review only | Claim-to-source check |
| Slack summary | Leaked expectation | No customer commitment | Owner correction |
| Ticket note | Working note | No final decision | Evidence packet |
| Customer message | Not permitted | Blocked | Accepted decision owner |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | Support manager owns the review request; finance/compliance own acceptance of their domains. |
| Source authority | Current policy, contract, billing ledger, outage evidence. |
| Update cadence | Rechecked for every exception request. |
| Failure signal | Recommendation travels outside review before required evidence exists. |
| Operational hook | Block customer communication and require reviewer packet before external commitment. |
| Escalation/retirement | If leaks continue, remove direct sharing path and require reviewer packet before Slack/customer output. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use sources for AI limitations, human review failures, process controls, and governance principles. |
| Normative control standard | It is fair to argue that consequential workflows should label generated work before acceptance. |
| Composite scenario | Treat all support-credit details as illustrative unless support-specific sources are later added. |

## Practical Takeaway

The first adoption question is not whether the AI output sounds right. It is
what state the work is in and what it may do before acceptance.

## Bridge

The opening shows the failure. Chapter 1 explains the category error underneath
it: useful AI work is not the same thing as accepted work.
