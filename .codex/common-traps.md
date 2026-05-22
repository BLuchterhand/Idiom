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
- **Tool results that prescribe strategy**: evidence tools should report facts,
  paths, symbols, counts, uncertainty, and provenance. Strategy guidance belongs
  in the runtime contract or a deterministic rejection message.

## Audit, Logs, And Feedback Traps

- **Diagnosing from memory**: start behavior debugging from lane history,
  conversation JSONL, telemetry JSONL, and `harness_audit`. If the cause cannot
  be seen without hand-reconstructing prose, improve structured logs/audit as
  part of the fix.
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

- **Rationale-free commits**: a commit subject that says what changed but not
  why is weak repo memory. For non-trivial changes, include the failure,
  requirement, or design pressure that made the change necessary; the chosen
  contract or tradeoff; meaningful alternatives avoided; and verification. Do
  not make future agents reconstruct intent from the diff alone.
- **Churn without history**: before undoing or reshaping established behavior,
  check focused git history, blame, or symbol history when the existing design
  is not self-explanatory. If the old rationale still applies, preserve it or
  explain why the new evidence supersedes it.
- **Changing prompts without gates**: prompt cleanup is useful, but a contract
  invariant needs a deterministic backstop and tests.
- **Changing gates without prompt parity**: the model should see the same
  contract the harness enforces. If a gate rejects an action, the prompt or
  runtime brief should make the valid alternatives visible.
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
6. Do logs/audits expose enough structured evidence to explain the run without
   reading the whole transcript?
7. Did the change add regression coverage for the bad behavior and a valid
   allowed path?
8. Did an independent reviewer check the root cause, north-star alignment, and
   rabbit-hole risk before the fix was finalized?
9. Was the reviewer set minimal and matched to the failure signature, rather
   than the full bench by default?
