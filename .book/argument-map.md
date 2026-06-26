# Argument Map

Canonical outline: `outline-v2-compact.md`

This map reflects the compact chapter structure.

## Starting Reader Beliefs

The target reader likely believes some mix of the following:

- AI is powerful but unreliable.
- The organization needs to adopt AI quickly or fall behind.
- The main adoption question is which model, tool, or vendor to choose.
- Better prompting, retrieval, evals, or policy may solve most reliability
  problems.
- Human review can catch model mistakes.
- Governance is necessary, but often slows work down.
- Productivity gains are real, but hard to measure.
- AI output should be checked, but nobody has enough review capacity.

The book should respect these beliefs. Most are partly true. The book's job is
to reorganize them into a more useful operating model.

## Ending Reader Beliefs

By the end, the reader should believe:

- AI is not trustworthy or untrustworthy in isolation. Trust belongs to the
  workflow.
- AI output begins as a proposal. Evidence supports it, verification checks it,
  accountable acceptance authorizes its use, and authority is the resulting
  permission to affect a decision, record, customer, or system state.
- Better models move the trust boundary; they do not remove the need for
  evidence, permissions, verification, incentives, review, governance, and
  learning.
- "Human in the loop" is not a control. A controlled human review process is a
  control.
- Governance matters only when it changes what the workflow can accept,
  escalate, or stop.
- The practical adoption question is: what can AI propose or do, under which
  evidence, with which owner, at what review capacity, and with what failure
  signal?

## Core Argument

AI adoption is a trust-boundary problem.

AI is valuable because it can generalize, synthesize, translate, draft, reason
through ambiguity, and navigate unfamiliar context. It is risky because fluent
output can look authoritative before it has earned authority. The answer is not
to treat AI as deterministic, and it is not to reject AI because it is
probabilistic. The answer is to put AI's strengths inside workflows that make
trust inspectable, enforceable, correctable, accountable, and politically real.

The contradiction resolves when responsibility is split correctly:

- Let AI own probabilistic work: exploration, synthesis, drafting, explanation,
  translation, and candidate generation.
- Let workflows own trust-producing work: source access, evidence, freshness,
  permissions, verification, audit, workflow state, and escalation.
- Let humans own goals, judgment, values, risk appetite, exceptions,
  accountability, and changing the workflow when evidence shows it is failing.

Central sidecar:

AI is not the first fallible producer of important work. Humans were. Mature,
high-consequence workflows often use evidence, separation of duties, review,
approval, audit, verification, and incident learning because raw human work was
not enough by itself in high-consequence settings. But those controls also fail
under pressure, overload, weak rejection authority, misaligned incentives, and
ceremony.
AI adoption extends that older lesson to a new kind of fallible contributor.

## Argument Arc

### Turn 0: The Operational Failure

Structure: opening teardown.

Reader movement:

- From "AI can be wrong" to "the real failure is fluent output escaping into
  authority before the workflow is ready."

Claims:

- The problem appears in a concrete operational decision, not only in abstract
  model reliability.
- A process can fail by accepting stale evidence, hiding unsupported decisions,
  overloading reviewers, creating audit gaps, or pushing work into shadow AI.
- The minimum viable adoption path starts with one workflow, one
  action limit before acceptance, one source of truth, one checker, one owner,
  and one stop condition.

### Turn 1: The Trust Mismatch

Structure: Chapters 1-3.

Reader movement:

- From "AI is either amazing or unreliable" to "AI is a different kind of
  contributor, so ordinary trust instincts misfire."

Claims:

- AI can exhibit useful learned capabilities without granting itself
  operational authority.
- Mature workflows learned, imperfectly, not to trust consequential human work
  raw either.
- Artifact fixes such as prompts, RAG, human review labels, policy, and eval dashboards
  are useful improvements, not decision controls by themselves.

### Turn 2: Put AI Where It Is Strong

Structure: Chapters 4-6.

Reader movement:

- From "Where can we use AI?" to "Which parts of the work are proposal,
  evidence, responsibility, and authority?"

Claims:

- Good AI tasks have clear scope, accessible evidence, bounded permissions,
  observable output, acceptance criteria, and a recovery path.
- Context is not authority. Evidence must be fresh, relevant, inspected, and
  adequate for the claim.
- Responsibility must include incentives, budget, rejection authority,
  escalation, and reviewer independence.

### Turn 3: Build Trust Into The Workflow

Structure: Chapters 7-9.

Reader movement:

- From "How do we make the model reliable?" to "What does the workflow enforce,
  verify, and review?"

Claims:

- Rails should preserve model judgment while constraining access, assertions,
  effects, retries, approvals, and escalation.
- Accepted verified output is the proposed metric for consequential AI
  workflows with verification bottlenecks.
- Human review works only when reviewers have evidence, time, independence,
  expertise, and authority to reject.

### Turn 4: Operate AI Under Pressure

Structure: Chapters 10-11.

Reader movement:

- From "AI rollout" to "AI operations under tool sprawl, bypass, incidents, and
  governance pressure."

Claims:

- Move from assistant to bounded agent only when the workflow has scoped tools,
  explicit permissions, valid failure states, and post-action verification.
- Shadow AI is a pressure pattern to detect, not only a policy violation to
  condemn.
- Trust the process only if the process itself is validated through sampling,
  rejected-decision review, stale-source drills, reviewer calibration, bypass
  detection, escaped-error review, and control retirement.

### Turn 5: Apply The Model

Structure: Chapter 12 and closing.

Reader movement:

- From theory to a Monday workflow design.

Claims:

- Start narrow.
- Scale controls by consequence.
- Use the minimum viable decision acceptance contract for low-risk work and
  stronger controls for reviewed proposals and bounded actions.
- Do not ask AI to become deterministic; build systems where AI, humans, and
  workflows each do the work they are best suited to do.

## Recurring Concepts

### Proposal Versus Authority

AI output begins as a proposal. Evidence supports it, verification checks it,
accountable acceptance authorizes its use, and authority is the resulting
permission to influence a decision or system state.

Used in: opening teardown; Chapters 1, 3, 5, 7, 8, 9, 12.

### Strategy Versus Contract

The model can choose a path through uncertainty. The surrounding workflow
defines what is allowed, what must be proven, and how completion is accepted.

Used in: Chapters 3, 6, 7, 10, 12.

### Evidence Hierarchy

Not all context is equal. Model memory, user context, retrieved snippets,
inspected sources, official records, tests, telemetry, and audits carry
different authority.

Used in: Chapters 5, 8, 9, 11, 12.

### Freshness

Evidence can expire. A source-grounded answer from stale context may be more
dangerous than an answer that admits uncertainty.

Used in: opening teardown; Chapters 5, 7, 11, 12.

### Minimum Viable Decision Acceptance Contract

The smallest useful decision acceptance contract is one decision, one source of
truth, one action limit before acceptance, one checker, one stop condition, and
one owner.
Everything else is an escalation tier.

Used in: opening teardown; Chapters 4, 7, 8, 12.

### Review Capacity

AI can increase proposal volume faster than an organization can increase
review, verification, and correction capacity.

Used in: opening teardown; Chapters 6, 8, 9, 12.

### Control Validation

A control is trustworthy only if it is itself tested. Sample accepted decisions,
review rejected decisions, drill stale sources, calibrate reviewers, detect
bypass, inspect escaped errors, and retire controls that never change
acceptance.

Used in: Chapters 8, 9, 11, 12.

### Process-Certified Trust

The book does not ask readers to trust AI output raw. It asks them to build
processes where output can earn trust through evidence, verification,
acceptance, and learning.

Used throughout.

## Objections The Book Must Answer

### "Models Are Getting Better, So This Will Be Obsolete"

Response: model improvement moves the trust boundary. It does not remove the
need to know what evidence was used, who accepted the output, what effects were
allowed, and how failure is detected.

### "This Sounds Like Too Much Process"

Response: controls scale by consequence. Low-risk personal assistance needs a
minimum packet; consequential decisions need stronger evidence, review,
verification, and audit.

### "Human Review Is Enough"

Response: human review is a control only when reviewers have evidence, time,
independence, expertise, and authority to reject.

### "We Need Speed, Not More Gates"

Response: unchecked generation can move the bottleneck into review, correction,
or risk. The goal is faster accepted verified output, not more generated work.

### "This Is Just Governance"

Response: governance matters only when it changes operational reality:
permissions, evidence requirements, queue limits, acceptance, audit,
escalation, stop conditions, or incident learning.

## Drafting Test

Every chapter should be able to answer:

- What belief changes for the reader?
- What part of the running case advances?
- What part of the decision acceptance contract becomes clearer?
- What would prove this control failed?
- What claim is evidence-backed, and what claim is still a design thesis?
