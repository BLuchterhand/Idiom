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

## No Prompt-Magic Contract Triggers

Contract selection must not depend on magic phrases in atom specs or strategy
prompts. Do not encode a harness contract through wording such as
"Contract: implementation_result", "first action must be edit_file", or any
other ad hoc string that a caller is expected to sprinkle into prose to force a
particular run mode.

Contract selection belongs in run metadata, typed route plans, runtime briefs,
roles, or command modes — not in the model's strategy text. When a harness
feature exists (for example, `idiom implement` sets
`runOptions{TaskFamily: "implementation"}`), use it. If generic prompt routing
misclassifies a write task, fix the structured routing or brief construction
rather than adding magic phrases to atom specs.

Good: run metadata or a command mode declares the contract; the model receives a clean strategy brief.

Bad: a strategy prompt or atom spec includes a magic phrase that a harness component parses to override route classification.

## Prompt Surface Ownership

Prompting has a proper place: stable working style, domain norms, and
model-readable context. It becomes drift when the same obligation is repeated
across base prompts, profiles, project docs, late instruction attachments,
runtime briefs, recovery packets, and wrapper-generated briefs.

The rule is:

- One obligation gets one canonical owner.
- Repeated operational requirements belong in typed runtime briefs, tool
  schemas, deterministic checks, completion gates, or telemetry.
- Prompt text should explain behavior the model must reason about, not emulate
  a missing contract.
- If a prompt addition fixes a recurring failure, first ask whether the failure
  should instead be a route-plan field, required edit path, tool availability
  change, stale-context refresh rule, or audit finding.

Use prompt text for:

- durable engineering taste and communication style
- project conventions that genuinely vary by workspace or directory
- examples that make an existing structured contract easier to understand

Do not use prompt text for:

- hidden mode switches or magic phrases
- repeated edit-scope, verification, or tool-availability requirements
- compensating for unknown config keys or unmodeled runtime settings
- scenario-specific recovery scripts after a generic contract failed

## Drift Detection

Prompt overlap is a harness smell, not merely a writing problem. Look for these
signals before adding more instructions:

- the same rule appears in multiple prompt/profile/project files
- late system-message attachment is needed routinely for normal tasks
- wrapper docs tell agents to rely on a behavior that is not represented in a
  config schema, runtime brief, tool schema, or audit field
- recovery packets prescribe a strategy instead of reporting the violated
  contract and keeping safe choices open
- agents enter read/search loops after a bounded implementation brief because
  the edit scope, exact anchor, or verification owner was only prose
- behavior audits show `prompt_surface_overlap`, repeated rejections, broad-read
  loops, or structured `harness_feedback` against prompt/tool/context contracts

The repair path is:

1. Identify the single owner for the behavior.
2. Promote repeated obligations into structured metadata or deterministic gates.
3. Remove duplicate prose from secondary prompt surfaces.
4. Add or update audit telemetry so the same drift shape is visible next time.
5. Add an expectation test for the behavior, not a coverage-driven test of the
   implementation detail.

Nudges are temporary. If the same nudge becomes routine, graduate it into a
generic contract or delete it.

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
- evidence tools should describe findings, counts, paths, handles, and symbols without prescribing the next tool call
- strategy guidance belongs in runtime briefs and control rails, not in evidence summaries or result payloads
- do not hardcode strategy around one recurring scenario or question class; if a rule is real, express it in generic evidence/contract terms rather than domain-specific terms
- answer composition must be evidence-shaped, not topic-template-shaped; repeated live-test topics should become generic contracts or eval fixtures, not special final-answer branches
- before adding prompt text, check whether the same obligation is already owned by the base prompt, a profile, runtime brief, control rail, tool schema, wrapper doc, or recovery packet
- prompt-surface drift should produce audit-visible evidence (`prompt_surface_overlap` or `harness_feedback`) and a consolidation plan, not another overlapping instruction

The goal is frontier-style behavior: open-ended model decisioning constrained by explicit, observable harness contracts.
