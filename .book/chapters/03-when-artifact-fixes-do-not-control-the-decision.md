# Chapter 3: When Artifact Fixes Do Not Control The Decision

Artifact fixes improve what the AI produces or how the packet is presented. They
do not, by themselves, control which work can become a decision.

After the Friday support-credit incident, the team made several reasonable
changes. They rewrote the prompt so the AI had to say when evidence was missing.
They connected retrieval to the policy knowledge base. They added a label at the
top of every answer: "AI-generated draft; human review required." They published
a short policy telling teams not to send AI-generated proposals to customers
without approval. They created a dashboard showing sample quality scores from
recent outputs.

The next recommendation had better output properties. The language was cleaner.
The answer linked to a policy page. It listed two assumptions. The review label
was visible.

The same recommendation could still become the working decision before review.
It reached Slack as the likely answer. A support note captured it before finance
had reviewed the amount. The account team asked whether it could tell the
customer approval was coming. The workflow had improved the draft without
changing the moment at which the draft became organizationally relied upon.

The failure is specific: artifact fixes improved the work object but did not
control the decision. Artifact fixes help. They become risky when the
organization treats them as decision controls.

> **Real case: CNET AI-written articles.** CNET's AI article program had several
> artifact-level controls: some marked AI-written articles carried or later
> received disclosures and warning labels, the outlet said AI-assisted articles
> were edited and fact-checked by humans, and corrections were later added. Those
> controls did not keep unsupported work from publication.
>
> The Verge reported that CNET corrected 41 of 77 AI-written stories after
> internal review found errors. After the errors and weeks of debate, CNET
> leadership temporarily paused AI-generated content across its sites. The
> consequence was correction work, public criticism, credibility risk, and an
> interrupted publishing program. The control lesson is that disclosure,
> human-review language, and correction notes help, but they do not substitute
> for a publishing path that can reject unsupported work before readers receive
> it as accepted journalism (Sato and Roth 2023; Christian 2023).

## Better Prompts Improve Shape

The prompt change told the AI to cite sources, list missing
evidence, avoid overclaiming, and mark the answer as a recommendation rather
than a decision.

That improved the output. The recommendation became easier to review. It used a
consistent structure. It separated facts from assumptions more often. It asked
for the contract cap when that field was absent.

The prompt did not control the workflow.

A prompt can shape what the model is likely to produce, but it does not control
the path that follows the answer. It cannot prevent a person from pasting a
proposal into Slack as a likely answer, verify policy freshness without workflow
support, grant finance authority, preserve a label after text is copied, or stop
customer communication while downstream tools still allow the message to go out.

Prompts matter. They are part of the system. But a prompt is not an acceptance
rule unless it is connected to something that can block, escalate, log, throttle,
or constrain an effect.

Test the prompt against the effect: if the model ignores this prompt, what
prevents the bad effect? If the answer is "we hope the reviewer notices," the
prompt is an input improvement, not the control.

## Retrieval Improves Context

The retrieval change connected the assistant to the policy knowledge base and
ticket records. The model no longer had to answer from the user's short
description alone.

That also helped. Retrieval brought relevant materials into the answer. It made
some citations visible. It reduced the chance that the model would invent a
policy name. It helped the support manager find the right family of exception
rules faster.

Retrieval still did not answer the authority question.

A retrieved document can improve the answer and still fail the decision. The
source may be real but stale, relevant but not controlling, or useful for one
claim while leaving another unsupported. A knowledge-base summary is not the same
thing as the approved policy, and neither source can account for a missing
customer contract amendment or the wrong operational state.

In the support-credit workflow, retrieval may be exactly how a bad recommendation
gets stronger. The AI finds an old but genuine policy page. The source link
makes the answer feel grounded. A busy reader sees a citation and lowers their
guard. The policy has not become current merely because it was retrieved.

Retrieval is infrastructure for better candidate context, but the workflow still
needs a rule for accepting the decision.

The workflow still needs to ask which source is allowed to support which claim,
how fresh that source must be, and what happens when sources conflict.

## Review Labels Improve Awareness

The review-label change put a visible label at the start of every AI answer: the
output was a draft requiring human review.

That label was better than silence. Labels can slow careless forwarding. They can
remind users that generated output is not accepted work. They can make the
desired state language visible.

But labels are weak when the surrounding workflow rewards speed.

A label is easy to lose or ignore. It may disappear when text is copied, fade
into background noise, or yield to managerial pressure. Most importantly, it can
say that review is required without giving the reviewer time, evidence,
authority, or a button that blocks the effect. The organization feels covered,
but the actual path has not changed.

The support thread showed the weakness. "AI draft, likely path, checking
finance" contained caution and leakage in the same sentence. The caution said
draft. The leakage said likely. The account team reacted to the part that solved
its immediate problem.

Review labels become controls only when they are attached to effect boundaries.
If the output is labeled proposal, the customer-send path should remain blocked.
If the ticket note includes an AI-generated proposal, the system should preserve its
state. If a Slack share would remove required context, the share should carry
the state or route to the reviewer packet instead.

Awareness helps, but it does not enforce the boundary.

## Policies Improve Expectations

The policy change said AI-generated proposals could not be sent to customers
without human review and the required approval path. It required current sources
for policy exceptions. It told employees not to rely on AI for final decisions.

The policy was reasonable. The company needed a shared expectation. Managers
needed language for coaching. Compliance needed a standard to point to when
reviewing exceptions.

The policy still lived outside the runtime path.

If the ticket system allows an unsupported recommendation to be marked final,
the policy has not changed the ticket system. If Slack remains the fastest way
to create pressure, the policy has not changed Slack. If finance is notified
only after support and sales have aligned, the policy has not changed the order
of operations. If reviewers have no capacity, the policy has not created time.

This is familiar outside AI. A release checklist helps only if missing checks
can stop the release. An expense policy helps only if approvals and
reconciliation can catch violations. An incident review policy helps only if
actions are owned and tracked. Written expectations matter, but they become
controls when they are connected to a decision rule, escalation, logging,
blocking, throttling, or permissions.

The practical question for an AI policy is not whether it sounds responsible.
It is where the policy changes the path of work.

## Evals Improve Measurement

The fifth fix was an eval dashboard. The team sampled recommendations and scored
whether they were coherent, cited sources, and matched reviewer outcomes. The
dashboard showed improvement after the prompt and retrieval changes.

That information was useful. Aggregate quality matters. A team should know
whether a model upgrade improves source use, whether a prompt increases missing
evidence disclosure, and whether reviewers keep correcting the same class of
mistake.

But a dashboard can hide the single escape that matters.

An eval score may say the system is usually good. The next high-risk case may
still involve a stale policy, a contract conflict, or a credit amount above the
approval threshold. Aggregate measurement does not automatically block a
specific unsupported decision. A dashboard that no one uses to change routing,
scope, reviewer capacity, or launch thresholds is observability without control.

Measurement becomes part of the decision rule when it changes behavior.
If stale-source failures exceed a threshold, narrow the workflow. If reviewer
overrides spike, pause automation. If a model change worsens missing-evidence
disclosure, roll it back. If certain case types repeatedly require escalation,
exclude them until source ownership is fixed.

Evals become more than reporting when they create stop conditions. They are
incomplete when they only show that the report looks better this month.

## The Rule For Accepting The Decision

The missing piece in all five fixes was the same: an acceptance rule.

An acceptance rule says what has to be true before AI output can affect a
customer, money, a record, or a system. It also says what happens when those
conditions are not met.

For the support-credit workflow, an early rule could be:

```text
An AI-generated credit recommendation may be used only as an internal proposal
until the current policy, customer contract cap, billing ledger, and
customer-specific impact evidence are attached or marked unavailable. Customer
communication that implies approval is blocked until finance and compliance
approve or clear the claims they own, or the case is escalated.
```

This rule is narrower than a governance slogan and stronger than a prompt. It
names the supporting sources, the blocked effect, the roles that must approve or
reject their claims, and the incomplete state.

The rule does not make every answer correct. It makes the workflow inspectable.
If the recommendation escapes anyway, the failure is visible: the acceptance
rule was bypassed, misimplemented, or too weak. If reviewers cannot keep up, the
failure is visible: the workflow lacks capacity and must narrow or throttle. If
sources are stale, the failure is visible: the source map is wrong or source
ownership is missing.

That is the difference between an artifact improvement and a control.

## Classifying The Workflow

Not every AI use needs the same acceptance rule. A useful classification has
four levels.

The first is discovery only. The AI helps find, compare, or inspect information
without drafting work that can be accepted. The main controls are source access,
data boundaries, and clarity that the output is only exploratory.

The second is draft assistance. The AI helps an individual think, draft, or
summarize for private use. Personal-assistant use belongs here when it has no
independent operational effect. The main concerns are data boundaries, user
judgment, and avoiding accidental disclosure.

The third is a reviewed proposal. The AI produces work that may influence a
decision, but a human process must accept it before it affects customers, money,
records, or systems. The service-credit recommendation belongs here if the AI
only drafts and assembles the packet.

The fourth is bounded action. The AI can change workflow state, send messages,
modify records, trigger payments, alter configurations, or take another effect
through tools. This requires stronger permission boundaries, post-action
verification, audit trails, kill switches, and incident obligations.

Many adoption problems come from treating a reviewed proposal as if it were a
draft assistance task, or allowing it to behave like a bounded action without the
controls required for action.

The classification should be written into the workflow:

| Workflow Class | Allowed Effect | Minimum Control |
| --- | --- | --- |
| Discovery only | Source inspection | Source access and data boundary |
| Draft assistance | Private user aid | Data boundary and user responsibility |
| Reviewed proposal | Internal recommendation | Evidence, reviewer, acceptance rule |
| Bounded action | State-changing effect | Permissions, verification, audit, rollback |

The support-credit workflow should not begin by granting bounded action. Let the AI
summarize, identify missing evidence, draft internal packets, and prepare
customer language after acceptance. Do not let it approve credits, update final
ticket disposition, or imply customer commitment until the workflow has earned
those effects.

## Turning Artifact Fixes Into Controls

The right lesson is to connect artifact fixes to the authority boundary.

Promote artifact fixes by tying each one to an effect. Prompts should produce
structured fields reviewers can accept or reject. Retrieval should supply
sources with freshness checks. Labels should travel with the output and block
customer sends when the state is not accepted. Policies should become routing,
permission, escalation, and audit rules. Eval thresholds should change launch,
scope, or rollback decisions.

The authority-boundary test turns each artifact fix into a control question:

| Fix | What It Improves | What It Still Cannot Stop | How To Promote It |
| --- | --- | --- | --- |
| Better prompt | Output shape | Unsupported decision | Require structured missing-evidence fields |
| Retrieval | Candidate context | Stale or wrong authority | Map sources to claims and freshness |
| Review label | Awareness | Review without evidence, time, or rejection authority | Block effects until state changes |
| Eval dashboard | Aggregate quality | Single high-risk escape | Tie thresholds to launch and rollback |
| Policy | Expected behavior | Runtime action | Encode into permissions and escalation |

The last column is the work. It turns reminders into controls.

For every proposed AI fix, ask: what can this fix block, escalate, log,
throttle, or prevent from becoming an accepted decision? If the answer is
nothing, keep the fix if it improves quality, but do not count it as an
authority control.

Choose work that can be controlled. Some tasks are good AI tasks because the
objective is clear, evidence is accessible, effects are bounded, and
verification is cheap. Others are poor first candidates because the workflow
cannot yet say what acceptance means.
