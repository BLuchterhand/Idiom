# BDD-TDD Production Readiness Requirements

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: P0 automated BDD-TDD gates implemented; production declaration requires
the release checklist evidence below to stay green, 2026-06-22

## Purpose

This document defines the requirements Idiom's BDD-TDD flow must continue to
satisfy to remain production-ready.

The scope is the integrated path from a product or user feature request through
feature planning, BDD coverage, TDD obligations, approval, implementation
handoff, verification, and audit. It does not replace the target design in
[Behavioral TDD Coverage](design-tdd-coverage.md) or the authority bridge in
[Feature-Planner Product Workflow Integration](design-feature-planner-product-workflow-integration.md).
It turns those designs into release gates.

## Lead Verdict

The BDD-TDD flow is production-ready only while the gates in this document pass
against current automated evidence and a fresh feature-planner acceptance run
does not contradict them.

The 2026-06-22 live Brain greenfield run contradicted the previous readiness
claim. Its rendered plan showed `0` variants, `0` domain invariants, `0`
concrete test cases, and every behavior missing concrete tests. The persisted
candidate only carried narrow TDD obligations with `id`, `behavior`, `command`,
and `required_before`; it did not carry Given/When/Then scenarios, mapped
invariants, concrete test obligations, or a reviewable traceability matrix.

The root gap was not only presentation. The richer product workflow behavioral
coverage contract existed, and the feature-planner renderer could display
coverage when present, but standalone feature planning did not require the
provisional `behavioral_coverage_draft` contract. A model could therefore
submit a TDD-only plan that looked structurally valid enough to surface to the
user while failing the BDD-TDD intent.

As a technical lead, I would declare this production-ready only when a reviewer
can inspect one canonical planning artifact from both standalone and
product-workflow-backed runs and see:

- the factual feature intent
- the accepted requirements and assumptions
- concrete BDD scenarios
- explicit variants and invariants
- mapped TDD obligations
- executable verification commands
- plan revision, approval, resume, execution, and verification state
- audit evidence proving those states came from the harness, not from loose
  model prose

The requirements below are binding release gates. The 2026-06-22 repair moved
the missing P0 hooks into typed contracts: standalone coverage authority,
artifact-bound approval, release-readiness decisions, verification closures,
plan-tab projection, and audit lifecycle summaries. If any gate regresses, the
BDD-TDD flow reverts to experimental.

## Production-Ready Definition

Production-ready means a feature request can move through this lifecycle without
losing authority, coverage, or user clarity:

```text
request
  -> requirements and assumptions
  -> BDD behavior inventory
  -> variants and invariants
  -> TDD obligations
  -> plan artifact
  -> user review and approval
  -> implementation handoff
  -> verification
  -> audit and release decision
```

The model owns the strategy inside that lifecycle. It can ask clarifying
questions, propose assumptions, choose a design, and patch the plan. The harness
owns the contracts: schema validity, authority, evidence freshness, path
permissions, coverage gates, approval scope, state persistence, and
verification closure.

Production readiness is stricter than implementation dispatch readiness. A
plan may be dispatchable when required tests are planned, approved, and bound to
pre-execution red-test evidence. It is not release-ready until required
verification commands have executed, verification closures are recorded, and
audit can correlate the closed obligations back to the accepted artifact
revision and digest.

## Non-Goals

- Do not add timeout or budget fixes to make this flow appear more responsive.
  Idiom is expected to take a while on large planning and implementation work.
- Do not force the model to pick a specific strategy, outline, next probe, or
  answer timing when multiple valid strategies remain.
- Do not treat path names, search hits, packets, or summaries as source-body
  evidence when a contract requires source-body support.
- Do not solve user-facing plan quality by adding scenario-specific prompt
  nudges. Fix the shared artifact, state, validation, and rendering contracts.

## Current Hookup Audit

Current implementation surfaces:

- `internal/workflows/productworkflow/behavioral_coverage_contract.go` owns the
  accepted rich behavioral coverage schema for product-workflow-backed efforts.
- `internal/workflows/featureplanner/types.go` owns the feature-planner plan
  schema, including product `accepted_coverage` and standalone provisional
  `behavioral_coverage_draft`.
- `internal/workflows/featureplanner/orchestrate.go` owns the model-facing
  feature-planner protocol and must advertise the same fields the validators
  require.
- `internal/workflows/featureplanner/validate.go` owns plan acceptance and must
  reject standalone plans that do not provide provisional BDD coverage.
- `internal/surfaces/tui/app/feature_planner_plan_tab.go` owns user-facing plan
  projection and must render traceability from the canonical artifact rather
  than inventing or hiding coverage state.

The standalone path is production-suitable only when:

- every submitted plan includes `behavioral_coverage_draft` unless product
  workflow has supplied accepted coverage;
- every TDD obligation maps to at least one BDD scenario, one invariant, and one
  concrete test obligation;
- the plan tab shows actual IDs and paths in the traceability matrix, not only
  counts; and
- a real run that omits those fields is blocked before approval or dispatch.

Implemented P0 hooks after the 2026-06-22 repair:

- Full lifecycle scripted coverage now exercises standalone coverage authority,
  approval-bound artifact stamping, implementation handoff, verification
  closure, release-readiness projection, resume, chat interactivity, and
  plan-tab rendering.
- Artifact parity is enforced by artifact revision, digest, workspace
  fingerprint, approval event, and verification closure fields on durable
  state, executor handoff, telemetry events, plan-tab projection, and audit.
- Audit summarizes feature-planner lifecycle events without raw payload leakage
  and flags missing artifact, approval, verification closure, coverage
  authority, and release-decision bindings.
- Release readiness is a typed harness-owned decision over the accepted plan
  artifact, terminal atom states, verification closures, approval, open
  questions, open blockers, active runs, and verification command results. It
  can be dispatch-ready while still release-blocked.

Fresh live acceptance remains useful before broad rollout because the original
Brain failure came from model behavior in a real greenfield run. The automated
contract now blocks the missing-coverage shape that caused that failure, but a
new live run is the final rollout check, not a substitute for the typed gates.

## Remaining P0 Requirements

P0 requirements block a production-readiness declaration.

### 1. Canonical Planning Artifact

There must be one canonical feature-planning artifact for the BDD-TDD flow.
Derived views may exist, but they must identify the artifact revision and
digest they came from.

Required fields:

- feature intent and user objective
- accepted requirements
- open questions and assumptions
- BDD scenarios with Given/When/Then content
- behavior variants
- invariants
- failure modes and recovery expectations
- TDD obligations
- test artifact paths or planned test artifact paths
- verification commands
- source and packet evidence references
- approval state
- execution handoff state
- verification result state
- artifact revision, digest, and workspace fingerprint

The plan tab, CLI summaries, executor handoff, and durable JSON files must not
drift from each other. If there are timestamped plan files and a `last.plan`
pointer, the pointer must identify the same revision and same authority fields
that the UI and executor use.

### 2. Explicit BDD Coverage

BDD coverage must be concrete enough for engineering review. A plan cannot say
"add tests for coverage" and pass.

Each accepted requirement must map to one or more BDD scenarios. Each scenario
must include:

- scenario ID
- requirement reference
- Given/When/Then steps with concrete conditions
- expected observable outcome
- covered variant or invariant IDs
- status: proposed, accepted, blocked, implemented, or verified

The coverage set must include the normal path, boundaries, invalid input,
permission or authority behavior, persistence or resume behavior, user-facing
interaction behavior, observability, and security or privacy behavior when
those domains are relevant to the feature.

### 3. Variants And Invariants

Variants and invariants must be first-class records, not buried in prose.

Variants describe behavior combinations that need coverage, such as:

- accepted source present versus missing
- approved plan versus rejected plan
- fresh evidence versus stale evidence
- greenfield project versus existing project
- initial request versus follow-up request
- resumed session versus new session
- UI plan view versus CLI output

Invariants describe properties that must always hold, such as:

- chat output does not leak model-only repair or harness text
- approval applies to a specific plan revision and digest
- resume restores the correct lane and planning state
- execution cannot proceed with open required coverage gates
- user-facing plan rendering does not change execution authority
- source-body obligations cannot be satisfied by summaries alone

Every variant and invariant must map to at least one BDD scenario or explicit
test obligation.

### 4. TDD Obligation Mapping

Every accepted BDD scenario and invariant must map to TDD obligations before
implementation dispatch.

Each TDD obligation must include:

- obligation ID
- linked requirement, scenario, variant, or invariant IDs
- test type: unit, integration, scripted e2e, regression eval, mutation, or
  manual-release check
- required test file or artifact path, when known
- executable command
- expected pre-implementation state: red test, characterization test,
  regression fixture, or approved planned test
- required status before execution
- required status before release

The gate must distinguish "test planned" from "test exists and ran." Both can
be valid at different stages, but they are not the same state.

### 5. Model Plan Refinement

The model must have a typed way to refine the plan artifact.

The model should be able to:

- ask user-facing clarification questions
- propose assumptions when the request is broad
- add, remove, or revise requirements
- add or revise BDD scenarios
- add variants and invariants
- propose TDD obligations
- explain the plan in normal chat without changing artifact authority

The harness must validate any proposed plan patch for schema, permissions,
authority, evidence references, and coverage completeness. Rejected patches
must produce user-facing recovery that names the contract issue without leaking
internal prompt text or repair scaffolding.

### 6. Approval And Resume State

Approval must bind to a specific artifact revision, digest, and workspace
fingerprint. A loose "approved" flag is not enough.

The system must support:

- approving the current plan revision
- rejecting or requesting changes
- continuing a conversation after a clarification question
- resuming the session in the feature-planner lane
- preserving the current plan revision across restarts
- rejecting stale approvals after artifact or workspace drift
- preventing an older plan artifact from replacing a newer accepted state

The resume path must be tested as a core workflow, not as a convenience path.

### 7. Implementation Handoff

The implementation handoff must carry the BDD-TDD contract, not just edit
targets.

Executor input must include:

- required edit paths
- allowed edit paths
- read-only evidence paths
- accepted requirements
- accepted scenarios
- variants and invariants under test
- required TDD obligations
- required verification commands
- open blockers, if any
- plan revision and digest

Execution must be blocked when required coverage, approval, evidence, or path
authority is open or stale.

### 8. Verification Closure

Verification obligations must close before release readiness.

For each required command or scripted check, the artifact must record:

- command
- start and end time
- exit status
- relevant output summary
- linked obligations
- failure classification when the command fails
- retry or recovery state, if any

A plan with open verification obligations can be a draft, but it cannot be
called production-ready or release-ready.

### 9. User-Facing Plan Presentation

The plan tab must make engineering review easier than reading raw JSON or dense
model prose.

Required presentation behavior:

- render the canonical artifact, not an independent text blob
- order sections by review flow: objective, status, questions, requirements,
  BDD scenarios, variants, invariants, TDD obligations, execution, verification,
  audit
- make major sections collapsible
- default to showing status and open blockers first
- hide or de-emphasize unstructured raw text unless explicitly expanded
- show IDs and statuses clearly enough to discuss them in review
- preserve markdown where the artifact intentionally carries markdown
- keep chat free for normal model communication and clarification

The UI must not be the source of authority. It is a projection of the canonical
artifact.

### 10. Audit, Telemetry, And Evals

Audit output must prove the lifecycle without hand-reconstructing raw
transcripts.

Required audit facts:

- plan artifact ID, revision, digest, and workspace fingerprint
- product workflow or feature-planner source context, when present
- BDD coverage gate status
- TDD obligation gate status
- open question events and answer events
- approval, rejection, and revision events
- resume lane and state restoration events
- execution handoff event
- verification command events
- final release decision

Regression evals must cover the previous failure classes:

- leaked internal or model-only text in chat
- template-only plan explanation
- broad MVP request that needs assumptions or questions
- follow-up request about TDD coverage
- feature-planner resume returning to the wrong lane
- stale or garbage plan replacement
- missing or drifted packet/plan evidence
- open verification obligations being treated as done
- exact path authorization confused with requirements clarification

## P1 Requirements

P1 requirements do not block an initial production declaration if the P0 gates
are satisfied, but they should be completed before broad rollout.

- Coverage technique expansion from basic BDD into decision tables, state
  models, failure-mode analysis, and mutation verification for high-risk
  contracts.
- Coverage-delta reporting that shows what changed between plan revisions.
- Plan artifact diff UI for reviewer approval.
- Test quality scoring beyond command success, including mutation score or
  equivalent constraint checks where practical.
- Historical learning from execution failures back into coverage obligations.
- Better cross-linking from plan sections to source evidence and test results.

## Required Test Matrix

Before production readiness, the following test coverage must exist.

| Area | Required tests |
| --- | --- |
| Artifact schema | Unit tests for required fields, revisioning, digest binding, status transitions, and invalid patches |
| Coverage mapping | Unit tests mapping requirements to BDD scenarios, variants, invariants, and TDD obligations |
| Question handling | Unit and integration tests for clarification questions, path-authorization questions, rejected questions, and user answers |
| State persistence | Integration tests for restart, resume, stale artifact rejection, lane restoration, and plan revision preservation |
| Approval | Integration tests for approve, reject, revise, approve-after-drift rejection, and approval digest binding |
| UI projection | Scripted e2e tests for collapsible sections, ordering, markdown rendering, hidden raw text, open blockers, and status rendering |
| Chat behavior | Scripted e2e tests proving chat contains normal user-facing explanation or questions and never internal repair scaffolding |
| Handoff | Integration tests proving executor receives BDD-TDD obligations and blocks on open gates |
| Verification | Integration tests for passing commands, failing commands, recovered commands, and open verification obligations |
| Audit and evals | Regression fixtures for all known leaked-output, template-only, resume, stale-state, and drift failures |

## Release Gate Checklist

| Gate | Pass criterion | Evidence |
| --- | --- | --- |
| Canonical artifact | One artifact revision drives UI, chat references, approval, handoff, and audit | `TestFeaturePlannerPlanTabRendersCanonicalArtifactAndApproval`, `TestPlanExecutionContractCarriesApprovedPlanArtifactBinding`, `TestValidateDurablePlanRejectsApprovalAfterPlanDigestDrift` |
| BDD coverage | Every accepted requirement maps to concrete scenarios | `TestValidatePlanStandaloneWorkspaceContextRequiresProvisionalCoverageDraft`, `TestValidatePlanStandaloneWorkspaceContextRequiresEveryTDDObligationMappedByCoverageDraft`, product workflow coverage-gate tests |
| Variants and invariants | Every listed variant and invariant maps to tests or scenarios | feature-planner draft/accepted coverage validation tests and product workflow traceability matrix tests |
| TDD obligations | Every scenario and invariant has executable or approved planned tests | `TestRunStandaloneRejectsNarrowTDDOnlyPlanBeforeDispatch`, `TestDispatchStopsAfterPreDispatchTDDBlocker`, terminal evidence contract tests |
| Model refinement | Model can patch or replace the plan through a validated contract | feature-planner follow-up, revision, and open-question tests under `internal/surfaces/tui/app/feature_planner_product_test.go` |
| Approval | Approval binds to revision, digest, and workspace fingerprint | `TestValidateDurablePlanRejectsApprovalAfterPlanDigestDrift`, artifact event assertions in durable tests |
| Resume | Feature-planner lane and plan state restore correctly | `TestFeaturePlannerTUIResumeE2EFollowupQuestionReceivesActivePlanContext`, session restore tests |
| Handoff | Executor receives exact BDD-TDD contract and blocks on open gates | `TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute`, `TestDispatchRecordsPlanExecuteLaneFamily`, plan execution contract tests |
| Verification | Required commands run and close linked obligations | `TestApplyImplementorOutputAcceptsObservedPlanExecuteCompletion`, `TestReplayRestoresVerificationClosureFromTerminalEvent`, `TestValidateDurableStateRequiresVerificationClosureForApprovedTerminalAtom` |
| Release readiness | Open or planned verification cannot be called release-ready | `TestEvaluateReleaseReadinessBlocksOpenVerificationObligations`, `TestApplyImplementorOutputAcceptsObservedPlanExecuteCompletion` |
| UI | Plan tab is readable, ordered, collapsible, and artifact-backed | plan-tab rendering and collapse tests in `internal/surfaces/tui/app/feature_planner_product_test.go` |
| Chat | Chat remains interactive and free of internal garbage text | feature-planner chat sanitizer, open-question, and repair-to-approval tests |
| Audit | Harness audit surfaces lifecycle findings without raw leakage | `TestHarnessAuditBehaviorSummarizesFeaturePlannerLifecycleWithoutRawPayloadLeakage`, `TestHarnessAuditBehaviorFlagsFeaturePlannerLifecycleMissingBindings`, `TestHarnessAuditBehaviorFlagsFeaturePlannerMissingCoverageAuthorityWithoutRawLeakage` |
| Build and tests | Repo-wide tests and build pass | `go test ./...` and `go build ./cmd/idiom` |

## Release Decision

The full BDD-TDD flow is production-ready while the following gates continue to
pass with automated evidence:

1. a canonical artifact captures the complete BDD-TDD contract,
2. the model can refine that artifact without bypassing harness validation,
3. approval and resume are bound to artifact state,
4. execution and verification consume the same contract, and
5. scripted e2e tests prevent the prior leaked-output, stale-state, drift, and
   vague-coverage failures from recurring.

If any gate fails, the release decision reverts to experimental until the
contract, projection, or test gap is fixed.
