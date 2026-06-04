---
schema_version: idiom.adr.v1
adr_id: ADR-SCRIPTED-APPROVAL
status: accepted
accepted_at: 2026-06-03T00:00:00Z
accepted_by: scripted-fixture
acceptance_profile: scripted
prd_refs:
  - REQ-SCRIPTED-APPROVAL
evidence_refs:
  - EV-SCRIPTED-APPROVAL
tdd_obligations:
  - TDD-SCRIPTED-APPROVAL
coverage_tier: unit
---

# ADR-SCRIPTED-APPROVAL: Scripted Approval Fixture

## Context

The scripted approval test needs a real approvable product workflow.

## Decision

Represent the fixture as a workflow already waiting in human verification.

## Alternatives Considered

Using a blocked workflow was rejected because attempted transitions do not prove approval.

## Evidence

- EV-SCRIPTED-APPROVAL: The workflow state exposes approve_for_implementation as the next action.

## Consequences

The TUI approval oracle can assert an applied transition event.

## Compatibility and Migration

The fixture is isolated to scripted-contract testdata.

## TDD Obligations

- TDD-SCRIPTED-APPROVAL: Product approval emits an applied transition.

## Rollback

Remove the fixture files.

## Open Questions

None.
