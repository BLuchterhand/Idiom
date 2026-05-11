# META — Review brief (refine loop)

## Required steps (in order)

1. **Read** `{{DRAFT_PATH}}` (the drafted brief).
2. **Read** up to 4 source files cited in the brief (bounded
   ranges only).
3. **`write_file`** to `{{REVIEW_PATH}}` with the markdown review
   (format below).
4. Give a one-sentence final answer ("Done. Review at `{{REVIEW_PATH}}`.").

**The deliverable IS the file at `{{REVIEW_PATH}}`. A final-answer
text containing the review is NOT the deliverable — the gate WILL
reject it. If you find no bugs, still call `write_file` with
"Bugs found: none" and verdict "ship-as-is".**

If the brief is unreadable or you genuinely can't write a review,
surface `<<QUESTION>>:` with the specific blocker. Do NOT just
summarize without writing.

## What to read (BOUNDED — total ≤5 reads)

1. The drafted brief: `{{DRAFT_PATH}}` (full read).
2-5. Source files cited in the brief (prioritize files with line
     anchors; bounded line ranges only — do NOT read whole files).

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
  every test). Flag and suggest a longer unique anchor like the
  closing `}` of a SPECIFIC named test.
- Acceptance commands that wouldn't actually trigger the behavior.
- Outer ```text fence wrapping the whole brief (silent parse break).
- Provenance / heuristic logic gaps.

## Output format (write to `{{REVIEW_PATH}}`)

```text
# Review of brief

## Bugs found

1. **<short title>**: <description>. <how to fix>.

## OK

- <thing the brief gets right>

## Verdict

<ship-as-is | fix-bugs-then-ship | reject-and-redraft>
```

If zero bugs found, write the file with "Bugs found: none" and
verdict "ship-as-is".

## Files To Modify

| File | Change |
|------|--------|
| `{{REVIEW_PATH}}` | NEW FILE: review of the drafted brief |

## Done when

`test -f {{REVIEW_PATH}}` returns 0.

## STOP AND WRITE NOW

You have everything you need. Your VERY NEXT TOOL CALL MUST be
`write_file` with:

- `path`: `{{REVIEW_PATH}}`
- `content`: the markdown review described in "Output format" above

Do NOT do another read. Do NOT do another grep / search /
code_navigate. Do NOT put the review content into a final answer
text message — the lane will reject the answer because no file
was written. The deliverable IS the file at `{{REVIEW_PATH}}`,
not a prose summary.

After `write_file` returns success, give a one-sentence final
answer (e.g. "Done. Review at `{{REVIEW_PATH}}`.") and stop.

If you genuinely cannot write a review (e.g., the brief is
unreadable), surface `<<QUESTION>>:` with the specific blocker —
do NOT just stop or summarize without writing.
