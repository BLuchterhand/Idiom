# Feature-Planner Product Workflow Integration

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

This document is the tracked implementation plan for connecting the product
workflow pipeline (`internal/workflows/productworkflow/`) to the feature-planner
(`internal/workflows/featureplanner/`).

## Design Authority

The canonical design contract is
[docs/design-feature-planner-product-workflow-integration.md](/Users/ben/Documents/GitHub/idiom/docs/design-feature-planner-product-workflow-integration.md).
This file tracks implementation progress against that design through discrete
atoms.

## Current State

- Effort ID: `feature-planner-pw-integration`
- Status bucket: `started`
- Current phase: `phase-0-planning`
- Context pack: not yet generated
- Coverage tier: `behavior_standard`

## Implementation Atoms

Each atom is one bounded work unit with explicit scope, SDD refs, TDD
obligations, and verification commands. Atoms are sequential — each must
complete before the next dispatches.

---

### Atom 1: ProductWorkflowBrief type and RunOptions integration

| Field | Value |
|---|---|
| ID | `impl-0001-product-workflow-brief-type` |
| Status | planned |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-1 |
| Required edit paths | `internal/workflows/featureplanner/product_context.go` (new), `internal/workflows/featureplanner/orchestrate.go` |
| Allowed edit paths | `internal/workflows/featureplanner/product_context.go`, `internal/workflows/featureplanner/orchestrate.go`, `internal/workflows/featureplanner/product_context_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestProductWorkflowBrief` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestProductWorkflowBrief -count=1` |

**Deliverable**: `ProductWorkflowBrief`, `PRDRequirementBrief`, `TDDObligationBrief`
types in new file `product_context.go`. Optional `ProductWorkflowBrief` pointer
added to `RunOptions` and threaded through `Run()` to `BuildPlannerPrompt()`.
JSON round-trip test with minimal and full payloads.

**Verification**:

- [ ] `go test ./internal/workflows/featureplanner/... -run TestProductWorkflowBrief`
- [ ] JSON round-trip: marshal full brief, unmarshal, assert field equality

---

### Atom 2: BuildPlannerPrompt product context injection

| Field | Value |
|---|---|
| ID | `impl-0002-planner-prompt-context-injection` |
| Status | planned |
| Dependencies | impl-0001, impl-0004 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-2 |
| Required edit paths | `internal/workflows/featureplanner/orchestrate.go` |
| Allowed edit paths | `internal/workflows/featureplanner/orchestrate.go`, `internal/workflows/featureplanner/orchestrate_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestBuildPlannerPrompt` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestBuildPlannerPrompt -count=1` |

**Deliverable**: When `ProductWorkflowBrief != nil`, inject a "Product Workflow
Context" section into the planner prompt listing real PRD requirement IDs, ADR
IDs, TDD obligation IDs, coverage gate status, and edit scope. Replace the
hardcoded example `docs/path.md:line` with the instruction to reference only
IDs from the provided context. When brief is nil, the hardcoded `docs/path.md:line` example
is replaced with empty `"refs":[]` plus an `"acceptance":[...]` placeholder
and the instruction that acceptance criteria strings are valid.
This removes the hallucination template in both branches.

When `CoverageGateResult.Status != "pass"`, the prompt adds a nudge:
"The coverage gate has NOT passed. Do not produce a feature plan; instead,
use ask_planner_question to report the blocked gate."

**Verification**:

- [ ] `TestBuildPlannerPrompt_WithProductContext` — prompt contains PRD req IDs, ADR IDs, TDD obligation IDs, no `docs/path.md:line`
- [ ] `TestBuildPlannerPrompt_WithoutProductContext` — prompt mentions acceptance criteria as valid alternative

---

### Atom 3: CLI flags for effort context

| Field | Value |
|---|---|
| ID | `impl-0003-cli-effort-flags` |
| Status | planned |
| Dependencies | impl-0001 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-3 |
| Required edit paths | `internal/surfaces/cli/featureplanner/feature_planner.go` |
| Allowed edit paths | `internal/surfaces/cli/featureplanner/feature_planner.go`, `internal/surfaces/cli/featureplanner/feature_planner_test.go` |
| Required tests | `go test ./internal/surfaces/cli/featureplanner/... -run TestCLI_Effort` |
| Test-first commands | `go test ./internal/surfaces/cli/featureplanner/... -run TestCLI_Effort -count=1` |

**Deliverable**: `--effort-root` and `--effort-id` flags. Loading logic in
`defaultHeadlessFeaturePlanner`: resolve effort root, load
`workflow_state.json`, parse `prd.md`, `adrs/index.json`, `tdd.json`, run
`EvaluateCoverageGate()` and `GenerateImplementationBrief()`, populate a
`ProductWorkflowBrief`, pass through `RunOptions`.

**Implementation note**: `EvaluateCoverageGate` requires `[]ParsedADR`
(load each accepted ADR markdown via `ParseADRMarkdown` and convert) plus a
`TraceabilityMatrix` (load `evidence/traceability_matrix.json` via
`ParseTraceabilityMatrixJSON`). `GenerateImplementationBrief` requires
`ImplementationRecords` (from `implementation_ledger.jsonl`) and
`ExistingWorkspacePath` (filesystem check of planned scope files).
This is ~8-10 filesystem reads, not 3 function calls.
Import of `productworkflow` from `featureplannercmd` is architecturally
intentional — the CLI adapter layer may import both leaf packages.

**Verification**:

- [ ] `TestCLI_EffortRootFlag` — flag parsing
- [ ] `TestCLI_EffortRootLoadsProductWorkflowBrief` — integration test with fixture effort folder

---

### Atom 4: SDDContract schema upgrade

| Field | Value |
|---|---|
| ID | `impl-0004-sdd-contract-schema-upgrade` |
| Status | planned |
| Dependencies | impl-0001 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-4, TDD-10 |
| Required edit paths | `internal/workflows/featureplanner/types.go`, `internal/workflows/featureplanner/validate.go` |
| Allowed edit paths | `internal/workflows/featureplanner/types.go`, `internal/workflows/featureplanner/validate.go`, `internal/workflows/featureplanner/validate_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run "TestSDDContract_V2\|TestValidatePlan_AcceptsStructured\|TestValidatePlan_MigratesLegacy\|TestFeaturePlan_V2_RoundTrip\|TestValidatePlan_AcceptsV1AndV2"` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestSDDContract_V2 -count=1` |

**Deliverable**: Add `prd_requirement_refs` and `adr_refs` to `SDDContract`.
Add `coverage_gate_event_id` to `TDDContract`. Add `coverage_gate_result` to
`FeaturePlan`. Bump `FeaturePlanSchemaVersion` to `idiom.feature_plan.v2`.
Legacy `refs` entries treated as acceptance criteria in `ValidateDurablePlan`.

**Verification**:

- [ ] `TestSDDContract_V2_RoundTrip` — JSON round-trip with new fields
- [ ] `TestValidatePlan_AcceptsStructuredSDDRefs` — plan with PRD req refs passes
- [ ] `TestValidatePlan_MigratesLegacyRefs` — legacy refs treated as acceptance

---

### Atom 5: SDD ref file-existence validation

| Field | Value |
|---|---|
| ID | `impl-0005-sdd-ref-file-existence` |
| Status | planned |
| Dependencies | impl-0004 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-5 |
| Required edit paths | `internal/workflows/featureplanner/validate.go`, `internal/surfaces/cli/featureplanner/feature_planner.go` |
| Allowed edit paths | `internal/workflows/featureplanner/validate.go`, `internal/surfaces/cli/featureplanner/feature_planner.go`, `internal/workflows/featureplanner/validate_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanSDDRefs` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanSDDRefs -count=1` |

**Deliverable**: `ValidatePlanSDDRefs(plan FeaturePlan, artifactRoot string) error`
that resolves SDD refs against artifact root and checks file existence. Called
from CLI layer after `ValidateDurablePlan` passes. Rejects refs that don't
resolve. Strips `:line` suffix.

**Verification**:

- [ ] `TestValidatePlanSDDRefs_ExistingFile`
- [ ] `TestValidatePlanSDDRefs_MissingFile`
- [ ] `TestValidatePlanSDDRefs_StripsLineSuffix`

---

### Atom 6: TDD obligation cross-reference validation

| Field | Value |
|---|---|
| ID | `impl-0006-tdd-obligation-cross-ref` |
| Status | planned |
| Dependencies | impl-0004 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-6 |
| Required edit paths | `internal/workflows/featureplanner/validate.go`, `internal/surfaces/cli/featureplanner/feature_planner.go` |
| Allowed edit paths | `internal/workflows/featureplanner/validate.go`, `internal/surfaces/cli/featureplanner/feature_planner.go`, `internal/workflows/featureplanner/validate_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanTDDObligations` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanTDDObligations -count=1` |

**Deliverable**: `ValidatePlanTDDObligations(plan FeaturePlan, knownIDs map[string]bool) error`
that verifies every TDD obligation ID and atom `TestFirst.ObligationRefs`
references a known obligation from the product workflow ledger.

**Verification**:

- [ ] `TestValidatePlanTDDObligations_AllKnown`
- [ ] `TestValidatePlanTDDObligations_UnknownRef`
- [ ] `TestValidatePlanTDDObligations_AtomObligationRefsChecked`

---

### Atom 7: Coverage gate intake validation

| Field | Value |
|---|---|
| ID | `impl-0007-coverage-gate-intake` |
| Status | planned |
| Dependencies | impl-0004 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-7 |
| Required edit paths | `internal/workflows/featureplanner/validate.go` |
| Allowed edit paths | `internal/workflows/featureplanner/validate.go`, `internal/workflows/featureplanner/validate_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestValidatePlan.*CoverageGate` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestValidatePlan.*CoverageGate -count=1` |

**Deliverable**: Validation in `ValidatePlan` that blocks when
`CoverageGateResult != nil && Status != "pass"`. Enforces the authority
boundary: feature-planner cannot dispatch atoms for an effort whose coverage
gate never passed.

**Verification**:

- [ ] `TestValidatePlan_RejectsBlockedCoverageGate`
- [ ] `TestValidatePlan_AcceptsPassedCoverageGate`
- [ ] `TestValidatePlan_AcceptsNilCoverageGate` — standalone mode preserved

---

### Atom 8: End-to-end integration test

| Field | Value |
|---|---|
| ID | `impl-0008-e2e-integration-test` |
| Status | planned |
| Dependencies | impl-0001, impl-0002, impl-0003, impl-0004, impl-0005, impl-0006, impl-0007 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-8 |
| Required edit paths | `testdata/feature-planner-pw-integration/` (new fixture), `internal/surfaces/cli/featureplanner/feature_planner_test.go` |
| Allowed edit paths | `testdata/feature-planner-pw-integration/`, `internal/surfaces/cli/featureplanner/feature_planner_test.go` |
| Required tests | `go test ./internal/surfaces/cli/featureplanner/... -run TestFeaturePlanner_ProductWorkflowIntegration` |
| Test-first commands | N/A (integration test — fixture first, then test) |

**Deliverable**: Fixture effort folder with `workflow_state.json`, `prd.md`,
`adrs/`, `tdd.json`. Run `idiom feature-planner --effort-root <fixture>` and
verify: planner prompt contains real artifact IDs, submitted plan passes SDD
ref validation, atom TDD obligations match ledger.

**Verification**:

- [ ] `TestFeaturePlanner_ProductWorkflowIntegration_EndToEnd`

---

### Atom 9: FeatureState effort identity persistence

| Field | Value |
|---|---|
| ID | `impl-0009-effort-identity-persistence` |
| Status | planned |
| Dependencies | impl-0001, impl-0004 |
| SDD refs | `docs/design-feature-planner-product-workflow-integration.md` |
| TDD obligations | TDD-9 |
| Required edit paths | `internal/workflows/featureplanner/types.go`, `internal/workflows/featureplanner/store.go` |
| Allowed edit paths | `internal/workflows/featureplanner/types.go`, `internal/workflows/featureplanner/store.go`, `internal/workflows/featureplanner/store_test.go` |
| Required tests | `go test ./internal/workflows/featureplanner/... -run TestFeatureState_EffortIdentity` |
| Test-first commands | `go test ./internal/workflows/featureplanner/... -run TestFeatureState_EffortIdentity -count=1` |

**Deliverable**: Add `effort_id` and `effort_root` to `FeatureState`. Persist
in `SaveBundle` and `LoadBundle` (FeatureState is serialized inside the Bundle). Include in feature plan JSON output.

**Verification**:

- [ ] `TestFeatureState_EffortIdentityRoundTrip` — save/load preserves fields
- [ ] `TestFeatureState_StandaloneNoEffortIdentity` — nil fields survive

---

## TDD Obligations

| ID | Behavior | Command | Required before |
|---|---|---|---|
| TDD-1 | ProductWorkflowBrief type exists, JSON round-trips, threads through RunOptions | `go test ./internal/workflows/featureplanner/... -run TestProductWorkflowBrief` | implementation |
| TDD-2 | Planner prompt injects product context when brief is non-nil, backward-compatible when nil | `go test ./internal/workflows/featureplanner/... -run TestBuildPlannerPrompt` | implementation |
| TDD-3 | CLI accepts --effort-root and --effort-id flags, loads product workflow brief from fixture | `go test ./internal/surfaces/cli/featureplanner/... -run TestCLI_Effort` | implementation |
| TDD-4 | SDDContract has structured refs, v2 schema round-trips, legacy refs migrate | `go test ./internal/workflows/featureplanner/... -run TestSDDContract_V2\|TestValidatePlan_AcceptsStructured\|TestValidatePlan_MigratesLegacy` | implementation |
| TDD-5 | SDD refs validated against filesystem, missing files rejected, line suffixes stripped | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanSDDRefs` | implementation |
| TDD-6 | TDD obligation IDs cross-referenced against known ledger, unknown refs rejected | `go test ./internal/workflows/featureplanner/... -run TestValidatePlanTDDObligations` | implementation |
| TDD-7 | Coverage gate result validated at intake, blocked gate rejects plan, nil gate accepted | `go test ./internal/workflows/featureplanner/... -run TestValidatePlan.*CoverageGate` | implementation |
| TDD-8 | End-to-end test: fixture effort → planner prompt → valid plan → atom dispatch | `go test ./internal/surfaces/cli/featureplanner/... -run TestFeaturePlanner_ProductWorkflowIntegration` | implementation |
| TDD-9 | FeatureState effort identity persists across save/load, nil fields survive | `go test ./internal/workflows/featureplanner/... -run TestFeatureState_EffortIdentity` | implementation |
| TDD-10 | FeaturePlan v2 round-trips with CoverageGateResult, ValidateDurablePlan accepts both v1 and v2 | `go test ./internal/workflows/featureplanner/... -run "TestFeaturePlan_V2_RoundTrip\|TestValidatePlan_AcceptsV1AndV2"` | implementation |

## Non-Goals

- Product workflow pipeline completion (PRD/ADR generation, TDD synthesis) —
  tracked in `prd-adr-brief-workflow` effort.
- TDDObligation schema unification between packages — translation at CLI
  boundary is sufficient.
- TUI continuation/handoff — tracked separately.
- Sidecar/MCP exposure — tracked separately.

## Model Policy

- **Orchestrator**: Use default profile (gpt-5.5 or ds4-pro). The orchestrator
  plans, reviews, verifies, and commits.
- **Implementor**: Use `ds4-flash-coder` profile. This is hosted DeepSeek V4
  Flash through Moon Bridge (`model_provider = "moonbridge"`) at the same API
  endpoint as ds4-pro (`http://127.0.0.1:38440/v1`), with
  `model_reasoning_effort = "medium"` and 200K context window. The medium
  reasoning level prioritizes implementation speed — appropriate for bounded,
  well-specified patch-shaped atoms.

## Implementation Guidelines

- Context discovery: start with `idiom tool context_pack` for localization,
  then prefer Idiom tools over raw rg/file reads.
- Each atom: implement, verify with focused tests, run product lint/audit when
  applicable, commit and push.
- Sequential atoms only — all atoms touch `featureplanner` or
  `featureplannercmd` and share validation/state, so parallel implementation
  would conflict.
- Before claiming an atom complete: focused tests pass, no regression in
  `go test ./internal/workflows/featureplanner/...`, `git diff --check` clean.
- After Atom 8: run `make build && make test`.
- After Atom 9: run scripted feature-planner contract tests:
  `make eval LOGS=testdata/scripted-contracts/generated-logs/scripted-feature-planner-*.jsonl`.

## Resume Path

Start future sessions by reading:

1. `docs/product-workflows/efforts/index.json`
2. `docs/product-workflows/efforts/started/feature-planner-pw-integration/workflow_state.json`
3. This `design.md`
4. `docs/design-feature-planner-product-workflow-integration.md`

## Artifact Map

- `design.md` — this file, the tracked implementation plan
- `source_design_ref.json` — pointer to the canonical design doc
- `workflow_state.json` — durable state
- `events.jsonl` — atom dispatch/completion events
- `implementation_ledger.jsonl` — per-atom changed paths and status
- `tdd.json` — TDD obligation ledger
- `reports/` — per-atom implementation, review, and investigation reports

