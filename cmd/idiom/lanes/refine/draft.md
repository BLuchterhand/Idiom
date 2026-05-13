# META — Draft brief (refine loop, iter 1)

Your only job: call `write_file` to create `{{DRAFT_PATH}}` with the
content described below. Read the spec, fill in the brackets, write
the file. Do not analyze, summarize, or comment — write the file.

## Files To Modify

| File | Change |
|------|--------|
| `{{DRAFT_PATH}}` | NEW FILE: refine scratchpad draft brief |

## Spec

{{SPEC_BODY}}

## Draft phase evidence contract

- Use the embedded spec above as the evidence for this first
  scratchpad draft.
- If exact source evidence is missing or seems stale, record
  `Source verification pending:` in the draft. Review, lint, and
  patch phases are responsible for source-backed refinement.
- Stay on the draft deliverable instead of doing source discovery in
  this phase. Ask only if the spec is too vague to draft from.

## Write `{{DRAFT_PATH}}` with this exact shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. The brief MUST contain every
section below in this order:

```text
# {{TITLE}}

## Problem

<one or two sentences explaining what the spec asks for and why>

<!-- idiom:harness-deliverable-block -->

## Verified Current State

<quote exact anchors already present in the spec. If the spec does
not provide enough exact current source evidence, write a short
`Source verification pending:` gap here. Do not read files to fill
this section during the draft phase.>

## Required Behavior

1. <numbered step with EXACT old_text/new_text or full new-file content in ```text fences>
2. <next step>

## Acceptance

- `<shell command that verifies the change>`
- `<another shell command>`
```

## What the marker means

The `<!-- idiom:harness-deliverable-block -->` line above is NOT a
placeholder you fill in. It is a literal HTML comment that the harness
substitutes at finalize time with the deliverable's `## Files To Modify`
block, populated from the spec's named target. **Emit the marker
verbatim. Do not replace it with your own `## Files To Modify` block.
Do not paste any file path on or near that line.** The harness owns
the deliverable path; your job is the content sections only.

If you accidentally add a `## Files To Modify` section yourself, the
lint will flag the duplicate at iter-N+1.

## Format constraints

- Use ` ```text ` fences for inlined code (not ` ```go ` / ` ```python ` / etc.).
- Do NOT wrap the entire brief in an outer ``` fence — start with the H1 heading directly.

## Where you write THIS draft.md

Your output file (refine's scratchpad for this iteration) is `{{DRAFT_PATH}}`. The
future plan-execute lane writes wherever the harness-injected `## Files To Modify`
block names — you never see or choose that path. Two different files, two different
consumers, both harness-owned.

## STOP AND WRITE NOW

Your VERY NEXT TOOL CALL MUST be `write_file` with:

- `path`: `{{DRAFT_PATH}}`
- `content`: the filled-in template from "Write `{{DRAFT_PATH}}` with
  this exact shape" above, with every `<bracket>` replaced and the
  section headings (including the `<!-- idiom:harness-deliverable-block -->`
  marker line) preserved verbatim.

Stay on the draft deliverable: write the file from the embedded spec
and recorded gaps instead of doing source discovery in this phase.
The file content goes inside the `write_file` call's `content`
argument, not in a chat message. After `write_file` returns success,
give a single-sentence final answer ("Done. Brief at
`{{DRAFT_PATH}}`.") and stop.

If the spec is too vague to write a brief from, surface `<<QUESTION>>:`
with the specific gap — do NOT just stop or summarize without writing.

## Completion

The successful `write_file` result is the completion proof. Do not run
`test -f`, `git diff`, `shell`, or any other follow-up check for this
scratchpad file.
