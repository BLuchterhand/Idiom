package agent

import (
	"context"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

// runnerRunState is the bridge between Runner.Run's loop-local variables and
// extracted phase helpers. Keeping the handoff explicit avoids hidden captures
// while Run is still being split apart incrementally.
type runnerRunState struct {
	ctx     context.Context
	policy  RunPolicy
	brief   workerruntime.WorkerRuntimeBrief
	metrics runnerMetrics

	opts             llama.ChatOptions
	collectedResults []workspace.ToolResult
	turnMessages     []llama.ChatMessage
	turnHiddenTools  map[string]bool

	stepNumber       int
	activeToolCall   *llama.ToolCall
	assistantMessage llama.ChatMessage
	budgetExhausted  bool
	stopReason       string

	currentAllowedTools                   []string
	implementationWriteOnlyRecoveryActive bool
	requiredPathCompletionRecoveryActive  bool
	exactAnchorWriteOnlyActive            bool

	executionState                  *runnerExecutionState
	implementationScopedHiddenTools map[string]bool
	delegatedReadOnlyAnswerReady    bool
	postChildPacketActive           bool
	directAnswerSurfaceEmitted      bool

	planThenExecuteNativeSubmitPlanDisabled        bool
	planThenExecuteNativeSubmitPlanNoCallFallbacks int
	planThenExecuteReadinessNudged                 bool
	planThenExecuteCorrectionAfterRefreshNudged    bool
	planThenExecuteUnreadSourceNudgedFor           string
	planThenExecuteReadySourceRefreshes            int
	planThenExecuteLoopBudgetRecoveryUsed          bool

	deferredGitHistorySubmitPlan    *workspace.ToolRequest
	deferredGitHistorySubmitPlanRaw string

	replayedToolRequests map[string]int

	autonomyRecorder                *runnerAutonomyRecorder
	completionGateRejections        int
	completionStatusEmitted         bool
	completionStatusLastStatus      string
	completionStatusLastBlockerKind string
	acceptedAnswerSignalsEmitted    bool

	controller runnerTurnController
}

// setCurrentAllowedTools syncs the Runner-level currentAllowedTools field.
func (r *Runner) setCurrentAllowedTools(state *runnerRunState, tools []string) {
	tools = append([]string(nil), tools...)
	if state != nil {
		state.currentAllowedTools = append(state.currentAllowedTools[:0], tools...)
	}

	r.currentAllowedTools = append(r.currentAllowedTools[:0], tools...)
}

// syncCurrentAllowedToolsFromOptions rebuilds currentAllowedTools from opts.Tools.
func (r *Runner) syncCurrentAllowedToolsFromOptions(state *runnerRunState) {
	state.currentAllowedTools = state.currentAllowedTools[:0]
	for _, tool := range state.opts.Tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name != "" {
			state.currentAllowedTools = append(state.currentAllowedTools, name)
		}
	}

	r.setCurrentAllowedTools(state, state.currentAllowedTools)
}

// terminalSubmitPlanNativeTools reports whether native submit_plan is enabled.
func (r *Runner) terminalSubmitPlanNativeTools(state *runnerRunState) bool {
	return !state.planThenExecuteNativeSubmitPlanDisabled && r.supportsTerminalSubmitPlanNativeTools(state.collectedResults)
}

// syncPlanThenExecuteRecoveryAllowedTools rebuilds allowed tools from opts and
// overrides with plan-execute recovery surface callable tools.
func (r *Runner) syncPlanThenExecuteRecoveryAllowedTools(state *runnerRunState) {
	r.syncCurrentAllowedToolsFromOptions(state)

	if !r.planThenExecutePlannerPhase() {
		return
	}

	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh, PlanExecuteRecoverySurfaceHistoryPending:
		if len(state.opts.Tools) == 0 || !state.opts.ForceNativeTools {
			state.currentAllowedTools = append(state.currentAllowedTools[:0], surface.State.CallableTools...)
			r.setCurrentAllowedTools(state, state.currentAllowedTools)
		}
	case PlanExecuteRecoverySurfaceSubmitReady, PlanExecuteRecoverySurfaceSubmitCorrection:
		state.currentAllowedTools = append(state.currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
		r.setCurrentAllowedTools(state, state.currentAllowedTools)
	}
}

// rebuildRunTools is the central tool-rebuild function. It reads state for
// hidden-tool tracking, write-only/scoped-edit surfaces, execution state,
// and post-readiness contracts, then writes state.opts.Tools and calls sync.
func (r *Runner) rebuildRunTools(state *runnerRunState) {
	if r.Workspace == nil {
		return
	}

	allowed := r.advertisedAllowedTools(r.Workspace.AllowedTools())
	writeOnlySurface := state.implementationWriteOnlyRecoveryActive ||
		state.requiredPathCompletionRecoveryActive ||
		implementationPreWriteToolSurfaceActive(r.RuntimeBrief, state.collectedResults)
	scopedEditSurface := !writeOnlySurface && implementationScopedEditSurfaceActive(r.RuntimeBrief, state.collectedResults)

	if scopedEditSurface {
		hideImplementationScopedToolsForTurn(state.turnHiddenTools, state.implementationScopedHiddenTools, r.Workspace)
	} else {
		releaseHiddenTools(state.turnHiddenTools, state.implementationScopedHiddenTools)
	}

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) && !state.executionState.verificationAttempted {
		filtered := make([]string, 0, 1)

		for _, t := range allowed {
			if t == "shell" {
				filtered = append(filtered, t)
			}
		}

		allowed = filtered
	}

	if runnerBriefAllowsExternalToolAppend(r.RuntimeBrief) && !writeOnlySurface && !scopedEditSurface {
		if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
			seen := make(map[string]struct{}, len(allowed))
			for _, name := range allowed {
				seen[name] = struct{}{}
			}

			for _, name := range r.Workspace.AvailableExternalToolNames() {
				if _, ok := seen[name]; !ok {
					allowed = append(allowed, name)
					seen[name] = struct{}{}
				}
			}
		}
	}

	if writeOnlySurface {
		allowed = implementationPreWriteAllowedTools(allowed)
	} else if scopedEditSurface {
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief:            r.RuntimeBrief,
			Results:          state.collectedResults,
			BaseAllowedTools: allowed,
		})

		allowed = append([]string(nil), contract.AllowedTools...)

		if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
			for _, name := range r.Workspace.AvailableExternalToolNames() {
				if extReg.IsReadOnlyDiscovery(name) && !containsRunnerTool(allowed, name) {
					allowed = append(allowed, name)
				}
			}
		}
	} else if contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          state.collectedResults,
		BaseAllowedTools: allowed,
	}); contract.PhaseID == turnPhaseImplementationTargetSource ||
		contract.PhaseID == turnPhaseImplementationVerifyReady ||
		contract.PhaseID == turnPhaseImplementationWriteReady {

		allowed = append([]string(nil), contract.AllowedTools...)
	}

	if surfaceTools := DirectAnswerSurfaceForBriefTools(r.RuntimeBrief); surfaceTools != nil {
		allowed = append([]string(nil), surfaceTools...)

		if !state.directAnswerSurfaceEmitted {
			if surface, ok := DirectAnswerSurfaceForBrief(r.RuntimeBrief); ok {
				EmitDirectAnswerSurfaceEvent(r.emitEvent, surface)

				state.directAnswerSurfaceEmitted = true
			}
		}
	}

	if len(state.turnHiddenTools) > 0 {
		filtered := make([]string, 0, len(allowed))
		for _, t := range allowed {
			if !state.turnHiddenTools[t] {
				filtered = append(filtered, t)
			}
		}

		allowed = filtered
	}

	var tools []llama.Tool

	if len(allowed) > 0 || !workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		tools = buildLlamaTools(r.Workspace.BuildToolDefinitions(allowed))
		if !workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) &&
			r.lanePolicy().ShouldHaveNoteTool(r.RuntimeBrief.Contract) {

			tools = append(tools, noteToolDefinition())
			if !slices.Contains(r.RuntimeBrief.AllowedTools, noteToolName) {
				r.RuntimeBrief.AllowedTools = append(r.RuntimeBrief.AllowedTools, noteToolName)
			}
		}
	}

	if contract := postReadinessToolContractForBrief(r.RuntimeBrief, state.collectedResults, state.delegatedReadOnlyAnswerReady, state.postChildPacketActive); contract.RequireSourceRefreshContract {
		tools = stripPostReadinessBroadTools(tools)
		tools = applyPostReadinessSchemaRequirements(tools)
	}

	state.opts.Tools = tools
	r.syncCurrentAllowedToolsFromOptions(state)

	if len(state.opts.Tools) == 0 {
		state.opts.ToolChoice = ""
	} else if strings.TrimSpace(state.opts.ToolChoice) == "" {
		state.opts.ToolChoice = "auto"
	}
}

// refreshPlanThenExecuteTerminalSurface refreshes the terminal surface.
func (r *Runner) refreshPlanThenExecuteTerminalSurface(state *runnerRunState, reason string) {
	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh:
		r.refreshPlanThenExecuteAnchorRefreshSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		r.refreshPlanThenExecuteTerminalOutcomeSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSubmitReady, PlanExecuteRecoverySurfaceSubmitCorrection:
	default:
		r.prunePlanThenExecuteTerminalSurfaceHistory(&state.turnMessages, reason)

		return
	}

	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// forcePlanThenExecuteTerminalSurface unconditionally forces the terminal surface.
func (r *Runner) forcePlanThenExecuteTerminalSurface(state *runnerRunState, reason string) {
	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// injectPlanThenExecuteRecoveryTail handles the common boilerplate at the end
// of every plan-execute recovery injection.
func (r *Runner) injectPlanThenExecuteRecoveryTail(state *runnerRunState, prompt string, eventRole string, surfaceKey string) {
	r.refreshPlanThenExecuteTerminalSurface(state, surfaceKey)

	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	r.rebuildRunTools(state)

	state.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(state.opts, r.RuntimeBrief, state.collectedResults, r.terminalSubmitPlanNativeTools(state))
	r.syncPlanThenExecuteRecoveryAllowedTools(state)
	r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
}

// transitionPlanThenExecuteSubmitReadyTerminal applies the full submit-ready
// runtime surface. Prompt-only transitions are unsafe here: once the model is
// told to call submit_plan, currentAllowedTools must agree even if stale
// read-only native tools are still present in chat options.
func (r *Runner) transitionPlanThenExecuteSubmitReadyTerminal(state *runnerRunState, prompt string, eventRole string, surfaceKey string) {
	r.refreshPlanThenExecuteTerminalSurface(state, surfaceKey)
	state.opts = planThenExecuteReadyToSubmitChatOptionsForBackend(state.opts, r.RuntimeBrief, state.collectedResults, r.terminalSubmitPlanNativeTools(state))
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	r.syncPlanThenExecuteRecoveryAllowedTools(state)
	r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
}

// emitStateCompletionStatus records completion status and delegates to Runner method.
func (r *Runner) emitStateCompletionStatus(state *runnerRunState, payload runnerCompletionStatus) {
	state.completionStatusEmitted = true
	state.completionStatusLastStatus = strings.TrimSpace(payload.Status)
	state.completionStatusLastBlockerKind = strings.TrimSpace(payload.BlockerKind)
	r.emitCompletionStatus(payload)
}

// emitTerminalAnswerSignalsOnce emits final-answer signals exactly once.
func (r *Runner) emitTerminalAnswerSignalsOnce(state *runnerRunState, decision terminalAnswerDecision) {
	if state.acceptedAnswerSignalsEmitted || strings.TrimSpace(decision.RawAnswer) == "" {
		return
	}

	switch decision.Outcome {
	case terminalAnswerAccepted:
		if state.autonomyRecorder != nil {
			reasonCode := "final_answer_accepted"
			if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedAnswerIntent(decision.RawAnswer) {
				reasonCode = "plan_source_satisfied_short_circuit"
			}

			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyAllow, reasonCode, state.collectedResults, state.completionGateRejections)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)
	case terminalAnswerBlocked, terminalAnswerTransportBlocked:
		blockerKind := util.FirstNonEmpty(strings.TrimSpace(decision.Acceptance.BlockerKind), strings.TrimSpace(decision.StopReason))
		if state.autonomyRecorder != nil {
			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, blockerKind, state.collectedResults, state.completionGateRejections)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)

		if terminalBlockedStatusShouldEmit(state.completionStatusEmitted, state.completionStatusLastStatus, state.completionStatusLastBlockerKind, blockerKind) {
			// Construct the runnerCompletionStatus once, shared by the legacy event emission below.
			changedPaths := mapKeys(changedPathsFromToolResults(state.collectedResults))
			completionPayload := runnerCompletionStatus{
				Lane:               r.agentName(),
				TaskFamily:         strings.TrimSpace(r.RuntimeBrief.TaskFamily),
				Contract:           strings.TrimSpace(r.RuntimeBrief.Contract),
				Status:             "blocked",
				Reason:             strings.TrimSpace(decision.Acceptance.Reason),
				BlockerKind:        strings.TrimSpace(blockerKind),
				RequiresEdit:       runnerBriefRequiresMutation(r.RuntimeBrief),
				EditAttempted:      len(changedPaths) > 0,
				ChangedPaths:       changedPaths,
				UserVisibleBlocker: true,
			}
			r.emitStateCompletionStatus(state, completionPayload)
		}
	}

	state.acceptedAnswerSignalsEmitted = true
}

// finish is the terminal answer path for Run().
func (r *Runner) finish(state *runnerRunState, value string, finishErr error, reason string, exhausted bool) (string, error) {
	if strings.TrimSpace(reason) != "" {
		state.stopReason = reason
	}

	state.budgetExhausted = exhausted
	if finishErr == nil {
		decision := r.classifyTerminalAnswer(value, state.stopReason, state.collectedResults)
		presentedBeforeDecoration := decision.PresentedAnswer

		decision.PresentedAnswer = r.decorateTerminalAnswerDecision(decision, state.collectedResults)

		if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(presentedBeforeDecoration) {
			r.emitFinalAnswerDecorationEvent(decision.StopReason, presentedBeforeDecoration, decision.PresentedAnswer)

			if strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "" {
				r.LastAssistantMessage.Content = decision.PresentedAnswer
			}
		}

		value = decision.PresentedAnswer
		state.stopReason = decision.StopReason

		if decision.RetryExhausted {
			state.budgetExhausted = false
		}

		if decision.OverrideSource != "" {
			r.emitFinalAnswerBlockerOverride(decision.Acceptance, decision.StopReason, decision.OverrideSource)
		}

		r.emitTerminalAnswerSignalsOnce(state, decision)
	}

	return value, finishErr
}

// retryRejectedFinalAnswer handles rejected final answer with retry logic.
func (r *Runner) retryRejectedFinalAnswer(state *runnerRunState, stepNumber int, activeToolCall *llama.ToolCall, assistantMessage llama.ChatMessage, acceptance finalAnswerAcceptance, result workspace.ToolResult) (bool, string) {
	if !r.retryableFinalAnswerAcceptanceRejection(acceptance, state.collectedResults, state.completionGateRejections, state.currentAllowedTools) {
		return false, ""
	}

	priorGateRejections := state.completionGateRejections

	result.Error = escalateCompletionGateMessage(result.Error, priorGateRejections)

	if state.autonomyRecorder != nil {
		state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), state.collectedResults, priorGateRejections)
	}

	state.controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)

	state.completionGateRejections = priorGateRejections + 1

	// Store the current rejection error so recovery prompt functions
	// can inspect it even though it is not yet in collectedResults.
	r.lastCompletionGateError = result.Error

	var recoveryPrompt string

	if prompt, _, ok := r.completionGateRecoveryPrompt(state.collectedResults); ok {
		if card := r.renderRecoveryCardForRejectedAction(workspace.ToolRequest{Tool: "completion_gate"}, result, state.collectedResults, state.currentAllowedTools); card != "" {
			prompt = appendRecoveryCardToMessage(prompt, card)
		}

		recoveryPrompt = prompt
		r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
	}

	return true, recoveryPrompt
}
