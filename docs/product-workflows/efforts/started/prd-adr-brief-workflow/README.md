# PRD/ADR/Brief Workflow Effort

This effort is the durable Phase 0 fixture for implementing
`docs/design-prd-adr-brief-research-workflow.md`.

## Current State

- Effort ID: `prd-adr-brief-workflow`
- Active atom: `impl-0001-phase-0-durable-workflow-substrate`
- Status bucket: `started`
- Current phase: `phase-0-design-fixtures`
- Context pack: `03dd97ee30a4e3d7`
- Coverage tier: `documentation`

The workflow implementation is still proposed. These files are source-controlled
fixtures and resumable implementation state, not shipped CLI behavior.

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
- `tdd.json`: documentation-tier TDD obligations.
- `briefs/`: dispatch-shaped fixture brief.
- `reports/`: implementation, review, and investigation reports.

## Next Valid Action

After `impl-0001-phase-0-durable-workflow-substrate` is reviewed and its
post-effort investigation is complete, the next valid action is to plan the
Phase 1 schema/validator atom from this effort state.
