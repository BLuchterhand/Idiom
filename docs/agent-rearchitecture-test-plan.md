# Pre-Migration Test Coverage Plan

**Goal:** Add targeted test coverage in the highest-risk, lowest-coverage areas **before** the rearchitecture migration, so regressions are caught early and each phase's "Verify: all tests pass" gate is meaningful.

**Current coverage:** 82.5% overall. The gaps cluster in migration-critical subsystems.

---

## Phase 1 (StateStore): Coverage gaps in `runner_recording.go`, `runner_request_budget.go`, `runner_execution_state.go`

These files contain the mutable state that StateStore will own. Low coverage here means extraction bugs slip silently.

### P1-A: `runner_recording.go` — result recording and tool-message formatting

Current: ~50-60% hit by integration tests, but the edge cases (tool failure, recovery card appending, multi-result ordering) are untested.

**Test the following:**

| Behavior | File:Line | Why critical |
|---|---|---|
| `recordWithOptions` with rejected tool (error present) | runner_recording.go:88-145 | Error paths are poorly covered; extraction preserves error formatting |
| `modelFacingToolResult` with recovery card appending | runner_recording.go:180-250 | Card appending into tool messages has no direct test |
| Multiple results with same tool key — ordering | runner_recording.go:~300 | Order preservation matters for evidence qualification |
| `toolResultMessage` formatting for each transport mode | runner_recording.go:400-600 | 3+ message formats; only native toolcall is tested |

### P1-B: `runner_request_budget.go` — fingerprint and budget decisions

Current: ~40% line coverage. Core budget logic is exercised by integration tests, but many decision branches are untested.

| Behavior | File:Line | Why critical |
|---|---|---|
| `fingerprintDecision` — all 5 decision types (allow/reject/block/force/complete) | runner_request_budget.go:~200 | Budget decisions gate tool execution; extraction must preserve decision logic |
| `zeroHitSearchFamilyDecision` — retry exhaustion | runner_request_budget.go:~600 | 0% covered in isolation — only tested through full-run integration |
| `samePathFixationDecision` — threshold detection | runner_request_budget.go:~500 | Fixation detection prevents tool loops; extraction must preserve counts |
| `terminalArtifactRejectedRetryDecision` — retry exhaustion path | runner_request_budget.go:~700 | 0% covered — artifact rejection retries have no focused test |
| `explorationDecision` — nudge thresholds | runner_request_budget.go:~800 | Exploration delegation nudges only tested through child task runs |

### P1-C: `runner_execution_state.go` — state transitions

| Behavior | File:Line | Why critical |
|---|---|---|
| `attachInstructionsForTouchedPaths` — path matching | runner_execution_state.go:461 | 25% coverage — instruction attachment logic is dense with path parsing |
| `applyResult` — each result type handler | runner_execution_state.go:100-300 | Key state machine transitions under-tested |

---

## Phase 2 (EvidenceEngine): Coverage gaps in source tracking, answerability, evidence

This is the **largest consolidation** (~12,000 lines from ~20 files). Coverage gaps here mean bugs in evidence qualification go undetected.

### P2-A: `read_only_answerability.go` — the answerability ledger builder

Current: ~60% line coverage (hit by integration tests), but the ~40 ledger construction paths are exercised unevenly.

| Behavior | File:Line | Why critical |
|---|---|---|
| Path from `sourceEvidenceForPath` / `sourceEvidenceBodyPaths` with mixed result types (reads + search + error) | source_evidence_state.go:40-120 and read_only_answerability.go:80-280 | Ledger construction has ~20 branches; test coverage is path-dependent |
| `candidatePath` extraction from code_navigate outputs with no body | read_only_answerability.go:150-250 | Routed vs non-routed tool results produce different candidate sets |
| `sourceBodyTaskPaths` vs `sourceBodyObjectivePaths` separation | read_only_answerability.go:~400 | Objective vs task path separation is subtle; extraction must preserve the distinction |
| `requiredSourceBodyObjectiveTerms` — threshold calculations | read_only_answerability.go:~900 | Collective evidence threshold logic has no direct unit test |
| Diagnostic path relevance flag | read_only_answerability.go:~1200 | Diagnostic relevant state only set through full read-only runs |

### P2-B: `source_evidence_state.go` — obligation tracking

| Behavior | File:Line | Why critical |
|---|---|---|
| `sourceEvidencePendingRedirects` for each redirect kind | source_evidence_state.go:129-200 | Each of the 5+ redirect types (read_file→code_navigate, anchor refresh, etc.) has different trigger conditions |
| `sourceEvidencePendingRedirectForPath` with pending vs resolved | source_evidence_state.go:293-350 | Path resolution tracking crosses from EvidenceEngine into RecoveryEngine territory |
| `implementationTargetWriteReadyRedirectedSourceTool` | source_evidence_state.go:720 | Only 0% covered — write-ready redirect detection entirely untested |

### P2-C: `read_only_evidence_packet.go` — delegated discovery packets

| Behavior | File:Line | Why critical |
|---|---|---|
| Packet answerability — `readOnlyEvidencePacketAnswerableForObjective` | read_only_evidence_packet.go:~400 | Packet answerability judges whether delegated results suffice; 0% covered at unit level |
| `stripNestedFallbackTemplate` — response template parsing | read_only_evidence_packet.go:1468 | 21% coverage — response template extraction has subtle formatting logic |

### P2-D: `read_only_source_owner.go` — source owner matching

| Behavior | File:Line | Why critical |
|---|---|---|
| `sourceOwnerMatchedTerms` / `sourceOwnerMatchedWeakTerms` for each owner role | read_only_source_owner.go:120-260 | Owner matching has 3 roles with different term patterns; only 30% covered |

---

## Phase 3 (GateEngine + CardEngine + GuidanceEngine): Coverage gaps in recovery

This is the second-largest migration. Recovery logic has the most code paths per function and the lowest coverage.

### P3-A: `recovery_card.go` — recovery card building

| Behavior | File:Line | Why critical |
|---|---|---|
| `RenderRecoveryCard` with each invariant ID | recovery_card.go:108-150 | Each of 15+ invariant IDs produces different card content; only ~3 are tested |
| `recoveryCardActionClassesExactly` with overlapping classes | recovery_card.go:463 | Action class filtering gates tool visibility |
| Recovery card for plan-execute phases | recovery_card.go:500-700 | Phase-specific cards (submit-ready, submit-correction, anchor refresh) have distinct layouts |

### P3-B: `recovery_mutation.go` — mutation recovery

| Behavior | File:Line | Why critical |
|---|---|---|
| `mutationRejectedRetryWriteRecovery` — each rejection reason | recovery_mutation.go:43-200 | 16% coverage — the mutation retry flow has 5+ rejection reason branches |
| `retryableFinalAnswerAcceptanceRejection` — complete decision table | recovery_mutation.go:1624 | Has ~10 branches (lane type + blocker kind + tools available); only 2 paths are tested |

### P3-C: `runner_completion_gate.go` — gate pipeline

| Behavior | File:Line | Why critical |
|---|---|---|
| `completionGateRejection` — each pipeline check | runner_completion_gate.go:15-60 | 60% covered — the pipeline has 5 checks (evidence, substantive, search-hit, unexecuted, process-intent); the unexecuted-tool and process-intent paths are untested |
| `crossWorkspaceLogToolError` — error formatting | runner_completion_gate.go:156 | 33% covered — log error extraction in completion context |

### P3-D: `recovery_discovery.go` — discovery recovery

| Behavior | File:Line | Why critical |
|---|---|---|
| `recoverContextPackFreshness` — staleness detection | recovery_discovery.go:604 | 15% covered — context pack staleness drives recovery prompts |
| Plan-execute discovery recovery (breadth search, outline fallback) | recovery_discovery.go:400-600 | PTE-specific discovery recovery only tested through full-run integration |

### P3-E: `runner_recovery_nudges.go` — recovery nudges

| Behavior | File:Line | Why critical |
|---|---|---|
| `debugLogInvestigationRecoveryNudge` — nudge thresholds | runner_recovery_nudges.go:211 | 9% covered — debug log nudges fire at specific read-to-write ratios |
| Generic recovery nudge for loop detection | runner_recovery_nudges.go:~100 | Loop detection nudges have no focused unit test |

### P3-F: `plan_execute_recovery_plan.go` — PTE recovery plans

| Behavior | File:Line | Why critical |
|---|---|---|
| All 8 recovery plan functions | plan_execute_recovery_plan.go:174-311 | **0% coverage on every function** — this entire file is untested. Submit correction, anchor refresh, repeated correction, hidden-only fallback — all uncovered. |

---

## Phase 4 (TurnEngine): Coverage gaps in run-loop and phase management

The Run() method itself is 3,895 lines — impossible to unit test. But the helper functions extracted into TurnEngine should each be testable.

### P4-A: `runner_implementation_scope.go` — write-ready surface

| Behavior | File:Line | Why critical |
|---|---|---|
| `implementationPreWriteNonWriteRecovery` — recovery tool hiding | runner_implementation_scope.go:525 | 13% covered — write-ready tool hiding is a key recovery action |
| `deterministicPlanExecuteSourceSatisfiedEvidenceRead` | runner_implementation_scope.go:740 | 16% covered — deterministic read synthesis for source-satisfied claims |
| `planExecuteGroundingSearchPattern` — search pattern generation | runner_implementation_scope.go:802 | 0% covered — search pattern for plan execute grounding |
| `toolNameOverlapsObjective` | runner_implementation_scope.go:858 | 0% covered — tool-to-objective relevance filtering |

### P4-B: `runner_plan_then_execute_guard.go` — the largest file

| Behavior | File:Line | Why critical |
|---|---|---|
| `applyPlanProjectionEdit` — edit projection | runner_plan_then_execute_guard.go:993 | 26% covered — edit projection determines whether a plan edit is valid |
| `PlanThenExecuteSourceSatisfiedMissingCoveragePaths` | runner_plan_then_execute_guard.go:3641 | 0% covered — this function determines whether submit_plan has sufficient source coverage |
| `planThenExecuteRequiredEditSnippetsSourceSatisfied` | runner_plan_then_execute_guard.go:3849 | 31% covered — required snippet analysis for source-satisfied claims |
| `reviewRepairPhaseEvidenceSectionHasEntries` | runner_plan_then_execute_guard.go:3303 | 41% covered — review-repair evidence section detection |

### P4-C: `runner_implementation_brief.go` — phase brief construction

| Behavior | File:Line | Why critical |
|---|---|---|
| `implementationPreWriteRuntimeBrief` | runner_implementation_brief.go:21 | **0% covered** — this constructs the brief that gates tool visibility for pre-write phase |

### P4-D: `runner_autonomy.go` — autonomy event building

| Behavior | File:Line | Why critical |
|---|---|---|
| `runnerAutonomyGatePackID` — pack ID for gate events | runner_autonomy.go:967 | 46% covered — pack ID construction for autonomy gate events |
| `runnerAutonomyPolicyFrictionInvalidAction` | runner_autonomy.go:2379 | 40% covered — invalid action detection in autonomy policy |

---

## Phase 5 (AnswerPipeline): Coverage gaps in answer decoration

The pipeline methods themselves have good coverage (~80%) but specific decoration branches are uncovered.

### P5-A: `final_answer_decorator.go` — decoration paths

| Behavior | File:Line | Why critical |
|---|---|---|
| `presentableQuestionBlockerDeterministicAnswerSupported` | final_answer_decorator.go:199 | **0% covered** — `<<QUESTION>>` blocker detection in decoration |
| `sourceEvidenceLineCarriesAnswerContext` | final_answer_decorator.go:1766 | **0% covered** — context-bearing line detection |
| `sourceEvidenceAnswerLinesForObjective` | final_answer_decorator.go:1696 | 16% covered — objective-relevant line extraction |
| `sourceOwnerEvidenceForBestAnchor` | final_answer_decorator.go:2027 | **0% covered** — source owner → anchor matching in decoration |
| `presentableExactProbeAnchorObjectiveScore` | final_answer_decorator.go:2032 | **0% covered** — anchor scoring for exact-probe presentation |
| `writeFailureTargetPath` | final_answer_decorator.go:2450 | 46% covered — write failure path extraction |
| `presentableGroundedAnswerFromRenderedPacket` | final_answer_decorator.go:695 | 36% covered — packet-based answer rendering |
| `extractMalformedWriteBlockerReason` | final_answer_decorator.go:375 | 44% covered — malformed write detection |

### P5-B: `runner_terminal_answer.go` — terminal classification

| Behavior | File:Line | Why critical |
|---|---|---|
| `hostOwnedReadOnlyCloseoutAccepted` — closeout conditions | runner_terminal_answer.go:75 | Each of the 4 preconditions has different failure paths |
| `emitFinalAnswerTelemetry` — event payload shaping | runner_terminal_answer.go:142 | Telemetry event fields must match schema; extraction must preserve field names |

---

## Summary: Priority Matrix

| Priority | Phase | Area | File | Current coverage | Risk |
|---|---|---|---|---|---|
| **P0** | 3 | PTE recovery plans | `plan_execute_recovery_plan.go` | **0%** on 8 functions | Will silently break when extracted |
| **P0** | 4 | Pre-write runtime brief | `runner_implementation_brief.go:21` | **0%** | Brief construction gates tool visibility |
| **P0** | 5 | Answer decoration blockers | `final_answer_decorator.go:199,2027,2032` | **0%** | Question-blocker detection untested |
| **P0** | 2 | Write-ready redirect tool | `source_evidence_state.go:720` | **0%** | Write-ready source detection |
| **P1** | 3 | Recovery nudges | `runner_recovery_nudges.go:211` | **9%** | Nudge threshold logic |
| **P1** | 2 | Obligation redirects | `source_evidence_state.go:129-350` | ~25% | Redirect state machine |
| **P1** | 3 | Discovery recovery freshness | `recovery_discovery.go:604` | **15%** | Freshness detection |
| **P1** | 3 | Mutation retry recovery | `recovery_mutation.go:43` | **16%** | Rejection reason branching |
| **P1** | 4 | Write-ready non-write recovery | `runner_implementation_scope.go:525` | **13%** | Tool hiding in write-ready |
| **P1** | 4 | Source-satisfied coverage paths | `runner_plan_then_execute_guard.go:3641` | **0%** | Submit_plan coverage |
| **P1** | 5 | Context-bearing line detection | `final_answer_decorator.go:1766` | **0%** | Source line context |
| **P2** | 3 | Mutation retryability decision table | `recovery_mutation.go:1624` | ~30% | 10-branch decision |
| **P2** | 2 | Objective vs task path separation | `read_only_answerability.go:~400` | ~40% | Path classification |
| **P2** | 4 | Grounding search pattern | `runner_implementation_scope.go:802` | **0%** | Search pattern generation |
| **P2** | 4 | Edit projection | `runner_plan_then_execute_guard.go:993` | 26% | Plan edit validity |
| **P2** | 1 | Fingerprint decision types | `runner_request_budget.go:~200` | ~40% | Budget decision branching |
| **P2** | 1 | Instruction attachment | `runner_execution_state.go:461` | 25% | Path matching |

**Total of 25 targeted test additions.** This is ~1,500-2,000 lines of new test code covering the highest-risk migration paths — a 2-3% increase in test code against the existing 73k lines of tests.
