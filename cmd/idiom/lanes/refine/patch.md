# META — Patch brief (refine loop)

A previous draft was reviewed; bugs were flagged. Produce a
corrected version. Use `write_file` once to create
`{{NEW_DRAFT_PATH}}`. Final action MUST be the write_file call.

## What to read

1. Previous draft: `{{PREV_DRAFT_PATH}}` (full read).
2. Review with bug list: `{{PREV_REVIEW_PATH}}` (full read).

## Required behavior

Take the previous draft and modify it to fix EVERY bug listed in
the review's "Bugs found" section. Preserve everything else from
the previous draft — sections, anchors, OK content, structure.

For each bug:
- Read the bug's "how to fix" text in the review.
- Apply that fix to the corresponding part of the draft.
- If the fix is unclear, surface a `<<QUESTION>>:` marker — do NOT
  guess.

Write the FULL corrected brief to `{{NEW_DRAFT_PATH}}` (not just
a diff). The output is a complete brief, ready for re-review.

## Format constraints

- Same as the draft step: no outer ``` fence, ```text for code,
  one action per Files-to-Modify row.
- Do NOT introduce new bugs not flagged by the review (e.g., do
  not refactor "while you're at it").

## Files To Modify

| File | Change |
|------|--------|
| `{{NEW_DRAFT_PATH}}` | NEW FILE: corrected brief addressing review feedback |

## Done when

`test -f {{NEW_DRAFT_PATH}}` returns 0.
