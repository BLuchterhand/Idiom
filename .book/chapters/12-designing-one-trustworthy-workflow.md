# Chapter 12: Designing One Trustworthy Workflow

A trustworthy pilot assembles the controls into one narrow workflow.

The worked example uses a recognizable support exception: a strategic customer
asked for a service credit after an outage. Support wanted a faster answer, sales
wanted the renewal protected, finance owned the credit limit, compliance owned
policy interpretation, security wanted customer data to stay inside approved
tools, and the AI team wanted to help. The design premise was fixed: a useful AI
draft is not the same as an accepted decision.

The team chose one workflow.

Not the whole support queue. Not all customer communications. Not every finance
exception. One narrow decision: should the support team recommend a service
credit for this class of outage-related customer request?

The answer would begin as a reviewed proposal only. The AI assistant could draft
and prepare the packet. It could not apply a credit, promise approval, or update
the customer-facing account record. Acceptance required current policy, the
customer contract, the billing ledger, customer-specific outage evidence, and a
finance or compliance reviewer when the case crossed a threshold.

The decision acceptance contract now runs through one Week 0 setup and three
operating weeks. The support-credit workflow remains a composite operating illustration.
Its value is not that it establishes a universal pattern. Its value is that it
forces the workflow to show its evidence, permissions, review capacity, stop
conditions, and learning loop before the organization scales the idea.

> **Real case: Morgan Stanley's internal GPT-4 assistant.** Morgan Stanley's
> public materials describe an internal wealth-management assistant designed to
> answer from Morgan Stanley content, link to source documents, and keep
> financial advisors at the center of client relationships. Because the case is
> company-reported, it should not be treated as independent proof of outcomes.
> Its value is as a positive workflow-control example. The workflow narrows the corpus,
> keeps the tool internal, shows source documents, collects feedback, and
> preserves professional authority.
>
> The contrast with the incident cases is that the control is designed before
> the output reaches clients. The lesson for a trustworthy pilot is to assemble
> controls around one bounded workflow before treating the assistant as
> client-facing authority (Morgan Stanley 2023; OpenAI 2024).

## Week 0: Set Up The Decision, Not The Program

Week 0 is where many AI programs skip the control work.

The team would rather compare models, choose a platform, draft a governance
announcement, or promise a quarterly productivity target. Those tasks may
matter later. For this pilot, Week 0 has a different job: define the smallest
decision that can be accepted or rejected under explicit rules.

The team writes the first version of the packet:

| Field | Pilot State |
| --- | --- |
| Decision | Recommend service credit? |
| Action limit before acceptance | Draft and reviewed proposal only |
| Source authority | Current policy, customer contract, billing ledger, customer-specific outage evidence |
| Checker | Finance or compliance reviewer for threshold and conflict cases |
| Stop condition | Missing source, stale source, contract-policy conflict, or queue over capacity |
| Owner | Support manager after accepted recommendation |

That table is intentionally small. It does not describe the company's AI
strategy. It describes what this workflow can accept.

Week 0 also exposes the first source-owner delay. The support knowledge base has
a policy page, but compliance says the approved version lives in a controlled
document repository. The contract summary in the CRM is useful for triage, but
legal says the executed contract and amendments are authoritative. The billing
ledger has current credit history, but only finance can confirm the threshold.
Security classifies contract and billing fields as restricted, which means the
assistant can use them only inside the approved workflow.

The pilot is already doing useful work before the model has answered a single
customer question.

The workflow has discovered which sources can support authority-bearing claims,
who owns them, and which data boundaries apply. If those facts remain vague,
the AI will inherit the organization's ambiguity and make it faster.

## Week 1: Reviewed Proposals Only

In Week 1, the team runs the assistant in reviewed proposal mode.

The AI may summarize the ticket, identify claims, draft a recommendation, and
prepare the reviewer packet. It may not update CRM fields, send customer
language, or apply a credit. Incomplete packets are auto-rejected before they
reach a human reviewer.

That last rule feels harsh until the queue starts moving.

Before the pilot, reviewers spent time reconstructing missing evidence. A
support manager would send a polished recommendation and the reviewer would
hunt for the contract clause, ask for billing history, check whether the outage
actually affected the customer, and wonder which policy version had been used.
The labor was real, but it was hidden inside Slack threads and ticket comments.

In Week 1, the packet has to show the claims and sources:

| Claim | Required Evidence | Packet Result |
| --- | --- | --- |
| Customer requested credit | Current support ticket | Present |
| Outage affected this customer | Customer-specific impact evidence | Missing in some cases |
| Credit is allowed | Current policy and contract clause | Present or blocked |
| Amount is within authority | Billing ledger and finance threshold | Missing in some cases |
| Customer can be told approval is likely | Accepted recommendation and communication owner | Not permitted until accepted |

Incomplete packets do not disappear. They become visible as incomplete. If a
packet lacks the contract clause, it returns to source collection. If the policy
version is stale, it blocks. If the customer-specific outage evidence is
missing, the assistant can draft a request for the missing evidence, but it
cannot convert the recommendation into approval.

The AI is still useful. It reduces blank-page work, standardizes packet shape,
and helps reviewers see what is missing. But the workflow refuses to buy speed
by moving evidence reconstruction into the reviewer's unpaid time.

## Week 2: Capacity Becomes A Product Constraint

By Week 2, the worked example has enough volume to confront review capacity.

Use these numbers as scenario assumptions, not as evidence about typical
support operations. They are here to show the decision math a leader needs in
the meeting. In this example, the workflow expects 47 proposals each week. At
the start, about 70 percent arrive with complete packets. The target is 85
percent. Reviewers have six funded hours a week. At the current service time,
they can review about 24 proposals well. The queue math is not a moral
judgment. It is the operating limit of the workflow.

| Field | Pilot Assumption |
| --- | --- |
| Proposals expected | 47/week |
| Complete-packet rate | 70% at start, target 85% |
| Reviewer-hours funded | 6/week |
| Max reviewed proposals | 24/week |
| Throttle threshold | Queue above 24 open proposals or review debt above 9 complete packets |
| Stop/go rule | Continue reviewed proposal only if complete packets rise and sampled audits find no escaped unsupported decisions |

This is the moment when "human in the loop" becomes a serious design question.

If the workflow generates 47 plausible recommendations and can review 24 of
them well, the loop is underfunded. If the team ignores that fact, reviewers
may work unpaid overtime, accept packets too quickly, or push decisions back
into informal channels. None of those outcomes should be counted as AI success.

The pilot therefore throttles. It prioritizes cases with complete evidence and
clear authority. It auto-rejects incomplete packets with a specific missing
source. It escalates contract-policy conflicts. It stops new proposals when
review debt crosses the threshold.

The throttle frustrates Sales. That is the point at which governance has to be
politically real. The reviewer is not slowing the business because they dislike
AI. The workflow has a stated capacity limit, and the stop condition protects
the organization from accepting unsupported decisions faster than it can inspect
them.

Week 2 also runs a stale-source drill. The assistant is given a real-looking
old policy and a current policy with a changed threshold. The workflow passes
only if the packet marks the old policy as stale and blocks acceptance. This is
not a trick. It is Chapter 11's stale-source near miss turned into a launch
drill.

## Week 2: The Bypass Check

The same week, the team looks for shadow paths.

In the worked example, they find a pressure pattern to investigate: a few
account teams have been using private AI tools to draft customer updates because
the official workflow feels slow. Some drafts use copied ticket notes and old
templates. The account teams were trying to answer customers before the
sanctioned path caught up.

Discipline matters, but the durable response is to design a path people can
use.

The team creates a sanctioned draft assistance path for low-risk customer
language. Its inputs are approved ticket summaries and public incident language,
not contract terms, billing history, or credit thresholds. It cannot state that a
credit is approved or likely. When a customer asks about money, the draft path
links into the reviewed proposal workflow.

This does three things. It gives teams a sanctioned path for low-risk drafting,
and it removes one excuse for copying restricted data into private tools. It
also preserves the distinction between assistance and authority. The sanctioned
path may help people write. It may not make financial decisions.

The bypass shows that the official path was too slow for some work. The team
creates a lower-authority path that fits the actual work instead of granting
agency prematurely.

## Week 3: The Stop/Go Decision

At the end of Week 3, the team does not ask whether the AI was impressive.

It asks what the workflow can now accept.

The review is concrete:

| Item | Final State |
| --- | --- |
| Accepted | Low-risk credit recommendations with current policy, contract cap, billing ledger, and customer-specific outage evidence |
| Blocked | Recommendations missing current policy, contract clause, billing evidence, or reviewer capacity |
| Escalated | Contract-policy conflicts and executive override requests |
| Shadow path | Private AI draft path replaced by sanctioned low-friction draft assistance path |
| Automation rejected | Direct credit application deferred until post-action verification, audit, and capacity data improve |

That last row is important. The pilot succeeds partly by refusing a tempting
automation step.

Applying credits directly would make the demo more exciting. It would also move
the workflow from reviewed proposal to bounded action. That requires stronger
permissions, post-action verification, audit sampling, rollback behavior, and a
clear rule for duplicate or erroneous credits. The team does not yet have
enough evidence that those controls work. So direct credit application remains
out of scope.

The decision scales authority more slowly than drafting speed.

## The Final Case Decision

The customer still needs an answer.

In the final review, the packet has current policy, the executed contract, the
billing ledger, and customer-specific impact evidence for a low-risk credit
below the finance threshold. Finance accepts the amount. Compliance records
that the case fits the current exception rule and does not require an executive
override. The support manager owns the customer communication after acceptance.

The customer-safe message is restrained:

```text
We reviewed your request against the current service-credit policy, your
contract terms, the incident record, and your account history. Your case
qualifies for a service-credit recommendation under the current exception
process. Our support manager will send the formal credit details after finance
records the accepted amount.
```

The message stays inside the accepted decision: no stale-policy citation, no
implied executive override, and no direct customer send from the AI assistant.

One related case is escalated because the contract and policy conflict. One
incomplete case is blocked for missing customer-specific impact evidence. A
private draft path is replaced with the sanctioned low-risk drafting assistant.
Direct credit application remains rejected for now because post-action
verification, duplicate-credit recovery, and accepted-decision sampling are not
yet strong enough for bounded action.

This is the payoff the opening lacked. The organization gives the customer an
answer, protects the reviewer, absorbs the shadow path, and refuses the tempting
automation step that would grant the system more authority than the workflow can
support.

## The Assumption Ledger

The pilot should leave behind a ledger that a skeptical operator can inspect.
Because this is a composite worked example, the right labels are assumption and
decision effect, not measured before-and-after result.

| Measure | Starting Visibility | Worked-Example Decision Effect |
| --- | --- | --- |
| Generated recommendations | Counted as activity | Counted separately from accepted work |
| Accepted verified output | Unknown | 18/week as a worked-example estimate under the 47 x 70% x capacity x 75% assumptions |
| Review debt | Hidden in Slack and tickets | 9 complete packets over funded capacity triggers throttle |
| Incomplete packets | Reviewer reconstruction work | 14/week auto-rejected with missing evidence |
| Escaped unsupported decisions | Not sampled | No unresolved known escaped unsupported decisions; sampled audit required |
| Shadow path | Private drafts visible only after leak | Sanctioned draft assistance path plus bypass check |
| Automation scope | Pressure for direct credit action | Direct credit application deliberately deferred |

This ledger changes the executive conversation.

Before the pilot, the visible metric was generated recommendations. That number
would reward output volume even if reviewers were overloaded or decisions were
unsupported. After the pilot, generated recommendations are still counted, but
they are not treated as accepted work. Accepted verified output is smaller. It
is also more accurate.

The ledger does not mark the workflow as done. It shows the next work:
complete-packet rate has to rise, review capacity is still scarce, source
ownership still matters, shadow paths need ongoing checks, and the direct-action
agent is deferred rather than forgotten.

The pilot has earned a narrower claim: this reviewed proposal workflow can run
for low-risk cases when the packet is complete, source freshness is checked,
review debt stays under the threshold, and accepted decisions are sampled.

That is a better claim than a broad promise that AI will remake support.

## The Payoff Model

The capacity model says whether the workflow can review the work. It does not
say whether the workflow is worth funding. That requires a modest payoff model
with assumptions labeled plainly.

For the support-credit workflow, the model can fit in one table:

| Field | Worked-Example Assumption | Confidence | Decision Use |
| --- | --- | --- | --- |
| Accepted verified output | 18/week as a worked-example estimate | Medium | Shows throughput under current review capacity |
| Value per accepted output | Scenario-specific estimate | Low until measured | Compares benefit with review cost |
| Reviewer cost | 6 funded hours/week | High | Makes the control cost visible |
| Source-fix cost | Compliance and finance owner time | Medium | Decides whether to repair source integration |
| Cost of delay | Renewal/customer-risk estimate | Low to medium | Decides whether to fund capacity or narrow scope |
| Rework avoided | Incomplete packets no longer reconstructed by reviewers | Low until measured | Supports source-packet investment |
| Expected escaped-error cost | Incident and audit estimate | Low until sampled | Sets stop/go risk tolerance |

The table exposes weak numbers instead of hiding them inside a confident ROI
claim. The executive decision becomes explicit:

```text
Continue reviewed proposals if accepted verified output is valuable enough to
fund review and source fixes, review debt stays under threshold, and sampled
accepted decisions show no unsupported escapes.
Narrow or stop if value is speculative, review debt grows, source ownership
does not improve, or escaped unsupported decisions appear.
```

This is only a rough financial model, but it prevents the pilot from calling
generated volume "ROI."

## When The Answer Is No

A trustworthy pilot must be allowed to reject the workflow.

The support-credit example could fail the stop/go review in several legitimate
ways. If the current policy source has no owner, the workflow cannot verify the
most important claim. If finance can fund only a fraction of the required review
time, generated recommendations will create review debt rather than accepted
work. If account teams keep moving restricted customer details into private
tools, the official workflow has not solved the bypass pressure. If sampled
accepted decisions show stale evidence or unsupported customer expectations, the
workflow is not ready to scale.

Those findings should change the decision:

| Finding | Decision |
| --- | --- |
| Source owner missing | Stop or run draft assistance until the source is owned |
| Review capacity unfunded | Narrow scope, fund review, or stop proposals |
| Bypass persists | Add a sanctioned lower-authority path or pause rollout |
| Escaped unsupported decision appears | Stop scaling and turn the case into a regression test |
| Direct action cannot be verified or reversed | Do not grant bounded action |

This is where the book's method is deliberately less exciting than an AI demo.
Some workflows are bad first candidates. Some should stay in draft assistance or
reviewed proposal only. Some should wait until source systems, review budgets,
security boundaries, or recovery paths are repaired. A process that cannot say
no has not controlled the decision. It has only added review language to work
that is still moving toward launch by default.

## The Operating Cadence

A trustworthy pilot needs a cadence plain enough to survive contact with work.

Week 0 establishes intake, task fit, data classes, source owners, what the
output may do before acceptance, reviewer commitment, stop conditions, and the
initial eval cases. Week 1 runs reviewed proposals only and rejects incomplete
packets before review. Week 2 checks queue math, runs stale-source drills, and
looks for bypass. Week 3 makes the stop/go decision and updates the workflow
before scaling.

The meetings should be short and evidence-bound:

- a queue review that compares proposal volume with reviewer capacity;
- a packet review that samples accepted, rejected, and escalated decisions;
- an incident review for stale evidence, unsupported acceptance, or bypass;
- office hours for source owners and frontline users;
- an executive review that uses accepted verified output, not generated drafts,
  as the primary adoption signal.

This cadence is not process for its own sake. Each meeting can change a rule:
throttle the queue, narrow scope, repair a source, change a permission, add a
regression case, sanction a fast path, or stop the pilot.

If a meeting cannot change the workflow, it should not be part of the operating
cadence.

## The One-Page Contract

At the end of the pilot, the leader needs one artifact that can go into the
next meeting. For the worked example, the filled decision acceptance contract
looks like this:

| Field | Filled Contract | What Changes When It Fails |
| --- | --- | --- |
| Decision | Recommend a service credit for outage-related support exception | If decision changes, rerun task-fit and source map |
| Mode | Reviewed proposal | No direct credit action or customer promise |
| What the output may do before acceptance | Draft packet, internal recommendation, missing-evidence request | Actions outside this list are blocked |
| What the output must not do before acceptance | Apply credit, promise approval, update final account status | Tool permission disabled or escalation required |
| Source/freshness rules | Current policy, executed contract, billing ledger, customer-specific impact evidence | Missing/stale source moves packet to blocked or needs-evidence |
| Reviewer roles | Support for customer context, finance for amount, compliance for policy conflict | Claim routes to correct reviewer or escalates |
| Machine gates | Required fields present, source version shown, data class allowed | Packet cannot enter review queue |
| Queue thresholds | 24 reviewed packets/week, review debt threshold 9 complete packets | Throttle, narrow, or fund more review |
| Audit fields | Claim list, source versions, reviewer, decision state, blocked effects, final customer message | Accepted decision cannot close without log |
| Bypass response | Sanctioned draft assistance path for low-risk language | Restricted private-tool use triggers incident review |
| Stop/go rule | Continue only with complete packets improving, review debt controlled, and zero escaped unsupported decisions found in the sampled audit | Narrow, reviewed-only, or stop |
| Owner and cadence | Workflow sponsor; weekly queue/sample review; Week 3 stop/go | No owner or cadence means no scale decision |

The blank version is the Monday template:

| Field | Fill This In | What Changes When It Fails |
| --- | --- | --- |
| Decision |  |  |
| Mode | Discovery only / draft assistance / reviewed proposal / bounded action |  |
| What the output may do before acceptance |  |  |
| What the output must not do before acceptance |  |  |
| Source/freshness rules |  |  |
| Reviewer roles |  |  |
| Machine gates |  |  |
| Queue thresholds |  |  |
| Audit fields |  |  |
| Bypass response |  |  |
| Stop/go rule |  |  |
| Owner and cadence |  |  |

If a row has no failure consequence, the row is not part of the contract yet.
It is a note.

## What Scaling Means

At the end of the pilot, scaling should not mean copying the assistant into
every adjacent workflow.

It should mean one of four decisions:

1. Continue reviewed proposals for the narrow case.
2. Narrow the workflow until the evidence and review capacity are adequate.
3. Expand only to a similar case with the same source and authority pattern.
4. Stop until source ownership, permissions, or review capacity improve.

Direct automation is a separate decision. It should wait until the workflow can
verify actions after they happen, recover from mistakes, sample accepted
actions, and show that the permission boundary is not broader than the decision
requires.

Smallness matters here. A small pilot is not trivial if the decision is
real, the sources are messy, reviewers are scarce, and the outcome has
consequence. Small enough to learn is different from small enough to ignore.

## The Scaling Rule

The first trustworthy AI workflow should be narrow enough to inspect and
consequential enough to matter. It should leave behind a contract that names the
decision, what the output may do before acceptance, source rules, reviewers,
gates, thresholds, audit fields, bypass response, stop/go rule, owner, cadence,
and failure consequence.

Everything else is an escalation tier.

The support pilot ends with a useful but restrained result. The AI assistant
drafts and assembles reviewed proposals for low-risk service-credit
recommendations, exposes missing evidence, reduces reconstruction work, and
helps the organization see where source ownership, review capacity, and shadow
paths are blocking trust.

The restraint matters: the assistant still cannot apply credits directly, promise
approval, make stale evidence current by summarizing it well, or turn review debt
into capacity.

That restraint is operating discipline. It lets useful AI work become accepted
work while keeping the trust problem visible.

The closing chapter returns to the human assignment underneath the machinery:
AI can propose, explore, and draft at a scale people could not. People and
institutions still own judgment, accountability, and the workflow that turns
probable output into accepted action.
