# Common Maintenance Traps

Use this when changing Idiom prompts, control rails, lane orchestration,
context tools, queueing, audits, or generated atom/spec workflows. These are
failure patterns that have cost time in real Idiom/Colony runs.

## Prompt And Authority Traps

- **Conflicting terminal states**: do not let an early prompt allow one closeout
  while a later postamble narrows completion to another. Late prompt layers tend
  to win. Example: plan-execute must list all terminal outcomes together:
  successful `submit_plan`, accepted `Decision: source-satisfied`, or
  `<<QUESTION>>:` blocker.
- **Literal final-action wording**: avoid saying the model's final action must
  be a write when another contract also requires lane sanity or a final summary.
  Say "successful writes before closeout" and let the runtime contract own the
  next required state.
- **Duplicate authority**: one obligation should have one canonical owner.
  Stable behavior belongs in prompts; permissions, required paths, tool
  availability, completion gates, stale/source-satisfied classification, and
  verification requirements belong in typed runtime contracts or gates.
- **Prompt text parsed as authority**: generated prompt layers, examples, tool
  schema prose, context packets, transcript excerpts, and recovery nudges are
  evidence or explanation. They must not become hard scope/coverage authority
  unless the lane compiler turns them into typed contract fields first.
- **Prompt reminders as contracts**: if a repeated failure is mechanically
  knowable, fix preflight, route metadata, runtime brief, tool schema, or a
  deterministic gate instead of adding another reminder.
- **Recovery nudges hiding a weak lead-in**: if the same recovery message fires
  repeatedly, do not only strengthen the recovery text. First check whether the
  phase's initial prompt tells the model, in execution order, what state it is
  in, which evidence must be gathered before closeout, which exact tool/schema
  shape satisfies the contract, and which terminal outcomes are valid. Nudges
  should recover from drift, not teach the basic protocol after failure.
- **Generated scratchpads as implementation lanes**: if a phase needs one
  generated markdown artifact and no workspace inspection, use `idiom artifact`
  or an equivalent host-materialized artifact path. Do not ask the model to
  create that scratchpad via `write_file` when the harness can own the write.
- **Magic phrases in briefs**: do not make callers sprinkle prose strings to
  select harness behavior. Use command modes, route metadata, typed artifacts,
  or runtime brief fields.
- **Project-specific repairs in generic prompts**: a project run can be the
  reproducer, but reusable Idiom fixes must be generic and evidence-shaped.
- **Mistaking harness misalignment for weak model capability**: assume the
  model may be capable until the logs prove otherwise. If a model that usually
  handles multi-file edits starts emitting shallow prose, malformed structured
  artifacts, missing paths, or repeated contract failures, inspect the selected
  model/role, thinking settings, token budget, prompt lead-in, visible tool
  surface, lane contract, and completion gate before adding reminders. A route
  or prompt mismatch can make a strong model look unintelligent.
- **Unlogged runtime-state drift**: temperature, sampling, thinking policy,
  token budget, parser contract, response format, tool transport, and exposed
  tools are part of the lane contract. When behavior changes at a route, lane,
  phase, recovery, or fallback transition, verify the effective before/after
  runtime state from telemetry or phase-contract events before blaming the
  model or prompt.

## Plan-Execute And Implementation Traps

- **Placeholder edits**: never submit or make a whitespace/no-op/verification
  edit just because the contract is write-shaped. If source already satisfies
  the task and the lane allows it, close as source-satisfied with per-path
  evidence. Otherwise return a typed blocker.
- **Planner/executor tool confusion**: plan-execute planner phases use
  `submit_plan`; implement phases use write tools. Do not blur these surfaces
  in prompts, recovery nudges, or examples.
- **Planner/executor actor confusion**: the planner does not implement or
  modify files. It emits one successful plan artifact, closes as
  source-satisfied, or reports a typed blocker. UI/log/recovery text should say
  "failed to produce a valid `submit_plan`" rather than "failed to implement"
  when the planner never reached the executor.
- **Semantic editing forced into brittle serialization**: exact-edit
  `submit_plan` is a good fit for exact anchors, full-body packets, and
  deterministic patch handoff. It is a poor fit for open-ended semantic
  multi-file implementation unless the brief already contains executable edit
  bodies. When that mismatch appears, prefer changing the actor boundary or
  lane contract over asking the model to serialize larger JSON patches more
  carefully.
- **Overloaded required-path language**: keep `required_edit_paths`,
  source-body obligations, and `submit_plan` coverage distinct. Required paths
  authorize mutation scope; source-body obligations prove current code; plan
  coverage means every required path has a valid edit entry or terminal
  source-satisfied evidence. A read path is not automatically a planned edit.
- **Candidate scope promoted to hard obligations**: discovered paths, context
  packet candidates, sibling source/test guesses, and recovery scopes are
  candidate authority. Put them in `allowed_edit_paths`, not
  `required_edit_paths`, unless a human or durable artifact explicitly made
  every path mandatory. Encode "must include at least one test edit" or "must
  include at least one non-test implementation edit" as typed edit-set
  obligations, not by forcing a guessed file into required coverage.
- **Lost typed obligations on retry/resume**: if recovery adds an edit-set
  obligation, store it in the scope contract and progress state. Review-repair,
  resume, UI retry, and audit should read the typed field, not re-infer the
  obligation from the original prompt or latest recovery prose.
- **Vague source evidence**: do not say a path has "evidence" when only an
  outline or search hit exists. Path/packet evidence scopes work, outline/search
  localizes anchors, and only source-body reads satisfy source-body obligations.
- **Rejected plan as consumed plan**: a rejected `submit_plan` is an invalid
  attempt, not the single successful plan artifact. Correction recovery should
  force one parser-readable corrected `submit_plan` request, or a typed blocker
  when that exact correction is impossible.
- **Stale anchors**: an `old_text` miss means the file is the authority. Refresh
  the exact anchor with semantic/context evidence or one narrow required-path
  read, then retry or block. Do not invent a near match.
- **Anchored replacement false-stale**: a `new_text` block can legitimately
  share most lines with current source when its paired `old_text` still matches
  exactly, especially for insertions. Treat that as dispatchable work, not as a
  partially applied stale artifact. Only classify the `new_text` near-match as
  stale when the paired old/current anchor no longer matches or no anchor was
  provided.
- **Non-unique anchors**: repeated `old_text` usually indicates duplicate or
  underspecified logic. Extend the anchor or use a line-range/whole-file shape;
  do not rely on first-match replacement when semantics require a specific site.
- **Source-satisfied dispatch**: already-complete or source-satisfied artifacts
  should be classified before spending model tokens. If such a run is useful,
  treat it as a regression fixture for the gate, not normal throughput.
- **Evidence paths as write scope**: command examples, freshness probes, context
  packets, audit output, and transcript excerpts are evidence, not edit
  authority. Required edit paths must come from explicit allowlist headings or
  structured top-level allowlist fields; broad mentioned-path scans may populate
  target/evidence refs but must not authorize writes.
- **Review that cannot act**: a review pass should verify behavior, contracts,
  scope, evidence, and test coverage, then either repair within its authority or
  produce a typed blocker. A pure "looks good" review is weak for local models.
- **Contradictory review passes**: do not accept `Decision: pass` when the same
  answer says required implementation, behavior, evidence, or checklist items
  are missing or failed. That contradiction is a contract failure; the review
  must repair in scope or return a blocker.

## Evidence And Context Traps

- **Broad reads before context tools**: start with `context_pack`, language
  navigation, or declared project discovery when localizing a change. Raw
  search/read is a fallback for literal strings, exact ranges, or unsupported
  questions.
- **Evidence not available at the right time**: plans, implementations, and
  reviews should receive the context packet, required paths, freshness state,
  current-state anchors, and audit/log evidence when those facts are needed.
  Do not rely on the model to rediscover facts the harness already knows.
- **Stale context treated as truth**: context graph output is evidence with
  freshness/confidence, not a live watcher. Refresh or rebuild the smallest
  relevant scope when freshness is stale or the referenced path changed.
- **Read caps and forced budgets as band-aids**: hard caps can hide the real
  issue: unclear readiness, missing evidence, weak closeout nudges, or tool
  surfaces that keep offering discovery after the answer is known. Prefer
  readiness contracts and targeted recovery.
- **Readiness mistaken for permission denial**: `read_only_answer_ready` or
  delegated packet closure is advisory evidence state, not authority to deny
  more source reads. If the model asks for another read/search, honor it through
  the normal lane/tool permission policy unless an explicit lane contract,
  protected path, configured tool rule, verification state, or loop invariant
  forbids it. A read request after readiness usually means the prior evidence
  was not sufficient for synthesis; do not force host closeout or require
  `named_gap` / `why_not_answering` fields as a generic read gate.
- **Tool results that prescribe strategy**: evidence tools should report facts,
  paths, symbols, counts, uncertainty, and provenance. Strategy guidance belongs
  in the runtime contract or a deterministic rejection message.
- **Looping as disobedience instead of unmet need**: when the model repeatedly
  asks for the same kind of evidence, assume the request is meaningful until
  logs prove otherwise. First check whether the tool result failed to return the
  shape, anchors, source body, handles, or confidence the model reasonably
  expected, or whether the prompt/tool schema promised an affordance the command
  does not actually satisfy. Fix the command, packet, schema, or expectation
  before adding another nudge that pushes the model away from what it is trying
  to inspect.
- **Generic surface after specific evidence need**: after discovery returns
  candidates, do not keep presenting the same generic `read/search` menu while
  prose says "read the highest-confidence hit." The active phase must reconcile
  the visible surface with the open evidence obligation: identify source-like
  candidates, expose concrete source-body read/capability affordances, and
  state when hits are only docs, fixtures, generated logs, or diagnostics. If
  the model repeats a search, first assume the returned candidates did not
  satisfy the needed evidence shape; fix ranking, candidate typing, or
  model-facing affordances before adding a loop guard.
- **Classifier answers overridden by keyword lanes**: if the route classifier
  emits a coherent read-only/direct-answer route, do not promote it to a
  special lane with keyword heuristics such as subsystem names, feature names,
  or common debugging nouns. Those overrides are brittle authority leaks. If a
  route is wrong, improve the classifier contract, typed outcome fields, or
  eval evidence instead of hardcoding a lane switch after classification.
- **Redirecting literal search away from the requested evidence**: a
  `search_content` request for a literal term is often exactly what the model
  needs. Do not convert literal text search into a graph/context query merely
  because a structured tool exists. Structured tools are valuable when the
  request is symbol-shaped or the model asks for relationships; literal search
  should return literal hits, especially when the model is narrowing from a
  broad or noisy result set.
- **Salvaged read-only tools bypassing validation**: permissive parsing of
  visible or reasoning-text tool requests is intake, not authority. A salvaged
  `ToolRequest` must pass the same active phase, capability, enum, target-shape,
  readiness, and recovery-budget validator as a normal tool call. Do not let a
  non-mutating tool such as `discovery_query`, `context_*`, or `harness_audit`
  skip normalization merely because it cannot edit files; broad read-only calls
  can still consume budget, reset recovery, and prevent final answers.
- **Generated artifacts mistaken for source bodies**: `.idiom/` context views,
  discovery query JSON, generated live/eval logs, top-level logs, and top-level
  transcripts are diagnostic artifacts. They can explain what happened in a run,
  but they must not satisfy source-body evidence, final-answer citations, or
  readiness for codebase behavior. Preserve real checked-in source packages such
  as `internal/transcript/` and explicit fixture reads when the fixture itself
  is in scope.
- **Mentioned paths promoted to closeout evidence**: host-owned read-only
  closeout evidence is final-answer citation authority. It must come from typed
  evidence packets, exact source-body reads, direct probes, or explicitly
  accepted inspected paths. Broad mentioned-path scans across summaries, errors,
  context packets, `.idiom/` artifacts, or generated logs may suggest candidates
  or diagnostics, but they must not ride along in the final evidence path list.
- **Closest-path hints without recovery**: when `read_file` can prove exactly
  one same-directory existing file has the same normalized basename as the
  requested missing path, the tool should satisfy the model by reading that
  file and naming the recovery in the summary. If the closest match is
  ambiguous, in another directory, or has a different extension, keep the error
  and suggestions visible rather than guessing.
- **Readiness after one noisy hit**: one source-body read is not enough to close
  a read-only answer when the immediately preceding discovery was a broad,
  high-cardinality search. Treat that state as partially grounded: the model has
  a real anchor, but it may still need to narrow to the source body that answers
  the actual question.
- **Small patches after repeated failures**: when two or more fixes in the same
  subsystem add guards, string filters, prompt reminders, eval exceptions, or
  answer-shape cleanup, stop treating the next failure as local. Reconstruct the
  first broken invariant, identify the owning boundary, and consolidate the
  behavior into one typed contract or abstraction before adding another patch.
- **Passing the assertion instead of fixing the invariant**: a live/eval
  expectation is evidence, not the root cause. Do not update an oracle, add a
  topic-specific path, or suppress an error counter until the reusable failure
  class is named and reviewer-validated. A correct fix should explain what
  future bug shape is now impossible.
- **Weak oracle that allows garbage**: absence-only assertions can let a test
  pass while the answer is irrelevant. Every scripted regression for answer
  quality should include positive semantic assertions that prove the answer
  addressed the user's actual question, plus negative assertions only for known
  bad fallback shapes.
- **Fixture mismatch**: a fixture that does not contain the concept under test
  invites irrelevant evidence and misleading success. For answer-quality,
  routing, or evidence regressions, the fixture, scripted tool calls, required
  evidence paths, and final-answer oracle must all point at the same domain
  concept. Do not reuse the frozen label fixture for unrelated policy,
  architecture, or evidence questions just because it is convenient.
- **Bespoke logic replacing a missing contract**: repeated local branches,
  string matchers, lane-specific helpers, prompt snippets, or eval-only
  exceptions are usually evidence that the shared invariant has no owner. Stop
  before adding another special case. Name the common behavior, decide whether
  it belongs in a typed contract, evidence ledger, tool adapter, gate, scorer,
  or presenter, then implement it once behind that boundary. If a special case
  is genuinely required, make the boundary explicit, keep it isolated at the
  adapter edge, add tests proving it does not shadow the generic path, and get
  reviewer agreement before landing it.
- **Answer-shape cleanup as evidence grounding**: filtering phrases like
  "retained source context" or "What this establishes" can make a transcript
  look better while leaving readiness and citation authority broken. Fix the
  evidence ledger, source-body contract, and final-answer critic before adding
  presentation cleanup.

## Audit, Logs, And Feedback Traps

- **Diagnosing from memory**: for behavior bugs, lane history, conversation
  JSONL, telemetry JSONL, and `harness_audit` are the source of truth. Start
  there, cite the exact log/session ids, stop reason, rejected-action counts,
  completion-gate/recovery events, and tool sequence, then patch. If the cause
  cannot be seen without hand-reconstructing prose, improve structured
  logs/audit as part of the fix.
- **Treating the latest run as isolated**: compare the newest failure with
  prior similar logs before classifying root cause. If the same signature
  appears across attempts, fix the reusable harness contract or audit/scorer
  gap rather than adding a one-off prompt nudge.
- **Committing failed generated logs as fixtures**: generated live/eval logs,
  score outputs, and golden-like artifacts are churn by default after a failing
  run. Do not commit them, cite them as passing proof, or bless oracle changes
  unless the suite contract explicitly expects a fixture refresh, the reusable
  behavior change has been named, and the focused suite passes after the
  artifact update.
- **TUI completion marker on the wrong turn**: multi-turn scripted TUI tests
  use completion markers as terminal state. Put the marker only in the intended
  final assistant response; otherwise the watchdog can stop before later turns
  run and the suite may report a misleading one-turn result.
- **Approval terms collapsed together**: permission approvals, route
  confirmations, and user-facing lane confirmations are different contracts.
  When debugging "approval" surprises, inspect the log role/event first
  (`permission_decision`, `route_confirmation`, assistant confirmation text,
  etc.) and name which contract fired before changing either policy or oracle.
- **Skipping model-state checks**: for hallucinated paths, malformed tool JSON,
  missing visible tool calls, repeated `submit_plan` rejection loops, or
  suddenly weak local-model behavior, explicitly check the logged model alias,
  backend, tool transport, parser contract, `temperature`, `top_p`, `top_k`,
  `min_p`, penalties, thinking policy, max tokens, response format, tool
  choice, and visible tool surface at the failure turn and the preceding state
  transition.
- **Raw TUI PTY polling**: live TUI testing should exercise the real alternate
  screen, but repeated PTY reads can dump spinner redraw frames into the agent
  context. Run the TUI in a detached PTY such as `tmux`, send input through the
  PTY controller, and monitor conversation/telemetry JSONL first. Capture only
  small sanitized pane snippets when UI state itself is the claim under test.
- **Missing final-state telemetry**: terminal outcomes such as source-satisfied,
  stale, failed-refine, dirty-scope, no-edit-attempt, and verification-blocked
  should be typed events or ledger states, not only words in the transcript.
- **Missing generated artifacts**: artifact-owned phases should report the
  output path, report path, and typed state (`ready`, `missing`, `trivial`) in
  durable metrics. A missing file is a contract outcome, not a normal model
  success.
- **No regression fixture**: every harness behavior fix should include a test
  for the bad behavior and at least one valid case that remains allowed.
- **Privacy leaks in audit**: logs and audit outputs should keep bounded counts,
  state names, paths, session ids, and command ids. Avoid raw prompts, long
  transcript excerpts, file bodies, secrets, or customer-identifying strings in
  durable artifacts.
- **Feedback as product bug bucket**: use `harness_feedback` for reusable Idiom
  harness/tool/prompt/context gaps, not for ordinary target-repo defects.

## Process Traps

- **Bypassing idiom after an idiom-shaped failure**: when the failure is in
  idiom behavior, route, tools, TUI, or plan-execute, do the research and fix
  through idiom first. Only fall back externally when idiom itself cannot run,
  cannot produce the required plan/tool call, or would corrupt scoped dirty
  work. Record the idiom session/failure id and the exact blocker before using
  the fallback.
- **Manual implementation before idiom synthesis**: the default loop is idiom
  researches/debugs and returns evidence, the operator synthesizes and verifies
  the intended approach, then idiom implements through `plan-execute`. Skipping
  straight to external edits loses the audit trail the harness is supposed to
  provide.
- **Over-polling live idiom runs**: slow down CLI/TUI polling. Prefer
  conversation JSONL, telemetry JSONL, progress state, and recovery records over
  raw terminal panes; when terminal state matters, read tiny bounded snippets
  rather than dumping full alternate-screen redraws into context.
- **Rationale-free commits**: a commit subject that says what changed but not
  why is weak repo memory. For non-trivial changes, include the failure,
  requirement, or design pressure that made the change necessary; the chosen
  contract or tradeoff; meaningful alternatives avoided; and verification. Do
  not make future agents reconstruct intent from the diff alone.
- **Churn without history**: before undoing or reshaping established behavior,
  check focused git history, blame, or symbol history when the existing design
  is not self-explanatory. If the old rationale still applies, preserve it or
  explain why the new evidence supersedes it.
- **Broad rollback before retention proof**: "review the previous commit" does
  not mean broad-revert first. Build a retention table before changing files:
  changed area, failure it addressed, current tests/logs that depend on it,
  keep/remove decision, and verification target. Treat deletion of existing
  harness behavior as a behavior change that needs focused live evidence before
  it lands.
- **Treating rollback failure as noise**: if removing prior behavior causes a
  live or scripted suite regression and restoring it makes the suite pass, the
  rollback is the regression. Record that evidence and stop the cleanup path
  rather than adding compensating patches around the removal.
- **Changing prompts without gates**: prompt cleanup is useful, but a contract
  invariant needs a deterministic backstop and tests.
- **Changing gates without prompt parity**: the model should see the same
  contract the harness enforces. If a gate rejects an action, the prompt or
  runtime brief should make the valid alternatives visible.
- **Skipping the root-cause checkpoint**: before editing for any bug, write down
  the first broken invariant, owner boundary, proving evidence, tempting smaller
  patch, and reason the chosen fix is generic. If any item is unknown, gather
  more evidence or ask reviewers before patching.
- **Fixing only the late symptom**: completion gates and recovery controllers
  are often where a bug becomes visible, but the cause may be an earlier prompt
  that sounded advisory, omitted the executable action sequence, or contradicted
  the gate's evidence ordering. Root-cause from the first prompt the model saw,
  then keep deterministic recovery as the backstop.
- **Unreviewed root-cause claims**: a behavior fix is incomplete until an
  independent reviewer has challenged the diagnosis, north-star alignment, and
  follow-on risk. Do not land a patch solely because it fixes the latest live
  run; record why it removes the cause without opening a new compensating-fix
  chain. Use the reviewer bench in `docs/debugging-reviewer-bench.md`: default
  to Incident Root-Cause Reviewer plus one specialist, add a second specialist
  only when evidence spans two concrete domains, and add North-Star /
  Architecture review for broad or repeated harness failures.
- **Rerunning the same failed atom repeatedly**: after two harness-shaped
  failures, write a focused regression test from the logged failure shape before
  more live runs.
- **Canonical binary drift**: after entrypoint or embedded-prompt changes, run
  the build path that refreshes the single canonical `idiom` binary used by
  callers. Do not validate against a stale debug copy.
- **Dirty scoped files**: do not dispatch atoms against dirty required paths
  unless the dirty state is intentional and represented in the artifact. Dirty
  scope should be a preflight state, not a surprise during execution.

## Quick Review Checklist

Before calling an Idiom harness change complete, check:

1. Does one prompt layer contradict another, especially in the last postamble or
   recovery nudge the model sees?
2. Does the first phase-specific lead-in state the executable protocol before
   the model can drift into prose or planning narration?
3. Is each invariant enforced by the harness rather than only by prose?
4. Are all valid terminal states visible to both the model and the gate?
5. Would source-satisfied, stale, dirty, or non-dispatchable work be classified
   before a model spends tokens?
6. Did you cite the exact log/session ids, stop reason, rejected-action counts,
   recovery/gate events, and tool sequence before changing behavior?
7. Do logs/audits expose enough structured evidence to explain the run without
   reading the whole transcript?
8. Did the change add regression coverage for the bad behavior and a valid
   allowed path?
9. For scripted answer tests, does the oracle require positive semantic content
   and use a fixture that actually contains the concept under test?
10. If behavior was removed or reverted, did a retention table prove that no
   current live/scripted contract still depends on it?
11. Did an independent reviewer check the root cause, north-star alignment, and
   rabbit-hole risk before the fix was finalized?
12. Was the reviewer set minimal and matched to the failure signature, rather
   than the full bench by default?
