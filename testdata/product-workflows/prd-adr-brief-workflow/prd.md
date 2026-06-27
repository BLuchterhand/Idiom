---
schema_version: idiom.prd.v1.fixture
prd_id: prd-impl-0001-phase-0
status: accepted
task_id: impl-0001-phase-0-durable-workflow-substrate
evidence_ledger: evidence/evidence.jsonl
created_at: 2026-05-17T15:20:00Z
durability: source_controlled
accepted_at: 2026-05-17T15:20:00Z
accepted_by: codex-orchestrator
acceptance_profile: fixture_seed_human_review
---

# PRD: Phase 0 Durable Workflow Substrate

## Problem Statement

The PRD/ADR/brief workflow design exists as a cross-pillar design proposal with
zero implementation code and zero durable effort artifacts. Future implementation
sessions need a resumable, source-controlled effort folder that records workflow
state, evidence, decisions, obligations, and implementation progress so that work
can continue across sessions without relying on raw transcript replay.

## User Persona

Implementation agent (OpenCode GLM) and reviewer agent (Codex GPT) operating
under the long-lived implementation operating model defined in the product
workflow roadmap.

## Goals

- Seed a tracked durable effort folder under `docs/product-workflows/efforts/`
- Provide valid fixture artifacts for workflow state, events, evidence, PRD, ADR,
  TDD obligations, brief, and implementation reports
- Enable future sessions to resume from `docs/product-workflows/efforts/index.json`
  without chat history

## Non-Goals

- Do not implement product workflow CLI commands
- Do not modify Go source, prompts, runtime code, or existing design docs
- Do not build workflow orchestration logic

## Requirements

### REQ-IMPL-0001

- **id**: REQ-IMPL-0001
- **priority**: must
- **statement**: Create a resumable durable implementation substrate with valid
  workflow state, event log, evidence cards, traceability, PRD, ADR, TDD
  obligations, brief, and implementation report as source-controlled fixtures.
- **evidence_refs**: [EV-001, EV-002, EV-003, EV-004, EV-005, EV-006]
- **owner_alignment**: technical
- **testability**: automated
- **tdd_obligations**: [TDD-IMPL-0001-001, TDD-IMPL-0001-002, TDD-IMPL-0001-003, TDD-IMPL-0001-004, TDD-IMPL-0001-005]
- **open_questions**: []
- **metric**: 15 fixture files created, all JSON/JSONL parse without error, no
  out-of-scope file modifications.

## Current Behavior

No durable effort folder or fixture artifacts exist. The roadmap confirms PRD/ADR/
brief workflow is NOT STARTED.

## Desired Behavior

A complete effort folder at
`docs/product-workflows/efforts/started/prd-adr-brief-workflow/` with all
required artifacts, plus a testdata fixture index, such that any future session
can load `workflow_state.json`, read `README.md`, and determine what has been done
and what remains.

## Success Metrics

- All 15 allowed files exist and parse correctly
- No forbidden files were modified
- Privacy scan finds no secrets or raw prompts
- Git diff passes `--check`

## Risks

| Risk | Mitigation |
| --- | --- | --- |
| Fixture schemas drift from eventual implementation | Fixture schema versions include `.fixture` suffix to distinguish from production schemas |
| Future sessions ignore the effort folder | README.md provides explicit resumption instructions |

## Open Questions

None.
