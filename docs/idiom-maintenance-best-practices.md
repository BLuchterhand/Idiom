# Idiom Maintenance Best Practices

Use this before changing idiom's harness behavior, prompt layers, preflight
checks, queueing, atom intake, audit, or lane orchestration.

## North Star

The model owns strategy; idiom owns bounded, observable state transitions. When
a repeated failure looks like "the model should have known," first ask whether
idiom could have classified the state deterministically before model dispatch.
Prefer executable contracts over prompt reminders.

For route authority, "executable" means typed. Required coverage paths,
candidate write scope, exact snippets, edit-kind obligations, evidence paths,
and recovery/resume state must be fields on a contract or durable progress
record, not facts rediscovered from generated prompt prose.

When a capable model suddenly appears weak, repetitive, or unable to complete a
task shape it normally handles, treat that as an alignment symptom before
blaming model quality. Inspect the runtime configuration, selected role/model,
thinking settings, token budget, prompt lead-in, visible tool surface, lane
contract, and completion gate as one system. Local models often degrade when a
route silently changes actors, when thinking is disabled for a phase that
depends on synthesis, when prompts ask for one role while gates enforce
another, or when the harness asks the model to serialize an unnatural artifact
instead of using its normal editing capability.

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
- For live TUI tests, keep the TUI attached to a detached PTY (`tmux`, `screen`,
  or an equivalent controller) and treat conversation/telemetry JSONL as the
  primary monitor. Do not poll the raw alternate-screen terminal as the normal
  status channel; spinner redraws and full-screen refreshes consume reviewer
  context without adding evidence. Use bounded, sanitized pane captures only for
  user-visible rendering claims.
- Every run should leave enough structured evidence to answer: artifact/spec
  id, intake state, selected pool, required paths, tool/model calls, token
  usage, final state, writes attempted, files changed, and failure reason.
- If a bug requires hand-reading raw transcripts to diagnose, improve the
  ledger/audit surface as part of the fix.
- Keep logged evidence bounded and privacy-safe: aggregate counts, paths,
  command ids, state names, and session ids are useful; raw prompts, file
  bodies, customer names, secrets, and long transcript excerpts are not.
- For "degraded intelligence" failures, log and compare the selected
  planner/executor/classifier model, role aliases, thinking/preserve-thinking
  settings, max-token override, active fallback state, prompt family, and
  exposed tools. If the model actor or prompt contract changed, fix that
  alignment before adding more recovery nudges.

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
- If a prompt or recovery packet currently acts as authority, extract the
  authority into a typed contract first, then make the prompt describe that
  contract. Prompt text, examples, tool schemas, and context packets may explain
  or localize; they should not be parsed as the source of hard obligations.
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
- Keep candidate scope and coverage separate end to end. Discovered paths,
  context-packet candidates, sibling test/source guesses, and `editable files`
  headings belong in allowed/candidate scope unless a durable artifact
  explicitly made every path mandatory.
- Store edit-kind obligations in the run contract and progress state. A retry
  that requires "at least one test edit" or "at least one implementation-shaped
  edit" must carry that typed obligation through planner retry, executor,
  review-repair, resume, telemetry, and audit.
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

- Every non-trivial behavior fix needs an explicit decision record before it is
  considered complete: the verified root cause, the north-star alignment, the
  reason it does not create a new rabbit hole, and the evidence that similar
  nearby failure modes were checked. Prefer the smallest contract/authority
  correction that removes the cause over another late nudge or budget.
- Run that decision record by an independent reviewer before applying or
  finalizing the fix. The reviewer should challenge whether the diagnosis is
  log/test-backed, whether the harness is enforcing a real invariant rather than
  picking model strategy, whether the change preserves valid lanes and CLI/TUI
  parity, and whether it is likely to cause follow-on compensating fixes.
- Before replacing or reverting non-trivial behavior, inspect the relevant
  commit/blame/symbol history when current intent is ambiguous. History is not
  a veto, but it is evidence: preserve still-valid rationale and call out when
  new logs, tests, or product requirements supersede it.
- Add regression tests for both sides of a boundary: the bad artifact must be
  blocked, and a valid artifact must remain dispatchable.
- Validate with the narrow package tests first, then `go test ./...` for shared
  runner, CLI, workspace, audit, or prompt-contract changes.
- After changing entrypoints or binary wiring, run `make build`.
- Before calling a fix complete, inspect the diff for duplicated phase
  authority, privacy leaks in logs, stale documentation, and false-positive
  blocking of legitimate dispatchable work.
- When committing, include why the change exists, not just what files changed:
  the observed failure or requirement, the contract/design decision, important
  alternatives avoided, and the verification evidence. Commit history should be
  useful enough for a future maintainer to distinguish intentional design from
  accidental churn.
