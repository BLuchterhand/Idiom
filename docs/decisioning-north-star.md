# Decisioning North Star

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

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

## Model-Harness Parity

The ideal state is not a clever harness that constantly corrects the model. The
ideal state is parity: the model understands the same observable contract the
harness enforces, and the harness backstop rarely fires.

Use prompting, runtime briefs, and step-state packets to teach contract
semantics in model-readable language:

- what evidence means a task is ready to answer or write
- what exact missing fact justifies one more probe
- what counts as completion, verification, no-op, or blocker
- which obligations are planner-owned, lane-owned, or host-owned

Readiness state is harness-computed observable state, not a model self-grade.
It should summarize closed claims, open obligations, evidence sources, and
coarse readiness classes such as answer, write, or verify. It may be computed
from tool results, prompt-provided context packets, architect packets,
discovery packets, exact anchors, changed paths, and verification results. It
must not prescribe the exact next tool unless a hard invariant is being
enforced.

## Capability Menus, Not Language Menus

The model should declare the capability it needs in task terms: inspect this
symbol, check this source file, explain this dependency, find callers, verify
this edit. The harness should resolve that request to the best concrete
resource for the affected file, language, and workspace.

Prefer generic, capability-oriented routes over exposing language-specific
strategy choices in prompts or recovery text:

- Good: the model asks for source sanity on `src/foo.cpp`; the harness routes
  to C++ diagnostics when available and falls back to a generic source check
  when they are not.
- Good: the model asks for symbol navigation on a Go file; the harness uses
  the Go-aware indexer if present and otherwise reports that only generic
  search is available.
- Bad: a prompt tells the model to choose between `go_diagnostics`,
  `typescript_diagnostics`, `rust_diagnostics`, and `cpp_diagnostics` as a
  strategy step.

Language-specific tools are still valid implementation details and may remain
available when useful. They should not be the canonical way to express a
contract. The durable contract is the capability, the path or symbol it applies
to, the evidence returned, and any uncertainty or fallback used.

Use deterministic checks for the invariant boundary:

- required edit paths must be changed or explicitly blocked
- writes must stay inside the allowed scope
- verification evidence must match the artifact risk and occur after the
  relevant writes
- workflow artifacts must move through typed state transitions; terminal,
  source-satisfied, stale, failed-refine, dirty-scope, invalid-scope, and
  dispatchable states are harness facts, not prose guesses
- repeated low-value reads/searches must eventually stop
- when the harness has computed a required-path implementation state, the
  tool surface should expose the contract menu: in-scope write capabilities,
  semantic/context/declared project discovery or narrow required-path source
  reads for named edit-safety gaps, or a concrete blocker. Exact-anchor
  packets may be write-first; abstract/rich packets should not be treated as
  if the precise edit anchor is guaranteed.

If a deterministic backstop fires repeatedly, treat that as an alignment gap,
not as proof that the model should be micromanaged. The first repair should be
to make the structured contract clearer at its canonical owner: route metadata,
runtime brief, tool result, step-state packet, or one prompt surface. Do not add
the same rule to every prompt layer.

Do not confuse harness-induced degradation with model incapability. When a
model that is normally capable of multi-file editing or planning starts
producing shallow prose, malformed artifacts, omitted paths, or repeated
contract misses, first compare the actual runtime against the intended one:
role selection, model route, thinking mode, context budget, prompt lead-in,
available tools, recovery state, and completion gate. If the harness changes a
semantic editing task into a brittle serialization task, or routes a planning
contract through an actor configured for a different role, the right fix is to
realign the lane contract or actor boundary before tightening prompts.

## Workflow Transition Contracts

Long-running agent workflows should be modeled as bounded, auditable state
machines. Before a model receives an implementation lane, the harness should
classify the artifact it is about to act on: dispatchable, complete,
source-satisfied, stale-needs-rebase, failed-refine-draft, blocked by dirty
scoped files, or missing the scope required for a safe write. Scope must be
inside the workspace root; outside-root paths are invalid even when they exist
on disk. These classifications are contract outcomes. A non-dispatchable
classification is not a model failure and should be logged as useful evidence
for the caller.

The generic pattern is:

- Intake reads a durable artifact plus the workspace state and emits a typed
  report.
- Execution lanes refuse terminal, stale, dirty, or unscoped artifacts before
  spending model tokens.
- Review and acceptance lanes write explicit source-satisfied or complete
  outcomes instead of rewriting already-satisfied work.
- Queue producers consume the same state report instead of duplicating
  project-specific status parsing.

This belongs in idiom core when the invariant is generic: do not ask an agent
to mutate stale, already-complete, or ambiguous work. Project policies can
choose what artifact format to feed the gate, but the bounded transition and
audit behavior are harness responsibilities.

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

## Typed Authority Contracts

Prompts may explain a contract, but they are not the contract's source of
truth. When a lane needs authority or obligations, compile them once into a
typed route/runtime contract and carry that contract through CLI, TUI, runner,
executor, review, resume, recovery, telemetry, and audit.

Keep these fields distinct:

- required coverage paths: every path must be edited, source-satisfied, or
  explicitly blocked
- allowed candidate paths: safe write/search scope, not mandatory coverage
- exact edit snippets: source/current-state anchors that must survive into the
  edit or `submit_plan`
- edit-set obligations: run-level requirements such as at least one test edit
  or at least one implementation-shaped edit
- evidence paths: facts observed in packets, logs, commands, or source reads
- recovery state: which typed obligation already failed and which retry is
  still allowed

Do not infer hard authority from generated prompt text, examples, tool schemas,
context packets, transcript excerpts, recovery prose, or headings such as
`editable files`. Those may localize or explain work, but they must enter the
typed contract as candidate scope unless a human, command mode, durable
artifact, or explicit structured field makes them required coverage. If a
recovery retry adds a new obligation, store it in the typed contract and progress
state before the next model call; do not rely on the retry addendum to be
rediscovered later.

## Git History As Decision Evidence

Repository history is part of Idiom's memory substrate. Before changing,
reverting, or simplifying established behavior, use focused history evidence
when the existing rationale is not obvious: line history, symbol history, blame,
or a narrow `git log` over affected paths. Old rationale is not a veto, but it
must be considered as evidence alongside current logs, tests, audits, and
product requirements.

Commits should preserve the decision context needed by future agents. For
non-trivial changes, the commit body should explain why the change was needed,
which failure or requirement it addresses, what contract or tradeoff was chosen,
what meaningful alternatives or reversions were avoided, and what verification
supports the result. A diff tells what changed; the commit should tell why this
change should survive.

## Prompt Surface Ownership

Prompting has a proper place: stable working style, domain norms, and
model-readable context. It becomes drift when the same obligation is repeated
across base prompts, profiles, project docs, late instruction attachments,
runtime briefs, recovery packets, and wrapper-generated briefs.

The rule is:

- One obligation gets one canonical owner.
- Repeated operational requirements belong in typed runtime briefs, tool
  schemas, deterministic checks, completion gates, or telemetry.
- Prompt text should explain behavior the model must reason about and align the
  model with existing contracts, not emulate a missing contract.
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

## Frontier Harness Simplicity

When comparing idiom to frontier coding harnesses, prefer the simplest
equivalent contract over another prompt layer or phase machine.

Frontier-style does not mean fewer capabilities. It means:

- small, typed tool surfaces where possible
- permissions, writes, verification, and loop breaks enforced by the host
- edit tools returning or triggering enough diff/diagnostic evidence to make
  the next model choice obvious
- prompts describing stable behavior, not compensating for missing contracts
- model-directed continuation whenever a safe, contract-compliant path remains

Before adding a workflow stage, prompt reminder, or special-case recovery,
ask whether the issue is really one of these simpler contract gaps:

- the tool result lacks the evidence the model needs
- the route brief failed to declare the contract
- the completion gate accepted weaker evidence than the changed artifact
  requires
- the loop fuse allowed repeated low-value work after the evidence was already
  sufficient
- telemetry cannot show the failure shape clearly enough for the next audit

Verification must match artifact risk. Patch-hygiene checks such as
`git diff --check` can satisfy prose/config formatting checks, but they are not
source-aware verification for code edits. Source edits require language
diagnostics through the generic `source_sanity` route, a focused build/test, or
a concrete blocker that says why source-aware evidence is unavailable.
Tautological assertions (`assert true`, `CHECK(true)`, `_assert_true(true)`) and
comments that admit the behavior is not inspected are not verification evidence;
they should be rejected by deterministic lane contracts, not negotiated in a
prompt.

Phase transitions should carry a compact evidence bundle rather than asking the
next model pass to rediscover context. A review pass should see packet status,
candidate files/symbols/tests, unresolved packet gaps, changed paths,
source-sanity results, diff hygiene, and open obligations before it decides to
pass, repair, or block.

## Drift Detection

Prompt overlap is a harness smell, not merely a writing problem. Look for these
signals before adding more instructions:

- behavior claims are not backed by conversation logs, telemetry logs, or
  `harness_audit` output
- a failure can only be diagnosed by hand-reading one raw JSONL file because
  the audit layer does not summarize the relevant contract, recovery, or
  no-progress shape
- the same rule appears in multiple prompt/profile/project files
- late system-message attachment is needed routinely for normal tasks
- wrapper docs tell agents to rely on a behavior that is not represented in a
  config schema, runtime brief, tool schema, or audit field
- recovery packets prescribe a strategy instead of reporting the violated
  contract and keeping safe choices open
- agents enter read/search loops after a bounded implementation brief because
  the edit scope, exact anchor, or verification owner was only prose
- deterministic backstops fire frequently for behavior the model should be able
  to infer from the runtime brief and current evidence
- behavior audits show `prompt_surface_overlap`, repeated rejections, broad-read
  loops, or structured `harness_feedback` against prompt/tool/context contracts

The repair path is:

1. Identify the single owner for the behavior.
2. Decide whether the problem is contract absence, contract opacity, or model
   non-compliance with a clear contract.
3. Promote repeated obligations into structured metadata or deterministic gates
   when the contract is missing.
4. Align the model-facing wording at the canonical owner when the contract
   exists but the model is not naturally following it.
5. Use logs as the source of truth for whether the repair worked; if the logs
   cannot answer that question, fix the logging or audit path before declaring
   the behavior understood.
6. Remove duplicate prose from secondary prompt surfaces.
7. Add or update audit telemetry so the same drift shape is visible next time.
8. Add an expectation test for the behavior, not a coverage-driven test of the
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
- model-facing guidance and harness enforcement should describe the same
  contract from different sides: prompts explain how to recognize readiness,
  while gates enforce the invariant when readiness is ignored
- prompt-surface drift should produce audit-visible evidence (`prompt_surface_overlap` or `harness_feedback`) and a consolidation plan, not another overlapping instruction

The goal is frontier-style behavior: open-ended model decisioning constrained by explicit, observable harness contracts.
