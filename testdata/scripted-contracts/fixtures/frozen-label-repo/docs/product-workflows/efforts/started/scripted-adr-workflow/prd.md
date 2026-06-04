---
schema_version: idiom.prd.v1
prd_id: PRD-SCRIPTED-APPROVAL
status: accepted
task_id: scripted-adr-workflow
created_at: 2026-06-03T00:00:00Z
accepted_at: 2026-06-03T00:00:00Z
accepted_by: scripted-fixture
acceptance_profile: scripted
---

# PRD: Scripted Approval Workflow

## Requirements

### REQ-SCRIPTED-APPROVAL

- **id**: REQ-SCRIPTED-APPROVAL
- **priority**: must
- **statement**: The scripted product workflow approval fixture must represent a workflow that can be approved for implementation.
- **evidence_refs**: [EV-SCRIPTED-APPROVAL]
- **owner_alignment**: product_workflow
- **testability**: scripted
- **tdd_obligations**: [TDD-SCRIPTED-APPROVAL]
- **open_questions**: []
- **metric**: The workflow transition emits an applied approval event.
