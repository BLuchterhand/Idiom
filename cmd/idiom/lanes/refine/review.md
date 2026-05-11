# META — Review brief (refine loop)

Your only job: call `write_file` to create `{{REVIEW_PATH}}` with a
review of the drafted brief. Read the brief, read at most 4 source
files it cites, write the review file. The file IS the deliverable —
a final-answer text containing the review WILL be rejected.

## Required steps (in order)

1. **Read** `{{DRAFT_PATH}}` (the drafted brief).
2. **Read** up to 4 source files cited in the brief (bounded line
   ranges only; do NOT whole-file-read).
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

## What counts as a bug

- Hardcoded line numbers wrong (verify against the actual file).
- Proposed code won't compile (missing imports, wrong package,
  wrong function signature, scope errors).
- Edit-shape problems: append-to-end-of-existing-file is wedge-prone;
  prefer NEW FILE; substring anchors must be unique in the target.
- **Anchor uniqueness**: for each non-NEW-FILE edit, mentally
  `grep -c '<old_text>' <target_file>` — if the anchor matches more
  than once, the executor will fail with "old_text matched N
  locations". Common offender: a test file's closing `}` (matches
  every test). Flag and suggest a longer unique anchor.
- Acceptance commands that wouldn't actually trigger the behavior.
- Outer ```text fence wrapping the whole brief (silent parse break).
- Provenance / heuristic logic gaps.

## Files To Modify

| File | Change |
|------|--------|
| `{{REVIEW_PATH}}` | NEW FILE: review of the drafted brief |

## STOP AND WRITE NOW

After the bounded reads (≤ 5 total: brief + up to 4 source files),
your VERY NEXT TOOL CALL MUST be `write_file` with:

- `path`: `{{REVIEW_PATH}}`
- `content`: the filled-in review from "Write `{{REVIEW_PATH}}` with
  this exact shape" above, with every `<bracket>` replaced.

Do NOT continue thinking. Do NOT do another read / grep / search /
code_navigate. Do NOT put the review content into a final-answer
text — the gate will reject it. The deliverable IS the file. After
`write_file` returns success, give the single-sentence final answer
and stop.

If you genuinely cannot write a review (e.g., the brief is
unreadable), surface `<<QUESTION>>:` with the specific blocker —
do NOT just stop or summarize without writing.

## Done when

`test -f {{REVIEW_PATH}}` returns 0.
