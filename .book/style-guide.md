# Style Guide

Purpose: keep the book serious, useful, and readable without adopting the
sales-deck tone common in AI writing.

This guide synthesizes genre-level habits from strong technical nonfiction,
software reliability writing, operations books, and research-backed business
books. It should not imitate any one author. Use it as a quality bar for this
book's own voice.

## Voice North Star

The voice should sound like a senior practitioner explaining a hard operational
truth to capable peers.

Core qualities:

- Calm.
- Specific.
- Evidence-aware.
- Operational.
- Skeptical without being cynical.
- Optimistic only after showing the mechanism.
- Plainspoken without flattening the technical point.

The reader should feel: "This author has seen real systems fail, understands why
AI is useful anyway, and is not trying to sell me magic."

## Critique The Workflow, Not Motives

The book should be direct about failure without implying bad faith where the
real issue is missing state, missing evidence, weak incentives, overloaded
review, or an unenforced control.

Avoid blame-loaded intent language unless the chapter is explicitly discussing
misconduct, legal fault, or incentive pressure. Words such as `pretend`,
`dishonest`, `fake`, `theater`, `blame`, and `negligent` should trigger a
rewrite check.

Prefer workflow-state language:

```text
Weak: The team admitted the fluent answer was not safe.
Better: The workflow no longer treated a fluent answer as an accepted decision.
```

```text
Weak: The reviewer approved the packet without looking.
Better: The review accepted the packet without current evidence, enough time,
or authority to reject or escalate.
```

```text
Weak: The governance process was theater.
Better: The governance artifact could not change acceptance, permission,
evidence requirements, escalation, logging, throttling, or retirement.
```

The target tone is firm but not accusatory. The book should make the failure
inspectable enough that the reader can fix it.

## Tonal Reference Qualities

Use these as genre references, not as imitations:

- **Designing Data-Intensive Applications**: precise definitions, careful
  tradeoffs, no hype.
- **Site Reliability Engineering**: reliability as an operating discipline,
  not a slogan.
- **Software Engineering at Google**: pragmatic engineering judgment, examples,
  and organizational context.
- **Accelerate**: claims tied to measurement and caveats.
- **Release It!**: concrete failure modes and production-minded advice.
- **The Pragmatic Programmer**: memorable rules, but grounded in practice.
- **Human-Centered AI**: human responsibility and system design without treating
  humans as magic.

Do not copy cadence, phrasing, metaphors, or signature style from these books.
Borrow only the discipline: define terms, show tradeoffs, name failure modes,
and make claims earn their confidence.

## Technical Book Structure Standard

The book should read closer to a practical technical field guide than an AI
strategy essay. Strong technical books such as reliability, data systems,
refactoring, and pragmatic engineering texts tend to use the same underlying
movement:

1. Name the practical problem.
2. Show a concrete situation.
3. Define the distinction or mechanism.
4. Show where the naive approach fails.
5. Give a control, pattern, checklist, artifact, or decision rule.
6. State tradeoffs and limits.
7. Leave the reader with a usable handle.

For this book, the preferred chapter movement is:

1. Chapter claim.
2. Running workplace example or human-process parallel.
3. Real case with visible cost.
4. Control principle.
5. AI-specific mechanics and analogy break.
6. Operator artifact, checklist, table, or decision rule.
7. Failure modes and limits.
8. Practical close.

This structure should be visible to the reader. Use quiet headings, tables, and
short rules to expose the argument. Do not hide a chapter's practical tool in a
long essay shape.

The prose should favor cause-and-effect sentences:

```text
If the reviewer cannot see the source, review becomes trust in the packet.
```

It should also favor scoped control rules:

```text
This control will not eliminate bad outputs. It changes which outputs can
become decisions.
```

Avoid persuasion-first openings:

```text
AI is transforming work, and leaders need a new approach to trust.
```

Use work-first openings:

```text
An AI-generated recommendation becomes risky when the workflow treats it as
accepted work before evidence, authority, and review have caught up.
```

## What The Prose Should Do

Each section should usually do one of five jobs:

1. Define a distinction.
2. Show a failure mode.
3. Explain why the failure happens.
4. Present a control or practice.
5. State how the reader can tell whether it worked.

If a paragraph does none of those, cut it or move it to notes.

## Energy Without Hype

Avoiding sales prose does not mean writing dry governance doctrine. Every major
section should include at least one concrete person, decision, deadline,
consequence, or tradeoff before returning to control vocabulary.

Good energy comes from pressure:

- A support manager has to answer the customer by Friday.
- Finance has two reviewer-hours left this week.
- The policy source and contract disagree.
- A reviewer says no and needs protection from escalation pressure.
- A tool is useful but cannot touch customer records.

Use active verbs. Let sentences carry stakes.

Good hinge sentence:

```text
The workflow has not gained trust. It has moved the bottleneck.
```

Aim for one memorable sentence per section, earned by the surrounding evidence.

## Sentence-Level Rules

Prefer:

- "This fails when..."
- "The useful distinction is..."
- "The workflow should..."
- "The evidence supports..."
- "In high-risk workflows..."
- "A practical version is..."
- "The tradeoff is..."

Avoid:

- "AI is revolutionizing..."
- "Unlock the power of..."
- "In today's rapidly evolving landscape..."
- "Game-changing."
- "Seamless."
- "Transform your organization."
- "Harness AI to supercharge..."
- "The future belongs to..."
- "Simply..."
- "Just..."
- "All you need to do..."
- "Appropriate" without naming the standard.
- "Thoughtful" without naming the tradeoff.
- "Intentional" without naming the decision.
- "Holistic" without naming the boundary.
- "Best practice" without naming the evidence.
- "Responsible use" without naming the control.
- "Ensure" when the workflow only encourages or checks.
- "Leverage" when "use" is enough.
- "Pretend," "dishonest," "fake," "theater," "blame," or "negligent" when the
  sentence can name the missing workflow state or control.

Replace universal claims with scoped claims:

- Bad: "AI makes verification the bottleneck."
- Better: "In many consequential workflows, verification becomes the scarce
  adoption capability."

- Bad: "Human review fails."
- Better: "Human review can fail when reviewers lack evidence, time,
  independence, or authority to reject."

- Bad: "Governance solves this."
- Better: "Governance helps only when it changes what the workflow can accept,
  escalate, or stop."

## Paragraph Shape

Default paragraph pattern:

1. Claim.
2. Concrete reason.
3. Operational consequence.

Example:

```text
An AI-generated recommendation should begin as a proposal, not a decision. The
model can synthesize the ticket, contract, and policy faster than a human can,
but it cannot grant itself credit authority. The workflow has to decide when the
proposal has enough evidence to become an accepted action.
```

Avoid paragraphs that only intensify the same idea:

```text
AI is changing everything. Organizations must move quickly. The opportunities
are enormous, but so are the risks. Leaders need a new way to think about trust.
```

That paragraph sounds plausible and says almost nothing.

## Chapter Rhythm

Each chapter should have this rhythm:

1. **Operational opening**: a concrete problem from the running case or a
   similarly grounded scenario.
2. **Distinction**: the conceptual move the chapter owns.
3. **Mechanism**: why the problem happens.
4. **Practice**: what changes in the workflow.
5. **Failure test**: how the practice can fail or become theater.
6. **Reader handle**: a short rule, question, or artifact field the reader can
   reuse.
7. **Bridge**: why the next chapter is necessary.

Do not open chapters with broad AI commentary unless the commentary immediately
resolves into an operational question.

## Heading Tone

Headings should sound like a serious book, not a keynote deck or AI-generated
manifesto.

Prefer quiet operational headings:

- "How context gets mistaken for evidence."
- "When real sources go stale."
- "Mapping which source can answer which claim."
- "Decision states after the evidence check."

Avoid slogan headings:

- "Context Helps The Model. Evidence Lets The Workflow Accept."
- "Freshness Is A Control, Not A Courtesy."
- "The Claim Is The Unit Of Verification."
- "Trust The Process, Not The Prompt" as a repeated section pattern.

Avoid overusing:

- "X, not Y."
- "This thing enables that thing."
- Sentence fragments that sound like product positioning.
- Grand binary claims where a workmanlike description would do.

Use the strongest aphoristic sentence rarely, after the chapter has earned it.
Most section titles should name the work being done, the failure being exposed,
or the artifact being introduced.

## Storytelling Pattern

Use `storytelling-guide.md` as the standard for examples and section openings.
The preferred movement is:

1. workplace scene;
2. human fallibility pattern;
3. existing workplace contract;
4. AI analogue;
5. analogy break;
6. decision acceptance contract.

The book should not ask readers to accept abstract claims about evidence,
review, permissions, or governance until they can see a familiar work situation
where the control pattern already exists for fallible humans and where AI
changes the mechanics.

Strong example sentence:

```text
The organization does not trust the manager's memory of the policy; it trusts
the current policy, the approval limit, the finance review, and the audit trail.
```

Then move to AI:

```text
The AI assistant changes the speed of the draft, not the source of authority.
```

If a major assertion cannot be tied to a visual workplace example, mark it as
unfinished rather than polishing it.

## Use Of The Running Case

The case should feel like work, not like a parable.

Include:

- Conflicting incentives.
- Missing data.
- Stale sources.
- Overloaded reviewers.
- Tool sprawl.
- Executive pressure.
- A moment where the framework escalates instead of giving a neat answer.

Avoid:

- Perfect examples where every control works.
- Fictional dialogue that sounds like a training video.
- Characters who exist only to say the author's point.

## Metaphor Policy

Use few metaphors. Prefer operational language.

Allowed if useful:

- Boundary.
- Proposal.
- Authority.
- Control.
- Evidence.
- Queue.
- Throttle.
- Escalation.
- Audit trail.

Use sparingly:

- Copilot.
- Guardrails.
- Assembly line.
- Factory.

Avoid:

- Magic.
- Crystal ball.
- Oracle.
- Superpowers.
- Brain.
- Digital employee, unless critiquing the phrase.

## Evidence Tone

Write evidence honestly:

- "This study suggests..."
- "This source supports a narrower claim..."
- "This is a governance standard, not empirical proof."
- "The evidence here is strongest in software and operations."
- "This remains a source gap."

Do not convert weak evidence into strong prose.

Avoid:

- "Research settles..."
- "Studies show..." without naming what kind of study.
- "Everyone knows..."
- "It is clear that..." when the argument is doing the work.

## How To Discuss AI Capability

The book should not sound anti-AI.

Say:

- AI can produce useful judgment-like work.
- LLMs can learn internal structure and adapt in context.
- Generation can be valuable even when it is not authoritative.
- Capability growth changes where the trust boundary sits.

Do not say:

- AI is "just autocomplete" as the author's final position.
- AI "understands" without qualifying what kind of evidence supports that word.
- Better models will remove the need for process.
- Current failures establish future uselessness.

## How To Discuss Governance

Governance should sound operational, not bureaucratic.

Good governance language:

- "What does this policy prevent?"
- "Who can override it?"
- "What log shows it fired?"
- "What happens when it fails?"
- "Who pays for review capacity?"

Bad governance language:

- "Establish a robust framework."
- "Ensure responsible AI across the organization."
- "Promote stakeholder alignment."
- "Drive governance maturity."

If a governance sentence could appear in a consulting slide without changing
any real workflow, rewrite it.

## Anti-Sales Checklist

Before accepting a section, ask:

- Did it name a specific workflow, failure, control, or tradeoff?
- Did it avoid promising transformation without mechanism?
- Did it say who owns the work?
- Did it say what evidence is required?
- Did it say what happens when the control fails?
- Did it avoid "AI will..." unless the claim is sourced and scoped?
- Did it avoid turning uncertainty into confidence?

## Example Rewrites

### Example 1: Salesy Version

```text
AI gives organizations an unprecedented opportunity to transform how work gets
done. By embracing AI responsibly, leaders can unlock productivity while
maintaining trust and governance.
```

### Example 1: Book Voice

```text
AI makes some work cheaper to produce before it makes that work cheaper to
trust. A support team can generate exception recommendations all afternoon. If
finance and compliance can review only ten of them, the workflow has not gained
trusted throughput. It has moved the bottleneck.
```

### Example 2: Salesy Version

```text
The key is to keep humans in the loop so AI remains safe, ethical, and aligned
with organizational values.
```

### Example 2: Book Voice

```text
A human in the loop is not a control by itself. The reviewer needs the evidence
packet, time to inspect it, independence from the team asking for speed, and
authority to reject the recommendation. Without those conditions, review becomes
visible process without operational control.
```

### Example 3: Salesy Version

```text
With the right governance framework, companies can confidently scale AI across
the enterprise.
```

### Example 3: Book Voice

```text
Governance matters only when it changes what the workflow can accept, escalate,
or stop. A policy that does not affect permissions, evidence requirements,
review queues, or audit logs is not yet a control. It is a document.
```

## Preferred Terms

Use consistently:

- AI output begins as a **proposal**.
- A proposal needs **evidence**.
- Evidence supports a claim; **verification** checks it.
- **Acceptance** grants permission to use the output.
- **Authority** means the output may change a decision, record, customer, or
  system state after acceptance.
- **Controls** must have hooks.
- **Trust** is earned by the workflow, not by fluent prose.

Avoid term drift:

- Do not use evidence, verification, approval, acceptance, and authority as
  synonyms.
- Do not call every control deterministic.
- Do not call every workflow a governance framework.

## Term Coherence And Correctness

Term coherence is not only repetition. A term is coherent when it names the same
concept every time, and it is correct when it does not grant more certainty,
authority, causality, or domain weight than the evidence supports.

Use this table as the book's working term ledger:

| Term | Use It For | Do Not Use It For |
| --- | --- | --- |
| **Output** | Any material produced by the model or AI-assisted workflow. | A decision, unless the workflow has accepted it. |
| **Draft** | Text or work material that has not entered an acceptance path. | A customer commitment or system change. |
| **Proposal** | An output being considered for use. | Accepted work or approved action. |
| **Claim** | A statement in the proposal that can be checked. | The whole answer when only one sentence is at issue. |
| **Context** | Material the model or reviewer can use to understand the situation. | Evidence with authority for a specific claim. |
| **Evidence** | Material the workflow can use to support or reject a claim. | Any retrieved snippet, memory, or plausible source. |
| **Freshness** | Whether a source is current enough for the decision. | General quality or accuracy. |
| **Verification** | Checking a claim, source, state, or condition. | Granting permission to act. |
| **Review** | Human or automated inspection against a standard. | Authority by itself. |
| **Approval** | Permission granted by a named role, policy, or owner. | A vague positive reaction such as "looks good." |
| **Acceptance** | The workflow state that allows the output to be used. | Individual confidence, approval vibes, or generated quality. |
| **Authority** | The right to affect a decision, record, customer, money, or system state. | Fluency, usefulness, or source citation. |
| **Control** | A hook that can block, route, require, log, throttle, escalate, or stop work. | A reminder, label, policy, or dashboard with no effect on the workflow. |
| **Boundary** | A limit on access, communication, mutation, tool use, or what the output may do before acceptance. | A preference written only in prose. |
| **Trustworthy** | A workflow condition created by evidence, permission, review, audit, and correction. | A permanent property of an AI model or output. |
| **Probabilistic** | Behavior involving uncertainty, distributional prediction, judgment, or variable outcomes. | Randomness in the casual sense, or proof that the system is unusable. |
| **Deterministic** | Behavior where the same defined input and state produce the same defined output. | A synonym for trustworthy, safe, audited, or controlled. |

Term-correctness checks:

1. If a sentence says `decision`, ask what accepted it.
2. If a sentence says `evidence`, ask which claim it supports.
3. If a sentence says `verification`, ask what was checked and against what
   standard.
4. If a sentence says `approval`, ask who had permission to grant it.
5. If a sentence says `authority`, ask what action, change, or communication
   became permitted.
6. If a sentence says `control`, ask what it can block, route, log, throttle,
   escalate, require, or stop.
7. If a sentence says `trustworthy`, ask whether the trust belongs to the
   model, the output, or the workflow. Prefer the workflow unless the claim is
   deliberately narrower.
8. If a sentence says `caused`, ask whether the cited source supports causality
   rather than contribution, sequence, or analogy.

Use stronger terms only after the workflow earns them:

```text
Draft -> proposal -> ready for review -> accepted -> approved effect.
```

Avoid softer terms when the workflow needs precision:

- Replace "AI recommendation" with "AI-generated proposal" when authority has
  not been granted.
- Replace "the reviewer approved it" with "the reviewer accepted the packet" if
  the reviewer checked completeness but did not grant business permission.
- Replace "the control improved safety" with the specific effect: "the gate
  blocked customer send when the policy source was stale."
- Replace "the source established the claim" with "the source supported the claim"
  unless the source actually establishes proof.

## Line Editing Pass

After drafting a chapter, run this pass:

1. Search for hype words: revolutionize, unlock, transform, supercharge,
   seamless, game-changing, future, powerful, robust.
2. Search for vague nouns: framework, alignment, enablement, maturity,
   innovation, governance, transformation.
3. For each, ask whether the sentence names a real mechanism.
4. Replace vague claims with workflow claims.
5. Replace universal claims with scoped claims.
6. Replace confidence with evidence where appropriate.
7. Run the term-correctness checks above for recurring technical terms.
8. Run the fault-tone check: replace blame-loaded intent language with the
   missing state, source, owner, queue, permission, or control.
9. Flag noun stacks with three or more abstract nouns in a row.
10. Rewrite at least half of flagged noun stacks into actor + action + object +
   consequence.

Example:

```text
Weak: The organization needs an operational trust architecture for AI adoption.
Better: The support workflow needs to block a credit recommendation when the
policy source is stale.
```

The goal is not dry prose. The goal is earned force.
