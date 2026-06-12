package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func (r *Runner) emitNextActionState(step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) {
	if r == nil || r.OnEvent == nil {
		return
	}

	brief := r.visibleBriefForToolSurface(r.RuntimeBrief, results)
	state, phaseContract := buildRunnerNextActionStateWithAllowedToolsAndContract(r.agentName(), step, reason, req, result, results, declarationMovesForbidden, brief, r.currentAllowedTools)

	if r.activeAutonomy != nil {
		r.activeAutonomy.emitTurnPhaseSurfaceSummary(turnPhaseSurfaceSummaryFromNextActionState(state, phaseContract), phaseContract)

		if strings.TrimSpace(result.Error) != "" {
			card := BuildRecoveryCard(RecoveryCardInput{
				PhaseContract:       phaseContract,
				CurrentAllowedTools: effectiveRecoveryCardAllowedTools(r.currentAllowedTools, brief, results, phaseContract),
				RejectedRequest:     req,
				RejectedResult:      result,
				EvidenceRefs:        state.EvidencePaths,
			})
			r.activeAutonomy.emitTurnPhaseSurfaceSummary(card.TurnPhaseSurfaceSummary(phaseContract), phaseContract)
		}
	}

	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	r.OnEvent(RunEvent{Role: "next_action_state", Content: string(data)})
}

func (r *Runner) runtimeBriefWithLiveState(policy RunPolicy, messages []llama.ChatMessage, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	if r == nil {
		return workerruntime.WorkerRuntimeBrief{}
	}

	brief := r.RuntimeBrief
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}

	if strings.TrimSpace(brief.Objective) == "" {
		brief.Objective = latestUserObjective(messages)
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		brief.TargetPacket = mergeRunnerStateStrings(brief.TargetPacket, reviewRepairPhaseEvidencePacket(brief.Objective))
	}

	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps:       policy.MaxToolSteps,
			MaxModelCalls:      policy.MaxModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		}
	}

	liveEvidencePaths := runnerEvidencePathsForBrief(brief, results, 8)
	if readOnlyDirectAnswerLane(brief) {
		if len(liveEvidencePaths) > 0 {
			brief.EvidencePaths = liveEvidencePaths
		}
	} else {
		brief.EvidencePaths = mergeRunnerStateStrings(brief.EvidencePaths, liveEvidencePaths)
	}

	brief.ChangedPaths = mergeRunnerStateStrings(brief.ChangedPaths, mapKeys(changedPathsFromToolResults(results)))
	brief = runnerBriefWithPhaseHints(brief, results)
	brief.Readiness = runnerReadinessState(r.agentName(), brief, results)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerReadinessState(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *workerruntime.ReadinessState {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	readiness := &workerruntime.ReadinessState{}
	readOnlyBrief := brief

	if readOnlyDirectAnswerLane(brief) {
		readOnlyBrief = readOnlyDirectAnswerAcceptanceLedgerBrief(brief)
	}

	readOnlyLedger := readOnlyAnswerabilityForResults(readOnlyBrief, results)
	addClosed := func(values ...string) {
		readiness.ClosedClaims = mergeRunnerStateStrings(readiness.ClosedClaims, values)
	}
	addOpen := func(values ...string) {
		readiness.OpenObligations = mergeRunnerStateStrings(readiness.OpenObligations, values)
	}
	addSource := func(values ...string) {
		readiness.EvidenceSources = mergeRunnerStateStrings(readiness.EvidenceSources, values)
	}

	if len(brief.TargetRefs) > 0 {
		addSource("prompt_target_refs")
	}

	if len(brief.TargetPacket) > 0 {
		addSource("context_packet")
		addClosed("context_packet_available")
	}

	if len(brief.EvidencePaths) > 0 {
		addSource("evidence_paths")
	}

	if len(brief.RequiredEditPaths) > 0 {
		addSource("required_edit_paths")
		addClosed("required_edit_paths_define_write_scope")
	}

	if len(brief.RequiredEditSnippets) > 0 {
		addSource("required_edit_snippets")
		addClosed("required_edit_snippets_available")
	}

	if len(results) > 0 {
		addSource("tool_results")
	}

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		addSource("discovery_packet")
		addClosed("composite_discovery_packet_available")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceContextPacket) {
		addSource("context_packet")
		addClosed("context_packet_localized_candidates")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceSearchHit) {
		addSource("search_hits")
		addClosed("search_hits_localized_candidates")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceOutline) {
		addSource("source_outline")
		addClosed("source_outline_localized_candidates")
	}

	if len(readOnlyLedger.RelevantSourceBodyPaths) > 0 {
		addSource("source_body")
		addClosed("relevant_source_body_evidence")
	}

	if readOnlyLedger.DiagnosticEvidence {
		addSource("diagnostic_evidence")
		addClosed("diagnostic_evidence_available")
	}

	if delegatedReadOnlyClosureReady(agent, brief, results) {
		readiness.ReadyToAnswer = true

		addClosed("answer_readiness_reached")

		readiness.FurtherReadsRequire = "one exact missing claim, one exact probe, and why the current evidence would be materially incomplete"
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		readiness.ReadyToAnswer = true

		addClosed("child_packet_available")

		readiness.FurtherReadsRequire = "one exact packet-anchored missing claim and one exact probe"
	}

	changed := changedPathsFromToolResults(results)
	if len(changed) > 0 {
		addSource("changed_paths")
		addClosed("workspace_changes_present")
	}

	if hasSuccessfulVerificationResult(results) {
		addSource("verification_result")
		addClosed("verification_result_observed")
	}

	if sourceEditsAreSemanticallyVerified(brief, results) {
		addClosed("source_aware_verification_after_source_edit")
	}

	if runnerBriefRequiresMutation(brief) {
		if planThenExecutePlannerBrief(brief) && len(brief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
			addSource("required_source_evidence_contract")

			if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6); pending != "" {
				addOpen("pending_required_source_anchors: " + pending)

				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else {
				addClosed("required_source_anchors_ready")
			}
		} else if len(brief.RequiredEditPaths) > 0 {
			addSource("required_source_evidence_contract")

			if implementationRequiredPathAnchorsReady(brief, results) {
				addClosed("required_source_anchors_ready")
			} else if len(results) > 0 {
				if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6); pending != "" {
					addOpen("pending_required_source_anchors: " + pending)
				}
			}
		}

		missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)
		switch {
		case len(brief.RequiredEditPaths) > 0 && len(changed) == 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true

			addOpen("required_edit_paths_not_changed")

			if planThenExecutePlannerBrief(brief) && planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 1) != "" {
				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else if exactAnchorPreWriteSurfaceActive(brief, results) {
				readiness.FurtherReadsRequire = "no further reads before the first exact-anchor write; edit an in-scope required path or report <<QUESTION>> with the stale or missing exact anchor"
			} else {
				readiness.FurtherReadsRequire = "only semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact; otherwise edit an in-scope required path or report <<QUESTION>> with the exact path-specific blocker"
			}
		case len(missing) > 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true

			addOpen("required_edit_paths_remaining: " + strings.Join(missing, ", "))

			readiness.FurtherReadsRequire = "resolve each remaining required path with a write, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> naming the exact blocker"
		case len(brief.RequiredEditPaths) > 0:
			addClosed("required_edit_paths_changed")
		case len(changed) == 0 && targetPacketSupportsWrite(brief):
			readiness.ReadyToWrite = true

			addOpen("workspace_change_not_attempted")

			readiness.FurtherReadsRequire = "one exact edit-safety gap grounded in the context packet"
		}

		switch {
		case hasPendingVerificationAfterWrites(results, brief):
			readiness.ReadyToAnswer = false

			if plannerOwnsVerification(brief) {
				if sourceVerificationGateApplies(brief, results) {
					addOpen("planner_owned_source_lane_sanity_pending")
				} else {
					addOpen("planner_owned_verification_status_pending")
				}
			} else {
				readiness.ReadyToVerify = true

				if sourceVerificationGateApplies(brief, results) {
					addOpen("source_aware_verification_pending")
				} else {
					addOpen("verification_pending_after_write")
				}
			}
		case sourceEditsNeedSemanticVerification(brief, results):
			if plannerOwnsVerification(brief) {
				readiness.ReadyToAnswer = false
				readiness.ReadyToVerify = true

				addOpen("planner_owned_source_lane_sanity_pending")

				readiness.FurtherReadsRequire = "compiler-backed source-sanity or language diagnostics for the changed source path, or a concrete lane-sanity blocker if source sanity is unavailable"
			} else {
				readiness.ReadyToAnswer = false
				readiness.ReadyToVerify = true

				addOpen("source_aware_verification_pending")
			}
		case len(changed) > 0 && len(missing) == 0:
			readiness.ReadyToAnswer = true
		}
	} else if runnerVerificationRequired(brief) && !hasSuccessfulVerificationResult(results) {
		readiness.ReadyToAnswer = false
		readiness.ReadyToVerify = true

		addOpen("verification_result_missing")
	} else if readOnlyDirectAnswerLane(brief) && readOnlyLedger.Answerable() {
		readiness.ReadyToAnswer = true

		addClosed("read_only_evidence_ready")

		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if readOnlyDirectAnswerLane(brief) {
		if len(readOnlyLedger.CandidatePaths) > 0 {
			addOpen("read_only_source_body_evidence_pending")

			readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
		}
	} else if readOnlyAnalysisReadyFromEvidence(results) {
		readiness.ReadyToAnswer = true

		addClosed("read_only_evidence_ready")

		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if len(readOnlyLedger.CandidatePaths) > 0 {
		addOpen("read_only_source_body_evidence_pending")

		readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
	}

	return workerruntime.NormalizeReadinessState(readiness)
}

func targetPacketSupportsWrite(brief workerruntime.WorkerRuntimeBrief) bool {
	return len(brief.TargetPacket) > 0 &&
		(len(brief.TargetRefs) > 0 ||
			len(brief.EvidencePaths) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0)
}

func readOnlyAnalysisReadyFromEvidence(results []workspace.ToolResult) bool {
	if !readOnlyClosureDecisionForResults(workerruntime.WorkerRuntimeBrief{}, results).Answerable() {
		return false
	}

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		return readOnlyAnalysisExactProbeCount(results) >= 1 && len(promptEvidencePathsFromToolResults(results, 6)) >= 2
	}

	return readOnlyAnalysisExactProbeCount(results) >= 2 && len(promptEvidencePathsFromToolResults(results, 6)) >= 1
}
