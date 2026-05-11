# META — Review brief (refine loop)

Your only job: call `write_file` to create `{{REVIEW_PATH}}` with a
review of the drafted brief. **Read the brief ONCE, then write the
review.** The file IS the deliverable — a final-answer text
containing the review WILL be rejected.

## Hard tool budget

- **Exactly ONE `read_file`** of the drafted brief is required.
- **At most ONE additional `read_file`** is permitted, only if the
  brief makes a single specific anchor claim you genuinely doubt
  (e.g., "line 424 has X" and X looks implausible). Skip this when
  the brief's claims look internally consistent — most reviews
  don't need it.
- **No grep, search_content, code_navigate, or shell.** Validate
  from the brief's stated content alone.
- After the budget, your VERY NEXT TOOL CALL MUST be `write_file`.
  Repeated read attempts trigger the doom-loop guard and the lane
  fails with `no_edit_attempt`.

## Required steps (in order)

1. **Read** `{{DRAFT_PATH}}` (the drafted brief; full read).
2. (OPTIONAL, ONCE) spot-check ONE anchor against its source file
   ONLY if you genuinely doubt a specific line-number claim.
3. **`write_file`** to `{{REVIEW_PATH}}` with the content described
   in "Write `{{REVIEW_PATH}}` with this exact shape" below.
4. Single-sentence final answer ("Done. Review at `{{REVIEW_PATH}}`.").

## Write `{{REVIEW_PATH}}` with this exact shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. If you find zero bugs, still
emit the file with "Bugs found: none" and verdict "ship-as-is".

```text
# Review of brief

## Bugs found

1. **<short title>**: <description>. <how to fix>.
2. <next bug, if any; or write "none" if no bugs found>

## OK

- <what the brief gets right>

## Verdict

<ship-as-is | fix-bugs-then-ship | reject-and-redraft>
```

## What to check (from the brief alone)

These bugs are detectable from the brief's own content — no source
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

## What to spot-check (requires the ONE optional read)

These ONLY need the optional source-file read:

- A specific `<file>:<line>` anchor that seems wrong (e.g., "line
  424 has `--lane-mode`" when you suspect the actual line has
  different content). Read the file's narrow range around that
  line ONCE, then write the review.

If you don't have a specific reason to doubt an anchor, skip the
spot-check and write the review now.

## Files To Modify

| File | Change |
|------|--------|
| `{{REVIEW_PATH}}` | NEW FILE: review of the drafted brief |

## STOP AND WRITE NOW

After the required read (and the optional spot-check, if any), your
VERY NEXT TOOL CALL MUST be `write_file` with:

- `path`: `{{REVIEW_PATH}}`
- `content`: the filled-in review from "Write `{{REVIEW_PATH}}` with
  this exact shape" above, with every `<bracket>` replaced.

Do NOT continue reading. Do NOT do grep / search / code_navigate.
Do NOT put the review content into a final-answer text — the gate
will reject it. The deliverable IS the file. After `write_file`
returns success, give the single-sentence final answer and stop.

If you genuinely cannot write a review (e.g., the brief is
unreadable), surface `<<QUESTION>>:` with the specific blocker —
do NOT just stop or summarize without writing.

## Done when

`test -f {{REVIEW_PATH}}` returns 0.
