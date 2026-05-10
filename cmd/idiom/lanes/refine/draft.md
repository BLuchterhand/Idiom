# META — Draft brief (refine loop, iter 1)

You're drafting an implementation brief from a 1-paragraph spec.
Use `write_file` once to create `{{DRAFT_PATH}}`. Final action MUST
be the write_file call (not a final-answer text). Then stop.

## Spec

{{SPEC_BODY}}

## Required brief sections

`## Problem` (1-2 sentences), `## Files To Modify` (one row per
discrete action; prefer NEW FILE for new helpers/tests over
append-to-existing-file), `## Verified Current State` (quote the
relevant existing code with `<file>:LINE` anchors — verify line
numbers exist before citing), `## Required Behavior` (numbered
steps with EXACT old_text/new_text or full new-file content),
`## Acceptance` (shell commands).

## Format constraints

- Use ` ```text ` fences for inlined code (not ```go/```python/etc.).
- Do NOT wrap the entire brief in an outer ``` fence — start with
  the H1 heading directly.
- Each Files-to-Modify row describes ONE action; "NEW FILE: ..."
  prefix is exempt from the multi-action lint.

## Files to Modify

| File | Change |
|------|--------|
| `{{DRAFT_PATH}}` | NEW FILE: implementation brief for {{TITLE}} |

## Done when

`test -f {{DRAFT_PATH}}` returns 0.
