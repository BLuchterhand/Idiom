---
schema_version: idiom.brief.v1.fixture
task_id: impl-0001-phase-0-durable-workflow-substrate
effort_id: prd-adr-brief-workflow
status: executed_fixture
complexity_tier: C0
implementor_id: opencode-glm-impl-0001
created_at: 2026-05-17T15:20:00Z
executed_at: 2026-05-17T15:20:00Z
context_pack_id: 03dd97ee30a4e3d7
---

# Brief: Phase 0 Durable Workflow Substrate

This brief has already been executed by the GLM implementor during the current
session. It is recorded here as a fixture, not as a future dispatch request.

## Files To Modify

| File | Action |
| --- | --- |
| docs/product-workflows/efforts/index.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/README.md | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/source_design_ref.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/workflow_state.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/events.jsonl | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/implementation_ledger.jsonl | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/tdd.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/evidence/evidence.jsonl | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/evidence/traceability_matrix.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/prd.md | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/adrs/ADR-0001-durable-workflow-substrate.md | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/adrs/index.json | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/briefs/impl-0001-phase-0-durable-workflow-substrate.md | create |
| docs/product-workflows/efforts/started/prd-adr-brief-workflow/reports/impl-0001-implementation.json | create |
| testdata/product-workflows/prd-adr-brief-workflow/README.md | create |

## Verified Current State

- Repository: `/Users/ben/Documents/GitHub/idiom`
- HEAD: `85af477 Configure GLM OpenCode implementation workflow`
- No prior effort folder or fixture artifacts exist
- Design docs are at `docs/design-prd-adr-brief-research-workflow.md` and
  `docs/design-tdd-coverage.md`
- Roadmap confirms Phase 0 scope: fixtures and ledger seeding only

## Required Behavior

Create all 15 files listed above with valid JSON/JSONL/YAML-MD content following
the artifact schemas defined in the design docs. All fixture schema versions
must include the `.fixture` suffix. No Go source, prompts, runtime code, or
existing design docs may be modified.

## Constraints

- `.idiom/` is ignored local state; write tracked state under
  `docs/product-workflows/`
- Privacy-safe artifacts only: IDs, paths, hashes/counts/statuses are OK; no
  raw prompts, raw transcripts, file bodies, secrets, or customer-identifying
  data
- Timestamps at `2026-05-17T15:20:00Z` unless updated_at after verification
- No non-ASCII punctuation
- Do not claim the product workflow CLI exists

## Tests

1. `python3 -m json.tool` on each JSON file
2. `python3 json.loads` per line on each JSONL file
3. `git diff --check` for whitespace hygiene
4. Scope check: `git diff --name-only` shows only allowed paths

## Acceptance

- All 15 files exist and parse correctly
- No forbidden paths modified
- Privacy scan finds no secrets
- Implementation ledger records both planned and completed entries
- Workflow state is valid for an active effort in phase-0-design-fixtures

## Decisions

- D-001: Use `.fixture` schema version suffix to distinguish from production
  schemas (per ADR-0001)
- D-002: Seed directly into `docs/product-workflows/efforts/started/` rather
  than `unstarted` because the implementor is actively working
- D-003: Open obligation OBL-POST-EFFORT-REVIEW for Codex reviewer; not
  blocking
