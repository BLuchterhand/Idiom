# North-Star Drift Recommendation

## Summary

Idiom has drifted from its intended decision model through additive local fixes.
The stated north star is still correct:

- The model owns strategy.
- The harness owns contracts.

The current risk is that too much behavior is expressed as prompt prose across
too many layers. Base prompts, profile prompts, wrapper prompts, runtime briefs,
dynamic instruction attachments, recovery packets, read-budget nudges, and
verification nudges can all appear in one run. Even when each layer is
reasonable in isolation, the combined system asks the model to reconcile
competing advice instead of following one clear contract.

The recommendation is to reduce behavioral prompt surface area, encode repeated
requirements as typed harness contracts, and use nudges only as temporary
diagnostics or narrow recovery for true invariant violations.

## How Drift Happened

The drift pattern was additive repair:

1. A model behavior failed.
2. A prompt nudge or recovery message was added.
3. Another failure appeared in a nearby scenario.
4. Another nudge, gate, or late instruction was added.
5. The system accumulated overlapping guidance for the same decision.

This creates hidden ownership conflicts:

- The prompt says to use judgment.
- The runtime brief says to follow a lane contract.
- The recovery packet says to do one next action.
- The read-budget rail says to stop reading.
- The verification rail says to verify now.
- Dynamic instruction attachment may add project-level discovery rules mid-run.

The model then sees a noisy hierarchy rather than a clean execution contract.
Partial implementations and false closeouts become more likely.

## Concrete Failure Modes

### Silent Config Drift

Configuration keys that are ignored silently are dangerous. Operators may think
a behavior is disabled or enabled when the running harness does something else.

Recommendation:

- Add warnings or strict validation for unknown config keys.
- Keep backward-compatible aliases only when they are explicitly modeled and
tested.
- Surface loaded prompt sources and dynamic instruction settings in audit output.

### Dynamic Instruction Leakage

Late system-message injection can override or compete with the original lane
contract. Project instruction attachment should not be a hidden default in
implementation lanes.

Recommendation:

- Make project instruction attachment opt-in, not implicit.
- Honor `attach_instruction_files` at the session-construction boundary.
- Log whether dynamic instruction attachment is enabled.
- Treat late system messages as exceptional contract events, not normal context
hydration.

### Prompt Nudge Accretion

Repeated instructions such as "write first," "stop reading," or "verify now" are
too important to live in several prompt layers.

Recommendation:

- Replace repeated prose with one typed contract.
- Keep prompts focused on stable working style.
- Keep runtime briefs focused on lane-specific state.
- Keep runner gates focused on invariant enforcement.
- Remove or consolidate duplicate prompt language when a contract is added.

### Over-Eager Verification Gating

Verification gates can accidentally convert partial work into accepted work if
they block normal inspection or editing after a small number of writes.

Recommendation:

- Planner-owned verification should block build/test commands, not additional
  scoped edits, `git diff`, or targeted inspection.
- Verification pressure should not fire until the model is attempting to close,
  run disallowed build/test commands, or continue unrelated exploration.
- Completion gates should check changed-path and required-edit coverage before
  accepting an implementation final answer.

### Read-Budget Recovery Too Late

A hard read budget after many reads can still leave the model with scattered
context and no clear edit sequence. The recovery prompt then asks the model to
salvage the turn from partial evidence.

Recommendation:

- For exact-anchor implementation contracts, shape the tool surface before the
  first model call.
- Hide or sharply cap read tools before the first edit attempt when the target
  packet already contains exact anchors or patches.
- If the first edit anchor fails, require a concrete blocker rather than broad
  rediscovery.

### Source Path Parsing Is Too Narrow

If target-reference extraction recognizes only a subset of common source file
extensions, runtime contracts become weaker for many languages.

Recommendation:

- Treat source extensions generically: C, C++, Java, JavaScript, TypeScript,
  Python, Rust, Go, GDScript, shell, config, docs, and test fixtures.
- Use path parsing only to identify references and target scopes, not to infer
  project-specific behavior.

## Proposed North-Star Amendments

### 1. Behavioral Surface Minimalism

Every prompt, runtime brief, recovery message, tool-result hint, and dynamic
instruction is behavioral surface area.

New guidance must either:

- replace existing guidance,
- simplify existing guidance, or
- protect a new invariant that cannot be enforced structurally.

If it does none of those, do not add it.

### 2. One Owner Per Decision

Each behavior must have one owner:

- Base/profile prompts own general working style.
- Runtime contracts own lane-specific obligations.
- Tool schemas and permissions own available actions.
- Runner gates own invariant enforcement.
- Completion gates own acceptance criteria.

Avoid duplicate instructions across these layers.

### 3. Typed Contracts Over Prompt Nudges

Repeated behavioral requirements should become structured metadata, tool
availability, or acceptance gates.

Examples:

- Use `exact_anchor_implementation` instead of repeating "write first."
- Use `planner_owned_verification` to block build/test commands.
- Use completion gates to reject false closeout.
- Use strict config validation for unknown keys.

Do not encode contracts through magic phrases inside free-form prompts.

### 4. Late System Messages Are Exceptional

Late system messages should appear only for observable contract events:

- a tool request violated a permission rule,
- a required field is missing,
- a budget or loop fuse fired,
- a completion gate found a concrete mismatch.

They should not be used as routine context hydration after arbitrary file
touches unless explicitly enabled.

### 5. Nudges Are Temporary

A nudge is a diagnosis tool, not permanent architecture.

If a nudge repeatedly improves behavior, promote the underlying rule into:

- a typed route contract,
- a tool-surface restriction,
- a schema requirement,
- a deterministic validation check, or
- a completion gate.

Then remove or consolidate the prose.

### 6. Completion Quality Is A Harness Contract

Implementation lanes should not accept a final answer unless one of these is
true:

- the changed paths and required edit surface match the target contract,
- the requested work is explicitly reported as blocked with a concrete reason,
- the task is intentionally a partial atom and the final answer says so.

This is not strategy selection. It is contract enforcement.

### 7. Config Must Be Observable

The loaded runtime should be auditable.

Audit output should report:

- loaded prompt files,
- skipped or missing prompt files,
- unknown config keys when strict validation is enabled,
- dynamic instruction attachment state,
- active task family and contract,
- available tool families after contract shaping.

### 8. Evals Must Match Real Use

Regression tests should include the workflows Idiom is expected to handle, not
only small or read-only cases.

Recommended canaries:

- exact-anchor cross-file implementation,
- planner-owned verification with continued edits after two writes,
- ignored or unknown config-key warning,
- source path parsing across common language extensions,
- dynamic instruction attachment disabled,
- implementation final answer rejected when required target files were skipped,
- prompt-stack deduplication and source reporting.

## Prompting Recommendations

### Keep

- A short base prompt for general working style.
- A concise lane/runtime brief for the current contract.
- A minimal failure packet only when a real invariant is violated.

### Remove Or Consolidate

- Repeated "write first" language across wrapper, base, profile, and runtime
  brief.
- Repeated "stop reading" language after the tool surface can enforce it.
- Repeated verification prose when shell/tool policy can enforce it.
- Dynamic project instruction injection unless explicitly enabled.

### Avoid

- Prompt phrases that act like hidden mode switches.
- Recovery messages that prescribe strategy rather than state the violated
  contract.
- Tool-result summaries that tell the model which tool to call next.
- Project-specific examples in reusable harness prompts.

## Harness Recommendations

### Exact-Anchor Implementation Contract

Add a typed implementation contract for briefs that provide exact anchors,
exact old/new snippets, or unified patches.

Expected behavior:

- Treat the packet as sufficient evidence for the first edit attempt.
- Hide or cap read-class tools before the first edit attempt.
- Keep write tools available.
- If an anchor does not match, report the missing anchor.
- After a write, allow continued scoped edits and diff inspection.
- Accept closeout only when required changed paths or blockers are reported.

### Planner-Owned Verification Contract

Planner-owned verification should mean:

- block build/test shell commands,
- allow cheap sanity checks such as diff checks,
- allow additional scoped edits after sanity checks,
- do not block targeted inspection required to complete remaining edits,
- do not accept final answers that skip required edit surfaces.

### Completion Gate

The completion gate should be objective and generic.

Inputs:

- target refs,
- changed paths,
- required edit markers from the runtime contract when available,
- verification status or planner-owned verification status,
- explicit blocker text.

Accept only if:

- required changed paths are covered, or
- a concrete blocker is reported.

## Migration Plan

1. Add strict or warning-mode config validation for unknown keys.
2. Model any currently used config aliases explicitly.
3. Expand generic workspace path parsing.
4. Add typed `exact_anchor_implementation` contract metadata.
5. Shape the tool surface from the contract before the first model call.
6. Relax planner-owned verification gating so it blocks only build/test
   commands and unrelated drift.
7. Add completion gates for implementation closeout.
8. Remove duplicate prompt nudges that are now covered by contracts.
9. Add canary tests and one live implementation retry before resuming large
   feature-shipping loops.

## Review Checklist For Future Changes

Before adding a prompt, recovery message, or gate, ask:

1. Is this protecting an invariant, or choosing strategy for the model?
2. Which layer owns this behavior today?
3. Will this duplicate an existing instruction?
4. Can this be represented as typed contract state instead of prose?
5. How will audit output show that this behavior is active?
6. What canary would fail if this regresses?

If the answer is unclear, do not add another nudge. Simplify the contract first.

