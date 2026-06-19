# Agent Rearchitecture Design Doc v2

**Status:** DESIGN COMPLETE — target architecture and migration contract are
closed; implementation progress is tracked in
[idiom-rearchitecture-tracker.md](idiom-rearchitecture-tracker.md)
**Reviewers:** Architecture, RecoveryState, EvidenceAnswer, Practicality

**Authority:** [idiom-rearchitecture-tracker.md](idiom-rearchitecture-tracker.md)
is the authoritative cross-repo migration contract. This document is the
runner-internal extraction design subordinate to that tracker. Its numbered
phases are runner extraction slices only; they do not redefine the tracker phase
numbers, cross-surface sequencing, contract ownership, or enforcement gates.

## 0. Implementation Checkpoint — 2026-06-17

This pass treated the design doc as a refactor contract and first restored a
verified baseline before any larger extraction:

- Resolved the dirty merge state by keeping the current root CLI architecture,
  removing stale `internal/agent` and `internal/app` roots, and preserving the
  current plan-execute evidence surface.
- Fixed two extracted-boundary regressions:
  - `GateEngine.SourceGroundingRejection` now delegates to the existing
    source-grounding policy until the full semantics can be moved intact.
  - deterministic no-write/evidence-path recovery methods were restored from
    stubs using the existing shared recovery helpers.
- Removed incomplete duplicate surfaces that increased code without enforcing
  new boundaries:
  - unused `TurnEngine` run-loop copy (`turn_run.go` plus its small wrapper
    files);
  - unused broad AnswerPipeline host/adapter bridge (`runner_processor.go` and
    `answer/pipeline*.go`).
- Removed the plan-execute recovery wrapper surface after `CardEngine` became
  the tested authority for submit-correction, anchor-refresh, repeated
  correction, hidden-only, and native no-call fallback recovery plans.
- Removed the recovery-card constructor/render wrapper surface; recovery-card
  tests and runner helper paths now use `CardEngine` directly, with zero-value
  runner compatibility preserved by defaulting nil engines to `newCardEngine()`.
- Removed the unused standalone reasoning-tool salvage wrapper; runtime salvage
  remains on `Runner`, and reusable context-free salvage remains on `CardEngine`.
- Removed the unused `EvidenceEngine.HasSourceBody` method, matching the design
  contract that source-body checks flow through `EvidenceForPath(...).Level`.
- Trimmed additional unused transitional `EvidenceEngine` methods that had no
  live engine callers; underlying source-evidence helpers remain available to
  current runner code until the full EvidenceEngine migration moves them.
- Removed the unused `GuidanceEngine.NextActionEvent` runner-callback wrapper;
  next-action event transport remains owned by the runner recording/state
  guidance path instead of the guidance surface.
- Routed submit-correction hidden-only and repeated-exhausted production
  prompts through `CardEngine`, removing duplicate local prompt text for the
  submit-plan-only variants while keeping blocker-allowed policy text local.
- Moved the native `submit_plan` correction prompt builder into `CardEngine`
  while preserving the exact native-only prompt shape; the runner now delegates
  to that CardEngine method instead of hand-building the prompt.
- Moved the native `submit_plan` no-call fallback prompt builder into
  `CardEngine`, preserving the exact textual fallback prompt shape and keeping
  generic recovery-plan renderer text out of model-facing fallback prompts.
- Moved the plan-execute anchor-refresh prompt builder into `CardEngine`,
  preserving the exact read-only/source-refresh prompt shape and keeping the
  generic recovery-plan renderer out of the model-facing anchor-refresh branch.
- Moved submit-plan parse-recovery plan construction into `CardEngine` and
  removed the runner prompt wrapper; the runner now keeps only eligibility
  gating before rendering the CardEngine-owned recovery plan.
- Removed the remaining pure native submit-correction and anchor-refresh prompt
  forwarders from `runner_recovery_chat_options.go`; production runner paths now
  call the runner-owned `CardEngine` directly, with nil-safe fallback, so future
  CardEngine instrumentation or test substitution is not bypassed.
- Moved the remaining standalone plan-execute recovery-plan builders for
  low-value candidate actions, grounding-required rejection, and unscoped empty
  `submit_plan` into `CardEngine`, leaving `plan_execute_recovery_plan.go` as
  the shared recovery-plan type/renderer surface instead of a mixed content
  generator.
- Centralized runner-owned `CardEngine` fallback through `r.cardEngine()` for
  recovery-card rendering and next-action/autonomy recovery-card summaries, so
  the JSON next-action state and autonomy surface cannot diverge through
  separate nil-handling paths.
- Verification after the cleanup:
  - `go test ./internal/runtime/agent/...`
  - `go test ./...`
  - `git diff --check`
- Latest focused verification after the anchor-refresh consolidation:
  - `go test ./internal/runtime/agent -run 'AnchorRefresh|InvalidAnchor|StaleAnchor|BoundedSubmitPlanAnchorRefresh|PlanThenExecuteAnchorRefreshRecoveryPlan|PlanThenExecuteAnchorRefreshPromptStaysReadOnlyAndRendererFree|PlanExecuteAnchorRefreshRecoveryCardAdvertisesDerivedReadTools' -count=1`
  - `git diff --check -- internal/runtime/agent/card_engine.go internal/runtime/agent/runner_recovery_chat_options.go internal/runtime/agent/recovery_plan_and_brief_test.go`
- Latest focused verification after the parse-recovery consolidation:
  - `go test ./internal/runtime/agent -run 'SubmitPlanParse|ParseRecovery|PlanExecuteSubmitReadyParseRecoveryBlocksFurtherReads|PlanExecuteStringifiedSubmitPlanParseFailureEmitsRecoveryEventWithoutPrompt|PlanExecuteParseRecovery' -count=1`
  - `git diff --check -- internal/runtime/agent/card_engine.go internal/runtime/agent/runner_recovery_chat_options.go internal/runtime/agent/plan_execute_recovery_plan.go internal/runtime/agent/plan_execute_recovery_plan_test.go internal/runtime/agent/runner_plan_then_execute_guard_test_part06_test.go`
- Latest focused verification after the prompt-forwarder deletion:
  - `go test ./internal/runtime/agent -run 'NativeSubmitPlanCorrection|AnchorRefresh|InvalidAnchor|StaleAnchor|BoundedSubmitPlanAnchorRefresh|PlanThenExecuteNativeSubmitPlanCorrectionPromptStaysNativeOnly|PlanThenExecuteAnchorRefreshPromptStaysReadOnlyAndRendererFree|PlanExecuteMLXSubmitPlanAfterRefreshPromptMatchesNativeTransport|SubmitPlanParse|ParseRecovery|PlanExecuteSubmitPlanParseRecoveryUsesRunnerCardEngine' -count=1`
  - `git diff --check -- internal/runtime/agent/card_engine.go internal/runtime/agent/runner.go internal/runtime/agent/runner_answer_validation.go internal/runtime/agent/runner_recovery_chat_options.go internal/runtime/agent/runner_plan_then_execute_guard_test_part06_test.go internal/runtime/agent/runner_plan_then_execute_guard_test_part11_test.go`
- Latest focused verification after the remaining recovery-plan builder move:
  - `go test ./internal/runtime/agent -run 'LowValueRecoveryPlan|GroundingRequiredRecoveryPlan|EmptySubmitPlanRecovery|UnscopedEmptySubmitPlan|LowValueAction|GroundingRequired|SubmitPlanOnlySurface' -count=1`
  - reviewer also ran `go test ./internal/runtime/agent -count=1`
  - `git diff --check -- internal/runtime/agent/card_engine.go internal/runtime/agent/plan_execute_recovery_plan.go internal/runtime/agent/plan_execute_recovery_plan_test.go internal/runtime/agent/runner.go internal/runtime/agent/runner_plan_then_execute_guard.go`
- Latest focused verification after centralizing runner `CardEngine` fallback:
  - `go test ./internal/runtime/agent -run 'RecoveryCard|NextActionState|TurnPhaseSurface|Autonomy.*Surface|RejectedActionDefaultsNilCardEngine' -count=1`
  - `git diff --check -- internal/runtime/agent/recovery_card.go internal/runtime/agent/runner_state_guidance.go internal/runtime/agent/runner_test_part21_test.go`

The design phases below are the closed target architecture. Future
implementation should continue with tested, behavior-preserving extractions that
remove or consolidate live `Runner` responsibility rather than adding parallel
wrapper surfaces. Any work not yet enforced in code is tracked as an
implementation backlog item in
[idiom-rearchitecture-tracker.md](idiom-rearchitecture-tracker.md), not as an
open design decision.

## 1. Problem Statement

The `internal/runtime/agent` package has grown to **122 `runner_*.go` files** and **~93,000 lines** of code, all organized around a single `Runner` struct. This creates:

- **No boundary enforcement** — any method on `*Runner` can read/write any field.
- **Untestable** — constructing a `Runner` requires ~30 fields; testing one subsystem means instantiating all of them.
- **High cognitive load** — no developer can hold the full Runner in their head.
- **Linear scaling** — every new feature adds another `runner_*.go` file.

## 2. Runner-Internal Target Architecture

Within the tracker architecture, replace the flat `*Runner` monolith with **7
focused sub-systems**, each with a clear interface boundary. The Runner becomes
a thin orchestrator. These engines are runtime adapters over the canonical
contract/kernel decisions; they do not own cross-surface authority.

```
                         Runner (thin orchestrator)
                              ~800 lines, 2 files
       ┌──────────┬──────────┬──┴──────────┬──────────┬──────────┐
       ▼          ▼          ▼             ▼          ▼          ▼
  ┌─────────┐┌─────────┐┌──────────┐┌───────────┐┌─────────┐┌──────────┐
  │TurnEngine││Answer   ││GateEngine││CardEngine ││Evidence ││StateStore│
  │Run loop ││Pipeline ││Gate eval ││Recovery   ││Engine   ││Budget    │
  │Tool     ││Accept   ││Retry     ││cards      ││Source   ││Fingerprints│
  │dispatch ││Decorate ││budgets   ││Nudges     ││Readiness││Edits     │
  │Phase    ││Terminal ││          ││Guidance   ││Obligations││Results   │
  │contract ││         ││          ││           ││          ││Iterations│
  └─────────┘└─────────┘└──────────┘└───────────┘└─────────┘└──────────┘
                              │
                         ┌────┴────┐
                         │Guidance │  (new — detached from RecoveryEngine)
                         │Engine   │
                         │Prompts  │
                         │Nudges   │
                         └─────────┘
```

**Key changes from v1:**
- RecoveryEngine split into **GateEngine** (gates, retry budgets), **CardEngine** (recovery cards, prompts), **GuidanceEngine** (next-action guidance, nudges, closure guidance)
- **VerificationService** added as a cross-cutting concern (not a subsystem, but its responsibilities are explicitly assigned)
- **Autonomy** explicitly mapped to contract packages for authority, TurnEngine
  for active-contract transport, and StateStore for runtime obligation
  snapshots
- `runner_plan_then_execute_guard.go` explicitly partitioned across EvidenceEngine, TurnEngine, RecoveryEngine
- All 61 non-test runner files mapped to a subsystem (see Appendix B)

**Authority boundary:** contract packages own schemas, invariant decisions,
path roles, obligation vocabulary, protocol projection, and audit semantics.
Runner engines may store per-run instance state, transport model-facing
guidance, render typed recovery cards, and execute decisions that cite a
contract id, invariant id, and protocol hash. A runner engine must not compile
authority from prompt text, transcript prose, or local string constants.

## 3. Sub-System Interfaces

### 3.1 StateStore

**Purpose:** Owns all per-run mutable state: tool budget, fingerprints, execution state, edit tracking, recorded results, iteration counters, rejection tracking.

```go
type StateStore interface {
    // Tool results
    RecordResult(req workspace.ToolRequest, result workspace.ToolResult)
    Results() []workspace.ToolResult
    LastResult() (workspace.ToolRequest, workspace.ToolResult, bool)

    // Budget & fingerprints (clean interface types, not runner_ internal structs)
    BudgetState() BudgetState       // defined below
    ComputeFingerprint(req workspace.ToolRequest) string
    TrackFingerprint(fp string, result workspace.ToolResult)

    // Execution state
    ExecutionState() ExecutionState // defined below
    SetAllowedTools(tools []string)
    AllowedTools() []string

    // Edit tracking
    ChangedPaths() []string
    RequiredEditsRemaining() []string
    TrackWritePath(path string)

    // Iteration tracking (NEW — explicitly called out by Recovery/State reviewer)
    GateRejectionCount() int
    IncrementGateRejections()
    ResetGateRejections()
    ProseRetriesRemaining() int
    SetProseRetries(n int)
    DecrementProseRetries() int
    LastGateError() string
    SetLastGateError(err string)

    // Metrics
    Metrics() MetricsSnapshot     // clean interface, not *runnerMetrics
    AddUsage(u llama.Usage)
}

type BudgetState struct {
    StepBudget    int
    StepsUsed     int
    ReadCalls     int
    WriteCalls    int
    NoProgress    int
    RejectedCount int
}

type ExecutionState struct {
    Phase          TurnPhaseID
    Fingerprint    string
    LastActionKind string
    PendingRedirects []string
}
```

**Reviewer issues addressed:**
- [Architecture-HIGH] Returns clean interface types (`BudgetState`, `ExecutionState`, `MetricsSnapshot`), not `*runnerRequestBudgetState`
- [Architecture-MEDIUM] `EmitEvent`/`OnEvent` removed — moved to TurnEngine
- [Architecture-MEDIUM] `LastToolKey()`/`LastToolResult()` removed — `LastResult()` covers the pattern
- [RecoveryState-HIGH] Explicit gate rejection counters, prose retry tracking, last gate error added
- [RecoveryState-MEDIUM] Iteration tracking (`StepsUsed`, `ReadCalls`, `WriteCalls`) in BudgetState
- [Practicality-HIGH] Runner Slice 1 does *not* attempt to fully decouple;
  cross-subsystem hooks must be tracked as named deferred integration notes, not
  unresolved source TODOs.

**Moved files:** `runner_execution_state.go`, `runner_request_budget.go`, `runner_changed_paths.go`, `runner_recording.go` (storage portion), `runner_state.go`, `runner_state_strings.go`, `runner_metrics.go` (distilled), `runner_task_ledger.go` (obligation portion)

### 3.2 TurnEngine

**Purpose:** The main loop — sets up model calls, dispatches tool results, handles reasoning, phase management, event emission, plan-execute planner detection, autonomy contracts.

```go
type TurnEngine interface {
    Run(ctx context.Context, opts *llama.ChatOptions) (string, error)

    // Phase management
    CurrentPhase() TurnPhaseContract
    PhaseFor(results []workspace.ToolResult) TurnPhaseContract

    // Planner phase detection (from runner_plan_then_execute_guard.go)
    PlannerPhase() bool
    PlanSubmissionPhase() bool
    PlannerBrief() workerruntime.WorkerRuntimeBrief

    // Tool dispatch
    ExecuteTool(ctx context.Context, req workspace.ToolRequest) workspace.ToolResult
    BuildToolDefinitions(state StateStore) []llama.Tool // uses state for phase scoping
    BuildToolsForPhase(phase TurnPhaseContract) []llama.Tool

    // Loop control
    ShouldContinue(ctx context.Context, step int, state StateStore) bool
    TerminateEarly(reason string)

    // Events (moved from StateStore per Architecture reviewer)
    OnEvent() func(RunEvent)
    EmitEvent(role, content string)
    EmitModelExchange(payload autonomy.ModelExchangeEvent)

    // Autonomy contracts (NEW — explicitly mapped)
    AutonomyContract(brief workerruntime.WorkerRuntimeBrief) autonomy.AutonomyContract
    EmitAutonomyEvent(event autonomy.TurnPhaseSurfaceSummaryEvent)
}
```

**Reviewer issues addressed:**
- [Architecture-MEDIUM] Context on ExecuteTool and ShouldContinue
- [Architecture-MEDIUM] RebuildTools takes StateStore as a dependency (acceptable — TurnEngine is the orchestrator)
- [Architecture-HIGH] Autonomy explicitly assigned to contract packages for
  derivation; TurnEngine consumes and emits the active contract, and StateStore
  keeps only per-run snapshots/obligation instance state.
- [EvidenceAnswer-HIGH] Planner phase detection from `runner_plan_then_execute_guard.go` mapped here
- [Practicality-MEDIUM] Runner Slice 4 includes specific collapse plan for the remaining ~20 files

**Moved files:** `runner.go` (Run method), `runner_turn_control.go`, `runner_phase_contract.go`, `runner_phase_state.go`, `runner_tool_surface_scope.go`, `runner_recovery_chat_options.go`, `runner_runstate.go`, `runner_lane_policy.go`, `runner_no_tools_surface.go`, `runner_child_tasks.go`, `runner_delegated_synthesis.go`, `runner_autonomy.go` (contract derivation portion), `runner_plan_then_execute_guard.go` (planner phase detection + submit_plan write-only surface)

### 3.3 EvidenceEngine

**Purpose:** All source-body evidence tracking, read-only answerability, source obligations, plan-then-execute source grounding, coverage analysis. Split into two coordinating components: `EvidenceRegistry` (storage) and `ReadOnlyEvaluator` (evaluation).

```go
type EvidenceEngine interface {
    // === EvidenceRegistry (storage) ===
    TrackRead(path string, level EvidenceLevel)
    SourceBodyPaths() []string
    EvidenceForPath(path string) SourceEvidence

    // === ReadOnlyEvaluator (evaluation) ===
    Answerability(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) AnswerabilityLedger
    ClosureDecision(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ReadOnlyClosureDecision

    // === Source obligations ===
    PendingObligations(brief workerruntime.WorkerRuntimeBrief) []SourceObligation
    PendingRedirects(brief workerruntime.WorkerRuntimeBrief) []SourceRedirect
    RequiredSourcePaths() []string
    SourceSatisfied(answer string, results []workspace.ToolResult) bool

    // === Plan-execute source grounding (NEW — from runner_plan_then_execute_guard.go) ===
    ObserveSourceEvidenceForPath(path string, results []workspace.ToolResult) PlanExecuteSourceEvidence
    PathHasOutlineEvidence(path string, results []workspace.ToolResult) bool
    RequiredEditScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string
    SubmitPlanSourceCoverage(edits []workspace.PlanEdit, results []workspace.ToolResult) PlanCoverageResult
}
```

**Reviewer issues addressed:**
- [EvidenceAnswer-HIGH] Plan-execute source grounding methods added (`ObserveSourceEvidenceForPath`, `PathHasOutlineEvidence`, `RequiredEditScope`, `SubmitPlanSourceCoverage`)
- [EvidenceAnswer-HIGH] `ObjectiveRelevance` moved to AnswerPipeline (not here)
- [EvidenceAnswer-HIGH] EvidenceEngine scope acknowledged as large — documented as two coordinating components (Registry + Evaluator) rather than a single blob
- [EvidenceAnswer-MEDIUM] `HasSourceBody(path)` dropped from interface (redundant with `EvidenceForPath().Level`)
- [Architecture-HIGH] `runner_plan_then_execute_guard.go` source evidence partitioned here

**Moved files:** `source_evidence_state.go`, `read_only_answerability.go`,
`read_only_evidence_packet.go`, `read_only_closure.go`,
`read_only_source_owner.go`, `read_only_objective_relevance.go`, `evidence/`
(evaluator.go, evaluators.go, registry.go),
`runner_required_source_recovery.go` (obligation portions),
`runner_plan_then_execute_guard.go` (source evidence + coverage portions),
`runner_source_grounding.go`, `runner_target_source_evidence.go`

**Not moved:**
- `ObjectiveRelevance()` — moved to AnswerPipeline (see §3.4)

### 3.4 AnswerPipeline

**Purpose:** Accept, decorate, terminal classify. (Already partially extracted — PipelineHost + Pipeline in `answer/`.)

```go
// PipelineHost is the Runner surface the answer pipeline needs — now split:
type BasePipelineHost interface {
    Brief() workerruntime.WorkerRuntimeBrief
    AgentName() string
    UserFacingFinalAnswer() bool
}

type PlanExecutePipelineHost interface {
    BasePipelineHost
    PlannerPhase() bool
    SourceSatisfiedAnswerCovers(string, []workspace.ToolResult) bool
    AnchorRefreshPending([]workspace.ToolResult) bool
}

// ObjectiveRelevance query — extracted from EvidenceEngine per EvidenceAnswer reviewer
type ObjectiveRelevance interface {
    RelevanceFor(answer string, results []workspace.ToolResult) RelevanceResult
}

type RelevanceResult struct {
    Status                  answer.RelevanceStatus
    BlockerKind             answer.BlockerKind
    Reason                  string
    RelevantSourceBodyPaths []string
    AnswerObjectiveTerms    []string
}

// AnswerPipeline = answer.Processor (already defined)
```

`RelevanceStatus` and `BlockerKind` are typed answer-domain values. `Reason`
remains diagnostic text only and must not be parsed as authority.

**Reviewer issues addressed:**
- [Architecture-MEDIUM] PipelineHost split into `BasePipelineHost` (3 methods) + `PlanExecutePipelineHost` (embedding + 3 more = 6 total, down from 14)
- [Architecture-MEDIUM] `ReadOnlyDirectAnswerRelevance` returns `RelevanceResult` struct (not 5-tuple)
- [EvidenceAnswer-MEDIUM] PipelineHost methods that route to other engines (SourceGroundingRejectionReason, ComposedFinalAnswer, etc.) documented as targeting Runner Slice 5 removal
- [EvidenceAnswer-HIGH] ~20 standalone agent functions (looksLikeNonAnswerProse, etc.) remain in `agent` package. Pipeline takes them as injected function fields. If circular dep becomes a problem, a new `answer/internal/heuristics/` sub-package is the escape hatch.

**Moved files:** Already in `answer/`. Runner Slice 5 moves the implementations
out of `final_answer_acceptance.go`, `final_answer_decorator.go`,
`runner_terminal_answer.go` into `answer/pipeline_impl.go`.

### 3.5 GateEngine (split from RecoveryEngine)

**Purpose:** Completion gate evaluation, retry budgets, retryability decisions.

```go
type GateEngine interface {
    EvaluateGate(reply string, results []workspace.ToolResult, state StateStore) GateDecision
    RetryBudget(results []workspace.ToolResult) int
    RemainingRetries(state StateStore) int
    Retryable(decision GateDecision, state StateStore) bool

    // Source-grounding gate (from runner_source_grounding.go)
    SourceGroundingRejection(answer string, results []workspace.ToolResult) string
}

type GateDecision struct {
    Accepted    bool
    Kind        GateDecisionKind
    InvariantID autonomy.InvariantID
    Reason      string
    HideReads   bool
    EvidencePaths []string
}
```

`GateDecisionKind` is a typed value object owned by the contract/runtime-policy
boundary. New gate outcomes require an invariant id and blocked/allowed tests;
free-form string categories are not valid target architecture.

**Reviewer issues addressed:**
- [Architecture-MEDIUM] Split from RecoveryEngine as primary design (not just risk mitigation)
- [RecoveryState-HIGH] Rejection tracking on StateStore feeds into GateEngine

**Moved files:** `runner_completion_gate.go`, `recovery_mutation.go` (gate-level retry logic), `runner_final_answer_retry.go`, `runner_source_grounding.go` (gate rejection portions), `runner_plan_then_execute_guard.go` (anchor refresh gate)

### 3.6 CardEngine (split from RecoveryEngine)

**Purpose:** Recovery card generation, recovery prompts, submit-plan recovery, source-refresh recovery.

```go
type CardEngine interface {
    BuildCard(decision GateDecision, state StateStore, phase TurnPhaseContract) RecoveryCard
    Prompt(decision GateDecision, state StateStore) (prompt string, hideReads bool)
    BlockerAnswer(result workspace.ToolResult, results []workspace.ToolResult) string

    // Plan-then-execute recovery (from runner_required_source_recovery.go)
    SubmitPlanRecovery(req workspace.ToolRequest, result workspace.ToolResult, state StateStore) (RecoveryAction, bool)
    SourceRefreshRecovery(paths []string, state StateStore) (RecoveryAction, bool)
    AnchorRefreshResponse(state StateStore) RecoveryAction

    // Verification intercept
    VerificationReadRedirect(req workspace.ToolRequest, state StateStore) *workspace.ToolResult
}

type RecoveryAction struct {
    Kind    RecoveryActionKind
    InvariantID autonomy.InvariantID
    Message string
    Tool    string  // optional — the tool to redirect to
    Target  string  // optional — the target path/symbol
}
```

`RecoveryActionKind` is a typed value object. Recovery actions that restrict
tools, redirect reads, request `submit_plan`, or terminate work are valid only
when the active phase contract allows that action class. Read-only strategy
lanes may receive advisory guidance, but readiness alone must not hide relevant
read tools, force a host-composed answer, force `submit_plan`, or terminate a
turn.

**Reviewer issues addressed:**
- [Architecture-MEDIUM] CardEngine handles "content generation" — recovery cards, prompts, blocker answers
- [RecoveryState-MEDIUM] `SubmitPlanRecovery` and `SourceRefreshRecovery` return structured `RecoveryAction` (not free-form strings)
- [Architecture-HIGH] Verification intercept explicitly assigned here
- [Architecture-MEDIUM] Submit-plan redirect/rejection recovery from plan-execute guard mapped here

**Moved files:** `recovery_card.go`, `recovery_mutation.go` (card-level), `runner_required_source_recovery.go` (recovery portions), `runner_verification_intercept.go`, `runner_verification.go`, `runner_deterministic_recovery.go`, `runner_visible_tool_salvage.go`, `runner_preimage_checkpoint.go`, `runner_plan_then_execute_guard.go` (submit-plan rejection + anchor refresh recovery)

### 3.7 GuidanceEngine (split from RecoveryEngine)

**Purpose:** Next-action guidance, phase guidance, closure guidance, scratchpad guidance, brief hints — everything that shapes what the model sees next.

```go
type GuidanceEngine interface {
    NextActionGuidance(state StateStore, phase TurnPhaseContract) GuidanceState
    PhaseGuidance(brief workerruntime.WorkerRuntimeBrief, phase TurnPhaseContract) GuidanceState

    // Closure guidance (from runner_closure_guidance.go)
    ClosureActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool
    ApplyClosureBias(state GuidanceState, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) GuidanceState

    // Scratchpad guidance (from runner_scratchpad_guidance.go)
    ScratchpadCloseoutApplied(state GuidanceState) GuidanceState

    // Brief hints (from runner_brief_hints.go)
    EnrichBrief(brief workerruntime.WorkerRuntimeBrief, phase TurnPhaseContract) workerruntime.WorkerRuntimeBrief

    // Recovery nudges (from runner_recovery_nudges.go)
    RecoveryNudge(decision GateDecision, state StateStore) string
}

type GuidanceState struct {
    Constraints      []string
    NextValidActions []autonomy.ActionClass
    InvalidActions   []autonomy.ActionClass
    EvidenceStatus   []string
    ContractID       autonomy.ContractID
    InvariantID      autonomy.InvariantID
    Reason           string
    Phase            TurnPhaseID
}
```

Closure guidance is advisory unless a real harness invariant is violated.
`ClosureActive` and `ApplyClosureBias` may shape wording for answer-ready
states, but they must not become authority for no-tools turns in read-only
strategy lanes.

**Reviewer issues addressed:**
- [Architecture-MEDIUM] GuidanceEngine is a separate subsystem (not folded into a single RecoveryEngine)
- [RecoveryState-MEDIUM] All guidance files (`runner_closure_guidance.go`, `runner_scratchpad_guidance.go`, `runner_state_guidance.go`, `runner_brief_hints.go`) mapped here
- [RecoveryState-MEDIUM] GuidanceState struct provides structured output (not free-form)

**Moved files:** `runner_closure_guidance.go`, `runner_scratchpad_guidance.go`, `runner_state_guidance.go`, `runner_brief_hints.go`, `runner_recovery_nudges.go`, `runner_post_readiness.go`

### 3.8 Verification (cross-cutting)

Verification is not a separate subsystem but its responsibilities are explicitly assigned:

| Responsibility | Assigned to |
|---|---|
| Verification result parsing | CardEngine (as part of intercept) |
| Verification read redirects | CardEngine (`VerificationReadRedirect`) |
| Verification source sanity (language diagnostics) | EvidenceEngine (source evidence) |
| Verification-bootstrap tool setup | TurnEngine (tool dispatch) |

**Reviewer issues addressed:**
- [Architecture-HIGH] Verification now has a home — explicitly mapped across three subsystems

## 4. Runner Extraction Sequence

This sequence is subordinate to the tracker. Use labels `Runner Slice 1` through
`Runner Slice 5` in implementation atoms to avoid confusing these internal
runner slices with tracker Phases 1 through 9.

### Runner Slice 1: StateStore — Internal Extraction

**Goal:** Extract storage into `StateStore` interface within `agent/` package. Cross-subsystem event/registry/recovery calls remain explicit deferred integration points with named owners and tracker rows, not loose source TODO hooks.

**Scope:** ~5,000 lines.

**Test strategy:**
- Build `NewTestStateStore(t) StateStore` constructor
- Existing `runner_test_part*.go` tests keep working (Runner wraps StateStore)
- No behavioral change

**Verification:**
- `go build ./internal/runtime/agent/...`
- `go test ./internal/runtime/agent/...`
- `deadcode ./internal/runtime/agent/...`

### Runner Slice 2: EvidenceEngine

**Goal:** Consolidate ~12,000 lines from ~20 files into EvidenceEngine with Registry + Evaluator components.

**Scope:** ~12,000 lines.

**Key partitioning:** `runner_plan_then_execute_guard.go` source evidence + coverage logic moves here.

**Test strategy:**
- `NewTestEvidenceEngine(t) EvidenceEngine` — constructs with in-memory registry
- Reduced-integration tests: `NewTestRunner(t, WithEvidenceEngine(mock))`
- Existing full-integration tests kept as safety net

**Verification:** Same as Runner Slice 1.

### Runner Slice 3: GateEngine + CardEngine + GuidanceEngine

**Goal:** Split recovery logic into three focused subsystems. `runner_plan_then_execute_guard.go` submit-plan rejection + anchor refresh logic moves to CardEngine/GateEngine.

**Scope:** ~15,000 lines total (~5,000 each).

**Test strategy:** `NewTestGateEngine(t)`, `NewTestCardEngine(t)`, `NewTestGuidanceEngine(t)`.

### Runner Slice 4: TurnEngine Consolidation

**Goal:** Collapse the ~20 remaining runner files into ~6 TurnEngine files. `runner_plan_then_execute_guard.go` planner detection moves here.

**Files to consolidate** (from Appendix B):
- `runner.go` (Run method) → `turn_run.go`
- `runner_lane_policy.go` + `runner_no_tools_surface.go` → `turn_lane.go`
- `runner_turn_control.go` + `runner_runstate.go` → `turn_control.go`
- `runner_phase_contract.go` + `runner_phase_state.go` → `turn_phase.go`
- `runner_child_tasks.go` + `runner_delegated_synthesis.go` + `runner_delegate_edit.go` + `runner_evidence_first.go` + `runner_scoped_implementation.go` + `runner_review_repair.go` → `turn_dispatch.go`
- `runner_tool_surface_scope.go` + `runner_tool_rewrite.go` + `runner_message_history.go` + `runner_control_rails.go` + `runner_budget_diag.go` → `turn_tools.go`
- `runner_autonomy.go` (contract derivation portion) + `runner_plan_execute_terminal.go` + `runner_apply_patch_preflight.go` + `runner_recovery_chat_options.go` → `turn_options.go`

**Target files:** 7 (from ~22)

**Scope:** ~15,000 lines.

### Runner Slice 5: Answer Pipeline Finalization

**Goal:** Move Accept/Decorate/Terminate implementations into `answer.Pipeline`, remove function-field bridge.

**Scope:** ~4,000 lines.

## 5. Deployment Strategy

**Slice-per-PR on main.** Each runner slice lands independently on main after
its tracker atom gates are satisfied. This forces genuine backward compatibility
and prevents long-lived branch divergence.

**Internal extraction first.** All interfaces and implementations live in
`agent/` until Runner Slice 5. No sub-package moves until the final runner
slice. This avoids import cycles during the transition.

**Feature gates.** `if r.stateStore != nil { ... }` guards let old and new code coexist within the same PR. The old path is removed in the next PR.

**PR size limit.** Each runner-slice PR is capped at ~5,000 lines changed
(additions + deletions). If a slice exceeds this, split it into multiple
stacked PRs.

**CI gate for each PR:**
```bash
go build ./internal/runtime/agent/...
go vet ./internal/runtime/agent/...
go test ./internal/runtime/agent/...
deadcode ./internal/runtime/agent/...
grep -rn "POSSIBLY ORPHANED"  # from cross-reference check
```

## 6. Expected Outcomes

| Metric | Current | Target |
|---|---|---|
| `runner_*.go` production files | 61 | ~35 |
| `*Runner` fields in each subsystem | All 30+ | Subsystem owns its fields |
| Run() method length | 3,895 lines | ~800 lines |
| Cross-file method coupling | ~200 methods | ~40 interface methods |
| Time to add new feature | High (find the right file) | Low (implement interface + slot in) |

**File count rationale (Practicality reviewer feedback):** Target is ~35, not ~25. The lane-specific files (`runner_lane_policy.go`, `runner_implementation_*.go`, `runner_delegated_*.go`) may legitimately resist consolidation into the 7 subsystems. The count target is flexible — interface boundaries matter more than file count.

## 7. Dead Code Detection

After each phase:
```bash
# Dead function detection
deadcode ./internal/runtime/agent/...

# Cross-reference — find Runner methods with zero callers
for method in $(grep -rn 'func (r \*Runner) ' internal/runtime/agent/runner_*.go \
  | grep -v '_test' | sed 's/.*func (\*Runner) \([A-Za-z0-9_]*\)(.*/\1/'); do
  count=$(grep -rn "\.${method}(" internal/runtime/agent/ --include='*.go' \
    | grep -v '_test.go' | grep -v "$(grep -rl "func (r \*Runner) $method" \
    internal/runtime/agent/runner_*.go)" | wc -l)
  [ "$count" -eq 0 ] && echo "POSSIBLY ORPHANED: $method"
done
```


## 9. Appendix A: Dependency Graph

### Subsystem Dependencies

| Subsystem | Depends on | Depended on by | Key risk |
|---|---|---|---|
| **StateStore** | Nothing | Everything | Foundation — must be first |
| **EvidenceEngine** | StateStore (reads Results, AllowedTools) | GateEngine, CardEngine, GuidanceEngine, TurnEngine | Shared file: `runner_plan_then_execute_guard.go` source evidence with TurnEngine |
| **GateEngine** | StateStore (rejection counts, results) | TurnEngine | Shared file: `runner_recovery_mutation.go` gate vs card logic with CardEngine |
| **CardEngine** | StateStore, EvidenceEngine (source obligations) | TurnEngine | Shared file: `runner_required_source_recovery.go` obligations with EvidenceEngine |
| **GuidanceEngine** | StateStore, EvidenceEngine (answerability) | TurnEngine | None — cleanest dependency boundary |
| **TurnEngine** | All of the above | Nothing (orchestrator) | Highest coupling — must come last |
| **AnswerPipeline** | PipelineHost (Runner adapter) | Nothing (called by TurnEngine) | Standalone — no subsystem deps |

### Shared File Partitioning Risks

These files contain logic that belongs to **multiple** subsystems and must be carefully partitioned during migration:

| File | Subsystems | Partition approach |
|---|---|---|
| `runner_plan_then_execute_guard.go` | EvidenceEngine + TurnEngine + CardEngine | Extract source evidence → EvidenceEngine, phase detection → TurnEngine, reject recovery → CardEngine |
| `runner_autonomy.go` | TurnEngine + StateStore | Contract derivation → TurnEngine, obligation recording → StateStore |
| `runner_recording.go` | StateStore + TurnEngine | Storage → StateStore, event emission → TurnEngine |
| `runner_required_source_recovery.go` | CardEngine + EvidenceEngine | Recovery prompts → CardEngine, obligation queries → EvidenceEngine |
| `runner_recovery_mutation.go` | GateEngine + CardEngine | Gate decisions → GateEngine, card building → CardEngine |
| `runner_telemetry_compaction.go` | StateStore + TurnEngine | Metrics → StateStore, event shaping → TurnEngine |


## 10. Appendix B: Complete File Mapping

All 61 non-test `runner_*.go` files mapped to a subsystem:

| File | Lines | Subsystem | Notes |
|---|---|---|---|
| runner.go | 3,895 | TurnEngine | Run() splits; struct stays until Runner Slice 5 |
| runner_autonomy.go | 3,084 | TurnEngine (contracts) + StateStore (obligations) | Split across both |
| runner_plan_then_execute_guard.go | 4,780 | Partitioned: EvidenceEngine + TurnEngine + CardEngine | **Largest partitioning** |
| runner_lane_policy.go | 2,660 | TurnEngine | Lane dispatch stays here |
| runner_no_tools_surface.go | 1,432 | TurnEngine | Tool-less surface for phases |
| runner_phase_contract.go | 421 | TurnEngine | Phase contract derivation |
| runner_phase_state.go | 349 | TurnEngine | Next-action state building |
| runner_turn_control.go | 1,078 | TurnEngine | Turn controller |
| runner_recovery_chat_options.go | 1,263 | TurnEngine | Chat option building |
| runner_runstate.go | 421 | TurnEngine | Run state + retry |
| runner_child_tasks.go | 721 | TurnEngine | Child task dispatch |
| runner_delegated_synthesis.go | 666 | TurnEngine | Delegated synthesis |
| runner_recording.go | 1,412 | StateStore (storage) + TurnEngine (events) | Split across both |
| runner_request_budget.go | 1,015 | StateStore | Budget + fingerprints |
| runner_execution_state.go | ~150 | StateStore | Execution state |
| runner_state.go | 243 | StateStore | State types |
| runner_state_strings.go | 66 | StateStore | State string helpers |
| runner_state_guidance.go | 1,224 | GuidanceEngine | Next-action state event |
| runner_closure_guidance.go | 119 | GuidanceEngine | Closure guidance |
| runner_scratchpad_guidance.go | 225 | GuidanceEngine | Scratchpad guidance |
| runner_brief_hints.go | 451 | GuidanceEngine | Brief hints |
| runner_post_readiness.go | 452 | GuidanceEngine | Post-readiness guidance |
| runner_changed_paths.go | ~500 | StateStore | Changed path tracking |
| runner_implementation_scope.go | 1,083 | TurnEngine (scope) + StateStore (edits) | Write-ready surface |
| runner_implementation_brief.go | 1,092 | TurnEngine | Phase brief builder |
| runner_plan_execute_terminal.go | 1,241 | TurnEngine | Terminal surface |
| runner_required_source_recovery.go | 1,256 | CardEngine (recovery) + EvidenceEngine (obligations) | Split across both |
| runner_completion_gate.go | 208 | GateEngine | Gate invocation |
| runner_final_answer_retry.go | ~100 | GateEngine | Retry decision |
| runner_source_grounding.go | 663 | GateEngine (gate) + EvidenceEngine (evidence) | Split across both |
| runner_verification.go | 678 | CardEngine | Verification result handling |
| runner_verification_intercept.go | 227 | CardEngine | Verification redirects |
| runner_verification_bootstrap.go | 6 | TurnEngine | Bootstrap tools for verify |
| runner_recovery_mutation.go | ~900 | CardEngine (cards) + GateEngine (gates) | Split across both |
| runner_recovery_discovery.go | ~800 | CardEngine | Discovery recovery |
| runner_recovery_card.go | ~1,300 | CardEngine | Recovery card building |
| runner_recovery_nudges.go | 504 | GuidanceEngine | Recovery nudges |
| runner_deterministic_recovery.go | 242 | CardEngine | Deterministic recovery |
| runner_visible_tool_salvage.go | 772 | CardEngine | Tool salvation |
| runner_preimage_checkpoint.go | 350 | CardEngine | Preimage checkpointing |
| runner_telemetry_compaction.go | 1,078 | StateStore (metrics) + TurnEngine (events) | Telemetry construction |
| runner_debug_log.go | 1,220 | Cross-cutting | Utility — stays |
| runner_apply_patch_preflight.go | 1,135 | TurnEngine | Apply_patch preflight |
| runner_patterns.go | ~200 | Cross-cutting | Utility — stays |
| runner_terminal_answer.go | 876 | AnswerPipeline | Terminal classification |
| runner_terminal.go | ~100 | AnswerPipeline | Terminal state |
| runner_final_answer_quality.go | ~200 | AnswerPipeline | Quality checks |
| runner_delegate_edit.go | 293 | TurnEngine | Delegate edit dispatch |
| runner_scoped_implementation.go | 78 | TurnEngine | Scoped implementation |
| runner_review_repair.go | 397 | TurnEngine | Review-repair |
| runner_control_rails.go | 175 | TurnEngine | Control rails |
| runner_reasoning_answer_salvage.go | 527 | AnswerPipeline | Answer salvage |
| runner_message_history.go | 114 | TurnEngine | Message history |
| runner_tool_rewrite.go | 169 | TurnEngine | Tool rewrite |
| runner_target_source_evidence.go | 398 | EvidenceEngine | Target evidence |
| runner_task_ledger.go | 494 | StateStore (obligations) + TurnEngine (events) | Split |
| runner_evidence_first.go | 418 | TurnEngine | Evidence-first mode |
| runner_budget_diag.go | ~200 | StateStore | Budget diagnostics |
| runner_prefix_diagnostics.go | ~200 | Cross-cutting | Utility |
| runner_recording.go | 1,412 | StateStore + TurnEngine | Split |
| runner_telemetry_compaction.go | 1,078 | StateStore + TurnEngine | Split |


## 11. Appendix C: Verification and Deployment (from Practicality Reviewer)

### PR Strategy
- **Slice-per-PR on main** — never a long-lived branch
- **Internal extraction first** — interfaces and impls stay in `agent/` package; no sub-package moves until Runner Slice 5
- **Feature gates** — `if r.stateStore != nil { ... }` guards for coexistence
- **PR size cap** — ~5,000 lines changed max; split if larger

### CI Gate
```bash
go build ./internal/runtime/agent/...
go vet ./internal/runtime/agent/...
go test ./internal/runtime/agent/...
deadcode ./internal/runtime/agent/...
# Cross-reference Runner orphans
```

### Dead Code Detection
After each phase, run `deadcode` + grep-based cross-reference check (see §7).

## 12. Appendix D: Handling Standalone Agent Functions

~23 standalone functions (e.g., `looksLikeNonAnswerProse`, `readOnlyClosureDecisionForResults`) remain in the `agent` package and are called by the answer pipeline.

**Current checkpoint:** The incomplete `answer.Pipeline` function-field bridge
and `runner_processor.go` adapter were removed on 2026-06-17 because they were
not wired into production and widened the surface without reducing live Runner
responsibility. The standalone functions remain in `agent` until Runner Slice 5 moves
the implementation intact.

**Future resolution:** When Runner Slice 5 is resumed, introduce the answer pipeline
only with a live caller and a narrow host surface. Prefer moving pure-string
heuristics into an `answer/internal/heuristics/` package before adding a broad
adapter back to `agent`.

**Escape hatch:** If the injection surface grows too large (>10 functions), create `answer/internal/heuristics/` sub-package where pure-string heuristics can live without importing `agent`.
