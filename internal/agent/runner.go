package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"idiom/internal/frontier"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

var (
	stableHandlePathLinePattern = regexp.MustCompile(`(?m)^f\d+\s+(.+)$`)
	stableHandleInlinePattern   = regexp.MustCompile(`\bf\d+\s+((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+)\b`)
)

type Runner struct {
	Client *llama.Client
	// ImplementerClient, when non-nil, is used for write-enabled child
	// tasks (isolated workbranch children). Read-only children and the
	// parent loop stay on Client. When nil, all child work uses Client.
	ImplementerClient     *llama.Client
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
	// InstructionAttacher injects per-directory AGENTS.md / CLAUDE.md
	// guidance whenever the model touches a file under that directory.
	// Mirrors OpenCode's session/instruction.ts behavior.
	InstructionAttacher *InstructionAttacher
	// DoomLoopGate, if non-nil, is called when the model emits three identical
	// consecutive tool calls. Returning true approves the call (resets the
	// streak and lets execution continue); returning false rejects it and
	// terminates the turn. When nil the runner falls back to a hard rejection.
	DoomLoopGate         func(req workspace.ToolRequest, count int) bool
	LastAssistantMessage llama.ChatMessage
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
	executionState := newRunnerExecutionState(r.InitialNeedsNarrowing, r.InitialBroadInspects)
	// turnHiddenTools tracks tools that have been gated enough times this
	// turn that we should stop advertising them in the manifest. Removes
	// a class of waste outright: if list_files is chained past the gate,
	// hiding it for the rest of the turn prevents another round of token
	// spend on calls we're going to reject anyway.
	turnHiddenTools := make(map[string]bool)
	forceOnlyTaskTool := false
	rebuildTools := func() {
		if r.Workspace == nil {
			return
		}
		allowed := r.advertisedAllowedTools(r.Workspace.AllowedTools())
		if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "read_only_verification_shell") && !executionState.verificationAttempted {
			filtered := make([]string, 0, 1)
			for _, t := range allowed {
				if t == "shell" {
					filtered = append(filtered, t)
				}
			}
			allowed = filtered
		}
		if forceOnlyTaskTool {
			filtered := make([]string, 0, 2)
			for _, t := range allowed {
				if t == "task" {
					filtered = append(filtered, t)
				}
			}
			allowed = filtered
		}
		if len(turnHiddenTools) > 0 {
			filtered := make([]string, 0, len(allowed))
			for _, t := range allowed {
				if !turnHiddenTools[t] {
					filtered = append(filtered, t)
				}
			}
			allowed = filtered
		}
		opts.Tools = buildLlamaTools(workspace.BuildToolDefinitions(allowed))
	}
	if r.Workspace != nil {
		rebuildTools()
	}
	if strings.TrimSpace(opts.ToolChoice) == "" && len(opts.Tools) > 0 {
		opts.ToolChoice = "auto"
	}
	policy := r.effectivePolicy()
	metrics := runnerMetrics{startedAt: time.Now()}
	stopReason := "completed"
	budgetExhausted := false
	defer func() {
		r.emitBudgetLedger(policy, metrics, stopReason, budgetExhausted)
	}()
	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		if strings.TrimSpace(reason) != "" {
			stopReason = reason
		}
		budgetExhausted = exhausted
		return value, finishErr
	}
	var lastToolKey string
	var lastToolResult *workspace.ToolResult
	// activeToolCall points at the native tool_call we're currently
	// responding to (when the model sent one). Turn helpers consult it to
	// emit role:"tool" + tool_call_id responses instead of
	// role:"system" text. Reset to nil at the top of every iteration.
	var activeToolCall *llama.ToolCall
	replayedToolRequests := make(map[string]int)
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	declarationMovesForbidden := declarationMovesForbiddenByMessages(turnMessages)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	consecutiveNoProgress := 0
	discoveryRedirect := NewDiscoveryRedirect()
	taskLedger := newTaskLedgerState(r.TaskLedger)
	r.RuntimeBrief = r.runtimeBriefWithLiveState(policy, turnMessages, collectedResults)
	if initialBrief := workerruntime.RenderInitialRuntimeBrief(r.RuntimeBrief); strings.TrimSpace(initialBrief) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: initialBrief})
	}
	if bootstrap := r.initialBootstrapInstruction(); strings.TrimSpace(bootstrap) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: bootstrap})
	}
	r.emitControlRails(turnMessages)
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

	// currentFingerprint is the fingerprint of the in-flight tool call,
	// set at the top of each loop iteration. The turn controller reads it
	// via pointer so every rejection path can record the
	// fingerprint in rejectedFingerprints — letting a later pre-execute
	// guard short-circuit a byte-identical retry of a rejected call.
	var currentFingerprint string
	controller := runnerTurnController{
		runner:                     r,
		ctx:                        ctx,
		opts:                       &opts,
		metrics:                    &metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collectedResults,
		rejectedActions:            rejectedActions,
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejectedActions,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}

	if req, ok := r.verificationBootstrapRequest(); ok && r.Workspace != nil && len(collectedResults) == 0 {
		raw := marshalToolRequestRaw(req)
		metrics.ToolCalls++
		if onToolRequest != nil {
			profile := workspace.DiscoveryProfileForRequest(req)
			onToolRequest(ToolRequestEvent{
				Step:           1,
				Agent:          r.agentName(),
				Raw:            raw,
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
		result := r.Workspace.Execute(ctx, req)
		recorder.recordWithOptions(1, req, result, "verification_auto_bootstrap", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		update := executionState.applyResult(req, result, toolRequestKey(req), r.Workspace, &requestBudget)
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		answer := synthesizeVerificationOnlyAnswer(req, result)
		r.emitAcceptedAnswerSignals(answer, collectedResults)
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		if policy.shouldStopForDuration(metrics.startedAt) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Run duration budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.")
			return finish(answer, err, "duration_budget_exhausted", true)
		}
		if policy.shouldStopForModelCalls(metrics.ModelCalls) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Model-call budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.")
			return finish(answer, err, "model_call_budget_exhausted", true)
		}
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

		metrics.ModelCalls++
		modelStart := time.Now()
		assistantMessage, err := r.Client.CompleteChatMessage(ctx, turnMessages, opts)
		if err != nil {
			return finish("", err, "model_error", false)
		}
		metrics.addUsage(assistantMessage.Usage)
		r.emitModelCallTrace(stepNumber, "main_loop", turnMessages, assistantMessage, time.Since(modelStart).Milliseconds())
		reply := assistantMessage.Content
		r.emitThinkingTrace(stepNumber, "model_reply", assistantMessage, opts, false)

		activeToolCall = nil
		var (
			req      workspace.ToolRequest
			raw      string
			ok       bool
			parseErr error
		)
		if len(assistantMessage.ToolCalls) > 0 {
			call := assistantMessage.ToolCalls[0]
			activeToolCall = &call
			req, ok, parseErr = parseToolCall(call)
			raw = call.Function.Arguments
		} else {
			req, raw, ok, parseErr = workspace.ParseToolRequest(reply)
		}
		err = parseErr
		if err != nil {
			controller.recordParseFailure(stepNumber, activeToolCall, raw, err)
			continue
		}
		if !ok {
			if result, rejected := r.completionGateRejection(reply, collectedResults, stepNumber, taskLedger, declarationMovesForbidden); rejected {
				if completionGateRejections >= 3 {
					answer, err := controller.completeWithoutMoreToolsWithReason("Completion gate has rejected the final answer repeatedly. Do not request any more tools. Summarize the concrete blocker and the next action required.")
					return finish(answer, err, "completion_gate_repeated_rejection", true)
				}
				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, opts, collectedResults, &metrics)
			r.emitAcceptedAnswerSignals(answer, collectedResults)
			return finish(answer, err, "model_final", false)
		}
		preflight := r.applyLanePreflight(runnerLanePreflightInput{
			Request:               req,
			Raw:                   raw,
			Results:               collectedResults,
			VerificationAttempted: executionState.verificationAttempted,
		})
		req = preflight.Request
		raw = preflight.Raw
		if preflight.RejectSummary != "" {
			if preflight.ForceAnswer != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: preflight.RejectSummary,
					Error:   preflight.RejectError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				if answer, ok := r.directForcedAnswer(preflight.ForceAnswer, collectedResults); ok {
					r.emitAcceptedAnswerSignals(answer, collectedResults)
					return finish(answer, nil, preflight.ForceAnswer.Reason, true)
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

		turnMessages = append(turnMessages, assistantMessage)
		r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantMessage, opts, true)
		metrics.ToolCalls++
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

		toolKey := toolRequestKey(req)
		// DOOM_LOOP guard — if the model emits exactly the same tool + args
		// three turns in a row, stop the auto-execute path and surface a
		// concrete blocker to the user. OpenCode uses threshold=3 at
		// session/processor.ts; we match that. Counting includes accepted
		// calls (not just replays/rejections) because the pathology is the
		// model getting stuck even when the harness doesn't push back.
		currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		identicalCallRun := requestBudget.noteFingerprint(currentFingerprint)
		if identicalCallRun >= 3 {
			if r.DoomLoopGate != nil {
				r.emitEvent(RunEvent{Role: "doom_loop_guard", Content: fmt.Sprintf("model repeated %s %d times — approval required", req.Tool, identicalCallRun)})
				if r.DoomLoopGate(req, identicalCallRun) {
					// User approved — reset the streak and let this call execute.
					requestBudget.identicalCallRun = 0
				} else {
					// User denied — feed a blocker result and end the turn.
					answer, err, _ := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						"Doom-loop guard: user denied continuation",
						fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; user denied continuation. Summarize the evidence already gathered and the concrete blocker.", req.Tool, identicalCallRun),
					)
					return finish(answer, err, "doom_loop_guard", true)
				}
			} else {
				answer, err, _ := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Doom-loop guard tripped",
					fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row — stopping to prevent budget exhaustion. Summarize the evidence already gathered and the concrete blocker.", req.Tool, identicalCallRun),
				)
				return finish(answer, err, "doom_loop_guard", true)
			}
		}
		if decision := requestBudget.fingerprintDecision(policy, req, currentFingerprint); decision.Handled {
			if decision.Nudge != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
			}
			if decision.EventRole != "" {
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: firstNonEmpty(decision.EventContent, decision.Nudge)})
			}
			if decision.ForceAnswerPrompt != "" {
				answer, err := controller.completeWithoutMoreToolsWithReason(decision.ForceAnswerPrompt)
				return finish(answer, err, decision.ForceAnswerReason, true)
			}
			if decision.RejectSummary != "" {
				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, decision.RejectSummary, decision.RejectError)
				if done {
					return finish(answer, err, decision.RejectStopReason, true)
				}
				continue
			}
		}
		if decision := requestBudget.explorationDecision(r, req, collectedResults); decision.Handled {
			if decision.Nudge != "" {
				forceOnlyTaskTool = true
				rebuildTools()
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
			}
			if decision.EventRole != "" {
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: firstNonEmpty(decision.EventContent, decision.Nudge)})
			}
			if decision.RejectSummary != "" {
				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, decision.RejectSummary, decision.RejectError)
				if done {
					return finish(answer, err, decision.RejectStopReason, true)
				}
				continue
			}
		}
		if decision := requestBudget.synthesisDecision(policy, metrics.ToolCalls); decision.Handled {
			if decision.Nudge != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
			}
			if decision.EventRole != "" {
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: firstNonEmpty(decision.EventContent, decision.Nudge)})
			}
		}
		if isReadClassTool(req) {
			requestBudget.noteReadClassRequest()
			metrics.ReadClassCalls++
			if decision := requestBudget.readBudgetDecision(policy, req); decision.Handled {
				if decision.EventRole != "" {
					r.emitEvent(RunEvent{Role: decision.EventRole, Content: firstNonEmpty(decision.EventContent, decision.Nudge)})
				}
				if decision.Nudge != "" {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
				}
				if decision.RejectSummary != "" {
					answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, decision.RejectSummary, decision.RejectError)
					if done {
						return finish(answer, err, decision.RejectStopReason, true)
					}
				}
				if decision.ForceAnswerPrompt != "" {
					answer, err = controller.completeWithoutMoreToolsWithReason(decision.ForceAnswerPrompt)
					return finish(answer, err, decision.ForceAnswerReason, true)
				}
				if decision.RejectSummary != "" {
					continue
				}
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
		if actionClass != "" && rejectedActions[actionClass] > 0 {
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
				"explore mode already has a workspace overview for this turn. Use the existing overview plus search_files, search_content, or read_file on discovered handles instead of requesting another broad listing.",
			)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(req.Tool), "list_files") && executionState.consecutiveListFiles >= 2 {
			// Hide list_files from the manifest for the rest of the turn
			// — at this point we've told the model "stop walking dirs"
			// once already, and keeping list_files in the tool list just
			// invites another rejected call. Mode-gating the tool out is
			// more effective than rejecting each retry with a message the
			// model already ignored.
			if !turnHiddenTools["list_files"] {
				turnHiddenTools["list_files"] = true
				rebuildTools()
				r.emitEvent(RunEvent{Role: "tool_hidden", Content: "list_files hidden for rest of turn after chained-gate fire"})
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Chained list_files rejected",
				fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files has been removed from your available tools for the remainder of this turn. Use search_content with keywords from the user's question, read_file on a path you already discovered, or a language-specific symbol tool (go_workspace_symbol, go_references) instead.", executionState.consecutiveListFiles),
			)
			if done {
				return finish(answer, err, "list_files_chain_budget_exhausted", true)
			}
			continue
		}
		if executionState.shouldConsumeSearchHitBeforeMoreSearch(req) {
			if tool := strings.TrimSpace(req.Tool); (tool == "search_files" || tool == "search_content") && !turnHiddenTools[tool] {
				turnHiddenTools[tool] = true
				rebuildTools()
				r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after chained-search gate fire", tool)})
			}
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
			errorText := "you already have a broad workspace overview. Next use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
			if r.Workspace.IsExploreMode() {
				errorText = "you already have a broad workspace overview. In explore mode you must narrow next with search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
			}
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, "Broad inspection request rejected", errorText)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
			if lastToolResult != nil {
				if replayedToolRequests[toolKey] > 0 {
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						"Repeated tool request rejected",
						"this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker.",
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
					answer, err := controller.completeWithoutMoreToolsWithReason("Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Do not request any more tools. Summarize the blocker and the narrowest remaining next action.")
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			errorText := "you just ran the same tool request. Do not loop on broad inspection. Use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command to narrow, or answer with what you already know."
			if strings.TrimSpace(req.Tool) == "go_move_declarations" {
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
		if permissionDecision.Decision != PermissionAllow {
			r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))
			result := permissionDecision.ToolResult()
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "permission_denied", runnerRecordOptions{
				CountRejected:  true,
				SetLastToolKey: true,
			})
			if permissionDecision.Decision == PermissionAsk {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "permission_approval_required", false)
			}
			continue
		}
		r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))
		if strings.TrimSpace(req.Tool) == "task" {
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
				requestBudget.broadEvidenceDelegationSatisfied = true
				requestBudget.readOnlyChildTaskSatisfied = true
				forceOnlyTaskTool = false
				hideBroadDiscoveryTools(turnHiddenTools)
				turnHiddenTools["task"] = true
				rebuildTools()
				requestBudget.postChildReadCalls = 0
				nudge := "The delegated read_only child result is now the evidence packet. Treat it as inspected evidence; do not restart broad discovery in the parent. Use only a few targeted reads for named gaps, then answer, write, verify, or report the blocker."
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
			}
			recorder.record(stepNumber, req, result, "child_task_result", true, true)
			if shouldAnswerDirectlyFromChildPacket(r.RuntimeBrief, result) {
				answer := strings.TrimSpace(result.Output)
				if answer == "" {
					if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
						answer = fallback
					}
				}
				if answer != "" {
					answer = annotateScopedFallbackAnswer(answer, r.RuntimeBrief.Objective)
					r.emitAcceptedAnswerSignals(answer, collectedResults)
					return finish(answer, nil, "child_packet_answered", false)
				}
			}
			if strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Output) != "" &&
				!runnerBriefRequiresMutation(r.RuntimeBrief) && !runnerVerificationRequired(r.RuntimeBrief) {
				answer, err := controller.completeWithoutMoreToolsWithReason(strings.Join([]string{
					"Use the delegated child packet as the evidence source.",
					"Do not request any more tools.",
					"Answer the user's question directly with the mechanism, inspected repository-relative paths, and confidence.",
					"Do not use a partial-findings template.",
					"Do not mention budget exhaustion unless the packet explicitly leaves a named unresolved gap.",
				}, "\n"))
				r.emitAcceptedAnswerSignals(answer, collectedResults)
				return finish(answer, err, "child_packet_synthesized_answer", false)
			}
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Do not request any more tools. Summarize the work completed, the child-task result, and the narrowest remaining next action.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "task_ledger" {
			result := taskLedger.apply(req)
			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", true, true)
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}

		if decision := discoveryRedirect.Consider(req, r.Workspace); decision.ShouldRedirect {
			// Hard-reject path: the model has been nudged enough times
			// on this redirect class. Route through rejectTool so the
			// fingerprint is recorded in rejectedFingerprints and a
			// repeat terminates the turn. The soft-redirect path stays
			// as a result append with no rejection counters bumped.
			if decision.HardReject {
				if strings.TrimSpace(decision.DecisionGuide) != "" {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
					r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
				}
				r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s) HARD_REJECT", req.Tool, decision.Language, decision.Capability)})
				// Hide the source tool from the manifest for the rest
				// of the turn. If shell was redirected to a structured
				// tool N times and now hard-rejected, the model has
				// shown it won't self-correct — remove the tool.
				if tool := strings.TrimSpace(req.Tool); tool != "" && !turnHiddenTools[tool] {
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
			recorder.record(stepNumber, req, result, "discovery_redirect", false, false)
			if strings.TrimSpace(decision.DecisionGuide) != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
				r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
			}
			r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s)", req.Tool, decision.Language, decision.Capability)})
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
				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					answer, err := controller.completeWithoutMoreTools()
					return finish(answer, err, "write_approval_required", false)
				}
				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && consecutiveRejectedActions >= 2 {
					answer, err := controller.completeWithoutMoreTools()
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}
				if bootstrapReadReminder && policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Do not request more tools. Summarize the blocker and the narrowest remaining action.")
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
			if consecutiveRejectedActions >= 2 {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "write_description_budget_exhausted", true)
			}
			continue
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

		if executionState.shouldRequireVerification(req) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Verification required after workspace edits",
				fmt.Sprintf("the workspace has %d successful write(s) since the last verification. Run a focused verification command (for example go test ./..., package tests, diagnostics, or the relevant build/test command) before more inspection or edits, or answer with the concrete blocker.", executionState.writesSinceVerification),
			)
			if done {
				return finish(answer, err, "verification_required_after_writes", true)
			}
			continue
		}

		result := r.Workspace.Execute(ctx, req)
		recorder.activeToolCall = activeToolCall
		recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		update := executionState.applyResult(req, result, currentFingerprint, r.Workspace, &requestBudget)
		if update.ResetVerificationGuards {
			clearVerificationGateFingerprints(requestBudget.rejectedFingerprints)
			clearRunnerStringIntMap(rejectedActions)
			rebuildTools()
		}
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		r.attachInstructionsForTouchedPaths(&turnMessages, req, result)
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
					if nudge := buildRejectionRecoveryNudge(req, collectedResults, r.Workspace, r.RuntimeBrief); nudge != "" {
						executionState.softFailureNudgeUsed = true
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})
						executionState.consecutiveToolFailures = 0
					}
				} else {
					// Soft nudge already fired. Another 3-failure streak
					// on the same stem means the model isn't absorbing
					// the recovery hint. Terminate with what we have.
					answer, err := controller.completeWithoutMoreToolsWithReason(fmt.Sprintf("Three more %s failures on %s after the recovery nudge - the model is not recovering. Do not request any more tools. Summarize the blocker and the narrowest remaining next action using the evidence you already have.", req.Tool, stem))
					return finish(answer, err, "tool_failure_streak_exhausted", true)
				}
			}
		} else {
			executionState.noteToolFailure(req, result)
		}
		if executionState.noteMissingPathListFailure(r.Workspace.IsExploreMode(), req, result) >= 2 {
			answer, err := controller.completeWithoutMoreToolsWithReason("Exploration is probing conventional paths that do not exist in this repository. Do not request any more tools. Answer from the bootstrapped evidence and state the narrowest remaining validation step.")
			return finish(answer, err, "missing_path_probe_budget_exhausted", true)
		}
		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
			return finish(answer, err, "no_progress_budget_exhausted", true)
		}
	}

	answer, err = controller.completeWithoutMoreToolsWithReason(maxToolStepsExhaustedReason())
	return finish(answer, err, "tool_step_budget_exhausted", true)
}

// maxToolStepsExhaustedReason mirrors OpenCode's max-steps.txt: when the
// per-turn tool-step budget is spent, tools are disabled and the model must
// synthesize a text-only reply now from the evidence already gathered. The
// wording is deliberately blunt about both constraints — no more tool calls,
// and no empty responses — to steer small local models away from the
// "keep probing" loop that produced the empty-answer failure in
// logs/conversations/20260421-053044.479.jsonl.
func maxToolStepsExhaustedReason() string {
	return "Maximum tool steps for this turn have been reached. Tools are now disabled — do NOT emit another tool block. Respond with a concise, direct, text-only answer synthesized from the evidence already gathered. If a concrete answer is not yet possible, state the narrowest remaining blocker and the next action you would take. Do not leave the response blank."
}

func replayRepeatedToolRequest(req workspace.ToolRequest, source workspace.ToolResult) workspace.ToolResult {
	summary := strings.TrimSpace(source.Summary)
	if summary == "" {
		if strings.TrimSpace(source.Error) != "" {
			summary = "Repeated tool request replayed the same blocker"
		} else {
			summary = "Repeated tool request reused prior evidence"
		}
	} else if strings.TrimSpace(source.Error) != "" {
		summary += " (replayed same blocker)"
	} else {
		summary += " (replayed prior evidence)"
	}
	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  source.Output,
		Error:   source.Error,
	}
}

func (r *Runner) emitThinkingTrace(step int, reason string, message llama.ChatMessage, opts llama.ChatOptions, preservedInHistory bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	if opts.EnableThinking == nil && !opts.PreserveThinking && strings.TrimSpace(message.ReasoningContent) == "" {
		return
	}
	trace := runnerThinkingTrace{
		Lane:               r.agentName(),
		Step:               step,
		Reason:             strings.TrimSpace(reason),
		EnableThinking:     thinkingOptionForLog(opts.EnableThinking),
		PreserveThinking:   opts.PreserveThinking,
		ContentChars:       len(message.Content),
		ReasoningChars:     len(message.ReasoningContent),
		PreservedInHistory: preservedInHistory && strings.TrimSpace(message.ReasoningContent) != "",
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "thinking_trace", Content: string(data)})
}

func thinkingOptionForLog(enableThinking *bool) string {
	if enableThinking == nil {
		return "auto"
	}
	if *enableThinking {
		return "on"
	}
	return "off"
}

func (r *Runner) emitNextActionState(step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	state := buildRunnerNextActionState(r.agentName(), step, reason, req, result, results, declarationMovesForbidden, r.RuntimeBrief)
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "next_action_state", Content: string(data)})
}

func (r *Runner) renderStepInstruction(policy RunPolicy, messages []llama.ChatMessage, step int, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) string {
	if r == nil {
		return ""
	}
	brief := r.runtimeBriefWithLiveState(policy, messages, results)
	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    "current_state",
		Request:                   req,
		Result:                    result,
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
	})
	failureClass := ""
	if strings.TrimSpace(result.Error) != "" {
		failureClass = workerruntime.ClassifyFailure("current_state", req, result)
	}
	state := workerruntime.RunState{
		Lane:           r.agentName(),
		Phase:          runnerLivePhase(brief, req, result, results),
		Step:           step,
		EvidencePaths:  pinnedPathsFromToolResults(results, 6),
		ChangedPaths:   mapKeys(changedPathsFromToolResults(results)),
		Constraints:    mergeRunnerStateStrings(brief.Constraints, guidance.Constraints),
		LastTool:       firstNonEmpty(strings.TrimSpace(req.Tool), strings.TrimSpace(result.Tool)),
		LastResult:     workerruntime.SummarizeToolResult(result),
		FailureClass:   failureClass,
		NextActions:    mergeRunnerStateStrings(brief.RequiredNext, guidance.ValidNext),
		InvalidActions: mergeRunnerStateStrings(brief.InvalidActions, guidance.InvalidNext),
	}
	// After step 3 the static scaffolding (tool_manifest, allowed/forbidden
	// tools, readiness gate, search policy, stop conditions) is already in
	// the model's context — repeating it each step just burns tokens. Emit
	// only the mutable fields from step 4 on.
	if step <= 3 {
		state = workerruntime.MergeRuntimeBriefIntoState(brief, state)
	} else {
		state = workerruntime.MergeRuntimeBriefIntoStateDelta(brief, state)
	}
	return workerruntime.RenderStepInstruction(workerruntime.BuildStepInstruction(state))
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
	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps:       policy.MaxToolSteps,
			MaxModelCalls:      policy.MaxModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		}
	}
	brief.EvidencePaths = mergeRunnerStateStrings(brief.EvidencePaths, pinnedPathsFromToolResults(results, 8))
	brief.ChangedPaths = mergeRunnerStateStrings(brief.ChangedPaths, mapKeys(changedPathsFromToolResults(results)))
	return runnerBriefWithPhaseHints(brief, results)
}

func runnerBriefWithPhaseHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	switch phase := debugLogPhase(brief, results); phase {
	case "latest_log_selection":
		brief.Constraints = mergeRunnerStateStrings([]string{"latest_log_selection_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"select the newest real log artifact before inspecting source files",
			"prefer log directories and log extensions over source files whose names merely mention logging",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source-code inspection before selecting a concrete log artifact",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer newest actual log artifacts over source files with log-like names",
		}, brief.SearchPolicy)
	case "latest_log_tail":
		brief.Constraints = mergeRunnerStateStrings([]string{"latest_log_tail_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"read the newest selected log tail now",
			"stay on the selected log until you can cite a weird decision or declare the missing datum",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"additional workspace discovery before the first selected-log read",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"read the selected log tail before any deeper search",
		}, brief.SearchPolicy)
	case "selected_log_analysis":
		brief.Constraints = mergeRunnerStateStrings([]string{"selected_log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"answer from the selected log if the weird decision is already clear",
			"otherwise stay inside the selected log with one targeted search_content before answering",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source-code inspection outside the selected log before answering",
			"another blind read_file on the selected log before a concrete hit",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"stay inside the selected log until the answer is supported or the gap is explicit",
		}, brief.SearchPolicy)
	case "failure_window":
		brief.Constraints = mergeRunnerStateStrings([]string{"failure_window_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"read the bounded window around the concrete failure hit now",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"more log searching before reading the failure window",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"extract the failure window immediately after a concrete hit",
		}, brief.SearchPolicy)
	case "log_analysis":
		brief.Constraints = mergeRunnerStateStrings([]string{"log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"answer from the gathered log evidence now unless one specific missing datum remains",
		}, brief.RequiredNext)
	}
	switch phase := evidenceFirstMutationPhase(brief, results); phase {
	case "source_evidence":
		brief.Constraints = mergeRunnerStateStrings([]string{"source_evidence_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"inspect one non-target implementation file or symbol before the named target",
			"after concrete non-target evidence, read one bounded window of the named target",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"open-ended read_file on named target refs before source evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer semantic inspection of discovered implementation files before target reads",
		}, brief.SearchPolicy)
	case "target_entry":
		brief.Constraints = mergeRunnerStateStrings([]string{"first_target_read_pending"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"read one bounded window of the named target now",
			"stay on the named target for the next step instead of resuming source discovery",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"additional source discovery before the first target read",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"enter the named target as soon as concrete source evidence exists",
		}, brief.SearchPolicy)
	case "target_inspection":
		brief.Constraints = mergeRunnerStateStrings([]string{"target_inspection_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"make the edit in the named target if the change is already clear",
			"otherwise use search_content or a narrow read_file scoped to the named target only",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source discovery outside the named target during target inspection",
			"open-ended read_file on the named target",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer target-scoped follow-up over renewed source discovery",
		}, brief.SearchPolicy)
	case "target_write":
		brief.Constraints = mergeRunnerStateStrings([]string{"target_write_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"edit_file in the named target now or report a concrete blocker",
			"if one exact anchor is missing, stay inside the named target with search_content or a narrow read_file",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source discovery outside the named target after target inspection",
			"broad workspace discovery after target inspection",
			"open-ended read_file on the named target",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"finish from the named target instead of reopening discovery",
		}, brief.SearchPolicy)
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

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
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
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
	if targetReadCount < maxEvidenceFirstTargetReadWindows {
		return "target_inspection"
	}
	return "target_write"
}

func buildRunnerNextActionState(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerNextActionState {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	brief = runnerBriefWithPhaseHints(brief, results)
	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
	})
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Phase:             runnerLivePhase(brief, req, result, results),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          tool,
		LastTarget:        workerruntime.ToolRequestTarget(req),
		ResultSummary:     truncateRunnerStateText(result.Summary, 220),
		ResultError:       truncateRunnerStateText(result.Error, 260),
		ResultOutputChars: len(result.Output),
		EvidencePaths:     pinnedPathsFromToolResults(results, 6),
	}
	state.Constraints = mergeRunnerStateStrings(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = mergeRunnerStateStrings(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = mergeRunnerStateStrings(brief.RequiredNext, guidance.ValidNext)
	state.StateSummary = runnerStateSummary(state)
	return state
}

func buildRunnerFailureFeedbackPacket(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerFailureFeedbackPacket {
	state := buildRunnerNextActionState(agent, step, reason, req, result, results, declarationMovesForbidden, brief)
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		EvidencePaths:             state.EvidencePaths,
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
	})
	packet := runnerFailureFeedbackPacket{
		Lane:               state.Lane,
		Step:               state.Step,
		Reason:             state.Reason,
		FailedTool:         state.LastTool,
		FailedTarget:       state.LastTarget,
		FailureClass:       recovery.FailureClass,
		ResultSummary:      state.ResultSummary,
		ResultError:        state.ResultError,
		InvalidNext:        mergeRunnerStateStrings(state.InvalidNextActions, recovery.InvalidNext),
		ValidNext:          mergeRunnerStateStrings(state.NextValidActions, recovery.ValidNext),
		StateSummary:       firstNonEmpty(recovery.StateSummary, state.StateSummary),
		UserVisibleBlocker: recovery.UserVisibleBlocker,
	}
	packet.DoNotRepeat = mergeRunnerStateStrings(recovery.DoNotRepeat, packet.InvalidNext)
	packet.RequiredNext = mergeRunnerStateStrings(recovery.RequiredNext, packet.ValidNext)
	return packet
}

func renderRunnerFailureFeedbackPacket(packet runnerFailureFeedbackPacket) string {
	return workerruntime.RenderFailureFeedbackPacket(packet)
}

func mergeRunnerStateStrings(groups ...[]string) []string {
	var out []string
	for _, group := range groups {
		for _, value := range group {
			out = appendUniqueRunnerState(out, value)
		}
	}
	return out
}

func runnerStateSummary(state runnerNextActionState) string {
	parts := []string{state.Reason}
	if state.LastTool != "" {
		if state.LastTarget != "" {
			parts = append(parts, state.LastTool+" "+state.LastTarget)
		} else {
			parts = append(parts, state.LastTool)
		}
	}
	if state.ResultError != "" {
		parts = append(parts, "blocked: "+state.ResultError)
	} else if state.ResultSummary != "" {
		parts = append(parts, state.ResultSummary)
	}
	return truncateRunnerStateText(strings.Join(parts, " | "), 320)
}

func appendUniqueRunnerState(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func truncateRunnerStateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	if max <= 3 {
		return value[:max]
	}
	return strings.TrimSpace(value[:max-3]) + "..."
}

func (r *Runner) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	ledger := runnerBudgetLedger{
		Lane:                     r.agentName(),
		UsedModelCalls:           metrics.ModelCalls,
		MaxModelCalls:            policy.MaxModelCalls,
		UsedToolResults:          metrics.ToolCalls,
		PromptTokens:             metrics.PromptTokens,
		CompletionTokens:         metrics.CompletionTokens,
		TotalTokens:              metrics.TotalTokens,
		ProgressTurns:            metrics.ProgressTurns,
		RejectedActions:          metrics.RejectedActions,
		CompletionGateRejections: metrics.CompletionGateRejections,
		ContextCompactions:       metrics.Compactions,
		ReadClassCalls:           metrics.ReadClassCalls,
		DurationMillis:           time.Since(metrics.startedAt).Milliseconds(),
		StopReason:               strings.TrimSpace(stopReason),
		BudgetExhausted:          exhausted,
	}
	if policy.MaxToolSteps > 0 {
		ledger.MaxToolSteps = policy.MaxToolSteps
	}
	if policy.ReadBudgetHard > 0 {
		ledger.MaxReadBudget = policy.ReadBudgetHard
	}
	if exhausted {
		ledger.NextAllowedActions = []string{"answer_with_explicit_uncertainty", "ask_user_for_narrower_scope", "resume_with_fresh_budget"}
		ledger.UserVisibleUncertainty = "state that a runner safety budget was exhausted and identify unresolved gaps"
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "budget_ledger", Content: string(data)})
}

func (r *Runner) emitModelCallTrace(step int, label string, prompt []llama.ChatMessage, reply llama.ChatMessage, durationMs int64) {
	if r == nil || r.OnEvent == nil {
		return
	}
	chosen := "<answer>"
	if len(reply.ToolCalls) > 0 && strings.TrimSpace(reply.ToolCalls[0].Function.Name) != "" {
		chosen = strings.TrimSpace(reply.ToolCalls[0].Function.Name)
	}
	roleChars := make(map[string]int, 4)
	for _, m := range prompt {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "unknown"
		}
		roleChars[role] += len(m.Content)
	}
	payload := map[string]any{
		"step":              step,
		"label":             label,
		"agent":             r.agentName(),
		"prompt_tokens":     reply.Usage.PromptTokens,
		"completion_tokens": reply.Usage.CompletionTokens,
		"total_tokens":      reply.Usage.TotalTokens,
		"ctx_size":          r.ContextSize,
		"message_count":     len(prompt),
		"role_chars":        roleChars,
		"chosen_tool":       chosen,
		"duration_ms":       durationMs,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "model_call", Content: string(data)})
}

func (r *Runner) emitEvent(event RunEvent) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(event.Role) == "" {
		return
	}
	r.OnEvent(event)
}

func (r *Runner) compactTurnMessagesIfNeeded(messages *[]llama.ChatMessage, policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}
	if policy.MaxContextMessages <= 0 && policy.MaxContextChars <= 0 {
		return false
	}
	current := *messages
	tooManyMessages := policy.MaxContextMessages > 0 && len(current) > policy.MaxContextMessages
	tooManyChars := policy.MaxContextChars > 0 && chatMessagesCharCount(current) > policy.MaxContextChars
	if !tooManyMessages && !tooManyChars {
		return false
	}
	if len(current) <= 3 {
		return false
	}

	prefixEnd := runnerCompactionPrefixEnd(current)
	tailCount := 6
	if maxTail := len(current) - prefixEnd - 2; maxTail < tailCount {
		tailCount = maxTail
	}
	if tailCount < 2 {
		tailCount = 2
	}
	tailStart := len(current) - tailCount
	if tailStart < prefixEnd {
		tailStart = prefixEnd
	}
	summary := llama.ChatMessage{
		Role:    "system",
		Content: r.runnerCompactionSummary(policy, ledger, results),
	}
	compacted := make([]llama.ChatMessage, 0, prefixEnd+1+len(current)-tailStart)
	compacted = append(compacted, current[:prefixEnd]...)
	compacted = append(compacted, summary)
	compacted = append(compacted, current[tailStart:]...)
	if len(compacted) >= len(current) {
		return false
	}
	*messages = compacted
	r.emitContextCompaction(runnerContextCompaction{
		Lane:             r.agentName(),
		DroppedMessages:  len(current) - len(compacted),
		RetainedMessages: len(compacted),
		MaxHistory:       policy.MaxContextMessages,
		MaxChars:         policy.MaxContextChars,
		PinnedPaths:      pinnedPathsFromToolResults(results, 8),
		PinnedMemoryKind: "runner_task_state",
		Strategy:         "retain_system_and_user_objective_with_task_ledger_recent_tools_and_blockers",
	})
	return true
}

func (r *Runner) emitContextCompaction(compaction runnerContextCompaction) {
	if r == nil || r.OnEvent == nil {
		return
	}
	data, err := json.Marshal(compaction)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "context_compaction", Content: string(data)})
}

func (r *Runner) runnerCompactionSummary(policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("[runner context compaction]\n")
	b.WriteString("agent: ")
	b.WriteString(r.agentName())
	b.WriteString("\nstrategy: older runner turns were compacted; preserve this state when deciding the next action.\n")
	if brief := workerruntime.RenderRuntimeBriefSummary(r.runtimeBriefWithLiveState(policy, nil, results)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}
	if ledger != nil && ledger.created {
		b.WriteString("\nTask ledger:\n")
		b.WriteString(ledger.render())
		b.WriteString("\n")
	}
	if paths := pinnedPathsFromToolResults(results, 8); len(paths) > 0 {
		b.WriteString("\nPinned paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}
	if len(results) > 0 {
		b.WriteString("\nRecent tool evidence:\n")
		for _, result := range tailToolResults(results, 8) {
			b.WriteString("- ")
			b.WriteString(result.Tool)
			if strings.TrimSpace(result.Summary) != "" {
				b.WriteString(": ")
				b.WriteString(result.Summary)
			}
			if strings.TrimSpace(result.Error) != "" {
				b.WriteString(" [error: ")
				b.WriteString(result.Error)
				b.WriteString("]")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func runnerCompactionPrefixEnd(messages []llama.ChatMessage) int {
	prefixEnd := 0
	for prefixEnd < len(messages) && messages[prefixEnd].Role == "system" {
		prefixEnd++
	}
	for i := prefixEnd; i < len(messages); i++ {
		if messages[i].Role == "user" {
			return i + 1
		}
	}
	if prefixEnd > 0 {
		return prefixEnd
	}
	if len(messages) < 2 {
		return len(messages)
	}
	return 2
}

func chatMessagesCharCount(messages []llama.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent)
	}
	return total
}

func tailToolResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}
	return results[len(results)-max:]
}

func latestUserObjective(messages []llama.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}
		if content := strings.TrimSpace(messages[i].Content); content != "" {
			return truncateRunnerStateText(content, 500)
		}
	}
	return ""
}

func pinnedPathsFromToolResults(results []workspace.ToolResult, max int) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range results {
		for _, field := range []string{result.Summary, result.Output, result.Error} {
			for _, path := range pathparse.MentionedWorkspacePaths(field) {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				paths = append(paths, path)
				if max > 0 && len(paths) >= max {
					return paths
				}
			}
		}
	}
	return paths
}

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) (workspace.ToolResult, bool) {
	if taskLedger != nil {
		if result, rejected := taskLedger.completionRejection(reply); rejected {
			return result, true
		}
	}
	if result, rejected := r.mutationCompletionRejection(reply, results); rejected {
		return result, true
	}
	if r.CompletionGate == nil {
		return workspace.ToolResult{}, false
	}
	gate := r.CompletionGate(CompletionGateInput{
		AgentName: r.agentName(),
		Reply:     reply,
		Results:   append([]workspace.ToolResult(nil), results...),
		Steps:     step,
	})
	if gate.Accept {
		return workspace.ToolResult{}, false
	}
	summary := strings.TrimSpace(gate.Summary)
	if summary == "" {
		summary = "Final answer rejected"
	}
	errorText := strings.TrimSpace(gate.Error)
	if errorText == "" {
		errorText = "the answer does not satisfy the task completion gate; continue with the next tool call or state a concrete blocker"
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: summary,
		Error:   errorText,
	}, true
}

func (r *Runner) mutationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolResult{}, false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}
	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if containsAny(lowerReply, "blocked", "cannot", "can't", "no-op", "no change needed", "already") {
		return workspace.ToolResult{}, false
	}
	if len(changedPathsFromToolResults(results)) == 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present. Make the smallest necessary edit, or state a concrete blocker/no-op reason.",
		}, true
	}
	if !hasSuccessfulVerificationResult(results) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "workspace changes are present, but no successful verification result is present. Run the relevant test/build/check command, or state the concrete verification blocker.",
		}, true
	}
	return workspace.ToolResult{}, false
}

func runnerBriefRequiresMutation(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	contract := strings.ToLower(strings.TrimSpace(brief.Contract))
	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))
	return strings.Contains(family, "implement") ||
		strings.Contains(family, "edit") ||
		strings.Contains(contract, "implementation") ||
		strings.Contains(permission, "write")
}

func hasSuccessfulVerificationResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "shell", "go_diagnostics":
			return true
		}
	}
	return false
}

func hasSuccessfulTaskResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" {
			continue
		}
		if strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}

func postChildReadBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return 3
	}
	if strings.EqualFold(strings.TrimSpace(brief.Contract), "direct_answer") {
		return 0
	}
	return 2
}

func successfulReadClassResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		req := workspace.ToolRequest{Tool: strings.TrimSpace(result.Tool)}
		if isReadClassTool(req) {
			count++
		}
	}
	return count
}

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizedToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeRunnerToolRequest(req workspace.ToolRequest) workspace.ToolRequest {
	req.Path = normalizeRunnerPath(req.Path)
	req.Destination = normalizeRunnerPath(req.Destination)
	req.Workdir = normalizeRunnerPath(req.Workdir)
	for i := range req.Moves {
		req.Moves[i].Destination = normalizeRunnerPath(req.Moves[i].Destination)
	}
	return req
}

// truncForRetryReason trims a summary so the stored "why this was
// blocked" string stays small — model-facing text, not log-sized.
func truncForRetryReason(s string) string {
	s = strings.TrimSpace(s)
	const max = 160
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// isContentInspectionTool reports whether a tool actually inspects
// content (as opposed to listing directory names). Used to reset the
// consecutive-list_files streak — running search_content, read_file, a
// go_* symbol tool, or a cpp_* symbol tool is exactly the narrowing
// behavior we want to reward.
func isContentInspectionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "search_content", "search_files", "rg", "grep", "read_file",
		"go_file_outline", "go_read_symbol", "go_find_symbol", "go_find_type",
		"go_find_const", "go_workspace_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body",
		"go_related_files", "go_find_package", "go_importers", "go_diagnostics",
		"cpp_find_symbol", "cpp_read_symbol", "cpp_workspace_symbol",
		"cpp_definition", "cpp_declaration", "cpp_implementations",
		"cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_related_files", "cpp_includes", "cpp_who_includes",
		"cpp_blast_radius", "cpp_dead_symbol", "cpp_diagnostics":
		return true
	}
	return false
}

func isConcreteInspectionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file",
		"go_file_outline", "go_read_symbol", "go_definition", "go_references", "go_calls_from", "go_calls_into", "go_extract_body", "go_related_files",
		"cpp_read_symbol", "cpp_definition", "cpp_declaration", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body", "cpp_related_files", "cpp_file_outline",
		"python_read_symbol", "python_definition", "python_references", "python_calls_from", "python_calls_into", "python_related_files", "python_file_outline",
		"ts_read_symbol", "ts_definition", "ts_references", "ts_calls_from", "ts_calls_into", "ts_related_files", "ts_file_outline",
		"rust_read_symbol", "rust_definition", "rust_references", "rust_calls_from", "rust_calls_into", "rust_related_files", "rust_file_outline",
		"java_read_symbol", "java_definition", "java_references", "java_calls_from", "java_calls_into", "java_related_files", "java_file_outline":
		return true
	}
	return false
}

// isReadClassTool reports whether a tool request is read-only / inspection.
// Used to drive the plan-mode read-budget nudge. Kept explicit (allow-list)
// so mutating tools can never be miscounted. Shell is excluded because it
// can mutate even when the command looks benign.
func isReadClassTool(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "list_files", "read_file",
		"search_files", "search_content", "rg", "grep",
		"discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_type", "go_find_const",
		"go_file_outline", "go_read_symbol", "go_find_package", "go_related_files",
		"go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol",
		"cpp_related_files", "cpp_definition", "cpp_declaration", "cpp_implementations",
		"cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol",
		"cpp_workspace_symbol", "cpp_diagnostics",
		"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol",
		"python_related_files", "python_definition", "python_references", "python_implementations",
		"python_calls_from", "python_calls_into", "python_workspace_symbol", "python_diagnostics", "python_file_outline",
		"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol", "ts_related_files",
		"ts_definition", "ts_implementations", "ts_references", "ts_calls_from", "ts_calls_into",
		"ts_workspace_symbol", "ts_diagnostics", "ts_file_outline",
		"rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_read_symbol", "rust_related_files",
		"rust_definition", "rust_implementations", "rust_references", "rust_calls_from", "rust_calls_into",
		"rust_workspace_symbol", "rust_diagnostics", "rust_file_outline",
		"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files",
		"java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into",
		"java_workspace_symbol", "java_diagnostics", "java_file_outline":
		return true
	default:
		return false
	}
}

func toolActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "list_files":
		if workspace.IsScopedInspectionRequest(req) {
			return "list_files:" + normalizeActionToken(req.Path)
		}
		return "broad_inspection"
	case "shell":
		return shellActionClass(req.Command)
	case "read_file":
		if target := firstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path)); target != "" {
			return "read_file:" + normalizeActionToken(target)
		}
		return "read_file"
	case "edit_file":
		shape := strings.TrimSpace(req.OldText) + "->" + strings.TrimSpace(req.NewText)
		if req.StartLine > 0 || req.EndLine > 0 || strings.TrimSpace(req.Content) != "" {
			shape = fmt.Sprintf("%d-%d:%s", req.StartLine, req.EndLine, strings.TrimSpace(req.Content))
		}
		return "edit_file:" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(shape)
	case "search_files", "search_content", "rg":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
	case "discovery_query":
		purpose := firstNonEmpty(strings.TrimSpace(req.Purpose), strings.TrimSpace(req.Query))
		return tool + ":" + normalizeActionToken(purpose) + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case "structural_search":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case "dependency_graph":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Language)
	case "history_range", "blame_range":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(fmt.Sprintf("%d-%d", req.StartLine, req.EndLine))
	case "history_symbol", "ctags_symbols", "semgrep_scan":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern)
	case "joern_scan":
		return tool + ":" + normalizeActionToken(req.Path)
	case "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		target := firstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	case "go_move_declarations":
		target := firstNonEmpty(strings.TrimSpace(req.Path)+"->"+strings.TrimSpace(req.Destination), strings.TrimSpace(req.Pattern))
		if len(req.Moves) > 0 {
			target = strings.TrimSpace(req.Path) + "->batch"
		}
		return tool + ":" + normalizeActionToken(target)
	case "task_ledger":
		return "task_ledger"
	default:
		return tool
	}
}

func isBootstrappedTargetReadReminder(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == "read_file" &&
		strings.TrimSpace(result.Summary) == "Implementation bootstrap already inspected this target" &&
		strings.TrimSpace(result.Error) != ""
}

func goMoveDeclarationFailed(results []workspace.ToolResult) bool {
	for _, result := range results {
		if result.Tool == "go_move_declarations" && strings.TrimSpace(result.Error) != "" {
			return true
		}
	}
	return false
}

func declarationMovesForbiddenByMessages(messages []llama.ChatMessage) bool {
	if len(messages) == 0 {
		return false
	}
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == "user" {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return false
	}
	for _, message := range messages[lastUser:] {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "system" {
			continue
		}
		if textForbidsDeclarationMoves(message.Content) {
			return true
		}
	}
	return false
}

func textForbidsDeclarationMoves(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return containsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func declarationMoveConstraintRejection(req workspace.ToolRequest, declarationMovesForbidden bool) (string, string, bool) {
	if !declarationMovesForbidden {
		return "", "", false
	}
	switch strings.TrimSpace(req.Tool) {
	case "go_move_declarations":
		return "Declaration move rejected by task constraint",
			"the current task explicitly forbids moving declarations. Do not call go_move_declarations; continue with read_file, edit_file within the approved same-file target, verification, or report a concrete blocker.",
			true
	case "go_file_outline":
		return "Go outline rejected by task constraint",
			"the current task explicitly forbids declaration moves, and go_file_outline primarily suggests declaration-move batches. Do not call go_file_outline again; use read_file for exact lines, edit_file within the approved same-file target, verification, or report a concrete blocker.",
			true
	default:
		return "", "", false
	}
}

func marshalToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(data)
}

func shellActionClass(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return "shell"
	}
	switch fields[0] {
	case "ls", "find", "tree":
		return "broad_inspection"
	case "rg", "grep", "awk", "sed", "cat", "head", "tail":
		return "shell_narrow:" + normalizeActionToken(command)
	default:
		return "shell:" + normalizeActionToken(fields[0])
	}
}

func normalizeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "_"
	}
	if len(value) > 96 {
		return value[:96]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsRunnerTool(tools []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, tool := range tools {
		if strings.TrimSpace(tool) == target {
			return true
		}
	}
	return false
}

func containsRunnerString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func runnerAllowsTool(tools []string, tool string) bool {
	return containsRunnerTool(tools, tool)
}

func runnerHasWorkspaceWriteCapability(brief workerruntime.WorkerRuntimeBrief, tools []string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "write") {
		return true
	}
	for _, tool := range tools {
		if workspace.IsWorkspaceWriteTool(tool) {
			return true
		}
	}
	return false
}

func runnerVerificationRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.ToLower(strings.TrimSpace(brief.VerificationContract))
	return contract != "" && !strings.Contains(contract, "not_required")
}

func defaultRunnerStateSlice(values []string, defaults []string) []string {
	if len(values) > 0 {
		return values
	}
	return append([]string(nil), defaults...)
}

func appendRunnerTools(values []string, tools ...string) []string {
	for _, tool := range tools {
		values = appendUniqueRunnerState(values, tool)
	}
	return values
}

func filterOutRunnerTools(values []string, disallowed ...string) []string {
	if len(values) == 0 || len(disallowed) == 0 {
		return values
	}
	blocked := make(map[string]struct{}, len(disallowed))
	for _, tool := range disallowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			blocked[tool] = struct{}{}
		}
	}
	filtered := values[:0]
	for _, tool := range values {
		if _, skip := blocked[strings.TrimSpace(tool)]; skip {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func shouldAnswerDirectlyFromChildPacket(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
		return false
	}
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Output)), "partial findings from this turn") {
		return false
	}
	return childPacketContainsGroundedAnswerEvidence(result.Output)
}

func childPacketContainsGroundedAnswerEvidence(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	if containsAny(lower,
		"redirected to ",
		"try: {\"tool\":",
		"retry your original request and it will execute",
	) {
		return false
	}
	if len(pathparse.MentionedWorkspacePaths(output)) == 0 {
		return false
	}
	return containsAny(lower,
		"subagent handoff architecture",
		"core files:",
		"primary files:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"important flow:",
		"key findings:",
		"\n- read ",
		"\n- found ",
		"\n- located ",
		"\n- calls ",
		"\n- uses ",
		"\n- references ",
	)
}

func annotateScopedFallbackAnswer(answer string, objective string) string {
	answer = strings.TrimSpace(answer)
	objective = strings.TrimSpace(objective)
	if objective == "" || answer == "" || !strings.HasPrefix(answer, "Partial findings from this turn") {
		return answer
	}
	return "Question in scope: " + objective + "\nSource: delegated task packet.\n\n" + answer
}

func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	return answer
}

func (r *Runner) directForcedAnswer(forced *runnerForcedAnswer, results []workspace.ToolResult) (string, bool) {
	if r == nil || forced == nil {
		return "", false
	}
	switch strings.TrimSpace(forced.Reason) {
	case "delegated_read_only_answer_ready":
		return synthesizeDelegatedReadOnlyAnswer(r.RuntimeBrief.Objective, results)
	default:
		return "", false
	}
}

func synthesizeDelegatedReadOnlyAnswer(objective string, results []workspace.ToolResult) (string, bool) {
	useful := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || !isResultUsefulForFallback(result) {
			continue
		}
		useful = append(useful, result)
	}
	if len(useful) == 0 {
		return "", false
	}

	paths := delegatedReadOnlyInspectedImplementationPaths(results)
	if len(paths) == 0 {
		paths = delegatedDiscoveryQueryEvidencePaths(results, 4)
	}
	if len(paths) == 0 {
		paths = pinnedPathsFromToolResults(results, 4)
	}
	if len(paths) == 0 {
		return "", false
	}
	if answer, ok := synthesizeDelegatedDiscoveryAnswer(objective, results, paths); ok {
		return answer, true
	}

	objective = strings.TrimSpace(objective)
	var b strings.Builder
	if objective != "" {
		b.WriteString("Question: ")
		b.WriteString(delegatedDisplayQuestion(objective))
		b.WriteString("\n")
	}
	b.WriteString("Source: delegated subagent task packet.\n\n")
	b.WriteString("Inspected paths:\n")
	for _, path := range limitRunnerStrings(paths, 3) {
		b.WriteString("- `")
		b.WriteString(strings.TrimSpace(path))
		b.WriteString("`\n")
	}
	b.WriteString("\nKey findings:\n")
	for _, result := range limitFallbackResults(useful, 3) {
		summary := strings.TrimSpace(result.Summary)
		if summary == "" {
			summary = strings.TrimSpace(result.Tool)
		}
		b.WriteString("- ")
		b.WriteString(summary)
		if output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool)); output != "" {
			b.WriteString("\n")
			for _, line := range strings.Split(output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			continue
		}
		b.WriteString("\n")
	}
	confidence := "medium"
	if len(paths) >= 2 {
		confidence = "high"
	}
	b.WriteString("\nConfidence: ")
	b.WriteString(confidence)
	return strings.TrimSpace(b.String()), true
}

type delegatedDiscoverySummary struct {
	Purpose      string `json:"purpose"`
	ArtifactPath string `json:"artifact_path"`
	Evidence     []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Gaps []string `json:"gaps,omitempty"`
}

func synthesizeDelegatedDiscoveryAnswer(objective string, results []workspace.ToolResult, paths []string) (string, bool) {
	summaries := delegatedDiscoverySummaries(results)
	question := delegatedDisplayQuestion(objective)
	evidencePaths := delegatedDiscoveryAnswerEvidencePaths(question, paths)
	return AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      question,
		EvidencePaths: evidencePaths,
		Summaries:     summaries,
	})
}

func delegatedDiscoverySummaries(results []workspace.ToolResult) []delegatedDiscoverySummary {
	var summaries []delegatedDiscoverySummary
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "discovery_query" || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		var summary delegatedDiscoverySummary
		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}
		if summary.ArtifactPath == "" && len(summary.Evidence) == 0 {
			continue
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func delegatedDisplayQuestion(objective string) string {
	for _, line := range strings.Split(strings.TrimSpace(objective), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return inspected paths") ||
			strings.HasPrefix(lower, "- ") {
			continue
		}
		return line
	}
	return strings.TrimSpace(objective)
}

func delegatedObjectiveLooksLikeSubagents(objective string) bool {
	lower := strings.ToLower(objective)
	return strings.Contains(lower, "subagent") ||
		strings.Contains(lower, "sub-agent") ||
		strings.Contains(lower, "sub agent") ||
		(strings.Contains(lower, "task") && strings.Contains(lower, "delegate"))
}

func delegatedObjectiveLooksLikeDebugLogRedaction(objective string) bool {
	lower := strings.ToLower(objective)
	return strings.Contains(lower, "debug log") &&
		(strings.Contains(lower, "redacted") || strings.Contains(lower, "redaction") || strings.Contains(lower, "mode"))
}

func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}
	if delegatedObjectiveLooksLikeSubagents(objective) {
		paths = canonicalDelegatedSubagentEvidencePaths(paths)
	}
	matches := func(needles ...string) []string {
		filtered := make([]string, 0, len(paths))
		for _, path := range paths {
			lower := strings.ToLower(filepath.ToSlash(path))
			for _, needle := range needles {
				if strings.Contains(lower, strings.ToLower(needle)) {
					filtered = append(filtered, path)
					break
				}
			}
		}
		return uniqueDelegatedDiscoveryPaths(filtered)
	}
	switch {
	case delegatedObjectiveLooksLikeDebugLogRedaction(objective):
		if filtered := matches("internal/transcript/debuglog", "internal/app/model_state"); len(filtered) > 0 {
			return filtered
		}
	case delegatedObjectiveLooksLikeSubagents(objective):
		if filtered := matches("runner_child_tasks.go", "runner.go", "runner_lane_policy.go", "tool_manifest.go"); len(filtered) > 0 {
			return filtered
		}
	}
	return paths
}

func canonicalDelegatedSubagentEvidencePaths(paths []string) []string {
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		switch filepath.ToSlash(strings.TrimSpace(path)) {
		case "runner.go":
			canonical = append(canonical, "internal/agent/runner.go")
		case "runner_child_tasks.go":
			canonical = append(canonical, "internal/agent/runner_child_tasks.go")
		case "runner_lane_policy.go":
			canonical = append(canonical, "internal/agent/runner_lane_policy.go")
		case "tool_manifest.go":
			canonical = append(canonical, "internal/workspace/tool_manifest.go")
		default:
			canonical = append(canonical, path)
		}
	}
	return uniqueDelegatedDiscoveryPaths(canonical)
}

func delegatedDiscoverySnippetEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if !delegatedObjectiveLooksLikeSubagents(objective) || len(paths) <= 1 {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if filepath.ToSlash(path) == "internal/agent/runner.go" {
			continue
		}
		filtered = append(filtered, path)
	}
	if len(filtered) == 0 {
		return paths
	}
	return filtered
}

func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func delegatedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	var lines []string
	for _, summary := range summaries {
		for _, evidence := range summary.Evidence {
			name := strings.TrimSpace(evidence.Name)
			if name == "" || name == "project_behavior_digest" || name == "target_listing" {
				continue
			}
			text := strings.TrimSpace(evidence.Summary)
			if text == "" || strings.TrimSpace(evidence.Error) != "" {
				continue
			}
			lines = append(lines, name+": "+text)
			if limit > 0 && len(lines) >= limit {
				return lines
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "The deterministic discovery query completed, but only produced low-level evidence; inspect the listed evidence paths for detail.")
	}
	return lines
}

func delegatedDiscoverySnippetLines(summaries []delegatedDiscoverySummary, paths []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	wanted := make(map[string]struct{})
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path != "" {
			wanted[path] = struct{}{}
		}
	}
	var snippets []string
	seen := make(map[string]struct{})
	for _, summary := range summaries {
		for _, evidence := range summary.Evidence {
			switch strings.TrimSpace(evidence.Name) {
			case "project_behavior_digest", "target_listing":
				continue
			}
			for _, line := range strings.Split(evidence.OutputExcerpt, "\n") {
				line = strings.TrimSpace(line)
				if !delegatedDiscoverySnippetIsUseful(line) {
					continue
				}
				if len(wanted) > 0 {
					matched := false
					for path := range wanted {
						if strings.Contains(line, path) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				line = truncateRunnerStateText(line, 180)
				if _, ok := seen[line]; ok {
					continue
				}
				seen[line] = struct{}{}
				snippets = append(snippets, line)
				if len(snippets) >= limit {
					return snippets
				}
			}
		}
	}
	return snippets
}

func delegatedDiscoverySnippetIsUseful(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") || strings.HasPrefix(line, `"`) || delegatedDiscoveryLineIsBarePath(line) {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "_test.go:") ||
		strings.Contains(lower, "b.writestring(") ||
		strings.Contains(lower, "for _, term := range []string") ||
		strings.Contains(lower, "question in scope:") ||
		strings.Contains(lower, "expected ") ||
		strings.Contains(lower, "t.fatalf(") {
		return false
	}
	return strings.Contains(line, ":")
}

func delegatedDiscoveryLineIsBarePath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}
	path := normalizeDelegatedDiscoveryEvidencePath(line)
	return path != "" && delegatedDiscoveryLooksLikeEvidencePath(path)
}

func limitRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	return values[:max]
}

func delegatedDiscoveryQueryEvidencePaths(results []workspace.ToolResult, limit int) []string {
	if limit <= 0 {
		limit = 4
	}
	type discoveryPathCandidate struct {
		path  string
		score int
	}
	best := make(map[string]int)
	addCandidate := func(path string, layerName string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
			return
		}
		if !delegatedDiscoveryLooksLikeEvidencePath(path) {
			return
		}
		if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
			return
		}
		score := delegatedDiscoveryLayerPriority(layerName)*10 + delegatedDiscoveryPathPriority(path)
		if prior, ok := best[path]; ok && prior <= score {
			return
		}
		best[path] = score
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "discovery_query" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		var summary struct {
			Evidence []struct {
				Name          string `json:"name"`
				Summary       string `json:"summary"`
				OutputExcerpt string `json:"output_excerpt"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}
		for _, evidence := range summary.Evidence {
			text := strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt)
			for _, path := range pathparse.MentionedWorkspacePaths(text) {
				addCandidate(path, evidence.Name)
			}
		}
	}
	candidates := make([]discoveryPathCandidate, 0, len(best))
	for path, score := range best {
		candidates = append(candidates, discoveryPathCandidate{path: path, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	paths := make([]string, 0, limit)
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
		if len(paths) >= limit {
			return paths
		}
	}
	return paths
}

func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := strings.Index(path, ext+":"); idx >= 0 {
			return path[:idx+len(ext)]
		}
	}
	return path
}

func delegatedDiscoveryLooksLikeEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	switch {
	case strings.HasPrefix(path, "search_content/"),
		strings.HasPrefix(path, "search_files/"),
		strings.HasPrefix(path, "read/"),
		strings.HasPrefix(path, "list_files/"):
		return false
	case pathparse.HasWorkspaceFileExtension(path):
		return true
	default:
		return false
	}
}

func delegatedDiscoveryLayerPriority(name string) int {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return 0
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return 1
	case "go_references", "cpp_references", "python_references", "ts_references", "rust_references", "java_references":
		return 2
	case "go_definition", "cpp_definition", "python_definition", "ts_definition", "rust_definition", "java_definition":
		return 3
	case "go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return 4
	case "related_files":
		return 5
	case "dependency_graph":
		return 6
	case "ctags_symbols":
		return 7
	case "target_listing":
		return 20
	case "project_behavior_digest":
		return 30
	default:
		return 10
	}
}

func delegatedDiscoveryPathPriority(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 0
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 1
	case strings.HasSuffix(path, "_test.go"):
		return 2
	case strings.HasPrefix(path, "internal/"):
		return 3
	default:
		return 4
	}
}

func (r *Runner) emitAcceptedAnswerSignals(answer string, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}
	lane := firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), strings.TrimSpace(r.RuntimeBrief.TaskFamily), r.agentName())
	contract := firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), "direct_answer")
	evidencePaths := mergeRunnerStateStrings(pinnedPathsFromToolResults(results, 8), pathparse.MentionedWorkspacePaths(answer))
	confidence := "medium"
	if len(evidencePaths) >= 2 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "partial findings from this turn") {
		confidence = "high"
	}
	readiness := map[string]any{
		"lane":           lane,
		"ready":          true,
		"confidence":     confidence,
		"action":         "answer",
		"reason":         "answer satisfies lane contract and evidence gate",
		"evidence_paths": evidencePaths,
	}
	r.emitEvent(marshalRunEvent("readiness_check", readiness))
	critic := map[string]any{
		"contract": contract,
		"passed":   true,
		"action":   "accept",
	}
	r.emitEvent(marshalRunEvent("final_answer_critic", critic))
	r.emitEvent(marshalRunEvent("control_rails_enforcement", map[string]any{
		"lane":           lane,
		"task_family":    firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
		"contract":       contract,
		"passed":         true,
		"action":         "accept",
		"evidence_paths": evidencePaths,
	}))
}

func (r *Runner) agentName() string {
	if strings.TrimSpace(r.AgentName) == "" {
		return "main"
	}
	return r.AgentName
}

func (r *Runner) explorationDelegationNudge(readCallsSinceWrite int, results []workspace.ToolResult) string {
	if r == nil || r.Workspace == nil || r.DisableTaskTool || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return ""
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}
	allowedTools := r.advertisedAllowedTools(r.Workspace.AllowedTools())
	if !containsRunnerTool(allowedTools, "task") {
		return ""
	}
	if len(changedPathsFromToolResults(results)) > 0 || hasSuccessfulTaskResult(results) {
		return ""
	}
	minEvidenceResults := 2
	threshold := 4
	if prefersEarlyReadOnlyDelegation(r.RuntimeBrief) {
		minEvidenceResults = 1
		threshold = 1
	}
	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.Contract), "direct_answer") {
		if hasConcreteReadOnlyInspectionEvidence(results) {
			return ""
		}
		minEvidenceResults = 2
		threshold = 2
	}
	if successfulReadClassResultCount(results) < minEvidenceResults {
		return ""
	}
	if readCallsSinceWrite < threshold {
		return ""
	}
	return fmt.Sprintf("You have already used %d read/search steps in the main loop without a write. Stop broad exploration now and delegate one bounded read_only task with the task tool. Ask for inspected paths, key findings, confidence, and named gaps, then continue from that evidence packet.", readCallsSinceWrite)
}

func prefersEarlyReadOnlyDelegation(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(brief.Contract), "direct_answer") {
		return false
	}
	for _, policy := range brief.SearchPolicy {
		if strings.EqualFold(strings.TrimSpace(policy), "delegate_broad_discovery_when_needed") {
			return true
		}
	}
	return false
}

func hasConcreteReadOnlyInspectionEvidence(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "read_file",
			"go_read_symbol", "go_file_outline",
			"cpp_read_symbol", "cpp_file_outline",
			"python_read_symbol", "python_file_outline",
			"ts_read_symbol", "ts_file_outline",
			"rust_read_symbol", "rust_file_outline",
			"java_read_symbol", "java_file_outline":
			return true
		}
	}
	return false
}

func (r *Runner) initialBootstrapInstruction() string {
	if r == nil {
		return ""
	}
	family := strings.TrimSpace(r.RuntimeBrief.TaskFamily)
	profile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile)
	switch {
	case strings.HasPrefix(r.agentName(), "child:") && family == "delegated_read_only":
		return strings.Join([]string{
			"[delegated read-only bootstrap]",
			"Consume the delegated scope first. Do not start with directory walking when the delegated prompt already names paths or components.",
			"Use one targeted search_content or language symbol tool to locate the mechanism, then inspect the best hit directly with read_file or a symbol-read tool.",
			"After any successful search hit, inspect that hit before doing another filename or content search.",
			"Stop as soon as the delegated question is answered. Return concise grounded findings with inspected paths and confidence.",
		}, "\n")
	case family == "verification_only" || strings.EqualFold(profile, "read_only_verification_shell"):
		cmd := "go test ./..."
		if len(r.RuntimeBrief.VerificationPlan) > 0 && strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0])
		}
		return strings.Join([]string{
			"[verification bootstrap]",
			"First action now: run the verification command before any inspection.",
			"Emit exactly one shell tool call next.",
			fmt.Sprintf(`{"tool":"shell","command":"%s","workdir":"."}`, cmd),
			"Do not call pwd, ls, find, search_files, search_content, or read_file before that shell command.",
		}, "\n")
	case family == "debug_logs":
		return strings.Join([]string{
			"[debug-log bootstrap]",
			"Start from the newest relevant log artifact, not from source-code orientation.",
			"First choose the newest real log file (for example under logs/, *.log, or *.jsonl), then read its recent tail.",
			"Stay on that selected log until you can cite the weird decision or declare the missing datum.",
			"After one bounded log read, answer from the logs if the cause is already clear; otherwise do one targeted follow-up inside the same log.",
			"Do not call pwd for a log-investigation turn.",
		}, "\n")
	case isEvidenceFirstMutationTaskFamily(family) && len(r.RuntimeBrief.TargetRefs) > 0:
		return strings.Join([]string{
			"[evidence-first mutation bootstrap]",
			fmt.Sprintf("This turn will update named target file(s): %s.", strings.Join(r.RuntimeBrief.TargetRefs, ", ")),
			"Do not read the entire named target file first.",
			"First gather source evidence from the implementation files or configs that drive the change, then come back and read only the target section you plan to edit.",
		}, "\n")
	default:
		return ""
	}
}

func (r *Runner) verificationBootstrapRequest() (workspace.ToolRequest, bool) {
	if r == nil {
		return workspace.ToolRequest{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "read_only_verification_shell") {
		return workspace.ToolRequest{}, false
	}
	cmd := "go test ./..."
	for _, candidate := range r.RuntimeBrief.VerificationPlan {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			cmd = trimmed
			break
		}
	}
	if strings.TrimSpace(cmd) == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:    "shell",
		Command: cmd,
		Workdir: ".",
	}, true
}

func synthesizeVerificationOnlyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(req.Tool)
	}
	status := "passed"
	if strings.TrimSpace(result.Error) != "" {
		status = "failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Verification %s.\n\n", status)
	if command != "" {
		fmt.Fprintf(&b, "Command: `%s`\n", command)
	}
	if errText := strings.TrimSpace(result.Error); errText != "" {
		fmt.Fprintf(&b, "Failure: %s\n", errText)
	}
	if output := compactOutput(result.Output, 24); strings.TrimSpace(output) != "" {
		b.WriteString("\nOutput:\n")
		b.WriteString(output)
		b.WriteString("\n")
	}
	b.WriteString("\nNo files were edited.")
	return strings.TrimSpace(b.String())
}

func (r *Runner) debugLogPreflightRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "debug_logs" || debugLogEvidenceSatisfied(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}
	discovery, _ := debugLogDiscoveryRequest(r.Workspace)
	selection, ok := latestDebugLogSelection(results)
	if !ok {
		if debugLogRequestMatchesDiscovery(req, discovery) || isConcreteDebugLogArtifactRequest(req) {
			return workspace.ToolRequest{}, false
		}
		return discovery, true
	}
	if !hasDebugLogReadForSelection(results, selection) {
		if strings.TrimSpace(req.Tool) == "read_file" && debugLogRequestTargetsSelection(req, selection) {
			return workspace.ToolRequest{}, false
		}
		return debugLogTailRequest(selection), true
	}
	if _, ok := latestDebugLogSearchHit(results); !ok {
		if shouldDeepenDebugLogSearch(results, selection) {
			if next, ok := nextDebugLogSelection(results, selection); ok {
				if strings.TrimSpace(req.Tool) == "read_file" && debugLogRequestTargetsSelection(req, next) {
					return workspace.ToolRequest{}, false
				}
				return debugLogTailRequest(next), true
			}
		}
		if redirected, ok := debugLogScopedSearchRedirect(req, selection); ok {
			return redirected, true
		}
	}
	return workspace.ToolRequest{}, false
}

func prematureTargetReadRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || !containsRunnerString(brief.TargetRefs, path) {
		return "", "", false
	}
	if !isLargeTargetRead(req) {
		return "", "", false
	}
	if hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "", "", false
	}
	return "Premature target-file read rejected",
		"this turn needs source evidence before editing the named target file. Do not read the entire target file first. Inspect the implementation files or configs that drive the change, then read only the target section you plan to edit.",
		true
}

func evidenceFirstTargetReadBudgetRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return "", "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !containsRunnerString(brief.TargetRefs, strings.TrimSpace(req.Path)) {
		return "", "", false
	}
	if successfulTargetReadCount(results, brief.TargetRefs) < maxEvidenceFirstTargetReadWindows {
		return "", "", false
	}
	if !targetReadNeedsBounding(req) {
		return "", "", false
	}
	return "Target-read budget exhausted",
		fmt.Sprintf("you already inspected %d bounded windows of the named target file in this turn. Stop reading the whole target. Make the edit now, or if one exact detail is still missing, use a narrow line range or a targeted search anchored to that missing section.", maxEvidenceFirstTargetReadWindows),
		true
}

func evidenceFirstTargetReadRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return workspace.ToolRequest{}, false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, false
	}
	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return workspace.ToolRequest{}, false
	}
	if targetReadCount == 0 {
		if !isReadClassTool(req) {
			return workspace.ToolRequest{}, false
		}
		path := strings.TrimSpace(req.Path)
		if strings.TrimSpace(req.Tool) == "read_file" && containsRunnerString(brief.TargetRefs, path) && !isLargeTargetRead(req) {
			return workspace.ToolRequest{}, false
		}
		return firstTargetReadWindowRequest(brief.TargetRefs), true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !containsRunnerString(brief.TargetRefs, strings.TrimSpace(req.Path)) {
		return workspace.ToolRequest{}, false
	}
	if targetReadCount >= maxEvidenceFirstTargetReadWindows {
		return workspace.ToolRequest{}, false
	}
	if !targetReadNeedsBounding(req) {
		return workspace.ToolRequest{}, false
	}
	return boundedFollowupTargetReadRequest(req, results, brief.TargetRefs), true
}

func evidenceFirstTargetWritePhaseRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !evidenceFirstTargetWritePhaseActive(brief, results) {
		return "", "", false
	}
	if !isReadClassTool(req) {
		return "", "", false
	}
	if postTargetInspectionReadAllowed(req, brief.TargetRefs) {
		return "", "", false
	}
	return "Target-inspection phase complete",
		"you already gathered source evidence and inspected the named target file in bounded windows. Do not resume source-file discovery. Make the edit now, or if one exact detail is still missing, use search_content or a narrow read_file scoped to the named target only.",
		true
}

func isEvidenceFirstMutationTaskFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return true
	default:
		return false
	}
}

func isLargeTargetRead(req workspace.ToolRequest) bool {
	start := req.StartLine
	end := req.EndLine
	if start <= 0 && end <= 0 {
		return true
	}
	if start <= 0 {
		start = 1
	}
	if end <= 0 {
		return true
	}
	return end-start > 160
}

func hasFocusedNonTargetEvidence(results []workspace.ToolResult, targetRefs []string) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		switch tool := strings.TrimSpace(result.Tool); {
		case tool == "read_file":
			path := extractRunnerFilePath(result.Summary)
			if path != "" && !containsRunnerString(targetRefs, path) {
				return true
			}
		case strings.HasSuffix(tool, "_file_outline"), strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			path := extractSemanticInspectionPath(result.Summary)
			if path != "" && !containsRunnerString(targetRefs, path) {
				return true
			}
		}
	}
	return false
}

func hasSuccessfulTargetRead(results []workspace.ToolResult, targetRefs []string) bool {
	return successfulTargetReadCount(results, targetRefs) > 0
}

func successfulTargetReadCount(results []workspace.ToolResult, targetRefs []string) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if path := extractRunnerFilePath(result.Summary); containsRunnerString(targetRefs, path) {
			count++
		}
	}
	return count
}

func evidenceFirstTargetReadUnlocked(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		!hasSuccessfulTargetRead(results, brief.TargetRefs)
}

func evidenceFirstTargetWritePhaseActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		len(changedPathsFromToolResults(results)) == 0 &&
		successfulTargetReadCount(results, brief.TargetRefs) >= maxEvidenceFirstTargetReadWindows
}

const (
	initialEvidenceFirstTargetReadWindowEnd = 220
	followupEvidenceFirstTargetReadLines    = 140
	maxEvidenceFirstTargetReadWindows       = 2
)

func firstTargetReadWindowRequest(targetRefs []string) workspace.ToolRequest {
	for _, path := range targetRefs {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			return workspace.ToolRequest{
				Tool:      "read_file",
				Path:      trimmed,
				StartLine: 1,
				EndLine:   initialEvidenceFirstTargetReadWindowEnd,
			}
		}
	}
	return workspace.ToolRequest{}
}

func boundedFollowupTargetReadRequest(req workspace.ToolRequest, results []workspace.ToolResult, targetRefs []string) workspace.ToolRequest {
	start := req.StartLine
	if start <= 0 {
		if window, ok := latestSuccessfulTargetReadWindow(results, targetRefs); ok {
			start = window.End + 1
		} else {
			start = 1
		}
	}
	if start <= 0 {
		start = 1
	}
	return workspace.ToolRequest{
		Tool:      "read_file",
		Path:      strings.TrimSpace(req.Path),
		StartLine: start,
		EndLine:   start + followupEvidenceFirstTargetReadLines - 1,
	}
}

func targetReadNeedsBounding(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	start := req.StartLine
	end := req.EndLine
	if start <= 0 || end <= 0 {
		return true
	}
	return end-start+1 > followupEvidenceFirstTargetReadLines
}

func postTargetInspectionReadAllowed(req workspace.ToolRequest, targetRefs []string) bool {
	path := strings.TrimSpace(req.Path)
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return containsRunnerString(targetRefs, path) && !targetReadNeedsBounding(req)
	case "search_content":
		return containsRunnerString(targetRefs, path)
	default:
		return false
	}
}

func latestSuccessfulTargetReadWindow(results []workspace.ToolResult, targetRefs []string) (debugLogReadWindow, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok || !containsRunnerString(targetRefs, strings.TrimSpace(window.Path)) {
			continue
		}
		return window, true
	}
	return debugLogReadWindow{}, false
}

func clearEvidenceFirstTargetReadActionClasses(rejectedActions map[string]int, brief workerruntime.WorkerRuntimeBrief) {
	if len(rejectedActions) == 0 {
		return
	}
	for _, path := range brief.TargetRefs {
		actionClass := toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: strings.TrimSpace(path)})
		if actionClass == "" {
			continue
		}
		delete(rejectedActions, actionClass)
	}
}

type debugLogSearchHit struct {
	Handle string
	Path   string
	Line   int
}

type debugLogFileSelection struct {
	Handle string
	Path   string
}

type debugLogReadWindow struct {
	Handle string
	Path   string
	Start  int
	End    int
}

var singleSearchContentSummaryRE = regexp.MustCompile(`^Found 1 matching line at (.+):([0-9]+)\.`)

func debugLogEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return false
	}
	hit, ok := latestDebugLogSearchHit(results)
	if !ok {
		return false
	}
	return hasDebugLogReadWindowForHit(results, hit)
}

func debugLogPhaseScopeRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" || debugLogEvidenceSatisfied(brief, results) {
		return "", "", false
	}
	selection, ok := latestDebugLogSelection(results)
	if !ok || !hasDebugLogReadForSelection(results, selection) {
		return "", "", false
	}
	if isReadClassTool(req) && !debugLogRequestTargetsSelection(req, selection) {
		return "Selected-log scope enforced",
			"you already selected and read the newest relevant log. Stay on that log until you can cite the weird decision or declare the missing datum. Do not return to source-code or workspace discovery.",
			true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !debugLogRequestTargetsSelection(req, selection) {
		return "", "", false
	}
	if _, ok := latestDebugLogSearchHit(results); ok {
		return "", "", false
	}
	return "Blind log reread rejected",
		"you already have the newest tail of the selected log. Answer from that tail if it is sufficient, or use search_content scoped to the selected log to find one concrete marker. Do not keep rereading raw windows of the same log before you have a hit.",
		true
}

func debugLogFollowupRequest(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return workspace.ToolRequest{}, false
	}
	hit, ok := latestDebugLogSearchHit(results)
	if !ok || hasDebugLogReadWindowForHit(results, hit) {
		return workspace.ToolRequest{}, false
	}
	switch strings.TrimSpace(req.Tool) {
	case "search_content":
		return debugLogReadWindowRequest(hit), true
	case "read_file":
		if debugLogRequestTargetsHit(req, hit) && debugLogRequestCoversHit(req, hit) {
			return workspace.ToolRequest{}, false
		}
		return debugLogReadWindowRequest(hit), true
	default:
		return workspace.ToolRequest{}, false
	}
}

func latestDebugLogSelection(results []workspace.ToolResult) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case "read_file":
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !isStrictDebugLogArtifactPath(window.Path) {
				continue
			}
			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case "search_content":
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) {
				continue
			}
			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case "search_files":
			selection, ok := parseDebugLogFileSelection(result)
			if ok {
				return selection, true
			}
		}
	}
	return debugLogFileSelection{}, false
}

func parseDebugLogFileSelection(result workspace.ToolResult) (debugLogFileSelection, bool) {
	selections := parseDebugLogFileSelections(result)
	if len(selections) == 0 {
		return debugLogFileSelection{}, false
	}
	return selections[0], true
}

func parseDebugLogFileSelections(result workspace.ToolResult) []debugLogFileSelection {
	if strings.TrimSpace(result.Tool) != "search_files" || strings.TrimSpace(result.Error) != "" {
		return nil
	}
	var selections []debugLogFileSelection
	for _, line := range strings.Split(result.Output, "\n") {
		handle, path := parseSearchFilesSelectionLine(line)
		if path == "" || !isStrictDebugLogArtifactPath(path) {
			continue
		}
		selections = append(selections, debugLogFileSelection{Handle: handle, Path: path})
	}
	return selections
}

func parseSearchFilesSelectionLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	handle := ""
	if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "f") {
		handle = strings.TrimSpace(fields[0])
		line = strings.TrimSpace(strings.TrimPrefix(line, handle))
	}
	if idx := strings.Index(line, " (modified "); idx >= 0 {
		line = line[:idx]
	}
	return handle, strings.TrimSpace(line)
}

func hasDebugLogReadForSelection(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}
		if selection.Handle != "" && window.Handle == selection.Handle {
			return true
		}
		if selection.Path != "" && window.Path == selection.Path {
			return true
		}
	}
	return false
}

func nextDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_files" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		selections := parseDebugLogFileSelections(result)
		for idx := range selections {
			selection := selections[idx]
			if !sameDebugLogSelection(selection, current) {
				continue
			}
			for nextIdx := idx + 1; nextIdx < len(selections); nextIdx++ {
				if !hasDebugLogReadForSelection(results, selections[nextIdx]) {
					return selections[nextIdx], true
				}
			}
		}
	}
	return debugLogFileSelection{}, false
}

func sameDebugLogSelection(a debugLogFileSelection, b debugLogFileSelection) bool {
	if a.Handle != "" && b.Handle != "" && a.Handle == b.Handle {
		return true
	}
	return a.Path != "" && b.Path != "" && a.Path == b.Path
}

func shouldDeepenDebugLogSearch(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	_ = selection
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_content" {
			continue
		}
		if strings.TrimSpace(result.Error) != "" {
			return false
		}
		return workerruntime.IsZeroHitSearchResult(result)
	}
	return false
}

func debugLogDiscoveryRequest(mgr *workspace.Manager) (workspace.ToolRequest, bool) {
	pattern := "*.log"
	if mgr != nil {
		root := strings.TrimSpace(mgr.Root())
		for _, rel := range []string{"logs", "log", "traces", "trace"} {
			if root == "" {
				break
			}
			if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
				pattern = filepath.ToSlash(rel) + "/**/*"
				break
			}
		}
	}
	return workspace.ToolRequest{Tool: "search_files", Pattern: pattern, Path: "."}, true
}

func debugLogTailRequest(selection debugLogFileSelection) workspace.ToolRequest {
	req := workspace.ToolRequest{Tool: "read_file"}
	if selection.Handle != "" {
		req.ID = selection.Handle
		return req
	}
	req.Path = selection.Path
	return req
}

func debugLogRequestTargetsSelection(req workspace.ToolRequest, selection debugLogFileSelection) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		if selection.Handle != "" && strings.TrimSpace(req.ID) == selection.Handle {
			return true
		}
		return selection.Path != "" && strings.TrimSpace(req.Path) == selection.Path
	case "search_content":
		if selection.Path == "" {
			return false
		}
		return strings.TrimSpace(req.Path) == selection.Path || strings.TrimSpace(req.Glob) == selection.Path
	default:
		return false
	}
}

func debugLogRequestMatchesDiscovery(req workspace.ToolRequest, discovery workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != strings.TrimSpace(discovery.Tool) {
		return false
	}
	return strings.TrimSpace(req.Pattern) == strings.TrimSpace(discovery.Pattern) &&
		strings.TrimSpace(req.Path) == strings.TrimSpace(discovery.Path)
}

func isConcreteDebugLogArtifactRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path))
	case "search_content":
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path)) || isStrictDebugLogArtifactPath(strings.TrimSpace(req.Glob))
	default:
		return false
	}
}

func debugLogScopedSearchRedirect(req workspace.ToolRequest, selection debugLogFileSelection) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != "search_content" || debugLogRequestTargetsSelection(req, selection) {
		return workspace.ToolRequest{}, false
	}
	if !mentionsConcreteDebugLogSelector(req.Path) && !mentionsConcreteDebugLogSelector(req.Glob) {
		return workspace.ToolRequest{}, false
	}
	redirected := req
	redirected.Path = selection.Path
	redirected.Glob = ""
	return redirected, true
}

func isConcreteDebugLogRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content":
		return mentionsConcreteDebugLogSelector(req.Path) ||
			mentionsConcreteDebugLogSelector(req.Glob) ||
			mentionsConcreteDebugLogSelector(req.Pattern)
	case "read_file":
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path))
	default:
		return false
	}
}

func mentionsConcreteDebugLogSelector(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.Contains(lower, "/log/") ||
		strings.HasPrefix(lower, "log/") ||
		strings.Contains(lower, "/traces/") ||
		strings.HasPrefix(lower, "traces/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func isStrictDebugLogArtifactPath(path string) bool {
	return mentionsConcreteDebugLogSelector(path)
}

func latestDebugLogSearchHit(results []workspace.ToolResult) (debugLogSearchHit, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if hit, ok := parseDebugLogSearchHit(result); ok {
			return hit, true
		}
	}
	return debugLogSearchHit{}, false
}

func parseDebugLogSearchHit(result workspace.ToolResult) (debugLogSearchHit, bool) {
	match := singleSearchContentSummaryRE.FindStringSubmatch(strings.TrimSpace(result.Summary))
	if len(match) != 3 {
		return debugLogSearchHit{}, false
	}
	line := parseRunnerInt(match[2])
	if line <= 0 {
		return debugLogSearchHit{}, false
	}
	hit := debugLogSearchHit{Path: strings.TrimSpace(match[1]), Line: line}
	firstLine := firstNonEmptyLine(result.Output)
	if firstLine == "" {
		return hit, true
	}
	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "f") {
		hit.Handle = strings.TrimSpace(parts[0])
	}
	return hit, true
}

func hasDebugLogReadWindowForHit(results []workspace.ToolResult, hit debugLogSearchHit) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}
		if hit.Handle != "" && window.Handle != "" && hit.Handle == window.Handle && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
		if hit.Path != "" && window.Path == hit.Path && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
	}
	return false
}

func debugLogReadWindowRequest(hit debugLogSearchHit) workspace.ToolRequest {
	start := hit.Line - 20
	if start < 1 {
		start = 1
	}
	end := hit.Line + 20
	req := workspace.ToolRequest{
		Tool:      "read_file",
		StartLine: start,
		EndLine:   end,
	}
	if hit.Handle != "" {
		req.ID = hit.Handle
	} else {
		req.Path = hit.Path
	}
	return req
}

func debugLogRequestTargetsHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Handle != "" && strings.TrimSpace(req.ID) == hit.Handle {
		return true
	}
	return hit.Path != "" && strings.TrimSpace(req.Path) == hit.Path
}

func debugLogRequestCoversHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Line <= 0 {
		return false
	}
	start := req.StartLine
	end := req.EndLine
	if start <= 0 {
		start = hit.Line
	}
	if end <= 0 {
		end = start + 80
	}
	return hit.Line >= start && hit.Line <= end
}

func parseDebugLogReadWindow(result workspace.ToolResult) (debugLogReadWindow, bool) {
	summary := strings.TrimSpace(result.Summary)
	if !strings.HasPrefix(summary, "Read ") {
		return debugLogReadWindow{}, false
	}
	body := strings.TrimPrefix(summary, "Read ")
	linesIdx := strings.Index(body, " lines ")
	if linesIdx <= 0 {
		return debugLogReadWindow{}, false
	}
	left := strings.TrimSpace(body[:linesIdx])
	right := body[linesIdx+len(" lines "):]
	rangeField := right
	if space := strings.IndexByte(rangeField, ' '); space >= 0 {
		rangeField = rangeField[:space]
	}
	rangeParts := strings.SplitN(rangeField, "-", 2)
	if len(rangeParts) != 2 {
		return debugLogReadWindow{}, false
	}
	start := parseRunnerInt(rangeParts[0])
	end := parseRunnerInt(rangeParts[1])
	if start <= 0 || end < start {
		return debugLogReadWindow{}, false
	}
	window := debugLogReadWindow{Start: start, End: end}
	leftParts := strings.SplitN(left, " ", 2)
	if len(leftParts) == 2 && strings.HasPrefix(leftParts[0], "f") {
		window.Handle = strings.TrimSpace(leftParts[0])
		window.Path = strings.TrimSpace(leftParts[1])
		return window, window.Path != ""
	}
	window.Path = left
	return window, window.Path != ""
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func parseRunnerInt(s string) int {
	value := 0
	for _, ch := range strings.TrimSpace(s) {
		if ch < '0' || ch > '9' {
			return 0
		}
		value = value*10 + int(ch-'0')
	}
	return value
}

type runnerControlRails struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

func (r *Runner) emitControlRails(messages []llama.ChatMessage) {
	if r == nil || r.OnEvent == nil {
		return
	}
	rails := r.controlRails(messages)
	data, err := json.Marshal(rails)
	if err != nil {
		return
	}
	r.emitEvent(RunEvent{Role: "control_rails", Content: string(data)})
}

func (r *Runner) controlRails(messages []llama.ChatMessage) runnerControlRails {
	objective := strings.TrimSpace(r.RuntimeBrief.Objective)
	if objective == "" {
		objective = latestUserMessage(messages)
	}
	allowedTools := []string(nil)
	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "conversation_only") {
		allowedTools = nil
	} else if len(r.RuntimeBrief.AllowedTools) > 0 {
		allowedTools = append([]string(nil), r.RuntimeBrief.AllowedTools...)
	} else if r.Workspace != nil {
		allowedTools = r.Workspace.AllowedTools()
	}
	profile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile)
	if profile == "" {
		if len(allowedTools) == 0 {
			profile = "conversation_only"
		} else {
			profile = "read_only_workspace"
		}
	}
	rails := runnerControlRails{
		Lane:                 firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), r.agentName()),
		Contract:             firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), "direct_answer"),
		TaskFamily:           firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), "direct_answer"),
		PermissionProfile:    profile,
		RequiredEvidence:     append([]string(nil), r.RuntimeBrief.RequiredEvidence...),
		RequiredTools:        append([]string(nil), r.RuntimeBrief.RequiredTools...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       append([]string(nil), r.RuntimeBrief.ForbiddenTools...),
		ReadinessGate:        append([]string(nil), r.RuntimeBrief.ReadinessGate...),
		SearchPolicy:         append([]string(nil), r.RuntimeBrief.SearchPolicy...),
		VerificationContract: firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.VerificationContract), "not_required_for_conversation"),
		StopConditions:       append([]string(nil), r.RuntimeBrief.StopConditions...),
		FinalCritic:          append([]string(nil), r.RuntimeBrief.FinalCritic...),
	}
	if len(rails.RequiredEvidence) == 0 {
		rails.RequiredEvidence = []string{"answer_addresses_user_request"}
	}
	if len(rails.StopConditions) == 0 {
		rails.StopConditions = []string{"answered_user_request", "blocked_by_missing_information"}
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		rails.Lane = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), "main")
		rails.Contract = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), "implementation_result")
		rails.TaskFamily = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), "implementation")
		rails.PermissionProfile = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "guarded_workspace_write")
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{"changed_paths_or_noop_reason", "verification_status"})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{"edit_file_or_apply_patch", "verification_shell"})
		rails.ForbiddenTools = appendUniqueRunnerState(rails.ForbiddenTools, "unbounded_write")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"changed_paths_reported", "verification_status_reported"})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"})
		rails.VerificationContract = firstNonEmpty(strings.TrimSpace(rails.VerificationContract), "tests_passed_or_failed_or_skipped_with_blocker")
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{"change_verified", "blocked_with_failed_verification_or_scope"})
		rails.FinalCritic = []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"}
		return rails
	}
	if runnerAllowsTool(rails.AllowedTools, "shell") && !runnerHasWorkspaceWriteCapability(r.RuntimeBrief, rails.AllowedTools) && runnerVerificationRequired(r.RuntimeBrief) {
		rails.TaskFamily = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), "verification")
		rails.Contract = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), "grounded_explanation")
		rails.PermissionProfile = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "read_only_verification_shell")
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{"verification_command", "pass_fail_status"})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{"shell"})
		rails.ForbiddenTools = appendRunnerTools(rails.ForbiddenTools, "edit_file", "write_file", "append_file", "mkdir", "mutating_shell")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"verification_status_reported"})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"run_requested_check", "capture_failure_output", "do_not_edit"})
		rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		return rails
	}
	if strings.EqualFold(strings.TrimSpace(rails.TaskFamily), "debug_logs") {
		rails.FailureToEval = true
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"latest_log_selected", "failure_cause_supported_by_log", "low_confidence_triggers_deepening"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}
	if rails.Contract == "direct_answer" {
		rails.TaskFamily = firstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), "direct_answer")
		if len(rails.AllowedTools) == 0 {
			rails.PermissionProfile = "conversation_only"
			rails.ForbiddenTools = appendUniqueRunnerState(rails.ForbiddenTools, "shell")
		}
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{"answer_addresses_prompt"})
		rails.VerificationContract = firstNonEmpty(strings.TrimSpace(rails.VerificationContract), "not_required_for_read_only_lane")
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{"required_evidence_satisfied"})
		if runnerUsesSoftReadOnlyShapingOnly(r.RuntimeBrief, rails) {
			rails.ReadinessGate = nil
			rails.FinalCritic = nil
		}
	}
	if runnerUsesSoftReadOnlyShapingOnly(r.RuntimeBrief, rails) {
		rails.ReadinessGate = nil
		rails.FinalCritic = nil
	}
	return rails
}

func runnerUsesSoftReadOnlyShapingOnly(brief workerruntime.WorkerRuntimeBrief, rails runnerControlRails) bool {
	if runnerBriefRequiresMutation(brief) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(rails.TaskFamily), "debug_logs") {
		return false
	}
	if runnerVerificationRequired(brief) {
		return false
	}
	switch strings.TrimSpace(rails.PermissionProfile) {
	case "read_only_workspace":
		return true
	default:
		return false
	}
}

func latestUserMessage(messages []llama.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(messages[i].Role, "user") {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func hideBroadDiscoveryTools(hidden map[string]bool) {
	for _, tool := range []string{
		"list_files",
		"discovery_query", "dependency_graph", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints",
		"cpp_overview", "cpp_entrypoints",
		"python_overview", "python_entrypoints",
		"ts_overview", "ts_entrypoints",
		"rust_overview", "rust_entrypoints",
		"java_overview", "java_entrypoints",
	} {
		hidden[tool] = true
	}
}

func (r *Runner) augmentReadOnlyChildPrompt(prompt string) string {
	guidance := []string{
		"Answer the delegated question directly once the evidence is sufficient.",
		"Use repository-relative workspace paths in the answer. Do not rely on transient file handles such as f23 or tool-local aliases.",
		"Prefer targeted symbol tools and bounded file reads over workspace-wide overviews.",
	}
	if namedPaths := frontier.PromptMentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		guidance = append(guidance, fmt.Sprintf("Start from these named workspace paths: %s.", strings.Join(namedPaths, ", ")))
	}
	return strings.TrimSpace(prompt) + "\n\nScope guidance:\n- " + strings.Join(guidance, "\n- ")
}

func (r *Runner) completeWithoutMoreTools(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {
	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Tools are now disabled — do NOT emit another tool block. Reply with a concise, direct, text-only answer from the evidence you already have. If a concrete answer is not yet possible, state the narrowest remaining blocker and the next action. Do not leave the response blank.", metrics)
}

func (r *Runner) completeWithoutMoreToolsWithReason(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
	metrics *runnerMetrics,
) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Do not request any more tools. Answer with the evidence you already have and explain any remaining blocker clearly."
	}
	if answer, ok := structuredDebugLogAnswer(r.Workspace, r.RuntimeBrief, collectedResults); ok {
		return answer, nil
	}
	if !shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief) {
		if answer, ok := immediateBudgetFallbackAnswer(r.agentName(), r.RuntimeBrief, reason, collectedResults); ok {
			return answer, nil
		}
	}
	if shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief) {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
	}
	// If any task_ledger items are still in_progress, prepend a closure
	// hint so the forced-completion reply actually resolves them instead
	// of leaving dangling in_progress todos in the transcript.
	if hint := taskLedgerClosureHint(collectedResults); hint != "" {
		reason = hint + "\n\n" + reason
	}
	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts)
	assistantMessage, err := r.Client.CompleteChatMessage(ctx, turnMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}
	r.emitModelCallTrace(0, "forced_completion", turnMessages, assistantMessage, time.Since(modelStart).Milliseconds())
	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, textOnlyOpts, false)

	if len(assistantMessage.ToolCalls) > 0 {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}
	if looksLikeTextualToolCall(reply) {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	return r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief) bool {
	if !strings.HasPrefix(agentName, "child:") {
		return false
	}
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	return true
}

func (r *Runner) advertisedAllowedTools(base []string) []string {
	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "conversation_only") {
		return nil
	}
	allowed := append([]string(nil), base...)
	if len(r.RuntimeBrief.AllowedTools) > 0 {
		available := make(map[string]string, len(base))
		for _, tool := range base {
			trimmed := strings.TrimSpace(tool)
			if trimmed != "" {
				available[trimmed] = tool
			}
		}
		filtered := make([]string, 0, len(r.RuntimeBrief.AllowedTools))
		seen := make(map[string]struct{}, len(r.RuntimeBrief.AllowedTools))
		for _, tool := range r.RuntimeBrief.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			if _, ok := seen[tool]; ok {
				continue
			}
			if original, ok := available[tool]; ok {
				filtered = append(filtered, original)
				seen[tool] = struct{}{}
			}
		}
		allowed = filtered
	}
	if r.DisableTaskTool {
		filtered := allowed[:0]
		for _, tool := range allowed {
			if tool != "task" {
				filtered = append(filtered, tool)
			}
		}
		allowed = filtered
	}
	return allowed
}

func textOnlyChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""
	if opts.MaxTokens <= 0 || opts.MaxTokens > 384 {
		opts.MaxTokens = 384
	}
	return opts
}

func looksLikeTextualToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>")
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "task_ledger":
		return strings.TrimSpace(result.Output) != ""
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations", "apply_patch":
		summary := strings.ToLower(strings.TrimSpace(result.Summary))
		return summary != "" && !strings.Contains(summary, "no-op")
	case "read_file", "search_files", "search_content", "rg",
		"go_file_outline", "go_find_symbol", "go_find_type", "go_find_const", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	case "shell":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	default:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	}
}

func isMissingPathListFailure(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "list_files" {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(result.Error))
	return strings.Contains(errText, "no such file or directory") || strings.Contains(errText, "cannot find the path")
}

func changedPathsFromToolResults(results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range changedPathsFromToolResult(result) {
			paths[normalizeRunnerPath(path)] = struct{}{}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func changedPathsFromToolResult(result workspace.ToolResult) []string {
	switch strings.TrimSpace(result.Tool) {
	case "edit_file", "write_file", "append_file", "mkdir":
		return changedPathFromSummary(result.Summary)
	case "go_move_declarations":
		return changedGoMovePathsFromSummary(result.Summary)
	case "apply_patch":
		return changedPathsFromPatchOutput(result.Output)
	default:
		return nil
	}
}

func changedPathsFromPatchOutput(output string) []string {
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "add "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "add ")))
		case strings.HasPrefix(line, "delete "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "delete ")))
		case strings.HasPrefix(line, "update+move "):
			rest := strings.TrimPrefix(line, "update+move ")
			source, destination, moved := strings.Cut(rest, " -> ")
			if moved {
				paths = append(paths, patchOutputPath(source), patchOutputPath(destination))
			} else {
				paths = append(paths, patchOutputPath(rest))
			}
		case strings.HasPrefix(line, "update "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "update ")))
		}
	}
	return paths
}

func patchOutputPath(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, " ("); ok {
		value = before
	}
	return strings.TrimSpace(value)
}

func changedPathFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) < 2 {
		return nil
	}
	switch fields[0] {
	case "Edited", "Wrote", "Appended":
		return []string{fields[1]}
	case "Created":
		if len(fields) >= 3 && fields[1] == "directory" {
			return []string{fields[2]}
		}
	}
	return nil
}

func changedGoMovePathsFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) == 0 || fields[0] != "Moved" {
		return nil
	}
	var paths []string
	for i, field := range fields {
		switch field {
		case "from", "to":
			if i+1 < len(fields) {
				paths = append(paths, fields[i+1])
			}
		}
	}
	return paths
}

func normalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isWriteTool(req workspace.ToolRequest) bool {
	return workspace.IsWorkspaceWriteTool(req.Tool)
}

func isVerificationRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "go_diagnostics":
		return true
	case "shell":
		command := strings.ToLower(strings.TrimSpace(req.Command))
		if command == "" {
			return false
		}
		verificationSnippets := []string{
			"go test", "go vet", "go build",
			"npm test", "npm run test", "npm run build",
			"pnpm test", "pnpm run test", "pnpm build",
			"yarn test", "yarn build",
			"pytest", "python -m pytest", "python3 -m pytest",
			"cargo test", "cargo check", "cargo build",
			"make test", "make check",
			"ctest", "mvn test", "gradle test",
			"tsc", "ruff check", "eslint",
		}
		for _, snippet := range verificationSnippets {
			if strings.Contains(command, snippet) {
				return true
			}
		}
	}
	return false
}

func clearVerificationGateFingerprints(fingerprints map[string]string) {
	for fingerprint, reason := range fingerprints {
		if strings.Contains(reason, "Verification required after workspace edits") {
			delete(fingerprints, fingerprint)
		}
	}
}

func clearRunnerStringIntMap(values map[string]int) {
	for key := range values {
		delete(values, key)
	}
}

func (r *Runner) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {
	trimmed := strings.TrimSpace(reply.Content)
	if trimmed != "" {
		if looksLikeTextualToolCall(trimmed) {
			if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
				return fallback, nil
			}
			return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
		}
		r.LastAssistantMessage = reply
		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, llama.ChatMessage{
			Role:             firstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		})
	}
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous response was empty. Tools are disabled now — do NOT emit a tool block. " +
			"Reply with a direct, non-empty answer based on the evidence already gathered. Keep it short " +
			"(a few sentences or a brief list). No preamble like \"Based on...\" or \"Here is...\" — just the answer. " +
			"If a concrete answer is not yet possible, state the narrowest remaining blocker in one sentence.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts)
	retryMessage, err := r.Client.CompleteChatMessage(ctx, retryMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}
	r.emitModelCallTrace(0, "empty_answer_retry", retryMessages, retryMessage, time.Since(modelStart).Milliseconds())
	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, textOnlyOpts, false)
	if len(retryMessage.ToolCalls) > 0 {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}
	if looksLikeTextualToolCall(retryReply) {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent returned an empty response", r.agentName())
	}
	r.LastAssistantMessage = retryMessage
	return retryTrimmed, nil
}

func immediateBudgetFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, reason string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}
	lowerReason := strings.ToLower(strings.TrimSpace(reason))
	if !containsAny(lowerReason,
		"budget",
		"maximum tool steps",
		"tools are now disabled",
		"run duration",
		"model-call budget",
	) {
		return "", false
	}
	if !hasSuccessfulTaskResult(results) && !strings.HasPrefix(agentName, "child:") {
		return "", false
	}
	if usefulFallbackResultCount(results) < 2 {
		return "", false
	}
	return synthesizeFallbackAnswer(agentName, results)
}

func synthesizeFallbackAnswer(agentName string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}

	var firstSuccessful *workspace.ToolResult
	var lastError *workspace.ToolResult
	var successfulNarrowing []workspace.ToolResult
	for i := range results {
		result := results[i]
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && firstSuccessful == nil {
			copy := result
			firstSuccessful = &copy
		}
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && isResultUsefulForFallback(result) {
			successfulNarrowing = append(successfulNarrowing, result)
		}
		if result.Error != "" {
			copy := result
			lastError = &copy
		}
	}

	// OpenCode-style fallback framing (see docs/opencode-fixes-design.md,
	// `default.txt`): the model failed to synthesize a direct reply, but the
	// evidence collected during the turn is still a legitimate partial
	// answer. Lead with the findings as findings — not with an apology.
	// Prior wording ("did not complete cleanly, but here is the evidence
	// gathered") read as a system error to users; this reframes the same
	// content as an honest "partial findings" report.
	var b strings.Builder
	if agentName == "explore" {
		b.WriteString("Partial findings from this turn (explore lane reached its budget before synthesizing):\n\n")
	} else {
		b.WriteString("Partial findings from this turn (the budget was reached before a full synthesis):\n\n")
	}

	if len(successfulNarrowing) > 0 || firstSuccessful != nil {
		b.WriteString("Verified context:\n")
		if len(successfulNarrowing) == 0 && firstSuccessful != nil {
			successfulNarrowing = append(successfulNarrowing, *firstSuccessful)
		}
		for _, result := range limitFallbackResults(successfulNarrowing, 3) {
			b.WriteString("- ")
			b.WriteString(result.Summary)
			if output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool)); output != "" {
				b.WriteString("\n")
				b.WriteString(output)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if lastError != nil {
		b.WriteString("Final blocker:\n")
		b.WriteString("- ")
		b.WriteString(lastError.Summary)
		if errText := strings.TrimSpace(lastError.Error); errText != "" {
			b.WriteString(": ")
			b.WriteString(errText)
		}
		b.WriteString("\n\n")
	}

	// Before giving up, sift the verified context for any concrete
	// observation the agent can offer instead of a generic apology. Files
	// above a size threshold are surfaced as plausible refactor candidates
	// when the user's original prompt looked like a review / cleanup ask.
	if observations := inferFallbackObservations(successfulNarrowing); len(observations) > 0 {
		b.WriteString("Best-effort observations from what was read:\n")
		for _, obs := range observations {
			b.WriteString("- ")
			b.WriteString(obs)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if fallbackLooksLikeLogInvestigation(successfulNarrowing) {
		b.WriteString("Takeaway:\n")
		b.WriteString("- The logs suggest the harness can be made smarter by forcing synthesis earlier after repeated blocked reads and by preferring targeted search over rereading the same selected log.\n\n")
	}

	b.WriteString("Recommended next step:\n")
	if agentName == "explore" {
		b.WriteString("- run a targeted `search_content` or `read_file` on one of the discovered files instead of another broad listing")
	} else {
		b.WriteString("- retry with a more specific file or symptom so the agent can narrow immediately")
	}

	return strings.TrimSpace(b.String()), true
}

// inferFallbackObservations turns a handful of read_file / outline results
// into concrete talking points so the forced-completion fallback can offer
// something useful instead of a bare "I failed" message. Observations are
// bounded in count and content so they do not balloon the reply.
func inferFallbackObservations(results []workspace.ToolResult) []string {
	if len(results) == 0 {
		return nil
	}
	observations := make([]string, 0, 3)
	seen := make(map[string]struct{})
	for _, result := range results {
		if result.Tool != "read_file" {
			continue
		}
		path := extractReadFilePath(result.Summary)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		lines := strings.Count(result.Output, "\n")
		if lines >= 600 {
			observations = append(observations, fmt.Sprintf("%s is unusually large (%d lines) — a plausible refactor candidate on its own, even without deeper evidence.", path, lines))
		} else if lines >= 250 {
			observations = append(observations, fmt.Sprintf("%s (%d lines) was read; scan for functions exceeding one screen or mixed responsibilities as a small follow-up target.", path, lines))
		}
		if len(observations) >= 3 {
			break
		}
	}
	return observations
}

func fallbackLooksLikeLogInvestigation(results []workspace.ToolResult) bool {
	for _, result := range results {
		for _, field := range []string{result.Summary, result.Output} {
			if mentionsConcreteDebugLogSelector(field) {
				return true
			}
		}
	}
	return false
}

// extractReadFilePath pulls the path out of a read_file result's summary
// (the canonical format is "Read <display> lines N-M"). Returns empty on
// any parse failure so the caller just skips the observation.
func extractReadFilePath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if !strings.HasPrefix(trimmed, "Read ") {
		return ""
	}
	rest := strings.TrimPrefix(trimmed, "Read ")
	idx := strings.Index(rest, " lines ")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:idx])
}

func extractRunnerFilePath(summary string) string {
	path := extractReadFilePath(summary)
	if path == "" {
		return ""
	}
	fields := strings.Fields(path)
	if len(fields) >= 2 && looksLikeRunnerFileHandle(fields[0]) {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return path
}

func extractSemanticInspectionPath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	for _, marker := range []string{" lines ", ". Use "} {
		idx := strings.Index(trimmed, marker)
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			break
		}
	}
	for _, prefix := range []string{"Read ", "Extracted ", "Outlined "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		for _, separator := range []string{" in ", " from "} {
			idx := strings.LastIndex(trimmed, separator)
			if idx <= len(prefix) {
				continue
			}
			return strings.TrimSpace(trimmed[idx+len(separator):])
		}
	}
	return ""
}

func looksLikeRunnerFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}
	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func isResultUsefulForFallback(result workspace.ToolResult) bool {
	lowerSummary := strings.ToLower(strings.TrimSpace(result.Summary))
	lowerOutput := strings.ToLower(strings.TrimSpace(result.Output))
	if strings.HasPrefix(lowerSummary, "redirected to ") || containsAny(lowerOutput,
		"try: {\"tool\":",
		"retry your original request and it will execute",
	) {
		return false
	}
	switch result.Tool {
	case "task", "discovery_query", "read_file", "search_content", "search_files",
		"go_read_symbol", "go_file_outline",
		"cpp_read_symbol", "cpp_file_outline",
		"python_read_symbol", "python_file_outline",
		"ts_read_symbol", "ts_file_outline",
		"rust_read_symbol", "rust_file_outline",
		"java_read_symbol", "java_file_outline":
		return true
	default:
		return false
	}
}

func usefulFallbackResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || !isResultUsefulForFallback(result) {
			continue
		}
		count++
	}
	return count
}

func limitFallbackResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}
	return results[:max]
}

func fallbackLineBudgetForTool(tool string) int {
	switch tool {
	case "discovery_query":
		return 20
	case "search_content":
		return 6
	case "read_file":
		return 10
	default:
		return 8
	}
}

// taskLedgerClosureHint inspects the collected tool-results set and, if the
// most recent task_ledger state still has any items flagged in_progress,
// returns a short reminder asking the model to close them with a final
// status in the upcoming reply. Returns the empty string when there is
// nothing open.
func taskLedgerClosureHint(results []workspace.ToolResult) string {
	var latest *workspace.ToolResult
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Tool == "task_ledger" && strings.TrimSpace(results[i].Error) == "" {
			copyResult := results[i]
			latest = &copyResult
			break
		}
	}
	if latest == nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(latest.Output), "in_progress") {
		return ""
	}
	var openItems []string
	for _, line := range strings.Split(latest.Output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "in_progress") {
			openItems = append(openItems, strings.TrimSpace(line))
		}
	}
	if len(openItems) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Task ledger has open items you never closed. Resolve them in your reply (state each as completed / blocked / cancelled with one short evidence sentence):\n")
	for _, item := range openItems {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// toolFailurePathStem returns the first two path segments of the request's
// target path (e.g. "internal/llm" for "internal/llm/llm.go"). Used by the
// same-stem failure counter so that five different hallucinated filenames
// inside the same made-up directory count as repeated failures, not fresh
// attempts.
func toolFailurePathStem(req workspace.ToolRequest) string {
	raw := strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// buildRejectionRecoveryNudge returns a concrete, copy-paste-ready next
// action the model can execute after its second consecutive rejected tool
// call in a turn. Called once per turn (gated by rejectionRecoveryNudgeUsed)
// before the runner bails out with repeated_dead_end_budget_exhausted. The
// goal is not to be clever — it is to hand a small local model an escape
// hatch that it can execute verbatim.
func buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult, mgr *workspace.Manager, brief workerruntime.WorkerRuntimeBrief) string {
	tool := strings.TrimSpace(req.Tool)
	var b strings.Builder
	b.WriteString("Recovery nudge — this is your last chance before the harness ends the turn. Pick ONE of the concrete next steps below (you can copy-paste the JSON). Do NOT repeat the rejected tool call.\n\n")

	if strings.EqualFold(strings.TrimSpace(brief.PermissionProfile), "read_only_verification_shell") {
		cmd := "go test ./..."
		if len(brief.VerificationPlan) > 0 && strings.TrimSpace(brief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(brief.VerificationPlan[0])
		}
		b.WriteString("This is a verification-only turn. Do not explore first.\n")
		b.WriteString("Run the requested check now:\n")
		b.WriteString("  {\"tool\":\"shell\",\"command\":\"")
		b.WriteString(strings.ReplaceAll(cmd, "\"", "\\\""))
		b.WriteString("\",\"workdir\":\".\"}\n")
		b.WriteString("If that command already ran, answer with the observed pass/fail result instead of calling another tool.\n")
		b.WriteString("\nDo not call `pwd`, `ls`, `find`, `search_files`, `search_content`, or `read_file` before the verification command.\n")
		return b.String()
	}
	if strings.TrimSpace(brief.TaskFamily) == "debug_logs" {
		if selection, ok := latestDebugLogSelection(results); ok {
			b.WriteString("This is a debug-log investigation. Stay on real log artifacts.\n")
			if shouldDeepenDebugLogSearch(results, selection) {
				if next, ok := nextDebugLogSelection(results, selection); ok {
					b.WriteString("The current selected log did not yield a concrete hit. Deepen to the next newest log now:\n")
					b.WriteString("  ")
					b.WriteString(marshalToolRequestRaw(debugLogTailRequest(next)))
					b.WriteString("\n")
					b.WriteString("If that next log is still not useful, answer with the current uncertainty and name the missing symptom.\n")
					return b.String()
				}
			}
			b.WriteString("Answer from the selected log if it is already clear.\n")
			redirected, redirectedOK := debugLogScopedSearchRedirect(req, selection)
			if redirectedOK {
				b.WriteString("Or search inside the same selected log with your current pattern:\n")
				b.WriteString("  ")
				b.WriteString(marshalToolRequestRaw(redirected))
				b.WriteString("\n")
			}
			b.WriteString("Do not switch back to source files, broad workspace discovery, or another raw read_file window of the same log.\n")
			return b.String()
		}
	}
	if isPrematureEvidenceFirstTargetRead(req, results, brief) {
		b.WriteString("This turn still needs source evidence before the named target file. Do not keep reading the target yet.\n")
		candidates := nonTargetRecoveryFileCandidates(results, brief.TargetRefs)
		if len(candidates) > 0 {
			b.WriteString("Inspect one implementation file first:\n")
			for _, path := range candidates {
				if strings.HasSuffix(strings.ToLower(path), ".go") {
					b.WriteString("  {\"tool\":\"go_file_outline\",\"path\":\"")
					b.WriteString(strings.ReplaceAll(path, "\"", "\\\""))
					b.WriteString("\"}\n")
					continue
				}
				b.WriteString("  {\"tool\":\"read_file\",\"path\":\"")
				b.WriteString(strings.ReplaceAll(path, "\"", "\\\""))
				b.WriteString("\",\"start_line\":1,\"end_line\":160}\n")
			}
		} else {
			b.WriteString("Pick one non-target implementation file or search result and inspect it before coming back to the doc.\n")
		}
		b.WriteString("\nOr, if the source evidence is already sufficient in prior results, answer the user directly instead of calling another tool.\n")
		return b.String()
	}
	if evidenceFirstTargetWritePhaseActive(brief, results) {
		target := firstNonEmptyNonEmpty(brief.TargetRefs...)
		if target == "" {
			target = strings.TrimSpace(req.Path)
		}
		b.WriteString("This turn is now in target-write phase. Do not inspect more source files.\n")
		b.WriteString("Either edit the target now, or if one exact detail is still missing, stay inside the named target with one narrow read.\n")
		if target != "" {
			start, end := 1, 40
			if window, ok := latestSuccessfulTargetReadWindow(results, brief.TargetRefs); ok {
				start = window.End - 19
				if start < 1 {
					start = 1
				}
				end = window.End
			}
			b.WriteString("Narrow target read example:\n")
			b.WriteString("  {\"tool\":\"read_file\",\"path\":\"")
			b.WriteString(strings.ReplaceAll(target, "\"", "\\\""))
			b.WriteString("\",\"start_line\":")
			b.WriteString(fmt.Sprintf("%d", start))
			b.WriteString(",\"end_line\":")
			b.WriteString(fmt.Sprintf("%d", end))
			b.WriteString("}\n")
		}
		b.WriteString("\nOr, if you already have enough evidence, answer the user now with what you observed — do not call any more tools.\n")
		return b.String()
	}

	switch {
	case tool == "list_files" || tool == "list_dir":
		candidates := rejectionRecoveryFileCandidates(results)
		if len(candidates) > 0 {
			b.WriteString("You already have a workspace overview. Narrow next. Try any of:\n")
			b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"TODO|FIXME|XXX|HACK\",\"path\":\".\"}\n")
			b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"panic\\\\(\",\"path\":\".\"}\n")
			for _, path := range candidates {
				b.WriteString("  {\"tool\":\"read_file\",\"path\":\"")
				b.WriteString(path)
				b.WriteString("\",\"start_line\":1,\"end_line\":200}\n")
			}
		} else {
			b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"TODO|FIXME|XXX|HACK\",\"path\":\".\"}\n")
			b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"panic\\\\(\",\"path\":\".\"}\n")
		}
	case tool == "read_file":
		path := strings.TrimSpace(req.Path)
		if path != "" {
			b.WriteString("Narrow the read (request a smaller window) or switch to a structured tool:\n")
			b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"func \",\"path\":\"")
			b.WriteString(path)
			b.WriteString("\"}\n")
			b.WriteString("  {\"tool\":\"read_file\",\"path\":\"")
			b.WriteString(path)
			b.WriteString("\",\"start_line\":1,\"end_line\":80}\n")
		} else {
			b.WriteString("  {\"tool\":\"search_files\",\"pattern\":\"*.go\"}\n")
		}
	case tool == "search_content":
		b.WriteString("Change the pattern materially (new keyword) or scope to a specific path:\n")
		b.WriteString("  {\"tool\":\"search_files\",\"pattern\":\"*.go\"}\n")
		if strings.TrimSpace(req.Path) != "" {
			b.WriteString("  {\"tool\":\"read_file\",\"path\":\"")
			b.WriteString(req.Path)
			b.WriteString("\",\"start_line\":1,\"end_line\":160}\n")
		}
	default:
		b.WriteString("Pick a materially different move. Good general options:\n")
		b.WriteString("  {\"tool\":\"search_content\",\"pattern\":\"TODO|FIXME\",\"path\":\".\"}\n")
		b.WriteString("  {\"tool\":\"search_files\",\"pattern\":\"*.go\"}\n")
	}
	b.WriteString("\nOr, if you already have enough evidence, answer the user now with what you observed — do not call any more tools.\n")
	return b.String()
}

func firstNonEmptyNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isPrematureEvidenceFirstTargetRead(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		strings.TrimSpace(req.Tool) == "read_file" &&
		containsRunnerString(brief.TargetRefs, strings.TrimSpace(req.Path)) &&
		!hasFocusedNonTargetEvidence(results, brief.TargetRefs)
}

func nonTargetRecoveryFileCandidates(results []workspace.ToolResult, targetRefs []string) []string {
	raw := rejectionRecoveryFileCandidates(results)
	filtered := make([]string, 0, len(raw))
	for _, path := range raw {
		if containsRunnerString(targetRefs, path) {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

// rejectionRecoveryFileCandidates pulls up to 3 plausible files the model
// can read_file next, harvested from whatever listing results it already
// collected this turn. Preference: .go files in internal/, then anything
// .go, then any discovered path.
func rejectionRecoveryFileCandidates(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	internalGo := make([]string, 0, 3)
	anyGo := make([]string, 0, 3)
	anything := make([]string, 0, 3)
	for _, r := range results {
		for _, line := range strings.Split(r.Output, "\n") {
			path := recoveryCandidatePath(line)
			if path == "" || strings.HasPrefix(path, "/") {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			switch {
			case strings.HasSuffix(path, ".go") && strings.HasPrefix(path, "internal/"):
				if len(internalGo) < 3 {
					internalGo = append(internalGo, path)
				}
			case strings.HasSuffix(path, ".go"):
				if len(anyGo) < 3 {
					anyGo = append(anyGo, path)
				}
			default:
				if len(anything) < 3 {
					anything = append(anything, path)
				}
			}
		}
	}
	if len(internalGo) > 0 {
		return internalGo
	}
	if len(anyGo) > 0 {
		return anyGo
	}
	return anything
}

func recoveryCandidatePath(line string) string {
	path := strings.TrimSpace(line)
	path = strings.TrimPrefix(path, "- ")
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if colon := strings.IndexByte(path, ':'); colon > 0 {
		prefix := strings.TrimSpace(path[:colon])
		if strings.Contains(prefix, "/") || strings.Contains(prefix, ".") {
			path = prefix
		}
	}
	if strings.Contains(path, " ") {
		return ""
	}
	return path
}

func compactOutput(output string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}
