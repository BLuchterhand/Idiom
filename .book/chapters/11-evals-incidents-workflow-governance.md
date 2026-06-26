# Chapter 11: Evals, Incidents, And Workflow Governance

Evals, audits, incidents, and governance are controls only when they change what
the workflow can accept.

In the support-credit workflow, a sampled audit found a stale policy source in
an accepted packet. No customer had complained. No money had moved outside the
approved range. The support manager had followed the visible process: an
AI-generated service-credit recommendation came in with a summary, a reviewer
looked at it, and the case was accepted as a low-risk exception.

The recommendation had used a policy page that looked official but was no
longer current. The reviewer had seen the policy language in the packet, but not
the source age. The account history was current. The ticket was current. The
outage note was current. The one source that controlled the exception rule was
stale.

The team could not explain the failure by pointing to one careless person. The
AI assistant had retrieved a real document. The reviewer had checked the packet
that the screen presented. The support manager had accepted a recommendation
that appeared to satisfy the policy. The governance document said current policy
should be used, but the workflow had no freshness gate that could stop the
packet.

The team could update the policy document, remind reviewers to be careful, and
record the incident as handled. Or it could change the workflow that had
accepted the bad packet.

The workflow change is the useful response. Evals, audits, incidents, and
governance matter when they change what the workflow can accept, stop,
escalate, measure, throttle, or learn. When they only record that risk exists,
they may satisfy a paperwork need, but they have not made this decision process
more controlled.

The support-credit workflow here remains a composite illustration. It is not proof that
this exact failure is typical. It is a useful pressure test because the failure
is ordinary enough to recognize: the visible process can be followed, and the
process can still accept a packet it should have blocked.

> **Real case: Waymo flooded-road recall.** In NHTSA recall 26E026, filed in May
> 2026, an unoccupied Waymo
> vehicle encountered a flooded higher-speed roadway, detected potentially
> untraversable water, but proceeded at reduced speed. For this book's workflow
> lesson, the weak control was the prior operating scope: the vehicle could still
> enter a situation the system had not handled well enough. Waymo's Safety Board
> reviewed the issue and decided to conduct a recall. The interim remedy changed
> the approved operating scope with weather-related controls and map changes
> while a final remedy was developed.
>
> The consequence was a formal recall and a narrower operating envelope while
> the remedy work continued. The control lesson is governance as an operating
> loop: incident evidence changed where and when the system could operate,
> rather than remaining a post-launch report. Treat this flooded-road recall as
> one date-bound example, not a complete account of every later Waymo recall; a
> later June 2026 NHTSA recall concerned freeway construction-zone behavior
> (NHTSA 2026a; NHTSA 2026b; NHTSA 2026c; NHTSA 2026d).

## The Near Miss

Operations teams know this pattern from incident review.

After an outage, the weak version of a review finds the person closest to the
failure and asks them to try harder. The stronger version asks what the system
accepted, what it failed to observe, which alarm did not fire, which runbook was
wrong, which test was missing, and which owner can change the condition before
the next event. Postmortems, regression tests, monitors, runbooks, and ownership
changes are not valuable because they are solemn. They are valuable because they
alter the operating system around the work.

The same distinction applies to AI governance.

The stale-policy packet did not call for a new slogan about responsible AI. It
called for a workflow change. The packet needed a freshness check for sources
that carry authority. The reviewer screen needed to show source age next to
source status. The eval set needed a stale-source drill. The audit sample needed to
include accepted decisions, not only rejected or escalated ones. The governance
owner needed a way to retire the older control that asked reviewers to "confirm
policy" without giving them the policy version.

A policy reminder might still be appropriate. But the reminder is not the fix.
The fix is the change that prevents the same acceptance failure from passing
silently next time.

## Evaluate The Workflow, Not Only The Model

Model evals matter. They can show whether a model follows instructions,
extracts claims, cites sources, refuses unsafe requests, or handles known test
cases. Standards and risk-management guidance increasingly treat evaluation,
monitoring, documentation, governance, and risk treatment as lifecycle or
management-system concerns rather than one-time launch checks (NIST 2023; NIST
2024; International Organization for Standardization 2023).

But the support-credit failure was not only a model failure.

The model retrieved and summarized a real source. The source was stale. The
reviewer packet hid the source age. The acceptance rule did not require a
current-source check. The audit did not sample enough accepted decisions to
catch the pattern earlier. The queue pressure made reviewers prefer packets
that looked complete. Each component contributed a small part of the failure.

An eval that asks only, "Did the model answer correctly?" is too narrow for
this class of work. The workflow also needs to ask:

1. Did retrieval bring in an authority-bearing source?
2. Was the source current for the claim it supported?
3. Did the model distinguish proposal, evidence, and accepted decision?
4. Did the reviewer see the missing or stale evidence?
5. Did the queue give the reviewer enough time to reject?
6. Did the accepted packet leave an audit trail that can be sampled later?
7. Did bypass paths appear because the official process was too slow?

Model evaluation belongs inside the decision system where the model is used.

Evaluation scope follows effect scope. A discovery only or draft assistance task
may need only a model eval because the output has no operational effect. A
reviewed proposal also needs source mapping, packet completeness, reviewer
calibration, and accepted-decision sampling. A bounded action adds permission
checks, post-action verification, rollback behavior, and incident reporting.

The workflow is the unit of trust assessment: model, sources, tools,
permissions, reviewers, incentives, logs, and stop conditions together.

## Turn Failures Into Regression Cases

Software teams already understand one useful discipline: when a bug escapes, a
good fix often includes a regression test. The test is not a punishment. It is a
memory aid. It keeps the system from accepting the same failure after people
forget the details.

AI workflows need the same habit.

The stale-policy near miss should become a regression case. The case should
include an old policy page, a current policy page, a customer contract with a
narrower cap, a ticket that creates pressure, and a draft recommendation that
looks plausible if the stale page is accepted. The workflow should pass only if
it marks the stale source, blocks acceptance, and routes the packet to the
right owner.

Other regression cases come from different failure shapes:

| Failure Found | Regression Case |
| --- | --- |
| Accepted decision based on stale evidence | A stale-source drill with an old policy and current contract |
| Escaped unsupported decision | A packet with a fluent recommendation but missing authority |
| Reviewer overload | A queue simulation where complete packets exceed funded review hours |
| Bypass use | A case where the official path is slower than a sanctioned draft assistance path |
| Tool overreach | A bounded agent tries to apply a credit without checked evidence |

These cases should not be decorative. They should run before expanding scope,
after changing the model, after changing retrieval, after changing source
systems, and after materially changing reviewer screens or permission tiers.

The workflow should preserve the lesson the incident paid for without freezing
itself around one historical case.

## Audit Accepted Decisions And Control Failures

Audits often focus on obvious exceptions: escalations, overrides, rejected
packets, or high-value cases. Those are important, but they are not enough.

The near miss lived inside an accepted decision. That is why it mattered. The
workflow had converted an AI proposal into a decision with operational authority
without noticing that the authority-bearing source was stale.

Accepted-decision sampling asks a simple question: when the workflow said yes,
what exactly did it accept?

For the support-credit workflow, a useful audit sample would inspect:

- the claim list in the accepted packet;
- the source used for each authority-bearing claim;
- source version and freshness status;
- reviewer decision and any override;
- queue state when the reviewer accepted the packet;
- whether the customer-facing communication matched the accepted decision;
- whether a bypass path existed for the same case.

The audit should also inspect control failures, not only decision outcomes. If
ten packets were blocked for missing billing evidence, that may show the gate is
working. It may also show the billing source has no owner or the integration is
too slow for the workflow. If reviewers reject many packets for the same missing
source, governance has found an operating problem, not merely a model-quality
issue.

Governance can protect reviewers here. A reviewer who rejects a packet
for missing contract authority should not have to argue from personal caution.
The workflow should give them a named stop condition, an escalation path, and a
record showing that the rejection followed the acceptance contract.

Good governance changes the politics of saying no. It turns "I am not
comfortable" into "this packet lacks the source required for acceptance."

## Control Validation Loop

The operator artifact is a control validation loop: a small table that connects
signals to review actions and workflow changes.

| Signal | Review Action | Workflow Change |
| --- | --- | --- |
| Accepted decision based on stale evidence | Stale-source drill | Freshness gate |
| Rejected incomplete packets | Queue and source review | Source owner fix |
| Reviewer overload | Capacity check | Throttle or WIP limit |
| Bypass use | Bypass postmortem | Sanctioned fast path |
| Escaped unsupported decision | Incident review | Regression case |

This table is deliberately plain. Its value is that every row has an operational
hook.

The owner of governance maintains the loop. Workflow owners implement changes.
Incident records, sampled decisions, eval results, audit logs, reviewer
calibration, and bypass reports provide the evidence. The cadence can be
monthly in steady state, but high-consequence escapes should trigger immediate
review. A control fails when it records risk but changes no acceptance rule,
permission boundary, evidence requirement, escalation path, logging behavior,
throttle, or retirement decision.

The last item matters. Some controls should be retired.

If a checklist item does not block packets, change routing, inform sampling,
affect permission, or help incident review, it may only be adding visible
process without a hook. Removing a dead control can feel risky because it
reduces visible process. But keeping ineffective controls can hide the fact that
the real bottleneck is elsewhere: missing source ownership, reviewer capacity,
stale retrieval, or an unauthorized fast path.

Retirement should be cautious, documented, and tied to evidence. The practical
question is what the workflow loses if the control disappears.

## Governance That Can Say No

One failure mode deserves an explicit test: the governance body approves a tool,
records caveats, and then has little practical control over daily use. The safer
claim is an operating test: governance is weak for this workflow when it cannot
change acceptance, permission, evidence, escalation, logging, throttling, or
retirement.

That test keeps the discussion out of slogans.

Governance review has to change the workflow it inspects. Stale policy evidence
calls for a source-freshness fix. A six-hour reviewer budget for a twelve-hour
queue calls for throttling, narrower scope, or more review capacity. Private AI
drafts caused by a slow sanctioned path call for a low-friction draft assistance
alternative, not only a warning. A direct-credit agent that applies changes
without post-action verification should lose that permission until the missing
control exists.

Governance becomes operational when it can alter the path of work.

The most important governance question is therefore not, "Do we have a policy?"
It is, "What changes when the policy discovers a failure?"

## The Workflow-Change Test

For every eval, audit, incident review, dashboard, policy, or committee, ask one
question:

```text
What can this artifact change in the workflow?
```

Acceptable answers are concrete: it can block acceptance, change a permission,
require evidence, route escalation, add logging, trigger a throttle, create a
regression case, or retire a dead control.

If the artifact cannot do any of those things, it may still have legal,
communications, or coordination value. But it is not yet governing the work.

The near miss did not require the team to become more solemn about AI. It
required the team to make the next stale-source packet harder to accept. That is
the difference between governance as a record of concern and governance as part
of the workflow.

The pieces now fit into one narrow operating design: a Week 0 setup, a
three-week pilot, and a stop/go decision that blocks scaling until the workflow
can accept, reject, escalate, throttle, and learn.
