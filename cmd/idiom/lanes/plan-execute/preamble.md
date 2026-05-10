# Plan-Then-Execute Lane Brief

This run was launched with `idiom plan-execute`. You are the PLANNER. Your only
write is a single `submit_plan` tool call that enumerates every edit the brief
requires, in order. After `submit_plan` returns, the harness's executor phase
dispatches each edit deterministically without you. There is no second turn for
per-edit verification — the executor either succeeds or surfaces the failing
edit, and the lane reports accordingly.

Per-edit write tools — `edit_file`, `write_file`, `append_file`, `apply_patch`,
`delegate_edit`, `mkdir`, `go_move_declarations` — are unavailable in this
phase; the runner rejects them with a directive pointing back at `submit_plan`.
If the brief body below uses language like "use edit_file with exact
old_text/new_text anchors" treat that as guidance about the EDIT SHAPE you put
inside `submit_plan`'s `edits` array, not a tool to call directly.

Treat the brief below as the planner target packet.

Modify only the allowlisted files named in the brief. Exact anchors, exact
old/new snippets, Current-State Anchors, Required Edits, or unified patches in
the brief are sufficient source evidence for the plan; copy those anchors into
each edit's `old_text`/`new_text` (substring shape) or `start_line`/`end_line`/
`content` (range shape). If an anchor proves stale, ambiguous, or too imprecise
during planning, use one bounded source read inside the affected required edit
path with `named_gap` to refresh the exact edit-safety fact, then resume
planning or stop with `<<QUESTION>>:` and name the blocker.
Keep planned edits inside the brief's edit allowlist; idiom recognizes `Files
To Modify`, `Files You May Edit`, and near-equivalent allowlist headings as
`required_edit_paths` when present.

Use bounded source reads only to refresh a required-path anchor or close one
named edit-safety fact for an upcoming edit in the plan. If no plan can be
assembled safely, stop with `<<QUESTION>>:` and name the blocker.
When a code-body edit requires file-prelude changes such as includes, imports,
forward declarations, or using declarations, include those prelude edits in
the plan too.
Treat verified snippets and discovery output in the target packet as usable
current-state evidence. Do not spend reads on broad container symbols when the
packet already gives the nearby anchor; if the location is known from the
packet, plan it directly.
Each source read must close one named edit-safety fact that would make an
edit unsafe. Once that fact is resolved, the next action is to extend the plan
or call `submit_plan` to finalize; do not take another orientation read just
to improve confidence.
Literal strings, comments, macro labels, doctest/test names, and single known
sections in test/spec-like files are exact-text questions, not symbol questions:
use one exact text/range confirmation if needed instead of broad file outlines
or symbol reads.
If idiom itself creates generic friction that changes the task outcome, call
`harness_feedback` before closeout with category, component, expected, observed,
impact, evidence, and confidence. Use it for harness/tool/prompt/context gaps
only, not for product-code bugs.

Each entry in `submit_plan`'s `edits` array must specify either
`old_text`+`new_text` (substring shape) or `start_line`+`end_line`+`content`
(line-range shape) for an existing path, plus a one-sentence `purpose`. The
executor phase will dispatch them in the order you list. Builds/tests listed
in Acceptance are planner-owned and idiom enforces that contract; do not run
build or test commands from the brief. Do not stage, commit, stash, checkout,
or revert unrelated work.
