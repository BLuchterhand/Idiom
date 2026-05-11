# META — Draft brief (refine loop, iter 1)

Your only job: call `write_file` to create `{{DRAFT_PATH}}` with the
content described below. Read the spec, fill in the brackets, write
the file. Do not analyze, summarize, or comment — write the file.

## Spec

{{SPEC_BODY}}

## Write `{{DRAFT_PATH}}` with this exact shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. The brief MUST contain every
section below in this order:

```text
# {{TITLE}}

## Problem

<one or two sentences explaining what the spec asks for and why>

## Files To Modify

| File | Change |
|------|--------|
| `<workspace/relative/path.go>` | <ADD/MODIFY/NEW FILE: one action per row; NEW FILE rows are exempt from the one-action rule> |

## Verified Current State

<quote relevant existing code with `<file>:LINE` anchors. Verify line numbers exist before citing.>

## Required Behavior

1. <numbered step with EXACT old_text/new_text or full new-file content in ```text fences>
2. <next step>

## Acceptance

- `<shell command that verifies the change>`
- `<another shell command>`
```

## Format constraints

- Use ` ```text ` fences for inlined code (not ` ```go ` / ` ```python ` / etc.).
- Do NOT wrap the entire brief in an outer ``` fence — start with the H1 heading directly.

## Files To Modify

| File | Change |
|------|--------|
| `{{DRAFT_PATH}}` | NEW FILE: implementation brief for {{TITLE}} |

## STOP AND WRITE NOW

Your VERY NEXT TOOL CALL MUST be `write_file` with:

- `path`: `{{DRAFT_PATH}}`
- `content`: the filled-in template from "Write `{{DRAFT_PATH}}` with
  this exact shape" above, with every `<bracket>` replaced and the
  section headings preserved verbatim.

Do not read files. Do not search. Do not think out loud. The file
content goes inside the `write_file` call's `content` argument, not
in a chat message. After `write_file` returns success, give a single-
sentence final answer ("Done. Brief at `{{DRAFT_PATH}}`.") and stop.

If the spec is too vague to write a brief from, surface `<<QUESTION>>:`
with the specific gap — do NOT just stop or summarize without writing.

## Done when

`test -f {{DRAFT_PATH}}` returns 0.
