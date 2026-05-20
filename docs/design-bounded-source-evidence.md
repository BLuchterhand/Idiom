# Bounded Source Evidence Design

## Objective

Keep implementation lanes source-grounded without forcing local models to spend
large parts of their context window on whole-file dumps.

The long-term intent is a source evidence receipt system: the model can ask for
the source truth it needs, the harness can provide a bounded range or symbol
body with provenance, and readiness gates can verify that enough current source
was seen before a write or `submit_plan` without equating evidence with an
entire file body.

North star: the model owns strategy; the harness owns contracts. The harness
should not choose the implementation approach, but it should own evidence
shape, scope, freshness, and auditability.

## Failure Shape

The live TUI run exposed a contract mismatch:

- `required_edit_paths` correctly named the mutation scope.
- Scoped implementation correctly allowed source reads on those paths.
- A range-less `read_file` on a 6,000+ line required path then returned the
  whole file, consumed a large context slice, and forced immediate compaction.

The bug was not that the model wanted source. The bug was that the harness
treated "this path is in scope" as "the only valid evidence shape is the full
file." Required paths are authority for mutation scope, not an instruction to
flood the transcript.

## Design Principles

- Keep `required_edit_paths` as mutation authority only.
- Treat source evidence as a separate, typed thing: path, range or symbol,
  freshness, source of selection, and uncertainty.
- Prefer bounded ranges, symbols, outlines, and context views over raw whole
  files for large paths.
- Let the model select follow-up probes from concrete options. The harness may
  bound an unsafe evidence shape, but it should return actionable next ranges
  instead of merely refusing the request.
- Preserve small-file ergonomics. Range-less `read_file` remains useful for
  small required files and for generic workspace use outside scoped
  implementation.
- Count bounded source-body evidence as real evidence when it carries current
  source lines from the target path.

## Target Contract

A source evidence receipt should eventually be a first-class record with:

- `path`: workspace-relative source path.
- `evidence_kind`: `range`, `symbol`, `outline`, `search_hit`, `context_view`,
  or `packet_anchor`.
- `range`: exact line span when source-body evidence is present.
- `selector`: why this span was chosen, such as task term, symbol, packet
  anchor, failing test, or fallback top window.
- `freshness`: source fingerprint or index id used to prove current-state
  evidence.
- `sufficiency`: whether this evidence can satisfy read-before-write,
  planner source-body, or only orientation.
- `uncertainty`: what remains unknown after this receipt.

Readiness gates should consume these receipts instead of parsing long raw
outputs. Transcript compaction should pin receipts and changed paths, not
entire file bodies.

## Immediate Fix

For scoped implementation only:

- A range-less `read_file` targeting a required or explicit evidence path keeps
  bypassing generic discovery redirect.
- If the target is small, execute the normal `read_file`.
- If the target is large, return a bounded source packet instead of the full
  body. The packet includes total line count, the returned line range, why the
  full read was bounded, and concrete follow-up `read_file` ranges the model
  can request.
- Explicit `start_line` / `end_line` reads remain untouched and count as
  source evidence.
- Generic workspace `read_file` behavior remains unchanged; this is a scoped
  implementation contract, not a global file-reading policy.

## Future Work

- Persist source evidence receipts in the runner ledger and audit output.
- Teach `context_pack` and `context_zoom` to emit receipt ids for source
  ranges, symbols, and packet anchors.
- Update planner source-body gates to accept receipt coverage instead of
  requiring whole-path reads.
- Add compact receipt summaries to context compaction so future turns can
  preserve evidence without replaying source bodies.
- Surface receipt coverage in TUI progress logs: path, source range, selector,
  and remaining required evidence gaps.

## Validation

The immediate fix should prove:

- Non-scoped reads keep the existing discovery redirect behavior.
- Scoped small required files can still be read directly.
- Scoped large range-less required reads return bounded source, not whole-file
  output.
- A later bounded range read on the same path remains valid evidence before an
  edit.

The longer-term receipt work should add audit coverage showing which paths and
ranges satisfied each readiness obligation.
