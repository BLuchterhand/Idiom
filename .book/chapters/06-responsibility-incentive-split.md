# Chapter 6: The Responsibility And Incentive Split

Accountability requires evidence, authority, capacity, incentives, and
protection.

In the support-credit workflow, the ownership meeting assigned a name before it
assigned control. The support manager would own the customer answer. Everyone
nodded because the sentence sounded clean. Support had the ticket. Support
talked to the customer. Support would use the AI assistant to prepare the
exception recommendation and send the response after review.

The control gaps appeared in the questions from finance, compliance, sales, and
support.

Finance asked who owned the credit exposure if the amount exceeded the normal
threshold. Compliance asked who owned consistency across customers if this
exception became precedent. Sales asked why review needed to slow down a
renewal conversation that was already at risk. The support manager asked
whether she could delay the answer until the contract and billing ledger were
checked.

The room had named an accountable person. It had not given that person enough
authority to be accountable.

The AI assistant did not create that problem. It made the missing authority look
resolved. A human name could appear in the workflow while the human lacked
finance authority, compliance backing, reviewer budget, and political cover to
say no. The recommendation would still look reviewed. The customer answer
would still carry a manager's name. The decision would still be shaped by
people who benefited from speed and paid little of the downside when the answer
was wrong.

Accountability has to be more than a label: rights, evidence, capacity, and
consequences all matter.

> **Real case: Post Office Horizon.** In the UK Post Office Horizon litigation,
> the High Court found a material risk that Horizon bugs, errors, and defects
> could cause branch-account shortfalls. Subpostmasters were held responsible for
> discrepancies while the institution and supplier controlled much of the system,
> data, disclosure, and investigative posture. For this book's workflow lesson,
> the weak control was individual accountability without matching evidence
> rights. People closest to the shortfall
> were expected to answer for numbers they could not fully inspect or challenge.
>
> The consequence was years of harm to people held responsible for system
> assertions they could not independently test. Later public inquiry and
> compensation processes addressed institutional accountability, not only
> individual blame. The control lesson is ownership:
> accountability without access to evidence, authority to challenge, and
> protection from institutional pressure becomes blame, not governance (Bates
> and Others v. Post Office Ltd 2019; Post Office Horizon IT Inquiry n.d.).

## Accountability Without Control

Organizations often assign ownership faster than they assign control.

That habit appears outside AI. A project manager may be told she owns a launch,
while security owns the approval, finance owns the budget, legal owns the terms,
and engineering owns the rollback. If the launch slips, her name is on the
status report. If she tries to slow the launch for a real blocker, she may
discover that her authority is weaker than her accountability.

Human workflows have developed ways to make that split visible, at least when
the stakes are high. Approval limits clarify who can spend. Compliance review
clarifies who can reject. Incident roles clarify who commands the response and
who communicates externally. Code ownership clarifies who can accept a change.
Those controls do not remove politics, but they give the workflow something
firmer than blame after the fact. Accountability, internal-control, and finance
model-risk guidance make the same general distinction: owners need designed
controls, monitoring, evidence, and effective challenge, not only a name beside
the outcome (GAO 2021; GAO 2025; Board of Governors et al. 2026).

The support-credit workflow needed the same clarity. The support manager could
own the customer relationship and the quality of the response. She could not,
by title alone, own whether the credit exceeded finance limits, whether the
exception was consistent with policy, or whether a contract clause permitted
the proposed remedy.

If the workflow says she is accountable anyway, it has confused message
ownership with decision authority.

## When You Do Not Control The Whole Workflow

Many readers will not control the whole chain. A support leader may own the
customer answer but not the finance threshold. A staff engineer may own the AI
integration but not the policy source. A governance lead may own the review
standard but not the queue budget. A product manager may be asked to ship the
assistant while security, legal, operations, and data owners each control a
piece of the acceptance path.

That situation is normal. It is also where AI adoption becomes easy to
misrepresent. A team can be asked to "own" the AI workflow while only owning
the model call, prompt, or user interface. The rest of the decision still
depends on sources, permissions, review capacity, escalation rights, and
post-acceptance accountability that sit elsewhere.

The practical rule is simple: own only the authority you can actually control,
and document the authority you cannot.

If your team controls only the draft assistance surface, keep the AI in
discovery only or draft assistance. You can improve intake, summarize messy
records, identify missing claims, prepare review packets, and draft language
marked as not accepted. You should not let the workflow imply approval, update
authority-bearing records, apply credits, or tell a customer what the
organization has decided.

If your team controls the packet but not the decision, make the packet honest.
Name the claim, source, freshness rule, missing evidence, reviewer, and
unowned approval. Do not hide the missing owner inside a confident
recommendation. A packet that says "finance approval unavailable" is more
reviewable than a polished answer that implies finance approval already exists.

If your team is accountable for an outcome but lacks the control to accept or
reject it, escalate the mismatch before launch. The escalation should be
specific:

```text
We can own draft quality and packet completeness.
We cannot own credit approval above the threshold.
We need finance approval before customer commitment.
Until that is assigned, the AI workflow must remain reviewed proposal only.
```

This is not bureaucracy. It is boundary clarity. The alternative is worse: a
team accepts responsibility for work it cannot verify, cannot fund, cannot
block, and cannot correct when the answer escapes.

Partial authority still leaves room for progress. You can start with a
lower-authority path, collect evidence about missing owners, measure review
debt, and show where the workflow needs investment. The pilot can produce a
useful answer even if the final decision is "do not expand authority yet."

The important thing is to avoid letting AI fluency make missing authority look
resolved. A model can make an unowned decision sound finished. The workflow has
to keep it unfinished until the right owner, source, and review path exist.

## Three Kinds Of Work

AI adoption becomes clearer when three kinds of work are separated.

The model does strategy inside the task. It reads available context, proposes
language, compares options, identifies likely sources, and drafts artifacts.
That work can be valuable. In the support-credit workflow, the AI can produce a concise
case packet faster than a busy manager can assemble one from ticket notes,
policy pages, and incident updates.

The workflow owns contracts: which sources count, what the output may do before
acceptance, which claims require review, who can approve, and what happens when
the answer is incomplete. That contract rejects stale policy pages as support
for approval claims and blocks customer communication that implies approval
before finance approves the amount and compliance clears the policy issue.

Humans exercise judgment inside assigned authority. Finance judges credit
exposure. Compliance judges policy consistency. Support judges customer tone
and operational context. Sales contributes commercial urgency, but urgency is
not the same as approval authority.

When these jobs are mixed together, the system becomes hard to trust. The model
appears to make policy. The workflow hides missing evidence. The human reviewer
is left to absorb pressure without rejection authority.

The phrase "human in the loop" does not solve that problem. A human can be in
the loop as a real decision owner, a reviewer without evidence, a source of
local knowledge, a reviewer without rejection authority, or a person under queue
pressure clicking through work. The workflow has to say which condition it is
creating.

## The Incentive Map

The support exception is an incentive problem as well as an evidence problem.

Sales wants speed because the renewal conversation is close. Support wants
closure because open escalations consume time and damage the relationship.
Finance wants budget control because credits accumulate and set expectations.
Compliance wants consistency because exceptions can become hard to defend.
The customer wants an answer that recognizes the outage and the business
impact.

Those interests are legitimate. They are not identical.

A responsibility and incentive map makes the split explicit:

| Role | Wants | Pays Cost | Owns Risk | Veto Right | Escalation |
| --- | --- | --- | --- | --- | --- |
| Sales | Speed, retention | Low review cost | Renewal risk | No credit veto | Executive sponsor |
| Support | Clear response, ticket closure | Case assembly and customer time | Customer message quality | Weak until defined | Support lead |
| Finance | Budget control | Credit review time | Credit exposure | Yes above threshold | Finance leader |
| Compliance | Consistent exceptions | Policy review time | Fairness and audit risk | Yes on policy conflict | Legal or compliance lead |
| Customer owner | Relationship continuity | Account coordination | Commercial fallout | No policy veto | Account leadership |

Enforcement view:

| Condition | Owner | Enforcement Hook | Failure Consequence |
| --- | --- | --- | --- |
| Support lacks finance authority | Finance owner | Block credit approval above threshold | Escalate or narrow what the output may do before acceptance |
| Compliance source conflict | Compliance owner | Block customer commitment | Escalate policy interpretation |
| Review capacity unfunded | Executive sponsor | Throttle queue or fund review | Pilot narrows or stops |
| Informal override pressure | Workflow sponsor | Require documented escalation | Slack approval does not change decision state |

The exact roles will vary. The map matters because a workflow can separate the
people who benefit from speed from the people who pay review cost or own the
downside. If the workflow does not expose that split, the AI system will be
judged by the politics around it rather than the quality of its controls.

The map should change operations. If finance has rejection authority above a threshold,
the workflow should not ask support to approve credits above that threshold. If
compliance owns policy consistency, the workflow should not let a support
manager override a policy conflict because the customer is important. If sales
benefits from speed, sales may need a defined escalation path, not informal
pressure on the reviewer.

No map removes disagreement. A good map makes disagreement honest enough to
handle.

## Who Pays For Review

In this pilot, the risk to test is whether AI increases proposal volume faster
than the organization funds review capacity.

That is one reason accountability breaks down. A team launches an assistant
that creates more recommendations, summaries, classifications, or draft
decisions. The benefit appears in the team that receives more throughput. The
cost appears in finance, compliance, legal, security, or senior reviewers who
must check the work. If that review capacity is not funded, the process is at
risk of degrading.

The degradation may be quiet, so the pilot should test for it directly: whether
reviewers skim, approve based on surface plausibility, stop requesting sources
because the queue is long, or face escalation work no one has time for. The
workflow may still say a human reviewed the answer while review has become a
scarce resource allocated by pressure.

The support manager in the ownership meeting could not solve that alone. If the
AI assistant produces twice as many exception packets, someone must decide who
reviews them, what standard applies, and when the queue stops accepting new
work. Otherwise the workflow moves the bottleneck from drafting to reviewing
and leaves the bottleneck hidden.

Executive sponsorship becomes practical here. Sponsorship is not only
permission to buy a tool. It is responsibility for resolving the cost and risk
split. If sales receives the benefit of faster renewal support, finance and
compliance cannot be expected to absorb unlimited review work as a side duty.
The sponsor has to narrow the workflow, fund review, reduce what the output may
do before acceptance, or accept that some decisions will wait.

## What Accountable Owners Need

An accountable owner needs more than a name in a RACI chart.

First, the owner needs evidence access. A support manager cannot be accountable
for an exception decision if she cannot see the current policy, relevant
contract clause, customer-specific impact evidence, and billing status or a
clear statement that those sources are missing.

Second, the owner needs authority to reject or halt. If every rejection creates
an escalation from sales, the right to reject is ceremonial. The workflow must
state that missing evidence, stale sources, policy conflict, or insufficient
review capacity are valid stop conditions.

Third, the owner needs capacity or budget. Review time is work. If review is
expected, it must be scheduled, staffed, sampled, or throttled. Otherwise the
system depends on unpaid attention from people who already have full jobs.

Fourth, the owner needs an escalation path. Some conflicts cannot be resolved
inside the immediate workflow. A policy and contract may disagree. A large
customer may ask for an exception that finance and sales view differently. A
reviewer may be pressured to approve. The workflow needs a named path for those
cases before the first high-pressure ticket arrives.

Fifth, the owner needs escalation cover for delay. A person cannot be
accountable for safe acceptance if the organization punishes the person every
time safety slows a desirable answer.

Test the role directly: if the owner says "no" for a reason named in the
workflow, what happens next?

## Rejection Authority

Rejection authority is not obstruction. It is the workflow's permission to
reject, return, block, or escalate a packet when required evidence, authority,
capacity, or acceptance conditions are missing.

In the support-credit workflow, rejection authority might look like this:

```text
Decision state: blocked.
Reason: current contract clause unavailable and requested credit exceeds
support approval authority.
Owner: support manager.
Escalation: finance review required before customer commitment.
Customer communication: acknowledge investigation, do not imply approval.
```

That output protects the customer relationship better than a premature yes. It
allows support to respond without fabricating authority. It tells sales what is
missing. It gives finance a clear request. It leaves an audit trail showing why
the answer slowed down.

Rejection authority also protects the AI adoption program. Without it, teams
learn that the official workflow is useful only when it approves quickly. The
moment it blocks a high-value case, people start looking for side paths: a
private AI tool, an informal spreadsheet, a copied customer note, a Slack
approval that never reaches the record. Shadow AI is not always a rebellion
against rules. It can be a pressure response when the official path cannot
reject a packet and still help the business move.

Rejection authority does not mean every reviewer gets unlimited power to block
work. It must scale by consequence. A low-risk internal draft may need only user
judgment. A reviewed proposal may need a domain owner to reject unsupported
claims. A bounded action that changes money, records, or customer commitments
needs stronger approval and escalation rights.

Stopping work is healthy when the workflow protects it. The failure comes when
stopping work is informal, punished, or bypassed.

## When This Sounds Like Politics

It is politics, in the ordinary organizational sense: people have different
goals, constraints, costs, and exposure to downside risk.

Calling that politics should not make the topic feel less technical. Incentive
design changes system behavior as surely as a permission check does. If a
reviewer is measured only on queue speed, the reviewer will experience
evidence requests as friction. If a sales team is measured only on renewal
timing, it will experience compliance review as delay. If support is measured
only on ticket closure, it will experience blocked decisions as failure. The
AI system sits inside those measurements.

The responsibility map is a way to make the operating system visible.

Review the map after blocked decisions, overrides, and repeated reviewer
pressure. The review asks whether the named owner had the evidence, authority,
capacity, escalation path, and protection needed to make the decision safely. If
not, the fix is not another reminder to "use AI responsibly." The fix is to
change the workflow's rights and costs.

## A Rule For Naming Owners

Do not name an accountable owner unless the workflow also gives that owner
evidence, rejection authority, escalation, capacity, and escalation cover for
delay.

This rule is deliberately stricter than the way many organizations talk about
ownership. It has to be. AI increases the number of plausible proposals that
can arrive at a human's desk. If the human cannot inspect, reject, or escalate
those proposals, the name in the loop becomes an accountability label without
control.

The support workflow now knows which evidence a recommendation needs, who can
accept or reject it, who pays for review, and who owns the downside. The next
step is to turn some of those responsibilities into boundaries the system can
enforce.
