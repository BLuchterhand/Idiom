# Rearchitecture Phase 0 Evidence

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: Phase 0 evidence record
Date: 2026-06-18
Tracker: [idiom-rearchitecture-tracker.md](idiom-rearchitecture-tracker.md)

## Scope

This record started as evidence for the first migration slice, `R0A-S1`, whose
child atoms are `R0A-A`, `R0A-B`, and `R0C-A`. That slice is documentation and
evidence only. The later rows in this note also track the first test-only
runtime atoms after Phase 0C; those rows may touch named test files, but they
must not move runtime authority, prompts, contracts, workspace tools, CLI/TUI
behavior, or backend configuration.

Allowed files:

- `docs/idiom-rearchitecture-tracker.md`
- `docs/rearchitecture-phase0-evidence.md`
- `docs/rearchitecture-smoke-brief.md`
- `docs/rearchitecture-smoke-target.md`
- `docs/rearchitecture-smoke-target.template.md`
- `.idiom/rearchitecture-smoke-config.json` as an ignored local smoke-only
  configuration file

Forbidden files for the `R0A-S1` evidence slice:

- `internal/runtime/agent/**`
- `internal/contracts/**`
- `prompts/**`
- `cmd/**`
- `internal/surfaces/**`
- `.idiom/backends.json`
- production/runtime enforcement paths

Additional allowed files for post-Phase-0 test-only atoms:

- `internal/runtime/agent/plan_execute_recovery_plan_test.go` for
  `R0T-P3F-A`
- `internal/runtime/agent/card_engine.go` and
  `internal/runtime/agent/plan_execute_recovery_plan.go` for `R0T-P3F-B`
- `internal/runtime/agent/recovery_plan_and_brief_test.go` for
  `R0T-P4C-A`

`R0T-P3F-B` owns the dirty recovery-builder source split that `R0T-P3F-A`
exercises: `card_engine.go` owns recovery plan builders and production
recovery-builder callsites, while `plan_execute_recovery_plan.go` owns value
types and rendering. Later source atoms must still name exact ownership before
rewriting broader runtime-agent files.

## Phase 0A Current-State Reconciliation

The migration is not starting from zero. Existing report-only and projection
pieces are already present.

| Area | Current owner | Evidence |
| --- | --- | --- |
| Contract vocabulary and ownership model | `internal/contracts/autonomy` | `contract.go` defines schema/version, invocation kinds, path roles, capabilities, obligations, gates, enforcement modes, and metadata. |
| Structured contract events | `internal/contracts/autonomy` | `event.go` includes `contract_compiled`, `contract_presented`, `tool_policy_decision`, `gate_decision`, `completion_gate_decision`, `model_exchange`, and `turn_phase_contract` payloads. |
| Audit and replay reducers | `internal/contracts/autonomy` | `audit.go` and `replay.go` cover contract coverage, parity, privacy, replay violations, and redaction. |
| Turn-phase surface reconciliation | `internal/contracts/autonomy` | `turn_phase_reconciliation.go` includes surface summaries for turn-phase contracts and model protocol projections. |
| Plan-execute compiler and protocol projection | `internal/contracts/compile` | `compile.go` includes `CompilePlanExecutePlanner`, autonomy contract compilation, runtime envelope compilation, protocol projection compilation, and active-authority binding. |
| Plan-execute scope and execution contract | `internal/contracts/plan` | `scope_contract.go`, `execution_contract.go`, `edit_set_obligations.go`, and source-satisfied helpers already hold plan/executor scope and closeout vocabulary. |
| Path and prompt-scope parsing | `internal/contracts/pathparse` | `pathparse.go` owns bounded extraction of prompt path intent so route/path phases do not duplicate ad hoc parsing. |
| Writer route decisions | `internal/contracts/writerroute` | `decision.go` owns writer route decision vocabulary used by lane routing and prevents prompt prose from becoming route authority. |
| Runtime protocol projection caller | `internal/runtime/agent` | `model_protocol_projection.go` calls the compiler and emits `model_protocol_projection_gap` on failure. |
| Runtime turn-phase contract projection | `internal/runtime/agent` | `phase_contract.go` defines `TurnPhaseContract`, `PhaseEvidenceState`, and autonomy payload generation. |
| Active protocol card presenter | `internal/runtime/workerruntime` | `protocol_card.go` consumes compiled projection details for active protocol cards. |
| Plan-execute terminal recording | `internal/workflows/planexecute` | `terminal_completion.go` records structured terminal completion state; `helpers.go` emits structured autonomy records. |

Existing coverage anchors:

- `internal/contracts/compile/compile_test.go`
- `internal/contracts/autonomy/event_test.go`
- `internal/contracts/autonomy/audit_test.go`
- `internal/contracts/autonomy/replay_test.go`
- `internal/runtime/agent/phase_contract_test.go`
- `internal/runtime/agent/runner_autonomy_test.go`
- `internal/runtime/agent/runner_plan_then_execute_guard_test*.go`
- `internal/workflows/planexecute/*_test.go`

Phase status implication:

- Phase 0A should be marked `Complete` for this design-doc slice.
- Phase 1 remains `Report-only` for the already-shipped compiler/projection
  foundation; enforcement is not being changed by this slice.

## Phase 0B Authority Inventory

| Invariant id | Authority source | Canonical owner package | Projection/presenter/consumer evidence | Parity risk |
| --- | --- | --- | --- | --- |
| `AUTH-ROUTE-01` | Route selection and route contract | `internal/contracts/writerroute`, feeding `internal/contracts/autonomy` | TUI turn planner and runtime brief consume the route decision; TUI routing tests cover current behavior. | Prompt wording and classifier fallback can leak prose authority into lane choice. |
| `AUTH-BRIEF-01` | Prompt lead-in and initial runtime brief | `internal/contracts/compile` protocol projection, with `internal/runtime/workerruntime` as presenter only | `RenderInitialRuntimeBrief` renders typed brief/projection fields; prompt runtime tests cover current presentation. | Lead-in can embed recovery/protocol text directly and become shadow authority. |
| `AUTH-RUNTIME-01` | Runtime brief | `internal/contracts/compile` and `internal/contracts/autonomy` | `WorkerRuntimeBrief` remains an input/adapter; compiler output is the intended authority. | Brief fields mix lane, evidence, tools, verification, and recovery authority. |
| `AUTH-TOOLS-01` | Visible, callable, and hidden tools | `internal/contracts/autonomy` | Runner phase state and turn-phase reconciliation project visible/hidden/callable tool surfaces. | Actual callable tools, hidden tools, recovery card, and next-action state can drift. |
| `AUTH-RECOVERY-01` | Recovery cards and nudges | `internal/contracts/autonomy` typed recovery state | Recovery card and card-engine paths should become presenters of typed recovery decisions. | Recovery prose can encode policy and become a hidden contract. |
| `AUTH-VERIFY-01` | Verification | `internal/contracts/plan` execution contract plus `internal/contracts/autonomy` obligations | Runtime verification helpers consume plan/executor evidence and obligation state. | Planner-owned and executor-owned verification can blur; post-write proof can be over- or under-required. |
| `AUTH-FINAL-01` | Final-answer acceptance | `internal/contracts/autonomy` answer policy and obligation ledger | Final-answer acceptance/decorator paths consume typed evidence and terminal state. | Acceptance mixes grounding, relevance, protocol shape, and lane-specific closeout. |
| `AUTH-SIDECAR-01` | Sidecar envelopes | `internal/capabilities/workspace` as projection, tied to `internal/contracts/autonomy` | Tool sidecar envelope reports active contract/protocol metadata without owning policy. | Envelope fields can become de facto policy if not tied to the active contract. |
| `AUTH-TUI-01` | TUI next-action state | `internal/contracts/autonomy` surface summary | Runner phase state and TUI history render next valid actions from the active contract. | Next-action text, invalid actions, and recovery cards can disagree. |

Phase status implication:

- Phase 0B should be marked `Report-only`: the inventory exists, but old/new
  decision parity fixtures are not yet complete.

## Phase 0C Baseline Evidence

Commands run:

```bash
git status --short
idiom tool context_status --input '{"refresh":false,"limit":20}'
idiom tool harness_audit --input '{"purpose":"architecture_state"}'
idiom tool harness_audit --input '{"purpose":"behavior"}'
idiom backend status --profile deepseek-v4-flash
go test ./internal/contracts/... ./internal/runtime/workerruntime/... -count=1
go test ./internal/runtime/agent -run 'RecoveryCard|SubmitPlan|AnchorRefresh|SourceSatisfied|CompletionGate|Autonomy|TurnPhase' -count=1
go test ./internal/capabilities/workspace -run 'TestHarnessAudit|ToolSchema|ToolProfile|Sidecar|Capability|Policy' -count=1
idiom tool context_pack --input '{"task":"rearchitecture phase 0 baseline","mode":"plan","limit":5}'
go test ./...
go test ./internal/contracts/compile -run 'TestPackageImportBoundaries|TestCoreContractPackages|TestLegacyContractRootsAreRetired|TestMovedInternalRootsAreRetired|TestRuntimeAndCapabilitiesDoNotDependOnSurfaces' -count=1
idiom tool context_pack --input '{"task":"rearchitecture phase 0 sidecar replay corpus","mode":"plan","limit":5}'
go test ./internal/runtime/agent -run 'TestReadOnlyAnswerabilityContextPacketOnlyLocalizes|TestReadOnlyAnswerabilityHighCardinalitySearchRequiresReturnedCandidateRead|TestReadOnlyAnswerabilityOutlinesAndGeneratedArtifactsDoNotAnswer|TestReadOnlyAnswerabilityContextPackCandidateRequiresObjectiveRelevantSourceBody' -count=1
go test ./internal/surfaces/cli/planexecute -run 'TestRunExecutorPhaseEmitsExecutorInheritsPackEvent|TestRunExecutorPhaseEmitsExecutorPackMissOnUnknownPackID|TestRunExecutorPhaseDispatchesEditsInOrder|TestRunExecutorPhaseAppliesInsertAfterLineShape' -count=1
go test ./internal/surfaces/cli/lane -run 'TestRunLaneImplementRecoveryWritesFailureAndPacket|TestRunLaneImplementRecoveryCapturesPromptRunnerSessionID' -count=1
go test ./internal/surfaces/cli/recover -run 'TestRunMainRecoverInspectReadsFailureRecord|TestRunRecoverInspectAndDecomposeReadImplementFailureRecord|TestRunRecoverInspectComputesLegacyFailedProgressWithoutWriting' -count=1
go test ./internal/capabilities/workspace -run 'TestHarnessAudit|ToolSchema|ToolProfile|Sidecar|Capability|Policy' -count=1
go test ./internal/runtime/agent -run 'TestPlanExecute.*RecoveryPlan|TestPlanExecute.*Recovery.*|TestPlanExecute.*Fallback|TestPlanThenExecute.*RecoveryPlan|TestPlanThenExecute.*RecoveryPrompt|TestPlanThenExecute.*Fallback' -count=1
rg -n 'func planThenExecute(LowValueActionRecoveryPlan|GroundingRequiredRecoveryPlan|UnscopedEmptySubmitPlanRecoveryPlan|SubmitPlanParseRecoveryPlan|SubmitCorrectionRecoveryPlan|NativeSubmitPlanCorrectionRecoveryPlan|AnchorRefreshRecoveryPlan|RepeatedSubmitCorrectionRecoveryPlan|RepeatedCorrectionExhaustedRejection|CorrectionHiddenOnlyRecoveryPrompt|NativeNoCallFallbackRecoveryPlan)|PlanExecute(LowValueActionPlan|GroundingRequiredPlan|UnscopedEmptySubmitPlanPlan|SubmitPlanParseRecoveryPlan|SubmitCorrectionPlan|NativeSubmitCorrectionPlan|AnchorRefreshPlan|RepeatedCorrectionPlan|RepeatedCorrectionExhausted|CorrectionHiddenOnlyPrompt|NativeNoCallFallbackPlan)' internal/runtime/agent -g'*.go'
go test ./internal/runtime/agent -run 'TestImplementationPreWriteRuntimeBriefSetsWriteOnlySurface|TestImplementationPreWriteRuntimeBriefPreservesRequiredEditPaths' -count=1
go test ./internal/runtime/agent -count=1
idiom tool harness_audit --input '{"purpose":"behavior"}'
idiom brief lint docs/rearchitecture-smoke-brief.md
git diff --check
go test ./...
```

Baseline facts:

- Worktree is dirty. There are many modified `internal/runtime/agent/**` files,
  untracked runtime helper files, untracked rearchitecture docs, and unrelated
  workspace changes. This slice must not edit runtime source.
- Context index is present with index id `c8aba855cd524a27`.
- Context index is stale for 12 files, all in `internal/runtime/agent/**`.
- `context_status` reported 1001 indexed files, 21840 symbols, and 117986
  edges.
- `context_pack` timed out after 30 seconds. This is baseline evidence, not a
  green discovery signal.
- Sidecar replay `context_pack` succeeded for task `rearchitecture phase 0
  sidecar replay corpus`, wrote
  `.idiom/comprehension/views/d3358133d6a0a750.json`, and returned pack id
  `d3358133d6a0a750` on index id `27e9b06511147365`.
- `harness_audit` summary reported:
  - 135 manifest/schema/exported tools with zero drift;
  - capability coverage 55/112 read-only adapters mapped, 57 unmapped;
  - 104 logs scanned;
  - 5737 contract events;
  - 79 gate decisions;
  - 396 ranker-learning events;
  - 94 context-cache rows;
  - 11 current findings and 2 historical findings;
  - 472 turn-phase contract contradictions;
  - 10 gate open-obligation inconsistencies;
  - 4 unmatched obligation references.
- `harness_audit` behavior summary reported 104 logs scanned, 12 child
  lifecycle records, 4 invocations, 4 terminals, 4 acceptances, 5737 contract
  events, 79 gate decisions, 396 ranker-learning events, 96 context-cache rows,
  8 current findings, and 2 historical findings.
- DeepSeek hosted profile status: `UP deepseek-v4-flash`.
- Focused tests passed:
  - `go test ./internal/contracts/... ./internal/runtime/workerruntime/... -count=1`
  - `go test ./internal/runtime/agent -run 'RecoveryCard|SubmitPlan|AnchorRefresh|SourceSatisfied|CompletionGate|Autonomy|TurnPhase' -count=1`
  - `go test ./internal/capabilities/workspace -run 'TestHarnessAudit|ToolSchema|ToolProfile|Sidecar|Capability|Policy' -count=1`
  - `go test ./internal/contracts/compile -run 'TestPackageImportBoundaries|TestCoreContractPackages|TestLegacyContractRootsAreRetired|TestMovedInternalRootsAreRetired|TestRuntimeAndCapabilitiesDoNotDependOnSurfaces' -count=1`
  - `go test ./internal/runtime/agent -run 'TestReadOnlyAnswerabilityContextPacketOnlyLocalizes|TestReadOnlyAnswerabilityHighCardinalitySearchRequiresReturnedCandidateRead|TestReadOnlyAnswerabilityOutlinesAndGeneratedArtifactsDoNotAnswer|TestReadOnlyAnswerabilityContextPackCandidateRequiresObjectiveRelevantSourceBody' -count=1`
  - `go test ./internal/surfaces/cli/planexecute -run 'TestRunExecutorPhaseEmitsExecutorInheritsPackEvent|TestRunExecutorPhaseEmitsExecutorPackMissOnUnknownPackID|TestRunExecutorPhaseDispatchesEditsInOrder|TestRunExecutorPhaseAppliesInsertAfterLineShape' -count=1`
  - `go test ./internal/surfaces/cli/lane -run 'TestRunLaneImplementRecoveryWritesFailureAndPacket|TestRunLaneImplementRecoveryCapturesPromptRunnerSessionID' -count=1`
  - `go test ./internal/surfaces/cli/recover -run 'TestRunMainRecoverInspectReadsFailureRecord|TestRunRecoverInspectAndDecomposeReadImplementFailureRecord|TestRunRecoverInspectComputesLegacyFailedProgressWithoutWriting' -count=1`
- Full test suite passed: `go test ./...`.
- After `R0T-P3F-A`, focused recovery-plan tests passed:
  `go test ./internal/runtime/agent -run 'TestPlanExecute.*RecoveryPlan|TestPlanExecute.*Recovery.*|TestPlanExecute.*Fallback|TestPlanThenExecute.*RecoveryPlan|TestPlanThenExecute.*RecoveryPrompt|TestPlanThenExecute.*Fallback' -count=1`.
- `R0T-P3F-B` source split check: `card_engine.go` is the source file defining
  `PlanExecute*Plan` builder methods and state-aware variants, legacy free
  `planThenExecute*RecoveryPlan` builder functions are gone, and production
  submit-correction, repeated-correction, hidden-only submit-correction,
  low-value, parser-recovery, anchor-refresh, native-correction, and native
  no-call fallback paths route through `CardEngine`. The focused recovery-plan
  tests above passed against that split, and injected-engine ownership tests now
  cover submit-correction required/ready/wrong-tool, anchor-refresh
  post-readiness/required-path/write-only, low-value, repeated-correction,
  hidden-only, and native no-call fallback callsites.
- After `R0T-P4C-A`, focused pre-write runtime brief tests passed:
  `go test ./internal/runtime/agent -run 'TestImplementationPreWriteRuntimeBriefSetsWriteOnlySurface|TestImplementationPreWriteRuntimeBriefPreservesRequiredEditPaths' -count=1`.
- After `R0T-P3F-A`, `R0T-P3F-B`, and `R0T-P4C-A`, full runtime-agent
  package passed:
  `go test ./internal/runtime/agent -count=1`.
- After `R0T-P3F-A`, `R0T-P3F-B`, and `R0T-P4C-A`, full suite passed again:
  `go test ./...`.
- After `R0T-P3F-A`, `R0T-P3F-B`, and `R0T-P4C-A`,
  `idiom brief lint docs/rearchitecture-smoke-brief.md` and
  `git diff --check` passed.
- After `R0T-P3F-A` and again after `R0T-P3F-B`,
  `idiom tool harness_audit --input '{"purpose":"behavior"}'` completed. It
  still reports the known baseline behavior findings, including turn-phase
  contract contradictions, gate open-obligation inconsistencies, and unmatched
  obligation references; these atoms did not move enforcement or claim those
  findings are resolved.

Hosted DeepSeek smoke:

```bash
SMOKE_BRIEF=docs/rearchitecture-smoke-brief.md
SMOKE_TARGET=docs/rearchitecture-smoke-target.md
cp docs/rearchitecture-smoke-target.template.md "$SMOKE_TARGET"
idiom lane plan-execute --config .idiom/rearchitecture-smoke-config.json --project-root . --backend-profile deepseek-v4-flash --allow-stale-brief --allow-dirty --json "$SMOKE_BRIEF"
cp docs/rearchitecture-smoke-target.template.md "$SMOKE_TARGET"
```

Smoke facts:

- The first direct smoke without `--allow-dirty` was correctly blocked by atom
  intake because the reset target was dirty.
- A second smoke with `--allow-dirty` and the default `.idiom/config.json`
  produced mismatched hosted/local attribution and failed before model
  execution with `no launchable backend controller configured`; this is not
  accepted as a green smoke.
- The hosted benchmark config selected DeepSeek correctly but failed executor
  writes because that config is read-only.
- The accepted smoke used ignored local config
  `.idiom/rearchitecture-smoke-config.json`, which selects hosted DeepSeek V4
  Flash and enables workspace writes for the disposable smoke target only.
- Accepted smoke session: `73e7a98210375846`.
- Planner session: `20260618-023505.052-92ce2b1f2fde6898`.
- Plan artifact: `.idiom/plans/20260618-063503510.plan.json`.
- Planner/executor context pack: `2799995167e04e15`.
- Scope contract id/hash: `3dfb7f1fc9c63a3b`.
- Runtime attribution recorded `backend_profile:"deepseek-v4-flash"`,
  `backend_kind:"deepseek"`, and planner, executor, classifier, and effective
  roles all as `deepseek-v4-flash`.
- Terminal completion recorded `exit_code:0`, `status:"completed"`,
  `terminal_class:"success"`, and required-path coverage status `changed`.
- The smoke target was restored from
  `docs/rearchitecture-smoke-target.template.md` after the run.

Replay corpus facts:

- Direct answer/read-only architecture behavior is covered by the focused
  read-only answerability tests listed above.
- Plan-execute planner and executor behavior is covered by the hosted DeepSeek
  smoke plus focused `internal/surfaces/cli/planexecute` tests.
- Implement-lane recovery behavior is covered by focused
  `internal/surfaces/cli/lane` tests.
- Recovery flow behavior is covered by focused `internal/surfaces/cli/recover`
  tests.
- Sidecar `context_pack` behavior is covered by the successful replay pack id
  `d3358133d6a0a750`.

Import-boundary facts:

- Contract/kernel boundary tests already exist in
  `internal/contracts/compile/compile_test.go` and
  `internal/contracts/compile/layer_boundaries_test.go`.
- `go list ./internal/contracts/...` shows narrow contract imports only:
  `autonomy` imports platform utility code, `compile` imports `autonomy` and
  platform utility code, `pathparse` is standard-library-only, `plan` imports
  `pathparse`, and `writerroute` is standard-library-only.
- There is no `internal/kernel/` tree in this repository today; current
  canonical contract packages live under `internal/contracts/...`.

Phase status implication:

- Phase 0C is `Complete` for baseline/evidence purposes. Phase 0B remains
  `Report-only` because old/new decision parity fixtures are still required
  before enforcement can graduate.

## First Slice Atomization

| Atom id | Status | Invariant | Owner | Expected files | Forbidden files | Verification |
| --- | --- | --- | --- | --- | --- | --- |
| R0A-A | Complete | migration state must not start from false zero | docs / tracker | `docs/idiom-rearchitecture-tracker.md`, this evidence note, smoke brief, smoke target, smoke template | runtime, contracts, prompts, CLI/TUI, backend config | Phase 0A inventory recorded |
| R0A-B | Complete | P0/P1 legacy gaps must be mapped before extraction | docs / tracker | `docs/idiom-rearchitecture-tracker.md`, this evidence note, smoke brief, smoke target, smoke template | runtime, contracts, prompts, CLI/TUI, backend config | old test-plan gap mapping recorded |
| R0C-A | Complete | baseline commands and runtime attribution must be recorded | docs / tracker | `docs/idiom-rearchitecture-tracker.md`, this evidence note, smoke brief, smoke target, smoke template, ignored local smoke config | runtime, contracts, prompts, CLI/TUI, backend config | focused tests, full suite, replay corpus, import-boundary confirmation, and live DeepSeek smoke recorded |
| R0T-P3F-A | Complete, test-only | plan-execute recovery plans need focused coverage before extraction | `internal/runtime/agent` tests | `internal/runtime/agent/plan_execute_recovery_plan_test.go` | runtime source, contracts, prompts, CLI/TUI, backend config | focused recovery-plan tests and full `internal/runtime/agent` package passed |
| R0T-P3F-B | Complete | plan-execute recovery plan builders need one source owner before recovery extraction | `internal/runtime/agent` `CardEngine` | `internal/runtime/agent/card_engine.go`, `internal/runtime/agent/plan_execute_recovery_plan.go`, recovery runner callsites, recovery tests | contracts, prompts, CLI/TUI, backend config | focused recovery-plan tests passed; production recovery builder callsites route through `CardEngine` |
| R0T-P4C-A | Complete, test-only | pre-write runtime brief must narrow to write-only behavior before the first required-path write | `internal/runtime/agent` tests | `internal/runtime/agent/recovery_plan_and_brief_test.go` | runtime source, contracts, prompts, CLI/TUI, backend config | focused pre-write runtime brief tests passed |
| R45-A | Complete, report-only | recovery needs a typed projection before enforcement moves out of runner prompts | `internal/runtime/agent` typed recovery state | `internal/runtime/agent/plan_execute_recovery_state.go`, `internal/runtime/agent/plan_execute_recovery_state_test.go` | contracts, prompts, CLI/TUI, backend config | focused recovery-state tests passed; no runtime enforcement changed |

Schema status:

- `R0A-A` satisfies the implementation atom schema as a documentation/evidence
  atom. TDD obligations are evidence-only inventory checks; blocked tests are
  dirty-scope/no-runtime-edit checks; allowed tests are existing contract and
  runtime projection tests; protocol, terminal, and transport matrices are not
  touched and are marked not applicable until runtime behavior changes.
- `R0A-B` satisfies the schema as a documentation/evidence atom. Its old
  test-plan mapping is complete for P0/P1 rows and it is non-runtime.
- `R0C-A` is complete for the baseline/evidence slice. It remains
  non-runtime: replay corpus evidence, contract/kernel import-boundary
  confirmation, and live DeepSeek telemetry attribution are captured, but no
  runtime authority moved.
- `R0T-P3F-A` is complete as a test-only atom. It added blocked/allowed
  recovery-plan coverage for malformed submit-plan parse, missing-target
  low-value recovery, anchor refresh, repeated correction, and native no-call
  fallback. It did not move runtime source authority; any later recovery-policy
  source atom still needs a protocol/terminal/transport matrix, audit signal,
  rollback trigger, reviewer evidence, and a narrowed dirty write scope.
- `R0T-P3F-B` is complete as the source-owning recovery-builder atom. It
  adopts the dirty split where `CardEngine` owns recovery plan builders and
  production recovery-builder callsites, while
  `plan_execute_recovery_plan.go` owns only value types and rendering. It keeps
  blocker-allowed variants explicit in the engine; it does not change
  contracts, CLI/TUI behavior, or backend configuration.
- `R0T-P4C-A` is complete as a test-only atom. It tightened pre-write runtime
  brief assertions so the surface must remove read/search/shell affordances,
  keep write tools, add write-only guidance, and preserve required edit paths
  plus existing constraints. It did not move runtime source authority; any
  later pre-write-policy source atom still needs a protocol/terminal/transport
  matrix, audit signal, rollback trigger, reviewer evidence, and a narrowed
  dirty write scope.
- `R45-A` is complete as a report-only typed recovery-state atom. It adds
  `PlanExecuteRecoveryState` with recovery id, failed invariant id, action
  class, rejected subject, remaining budget, visible safe alternatives,
  terminal blocker allowance, and recovery-card/protocol projection id derived
  from existing plan-execute state and turn-phase contracts. It does not change
  runtime enforcement.

## Old Test-Plan Gap Mapping

| Legacy gap | Mapping | Status |
| --- | --- | --- |
| Plan-execute recovery plans | `R0T-P3F-A`, `R0T-P3F-B` | covered as focused tests plus source owner split; broader typed recovery-policy extraction still open |
| Pre-write runtime brief | `R0T-P4C-A` | covered as test-only baseline; source extraction still open for later pre-write-policy atom |
| Answer decoration blockers | `R0T-P5A-A` | still open |
| Write-ready redirect detection | `R0T-P2B-A` | still open |
| Source evidence redirects | `R0T-P2B-B` | still open |
| Submit-plan coverage paths | `R0T-P4B-A` | still open |
| Recovery nudges | `R0T-P3E-A` | still open |
| Budget and fingerprint decisions | `R0T-P1B-A` | still open |
| Discovery recovery freshness | `R0T-P3G-A` | still open |
| Mutation retry recovery | `R0T-P3H-A` | still open |
| Write-ready non-write recovery | `R0T-P4D-A` | still open |
| Context-bearing line detection | `R0T-P5B-A` | still open |

The first test-code atom after Phase 0 was `R0T-P3F-A` for
`plan_execute_recovery_plan_test.go`. User migration approval allowed this
test-only edit, but runtime-agent source files still have overlapping dirty
changes and must not be rewritten without a later atom naming exact ownership.
