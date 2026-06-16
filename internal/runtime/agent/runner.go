package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type Runner struct {
	Client llama.LLMClient
	// ImplementerClient, when non-nil, is used for write-enabled child
	// tasks (isolated workbranch children). Read-only children and the
	// parent loop stay on Client. When nil, all child work uses Client.
	ImplementerClient     llama.LLMClient
	Workspace             *workspace.Manager
	MaxToolSteps          int
	Policy                RunPolicy
	AgentName             string
	InitialNeedsNarrowing bool
	InitialBroadInspects  int
	SeedResults           []workspace.ToolResult
	PreExecute            func(step int, req workspace.ToolRequest) *workspace.ToolResult
	CompletionGate        CompletionGate
	TaskLedger            TaskLedgerPolicy
	PermissionPolicy      ToolPermissionPolicy
	CheckpointPolicy      CheckpointPolicy
	RuntimeBrief          workerruntime.WorkerRuntimeBrief
	OnEvent               func(RunEvent)
	// ContextSize is the configured llama.cpp context window in tokens.
	// Surfaced in model_call telemetry so log analysis can compute
	// ctx_pct (prompt_tokens / ctx_size) without having to reconcile
	// the running config separately. Zero means unknown.
	ContextSize     int
	DisableTaskTool bool
	// LanePolicy is the lane-specific behavioral policy derived from
	// RuntimeBrief at runner construction time. The policy replaces
	// inline TaskFamily/Contract string-comparison guards with a
	// single dispatch point. When nil, falls back to DefaultLanePolicy.
	LanePolicy LanePolicy

	// PathEvidenceRegistry is the consolidated path-existence, body-read,
	// candidate, and basename-resolution service. Populated once per turn
	// by RegisterToolResults; queried by the completion gate, readiness
	// gate, and discovery gate instead of calling os.Stat or scanning
	// raw tool results. When nil, the old fallback paths are used.
	PathEvidenceRegistry *PathEvidenceRegistry

	// InstructionAttacher injects configured per-directory guidance
	// whenever the model touches a file under that directory.
	InstructionAttacher *InstructionAttacher
	// DoomLoopGate, if non-nil, is called when the model emits three identical
	// consecutive tool calls. Returning true approves the call (resets the
	// streak and lets execution continue); returning false rejects it and
	// terminates the turn. When nil the runner falls back to a hard rejection.
	DoomLoopGate         func(req workspace.ToolRequest, count int) bool
	LastAssistantMessage llama.ChatMessage
	currentAllowedTools  []string
	activeAutonomy       *runnerAutonomyRecorder

	// lastCompletionGateError stores the error text from the most recent
	// completion-gate rejection so that recovery prompt functions can
	// inspect it without the result being in collectedResults yet.
	lastCompletionGateError string
}

func (r *Runner) supportsNativeTools() bool {
	return r == nil || r.Client == nil || r.Client.SupportsNativeTools()
}

// lanePolicy returns the lane-specific behavioral policy, falling back
// to LanePolicyForBrief when the Runner.LanePolicy field is nil.
func (r *Runner) lanePolicy() LanePolicy {
	if r != nil && r.LanePolicy != nil {
		return r.LanePolicy
	}

	if r != nil {
		return LanePolicyForBrief(r.RuntimeBrief)
	}

	return DefaultLanePolicy{}
}
func (r *Runner) supportsTerminalSubmitPlanNativeTools(results []workspace.ToolResult) bool {
	if r == nil || r.Client == nil {
		return true
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if r.planThenExecutePlannerPhase() && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return false
	}

	return r.Client.SupportsNativeTools()
}

func (r *Runner) Run(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	onStatus func(string),
	onToolRequest func(ToolRequestEvent),
	onToolResult func(ToolResultEvent),
) (answer string, err error) {

	turnMessages := append([]llama.ChatMessage(nil), messages...)

	// Ensure LanePolicy is initialized — falls back to auto-detection from RuntimeBrief.
	if r.LanePolicy == nil {
		r.LanePolicy = LanePolicyForBrief(r.RuntimeBrief)
	}

	lp := r.LanePolicy

	executionState := newRunnerExecutionState(r.InitialNeedsNarrowing, r.InitialBroadInspects)
	delegatedReadOnlyAnswerReadyNudged := false
	planThenExecuteReadinessNudged := false
	planThenExecuteSubmitOnlyReadinessNudged := false
	planThenExecuteCandidateReadinessNonSubmitTools := 0
	conversationEvidenceNudged := false
	convFirstMilestone, convNudgeInterval := lp.CallCountMilestones(r.ContextSize)

	planThenExecuteCorrectionAfterRefreshNudged := false
	planThenExecuteUnreadSourceNudgedFor := ""
	delegatedReadOnlyAnswerReady := slices.Contains(r.RuntimeBrief.Constraints, "answer_readiness_reached")
	delegatedReadOnlyPostReadyFollowups := 0
	delegatedReadOnlyDiminishingNudgeStage := 0
	postChildPacketActive := false
	postChildFollowupReads := 0
	postChildDiminishingNudgeStage := 0
	postReadinessProbeBudget := postReadinessProbeBudgetForBrief(r.RuntimeBrief)
	implementationWriteOnlyRecoveryActive := false
	requiredPathCompletionRecoveryActive := false
	writeDescriptionRepairActive := false
	writeDescriptionDetourRejections := 0
	writeDescriptionRepairPath := ""
	implementationWriteOnlyHiddenTools := make(map[string]bool)
	scopedHiddenNonWriteRecoveryUsed := false
	scopedLastChanceWriteOnlyActive := false
	scratchpadCloseoutNonWriteRejections := 0
	// recoveryReadAllowanceUsed tracks paths that have already consumed
	// their one bounded read during the current write-only recovery. The
	// allowance lets an agent that exhausted its pre-write budget still
	// confirm one anchor on each remaining required edit path before
	// giving up. A path can be granted at most one read; subsequent reads
	// fall back to the standard "answer with <<QUESTION>>" blocker.
	recoveryReadAllowanceUsed := map[string]bool{}
	planThenExecuteReadySourceRefreshes := 0
	planThenExecuteNativeSubmitPlanNoCallFallbacks := 0
	planThenExecuteNativeSubmitPlanDisabled := false
	// postCompletionVerificationCalls counts non-write tool requests issued
	// AFTER all required edit paths have been written. Models commonly use
	// this phase to read the files they just wrote and confirm the edits
	// landed — useful in moderation, but past a small budget it just burns
	// tool steps and surfaces as a budget-exhausted "failure" even though
	// the implementation contract is already fulfilled. Once the counter
	// crosses postCompletionVerificationCutoff, the runner force-answers
	// with a CLEAN success completion.
	postCompletionVerificationCalls := 0
	// turnHiddenTools tracks tools that have been gated enough times this
	// turn that we should stop advertising them in the manifest. Removes
	// a class of waste outright: if list_files is chained past the gate,
	// hiding it for the rest of the turn prevents another round of token
	// spend on calls we're going to reject anyway.
	turnHiddenTools := make(map[string]bool)
	exactAnchorHiddenTools := make(map[string]bool)
	implementationScopedHiddenTools := make(map[string]bool)

	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	exactAnchorWriteOnlyActive := exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, collectedResults)
	exactAnchorWriteFailures := 0
	currentAllowedTools := []string(nil)
	runState := &runnerRunState{
		ctx:                                     ctx,
		opts:                                    opts,
		collectedResults:                        collectedResults,
		turnMessages:                            turnMessages,
		turnHiddenTools:                         turnHiddenTools,
		executionState:                          &executionState,
		implementationScopedHiddenTools:         implementationScopedHiddenTools,
		delegatedReadOnlyAnswerReady:            delegatedReadOnlyAnswerReady,
		postChildPacketActive:                   postChildPacketActive,
		implementationWriteOnlyRecoveryActive:   implementationWriteOnlyRecoveryActive,
		requiredPathCompletionRecoveryActive:    requiredPathCompletionRecoveryActive,
		exactAnchorWriteOnlyActive:              exactAnchorWriteOnlyActive,
		planThenExecuteNativeSubmitPlanDisabled: planThenExecuteNativeSubmitPlanDisabled,
	}
	syncRunState := func() {
		runState.opts = opts
		runState.collectedResults = collectedResults
		runState.turnMessages = turnMessages
		runState.turnHiddenTools = turnHiddenTools
		runState.currentAllowedTools = append(runState.currentAllowedTools[:0], currentAllowedTools...)
		runState.executionState = &executionState
		runState.implementationScopedHiddenTools = implementationScopedHiddenTools
		runState.delegatedReadOnlyAnswerReady = delegatedReadOnlyAnswerReady
		runState.postChildPacketActive = postChildPacketActive
		runState.implementationWriteOnlyRecoveryActive = implementationWriteOnlyRecoveryActive
		runState.requiredPathCompletionRecoveryActive = requiredPathCompletionRecoveryActive
		runState.exactAnchorWriteOnlyActive = exactAnchorWriteOnlyActive
		runState.planThenExecuteNativeSubmitPlanDisabled = planThenExecuteNativeSubmitPlanDisabled
	}
	applyRunState := func() {
		opts = runState.opts
		collectedResults = runState.collectedResults
		turnMessages = runState.turnMessages
		currentAllowedTools = runState.currentAllowedTools
		planThenExecuteNativeSubmitPlanDisabled = runState.planThenExecuteNativeSubmitPlanDisabled
	}

	defer func() {
		r.currentAllowedTools = nil
		r.activeAutonomy = nil
	}()

	setCurrentAllowedTools := func(tools []string) {
		r.setCurrentAllowedTools(runState, tools)
	}

	if r.Workspace != nil {
		priorPlanThenExecuteMode := r.Workspace.PlanThenExecutePlannerMode()
		switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
		case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
			r.Workspace.SetPlanThenExecutePlannerMode(true)
		default:
			r.Workspace.SetPlanThenExecutePlannerMode(false)
		}

		restoreWorkspacePlanThenExecuteMode := func() {
			r.Workspace.SetPlanThenExecutePlannerMode(priorPlanThenExecuteMode)
		}

		defer restoreWorkspacePlanThenExecuteMode()
	}

	syncCurrentAllowedToolsFromOptions := func() {
		syncRunState()
		r.syncCurrentAllowedToolsFromOptions(runState)
		applyRunState()
	}
	rebuildTools := func() {
		syncRunState()
		r.rebuildRunTools(runState)
		applyRunState()
	}
	terminalSubmitPlanNativeTools := func() bool {
		syncRunState()

		return r.terminalSubmitPlanNativeTools(runState)
	}
	syncPlanThenExecuteRecoveryAllowedTools := func() {
		syncRunState()
		r.syncPlanThenExecuteRecoveryAllowedTools(runState)
		applyRunState()
	}
	refreshPlanThenExecuteTerminalSurface := func(reason string) {
		syncRunState()
		r.refreshPlanThenExecuteTerminalSurface(runState, reason)
		applyRunState()
	}
	transitionPlanThenExecuteSubmitReadyTerminal := func(prompt string, eventRole string, surfaceKey string) {
		syncRunState()
		r.transitionPlanThenExecuteSubmitReadyTerminal(runState, prompt, eventRole, surfaceKey)
		applyRunState()
	}
	forcePlanThenExecuteTerminalSurface := func(reason string) {
		syncRunState()
		r.forcePlanThenExecuteTerminalSurface(runState, reason)
		applyRunState()
	}

	if r.Workspace != nil {
		if exactAnchorWriteOnlyActive {
			hideExactAnchorReadTools(turnHiddenTools, exactAnchorHiddenTools, r.Workspace)
		}

		rebuildTools()
	}

	if strings.TrimSpace(opts.ToolChoice) == "" && len(opts.Tools) > 0 {
		opts.ToolChoice = "auto"
	}

	policy := r.effectivePolicy()
	metrics := runnerMetrics{startedAt: time.Now()}
	stopReason := "completed"
	budgetExhausted := false
	planThenExecuteLoopBudgetRecoveryUsed := false

	var autonomyRecorder *runnerAutonomyRecorder

	acceptedAnswerSignalsEmitted := false
	completionStatusEmitted := false
	completionStatusLastStatus := ""
	completionStatusLastBlockerKind := ""
	completionGateRejections := 0
	emitCompletionStatus := func(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
		completionStatusEmitted = true
		completionStatusLastStatus = strings.TrimSpace(status)
		completionStatusLastBlockerKind = strings.TrimSpace(blockerKind)
		r.emitCompletionStatus(status, reason, blockerKind, results, userVisibleBlocker)
	}

	defer func() {
		if autonomyRecorder != nil {
			autonomyRecorder.emitLoopCloseout(stopReason)
			autonomyRecorder.emitBudgetGate(policy, metrics, stopReason, budgetExhausted)
		}

		r.emitBudgetLedger(policy, metrics, stopReason, budgetExhausted)
	}()

	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		syncRunState()

		runState.policy = policy
		runState.metrics = metrics
		runState.stopReason = stopReason
		runState.budgetExhausted = budgetExhausted
		runState.autonomyRecorder = autonomyRecorder
		runState.completionGateRejections = completionGateRejections
		runState.completionStatusEmitted = completionStatusEmitted
		runState.completionStatusLastStatus = completionStatusLastStatus
		runState.completionStatusLastBlockerKind = completionStatusLastBlockerKind
		runState.acceptedAnswerSignalsEmitted = acceptedAnswerSignalsEmitted
		value, finishErr = r.finish(runState, value, finishErr, reason, exhausted)

		applyRunState()

		stopReason = runState.stopReason
		budgetExhausted = runState.budgetExhausted
		acceptedAnswerSignalsEmitted = runState.acceptedAnswerSignalsEmitted
		completionStatusEmitted = runState.completionStatusEmitted
		completionStatusLastStatus = runState.completionStatusLastStatus
		completionStatusLastBlockerKind = runState.completionStatusLastBlockerKind

		return value, finishErr
	}

	var (
		lastToolKey    string
		lastToolResult *workspace.ToolResult
	)
	// activeToolCall points at the native tool_call we're currently
	// responding to (when the model sent one). Turn helpers consult it to
	// emit role:"tool" + tool_call_id responses instead of
	// role:"system" text. Reset to nil at the top of every iteration.
	var activeToolCall *llama.ToolCall

	replayedToolRequests := make(map[string]int)
	declarationMovesForbidden := declarationMovesForbiddenByMessages(turnMessages)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	transportRetries := 0
	reviewRepairTerminalRequiredRejects := 0
	rejectionRecoveryNudgeUsed := false
	consecutiveNoProgress := 0
	discoveryRedirect := NewDiscoveryRedirect()
	consecutiveNarrowToolCalls := 0
	lastNarrowToolFamily := ""
	narrowToolFamilyNudgeFired := false
	narrowToolFamilyRedirectIssued := false
	narrowToolFamilyRedirectedOriginal := ""

	var preimageStore *PreimageCheckpointStore

	if r.Workspace != nil {
		preimageStore = NewPreimageCheckpointStore(r.Workspace.Root())
	}

	taskLedgerPolicy := r.TaskLedger
	if runnerBriefRequiresMutation(r.RuntimeBrief) && !strings.HasPrefix(r.agentName(), "child_write:") {
		taskLedgerPolicy.RequireEvidence = true
	}

	taskLedger := newTaskLedgerState(taskLedgerPolicy)

	r.RuntimeBrief = r.runtimeBriefWithLiveState(policy, turnMessages, collectedResults)

	// Seed the PathEvidenceRegistry with candidate paths from a prior session
	// whose prompt is similar to the current prompt. This lets the model
	// pick up where it left off across sessions without re-reading files.
	if r.PathEvidenceRegistry != nil && r.Workspace != nil {
		if priorPaths := PriorSessionCandidatePaths(r.Workspace.Root(), r.RuntimeBrief.Objective); len(priorPaths) > 0 {
			r.PathEvidenceRegistry.MergeFromPriorSession(priorPaths)
		}
	}

	if r.Workspace != nil {
		rebuildTools()
	}

	visibleRuntimeBrief := r.visibleBriefForToolSurface(r.RuntimeBrief, collectedResults)

	autonomyRecorder = newRunnerAutonomyRecorder(r, policy, visibleRuntimeBrief, collectedResults)
	r.activeAutonomy = autonomyRecorder

	if autonomyRecorder != nil {
		autonomyRecorder.emitContractStarted(visibleRuntimeBrief)
	}

	if initialBrief := workerruntime.RenderInitialRuntimeBrief(visibleRuntimeBrief); strings.TrimSpace(initialBrief) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: initialBrief})
	}

	if protocol := r.textualToolProtocolPrompt(visibleRuntimeBrief); strings.TrimSpace(protocol) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: protocol})
	}

	if bootstrap := r.initialBootstrapInstruction(); strings.TrimSpace(bootstrap) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: bootstrap})
	}

	// Investigation lane: recommended strategy to avoid exhaustive
	// symbol-by-symbol enumeration. One-shot at session start.
	if _, ok := r.lanePolicy().(ReadOnlyInvestigationLanePolicy); ok {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: readOnlyInvestigationStrategyPrompt()})
	}

	r.emitControlRailsForBrief(turnMessages, visibleRuntimeBrief)
	r.emitBriefAudit(visibleRuntimeBrief, collectedResults)

	evidenceFirstTargetPhaseCleared := false
	postChildReadBudget := postChildReadBudgetForBrief(r.RuntimeBrief)
	requestBudget := newRunnerRequestBudgetState(postChildReadBudget)
	recorder := runnerTurnRecorder{
		runner:                    r,
		onToolResult:              onToolResult,
		collectedResults:          &collectedResults,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejectedActions,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   &metrics,
		declarationMovesForbidden: declarationMovesForbidden,
	}

	collectedResults = r.maybeApplyReadOnlyNamedPathPrefetch(recorder, collectedResults)

	// currentFingerprint is the fingerprint of the in-flight tool call,
	// set at the top of each loop iteration. The turn controller reads it
	// via pointer so every rejection path can record the
	// fingerprint in rejectedFingerprints — letting a later pre-execute
	// guard short-circuit a byte-identical retry of a rejected call.
	var currentFingerprint string

	implementationTargetWriteReadySourceRefreshes := 0
	implementationTargetWriteReadyNonWriteRejections := 0
	implementationWriteOnlyNonWriteRejects := 0

	var (
		deferredSourceEvidenceWrite     *implementationDeferredSourceEvidenceWrite
		deferredGitHistorySubmitPlan    *workspace.ToolRequest
		deferredGitHistorySubmitPlanRaw string
	)

	controller := runnerTurnController{
		runner:                     r,
		ctx:                        ctx,
		opts:                       &opts,
		metrics:                    &metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		autonomyRecorder:           autonomyRecorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collectedResults,
		rejectedActions:            rejectedActions,
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejectedActions,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}
	if answer, ok := r.runVerificationBootstrap(runnerVerificationBootstrapInput{
		ctx:              ctx,
		collectedResults: collectedResults,
		metrics:          &metrics,
		recorder:         &recorder,
		executionState:   &executionState,
		requestBudget:    &requestBudget,
		autonomyRecorder: autonomyRecorder,
		onToolRequest:    onToolRequest,
	}); ok {
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		// Conversation lanes: the model owns the stop decision. Duration
		// and model-call budgets cut off legitimate investigation and
		// produce garbage forced answers. Per north star, trust the
		// model's intelligence — if it needs more time, let it continue.
		if !r.lanePolicy().SkipDurationBudget() {
			if policy.shouldStopForDuration(metrics.startedAt) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Run duration budget is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "duration_budget_exhausted", true)
			}
		}

		if !r.lanePolicy().SkipModelCallBudget() {
			if policy.shouldStopForModelCalls(metrics.ModelCalls) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Model-call budget is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "model_call_budget_exhausted", true)
			}
		}

		if r.scopedImplementationLastChanceWriteOnlyApplies(policy, metrics.ModelCalls, collectedResults) && !scopedLastChanceWriteOnlyActive {
			scopedLastChanceWriteOnlyActive = true
			implementationWriteOnlyRecoveryActive = true
			requiredPathCompletionRecoveryActive = true

			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()

			prompt := r.scopedImplementationLastChanceWriteOnlyPrompt(collectedResults)

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			opts = forceVisibleActionChatOptions(opts)
			opts = planExecuteSetExactMaxTokens(opts, 768)

			r.emitEvent(RunEvent{Role: "implementation_scoped_last_chance_write_only", Content: prompt})
		}

		modelCallOpts := readOnlyEvidenceAvailableTerminalChatOptions(opts, r.RuntimeBrief, collectedResults)
		if r.compactTurnMessagesIfNeeded(&turnMessages, policy, taskLedger, collectedResults) {
			metrics.Compactions++
		}

		if onStatus != nil && step > 0 {
			if policy.unbounded() {
				onStatus(fmt.Sprintf("Qwen is still working (%d steps).", stepNumber))
			} else {
				onStatus(fmt.Sprintf("Qwen is still working (%d/%d).", stepNumber, policy.MaxToolSteps))
			}
		}

		modelCallNumber := metrics.ModelCalls + 1
		phaseHint := ""

		if implementationWriteOnlyRecoveryActive && strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
			phaseHint = turnPhaseReviewRepairTerminal
		}

		contractID := ""
		if autonomyRecorder != nil {
			contractID = autonomyRecorder.contractIDForTurnPhase()
		}

		phaseContract := r.deriveCurrentTurnPhaseContract(runnerTurnPhaseContractInput{
			ContractID:          contractID,
			ModelCallNumber:     modelCallNumber,
			Step:                stepNumber,
			PhaseHint:           phaseHint,
			Brief:               r.RuntimeBrief,
			Results:             collectedResults,
			AllowedTools:        currentAllowedTools,
			HiddenTools:         turnHiddenTools,
			Options:             modelCallOpts,
			SupportsNativeTools: terminalSubmitPlanNativeTools(),
		})
		modelProtocol, hasModelProtocol := r.compileModelProtocolProjection(r.RuntimeBrief, collectedResults, phaseContract, policy, terminalSubmitPlanNativeTools())

		if autonomyRecorder != nil {
			modelProtocol = autonomyRecorder.bindModelProtocolProjection(modelProtocol)
			autonomyRecorder.emitTurnPhaseContract(phaseContract, modelProtocol)
		}

		modelStart := time.Now()

		turnMessages = stripOrphanedToolCalls(turnMessages)

		requestMessages := turnMessages

		if hasModelProtocol {
			requestMessages = modelMessagesWithProtocolProjection(requestMessages, modelProtocol)
			if autonomyRecorder != nil {
				autonomyRecorder.markModelProtocolPresented(modelProtocol)
			}
		}

		requestMessages = chatMessagesForOptions(requestMessages, modelCallOpts)

		assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, modelCallOpts)
		if err != nil {
			if answer, ok := r.modelErrorAfterPartialEditBlockerAnswer(err, collectedResults); ok {
				emitCompletionStatus("blocked", "model_error_after_partial_edit", "model_error_after_partial_edit", collectedResults, true)

				return finish(answer, nil, "model_error_after_partial_edit", false)
			}

			if transportRetries < 1 && retryableModelTransportError(err) {
				transportRetries++

				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The previous model request was interrupted by a temporary connection issue. Continue from where you left off, repeating any incomplete tool call or thought.",
				})
				r.emitEvent(RunEvent{Role: "transport_retry", Content: fmt.Sprintf("transport error, retry %d/1: %s", transportRetries, strings.ToLower(err.Error()))})

				continue
			}

			if prompt, ok := r.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(err, opts, collectedResults, planThenExecuteNativeSubmitPlanNoCallFallbacks); ok {
				planThenExecuteNativeSubmitPlanNoCallFallbacks++

				planThenExecuteNativeSubmitPlanDisabled = true
				opts = planThenExecuteTextualSubmitPlanFallbackChatOptions(opts, r.RuntimeBrief, collectedResults)

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)
				r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&turnMessages, collectedResults, "native_required_no_tool_call")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

				continue
			}

			return finish("", err, "model_error", false)
		}

		metrics.ModelCalls++
		metrics.addUsage(assistantMessage.Usage)
		r.emitModelCallTraceWithOptions(stepNumber, "main_loop", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), modelCallOpts, terminalSubmitPlanNativeTools(), collectedResults)

		reply := assistantMessage.Content
		// Capture the full message (including ReasoningContent) so the
		// auto-salvage path can recover review prose that landed inside
		// a runaway <think> block instead of visible content (#95).
		r.LastAssistantMessage = assistantMessage
		r.emitThinkingTrace(stepNumber, "model_reply", assistantMessage, modelCallOpts, false)

		activeToolCall = nil

		var (
			req                                  workspace.ToolRequest
			raw                                  string
			ok                                   bool
			parseErr                             error
			deterministicPlanExecuteEvidenceRead bool
			replayedOverVerificationRequest      *workspace.ToolRequest
		)

		normalization := normalizeToolRequestSyntax(assistantMessage)

		req, raw, ok, parseErr = normalization.Request, normalization.Raw, normalization.OK, normalization.ParseErr
		activeToolCall = normalization.ActiveToolCall

		if normalization.EventOutcome != "" && normalization.EventTransport != "" {
			r.emitToolRequestNormalizerEvent(normalization.EventOutcome, normalization.EventTransport, req, parseErr, normalization.ParserFailureClass)
		}

		if normalization.Repaired && normalization.EventTransport == toolRequestNormalizerTransportStringifiedSubmitRepair {
			r.emitEvent(RunEvent{Role: "submit_plan_stringified_edits_repaired", Content: "repaired submit_plan.edits from a quoted JSON string into a typed edits array before validation"})
		}

		if !ok && parseErr == nil {
			if intentReq, intentRaw, compiled := r.compilePlanIntentToolRequest(reply); compiled {
				req, raw, ok = intentReq, intentRaw, true
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "visible_prose_salvage", req, nil, "")
				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in prose; executing the request", req.Tool)})
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageToolRequestFromReasoning(assistantMessage, collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "reasoning_salvage", req, nil, "")
				r.emitEvent(RunEvent{Role: "reasoning_tool_salvage", Content: fmt.Sprintf("model emitted parser-readable %s request in hidden reasoning while visible content was empty; executing the request", req.Tool)})
			} else if answerText, salvaged := r.salvageAnswerFromReasoning(assistantMessage, collectedResults); salvaged {
				r.emitEvent(RunEvent{Role: "reasoning_answer_salvage", Content: fmt.Sprintf("model emitted answer prose in hidden reasoning while visible content was empty; extracted %d chars", len(answerText))})

				return finish(answerText, nil, "reasoning_answer_salvage", false)
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteRejectedRequiredSourceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool planner text and issued source read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicImplementationRejectedRequiredPathRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool implementation text and issued bounded read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				deterministicPlanExecuteEvidenceRead = true

				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_source_satisfied_evidence_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_source_satisfied_read", Content: fmt.Sprintf("completion recovery read source-satisfied evidence path %s before accepting the closeout", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_grounding_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery ignored non-tool planner text after ungrounded submit_plan and issued %s", req.Tool)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteNonToolCandidateRecovery(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_candidate_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_candidate", Content: fmt.Sprintf("completion recovery ignored non-terminal planner prose and issued %s for %s", req.Tool, planThenExecuteRequestTargetPath(req))})
			} else if strings.TrimSpace(reply) == "" {
				if recoveredReq, recoveredRaw, event, recovered := r.deterministicEmptyReplyRecovery(collectedResults); recovered {
					req, raw, ok = recoveredReq, recoveredRaw, true

					r.emitEvent(event)
				}
			}
		}

		if parseErr != nil {
			if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, collectedResults); salvaged {
				req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
				activeToolCall = nil

				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool text; executing the safe request before recording tool_parse", req.Tool)})
			} else if raw != reply {
				if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(raw, collectedResults); salvaged {
					req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
					activeToolCall = nil

					r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool arguments; executing the safe request before recording tool_parse", req.Tool)})
				}
			}
		}

		if parseErr != nil {
			if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest(reply, collectedResults); recovered {
				req, raw, ok, parseErr = recoveredReq, recoveredRaw, true, nil
				activeToolCall = nil

				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery ignored malformed planner tool JSON after ungrounded submit_plan and issued %s", req.Tool)})
			}
		}

		err = parseErr
		if err != nil {
			var assistantToolTurn llama.ChatMessage

			if activeToolCall != nil {
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
			} else if !r.supportsNativeTools() {
				assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, workspace.ToolRequest{Tool: "tool_parse"}, raw, opts)
				activeToolCall = nil
			} else {
				call := syntheticLegacyParseToolCall(stepNumber, raw)

				activeToolCall = &call
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
			}

			assistantToolTurn = assistantHistoryMessageForOptions(assistantToolTurn, opts)
			turnMessages = append(turnMessages, assistantToolTurn)
			r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, opts, true)
			controller.recordParseFailure(stepNumber, activeToolCall, raw, err)

			if recoveryPlan, ok := r.planThenExecuteSubmitPlanParseRecoveryPlan(raw, err, collectedResults); ok {
				prompt := RenderPlanExecuteRecoveryPlan(recoveryPlan)

				implementationWriteOnlyRecoveryActive = true

				if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
					requiredPathCompletionRecoveryActive = true
				}

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()

				opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				refreshPlanThenExecuteTerminalSurface("parse_recovery")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitPlanExecuteSubmitPlanParseRecoveryEvent(recoveryPlan, prompt)
			} else {
				r.emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw, err)
			}

			continue
		}

		if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterEvidence(req, collectedResults, currentAllowedTools); replay {
			originalReq := req
			if strings.TrimSpace(originalReq.Tool) == workspace.ToolNameSourceSanity || strings.TrimSpace(originalReq.Tool) == workspace.ToolNameShell {
				reqCopy := originalReq

				replayedOverVerificationRequest = &reqCopy
			} else if deferredSourceEvidenceWrite != nil && deferredSourceEvidenceWrite.RequiresHistory {
				if verifyReq, ok := deferredSourceEvidenceWrite.fallbackVerificationRequest(); ok {
					replayedOverVerificationRequest = &verifyReq
				}
			}

			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(replayReq.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(replayRaw),
				"reason":         "implementation_write_after_source_evidence_replay",
			}))

			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, replayReq.Tool),
			})
			req = replayReq
			raw = replayRaw
			ok = true
			activeToolCall = nil
			deferredSourceEvidenceWrite = nil
		}

		if ok && strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan &&
			planThenExecuteGroundingRejectedSeen(collectedResults) &&
			!planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, collectedResults) &&
			len(r.ungroundedSubmitPlanEditPaths(req, collectedResults)) > 0 {

			if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest("", collectedResults); recovered {
				req, raw = recoveredReq, recoveredRaw
				activeToolCall = nil

				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery replaced repeated ungrounded submit_plan with %s", req.Tool)})
			}
		}

		// Answer tool: the model can explicitly signal it's ready to
		// answer by calling the "answer" tool. This gives tool-biased
		// models a tool-call-shaped way to produce prose, matching their
		// training bias toward tool use over spontaneous prose.
		if ok && strings.TrimSpace(req.Tool) == "answer" {
			// The answer content comes from the tool call's arguments.
			// For native tool calls, raw is the JSON arguments string
			// like {"content": "the answer text"}. For textual tool
			// calls, raw is the full tool request JSON.
			answerContent := ""

			var answerArgs struct {
				Content string `json:"content"`
			}

			if err := json.Unmarshal([]byte(raw), &answerArgs); err == nil && answerArgs.Content != "" {
				answerContent = answerArgs.Content
			}

			if answerContent != "" {
				if acceptance := r.evaluateFinalAnswerAcceptance(answerContent, collectedResults); acceptance.BlockerKind != "" {
					blockerKind := acceptance.BlockerKind
					reason := acceptance.Reason
					blockerResult := workspace.ToolResult{
						Tool:    "completion_gate",
						Summary: "Answer tool rejected by blocker check",
						Error:   r.finalAnswerBlockerErrorText(blockerKind, reason, answerContent, collectedResults),
					}

					if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
						stepNumber:               stepNumber,
						activeToolCall:           activeToolCall,
						assistantMessage:         assistantMessage,
						acceptance:               acceptance,
						result:                   blockerResult,
						collectedResults:         collectedResults,
						currentAllowedTools:      currentAllowedTools,
						completionGateRejections: &completionGateRejections,
						turnMessages:             &turnMessages,
						turnHiddenTools:          turnHiddenTools,
						controller:               &controller,
						autonomyRecorder:         autonomyRecorder,
						rebuildTools:             rebuildTools,
					}) {
						continue
					}

					if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok &&
						!readOnlyAnswerReportsInsufficientEvidence(answer) {

						return finish(answer, nil, "deterministic_final_answer_recovery", false)
					}

					return finish(answerContent, nil, blockerKind, true)
				}

				return finish(answerContent, nil, "model_answer_tool", false)
			}
		}

		if !ok {
			if len(changedPathsFromToolResults(collectedResults)) == 0 {
				if req, result, salvaged := r.autoSalvageMarkdownDeliverable(reply); salvaged {
					metrics.ToolCalls++

					if onToolRequest != nil {
						profile := workspace.DiscoveryProfileForRequest(req)
						onToolRequest(ToolRequestEvent{
							Step:           stepNumber,
							Agent:          r.agentName(),
							Raw:            marshalToolRequestRaw(req),
							Request:        req,
							Mode:           r.Workspace.Mode(),
							Broad:          workspace.IsBroadInspectionRequest(req),
							Narrowing:      workspace.IsNarrowingRequest(req),
							Action:         toolActionClass(req),
							DiscoveryClass: profile.Class,
							DiscoveryScope: profile.Scope,
							DiscoveryCost:  profile.Cost,
						})
					}

					recorder.activeToolCall = activeToolCall
					recorder.recordWithOptions(stepNumber, req, result, "auto_salvage_markdown_deliverable", runnerRecordOptions{
						TrackProgress:  true,
						ResetRejected:  true,
						SetLastToolKey: true,
					})
					r.emitEvent(RunEvent{
						Role:    "auto_salvage_markdown_deliverable",
						Content: fmt.Sprintf("model emitted a single-file markdown deliverable as final-answer text; recorded it through write_file for %s", strings.TrimSpace(req.Path)),
					})
					emitCompletionStatus("completed", "auto_salvage_markdown_deliverable", "", collectedResults, false)

					return finish(fmt.Sprintf("Done. Wrote %s via auto-salvage after the model emitted the deliverable as final-answer text.", strings.TrimSpace(req.Path)), nil, "auto_salvage_markdown_deliverable", false)
				}
			}

			result, rejected := workspace.ToolResult{}, false
			if requiredPathCompletionRecoveryActive {
				result, rejected = r.requiredPathCompletionRecoveryRejection(reply, collectedResults)
			}

			gateReply := reply
			if !rejected {
				gateReply = r.decoratedReadOnlyReplyForCompletionGate(reply, collectedResults)
				result, rejected = r.completionGateRejection(gateReply, collectedResults, stepNumber, taskLedger, declarationMovesForbidden)
			}

			if rejected {
				priorGateRejections := completionGateRejections

				result.Error = escalateCompletionGateMessage(result.Error, priorGateRejections)

				if autonomyRecorder != nil {
					autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), collectedResults, priorGateRejections)
				}

				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)

				if looksLikeTextualToolCall(reply) {
					r.LastAssistantMessage.Content = "[XML tool-call markup detected and sanitized — the model emitted its native XML format instead of a proper tool call or prose. The harness recovery path will retry with a corrected prompt.]"
				}

				completionGateRejections = priorGateRejections + 1

				// Store the current rejection error so recovery prompt
				// functions can inspect it even though it is not yet in
				// collectedResults.
				r.lastCompletionGateError = result.Error

				shouldAttachRecoveryPrompt := priorGateRejections == 0 ||
					result.Summary == "Completion gate rejected unresolved process narration" ||
					(r.planThenExecuteCompletionGateSummaryRetryable(result.Summary, collectedResults, priorGateRejections)) ||
					(r.textualRequiredPathCompletionRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.unscopedNoWriteCompletionRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.completionGateMutationRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults))

				if prompt, hideReads, ok := r.completionGateRecoveryPrompt(collectedResults); ok && shouldAttachRecoveryPrompt {
					opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					hiddenOnlyNoWriteTerminalRecovery := mutationHiddenOnlyNoWriteTerminalRecoveryStall(r.RuntimeBrief, collectedResults, assistantMessage, priorGateRejections, terminalSubmitPlanNativeTools())

					if hiddenOnlyNoWriteTerminalRecovery {
						opts = forceVisibleActionChatOptions(opts)
						prompt = hiddenOnlyNoWriteTerminalRecoveryPrompt()
						hideReads = true

						r.emitEvent(RunEvent{Role: "mutation_visible_write_mode", Content: "textual mutation recovery produced hidden-only output after concrete source evidence; retrying once with hidden thinking disabled for the terminal write/blocker action"})
					} else if implementationRequiredPathHiddenOnlyRecoveryStall(r.RuntimeBrief, collectedResults, assistantMessage, priorGateRejections, terminalSubmitPlanNativeTools()) {
						opts = forceVisibleActionChatOptions(opts)

						if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, collectedResults) {
							prompt = planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(prompt, r.RuntimeBrief, collectedResults)

							r.emitEvent(RunEvent{Role: "plan_then_execute_submit_correction_visible_mode", Content: "submit-correction recovery produced hidden-only output; retrying once with hidden thinking disabled for the terminal submit_plan action"})
						} else {
							prompt = "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce the required visible tool JSON or a visible `<<QUESTION>>:` blocker now.\n\n" + prompt

							r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "anchor-ready textual recovery produced hidden-only output; retrying once with hidden thinking disabled for the terminal write/blocker action"})
						}
					}

					if hideReads {
						implementationWriteOnlyRecoveryActive = true

						if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
							requiredPathCompletionRecoveryActive = true
						}

						markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)

						if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
							hideReviewRepairTerminalOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
						} else {
							hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
						}

						rebuildTools()

						opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
						opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
					}

					refreshPlanThenExecuteTerminalSurface("completion_gate_recovery")

					if card := r.renderRecoveryCardForRejectedAction(workspace.ToolRequest{Tool: "completion_gate"}, result, collectedResults, currentAllowedTools); card != "" {
						prompt = appendRecoveryCardToMessage(prompt, card)
					}

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
				}

				if priorGateRejections >= r.completionGateRetryLimit(collectedResults) {
					answer := r.completionGateRepeatedBlockerAnswer(result, collectedResults)
					blockerKind := r.completionGateRepeatedBlockerKind(collectedResults)
					emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

					return finish(answer, nil, blockerKind, true)
				}

				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}

				continue
			}

			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, modelCallOpts, collectedResults, &metrics)
			if strings.TrimSpace(gateReply) != "" &&
				strings.TrimSpace(gateReply) != strings.TrimSpace(reply) &&
				strings.TrimSpace(answer) == strings.TrimSpace(reply) &&
				!strings.HasPrefix(strings.TrimSpace(gateReply), "<<QUESTION>>") {

				answer = gateReply
			}

			if reason := retryAnswerHedgeRejectionReason(answer); reason != "" {
				// The retry answer is a hedge or meta-response about evidence
				// sufficiency rather than a substantive answer — reject and block.
				r.emitCompletionStatus("blocked", reason, "content_gap", collectedResults, true)

				blockerResult := workspace.ToolResult{Tool: "completion_gate", Summary: "Retry answer rejected as hedge", Error: reason}
				overridden := r.completionGateRepeatedBlockerAnswer(blockerResult, collectedResults)

				return finish(overridden, nil, "content_gap", true)
			}

			if acceptance := r.evaluateFinalAnswerAcceptance(answer, collectedResults); acceptance.BlockerKind != "" {
				blockerKind := acceptance.BlockerKind
				reason := acceptance.Reason
				// Override only fires when the model fabricates
				// completion. A <<QUESTION>>: per-path blocker IS the
				// proper way to report unfulfilled paths, so we keep
				// the model's text in that case — only swap when the
				// answer claims success against the harness's actual
				// changed_paths state.
				if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
					blockerResult := workspace.ToolResult{
						Tool:    "completion_gate",
						Summary: "Final answer rejected by blocker check",
						Error:   r.finalAnswerBlockerErrorText(blockerKind, reason, answer, collectedResults),
					}
					if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
						stepNumber:               stepNumber,
						activeToolCall:           activeToolCall,
						assistantMessage:         assistantMessage,
						acceptance:               acceptance,
						result:                   blockerResult,
						collectedResults:         collectedResults,
						currentAllowedTools:      currentAllowedTools,
						completionGateRejections: &completionGateRejections,
						turnMessages:             &turnMessages,
						turnHiddenTools:          turnHiddenTools,
						controller:               &controller,
						autonomyRecorder:         autonomyRecorder,
						rebuildTools:             rebuildTools,
					}) {
						continue
					}

					if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok &&
						!readOnlyAnswerReportsInsufficientEvidence(answer) {

						return finish(answer, nil, "deterministic_final_answer_recovery", false)
					}

					return finish(answer, err, blockerKind, true)
				}
			}

			return finish(answer, err, "model_final", false)
		}

		var assistantToolTurn llama.ChatMessage

		if activeToolCall != nil {
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
		} else if !r.supportsNativeTools() {
			assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, req, raw, modelCallOpts)
			activeToolCall = nil
		} else {
			call := syntheticLegacyToolCall(stepNumber, req, raw)

			activeToolCall = &call
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
		}

		assistantToolTurn = assistantHistoryMessageForOptions(assistantToolTurn, modelCallOpts)
		turnMessages = append(turnMessages, assistantToolTurn)
		r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, modelCallOpts, true)

		if r.shouldForceAnswerForPostCompletionVerification(req, collectedResults, &postCompletionVerificationCalls) {
			emitCompletionStatus("completed", "post_completion_verification_cutoff", "", collectedResults, false)
			r.emitEvent(RunEvent{
				Role:    "post_completion_verification_cutoff",
				Content: fmt.Sprintf("all required edit paths written; cutting off self-verification after %d non-write call(s) to avoid false budget-exhausted failure signal", postCompletionVerificationCalls),
			})

			answer, err := controller.completeWithoutMoreToolsWithReason("All required edit paths have been written and the implementation contract is fulfilled. Tools are now disabled. Write the standard final answer now: report the changed paths and verification status. Do not call further tools.")

			return finish(answer, err, "post_completion_verification_cutoff", false)
		}

		preflight := r.applyLanePreflight(runnerLanePreflightInput{
			Request:               req,
			Raw:                   raw,
			Results:               collectedResults,
			VerificationAttempted: executionState.verificationAttempted,
		})

		req = preflight.Request
		raw = preflight.Raw

		if preflight.OriginalRaw != "" && strings.TrimSpace(preflight.OriginalRaw) != strings.TrimSpace(preflight.Raw) {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(preflight.OriginalRequest.Tool),
				"rewritten_tool": strings.TrimSpace(preflight.Request.Tool),
				"original_raw":   strings.TrimSpace(preflight.OriginalRaw),
				"rewritten_raw":  strings.TrimSpace(preflight.Raw),
			}))

			// Make the rewrite visible to the model. A silent substitution
			// is firmer than a "you must" rejection — it removes the
			// model's signal entirely. A visible note keeps the contract
			// (the rewritten call still runs) while letting the model
			// learn the routing rule for next time.
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(preflight.OriginalRequest.Tool, preflight.Request.Tool),
			})
		}

		if redirected, redirectedRaw, ok := repeatedDiagnosticLogReadFileBackfill(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "repeated_diagnostic_log_read_previous_window")
		}

		if summary, errorText, reject := diagnosticLogUnboundedReadCoverageRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "diagnostic_log_read_coverage_rejected", true)
			}

			continue
		}

		if redirected, redirectedRaw, reason, ok := generatedEvidenceArtifactReadRedirectRequest(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("generated_evidence_artifact_read_redirect", map[string]any{
				"from_path": normalizeRunnerPath(req.Path),
				"to_path":   normalizeRunnerPath(redirected.Path),
				"reason":    reason,
			}))

			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := structuredSourceReadRewriteRequest(req, r.Workspace, currentAllowedTools, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := pathlessReadSymbolAfterOutlineRewriteRequest(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := repeatedFileOutlineToReadSymbolRewriteRequest(req, collectedResults, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := readOnlyDefinitionToReadSymbolRewriteRequest(req, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "repeated_diagnostic_log_window_rejected", true)
			}

			continue
		}

		if redirected, redirectedRaw, ok := r.repeatedRangeLessReadFileRedirect(req, collectedResults, currentAllowedTools); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "repeated_rangeless_read_file_bounded_source")
		}

		if repaired, repairedRaw, ok := r.writeDescriptionRepairSynthesizedRequest(req, writeDescriptionRepairActive); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, repaired, repairedRaw, "write_description_repair_synthesized")
			r.emitWriteDescriptionValidatorDecision(stepNumber, repaired, "repair", "description_synthesized", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_synthesized", Content: fmt.Sprintf("synthesized missing write description for %s on %q", strings.TrimSpace(repaired.Tool), normalizeRunnerPath(repaired.Path))})
		}

		currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		if (writeDescriptionRepairActive ||
			lastResultSummaryIs(collectedResults, "Missing write description") ||
			lastResultSummaryIs(collectedResults, "Write-description repair rejected non-write detour")) &&
			!r.writeOnlyRecoveryToolIsWrite(req.Tool) {

			writeDescriptionRepairActive = true
			writeDescriptionDetourRejections++

			prompt := r.writeDescriptionRepairPrompt(req)
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Write-description repair rejected non-write detour",
				Error:   fmt.Sprintf("%s is not available while write-description repair is pending. Reissue the same in-scope write with a non-empty description, or answer with <<QUESTION>> and the exact blocker.", strings.TrimSpace(req.Tool)),
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			r.emitWriteDescriptionValidatorDecision(stepNumber, req, "reject", "non_write_detour", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_recovery", Content: prompt})

			if writeDescriptionDetourRejections >= 2 {
				blockerKind := "write_description_repair_detour_repeated"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

				answer := "<<QUESTION>>: write-description repair rejected repeated non-write detours. The required edit path remains unchanged; reissue the same in-scope write with a non-empty description, or provide the exact blocker."

				return finish(answer, nil, blockerKind, true)
			}

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			continue
		}

		if workspace.IsWorkspaceWriteTool(req.Tool) && implementationTargetSourceWriteTargetsHaveEvidence(req, collectedResults) {
			clearRejectedFingerprintSummary(requestBudget.fingerprint.rejected, currentFingerprint, "Write rejected before source-body evidence")
			delete(rejectedActions, toolActionClass(req))
		}

		if !exactAnchorWriteOnlyActive {
			requestBudget.setRecoveryContext(r.RuntimeBrief, collectedResults)
			decision := requestBudget.earlyRejectedFingerprintDecision(req, currentFingerprint)
			if result, prompt, ok := r.mutationRejectedRetryWriteRecovery(decision, req, collectedResults); ok {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				implementationWriteOnlyRecoveryActive = true

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "mutation_rejected_retry_write_recovery", Content: prompt})

				continue
			}

			if res := decision.apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}

		if preflight.RejectSummary != "" {
			if preflight.ForceAnswer != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: preflight.RejectSummary,
					Error:   preflight.RejectError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				if preflight.ForceAnswer.Reason == "conversation_evidence_sufficient" {
					if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, "The retained source context identifies the relevant implementation surface."); ok {
						return finish(presentable, nil, preflight.ForceAnswer.Reason, true)
					}
				}

				answer, err := controller.completeWithoutMoreToolsWithReason(preflight.ForceAnswer.Prompt)

				return finish(answer, err, preflight.ForceAnswer.Reason, true)
			}

			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, preflight.RejectSummary, preflight.RejectError)
			if done {
				return finish(answer, err, preflight.RejectStopReason, true)
			}

			continue
		}

		recoveryReadAllowanceGrantedThisRequest := false

		if summary, errorText, prompt, target, reject := r.scratchpadDeliverableCloseoutNonWriteRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			scratchpadCloseoutNonWriteRejections++
			if scratchpadCloseoutNonWriteRejections >= 2 {
				blockerKind := "scratchpad_deliverable_closeout_non_write_repeated"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

				answer := fmt.Sprintf("<<QUESTION>>: completion gate rejected a prose-only response for %s, but the model kept selecting non-write tools. The deliverable-write contract must be satisfied, recording uncertainty in the file, or the lane must be rerun with corrected evidence.", target)

				return finish(answer, nil, blockerKind, true)
			}

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_closeout_recovery", Content: prompt})

			continue
		}

		if summary, errorText, prompt, reject := r.scratchpadDeliverableSourceDiscoveryRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_source_discovery_recovery", Content: prompt})

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMissingTargetReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_missing_target_read_ranked_candidate")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, prompt, reject := planThenExecuteReadinessNudgedNonSubmitRejection(req, r.RuntimeBrief, collectedResults, planThenExecuteSubmitOnlyReadinessNudged); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			refreshPlanThenExecuteTerminalSurface("readiness_non_submit_rejection")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

			continue
		}

		if planThenExecuteReadinessNudged &&
			!planThenExecuteSubmitOnlyReadinessNudged &&
			r.planThenExecutePlannerPhase() &&
			strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan &&
			successfulSubmitPlanResultCount(collectedResults) == 0 {

			planThenExecuteCandidateReadinessNonSubmitTools++
			if planThenExecuteCandidateReadinessNonSubmitTools >= 12 {
				blockerKind := "plan_execute_candidate_readiness_tool_budget_exhausted"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

				answer := "<<QUESTION>>: plan-execute gathered candidate source evidence and made `submit_plan` available, but the planner kept requesting additional tools instead of producing a terminal plan artifact, source-satisfied decision, or exact blocker. Rerun with a narrower objective or exact edit anchor if a plan artifact is still required."

				return finish(answer, nil, blockerKind, true)
			}
		}

		if r.implementationTargetWriteReadySourceReadAllowed(req, collectedResults, implementationTargetWriteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			implementationTargetWriteReadySourceRefreshes++

			implementationTargetWriteReadyNonWriteRejections++

			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed write-ready %s source expansion on target path %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(req.Path)),
			})
		} else if _, _, prompt, reject := r.implementationTargetWriteReadyNonWriteRecovery(req, collectedResults); reject {
			implementationTargetWriteReadyNonWriteRejections++

			requestBudget.writeReadyNudgeActive = true

			nudgeMsg := targetWriteReadyStagedNudge(prompt, req, implementationTargetWriteReadyNonWriteRejections)

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_recovery", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_nudge", Content: nudgeMsg})
		}

		scopedHiddenNonWrite := implementationScopedHiddenTools[strings.TrimSpace(req.Tool)] && !workspace.IsWorkspaceWriteTool(req.Tool)
		if summary, errorText, prompt, reject := r.implementationScopedDisallowedToolRecovery(req, collectedResults); reject && !(scopedHiddenNonWrite && scopedHiddenNonWriteRecoveryUsed) {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			if scopedHiddenNonWrite {
				scopedHiddenNonWriteRecoveryUsed = true
			}

			rebuildTools()

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "implementation_scoped_tool_recovery", Content: prompt})

			continue
		}

		if !turnHiddenTools[strings.TrimSpace(req.Tool)] {
			if summary, errorText, prompt, hideNonWrite, reject := r.writeReadyPacketReadWithoutGapRecovery(req, collectedResults); reject {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: summary,
					Error:   errorText,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				if requestBudget.scopedReadRejections != nil {
					key := currentFingerprint
					if strings.TrimSpace(key) == "" {
						key = scopedReadRecoveryRejectionKey(req)
					}

					requestBudget.scopedReadRejections[key]++
					if requestBudget.scopedReadRejections[key] >= 2 {
						blockerKind := "scoped_implementation_read_repeated"
						emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

						target := "the required edit path"
						if paths := r.remainingRequiredEditPaths(collectedResults); len(paths) > 0 {
							target = strings.Join(paths, ", ")
						} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
							target = strings.Join(paths, ", ")
						}

						answer := fmt.Sprintf("<<QUESTION>>: scoped implementation recovery rejected the same %s request twice for %s. The next valid outcome is a required-path write from the packet and scoped evidence, or a blocker naming the exact missing edit-safety fact.", strings.TrimSpace(req.Tool), target)

						return finish(answer, nil, blockerKind, true)
					}
				}

				if hideNonWrite {
					implementationWriteOnlyRecoveryActive = true

					if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
						requiredPathCompletionRecoveryActive = true
					}

					markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
					hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				}

				rebuildTools()

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				role := "implementation_scoped_read_recovery"

				if hideNonWrite {
					role = "implementation_write_only_recovery"
				}

				r.emitEvent(RunEvent{Role: role, Content: prompt})

				continue
			}
		}

		if summary, errorText, prompt, reject := r.unscopedContextPackCandidateReadRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "context_pack_read_recovery", Content: prompt})

			continue
		}

		if deterministicPlanExecuteEvidenceRead {
			recoveryReadAllowanceGrantedThisRequest = true

			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_source_satisfied_evidence_read_allowed",
				Content: fmt.Sprintf("allowed bounded %s for source-satisfied evidence path %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if refreshReason, ok := r.planThenExecuteDirectSourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes); ok {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for %s %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, refreshReason, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if redirected, redirectedRaw, ok := r.planThenExecuteRejectedPlanRefreshReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_rejected_plan_source_refresh_region")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("redirected bounded %s source refresh %d/%d for rejected plan path %q", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if r.planThenExecuteCandidateSourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for candidate path %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		}

		if implementationWriteOnlyRecoveryActive && !r.writeOnlyRecoveryToolIsWrite(req.Tool) {
			if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily && !recoveryReadAllowanceGrantedThisRequest {
				reviewRepairTerminalRequiredRejects++

				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Review-repair terminal outcome required",
					Error:   reviewRepairTerminalOutcomeRequiredPrompt(r.RuntimeBrief, req.Tool),
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				refreshPlanThenExecuteTerminalSurface("review_repair_terminal_recovery")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
				opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_terminal_required", Content: result.Error})

				if reviewRepairTerminalRequiredRejects >= 2 {
					return finish("<<QUESTION>>: review-repair could not close because it repeatedly requested hidden discovery after the closeout gate instead of a terminal `Decision: pass`, corrective `submit_plan`, or blocker.", nil, "review_repair_terminal_recovery_budget_exhausted", true)
				}

				continue
			}

			if r.planThenExecutePlannerPhase() {
				if !recoveryReadAllowanceGrantedThisRequest {
					if result, ok := r.planThenExecuteAnchorRefreshWriteOnlyRejection(req, collectedResults); ok {
						controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
						refreshPlanThenExecuteTerminalSurface("anchor_refresh_recovery")

						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
						opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
						r.emitEvent(RunEvent{Role: "plan_then_execute_anchor_refresh_required", Content: result.Error})

						continue
					}

					refreshNote := ""
					if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(collectedResults); ok {
						refreshNote = " " + planThenExecutePostSubmitRejectionRefreshNote(r.RuntimeBrief, collectedResults, rejected, rejectedAt)
					}

					result := workspace.ToolResult{
						Tool:    req.Tool,
						Summary: "Plan-execute submit_plan required",
						Error:   fmt.Sprintf("%s is not available while plan-execute planner recovery is waiting for a visible `submit_plan`. The next visible action must be a parser-readable `submit_plan`. %s%s", strings.TrimSpace(req.Tool), planThenExecuteSubmitPlanCoverageSentence(), refreshNote),
					}
					controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
					refreshPlanThenExecuteTerminalSurface("submit_plan_recovery")

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
					opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					syncPlanThenExecuteRecoveryAllowedTools()
					r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: result.Error})

					continue
				}
			}

			if recoveryReadAllowanceGrantedThisRequest {
				// The plan-execute branch above grants only the explicit
				// source-refresh exception after a rejected submit_plan.
			} else if r.consumeRecoveryReadAllowance(req, recoveryReadAllowanceUsed, collectedResults) {
				recoveryReadAllowanceGrantedThisRequest = true

				r.emitEvent(RunEvent{
					Role:    "implementation_write_only_recovery_read_allowance",
					Content: fmt.Sprintf("granted one bounded %s on remaining required edit path %q", strings.TrimSpace(req.Tool), strings.TrimSpace(req.Path)),
				})
			} else {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Write-only recovery blocked non-write tool",
					Error:   fmt.Sprintf("%s is not available while write-ready recovery is active and required edit paths remain unchanged. Use an available write tool, or answer with <<QUESTION>> and the exact path-specific blocker.", strings.TrimSpace(req.Tool)),
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				implementationWriteOnlyNonWriteRejects++
				if implementationWriteOnlyNonWriteRejects < 2 {
					prompt := r.writeOnlyRecoveryNonWriteRepairPrompt(req, collectedResults)

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					opts = forceVisibleActionChatOptions(opts)
					opts = planExecuteSetExactMaxTokens(opts, 768)

					hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
					rebuildTools()
					r.emitEvent(RunEvent{Role: "implementation_write_only_recovery_non_write_repair", Content: prompt})

					continue
				}

				if answer, ok := r.writeOnlyRecoveryBlockerAnswer(collectedResults); ok {
					blockerKind := r.writeOnlyRecoveryBlockerKind(collectedResults)
					emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

					return finish(answer, nil, blockerKind, true)
				}

				answer, err := controller.completeWithoutMoreToolsWithReason("Write-only recovery is active. Tools are now disabled. Answer with the changed paths already produced, or with <<QUESTION>> and the exact blocker that prevents the required edit.")

				return finish(answer, err, "write_only_recovery_non_write_tool", true)
			}
		}

		if turnHiddenTools[strings.TrimSpace(req.Tool)] && !recoveryReadAllowanceGrantedThisRequest {
			hiddenSummary := "Tool hidden by active contract"
			hiddenError := fmt.Sprintf("%s is hidden for the rest of this turn by the active runtime contract. Use an available tool, or answer with a concrete blocker if the contract prevents completion.", strings.TrimSpace(req.Tool))

			if strings.TrimSpace(req.Tool) == workspace.ToolNameTask && requestBudget.readOnlyChildTaskSatisfied && !isWriteChildMode(req.Mode) {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Duplicate child task rejected",
					Error:   "the delegated read-only child result is already available, and the task tool is no longer callable this turn. Use an available bounded read/search tool for one exact remaining evidence gap, or answer from the child result and inspected evidence with material uncertainty.",
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
				r.emitEvent(RunEvent{Role: "post_child_duplicate_task_rejected", Content: result.Error})

				continue
			}

			if exactAnchorWriteOnlyActive && !workspace.IsWorkspaceWriteTool(req.Tool) && rejectionRecoveryNudgeUsed {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: hiddenSummary,
					Error:   hiddenError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				if answer, ok := r.exactAnchorHiddenToolBlockerAnswer(req, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_prewrite_hidden_tool_repeated", "exact_anchor_prewrite_hidden_tool_repeated", collectedResults, true)

					return finish(answer, nil, "exact_anchor_prewrite_hidden_tool_repeated", true)
				}
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				hiddenSummary,
				hiddenError,
			)
			if done {
				if answer, ok := r.exactAnchorHiddenToolBlockerAnswer(req, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_prewrite_hidden_tool_repeated", "exact_anchor_prewrite_hidden_tool_repeated", collectedResults, true)

					return finish(answer, nil, "exact_anchor_prewrite_hidden_tool_repeated", true)
				}

				return finish(answer, err, "hidden_tool_repeated", true)
			}

			continue
		}

		if summary, errorText, prompt, reject := r.implementationPreWriteNonWriteRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			implementationWriteOnlyRecoveryActive = true

			if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
				requiredPathCompletionRecoveryActive = true
			}

			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "implementation_write_only_recovery", Content: prompt})

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMissingTargetReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_missing_target_read_ranked_candidate")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		planningTransport := r.planExecutePlanningTransportFromOptions(opts, terminalSubmitPlanNativeTools(), false)
		planningState := DerivePlanExecutePlanningActionState(r.RuntimeBrief, collectedResults, planningTransport, req)

		if !recoveryReadAllowanceGrantedThisRequest {
			if reason, prompt, ok := planThenExecuteCandidateLowValueAction(req, r.RuntimeBrief, collectedResults, planningTransport); ok {
				r.emitEvent(marshalRunEvent("plan_then_execute_low_value_action", map[string]any{
					"agent":             r.agentName(),
					"tool":              strings.TrimSpace(req.Tool),
					"path":              normalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
					"reason":            reason,
					"terminal_options":  planningState.TerminalOptions,
					"low_value_actions": planningState.LowValueActions,
				}))

				if slices.Contains(planningState.TerminalOptions, workspace.ToolNameSubmitPlan) {
					opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
					setCurrentAllowedTools(currentAllowedTools)
					refreshPlanThenExecuteTerminalSurface("low_value_candidate_action")
					syncPlanThenExecuteRecoveryAllowedTools()
				}

				if planThenExecuteLowValueActionShouldReject(reason) {
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						planThenExecuteLowValueActionRejectSummary(reason),
						prompt,
					)
					if done {
						return finish(answer, err, planThenExecuteLowValueActionStopReason(reason), true)
					}

					continue
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				continue
			}
		}

		if summary, errorText, prompt, reject := r.planThenExecuteSubmitPlanOnlySurfaceRejection(req, collectedResults, currentAllowedTools); reject && !recoveryReadAllowanceGrantedThisRequest {
			controller.recordRejectedResult(stepNumber, activeToolCall, req, workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}, "tool_rejected")
			refreshPlanThenExecuteTerminalSurface("submit_plan_only_rejection")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

			continue
		}

		if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
			r.emitEvent(RunEvent{
				Role:    closeout.EventRole,
				Content: closeout.EventContent,
			})

			return finish(closeout.Answer, nil, closeout.StopReason, false)
		}

		if summary, errorText, reject := r.planThenExecuteClampedEOFReadRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_clamped_eof_read_rejected", true)
			}

			continue
		}

		surfaceAllowedTools := runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools, r.RuntimeBrief, collectedResults, req)
		historyRedirectAllowedTools := append(append([]string(nil), surfaceAllowedTools...), workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol)

		if summary, errorText, reject := currentToolSurfaceRejection(req, surfaceAllowedTools); reject && !recoveryReadAllowanceGrantedThisRequest {
			redirected := false

			if planThenExecuteUnscopedEmptySubmitPlanAttempt(r.RuntimeBrief, req) {
				result := workspace.ToolResult{
					Tool:    workspace.ToolNameSubmitPlan,
					Code:    workspace.SubmitPlanEmptyEditsCode,
					Summary: "submit_plan rejected",
					Error:   "submit_plan requires at least one edit in the edits array",
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())

				refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				rebuildTools()

				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_empty_submit_plan_recovery", Content: prompt})

				continue
			} else if redirectedReq, redirectedRaw, ok := r.submitPlanBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
				submitReq := req

				deferredGitHistorySubmitPlan = &submitReq
				deferredGitHistorySubmitPlanRaw = raw
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_submit_plan_before_history")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, redirectedRaw, ok := r.sourceReadBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_source_read_before_history")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, ok := postWriteSourceSanityRedirect(req, r.RuntimeBrief, collectedResults, surfaceAllowedTools); ok {
				redirectedRaw := marshalToolRequestRaw(redirectedReq)
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "post_write_lane_sanity_requires_source_sanity")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if historySummary, historyError, ok := planThenExecuteUnavailableHistoryToolNudge(req, r.RuntimeBrief, collectedResults, surfaceAllowedTools); ok {
				summary = historySummary
				errorText = historyError
			} else if submitCorrectionSummary, submitCorrectionError, prompt, ok := planThenExecuteSubmitCorrectionCurrentSurfaceRejection(req, r.RuntimeBrief, collectedResults); ok {
				summary = submitCorrectionSummary
				errorText = submitCorrectionError
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)
				refreshPlanThenExecuteTerminalSurface("submit_correction_wrong_tool")
				syncPlanThenExecuteRecoveryAllowedTools()
			} else if newFileSummary, newFileError, ok := unavailableRequiredNewFileProbeNudge(req, r.RuntimeBrief, r.Workspace); ok {
				summary = newFileSummary
				errorText = newFileError
			} else if action, ok := r.planThenExecuteRequiredSourceSurfaceAction(req, collectedResults, surfaceAllowedTools); ok {
				if action.Redirect {
					r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
						"agent":          r.agentName(),
						"original_tool":  strings.TrimSpace(req.Tool),
						"rewritten_tool": strings.TrimSpace(action.Request.Tool),
						"original_raw":   strings.TrimSpace(raw),
						"rewritten_raw":  strings.TrimSpace(action.Raw),
					}))

					turnMessages = append(turnMessages, llama.ChatMessage{
						Role:    "system",
						Content: rewriteVisibilityNote(req.Tool, action.Request.Tool),
					})
					req = action.Request
					raw = action.Raw
					currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
					redirected = true
				} else {
					summary = action.Summary
					errorText = action.Error
				}
			}

			if !redirected {
				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
				if done {
					return finish(answer, err, "tool_not_allowed_by_runtime_surface", true)
				}

				continue
			}
		}

		if redirectedReq, redirectedRaw, ok := r.submitPlanBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
			submitReq := req

			deferredGitHistorySubmitPlan = &submitReq
			deferredGitHistorySubmitPlanRaw = raw
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_submit_plan_before_history")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.implementationTargetSourceWriteBeforeEvidenceRedirect(req, collectedResults, currentAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "implementation_write_before_evidence_bounded_read")
			deferredSourceEvidenceWrite = newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, collectedResults, currentAllowedTools); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "implementation_target_source_write_rejected", true)
			}

			continue
		}

		implementationHistoryRedirectAllowedTools := append(append([]string(nil), currentAllowedTools...), workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol)
		if redirected, redirectedRaw, ok := r.writeBeforeGitHistoryEvidenceRedirect(req, collectedResults, implementationHistoryRedirectAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "write_before_git_history_bounded_history")
			deferredSourceEvidenceWrite = newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path)

			if deferredSourceEvidenceWrite != nil {
				deferredSourceEvidenceWrite.RequiresHistory = true
			}

			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, reject := r.writeBeforeGitHistoryEvidenceRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "write_before_git_history_rejected", true)
			}

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteCandidateRangeLessReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_candidate_rangeless_read_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMalformedSymbolReadRedirect(req, collectedResults, currentAllowedTools); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_malformed_symbol_read_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, ok := rewriteVerificationShellReadToSourceSanity(req, r.RuntimeBrief, collectedResults, currentAllowedTools); ok {
			redirectedRaw := marshalToolRequestRaw(redirected)
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":                      "normalized",
				"transport":                    "verification_shell_read_rewrite",
				"tool":                         strings.TrimSpace(redirected.Tool),
				"path":                         strings.TrimSpace(redirected.Path),
				"original_tool":                strings.TrimSpace(req.Tool),
				"original_raw":                 strings.TrimSpace(raw),
				"rewritten_raw":                strings.TrimSpace(redirectedRaw),
				"reason":                       "source_edit_verification_requires_source_sanity",
				workspace.ToolNameSourceSanity: true,
				"changed_source":               true,
			}))

			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, ok := fillSourceSanityPathFromSingleChangedSource(req, r.RuntimeBrief, collectedResults, currentAllowedTools); ok {
			redirectedRaw := marshalToolRequestRaw(redirected)
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":                      "normalized",
				"transport":                    "source_sanity_path_repair",
				"tool":                         strings.TrimSpace(redirected.Tool),
				"path":                         strings.TrimSpace(redirected.Path),
				"original_tool":                strings.TrimSpace(req.Tool),
				"original_raw":                 strings.TrimSpace(raw),
				"rewritten_raw":                strings.TrimSpace(redirectedRaw),
				"reason":                       "source_edit_verification_requires_changed_source_path",
				workspace.ToolNameSourceSanity: true,
				"changed_source":               true,
			}))

			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.deterministicPlanExecuteChainedSearchRecovery(req, collectedResults, &executionState); ok {
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":       toolRequestNormalizerOutcomeNormalized,
				"transport":     "plan_execute_chained_search_recovery",
				"tool":          strings.TrimSpace(redirected.Tool),
				"path":          strings.TrimSpace(redirected.Path),
				"original_tool": strings.TrimSpace(req.Tool),
				"original_raw":  strings.TrimSpace(raw),
				"rewritten_raw": strings.TrimSpace(redirectedRaw),
				"reason":        "consume_context_pack_candidate_before_more_search",
			}))

			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		metrics.ToolCalls++
		// Conversation lane: periodic call-count awareness nudges.
		// Milestones derived from model context window size so
		// smaller models get earlier nudges, larger models later ones.
		// The auto-findings capture fires at convFirstMilestone;
		// awareness nudges begin one nudgeInterval later so the two
		// signals are staggered rather than competing at the same call.
		if convFirstMilestone > 0 && metrics.ToolCalls > 5 {
			shouldNudge := false

			var nudgeMsg string

			firstNudgeAt := convFirstMilestone + convNudgeInterval

			if metrics.ToolCalls == firstNudgeAt {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. If you have evidence to answer this specific question, answer now in plain prose. Do not begin new investigation on neighboring topics.", metrics.ToolCalls, strings.TrimSpace(r.RuntimeBrief.Objective))
			} else if metrics.ToolCalls >= firstNudgeAt+convNudgeInterval && (metrics.ToolCalls-firstNudgeAt)%convNudgeInterval == 0 {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. Answer this question directly from the evidence gathered, or if more evidence is genuinely needed, take one narrow next step. Do not explore related code or begin new investigations.", metrics.ToolCalls, strings.TrimSpace(r.RuntimeBrief.Objective))
			}

			if shouldNudge {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
				r.emitEvent(RunEvent{Role: "call_count_awareness", Content: nudgeMsg})
			}
		}

		// Narrow-tool repetition nudge: if the model repeatedly calls the
		// same tool family (code_navigate, read_file, search_*) without
		// trying a broader discovery tool, suggest a strategy shift.
		// This catches patterns like 50 consecutive code_navigate
		// read_symbol calls — each targeting a different symbol, so
		// same-path fixation and fingerprint hard block never fire.
		if narrowFamily := narrowToolFamily(req.Tool); narrowFamily != "" {
			if narrowFamily == lastNarrowToolFamily {
				consecutiveNarrowToolCalls++
			} else {
				consecutiveNarrowToolCalls = 1
				lastNarrowToolFamily = narrowFamily
			}

			// Only nudge if the model has been doing the same narrow
			// family for a while and hasn't used a broad discovery tool
			// recently (tracked via discovery tool reset below).
			if consecutiveNarrowToolCalls >= narrowToolFamilyNudgeAt && !narrowToolFamilyNudgeFired {
				narrowToolFamilyNudgeFired = true
				prompt := narrowToolFamilyRepetitionNudgePrompt(narrowFamily, consecutiveNarrowToolCalls)
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "narrow_tool_repetition_nudge", Content: prompt})
			}

			// Auto-redirect: only for code_navigate families. Redirecting
			// read_file or search away from their target breaks legitimate
			// evidence gathering. The model can override by repeating.
			if narrowToolFamilyNudgeFired && !narrowToolFamilyRedirectIssued && narrowToolFamilyAutoRedirect(narrowFamily) {
				narrowToolFamilyRedirectIssued = true
				narrowToolFamilyRedirectedOriginal = req.Tool + "|" + narrowFamily

				redirected := req
				redirected.Tool = workspace.ToolNameContextQuery
				redirected.Query = r.RuntimeBrief.Objective
				redirected.Pattern = ""
				redirected.Path = ""
				redirected.Capability = ""
				redirected.Purpose = ""

				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, "", "narrow_tool_redirect_to_context_query")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				r.emitEvent(RunEvent{Role: "narrow_tool_redirect", Content: fmt.Sprintf("redirected %s to context_query after %d consecutive same-family calls", lastNarrowToolFamily, consecutiveNarrowToolCalls)})
			} else if narrowToolFamilyNudgeFired && narrowToolFamilyRedirectIssued {
				// Model repeated the original call after redirect:
				// let it through. Reset redirect tracking so the
				// pattern can be observed again if needed.
				redirectKey := req.Tool + "|" + narrowFamily
				if redirectKey == narrowToolFamilyRedirectedOriginal {
					narrowToolFamilyRedirectIssued = false
					narrowToolFamilyRedirectedOriginal = ""
				}
			}
		} else {
			// Reset on any broad/non-narrow tool — the model tried
			// something different, so clear the repetition tracking.
			consecutiveNarrowToolCalls = 0
			lastNarrowToolFamily = ""
			narrowToolFamilyNudgeFired = false
			narrowToolFamilyRedirectIssued = false
			narrowToolFamilyRedirectedOriginal = ""
		}

		actionClass := toolActionClass(req)
		discoveryProfile := workspace.DiscoveryProfileForRequest(req)

		if !discoveryProfile.Discovery {
			discoveryProfile = workspace.ToolDiscoveryProfile{}
		}

		if onToolRequest != nil {
			onToolRequest(ToolRequestEvent{
				Step:           stepNumber,
				Agent:          r.agentName(),
				Raw:            raw,
				Request:        req,
				Mode:           r.Workspace.Mode(),
				Broad:          workspace.IsBroadInspectionRequest(req),
				Narrowing:      workspace.IsNarrowingRequest(req),
				Action:         actionClass,
				DiscoveryClass: discoveryProfile.Class,
				DiscoveryScope: discoveryProfile.Scope,
				DiscoveryCost:  discoveryProfile.Cost,
			})
		}

		if summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_required_path_discovery_rejected", true)
			}

			continue
		}

		if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_candidate_post_readiness_discovery_rejected", true)
			}

			if summary == "Anchor refresh capability required" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
				r.emitEvent(RunEvent{Role: "plan_then_execute_anchor_refresh_required", Content: errorText})

				continue
			}

			prompt := r.planThenExecuteSubmitReadyTerminalPrompt(errorText, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
			transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_ready_terminal_required", "candidate_post_readiness_discovery")

			continue
		}

		if summary, errorText, reject := r.repeatedRangeLessReadFileRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "repeated_rangeless_read_file_rejected", true)
			}

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, collectedResults, currentAllowedTools); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_symbol_read_after_miss_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		toolKey := toolRequestKey(req)
		if prompt, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastToolResult, collectedResults, terminalSubmitPlanNativeTools()); ok && toolKey != "" && toolKey == lastToolKey && replayedToolRequests[toolKey] < 2 {
			replayedToolRequests[toolKey]++

			refreshPlanThenExecuteTerminalSurface("submit_plan_replayed_invalid")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_repeated_submit_plan_correction", Content: prompt})

			continue
		}

		if shouldRejectExactReplayBeforeLoopGate(req) && toolKey != "" && toolKey == lastToolKey && replayedToolRequests[toolKey] > 0 {
			summary := "Repeated tool request rejected"
			errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."

			if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, lastToolResult, collectedResults); ok {
				summary = replaySummary
				errorText = replayError
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				summary,
				errorText,
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}

			continue
		}

		// DOOM_LOOP guard — if the model emits exactly the same tool + args
		// three turns in a row, stop the auto-execute path and surface a
		// concrete blocker to the user. OpenCode uses threshold=3 at
		// session/processor.ts; we match that. Counting includes accepted
		// calls (not just replays/rejections) because the pathology is the
		// model getting stuck even when the harness doesn't push back.
		identicalCallRun := requestBudget.noteFingerprint(currentFingerprint)
		if isReadClassTool(req) {
			if decision := requestBudget.samePathFixationDecision(req, genericReadOnlyEvidenceReady(r.RuntimeBrief, collectedResults)); decision.Handled {
				if tool := strings.TrimSpace(decision.HideTool); tool != "" && !turnHiddenTools[tool] {
					turnHiddenTools[tool] = true

					rebuildTools()
					r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after same-path fixation", tool)})
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: util.FirstNonEmpty(decision.EventContent, decision.Nudge)})

				continue
			}
		}

		if identicalCallRun >= 3 {
			if result, prompt, ok := r.mutationNoWriteLoopRecovery(req, collectedResults, identicalCallRun); ok {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				implementationWriteOnlyRecoveryActive = true

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "mutation_no_write_loop_recovery", Content: prompt})

				continue
			}

			if !planThenExecuteLoopBudgetRecoveryUsed {
				if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row", req.Tool, identicalCallRun), chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
					planThenExecuteLoopBudgetRecoveryUsed = true
					opts = forceVisibleActionChatOptions(opts)

					if recovery.TerminalOnly && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
						opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
						opts.ToolChoice = "auto"
					}

					syncCurrentAllowedToolsFromOptions()

					if recovery.TerminalOnly {
						forcePlanThenExecuteTerminalSurface("loop_budget_recovery")

						currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
						setCurrentAllowedTools(currentAllowedTools)
					}

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
					r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

					continue
				}
			} else if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments after a loop-budget recovery", req.Tool), chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}

			if autonomyRecorder != nil {
				autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", currentFingerprint, identicalCallRun)
			}

			// Conversation lanes: repeated reads on the same file are
			// legitimate investigation, not loops. The model is probing
			// different symbols, line ranges, or patterns — not stuck.
			// Skip the doom-loop gate for read-class tools in conversation;
			// same-path fixation is handled by requestBudget before this
			// exact-argument branch so varied ranges are covered too.
			if r.lanePolicy().SkipFingerprintHardBlock() && isReadClassTool(req) {
				requestBudget.fingerprint.identicalRun = 0
			} else if r.DoomLoopGate != nil {
				r.emitEvent(RunEvent{Role: "doom_loop_guard", Content: fmt.Sprintf("model repeated %s %d times — approval required", req.Tool, identicalCallRun)})

				if r.DoomLoopGate(req, identicalCallRun) {
					// User approved — reset the streak and let this call execute.
					requestBudget.fingerprint.identicalRun = 0
				} else {
					if answer, reason, exhausted, ok := r.reviewRepairDoomLoopTerminalCloseout(collectedResults); ok {
						r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_doom_loop_closeout", Content: reason})

						return finish(answer, nil, reason, exhausted)
					}

					// User denied — feed a blocker result, then force a
					// no-tools closeout from gathered evidence. Returning the
					// raw rejectTool result can produce an empty final answer
					// when this is the first rejection in the turn.
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						"Doom-loop guard: user denied continuation",
						fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; user denied continuation. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
					)
					if !done {
						answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
					}

					return finish(answer, err, "doom_loop_guard", true)
				}
			} else {
				if answer, reason, exhausted, ok := r.reviewRepairDoomLoopTerminalCloseout(collectedResults); ok {
					r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_doom_loop_closeout", Content: reason})

					return finish(answer, nil, reason, exhausted)
				}

				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Doom-loop guard tripped",
					fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; stopping to prevent budget exhaustion. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
				)
				if !done {
					answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
				}

				return finish(answer, err, "doom_loop_guard", true)
			}
		}

		requestBudget.setRecoveryContext(r.RuntimeBrief, collectedResults)
		fingerprintDecision := requestBudget.fingerprintDecision(policy, req, currentFingerprint)
		if !planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				planThenExecuteLoopBudgetRecoveryUsed = true
				opts = forceVisibleActionChatOptions(opts)

				if recovery.TerminalOnly && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()

				if recovery.TerminalOnly {
					forcePlanThenExecuteTerminalSurface("loop_budget_recovery")

					currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
					setCurrentAllowedTools(currentAllowedTools)
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}
		} else if planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}
		}

		// Conversation lanes: the fingerprint hard block's forced
		// completion cuts off legitimate investigation. Clear the
		// ForceAnswerPrompt before apply() so the model can continue.
		if r.lanePolicy().SkipFingerprintHardBlock() && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			fingerprintDecision.ForceAnswerPrompt = ""
			fingerprintDecision.ForceAnswerReason = ""
		}

		if res := fingerprintDecision.apply(budgetDecisionContext{
			controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
		}); res.shouldFinish {
			return finish(res.answer, res.err, res.reason, true)
		} else if res.shouldContinue {
			continue
		}

		if prompt, ok := closureSynthesisAfterIgnoredNudgePrompt(
			r.agentName(),
			r.RuntimeBrief,
			collectedResults,
			req,
			delegatedReadOnlyAnswerReady,
			postChildPacketActive,
			delegatedReadOnlyDiminishingNudgeStage,
			postChildDiminishingNudgeStage,
		); ok {
			answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
			return finish(answer, err, "closure_synthesis_after_ignored_nudge", true)
		}

		if res := requestBudget.explorationDecision(r, req, collectedResults).apply(budgetDecisionContext{
			controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
		}); res.shouldFinish {
			return finish(res.answer, res.err, res.reason, true)
		} else if res.shouldContinue {
			continue
		}

		if !r.planThenExecutePlannerPhase() &&
			// The synthesis nudge uses generic "answer now" language that
			// contradicts the conversation lane's evidence requirement
			// (file:line from at least one inspected source location).
			// The conversation lane has its own evidence-aligned nudge.
			!r.lanePolicy().SkipSynthesisNudge() {

			if res := requestBudget.synthesisDecision(policy, metrics.ToolCalls, runnerBriefRequiresMutation(r.RuntimeBrief)).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}

		if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject && !recoveryReadAllowanceGrantedThisRequest {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)

			if r.planThenExecutePlannerPhase() {
				if planThenExecutePostReadinessRejectionRequiresSubmitReadyTransition(summary) {
					prompt := r.planThenExecuteSubmitReadyTerminalPrompt(errorText, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
					transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_plan_required", "corrected_submit_plan_required")
				} else {
					rebuildTools()

					opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					syncPlanThenExecuteRecoveryAllowedTools()
					r.refreshPlanThenExecuteTerminalToolSurface(&turnMessages, collectedResults, "corrected_submit_plan_required")

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
					r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: errorText})
				}
			}

			if done {
				return finish(answer, err, "plan_then_execute_post_readiness_discovery_rejected", true)
			}

			continue
		}

		if isReadClassTool(req) {
			requestBudget.noteReadClassRequest(req)

			metrics.ReadClassCalls++

			if res := requestBudget.readBudgetDecision(policy, req).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}

		// Note tool: harness-owned scratch metadata, not a workspace mutation.
		if ok && strings.TrimSpace(req.Tool) == noteToolName {
			noteResult := runNoteTool(r.Workspace.Root(), raw)

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, noteResult, "note_tool", runnerRecordOptions{})
			r.emitEvent(RunEvent{Role: "note_tool", Content: noteResult.Summary})

			continue
		}

		discoveryRedirect.SetAllowedTools(currentAllowedTools)

		if !implementationScopedReadFileRequestInExplicitScope(r.RuntimeBrief, collectedResults, req) {
			// Conversation-lane redirect: repeated read_file → code_navigate.
			// The model often reads the same file in 50-line chunks when it
			// could use read_symbol to get full symbol bodies in one call.
			if decision := discoveryRedirect.considerConversationReadFileRedirect(req, r.RuntimeBrief); decision.ShouldRedirect {
				result := decision.Result

				recorder.activeToolCall = activeToolCall
				recorder.record(stepNumber, req, result, "discovery_redirect_conversation_read", runnerRecordOptions{})
				r.emitEvent(RunEvent{Role: "discovery_redirect_conversation", Content: fmt.Sprintf("%s → code_navigate read_symbol", req.Tool)})

				continue
			}

			if decision := discoveryRedirect.Consider(req, r.Workspace); decision.ShouldRedirect {
				// Hard-reject path: the model has been nudged enough times
				// on this redirect class. Route through rejectTool so the
				// fingerprint is recorded in rejectedFingerprints and a
				// repeat terminates the turn. The soft-redirect path stays
				// as a result append with no rejection counters bumped.
				if decision.HardReject {
					if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
						r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
					}

					r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s) HARD_REJECT", req.Tool, decision.Language, decision.Capability)})

					// Hide the source tool from the manifest for the rest
					// of the turn. If shell was redirected to a structured
					// tool N times and now hard-rejected, the model has
					// shown it won't self-correct — remove the tool.
					if tool := strings.TrimSpace(req.Tool); tool != "" && !turnHiddenTools[tool] && r.shouldHideToolAfterDiscoveryHardRedirect(req) {
						turnHiddenTools[tool] = true

						rebuildTools()
						r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after hard-redirect", tool)})
					}

					answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, decision.Result.Summary, decision.Result.Error)
					if done {
						return finish(answer, err, "redirect_hard_block_exhausted", true)
					}

					continue
				}

				result := decision.Result

				recorder.activeToolCall = activeToolCall
				recorder.record(stepNumber, req, result, "discovery_redirect", runnerRecordOptions{})

				if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
					r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
				}

				r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s)", req.Tool, decision.Language, decision.Capability)})

				continue
			}
		}

		if summary, errorText, reject := declarationMoveConstraintRejection(req, declarationMovesForbidden); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "go_refactor_guard_exhausted", true)
			}

			continue
		}

		if !evidenceFirstTargetPhaseCleared && evidenceFirstTargetReadUnlocked(r.RuntimeBrief, collectedResults) {
			clearEvidenceFirstTargetReadActionClasses(rejectedActions, r.RuntimeBrief)

			evidenceFirstTargetPhaseCleared = true
		}

		repeatedDeadEndRecovered := false

		if actionClass != "" && rejectedActions[actionClass] > 0 {
			if redirected, redirectedRaw, ok := r.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, collectedResults, currentAllowedTools); ok {
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_symbol_read_after_read_failure_bounded")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				repeatedDeadEndRecovered = true
			}
		}

		if actionClass != "" && rejectedActions[actionClass] > 0 && !repeatedDeadEndRecovered {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Repeated dead-end action rejected",
				"this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
			)
			if done {
				return finish(answer, err, "repeated_dead_end_budget_exhausted", true)
			}

			continue
		}

		if r.Workspace.IsExploreMode() && workspace.IsBroadInspectionRequest(req) && executionState.broadInspectionCount >= 1 {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Additional broad inspection rejected",
				"explore mode already has a workspace overview for this turn. Available contract outcomes are an answer from the existing overview, file-name search, exact text search, or bounded reads on discovered handles instead of another broad listing.",
			)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}

			continue
		}

		if util.EqualTrimFold(req.Tool, workspace.ToolNameListFiles) && executionState.consecutiveListFiles >= 2 {
			// Hide list_files from the manifest for the rest of the turn
			// — at this point we've told the model "stop walking dirs"
			// once already, and keeping list_files in the tool list just
			// invites another rejected call. Mode-gating the tool out is
			// more effective than rejecting each retry with a message the
			// model already ignored.
			if !turnHiddenTools[workspace.ToolNameListFiles] {
				turnHiddenTools[workspace.ToolNameListFiles] = true

				rebuildTools()
				r.emitEvent(RunEvent{Role: "tool_hidden", Content: "list_files hidden for rest of turn after chained-gate fire"})
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Chained list_files rejected",
				fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files is hidden for the rest of this turn. Take one narrower next step on a path you already discovered, or answer with the evidence already gathered.", executionState.consecutiveListFiles),
			)
			if done {
				return finish(answer, err, "list_files_chain_budget_exhausted", true)
			}

			continue
		}

		if executionState.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, r.RuntimeBrief) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Discovery consumption required before more broad discovery",
				executionState.discoveryConsumptionError(),
			)
			if done {
				return finish(answer, err, "discovery_consumption_budget_exhausted", true)
			}

			continue
		}

		if executionState.shouldConsumeSearchHitBeforeMoreSearch(req) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Chained search rejected",
				executionState.searchHitConsumptionError(),
			)
			if done {
				return finish(answer, err, "search_chain_budget_exhausted", true)
			}

			continue
		}

		if executionState.needsNarrowingAfterBroad && workspace.IsBroadInspectionRequest(req) {
			errorText := "you already have a broad workspace overview. Either answer from the evidence already gathered, or take one narrower next step (a discovered handle, a single named symbol, or a focused search) before any further broad inspection."
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, "Broad inspection request rejected", errorText)

			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}

			continue
		}

		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
			if lastToolResult != nil {
				if allowRecoverablePostReadinessRepairExecution(req, *lastToolResult) {
					delete(replayedToolRequests, toolKey)
				} else {
					if summary, errorText, reject := repeatedDiagnosticLogReadFileExactRejection(req, *lastToolResult); reject {
						answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
						if done {
							return finish(answer, err, "repeated_diagnostic_log_read_rejected", true)
						}

						continue
					}

					if replayedToolRequests[toolKey] > 0 {
						summary := "Repeated tool request rejected"
						errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."

						if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, lastToolResult, collectedResults); ok {
							summary = replaySummary
							errorText = replayError
						}

						answer, err, done := controller.rejectTool(
							stepNumber,
							activeToolCall,
							req,
							summary,
							errorText,
						)
						if done {
							return finish(answer, err, "repeated_tool_budget_exhausted", true)
						}

						continue
					}

					replayed := replayRepeatedToolRequest(req, *lastToolResult)
					replayedToolRequests[toolKey]++

					recorder.activeToolCall = activeToolCall
					recorder.recordWithOptions(stepNumber, req, replayed, "tool_replayed", runnerRecordOptions{
						ResetRejected:       true,
						IncrementNoProgress: true,
						SetLastToolKey:      false,
					})

					if policy.shouldStopForNoProgress(consecutiveNoProgress) {
						answer, err := controller.completeWithoutMoreToolsWithReason("Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the repeated request leaves one exact claim unsupported, state that uncertainty after the supported answer.")
						return finish(answer, err, "no_progress_budget_exhausted", true)
					}

					continue
				}
			}

			errorText := "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes."
			if strings.TrimSpace(req.Tool) == workspace.ToolNameGoMoveDeclarations {
				errorText = "you just ran the same declaration move. Choose a different destination/declaration batch, run verification, or answer with the current blocker."
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Repeated tool request rejected",
				errorText,
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}

			continue
		}

		permissionDecision := r.PermissionPolicy.Decide(req)
		if r.Workspace != nil && r.Workspace.EvaluateConfiguredToolRule(req) == workspace.ToolRuleDeny {
			permissionDecision = PermissionDecisionSnapshot{
				Tool:     strings.TrimSpace(req.Tool),
				Target:   permissionTarget(req),
				Decision: PermissionDeny,
				Rule:     "workspace.tool_rules decision=deny",
			}
		}

		if autonomyRecorder != nil {
			autonomyRecorder.emitIntentClassified(req)
			autonomyRecorder.emitPathRolesResolved(req)
		}

		if permissionDecision.Decision != PermissionAllow {
			r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))

			if autonomyRecorder != nil {
				decision := autonomy.PolicyDeny
				resultLabel := "denied"

				if permissionDecision.Decision == PermissionAsk {
					decision = autonomy.PolicyAsk
					resultLabel = "approval_required"
				}

				autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:         decision,
					ReasonCode:       "permission_policy",
					ApprovalRequired: permissionDecision.Decision == PermissionAsk,
					Result:           resultLabel,
					ErrorClass:       "permission_policy",
					Source:           "runner_permission_policy",
				})
			}

			result := permissionDecision.ToolResult()

			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "permission_denied", runnerRecordOptions{
				CountRejected:  true,
				SetLastToolKey: true,
			})

			if workspaceToolRulesPermissionDeny(permissionDecision) {
				answer := workspaceToolRulesPermissionDenyAnswer(req, result)

				emitCompletionStatus("blocked", "workspace_tool_rules_deny", "workspace_tool_rules_deny", collectedResults, true)

				return finish(answer, nil, "workspace_tool_rules_deny", true)
			}

			if permissionDecision.Decision == PermissionAsk {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "permission_approval_required", false)
			}

			continue
		}

		r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))

		if autonomyRecorder != nil {
			autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
				Decision:   autonomy.PolicyAllow,
				ReasonCode: "permission_policy",
				Result:     "no_approval_needed",
				Source:     "runner_permission_policy",
			})
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTask {
			if r.DisableTaskTool {
				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Child task request rejected",
					"nested task spawning is disabled inside child tasks. Answer with the evidence already collected or inspect directly with read-only tools.",
				)
				if done {
					return finish(answer, err, "nested_task_budget_exhausted", true)
				}

				continue
			}

			result := r.runChildTask(ctx, req, opts)

			recorder.activeToolCall = activeToolCall

			if strings.TrimSpace(result.Error) == "" && !isWriteChildMode(req.Mode) {
				requestBudget.readOnlyChildTaskSatisfied = true
				turnHiddenTools[workspace.ToolNameTask] = true

				rebuildTools()

				if ReadOnlyTaskResultAnswerableForObjective(result, r.RuntimeBrief.Objective) {
					evidencePaths := postChildClosureEvidencePaths(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, 4)

					requestBudget.broadEvidenceDelegationSatisfied = true
					r.RuntimeBrief.Constraints = mergeRunnerStateStrings(r.RuntimeBrief.Constraints, []string{"child_packet_available"})
					postChildPacketActive = true
					postChildFollowupReads = 0
					postChildDiminishingNudgeStage = 0
					r.RuntimeBrief.EvidencePaths = mergeRunnerStateStrings(r.RuntimeBrief.EvidencePaths, evidencePaths)
					requestBudget.noteReadOnlyChildPacket(evidencePaths)
					hideBroadDiscoveryTools(turnHiddenTools)
					rebuildTools()

					requestBudget.postChild.readCalls = 0

					nudge := childPacketBoundaryNudge(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, evidencePaths, postChildReadBudget)

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
				}
			}

			recorder.record(stepNumber, req, result, "child_task_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				if shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults) {
					continue
				}

				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Tools are now disabled. Use the child-task result and gathered evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the supported answer.")

				return finish(answer, err, "no_progress_budget_exhausted", true)
			}

			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTaskLedger {
			result := taskLedger.apply(req, collectedResults)

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}

			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameDelegateEdit {
			result := r.runDelegateEdit(ctx, req)

			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
				TrackProgress:  true,
				ResetRejected:  true,
				SetLastToolKey: true,
			})

			if checklist := r.requiredEditPathsProgressMessage(req, result, collectedResults); checklist != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: checklist})
				r.emitEvent(RunEvent{Role: "required_edit_paths_progress", Content: checklist})
			}

			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the delegated edit. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}

			continue
		}

		if r.PreExecute != nil {
			if guarded := r.PreExecute(stepNumber, req); guarded != nil {
				bootstrapReadReminder := isBootstrappedTargetReadReminder(req, *guarded)
				rejectedStreak := false
				rejectedActionCounted := false

				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder {
					rejectedActions[actionClass]++

					rejectedStreak = true
					rejectedActionCounted = true
				} else if strings.TrimSpace(guarded.Error) != "" {
					rejectedActionCounted = true
				}

				recorder.activeToolCall = activeToolCall
				recorder.recordWithOptions(stepNumber, req, *guarded, "pre_execute_guard", runnerRecordOptions{
					CountRejected:           rejectedActionCounted,
					IncrementRejectedStreak: rejectedStreak,
					IncrementNoProgress:     strings.TrimSpace(guarded.Error) != "" && !rejectedStreak,
					SetLastToolKey:          true,
				})
				r.markConcreteSourceEvidenceObserved(*guarded)

				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					if autonomyRecorder != nil {
						autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
							Decision:         autonomy.PolicyAsk,
							ReasonCode:       "write_approval_required",
							ApprovalRequired: true,
							Result:           "approval_required",
							ErrorClass:       "approval_guard",
							Source:           "runner_pre_execute_guard",
						})
					}

					answer, err := controller.completeWithoutMoreTools()

					return finish(answer, err, "write_approval_required", false)
				}

				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && consecutiveRejectedActions >= 2 {
					answer, err := controller.completeWithoutMoreTools()
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}

				if bootstrapReadReminder && policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}

				continue
			}
		}

		if guarded := checkWriteDescription(req); guarded != nil {
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, *guarded, "write_description_missing", runnerRecordOptions{
				CountRejected:           true,
				IncrementRejectedStreak: true,
				SetLastToolKey:          true,
			})

			writeDescriptionRepairActive = true
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = normalizeRunnerPath(req.Path)
			implementationWriteOnlyRecoveryActive = true

			if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
				requiredPathCompletionRecoveryActive = true
			}

			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()

			prompt := r.writeDescriptionRepairPrompt(req)
			r.emitWriteDescriptionValidatorDecision(stepNumber, req, "force_write_or_block", "write_description_missing", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_recovery", Content: prompt})

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			if consecutiveRejectedActions >= 2 {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "write_description_budget_exhausted", true)
			}

			continue
		}

		if summary, errorText, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
			if exactAnchorWriteOnlyActive && workspace.IsWorkspaceWriteTool(req.Tool) {
				exactAnchorWriteFailures++
			}

			if autonomyRecorder != nil {
				autonomyRecorder.emitWriteGate(req, autonomy.PolicyReject, "required_edit_scope_rejected", mapKeys(changedPathsFromToolResults(collectedResults)))
				autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:   autonomy.PolicyDeny,
					ReasonCode: "required_edit_scope_rejected",
					Result:     "denied",
					ErrorClass: "required_edit_scope",
					Source:     "runner_required_edit_scope",
				})
			}

			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if exactAnchorWriteOnlyActive && (rejectionRecoveryNudgeUsed || exactAnchorWriteFailures >= 3) {
				result := workspace.ToolResult{Tool: req.Tool, Summary: summary, Error: errorText}
				if blocker, ok := r.exactAnchorRepeatedWriteFailureBlockerAnswer(req, result, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_write_failure_repeated", "exact_anchor_write_failure_repeated", collectedResults, true)

					return finish(blocker, nil, "exact_anchor_write_failure_repeated", true)
				}
			}

			if done {
				return finish(answer, err, "required_edit_scope_rejected", true)
			}

			continue
		}

		if summary, errorText, reject := plannerOwnedVerificationShellRejection(req, r.RuntimeBrief); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "planner_owned_verification_shell_rejected", true)
			}

			continue
		}

		if summary, errorText, reject := sourceEditVerificationRejection(req, r.RuntimeBrief, collectedResults); reject {
			if autonomyRecorder != nil {
				autonomyRecorder.emitVerificationGate(req, autonomy.PolicyReject, "source_edit_verification_required", "pending")
			}

			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "source_edit_verification_required", true)
			}

			continue
		}

		if prompt, ok := sourceEditCloseoutPrompt(req, r.RuntimeBrief, collectedResults); ok {
			answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
			return finish(answer, err, "source_edit_closeout_after_verification", true)
		}

		if executionState.needsCheckpoint(r.CheckpointPolicy, req) {
			checkpoint, checkpointErr := CaptureGitCheckpoint(ctx, r.Workspace.Root())
			if checkpointErr != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Checkpoint creation failed",
					Error:   checkpointErr.Error(),
				}

				recorder.activeToolCall = activeToolCall
				recorder.recordWithOptions(stepNumber, req, result, "checkpoint_failed", runnerRecordOptions{
					CountRejected:  true,
					SetLastToolKey: true,
				})

				continue
			}

			executionState.markCheckpointCreated()
			r.emitEvent(marshalRunEvent("checkpoint", checkpoint))
		}

		if executionState.shouldRequireVerification(req, r.RuntimeBrief) {
			summary, errorText := verificationRequiredAfterWritesMessage(r.RuntimeBrief, executionState.writesSinceVerification)

			if autonomyRecorder != nil {
				autonomyRecorder.emitVerificationGate(req, autonomy.PolicyReject, "verification_required_after_writes", "pending")
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				summary,
				errorText,
			)
			if done {
				return finish(answer, err, "verification_required_after_writes", true)
			}

			continue
		}

		var result workspace.ToolResult

		reviewRepairNoopPassAnswer := ""

		if guard := r.preflightPlanThenExecuteWriteGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_write_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(req); guard.Intercepted {
			result = guard.Result

			if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
				reviewRepairNoopPassAnswer = closeout.Answer
			}

			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_review_repair_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, collectedResults); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, collectedResults); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecutePlannerToolGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_tool_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteReviewRepairBroadReadGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_review_repair_guard",
				Content: guard.Reason,
			})
		} else if intercept := r.preflightVerificationOnAlreadyEditedPath(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "verification_intercept_already_edited",
				Content: intercept.Reason,
			})
		} else if intercept := r.preflightScopedImplementationBoundedSourceRead(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "scoped_bounded_source_read",
				Content: intercept.Reason,
			})
		} else if intercept := r.preflightScopedImplementationLargeWriteFileReplacement(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "scoped_large_write_file_guard",
				Content: intercept.Reason,
			})
		} else {
			req = r.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, collectedResults)
			switch precheck := r.preflightApplyPatchAgainstChangedPaths(req, collectedResults); precheck.Action {
			case applyPatchPreflightAllSkipped:
				result = precheck.Result
				r.emitEvent(RunEvent{
					Role:    "apply_patch_changed_path_preflight",
					Content: fmt.Sprintf("all patch sections redundant; skipped %s", strings.Join(precheck.SkippedPaths, ", ")),
				})
			case applyPatchPreflightRewrite:
				r.emitEvent(RunEvent{
					Role:    "apply_patch_changed_path_preflight",
					Content: fmt.Sprintf("dropped %d redundant section(s) from apply_patch envelope before workspace dispatch: %s", len(precheck.SkippedPaths), strings.Join(precheck.SkippedPaths, ", ")),
				})

				req = precheck.Request
				result = r.executeWorkspaceWithPreimage(ctx, preimageStore, req)
			default:
				result = r.executeWorkspaceWithPreimage(ctx, preimageStore, req)
			}

			// Convert "file not found" probes on required_edit_paths
			// into an actionable nudge that points the planner at the
			// whole-file shape on submit_plan. Without this rewrite,
			// the planner treats absent new-file paths as a blocker
			// and gives up. See harness fix #7.
			result = rewriteMissingRequiredNewFileProbe(req, result, r.RuntimeBrief)
			result = r.recoverContextPackFreshness(ctx, req, result)
		}

		recorder.activeToolCall = activeToolCall
		recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		r.markConcreteSourceEvidenceObserved(result)

		if !recoveryReadAllowanceGrantedThisRequest && implementationTargetSourceReadEstablishedWriteReady(r.RuntimeBrief, req, result, collectedResults) {
			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed %s source read to establish target-write-ready evidence on %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(req.Path)),
			})
		}

		if completionGateRejections > 0 && strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Tool) != "completion_gate" {
			completionGateRejections = 0

			r.emitEvent(RunEvent{Role: "completion_gate_recovery_progress", Content: "successful tool result reset the completion-gate retry streak"})
		}

		update := executionState.applyResult(req, result, currentFingerprint, r.Workspace, &requestBudget)
		if autonomyRecorder != nil && workspaceWriteResultCompleted(req, result) {
			autonomyRecorder.emitWriteGate(req, autonomy.PolicyAllow, "write_result_completed", mapKeys(changedPathsFromToolResults([]workspace.ToolResult{result})))
		}

		if deferredGitHistorySubmitPlan != nil && gitHistoryEvidenceTool(req.Tool) {
			submitReq := *deferredGitHistorySubmitPlan
			if len(r.submitPlanPathsMissingGitHistory(submitReq, collectedResults)) == 0 {
				submitRaw := deferredGitHistorySubmitPlanRaw
				if strings.TrimSpace(submitRaw) == "" {
					submitRaw = marshalToolRequestRaw(submitReq)
				}

				metrics.ToolCalls++

				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(submitReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            submitRaw,
						Request:        submitReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(submitReq),
						Narrowing:      workspace.IsNarrowingRequest(submitReq),
						Action:         toolActionClass(submitReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}

				var submitResult workspace.ToolResult

				if guard := r.preflightPlanThenExecuteSubmitPlanGroundingGuard(submitReq, collectedResults); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else if guard := r.preflightPlanThenExecuteSubmitPlanHistoryGuard(submitReq, collectedResults); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else if guard := r.preflightPlanThenExecuteSubmitPlanCoverageGuard(submitReq); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else {
					submitResult = r.Workspace.Execute(ctx, submitReq)
				}

				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, submitReq, submitResult, "deferred_git_history_submit_plan", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})

				req = submitReq
				result = submitResult
				update = executionState.applyResult(submitReq, submitResult, toolRequestKey(submitReq), r.Workspace, &requestBudget)
				deferredGitHistorySubmitPlan = nil
				deferredGitHistorySubmitPlanRaw = ""
			}
		}

		if deferredSourceEvidenceWrite != nil && gitHistoryEvidenceTool(req.Tool) {
			if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterHistoryEvidence(collectedResults); replay {
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(replayReq.Tool),
					"original_raw":   marshalToolRequestRaw(req),
					"rewritten_raw":  strings.TrimSpace(replayRaw),
					"reason":         "implementation_write_after_history_evidence_replay",
				}))

				metrics.ToolCalls++

				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(replayReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            replayRaw,
						Request:        replayReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(replayReq),
						Narrowing:      workspace.IsNarrowingRequest(replayReq),
						Action:         toolActionClass(replayReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}

				replayResult := r.Workspace.Execute(ctx, replayReq)

				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, replayReq, replayResult, "deferred_git_history_write", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})
				r.markConcreteSourceEvidenceObserved(replayResult)

				req = replayReq
				result = replayResult
				update = executionState.applyResult(replayReq, replayResult, toolRequestKey(replayReq), r.Workspace, &requestBudget)
				deferredSourceEvidenceWrite = nil
			}
		}

		if replayedOverVerificationRequest != nil && workspaceWriteResultCompleted(req, result) {
			verifyReq := *replayedOverVerificationRequest
			verifyRaw := marshalToolRequestRaw(verifyReq)

			if hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) ||
				sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults) {

				prompt := postWriteVerificationRequiredPrompt(collectedResults, plannerOwnsVerification(r.RuntimeBrief))
				r.emitEvent(RunEvent{Role: "post_write_verification_required", Content: prompt})
			}

			metrics.ToolCalls++

			if onToolRequest != nil {
				profile := workspace.DiscoveryProfileForRequest(verifyReq)
				onToolRequest(ToolRequestEvent{
					Step:           stepNumber,
					Agent:          r.agentName(),
					Raw:            verifyRaw,
					Request:        verifyReq,
					Mode:           r.Workspace.Mode(),
					Broad:          workspace.IsBroadInspectionRequest(verifyReq),
					Narrowing:      workspace.IsNarrowingRequest(verifyReq),
					Action:         toolActionClass(verifyReq),
					DiscoveryClass: profile.Class,
					DiscoveryScope: profile.Scope,
					DiscoveryCost:  profile.Cost,
				})
			}

			verifyResult := r.Workspace.Execute(ctx, verifyReq)

			recorder.activeToolCall = nil
			recorder.recordWithOptions(stepNumber, verifyReq, verifyResult, "deferred_post_write_verification", runnerRecordOptions{
				TrackProgress:  true,
				ResetRejected:  true,
				SetLastToolKey: true,
			})
			r.markConcreteSourceEvidenceObserved(verifyResult)

			req = verifyReq
			result = verifyResult
			update = executionState.applyResult(verifyReq, verifyResult, toolRequestKey(verifyReq), r.Workspace, &requestBudget)
		}

		if workspaceWriteResultCompleted(req, result) {
			if verifyReq, ok := deterministicPostWriteVerificationRequest(collectedResults, r.RuntimeBrief); ok {
				verifyRaw := marshalToolRequestRaw(verifyReq)
				metrics.ToolCalls++

				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(verifyReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            verifyRaw,
						Request:        verifyReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(verifyReq),
						Narrowing:      workspace.IsNarrowingRequest(verifyReq),
						Action:         toolActionClass(verifyReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}

				verifyResult := r.Workspace.Execute(ctx, verifyReq)

				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, verifyReq, verifyResult, "deterministic_post_write_verification", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})
				r.markConcreteSourceEvidenceObserved(verifyResult)

				req = verifyReq
				result = verifyResult
				update = executionState.applyResult(verifyReq, verifyResult, toolRequestKey(verifyReq), r.Workspace, &requestBudget)
			}
		}

		if update.ResetVerificationGuards {
			clearVerificationGateFingerprints(requestBudget.fingerprint.rejected)
			clearRunnerStringIntMap(rejectedActions)
			rebuildTools()
		}

		if workspaceWriteResultCompleted(req, result) &&
			(hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) ||
				sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults)) {

			prompt := postWriteVerificationRequiredPrompt(collectedResults, plannerOwnsVerification(r.RuntimeBrief))

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			opts = forceVisibleActionChatOptions(opts)

			syncCurrentAllowedToolsFromOptions()
			r.emitEvent(RunEvent{Role: "post_write_verification_required", Content: prompt})
		}

		if !terminalSubmitPlanNativeTools() && len(r.remainingRequiredEditPaths(collectedResults)) > 0 &&
			implementationRequiredPathAnchorsReady(r.RuntimeBrief, collectedResults) &&
			(!r.planThenExecutePlannerPhase() || planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, collectedResults)) {

			opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, false)
			r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "all remaining required edit paths have anchor-ready source evidence; textual backend recovery will disable hidden thinking and require visible write/blocker output"})
		}

		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)

		if autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}

			autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}

		if update.VerificationState == "done" &&
			runnerBriefRequiresMutation(r.RuntimeBrief) &&
			len(r.RuntimeBrief.RequiredEditPaths) == 0 &&
			len(changedPathsFromToolResults(collectedResults)) > 0 &&
			!hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) &&
			!sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults) {

			_ = r.emitPostWriteVerificationTelemetry(req, result)

			answer, err := controller.completeWithoutMoreToolsWithReason("The requested implementation has a recorded workspace write and successful source-aware verification. Tools are now disabled. Write the implementation result now: changed paths and verification status.")

			return finish(answer, err, "source_edit_closeout_after_verification", false)
		}

		if exactAnchorWriteOnlyActive && workspaceWriteResultCompleted(req, result) {
			exactAnchorWriteOnlyActive = false

			releaseExactAnchorReadTools(turnHiddenTools, exactAnchorHiddenTools)
			rebuildTools()
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "read-class tools restored after successful exact-anchor write"})
		} else if exactAnchorWriteOnlyActive && workspace.IsWorkspaceWriteTool(req.Tool) && strings.TrimSpace(result.Error) != "" {
			exactAnchorWriteFailures++

			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "read-class tools remain hidden after failed exact-anchor write"})

			if rejectionRecoveryNudgeUsed || exactAnchorWriteFailures >= 3 {
				if answer, ok := r.exactAnchorRepeatedWriteFailureBlockerAnswer(req, result, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_write_failure_repeated", "exact_anchor_write_failure_repeated", collectedResults, true)

					return finish(answer, nil, "exact_anchor_write_failure_repeated", true)
				}
			}
		}

		if implementationWriteOnlyRecoveryActive && workspaceWriteResultCompleted(req, result) && len(r.remainingRequiredEditPaths(collectedResults)) == 0 {
			implementationWriteOnlyRecoveryActive = false
			implementationWriteOnlyNonWriteRejects = 0
			requiredPathCompletionRecoveryActive = false
			writeDescriptionRepairActive = false
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = ""

			releaseHiddenTools(turnHiddenTools, implementationWriteOnlyHiddenTools)
			rebuildTools()
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "non-write tools restored after required implementation writes completed"})
		}

		if writeDescriptionRepairActive && workspaceWriteResultCompleted(req, result) && normalizeRunnerPath(req.Path) == writeDescriptionRepairPath {
			writeDescriptionRepairActive = false
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = ""
		}

		if prompt, ok := r.scratchpadDeliverableFailedEvidenceNudge(req, result, collectedResults); ok {
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_failed_evidence_nudge", Content: prompt})
		}

		if checklist := r.requiredEditPathsProgressMessage(req, result, collectedResults); checklist != "" {
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: checklist})
			r.emitEvent(RunEvent{Role: "required_edit_paths_progress", Content: checklist})
		}

		if reviewRepairNoopPassAnswer != "" {
			closeout, _ := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req)
			r.emitEvent(RunEvent{Role: closeout.EventRole, Content: closeout.EventContent})

			return finish(reviewRepairNoopPassAnswer, nil, closeout.StopReason, false)
		}

		if reviewRepairNoopSubmitPlanCloseoutRequired(r.RuntimeBrief, req, result) {
			if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
				r.emitEvent(RunEvent{Role: closeout.EventRole, Content: closeout.EventContent})

				return finish(closeout.Answer, nil, closeout.StopReason, false)
			}

			answer, err := controller.completeWithoutMoreToolsWithReason(reviewRepairNoopSubmitPlanCloseoutPrompt(req, result))
			if err == nil {
				if gateResult, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(r.RuntimeBrief, answer); rejected {
					answer = reviewRepairNoopSubmitPlanDeterministicCloseout(r.RuntimeBrief, gateResult)
				}
			}

			return finish(answer, err, "review_repair_noop_plan_closeout", false)
		}

		if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(r.RuntimeBrief, req, result, collectedResults); ok {
			recorder.activeToolCall = activeToolCall

			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_source_satisfied_short_circuit",
				Content: "empty submit_plan encoded a source-satisfied terminal outcome; synthesizing closeout from required-path source evidence",
			})

			return finish(answer, nil, "plan_source_satisfied_short_circuit", false)
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			prompt := planThenExecuteSourceSatisfiedTerminalRequiredPrompt(r.RuntimeBrief, result)
			eventRole := "plan_then_execute_source_satisfied_terminal_required"

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
				if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(collectedResults); ok {
					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, rejected, rejectedAt)
				}

				eventRole = "plan_then_execute_anchor_refresh_required"
			}

			refreshPlanThenExecuteTerminalSurface("source_satisfied_terminal_required")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh || surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			} else {
				opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			}

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

			continue
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteCandidateSourceReadyResult(result) {
			prompt := r.planThenExecuteSubmitReadyTerminalPrompt(result.Error, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
			transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_ready_terminal_required", "candidate_source_ready")

			continue
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteSubmitPlanRejectedForCorrection(req, result) {
			recoveryState := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})

			if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
				replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&turnMessages, req, result)

				eventRole := "plan_then_execute_grounding_required"
				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteGroundingRequiredRecoveryPlan())

				if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
					rejectedAt := len(collectedResults) - 1

					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, result, rejectedAt)
					eventRole = "plan_then_execute_anchor_refresh_required"
				}

				refreshPlanThenExecuteTerminalSurface("submit_plan_grounding_rejected")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				rebuildTools()

				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

				continue
			}

			replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&turnMessages, req, result)

			rejectedAt := len(collectedResults) - 1
			refreshNote := planThenExecutePostSubmitRejectionRefreshNote(r.RuntimeBrief, collectedResults, result, rejectedAt)
			prompt := "The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(result) + planThenExecuteSubmitCorrectionOutcomeInstruction(r.RuntimeBrief, collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, result) + " " + refreshNote
			eventRole := "plan_then_execute_submit_plan_required"

			if planThenExecuteUnscopedEmptySubmitPlanRejected(r.RuntimeBrief, collectedResults) {
				prompt = RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())
				eventRole = "plan_then_execute_empty_submit_plan_recovery"
			} else if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
				prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, result, rejectedAt)
				eventRole = "plan_then_execute_anchor_refresh_required"
			} else if terminalSubmitPlanNativeTools() {
				prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, result)
			}

			refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

			continue
		}

		if !planThenExecuteReadinessNudged || !planThenExecuteSubmitOnlyReadinessNudged {
			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, collectedResults); nudge != "" {
				nextUnread := firstPlanThenExecuteUnreadRequiredPath(r.requiredEditCoveragePaths(), planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, collectedResults))
				if nextUnread != "" && nextUnread != planThenExecuteUnreadSourceNudgedFor {
					planThenExecuteUnreadSourceNudgedFor = nextUnread

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					opts = planThenExecuteUnreadRequiredSourceChatOptions(opts, r.RuntimeBrief, collectedResults)

					syncCurrentAllowedToolsFromOptions()
					r.emitEvent(RunEvent{Role: "plan_then_execute_unread_source_nudge", Content: nudge})
				}
			}

			if nudge := planThenExecuteReadyToSubmitNudge(r.RuntimeBrief, collectedResults); nudge != "" {
				submitOnlyReadiness := planThenExecuteTerminalSubmitPlanSurfaceReady(r.RuntimeBrief, collectedResults)
				if submitOnlyReadiness || !planThenExecuteReadinessNudged {
					planThenExecuteReadinessNudged = true

					if submitOnlyReadiness {
						planThenExecuteSubmitOnlyReadinessNudged = true
						refreshPlanThenExecuteTerminalSurface("readiness")

						if terminalSubmitPlanNativeTools() {
							nudge = planThenExecuteNativeSubmitPlanNudge(r.RuntimeBrief)
						}

						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})

						rebuildTools()

						opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
						r.emitEvent(RunEvent{Role: "plan_then_execute_readiness_nudge", Content: nudge})
					} else {
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						opts = forceVisibleActionChatOptions(opts)
						syncCurrentAllowedToolsFromOptions()
						r.emitEvent(RunEvent{Role: "plan_then_execute_candidate_readiness_nudge", Content: nudge})
					}
				}
			}
		}

		if !planThenExecuteCorrectionAfterRefreshNudged &&
			r.planThenExecutePlannerPhase() &&
			planThenExecuteReadinessNudged &&
			strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan &&
			successfulSubmitPlanResultCount(collectedResults) == 0 {

			recoveryState := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			if rejectedResult, _, rejected := lastRejectedSubmitPlanCoverageResult(collectedResults); rejected &&
				recoveryState.Mode != PlanExecutePlanningModeAnchorRefresh &&
				len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, collectedResults)) == 0 {

				planThenExecuteCorrectionAfterRefreshNudged = true

				prompt := "The bounded source refresh after the rejected `submit_plan` is complete. " + planThenExecuteSubmitCorrectionOutcomeInstruction(r.RuntimeBrief, collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejectedResult)

				refreshPlanThenExecuteTerminalSurface("submit_plan_after_refresh")

				if terminalSubmitPlanNativeTools() {
					prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, rejectedResult)
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				rebuildTools()

				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_after_refresh", Content: prompt})
			}
		}

		// Plan-then-execute short-circuit: once the planner emits a
		// successful submit_plan call, the next model turn would just
		// retype a summary already implicit in the plan artifact.
		// Synthesize the final answer from the tool result and exit
		// the runner instead of spending another ~4s of model time.
		if answer, ok := planThenExecuteShortCircuitAnswer(r.RuntimeBrief, req, result); ok {
			recorder.activeToolCall = activeToolCall

			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_short_circuit",
				Content: "submit_plan succeeded; synthesizing final answer from plan artifact instead of spending a second model turn",
			})

			return finish(answer, nil, "plan_submitted_short_circuit", false)
		}

		if !delegatedReadOnlyAnswerReady && !postChildPacketActive {
			r.attachInstructionsForTouchedPaths(&turnMessages, req, result)
		}

		childReadyBeforeResult := delegatedReadOnlyAnswerReady
		becameReadyFromDelegated := r.delegatedReadOnlyAnswerReadyAfterResult(req, result, collectedResults)
		becameReadyFromAnalysis := r.readOnlyAnalysisAnswerReadyAfterResult(req, result, collectedResults)

		if becameReadyFromDelegated || becameReadyFromAnalysis {
			justBecameReady := !delegatedReadOnlyAnswerReady

			delegatedReadOnlyAnswerReady = true
			r.RuntimeBrief.Constraints = mergeRunnerStateStrings(r.RuntimeBrief.Constraints, []string{"answer_readiness_reached"})

			if becameReadyFromDelegated {
				r.emitDelegatedReadOnlyAnswerReadyEvent(req, collectedResults)
			} else {
				r.emitReadOnlyAnswerReadyEvent(req, collectedResults)
			}

			if !delegatedReadOnlyAnswerReadyNudged {
				delegatedReadOnlyAnswerReadyNudged = true

				nudge := delegatedReadOnlyAnswerReadyNudge(r.RuntimeBrief.Objective, collectedResults, postReadinessProbeBudget)
				eventRole := "delegated_read_only_answer_ready_nudge"

				if becameReadyFromAnalysis {
					nudge = readOnlyAnswerReadyNudge(r.RuntimeBrief, collectedResults, postReadinessProbeBudget)
					eventRole = "read_only_answer_ready_nudge"
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				r.emitEvent(RunEvent{Role: eventRole, Content: nudge})
			}

			// Rebuild tools so readiness state can affect explicit
			// mutation/source-refresh contracts. Read-only readiness is
			// advisory and must not hide or reshape discovery tools.
			if justBecameReady {
				rebuildTools()
			}

		}

		// Conversation lane: once source evidence is available, the
		// generic post-readiness contract above owns further probes.
		// Keep this inspected-path note only for lanes that have not
		// entered answer readiness.
		if r.conversationEvidenceAvailableForObjective(collectedResults) && !conversationEvidenceNudged && !delegatedReadOnlyAnswerReady {
			conversationEvidenceNudged = true

			inspected := conversationInspectedPathSummary(collectedResults)

			if inspected != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "Inspected paths so far: " + inspected + ". Answer if the evidence supports the requested claim; otherwise continue only with one exact missing claim and one exact probe that would resolve it.",
				})
			}
		}

		// Conversation lane: auto-capture findings at the first context-derived
		// milestone so discoveries survive context compactions.
		if r.lanePolicy().AutoCaptureFindings() &&
			metrics.ToolCalls == convFirstMilestone && strings.TrimSpace(result.Error) == "" && r.Workspace != nil {

			if _, err := writeAutoFindingsNote(r.Workspace.Root(), r.RuntimeBrief, collectedResults); err == nil {
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The harness has captured your findings so far to .idiom/notes/auto-findings.md. This note persists across context compactions — review it if your working memory is refreshed. Continue investigating, or answer if you have enough evidence.",
				})
			} else {
				r.emitEvent(RunEvent{Role: "note_tool", Content: "Auto findings note rejected: " + err.Error()})
			}
		}

		if childReadyBeforeResult && !postChildPacketActive && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			delegatedReadOnlyPostReadyFollowups++
			// Diminishing-returns nudge stays as a soft signal; the hard
			// budget force-finish was a bandaid (per the no-bandaid
			// memory) and got removed. MaxToolSteps + doom_loop_guard
			// catch pathological loops; the completion gate enforces
			// mutation closure.
			if delegatedReadOnlyPostReadyFollowups > delegatedReadOnlyDiminishingNudgeStage {
				if nudge := delegatedReadOnlyDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, delegatedReadOnlyPostReadyFollowups); nudge != "" {
					delegatedReadOnlyDiminishingNudgeStage = delegatedReadOnlyPostReadyFollowups

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})

					role := "read_only_diminishing_returns_nudge"

					if strings.HasPrefix(r.agentName(), "child:") {
						role = "delegated_read_only_diminishing_returns_nudge"
					}

					r.emitEvent(RunEvent{Role: role, Content: nudge})
				}
			}
		}

		if postChildPacketActive && strings.TrimSpace(req.Tool) != workspace.ToolNameTask && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			postChildFollowupReads++
			if postChildFollowupReads > postChildDiminishingNudgeStage {
				if nudge := postChildDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, postChildFollowupReads); nudge != "" {
					postChildDiminishingNudgeStage = postChildFollowupReads

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_diminishing_returns_nudge", Content: nudge})
				}
			}

		}

		if strings.TrimSpace(result.Error) != "" {
			// Track same-stem tool failures so we can inject a recovery
			// nudge before burning the whole budget on hallucinated paths
			// — and, if the nudge already fired once this turn and the
			// streak starts up again, terminate the turn rather than let
			// the model keep churning (observed in live runs: 60+
			// tool calls on a single doc-edit task with only one nudge
			// fired and no escape).
			stem := toolFailurePathStem(req)
			if executionState.noteToolFailure(req, result) >= 3 {
				if !executionState.softFailureNudgeUsed {
					if nudge := buildRejectionRecoveryNudge(req, collectedResults, r.Workspace, r.RuntimeBrief, r.agentName(), delegatedReadOnlyAnswerReady, postChildPacketActive); nudge != "" {
						executionState.softFailureNudgeUsed = true

						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})

						executionState.consecutiveToolFailures = 0
					}
				} else {
					// Soft nudge already fired. Another 3-failure streak
					// on the same stem means the model isn't absorbing
					// the recovery hint. Terminate with what we have.
					answer, err := controller.completeWithoutMoreToolsWithReason(fmt.Sprintf("Three more %s failures on %s after the recovery nudge. Tools are now disabled. Write the normal final answer from the evidence already gathered. If those failures leave one exact claim unsupported, state that uncertainty after the supported answer.", req.Tool, stem))
					return finish(answer, err, "tool_failure_streak_exhausted", true)
				}
			}
		} else {
			executionState.noteToolFailure(req, result)
		}

		if executionState.noteMissingPathListFailure(r.Workspace.IsExploreMode(), req, result) >= 2 {
			answer, err := controller.completeWithoutMoreToolsWithReason("Exploration is probing conventional paths that do not exist in this repository. Tools are now disabled. Answer from the bootstrapped evidence; if one exact claim remains unsupported, state that uncertainty after the supported answer.")
			return finish(answer, err, "missing_path_probe_budget_exhausted", true)
		}

		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
			return finish(answer, err, "no_progress_budget_exhausted", true)
		}
	}

	answer, err = controller.completeWithoutMoreToolsWithReason(maxToolStepsExhaustedReason())

	return finish(answer, err, "tool_step_budget_exhausted", true)
}

func (r *Runner) planExecutePlanningTransportFromOptions(opts llama.ChatOptions, supportsNativeTools bool, terminalOnly bool) PlanExecutePlanningTransport {
	return PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, terminalOnly).PlanningTransport()
}

func (r *Runner) planThenExecuteCompletionGateSummaryRetryable(summary string, results []workspace.ToolResult, priorGateRejections int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || priorGateRejections >= r.completionGateRetryLimit(results) {
		return false
	}

	switch strings.TrimSpace(summary) {
	case "Plan-execute completion gate rejected final answer",
		"Completion gate rejected non-specific submit_plan blocker",
		"Completion gate rejected non-submit_plan correction":
		return true
	default:
		return false
	}
}

func maxToolStepsExhaustedReason() string {
	return "Maximum tool steps for this turn have been reached. Tools are now disabled. Respond with a concise, direct, text-only final answer synthesized from the evidence already gathered. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action. Do not leave the response blank."
}
