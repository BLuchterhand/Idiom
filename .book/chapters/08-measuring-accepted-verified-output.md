# Chapter 8: Measuring Accepted Verified Output

Accepted verified output measures AI-assisted work that has passed the
workflow's evidence, permission, review, and acceptance checks.

In the support-credit workflow, the adoption dashboard counted generated
recommendations. The support AI had produced forty-seven service-credit
recommendations that week. The team had asked for speed, and speed was visible.
The pilot lead could show usage, generated drafts, average drafting time, and a
clean chart moving up and to the right.

The review queue counted the bottleneck.

Six reviewer-hours were available before Friday's customer deadline. A complete
review took about fifteen minutes when the packet contained the claim list,
current policy, contract clause, outage evidence, billing threshold, and
compliance note. Fourteen of the forty-seven recommendations were missing at
least one required source. They should not have entered expert review at all.
The remaining thirty-three packets were complete enough to review, but six
hours at fifteen minutes per packet could cover only twenty-four of them.

The workflow had not produced forty-seven accepted decisions. It had produced
forty-seven proposals, fourteen incomplete packets, twenty-four review slots,
and nine complete packets of review debt.

The meeting changed once the team separated generated work from accepted work.
The question was no longer whether the model could generate more
recommendations. It plainly could. The question was how many recommendations
became accepted verified output: work that passed the workflow's evidence,
freshness, permission, review, and acceptance checks.

Accepted verified output is a practical operating metric for consequential AI
workflows with verification bottlenecks. It is not a universal economic law, and
the support-credit workflow here is a composite illustration rather than proof of typical
support operations. The metric is useful because it forces the workflow to count
what volume charts hide.

> **Real case: Michigan MiDAS.** Michigan's unemployment agency used the MiDAS
> automated system to accuse claimants of fraud and enforce penalties. The weak
> metric was processed determinations: the system could produce and enforce
> fraud findings, but that did not mean the findings had been accepted after
> evidence, review, and due process. Court records and later reporting described
> automated fraud determinations and inadequate human review, and a class action
> settlement resolved claims that automated false accusations led to seizure of
> property without due process.
>
> The consequence was not bad dashboard optics. More than 3,000 class members
> shared a $20 million settlement, and the public repair had to address refunds,
> reversed determinations, and limits on automated functions without human
> review. The measurement lesson is direct: generated or enforced output is not
> accepted verified output (Cahoo v. SAS Analytics Inc. 2019; Michigan Attorney
> General 2022; Oosting 2024; University of Michigan Science, Technology, and
> Public Policy Program 2024).

## Generated Work And Accepted Work

Workplaces already know the difference between activity and accepted work.

A software team can open many pull requests without increasing shipped
reliable software. A claims team can prepare many files without increasing
approved claims. A finance team can receive many reimbursement requests without
closing the books. The visible production step matters, but it is only one part
of the system.

Human teams use review queues, work-in-progress limits, quality gates, sampling,
and throttles because production and verification are different capacities.
Software inspection and testing research makes the cost shape visible: finding,
checking, and correcting defects is its own work, not free capacity attached to
production (Shull et al. 2009; NIST 2002). A developer can write code faster
than reviewers can understand it. An analyst can draft more memos than
approvers can accept. A support manager can prepare
more exception requests than finance and compliance can responsibly review.

AI makes this old bottleneck easier to miss. Proposal generation becomes cheap,
fast, and polished. The output looks closer to finished work than a rough human
draft. It can spread into tickets, documents, Slack threads, and account notes
before the workflow has established whether it is supported and permitted.

Generated output is useful. It is not the same as accepted output.

For the support-credit workflow, generated output means the AI produced a
recommendation or packet. Accepted verified output means the recommendation
made it through the acceptance contract:

- required claims were identified;
- authority-bearing sources were current enough for those claims;
- permissions and effect boundaries were respected;
- a reviewer had enough evidence, time, and authority to accept;
- the decision and its basis were logged.

The metric is intentionally stricter than "AI-assisted output." A draft can be
AI-assisted and still blocked. A packet can be complete and still rejected. A
review can occur and still leave the decision unaccepted. Accepted verified
output counts the work that actually crossed the trust boundary.

## The Queue Math

The first version of the calculator should be simple enough to do on a white
board.

In the Thursday support queue, the inputs were:

| Input | Example |
| --- | --- |
| Proposals expected | 47 per week |
| Complete-packet rate | 70 percent |
| Reviewer-hours available | 6 per week |
| Average review time | 15 minutes |
| Accepted packet rate after review | 75 percent scenario assumption |

From those inputs, the workflow can derive the first decision numbers.

Forty-seven proposals at a 70 percent complete-packet rate produce 32.9
complete packets, rounded to thirty-three. That leaves fourteen incomplete
packets. Incomplete does not mean bad faith or useless. It means the packet is
missing required evidence and cannot yet consume expert acceptance time.

Six reviewer-hours at fifteen minutes per review produce twenty-four review
slots:

```text
6 reviewer-hours x 60 minutes = 360 reviewer-minutes
360 reviewer-minutes / 15 minutes per packet = 24 reviewed packets
```

Thirty-three complete packets minus twenty-four review slots leaves nine
complete packets of review debt.

If the team assumes, for scenario planning, that reviewers accept 75 percent of
reviewed complete packets, then twenty-four reviewed packets produce eighteen
accepted verified recommendations:

```text
24 reviewed packets x 0.75 accepted rate = 18 accepted verified outputs
```

That last number is a planning estimate, not a claim about the true value of the
pilot. The accepted rate has to be measured during the pilot, and the value of
an accepted output depends on the workflow. A service-credit recommendation, a
legal clause review, and an incident summary do not have the same economic value
or risk.

The calculator exists to prevent a team from mistaking forty-seven generated
recommendations for forty-seven accepted decisions.

## Incomplete Packets Should Not Become Reviewer Homework

One common failure is using expert reviewers to reconstruct missing evidence.

In the support-credit workflow, fourteen packets lacked at least one required source. Some
were missing current policy. Some had the outage timeline but no
customer-specific impact evidence. Some had the ticket and draft customer
message but no billing threshold. A reviewer could chase those sources, but
that would turn review into investigation. The bottleneck would worsen, and the
workflow would hide the true cost of incomplete packets inside reviewer effort.

Human workflows often learn this lesson the hard way. A compliance team that
receives incomplete launch packets can either become the research staff for
every operating team or return the packet. A senior engineer reviewing a pull
request can either infer the missing test plan or ask the author to supply it.
A finance approver can either search for receipts or reject the expense as
incomplete.

The AI analogue is sharper because incomplete output can look finished. A
recommendation may contain confident language, a plausible rationale, and a
source citation while still missing the source that controls the decision. If
that packet enters review, the reviewer pays for the missing discipline.

The operating rule should be explicit:

```text
Required evidence missing: auto-reject to proposal state.
Reviewer action: not required until packet is complete.
Valid next step: gather missing source or narrow the recommendation.
```

Auto-reject does not mean discard. It means the output remains a proposal. The
AI may still be useful by listing the missing evidence, preparing the source
request, or narrowing the claim. But the workflow should not spend scarce
reviewer capacity on packets that are not ready.

Accepted verified output is more useful than raw generation count here. The
fourteen incomplete packets are not invisible. They are measured as incomplete
proposals, and their causes can be reviewed. If most incomplete packets are
missing customer-specific impact evidence, the workflow has a source integration
problem. If they are missing current policy, the retrieval path or policy
ownership may be wrong. If they are missing billing thresholds,
the proposed scope may exceed what the pilot can support.

## Review Debt Is A Stop Condition

Review debt is not merely an inconvenience. In consequential workflows, it is a
risk signal.

The Thursday queue had nine complete packets over funded capacity. The team had
to change either capacity or scope: add reviewer-hours, narrow the pilot to the
highest-value or lowest-risk requests, throttle generation above twenty-four open
proposals, defer lower-priority recommendations, or stop the workflow until
capacity matched demand.

What it should not do is let proposals pile up while the dashboard celebrates
generation.

Unreviewed complete packets create pressure. Sales sees a recommendation and
wants to move. Support sees a customer waiting and wants to close the ticket.
Managers see the model producing useful-looking work and ask why the team is
slow. The queue becomes a political object, not just an operational object.

Older workplace controls exist for this reason. Work-in-progress limits are not
only about efficiency. They protect quality by making overload visible. Quality
gates are not only about inspection. They prevent downstream systems from
absorbing work that has not met the acceptance standard. Throttles are not a
failure of adoption. They are the mechanism that keeps the workflow honest
about capacity.

For AI-assisted decisions, a practical threshold might read:

```text
Throttle condition: complete review queue exceeds funded review capacity for
the week, or any high-risk packet waits beyond the service-level target.

Allowed responses: add reviewer capacity, narrow scope, defer low-risk packets,
or stop generation for this workflow.
```

The exact threshold depends on the workflow. A low-risk internal summarization
tool may tolerate more backlog. A customer-credit recommendation, compliance
approval, clinical decision support packet, or security exception may need a
harder stop. The important feature is that the number changes what the workflow
can accept.

## What The Metric Can And Cannot Show

Accepted verified output is a proposed operating metric, not a complete ROI
model.

The metric tells the team how many generated proposals became accepted work
under the workflow's rules. It also exposes review debt, separates incomplete
packets from rejected complete packets, distinguishes better packet quality from
mere volume, and shows when review capacity, not drafting capacity, is the
limiting resource.

It cannot, by itself, establish organizational value.

The team still needs assumptions or measurements for value per accepted output,
reviewer cost, avoided rework, escaped-error rate, customer impact, and the
cost of delay. Those numbers should be labeled carefully. In the support-credit workflow,
the team might estimate avoided rework from rejected incomplete packets, but
that estimate is not the same as measured savings. It might assign a scenario
value to each accepted recommendation, but that value depends on the customer's
contract, the credit amount, retention risk, and downstream audit cost. It
might track escaped errors through incidents and sampled audits, but early
pilot data will be thin.

The honest scorecard keeps those categories separate:

| Measure | What It Says | What It Does Not Say |
| --- | --- | --- |
| Generated proposals | AI activity | Accepted work |
| Complete-packet rate | Source readiness | Correctness |
| Reviewed packets | Reviewer throughput | Acceptance |
| Accepted verified output | Work crossing the trust boundary | Full economic value |
| Review debt | Capacity mismatch | Root cause by itself |
| Escaped-error findings | Known misses | Total unknown risk |

This separation protects the pilot from two bad stories. The first story says
"AI generated more, so the pilot worked." The second says "review found
problems, so the pilot failed." Neither conclusion follows. A pilot can be
valuable because it exposes incomplete sources, review capacity limits, or a
scope that is too broad. It can also be unready because accepted verified output
is too low relative to cost and risk.

The metric does not decide the business case alone. It makes the business case
harder to overstate.

A sponsor still has to compare the workflow with alternatives: keep the current
manual process, narrow the AI task to drafting only, fix source ownership before
adding AI, fund more review without AI, or stop the pilot. Accepted verified
output earns a place in that comparison because it counts work that crossed the
trust boundary. It does not establish that the workflow is worth funding.

The executive question is therefore:

```text
Does the workflow produce enough accepted verified output, with low enough
escaped-error risk and funded enough review capacity, to justify the source,
review, tooling, and governance cost?
```

If the answer is no, the honest result is not a failed book method. It is a
useful stop signal. The team has learned that this workflow is not yet a good AI
adoption candidate, or that the work should remain draft assistance until source
ownership, review capacity, or recovery paths improve.

## A Weekly Scorecard

A practical weekly scorecard for the support-credit workflow can fit on one
page:

| Field | Week Example |
| --- | --- |
| Proposals generated | 47 |
| Incomplete packets auto-rejected | 14 |
| Complete packets | 33 |
| Reviewer-hours funded | 6 |
| Max reviewed packets | 24 |
| Accepted verified recommendations | 18 |
| Rejected reviewed packets | 6 |
| Review debt | 9 |
| Escaped-risk findings | Track from audits and incidents |
| Throttle decision | Narrow scope or add capacity |

The scorecard owner should be the workflow sponsor, not the model vendor and
not the reviewers alone. The sponsor is the person or group asking the workflow
to create value. That owner has to fund reviewer capacity, accept the throttle,
or narrow the use case. Reviewers should not be made responsible for a capacity
problem they cannot control.

The source authority for the scorecard should be mundane: queue logs, packet
states, reviewer time, accepted and rejected decisions, sampled audits, and
incident feedback. If the workflow cannot produce those records, then it cannot
honestly claim the metric.

The update cadence should be frequent during the pilot. Weekly is often enough
to expose the trend without turning measurement into daily ritual. After an
escaped high-consequence error, the scorecard should be reviewed immediately
because the risk model has changed.

## The Accepted-Output Test

Generated volume is a weak success measure for consequential AI work because it
stops before the trust boundary.

Accepted verified output asks a narrower and more operational question: how
many AI-assisted proposals became accepted work after evidence, permissions,
review, and logging did their jobs?

The answer may be lower than the adoption dashboard suggests. That is useful
because it shows where the real constraint is: incomplete packets, unready source
systems, too little review capacity, an overbroad pilot scope, or accepted work
that is not valuable enough to fund the bottleneck.

The failure test is simple. If the team cannot measure accepted verified output
from logged accepted decisions, or estimate expected throughput from proposal
count, packet completeness, review capacity, acceptance rate, and escaped-risk
feedback, it is measuring activity around the workflow rather than the workflow
itself.

Once the queue makes review capacity visible, the next question is whether the
review itself can change acceptance. A human in the loop answers who is present.
The workflow still needs a packet the reviewer can judge, time to judge it,
and authority to reject or escalate when the right answer is no.
