# Atom Spec Pipeline Hardening Prep

Date: 2026-05-13

This prep turns the recent Colony atom-spec failures into idiom-aligned work
items. It starts from idiom's north star: the model owns strategy, the harness
owns contracts, evidence, freshness, auditability, and deterministic safety
boundaries. The goal is not to hard-code Colony behavior into idiom. The goal is
to move repeatable failure classes into generic contracts and leave project
vocabulary in profiles, prompts, and validators.

## Evidence Reviewed

- `docs/decisioning-north-star.md`: typed contracts beat prompt-magic triggers;
  one obligation gets one canonical owner; hard intervention is reserved for
  invariants.
- `docs/idiom-strategic-thesis.md` and `docs/idiom-design-roadmap.md`: durable
  repo memory, freshness, packet handoff, and audit are core differentiators.
- `cmd/idiom/artifact.go`: `artifact --no-tools` is a one-turn drafting mode
  with optional tool-use failure and a project-provided validator.
- `docs/architect-packets.md`: architect packets and `plan-audit` already own
  structured planning context and deterministic audit.
- `cmd/idiom/brief-preflight.py`: `brief lint` already owns deterministic
  implementation-brief lint, including sections, anchors, old_text matching,
  and output-size risk.
- `cmd/idiom/lane.go`: implement and plan-execute lanes already auto-preload a
  compact `context_pack` section unless disabled.
- `cmd/idiom/plan_execute.go` and `internal/workspace/submit_plan.go`:
  plan-execute owns typed `submit_plan`, required-path preflight, pack-id
  handoff, session-scoped plan artifacts, and executor-side atomic preflight.
- `internal/agent/runner.go`: markdown deliverable auto-salvage is scoped to
  mutation lanes with exactly one required `.md` path. It is not an artifact
  command feature.
- `idiom tool harness_audit --input '{"purpose":"behavior"}'`: recent logs still
  show high generic read/search use and incomplete session summaries. This
  supports more audit coverage and packet-first planning, not more prompt text.

## Effort 1: Artifact Mode Contract

Proposal: make `idiom artifact --no-tools` truly draft-only and prevent nested
execution behavior.

North-star alignment: valid, but narrower than originally phrased. `artifact`
should remain a generic drafting surface. Its contract is "one model turn,
optional no-tools enforcement, optional validator." It should not become a
hidden implementation lane, planner, repair loop, or queue producer.

Current-state read: `cmd/idiom/artifact.go` already sets `TaskFamily` to
`direct_question` for `--no-tools`, captures JSONL events, and fails when tool
events occur. There is no direct plan-execute dispatch in that command. The
unexpected `plan-execute:*` queue item observed during Colony work should be
treated as a log-correlation issue until proven otherwise.

What to rework:

- Add artifact telemetry fields for `no_tools`, `fail_on_tools`,
  `tool_use_detected`, `validator_exit`, `answer_hash`, `output_path`, and
  `artifact_contract`.
- Add an explicit artifact contract test that asserts `--no-tools` cannot
  request tools and does not stamp mutation metadata, required paths, or
  plan-execute task family.
- Add audit classification for "artifact run caused tool/queue activity" using
  correlation IDs, so future reports can prove whether the source was artifact,
  a lane wrapper, or a separate queued command.

What to leave alone:

- Do not remove markdown auto-salvage globally. It is a mutation-lane recovery
  path for one required markdown deliverable, not artifact execution. If it is
  too broad, guard it by task family and required-path metadata instead.

Invalid form of the proposal:

- "Disable all salvage/fallback behavior anywhere artifact is mentioned" is too
  broad. Final-answer fallback is a runner behavior; artifact's invariant is
  no tool calls and validator correctness.

## Effort 2: Language-Aware Spec Lint

Proposal: add language-aware lint so GDScript specs do not prescribe C++
doctest patterns, and similar mismatches are caught before dispatch.

North-star alignment: valid if implemented as contract/profile validation, not
project-specific idiom core. The harness can identify language families and
known test idioms; project-specific acceptance wording belongs in a local
profile or validator.

Current-state read: `cmd/idiom/brief-preflight.py` is generic in intent, but it
still carries C++/Colony-shaped assumptions such as `native/tests/`,
`TEST_CASE`, and header/impl/test triplet logic. Those should not become the
basis for GDScript rules.

What to rework:

- Add a profile-aware lint layer, for example `.idiom/brief-lint/profile.json`
  or architect profile gates, with mappings from file extensions/languages to
  allowed test framework markers, forbidden foreign-framework markers, and
  acceptance-command shapes.
- Move hardcoded C++/Colony assumptions out of core lint or express them through
  the default profile as generic source/test heuristics. Core lint can keep
  extension and language-registry inference.
- Add fixture tests with at least:
  - `.gd` edit plus `TEST_CASE` or `CHECK` in acceptance: BLOCK.
  - `.cpp`/`.h` edit plus doctest acceptance: OK.
  - mixed-language brief: requires per-file acceptance mapping or warns when a
    single framework claim covers all files.

What to remove:

- Core wording that says "nearest existing TEST_CASE" as the universal test
  anchor. It should be "nearest existing test case for this language/profile."

Invalid form of the proposal:

- Hardcoding GDScript-specific rules into idiom core is not valid. GDScript is
  one client language; the generic fix is profile-driven language mismatch
  detection.

## Effort 3: Referenced Helper And Command Validation

Proposal: validate referenced helpers, commands, paths, and anchors before a
model-authored atom spec is considered dispatch-ready.

North-star alignment: valid. This is exactly harness-owned evidence. The model
may choose strategy, but the harness should determine whether cited files,
helpers, commands, and anchors exist and are fresh enough to rely on.

Current-state read: `brief lint` already verifies file existence and labeled
`old_text` blocks. `architect plan-audit` already checks candidate plans against
packets for missing file references, unresolved handles, protected paths, stale
context, and validation gaps. The missing piece is a shared reference resolver
for helper names and acceptance commands.

What to add:

- A generic reference-validation component used by both `brief lint` and
  `architect plan-audit`.
- Checks for:
  - workspace paths exist, unless the edit is explicitly new-file shaped;
  - quoted old_text appears exactly once when substring edits are requested;
  - named helper/symbol references resolve through language tools or the
    context graph when the language is supported;
  - acceptance commands reference existing scripts, test binaries, or package
    paths when those can be checked without executing the command;
  - unresolved references produce structured finding categories:
    `missing_path`, `missing_symbol`, `ambiguous_anchor`, `unknown_command`,
    `stale_reference`.

What not to do:

- Do not execute arbitrary acceptance commands during lint. Execution belongs to
  trace/postflight/verification contracts. Lint should be deterministic and
  side-effect free.
- Do not force a new read cap. If validation cannot resolve a claim, report the
  unresolved claim and let the caller decide whether to refresh context, ask a
  question, or narrow the atom.

## Effort 4: Structured Context Packets For Atom Specs

Proposal: use structured context packets instead of prose-only briefs for atom
spec drafting.

North-star alignment: strongly valid and already partially implemented.
`context_pack`, `architect packet --schema planning_v2`, `architect plan`,
`artifact --no-tools`, and `architect plan-audit` form the intended route.

Current-state read: `README.md` already documents the packet-first flow. Lanes
also auto-preload compact `context_pack` sections. The remaining issue is that
project wrappers can bypass these generated artifacts and fall back to manual
prose stitching.

What to add:

- A first-class atom-spec drafting command or documented recipe that produces:
  `context_pack` -> `architect packet` -> `architect plan prompt` ->
  `artifact --no-tools` -> `architect plan-audit`.
- Include explicit packet fields for allowed files, evidence paths, forbidden
  patterns, acceptance commands, freshness, language capabilities, unresolved
  unknowns, and `pack_id`.
- Teach wrappers to pass packet paths and prompt files, not heredoc-expanded
  markdown blobs. This avoids shell quoting failures like Markdown backticks
  being executed by the shell.
- Have plan-audit consume the packet and the model artifact before any human or
  queue dispatch labels the result "ready."

What to remove:

- Duplicate prose-only context sections in project wrappers once they are
  represented in the packet.
- Any wrapper logic that re-renders packet facts by string concatenation when
  idiom can render the prompt from the packet.

Invalid form of the proposal:

- Forcing every artifact through `context_pack` is not valid. Generic
  drafting artifacts can be pure text. Source-backed atom specs should use
  packets because they make claims about a repository.

## Effort 5: Constrained Repair, Not Redrafting

Proposal: when an artifact fails lint, repair only the invalid spans rather than
asking the model to redraft the entire spec.

North-star alignment: valid if repair is bounded by deterministic findings.
The repair loop should be evidence-preserving, not a second strategy pass that
can invent new files, commands, or anchors.

Current-state read: artifact has a validator but no artifact-repair surface.
Runner final-answer repair is for user-facing synthesis and should not be
repurposed as spec repair. Refine has reviewer/patcher phases, but that is a
lane workflow, not a generic artifact postprocessor.

What to add:

- Deterministic sanitizer for mechanical defects:
  - outer code fence wrappers;
  - placeholder tokens such as `<path>`, `<count>`, and bare ellipses in
    required sections;
  - missing trailing newline or marker format issues.
- A constrained `artifact repair` or `architect repair-plan` surface that takes
  the original artifact, validator findings, and packet, then emits either a
  patch/diff or a structured blocker.
- Repair policy:
  - cannot add files outside packet allowlist;
  - cannot add new commands not present in packet/profile unless it marks them
    unresolved;
  - cannot silently fill unknown placeholders with guessed facts;
  - must preserve validated sections and cite finding IDs it addressed.

What to remove:

- Any full redraft-on-failure workflow in wrappers. Redrafting hides whether the
  original model output was close, loses audit trail, and can introduce new
  unsupported claims.

Invalid form of the proposal:

- "Automatically fix placeholders by guessing the missing values" is not valid.
  Unknown placeholders should be blockers unless the value is mechanically
  derivable from the packet.

## Effort 6: Dispatch-Ready Provenance

Proposal: track whether a dispatch-ready atom spec came directly from idiom or
from manual cleanup.

North-star alignment: strongly valid. This belongs in audit/eval, not social
memory. Idiom should not count a manually normalized artifact as a clean model
success.

Current-state read: `artifact` logs start/write phases but not enough outcome
detail. Lane history and audit surfaces have grown, but artifact provenance and
manual-normalization status are not first-class.

What to add:

- Emit an artifact report next to generated artifacts or into telemetry with:
  `artifact_id`, `prompt_hash`, `packet_id`, `model`, `no_tools`,
  `validator`, `validator_exit`, `validator_findings_count`, `output_hash`,
  `repair_source`, `manual_modified_after_model`, and `dispatch_ready`.
- Teach `harness_audit` to aggregate artifact readiness:
  - raw model artifact passed;
  - deterministic repair passed;
  - model repair passed;
  - manual cleanup required;
  - failed validation.
- Have wrappers preserve the raw model artifact and write normalized artifacts
  to a separate path. The ready label should point to both hashes.

What to remove:

- Success messages that imply idiom shipped the artifact when human edits were
  required after generation.

## Wrapper Fixes

Some observed failures were wrapper failures, not idiom core failures. The
generic fix is to reduce wrapper responsibility.

Needed changes:

- Prefer `idiom architect ... --output <file>` and
  `idiom artifact --prompt-file <file> --output <file>` over shell heredocs.
- Do not use `eval`, unquoted command substitution, or unquoted Markdown in
  wrapper-generated shell scripts.
- Keep project-local validators as executable files or direct commands that
  consume `$IDIOM_ARTIFACT`; do not inline large validation programs into a
  shell string.
- When a wrapper must call idiom, pass JSON or files by path. The wrapper should
  not parse and rewrite large markdown sections unless idiom lacks a generic
  surface for that transformation.

## Implementation Order

1. Artifact telemetry and audit correlation.
   - Smallest slice, clarifies whether artifact is actually causing nested
     tool/queue behavior.
   - Tests: `go test ./cmd/idiom ./internal/workspace`.

2. Profile-driven language/spec lint.
   - Move C++/Colony assumptions behind profile hooks.
   - Tests: `go test ./cmd/idiom`, plus Python preflight fixture tests through
     existing Go harness.

3. Reference validator shared by brief lint and plan-audit.
   - Side-effect-free path/symbol/command checks.
   - Tests: `go test ./cmd/idiom ./internal/architect ./internal/workspace`.

4. Packet-first atom-spec command or recipe.
   - Wire the existing context/architect/artifact/plan-audit pieces into one
     documented flow with preserved artifacts and hashes.
   - Tests: CLI smoke with a temp repo fixture.

5. Constrained artifact repair.
   - Deterministic sanitizer first; model patch repair only after report format
     and audit fields exist.
   - Tests: validator-fail fixtures for placeholders, wrong framework, stale
     anchors, and outer fences.

6. Dispatch-ready provenance aggregation.
   - `harness_audit` and lane/artifact history summarize raw vs repaired vs
     manual readiness.
   - Tests: telemetry/audit aggregation fixtures.

## Out-Of-Scope For This Prep

- No new hard read budgets. Recent failures point to weak evidence/readiness
  signals and insufficient closeout contracts, not a need for more arbitrary
  caps. Size limits are valid only when tied to backend or protocol invariants,
  such as submit_plan truncation risk.
- No Colony-specific GDScript or doctest rules in idiom core. Those belong in a
  project profile, while core owns the generic language mismatch mechanism.
- No automatic command execution during spec lint. That would turn a
  deterministic validation step into a side-effecting verification run.
- No removal of plan-execute. The plan/submit/executor split is aligned with
  idiom's contract-first direction. The target is stronger packet and artifact
  quality before the plan reaches the executor.
