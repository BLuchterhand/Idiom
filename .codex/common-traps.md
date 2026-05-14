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
- **Prompt reminders as contracts**: if a repeated failure is mechanically
  knowable, fix preflight, route metadata, runtime brief, tool schema, or a
  deterministic gate instead of adding another reminder.
- **Magic phrases in briefs**: do not make callers sprinkle prose strings to
  select harness behavior. Use command modes, route metadata, typed artifacts,
  or runtime brief fields.
- **Project-specific repairs in generic prompts**: a project run can be the
  reproducer, but reusable Idiom fixes must be generic and evidence-shaped.

## Plan-Execute And Implementation Traps

- **Placeholder edits**: never submit or make a whitespace/no-op/verification
  edit just because the contract is write-shaped. If source already satisfies
  the task and the lane allows it, close as source-satisfied with per-path
  evidence. Otherwise return a typed blocker.
- **Planner/executor tool confusion**: plan-execute planner phases use
  `submit_plan`; implement phases use write tools. Do not blur these surfaces
  in prompts, recovery nudges, or examples.
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
- **Missing final-state telemetry**: terminal outcomes such as source-satisfied,
  stale, failed-refine, dirty-scope, no-edit-attempt, and verification-blocked
  should be typed events or ledger states, not only words in the transcript.
- **No regression fixture**: every harness behavior fix should include a test
  for the bad behavior and at least one valid case that remains allowed.
- **Privacy leaks in audit**: logs and audit outputs should keep bounded counts,
  state names, paths, session ids, and command ids. Avoid raw prompts, long
  transcript excerpts, file bodies, secrets, or customer-identifying strings in
  durable artifacts.
- **Feedback as product bug bucket**: use `harness_feedback` for reusable Idiom
  harness/tool/prompt/context gaps, not for ordinary target-repo defects.

## Process Traps

- **Changing prompts without gates**: prompt cleanup is useful, but a contract
  invariant needs a deterministic backstop and tests.
- **Changing gates without prompt parity**: the model should see the same
  contract the harness enforces. If a gate rejects an action, the prompt or
  runtime brief should make the valid alternatives visible.
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
2. Is each invariant enforced by the harness rather than only by prose?
3. Are all valid terminal states visible to both the model and the gate?
4. Would source-satisfied, stale, dirty, or non-dispatchable work be classified
   before a model spends tokens?
5. Do logs/audits expose enough structured evidence to explain the run without
   reading the whole transcript?
6. Did the change add regression coverage for the bad behavior and a valid
   allowed path?
