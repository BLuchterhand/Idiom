# Chapter 2: How Important Work Earns Trust

Important work earns trust through reviewable process, not confidence in the
producer. The support-credit workflow is easier to understand if we first look at a
familiar engineering control: a trusted engineer still has to pass release gates.

A trusted engineer opened a small pull request. She knew the service, understood
the release pressure, and had fixed similar bugs before. The change adjusted the
retry path for a customer-facing
workflow that had failed during a partial outage. The description was clear and the
tests she added matched the bug report. The team wanted the fix in before the
next deployment window.

And yet, the pull request still went through the normal gates. Each gate checked
a different claim about the work:

| Gate | Claim checked |
| --- | --- |
| Maintainer review | The diff matches the intended behavior and does not hide obvious risk. |
| Continuous integration | The existing test suite still passes with the change. |
| Release ownership | The retry path will not create unacceptable duplicate work downstream. |
| On-call review | The new failure mode will be visible in the dashboard. |

These gates are not signs that the engineer is untrusted. They are how the team
lets a trusted engineer's work become trusted release material.

That is the human-process parallel AI adoption has to recover. Important work
does not earn trust because the producer is competent. It earns trust when the
producer's work enters a process that can inspect evidence, catch ordinary
omissions, reject unsafe effects, and learn from misses.

> **Real case: Boeing 737 MAX certification.** Later reviews of the 737 MAX
> certification process illustrate why important engineering work cannot earn
> trust from reputation alone. Boeing and the FAA were experienced aviation
> institutions, but those reviews found that
> the certification process did not give independent reviewers enough visibility
> into MCAS, its assumptions, and its human-factors risks.
>
> The control issue for this book is fragmented delegated review: capable people
> were involved, but the process did not make the right evidence visible to the
> right reviewers before the aircraft entered service.
>
> The consequence was 346 deaths, a worldwide grounding, a long
> return-to-service review, and sustained public scrutiny of the certification
> process. The control lesson is human-process trust: consequential work needs
> evidence, independent review authority, acceptance gates, and failure states
> that can stop release.
> Confidence in the producer is not a substitute for a process that can inspect
> and reject the work (Joint Authorities Technical Review 2019; Federal Aviation
> Administration 2020; U.S. House Committee on Transportation and
> Infrastructure 2020).

## Where Expertise Fits

Professional work has long relied on capable people. It has also had to
account for capable people being tired, rushed, biased, underinformed,
politically constrained, or wrong in any manner of ordinary ways.

Software teams use tests, code review, type checks, release gates, logs, and
rollback plans because expertise alone does not make software safe to deploy.
Incident teams use timelines, telemetry, action items, and postmortems because a
plausible explanation is not the same as a demonstrated cause. Finance teams use
limits, approvals, reconciliations, and audit trails because a reasonable
expense can still violate policy or budget authority. Compliance teams ask for
current evidence because memory and intent are not enough.

These controls show a simple distinction: producing work and accepting work are
different states. AI belongs in that same frame. Model outputs can save time and
raise the quality of first drafts, but consequential workflows still have to ask
what must be true before a proposal may change a customer commitment, record,
payment, filing, or system.

The answer depends on the effect:

| Work effect | Process weight |
| --- | --- |
| Private summary | Lightweight source access and user judgment may be enough. |
| Reviewed proposal | The workflow needs evidence, reviewer authority, and a failure state. |
| Automatic credit | The workflow needs explicit permission, post-action verification, and rollback or correction. |
| Code suggestion | The workflow needs review and tests before merge. |
| Deployed change | The workflow needs release ownership, observability, and recovery. |

The control should scale with consequence.

The same inspection protects the producer. When the process is explicit, the
expert does not have to carry every risk alone in memory or reputation. The
review path can say which concern belongs to the author, which belongs to the
reviewer, which belongs to the release owner, and which should stop the work
until another source appears. That division keeps serious work from relying on
one capable person to hold every dependency at once.

## The Work Of Review

"Human in the loop" is a weak phrase because it does not say what the human can
actually do.

In our support-credit workflow, the support manager was nominally in the loop. The
manager saw the AI-generated proposal before the customer did, but that fact
alone did not make the workflow controlled. Finance authority, current policy
ownership, contract-cap evidence, renewal pressure, and the Friday deadline all
shaped what the manager could actually do. Calling that step review does not
answer whether the review could change the decision.

A real review job has materials, criteria, and effect. The reviewer needs
enough information to know what is being accepted and enough authority for the
review to change the path of the work. Without that, review becomes a confidence
signal rather than a control.

The same pattern appears outside AI adoption:

| Review form | When it stops being a control |
| --- | --- |
| Code review | It cannot block the release. |
| Incident review | It creates action items no one owns. |
| Audit checklist | It runs after the decision is irreversible. |
| Policy exception review | The reviewer cannot say no. |

What matters is what the review was allowed to do.

## Process Can Fail Too

A process can fail in ordinary ways:

- Reviewers become overloaded.
- Checklists become stale.
- Approval limits get routed around.
- Small exceptions become the real process.
- Audits turn into backward-looking paperwork.
- Incident reviews become tidy stories that protect prior decisions.
- Release gates are overridden so often that the override becomes the actual
  gate.

This matters because the answer to AI risk is not simply to "add process" in the
abstract.
A process that cannot detect its own failure becomes a performance of control.

The support-credit workflow shows the problem clearly:

| Artifact | What it says | Why it can still fail |
| --- | --- | --- |
| Credit policy | Credits above a threshold require finance approval. | The recommendation may reach the account team as a likely commitment before finance sees it. |
| AI review label | Generated proposals are non-final. | The label may not survive copying into Slack, a ticket note, or a customer update. |
| Ticket template | Evidence should be attached. | The field may be present even when the authority-bearing source is missing or stale. |
| Compliance review | Policy exceptions require review. | Compliance cannot protect the decision without rejection authority to slow or block the answer. |

The process has to be tested against the ways it will actually be pressured:
customer urgency, sales escalation, executive attention, reviewer backlog,
stale sources, unclear ownership, and the convenience of side channels.

A trustworthy process changes what can be accepted when the evidence is
missing; a complete-looking diagram is not enough.

## What AI Changes

AI does not create fallibility. It changes the rate, surface area, and opacity of
fallible work.

Rate matters because a team can generate more proposals than reviewers can
inspect. A support group that previously wrote ten exception recommendations a
week may suddenly have fifty AI-assisted drafts. If reviewer capacity remains
the same, the workflow has not gained trust. It has moved the bottleneck.

Surface area matters because AI output can travel through connected tools. A
recommendation can become a Slack summary, ticket note, CRM update, customer
email draft, and manager dashboard item. Each copy can shed the uncertainty that
belonged to the original output.

Opacity matters because AI can blend sources. A human memo often leaves visible
clues: the attached spreadsheet, the quoted policy, the email thread, the gaps
the author admits. AI can combine current records, stale retrieved documents,
user assumptions, and learned patterns in one smooth answer. The reviewer needs
help separating those materials back into claims and evidence.

Accountability matters because AI cannot be accountable in the human sense.
People and institutions remain accountable for what the workflow accepts. The
model can help produce a packet, but it cannot own the consequences of accepting
it.

These differences make reliability disciplines more valuable, not less. Better
generation raises the importance of knowing when generated work has actually
been accepted.

## The Process Evidence Check

The next artifact is a small process evidence check. It asks whether a work item
has evidence, a reviewer, an acceptance hook, and a failure state.

| Work Type | Evidence Required | Reviewer | Acceptance Hook | Failure State |
| --- | --- | --- | --- | --- |
| Pull request | Tests, diff, relevant owner review | Maintainer | Merge blocked until approval | Failing checks or unreviewed risk |
| Incident report | Timeline, logs, change records | Incident lead | Actions assigned and tracked | Unsupported root cause |
| Policy exception | Current policy, contract, billing evidence | Domain owner | Decision held until accepted | Escalate or block |

For the support-credit workflow, the row matters more than the table:

| Work Type | Evidence Required | Reviewer | Acceptance Hook | Failure State |
| --- | --- | --- | --- | --- |
| AI credit recommendation | Ticket, current policy, contract cap, billing ledger, customer impact evidence | Support, finance, compliance by domain | Customer commitment blocked until accepted | Missing evidence, stale source, or owner conflict |

Each field has to do work.

Evidence required names what the reviewer is allowed to rely on. Reviewer names
who has authority for the relevant domain. Acceptance hook says what the process
can stop. Failure state says what happens when the packet is incomplete.

If a field never blocks, escalates, logs, throttles, assigns corrective work, or
changes permission, it is not part of the control. It may be useful background,
but it should not be confused with the mechanism that earns trust.

## Accountability Preview

The broad lesson is enough for now: important work earns trust through a
reviewable process, not through confidence in the producer alone.

The next question is how to make the process concrete: which evidence can support
which claim, who has the authority and capacity to accept or reject the work, and
what the reviewer must see before the workflow can move forward.

## The Reader Handle

For any important AI workflow, do not begin with the model. Begin with the work
item.

Ask:

1. What effect can this work have?
2. What evidence is required before that action or change is allowed?
3. Who reviews which claim?
4. What can the reviewer block, escalate, or log?
5. What failure state stops acceptance?

Those questions are useful because they are old. They are the same kind of
questions teams ask about releases, incidents, approvals, audits, and customer
commitments. AI changes the implementation details, but it does not remove the
need for work to earn trust.

The next failure is also familiar. Teams add pieces of process that improve the
work but do not change acceptance. Better prompts, retrieval, labels, policies,
evals, and dashboards can all help. They can also leave the authority boundary
untouched.
