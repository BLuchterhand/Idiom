# META — Patch brief (refine loop)

Your only job: call `write_file` to create `{{NEW_DRAFT_PATH}}` with
the previous draft modified to fix every bug in the review. Read the
two inputs, apply the fixes, write the result. Do not analyze or
summarize — write the file.

## Required reads (exactly 2)

1. Previous draft: `{{PREV_DRAFT_PATH}}` (full read).
2. Review with bug list: `{{PREV_REVIEW_PATH}}` (full read).

## What to write

Take the previous draft and apply each fix listed in the review's
"Bugs found" section. Preserve everything else from the previous
draft — sections, anchors, OK content, structure. The output is the
FULL corrected brief (not a diff), ready for re-review.

For each bug:
- Read the bug's "how to fix" text in the review.
- Apply that fix to the corresponding part of the draft.
- If the fix is unclear or contradictory, surface `<<QUESTION>>:`
  with the specific gap — do NOT guess.

Do NOT introduce new bugs not flagged by the review (no "while
you're at it" refactors).

## Format constraints

- Same as the draft step: no outer ``` fence around the whole brief,
  use ` ```text ` for any inlined code (not ` ```go ` / etc.).
- Preserve the brief's H1 title and section headings verbatim from
  the previous draft.

## Files To Modify

| File | Change |
|------|--------|
| `{{NEW_DRAFT_PATH}}` | NEW FILE: corrected brief addressing review feedback |

## STOP AND WRITE NOW

After the two reads, your VERY NEXT TOOL CALL MUST be `write_file`
with:

- `path`: `{{NEW_DRAFT_PATH}}`
- `content`: the full corrected brief (previous draft body + all
  review-flagged fixes applied).

Do not do additional searches or reads. Do not put the corrected
brief in a chat message — it goes inside `write_file`'s `content`
argument. After `write_file` returns success, give a single-sentence
final answer ("Done. Brief at `{{NEW_DRAFT_PATH}}`.") and stop.

## Done when

`test -f {{NEW_DRAFT_PATH}}` returns 0.
