# Feature-Planner Product Workflow Integration

**North Star: AI coding agents are becoming common, but teams do not yet trust their context, decisions, or outputs. Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.**

Status: accepted design, drafted 2026-05-28

## Purpose

This document defines the integration between the upstream product workflow
pipeline (`internal/productworkflow/`) and the feature-planner orchestration
(`internal/featureplanner/`). Currently these systems are islands with zero
bridge code. The feature-planner's `BuildPlannerPrompt` forces the model to
produce SDD refs it cannot possibly know, because no effort context, artifact
paths, or PRD/ADR/TDD IDs are injected into the prompt. The model hallucinates
references like `docs/design/github-tools.md` as a predictable consequence.

This design defines the typed bridge, the authority boundary, and the
implementation atoms needed to close the gap.

## Design Basis

This integration is grounded in:

- [PRD To ADR To Brief Research Workflow](design-prd-adr-brief-research-workflow.md),
  which defines the upstream pipeline: research evidence → PRD → ADR → TDD
  obligations → coverage gate → brief → lane dispatch.
- [Feature Planner Orchestration](design-feature-planner-orchestration.md),
  which defines the downstream orchestrator with SDD/TDD dispatch contracts.
- [Design TDD Coverage](design-tdd-coverage.md), which defines coverage tiers
  and obligation types.
- [Decisioning North Star](decisioning-north-star.md), which defines the
  model-strategy vs harness-contract boundary.

Five independent reviewers (Incident Root-Cause, Prompt/Protocol, TDD/Acceptance,
North-Star/Architecture, and Harness/Go) reviewed the gap on 2026-05-28 and
converged on the diagnosis and fix direction summarized here.

## Controlling Policy

- The model owns strategy. The harness owns contracts.
- The product workflow pipeline is the single canonical owner of SDD/TDD
  artifact authority.
- The feature-planner is a consumer of that authority.
- The product workflow gate evaluates whether PRD/ADR/TDD evidence is
  sufficient. The feature-planner dispatch gate evaluates whether atoms cite
  that canonical evidence. Two distinct questions, two distinct owners, one
  authority chain.
- The feature-planner must remain usable standalone (without product workflow
  context) for ad-hoc feature planning. Product workflow context is an optional
  enrichment, not a hard dependency.

## Integration Contract

### Pipeline Handoff

```
Product Workflow Pipeline:
  Research → PRD → ADR → TDD Obligations → Coverage Gate → Brief → Brief Lint
                           ↓
  Feature-Planner Intake (NEW STEP)
                           ↓
  Feature Planner → Atomize → Dispatch → Implement → Verify → Reconcile
```

The handoff point is after Brief Lint/Intake and before lane dispatch. The
feature-planner receives a `ProductWorkflowBrief` carrying:

- `effort_id`, `effort_root`, `artifact_root_path`
- PRD requirement IDs, priorities, statements, and TDD obligation refs
- Accepted ADR IDs and paths
- TDD obligation IDs, behaviors, commands, statuses
- Coverage tier and gate pass/fail status
- Brief-level edit scope (files to modify, required behavior, tests, constraints)

### Authority Boundary

| Question | Owner |
|---|---|
| Are PRD/ADR/TDD artifacts sufficient? | Product workflow coverage gate |
| Does the feature plan cite canonical artifacts? | Feature-planner `ValidatePlan` |
| Are atom TDD pre-execution fields complete? | Feature-planner `MissingPreDispatchTDD` |
| Does the implementor produce correct code? | Model strategy (plan-execute lane) |

### Schema Changes

**FeatureState** gains optional fields:

- `effort_id` (string) — product workflow effort identity
- `effort_root` (string) — path to the effort folder

**SDDContract** gains structured fields (alongside existing `refs` and
`acceptance`):

- `prd_requirement_refs` ([]string) — PRD requirement IDs
- `adr_refs` ([]string) — ADR IDs

**CoverageGateResult** (new type, featureplanner-local):

- `status` (string) — `"pass"` or `"blocked"`, translated from
  `productworkflow.CoverageGateEvaluation.Status`
- `effort_id` (string) — product workflow effort identity for audit trail
- `coverage_tier` (string) — coverage tier at time of evaluation
- `checked_at` (string) — ISO 8601 timestamp

This type must remain a plain data struct with no import of `productworkflow`.

**TDDContract** gains:

- `coverage_gate_event_id` (string) — upstream coverage gate event for audit
  trail. Distinct scope from the existing per-atom
  `TestFirstPlan.CoverageGateEventID` and per-execution
  `TDDPreExecutionEvidence.CoverageGateEventID` — this is the plan-level
  binding to the upstream product workflow gate.

**FeaturePlan** gains:

- `coverage_gate_result` (optional CoverageGateResult) — pass/fail from upstream

### Backward Compatibility

Legacy `sdd_contract.refs` with raw file paths (e.g., `docs/path.md:line`) are
treated as soft acceptance criteria during validation. New plans should use
`prd_requirement_refs` and `adr_refs` for structured artifact references.
`FeaturePlan.SchemaVersion` bumps to `idiom.feature_plan.v2`.
`ValidateDurablePlan` must accept both `v1` and `v2` schema versions.
Plans with `v1` schema and legacy `refs` entries are treated as acceptance
criteria; plans with `v2` schema must use structured refs.

## Implementation Atoms

### Atom 1: ProductWorkflowBrief type and RunOptions integration

**Scope**: Add `ProductWorkflowBrief`, `PRDRequirementBrief`, `TDDObligationBrief`
types to `internal/featureplanner/product_context.go`. Add optional
`ProductWorkflowBrief` pointer to `RunOptions`. Thread it through `Run()` to
`BuildPlannerPrompt()`.

**Files**:

- `internal/featureplanner/product_context.go` (new)
- `internal/featureplanner/orchestrate.go` (RunOptions, Run signature)

**Tests**:

- `internal/featureplanner/product_context_test.go` (new) — JSON round-trip for
  ProductWorkflowBrief with minimal and full payloads

### Atom 2: BuildPlannerPrompt product context injection

**Scope**: When `ProductWorkflowBrief != nil`, inject a "Product Workflow
Context" section into the planner prompt listing real PRD requirements, ADR
IDs, TDD obligations, coverage gate status, and edit scope. Replace the
hardcoded example `docs/path.md:line` with the instruction to reference only
IDs from the provided context. When brief is nil, the hardcoded `docs/path.md:line` example
is replaced with an empty `"refs":[]` plus an `"acceptance":[...]`
placeholder, with instruction that acceptance criteria strings are valid.
This removes the hallucination template in both branches rather than
trying to counter-steer with prose.

When `CoverageGateResult.Status != "pass"`, the prompt adds a nudge:
"The coverage gate has NOT passed. Do not produce a feature plan; instead,
use ask_planner_question to report the blocked gate."

**Files**:

- `internal/featureplanner/orchestrate.go` (BuildPlannerPrompt)

**Tests**:

- `TestBuildPlannerPrompt_WithProductContext` — prompt contains PRD req IDs,
  ADR IDs, TDD obligation IDs, coverage gate status, no hardcoded example path
- `TestBuildPlannerPrompt_WithoutProductContext` — backward compatible, prompt
  mentions acceptance criteria as valid alternative

### Atom 3: CLI flags for effort context

**Scope**: Add `--effort-root` and `--effort-id` flags to
`parseFeaturePlannerOptions`. In `defaultHeadlessFeaturePlanner`, when either
flag is set, resolve the effort root, load `workflow_state.json`, parse
`prd.md`, `adrs/index.json`, `tdd.json`, run `EvaluateCoverageGate()` and
`GenerateImplementationBrief()`, populate a `ProductWorkflowBrief`, and pass it
through `RunOptions`.

**Files**:

- `internal/featureplannercmd/feature_planner.go` (flags, loading logic)

**Tests**:

- `TestCLI_EffortRootFlag` — flag parsing
- `TestCLI_EffortRootLoadsProductWorkflowBrief` — integration test with fixture
  effort folder

### Atom 4: SDDContract schema upgrade

**Scope**: Add `prd_requirement_refs` and `adr_refs` to `SDDContract`. Add
`coverage_gate_event_id` to `TDDContract`. Add `coverage_gate_result` to
`FeaturePlan`. Bump `FeaturePlanSchemaVersion` to `idiom.feature_plan.v2`.
Add migration path for legacy `refs` entries in `ValidateDurablePlan`.

**Files**:

- `internal/featureplanner/types.go` (SDDContract, TDDContract, FeaturePlan,
  new CoverageGateResult)
- `internal/featureplanner/validate.go` (ValidateDurablePlan migration)

**Tests**:

- `TestSDDContract_V2_RoundTrip` — JSON round-trip with new fields
- `TestValidatePlan_AcceptsStructuredSDDRefs` — plan with PRD req refs passes
- `TestValidatePlan_MigratesLegacyRefs` — legacy refs treated as acceptance

### Atom 5: SDD ref file-existence validation

**Scope**: Add `ValidatePlanSDDRefs(plan FeaturePlan, artifactRoot string) error`
that resolves SDD refs against the artifact root and checks file existence.
Call it from the CLI layer after `ValidateDurablePlan` passes (keeps
`featureplanner` free of filesystem dependencies). Reject plans with refs that
don't resolve.

**Files**:

- `internal/featureplanner/validate.go` (new validation function)
- `internal/featureplannercmd/feature_planner.go` (call after validation)

**Tests**:

- `TestValidatePlanSDDRefs_ExistingFile` — ref to real file passes
- `TestValidatePlanSDDRefs_MissingFile` — ref to nonexistent file returns error
- `TestValidatePlanSDDRefs_StripsLineSuffix` — `docs/prd.md:42` resolves to
  `docs/prd.md`

### Atom 6: TDD obligation cross-reference validation

**Scope**: Add `ValidatePlanTDDObligations(plan FeaturePlan, knownIDs map[string]bool) error`
that verifies every TDD obligation ID in the plan and every atom's
`TestFirst.ObligationRefs` references a known obligation from the product
workflow ledger. Call it from the CLI layer.

**Files**:

- `internal/featureplanner/validate.go` (new validation function)
- `internal/featureplannercmd/feature_planner.go` (call after validation)

**Tests**:

- `TestValidatePlanTDDObligations_AllKnown` — all refs match known IDs
- `TestValidatePlanTDDObligations_UnknownRef` — unknown ref returns error
- `TestValidatePlanTDDObligations_AtomObligationRefsChecked` — atom-level
  obligation refs also validated

### Atom 7: Coverage gate intake validation

**Scope**: Add validation in `ValidatePlan` that blocks when
`CoverageGateResult != nil && Status != "pass"`. This enforces the authority
boundary: the feature-planner cannot dispatch atoms for an effort whose
coverage gate never passed.

**Files**:

- `internal/featureplanner/validate.go` (ValidatePlan)

**Tests**:

- `TestValidatePlan_RejectsBlockedCoverageGate` — blocked gate → validation error
- `TestValidatePlan_AcceptsPassedCoverageGate` — passing gate → no error
- `TestValidatePlan_AcceptsNilCoverageGate` — nil gate (standalone mode) → no error

### Atom 8: End-to-end integration test

**Scope**: Create a fixture effort folder with `workflow_state.json`, `prd.md`,
`adrs/`, `tdd.json`. Run `idiom feature-planner --effort-root <fixture>` and
verify the planner prompt contains real artifact IDs, the submitted plan
passes SDD ref validation, and atom TDD obligations match the ledger.

**Files**:

- `testdata/feature-planner-pw-integration/` (fixture effort folder)
- `internal/featureplannercmd/feature_planner_test.go` (integration test)

**Tests**:

- `TestFeaturePlanner_ProductWorkflowIntegration_EndToEnd` — full pipeline:
  effort load → prompt injection → plan validation → atom dispatch

### Atom 9: FeatureState effort identity persistence

**Scope**: Add `effort_id` and `effort_root` to `FeatureState`. Persist them
in `SaveState` and `LoadState`. Include them in feature plan JSON output. This
makes the relationship auditable: a feature plan can be replayed and verified
against the product workflow state that existed at planning time.

**Files**:

- `internal/featureplanner/types.go` (FeatureState)
- `internal/featureplanner/store.go` (SaveState, LoadState)

**Tests**:

- `TestFeatureState_EffortIdentityRoundTrip` — save/load preserves effort_id
  and effort_root
- `TestFeatureState_StandaloneNoEffortIdentity` — nil fields survive round-trip

## Non-Goals For This Effort

- The product workflow pipeline's own completion (PRD/ADR generation, TDD
  synthesis, coverage gate hardening) — those are tracked in the
  `prd-adr-brief-workflow` effort.
- TDDObligation schema unification between `productworkflow` and
  `featureplanner` — the `TDDObligationBrief` translation at the CLI boundary
  is sufficient for this integration. Full unification is deferred.
- TUI continuation/handoff for the product workflow — tracked separately.
- Sidecar and MCP exposure of product workflow state — tracked separately.

## Verification

- Default verification: `go test ./internal/featureplanner/... ./internal/featureplannercmd/...`
- After Atom 8: `make build && make test`
- Scripted contract tests: `make eval LOGS=testdata/scripted-contracts/generated-logs/scripted-feature-planner-*.jsonl`

## Risk Assessment

- **North-star risk: Low.** The integration narrows the gap between "the model
  guesses what to build" and "the model references grounded product evidence."
  The optional `ProductWorkflowBrief` pointer preserves standalone mode.
- **Schema migration risk: Low.** `SDDContract` gains additive fields. Legacy
  `refs` entries are treated as acceptance criteria. No existing test fixtures
  use structured SDD refs.
- **Dependency risk: Low.** The `featureplanner` package does not import
  `productworkflow`. Translation happens at the `featureplannercmd` CLI layer.

