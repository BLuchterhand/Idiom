# Export Manifest

This bundle contains the Obsidian vault plus companion design docs that should
travel with it when the implementation plan is exported to a new project.

## Obsidian Vault

All Markdown notes from `docs/obsidian/` are copied into
`docs/obsidian/`. The vault notes are the primary export because they provide
the connected reading order, glossary, implementation plan, and current system
map.

## Core Strategy And Operating Policy

These docs define why the system exists and how decisions should be made:

- `docs/harness-strategic-thesis.md`
- `docs/harness-design-roadmap.md`
- `docs/decisioning-north-star.md`
- `docs/harness-maintenance-best-practices.md`

## Seven Pillar Designs

These should come along because they define the major feature families and the
contracts between them:

- `docs/design-contract-first-local-autonomy.md`
- `docs/design-persistent-repository-memory.md`
- `docs/design-capability-oriented-tooling.md`
- `docs/design-auditable-planning-handoff.md`
- `docs/design-local-model-operations.md`
- `docs/design-eval-audit-learning-flywheel.md`
- `docs/design-agent-substrate-sidecar.md`

## Cross-Pillar Workflow Designs

These docs explain important workflows that span multiple pillars:

- `docs/design-prd-adr-brief-research-workflow.md`
- `docs/design-tdd-coverage.md`
- `docs/design-decompose-on-failure.md`

## Implementation Mechanics

These docs give enough detail to rebuild the high-risk mechanics without
dragging every tactical note into the export:

- `docs/lane-architecture.md`
- `docs/codebase-comprehension-memory-design.md`
- `docs/architect-packets.md`
- `docs/planner-executor-delegation-design.md`
- `docs/capability-registry.md`
- `docs/product-evidence-export.md`
- `docs/security-privacy-posture.md`
- `docs/tool-sidecar-install.md`

## Portability And Reuse

These are useful when adapting the harness concepts to a different project or
organization:

- `docs/generic-harness-architecture.md`
- `docs/reusability-design.md`
- `docs/open-source-harness-lessons.md`

## Deliberately Excluded

The export leaves out tactical release review notes, one-off patch plans,
language-specific integration experiments, source-repository proof fixtures,
and validation reports by default. Those files are useful inside the original
repository, but they make a new-project export noisier and easier to misread as
required architecture.

Add excluded docs only when the target ecosystem needs that exact implementation
history.
