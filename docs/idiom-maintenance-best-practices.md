# Idiom Maintenance Best Practices

Use this before changing idiom's harness behavior, prompt layers, preflight
checks, queueing, atom intake, audit, or lane orchestration.

## North Star

The model owns strategy; idiom owns bounded, observable state transitions. When
a repeated failure looks like "the model should have known," first ask whether
idiom could have classified the state deterministically before model dispatch.
Prefer executable contracts over prompt reminders.

## Deterministic Gates

- Classify artifacts before dispatch: `dispatchable`, `source-satisfied`,
  `stale-needs-rebase`, `blocked-dirty-files`, `invalid-scope`,
  `missing-scope`, `complete`, or `failed-refine-draft`.
- Do not send already-complete, source-satisfied, stale, dirty, or invalid
  atoms to Qwen just to let the model discover that fact.
- Treat stale or partially-applied atom specs as tracker/spec maintenance work.
  Rebase the atom before live dispatch.
- Use stale/already-shipped atoms as deterministic regression fixtures, not
  throughput tests for the implementation pipeline.
- If a phase outcome is mechanically knowable, encode it in intake/preflight or
  the phase state machine. Prompt wording is secondary.
- If a phase only needs to generate one bounded scratchpad artifact, prefer an
  artifact-owned path (`idiom artifact --no-tools --output ...`) over asking an
  implementation lane to call `write_file`. Record output/report paths and a
  typed artifact state so no-write failures are diagnosable without transcript
  reconstruction.

## Log-First Debugging

- Start behavior debugging from the compact run ledger, conversation JSONL,
  telemetry JSONL, and `harness_audit`; do not reconstruct the story from
  memory.
- Every run should leave enough structured evidence to answer: artifact/spec
  id, intake state, selected pool, required paths, tool/model calls, token
  usage, final state, writes attempted, files changed, and failure reason.
- If a bug requires hand-reading raw transcripts to diagnose, improve the
  ledger/audit surface as part of the fix.
- Keep logged evidence bounded and privacy-safe: aggregate counts, paths,
  command ids, state names, and session ids are useful; raw prompts, file
  bodies, customer names, secrets, and long transcript excerpts are not.

## Avoid Low-Value Loops

- Stop rerunning the same atom after two harness failures. Write a focused
  unit/integration test against the logged failure shape instead.
- Do not debug backend EOFs, queue behavior, or model discipline on a run that
  should have been preflighted to a terminal non-dispatch state.
- Do not add hard read/write budgets to compensate for unclear phase contracts.
  Fix the contract, artifact shape, or evidence timing.
- Avoid adding the same nudge in prompts, runtime rails, decorators, and CLI
  wrappers unless each layer owns a distinct invariant.
- Treat repeated recovery nudges as evidence that the initial phase contract may
  be weak. Before adding another late message, inspect the first prompt the model
  saw and make sure it frontloads the executable sequence: evidence prerequisite,
  permitted tool action, schema shape, and terminal outcomes.

## Change Locality

- For a phase-contract bug, prefer one typed state transition plus a focused
  gate over scattered prompt text.
- When prompt text is the right fix, change the earliest phase-specific lead-in
  that owns the contract, not only the recovery nudge that catches the failure.
  The lead-in and gate must describe the same state machine in the same order.
- Use one vocabulary for each phase contract across lead-ins, runtime briefs,
  tool schemas, rejections, logs, and UI. In plan-execute, the planner produces
  a valid `submit_plan` artifact, `Decision: source-satisfied`, or a typed
  blocker; the executor implements edits. Do not call planner failure an
  implementation failure unless execution actually started.
- Do not collapse mutation scope, evidence prerequisites, and plan coverage
  into one "required paths" phrase. Prompts and rejections should distinguish
  `required_edit_paths`, source-body obligations, and `submit_plan.edits[]`
  coverage so the model cannot treat a read as a planned edit.
- When a `submit_plan` is rejected, recovery is a contract-correction state.
  Align prose and tool availability: the next model-visible surface should be a
  corrected native `submit_plan`, source-satisfied evidence, or a typed blocker,
  not another broad discovery menu.
- For generated scratchpad bugs, prefer host-owned artifact materialization plus
  validation over another "remember to write the file" prompt.
- For an observability bug, update the event/ledger/audit schema and tests
  before adding another prose diagnostic.
- For an atom/spec-quality bug, fix intake, brief lint, or refine output
  validation before changing implementation-lane behavior.
- Keep project-specific examples out of generic idiom logic. A project atom may
  be the reproducer; the fix must be reusable.

## Review And Validation

- Add regression tests for both sides of a boundary: the bad artifact must be
  blocked, and a valid artifact must remain dispatchable.
- Validate with the narrow package tests first, then `go test ./...` for shared
  runner, CLI, workspace, audit, or prompt-contract changes.
- After changing entrypoints or binary wiring, run `make build`.
- Before calling a fix complete, inspect the diff for duplicated phase
  authority, privacy leaks in logs, stale documentation, and false-positive
  blocking of legitimate dispatchable work.
