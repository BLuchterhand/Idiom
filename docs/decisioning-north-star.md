# Decisioning North Star

This is the decisioning standard for the harness.

## Core Split

- The model owns strategy.
- The harness owns contracts.

In practice:

- The model chooses which tool to call next, whether to answer now, whether a narrow follow-up is worth it, and how to synthesize the evidence.
- The harness enforces permissions, safety boundaries, protocol correctness, verification requirements, completion contracts, and loop/stall fuses.

## Preferred Behavior

When the model is making a normal execution choice, the harness should steer before it commands.

- Prefer a nudge over a forced tool choice.
- Prefer a redirect over a hard rejection on the first mistake.
- Prefer bounded follow-up budgets over automatic answer synthesis.
- Prefer “answer now or take one narrow next step” over “you must use tool X next”.

## Force Is Still Correct

Hard enforcement is still required for invariants. These are not strategy suggestions.

- Permission and write boundaries.
- Forbidden tools and protected paths.
- Native tool-call protocol correctness.
- Verification requirements after writes.
- Final-answer readiness and evidence contracts.
- Repeated identical-call loop breaks after warnings.
- Post-budget “no more tools” cutoffs.

## Audit Rule

For every host-side decision point, ask:

1. Is this protecting an invariant?
2. Or is this choosing a strategy the model should choose?

If it is protecting an invariant, force is appropriate.

If it is choosing a strategy, the default should be:

- nudge first
- block only the clearly wasteful or unsafe repeat
- keep at least one model-directed path open when that path is still safe and contract-compliant

## Examples

Good:

- “Do not restart broad discovery. Either answer now or do one targeted follow-up read.”
- “After a child packet, block unrelated broad rediscovery, but leave packet-anchored follow-up reads open.”
- “If a follow-up is rejected only because it failed to name the gap it is closing, require the named gap and allow one corrected retry.”
- “After readiness, ask the model to name one exact missing claim and one exact probe that would resolve it; do not hard-pick the probe for the model.”
- “Later recovery and failure prompts should stay closure-biased too: after readiness, prefer ‘answer now or one exact probe,’ not broad fallback menus like repo-wide filename search.”
- “Use the structured tool instead; if it returns nothing, retry the original request.”
- “You have already used many read calls. Pause, summarize, and commit to the minimal next step.”

Bad:

- “You must delegate a child task before any other read is allowed.”
- “You already have enough evidence, so the harness will synthesize the final answer for you.”
- “Only tool X remains available” when the model could still safely answer or use a narrow follow-up.

## Repo Policy

This standard applies across:

- static prompt files, role specs, runtime briefs, and recovery/failure packets
- runner preflight and request-budget logic
- discovery redirects
- completion and readiness gates
- delegated child-task handling
- control-rail documentation and future features

Prompt layers should agree on the same shape:

- structured tools before generic text search when the language supports them
- answer once the evidence is sufficient
- after readiness, continue only for one exact missing claim and one exact probe
- treat search policies and examples as ranked guidance, not mandatory scripts

The goal is frontier-style behavior: open-ended model decisioning constrained by explicit, observable harness contracts.
