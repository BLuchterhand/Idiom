# Chapter 4 Packet: The Shape Of A Good AI Task

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Split status.

Task-fit and human-centered allocation can be drafted with caveats. Broader
operational-decision claims should stay scoped to cited support-center
standards and the labeled composite case.

## Chapter Claim

AI fits best where the objective, evidence, permissions, output, acceptance
criteria, and recovery path are explicit enough for the workflow to judge the
result.

## Reader Starting Point

The reader may ask, "Where can we use AI?" without separating exploration,
reviewed proposal, and bounded action.

## First Visible Moment

An operations lead drops a ticket into the queue with the note, "handle this
exception." The ticket has a customer request and a stale policy link, but no
current source, approval limit, required output, or recovery path. One analyst
reads it as a draft-summary task. Another reads it as a recommendation. A third
starts looking for approval authority because the customer is already waiting.
The work is not impossible; the assignment has not said what would make the
result acceptable.

Scene beats:

1. The request sounds simple until people ask what decision is actually being
   made.
2. The visible ticket gives background, not enough authority to act.
3. Different analysts infer different outputs because the work order never
   names acceptance criteria.
4. The team realizes the first AI design choice is not the model; it is the
   shape of the task.
5. The diagnostic separates exploratory help, reviewed proposal, and bounded
   action.

## Workplace Behavior Parallel

A human analyst also fails when given an unclear assignment with hidden
constraints and no recovery path.

## Human Fallibility Pattern

People infer missing scope differently. They overfit to the visible request,
miss hidden authority constraints, or optimize for speed because the work order
does not say what counts as accepted.

## Existing Workplace Contract

Scoped assignments define objective, sources, permissions, deliverable shape,
acceptance criteria, and escalation path.

## AI Analogue

AI can do valuable work when those same boundaries are explicit. It becomes
risky when hidden state, stale facts, irreversible actions, or political
pressure turn proposals into decisions.

## Analogy Break

AI can act across tools and generate many plausible paths without knowing which
constraints are organizationally binding unless the workflow exposes them.

## Operator Artifact

Task-fit diagnostic:

| Dimension | Good Fit Signal | Risk Signal |
| --- | --- | --- |
| Objective | Clear outcome | Ambiguous goal |
| Evidence | Accessible sources | Hidden or contested sources |
| Permission | Read-only or bounded effect | Irreversible action |
| Output | Observable artifact | Vague judgment |
| Verification | Cheap check | Expensive or impossible check |
| Recovery | Easy rollback/escalation | No safe fallback |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | The intake owner maintains the task-fit diagnostic for each workflow. |
| Source authority | Fit depends on available evidence, permissions, verification, and recovery sources. |
| Update cadence | Re-run when the task moves from assistant to reviewed proposal or bounded action. |
| Failure signal | Teams disagree about objective, what the output may do before acceptance, or acceptance criteria. |
| Operational hook | Keep the task exploratory, require proposal review, or reduce what the output may do before acceptance until task shape is explicit. |
| Escalation/retirement | Tasks that cannot expose sources, permissions, or recovery paths stay exploratory. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use human-centered allocation and software/workflow sources for task-shape claims. |
| Normative control standard | Consequential tasks should expose objective, evidence, permissions, output, acceptance criteria, and recovery path. |
| Composite scenario | The work-order example is illustrative unless support-specific workflow sources are added. |

## Evidence Status

Sources: **D1**, **D2**, **D3**, **H5**, **H6**, **H7**, **S2**, **S5**,
**S6**, **A5**, **A10**.

Gaps: task-fit studies outside software; support QA and operational decisions.

## Main Sections

1. **The underspecified work order**: open with the exception request.
2. **Task shape before model choice**: explain the diagnostic.
3. **Good early uses**: summarization, extraction, drafting, critique,
   transformation, option generation.
4. **Risk conditions**: hidden state, irreversible effects, stale sources,
   contested authority.
5. **Classification**: discovery only, draft assistance, reviewed proposal, bounded action.

## Practical Takeaway

Choose first AI workflows where verification is cheaper than the value created
and where the output can do only one narrow thing before acceptance.

## Possible Objections

- "This excludes the most valuable work": answer that it sequences adoption.
- "We can rely on expert users": answer that the workflow still needs evidence.
- "Agents can handle ambiguity": answer that ambiguity and authority differ.

## Bridge

Chapter 4 chooses a task shape. Chapter 5 shows how evidence makes the chosen
task reviewable.
