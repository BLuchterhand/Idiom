# Harness Optimization Program Implementation Evidence

Status: first implementation wave reviewer-approved and verified, created
2026-05-24

Source design: [design-harness-optimization-program.md](../design-harness-optimization-program.md)

## Pre-Implementation Reviewer Bench

Full bench completed before code changes in this session.

| Reviewer | Result |
| --- | --- |
| Incident Root-Cause | Start with a fresh replay/baseline and passive guard-friction events; do not start with lifecycle closeout or protocol behavior changes. |
| Model Runtime / Config | Reuse existing runtime/transport/phase state; missing protocol hash must be represented as a parity gap or `unknown`, not fabricated certainty. |
| Harness / Go | First code slice should be completion-gate guard friction because the guard path is centralized and already emits autonomy gate events. |
| Prompt / Protocol | Keep the slice report-only; protocol projection parity is adjacent but must not become another prompt reminder layer. |
| TDD / Acceptance | Add schema, audit grouping, runner emission, harness-audit exposure, retry/avoidability/redaction tests; behavior must not change. |
| North-Star / Architecture | Passive observability is aligned; changing visible tools, parser mode, recovery wording, or capability routing before the baseline is a scope risk. |

Accepted risk: the worktree already contained uncommitted/untracked related WIP for protocol projection, turn-phase reconciliation, and guard-friction schema. This slice works with that state and keeps the implementation boundary passive.

## Atom Ledger

```json
{
  "schema_version": "idiom.design_doc_implementation_atom.v1",
  "effort_id": "harness-optimization-program",
  "task_id": "guard-friction-001",
  "phase": "guard-friction-observability-passive-events",
  "state": "implemented",
  "complexity_tier": "C2",
  "context_evidence": [
    "context_pack:timed_out; fallback bounded source reads",
    "reviewer_bench:full"
  ],
  "expected_files": [
    "internal/contracts/autonomy/event.go",
    "internal/contracts/autonomy/audit.go",
    "internal/contracts/autonomy/*_test.go",
    "internal/runtime/agent/runner_autonomy.go",
    "internal/runtime/agent/runner_autonomy_test.go",
    "internal/capabilities/workspace/discovery_tools_test.go"
  ],
  "forbidden_files": [
    "prompts/**",
    "runner visible tool filtering behavior",
    "parser enforcement behavior",
    "capability resolver behavior"
  ],
  "blast_radius": "Passive autonomy/audit event schema and guard-decision friction telemetry; no enforcement change intended.",
  "tdd_obligations": [
    "event schema and redaction",
    "audit grouping and all-clear behavior",
    "runner guard-friction emission cites model-visible protocol hash and safe alternatives only when a model protocol projection exists",
    "harness_audit exposes guard-friction coverage"
  ],
  "required_tests": [
    "go test ./internal/contracts/autonomy -run 'GuardFriction|TurnPhase|Audit' -count=1",
    "go test ./internal/runtime/agent -run 'RunnerAutonomyRecorderRejectedCompletionEmitsGuardFriction|RunnerAutonomyRecorderRejectedWriteGateEmitsGuardFriction|RunnerAutonomyRecorderEmitsTurnPhaseContractPresentation|RunnerRunEmitsTurnPhaseContractBeforeModelCallAndPolicyEvents|ModelProtocol' -count=1",
    "go test ./internal/capabilities/workspace -run 'TestHarnessAuditBehaviorIncludesContractCoverage' -count=1"
  ],
  "audit_signal_expected_to_move": [
    "guard_friction_coverage.guards_fired",
    "guard_friction_coverage.guard_surface_counts",
    "guard_friction_coverage.safe_alternative_coverage"
  ],
  "rollback_note": "Revert guard-friction event/audit additions and runner emission hook; existing gates and model dispatch should remain unchanged."
}
```

## Implementation Evidence

- `internal/contracts/autonomy` now has a passive `guard_friction` autonomy event with validation, redaction checks, copy/normalize support, and audit grouping.
- `internal/runtime/agent` now emits passive guard-decision `guard_friction` events for rejected/nudged/stopped gate decisions. Friction events use model-visible protocol fields only when a model protocol projection hash exists; internal turn-phase hashes remain gate/audit identity and are not treated as proof of model visibility.
- `internal/capabilities/workspace` harness-audit coverage now carries grouped guard-friction totals through `contract_coverage`.
- No prompt, visible-tool, parser, permission, verification, or gate enforcement behavior was intentionally changed by this slice.
- The active protocol projection now renders explicit visible and callable tool
  lists; guard friction cites that model-visible protocol hash only after the
  protocol card has actually been presented.
- Turn-phase reconciliation now checks contract id, phase id, model-call id,
  protocol hash, action classes, callable tools, terminal outcomes, and
  parser/runtime fields across covered surfaces.

## Post-Implementation Review Loop

Initial post-implementation review found two blockers:

- North-Star / Architecture blocked because the shared worktree also contains adjacent model-facing protocol/capability/recovery WIP, so the whole diff is not a clean passive-observability atom.
- Harness / Go blocked a concrete attribution bug: friction telemetry treated internal turn-phase protocol hashes as model-visible contract proof.

Corrective change applied:

- `guard_friction.protocol_hash` and `model_contract_visible` now come only from `ModelProtocolProjection.ProtocolHash`.
- Gate decision events may still carry internal protocol identity for audit correlation, but friction events do not report that identity as model-visible evidence.
- The write-gate regression now asserts that an empty model protocol projection leaves `protocol_hash` empty and `model_contract_visible=false`.
- Completion-gate retry attempts are routed through `emitCompletionGateAttempt`, so repeated rejection friction can carry retry count.
- `review_pass` is now a first-class autonomy action class because contract compilation and turn-phase reconciliation already use that class.
- `next_action_state` surface summaries now derive advertised action classes from rendered next-action text, so reconciliation catches invalid rendered guidance instead of only echoing the ideal contract.
- Harness-audit capability mix helpers are present and use existing exported evidence-capability metadata.

## Verification

Passed:

```sh
go test ./internal/contracts/autonomy -run 'GuardFriction|TurnPhase|Audit' -count=1
go test ./internal/runtime/agent -run 'RunnerAutonomyRecorderRejectedCompletionEmitsGuardFriction|RunnerAutonomyRecorderRejectedWriteGateEmitsGuardFriction|RunnerAutonomyRecorderEmitsGateEventsForExistingGates|ModelProtocol' -count=1
go test ./internal/capabilities/workspace -run 'TestHarnessAuditBehaviorIncludesContractCoverage' -count=1
go test ./internal/contracts/autonomy ./internal/runtime/agent ./internal/capabilities/workspace
go test ./internal/contracts/contractcompile -count=1
git diff --check
```

Additional reviewer-driven verification completed after the protocol projection
and reconciliation fixes:

```sh
go test -count=1 ./internal/contracts/contractcompile ./internal/contracts/autonomy
go test -count=1 ./internal/runtime/agent -run 'TestRunnerInjectsPlanExecuteModelProtocolProjectionCard|TestRunnerRunRepeatedCompletionRejectionEmitsRetryFriction|TestRunnerRunCompletionFrictionDoesNotClaimUnpresentedModelProtocol'
go test -count=1 ./internal/contracts/autonomy ./internal/contracts/contractcompile ./internal/runtime/workerruntime ./internal/capabilities/workspace ./internal/surfaces/tui/app ./internal/workflows/planexecute ./internal/runtime/agent
go test ./cmd/idiom-swe-eval -run TestScriptedContractSuiteLintsClean -count=1
go test ./...
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

Reviewer approval after fixes:

- Harness/gate reviewer approved guard-friction hash visibility and report-only
  compiler behavior.
- Architecture reviewer approved active contract identity binding and
  contract-id reconciliation.
- Model-runtime/protocol reviewer approved explicit empty callable-tool
  handling and visible/callable card rendering.
- TDD/design reviewer approved parity between the design doc and regression
  tests.

Audit note: the final behavior audit completed and reported historical findings
from capped logs, including existing turn-phase contradictions and lifecycle
closeout gaps. Those findings are now measurable through the new report-only
surfaces but are not claimed as fully remediated by this implementation wave.
