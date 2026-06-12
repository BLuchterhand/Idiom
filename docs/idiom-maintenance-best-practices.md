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

For human-facing lane activation, "typed" also means explicit. Do not use
keyword, regex, path-shape, or phrase heuristics to move a TUI prompt into
`implement`, `plan-execute`, `verification`, `locate`, `review`, or
`architecture`. Natural language must either be classified by the route
classifier or fall back conservatively to conversation. Essential lane entry
points should be slash commands or other typed invocations, with the chosen
route recorded in the run contract.

For delegated local harness work, "observable" means compact and artifact-led.
The supervising orchestrator should not ingest a worker's full progress stream
unless the stream itself is the subject of the investigation. Start workers with
stdout/stderr redirected to a run log, record the PID and expected artifacts,
and monitor only process liveness or a bounded `status.json` while work is
running. Require workers to leave a compact handoff surface where possible:
`summary.json` for machine-readable facts, `final.md` for human closeout,
`blockers.json` for typed blockers, and the declared changed paths or artifact
paths. On completion, the orchestrator reads those artifacts and checks the diff
or verification evidence. On failure, it reads only blocker artifacts and a
small tail of the run log. This keeps research, implementation, and review
inside the delegated harness while preserving enough evidence to make the next
orchestration decision.

When a capable model suddenly appears weak, repetitive, or unable to complete a
task shape it normally handles, treat that as an alignment symptom before
blaming model quality. Inspect the runtime configuration, selected role/model,
thinking settings, token budget, prompt lead-in, visible tool surface, lane
contract, and completion gate as one system. Local models often degrade when a
route silently changes actors, when thinking is disabled for a phase that
depends on synthesis, when prompts ask for one role while gates enforce
another, or when the harness asks the model to serialize an unnatural artifact
instead of using its normal editing capability.

## Required De-Drift Gate

Drift is cheap. A small sanitizer, phrase matcher, prompt nudge, fallback, or
test assertion can create a second contract beside the real one. Behavior fixes
are not ready for implementation until they have a de-drift analysis that names
the canonical owner and every consumer that can observe the changed state.

Before editing harness behavior, write down:

- **Canonical owner**: the existing module, gate, presenter, or typed contract
  that should own the behavior.
- **Consumer matrix**: whether the affected text/state is model-facing,
  machine-consumed, user-facing, audit-only, or mixed.
- **Similar-surface scan**: every module that can promote, reject, recover,
  compose, accept, hide, expose, or audit the same state. For read-only evidence,
  this includes typed packets, legacy packet parsing, answerability ledgers,
  child promotion, evidence-path collectors, fallback composers, decorators,
  final-answer acceptance, and lane policy.
- **Preserve lanes**: lanes where raw protocol must remain intact for recovery,
  plan execution, review-repair, mutation, or downstream detection.
- **Cleanup lanes**: lanes where the same protocol must be hidden or rewritten
  for user presentation.
- **Existing helper scan**: helpers, gates, tests, prompts, and audit code that
  already handle related behavior.
- **Authority classification**: whether paths or facts are source-body
  authority, accepted inspected evidence, candidate scope, generated diagnostics,
  audit-only metadata, or user-facing citations.
- **Projection parity**: for tool-surface or phase-contract changes, prove that
  model-visible manifests, active allowed tools, hidden tools, recovery cards,
  next-action state, phase-contract events, telemetry, and audit all describe
  the same callable surface.
- **Negative oracle**: at least one valid lane/use case that would regress if
  the fix is too broad.
- **Contradiction scan**: tests or docs that still bless the behavior now being
  forbidden.

For final-answer and protocol-marker changes, the minimum regression set is:

- the bad user-visible leak or malformed output is fixed;
- a normal answer passes through unchanged;
- machine-consumed protocol remains machine-readable in terminal phases;
- a similar-looking but wrong lane is not accepted as sufficient evidence;
- the cleaned output still carries useful evidence or blocker context.

For read-only answerability changes, the minimum regression set is:

- an off-objective child packet, discovery hit, or diagnostic path does not make
  the parent answer-ready;
- an on-objective typed packet with source-body findings is accepted by
  readiness, child promotion, fallback synthesis, and final-answer acceptance
  through the same decision path;
- mentioned or pinned paths remain candidates until accepted by that decision;
- the final answer cites only accepted evidence paths;
- a valid exact follow-up read remains allowed when the prior evidence is
  insufficient, and any post-readiness recovery explains the missing claim
  contract rather than blocking exploration generically.

For tool-surface changes, the minimum regression set is:

- a hidden tool is absent from the model manifest, `allowed_tools`,
  `callable_tool_names`, recovery cards, and next-action state;
- a valid release or phase transition restores the tool in every projection;
- audit catches an observed call to a tool that any projection still claims is
  hidden or absent;
- no caller has to remember a manual rebuild after mutating hidden/allowed tool
  state.

Use reviewers as separate checks, not as a rubber stamp. For non-trivial
behavior changes, request three lenses after the patch and again after the last
material revision: canonical owner/duplicate authority, blast radius across
lanes and consumers, and test-oracle quality. A review that only says the
current scenario passes is incomplete.

## Idiom-Offloaded Work Loop

Use idiom as the default worker for idiom maintenance. The expected loop is:
idiom researches and debugs through the real binary, CLI tools, conversation
logs, telemetry, and context graph; the external operator synthesizes and
verifies the approach from that evidence; then idiom applies actual repository
changes through `plan-execute` unless the current bug prevents idiom from doing
so. Prefer the TUI/live operator path when validating user-facing behavior, but
observe it through conversation/telemetry logs first and poll slowly with small
bounded reads.

If a run exposes an idiom bug, keep investigating and resolving it inside idiom
when possible. Treat the failed idiom run as primary evidence: preserve the
session id, recovery/failure record, stop reason, and tool sequence. If the bug
blocks idiom from producing or executing a plan, document that blocker and use
the smallest external fallback; do not normalize bypassing idiom as the default
path.

Behavior fixes need scripted e2e regression coverage in addition to focused
unit tests. The e2e should exercise the real CLI/TUI contract that failed and
assert acceptable, grounded, non-garbage output.

For retrospective or cleanup work, treat the existing harness as live product
behavior until evidence proves otherwise. A large, noisy commit may contain
churn, but deletion is still a behavior change. Review first, then remove only
the pieces whose original failure no longer applies or whose dependent
live/scripted contracts are covered by a better boundary.

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
- Treat read-only readiness as advisory evidence state, not a permission
  boundary. After answer readiness or delegated packet closure, the model should
  be nudged toward an answer, but a further read/search request should still
  flow through normal lane/tool permissions unless an explicit lane contract,
  protected path, configured tool rule, verification state, or loop invariant
  forbids it. Do not require generic `named_gap` / `why_not_answering` fields or
  synthesize host-owned closeout merely because readiness was observed.
- Do not convert read-only readiness into a forced no-tools/model-answer turn.
  If the model asks for more source, data, or diagnostics, the harness should
  satisfy that request unless a concrete invariant rejects it. A repeated,
  off-objective, unavailable, or forbidden request may be rejected, but the
  rejection must explain that invariant and leave the model a valid strategy
  choice: a different bounded probe, an answer from accepted evidence, or a
  precise blocker.
- If the model appears to have useful prefetch, context-pack, child-packet,
  telemetry, or recovery-state information but the run reports "not enough
  evidence," treat that as an evidence-registration bug first. Repair how the
  harness classifies and projects that evidence; do not force synthesis as a
  shortcut around missing registration.
- If a phase only needs to generate one bounded scratchpad artifact, prefer an
  artifact-owned path (`idiom artifact --no-tools --output ...`) over asking an
  implementation lane to call `write_file`. Record output/report paths and a
  typed artifact state so no-write failures are diagnosable without transcript
  reconstruction.

## Log-First Debugging

- Start behavior debugging from the compact run ledger, conversation JSONL,
  telemetry JSONL, and `harness_audit`; these logs are the source of truth for
  what the harness and model actually did. Do not reconstruct the story from
  memory, from a previous assistant summary, or from the user's paraphrase when
  local logs exist.
- Before proposing a behavior fix, write down the exact session/log paths,
  stop reason, rejected-action counts, completion-gate/recovery events, and
  tool sequence that prove the failure class. If any of those are missing,
  continue log inspection before patching prompts, gates, or scoring.
- Check the newest failure against prior similar logs and audit pattern counts.
  Treat repeated signatures as system behavior until logs prove they are an
  isolated target-repo defect.
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
- For lane or phase state-transition bugs, treat model runtime state as a
  first-class contract field. Check and log the before/after model alias,
  backend, tool transport, parser contract, `temperature`, `top_p`, `top_k`,
  `min_p`, penalties, thinking policy, max tokens, response format, tool
  choice, and exposed tools for the exact turn where behavior changed. Do not
  debug hallucinated paths, malformed tool calls, missing visible actions, or
  repeated recovery loops without first confirming these fields match the
  intended lane state.

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
- Treat repeated searches as an unmet-evidence signal before treating them as
  disobedience. After a candidate-producing search, the next phase surface must
  reconcile with the open evidence obligation: classify returned paths as
  source, test, doc, fixture, generated log, config, or directory; surface
  concrete source-body read/capability affordances for real source candidates;
  and state explicitly when the hits are only noisy diagnostics. A generic
  `read/search` menu plus prose like "read the highest-confidence hit" is not
  an executable contract.
- Unify repeated-fingerprint recovery before adjacent replay and UI doom-loop
  approval. Replaying prior evidence once is useful; after that, the same
  normalized request should enter one typed terminal/recovery state that owns
  the next visible surface and telemetry. Do not let separate counters,
  replay-once paths, and TUI approval hooks compete over the same repeat.
- Do not start cleanup with a broad revert. First make a retention table with:
  changed area, original failure or obligation, evidence that the obligation
  still exists, tests/logs that exercise it, keep/remove decision, and focused
  verification command. Run the smallest live/scripted contract for a removal
  before widening to suite-level validation.
- Treat rollback failures as first-class evidence. If a proposed cleanup causes
  live-readiness, TUI, or scripted-suite regressions and restoring the removed
  behavior makes them pass, the cleanup path is the regression. Stop there and
  either keep the behavior or replace it with a proven equivalent boundary.
- Treat migrated failures as first-class churn evidence. If a fix reduces a
  counter, changes the failed assertion, or moves failure from tool count to
  answer content while the original user intent still fails, do not call the
  system stable. Name which projection improved, which projection still fails,
  and whether the shared contract needs consolidation before the next patch.

## Change Locality

- For a phase-contract bug, prefer one typed state transition plus a focused
  gate over scattered prompt text.
- For post-readiness bugs, fix the shared terminal-action gate first. Avoid
  piling up counters, duplicate host closeout calls, or individual tool
  allowances around the runner loop unless they hang off that one typed
  readiness state.
- For read-only evidence bugs, fix the shared answerability boundary first.
  Avoid adding objective phrase checks, path collectors, fallback exceptions, or
  answer-shape filters in individual consumers unless those consumers delegate
  to one typed evidence decision. Candidate paths and source-body evidence must
  remain distinct through readiness, parent/child handoff, final-answer
  composition, telemetry, and audit.
- For tool-surface bugs, fix the shared turn surface first. Hidden tools,
  active allowed tools, callable tool names, textual tool menus, recovery cards,
  next-action state, and audit events should be projections of one object or
  state transition. A local map mutation plus scattered rebuild calls is a
  churn risk.
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
- Keep mutation scope and bounded dependency evidence scope distinct. A
  required edit path is the write allowlist; semantic read-only discovery may
  inspect the immediate package/directory that owns an explicit required or
  evidence file when the model names an exact edit-safety fact. Do not widen
  raw file reads, broad search, shell, or write permissions to satisfy that
  dependency lookup.
- Treat `write_file` as a whole-file replacement primitive, not a general patch
  primitive. In scoped implementation lanes, large existing source/test files
  should be edited through `apply_patch` or bounded `edit_file` calls so the
  harness preserves source truth without inviting accidental file truncation.
  Keep `write_file` available for new files, small files, and non-source
  deliverables where whole-file authorship is the actual contract.
- Do not fix local-Qwen write failures by toggling thinking alone. First check
  whether the backend's transcript matches its tool mode. Text-tool backends
  must see prior tool actions as visible assistant text plus textual tool
  results; synthetic native `tool_calls` create empty assistant turns in MLX
  history and train the next turn to stay empty. Once the transcript is
  text-native, terminal parser contracts such as `submit_plan` may use scoped
  no-thinking to force visible output instead of hidden reasoning.
- Keep candidate scope and coverage separate end to end. Discovered paths,
  context-packet candidates, sibling test/source guesses, and `editable files`
  headings belong in allowed/candidate scope unless a durable artifact
  explicitly made every path mandatory.
- Treat explicit lane requests as operator intent only when the surrounding
  scope can satisfy that lane's contract. For example, an explicit
  `plan-execute lane` request with multi-file source/test required paths should
  use the planner/executor split; casual wording for a single-file edit should
  not become a plan-execute contract by itself.
- Store edit-kind obligations in the run contract and progress state. A retry
  that requires "at least one test edit" or "at least one implementation-shaped
  edit" must carry that typed obligation through planner retry, executor,
  review-repair, resume, telemetry, and audit.
- When a `submit_plan` is rejected, recovery is a contract-correction state.
  Align prose and tool availability: the next model-visible surface should be a
  corrected native `submit_plan`, source-satisfied evidence, or a typed blocker,
  not another broad discovery menu.
- When malformed planner output contains useful edit intent, route only exact
  JSON or exact fenced `idiom.edit_intent.v1` through the deterministic
  compiler. The compiler emits candidate `workspace.PlanEdit` values, not
  executable authority; the normal `submit_plan` validation/preflight path
  remains the execution boundary. The `artifact_distiller` role is no-tool,
  no-write, and must never emit `submit_plan` directly.
- Apply the same acceptance parity to read-only tool salvage. A visible or
  reasoning-text `ToolRequest` parsed from malformed output is not dispatchable
  until it has passed the active phase validator, tool-specific enum validation,
  target-shape checks, post-readiness action-class policy, and recovery budget.
  Non-mutating discovery or audit tools can still consume context and block
  final answers, so they do not get a softer path than ordinary read tools.
- Host-owned closeout presenters must cite only ledger-backed evidence. Final
  evidence paths should come from typed evidence packets, exact source-body
  reads, direct source probes, or explicitly accepted inspected paths. Broad
  mentioned-path scans are useful for diagnostics and candidate lists, but they
  must not populate final closeout citations once the host is synthesizing the
  answer.
- For generated scratchpad bugs, prefer host-owned artifact materialization plus
  validation over another "remember to write the file" prompt.
- For an observability bug, update the event/ledger/audit schema and tests
  before adding another prose diagnostic.
- For runtime-state bugs, make the state transition observable before changing
  prompts. Telemetry and phase-contract events should show the effective model
  alias, sampling settings, thinking policy, token budget, parser contract,
  response format, tool choice, and exposed tools after every lane/phase
  transition that changes them.
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
- Select reviewers from [Debugging Reviewer Bench](debugging-reviewer-bench.md).
  The economical default is Incident Root-Cause Reviewer plus one specialist
  matched to the failure signature. Add a second specialist only when the
  evidence spans two concrete domains. Add North-Star / Architecture review when
  a fix changes shared harness behavior, lane contracts, gate authority, or
  follows repeated related live-run failures.
- Before replacing or reverting non-trivial behavior, inspect the relevant
  commit/blame/symbol history when current intent is ambiguous. History is not
  a veto, but it is evidence: preserve still-valid rationale and call out when
  new logs, tests, or product requirements supersede it.
- For "review the previous commit" work, include the retention table in the
  review notes or commit body. Future agents should not have to infer whether a
  removed hunk was deliberate cleanup, an accidental rollback, or a failed
  experiment.
- Add regression tests for both sides of a boundary: the bad artifact must be
  blocked, and a valid artifact must remain dispatchable.
- For answer-quality scripted regressions, require a positive semantic oracle.
  The test should fail if the model answers from an unrelated path, even when
  the answer avoids forbidden boilerplate. Align four things before trusting the
  result: fixture content, scripted model tool calls, required evidence paths,
  and final-answer assertions.
- For multi-turn TUI scripted regressions, place done markers only in the
  intended terminal response. If the marker appears in an intermediate answer,
  the runner can stop early and turn a multi-turn regression into a misleading
  one-turn success or failure.
- When debugging approval surprises, classify the event before changing policy:
  permission approval, route confirmation, and user-facing lane confirmation
  have different owners and different oracles. Use the log role/event name as
  the authority, not the user-visible phrasing alone.
- Treat generated logs and golden-like eval outputs as source changes only when
  the suite contract calls for an intentional fixture refresh. A fixture refresh
  needs the old/new behavior being blessed, the oracle rationale, and a passing
  focused eval or live-contract run after the artifact update. Failed-run
  generated logs are diagnostic evidence, not commit material.
- Validate with the narrow package tests first, then `go test ./...` for shared
  runner, CLI, workspace, audit, or prompt-contract changes.
- After changing entrypoints or binary wiring, run `make build`.
- Before calling a fix complete, inspect the diff for duplicated phase
  authority, privacy leaks in logs, stale documentation, and false-positive
  blocking of legitimate dispatchable work.
- Before calling an answerability or evidence-path fix complete, confirm every
  sibling promotion, composition, fallback, acceptance, telemetry, and audit
  surface delegates to the same decision or is intentionally isolated as an
  adapter.
- Before calling a tool-surface fix complete, confirm model manifests, active
  allowed tools, hidden tools, callable tool names, recovery cards,
  next-action state, phase-contract events, telemetry, and audit all project the
  same turn surface.
- If a live/scripted failure moves rather than disappears, classify the migrated
  failure before patching again. Name the projection that improved and the
  projection that still violates the original user intent.
- When committing, include why the change exists, not just what files changed:
  the observed failure or requirement, the contract/design decision, important
  alternatives avoided, and the verification evidence. Commit history should be
  useful enough for a future maintainer to distinguish intentional design from
  accidental churn.
