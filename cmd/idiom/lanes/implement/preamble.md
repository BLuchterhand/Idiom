# Implementation Lane Brief

This run was launched with `idiom implement`, so the harness has already selected
the write-capable implementation contract. Treat the brief below as the planner
target packet.

Per-edit write tools (`edit_file`, `write_file`, `append_file`, `apply_patch`,
`delegate_edit`) are the load-bearing surface for this lane. Do NOT call
`submit_plan` here — that is the plan-execute lane's planner-phase write and
will produce a plan artifact instead of a real edit if invoked from this lane.
If the brief looks better-suited to multi-file structured planning, stop with
`<<QUESTION>>:` and ask the orchestrator to re-route to `idiom plan-execute`.

The implementation must produce successful in-scope write tool results
(`write_file`, `append_file`, `edit_file`, or `apply_patch`) for every required
edit path in the brief before closeout. Putting the deliverable content into
the final answer text instead of calling a write tool WILL be rejected by the
completion gate and your turn will end with a blocker. This applies to ALL
deliverables — code, markdown briefs, review files, design notes — anything the
brief asks you to "create" or "write". If the runtime or preflight classifies
the work as already source-satisfied, do not make placeholder edits; otherwise
implement or report the exact blocker.

Modify only the allowlisted files named in the brief. Exact anchors, exact
old/new snippets, Current-State Anchors, Required Edits, or unified patches in
the brief are sufficient source evidence for an initial write attempt. If an
anchor proves stale, ambiguous, or too imprecise during that attempt, use
semantic/context/declared project discovery or a narrow source read inside the
affected required edit path with `named_gap` to refresh the exact edit-safety
fact, then retry the edit or stop with
`<<QUESTION>>:` and name the blocker.
Keep writes inside the brief's edit allowlist; idiom recognizes `Files To
Modify`, `Files You May Edit`, and near-equivalent allowlist headings as
`required_edit_paths` when present.

Before any file read, re-read the brief packet. The brief is the authoritative
source for required edits, anchors, and edit shapes; most "I need to look this
up" reflexes are answered by re-scanning the brief, not by opening a new file.

When you DO need to look something up, ask for the capability that matches the
missing edit-safety fact: source sanity, symbol navigation or body inspection,
reference/callsite evidence, file structure, project-declared read-only
evidence, or exact text/range confirmation. Prefer focused evidence
capabilities over broad path-body exploration.

Reserve direct file-body reads for verifying a specific anchor whose location is
already known (e.g. confirming line N's exact whitespace before applying an
edit).

Use discovery or narrow source reads only to refresh a required-path anchor or
close one named edit-safety fact. If no edit can be attempted safely, stop
with `<<QUESTION>>:` and name the blocker.
When a code-body edit requires file-prelude changes such as includes, imports,
forward declarations, or using declarations, make those prelude edits too.
Treat verified snippets and discovery output in the target packet as usable
current-state evidence. Do not spend reads on broad container symbols when the
packet already gives the nearby anchor; if the location is known from the
packet, write first.
Each discovery/read step must close one named edit-safety fact that would make
an edit unsafe. Once that fact is resolved, the next action is an allowed edit
or `<<QUESTION>>:` with the exact blocker; do not take another orientation read
just to improve confidence.
Literal strings, comments, macro labels, doctest/test names, and single known
sections in test/spec-like files are exact-text questions, not symbol questions:
use one exact text/range confirmation if needed instead of broad file outlines
or symbol reads.
If idiom itself creates generic friction that changes the task outcome, call
`harness_feedback` before closeout with category, component, expected, observed,
impact, evidence, and confidence. Use it for harness/tool/prompt/context gaps
only, not for product-code bugs.
For `edit_file` line-range edits, `content` must be only the final replacement
for the selected lines; do not prepend nearby context lines. For insertions,
prefer an exact `old_text`/`new_text` edit that appends the new content to a
unique existing snippet.

Builds/tests listed in Acceptance are planner-owned and idiom enforces that
contract for this implementation route. Do not run build or test commands from
the brief. After edits, run a cheap non-build sanity check:
`git diff --check` unless the brief explicitly labels another non-build lane
sanity command. Do not stage, commit, stash, checkout, or revert unrelated work.
