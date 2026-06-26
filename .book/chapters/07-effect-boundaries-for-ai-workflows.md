# Chapter 7: Effect Boundaries For AI Workflows

Effect boundaries define what AI may access, produce, communicate, mutate, or
trigger before and after acceptance.

A familiar finance boundary makes the next step visible: an employee may
prepare a payment request without being allowed to approve, release, and
reconcile it. That boundary is not a comment on her competence. She knows the
vendor, the contract, and the reason the invoice needs to be paid quickly. She
can fill out the request accurately, attach the invoice, route it to the right
cost center, and answer questions from finance.

But the approval button was unavailable because the amount crossed a threshold.
Release required a different authority. Reconciliation checked whether the
released payment matched the approved one. The workflow preserved useful work
while preventing one actor from creating, approving, executing, and validating
a consequential effect alone.

AI workflows need the same distinction.

Back in the support-credit workflow, the support assistant is useful inside the
work: summarizing the ticket, drafting an exception packet, listing missing
evidence, and preparing customer-safe language for an owner to review. Applying a
credit, updating the customer record, sending a commitment, or closing the ticket
changes the world outside the draft. Those effects need enforceable conditions,
not only polite instructions.

Good rails preserve judgment and constrain effects.

> **Real case: Cruise pedestrian-dragging incident.** In October 2023, a
> human-driven vehicle struck a pedestrian in San Francisco and pushed her into
> the path of a driverless Cruise vehicle. The Cruise vehicle braked and made
> contact. It then attempted to pull over, dragging the pedestrian forward
> instead of remaining stationary. Cruise's recall report said the automated
> driving system inaccurately characterized the collision as a lateral collision
> and commanded the vehicle to pull over.
>
> For this book's workflow lesson, the weak control was the post-collision
> effect boundary: the post-collision
> subsystem could still command a maneuver when the safe state should have been
> "stop and stay stopped."
>
> The immediate regulatory and operational consequences were concrete.
> California suspended Cruise's driverless testing and deployment permits.
> Cruise paused driverless operations nationwide. All 950 affected ADS software
> units were recalled for a software update. Reported broader operational fallout
> later included more than 900 job cuts during a wider restructuring.
>
> The transferable analogy is effect-boundary design: once an automated system
> can produce physical or institutional effects, the workflow needs
> states where action authority is removed, not merely reduced. The Cruise
> sources support the vehicle and regulatory facts; later chapters use other
> cases for financial and record-changing effects (NHTSA 2023; California
> Department of Motor Vehicles 2023; NHTSA 2024; Associated Press 2023; The
> Guardian 2023).

## Rails As Effect Boundaries

Many teams use the word "guardrails" to mean better prompting: tell the model
to be accurate, refuse unsafe requests, cite sources, and ask for clarification
when uncertain. Prompting matters. It can improve behavior and make the model's
job clearer. But a prompt is a weak boundary for actions the workflow cannot
afford to get wrong.

The useful question is what the workflow will allow to happen, not only what the
model is told to do.

An effect boundary defines what the AI may access, what it may produce, what it
may communicate, what records it may mutate, which tools it may call, and which
conditions must be true before a consequential action proceeds. It treats an AI
output as a proposed move inside a system, not as an answer floating in a chat
window.

For the support-credit workflow, the boundary might start like this:

| Action | AI Allowed? | Required Condition | Failure State |
| --- | --- | --- | --- |
| Summarize ticket | Yes | Data class allowed | Redact or block |
| Draft recommendation | Yes | Evidence status shown | Proposal only |
| Request missing evidence | Yes | Source owner identified | Needs evidence |
| Communicate to customer | No by default | Accepted decision and communication owner | Not permitted |
| Apply credit | No by default | Finance approval and audit record | Blocked |
| Update CRM commitment | Limited | Accepted decision state | Proposal only or blocked |
| Retry after blocker | Limited | New evidence supplied | Escalate after repeated blocker |

This table needs to be executable. Each row names an action, a condition, and a
valid failure state. If the only thing preventing a customer commitment is a
system prompt that says "do not promise approval," the boundary is too weak.

## Access Is An Effect

Teams often think of effects as writes: send the email, update the record,
apply the credit, trigger the workflow. Reads can also be effects.

An AI assistant that can read customer contracts, billing history, support
tickets, incident notes, and internal compliance comments has access to
material that may be sensitive, regulated, or irrelevant to some tasks. Even if
the assistant never changes a record, it can expose information in summaries,
blend data across contexts, or carry details into a draft seen by people who
would not otherwise have permission.

The first boundary is therefore data class and source access. The workflow
should say which sources are available for which task mode. A personal drafting
assistant may not need billing history. A reviewed proposal may need the billing
status but not unrelated contract terms. A finance-approved action may need
exact ledger fields and an audit trail.

This is another place where the human parallel helps but does not fully hold.
A human employee usually has social and organizational context about why a
document is sensitive. AI systems operate through tool permissions, retrieval
indexes, prompts, and integration code. If those mechanisms expose the wrong
source, the model may use it fluently without understanding the organizational
boundary it crossed.

Access control is not separate from answer quality. If the model cannot see a
required source, the workflow should block or downgrade the task. If the model
can see a prohibited source, the workflow may create a privacy or policy
failure even when the answer is accurate.

## Communication Is An Effect

A draft becomes more consequential when it leaves the private workspace.

In the support-credit workflow, an internal recommendation that says "likely approve" can
create pressure. A customer-facing note that says the same thing can create a
commitment. An account-channel message can shape sales expectations even if it
is not sent to the customer. A CRM update can become the next team's source of
truth.

For that reason, communication boundaries need to be explicit. The AI may draft
language for an owner. It may not send the message, imply approval, or update
customer-visible status unless the decision state allows it.

A useful boundary separates several surfaces:

| Surface | Allowed Before Acceptance | Allowed After Acceptance |
| --- | --- | --- |
| Private analyst workspace | Drafts, options, missing evidence | Final notes and rationale |
| Internal review packet | Claims, evidence status, blockers | Accepted decision record |
| Sales/account channel | Neutral status only | Approved talking points |
| Customer communication | Acknowledgment without commitment | Owner-approved response |
| CRM or ticket status | Evidence-needed marker | Accepted outcome and audit link |

This may look bureaucratic until the first premature statement escapes. Once a
customer hears that a credit is likely, the later finance review is no longer a
neutral decision. Once CRM says "credit approved pending processing," the next
team may treat the approval as real. Once the account channel sees a polished
recommendation, the reviewer who blocks it may be framed as the person causing
the customer problem.

The workflow should constrain the surface, not merely the sentence.

## Machine Checks And Human Decisions

Some boundaries can be enforced by software. Others require human authority.
The workflow should not confuse them.

Software is well suited to mechanical boundaries: prohibited tool calls, CRM
writes before accepted decision state, missing approval identifiers, source
version logs, prompted actions, tool calls, reviewer decisions, and repeated
retries against the same missing source.

A system may not be able to decide whether an exception is commercially wise,
whether a policy conflict should be escalated, or whether a customer-specific
impact note is persuasive enough. Those decisions belong to domain owners.

The boundary between machine checks and human decisions should be visible. If a
rule can be encoded cleanly, encode it. If it requires judgment, route it to
the owner with the evidence packet and a real reject path. Do not leave an
ambiguous judgment hidden inside a prompt.

For the support workflow, a machine check can enforce:

- no customer send before accepted decision;
- no credit tool call without finance approval;
- no approval claim when current policy is missing or stale;
- no repeated retry after the same blocker without new evidence;
- audit logging for source versions, decision state, owner, and effect.

A human owner still decides whether the evidence supports the exception, how to
weigh customer context, and whether to escalate a policy conflict.

The point is to keep consequential effects from depending on model obedience
alone.

## Typed Blockers

When a boundary stops work, it should say why.

Generic refusal is operationally weak. "Cannot complete" does not tell the
support manager what to fix. "Policy issue" does not tell finance whether the
amount, source, threshold, or approval path is missing. A useful rail returns a
typed blocker that matches the workflow's recovery path.

For the support-credit workflow, common blockers might include:

| Blocker | Meaning | Valid Next Step |
| --- | --- | --- |
| Missing source | Required evidence is unavailable | Request source owner or escalate |
| Stale source | Evidence exists but is not current enough | Fetch current version or block |
| Authority conflict | Sources disagree or approval owner differs | Escalate to named owner |
| Permission denied | AI lacks access or effect rights | Reduce task or request authorized path |
| Review capacity exceeded | Required reviewer unavailable within limit | Queue, throttle, or narrow scope |
| Repeated retry | Same blocker recurred without new evidence | Stop and escalate |

Typed blockers keep the workflow from treating every stop as model failure. The
AI may be doing the right thing when it refuses to approve. The system may be
doing the right thing when it prevents a send. The blocker tells the team
whether to supply evidence, change permissions, add review capacity, or accept
that this task should remain exploratory.

Typed blockers also improve audit. A later reviewer should be able to see that
the decision was blocked because the contract clause was missing, not because
the model was vague. If blocked packets accumulate for the same reason, the
organization has learned something about its source systems or authority model.

## Retries, Escalation, And Kill Switches

AI workflows often fail in loops before they fail visibly.

The loop can look productive while nothing important changes. The assistant asks
for a source, receives a stale page, tries again, fails the same evidence check,
receives reviewer pressure about the customer's importance, and drafts a stronger
recommendation. The underlying blocker remains.

Retries need boundaries too. A retry is valid when new evidence, changed
authority, or corrected input has arrived. A retry is not valid when it merely
rephrases the same unsupported proposal until someone accepts it.

A practical rule is to tie retries to blocker resolution. If the blocker is
missing contract evidence, the next attempt must include the contract source or
a named escalation that says the contract is unavailable. If the blocker is
finance authority, the next attempt must include finance approval or reduce the
requested effect. If the blocker is review capacity, the workflow must queue,
throttle, or downgrade the task rather than producing more proposals for the
same unavailable reviewer.

Kill switches are the broader version of the same idea. If customer
communications escape before acceptance, disable customer sends. If credits are
applied without audit records, disable the credit tool. If stale policy pages
keep entering evidence packets, remove that source from retrieval or mark it
ineligible until ownership is fixed. A kill switch is not an admission that AI
failed. It is an operational control that keeps a bounded failure from becoming
a routine practice.

## The Audit Trail Has To Show The Boundary

An audit log that records only the final answer is not enough.

For consequential AI workflows, the log should show the decision state, source
versions, tool calls, blocked actions, approvals, owner identity, and final
effect. It should preserve enough evidence to reconstruct why the workflow
accepted, blocked, escalated, or rolled back an effect.

In the support-credit workflow, a useful record might say:

```text
Draft packet created from ticket, outage timeline, current policy, and billing
status. Contract clause missing. Recommendation state: blocked. Customer send:
not permitted. Finance approval: absent. Escalated to finance and compliance.
No credit applied.
```

That record is more valuable than a transcript that merely shows the assistant
was asked to be careful. It shows whether the boundary acted. It also gives the
team a basis for improvement. If many cases block on missing contract clauses, the
source integration may be the constraint. If many cases block on review
capacity, Chapter 8's problem has arrived.

## When Boundaries Feel Like They Limit AI

They do. That is the point.

The question is whether they limit the useful part or the dangerous part. A
well-designed boundary should leave low-risk drafting, summarization,
extraction, and critique easy. It should become stricter as the workflow moves
toward customer communication, financial effect, record mutation, or system
operation.

This is how mature human workflows behave. They do not prevent an employee
from preparing a payment request. They prevent the same person from creating,
approving, releasing, and reconciling the payment alone. They do not prevent an
engineer from writing code. They require review, tests, deployment controls,
and rollback for production changes. The constraint is aimed at the effect, not
at the existence of judgment.

AI-specific boundaries must be stronger in some places because tool-mediated
effects can happen many times before a person notices. If the integration allows
it, a model may draft many messages, update many records, or retry many actions.
A prompted instruction may reduce the chance of misuse, but permissions and
workflow state decide what can actually happen.

## A Rule For Effect Boundaries

For each AI workflow, list the allowed actions, prohibited actions, required
conditions, valid blockers, audit fields, and rollback or kill-switch path.

If that list is impossible to write, the workflow is not ready for bounded
action. It may still be ready for discovery only, draft assistance, or reviewed
proposal mode. That classification protects the work from authority it has not
earned.

The support exception workflow can now say:

```text
Allowed: summarize ticket, draft internal packet, identify missing evidence.
Conditionally allowed: update internal review state after accepted decision.
Prohibited by default: customer commitment, credit application, CRM approval
status, ticket closure.
Blockers: missing source, stale source, authority conflict, permission denied,
review capacity exceeded, repeated retry.
Audit: source versions, decision state, reviewer, approval id, blocked effects,
final action.
Kill switch: disable customer send and credit tool if effects escape before
acceptance.
```

That specification lets the organization use the AI where the work is still a
proposal and stop it where the workflow has not earned authority.

The workflow now has enforceable boundaries for some responsibilities. The next
problem is capacity: once the AI can generate more reviewable packets, can the
organization verify them without turning review into an unfunded approval step?
