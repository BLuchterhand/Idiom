# META — Draft brief (refine loop, iter 1)

Your only job: call `write_file` to create `{{DRAFT_PATH}}` with the
content described below. Read the spec, fill in the brackets, write
the file. Do not analyze, summarize, or comment — write the file.

## Spec

{{SPEC_BODY}}

## Deliverable target

{{DELIVERABLE_TARGET_CONTEXT}}

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
| `RELATIVE_PATH` | <one action per row; NEW FILE rows are exempt from the one-action rule (ADD, MODIFY, or NEW FILE: ...)> |

## Verified Current State

<quote relevant existing code with `<file>:LINE` anchors. Verify line numbers exist before citing.>

## Required Behavior

1. <numbered step with EXACT old_text/new_text or full new-file content in ```text fences>
2. <next step>

## Acceptance

- `<shell command that verifies the change>`
- `<another shell command>`
```

## Critical: two file paths, two different purposes

The brief schema's `## Files To Modify` (inside the template above) is the **brief's edit target** — the file(s) the FUTURE `idiom lane plan-execute` will write when the brief is dispatched. Extract this from the spec text. Specs that say "produce `<path>`" / "create `<path>`" / "write `<path>`" / "output to `<path>`" name the target explicitly; use that path. If the spec doesn't name a target path, surface `<<QUESTION>>:` rather than guessing.

Your own write location (`{{DRAFT_PATH}}` — refine's scratchpad for THIS iteration) is **separate** from the brief's edit target. Do NOT put `{{DRAFT_PATH}}` in the brief's `Files To Modify` — that confuses the future lane into editing the wrong file. The Files To Modify section describes what the brief asks for, not what you wrote.

Example — for a spec that says "produce `docs/research/inventory.md` listing X":

```text
## Files To Modify

| File | Change |
|------|--------|
| `docs/research/inventory.md` | NEW FILE: inventory of X per spec |
```

NOT `| `{{DRAFT_PATH}}` | ... |` — that's where YOU write, not where the brief's edit lands.

## Format constraints

- Use ` ```text ` fences for inlined code (not ` ```go ` / ` ```python ` / etc.).
- Do NOT wrap the entire brief in an outer ``` fence — start with the H1 heading directly.

## Where you write THIS draft.md

Your output file (refine's scratchpad for this iteration) is `{{DRAFT_PATH}}`. This is unrelated to the brief content's `Files To Modify` field above — those are two different paths with two different purposes. You write to `{{DRAFT_PATH}}`; the future plan-execute lane writes to whatever the brief's `Files To Modify` names.

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
