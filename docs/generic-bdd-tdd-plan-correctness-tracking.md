# Generic BDD/TDD Top-Level Plan Correctness Tracking

Status: complete

This document tracks the work needed to make feature-planner produce and gate
top-level BDD/TDD plans that are useful across languages, frameworks, and task
types. The intent is generic correctness, not a template for one recurring
example.

## Problem

Recent feature-planner runs can now avoid leaking internal diagnostics and can
open concrete repair questions. The remaining quality gap is plan correctness:
a plan can still look structured while hiding broad behavior under one test
obligation, omitting behavior surfaces such as persistence or transport, or
failing to show which surfaces were considered and intentionally scoped out.

The model should own decomposition and strategy. The harness should own the
contract that says a top-level plan is not approval-ready until behavior,
variants, invariants, tests, verification, and scope decisions are explicit and
traceable.

## Generic Readiness Standard

A feature plan is approval-ready only when each user-facing behavior,
externally observable contract, and risky invariant is represented by:

- a requirement or acceptance reference;
- at least one BDD scenario with concrete example data;
- variants covering relevant positive, negative, boundary, and stateful cases;
- invariants with property or targeted test refs when invariants are claimed;
- failure modes with mitigation test refs when failure modes are claimed;
- a TDD obligation with a concrete test artifact and verification command;
- an implementation atom that names the mutation scope needed to satisfy it;
- an explicit scope decision for relevant behavior surfaces.

If the planner cannot know whether a surface is relevant, it should open a
typed question or mark the surface as `open_question`. `open_question` is a
structured non-ready state: it preserves the ambiguity for review or repair,
but it does not satisfy approval readiness. If a surface is intentionally
excluded, it should mark it `not_required` or `waived` with a reason. It should
not silently omit common surfaces.

## Behavior Surfaces

The readiness contract must be language-neutral and task-type-neutral. The
planner should consider these generic surfaces:

| Surface | Purpose | Coverage connection |
| --- | --- | --- |
| Domain logic | Rules, calculations, state transitions, decision tables | requirements, scenarios, invariants, state machines, decision tables |
| API / transport | HTTP, gRPC, CLI, SDK, events, queues, workers | provider contracts, e2e journeys, design contracts |
| Persistence | databases, files, cache, migrations, durability | scenarios, state machines, failure modes, e2e journeys |
| Integration / providers | external APIs, SDKs, model providers, queues | provider contracts, failure modes, observability |
| UI / interaction | screens, commands, accessibility, empty/loading/error states | e2e journeys, scenarios, failure modes |
| Security | authorization, validation, injection, abuse cases | security cases, boundary values |
| Privacy | data classes, retention, redaction, secrets | privacy controls, security cases |
| Observability | logs, metrics, traces, audit events, redaction | observability entries, verification gates |
| Performance / scale | latency, throughput, memory, batch size, degradation | performance bounds, measurement refs |
| Concurrency / idempotency | races, duplicate messages, ordering, retries | invariants, state machines, failure modes |
| Compatibility / migration | backward compatibility, data migration, versioning | e2e journeys, failure modes, provider contracts |
| Testability / verification | commands, fixtures, deterministic setup, red/green gates | test obligations, verification gates |

## Boundaries And Owners

| Boundary | Canonical owner | Required connection |
| --- | --- | --- |
| Prompt schema | `internal/workflows/featureplanner/prompt_schema.go` and planner prompt construction | Tell the model to emit generic surface inventory and complete BDD/TDD coverage. |
| Standalone coverage data | `internal/workflows/featureplanner/types.go` | Store provisional surface decisions and coverage artifacts in `BehavioralCoverageDraft`. |
| Standalone validation | `internal/workflows/featureplanner/validate.go` and `standalone_coverage_contract.go` | Reject approval when required surface decisions or traceability are missing. |
| Product coverage contract | `internal/workflows/productworkflow/behavioral_coverage_contract.go` | Keep accepted and standalone coverage semantics aligned. |
| Repair routing | `internal/workflows/featureplanner/orchestrate.go` | Treat coverage gaps as model-repairable unless they require operator/source authority. |
| Plan projection | `internal/workflows/featureplanner/plan_projection.go`, `plan_traceability_projection.go`, and `plan_coverage_presentation.go` | Show surface decisions and traceability gaps in the plan tab. |
| TUI plan tab | `internal/surfaces/tui/app/feature_planner_plan_tab.go` and feature-planner product tests | Render the structured information, not raw diagnostic blobs. |
| Scripted behavior tests | `internal/workflows/featureplanner/validate_test.go` and `internal/surfaces/tui/app/feature_planner_product_test.go` | Prove generic behavior across a representative plan and user-visible flow. |

## Implementation Checklist

- [x] Add a generic surface inventory model to `BehavioralCoverageDraft`.
- [x] Project standalone surface inventory into the canonical product coverage contract.
- [x] Validate each surface inventory item has a stable surface name, status, and decision rationale.
- [x] Require standalone plans to include surface inventory before approval.
- [x] Require every `in_scope` surface to have at least one connected TDD obligation; evidence refs may support the decision but cannot replace coverage.
- [x] Allow `not_required` and `waived` surface decisions without forcing fake tests when they include rationale.
- [x] Preserve `open_question` as a typed non-ready decision that blocks approval until resolved, waived, or converted into in-scope coverage.
- [x] Keep coverage-only failures model-repairable and preserve existing operator/source authority routing.
- [x] Add plan-tab presentation for surface decisions and missing/gated coverage.
- [x] Add traceability projection counts that make broad one-test plans visibly weak.
- [x] Update prompt/schema guidance so the model owns the strategy but must consider generic surfaces.
- [x] Add unit tests for valid, missing, in-scope-uncovered, waived, and open-question surface decisions.
- [x] Add scripted CLI/TUI regressions showing dispatch and plan-tab behavior remain blocked or explicit until the surface/BDD matrix is complete.
- [x] Run focused feature-planner/TUI tests and full `go test ./...`.

## Review Checklist

Before implementation is considered accepted:

- [x] Reviewer confirms the canonical owner is the coverage contract and
  standalone adapter, not a TUI-only or prompt-only rule.
- [x] Reviewer confirms the surface list is generic and does not hardcode the
  LLM-brain example.
- [x] Reviewer confirms validation does not force model strategy, paths,
  transport, or storage choices.
- [x] Reviewer confirms missing external authority still opens typed questions
  instead of leaking diagnostics.
- [x] Reviewer confirms tests include negative oracles for over-broad gates.

## Completion Evidence

Completion requires current evidence for each item:

- implementation diffs in the canonical owners above;
- unit tests covering the validation matrix;
- scripted TUI/e2e coverage for the user-visible plan tab and repair state;
- reviewer sign-off after the last material revision;
- focused package tests and full `go test ./...` passing.

Current verification:

- `go test ./internal/workflows/productworkflow ./internal/workflows/featureplanner`
- `go test ./internal/surfaces/tui/app -run 'TestFeaturePlannerPlanTabHidesRawSummaryAndRendersStructuredCoverage|TestFeaturePlannerPlanTabRenderedPanelShowsStructuredCoverageOnly|TestPlanPanel.*Collapse|TestPlanPanel.*Mouse|TestPlanPanel.*Heading'`
- `go test ./internal/surfaces/tui/app -run 'TestFeaturePlannerTUI|TestFeaturePlannerCommand|TestFeaturePlannerPlanTab|TestFeaturePlannerProduct'`
- `go test ./internal/surfaces/cli/featureplanner`
- `go test ./internal/workflows/productworkflow ./internal/workflows/featureplanner ./internal/surfaces/tui/app -run 'TestBehavioralCoverageContract|TestValidatePlanStandaloneWorkspaceContext|TestFeaturePlanTraceabilityReview|TestFeaturePlannerPlanTab|TestPlanPanel.*Collapse|TestPlanPanel.*Mouse|TestPlanPanel.*Heading'`
- `go test ./internal/workflows/productworkflow ./internal/workflows/featureplannerproduct ./internal/workflows/featureplanner`
- `go test ./internal/workflows/productworkflow`
- `go test ./internal/workflows/featureplanner ./internal/workflows/featureplannerproduct ./internal/surfaces/tui/app -run 'TestAcceptedCoveragePresentation|TestFeaturePlanCoveragePresentation|TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft|TestFeaturePlanTraceabilityReview|TestValidatePlanStandaloneWorkspaceContext'`
- `go test ./...`

Reviewer sign-off:

- General reviewer: approved after duplicate/conflicting surface decisions were rejected canonically and accepted product surfaces round-tripped.
- Root-cause reviewer: approved after accepted handoff blocked real unresolved surface gates while preserving mutation unavailable-tool waiver semantics.
