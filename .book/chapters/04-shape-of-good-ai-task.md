# Chapter 4: The Shape Of A Good AI Task

A good AI task defines objective, evidence, permission, output, verification,
and recovery.

In the support-credit workflow, a strategic customer had asked for a service
credit after an outage. The support queue showed the ticket, the customer's
latest complaint, and a link to an exception-policy page. Sales had already
posted in the account channel that the renewal call was coming. The operations
lead added one sentence to the ticket:

```text
Handle this exception.
```

Three capable people read the same assignment and saw three different jobs.

One analyst thought the task was to summarize the ticket and prepare a customer
update. Another thought the task was to recommend whether the credit should be
approved. A third started looking for the customer's contract and billing
history because "handle" sounded like a decision with financial effect. None of
them was being careless. The work order had not said which result would count
as acceptable.

The AI assistant made the ambiguity easier to miss. It could summarize the
ticket, find the policy page, draft a recommendation, and produce a polished
customer note in one pass. The output looked like progress. But the task shape
was still unresolved. Was the system helping a person understand the case? Was
it preparing a reviewed proposal? Was it allowed to move a customer record or
trigger a credit workflow? The same input could support all three, but each one
requires a different acceptance rule.

The first AI design choice is the shape of the work, before model choice.

> **Real case: NEDA's Tessa chatbot.** The National Eating Disorders
> Association made Tessa available as an eating-disorder prevention and
> body-image resource while it was also winding down a long-running human
> helpline. The task boundary mattered. A prevention exercise is not the same
> task as supporting people who arrive with active eating-disorder concerns.
>
> The weak boundary was relying on stated intent instead of separating prevention
> content from support, triage, crisis routing, and clinical advice. NEDA said
> Tessa was not meant to replace human interaction, but users still met it in a
> support context. After reports that Tessa gave weight-loss and
> body-measurement advice, NEDA took the chatbot offline.
>
> The consequence was a public controversy in a setting where the wrong kind of
> advice can reinforce the problem the service exists to reduce. This was not a
> clean generative-LLM case; it is mainly a task-boundary case. The control
> lesson is task shape: "help people with eating disorders" is too broad. The
> workflow has to
> separate prevention, education, triage, crisis routing, and clinical advice
> before an automated tool belongs in the path (Vinall 2023; Wells 2023; Knight
> 2023).

## The Underspecified Work Order

Human teams already know this problem. A manager who tells an analyst to "look
into this" may receive a spreadsheet, a memo, a recommendation, or an executed
change, depending on how the analyst interprets the hidden constraints.
Experienced teams avoid that ambiguity when the stakes are high. They name the
objective, the source material, the decision owner, the deliverable, the
deadline, and the escalation path.

The support exception needed that same discipline before AI entered the
workflow. A summary task would be useful and low risk: collect the customer
request, outage timeline, and open questions. A recommendation task would be
more consequential: compare the current policy, contract, customer impact, and
billing authority, then produce a proposal for review. A bounded action task
would be stricter still: apply a credit, update CRM status, or send customer
communication only after required approvals exist.

The phrase "handle this exception" did not distinguish among those tasks. It
gave background, not authority. It exposed a ticket, not a decision contract.

That failure is common because teams often ask "where can we use AI?" as if
the answer were a department, role, or tool category: support, finance,
engineering, research, or customer success. Those labels are too broad. The same
department contains tasks that are excellent AI fits and tasks that should not be
automated without stronger controls.

The useful unit is the task: its objective, evidence, permission, output,
acceptance criteria, and recovery path.

## Task Shape Before Model Choice

Model capability matters, but it cannot repair an incoherent assignment. A
stronger model may infer missing context better than a weaker one. It may ask
better clarifying questions. It may produce a more careful draft. But even a
stronger model cannot repair an assignment that hides the controlling source,
permitted effect, verification method, or stop condition. Fluency just turns
that ambiguity into premature confidence.

A task-fit diagnostic is a small way to force the question before the output
arrives:

| Dimension | Good Fit Signal | Risk Signal |
| --- | --- | --- |
| Objective | Clear outcome | Ambiguous goal |
| Evidence | Accessible sources | Hidden or contested sources |
| Permission | Read-only or bounded effect | Irreversible action |
| Output | Observable artifact | Vague judgment |
| Verification | Cheap check | Expensive or impossible check |
| Recovery | Easy rollback or escalation | No safe fallback |

The diagnostic works as an intake control. It asks whether this workflow can
judge the result well enough to let AI help.

In the support-credit workflow, the summary task passes earlier than the approval task.
The objective is clear: summarize the ticket, outage note, current customer
ask, and missing evidence. The evidence is accessible if the ticket and outage
timeline are available. The permission is read-only. The output is observable.
A human can compare the summary to the sources. Recovery is easy: correct the
summary or discard it.

The recommendation task needs more. The current exception policy must be
available. The customer contract and billing ledger must be reachable or the
output must state they are missing. The acceptance criteria must say what
counts as supported, blocked, or escalated. The reviewer must know whether the
AI is proposing a decision or merely listing options.

The bounded action task needs still more. The system must know who can approve
the credit, what amount crosses a finance threshold, whether customer
communication is allowed, what audit trail is required, and how the action can
be stopped or reversed. A prompt that says "be careful" is not a recovery path.

## Good Early Uses

Start where AI can prepare work before it can affect decisions.

AI is strongest early when it turns messy material into work a person can inspect:
a short case brief from a long ticket thread, dates and claimed impact from the
record, a standard template from messy notes, customer language for a human to
edit, options with their required evidence, or a critique of missing sources and
inconsistent claims.

Those uses are not trivial. They can save real time and improve consistency.
They also keep the acceptance problem visible. The output is a proposal,
summary, checklist, or draft. The workflow can inspect it against sources
before it affects money, records, or customers.

In software work, the same pattern appears when AI drafts tests, suggests a
refactor, or explains a failing build. The output can be valuable without being
accepted automatically. The pull request still enters review. The tests still
run. The owner still decides whether the change fits the system. The AI has
increased the proposal rate; it has not removed the acceptance rule.

For the support team, a strong early task might be:

```text
Create an exception-review packet for this ticket. Include the customer's
request, incident summary, policy source used, contract source status, billing
authority status, missing evidence, and a proposed decision state. Do not
recommend approval unless all required sources are current and present.
```

That task is much clearer than "handle this exception." It names the artifact,
limits the effect, and gives the workflow something to reject.

## Conditions That Make The Task Risky

Some task shapes should slow adoption down.

Hidden state is one risk. If the material needed to judge the answer lives in a
private spreadsheet and an account owner's memory, the task is not ready. The
same is true when the AI can reach only an outdated knowledge base while the
controlling contract amendment sits elsewhere. In both cases, the system may
produce a coherent answer from partial evidence. The bad output will not
necessarily look bad. It may be well written and sincerely wrong.

Contested authority is another. If support, finance, legal, and sales disagree
about which source controls the decision, AI cannot settle that disagreement by
choosing the most convenient source. The task should produce an escalation
packet, not an accepted recommendation.

Irreversible or externally visible effects also change the task. Sending a
customer note, changing account status, applying a credit, closing a ticket, or
marking a compliance review complete creates facts other people will act on.
Those effects need stronger boundaries than a draft.

Verification cost matters as much as generation quality. A workflow is a poor
early fit if producing the AI output takes ten seconds but verifying it takes
three hours of scarce expert time. That does not mean the task is impossible.
It means the workflow must narrow the output, sample instead of accept, create
better source access, or fund review capacity.

The hard question is not "can AI do this?" It is "can the workflow tell when
the result is good enough, and can it recover when it is not?"

## Applying The Four Modes To Task Fit

The decision acceptance contract uses four recurring task modes.

The first is discovery only. AI helps a person inspect, compare, or locate
information without producing work that can be accepted. The main controls are
source access, data boundaries, and clarity that the output is exploratory.

The second is draft assistance. AI helps an individual think, draft, search, or
reformat, but it has no operational effect by itself. Personal-assistant use is
the individual-use form of draft assistance. The main controls are data
boundaries, user responsibility, and clarity that the output is not accepted
work. This mode is appropriate for private summarization, option generation,
personal notes, and low-authority drafting where the user can inspect the
source.

The third is a reviewed proposal. AI produces a packet, recommendation, draft,
or classification that may influence a consequential decision. Evidence,
freshness, reviewer capacity, rejection rights, and auditability become
mandatory. The service-credit recommendation belongs here until the
organization has shown that stronger controls work.

The fourth is bounded action. AI can change workflow state, call tools, update
records, send messages, or trigger downstream processes. Permissions, approval
thresholds, post-action verification, logs, rollback, and kill switches become
part of the task. This mode should be narrow at first because the cost of a
mistake is no longer confined to the draft.

These modes are not maturity badges. Draft assistance may be the right permanent
design for some work. A bounded action mode may be appropriate for a low-risk,
reversible, well-instrumented task. The classification exists to stop teams from
smuggling one mode into another.

The support queue did not need to choose forever. It needed to choose for this
workflow, this quarter, with the sources and review capacity it actually had.

## The Task-Fit Diagnostic In Use

A practical intake review can be brief.

For each proposed AI workflow, the owner fills out six fields:

| Field | Question |
| --- | --- |
| Objective | What outcome should this task produce? |
| Evidence | Which sources can the workflow use and which source controls each claim? |
| Permission | What may the AI read, draft, change, send, or trigger? |
| Output | What artifact will the workflow inspect? |
| Verification | Who or what can check the artifact, and at what cost? |
| Recovery | What happens when evidence is missing, the answer is contested, or the effect fails? |

The diagnostic should change what happens next. If it only sits in a launch
document, it has not controlled the workflow.

If the objective is ambiguous, narrow the task. If evidence is hidden, keep the
workflow exploratory or invest in source access. If permission is too broad,
remove effects until approval rules exist. If the output is vague, require a
packet with fields the reviewer can inspect. If verification is expensive,
reduce volume, sample, or fund reviewers. If recovery is missing, do not launch
bounded action.

For the support exception, the first acceptable version might be a reviewed
proposal, not a bounded action:

```text
Mode: reviewed proposal.
Action limit before acceptance: create review packet and draft internal recommendation.
Not allowed: customer communication, credit application, CRM commitment, or
ticket closure.
Stop condition: current policy, contract clause, billing authority, or
customer-specific impact evidence missing or stale.
Recovery: escalate packet to finance and compliance with missing fields.
```

That small contract makes the task usable. The AI still does the tedious work of
assembling a packet, finding gaps, and drafting language. Missing authority,
however, remains missing authority.

## When The Diagnostic Feels Too Conservative

The predictable objection is that this approach excludes the most valuable
work. The large gains seem to come from letting AI handle messy, high-volume,
ambiguous tasks where humans are slow.

That objection is partly right. Many valuable workflows are messy. The answer
is sequencing, not refusal. Start where verification is cheaper than the value
created. Use early deployments to learn which sources are missing, which
decisions are contested, which reviewers are overloaded, and which effects need
stronger controls. Then widen the task only when the workflow can accept the
result without hiding missing acceptance conditions.

Another objection is that expert users can handle ambiguity. Sometimes they
can. But if the workflow depends on expert correction, the task design should
say so. It should name the expert, the evidence available to that expert, the
time budget, and the right to reject. Otherwise "expert in the loop" becomes a
polite way to hide missing acceptance criteria.

A third objection is that advanced agents can plan through ambiguity. They can
often help. They can ask questions, search sources, compare alternatives, and
identify blockers. But ambiguity about facts is different from authority to act.
An agent can discover that the contract is missing. It cannot decide that the
missing contract no longer matters.

## A Rule For Choosing The First Workflow

Choose the first AI workflow where the output can do only one narrow thing
before acceptance, the evidence is reachable, the output is inspectable,
verification is cheaper than the value created, and recovery is defined.

That rule makes learning cheaper. A team that starts with a well-shaped
reviewed proposal can observe real failure modes: stale policy links, missing
contract clauses, reviewer overload, incentive pressure, and unclear approval
thresholds. Those findings are useful because they are tied to a specific
workflow.

The support-credit workflow has now moved one step. The team no longer has a vague wish to
"use AI in support." It has a candidate task: produce an exception-review
packet and internal recommendation under defined limits. The workflow then has
to decide
whether the packet contains evidence the workflow can actually accept.

Task shape tells the organization what kind of output it is asking for.
Evidence tells it whether that output has earned review.
