# Compact Book Outline: Trust the Process, Not the Prompt

Subtitle: **How to Turn AI Output Into Trusted Work**

Brand doctrine:

> AI output starts as a proposal. A workflow accepts it for use only after
> required evidence is checked and a named owner accepts the decision or
> authorizes the permitted effect.

## Purpose Of This Version

This is the canonical drafting outline for the book.

Target shape: 13 chapters, an opening workflow teardown, and a short closing.

## Primary Illustration Domain

The book's primary illustration domain is AI-assisted operational decisions in
enterprise workflows. The running case is a customer-support policy-exception
decision after a service incident. Until support-specific source work is
complete, it is a composite design scenario that demonstrates the controls; it
is not proof that this exact support workflow is typical.

Software engineering, incident response, compliance operations, platform
governance, medicine, aviation, finance, law, and journalism should be used as
sourced analogies only. Each analogy must include both the control and the way
that control can fail.

## Reader Promise

By the end of the book, the reader should know how to adopt AI without either
overtrusting it or smothering it. They should be able to identify where AI is
valuable, where it must be bounded, and how to design workflows that turn fluent
outputs into trusted work through evidence, permissions, verification,
accountability, incentives, and learning.

## Core Argument

Model selection matters, but it is not enough. AI adoption is a trust-boundary
problem.

AI is valuable because it can generalize, synthesize, translate, draft, reason
through ambiguity, and navigate unfamiliar context. It is risky because fluent
output can look authoritative before it has earned authority. The answer is not
to treat AI as deterministic, and it is not to reject AI because it is
probabilistic. The answer is to put AI's strengths inside workflows that make
trust inspectable, enforceable, correctable, accountable, and politically real.

Central sidecar: AI is not the first fallible producer of important work.
Humans were. Mature, high-consequence workflows often use evidence, separation
of duties, review, approval, audit, verification, and incident learning because
raw human work is not enough by itself in high-consequence settings. But the
parallel must be honest: those controls also fail under pressure, overload,
weak rejection authority, misaligned incentives, and ceremony. AI adoption extends that
older lesson to a new kind of fallible contributor.

Storytelling rule: major assertions should move from a familiar workplace
scene, to the human fallibility it exposes, to the existing workplace contract
that handles it, and only then to the AI analogue and the place where the
analogy breaks. The reader should be able to visualize the human version before
being asked to accept the AI control.

## Running Case

Use one concrete running case throughout the book: an AI-assisted customer
support policy-exception workflow after a service incident. Until source work is
complete, treat it as a composite design scenario, not a documented case study.
Maintain the case details in `case-dossier-support-exception.md`.

Scenario:

- A strategic customer asks for a service credit after an outage.
- The AI assistant drafts an exception recommendation for the support manager.
- Source systems include the support ticket, customer contract, current
  exception policy, outage timeline, account history, billing ledger, and
  compliance notes.
- The policy in the knowledge base is stale; the approved policy changed last
  month.
- The AI produces a plausible recommendation that would approve the credit, but
  the contract, billing authority, and incident severity evidence are
  incomplete.
- The sales team wants a fast answer, support wants to close the ticket, finance
  owns the credit limit, legal/compliance owns policy interpretation, and the
  support manager is named accountable without enough rejection authority or review time.
- The bad outcome is not only a wrong answer. It is unauthorized financial
  impact, inconsistent customer treatment, audit exposure, reviewer overload,
  and a shadow-AI path where teams bypass the official workflow to move faster.
- The rollout is messy: access reviews delay source integration, CRM fields are
  stale, frontline teams keep using private tools, security asks for data-class
  boundaries, reviewers are already overloaded, and executives want ROI within
  the quarter.
- The case must include one moment where the framework struggles, such as a
  contested source of authority or executive pressure to override the stop
  condition.

This case should be repaired progressively through the book. Each chapter should
add one part of the decision acceptance contract and one way the workflow can
fail.

Use the support-credit workflow as the spine, not the only proof. Each chapter should have
one support-credit workflow beat plus at most one cross-domain analogy where that analogy
sharpens the control. Cross-domain analogies are source-gated.

## Decision Acceptance Contract

The book should not teach a pile of disconnected artifacts. It should build one
running decision acceptance contract: the smallest executable agreement that
defines when AI output can move from proposal to accepted work for a permitted
effect.

Every field in the contract must do at least one operational job: block,
escalate, log, sample, throttle, or retire. If a field cannot change what the
workflow accepts, escalates, or stops, it belongs in an appendix, not in the
core contract.

Contract sections:

1. Task-fit diagnostic.
2. Source-of-truth and freshness map.
3. Claim-to-evidence matrix.
4. Responsibility and incentive map.
5. Tool inventory, permission, and effect-boundary specification.
6. Verification and reviewer-capacity scorecard.
7. Reviewer packet.
8. Shadow-AI and bypass loop.
9. Eval, incident, and governance loop.
10. First-workflow design canvas.

No hook, no control. A map, matrix, policy, or scorecard that does not change
what the workflow accepts is ceremony.

The contract must scale by consequence. The minimum viable decision acceptance
contract is brutally small: one decision, one authority-bearing source, one
action limit before acceptance, one checker, one stop condition, and one owner.
Everything else is an escalation tier, not the default method.

Use four recurring tiers:

1. Discovery only: no accepted work; minimum packet focuses on source access,
   source boundaries, and data boundaries.
2. Draft assistance: no operational effect; minimum packet focuses on data
   boundaries and user responsibility.
3. Reviewed proposal: AI drafts a recommendation; evidence, reviewer capacity,
   and acceptance criteria become mandatory.
4. Bounded action: AI can change workflow state; permissions, post-action
   verification, audit, kill switches, and incident obligations become
   mandatory.

## Chapter Visual Jobs

Each chapter needs one first visible moment before abstract terminology appears.
Use these visual jobs to prevent repetition:

- Opening teardown: the recommendation escapes through Slack, ticket notes, and
  account updates before acceptance exists.
- Chapter 1: an analyst writes a polished memo from incomplete notes; fluency
  is useful but not source authority.
- Chapter 2: a competent expert's work still enters review, audit, or
  verification because expertise is fallible.
- Chapter 3: each artifact fix helps a little, but the recommendation still
  reaches Slack or CRM before the acceptance rule exists.
- Chapter 4: an operations work order says "handle this exception" without
  source access, approval limits, or acceptance criteria.
- Chapter 5: an analyst uses the spreadsheet or policy page that is available,
  not the source that is authoritative and current.
- Chapter 6: an ownership meeting reveals that the named accountable manager
  lacks budget, rejection authority, or escalation cover.
- Chapter 7: an employee may draft a payment request but cannot approve,
  release, and reconcile the payment alone.
- Chapter 8: a queue report shows more AI proposals than available
  reviewer-hours, forcing a throttle decision.
- Chapter 9: the reviewer screen shows claim list, source links, stale-source
  warning, reject/escalate controls, and rejection authority.
- Chapter 10: teams bypass slow official paths with spreadsheets, side Slack
  approvals, copied docs, unofficial templates, or private AI tools.
- Chapter 11: a near miss forces a choice between updating paperwork and
  changing the workflow that accepted the bad packet.
- Chapter 12: a Week 0 setup plus three-week pilot repairs the workflow, hits
  one source-owner delay, one review-capacity limit, one shadow-AI discovery,
  and one stop/go decision.

Each chapter should make one operator artifact concrete: packet, scorecard,
map, approval rule, queue report, escalation script, audit trail, calculator, or
meeting agenda.

## Part I: The Trust Mismatch

### Opening Teardown: The Fluent Output That Escaped

Start with the running case. Show the AI generating a clear, useful-looking
credit recommendation that moves through Slack, the support ticket, and an
account update before anyone has established whether it is supported, fresh,
permitted, verified, or accepted by an accountable owner.

The teardown introduces the whole book in miniature:

- Fluent output leaks into authority.
- "A human reviewed it" is too vague to be a control.
- Evidence, permissions, rails, review, and verification are separate jobs.
- The stop condition matters: if verification capacity is insufficient, the
  workflow must throttle, narrow scope, or refuse automation.
- Incentives matter: speed pressure, customer pressure, cost pressure, and
  promotion metrics can make bypass rational.
- Falsification matters: the workflow failed if it accepts stale evidence,
  escapes unsupported decisions, overloads reviewers, hides audit gaps, or
  pushes work into shadow AI.

Give the reader a lightweight Monday path immediately:

1. Pick one workflow.
2. Classify it as discovery only, draft assistance, reviewed proposal, or bounded action.
3. Name what the output may do before acceptance.
4. Name the authority-bearing source.
5. Name reviewer capacity and stop conditions.
6. Run a two-week pilot with captured failures.

Before launch, require a pre-deployment control test:

- A small sample of historical decisions.
- An adversarial stale-source drill.
- Reviewer calibration pass/fail criteria.
- Hard launch thresholds for accepted, rejected, escalated, and incomplete
  packets.
- Automatic rollback criteria if stale evidence, missing authority, or review
  overload appears.

Case beat: the initial recommendation escapes before the policy source,
contract, credit authority, and outage evidence are reconciled.

Reader can now: see the book's problem as an operational failure, not a generic
"AI can be wrong" warning.

### Chapter 1: The Category Error

AI is often treated as a new employee, search engine, or automation script. It
is none of those. It is a probabilistic system that can exhibit learned
capabilities such as pattern recognition, synthesis, translation, in-context
adaptation, tool use, and reasoning-like behavior. But it does not inherently
know when its output has earned operational authority.

Owns:

- A concise scientific explanation of next-token prediction, learned
  representations, sampling, context sensitivity, calibration limits, and why
  fluency is not truth.
- The balancing point: "just next-token predictor" is mechanically useful but
  rhetorically too thin.
- The distinction between useful judgment and authority.

Does not own:

- Full implementation detail of model architecture.
- A prediction about which current model failure modes will persist forever.

Case beat: the AI can synthesize the ticket, outage note, and policy language in
a way that is genuinely useful, while still overstating whether the credit is
authorized.

Packet added: proposal-versus-authority label.

Reader can now: describe what kind of thing they are working with without
collapsing into either mysticism or dismissal.

### Chapter 2: How Important Work Earns Trust

Engineering and operations developed controls because human expertise alone was
not enough. Types, tests, code review, audit logs, approval limits, runbooks,
incident reviews, and source records exist to make fallible work inspectable and
correctable. The same is true, with different details, in other professions.

The analogy must include the failure mode. Human controls degrade under
production pressure, weak rejection authority, overloaded reviewers, normalization of
deviance, and ritualized audits. The lesson is not that human process is
perfect. The lesson is that consequential work becomes trustworthy only when the
process can detect and correct its own failures.

Owns:

- The human-process parallel that runs through the book.
- Why "human in the loop" is not a control unless the review process is
  structured.
- Why AI raises the value of reliability disciplines instead of making them
  obsolete.
- The first warning against named accountability without control: naming an accountable
  person who lacks authority to reject, halt, fund, escalate, or change the
  workflow.

Does not own:

- Detailed AI governance program design.

Case beat: the support manager was named accountable but lacked current policy
evidence, finance authority, and protected time to review.

Packet added: accountability and rejection authority check.

Reader can now: explain why process is not anti-AI and why process itself must
be tested.

### Chapter 3: When Artifact Fixes Do Not Control The Decision

Organizations usually reach for one of several artifact fixes: upgrade the
model, add RAG, write better prompts, add a human-in-the-loop label, publish a
policy, or build an eval dashboard. Each can help. None is enough if unsupported
output can still become authority.

Owns:

- AI output as proposal, not authority.
- The productive middle: trust the verified process, not the raw answer.
- Why artifact fixes do not control the decision when they leave the authority boundary unchanged.
- Why useful fixes should be treated as artifact improvements, not dismissed as dumb
  ideas or accepted as complete solutions.
- A first workflow classification lens: discovery only, draft assistance,
  reviewed proposal, bounded action.

Does not own:

- The full practical design method.

Case beat: the team tries better prompt wording and a review label, but the
recommendation still spreads before evidence and acceptance are complete.

Packet added: workflow classification.

Reader can now: diagnose whether a proposed AI rollout changes the actual
authority boundary or only adds ceremony.

## Part II: Put AI Where It Is Strong

### Chapter 4: The Shape Of A Good AI Task

AI works best when the task has a clear objective, accessible evidence, bounded
permissions, observable output, acceptance criteria, and a cheap verification or
recovery path.

Owns:

- The task-fit diagnostic.
- Good uses: summarization, drafting, transformation, exploration, critique,
  classification with review, navigation, and option generation.
- Risk conditions: hidden state, stale facts, adversarial inputs, irreversible
  actions, long obligation chains, expensive verification, and political
  pressure to treat proposals as decisions.

Does not own:

- The full workflow implementation pattern.

Case beat: the exception workflow is split into safer subtasks: summarize ticket
history, identify policy clauses, list missing evidence, draft recommendation,
and propose next action.

Packet added: task-fit diagnostic.

Reader can now: choose early AI use cases that create useful proposals without
granting premature effect authority.

### Chapter 5: Evidence, Freshness, And Context

AI-generated claims should begin as proposals. They become usable only when tied
to adequate, current evidence for the decision at hand. Context is not one
thing: background knowledge, retrieved snippets, inspected source documents, and
fresh task-specific evidence carry different authority.

Owns:

- The source-of-truth and freshness map.
- The evidence hierarchy: model memory, user context, retrieved snippets,
  inspected sources, tests, telemetry, official records, and accountable review.
- Freshness and provenance.
- Claim-to-evidence matrices.
- The separation between evidence, verification, acceptance, and authority.

Does not own:

- Permission enforcement or workflow state transitions.

Case beat: the workflow discovers that the knowledge-base policy is stale and
that the contract, billing ledger, outage timeline, and current policy each
answer different parts of the decision.

Packet added: source-of-truth map and claim-to-evidence matrix.

Reader can now: tell the difference between helpful context and authority-bearing
evidence.

### Chapter 6: The Responsibility And Incentive Split

Trustworthy adoption separates model-owned strategy, system-owned contracts, and
human-owned judgment. It also makes incentives explicit: who benefits from
speed, who pays review cost, who owns downside risk, and who has rejection
authority when AI-generated work should slow or stop.

Opening scene: an ownership meeting where the support manager is named
accountable for customer communication but lacks finance authority, compliance
backing, reviewer-hour budget, and rejection authority.

Owns:

- Model-owned: exploration, synthesis, drafting, explanation, tradeoff analysis.
- System-owned: permissions, tool access, source requirements, output contracts,
  logs, blockers, verification gates, rollback, escalation.
- Human-owned: goals, values, risk appetite, exceptions, accountability, and
  changing the workflow when evidence shows it is failing.
- Incentive map: speed beneficiaries, review-cost bearers, risk owners,
  budget owners, rejection authority, escalation rights, and blame paths.
- Normative control standard: no accountable owner without authority to reject,
  halt, fund, escalate, and change the workflow.
- Adoption contract: sponsor, budget owner, risk owner, escalation path,
  reviewer SLA, and rejection authority.
- Claim gate: budget ownership, rejection authority, reviewer SLAs, and
  named accountability without control should be treated as source-gated claims or
  normative standards until the evidence base is stronger.

Does not own:

- The detailed economics of verification throughput.

Case beat: sales wants speed, support wants closure, finance owns credit
authority, compliance owns policy interpretation, and the support manager owns
customer communication. The old workflow hid these conflicts.

Packet added: responsibility and incentive map.

Reader can now: assign ownership without naming accountability where control is missing.

## Part III: Build Trust Into The Workflow

### Chapter 7: Effect Boundaries For AI Workflows

Good rails do not try to micromanage the model's thinking. They constrain what
the system may access, what claims may be accepted or communicated, what records
may change, and what actions require approval, retry, or escalation.

Owns:

- Permission boundaries.
- Data access controls.
- Output schemas.
- Source and freshness requirements.
- Action approval thresholds.
- Loop and budget controls.
- Typed blockers.
- Audit logs.
- The distinction between machine-checkable rails and socio-technical controls.
- The enforcement hook for every policy: permission change, budget gate, kill
  switch, approval threshold, audit query, exception owner, or incident trigger.

Does not own:

- Treating every problem as a prompt-writing problem.

Case beat: the AI can draft the recommendation freely, but it cannot approve,
apply, or communicate a credit unless contract, policy, billing authority, and
review requirements are satisfied.

Packet added: permission and effect-boundary specification.

Reader can now: specify what the workflow enforces even when the model is wrong.

### Chapter 8: Measuring Accepted Verified Output

AI makes generation cheaper. In many consequential workflows, accepted verified
output should be treated as the proposed adoption metric: can the organization
verify, review, and safely use work faster than it creates new review debt?

Opening scene: the queue report shows generated recommendations rising faster
than finance and compliance reviewer-hours. The chapter's visual job is
throughput economics and throttling, not reviewer ergonomics.

Owns:

- Output versus outcome.
- Verification cost and review capacity.
- Accepted verified output as the proposed durable metric.
- Proposed capacity model: proposals per reviewer-hour, max work in progress,
  auto-reject rules for incomplete packets, sampling rules, reviewer
  independence, funded review time, and hard throttles when review debt exceeds
  capacity.
- Example calculator: reviewer-hours available, expected proposals, average
  review time, cost per accepted output, cost of escaped error, and throttle
  threshold.
- The need to validate the verifier: tests, evals, reviewers, monitors, and
  policies can also fail.
- Stop conditions when checking capacity is insufficient.

Does not own:

- Full incident and governance operating model.

Case beat: the workflow initially generates more exception recommendations than
finance and compliance can review. The redesigned workflow throttles proposal
volume, rejects incomplete evidence packets, and measures accepted value rather
than generated recommendations.

Packet added: verification and reviewer-capacity scorecard.

Reader can now: measure whether AI is creating value or transferring cost into
review, correction, and risk.

### Chapter 9: Conditions For Useful Human Review

Human review can fail when reviewers receive polished output without the
evidence trail, decision context, independence, time, or authority to reject. A
controlled review process checks assumptions, sources, scope, verification, and
risk.

Opening scene: the reviewer screen shows the AI recommendation, claim list,
source links, stale-source warning, missing evidence, proposed action,
reject/escalate controls, and the rejection rule.

Owns:

- Why "human in the loop" is insufficient.
- Reviewer independence, expertise, time, evidence, and rejection authority.
- Review packets that include sources, claims, uncertainty, checks, proposed
  action, owner, and failure consequence.
- Review without enough time or evidence, and reviewer overload.
- What protects a reviewer who says no.

Does not own:

- Broad culture change beyond review practice.

Case beat: reviewers no longer receive only the AI's recommendation. They get
the claim list, source references, stale-source warning, missing evidence,
policy threshold, proposed action, and reject/escalate options.

Packet added: reviewer packet.

Reader can now: design human oversight as a control instead of a checkbox.

## Part IV: Operate AI Under Pressure

### Chapter 10: From Assistant To Bounded Agent

Move from chat to agency only when the work can be expressed as a bounded
workflow with scoped tools, planning handoff, explicit permissions, valid
failure states, and post-action verification.

Internal sequence: reviewer and capacity bottlenecks create bypass pressure;
sanctioned assistant paths relieve pressure; bounded-agent paths require
stricter permissions, verification, and rollback.

Opening scene: the official workflow is too slow, so teams use spreadsheets,
side Slack approvals, copied docs, unofficial templates, or private AI tools to
keep work moving. Shadow AI is treated as a pressure pattern to test, not only
as misconduct to punish.

Owns:

- Workbench versus bounded agent.
- Read-only assistance before write authority.
- Tool inventory and rationalization: sanctioned tools, unsanctioned tools,
  data classes, workflow owners, duplicate capabilities, allowed-use tiers, and
  retirement or consolidation decisions.
- Planning before execution.
- Handing off from proposal to mutation.
- Failure recovery by narrowing task scope.
- Shadow AI as a plausible pressure pattern to test: slow approval can lead to
  private tool use, lost evidence, governance blind spots, stricter bans, and
  more bypass.
- Countermeasures: sanctioned low-friction paths, acceptable-use tiers, logging,
  procurement/DLP signals, and bypass postmortems.

Does not own:

- Organization-wide governance policy.

Case beat: teams start using private tools to draft exception language when the
official workflow is slow. The redesign adds a sanctioned low-risk assistant path
and a stricter bounded-agent path for proposed credits.

Packet added: shadow-AI and bypass loop.

Reader can now: decide when AI may act and how to keep unofficial use from
becoming the real workflow.

### Chapter 11: Evals, Incidents, And Workflow Governance

Trustworthy adoption improves through feedback. Evals, incident reviews, audit
logs, and governance should expose whether the workflow protects real invariants
instead of producing paperwork nobody uses.

Owns:

- Evaluating workflows, not just models.
- Turning real failures into regression cases.
- AI incident reviews.
- Audit logs that show invariants and decisions, not raw noise.
- Governance as operational enforcement: data boundaries, approval models,
  vendor/model risk, compliance evidence, and kill switches.
- Detecting artifact ceremony: maps, matrices, policies, or scorecards that do
  not change what the workflow accepts.
- Control validation loop: sampled accepted decisions, rejected-decision review,
  stale-source drills, reviewer calibration, bypass detection, escaped-error
  review, and retiring controls that never change acceptance.
- Falsification criteria: escaped unsupported decisions, stale evidence accepted,
  reviewer overload, bypass growth, gamed controls, and audit gaps.

Does not own:

- A full legal compliance manual.

Case beat: a near-miss becomes a regression case. Governance changes the allowed
credit threshold, required sources, queue limit, audit query, and escalation
path rather than only updating the prompt.

Packet added: eval, incident, and governance loop.

Reader can now: make AI adoption improve after failure instead of hiding failure
inside policy ceremony.

## Part V: The Practical Playbook

### Chapter 12: Designing One Trustworthy Workflow

Close with a concrete method the reader can apply immediately. This chapter
pulls together maturity, leadership questions, economics, and the decision
acceptance contract. It should synthesize ideas already taught, not introduce a
new framework at the end.

Draft this chapter as a 30-day operating narrative before presenting the full
canvas. The story should include an intake meeting, source-owner negotiation,
reviewer-hour budget decision, security approval path, pilot review, executive
metric reset, and final stop/go decision.

Owns:

- Pick a narrow, valuable task with clear evidence and low blast radius.
- Define the authority-bearing source.
- Assign responsibility and incentives.
- Set permission and effect boundaries.
- Define the output contract.
- Add evidence and freshness requirements.
- Add verification and reviewer packets.
- Define stop conditions and escalation.
- Define the capacity model and budget owner.
- Define the shadow-AI path and sanctioned alternatives.
- Define the tool inventory and rationalization decision.
- Log enough to learn.
- Pilot with skeptical users.
- Run an adoption operating rhythm: weekly review queue check, incident review,
  office hours, user feedback, policy updates, and executive metric review.
- Use a 30-day operating calendar: intake meeting, source-owner negotiation,
  reviewer-hour budget, security approval path, pilot stop/go review,
  escalation script, and executive metric reset.
- Convert failures into workflow changes.
- Maturity stages from personal acceleration to team conventions, workflow
  integration, contracted autonomy, and learning system.

Does not own:

- Selling AI as magic or reducing adoption to prompt craft.

Case beat: the customer-support exception workflow is redesigned end to end and
the team decides what to automate now, what to keep as reviewed proposal, and
what to reject until evidence or review capacity improves.

Final payoff: end with the completed decision packet and one concrete decision
under pressure. The workflow should clarify authority, protect the reviewer,
close or absorb a shadow path, and deliberately reject at least one tempting
automation step.

Packet added: first-workflow design canvas.

Reader can now: start on Monday with a narrow workflow, explicit controls, and
failure criteria.

## Closing: Working With The Machine

Do not ask AI to become deterministic. Do not ask humans to manually compensate
for every AI weakness. Build workflows where each part does what it is best at:
AI supplies fast, broad, tireless probabilistic work; humans supply goals,
judgment, values, and accountability; workflows supply evidence, constraints,
memory, checks, recovery, and learning.

Final reader belief:

Trustworthy AI adoption is not prompt faith, model worship, or governance
ceremony. It is the disciplined design of work where powerful probabilistic
systems can contribute without granting themselves authority.

## Source Priorities Before Drafting

- Chapter 1: LLM capability, limitation, calibration, uncertainty, and
  hallucination sources.
- Chapter 2: software reliability and human-factors sources, plus carefully
  sourced cross-domain sidebars.
- Chapter 5: evidence, provenance, retrieval, freshness, and source-of-truth
  sources.
- Chapter 7: security, permissions, excessive agency, prompt injection, and
  secure AI lifecycle sources.
- Chapters 8 and 9: productivity, review cost, reviewer overload, automation
  bias, human oversight, and verification economics sources.
- Chapters 10 and 11: shadow AI, organizational incentives, incident learning,
  governance, audit, model/vendor risk, and policy-enforcement sources.

For future structural revisions, use this planning outline alongside
`compact-source-crosswalk.md`; the current reader-facing structure is the
chapter set and publication-readiness tracker.

## Drafting Tests

Every chapter should pass seven tests:

1. Does it move the reader to a new belief, not merely repeat the thesis?
2. Does it advance the running case and decision acceptance contract?
3. Does each major assertion have a workplace-scene example, a human
   fallibility pattern, an existing control, and an AI analogue?
4. Does it distinguish proposal, evidence, verification, acceptance, and
   authority?
5. Does it show how the human-process parallel helps while acknowledging that
   human process is not perfect?
6. Does it name what would prove the workflow failed?
7. Would the advice still work during a deadline, reviewer backlog, cost-cutting
   push, or executive productivity mandate?
