# META — Draft brief (refine loop, iter 1, markdown-deliverable mode)

The spec asks for a NEW markdown/text file deliverable (no source
code edits, no anchors). Your only job: call `write_file` to create
`{{DRAFT_PATH}}` with a brief that bundles the deliverable's content
ready for direct writing. Do not analyze, summarize, or comment —
write the file.

## Spec

{{SPEC_BODY}}

## Write `{{DRAFT_PATH}}` with this exact shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. The brief MUST contain every
section below in this order:

```text
# {{TITLE}}

## Problem

<one or two sentences explaining what the deliverable answers>

## Deliverable

Path: `<output path the spec asks for>`
Format: <markdown | text | json | etc.>

## Inline content

<the full content of the deliverable, ready to write verbatim into
the file at the Deliverable Path. Match whatever shape the spec asks
for — list, inventory, narrative, table, etc. Include enough detail
that a plan-execute lane can write this content with zero further
research. If the spec asks for "inventory of X", enumerate every X.
If it asks for a "design doc", write the doc.>

## Acceptance

- `test -f <path>` returns 0
- <optional: one structural check like `grep -q '^## Section' <path>`>
- <optional: line-count or byte-count lower bound>
```

## Format constraints

- Use ` ```text ` fences for inlined code (not ` ```go ` / ` ```python ` / etc.).
- Do NOT wrap the entire brief in an outer ``` fence — start with the H1 heading directly.
- The "Inline content" section IS the deliverable. The downstream
  plan-execute lane copies that section's content verbatim to the
  Deliverable Path. Do not paraphrase, summarize, or substitute
  `<TODO: fill in>` placeholders — fill in real content now.

## Files To Modify

| File | Change |
|------|--------|
| `{{DRAFT_PATH}}` | NEW FILE: markdown-deliverable brief for {{TITLE}} |

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
