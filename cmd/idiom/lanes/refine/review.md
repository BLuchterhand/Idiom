# META - Review brief (refine loop)

Expected path: artifact output `{{REVIEW_PATH}}`.

Artifact output path: `{{REVIEW_PATH}}`.

Review the drafted brief and return the filled review as your final
answer. Idiom writes that final answer to the artifact output path. If
the drafted brief is unreadable or too incomplete to review, return the
specific `<<QUESTION>>:` blocker instead of guesswork.

## Artifact Output

Path: `{{REVIEW_PATH}}`
Contract: final-answer artifact; no tool calls, no file edits, no shell commands.

## Pre-loaded lint findings

The refine wrapper already ran `idiom brief lint` on the draft and
captured the output below. Treat each BLOCK as a required edit
target and each WARN as a recommended one. A verdict of
`ship-as-is` REQUIRES zero BLOCKs (WARNs may be waived in the OK
section with a one-sentence rationale per warning).

```text
{{LINT_OUTPUT}}
```

## Pre-loaded drafted brief

Path: `{{DRAFT_PATH}}`

{{DRAFT_BODY}}

## Review evidence discipline

- Use the pre-loaded drafted brief and lint output above as the complete
  review evidence.
- Do not perform source reads, discovery queries, broad orientation, or
  lookup repair from this scratchpad review phase.
- Judge the draft from the drafted brief plus the pre-loaded lint output.
  If a concern depends on source truth (for example a doubtful
  `<file>:<line>` anchor, duplicate test name, or claimed existing
  symbol), record the uncertainty as a bug or OK caveat in the review
  instead of reopening source discovery. A bounded `Source verification
  pending:` note is not a bug by itself when the brief still gives a
  concrete edit shape and names the exact in-scope path/fact the
  implementation lane must confirm with one narrow read or discovery call.
- Return the review artifact. Stop with `<<QUESTION>>:` only if the
  drafted brief is unreadable or too incomplete to review.

## Return `{{REVIEW_PATH}}` With This Exact Shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. If you find zero bugs, still
emit the review with "Bugs found: none" and verdict "ship-as-is".

```text
# Review of brief

## Bugs found

1. **<short title>**: <description>. <how to fix>.
2. <next bug, if any; or write "none" if no bugs found>

## Lint findings

<copy each BLOCK and WARN from the pre-loaded lint output above
verbatim, OR write "none" if the lint pass was clean. The patch
phase consumes both this section and "Bugs found" as edit targets.>

## OK

- <what the brief gets right>
- <if you waive any WARN-level lint finding, justify here with
  the warning text + one-sentence rationale>

## Verdict

<ship-as-is | fix-bugs-then-ship | reject-and-redraft>
```

**Verdict rules**: `ship-as-is` is permitted ONLY when (a) "Bugs
found" is empty AND (b) "Lint findings" contains no BLOCK entries.
Any BLOCK forces at least `fix-bugs-then-ship`.

## What To Check From The Brief Alone

These bugs are detectable from the brief's own content, no source
reads needed:

- **Unfilled placeholders**: bracket tokens like `<short title>`,
  `RELATIVE_PATH`, `<numbered step>`, `<workspace/...>` left in the
  brief mean the model never finished the work.
- **Edit-shape problems**: append-to-end-of-existing-file is
  wedge-prone; prefer NEW FILE. Substring anchors must be unique in
  the target file (the brief should claim uniqueness explicitly).
- **Anchor uniqueness claims**: for each non-NEW-FILE edit, does
  the brief's `old_text` look unique? Common offender: a test file's
  closing `}` (matches every test). Flag and suggest a longer
  unique anchor.
- **Code that won't compile**: proposed `new_text` references
  symbols/imports/types that aren't introduced anywhere in the brief.
- **Acceptance commands** that wouldn't actually trigger the
  behavior (e.g., `go build` for a runtime-only bug, or a test
  pattern that doesn't match the new test names).
- **Outer ```text fence** wrapping the whole brief (silent parse break).
- **Self-contradictory**: the Problem says one thing and the
  Required Behavior does something different.

## What To Record As Uncertainty

These should be recorded in the review rather than checked through
source discovery:

- A specific `<file>:<line>` anchor that seems wrong (e.g., "line
  424 has `--lane-mode`" when you suspect the actual line has
  different content).
- A duplicate test name or exact symbol ownership question that would
  change whether the draft is executable.

Put bounded `Source verification pending:` entries in OK, not Bugs,
when all of the following are true:

- The pending fact is inside a file already listed in Files To Modify.
- The brief names the exact symbol, sentinel, boundary, or test
  insertion point to verify.
- The edit still has concrete behavior or replacement text once that
  single fact is confirmed.

Promote source uncertainty to Bugs only when it leaves the edit with no
concrete write shape, requires a source path outside Files To Modify, or
contradicts evidence already present in the draft/lint packet.

If you don't have a specific reason to doubt an anchor, skip the caveat
and return the review now.

## Artifact Closeout

If the drafted brief is unreadable, surface `<<QUESTION>>:` with the
specific blocker. Otherwise, return only the filled-in review from
"Return `{{REVIEW_PATH}}` With This Exact Shape" above, with every
`<bracket>` replaced.

Do not call tools, do not edit files, do not run shell commands, and do
not wrap the review in an outer code fence.

## Completion

The artifact command writing `{{REVIEW_PATH}}` is the completion proof.
