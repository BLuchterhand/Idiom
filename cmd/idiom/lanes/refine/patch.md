# META — Patch brief (refine loop)

Your only job: call `write_file` to create `{{NEW_DRAFT_PATH}}` with
the previous draft modified to fix every bug AND every lint BLOCK.
Read the two required inputs, apply the fixes, write the result. Do
not analyze or summarize — write the file.

## Pre-loaded lint findings (from previous draft)

The refine wrapper captured `idiom brief lint` output on the
previous draft. Each BLOCK below is a required edit target — treat
on equal footing with reviewer bugs. Each WARN is a recommended
target unless the review explicitly waived it with rationale.

```text
{{LINT_OUTPUT}}
```

## Required reads (exactly 2)

1. Previous draft: `{{PREV_DRAFT_PATH}}` (full read).
2. Review with bug list: `{{PREV_REVIEW_PATH}}` (full read).

## Primary evidence paths (read-only)

The paths below are the source paths the wrapper extracted from the
spec or previous draft. They are the primary evidence paths and they
remain read-only here: do not modify them from this scratchpad patch
phase.

If a reviewer bug, lint finding, or symbol question requires a source
path outside this list, treat that as missing scope evidence for the
draft. Do not inspect the outside path from this phase; write a
`## Scope Correction Required` section naming the missing path/fact.

{{EVIDENCE_PATHS}}

## Pre-loaded source evidence

The wrapper extracted compact exact-line evidence from the primary
evidence paths using identifiers already named in the draft, review,
and lint findings. Treat these as usable anchors. If they resolve the
review/lint gaps, write the corrected draft now instead of re-reading
the same source facts.

{{SOURCE_EVIDENCE}}

## Source evidence closure

This patch phase is a scratchpad writer phase, not a source discovery
phase. After the two required scratchpad reads, write the corrected
draft from the previous draft, review, lint output, and pre-loaded
source evidence above.

If a reviewer bug or lint BLOCK names a source fact that is not present
in the draft or pre-loaded evidence — for example a missing old_text
anchor, function signature, enum value, type owner, or duplicate-test
name check — do not reopen source discovery here. Record the remaining
uncertainty in the corrected draft as `Source verification pending:` or
add a `## Scope Correction Required` section naming the missing path or
fact. Stop with `<<QUESTION>>:` only when the missing fact makes the
corrected draft impossible to write even with that uncertainty recorded.

## What to write

Take the previous draft and apply **both** (a) every fix listed in
the review's "Bugs found" section AND (b) every BLOCK in the
pre-loaded lint findings above. Preserve everything else from the
previous draft — sections, anchors, OK content, structure. The
output is the FULL corrected brief (not a diff), ready for
re-review.

For each reviewer bug:
- Read the bug's "how to fix" text in the review.
- Apply that fix to the corresponding part of the draft.

For each lint BLOCK:
- Identify the named issue (unfilled placeholder, missing anchor,
  multi-action row, content fence with wrong language tag, etc.).
- Edit the draft to resolve it.

If a fix is unclear, contradictory, or depends on a write path outside
the brief's scope, surface `<<QUESTION>>:` with the specific gap — do
NOT guess.

Do NOT introduce new bugs not flagged by the review or lint (no
"while you're at it" refactors).

## Required Edits

Create `{{NEW_DRAFT_PATH}}` as a full corrected replacement draft.
Do not modify the primary evidence paths. They exist only to supply exact
anchors for the scratchpad draft.

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

Write the corrected draft after the two required reads. Do not perform
source reads, discovery queries, broad orientation, dependency graphs,
history, or lookup repair from this scratchpad phase. Write with:

- `path`: `{{NEW_DRAFT_PATH}}`
- `content`: the full corrected brief (previous draft body + all
  review-flagged fixes applied).

Do not put the corrected brief in a chat message — it goes inside
`write_file`'s `content` argument. After `write_file` returns success,
give a single-sentence final answer ("Done. Brief at
`{{NEW_DRAFT_PATH}}`.") and stop.

## Completion

The successful `write_file` result is the completion proof. Do not run
`test -f`, `git diff`, `shell`, or any other follow-up check for this
scratchpad file.
