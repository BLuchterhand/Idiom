# PRD/ADR/Brief Workflow Effort

This effort is the durable implementation state for delivering
`docs/design-prd-adr-brief-research-workflow.md`.

## Current State

- Effort ID: `prd-adr-brief-workflow`
- Last completed atom: `impl-0010-phase-1-product-audit-command`
- Status bucket: `started`
- Current phase: `phase-1-schemas-and-deterministic-linters`
- Context pack: `276cc0795cefadd0`
- Coverage tier: `unit`
- Blocker status: none open; fixture-status conflict resolved before Atom 8b
- Atom 9 report schema validation: all TDD obligations satisfied, product lint passes on 33 durable reports
- Atom 10 product audit command: all TDD obligations satisfied, 9 audit tests pass, product lint unchanged

The workflow implementation is in progress. These files are source-controlled
fixtures and resumable implementation state; shipped behavior is limited to the
committed product workflow validators plus lint and audit commands until later
atoms close.

## Implementation Guidelines

- Goal: deliver the full `docs/design-prd-adr-brief-research-workflow.md`
  design through durable atoms until the effort can be closed as complete.
- Codex orchestrates, verifies, reviews, investigates, records durable state, and
  commits. Dedicated implementor lanes make code changes by default; current
  eligible lanes are GLM through OpenCode and bounded DS4 trials through Codex
  CLI.
- Use OpenCode attached-server runs for implementor attempts, with a 10 minute
  cap before declaring timeout evidence:
  `OPENCODE_SERVER_URL=http://127.0.0.1:<port> ./scripts/run-opencode-glm.sh run --format default "<brief>"`.
- Select the implementor model from observed stability, not preference. As of
  `impl-0004`, GLM (`zai-coding-plan/glm-5.1`) is the most stable recorded
  choice because it produced partial patches on prior atoms; MiniMax 2.7
  highspeed (`minimax-coding-plan/MiniMax-M2.7-highspeed`) timed out after
  240 seconds with no output or code edits.
- `impl-0008` is a controlled DS4 trial using `ds4/deepseek-v4-flash` through
  OpenCode attached-server execution. Record whether it is actually local if the
  run surfaces process/locality evidence; otherwise treat locality as unverified.
- DS4 trial result for `impl-0008`: the JSON/log run errored with
  `Tool call not allowed while generating summary: read`; the default-output
  retry hit the 10 minute cap with no persisted code changes. DS4 did surface a
  real status-schema conflict in reasoning, but it did not produce a patch.
- DS4 through Codex CLI is now available for bounded implementor trials:
  `codex exec -p ds4 --cd /Users/ben/Documents/GitHub/idiom --sandbox danger-full-access -c approval_policy="never" --json "<brief>"`.
  A smoke test on 2026-05-17 returned `DS4_CODEX_READY` via JSONL in about 98
  seconds. Use it for the next small atom when the write set is clear; record
  GLM as fallback if DS4 stalls, times out, or produces no usable patch.
- Re-evaluate model stability after each atom and record the model, elapsed time,
  timeout status, and patch completeness in the implementation report.
- Keep implementor briefs patch-shaped, not task-shaped: name the exact
  decision, exact files, expected diff shape, forbidden choices, tests, and stop
  condition. Pre-resolve product/spec conflicts in Codex before handoff instead
  of asking the implementor to discover and decide them while coding.
- Prefer tiny implementation slices when a broader atom has coupled decisions.
  For example, migrate fixture data first, then add enum validation, then wire
  CLI failure behavior. Parallelize GLM implementors only when their write sets
  are disjoint; otherwise run sequential patch-shaped briefs and parallelize
  Codex-side context preparation, review, and verification.
- Prefer observable OpenCode runs for future implementor attempts:
  `--format json --print-logs --log-level INFO` for raw event/log streaming, and
  `--thinking` only when the provider supports it and the output remains safe to
  exclude from durable reports.
- Parallelize independent Codex-side reads, reviews, context preparation, and
  verification where this does not duplicate or conflict with a running
  implementor. Preserve sequential atom closeout: each atom still requires its
  own verification, review, investigation, commit, and push before the next atom
  is treated as complete.
- Commit and push after every atom completes.
- If an implementor times out or produces no usable closeout, record a
  `fallback_exception` before any Codex code edits. Keep fallback edits inside
  the planned atom scope and add compensating verification.

## Resume Path

Start future sessions by reading:

1. `docs/product-workflows/efforts/index.json`
2. `workflow_state.json`
3. `implementation_ledger.jsonl`
4. `events.jsonl`
5. the latest files under `reports/`

Do not use raw chat transcript memory as the source of truth for this effort.

## Artifact Map

- `source_design_ref.json`: source design and roadmap references.
- `evidence/evidence.jsonl`: privacy-safe evidence cards.
- `evidence/traceability_matrix.json`: evidence-to-brief links.
- `prd.md`: fixture PRD for the Phase 0 substrate.
- `adrs/`: fixture ADR set.
- `tdd.json`: current atom TDD obligations.
- `briefs/`: dispatch-shaped fixture brief.
- `reports/`: implementation, review, and investigation reports.

## Next Valid Action

Plan the next Phase 1 schema or deterministic linter atom. Prefer a bounded
DS4-through-Codex CLI implementor trial when the write set is clear; keep GLM 5.1
as fallback if DS4 stalls, times out, or produces no usable patch.
