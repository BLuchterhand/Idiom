package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func runnerLivePhase(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) string {
	if phase := debugLogPhase(brief, results); phase != "" {
		return phase
	}

	if phase := evidenceFirstMutationPhase(brief, results); phase != "" {
		return phase
	}

	return workerruntime.ClassifyPhase(req, result)
}

func debugLogPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if !debugLogContractApplies(brief, results) {
		return ""
	}

	if debugLogEvidenceSatisfied(brief, results) {
		return "log_analysis"
	}

	if _, ok := latestDebugLogSearchHit(results); ok {
		return "failure_window"
	}

	selection, ok := latestDebugLogSelection(results)
	if !ok {
		return "latest_log_selection"
	}

	if hasDebugLogReadForSelection(results, selection) {
		return "selected_log_analysis"
	}

	return "latest_log_tail"
}

func evidenceFirstMutationPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return ""
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "post_target_write"
	}

	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "source_evidence"
	}

	if targetReadCount == 0 {
		return "target_entry"
	}

	return "target_inspection"
}

func buildRunnerNextActionState(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerNextActionState {
	return buildRunnerNextActionStateWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, nil)
}

func buildRunnerNextActionStateWithAllowedTools(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) runnerNextActionState {
	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools)
	return state
}

func buildRunnerNextActionStateWithAllowedToolsAndContract(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) (runnerNextActionState, TurnPhaseContract) {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	brief = runnerBriefWithPhaseHints(brief, results)

	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		Constraints:               runnerGuidanceConstraints(agent, brief, results),
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Phase:             runnerLivePhase(brief, req, result, results),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          tool,
		LastTarget:        workerruntime.ToolRequestTarget(req),
		ResultSummary:     util.Truncate(strings.TrimSpace(result.Summary), 220),
		ResultError:       util.Truncate(strings.TrimSpace(result.Error), 260),
		ResultOutputChars: len(result.Output),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
	}
	// When context_pack returns a view with candidate files that carry
	// symbol handles, surface the top symbols as actionable annotations.
	if strings.TrimSpace(tool) == workspace.ToolNameContextPack && strings.TrimSpace(result.Error) == "" {
		state.CandidateSymbols = contextPackCandidateSymbols(result.Output, 5)
	}

	if status := scratchpadDeliverableFailedEvidenceStatus(brief, results); status != "" {
		state.EvidenceStatus = []string{status}
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") && delegatedReadOnlyClosureReady(agent, brief, results) {
		state.EvidencePaths = delegatedClosureEvidencePaths(brief.Objective, results, 6)
	}

	if !runnerBriefRequiresMutation(brief) && !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		state.EvidencePaths = postChildClosureEvidencePaths(brief.Objective, results, 6)
	}

	state.Constraints = util.Merge(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = util.Merge(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = util.Merge(brief.RequiredNext, guidance.ValidNext)

	phaseHint := ""

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		phaseHint = turnPhaseReviewRepairTerminal
	}

	phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:        phaseHint,
		Brief:            brief,
		Results:          results,
		LastRequest:      req,
		LastResult:       result,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
	})

	state = applyTurnPhaseContractToNextActionState(state, phaseContract)

	if strings.TrimSpace(result.Error) != "" {
		state.RecoveryCard = RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
			PhaseContract:       phaseContract,
			CurrentAllowedTools: effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, phaseContract),
			RejectedRequest:     req,
			RejectedResult:      result,
			EvidenceRefs:        state.EvidencePaths,
		}))
	}

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		state.Constraints = util.Merge(state.Constraints, []string{
			"review_repair_closeout_gate_fired",
			"terminal_review_repair_outcome_required",
		})
		state.InvalidNextActions = reviewRepairTerminalInvalidNextActions()
		state.NextValidActions = reviewRepairTerminalValidNextActions()
		state.StateSummary = runnerStateSummary(state)

		return state, phaseContract
	}

	state = applyDelegatedReadOnlyClosureBias(state, agent, brief, results)
	state = applyPostChildClosureBias(state, agent, brief, results)
	state = applyScratchpadDeliverableCloseoutBias(state, brief, results)
	state = applyReadOnlyDiscoveryPendingGuidance(state, brief, req, result, results)
	state = applyClosurePromptRefinement(state)
	state.StateSummary = runnerStateSummary(state)

	return state, phaseContract
}

func applyReadOnlyDiscoveryPendingGuidance(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) runnerNextActionState {
	if strings.TrimSpace(state.Phase) != "read_only_evidence_pending" || !readOnlyDirectAnswerLane(brief) {
		return state
	}

	if len(runnerEvidencePathsForBrief(brief, results, 1)) > 0 {
		return state
	}

	strength := discoveryConsumptionResultStrength(req, result)
	if strength == discoveryConsumptionNone {
		return state
	}

	candidates := readOnlySourceLikePaths(discoveryConsumptionCandidatesFromResult(result, 4), 4)
	if len(candidates) == 0 {
		return state
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	candidateSummary := strings.Join(candidates, ", ")

	state.Constraints = util.Merge(state.Constraints, []string{"source_body_evidence_missing_after_discovery"})
	state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
		"final answer before source-body evidence",
		"treating discovery candidates, context packets, or search hits as source-body evidence",
	})

	switch strength {
	case discoveryConsumptionCandidateOnly:
		state.EvidenceStatus = util.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate-only; source owner and source-body evidence are still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"source owner was not established by " + tool + "; use one materially different source-owner or source-body probe scoped to the question, or <<QUESTION>> with the exact unavailable evidence",
			"if candidates are plausible, inspect one returned path with source-body evidence; otherwise switch to an exact lexical/symbol/source-owner probe drawn from the user's claim and the code vocabulary",
		}, state.NextValidActions)
		state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
			"exact replay of the same weak discovery request",
		})
	case discoveryConsumptionSourceOwner:
		state.EvidenceStatus = util.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate owner paths returned, but source-body evidence is still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"inspect one returned path or handle with source-body evidence, take one targeted refinement with concrete scope or named_gap only if the returned owner does not establish the claim, or <<QUESTION>> with the exact blocker",
			"candidate path(s): " + candidateSummary,
		}, state.NextValidActions)
		state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
			"repeated broad or identical rediscovery before inspecting or materially refining returned candidates",
		})
	}

	return state
}

func buildRunnerFailureFeedbackPacket(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerFailureFeedbackPacket {
	return buildRunnerFailureFeedbackPacketWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, nil)
}

func buildRunnerFailureFeedbackPacketWithAllowedTools(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) runnerFailureFeedbackPacket {
	state := buildRunnerNextActionStateWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools)
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		EvidencePaths:             state.EvidencePaths,
		Constraints:               state.Constraints,
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})

	if scratchpadFailedEvidenceFeedbackApplies(brief, req, result, results) {
		recovery.DoNotRepeat = util.AppendUnique(recovery.DoNotRepeat, "same failed evidence lookup through a lower-level or raw read")
		recovery.RequiredNext = util.AppendUnique(recovery.RequiredNext, "record the failed, stale, ambiguous, or out-of-scope evidence as a deliverable gap")
		recovery.ValidNext = []string{
			"write the non-source deliverable with the evidence gap recorded",
			"answer with <<QUESTION>> only if the evidence gap makes the deliverable impossible",
		}
	}

	// failure_feedback is emitted right after next_action_state, which
	// already carries Lane/Step/Reason/InvalidNext/ValidNext/StateSummary
	// for the same step. Re-emitting those fields wastes context. Keep
	// only the failure-specific signals (FailedTool/FailedTarget for
	// quick reference, FailureClass, the recovery-specific
	// DoNotRepeat/RequiredNext, and the UserVisibleBlocker flag). Tests
	// still rely on RequiredNext/ValidNext carrying closure-biased
	// options, so ValidNext keeps recovery.ValidNext (the *new* options
	// the recovery plan introduces) but drops the duplicates that
	// next_action_state already published.
	packet := runnerFailureFeedbackPacket{
		Lane:               state.Lane,
		Step:               state.Step,
		Reason:             state.Reason,
		FailedTool:         state.LastTool,
		FailedTarget:       state.LastTarget,
		FailureClass:       recovery.FailureClass,
		ResultSummary:      state.ResultSummary,
		ResultError:        state.ResultError,
		EvidenceStatus:     append([]string(nil), state.EvidenceStatus...),
		ValidNext:          append([]string(nil), recovery.ValidNext...),
		RecoveryCard:       state.RecoveryCard,
		UserVisibleBlocker: recovery.UserVisibleBlocker,
	}

	packet.DoNotRepeat = append([]string(nil), recovery.DoNotRepeat...)
	packet.RequiredNext = append([]string(nil), recovery.RequiredNext...)

	if phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            runnerBriefWithPhaseHints(brief, results),
		Results:          results,
		LastRequest:      req,
		LastResult:       result,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
	}); phaseContract.PhaseID != "" && phaseContract.PhaseID != turnPhaseGeneric && !scratchpadFailedEvidenceFeedbackApplies(brief, req, result, results) {
		packet.ValidNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.RequiredNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.InvalidNext = append([]string(nil), phaseContract.InvalidNext...)
	}

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		packet.FailureClass = "terminal_outcome_required"
		packet.DoNotRepeat = reviewRepairTerminalInvalidNextActions()
		packet.RequiredNext = []string{
			"choose exactly one terminal review-repair outcome using the gathered evidence",
		}
		packet.ValidNext = reviewRepairTerminalValidNextActions()
		packet.InvalidNext = reviewRepairTerminalInvalidNextActions()
		packet.StateSummary = runnerStateSummary(state)
	}

	return packet
}

func effectiveRecoveryCardAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, contract TurnPhaseContract) []string {
	if len(contract.AllowedTools) > 0 {
		return append([]string(nil), contract.AllowedTools...)
	}

	if len(contract.ActionPolicy.CallableToolNames) > 0 {
		return append([]string(nil), contract.ActionPolicy.CallableToolNames...)
	}

	if strings.TrimSpace(contract.PhaseID) == turnPhaseStaleAnchorRefresh {
		return planThenExecuteAnchorRefreshToolNames()
	}

	if len(currentAllowedTools) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}

	if len(brief.AllowedTools) > 0 {
		return append([]string(nil), brief.AllowedTools...)
	}

	return nil
}
