# Chapter 13: Working With The Machine

AI outputs are proposals until workflow controls accept them for a permitted
effect.

## The Live Obligation

The support manager still has to answer the customer. Evidence, permissions,
review capacity, evals, governance, and workflow design do not remove that live
obligation: a customer is waiting, sales wants to protect the renewal, finance
owns the credit limit, compliance owns the policy, and support owns the
relationship in the moment when the answer has to be given.

AI can help in that moment by summarizing the ticket, comparing policy language,
preparing a reviewer packet, drafting customer language, and surfacing the
contract clause that conflicts with the general exception rule. That work is
useful before the organization is ready to grant the system any authority.

Usefulness and acceptance are different states.

## Proposal Before Authority

The argument is simple enough to state plainly: AI output starts as a proposal.
A workflow accepts it for use only after required evidence is checked and a
named owner accepts the decision or authorizes the permitted effect.

That sentence is a design constraint, not a slogan for buying less AI or more
governance software. A probabilistic system can produce work that is valuable,
incomplete, surprising, stale, overconfident, or exactly what the team needed.
The workflow has to decide which of those states it is in before the output
changes a customer record, releases money, updates code, advises a patient,
files a report, or tells a person what the organization has decided.

People learned this lesson before AI.

We do not trust a finance manager's memory of the policy when a payment crosses
a threshold. We do not trust a developer's confidence without review, tests, and
deployment controls. We do not trust an incident story merely because it is
coherent. We do not trust a compliance packet because it is formatted well.
Those controls are imperfect. They can lose their operational hook. They can be
starved of time, ignored by executives, or bent by incentives. But the
underlying lesson is durable: important work earns trust through evidence,
authority, verification, and accountability, not through fluency alone.

> **Real case: Deloitte Australia report.** In 2025, Deloitte Australia agreed
> to partially refund the Australian government after a published report was
> found to contain citation and reference errors, including errors in a court
> case summary. The report was for the Department of Employment and Workplace
> Relations; public reporting described suspected AI-generated errors, and
> department correspondence said generative AI tools likely contributed to
> identified errors in publishing-related tasks.
>
> The episode exposes a source-verification lesson for professional delivery:
> the report carried institutional weight before the citations, references, and
> case summary had been checked well enough. The report was revised and
> republished with an added disclosure that Azure OpenAI had been used, and
> Deloitte agreed to a partial refund while maintaining the report's findings
> and recommendations.
>
> The consequence was a public correction, a financial concession, and a public
> challenge to the reliability normally expected of a professional report. The
> lesson matches the book's closing claim: professional review does not make
> probabilistic output authoritative unless the workflow verifies claims, sources,
> and citations before delivery (McGuirk 2025; Australian Department of
> Employment and Workplace Relations 2025a; Australian Department of Employment
> and Workplace Relations 2025b; Australian Department of Employment and
> Workplace Relations 2026).

## What AI Changes At Scale

AI changes the mechanics of that older problem.

It changes volume, mixture, reach, and drift. The system can generate more
proposals than people can review, combine current records with stale documents
and learned patterns in one confident paragraph, act through tools when the
workflow grants them too broadly, make bypass easier when the official path is
slow, and change behavior when the model, prompt, retrieval system, vendor, or
source corpus changes.

## What The Output Can Affect

Consequence should set the control level. A private summary of a meeting note
does not need the same acceptance contract as a customer credit recommendation.
A draft assistance path does not need the same permission boundary as an agent
that changes workflow state. The point is to know which kind of work you are
doing.

The minimum practical question is:

```text
What can this output affect?
```

If it affects only the user's private thinking, the controls can be light:
clear data boundaries, user responsibility, and easy access to the source. If
it becomes a reviewed proposal, the workflow needs evidence, freshness,
reviewer capacity, rejection rights, and audit. If it can take bounded action,
the workflow also needs explicit permissions, post-action verification, rollback
or correction paths, incident handling, and a learning loop.

The pilot should measure the operating cost. The cost may not be the model. It
may be the missing source owner, the stale policy repository, the overloaded
reviewer, the executive metric that counts drafts as impact, or a private-tool
path that appears because the sanctioned path cannot keep up.

AI adoption exposes the operating system around work. It shows where knowledge
was informal, where authority was unclear, where review was underfunded, where
security boundaries were assumed rather than designed, and where speed pressure
made the unofficial path rational. That evidence is the material from which a
trustworthy workflow is built.

## Start With One Workflow

Start small.

Pick one workflow and make the contract explicit: the decision, what the output
may do before acceptance, the source that can support each important claim, the
checker and review capacity, the stop condition, and the owner who accepts the
decision after the workflow has done its job. Run the pilot long enough to see
stale evidence, incomplete packets, queue pressure, bypass, and at least one
contested escalation.

Use those results to decide.

The result may be reviewed proposals, a draft assistance path, a narrower scope, more
funded review, source repair, reviewer protection, or a decision to delay
direct-action agents. Each choice grants authority at the pace the workflow can
support.

## Grant The Next Effect From Evidence

Better models matter. They may make better drafts, catch more inconsistencies,
summarize more context, use tools more effectively, and reduce some failure
rates. A workflow should take those gains seriously. The argument of this book
does not depend on models staying weak.

The opposite is closer to true: capability increases the need for clear
authority. A model that writes a poor draft is easy to keep at the edge of the
workflow. A model that prepares a strong recommendation, cites plausible
sources, routes a packet, and offers to update a record will be invited closer
to the decision. The trust boundary moves because the work becomes more useful.

That is when the old shortcut becomes dangerous. The organization sees better
output and concludes that the workflow can grant more authority. Sometimes it
can. But the evidence has to come from the workflow, not from the impressiveness
of a single answer.

The question is not whether the new model sounds better. The question is what
changed in the operating record:

- Did accepted verified output increase?
- Did stale-source blocks still fire?
- Did reviewers keep enough time to inspect the packet?
- Did unsupported decisions escape less often?
- Did incident review change a control, not only a prompt?
- Did the lower-authority path reduce private-tool bypass?
- Did the owner after acceptance still have evidence and rejection authority?

If the answer is yes, the workflow may be ready for more authority. If the
answer is no, the model improvement is still useful, but the authority boundary
has not moved yet.

More agency raises the same question with higher stakes. A draft assistant can
be wrong without changing a record. A bounded agent can route, update, notify,
retry, schedule, apply, or trigger. Each added effect needs a matching failure
state, audit record, correction path, and owner. The workflow should grant the
next effect only when it can observe and recover from that effect.

This discipline is not caution for its own sake. It is how useful systems earn
more room. Authority should increase when the workflow can show that it
accepts, rejects, escalates, throttles, and learns under real pressure.

That also means authority should sometimes not increase. The right next step may
be a better draft assistance path, a source repair, a review budget decision, or a
plain refusal to automate a consequential action. The book's promise is not that
every AI workflow can be made reliable enough by adding more controls. The
promise is that the workflow can expose when trust has been earned and when it
has not.

## The Work Ahead

The best AI system helps people find the evidence, limits, and consequences
sooner.

Working with AI therefore requires a practical kind of ambition: build workflows
where machine speed increases useful output without replacing human
accountability. Confident text should not replace uncertainty management. A
working demo should not become authority before the organization knows how the
workflow accepts, rejects, escalates, and learns.

The customer still needs an answer. AI can draft that answer faster. The packet
can expose the sources. Stale-source blocking can stop unsafe claims. Named
acceptance can show who accepted the recommendation, who authorized the effect,
and why.

That is the work ahead: designing the process that lets useful AI become
trusted work.
