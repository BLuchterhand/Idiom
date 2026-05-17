# META - Patch brief (refine loop)

Expected path: artifact output `{{NEW_DRAFT_PATH}}`.

Artifact output path: `{{NEW_DRAFT_PATH}}`.

Return a full corrected replacement draft as your final answer. Idiom
writes that final answer to the artifact output path. Use the
pre-loaded previous draft, review, lint output, and source evidence
below. If a corrected draft is impossible, return the specific
`<<QUESTION>>:` blocker instead of guesswork.

## Artifact Output

Path: `{{NEW_DRAFT_PATH}}`
Contract: final-answer artifact; no tool calls, no file edits, no shell commands.

## Pre-loaded lint findings from previous draft

The refine wrapper captured `idiom brief lint` output on the previous
draft. Each BLOCK below is a required edit target, treat on equal
footing with reviewer bugs. Each WARN is a recommended target unless
the review explicitly waived it with rationale.

```text
{{LINT_OUTPUT}}
```

## Pre-loaded previous draft

Path: `{{PREV_DRAFT_PATH}}`

{{PREV_DRAFT_BODY}}

## Pre-loaded review with bug list

Path: `{{PREV_REVIEW_PATH}}`

{{PREV_REVIEW_BODY}}

## Primary evidence paths

The paths below are the source paths the wrapper extracted from the
spec or previous draft. They are read-only evidence for this scratchpad
patch phase. Do not modify them from this phase.

If a reviewer bug, lint finding, or symbol question requires a source
path outside this list, treat that as missing scope evidence for the
draft. Do not inspect the outside path from this phase; write a
`## Scope Correction Required` section naming the missing path/fact.

{{EVIDENCE_PATHS}}

## Pre-loaded source evidence

The wrapper extracted compact exact-line evidence from the primary
evidence paths using identifiers already named in the draft, review,
and lint findings. Treat these as usable anchors. If they resolve the
review/lint gaps, return the corrected draft now instead of re-reading
the same source facts.

{{SOURCE_EVIDENCE}}

## Source evidence closure

This patch phase is a scratchpad writer phase, not a source discovery
phase. Use the pre-loaded scratchpad and source evidence above to write
the corrected draft.

If a reviewer bug or lint BLOCK names a source fact that is not present
in the draft or pre-loaded evidence, for example a missing old_text
anchor, function signature, enum value, type owner, or duplicate-test
name check, do not reopen source discovery here. Record the remaining
uncertainty in the corrected draft as `Source verification pending:` or
add a `## Scope Correction Required` section naming the missing path or
fact. Stop with `<<QUESTION>>:` only when the missing fact makes the
corrected draft impossible to write even with that uncertainty recorded.

## What To Return

Take the previous draft and apply both (a) every fix listed in the
review's "Bugs found" section and (b) every BLOCK in the pre-loaded lint
findings above. Preserve everything else from the previous draft:
sections, anchors, OK content, and structure. The output is the full
corrected brief, not a diff, ready for re-review.

For each reviewer bug:
- Read the bug's "how to fix" text in the review.
- Apply that fix to the corresponding part of the draft.

For each lint BLOCK:
- Identify the named issue (unfilled placeholder, missing anchor,
  multi-action row, content fence with wrong language tag, etc.).
- Edit the draft to resolve it.

If a fix is unclear, contradictory, or depends on a write path outside
the brief's scope, surface `<<QUESTION>>:` with the specific gap. Do
not guess.

Do not introduce new bugs not flagged by the review or lint.

## Format Constraints

- Same as the draft step: no outer ``` fence around the whole brief,
  use ` ```text ` for any inlined code (not ` ```go ` / etc.).
- Preserve the brief's H1 title and section headings verbatim from
  the previous draft.

## Artifact Closeout

Return only the full corrected brief. Do not put a diff, summary, or
tool request in the answer. Do not call tools, do not edit files, do
not run shell commands, and do not wrap the brief in an outer code
fence.

## Completion

The artifact command writing `{{NEW_DRAFT_PATH}}` is the completion proof.
