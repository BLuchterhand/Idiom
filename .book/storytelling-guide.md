# Storytelling Guide: Make The Control Obvious

## Core Rule

Do not introduce an AI control first. Start with the human version professionals
already understand.

The book's strongest teaching pattern is:

1. **Workplace scene:** show a familiar human behavior under pressure.
2. **Human fallibility pattern:** name the fallibility that makes raw trust
   unsafe.
3. **Existing workplace contract:** show the control mature, high-consequence
   workflows often use.
4. **AI analogue:** show how AI creates the same trust problem faster, more
   fluently, or at greater scale.
5. **Analogy break:** name what is different about AI: scale, opacity,
   non-human accountability, tool authority, retrieval behavior, data leakage,
   prompt injection, or vendor drift.
6. **Decision acceptance contract:** name what the workflow must accept, block,
   escalate, log, throttle, or verify.

The reader should repeatedly think: "Of course. We learned the shape of this
problem with people, imperfectly. AI changes the mechanics, but not the need
for a trust-producing process."

## The Forehead Test

A section passes the test when a professional reader can visualize the example
without extra explanation:

- a finance manager approving spend near a limit;
- a senior engineer reviewing a plausible pull request before a release;
- a support manager facing customer pressure after an outage;
- a compliance reviewer rejecting an incomplete packet;
- an incident lead refusing a confident root-cause story without logs.

Then the section should make the control feel obvious:

- receipts, limits, reconciliation, and segregation of duties;
- tests, code review, CI, observability, and rollback;
- current policy, contract authority, approval limits, and audit trails;
- evidence packets, rejection rights, and escalation paths;
- telemetry, timelines, postmortems, and regression cases.

If the reader would not say "we already learned this lesson somewhere in human
work," rewrite the example before adding more AI terminology.

## Scene Pattern

Use this shape for major assertions:

```text
At 4:30 on Friday, a support manager has a strategic customer waiting for an
answer. Sales wants the credit approved. Finance owns the limit. Compliance
owns the policy. The manager can write a polished answer, but the organization
does not trust polish. It trusts the current contract, the approved policy, the
billing authority, and an audit trail showing who accepted the exception.

An AI assistant changes the speed and surface area, not the underlying control
problem. It can draft the answer faster than the manager, but it still cannot
grant itself policy authority, finance authority, or accountable acceptance.
```

This is the desired movement: scene first, control second, AI third, then the
AI-specific break that requires a stronger or different control.

## Evidence Discipline

The storytelling pattern is a drafting tool, not a license to overclaim.

Use these gates:

- **Analogy gate:** a non-software domain analogy needs one source for the
  control pattern, one source or incident for how the control can fail or
  degrade, and one sentence naming where the AI analogy breaks.
- **Case gate:** any recurring support-credit workflow detail must be tagged as sourced,
  composite, or hypothetical until the source ledger supports it.
- **Verb-strength gate:** avoid "always," "never," "must," "the metric," and
  "often" unless the mapped evidence directly supports the claim.
- **Human-control gate:** distinguish a normative control standard from an
  empirical claim about what organizations actually do.
- **Survey/vendor gate:** use consulting surveys as adoption color and vendor
  system cards as vendor claims, not as proof of causal organizational behavior.

## Examples To Reuse

### Expense Approval

Human behavior: an employee submits an expense that looks ordinary but sits near
a policy boundary.

Human fallibility pattern: people misremember policy, rationalize exceptions,
miss duplicate charges, or feel pressure from senior requesters.

Workplace contract: receipts, policy limits, manager approval, finance review,
segregation of duties, and audit sampling.

AI analogue: an AI can classify expenses, summarize policy, and draft approval
notes, but it must not approve reimbursement without source evidence, authority
limits, and an audit record.

Book use: fluency is not authority; automation must respect effect boundaries.

### Pull Request Review

Human behavior: a developer submits a clear, confident change that appears to
fix the bug.

Human fallibility pattern: the developer can miss edge cases, misunderstand the
system, overfit the test, or ignore operational impact.

Workplace contract: code review, tests, CI, type checks, design ownership,
deployment gates, observability, and rollback.

AI analogue: AI-generated code is another proposed change entering the same
trust pipeline. It may be useful, but acceptance still depends on evidence and
verification.

Book use: AI raises throughput pressure; accepted verified output is the
proposed metric for consequential workflows with verification bottlenecks.

### Incident Root Cause

Human behavior: a team writes a plausible incident explanation after a bad
customer impact.

Human fallibility pattern: humans jump to tidy stories, protect prior decisions,
anchor on recent changes, or confuse correlation with cause.

Workplace contract: timelines, logs, metrics, change records, postmortems,
action owners, and regression tests.

AI analogue: AI can summarize incident material and propose hypotheses, but it
must not turn a hypothesis into root cause without evidence.

Book use: proposals need promotion through source-grounded verification.

### Support Policy Exception

Human behavior: a support manager wants to keep a strategic customer happy
after an outage.

Human fallibility pattern: speed pressure, customer pressure, stale policy, and
unclear authority can make a wrong exception feel reasonable.

Workplace contract: current policy, customer contract, credit limit, finance
approval, compliance review, customer-communication authority, and audit trail.

AI analogue: AI can draft the exception recommendation, list missing evidence,
and prepare the reviewer packet. It cannot accept the decision.

Book use: this is the running case for the decision acceptance contract.

### Compliance Review

Human behavior: an operating team asks compliance to approve a launch packet
late in the process.

Human fallibility pattern: reviewers can be overloaded, politically pressured,
underinformed, or left without practical rejection authority.

Workplace contract: required evidence, reviewer independence, rejection rights,
escalation paths, queue limits, and documented residual risk.

AI analogue: adding AI increases packet volume and polish. The review process
must preserve evidence, time, authority, and the right to stop work.

Book use: "human in the loop" is not a control; a controlled review process is.

## Assertion Classes That Need This Treatment

Use the scene-control-AI pattern whenever the manuscript asserts:

- Fluency is not authority.
- Human review is not automatically meaningful.
- Evidence must be current and source-bearing.
- Permissions matter more than polite model behavior.
- Verification capacity is a product constraint.
- Governance is real only when it changes workflow acceptance.
- Shadow AI can be a pressure response, not only misconduct.
- Better models move the trust boundary rather than removing it.

Small claims can use a sentence or two. Major claims need a full scene. Do not
apply the pattern mechanically to every paragraph; full scenes belong at chapter
openings and major turns, while smaller claims can use micro-analogies.

## Where The Analogy Must Break

The book should not say AI is merely another employee. After using a human
parallel, name the AI-specific break:

- AI can generate more proposals than reviewers can inspect.
- AI can spread polished unsupported text across tools quickly.
- AI can combine stale, retrieved, user-supplied, and official context in one
  fluent answer.
- AI can act through tool permissions unless the workflow constrains effects.
- AI cannot be accountable in the human sense; people and institutions remain
  accountable for acceptance.
- Vendor, model, prompt, retrieval, and tool behavior can drift.

The mature move is not "treat AI like a person." It is "apply the old lesson
about fallible work, then add controls for AI's scale, opacity, and tool
authority."

## Chapter Drafting Checklist

For each chapter, identify at least three story anchors:

- One opening scene.
- One human-control parallel.
- One AI analogue that changes the stakes.

For each major section, ask:

- What does the reader see happening?
- What human fallibility is exposed?
- What existing workplace control handles it?
- What does AI change?
- What must the decision acceptance contract do?

If a section answers only in abstractions, it is not ready for reader draft.
