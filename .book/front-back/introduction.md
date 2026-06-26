# Introduction: Trust Belongs To The Workflow

Most teams do not need another reminder that AI can be wrong.

The harder problem is that useful AI output arrives in the middle of real work.
A customer is waiting. A ticket is open. A sales team wants language before a
renewal call. A reviewer has two hours left in the week. A policy source has
moved, but the old knowledge-base page still ranks first. A model produces a
clean recommendation that reduces ambiguity for everyone in the thread.

That recommendation may be useful. It may also be unsupported, stale,
unauthorized, or incomplete. The danger is not only the model's error. The
danger is the workflow treating the output as if it has already earned the
right to affect a customer, record, payment, legal filing, codebase, medical
judgment, or operational decision.

This book uses one operating rule:

```text
AI output starts as a proposal. A workflow accepts it for use only after
required evidence is checked and a named owner accepts the decision or
authorizes the permitted effect.
```

That rule is not anti-AI. It is the condition that lets organizations use AI
more confidently. A model can summarize, synthesize, draft, compare, translate,
search, reason through ambiguity, and prepare candidate work. Those are real
capabilities. They become valuable in production when the surrounding workflow
knows what the output may do before acceptance, which evidence supports each
claim, who can accept the decision, how review capacity is funded, and what
happens when evidence is missing.

## The Book's Practical Question

The wrong first question is usually:

```text
Can we trust this model?
```

A better question is:

```text
What process would make this model's useful work safe to accept here?
```

The answer depends on consequence. A private note summary does not need the
same control as a customer-credit recommendation. A draft assistance workflow
does not need the same boundary as an agent that updates records or triggers
payments. A low-risk writing aid can leave final judgment with the user. A
reviewed proposal needs evidence, freshness, reviewer capacity, acceptance
criteria, and audit. A bounded action needs explicit permissions,
post-action verification, rollback or correction paths, incident handling, and
a learning loop.

The book's promise is narrow on purpose: design one AI workflow your
organization can trust, measure, and scale. One workflow is enough to expose the
real work. You will find stale sources, contested authority, overloaded
reviewers, unclear rejection authority, private-tool bypass, executive impatience, and
metrics that count generated output as if it were accepted value. Those
findings are not side issues. They are the adoption work.

## What This Book Is Not

This is not a prompt cookbook. Prompt quality matters, but better prompts are
not decision controls by themselves.

This is not a model-selection guide. Better models matter, but stronger
capability moves the trust boundary rather than removing it.

This is not a generic governance manifesto. Governance matters only when it
changes what the workflow can access, accept, escalate, log, throttle, stop, or
retire.

This is not an argument that AI is too unreliable to use. It is an argument that
probabilistic systems should do the work they are good at while workflows own
evidence, permissions, verification, acceptance, and learning.

## How The Argument Builds

The opening teardown shows a useful AI-generated proposal escaping into
organizational reliance before anyone has confirmed the evidence, authority, or
review capacity behind it.

Chapters 1 through 3 name the trust mismatch. AI can produce useful
judgment-like work without granting itself authority. Important human work did
not earn trust raw either. Prompts, retrieval, review labels, policies, and
eval dashboards can improve the work object, but they do not control the
decision unless the workflow changes what can be accepted.

Chapters 4 through 6 put AI where it is strong. They define task fit,
claim-level evidence, source freshness, responsibility, and incentives.

Chapters 7 through 9 turn that shape into enforceable workflow. They define
effect boundaries, measure accepted verified output, and make human review a
real control.

Chapters 10 and 11 operate the workflow under pressure. They distinguish
assistants from bounded agents and make evals, incidents, and governance change
the workflow instead of only documenting it.

Chapter 12 assembles the pieces into one three-week pilot. Chapter 13 closes on
the discipline that keeps the system honest: grant authority at the pace the
workflow can support.

The appendices are working tools. Use them to design your first pilot, not to
create a binder of policies. If a field cannot block, route, require, log,
throttle, escalate, or stop work, it is not yet a control.
