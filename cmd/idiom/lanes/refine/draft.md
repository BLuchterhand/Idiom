# META — Draft brief (refine loop, iter 1)

Artifact output path: `{{DRAFT_PATH}}`.

Read the spec and return the filled-in brief below as your final
answer. Idiom writes that final answer to the artifact output path. If
the spec is too vague to draft from, return the specific
`<<QUESTION>>:` blocker instead of writing guesswork.

## Artifact Output

Path: `{{DRAFT_PATH}}`
Contract: final-answer artifact; no tool calls, no file edits, no executed shell commands. Textual acceptance commands may appear inside the artifact.

## Spec

{{SPEC_BODY}}

## Draft phase evidence contract

- Use the embedded spec above as the evidence for this first
  scratchpad draft.
- If exact source evidence is missing or seems stale, record
  `Source verification pending:` in the draft. Review, lint, and
  patch phases are responsible for source-backed refinement.
- If the spec's verified current-state anchors already show the
  requested final state for every target path, write
  `Status: source-satisfied - do not dispatch` immediately after the
  H1 and cite the exact path evidence instead of inventing a no-op edit.
- For existing source paths, the Required Behavior must use executable
  edit shape: exact `old_text`/`new_text` blocks or
  `start_line`/`end_line` plus replacement `content`. Never leave a
  prose-only mutation site such as "insert after", "append near", or
  "update around" without that edit shape.
- Stay on the draft deliverable instead of doing source discovery in
  this phase. Ask only if the spec is too vague to draft from.

## Return this exact artifact shape

Replace each `<bracket placeholder>` with your text. Keep the
section headings exactly as shown. The brief MUST contain every
section below in this order:

```text
# {{TITLE}}

Status: <pending | source-satisfied - do not dispatch>

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

Your output artifact (refine's scratchpad for this iteration) is
`{{DRAFT_PATH}}`. The future plan-execute lane writes wherever the
harness-injected `## Files To Modify` block names — you never see or
choose that path. Two different files, two different consumers, both
harness-owned.

## Artifact Closeout

If the spec is too vague to write a brief from, surface `<<QUESTION>>:`
with the specific gap now. Otherwise, return only the filled-in brief
from "Return this exact artifact shape" above, with every `<bracket>`
replaced and the section headings (including the
`<!-- idiom:harness-deliverable-block -->` marker line) preserved verbatim.

Stay on the draft deliverable: produce the brief from the embedded spec
and recorded gaps instead of doing source discovery in this phase. Do
not call tools, do not edit files, do not run shell commands, and do
not wrap the brief in an outer code fence.

## Completion

The artifact command writing `{{DRAFT_PATH}}` is the completion proof.
