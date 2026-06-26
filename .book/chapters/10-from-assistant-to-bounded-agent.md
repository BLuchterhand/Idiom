# Chapter 10: From Assistant To Bounded Agent

Assistance and agency differ by authority: drafting work is not the same as
tool-mediated action that changes workflow state.

In the support-credit workflow, side paths appeared because the official process
could not handle legitimate drafting pressure. The official support AI workflow
was slow. It required a complete evidence packet, current policy, contract
clause, billing threshold, and review before a service-credit recommendation
could be accepted. Those controls were there for good reasons. They also meant
the support manager could not answer a strategic customer as quickly as sales
wanted.

One account lead copied ticket notes into a private AI tool to draft a customer
message. A support lead kept a spreadsheet of prior exceptions because the
official packet took too long to assemble. A manager reused an old template
from a shared drive because it sounded right. A side Slack thread collected
"looks good to me" approvals that did not change the formal decision state.

Security saw customer context moving through an unapproved tool. Finance saw
credit decisions forming outside the credit workflow. Compliance saw stale
templates returning through the side door. Support saw the same operating
problem from another angle: the sanctioned path could not meet the pressure
placed on it.

Treat the bypass as both a policy violation and a signal that the official path
lacks a usable lower-authority option.

That pressure pattern is the bridge to bounded action: before granting more tool
authority, the workflow needs a sanctioned lower-authority path for the work
people are already trying to do.

An assistant may draft, summarize, classify, and prepare packets without
changing the world. A bounded agent can take actions through tools: update a
ticket, route an approval, send a message, issue a credit, change a
configuration, or close a case. That move changes authority, not only
capability.

Move from assistance to agency only when the workflow has scoped tools,
explicit permissions, valid failure states, sanctioned alternatives, logging,
and post-action verification.

> **Real case: Knight Capital.** In 2012, Knight Capital deployed code into an
> automated equity router with direct market access. A dormant function that was
> no longer intended for use was triggered. While processing 212 customer
> orders, the router sent millions of orders into the market and obtained more
> than 4 million executions.
>
> For this book's workflow lesson, the weak control was the absence of effective
> action-specific stops: the
> system could keep acting in the market even though its behavior was erroneous,
> and Knight lacked controls that could stop orders from exceeding firm-wide
> capital thresholds.
>
> The consequence was visible in market and capital terms. During the event,
> Knight acquired several billion dollars in unwanted positions. Unwinding those
> positions left the firm with a loss of more than $460 million.
>
> The control lesson is direct: once software can act through tools, markets,
> records, or money, the workflow needs action-specific limits, automated stops,
> incident authority, and clear conditions for disconnecting the system from the
> effect (Securities and Exchange Commission 2013a; Securities and Exchange
> Commission 2013b).

## Shadow Processes Are Older Than AI

Workarounds existed before generative AI.

Teams have long used spreadsheets beside official systems, copied templates
instead of current forms, local approval lists instead of central workflows, and
side-channel messages to move work around a slow process. Sometimes those
workarounds are irresponsible. Sometimes they are the only way people can meet
conflicting demands. Often they are both.

The familiar human-workflow issue is not hard to understand. People create local
paths when the sanctioned path is slower than the work requires. A support team
with a waiting customer will optimize for response. A sales team near renewal
will optimize for relationship. A finance team will optimize for control. A
compliance team will optimize for defensibility. If the official workflow does
not reconcile those pressures, side paths are a pressure pattern worth
investigating.

AI changes the mechanics. A private tool can ingest sensitive context, produce
polished customer-facing language, and spread unsupported recommendations
faster than an old spreadsheet. An unofficial agent can act through connected
tools if permissions are loose. A copied stale template can become the seed for
many fluent drafts.

The mature response asks why the bypass exists, which risks it creates, and what
sanctioned path can handle the legitimate need without giving away authority.

In the support-credit workflow, the first repair is a low-friction sanctioned draft path,
not a credit-issuing agent:

```text
Allowed: summarize ticket, draft customer-neutral holding response, list
missing evidence, prepare source request.

Not allowed: imply credit approval, send customer commitment, update billing,
or change decision state.
```

That path gives the team speed where speed is safe, while preserving the
acceptance contract for financial and policy effects.

## Assistance, Proposal, And Action

The word "agent" often hides the decision that matters.

The useful distinction is effect authority. What can the AI-mediated system
change?

At the lowest tier, discovery only has no operational effect and produces no
accepted work. It can help a user find, compare, or inspect information. The
main controls are source access, data boundaries, and clarity that the output is
exploratory.

At the draft assistance tier, the system can help a user think, summarize
private notes, or draft text the user will inspect. Personal-assistant use
belongs here when it remains private and low authority. The main controls are
data boundaries, user responsibility, and clarity that the output is not
accepted work.

At the reviewed proposal tier, AI prepares work for an accountable workflow. It
may draft a support recommendation, classify a request, assemble evidence, or
suggest a route. The output can influence a decision, but it cannot accept the
decision. Evidence, review, permissions, and logging become mandatory.

At the bounded action tier, the system can mutate workflow state. It might
update a ticket status, request a finance review, send an approved message, or
apply a credit under a threshold. This tier requires stricter controls because
the system is no longer only producing language. It is causing effects.

For the support workflow, the tiers might look like this:

| Tier | Allowed Work | Not Allowed |
| --- | --- | --- |
| Discovery only | Inspect approved sources | Customer commitment, drafting, or restricted data |
| Draft assistance | Private drafting with non-sensitive context | Customer commitment or restricted data |
| Reviewed proposal | Assemble packet and recommend action | Approve credit or send final message |
| Bounded action | Route approved packet, update permitted fields | Exceed threshold or bypass review |

This distinction keeps the organization from granting agency by accident. A
chat tool connected to the ticket system may feel like an assistant until it can
write ticket notes customers or auditors later read. A drafting tool may become
a decision tool when its output is automatically copied into a CRM field. A
routing helper may become an authority system when downstream teams treat its
status as accepted.

The control should follow the effect, not the marketing label.

## The Tool Inventory

The operator artifact is a tool inventory with authority tiers.

For the support-credit workflow, an initial inventory might read:

| Tool Or Path | Data Class | Allowed Use | Effect Authority | Logging |
| --- | --- | --- | --- | --- |
| Chat assistant | Low/internal | Draft and summarize | None | Optional |
| Official support AI | Ticket/contract | Reviewed proposal | No direct credit | Required |
| Credit agent | Restricted | Bounded action | Threshold-limited | Required |
| Private AI tool | Unknown | Not sanctioned | None | Missing |

Disposition view:

| Tool Or Path | Owner | Deadline | Enforcement Hook | Failure Consequence |
| --- | --- | --- | --- | --- |
| Chat assistant | Team lead | Current sprint | Data-class reminder | Remove restricted examples |
| Official support AI | Workflow owner | Pilot week 1 | Required packet and audit log | Proposal cannot be accepted |
| Credit agent | Finance/security | Future gate | Disabled until post-action verification exists | No direct credit action |
| Private AI tool | Security/governance | Pilot week 2 | Block restricted data and provide sanctioned draft path | Incident review if restricted data appears |

The table is simple, but it does several jobs.

It separates data access from effect authority. A tool may read a ticket without
reading a contract, draft a message without sending it, or route an approval
without approving the credit. Those distinctions matter because most operational
harm does not come from "AI use" in the abstract. It comes from particular data
crossing a boundary or a particular effect occurring without acceptance.

It names logging requirements. Personal draft assistance may not need the same
audit detail as a credit agent. A tool that touches restricted customer data or
changes workflow state needs logs sufficient for review, incident response, and
learning. If the organization cannot tell which tool produced a customer
message or changed a status field, it cannot govern the workflow after the
fact.

It exposes private paths. The private AI tool in the table is not sanctioned for
this workflow. The workflow owner should ask why it is being used. If the
official path is too slow for safe drafting, the organization should provide a
sanctioned drafting path. If people are trying to avoid review for financial
decisions, the organization should block the effect and escalate the behavior.

Tool inventory without an operational hook becomes paperwork. The hook is:
sanction a low-friction path, retire duplicate tools, block unsanctioned
effects, or reduce agency until logging and rollback exist.

## Permissions Should Match The Failure State

Permissions are not only about what the system can do when everything goes
well. They are about what happens when the system is uncertain, wrong, blocked,
or under pressure.

The support workflow needs valid failure states:

```text
Missing required evidence: remain proposal-only.
Stale source: block acceptance and request current source.
Authority conflict: escalate to finance or compliance.
Queue over capacity: throttle generation or narrow scope.
Post-action check failed: reverse permitted action or open incident.
```

Those states should be available to the AI-mediated system and to the humans
around it. If the only available tool action is "continue," the system will
continue through uncertainty. If the only socially acceptable human action is
"approve," the reviewer will absorb the risk.

Permissions should be scoped to those states. A reviewed proposal tool does not
need permission to issue credits. A packet assembler does not need permission to
send customer commitments. A bounded credit agent, if one exists later, should
have a threshold, approved source requirements, logging, and a way to fail
closed when evidence or authority is missing.

The AI analogy breaks from ordinary human delegation here. A human
employee understands social context, can be disciplined, and remains
accountable in ways a model cannot. An AI agent acts through tool permissions.
If the permission exists and the workflow does not block the action, the system
may produce real effects even though no accountable human has accepted the
decision. Accountability stays with the organization.

The permission model should therefore encode the acceptance contract rather
than rely on the model to be careful.

## Post-Action Verification

Bounded action requires checking after action, not only before action.

Even a well-scoped agent can fail by updating the wrong field, routing to the
wrong queue, applying a threshold incorrectly, or acting on stale state. External
systems can lag, permissions can be misconfigured, policies can change, and
users can supply misleading context. The workflow needs to detect and recover
from bad effects.

For an agent in the support-credit workflow, post-action verification might include:

- confirm the action matched the accepted packet;
- confirm the credit amount stayed within the approved threshold;
- confirm the customer message did not imply unapproved terms;
- confirm the ticket, billing note, and audit log agree;
- sample accepted actions for later review;
- open an incident or rollback path when verification fails.

Post-action verification changes the design conversation. The question is not
"Can the model do this?" The question is "Can the workflow detect when the
system did the wrong thing, limit the damage, and learn from the failure?"

Some actions are easy to reverse: a ticket label can be corrected, a routing
decision can be changed, and a draft message can be deleted before sending.
Other actions are harder. A customer commitment, a financial credit, a legal
filing, or a production change can create consequences that cannot be cleanly
undone.

The harder the reversal, the stronger the boundary should be before action.

## Sanctioned Alternatives Reduce Bypass

A policy that only says no can move risk elsewhere.

If support cannot use private AI tools but the official workflow cannot produce
a holding response quickly, the pressure remains. People may paste less data
into private tools, use personal accounts, copy old language, or ask colleagues
for side approvals. The organization will have less visibility, not less need.

The better pattern is to provide sanctioned alternatives at lower authority
tiers. For the support-credit workflow, the sanctioned assistant can help with safe work:

- summarize the customer's request from approved ticket fields;
- draft a neutral response that does not promise credit;
- list missing evidence for the packet;
- prepare a request to finance or compliance;
- explain why the decision is blocked in customer-safe language.

This path gives the team a place to work while the acceptance contract does its
job. It also creates logs and feedback. If the same missing evidence appears
every week, the workflow can fix the source path. If the same customer-safe
holding response is needed repeatedly, the team can
approve a standard template. If review debt keeps rising, the sponsor can fund
capacity or narrow scope.

Sanctioned alternatives also make enforcement more credible. Blocking private
tools for restricted data is easier when teams have a usable approved path, and
unofficial credit recommendations are easier to prohibit when the official
assistant can still help them prepare the packet.

## The Agency-Readiness Test

The move from assistant to bounded agent should be made slowly and explicitly.

Test the move to agency with specific questions:

1. What data can the system access?
2. What effect can it cause?
3. Which evidence is required before that effect?
4. Who accepts the decision?
5. What happens when evidence is missing?
6. How is the action logged?
7. How is the action verified afterward?
8. What sanctioned lower-authority path exists when the answer is no?

If the workflow cannot answer those questions, it is not ready for agency. It
may still be ready for assistance. That distinction lets the organization get
useful speed without smuggling authority through a tool connection.

The failure test is also concrete. If teams use unofficial tools because the
sanctioned path cannot meet demand, treat the bypass as evidence. If an agent
can change customer, financial, legal, security, or production state without
checked evidence and post-action verification, reduce its authority. If logs
exist only to assign blame after the fact, redesign them so they support
learning, audit, and recovery.

AI adoption does not become trustworthy when every assistant becomes an agent.
It becomes trustworthy when each workflow knows which actions are still drafts,
which are reviewed proposals, and which bounded effects the system is allowed to
cause.

Once bounded agents exist, failures will still happen. The organization then
has to decide whether failures and near misses are paperwork events or signals
that should change evals, controls, and governance.
