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
	contractcompile "idiom/internal/contracts/compile"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	compactionevidence "idiom/internal/evidence/compaction"
	"idiom/internal/evidence/trace"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/instructions"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func (r *Runner) Run(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	onStatus func(string),
	onToolRequest func(ToolRequestEvent),
	onToolResult func(ToolResultEvent),
) (string, error) {
	if r == nil {
		return "", fmt.Errorf("runner is nil")
	}
	if r.Client == nil {
		return "", fmt.Errorf("runner client is required")
	}
	runRunner := *r
	if r.Trace != nil {
		tracedEvent := r.OnEvent
		trun := r.Trace
		runRunner.OnEvent = func(event RunEvent) {
			trun.RecordRunEvent(event.Role, event.Content)
			if tracedEvent != nil {
				tracedEvent(event)
			}
		}
		tracedRequest := onToolRequest
		onToolRequest = func(event ToolRequestEvent) {
			requestFingerprint := workspace.ToolRequestFingerprint(event.Request)
			trun.RecordToolRequest(event.Request.Tool, requestFingerprint)
			if tracedRequest != nil {
				tracedRequest(event)
			}
		}
		tracedResult := onToolResult
		onToolResult = func(event ToolResultEvent) {
			requestFingerprint := workspace.ToolRequestFingerprint(event.Request)
			status := trace.StatusCompleted
			if strings.TrimSpace(event.Result.Error) != "" {
				status = trace.StatusFailed
			}
			trun.RecordToolResult(requestFingerprint, status, map[string]any{
				"tool":  event.Request.Tool,
				"error": event.Result.Error != "",
			})
			if tracedResult != nil {
				tracedResult(event)
			}
		}
	}
	run := newRunExecution(&runRunner, ctx, messages, opts)
	answer, err := run.runSessionLoop(onStatus, onToolRequest, onToolResult)
	if r.OnAssistantMessage != nil {
		r.OnAssistantMessage(run.state.lastAssistantMessage)
	}

	return answer, err
}

func newRunExecution(r *Runner, ctx context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) *runExecution {
	lanePolicy := r.LanePolicy
	if lanePolicy == nil {
		lanePolicy = LanePolicyForBrief(r.RuntimeBrief)
	}
	lanePolicySnapshot := *lanePolicy
	baseRuntimeBrief := cloneRunRuntimeBrief(r.RuntimeBrief)

	run := &runExecution{
		runner: r,
		state: runnerRunState{
			ctx:                          ctx,
			runtimeBrief:                 baseRuntimeBrief,
			opts:                         cloneRunChatOptions(opts),
			collectedResults:             append([]workspace.ToolResult(nil), r.SeedResults...),
			turnMessages:                 append([]llama.ChatMessage(nil), messages...),
			executionState:               newRunnerExecutionState(r.InitialNeedsNarrowing, r.InitialBroadInspects),
			delegatedReadOnlyAnswerReady: slices.Contains(baseRuntimeBrief.Constraints, "answer_readiness_reached"),
			lifecycle: runnerRunLifecycleState{
				progress: runnerProgressLifecycleState{
					replayedToolRequests: make(map[string]int),
					rejectedActions:      make(map[string]int),
				},
			},
			metrics:    runnerMetrics{startedAt: time.Now()},
			stopReason: "completed",
		},
		lanePolicySnapshot: lanePolicySnapshot,
	}
	run.state.bindWorkspaceInvocationAuthority(r)
	if r.Trace != nil {
		run.state.ctx = workspace.WithToolExecutionObserver(run.state.ctx, func(envelope workspace.ToolExecutionEnvelope) {
			r.Trace.RecordToolPolicy(envelope.RequestFingerprint, string(envelope.PreflightDecision), trace.StatusObserved)
			if envelope.EvidenceProduced {
				r.Trace.RecordToolEvidence(envelope.RequestFingerprint, map[string]any{
					"evidence_paths": len(envelope.EvidencePaths),
				})
			}
			if envelope.VerificationStatus != "unverified" {
				r.Trace.RecordToolVerification(envelope.RequestFingerprint, envelope.VerificationStatus)
			}
		})
	}
	if r.SeedContextCompactionProvenance != nil && !r.SeedContextCompactionProvenance.Acceptable() {
		run.state.initializationErr = fmt.Errorf("context compaction provenance is not accepted for resume")
	}
	if r.Workspace != nil {
		run.state.sourceWindowCurrent = func(evidence toolfacts.SourceWindowEvidence) bool {
			current, err := r.Workspace.SourceWindowCurrent(run.state.ctx, evidence)
			return err == nil && current
		}
	}
	if err := run.state.bindMutationScope(r.Workspace); err != nil {
		run.state.initializationErr = err
		return run
	}
	if err := run.state.restoreDurableMutationState(r.Workspace, r.SeedImplementationState); err != nil {
		run.state.initializationErr = err
		return run
	}
	run.state.policy = run.effectivePolicy()
	run.state.lifecycle.closure.conversationFirstMilestone,
		run.state.lifecycle.closure.conversationNudgeInterval = lanePolicyCallCountMilestones(run.lanePolicy(), r.ContextSize)
	return run
}

// runExecution owns one Runner.Run invocation. runner supplies shared,
// read-only configuration and dependencies; state is a complete run-local
// value initialized before any execution helper can observe it.
type runExecution struct {
	runner             *Runner
	state              runnerRunState
	lanePolicySnapshot LanePolicy
	taskLedger         *taskLedgerState
}

func cloneRunChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.Tools = append([]llama.Tool(nil), opts.Tools...)
	for index := range opts.Tools {
		opts.Tools[index].Function.Parameters = append(json.RawMessage(nil), opts.Tools[index].Function.Parameters...)
	}

	return opts
}

func cloneRunRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief.TargetRefs = append([]string(nil), brief.TargetRefs...)
	brief.RequiredEditPaths = append([]string(nil), brief.RequiredEditPaths...)
	brief.AllowedEditPaths = append([]string(nil), brief.AllowedEditPaths...)
	brief.AuthorizedNewFiles = append([]string(nil), brief.AuthorizedNewFiles...)
	brief.RequiredEditSnippets = slices.Clone(brief.RequiredEditSnippets)
	brief.EvidencePaths = append([]string(nil), brief.EvidencePaths...)
	brief.ChangedPaths = append([]string(nil), brief.ChangedPaths...)
	brief.UserBoundaries = append([]string(nil), brief.UserBoundaries...)
	brief.AllowedTools = append([]string(nil), brief.AllowedTools...)
	brief.ForbiddenTools = append([]string(nil), brief.ForbiddenTools...)
	brief.ToolManifest = append([]workspace.ToolManifestEntry(nil), brief.ToolManifest...)
	for index := range brief.ToolManifest {
		brief.ToolManifest[index].EvidenceCapabilities = append([]string(nil), brief.ToolManifest[index].EvidenceCapabilities...)
	}
	brief.EvidenceCapabilities = append([]string(nil), brief.EvidenceCapabilities...)
	brief.RequiredEvidence = append([]string(nil), brief.RequiredEvidence...)
	brief.RequiredTools = append([]string(nil), brief.RequiredTools...)
	brief.ReadinessGate = append([]string(nil), brief.ReadinessGate...)
	brief.SearchPolicy = append([]string(nil), brief.SearchPolicy...)
	brief.DiscoveryExamples = append([]string(nil), brief.DiscoveryExamples...)
	brief.VerificationPlan = append([]string(nil), brief.VerificationPlan...)
	brief.StopConditions = append([]string(nil), brief.StopConditions...)
	brief.FinalCritic = append([]string(nil), brief.FinalCritic...)
	brief.TargetPacket = append([]string(nil), brief.TargetPacket...)
	brief.Constraints = append([]string(nil), brief.Constraints...)
	brief.RequiredNext = append([]string(nil), brief.RequiredNext...)
	brief.InvalidActions = append([]string(nil), brief.InvalidActions...)
	if brief.Readiness != nil {
		readiness := *brief.Readiness
		readiness.ClosedClaims = append([]string(nil), readiness.ClosedClaims...)
		readiness.OpenObligations = append([]string(nil), readiness.OpenObligations...)
		readiness.EvidenceSources = append([]string(nil), readiness.EvidenceSources...)
		brief.Readiness = &readiness
	}

	return brief
}

func (r *runExecution) presentableFallbackCandidate(results []workspace.ToolResult, answer string) (string, bool) {
	if r == nil {
		return presentableFallbackCandidate("", workerruntime.WorkerRuntimeBrief{}, results, answer)
	}
	if runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return strings.TrimSpace(answer), strings.TrimSpace(answer) != ""
	}
	return readOnlyClosureDecisionForResults(r.state.runtimeBrief, results).PresentableFallbackCandidate(r.agentName(), answer)
}

func (r *runExecution) agentName() string {
	if strings.TrimSpace(r.runner.AgentName) == "" {
		return "main"
	}

	return r.runner.AgentName
}

func (r *runExecution) sessionID(ctx context.Context) string {
	if r == nil {
		return ""
	}
	if sessionID, bound := workspace.ExecutionSessionIDFromContext(ctx); bound {
		return sessionID
	}
	if r.runner == nil || r.runner.Workspace == nil {
		return ""
	}

	return r.runner.Workspace.SessionID()
}

func (r *runExecution) workspaceAllowedTools() []string {
	if r == nil || r.runner.Workspace == nil {
		return nil
	}
	return r.workspaceToolSurfaceSnapshot().AllowedTools()
}

func (r *runExecution) workspaceAdvertisedAllowedTools() []string {
	if r == nil {
		return nil
	}
	return r.advertisedAllowedTools(r.workspaceAllowedTools())
}

func (r *runExecution) workspaceToolSurfaceSnapshot() workspace.ToolSurfaceSnapshot {
	if r == nil || r.runner.Workspace == nil {
		return workspace.ToolSurfaceSnapshot{}
	}

	if profile, ok := workerruntime.WorkspaceToolSurfaceOverrideProfileForTaskFamily(r.state.runtimeBrief.TaskFamily); ok {
		return r.runner.Workspace.ToolSurfaceSnapshotForProfile(profile)
	}

	return r.runner.Workspace.ToolSurfaceSnapshot()
}

// permissionAuthorizedInspection classifies already-authorized inspection
// capabilities against the immutable workspace surface, not a phase profile.
// Phase profiles govern mutation and terminal actions; they must not erase a
// request-level read-only capability before the workspace can classify it.
func (r *runExecution) permissionAuthorizedInspection(authorized []string) workspace.PermissionAuthorizedInspectionProjection {
	if r == nil || r.runner.Workspace == nil {
		return workspace.PermissionAuthorizedInspectionProjection{}
	}

	surface := r.runner.Workspace.ToolSurfaceSnapshot()
	return surface.PermissionAuthorizedInspection(textutil.Merge(authorized, r.state.runtimeBrief.AllowedTools))
}

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
	// SeedImplementationState restores durable mutation receipts for a resumed
	// run. Live authorization is always rebound from RuntimeBrief and compared
	// with this projection before any execution helper observes the run.
	SeedImplementationState *recoverycontract.ImplementationState
	// SeedContextCompactionProvenance is the latest accepted compaction record
	// supplied by a resumable caller. Rejected provenance fails closed before
	// the resumed run can issue a model request.
	SeedContextCompactionProvenance *compactionevidence.Provenance
	PreExecute                      func(step int, req workspace.ToolRequest) *workspace.ToolResult
	CompletionGate                  CompletionGate
	TaskLedger                      TaskLedgerPolicy
	PermissionPolicy                toolpolicy.Policy
	CheckpointPolicy                CheckpointPolicy
	// RuntimeBrief is immutable input configuration. Each Run copies it into
	// runnerRunState before deriving live objective, evidence, and readiness.
	RuntimeBrief workerruntime.WorkerRuntimeBrief
	OnEvent      func(RunEvent)
	// Trace receives metadata-only invocation spans. It is optional so callers
	// that do not request diagnostics pay no trace allocation cost.
	Trace     *trace.Recorder
	OnThought func(ThoughtEvent)
	// ContextSize is the configured llama.cpp context window in tokens.
	// Surfaced in model_call telemetry so log analysis can compute
	// ctx_pct (prompt_tokens / ctx_size) without having to reconcile
	// the running config separately. Zero means unknown.
	ContextSize     int
	DisableTaskTool bool
	// LanePolicy is the lane-specific behavioral policy derived from
	// RuntimeBrief at runner construction time. The policy replaces
	// inline TaskFamily/Contract string-comparison guards with a
	// single dispatch point. When nil, falls back to the default lane policy.
	LanePolicy *LanePolicy

	// InstructionAttacher injects configured per-directory guidance
	// whenever the model touches a file under that directory.
	InstructionAttacher *instructions.Attacher
	// DoomLoopGate, if non-nil, is called when the model emits three identical
	// consecutive tool calls. Returning true approves the call (resets the
	// streak and lets execution continue); returning false rejects it and
	// terminates the turn. When nil the runner falls back to a hard rejection.
	DoomLoopGate func(req workspace.ToolRequest, count int) bool
	// OnAssistantMessage receives the final raw assistant message for this
	// invocation. Keeping the observation in a callback avoids mutable output
	// state on Runner when callers reuse it for sequential runs.
	OnAssistantMessage func(llama.ChatMessage)
	// ReadApprovalGate, when wired, lets the operator approve or deny a
	// read that the post-readiness candidate-scope rejection would
	// otherwise block. Only the "Plan-execute read outside allowed
	// candidate scope" rejection is overridable; all other rejections
	// enforce real invariants and pass through unchanged.
	ReadApprovalGate *ReadApprovalGate
}

func (r *runExecution) supportsNativeTools() bool {
	return r == nil || r.runner.Client == nil || r.runner.Client.SupportsNativeTools()
}

// lanePolicy returns the policy snapshot selected when this run was created.
func (r *runExecution) lanePolicy() *LanePolicy {
	if r != nil {
		return &r.lanePolicySnapshot
	}

	return &defaultLanePolicy
}

func (r *runExecution) supportsTerminalSubmitPlanNativeTools(results []workspace.ToolResult) bool {
	if r == nil || r.runner.Client == nil {
		return true
	}

	state := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
	if r.planThenExecutePlannerPhase() && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return false
	}

	return r.runner.Client.SupportsNativeTools()
}

func (r *runExecution) runSessionLoop(
	onStatus func(string),
	onToolRequest func(ToolRequestEvent),
	onToolResult func(ToolResultEvent),
) (answer string, err error) {
	runState := &r.state
	if runState.initializationErr != nil {
		return "", runState.initializationErr
	}
	ctx := runState.ctx

	if r.runner.Workspace != nil {
		r.rebuildRunTools()
	}

	if strings.TrimSpace(runState.opts.ToolChoice) == "" && len(runState.opts.Tools) > 0 {
		runState.opts.ToolChoice = "auto"
	}

	defer func() {
		if runState.autonomyRecorder != nil {
			runState.autonomyRecorder.emitLoopCloseout(runState.stopReason)
			runState.autonomyRecorder.emitBudgetGate(runState.policy, runState.metrics, runState.stopReason, runState.budgetExhausted)
		}

		r.emitBudgetLedger(runState.policy, runState.metrics, runState.stopReason, runState.budgetExhausted)
	}()

	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		value, finishErr = r.finish(value, finishErr, reason, exhausted)
		if finishErr == nil &&
			strings.TrimSpace(value) != "" &&
			strings.TrimSpace(value) != strings.TrimSpace(r.state.lastAssistantMessage.Content) &&
			(strings.TrimSpace(r.state.lastAssistantMessage.Role) != "" || strings.TrimSpace(r.state.lastAssistantMessage.Content) != "") {

			r.state.lastAssistantMessage.Content = value
		}

		return value, finishErr
	}

	// activeToolCall points at the native tool_call we're currently
	// responding to (when the model sent one). Turn helpers consult it to
	// emit role:"tool" + tool_call_id responses instead of
	// role:"system" text. Reset to nil at the top of every iteration.
	var activeToolCall *llama.ToolCall

	declarationMovesForbidden := declarationMovesForbiddenByMessages(runState.turnMessages)

	taskLedgerPolicy := r.runner.TaskLedger
	if runnerBriefRequiresMutation(r.state.runtimeBrief) && !strings.HasPrefix(r.agentName(), "child_write:") {
		taskLedgerPolicy.RequireEvidence = true
	}

	taskLedger := newTaskLedgerState(taskLedgerPolicy, r.runner.Workspace)
	r.taskLedger = taskLedger

	r.state.runtimeBrief = r.runtimeBriefWithLiveState(runState.policy, runState.turnMessages, runState.collectedResults)

	if r.runner.Workspace != nil {
		r.rebuildRunTools()
	}

	visibleRuntimeBrief := r.visibleBriefForToolSurface(r.state.runtimeBrief, runState.collectedResults)

	runState.autonomyRecorder = newRunnerAutonomyRecorder(r, runState.policy, visibleRuntimeBrief, runState.collectedResults)

	if runState.autonomyRecorder != nil {
		runState.autonomyRecorder.emitContractStarted(visibleRuntimeBrief)
	}

	if initialBrief := workerruntime.RenderInitialRuntimeBrief(visibleRuntimeBrief); strings.TrimSpace(initialBrief) != "" {
		runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: initialBrief})
	}

	if protocol := r.textualToolProtocolPrompt(visibleRuntimeBrief); strings.TrimSpace(protocol) != "" {
		runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: protocol})
	}

	if bootstrap := r.initialBootstrapInstruction(); strings.TrimSpace(bootstrap) != "" {
		runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: bootstrap})
	}

	// Investigation lane: recommended strategy to avoid exhaustive
	// symbol-by-symbol enumeration. One-shot at session start.
	if lanePolicyHasName(r.lanePolicy(), lanePolicyReadOnlyInvestigation) {
		runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: readOnlyInvestigationStrategyPrompt(visibleRuntimeBrief.AllowedTools)})
	}

	r.emitControlRailsForBrief(runState.turnMessages, visibleRuntimeBrief)
	r.emitBriefAudit(visibleRuntimeBrief, runState.collectedResults)

	requestBudget := newRunnerRequestBudgetState(postChildReadBudgetForBrief(r.state.runtimeBrief))
	recorder := runnerTurnRecorder{
		runner:                    r,
		onToolResult:              onToolResult,
		declarationMovesForbidden: declarationMovesForbidden,
	}

	runState.collectedResults = r.maybeApplyReadOnlyNamedPathPrefetch(recorder, runState.collectedResults)

	controller := runnerTurnController{
		runner:        r,
		requestBudget: &requestBudget,
		recorder:      &recorder,
	}
	finishNoProgressBudgetExhausted := func(results []workspace.ToolResult) (string, error) {
		if blocker, ok := r.implementationContractCloseoutBlocker(results, "no_progress_budget_exhausted"); ok {
			return finish(blocker, nil, "no_progress_budget_exhausted", false)
		}

		answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
		return finish(answer, err, "no_progress_budget_exhausted", false)
	}
	if answer, ok := r.runVerificationBootstrap(runnerVerificationBootstrapInput{
		collectedResults: runState.collectedResults,
		metrics:          &runState.metrics,
		recorder:         &recorder,
		executionState:   &runState.executionState,
		requestBudget:    &requestBudget,
		autonomyRecorder: runState.autonomyRecorder,
		onToolRequest:    onToolRequest,
	}); ok {
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; runState.policy.unbounded() || step < runState.policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		readiness := modelCallReadinessBeforeModelCall(ModelCallReadinessPolicyInput{
			Policy:  runState.policy,
			Metrics: runState.metrics,
		})
		if readiness.Stop {
			if blocker, ok := r.implementationContractCloseoutBlocker(runState.collectedResults, readiness.StopReason); ok {
				return finish(blocker, nil, readiness.StopReason, readiness.BudgetExhausted)
			}
			if strings.TrimSpace(readiness.CloseoutPrompt) != "" {
				answer, err := controller.completeWithoutMoreToolsWithReasonMode(readiness.CloseoutPrompt, readiness.CloseoutMode)
				return finish(answer, err, readiness.StopReason, readiness.BudgetExhausted)
			}
			return finish("", nil, readiness.StopReason, readiness.BudgetExhausted)
		}

		modelCallOpts := readOnlyEvidenceAvailableTerminalChatOptions(runState.opts, r.state.runtimeBrief, runState.collectedResults)
		runState.currentTransport = PlanExecuteRuntimeCallPolicyFromChatOptions(modelCallOpts, r.terminalSubmitPlanNativeTools(), false).PlanningTransport()
		if r.compactTurnMessagesIfNeeded(&runState.turnMessages, runState.policy, taskLedger, runState.collectedResults) {
			runState.metrics.Compactions++
		}

		if onStatus != nil && step > 0 {
			if runState.policy.unbounded() {
				onStatus(fmt.Sprintf("Idiom is still working (%d steps).", stepNumber))
			} else {
				onStatus(fmt.Sprintf("Idiom is still working (%d/%d).", stepNumber, runState.policy.MaxToolSteps))
			}
		}

		modelCallNumber := runState.metrics.ModelCalls + 1
		var phaseHint contractcompile.TurnPhaseID

		contractID := ""
		if runState.autonomyRecorder != nil {
			contractID = runState.autonomyRecorder.activeAuthorityContractID()
		}

		phaseContract := r.deriveCurrentTurnPhaseContract(runnerTurnPhaseContractInput{
			ContractID:          contractID,
			ModelCallNumber:     modelCallNumber,
			Step:                stepNumber,
			PhaseHint:           phaseHint,
			Brief:               r.state.runtimeBrief,
			Results:             runState.collectedResults,
			AllowedTools:        runState.currentAllowedTools,
			ActiveSurface:       runState.toolSurface,
			Options:             modelCallOpts,
			SupportsNativeTools: r.terminalSubmitPlanNativeTools(),
		})
		modelProtocol, hasModelProtocol := r.compileModelProtocolProjection(r.state.runtimeBrief, runState.collectedResults, phaseContract, runState.policy, r.terminalSubmitPlanNativeTools())

		if runState.autonomyRecorder != nil {
			modelProtocol = runState.autonomyRecorder.bindModelProtocolProjection(modelProtocol)
			runState.autonomyRecorder.emitTurnPhaseContract(phaseContract, modelProtocol)
		}

		modelStart := time.Now()

		requestMessages := runState.turnMessages

		if hasModelProtocol {
			requestMessages = modelMessagesWithProtocolProjection(requestMessages, modelProtocol)
			if runState.autonomyRecorder != nil {
				runState.autonomyRecorder.markModelProtocolPresented(modelProtocol)
			}
		}

		requestMessages = r.chatMessagesForOptions(requestMessages, modelCallOpts)

		assistantMessage, err := r.runner.Client.CompleteChatMessage(ctx, requestMessages, modelCallOpts)
		if err != nil {
			if answer, ok := r.modelErrorAfterPartialEditBlockerAnswer(err, runState.collectedResults); ok {
				r.emitStateCompletionStatusFromParams("blocked", "model_error_after_partial_edit", "model_error_after_partial_edit", runState.collectedResults, true)

				return finish(answer, nil, "model_error_after_partial_edit", false)
			}

			if runState.lifecycle.progress.transportRetries < 1 && retryableModelTransportError(err) {
				runState.lifecycle.progress.transportRetries++

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The previous model request was interrupted by a temporary connection issue. Continue from where you left off, repeating any incomplete tool call or thought.",
				})
				r.emitEvent(RunEvent{Role: "transport_retry", Content: fmt.Sprintf("transport error, retry %d/1: %s", runState.lifecycle.progress.transportRetries, strings.ToLower(err.Error()))})

				continue
			}

			if !runState.lifecycle.progress.modelCapacityRetryUsed && modelCapacityFailure(err) {
				runState.lifecycle.progress.modelCapacityRetryUsed = true
				runState.opts = modelCapacityRecoveryOptions(runState.opts)
				runState.turnMessages = llama.PrepareChatMessagesForBackend(
					runState.turnMessages,
					runState.opts,
					r.clientBackend(),
				)
				r.emitEvent(RunEvent{Role: "model_capacity_retry", Content: "model allocation failed; retrying with preserved reasoning disabled and an adaptive response budget"})

				continue
			}

			if prompt, ok := r.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(err, runState.opts, runState.collectedResults, runState.lifecycle.planExecute.nativeSubmitPlanNoCallFallbacks); ok {
				runState.lifecycle.planExecute.nativeSubmitPlanNoCallFallbacks++

				runState.planThenExecuteNativeSubmitPlanDisabled = true
				runState.opts = planThenExecuteTextualSubmitPlanFallbackChatOptions(runState.opts, r.state.runtimeBrief, runState.collectedResults)

				r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&runState.turnMessages, runState.collectedResults, "native_required_no_tool_call")

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

				continue
			}

			return finish("", err, "model_error", false)
		}

		runState.metrics.ModelCalls++
		runState.metrics.addUsage(assistantMessage.Usage)
		r.emitModelCallTraceWithOptions(stepNumber, "main_loop", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), modelCallOpts, r.terminalSubmitPlanNativeTools(), runState.collectedResults)

		reply := assistantMessage.Content
		// Capture the full message (including ReasoningContent) so the
		// auto-salvage path can recover review prose that landed inside
		// a runaway <think> block instead of visible content (#95).
		r.state.lastAssistantMessage = assistantMessage
		r.emitThinkingTrace(stepNumber, "model_reply", assistantMessage, modelCallOpts, false)

		activeToolCall = nil

		var (
			req      workspace.ToolRequest
			raw      string
			ok       bool
			parseErr error
		)

		normalization := normalizeToolRequestSyntax(assistantMessage)
		normalizedPolicyInput := runnerAutonomyPolicyInputFromNormalization(normalization)

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
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, runState.collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "visible_prose_salvage", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in prose; executing the request", req.Tool)})
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageToolRequestFromReasoning(assistantMessage, runState.collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "reasoning_salvage", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "reasoning_tool_salvage", Content: fmt.Sprintf("model emitted parser-readable %s request in hidden reasoning while visible content was empty; executing the request", req.Tool)})
			} else if answerText, salvaged := r.salvageAnswerFromReasoning(assistantMessage, runState.collectedResults); salvaged {
				r.emitEvent(RunEvent{Role: "reasoning_answer_salvage", Content: fmt.Sprintf("model emitted answer prose in hidden reasoning while visible content was empty; extracted %d chars", len(answerText))})

				return finish(answerText, nil, "reasoning_answer_salvage", false)
			}

			if !ok &&
				normalization.EventOutcome == toolRequestNormalizerOutcomeUnsafeToInfer &&
				r.unsafeToolShapeRecoveryBeforeCompletionGateActive(runState.collectedResults) {
				r.rebuildRunTools()
				if normalization.UnsafeExplicitTool {
					controller.recordUnsafeToolShape(stepNumber, activeToolCall, normalization, activeToolCall == nil || r.supportsNativeTools())
					if prompt := r.unsafeToolShapeRecoveryPrompt(normalization, runState.collectedResults); prompt != "" {
						runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
						r.emitEvent(RunEvent{Role: "unsafe_tool_shape_recovery", Content: prompt})
					}
					continue
				}
				if prompt := r.ambiguousModelOutputRecoveryPrompt(normalization, runState.collectedResults); prompt != "" {
					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "ambiguous_model_output_recovery", Content: prompt})
				}
				continue
			}
		}

		if parseErr != nil {
			if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, runState.collectedResults); salvaged {
				req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
				activeToolCall = nil

				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool text; executing the safe request before recording tool_parse", req.Tool)})
			} else if raw != reply {
				if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(raw, runState.collectedResults); salvaged {
					req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
					activeToolCall = nil

					r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool arguments; executing the safe request before recording tool_parse", req.Tool)})
				}
			}
		}

		err = parseErr
		if err != nil {
			var assistantToolTurn llama.ChatMessage
			suppressParseFailureModelMessage := false

			if activeToolCall != nil {
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
			} else if !r.supportsNativeTools() {
				assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, workspace.ToolRequest{Tool: parserRecoveryToolName}, raw)
				activeToolCall = nil
			} else {
				suppressParseFailureModelMessage = true
			}

			if strings.TrimSpace(assistantToolTurn.Role) != "" {
				assistantToolTurn = r.assistantHistoryMessageForOptions(assistantToolTurn, runState.opts)
				runState.turnMessages = append(runState.turnMessages, assistantToolTurn)
				r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, runState.opts, true)
			}
			controller.recordParseFailure(stepNumber, activeToolCall, raw, err, normalization.EventTransport, normalization.ParserFailureClass, suppressParseFailureModelMessage)

			if recoveryPlan, ok := r.planThenExecuteSubmitPlanParseRecoveryPlan(raw, err, runState.collectedResults); ok {
				prompt := RenderPlanExecuteRecoveryPlan(recoveryPlan)

				if len(r.remainingRequiredEditPaths(runState.collectedResults)) > 0 {
					runState.requiredPathCompletionRecoveryActive = true
				}

				r.rebuildRunTools()

				runState.opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools())
				runState.opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools())

				r.syncPlanThenExecuteRecoveryAllowedTools()
				r.refreshPlanThenExecuteTerminalSurface("parse_recovery")

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitPlanExecuteSubmitPlanParseRecoveryEvent(recoveryPlan, prompt)
			} else {
				if prompt := r.malformedToolRecoveryPrompt(raw, err, runState.collectedResults); prompt != "" {
					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "malformed_tool_recovery", Content: prompt})
				}
				r.emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw, err)
			}

			continue
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
				if acceptance := r.evaluateFinalAnswerAcceptance(answerContent, runState.collectedResults); acceptance.BlockerKind != "" {
					blockerKind := acceptance.BlockerKind
					reason := acceptance.Reason
					blockerResult, ok := finalAnswerCompletionGateProjection(
						acceptance,
						"Answer tool rejected by blocker check",
						r.finalAnswerBlockerErrorText(blockerKind, reason, answerContent, runState.collectedResults),
					)
					if !ok {
						return finish(answerContent, nil, "model_answer_tool", false)
					}

					if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
						stepNumber:       stepNumber,
						activeToolCall:   activeToolCall,
						assistantMessage: assistantMessage,
						acceptance:       acceptance,
						result:           blockerResult,
						controller:       &controller,
					}) {
						continue
					}

					if answer, ok := r.deterministicNoToolsFallback(noToolsFallbackAfterRejectedModelCloseout, runState.collectedResults); ok &&
						!readOnlyAnswerReportsInsufficientEvidence(answer) {

						return finish(answer, nil, "deterministic_final_answer_recovery", false)
					}

					return finish(answerContent, nil, blockerKind, true)
				}

				return finish(answerContent, nil, "model_answer_tool", false)
			}
		}

		if !ok {
			result, rejected := workspace.ToolResult{}, false
			result, rejected = r.requiredSourcePrerequisiteCompletionRejection(reply, runState.collectedResults)
			if !rejected && runState.requiredPathCompletionRecoveryActive {
				result, rejected = r.requiredPathCompletionRecoveryRejection(reply, runState.collectedResults)
			}

			gateReply := reply
			if !rejected {
				gateReply = r.decoratedReadOnlyReplyForCompletionGate(reply, runState.collectedResults)
				result, rejected = r.completionGateRejection(gateReply, runState.collectedResults, stepNumber, taskLedger, declarationMovesForbidden)
			}

			if rejected {
				priorGateRejections := runState.completionGateRecovery.PriorAttemptsFor(result)
				priorTotalRejections := runState.completionGateRecovery.TotalAttempts

				result.Error = r.escalateCompletionGateResultMessage(result, priorGateRejections, runState.currentAllowedTools)

				if runState.autonomyRecorder != nil {
					runState.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), runState.collectedResults, priorTotalRejections)
				}

				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
				retryLimit := r.completionGateActiveRetryLimit(result, runState.collectedResults, runState.currentAllowedTools)
				canRetryActiveViolation := runState.completionGateRecovery.CanRetryActive(retryLimit)

				if IsTextualToolCall(reply) {
					r.state.lastAssistantMessage.Content = "[XML tool-call markup detected and sanitized — the model emitted its native XML format instead of a proper tool call or prose. The harness recovery path will retry with a corrected prompt.]"
				}

				shouldAttachRecoveryPrompt := runState.completionGateRecovery.ShouldAttachPrompt(priorGateRejections, canRetryActiveViolation)

				if prompt, ok := r.completionGateRecoveryPrompt(runState.collectedResults); ok && shouldAttachRecoveryPrompt {
					runState.opts = completionGateRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority(), r.implementationCompletionState(runState.collectedResults))

					completionState := r.implementationCompletionState(runState.collectedResults)
					hiddenOnlyNoWriteTerminalRecovery := mutationHiddenOnlyNoWriteTerminalRecoveryStall(r.state.runtimeBrief, runState.collectedResults, assistantMessage, priorGateRejections, r.terminalSubmitPlanNativeTools(), completionState)

					if hiddenOnlyNoWriteTerminalRecovery {
						runState.opts = forceVisibleActionChatOptions(runState.opts)
						prompt = hiddenOnlyNoWriteTerminalRecoveryPrompt()

						r.emitEvent(RunEvent{Role: "mutation_visible_write_mode", Content: "textual mutation recovery produced hidden-only output after concrete source evidence; retrying once with hidden thinking disabled while permission-authorized inspection and mutation actions remain visible"})
					} else if implementationRequiredPathHiddenOnlyRecoveryStall(r.state.runtimeBrief, runState.collectedResults, assistantMessage, priorGateRejections, r.terminalSubmitPlanNativeTools(), completionState) {
						runState.opts = forceVisibleActionChatOptions(runState.opts)

						if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority()) {
							prompt = planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(prompt, r.state.runtimeBrief, runState.collectedResults)

							r.emitEvent(RunEvent{Role: "plan_then_execute_submit_correction_visible_mode", Content: "submit-correction recovery produced hidden-only output; retrying once with hidden thinking disabled for the terminal submit_plan action"})
						} else {
							prompt = "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce a visible parser-readable action from the currently callable inspection, mutation, or blocker surface.\n\n" + prompt

							r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "anchor-ready textual recovery produced hidden-only output; retrying once with hidden thinking disabled while permission-authorized inspection and mutation actions remain visible"})
						}
					}

					r.refreshPlanThenExecuteTerminalSurface("completion_gate_recovery")

					if card := r.renderRecoveryCardForRejectedActionWithTransport(workspace.ToolRequest{Tool: completionGateToolName}, result, runState.collectedResults, runState.currentAllowedTools, runState.planningTransport()); card != "" {
						prompt = appendRecoveryCardToMessage(prompt, card)
					}

					prompt = runState.setCompletionGateRecoveryMessage(prompt)
					r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
				}

				if !canRetryActiveViolation {
					if recovered, _, ok := r.readOnlyTerminalRecoveryCandidate(completionGateRepeatedRejectionAcceptance(), runState.collectedResults); ok {
						r.emitStateCompletionStatusFromParams("accepted", "read_only_terminal_recovery", "", runState.collectedResults, false)
						return finish(recovered, nil, "read_only_terminal_recovery", false)
					}

					answer := r.completionGateRepeatedBlockerAnswer(result, runState.collectedResults)
					blockerKind := string(r.completionGateRepeatedViolation(runState.collectedResults).Kind)
					r.emitStateCompletionStatusFromParams("blocked", blockerKind, blockerKind, runState.collectedResults, true)
					return finish(answer, nil, blockerKind, true)
				}

				if runState.policy.shouldStopForNoProgress(runState.lifecycle.progress.consecutiveNoProgress) {
					return finishNoProgressBudgetExhausted(runState.collectedResults)
				}

				continue
			}

			answer, err := r.ensureNonEmptyAnswer(ctx, runState.turnMessages, assistantMessage, modelCallOpts, runState.collectedResults, &runState.metrics)
			if strings.TrimSpace(gateReply) != "" &&
				strings.TrimSpace(gateReply) != strings.TrimSpace(reply) &&
				strings.TrimSpace(answer) == strings.TrimSpace(reply) &&
				!strings.HasPrefix(strings.TrimSpace(gateReply), "<<QUESTION>>") {

				answer = gateReply
			}

			if acceptance := r.evaluateFinalAnswerAcceptance(answer, runState.collectedResults); acceptance.BlockerKind != "" {
				blockerKind := acceptance.BlockerKind
				reason := acceptance.Reason
				rejectionFields := finalAnswerAcceptanceEventFields(acceptance)
				rejectionFields["blocker_kind"] = blockerKind
				rejectionFields["reason"] = reason
				r.emitEvent(marshalRunEvent("final_answer_recovery_required", rejectionFields))
				blockerResult, ok := finalAnswerCompletionGateProjection(
					acceptance,
					"Final answer rejected by blocker check",
					r.finalAnswerBlockerErrorText(blockerKind, reason, answer, runState.collectedResults),
				)
				if !ok {
					return finish(answer, err, "model_final", false)
				}
				if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
					stepNumber:       stepNumber,
					activeToolCall:   activeToolCall,
					assistantMessage: assistantMessage,
					acceptance:       acceptance,
					result:           blockerResult,
					controller:       &controller,
				}) {
					continue
				}

				if answer, ok := r.deterministicNoToolsFallback(noToolsFallbackAfterRejectedModelCloseout, runState.collectedResults); ok &&
					!readOnlyAnswerReportsInsufficientEvidence(answer) {

					return finish(answer, nil, "deterministic_final_answer_recovery", false)
				}

				return finish(answer, err, blockerKind, true)
			}

			return finish(answer, err, "model_final", false)
		}

		var assistantToolTurn llama.ChatMessage

		if activeToolCall != nil {
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
		} else if !r.supportsNativeTools() {
			assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, req, raw)
			activeToolCall = nil
		} else {
			call := syntheticLegacyToolCall(stepNumber, req, raw)

			activeToolCall = &call
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
		}

		assistantToolTurn = r.assistantHistoryMessageForOptions(assistantToolTurn, modelCallOpts)
		runState.turnMessages = append(runState.turnMessages, assistantToolTurn)
		r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, modelCallOpts, true)

		if r.shouldForceAnswerForPostCompletionVerification(req, runState.collectedResults, &runState.lifecycle.implementation.postCompletionVerificationCalls) {
			r.emitStateCompletionStatusFromParams("completed", "post_completion_verification_cutoff", "", runState.collectedResults, false)
			r.emitEvent(RunEvent{
				Role:    "post_completion_verification_cutoff",
				Content: fmt.Sprintf("all required edit paths written; cutting off self-verification after %d non-write call(s) to avoid false budget-exhausted failure signal", runState.lifecycle.implementation.postCompletionVerificationCalls),
			})

			answer, err := controller.completeWithoutMoreToolsWithReason("All required edit paths have been written and the implementation contract is fulfilled. Tools are now disabled. Write the standard final answer now: report the changed paths and verification status. Do not call further tools.")

			return finish(answer, err, "post_completion_verification_cutoff", false)
		}

		preflight := r.applyLanePreflight(runnerLanePreflightInput{
			Request:               req,
			Raw:                   raw,
			Results:               runState.collectedResults,
			VerificationAttempted: runState.executionState.verificationAttempted,
		})

		req = preflight.Request
		raw = preflight.Raw

		r.beginTurnRequest(req)
		requestBudget.clearStaleToolSurfaceRejection(req, runState.turn.currentFingerprint, runState.currentAllowedTools)

		if r.deferredRequestPrerequisitesSatisfied(req, runState.collectedResults) {
			requestBudget.clearRejectedFingerprint(runState.turn.currentFingerprint, toolfacts.ToolRejectionDeferredByPrerequisite)
			if workspace.IsWorkspaceWriteTool(req.Tool) {
				delete(runState.lifecycle.progress.rejectedActions, toolActionClass(req))
			}
		}

		requestBudget.setRecoveryContext(r.state.runtimeBrief, runState.collectedResults)
		decision := attachLoopPolicyDecision(requestBudget.earlyRejectedFingerprintDecision(req, runState.turn.currentFingerprint))
		if result, prompt, ok := r.mutationRejectedRetryWriteRecovery(decision, req, runState.collectedResults); ok {
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

			r.rebuildRunTools()

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
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

		if preflight.RejectSummary != "" {
			if preflight.ForceAnswer != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: preflight.RejectSummary,
					Error:   preflight.RejectError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				if preflight.ForceAnswer.Reason == "conversation_evidence_sufficient" {
					if presentable, ok := r.presentableFallbackCandidate(runState.collectedResults, "The retained source context identifies the relevant implementation surface."); ok {
						return finish(presentable, nil, preflight.ForceAnswer.Reason, true)
					}
				}

				answer, err := controller.completeWithoutMoreToolsWithReason(preflight.ForceAnswer.Prompt)

				return finish(answer, err, preflight.ForceAnswer.Reason, true)
			}

			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, runnerToolRejection{
				Summary:     preflight.RejectSummary,
				Error:       preflight.RejectError,
				Disposition: preflight.RejectDisposition,
				Facts:       preflight.RejectFacts,
			})
			if done {
				return finish(answer, err, preflight.RejectStopReason, true)
			}

			continue
		}

		configuredToolDecision := r.runner.Workspace.EvaluateConfiguredToolRule(req)
		configuredToolDenied := configuredToolDecision.Decision == toolpolicy.Deny

		inspection := r.permissionAuthorizedInspection(textutil.Merge(
			r.workspaceAdvertisedAllowedTools(),
			runState.currentAllowedTools,
			r.state.runtimeBrief.AllowedTools,
		))
		surfaceAllowedTools := r.runnerPlanExecuteTextualAllowedToolsForRequest(
			runState.currentAllowedTools,
			r.state.runtimeBrief,
			runState.collectedResults,
			req,
			inspection,
			r.planThenExecutePathAuthority(),
		)

		surfaceDecision := toolAuthorizationSurfaceDecision(req, surfaceAllowedTools, false)
		validWorkspaceRead := inspection.AllowsRequest(req)
		if surfaceDecision.Rejected && !validWorkspaceRead && !configuredToolDenied {
			summary, errorText := surfaceDecision.Summary, surfaceDecision.Error

			if planThenExecuteUnscopedEmptySubmitPlanAttempt(r.state.runtimeBrief, req) {
				emitToolAuthorizationSurfacePolicyResult(runState.autonomyRecorder, req, autonomy.PolicyReject)

				result := workspace.ToolResult{
					Tool:    workspace.ToolNameSubmitPlan,
					Code:    workspace.SubmitPlanEmptyEditsCode,
					Summary: "submit_plan rejected",
					Error:   "submit_plan requires at least one edit in the edits array",
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())

				r.refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				r.rebuildRunTools()

				runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())

				r.syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_empty_submit_plan_recovery", Content: prompt})

				continue
			} else if historySummary, historyError, ok := planThenExecuteUnavailableHistoryToolNudge(req, r.state.runtimeBrief, runState.collectedResults, surfaceAllowedTools, r.planThenExecutePathAuthority()); ok {
				summary = historySummary
				errorText = historyError
			}

			emitToolAuthorizationSurfacePolicyResult(runState.autonomyRecorder, req, autonomy.PolicyReject)
			disposition := toolSurfaceRejectionDisposition(req, r.workspaceAllowedTools(), surfaceAllowedTools)
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, runnerToolRejection{Summary: summary, Error: errorText, Disposition: disposition})
			if done {
				return finish(answer, err, "tool_not_allowed_by_runtime_surface", true)
			}

			continue
		}

		if redirected, ok := r.fillSourceSanityPathFromSingleChangedSource(req, runState.collectedResults, runState.currentAllowedTools); ok {
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

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			r.beginTurnRequest(req)
		}

		runState.metrics.ToolCalls++
		// Conversation lane: periodic call-count awareness nudges.
		// Milestones derived from model context window size so
		// smaller models get earlier nudges, larger models later ones.
		// The auto-findings capture fires at the first conversation milestone;
		// awareness nudges begin one nudgeInterval later so the two
		// signals are staggered rather than competing at the same call.
		if runState.lifecycle.closure.conversationFirstMilestone > 0 && runState.metrics.ToolCalls > 5 {
			shouldNudge := false

			var nudgeMsg string

			firstNudgeAt := runState.lifecycle.closure.conversationFirstMilestone + runState.lifecycle.closure.conversationNudgeInterval

			if runState.metrics.ToolCalls == firstNudgeAt {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. If you have evidence to answer this specific question, answer now in plain prose. Do not begin new investigation on neighboring topics.", runState.metrics.ToolCalls, strings.TrimSpace(r.state.runtimeBrief.Objective))
			} else if runState.metrics.ToolCalls >= firstNudgeAt+runState.lifecycle.closure.conversationNudgeInterval && (runState.metrics.ToolCalls-firstNudgeAt)%runState.lifecycle.closure.conversationNudgeInterval == 0 {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. Answer this question directly from the evidence gathered, or if more evidence is genuinely needed, take one narrow next step. Do not explore related code or begin new investigations.", runState.metrics.ToolCalls, strings.TrimSpace(r.state.runtimeBrief.Objective))
			}

			if shouldNudge {
				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
				r.emitEvent(RunEvent{Role: "call_count_awareness", Content: nudgeMsg})
			}
		}

		// Narrow-tool repetition nudge: if the model repeatedly calls the
		// same tool family (code_navigate, read_file, search_*) without
		// trying a broader discovery tool, suggest a strategy shift.
		// This catches patterns like 50 consecutive code_navigate
		// read_symbol calls — each targeting a different symbol, so
		// same-path fixation and fingerprint hard block never fire.
		if narrowFamily := workspace.RuntimeNarrowToolFamily(req.Tool); narrowFamily != "" {
			if narrowFamily == runState.lifecycle.progress.lastNarrowFamily {
				runState.lifecycle.progress.consecutiveNarrowCalls++
			} else {
				runState.lifecycle.progress.consecutiveNarrowCalls = 1
				runState.lifecycle.progress.lastNarrowFamily = narrowFamily
			}

			// Only nudge if the model has been doing the same narrow
			// family for a while and hasn't used a broad discovery tool
			// recently (tracked via discovery tool reset below).
			if runState.lifecycle.progress.consecutiveNarrowCalls >= narrowToolFamilyNudgeAt && !runState.lifecycle.progress.narrowFamilyNudgeFired {
				runState.lifecycle.progress.narrowFamilyNudgeFired = true
				prompt := narrowToolFamilyRepetitionNudgePrompt(narrowFamily, runState.lifecycle.progress.consecutiveNarrowCalls, runState.currentAllowedTools)
				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "narrow_tool_repetition_nudge", Content: prompt})
			}
		} else {
			// Reset on any broad/non-narrow tool — the model tried
			// something different, so clear the repetition tracking.
			runState.lifecycle.progress.consecutiveNarrowCalls = 0
			runState.lifecycle.progress.lastNarrowFamily = ""
			runState.lifecycle.progress.narrowFamilyNudgeFired = false
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
				Mode:           r.runner.Workspace.Mode(),
				Broad:          workspace.IsBroadInspectionRequest(req),
				Narrowing:      workspace.IsNarrowingRequest(req),
				Action:         actionClass,
				DiscoveryClass: discoveryProfile.Class,
				DiscoveryScope: discoveryProfile.Scope,
				DiscoveryCost:  discoveryProfile.Cost,
			})
		}

		toolKey := toolRequestKey(req)
		if prompt, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, runState.turn.lastToolResult, runState.collectedResults, r.terminalSubmitPlanNativeTools()); ok && toolKey != "" && toolKey == runState.turn.lastToolKey && runState.lifecycle.progress.replayedToolRequests[toolKey] < 2 {
			runState.lifecycle.progress.replayedToolRequests[toolKey]++

			repeatedResult := resultWithToolRejectionDisposition(workspace.ToolResult{
				Tool:    workspace.ToolNameSubmitPlan,
				Summary: "Repeated invalid submit_plan rejected",
				Error:   "the identical submit_plan request repeated a previously rejected plan; review the typed diagnostics and issue a corrected request",
				Facts:   runState.turn.lastToolResult.Facts.Clone(),
			}, toolfacts.ToolRejectionInvalidRequest)
			controller.recordRejectedResult(stepNumber, activeToolCall, req, repeatedResult, "plan_then_execute_repeated_submit_plan_rejected")

			r.refreshPlanThenExecuteTerminalSurface("submit_plan_replayed_invalid")

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			r.rebuildRunTools()

			runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())

			r.syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_repeated_submit_plan_correction", Content: prompt})

			continue
		}

		if shouldRejectExactReplayBeforeLoopGate(req) && toolKey != "" && toolKey == runState.turn.lastToolKey && runState.lifecycle.progress.replayedToolRequests[toolKey] > 0 {
			summary := "Repeated tool request rejected"
			errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."

			if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, runState.turn.lastToolResult, runState.collectedResults); ok {
				summary = replaySummary
				errorText = replayError
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				runnerToolRejection{Summary: summary, Error: errorText},
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
		identicalCallRun := requestBudget.noteFingerprint(runState.turn.currentFingerprint)
		if identicalCallRun >= 3 {
			loopRecoveryReason := fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row", req.Tool, identicalCallRun)
			if runState.lifecycle.planExecute.loopBudgetRecoveryUsed {
				loopRecoveryReason = fmt.Sprintf("the model repeated %s with the same arguments after a loop-budget recovery", req.Tool)
			}
			if r.tryApplyPlanExecuteLoopBudgetRecovery(req, loopRecoveryReason) {
				continue
			}

			if r.runner.DoomLoopGate != nil {
				if runState.autonomyRecorder != nil {
					runState.autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", runState.turn.currentFingerprint, identicalCallRun)
				}
				r.emitEvent(RunEvent{Role: "doom_loop_guard", Content: fmt.Sprintf("model repeated %s %d times — approval required", req.Tool, identicalCallRun)})

				if r.runner.DoomLoopGate(req, identicalCallRun) {
					// User approved — reset the streak and let this call execute.
					requestBudget.fingerprint.identicalRun = 0
				} else {
					// User denied — feed a blocker result, then force a
					// no-tools closeout from gathered evidence. Returning the
					// raw rejectTool result can produce an empty final answer
					// when this is the first rejection in the turn.
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						runnerToolRejection{Summary: "Doom-loop guard: user denied continuation", Error: fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; user denied continuation. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun)},
					)
					if !done {
						answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
					}

					return finish(answer, err, "doom_loop_guard", true)
				}
			} else {
				if runState.autonomyRecorder != nil {
					runState.autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", runState.turn.currentFingerprint, identicalCallRun)
				}
				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					runnerToolRejection{Summary: "Doom-loop guard tripped", Error: fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; stopping to prevent budget exhaustion. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun)},
				)
				if !done {
					answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
				}

				return finish(answer, err, "doom_loop_guard", true)
			}
		}

		requestBudget.setRecoveryContext(r.state.runtimeBrief, runState.collectedResults)
		fingerprintDecision := attachLoopPolicyDecision(requestBudget.fingerprintDecision(runState.policy, req, runState.turn.currentFingerprint))
		if fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" &&
			r.tryApplyPlanExecuteLoopBudgetRecovery(req, fingerprintDecision.ForceAnswerPrompt) {
			continue
		}

		if res := fingerprintDecision.apply(budgetDecisionContext{
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
			!r.lanePolicy().skipSynthesisNudge {

			if res := requestBudget.synthesisDecision(runState.policy, runState.metrics.ToolCalls, runnerBriefRequiresMutation(r.state.runtimeBrief)).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}

		if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			requestBudget.noteReadClassRequest(req)

			runState.metrics.ReadClassCalls++
		}

		// Note tool: harness-owned scratch metadata, not a workspace mutation.
		if ok && strings.TrimSpace(req.Tool) == noteToolName {
			noteResult := runNoteTool(r.runner.Workspace.Root(), raw)

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, noteResult, "note_tool", runnerRecordOptions{})
			r.emitEvent(RunEvent{Role: "note_tool", Content: noteResult.Summary})

			continue
		}

		if summary, errorText, reject := declarationMoveConstraintRejection(req, declarationMovesForbidden); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, runnerToolRejection{Summary: summary, Error: errorText})
			if done {
				return finish(answer, err, "go_refactor_guard_exhausted", true)
			}

			continue
		}

		if actionClass != "" && runState.lifecycle.progress.rejectedActions[actionClass] > 0 && !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				runnerToolRejection{Summary: "Repeated dead-end action rejected", Error: "this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have."},
			)
			if done {
				return finish(answer, err, "repeated_dead_end_budget_exhausted", true)
			}

			continue
		}

		if toolKey != "" && toolKey == runState.turn.lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
			if runState.turn.lastToolResult != nil {
				if runState.lifecycle.progress.replayedToolRequests[toolKey] > 0 {
					summary := "Repeated tool request rejected"
					errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."

					if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, runState.turn.lastToolResult, runState.collectedResults); ok {
						summary = replaySummary
						errorText = replayError
					}

					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						runnerToolRejection{Summary: summary, Error: errorText},
					)
					if done {
						return finish(answer, err, "repeated_tool_budget_exhausted", true)
					}

					continue
				}

				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					runnerToolRejection{
						Summary: "Repeated tool request rejected",
						Error:   "this exact request already completed earlier in the turn. Cached output is not substituted before admission; issue a fresh materially different request or answer from the recorded result.",
					},
				)
				if done {
					return finish(answer, err, "repeated_tool_budget_exhausted", true)
				}

				continue
			}

			errorText := "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes."
			if strings.TrimSpace(req.Tool) == workspace.ToolNameGoMoveDeclarations {
				errorText = "you just ran the same declaration move. Choose a different destination/declaration batch, run verification, or answer with the current blocker."
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				runnerToolRejection{Summary: "Repeated tool request rejected", Error: errorText},
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}

			continue
		}

		permissionDecision := permissionPolicyDecision(r.runner.Workspace, r.runner.PermissionPolicy, req)
		if configuredToolDenied {
			permissionDecision = PermissionDecisionSnapshot{
				Tool:     strings.TrimSpace(req.Tool),
				Target:   configuredToolDecision.Target,
				Decision: toolpolicy.Deny,
				Rule:     "workspace.tool_rules decision=deny",
			}
		} else if permissionDecision.Decision == toolpolicy.Allow {
			if readOnlyWriteChildDecision, ok := r.readOnlyWriteChildTaskPermissionDecision(req); ok {
				permissionDecision = readOnlyWriteChildDecision
			}
		}

		if runState.autonomyRecorder != nil {
			intentPolicyInput := normalizedPolicyInput
			intentPolicyInput.EventType = autonomy.EventToolIntentClassified
			intentPolicyInput.Source = "runner_tool_intent"
			runState.autonomyRecorder.emitIntentClassified(req, intentPolicyInput)

			pathPolicyInput := normalizedPolicyInput
			pathPolicyInput.EventType = autonomy.EventPathRolesResolved
			pathPolicyInput.Source = "runner_path_roles"
			runState.autonomyRecorder.emitPathRolesResolved(req, pathPolicyInput)
		}

		if permissionDecision.Decision != toolpolicy.Allow {

			if runState.autonomyRecorder != nil {
				decision := autonomy.PolicyDeny
				resultLabel := "denied"

				if permissionDecision.Decision == toolpolicy.Ask {
					decision = autonomy.PolicyAsk
					resultLabel = "approval_required"
				}

				runState.autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:         decision,
					Target:           permissionDecision.Target,
					PackID:           autonomy.ContractPackToolAuthorization,
					InvariantID:      toolAuthorizationPolicyInvariant,
					ReasonCode:       "permission_policy",
					Transport:        normalizedPolicyInput.Transport,
					ParserFailure:    normalizedPolicyInput.ParserFailure,
					RepairSource:     normalizedPolicyInput.RepairSource,
					ApprovalRequired: permissionDecision.Decision == toolpolicy.Ask,
					Result:           resultLabel,
					ErrorClass:       "permission_policy",
					Source:           strings.TrimSpace(permissionDecision.Rule),
				})
			}

			result := resultWithToolRejectionDisposition(permissionDecision.ToolResult(), toolfacts.ToolRejectionPolicyDenied)

			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "permission_denied", runnerRecordOptions{
				CountRejected:  true,
				SetLastToolKey: true,
			})

			if workspaceToolRulesPermissionDeny(permissionDecision) {
				answer := workspaceToolRulesPermissionDenyAnswer(req, permissionDecision)

				r.emitStateCompletionStatusFromParams("blocked", "workspace_tool_rules_deny", "workspace_tool_rules_deny", runState.collectedResults, true)

				return finish(answer, nil, "workspace_tool_rules_deny", true)
			}

			if permissionDecision.Decision == toolpolicy.Ask {
				answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()
				return finish(answer, err, "permission_approval_required", false)
			}

			continue
		}

		if runState.autonomyRecorder != nil {
			runState.autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
				Decision:      autonomy.PolicyAllow,
				Target:        permissionDecision.Target,
				PackID:        autonomy.ContractPackToolAuthorization,
				InvariantID:   toolAuthorizationPolicyInvariant,
				ReasonCode:    "permission_policy",
				Transport:     normalizedPolicyInput.Transport,
				ParserFailure: normalizedPolicyInput.ParserFailure,
				RepairSource:  normalizedPolicyInput.RepairSource,
				Result:        "no_approval_needed",
				Source:        strings.TrimSpace(permissionDecision.Rule),
			})
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTask {
			if r.runner.DisableTaskTool {
				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					runnerToolRejection{Summary: "Child task request rejected", Error: "nested task spawning is disabled inside child tasks. Answer with the evidence already collected or inspect directly with read-only tools."},
				)
				if done {
					return finish(answer, err, "nested_task_budget_exhausted", true)
				}

				continue
			}

			result := r.runChildTask(ctx, req, runState.opts)

			recorder.activeToolCall = activeToolCall

			if strings.TrimSpace(result.Error) == "" && !isWriteChildMode(req.Mode) {
				requestBudget.readOnlyChildTaskSatisfied = true

				childEvidence := newToolResultEvidenceProjection([]workspace.ToolResult{result})
				if childEvidence.hasAnswerableDelegatedPacket(r.state.runtimeBrief) {
					evidencePaths := childEvidence.postChildClosureEvidencePaths(r.state.runtimeBrief.Objective, 4)

					requestBudget.broadEvidenceDelegationSatisfied = true
					r.state.runtimeBrief.Constraints = textutil.Merge(r.state.runtimeBrief.Constraints, []string{"child_packet_available"})
					runState.postChildPacketActive = true
					runState.lifecycle.closure.postChildFollowupReads = 0
					runState.lifecycle.closure.postChildDiminishingNudgeStage = 0
					r.state.runtimeBrief.EvidencePaths = textutil.Merge(r.state.runtimeBrief.EvidencePaths, evidencePaths)
					requestBudget.noteReadOnlyChildPacket(evidencePaths)
					requestBudget.postChild.readCalls = 0

					nudge := buildReadinessNudge(ReadinessNudge{
						Headline:          "The delegated read_only child result is now available as an evidence packet.",
						EvidencePathLabel: "Packet-named paths: ",
						EvidencePaths:     evidencePaths,
						Objective:         r.state.runtimeBrief.Objective,
						RuntimeBrief:      r.state.runtimeBrief,
						Results:           []workspace.ToolResult{result},
						ContinuationScope: "child_packet_available",
						FooterLines: []string{
							"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
							"If you answer now, answer from the packet and inspected source evidence.",
						},
						IncludeProbeSuggestions: true,
					})

					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
				}
			}

			recorder.record(stepNumber, req, result, "child_task_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if runState.policy.shouldStopForNoProgress(runState.lifecycle.progress.consecutiveNoProgress) {
				if shouldPreferModelBudgetSynthesis(r.agentName(), r.state.runtimeBrief, runState.collectedResults) {
					continue
				}

				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Tools are now disabled. Use the child-task result and gathered evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the supported answer.")

				return finish(answer, err, "no_progress_budget_exhausted", false)
			}

			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTaskLedger {
			result := taskLedger.apply(req, runState.collectedResults)

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if runState.policy.shouldStopForNoProgress(runState.lifecycle.progress.consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", false)
			}

			continue
		}

		// Scoped implementation lanes categorically prohibit model-authored
		// filesystem mutation through shell. This contract must run before the
		// host approval hook: approval cannot make an unreceipted shell mutation
		// satisfy canonical path scope, mutation receipts, or post-write
		// verification ownership.
		if summary, errorText, reject := guardedImplementationShellRejection(req, r.state.runtimeBrief); reject {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				runnerToolRejection{Summary: summary, Error: errorText, Disposition: toolfacts.ToolRejectionPolicyDenied},
			)
			if done {
				return finish(answer, err, "mutation_capable_shell_rejected", true)
			}

			continue
		}

		if r.runner.PreExecute != nil {
			if guarded := r.runner.PreExecute(stepNumber, req); guarded != nil {
				guardedResult := *guarded
				if strings.TrimSpace(guardedResult.Error) != "" {
					guardedResult = resultWithToolRejectionDisposition(guardedResult, toolfacts.ToolRejectionPolicyDenied)
				}
				bootstrapReadReminder := isBootstrappedTargetReadReminder(req, *guarded)
				rejectedStreak := false
				rejectedActionCounted := false

				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder {
					runState.lifecycle.progress.rejectedActions[actionClass]++

					rejectedStreak = true
					rejectedActionCounted = true
				} else if strings.TrimSpace(guarded.Error) != "" {
					rejectedActionCounted = true
				}

				recorder.activeToolCall = activeToolCall
				recorder.recordWithOptions(stepNumber, req, guardedResult, "pre_execute_guard", runnerRecordOptions{
					CountRejected:           rejectedActionCounted,
					IncrementRejectedStreak: rejectedStreak,
					IncrementNoProgress:     strings.TrimSpace(guarded.Error) != "" && !rejectedStreak,
					SetLastToolKey:          true,
				})
				r.markConcreteSourceEvidenceObserved(guardedResult)

				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					if runState.autonomyRecorder != nil {
						runState.autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
							Decision:         autonomy.PolicyAsk,
							PackID:           autonomy.ContractPackToolAuthorization,
							InvariantID:      toolAuthorizationPolicyInvariant,
							ReasonCode:       "write_approval_required",
							ApprovalRequired: true,
							Result:           "approval_required",
							ErrorClass:       "approval_guard",
							Source:           "runner_pre_execute_guard",
						})
					}

					answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()

					return finish(answer, err, "write_approval_required", false)
				}

				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && runState.lifecycle.progress.consecutiveRejected >= 2 {
					answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}

				if bootstrapReadReminder && runState.policy.shouldStopForNoProgress(runState.lifecycle.progress.consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", false)
				}

				continue
			}
		}

		if guarded := CheckDescription(req); guarded != nil {
			result := resultWithToolRejectionDisposition(*guarded, toolfacts.ToolRejectionInvalidRequest)
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, writeDescriptionMissingCode, runnerRecordOptions{
				CountRejected:           true,
				IncrementRejectedStreak: true,
				SetLastToolKey:          true,
			})

			continue
		}

		if deferredResult, deferred := r.submitPlanHistoryDeferralResult(req, runState.collectedResults); deferred {
			controller.recordRejectedResult(stepNumber, activeToolCall, req, deferredResult, "submit_plan_deferred_by_history_prerequisite")
			r.refreshPlanThenExecuteTerminalSurface("submit_plan_deferred_by_history_prerequisite")
			r.rebuildRunTools()
			runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())
			r.syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_history_required", Content: deferredResult.Error})
			continue
		}

		if deferredResult, deferred := r.implementationWritePrerequisiteDeferralResult(req, runState.collectedResults); deferred {
			controller.recordRejectedResult(stepNumber, activeToolCall, req, deferredResult, "implementation_write_deferred_by_prerequisite")
			continue
		}

		if summary, errorText, reject := plannerOwnedVerificationShellRejection(req, r.state.runtimeBrief); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, runnerToolRejection{Summary: summary, Error: errorText})
			if done {
				return finish(answer, err, "planner_owned_verification_shell_rejected", true)
			}

			continue
		}

		if runState.executionState.needsCheckpoint(r.runner.CheckpointPolicy, req) {
			checkpoint, checkpointErr := CaptureGitCheckpoint(ctx, r.runner.Workspace.Root())
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

			runState.executionState.markCheckpointCreated()
			r.emitEvent(marshalRunEvent("checkpoint", checkpoint))
		}

		if runState.executionState.shouldRequireVerification(req, r.state.runtimeBrief) && !inspection.AllowsRequest(req) {
			summary, errorText := verificationRequiredAfterWritesMessage(r.state.runtimeBrief, runState.executionState.writesSinceVerification)

			if runState.autonomyRecorder != nil {
				runState.autonomyRecorder.emitVerificationGate(req, autonomy.PolicyReject, "verification_required_after_writes", "pending")
			}

			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				runnerToolRejection{Summary: summary, Error: errorText, Disposition: toolfacts.ToolRejectionDeferredByPhase},
			)
			if done {
				return finish(answer, err, "verification_required_after_writes", true)
			}

			continue
		}

		var result workspace.ToolResult

		if guard := r.preflightPlanThenExecuteWriteGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_write_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, runState.collectedResults); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, runState.collectedResults); guard.Intercepted {
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
		} else {
			req = r.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, runState.collectedResults)
			result = r.runner.Workspace.Execute(runState.ctx, req)
		}

		recorder.activeToolCall = activeToolCall
		recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		r.markConcreteSourceEvidenceObserved(result)

		update := runState.executionState.applyResult(req, result, runState.turn.currentFingerprint, r.runner.Workspace, &requestBudget)
		if runState.autonomyRecorder != nil && workspaceWriteResultCommitted(req, result) {
			runState.autonomyRecorder.emitWriteGate(req, autonomy.PolicyAllow, "write_result_completed", uniqueSortedRunnerPaths(newToolResultEvidenceRecord(result).ChangedPathList()))
		}

		if update.ResetVerificationGuards {
			clearRunnerStringIntMap(runState.lifecycle.progress.rejectedActions)
			r.rebuildRunTools()
		}

		if workspaceWriteResultCommitted(req, result) &&
			(r.hasPendingVerificationAfterWrites(runState.collectedResults) ||
				r.sourceEditsNeedSemanticVerification(runState.collectedResults)) {

			prompt := r.postWriteVerificationRequiredPrompt(runState.collectedResults, plannerOwnsVerification(r.state.runtimeBrief))

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			runState.opts = completionGateRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority(), r.implementationCompletionState(runState.collectedResults))
			runState.opts = forceVisibleActionChatOptions(runState.opts)

			r.rebuildRunTools()
			r.emitEvent(RunEvent{Role: "post_write_verification_required", Content: prompt})
		}

		if !r.terminalSubmitPlanNativeTools() && len(r.remainingRequiredEditPaths(runState.collectedResults)) > 0 &&
			implementationRequiredPathAnchorsReady(r.state.runtimeBrief, runState.collectedResults, r.completionChangedPaths(runState.collectedResults)) &&
			(!r.planThenExecutePlannerPhase() || planThenExecuteSubmitPlanOnlyReady(r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority())) {

			runState.opts = completionGateRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, false, r.planThenExecutePathAuthority(), r.implementationCompletionState(runState.collectedResults))
			r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "all remaining required edit paths have anchor-ready source evidence; textual backend recovery will disable hidden thinking and require visible write/blocker output"})
		}

		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)

		if runState.autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}

			runState.autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}

		if prompt, ok := r.scratchpadDeliverableFailedEvidenceNudge(req, result, runState.collectedResults); ok {
			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_failed_evidence_nudge", Content: prompt})
		}

		if checklist := r.requiredEditPathsProgressMessage(req, result, runState.collectedResults); checklist != "" {
			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: checklist})
			r.emitEvent(RunEvent{Role: "required_edit_paths_progress", Content: checklist})
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			surface := derivePlanExecuteRecoverySurface(r.state.runtimeBrief, runState.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true}, r.planThenExecutePathAuthority())
			prompt := planThenExecuteSourceSatisfiedTerminalRequiredPrompt(r.state.runtimeBrief, result)
			eventRole := "plan_then_execute_source_satisfied_terminal_required"

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
				if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(runState.collectedResults); ok {
					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.state.runtimeBrief, runState.collectedResults, rejected, rejectedAt)
				}

				eventRole = "plan_then_execute_anchor_refresh_required"
			}

			r.refreshPlanThenExecuteTerminalSurface("source_satisfied_terminal_required")

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			r.rebuildRunTools()

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh || surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
				runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())
			} else {
				runState.opts = planThenExecuteReadyToSubmitChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())
			}

			r.syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

			continue
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteSubmitPlanRejectedForCorrection(req, result) {
			recoveryState := r.derivePlanExecutePlanningState(runState.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})

			if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
				replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&runState.turnMessages, req, result)

				eventRole := "plan_then_execute_grounding_required"
				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteGroundingRequiredRecoveryPlan())

				if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
					rejectedAt := len(runState.collectedResults) - 1

					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.state.runtimeBrief, runState.collectedResults, result, rejectedAt)
					eventRole = "plan_then_execute_anchor_refresh_required"
				}

				r.refreshPlanThenExecuteTerminalSurface("submit_plan_grounding_rejected")

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

				r.rebuildRunTools()

				runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())

				r.syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

				continue
			}

			replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&runState.turnMessages, req, result)

			rejectedAt := len(runState.collectedResults) - 1
			refreshNote := planThenExecutePostSubmitRejectionRefreshNote(r.state.runtimeBrief, runState.collectedResults, result, rejectedAt)
			prompt := "The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(result) + planThenExecuteSubmitCorrectionOutcomeInstruction(r.state.runtimeBrief, runState.collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.state.runtimeBrief, result) + " " + refreshNote
			eventRole := "plan_then_execute_submit_plan_required"

			if planThenExecuteUnscopedEmptySubmitPlanRejected(r.state.runtimeBrief, runState.collectedResults) {
				emitToolAuthorizationSurfacePolicyResult(runState.autonomyRecorder, req, autonomy.PolicyAllow)
				prompt = RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())
				eventRole = "plan_then_execute_empty_submit_plan_recovery"
			} else if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
				prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.state.runtimeBrief, runState.collectedResults, result, rejectedAt)
				eventRole = "plan_then_execute_anchor_refresh_required"
			} else if r.terminalSubmitPlanNativeTools() {
				prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.state.runtimeBrief, result)
			}

			r.refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")

			runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			r.rebuildRunTools()

			runState.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())

			r.syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

			continue
		}

		if !runState.lifecycle.planExecute.readinessNudged {
			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority()); nudge != "" {
				pendingKey := strings.Join(planThenExecuteUnreadRequiredPathList(r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority()), "\x00")
				if pendingKey != "" && pendingKey != runState.lifecycle.planExecute.unreadSourceNudgeKey {
					runState.lifecycle.planExecute.unreadSourceNudgeKey = pendingKey

					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					runState.opts = planThenExecuteUnreadRequiredSourceChatOptions(runState.opts, r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority())

					runState.syncCurrentAllowedToolsFromOptions()
					r.emitEvent(RunEvent{Role: "plan_then_execute_unread_source_nudge", Content: nudge})
				}
			}

			if nudge := planThenExecuteReadyToSubmitNudge(r.state.runtimeBrief, runState.collectedResults, r.planThenExecutePathAuthority()); nudge != "" {
				runState.lifecycle.planExecute.readinessNudged = true
				r.transitionPlanThenExecuteSubmitReadyTerminal(nudge, "plan_then_execute_readiness_nudge", "plan_then_execute_readiness")
			}
		}

		if decision, ok := r.derivePlanThenExecuteCorrectionAfterRefreshDecision(result); ok {
			r.applyPlanThenExecuteCorrectionAfterRefreshDecision(decision)
		}

		if !runState.delegatedReadOnlyAnswerReady && !runState.postChildPacketActive {
			r.attachInstructionsForTouchedPaths(&runState.turnMessages, req, result)
		}

		childReadyBeforeResult := runState.delegatedReadOnlyAnswerReady
		becameReadyFromDelegated := r.delegatedReadOnlyAnswerReadyAfterResult(req, result, runState.collectedResults)
		becameReadyFromAnalysis := r.readOnlyAnalysisAnswerReadyAfterResult(req, result, runState.collectedResults)

		if becameReadyFromDelegated || becameReadyFromAnalysis {
			justBecameReady := !runState.delegatedReadOnlyAnswerReady

			runState.delegatedReadOnlyAnswerReady = true
			r.state.runtimeBrief.Constraints = textutil.Merge(r.state.runtimeBrief.Constraints, []string{"answer_readiness_reached"})

			if becameReadyFromDelegated {
				r.emitDelegatedReadOnlyAnswerReadyEvent(req, runState.collectedResults)
			} else {
				r.emitReadOnlyAnswerReadyEvent(req, runState.collectedResults)
			}

			if !runState.lifecycle.closure.delegatedAnswerReadyNudged {
				runState.lifecycle.closure.delegatedAnswerReadyNudged = true

				nudge := buildReadinessNudge(ReadinessNudge{
					Headline:          "The delegated read-only evidence is sufficient for a grounded answer to this question.",
					EvidencePathLabel: "Core evidence paths: ",
					EvidencePaths:     closurePromptEvidencePaths(newToolResultEvidenceProjection(runState.collectedResults).delegatedClosureEvidencePaths(r.state.runtimeBrief.Objective, 4), 4),
					Objective:         r.state.runtimeBrief.Objective,
					RuntimeBrief:      r.state.runtimeBrief,
					Results:           runState.collectedResults,
					ContinuationScope: "delegated_read_only_answer_ready",
					FooterLines: []string{
						"Carry forward the inspected paths above and keep the answer minimal; avoid side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
					},
				})
				eventRole := "delegated_read_only_answer_ready_nudge"

				if becameReadyFromAnalysis {
					nudge = buildReadinessNudge(ReadinessNudge{
						Headline:          "The read-only evidence is sufficient for a grounded answer to this question.",
						EvidencePathLabel: "Core evidence paths: ",
						EvidencePaths:     closurePromptEvidencePaths(newToolResultEvidenceProjection(runState.collectedResults).evidencePathsForBrief(r.state.runtimeBrief, 4), 4),
						Objective:         strings.TrimSpace(r.state.runtimeBrief.Objective),
						RuntimeBrief:      r.state.runtimeBrief,
						Results:           runState.collectedResults,
						ContinuationScope: "answer_readiness_reached",
						FooterLines: []string{
							"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit.",
						},
					})
					eventRole = "read_only_answer_ready_nudge"
				}

				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				r.emitEvent(RunEvent{Role: eventRole, Content: nudge})
			}

			// Rebuild tools so readiness state can affect explicit
			// mutation/source-refresh contracts. Read-only readiness is
			// advisory and must not hide or reshape discovery tools.
			if justBecameReady {
				r.rebuildRunTools()
			}

		}

		// Conversation lane: once source evidence is available, the
		// generic post-readiness contract above owns further probes.
		// Keep this inspected-path note only for lanes that have not
		// entered answer readiness.
		if r.conversationEvidenceAvailableForObjective(runState.collectedResults) && !runState.lifecycle.closure.conversationEvidenceNudged && !runState.delegatedReadOnlyAnswerReady {
			runState.lifecycle.closure.conversationEvidenceNudged = true

			inspected := conversationInspectedPathSummary(runState.collectedResults)

			if inspected != "" {
				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "Inspected paths so far: " + inspected + ". Answer if the evidence supports the requested claim; otherwise continue only with one exact missing claim and one exact probe that would resolve it.",
				})
			}
		}

		// Conversation lane: auto-capture findings at the first context-derived
		// milestone so discoveries survive context compactions.
		if r.lanePolicy().autoCaptureFindings &&
			runState.metrics.ToolCalls == runState.lifecycle.closure.conversationFirstMilestone && strings.TrimSpace(result.Error) == "" && r.runner.Workspace != nil {

			if _, err := writeAutoFindingsNoteFromResults(r.runner.Workspace.Root(), r.state.runtimeBrief.Objective, runState.collectedResults); err == nil {
				runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The harness has captured your findings so far to .idiom/notes/auto-findings.md. This note persists across context compactions — review it if your working memory is refreshed. Continue investigating, or answer if you have enough evidence.",
				})
			} else {
				r.emitEvent(RunEvent{Role: "note_tool", Content: "Auto findings note rejected: " + err.Error()})
			}
		}

		if childReadyBeforeResult && !runState.postChildPacketActive && workspace.IsReadTool(strings.TrimSpace(req.Tool)) && strings.TrimSpace(result.Error) == "" {
			runState.lifecycle.closure.delegatedPostReadyFollowups++
			// Diminishing-returns nudge stays as a soft signal; the hard
			// budget force-finish was a bandaid (per the no-bandaid
			// memory) and got removed. MaxToolSteps + doom_loop_guard
			// catch pathological loops; the completion gate enforces
			// mutation closure.
			if runState.lifecycle.closure.delegatedPostReadyFollowups > runState.lifecycle.closure.delegatedDiminishingNudgeStage {
				if nudge := delegatedReadOnlyDiminishingReturnsNudge(r.state.runtimeBrief.Objective, runState.collectedResults, runState.lifecycle.closure.delegatedPostReadyFollowups); nudge != "" {
					runState.lifecycle.closure.delegatedDiminishingNudgeStage = runState.lifecycle.closure.delegatedPostReadyFollowups

					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})

					role := "read_only_diminishing_returns_nudge"

					if strings.HasPrefix(r.agentName(), "child:") {
						role = "delegated_read_only_diminishing_returns_nudge"
					}

					r.emitEvent(RunEvent{Role: role, Content: nudge})
				}
			}
		}

		if runState.postChildPacketActive && strings.TrimSpace(req.Tool) != workspace.ToolNameTask && workspace.IsReadTool(strings.TrimSpace(req.Tool)) && strings.TrimSpace(result.Error) == "" {
			runState.lifecycle.closure.postChildFollowupReads++
			if runState.lifecycle.closure.postChildFollowupReads > runState.lifecycle.closure.postChildDiminishingNudgeStage {
				if nudge := postChildDiminishingReturnsNudge(r.state.runtimeBrief.Objective, runState.collectedResults, runState.lifecycle.closure.postChildFollowupReads); nudge != "" {
					runState.lifecycle.closure.postChildDiminishingNudgeStage = runState.lifecycle.closure.postChildFollowupReads

					runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_diminishing_returns_nudge", Content: nudge})
				}
			}

		}

		if strings.TrimSpace(result.Error) != "" {
			// Track same-stem tool failures only to provide one advisory
			// recovery nudge. The normal no-progress, exact-fingerprint,
			// doom-loop, and tool-step owners decide when execution stops.
			if runState.executionState.noteToolFailure(req, result) >= 3 {
				if !runState.executionState.softFailureNudgeUsed {
					if nudge := r.buildRejectionRecoveryNudge(req, runState.collectedResults); nudge != "" {
						runState.executionState.softFailureNudgeUsed = true

						runState.turnMessages = append(runState.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})

						runState.executionState.consecutiveToolFailures = 0
					}
				}
			}
		} else {
			runState.executionState.noteToolFailure(req, result)
		}

		if runState.policy.shouldStopForNoProgress(runState.lifecycle.progress.consecutiveNoProgress) {
			return finishNoProgressBudgetExhausted(runState.collectedResults)
		}
	}

	if blocker, ok := r.implementationContractCloseoutBlocker(runState.collectedResults, "implementation_tool_step_budget_blocked"); ok {
		return finish(blocker, nil, "implementation_tool_step_budget_blocked", true)
	}

	answer, err = controller.completeWithoutMoreToolsWithReason(maxToolStepsExhaustedReason())

	return finish(answer, err, "tool_step_budget_exhausted", true)
}
