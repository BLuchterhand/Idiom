---
schema_version: idiom.adr.v1.fixture
adr_id: ADR-0001
status: accepted
prd_refs:
  - REQ-IMPL-0001
evidence_refs:
  - EV-001
  - EV-002
  - EV-004
tdd_obligations:
  - TDD-IMPL-0001-001
  - TDD-IMPL-0001-002
  - TDD-IMPL-0001-005
coverage_tier: documentation
created_at: 2026-05-17T15:20:00Z
accepted_at: 2026-05-17T15:20:00Z
accepted_by: opencode-glm-impl-0001
acceptance_profile: fixture_seed
---

# ADR-0001: Seed Tracked Durable Effort Folder Before Code Implementation

## Context

The PRD/ADR/brief workflow design doc defines an effort folder structure for
tracking product workflow state, evidence, decisions, and implementation progress.
The roadmap confirms that zero implementation code exists and Phase 0 current
work is durable fixtures and implementation ledger seeding.

Before any Go code, prompt changes, or runtime behavior is implemented, the
project needs a source-controlled effort folder that serves as both a fixture
for future development and a proof that the artifact schemas can hold real
content.

## Decision

Seed a tracked durable effort folder at
`docs/product-workflows/efforts/started/prd-adr-brief-workflow/` with fixture
artifacts for workflow state, events, evidence, PRD, ADR, TDD obligations,
brief, and implementation reports. Use `.fixture` schema version suffixes to
distinguish these from eventual production schemas.

## Alternatives Considered

1. **Start with CLI commands first**: rejected because the CLI does not exist yet
   and fixture-first validates the artifact schemas without runtime dependencies.
2. **Use .idiom/ for fixtures**: rejected because `.idiom/` is ignored local
   state and the roadmap requires source-controlled durable artifacts.
3. **Skip fixtures entirely**: rejected because the long-lived implementation
   operating model requires resumable effort state across sessions.

## Evidence

- EV-001: README.md confirms existing lane and context_pack infrastructure.
- EV-002: Design doc defines the full lifecycle and artifact schemas.
- EV-004: Roadmap confirms NOT STARTED status and Phase 0 fixture scope.

## Consequences

- Positive: future sessions can resume from `index.json` without chat history.
- Positive: fixture schemas validate that the design is internally consistent.
- Positive: implementation ledger records the first atom for traceability.
- Risk: fixture schemas may drift from eventual implementation; the `.fixture`
  suffix makes this distinction explicit.

## Compatibility and Migration

Fixture artifacts use `idiom.*.v*.fixture` schema versions. When production
schemas are implemented, fixtures should be migrated or superseded. The effort
folder structure matches the design doc specification exactly.

## TDD Obligations

- TDD-IMPL-0001-001: evidence references cite real source paths
- TDD-IMPL-0001-002: JSON/JSONL parse without error
- TDD-IMPL-0001-005: no out-of-scope file modifications

## Rollback

Delete the effort folder and testdata directory. No runtime code was changed.

## Open Questions

None.
