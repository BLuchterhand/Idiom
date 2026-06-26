# Opening Teardown: The Fluent Output That Escaped

A fluent output escapes when it starts doing the work of an accepted decision
while the workflow still labels it a draft. The composite support-credit
scenario used throughout this book begins with a genuinely useful AI-generated
proposal. It organizes a messy thread, proposes a plausible credit, and gives
the account team language. The failure begins when
people treat that proposal as the current plan before anyone has checked the
evidence, confirmed who can accept the decision, or preserved the reviewer's
rejection authority.

A strategic customer had asked for a service credit after an outage. The support
manager had a renewal call on Monday, an account team pressing for language by
the end of the day, and a ticket thread that had already collected three
different versions of the story. The customer wanted to know whether the company
would make the incident right. Sales wanted a fast signal. Finance wanted to
avoid another informal exception. Compliance had been warning for months that
credits handled through side conversations were becoming hard to defend.

The support manager asked the AI assistant for a proposed path. The answer had
real utility. It summarized the ticket clearly. It identified the customer's
business impact. It found a policy family that seemed relevant. It proposed a
discretionary credit and drafted language the account team could use:

```text
Based on the incident severity and the customer's strategic importance, approve
a discretionary service credit and communicate that the company recognizes the
impact.
```

The draft solved an immediate work problem. It gave the manager a coherent
position where there had been a scattered thread. It used the vocabulary the
company used for severe incidents. It sounded measured rather than defensive.

The answer then moved through ordinary work channels.

The support manager pasted a shortened version into Slack with a careful note:
"AI draft, likely path, checking finance." The phrase "likely path" did more
work than intended. A sales lead replied, "Great, we'll tell the customer we are
working toward credit approval." A ticket note captured the recommendation as
the current plan. An account update said the team expected to approve a credit
pending final review. Finance entered the thread after the expectation had
already formed.

Several checks were still missing: the current approved policy, the customer's
contractual credit cap, prior credits in the billing ledger,
customer-specific impact evidence from the outage timeline, and compliance
review against recent exception decisions.

The AI produced useful work, but the workflow had no durable state between
generated text and organizational reliance.

> **Real case: Air Canada chatbot.** In 2024, the British Columbia Civil
> Resolution Tribunal held Air Canada liable after its website chatbot gave
> a customer incorrect bereavement-fare guidance. The customer relied on the
> answer, booked travel, and was later denied the refund path the chatbot had
> described.
>
> Air Canada's attempted fallback was that the correct policy was available
> elsewhere on the site. The tribunal rejected that as a defense because it did
> not repair the customer's reliance on the chatbot answer. For this book's
> workflow lesson, the missing control was a customer-facing answer path that
> checked chatbot guidance against current policy or routed unresolved fare
> questions to a human owner. The tribunal ordered compensation.
>
> The lesson was larger than the refund amount because the chatbot answer became
> the organization's represented position. The practical boundary is direct: a
> customer-facing AI answer is not a private draft. The workflow has
> to keep policy sources current, route uncertainty, and prevent unsupported
> guidance from becoming the organization's answer (Moffatt v. Air Canada
> 2024; Lifshitz and Hung 2024).

## The First Leak Is Social

Most AI adoption failures do not begin with a dramatic system takeover. They
begin with ordinary work habits.

A person under pressure receives a useful draft. The draft relieves ambiguity.
The person shares it to get agreement on the next step. The recipient reads a
proposal as a direction. Another team records the direction in a system of
record. By the time the missing evidence appears, reversing the expectation is
harder than refusing the proposal would have been.

This can happen without AI. A confident analyst writes a memo from incomplete
notes. A manager summarizes a hallway conversation as a decision. An account
lead forwards "probably approved" language to a customer team. Mature
organizations use approval limits, review packets, current source records, and
separation of duties because human drafts can also become commitments before
they have earned that status.

AI changes the mechanics. It makes the draft faster, more polished, and easier
to replicate into every work surface connected to the team. A human might leak a
proposal through one conversation. An AI-assisted workflow can leak through
Slack, a ticket note, a CRM update, an email, a copied summary, and a follow-up
automation before anyone has asked what the work may change, send, update,
route, or trigger.

The useful question is what state the work is in, not whether the draft sounds
responsible.

## The Proposal Was A Bundle Of Unchecked Claims

The Friday proposal sounded like one decision. It was really a bundle of
claims:

- the outage affected this customer;
- the incident met the policy threshold for a credit;
- the customer's contract allowed the proposed remedy;
- the credit amount was within finance authority;
- prior credits did not change the decision;
- compliance risk was acceptable;
- support could tell the account team approval was likely.

Different sources answer different claims. The support ticket shows what the
customer asked for, not contract eligibility. The outage timeline describes the
service incident, not whether this customer crossed the impact threshold. The
policy defines exception criteria, but it does not override the customer
contract. The billing ledger shows prior credits; finance still owns approval for
a new exception. Compliance notes identify consistency risk without granting
finance authority.

The AI draft compressed those distinctions into a paragraph. That compression is
one of the reasons the tool was useful. It is also why the workflow needed a
separate acceptance process.

Professionals already know this pattern. A software pull request is not merged
because the author is articulate. It is merged after the diff, tests, review,
and release risk are acceptable for the change. An incident timeline is not
accepted because the first narrative is plausible. It is checked against logs,
metrics, deployment records, and witness accounts. A finance approval is not
valid because an expense explanation sounds reasonable. It depends on receipts,
limits, budget ownership, and approval authority.

Important work becomes trustworthy when the organization can inspect how a
proposal was promoted into authority.

## "A Human Reviewed It" Is Too Vague

After the Slack thread spread, the first defense was predictable: a human had
reviewed the output.

That sentence did not answer the operational questions:

- Which human reviewed it?
- Which claims were they reviewing?
- Which sources could they inspect?
- How much time did they have?
- Could they say no?
- Could they stop customer communication?
- Did the workflow record what was accepted and what was still missing?

Human review becomes useful when it has shape. A reviewer who can edit tone but
cannot access the contract is not reviewing eligibility. A support manager who
can judge customer urgency but cannot approve the credit limit is not reviewing
financial authority. A compliance reviewer added after the account team has
already signaled likely approval is reviewing under political pressure created
by the workflow itself.

The support manager in this composite case was not careless. The workflow placed
the manager in an incoherent role: accountable for the customer response while
lacking several forms of authority needed to accept the decision. Finance owned
the credit limit, compliance owned policy interpretation, legal or account
operations owned parts of the contract record, and incident operations owned the
outage evidence. The manager could assemble a packet and request review, but
could not make those claims true by reading a fluent answer.

AI adoption often inherits an old organizational flaw. A person is
made accountable for an outcome but not given source access, rejection authority,
reviewer capacity, escalation cover, or permission boundaries. The AI system
then produces more work for that already ambiguous role.

The phrase "human in the loop" matters only when the loop can change what the
workflow accepts.

## The Missing State Between Draft And Decision

The first repair is modest: attach a decision state label to the work before it
can travel.

For the support-credit workflow, the label could have looked like this:

| Work Item | Current State | Allowed Effect | Required Next Check |
| --- | --- | --- | --- |
| AI-generated proposal | Proposal | Internal review only | Claim-to-source check |
| Slack summary | Working note | No customer commitment | Owner correction if overstated |
| Ticket note | Draft record | No final decision | Evidence packet |
| Customer message | Not permitted | Blocked | Named owner accepts decision |

When this table is wired into ticket status, customer-send permissions, and
reviewer routing, it changes the allowable effects. The proposal helps the
support manager organize the case without implying approval. The Slack summary
coordinates review without creating a customer expectation. The ticket note
records work in progress without becoming the final decision. Customer
communication stays blocked until required sources are checked and the named
owners approve the decision.

The state label also makes failure visible. If the proposal reaches the
account team as likely approval, the workflow has failed even if the final
answer later turns out to be acceptable. The failure is premature authority. A
good outcome reached through an uncontrolled path is still evidence about the
path.

This matters because many AI rollouts judge success by whether the generated
answer was "right enough." That is too narrow for consequential work. The system
can fail by accepting stale evidence, bypassing a required owner, overloading
reviewers, hiding uncertainty, or creating political pressure before review. The
wrong answer is only one failure mode.

## What The Workflow Should Have Done

The improved Friday flow is not complicated.

The AI assistant can still summarize the ticket, list likely policy questions,
draft an internal reviewer packet, and propose customer language. Those are
valuable tasks. But the workflow should attach the output to a state:

```text
Decision state: proposal.
Action limit before acceptance: internal review only.
Missing checks: current policy, customer contract cap, billing authority,
customer-specific outage impact, prior credits, compliance consistency review.
Customer communication: not permitted to imply approval.
```

That one state changes the social path. The Slack message becomes a review
request, not a likely commitment. The ticket note records that evidence is
missing. Sales can see why support has not promised a credit. Finance can review
the amount before the expectation hardens. Compliance can object before the case
becomes politically expensive to unwind.

Controls should scale with consequence. If an employee asks for help rewriting a
private note, a heavy evidence packet would be wasteful. But when a workflow can
affect customers, money, records, or system state, the work needs a promotion
path. It needs to say what the AI output currently is, what it may do before
acceptance, what evidence is missing, who can accept it, and what happens when
those conditions are not met.

This is the decision acceptance problem in miniature.

## A Monday Version

The Monday action is to write a one-page decision-acceptance preflight for one
workflow.

Do not start with an AI policy for the whole company. Pick one workflow where
AI output could plausibly affect a customer, financial exposure, an operational
record, or a system action. For that workflow, write down the output's
pre-review state, what it may do before acceptance, what it must not change,
send, update, route, apply, or trigger, and what has to be true before anyone
treats it as accepted work.

Then answer six questions:

1. Is this discovery only, draft assistance, reviewed proposal, or bounded action?
2. What may the output do before acceptance?
3. Which authority-bearing source answers the most important claim?
4. Who can accept, reject, or escalate the decision?
5. What reviewer capacity exists this week?
6. What stop condition forces the workflow to narrow, throttle, or refuse the
   automation?

The first output is not a governance program. It is a preflight sheet that a
team can inspect before the AI output reaches a customer, record, account, or
tool:

```text
Workflow:
Authority mode:
What the output may do before acceptance:
What the output must not do before acceptance:
Authority-bearing source:
Required reviewer:
Reviewer capacity:
Accepted state:
Blocked state:
Escalation path:
Stop condition:
Audit record:
```

Before launch, run a small control test against that sheet. Use historical
decisions if they are available. Include at least one stale-source drill. Ask
reviewers to pass or fail packets using the proposed criteria. Define thresholds
for accepted, rejected, escalated, and incomplete work. Define rollback criteria
for stale evidence, missing authority, or review overload.

The reader should leave this opening with one practical standard: a useful AI
output is not ready because it sounds right. It is ready only when the workflow
can say what state it is in, what it may do before acceptance, which source
controls the claim, who can accept it, and what happens when any of those checks
fail.

The workflow is not safe forever, but it no longer treats a fluent answer as
the same thing as an accepted decision.

The opening failure was ordinary: a useful output escaped before anyone had
checked the evidence, confirmed who could accept the decision, or preserved
rejection authority. The rest of this book builds the contract that should have
stood between the draft and the customer. The first step is naming the
category error underneath the escape.
