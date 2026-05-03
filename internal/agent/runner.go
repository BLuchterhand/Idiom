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
	"unicode"

	"idiom/internal/frontier"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

var (
	stableHandlePathLinePattern     = regexp.MustCompile(`(?m)^f\d+\s+(.+)$`)
	stableHandleInlinePattern       = regexp.MustCompile(`\bf\d+\s+((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+)\b`)
	delegatedPacketPathLinePattern  = regexp.MustCompile(`((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|md|json|yaml|yml|toml|txt)):\d+(?:-\d+)?(?:[:][^\s` + "`" + `"'(),.;:]+)?`)
	delegatedPacketPathQuotePattern = regexp.MustCompile("((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\\.(?:go|md|json|yaml|yml|toml|txt))[`'\"]+")
	closurePathLineSymbolPattern    = regexp.MustCompile(`(?m)(?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|md|json|yaml|yml|toml|txt):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathScopedSymbolPattern  = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathCallPattern          = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?:[^\n]*?\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	callLikeIdentifierPattern       = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	qualifiedIdentifierPattern      = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{1,}\.[A-Za-z_][A-Za-z0-9_]{3,})\b`)
	backtickedTokenPattern          = regexp.MustCompile("`([^`]+)`")
	goFuncDeclPattern               = regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	codeShapedTokenPattern          = regexp.MustCompile(`\b[A-Za-z][a-z]+(?:[A-Z][a-z0-9]+){1,}\b`)
	codeShapedSnakeCasePattern      = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+){1,}\b`)
	codeShapedLineRangePattern      = regexp.MustCompile(`(?i)\blines?\s+\d+(?:\s*[-–]\s*\d+)?\b`)
)

// delegatedReadOnlyProbeBudget is the soft cap on post-readiness probes the
// child explorer lane is expected to use before answering. The
// diminishing-returns nudge fires through followup count == 2 and stops
// after; this is the same number, surfaced into the readiness contract so
// the model and harness agree on the budget.
const delegatedReadOnlyProbeBudget = 2

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
	delegatedReadOnlyAnswerReadyNudged := false
	delegatedReadOnlyAnswerReady := containsRunnerString(r.RuntimeBrief.Constraints, "answer_readiness_reached")
	delegatedReadOnlyPostReadyFollowups := 0
	delegatedReadOnlyDiminishingNudgeStage := 0
	postChildPacketActive := false
	postChildFollowupReads := 0
	postChildDiminishingNudgeStage := 0
	postReadinessProbeBudget := postReadinessProbeBudgetForBrief(r.RuntimeBrief)
	// turnHiddenTools tracks tools that have been gated enough times this
	// turn that we should stop advertising them in the manifest. Removes
	// a class of waste outright: if list_files is chained past the gate,
	// hiding it for the rest of the turn prevents another round of token
	// spend on calls we're going to reject anyway.
	turnHiddenTools := make(map[string]bool)
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
		// Always include project-defined external tools, regardless of
		// the route's capability profile or the runtime brief's
		// AllowedTools narrowing. Externals are opaque shell invocations
		// the host project owns; idiom doesn't know whether a given
		// external is read-class or write-class, and the project's
		// intent in registering it is "make this callable from any
		// agent turn". MCP tools follow the same logic — once the user
		// configured an MCP server, its tools should remain reachable.
		if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
			seen := make(map[string]struct{}, len(allowed))
			for _, name := range allowed {
				seen[name] = struct{}{}
			}
			for _, name := range extReg.Names() {
				if _, ok := seen[name]; !ok {
					allowed = append(allowed, name)
					seen[name] = struct{}{}
				}
			}
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
		tools := buildLlamaTools(r.Workspace.BuildToolDefinitions(allowed))
		// Schema-level enforcement of the post-readiness contract: once
		// the lane has reached answer readiness or received a child
		// packet, mark named_gap and why_not_answering as required on
		// every read-class tool. Missing fields fail at the model's
		// tool-call decoder instead of being chased through prose
		// rejections that the model has been ignoring on the first
		// post-readiness call.
		if delegatedReadOnlyAnswerReady || postChildPacketActive {
			tools = applyPostReadinessSchemaRequirements(tools)
		}
		opts.Tools = tools
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
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	acceptedAnswerSignalsEmitted := false
	defer func() {
		r.emitBudgetLedger(policy, metrics, stopReason, budgetExhausted)
	}()
	emitAcceptedAnswerSignalsOnce := func(answer string) {
		if acceptedAnswerSignalsEmitted || strings.TrimSpace(answer) == "" {
			return
		}
		r.emitAcceptedAnswerSignals(answer, collectedResults)
		acceptedAnswerSignalsEmitted = true
	}
	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		if strings.TrimSpace(reason) != "" {
			stopReason = reason
		}
		budgetExhausted = exhausted
		if finishErr == nil {
			decorated := r.decorateFinalAnswer(FinalAnswerDecorateInput{
				Answer:     value,
				StopReason: stopReason,
				Brief:      r.RuntimeBrief,
				Results:    collectedResults,
				UserFacing: r.userFacingFinalAnswer(),
			})
			if strings.TrimSpace(decorated) != strings.TrimSpace(value) {
				r.emitFinalAnswerDecorationEvent(stopReason, value, decorated)
				if strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "" {
					r.LastAssistantMessage.Content = decorated
				}
			}
			value = decorated
			emitAcceptedAnswerSignalsOnce(value)
		}
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
		emitAcceptedAnswerSignalsOnce(answer)
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		if policy.shouldStopForDuration(metrics.startedAt) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Run duration budget is exhausted. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
			return finish(answer, err, "duration_budget_exhausted", true)
		}
		if policy.shouldStopForModelCalls(metrics.ModelCalls) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Model-call budget is exhausted. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
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
					answer, err := controller.completeWithoutMoreToolsWithReason("Completion gate has rejected the final answer repeatedly. Tools are now disabled. Rewrite the final answer so it satisfies the active task contract using the gathered evidence. Mention a blocker only if the task cannot be completed from that evidence.")
					return finish(answer, err, "completion_gate_repeated_rejection", true)
				}
				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, opts, collectedResults, &metrics)
			emitAcceptedAnswerSignalsOnce(answer)
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
		if preflight.RejectSummary != "" {
			if preflight.ForceAnswer != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: preflight.RejectSummary,
					Error:   preflight.RejectError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
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
						fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; user denied continuation. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
					)
					return finish(answer, err, "doom_loop_guard", true)
				}
			} else {
				answer, err, _ := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Doom-loop guard tripped",
					fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; stopping to prevent budget exhaustion. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
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
		if scope := postReadinessGapScope(r.agentName(), delegatedReadOnlyAnswerReady, postChildPacketActive, req); scope != "" {
			if gap := postReadinessGap(req); gap == "" {
				requestBudget.postReadinessNoGapRejections++
				if strings.HasPrefix(r.agentName(), "child:") {
					if requestBudget.postReadinessNoGapRejections >= 2 {
						prompt := "You have repeatedly issued post-readiness reads without `named_gap`. Tools are now disabled. Write the normal final answer from the evidence already gathered; if one exact claim remains unsupported, state that uncertainty after the supported answer."
						answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
						return finish(answer, err, "post_readiness_no_gap_repeated", true)
					}
					if prompt, exhausted := consumeRejectedPostReadinessProbe(scope, req, &delegatedReadOnlyPostReadyFollowups, &postChildFollowupReads, delegatedReadOnlyProbeBudget, postChildReadBudget); exhausted {
						answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
						return finish(answer, err, "post_readiness_probe_budget_exhausted", true)
					}
					summary, errorText := missingPostReadinessGapMessage(scope)
					answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
					if done {
						return finish(answer, err, "post_readiness_gap_required", true)
					}
					continue
				}
				prompt := "You issued a post-readiness read without `named_gap`. Tools are now disabled. Write the normal final answer from the evidence already gathered; if one exact claim remains unsupported, state that uncertainty after the supported answer."
				answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
				return finish(answer, err, "post_readiness_no_gap", true)
			}
			if summary, errorText, rejected := invalidPostReadinessGap(scope, r.RuntimeBrief.Objective, req, collectedResults); rejected {
				if prompt, exhausted := consumeRejectedPostReadinessProbe(scope, req, &delegatedReadOnlyPostReadyFollowups, &postChildFollowupReads, delegatedReadOnlyProbeBudget, postChildReadBudget); exhausted {
					answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
					return finish(answer, err, "post_readiness_probe_budget_exhausted", true)
				}
				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
				if done {
					return finish(answer, err, "post_readiness_gap_invalid", true)
				}
				continue
			}
			r.emitPostReadinessGapDeclared(scope, req)
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
		if decision := requestBudget.explorationDecision(r, req, collectedResults); decision.Handled {
			if decision.Nudge != "" {
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
			requestBudget.noteReadClassRequest(req)
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
				fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files is hidden for the rest of this turn. Take one narrower next step on a path you already discovered, or answer with the evidence already gathered.", executionState.consecutiveListFiles),
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
						answer, err := controller.completeWithoutMoreToolsWithReason("Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the repeated request leaves one exact claim unsupported, state that uncertainty after the supported answer.")
						return finish(answer, err, "no_progress_budget_exhausted", true)
					}
					continue
				}
			}
			errorText := "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes."
			if scope := postReadinessGapScope(
				r.agentName(),
				delegatedReadOnlyAnswerReady || delegatedReadOnlyClosureReady(r.agentName(), r.RuntimeBrief, collectedResults),
				postChildPacketActive || postChildPacketClosureReadyWithBrief(r.RuntimeBrief, collectedResults),
				req,
			); scope != "" {
				errorText = repeatedClosureRequestErrorText(scope)
			}
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
				requestBudget.readOnlyChildTaskSatisfied = true
				evidencePaths := postChildClosureEvidencePaths(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, 4)
				turnHiddenTools["task"] = true
				if childPacketContainsGroundedAnswerEvidence(r.RuntimeBrief.Objective, result.Output) {
					requestBudget.broadEvidenceDelegationSatisfied = true
					r.RuntimeBrief.Constraints = mergeRunnerStateStrings(r.RuntimeBrief.Constraints, []string{"child_packet_available"})
					postChildPacketActive = true
					postChildFollowupReads = 0
					postChildDiminishingNudgeStage = 0
					r.RuntimeBrief.EvidencePaths = mergeRunnerStateStrings(r.RuntimeBrief.EvidencePaths, evidencePaths)
					requestBudget.noteReadOnlyChildPacket(evidencePaths)
					hideBroadDiscoveryTools(turnHiddenTools)
					rebuildTools()
					requestBudget.postChildReadCalls = 0
					nudge := childPacketBoundaryNudge(r.RuntimeBrief.Objective, evidencePaths, postChildReadBudget)
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
				}
			}
			recorder.record(stepNumber, req, result, "child_task_result", true, true)
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Tools are now disabled. Use the child-task result and gathered evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "task_ledger" {
			result := taskLedger.apply(req)
			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", true, true)
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
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
				if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
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
			if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
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
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
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
					nudge = readOnlyAnswerReadyNudge(r.RuntimeBrief.Objective, collectedResults, postReadinessProbeBudget)
					eventRole = "read_only_answer_ready_nudge"
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				r.emitEvent(RunEvent{Role: eventRole, Content: nudge})
			}
			// Rebuild tools so the post-readiness schema constraint
			// (named_gap / why_not_answering required) attaches before
			// the next model call.
			if justBecameReady {
				rebuildTools()
			}
		}
		if childReadyBeforeResult && strings.HasPrefix(r.agentName(), "child:") && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			delegatedReadOnlyPostReadyFollowups++
			// Skip the diminishing-returns nudge on the very probe that
			// will trigger the disclosed-budget force-answer below:
			// firing both back-to-back wastes a turn message the model
			// never gets to react to before tools are disabled.
			budgetWillForce := postReadinessProbeBudget > 0 && delegatedReadOnlyPostReadyFollowups >= postReadinessProbeBudget
			if !budgetWillForce && delegatedReadOnlyPostReadyFollowups > delegatedReadOnlyDiminishingNudgeStage {
				if nudge := delegatedReadOnlyDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, delegatedReadOnlyPostReadyFollowups); nudge != "" {
					delegatedReadOnlyDiminishingNudgeStage = delegatedReadOnlyPostReadyFollowups
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "delegated_read_only_diminishing_returns_nudge", Content: nudge})
				}
			}
			// Disclosed-budget enforcement: the readiness contract told
			// the model "use at most N targeted probes." Once the count
			// hits N, force-answer with a stop reason that names the
			// disclosed budget — instead of letting MaxToolSteps end the
			// lane on the (N+1)th attempt with a generic exhaustion.
			if budgetWillForce {
				prompt := forcedSynthesisPromptReadOnly(delegatedReadOnlyPostReadyFollowups, postReadinessProbeBudget)
				answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
				return finish(answer, err, "post_readiness_probe_budget_exhausted", true)
			}
		}
		if postChildPacketActive && strings.TrimSpace(req.Tool) != "task" && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			postChildFollowupReads++
			budgetWillForce := postChildReadBudget > 0 && postChildFollowupReads >= postChildReadBudget
			if !budgetWillForce && postChildFollowupReads > postChildDiminishingNudgeStage {
				if nudge := postChildDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, postChildFollowupReads); nudge != "" {
					postChildDiminishingNudgeStage = postChildFollowupReads
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_diminishing_returns_nudge", Content: nudge})
				}
			}
			// Same disclosed-budget enforcement on the parent side once
			// the child packet is in hand. Caps at the live
			// postChildReadBudget value (the same number disclosed in
			// the boundary nudge).
			if budgetWillForce {
				prompt := forcedSynthesisPromptPostChild(postChildFollowupReads, postChildReadBudget)
				answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
				return finish(answer, err, "post_readiness_probe_budget_exhausted", true)
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
			answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
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
	return "Maximum tool steps for this turn have been reached. Tools are now disabled. Respond with a concise, direct, text-only final answer synthesized from the evidence already gathered. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action. Do not leave the response blank."
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

func allowRecoverablePostReadinessRepairExecution(req workspace.ToolRequest, lastToolResult workspace.ToolResult) bool {
	if postReadinessGap(req) == "" {
		return false
	}
	if isRecoverablePostReadinessGapRejection(lastToolResult.Summary) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(lastToolResult.Summary + " " + lastToolResult.Error))
	return strings.Contains(lower, "missing named gap") || strings.Contains(lower, "include `named_gap`")
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
		Constraints:               runnerGuidanceConstraints(r.agentName(), brief, results),
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})
	failureClass := ""
	if strings.TrimSpace(result.Error) != "" {
		failureClass = workerruntime.ClassifyFailure("current_state", req, result)
	}
	state := workerruntime.RunState{
		Lane:           r.agentName(),
		Phase:          runnerLivePhase(brief, req, result, results),
		Step:           step,
		EvidencePaths:  promptEvidencePathsFromToolResults(results, 6),
		ChangedPaths:   mapKeys(changedPathsFromToolResults(results)),
		Constraints:    mergeRunnerStateStrings(brief.Constraints, guidance.Constraints),
		LastTool:       firstNonEmpty(strings.TrimSpace(req.Tool), strings.TrimSpace(result.Tool)),
		LastResult:     workerruntime.SummarizeToolResult(result),
		FailureClass:   failureClass,
		NextActions:    mergeRunnerStateStrings(brief.RequiredNext, guidance.ValidNext),
		InvalidActions: mergeRunnerStateStrings(brief.InvalidActions, guidance.InvalidNext),
	}
	if !strings.HasPrefix(strings.TrimSpace(r.agentName()), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		state.EvidencePaths = postChildClosureEvidencePaths(brief.Objective, results, 6)
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
	brief.EvidencePaths = mergeRunnerStateStrings(brief.EvidencePaths, promptEvidencePathsFromToolResults(results, 8))
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
		Constraints:               runnerGuidanceConstraints(agent, brief, results),
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
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
		EvidencePaths:     promptEvidencePathsFromToolResults(results, 6),
	}
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") && delegatedReadOnlyClosureReady(agent, brief, results) {
		state.EvidencePaths = delegatedClosureEvidencePaths(brief.Objective, results, 6)
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		state.EvidencePaths = postChildClosureEvidencePaths(brief.Objective, results, 6)
	}
	state.Constraints = mergeRunnerStateStrings(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = mergeRunnerStateStrings(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = mergeRunnerStateStrings(brief.RequiredNext, guidance.ValidNext)
	state = applyDelegatedReadOnlyClosureBias(state, agent, brief, results)
	state = applyPostChildClosureBias(state, agent, results)
	state = applyClosurePromptRefinement(state)
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
		Constraints:               state.Constraints,
		DeclarationMovesForbidden: declarationMovesForbidden,
		Objective:                 brief.Objective,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})
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
		ValidNext:          append([]string(nil), recovery.ValidNext...),
		UserVisibleBlocker: recovery.UserVisibleBlocker,
	}
	packet.DoNotRepeat = append([]string(nil), recovery.DoNotRepeat...)
	packet.RequiredNext = append([]string(nil), recovery.RequiredNext...)
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

func prependRunnerStateStrings(preferred []string, existing []string) []string {
	out := make([]string, 0, len(preferred)+len(existing))
	for _, value := range preferred {
		out = appendUniqueRunnerState(out, value)
	}
	for _, value := range existing {
		out = appendUniqueRunnerState(out, value)
	}
	return out
}

func filterRunnerStateStrings(values []string, drop func(string) bool) []string {
	if len(values) == 0 {
		return values
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || drop(trimmed) {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return filtered
}

func runnerGuidanceConstraints(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	constraints := append([]string(nil), brief.Constraints...)
	if delegatedReadOnlyClosureReady(agent, brief, results) {
		constraints = mergeRunnerStateStrings(constraints, []string{"answer_readiness_reached"})
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		constraints = mergeRunnerStateStrings(constraints, []string{"child_packet_available"})
	}
	return constraints
}

func applyDelegatedReadOnlyClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !delegatedReadOnlyClosureReady(agent, brief, results) {
		return state
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{"answer_readiness_reached"})
	state.InvalidNextActions = filterRunnerStateStrings(state.InvalidNextActions, func(value string) bool {
		return value == "final_answer_without_satisfying_completion_gate"
	})
	state.NextValidActions = filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "gather or edit the missing evidence")
	})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"answer directly from the evidence already gathered",
	}, state.NextValidActions)
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"open-ended discovery after answer readiness without naming a concrete gap",
	})
	return state
}

func delegatedReadOnlyClosureReady(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if containsRunnerString(brief.Constraints, "answer_readiness_reached") {
		return true
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") || strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" {
		return false
	}
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	return delegatedReadOnlyHasCompositeDiscoveryPacket(results) || len(delegatedReadOnlyInspectedImplementationPaths(results)) >= 2
}

func applyPostChildClosureBias(state runnerNextActionState, agent string, results []workspace.ToolResult) runnerNextActionState {
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") || !postChildPacketClosureReadyWithBrief(workerruntime.WorkerRuntimeBrief{Constraints: state.Constraints}, results) {
		return state
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{"child_packet_available"})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"answer directly from the child packet if it already resolves the question",
	}, state.NextValidActions)
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"open-ended discovery after a child packet without naming a concrete gap",
	})
	return state
}

func postChildPacketClosureReady(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if childPacketContainsGroundedAnswerEvidence("", result.Output) {
			return true
		}
	}
	return false
}

func postChildPacketClosureReadyWithBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if containsRunnerString(brief.Constraints, "child_packet_available") {
		return true
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if childPacketContainsGroundedAnswerEvidence(brief.Objective, result.Output) {
			return true
		}
	}
	return false
}

func runnerClosureGuidanceActive(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) bool {
	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		return true
	}
	if delegatedReadOnlyClosureReady(agent, brief, results) {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") {
		return false
	}
	return postChildPacketClosureReadyWithBrief(brief, results)
}

// forcedSynthesisPromptDelegated and forcedSynthesisPromptPostChild build
// the prompts handed to the model when the disclosed post-readiness probe
// budget is hit. These prompts have to elicit an actual synthesis — the
// previous "Synthesize a direct answer from the inspected evidence" was
// too soft and produced 100-token replies that left the user with a
// fallback template instead of a real answer. The new prompts name the
// concrete obligations: cite paths, use the body content, write at least
// a substantive paragraph, and only fall back to gap-naming as a last
// resort.
func forcedSynthesisPromptDelegated(used, budget int) string {
	return fmt.Sprintf(
		"You have used your disclosed post-readiness probe budget (%d/%d). Tools are now disabled. Write the final answer to the delegated question now, citing each implementation path and symbol you inspected. Use the body content from your symbol/extract probes — do not just list paths. Aim for a substantive synthesis (a paragraph or two) anchored in what you actually read. Only after that, if a specific gap still remains, state it in prose at the end.",
		used, budget,
	)
}

func forcedSynthesisPromptReadOnly(used, budget int) string {
	return fmt.Sprintf(
		"You have used your disclosed post-readiness probe budget (%d/%d). Tools are now disabled. Write the final answer now from the inspected evidence, citing the implementation paths and symbols you actually read. Use the body content and call/reference evidence — do not just list paths. Aim for a substantive synthesis (a paragraph or two). Only after that, if a specific gap still remains, state it in prose at the end.",
		used, budget,
	)
}

func forcedSynthesisPromptPostChild(used, budget int) string {
	return fmt.Sprintf(
		"You have used your disclosed post-child probe budget (%d/%d). Tools are now disabled. Write the final answer to the user's question now, anchored in the child packet plus the bodies from your %d targeted probe(s). Cite each path and symbol you inspected. Use the body content — do not just list paths. Aim for a substantive synthesis (a paragraph or two). Only after that, if a specific gap still remains, state it in prose at the end.",
		used, budget, used,
	)
}

func consumeRejectedPostReadinessProbe(scope string, req workspace.ToolRequest, delegatedCount, postChildCount *int, delegatedBudget, postChildBudget int) (string, bool) {
	if !isReadClassTool(req) {
		return "", false
	}
	switch strings.TrimSpace(scope) {
	case "delegated_read_only_answer_ready":
		fallthrough
	case "answer_readiness_reached":
		if delegatedBudget <= 0 {
			if strings.TrimSpace(scope) == "delegated_read_only_answer_ready" {
				return "No further post-readiness probes are budgeted. Tools are now disabled. Write the final answer to the delegated question now from the evidence already gathered.", true
			}
			return "No further post-readiness probes are budgeted. Tools are now disabled. Write the final answer now from the evidence already gathered.", true
		}
		if delegatedCount != nil {
			*delegatedCount++
			if *delegatedCount >= delegatedBudget {
				if strings.TrimSpace(scope) == "delegated_read_only_answer_ready" {
					return forcedSynthesisPromptDelegated(*delegatedCount, delegatedBudget), true
				}
				return forcedSynthesisPromptReadOnly(*delegatedCount, delegatedBudget), true
			}
		}
	case "child_packet_available":
		if postChildBudget <= 0 {
			return "No further post-child probes are budgeted. Tools are now disabled. Write the final answer to the user's question now from the child packet and the evidence already gathered.", true
		}
		if postChildCount != nil {
			*postChildCount++
			if *postChildCount >= postChildBudget {
				return forcedSynthesisPromptPostChild(*postChildCount, postChildBudget), true
			}
		}
	}
	return "", false
}

func closureSynthesisAfterIgnoredNudgePrompt(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool, delegatedDiminishingNudges int, postChildDiminishingNudges int) (string, bool) {
	if !isReadClassTool(req) || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}
	// North-star contract: after readiness, the model is allowed "answer
	// now or one exact probe with a named gap." If the model has declared
	// a gap, it is satisfying the contract — let the upstream gap-shape
	// validator and the budget caps do their work. Force-closing here
	// short-circuits the contract on a request that just complied with
	// it, which is exactly the failure mode flagged in the in-flight log
	// (a declared-gap go_read_symbol probe was force-completed before the
	// tool ran).
	if postReadinessGap(req) != "" {
		return "", false
	}
	closureReady := false
	nudgeAlreadyFired := false
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") {
		closureReady = delegatedReadOnlyAnswerReady || delegatedReadOnlyClosureReady(agent, brief, results)
		nudgeAlreadyFired = delegatedDiminishingNudges > 0
	} else {
		closureReady = postChildPacketActive || postChildPacketClosureReadyWithBrief(brief, results)
		nudgeAlreadyFired = postChildDiminishingNudges > 0
	}
	if !closureReady || !nudgeAlreadyFired {
		return "", false
	}
	paths := delegatedClosureEvidencePaths(brief.Objective, results, 4)
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") {
		paths = postChildClosureEvidencePaths(brief.Objective, results, 4)
	}
	var b strings.Builder
	b.WriteString("Closure evidence is already sufficient and a closure nudge has already been followed by another read/search request. Tools are now disabled. Synthesize a direct answer from the retained evidence.")
	if len(paths) > 0 {
		b.WriteString("\nCarry forward the relevant inspected paths: ")
		b.WriteString(summarizePostChildEvidencePaths(paths, 4))
		b.WriteString(".")
	}
	b.WriteString("\nIf one detail remains uncertain, state that uncertainty in prose; do not request more tools or return a partial-findings template.")
	return b.String(), true
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
		ledger.UserVisibleUncertainty = "answer from gathered evidence and mention only unsupported claims that remain material"
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
	if finish := strings.TrimSpace(reply.FinishReason); finish != "" {
		payload["finish_reason"] = finish
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

func (r *Runner) emitDelegatedReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, results, 8)
	payload := map[string]any{
		"agent":                      r.agentName(),
		"reason":                     "sufficient_delegated_read_only_evidence",
		"trigger_tool":               strings.TrimSpace(req.Tool),
		"composite_discovery_packet": delegatedReadOnlyHasCompositeDiscoveryPacket(results),
		"evidence_paths":             paths,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "delegated_read_only_answer_ready", Content: string(data)})
}

func (r *Runner) emitReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	paths := promptEvidencePathsFromToolResults(results, 8)
	payload := map[string]any{
		"agent":          r.agentName(),
		"reason":         "sufficient_read_only_evidence",
		"trigger_tool":   strings.TrimSpace(req.Tool),
		"evidence_paths": paths,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "read_only_answer_ready", Content: string(data)})
}

func (r *Runner) emitPostReadinessGapDeclared(scope string, req workspace.ToolRequest) {
	if r == nil || r.OnEvent == nil {
		return
	}
	payload := map[string]any{
		"agent":             r.agentName(),
		"scope":             strings.TrimSpace(scope),
		"tool":              strings.TrimSpace(req.Tool),
		"target":            workerruntime.ToolRequestTarget(req),
		"named_gap":         strings.TrimSpace(req.NamedGap),
		"why_not_answering": strings.TrimSpace(req.WhyNotAnswering),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "post_readiness_gap_declared", Content: string(data)})
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
		PinnedPaths:      promptEvidencePathsFromToolResults(results, 8),
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
	if paths := promptEvidencePathsFromToolResults(results, 8); len(paths) > 0 {
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
			for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(field)) {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}
	paths = prioritizedClosureEvidencePaths(paths, results, len(paths))
	return limitRunnerStrings(paths, max)
}

func promptEvidencePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	paths := make([]string, 0, max*2)
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*6)...)
	paths = prioritizedClosureEvidencePaths(paths, results, max*3)
	return closurePromptEvidencePaths(paths, max)
}

func postChildClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		paths = append(paths, groundedChildPacketEvidencePaths(objective, result.Output)...)
	}
	paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)
	return closurePromptEvidencePaths(paths, max)
}

func childPacketBoundaryNudge(_ string, evidencePaths []string, probeBudget int) string {
	evidencePaths = closurePromptEvidencePaths(evidencePaths, 4)
	validity, _ := closureDecisionRationale("", nil, evidencePaths)
	lines := []string{
		"The delegated read_only child result is now your evidence packet.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(evidencePaths) > 0 {
		lines = append(lines, "Packet-named paths: "+summarizePostChildEvidencePaths(evidencePaths, 3))
	}
	lines = append(lines, postReadinessContinuationContract("child_packet_available", probeBudget))
	lines = append(lines,
		"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
		"Answer now from the packet.",
	)
	return strings.Join(lines, "\n")
}

func postReadinessGapScope(agent string, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool, req workspace.ToolRequest) string {
	if !isReadClassTool(req) {
		return ""
	}
	if postChildPacketActive && strings.TrimSpace(req.Tool) != "task" {
		return "child_packet_available"
	}
	if delegatedReadOnlyAnswerReady {
		if strings.HasPrefix(strings.TrimSpace(agent), "child:") {
			return "delegated_read_only_answer_ready"
		}
		return "answer_readiness_reached"
	}
	return ""
}

func postReadinessGap(req workspace.ToolRequest) string {
	return firstNonEmpty(req.NamedGap, req.WhyNotAnswering)
}

func invalidPostReadinessGap(scope string, objective string, req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	evidencePaths := closureEvidencePathsForScope(scope, results)
	if len(evidencePaths) == 0 {
		if strings.TrimSpace(scope) == "child_packet_available" {
			return "", "", false
		}
	}
	if summary, errorText, rejected := invalidPostReadinessGapContent(scope, objective, req); rejected {
		return summary, errorText, true
	}
	if strings.TrimSpace(scope) == "child_packet_available" {
		if postChildGapContradictsKnownEvidence(req, evidencePaths) {
			return "Post-child follow-up gap is contradicted by current evidence",
				"the child packet already surfaced grounded source evidence " + summarizePostChildEvidencePaths(evidencePaths, 3) + ". Do not claim that source files or harness code are still unseen when they are already in the packet. If a real gap remains, name the exact unresolved claim and anchor it to one packet-named path or symbol; otherwise answer now from the child packet.",
				true
		}
		if postChildFollowupNeedsPacketAnchor(req) && !postChildFollowupIsPacketAnchored(req, evidencePaths) {
			return "Post-child follow-up gap is not packet-anchored",
				"the child packet already surfaced relevant source paths " + summarizePostChildEvidencePaths(evidencePaths, 3) + ". If you continue, anchor the follow-up to one packet-named path, discovered handle, or packet-derived symbol and state the exact claim it would resolve. Do not restart repo-wide filename or content search.",
				true
		}
	}
	if summary, errorText, rejected := invalidPostReadinessProbeShape(scope, req, results, evidencePaths); rejected {
		return summary, errorText, true
	}
	return "", "", false
}

func closureEvidencePathsForScope(scope string, results []workspace.ToolResult) []string {
	switch strings.TrimSpace(scope) {
	case "delegated_read_only_answer_ready":
		return delegatedClosureEvidencePaths("", results, 8)
	case "child_packet_available":
		return postChildClosureEvidencePaths("", results, 8)
	default:
		return promptEvidencePathsFromToolResults(results, 8)
	}
}

func invalidPostReadinessProbeShape(scope string, req workspace.ToolRequest, results []workspace.ToolResult, evidencePaths []string) (string, string, bool) {
	if summary, errorText, rejected := invalidPostReadinessSemanticProbe(scope, req, results, evidencePaths); rejected {
		return summary, errorText, true
	}
	if summary, errorText, rejected := invalidPostReadinessSearchProbe(scope, req, evidencePaths); rejected {
		return summary, errorText, true
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}
	path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
	if path == "" {
		return "", "", false
	}
	gap := strings.TrimSpace(req.NamedGap)
	span := postReadinessReadSpan(req)
	fileTopicGap := postReadinessGapLooksFileTopic(gap, path)
	tooWide := span > 160
	inspected := containsRunnerString(delegatedReadOnlyInspectedImplementationPaths(results), path)
	pathCandidates := closureProbeCandidatesByPath(results, 4)
	knownExactProbes := pathCandidates[path]
	rawWholeFileRead := span == 0
	if !fileTopicGap && !tooWide && !(rawWholeFileRead && len(knownExactProbes) > 0) {
		return "", "", false
	}
	scopeLabel := "readiness"
	answerLabel := "current evidence"
	if strings.TrimSpace(scope) == "child_packet_available" {
		scopeLabel = "the child packet"
		answerLabel = "child packet"
	}
	lines := []string{
		fmt.Sprintf("after %s, a file-level raw read is too broad to count as one exact probe.", scopeLabel),
		"Name one exact branch, function body, symbol, caller edge, or narrow line range that would change the answer, or answer now from the evidence already gathered.",
	}
	if inspected {
		lines = append(lines, fmt.Sprintf("The current evidence already includes `%s`; do not keep rereading the same file at file level after readiness.", path))
	}
	if rawWholeFileRead && len(knownExactProbes) > 0 {
		lines = append(lines, "Known exact probes for this path: "+quotedRunnerStrings(knownExactProbes)+".")
	}
	if tooWide {
		lines = append(lines, fmt.Sprintf("The requested window (%d lines) is too wide for an exact probe after readiness.", span))
	}
	if len(evidencePaths) > 0 {
		lines = append(lines, "Current evidence already centers on "+summarizePostChildEvidencePaths(evidencePaths, 3)+".")
	}
	if suggestion := closureProbeSuggestionLine(results, 3); suggestion != "" {
		lines = append(lines, suggestion)
	}
	lines = append(lines, fmt.Sprintf("Choose one exact symbol/body/branch or answer now from the %s.", answerLabel))
	return "Post-readiness follow-up is not an exact probe", strings.Join(lines, " "), true
}

func invalidPostReadinessSearchProbe(scope string, req workspace.ToolRequest, evidencePaths []string) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != "search_content" {
		return "", "", false
	}
	pattern := strings.TrimSpace(req.Pattern)
	if !postReadinessSearchPatternLooksBroad(pattern) {
		return "", "", false
	}
	scopeLabel := "readiness"
	answerLabel := "current evidence"
	if strings.TrimSpace(scope) == "child_packet_available" {
		scopeLabel = "the child packet"
		answerLabel = "child packet"
	}
	lines := []string{
		fmt.Sprintf("after %s, `search_content` must be one exact probe, not a broad rediscovery pattern.", scopeLabel),
		"Use a concrete symbol/literal or narrow range whose result would change the answer, or answer now from the evidence already gathered.",
	}
	if len(evidencePaths) > 0 {
		lines = append(lines, "Current evidence already centers on "+summarizePostChildEvidencePaths(evidencePaths, 3)+".")
	}
	lines = append(lines, fmt.Sprintf("Choose one evidence-grounded exact probe or answer now from the %s.", answerLabel))
	return "Post-readiness search is broader than the current evidence requires", strings.Join(lines, " "), true
}

func postReadinessSearchPatternLooksBroad(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	if strings.Contains(pattern, "|") || strings.Contains(pattern, ".*") || (strings.Contains(pattern, "[") && strings.Contains(pattern, "]")) {
		return true
	}
	return len(postReadinessTokenSet(pattern)) == 0 && !codeShapedTokenPattern.MatchString(pattern) && !codeShapedSnakeCasePattern.MatchString(pattern)
}

func invalidPostReadinessSemanticProbe(scope string, req workspace.ToolRequest, results []workspace.ToolResult, evidencePaths []string) (string, string, bool) {
	tool := strings.TrimSpace(req.Tool)
	path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
	if path == "" {
		return "", "", false
	}
	scopeLabel := "readiness"
	answerLabel := "current evidence"
	if strings.TrimSpace(scope) == "child_packet_available" {
		scopeLabel = "the child packet"
		answerLabel = "child packet"
	}
	pathCandidates := closureProbeCandidatesByPath(results, 4)
	candidates := pathCandidates[path]
	if isClosureOverviewTool(tool) {
		lines := []string{
			fmt.Sprintf("after %s, a file-level outline is broad orientation, not one exact probe for `%s`.", scopeLabel, path),
			"Use one exact symbol/body/range probe whose result would change the answer, or answer now from the evidence already gathered.",
		}
		if len(candidates) > 0 {
			lines = append(lines, "Known exact probes for this path: "+quotedRunnerStrings(candidates)+".")
		}
		if len(evidencePaths) > 0 {
			lines = append(lines, "Current evidence already centers on "+summarizePostChildEvidencePaths(evidencePaths, 3)+".")
		}
		lines = append(lines, fmt.Sprintf("Choose one evidence-grounded exact probe or answer now from the %s.", answerLabel))
		return "Post-readiness follow-up is broader than the current evidence requires", strings.Join(lines, " "), true
	}
	if !isClosureExactProbeTool(tool) {
		return "", "", false
	}
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" || len(candidates) == 0 {
		return "", "", false
	}
	if containsExactProbeCandidate(candidates, pattern) {
		return "", "", false
	}
	lines := []string{
		fmt.Sprintf("after %s, this exact probe is not grounded in the current evidence for `%s`.", scopeLabel, path),
		"Do not invent a new symbol guess on the same path when the existing evidence already surfaced more direct exact probes.",
		"Known exact probes for this path: " + quotedRunnerStrings(candidates) + ".",
	}
	if len(evidencePaths) > 0 {
		lines = append(lines, "Current evidence already centers on "+summarizePostChildEvidencePaths(evidencePaths, 3)+".")
	}
	lines = append(lines, fmt.Sprintf("Use one evidence-grounded exact probe or answer now from the %s.", answerLabel))
	return "Post-readiness exact probe is not grounded in current evidence", strings.Join(lines, " "), true
}

func postReadinessReadSpan(req workspace.ToolRequest) int {
	if req.StartLine <= 0 || req.EndLine <= 0 || req.EndLine < req.StartLine {
		return 0
	}
	return req.EndLine - req.StartLine + 1
}

// invalidPostReadinessGapContent checks the content quality of named_gap and
// why_not_answering. The harness already validates presence and probe shape;
// this catches gaps that are *syntactically* valid but *semantically* empty
// — paraphrases of the original question or generic "explain how X works"
// asks that cannot be falsified by a single probe. Generic across question
// types and lanes — the validator inspects only the gap text vs. the
// objective text and looks for code-shaped tokens, no domain-specific rules.
func invalidPostReadinessGapContent(scope string, objective string, req workspace.ToolRequest) (string, string, bool) {
	gap := strings.TrimSpace(req.NamedGap)
	if gap == "" {
		gap = strings.TrimSpace(req.WhyNotAnswering)
	}
	if gap == "" {
		return "", "", false
	}
	if postReadinessGapParaphrasesObjective(gap, objective) {
		scopeLabel := "current evidence"
		if strings.TrimSpace(scope) == "child_packet_available" {
			scopeLabel = "the child packet"
		}
		return "Post-readiness named_gap restates the original question",
			"the named_gap repeats the question rather than naming an exact unresolved claim. A real gap names one specific symbol body, branch, line range, or call edge whose answer would be different depending on what the probe finds. Either answer now from " + scopeLabel + ", or restate the gap as a falsifiable claim about a specific code element.",
			true
	}
	if postReadinessGapIsGenericExplainer(gap) {
		return "Post-readiness named_gap is an explainer ask, not a falsifiable claim",
			"the named_gap reads as \"explain how X works\" rather than naming a specific symbol body, branch, line range, or call edge whose answer would change based on the probe. Either answer the explainer from the evidence already gathered, or restate the gap as a single concrete claim about one named code element (e.g. \"the body of `Foo.Bar` at runner.go:142\").",
			true
	}
	return "", "", false
}

// postReadinessGapParaphrasesObjective returns true when the gap text is
// largely the same content words as the objective (the user's original
// question). Uses Jaccard similarity over lowercased tokens of length ≥ 4
// after stripping common stop words. A near-duplicate gap is a syntactic
// satisfaction of the requirement, not a meaningful new claim.
func postReadinessGapParaphrasesObjective(gap string, objective string) bool {
	gapTokens := postReadinessTokenSet(gap)
	objTokens := postReadinessTokenSet(objective)
	// Real user questions are often short ("Explain how X uses Y") so
	// after stop-word filtering only 2 content tokens may remain. Don't
	// give those a free pass — apply the check whenever both sides have
	// at least two content tokens.
	if len(gapTokens) < 2 || len(objTokens) < 2 {
		return false
	}
	overlap := 0
	for token := range gapTokens {
		if _, ok := objTokens[token]; ok {
			overlap++
		}
	}
	denom := len(gapTokens) + len(objTokens) - overlap
	if denom <= 0 {
		return false
	}
	jaccard := float64(overlap) / float64(denom)
	return jaccard >= 0.5
}

// postReadinessGapIsGenericExplainer returns true when the gap reads like an
// "explain how X works" / "understand the flow of Y" ask without naming any
// code-shaped token (CamelCase symbol, snake_case identifier, file path with
// extension, or explicit line-range reference). These gaps cannot be closed
// by a single targeted probe — they're a request for synthesis, which is
// exactly what the model should produce as its answer.
func postReadinessGapIsGenericExplainer(gap string) bool {
	lower := strings.ToLower(strings.TrimSpace(gap))
	if lower == "" {
		return false
	}
	hasExplainerVerb := containsAny(lower,
		"explain how", "explain the", "understand how", "understand the",
		"describe how", "describe the",
		"how does", "how do ", "how the ",
		"what does", "what is the",
		"overview of", "general flow", "high-level", "high level",
	)
	if !hasExplainerVerb {
		return false
	}
	return !postReadinessGapNamesCodeToken(gap)
}

// postReadinessGapNamesCodeToken returns true when the gap contains at least
// one token that looks like a concrete code element: a CamelCase symbol, a
// snake_case identifier of length ≥ 4, a workspace path with a recognized
// file extension, or an explicit line-range reference. Generic across
// languages.
func postReadinessGapNamesCodeToken(gap string) bool {
	if codeShapedTokenPattern.MatchString(gap) {
		return true
	}
	if codeShapedSnakeCasePattern.MatchString(gap) {
		return true
	}
	if codeShapedLineRangePattern.MatchString(gap) {
		return true
	}
	for _, field := range strings.Fields(gap) {
		field = strings.Trim(field, "`'\"()[]{}<>,.:;!?")
		if frontier.HasWorkspaceFileExtension(field) {
			return true
		}
	}
	return false
}

// postReadinessTokenSet returns the lowercased content tokens of length ≥ 4
// from s, excluding common English stop words and explanatory verbs that
// don't carry topic content. Used for paraphrase-detection.
func postReadinessTokenSet(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if len(raw) < 4 {
			continue
		}
		switch raw {
		case "explain", "describe", "understand", "what", "where", "when",
			"this", "that", "these", "those", "with", "from", "into",
			"about", "have", "does", "doesn", "deos", "make", "makes",
			"work", "works", "using", "uses", "used", "utilize", "utilizes":
			continue
		}
		tokens[raw] = struct{}{}
	}
	return tokens
}

func postReadinessGapLooksFileTopic(gap string, path string) bool {
	lowerGap := strings.ToLower(strings.TrimSpace(gap))
	if lowerGap == "" {
		return false
	}
	if containsAny(lowerGap, "exact branch", "exact function", "function body", "symbol body", "exact symbol", "call site", "caller edge", "narrow line range", "exact range", "specific branch", "specific function", "specific symbol") {
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), filepath.Ext(strings.TrimSpace(path))))
	full := strings.ToLower(strings.TrimSpace(filepath.Base(path)))
	if containsAny(lowerGap, "understand how", "explain how", "how "+base, "how "+full, "inspect how", "read how") {
		return true
	}
	if (strings.Contains(lowerGap, base) || strings.Contains(lowerGap, full)) &&
		containsAny(lowerGap, "implements", "determines", "describes", "uses", "handles", "utilizes", "wires", "routes", "delegation") {
		return true
	}
	return false
}

func postChildFollowupNeedsPacketAnchor(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.ID) != "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "search_files":
		return true
	case "search_content", "read_file", "rg", "grep":
		return true
	default:
		path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
		return strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, "idiom/")
	}
}

func postChildFollowupIsPacketAnchored(req workspace.ToolRequest, evidencePaths []string) bool {
	if strings.TrimSpace(req.ID) != "" {
		return true
	}
	path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, "idiom/") {
		return false
	}
	for _, evidencePath := range evidencePaths {
		if path == evidencePath {
			return true
		}
	}
	return false
}

func postChildGapContradictsKnownEvidence(req workspace.ToolRequest, evidencePaths []string) bool {
	lower := strings.ToLower(strings.TrimSpace(strings.Join([]string{req.NamedGap, req.WhyNotAnswering}, " ")))
	if lower == "" {
		return false
	}
	if !containsAny(lower,
		"have not yet seen",
		"haven't seen",
		"have not seen",
		"need to find the actual source",
		"need actual source files",
		"need to find source files",
		"need to locate the harness code",
		"have not yet seen any harness code",
		"have not yet seen harness code",
		"child task output was incomplete and i need to find the actual source files",
	) {
		return false
	}
	return len(evidencePaths) > 0
}

func missingPostReadinessGapMessage(scope string) (string, string) {
	switch strings.TrimSpace(scope) {
	case "delegated_read_only_answer_ready":
		return "Post-readiness follow-up missing named gap",
			"answer readiness has already been reached for this delegated read_only lane. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now. Prefer one specific symbol read, discovered handle, or narrow line range over repeating a raw path request. If no concrete gap remains, answer now."
	case "answer_readiness_reached":
		return "Post-readiness follow-up missing named gap",
			"answer readiness has already been reached for this read-only lane. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now. Prefer one specific symbol read, call/reference probe, discovered handle, or narrow line range over repeating broad search or raw path guesses. If no concrete gap remains, answer now."
	case "child_packet_available":
		return "Post-child follow-up missing named gap",
			"the child packet already contains sufficient evidence to bias toward answering. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now. Prefer one specific packet-named path, symbol, discovered handle, or narrow line range over repeating a raw path request. Ignore packet artifacts that are not real repository file paths. If no concrete gap remains, answer now from the child packet."
	default:
		return "Post-readiness follow-up missing named gap",
			"before any additional read/search call after readiness, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify."
	}
}

func repeatedClosureRequestErrorText(scope string) string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return "you just retried the same follow-up after the child packet. Do not reopen broad discovery. Either answer from the child packet, or choose one exact packet-named symbol, discovered handle, or narrow line range that resolves a still-material missing claim."
	default:
		return "you just retried the same post-readiness follow-up. Do not reopen broad discovery. Either answer from the current evidence, or choose one exact symbol, discovered handle, or narrow line range that resolves a still-material missing claim."
	}
}

func delegatedReadOnlyAnswerReadyNudge(objective string, results []workspace.ToolResult, probeBudget int) string {
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"Answer now from the evidence above. The delegated read-only evidence is sufficient for this question.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}
	lines = append(lines, postReadinessContinuationContract("delegated_read_only_answer_ready", probeBudget))
	lines = append(lines,
		"Do not read additional files for completeness. Carry forward the inspected paths above and keep the answer minimal — no side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
	)
	return strings.Join(lines, "\n")
}

func readOnlyAnswerReadyNudge(objective string, results []workspace.ToolResult, probeBudget int) string {
	paths := closurePromptEvidencePaths(promptEvidencePathsFromToolResults(results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"Answer now from the evidence above. The read-only evidence is sufficient for this question.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}
	lines = append(lines, postReadinessContinuationContract("answer_readiness_reached", probeBudget))
	lines = append(lines,
		"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit. Do not read additional files for completeness.",
	)
	return strings.Join(lines, "\n")
}

func postReadinessProbeBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case "architecture_assessment", "review_findings":
		return 1
	default:
		return delegatedReadOnlyProbeBudget
	}
}

// postReadinessContinuationContract returns the generic block that discloses,
// up front, every constraint the harness will enforce on subsequent
// read/search calls after readiness or a child packet. Putting this in the
// readiness/boundary nudge — rather than letting the model discover each
// constraint via a separate rejection — collapses the rejection ladder that
// otherwise burns budget on retries the model could have avoided.
//
// budget is the number of post-readiness probes the harness will allow
// before forcing the answer. Disclosing the real cap (rather than a
// hard-coded "one") prevents the model/harness mismatch where the contract
// implies one probe but the budget caps actually allow two.
func postReadinessContinuationContract(scope string, budget int) string {
	anchor := "file-level raw reads will be rejected as too broad; prefer a symbol, branch, body, or narrow line-range probe"
	if strings.TrimSpace(scope) == "child_packet_available" {
		anchor = "repo-wide search will be rejected; anchor to a packet-named path or symbol with a symbol, branch, body, or narrow line-range probe"
	} else if strings.TrimSpace(scope) == "answer_readiness_reached" {
		anchor = "repo-wide rediscovery will be rejected; target a symbol, call/reference, branch, body, or narrow line-range probe tied to inspected paths"
	}
	probeBudgetLine := "one targeted probe"
	switch {
	case budget <= 0:
		probeBudgetLine = "no further reads"
	case budget == 1:
	default:
		probeBudgetLine = fmt.Sprintf("at most %d targeted probes", budget)
	}
	lines := []string{
		"Answer now unless one exact unresolved claim remains.",
		"To continue, include `named_gap` (one exact unresolved claim) and `why_not_answering` (what would be materially wrong if you answered now); " + anchor + "; " + probeBudgetLine + " before answering.",
		"The gap must reference a code-shaped token (symbol name, file path, or narrow line range) and must not paraphrase the original question.",
	}
	return strings.Join(lines, "\n")
}

func delegatedReadOnlyDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}
	// The readiness nudge already disclosed the continuation contract
	// (named_gap required, file-level reads rejected, one targeted probe).
	// Repeating that prose burns context for no behavior change. Emit a
	// factual probe-budget note instead and let the model decide.
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 3)
	line := fmt.Sprintf("Post-readiness probes used: %d. Answer now from the evidence above. This is your final soft warning before the harness disables tools.", followups)
	if len(paths) > 0 {
		line += " Inspected paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}
	return line
}

func closureProbeSuggestionLine(results []workspace.ToolResult, max int) string {
	candidates := closureProbeCandidates(results, max)
	if len(candidates) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		quoted = append(quoted, "`"+candidate+"`")
	}
	return "Evidence-grounded exact probes to prefer: " + strings.Join(quoted, ", ") + "."
}

func closureProbeCandidates(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 3
	}
	candidates := make([]string, 0, max)
	add := func(raw string) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw); ok {
			candidates = appendUniqueSearchTerm(candidates, symbol)
		}
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathLineSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[2])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
			for _, line := range strings.Split(text, "\n") {
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) > 1 {
						add(match[1])
						if len(candidates) >= max {
							return candidates[:max]
						}
					}
				}
			}
			for _, match := range goFuncDeclPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
		}
	}
	return candidates
}

func closureProbeCandidatesByPath(results []workspace.ToolResult, maxPerPath int) map[string][]string {
	if maxPerPath <= 0 {
		maxPerPath = 3
	}
	byPath := make(map[string][]string)
	add := func(path string, raw string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			return
		}
		symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
		if !ok {
			return
		}
		current := byPath[path]
		if containsExactProbeCandidate(current, symbol) {
			return
		}
		if len(current) >= maxPerPath {
			return
		}
		byPath[path] = append(current, symbol)
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathScopedSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}
			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}
			for _, line := range strings.Split(text, "\n") {
				paths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
				if len(paths) == 0 {
					continue
				}
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) <= 1 {
						continue
					}
					for _, path := range paths {
						add(path, match[1])
					}
				}
			}
		}
	}
	return byPath
}

func containsExactProbeCandidate(candidates []string, pattern string) bool {
	pattern, ok := delegatedReadOnlyLiteralSymbolCandidate(pattern)
	if !ok {
		pattern = strings.TrimSpace(pattern)
	}
	pattern = strings.ToLower(pattern)
	for _, candidate := range candidates {
		if strings.ToLower(strings.TrimSpace(candidate)) == pattern {
			return true
		}
	}
	return false
}

func quotedRunnerStrings(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

func isClosureOverviewTool(tool string) bool {
	return strings.HasSuffix(strings.TrimSpace(tool), "_file_outline")
}

func isClosureExactProbeTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
}

func postChildDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}
	// The child-packet boundary nudge already disclosed the continuation
	// contract; this is a factual probe-budget note, not another prose
	// block telling the model what was already said.
	paths := postChildClosureEvidencePaths(objective, results, 3)
	line := fmt.Sprintf("Post-child probes used: %d. Answer now from the packet above. This is your final soft warning before the harness disables tools.", followups)
	if len(paths) > 0 {
		line += " Packet paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}
	return line
}

func delegatedClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max*2)
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)
	return closurePromptEvidencePaths(paths, max)
}

func closureDecisionRationale(objective string, results []workspace.ToolResult, fallbackPaths []string) (string, string) {
	paths := delegatedClosureEvidencePaths(objective, results, 4)
	if len(paths) == 0 {
		paths = closurePromptEvidencePaths(fallbackPaths, 4)
	}
	switch {
	case len(paths) > 0 && closureHasExactProbeEvidence(results):
		return "the current evidence already names the core implementation paths " + summarizePostChildEvidencePaths(paths, 3) + " and includes at least one exact inspected probe", "another follow-up is only likely to help if it changes one named claim; otherwise it will mostly restate adjacent detail"
	case len(paths) > 0:
		return "the current evidence already names the core implementation paths " + summarizePostChildEvidencePaths(paths, 3), "another follow-up is only likely to help if it closes one named gap; otherwise additional reads mostly restate the same core files"
	default:
		return "the current evidence already covers the named scope well enough to support a grounded answer", "another follow-up is only likely to help if it closes one named gap"
	}
}

func closureHasExactProbeEvidence(results []workspace.ToolResult) bool {
	return len(closureProbeCandidates(results, 1)) > 0
}

func applyClosurePromptRefinement(state runnerNextActionState) runnerNextActionState {
	scope := ""
	switch {
	case containsRunnerString(state.Constraints, "child_packet_available"):
		scope = "child_packet_available"
	case containsRunnerString(state.Constraints, "answer_readiness_reached"):
		scope = "answer_readiness_reached"
	default:
		return state
	}
	state.NextValidActions = prependRunnerStateStrings(closurePromptNextActions(scope), filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, "search_files"),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "exact text search"),
			strings.Contains(lower, "narrow to one exact term or scope"),
			strings.Contains(lower, "continue from the latest evidence with a targeted tool"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "use a tool to gather or edit the missing evidence"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "gather or edit the missing evidence"),
			strings.Contains(lower, "edit_file"),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, "go_move_declarations"),
			strings.Contains(lower, "read_file"),
			strings.Contains(lower, "search_content"),
			strings.Contains(lower, "list_files"),
			strings.Contains(lower, "inspect"),
			strings.Contains(lower, "outline"),
			strings.Contains(lower, "workspace_symbol"):
			return true
		default:
			return false
		}
	}))
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, closurePromptInvalidActions(scope))
	return state
}

func closurePromptNextActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"answer directly from the child packet if it already resolves the question",
		}
	default:
		return []string{
			"answer directly from the evidence already gathered",
		}
	}
}

func closurePromptInvalidActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"broad rediscovery outside packet-named paths or symbols after the child packet",
			"README/docs or repo-wide search outside packet-named paths or symbols after the child packet",
		}
	default:
		return []string{
			"broad rediscovery after answer readiness without one exact missing claim and one exact probe",
			"README/docs or repo-wide search outside named implementation paths after answer readiness",
		}
	}
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
	if result, rejected := r.readOnlyExplanationCompletionRejection(reply, results); rejected {
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

func (r *Runner) readOnlyExplanationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}
	if reason := readOnlyExplanationMetaRejectionReason(reply); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected meta final answer",
			Error:   reason,
		}, true
	}
	if unsupported := architectureUnsupportedExactDetail(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Architecture completion gate rejected unsupported detail",
			Error:   "the answer names additional implementation detail " + quotedRunnerStrings(unsupported) + " without an exact inspected probe or explicit uncertainty. Either anchor that detail with one exact read, or qualify it as unverified.",
		}, true
	}
	return workspace.ToolResult{}, false
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

func readOnlyExplanationGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	switch strings.TrimSpace(brief.Contract) {
	case "architecture_assessment", "review_findings":
		return true
	}
	if strings.TrimSpace(brief.PermissionProfile) != "read_only_workspace" {
		return false
	}
	switch strings.TrimSpace(brief.TaskFamily) {
	case "architecture", "review":
		return true
	default:
		return false
	}
}

func readOnlyExplanationMetaRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"i have sufficient evidence",
		"the strongest implementation evidence is in",
		"the inspected implementation evidence is enough",
		"the retained source context identifies",
		"the gathered evidence points to these implementation surfaces",
		"enough for a bounded answer",
		"implementation surface",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer describes evidence sufficiency or implementation surface instead of explaining the concrete behavior from the inspected code. State the mechanism or findings directly."
		}
	}
	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 && !readOnlyExplanationHasMechanismVerb(lower) {
		return "the answer mostly inventories paths or scope and does not explain what the inspected code does. State the concrete behavior those files establish."
	}
	return ""
}

func readOnlyExplanationHasMechanismVerb(lower string) bool {
	for _, phrase := range []string{
		" uses ", " use ", " used ", " delegates ", " delegate ", " delegated ",
		" creates ", " create ", " created ", " calls ", " call ", " called ",
		" invokes ", " invoke ", " invoked ", " routes ", " route ", " routed ",
		" runs ", " run ", " ran ", " returns ", " return ", " returned ",
		" passes ", " pass ", " passed ", " enforces ", " enforce ", " enforced ",
		" spawns ", " spawn ", " spawned ", " builds ", " build ", " built ",
		" wires ", " wire ", " wired ", " folds ", " fold ", " folded ",
		" hands ", " hand ", " handed ", " launches ", " launch ", " launched ",
	} {
		if strings.Contains(" "+lower+" ", phrase) {
			return true
		}
	}
	return false
}

func architectureUnsupportedExactDetail(reply string, results []workspace.ToolResult) []string {
	exactSymbols := exactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 || answerHasExplicitUncertainty(reply) {
		return nil
	}
	mentioned := explicitAnswerCodeSymbols(reply)
	if len(mentioned) == 0 {
		return nil
	}
	var unsupported []string
	for _, symbol := range mentioned {
		key := normalizedAnswerSymbol(symbol)
		if key == "" {
			continue
		}
		if _, ok := exactSymbols[key]; ok {
			continue
		}
		unsupported = appendUniqueSearchTerm(unsupported, symbol)
	}
	return unsupported
}

func exactEvidenceSymbolSet(results []workspace.ToolResult) map[string]struct{} {
	symbols := make(map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		default:
			continue
		}
		_, symbol := extractExactProbeAnchor(result)
		key := normalizedAnswerSymbol(symbol)
		if key != "" {
			symbols[key] = struct{}{}
		}
		for _, call := range exactProbeCallNames(symbol, result.Output, 8) {
			key := normalizedAnswerSymbol(call)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
	return symbols
}

func explicitAnswerCodeSymbols(reply string) []string {
	symbols := make([]string, 0, 6)
	add := func(raw string) {
		key := normalizedAnswerSymbol(raw)
		if key == "" {
			return
		}
		symbols = appendUniqueSearchTerm(symbols, raw)
	}
	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	return symbols
}

func normalizedAnswerSymbol(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`'\"()[]{}<>,.:;!?")
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		raw = strings.TrimSpace(raw[dot+1:])
	}
	candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
	if !ok {
		return ""
	}
	return strings.ToLower(candidate)
}

func answerHasExplicitUncertainty(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"not directly inspected",
		"not directly read",
		"not confirmed",
		"not verified",
		"did not inspect",
		"didn't inspect",
		"uncertain",
		"may ",
		"might ",
		"appears ",
		"suggests ",
		"likely ",
		"seems ",
	)
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
	if runnerBriefThoroughness(brief) == "standard" {
		return 1
	}
	return 2
}

func runnerBriefThoroughness(brief workerruntime.WorkerRuntimeBrief) string {
	for _, constraint := range brief.Constraints {
		key, value, ok := strings.Cut(strings.TrimSpace(constraint), "=")
		if !ok || strings.TrimSpace(key) != "thoroughness" {
			continue
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
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
	// Args is `json:"-"` on ToolRequest (it intentionally stays out of
	// log/event serialization), so it would NOT contribute to this key
	// otherwise. For MCP tools and external tools the typed fields are
	// empty and Args carries the entire request shape — without this
	// suffix two materially-different calls (e.g. `lookup` with
	// {"enum":"JobType"} vs {"enum":"ColonistState"}) would collapse to
	// the same key and trip the "replayed prior evidence" guard.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
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
	req.NamedGap = ""
	req.WhyNotAnswering = ""
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

func childPacketContainsGroundedAnswerEvidence(objective string, output string) bool {
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
	if len(groundedChildPacketEvidencePaths(objective, output)) == 0 {
		return false
	}
	return containsAny(lower,
		"grounded answer",
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

func groundedChildPacketEvidencePaths(objective string, output string) []string {
	rawPaths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(output))
	if len(rawPaths) == 0 {
		return nil
	}
	paths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath(objective, path) {
			continue
		}
		paths = append(paths, path)
	}
	return uniqueDelegatedDiscoveryPaths(paths)
}

func isGroundedChildPacketEvidencePath(objective string, path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(normalizeDelegatedDiscoveryEvidencePath(path))))
	if path == "" {
		return false
	}
	switch {
	case strings.HasPrefix(path, ".idiom/"),
		strings.HasPrefix(path, "idiom/"),
		strings.HasPrefix(path, "logs/"),
		strings.HasPrefix(path, "transcripts/"),
		strings.HasPrefix(path, "testdata/swe/fixtures/"),
		strings.Contains(path, "/testdata/swe/fixtures/"):
		return false
	}
	base := filepath.Base(path)
	if allowsDocumentationOnlyPacket(objective) && (strings.HasSuffix(path, ".md") || base == "readme.md") {
		return true
	}
	switch base {
	case "go.mod", "go.sum", "go.work", "makefile", "package.json", "cargo.toml", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	}
	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".rs"),
		strings.HasSuffix(path, ".py"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".java"),
		strings.HasSuffix(path, ".c"),
		strings.HasSuffix(path, ".cc"),
		strings.HasSuffix(path, ".cpp"),
		strings.HasSuffix(path, ".cxx"),
		strings.HasSuffix(path, ".h"),
		strings.HasSuffix(path, ".hpp"),
		strings.HasSuffix(path, ".m"),
		strings.HasSuffix(path, ".mm"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".toml"),
		strings.HasSuffix(path, ".sh"),
		strings.HasSuffix(path, ".sql"),
		strings.HasSuffix(path, ".proto"):
		return true
	}
	return strings.HasPrefix(path, "internal/") ||
		strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "pkg/") ||
		strings.HasPrefix(path, "src/") ||
		strings.HasPrefix(path, "scripts/")
}

func allowsDocumentationOnlyPacket(objective string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	return containsAny(lower, " readme ", " doc ", " docs ", " documentation ", " markdown ")
}

func annotateScopedFallbackAnswer(answer string, objective string) string {
	answer = strings.TrimSpace(answer)
	objective = strings.TrimSpace(objective)
	if objective == "" || answer == "" || !strings.HasPrefix(answer, "Partial findings from this turn") {
		return answer
	}
	return "Question in scope: " + objective + "\nSource: delegated read-only evidence packet.\n\n" + answer
}

func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(normalizeWorkspacePathScanText(answer))
	if answer == "" {
		return ""
	}
	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathLinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathQuotePattern.ReplaceAllString(answer, "$1")
	return answer
}

func augmentDelegatedReadOnlyPacket(answer string, objective string, results []workspace.ToolResult) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	paths := delegatedClosureEvidencePaths(objective, results, 8)
	mentioned := normalizePostChildEvidencePaths(pathparse.MentionedWorkspacePaths(answer))
	missing := make([]string, 0, len(paths))
	mentionedSet := make(map[string]struct{}, len(mentioned))
	for _, path := range mentioned {
		mentionedSet[path] = struct{}{}
	}
	for _, path := range paths {
		if _, ok := mentionedSet[path]; ok {
			continue
		}
		missing = append(missing, path)
	}
	candidates := closureProbeCandidates(results, 4)
	missingCandidates := make([]string, 0, len(candidates))
	lowerAnswer := strings.ToLower(answer)
	for _, candidate := range candidates {
		if strings.Contains(lowerAnswer, strings.ToLower(candidate)) {
			continue
		}
		missingCandidates = append(missingCandidates, candidate)
	}
	if len(missing) == 0 && len(missingCandidates) == 0 {
		return answer
	}
	var b strings.Builder
	b.WriteString(answer)
	if len(missing) > 0 {
		b.WriteString("\n\nAdditional inspected paths:\n")
		for _, path := range limitRunnerStrings(missing, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}
	if len(missingCandidates) > 0 {
		b.WriteString("\nExact probes already inspected:\n")
		for _, candidate := range limitRunnerStrings(missingCandidates, 3) {
			b.WriteString("- `")
			b.WriteString(candidate)
			b.WriteString("`\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func synthesizeDelegatedReadOnlyAnswer(objective string, results []workspace.ToolResult) (string, bool) {
	useful := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		result = normalizeFallbackResult(result)
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || !isResultUsefulForFallback(result) {
			continue
		}
		useful = append(useful, result)
	}
	if len(useful) == 0 {
		return "", false
	}

	paths := delegatedReadOnlyInspectedImplementationPaths(results)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, 8)...)
	paths = append(paths, pinnedPathsFromToolResults(results, 8)...)
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, 8)
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
	b.WriteString("Source: delegated read-only evidence packet.\n\n")
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

func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}
	return prioritizedDelegatedEvidencePaths(paths, len(paths))
}

func delegatedDiscoverySnippetEvidencePaths(objective string, paths []string) []string {
	return uniqueDelegatedDiscoveryPaths(paths)
}

func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	fullByBase := make(map[string]string)
	ambiguousBase := make(map[string]bool)
	dirCounts := make(map[string]int)
	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if path == "" {
			continue
		}
		normalized = append(normalized, path)
		if strings.Contains(path, "/") {
			base := filepath.Base(path)
			dirCounts[filepath.Dir(path)]++
			if prior, ok := fullByBase[base]; ok && prior != path {
				ambiguousBase[base] = true
				continue
			}
			fullByBase[base] = path
		}
	}
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(normalized))
	dominantDir := ""
	dominantCount := 0
	for dir, count := range dirCounts {
		if count > dominantCount || (count == dominantCount && (dominantDir == "" || dir < dominantDir)) {
			dominantDir = dir
			dominantCount = count
		}
	}
	for _, path := range normalized {
		if !strings.Contains(path, "/") {
			base := filepath.Base(path)
			if full, ok := fullByBase[base]; ok && !ambiguousBase[base] {
				path = full
			} else if dominantCount >= 2 {
				path = filepath.ToSlash(filepath.Join(dominantDir, base))
			}
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func prioritizedDelegatedEvidencePaths(paths []string, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)
	if len(paths) <= 1 {
		return paths
	}
	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])
		return pi < pj
	})
	return limitRunnerStrings(paths, max)
}

func prioritizedClosureEvidencePaths(paths []string, results []workspace.ToolResult, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)
	if len(paths) <= 1 {
		return paths
	}
	sharedCounts, candidateCounts := closureEvidenceProbeScores(results)
	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])
		if pi != pj {
			return pi < pj
		}
		ni := normalizeDelegatedDiscoveryEvidencePath(paths[i])
		nj := normalizeDelegatedDiscoveryEvidencePath(paths[j])
		if sharedCounts[ni] != sharedCounts[nj] {
			return sharedCounts[ni] > sharedCounts[nj]
		}
		if candidateCounts[ni] != candidateCounts[nj] {
			return candidateCounts[ni] > candidateCounts[nj]
		}
		return false
	})
	return limitRunnerStrings(paths, max)
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

func closurePromptEvidencePaths(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range preferNonTestEvidencePaths(uniqueDelegatedDiscoveryPaths(paths)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}
		cleaned = append(cleaned, path)
	}
	return limitRunnerStrings(cleaned, max)
}

func closureEvidenceProbeScores(results []workspace.ToolResult) (map[string]int, map[string]int) {
	byPath := closureProbeCandidatesByPath(results, 6)
	probeFrequency := make(map[string]int)
	for _, candidates := range byPath {
		seen := make(map[string]struct{})
		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			probeFrequency[key]++
		}
	}
	sharedCounts := make(map[string]int, len(byPath))
	candidateCounts := make(map[string]int, len(byPath))
	for rawPath, candidates := range byPath {
		path := normalizeDelegatedDiscoveryEvidencePath(rawPath)
		if path == "" {
			continue
		}
		seen := make(map[string]struct{})
		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidateCounts[path]++
			if probeFrequency[key] > 1 {
				sharedCounts[path] += probeFrequency[key]
			}
		}
	}
	return sharedCounts, candidateCounts
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
	addCandidate := func(path string, layerName string, line string) {
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
		score := delegatedDiscoveryLayerPriority(layerName)*100 + delegatedDiscoveryEvidenceLinePriority(line)*10 + delegatedDiscoveryPathPriority(path)
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
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				for _, path := range pathparse.MentionedWorkspacePaths(line) {
					addCandidate(path, evidence.Name, line)
				}
				for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
					addCandidate(match, evidence.Name, line)
				}
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
			return preferNonTestEvidencePaths(paths)
		}
	}
	return preferNonTestEvidencePaths(paths)
}

func delegatedDiscoveryEvidenceLinePriority(line string) int {
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	switch {
	case line == "":
		return 5
	case strings.Contains(lower, "package "):
		return 5
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 0
	case strings.Contains(lower, "func "):
		return 1
	case strings.Contains(lower, "type "):
		return 2
	default:
		return 3
	}
}

func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = normalizeWorkspacePathScanText(path)
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"()[]{}<>,.;")
	if path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := strings.Index(lower, ext); idx >= 0 {
			end := idx + len(ext)
			suffix := path[end:]
			switch {
			case suffix == "":
				return path[:end]
			case strings.HasPrefix(suffix, ":"):
				return path[:end]
			case strings.Trim(suffix, "`'\"") == "":
				return path[:end]
			}
		}
	}
	return path
}

func normalizeWorkspacePathScanText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(text)
}

func preferNonTestEvidencePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	hasNonTestImplementation := false
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			hasNonTestImplementation = true
			break
		}
	}
	if !hasNonTestImplementation {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		filtered = append(filtered, raw)
	}
	if len(filtered) == 0 {
		return paths
	}
	return filtered
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
	case strings.HasPrefix(path, "internal/"):
		return 2
	case strings.HasSuffix(path, "_test.go"):
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
	evidencePaths := acceptedAnswerEvidencePaths(answer, results, r.RuntimeBrief)
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

func acceptedAnswerEvidencePaths(answer string, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) []string {
	if readOnlyExplanationGateApplies(brief) {
		paths := mergeRunnerStateStrings(exactProbeEvidencePaths(results), pathparse.MentionedWorkspacePaths(answer))
		paths = mergeRunnerStateStrings(paths, delegatedReadOnlyInspectedImplementationPaths(results))
		paths = prioritizedClosureEvidencePaths(paths, results, 8)
		paths = closurePromptEvidencePaths(paths, 8)
		if len(paths) > 0 {
			return paths
		}
	}
	paths := mergeRunnerStateStrings(pinnedPathsFromToolResults(results, 8), pathparse.MentionedWorkspacePaths(answer))
	paths = delegatedDiscoveryAnswerEvidencePaths(brief.Objective, paths)
	return closurePromptEvidencePaths(paths, 8)
}

func exactProbeEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		default:
			continue
		}
		path, _ := extractExactProbeAnchor(result)
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return preferNonTestEvidencePaths(paths)
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
	return fmt.Sprintf("You have already used %d read/search steps in the main loop without a write. Answer now if the current evidence already supports a concrete answer. Only if one broad missing claim genuinely remains and a bounded read_only child would close it faster than more local probing, you may delegate one bounded read_only task with the task tool.", readCallsSinceWrite)
}

func prefersEarlyReadOnlyDelegation(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	switch strings.TrimSpace(brief.Contract) {
	case "architecture_assessment", "review_findings":
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
			"Verification task contract: run the configured verification command before inspection.",
			fmt.Sprintf("Configured verification command: %s", cmd),
			"After it runs, answer with the observed pass/fail result.",
		}, "\n")
	case family == "debug_logs":
		return strings.Join([]string{
			"[debug-log bootstrap]",
			"Start from the newest relevant log artifact; source-code orientation is lower value until log evidence is exhausted.",
			"First choose the newest real log file (for example under logs/, *.log, or *.jsonl), then read its recent tail.",
			"Stay on that selected log until you can cite the weird decision or declare the missing datum.",
			"After one bounded log read, answer from the logs if the cause is already clear; otherwise do one targeted follow-up inside the same log.",
		}, "\n")
	case isEvidenceFirstMutationTaskFamily(family) && len(r.RuntimeBrief.TargetRefs) > 0:
		return strings.Join([]string{
			"[evidence-first mutation bootstrap]",
			fmt.Sprintf("This turn will update named target file(s): %s.", strings.Join(r.RuntimeBrief.TargetRefs, ", ")),
			"First gather source evidence from the implementation files or configs that drive the change.",
			"Then read only the target section you plan to edit.",
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
		"this turn needs source evidence before editing the named target file. Inspect the implementation files or configs that drive the change, then read only the target section you plan to edit.",
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
		fmt.Sprintf("you already inspected %d bounded windows of the named target file in this turn. Make the edit now, or if one exact detail is still missing, use a narrow line range or a targeted search anchored to that missing section.", maxEvidenceFirstTargetReadWindows),
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
		"you already gathered source evidence and inspected the named target file in bounded windows. Make the edit now, or if one exact detail is still missing, stay scoped to the named target.",
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
			"you already selected and read the newest relevant log. Stay on that log until you can cite the weird decision or declare the missing datum.",
			true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !debugLogRequestTargetsSelection(req, selection) {
		return "", "", false
	}
	if _, ok := latestDebugLogSearchHit(results); ok {
		return "", "", false
	}
	return "Blind log reread rejected",
		"you already have the newest tail of the selected log. Answer from that tail if it is sufficient, or search within the selected log for one concrete marker.",
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
	if rails.Contract == "architecture_assessment" {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}
	if rails.Contract == "review_findings" {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"})
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
	switch strings.TrimSpace(rails.Contract) {
	case "architecture_assessment", "review_findings":
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
		"Answer the delegated question directly once the evidence is sufficient. After readiness, answer immediately — do not continue reading for completeness.",
		"Treat inherited instructions to explore directories, entrypoints, or patterns as background only unless the delegated question explicitly requires them.",
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
	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Tools are now disabled.", metrics)
}

func answerFirstNoToolsPrompt(reason string) string {
	reason = strings.TrimSpace(reason)
	contract := "Final answer contract: tools are unavailable now. Write the normal final answer from the evidence already gathered. If the evidence supports a concrete answer, answer directly and do not lead with budget, blocker, partial-findings, or recommended-next-step framing. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action."
	if reason == "" {
		return contract
	}
	if strings.Contains(reason, "Final answer contract:") {
		return reason
	}
	return strings.TrimSpace(reason + "\n\n" + contract)
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
		reason = "Tools are now disabled."
	}
	reason = answerFirstNoToolsPrompt(reason)
	preferModelSynthesis := shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults)
	if !preferModelSynthesis {
		if answer, ok := immediateBudgetFallbackAnswer(r.agentName(), r.RuntimeBrief, reason, collectedResults); ok {
			return answer, nil
		}
	}
	if preferModelSynthesis {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
		if paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, collectedResults, 4); len(paths) > 0 {
			reason = strings.TrimSpace(reason + "\nCarry forward the inspected implementation paths explicitly in the answer: " + summarizePostChildEvidencePaths(paths, 4) + ". Do not collapse the answer to a single file when the evidence already spans multiple relevant implementation paths.")
		}
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
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
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
	preferDeterministicFallback := shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults)

	if len(assistantMessage.ToolCalls) > 0 {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "native tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}
	if looksLikeTextualToolCall(reply) {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "textual tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "malformed tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", parseErr
	} else if ok {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
	if err != nil {
		return "", err
	}
	if finalAnswerNeedsDecoration(answer) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, textOnlyOpts, answer, metrics); err != nil {
			return "", err
		} else if ok {
			return repaired, nil
		}
		if presentable, ok := presentableNoToolsFallbackAnswer(r.RuntimeBrief, collectedResults, answer); ok {
			return presentable, nil
		}
	}
	return answer, nil
}

func (r *Runner) repairOrFallbackFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {
	rawFallback, ok := synthesizeNoToolsFallbackAnswer(r.agentName(), r.RuntimeBrief, collectedResults)
	if !ok {
		return "", false, nil
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(rawFallback); ok {
		return cleaned, true, nil
	}
	if finalAnswerNeedsDecoration(rawFallback) && r.userFacingFinalAnswer() {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, rawFallback, metrics); err != nil {
			return "", false, err
		} else if ok {
			return repaired, true, nil
		}
	}
	if presentable, ok := presentableNoToolsFallbackAnswer(r.RuntimeBrief, collectedResults, rawFallback); ok {
		return presentable, true, nil
	}
	rawFallback = strings.TrimSpace(rawFallback)
	return rawFallback, rawFallback != "", nil
}

func (r *Runner) repairInternalFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	internalAnswer string,
	metrics *runnerMetrics,
) (string, bool, error) {
	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false, nil
	}
	repairMessages := append([]llama.ChatMessage(nil), turnMessages...)
	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: "The previous final synthesis still did not produce a usable user-facing answer. Tools remain disabled. " +
			"The text below is an internal evidence summary, not a final-answer template. Rewrite it into the normal final answer to the user's request using only claims supported by that evidence and the prior tool results. " +
			"Do not mention evidence packets, probes, tool calls, budgets, fallback behavior, or internal sections. If one exact claim is unsupported, state that uncertainty at the end.\n\nInternal evidence summary:\n" + internalAnswer,
	})
	if metrics != nil {
		metrics.ModelCalls++
	}
	modelStart := time.Now()
	repairMessage, err := r.Client.CompleteChatMessage(ctx, repairMessages, opts)
	if err != nil {
		return "", false, err
	}
	if metrics != nil {
		metrics.addUsage(repairMessage.Usage)
	}
	r.emitModelCallTrace(0, "fallback_final_answer_retry", repairMessages, repairMessage, time.Since(modelStart).Milliseconds())
	r.emitThinkingTrace(0, "fallback_final_answer_retry_reply", repairMessage, opts, false)

	if len(repairMessage.ToolCalls) > 0 {
		return "", false, nil
	}
	content := strings.TrimSpace(repairMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}
	if finalAnswerNeedsDecoration(content) {
		return "", false, nil
	}
	r.LastAssistantMessage = repairMessage
	return content, true, nil
}

func (r *Runner) retryInvalidFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
	reason string,
) (string, bool, error) {
	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, llama.ChatMessage{
			Role:             firstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		})
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool request"
	}
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous final response was a " + reason + ", but tools are disabled for final synthesis. " +
			"Do not emit tool JSON, XML, tool blocks, or another probe. Reply in normal prose from the evidence already gathered. " +
			"If the evidence is sufficient, answer directly; if one exact claim remains unsupported, add that uncertainty after the supported answer in one sentence. " +
			"Do not list internal tool traces, exact probes, or inspected-path inventories unless they are necessary to explain the answer.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	modelStart := time.Now()
	retryMessage, err := r.Client.CompleteChatMessage(ctx, retryMessages, opts)
	if err != nil {
		return "", false, err
	}
	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}
	r.emitModelCallTrace(0, "invalid_final_answer_retry", retryMessages, retryMessage, time.Since(modelStart).Milliseconds())
	r.emitThinkingTrace(0, "invalid_final_answer_retry_reply", retryMessage, opts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return "", false, nil
	}
	content := strings.TrimSpace(retryMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}
	if finalAnswerNeedsDecoration(content) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, content, metrics); err != nil {
			return "", false, err
		} else if ok {
			return repaired, true, nil
		}
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}
		return "", false, nil
	}
	r.LastAssistantMessage = retryMessage
	return content, true, nil
}

func shouldPreferDeterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if strings.HasPrefix(agentName, "child:") {
		return true
	}
	return strings.TrimSpace(brief.PermissionProfile) == "read_only_workspace" && postChildPacketClosureReady(results)
}

func deterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	answer, ok := synthesizeNoToolsFallbackAnswer(agentName, brief, results)
	if !ok {
		return "", false
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
		return cleaned, true
	}
	if presentable, ok := presentableNoToolsFallbackAnswer(brief, results, answer); ok {
		return presentable, true
	}
	answer = strings.TrimSpace(answer)
	return answer, answer != ""
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if strings.HasPrefix(agentName, "child:") {
		return true
	}
	return strings.TrimSpace(brief.PermissionProfile) == "read_only_workspace" && postChildPacketClosureReadyWithBrief(brief, results)
}

func (r *Runner) advertisedAllowedTools(base []string) []string {
	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "conversation_only") {
		// Even in conversation_only mode, expose project-defined external
		// tools so the model can call host-registered discovery commands
		// (e.g. trace_code, lookup, api_access). Built-in mutating tools
		// stay hidden — that's what conversation_only is for.
		if r.Workspace == nil {
			return nil
		}
		extReg := r.Workspace.ExternalToolsRegistry()
		if extReg == nil {
			return nil
		}
		names := extReg.Names()
		// Preserve order by filtering base to only externals; this also
		// drops any non-external entry that snuck into base via prior
		// pipeline mutations.
		known := make(map[string]struct{}, len(names))
		for _, n := range names {
			known[n] = struct{}{}
		}
		var out []string
		for _, name := range base {
			if _, ok := known[strings.TrimSpace(name)]; ok {
				out = append(out, name)
			}
		}
		// If base was empty (e.g. brief AllowedTools = nil), append
		// directly from the registry so the externals still surface.
		if len(out) == 0 {
			out = append(out, names...)
		}
		return out
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

func textOnlyChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""
	limit := textOnlyMaxTokensForBrief(brief)
	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}
	return opts
}

func textOnlyMaxTokensForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case "direct_answer":
		return 1024
	case "grounded_explanation", "grounded_evidence_summary", "locate_targets":
		return 1536
	case "architecture_assessment", "review_findings":
		return 2048
	default:
		return 768
	}
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
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
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
			"If one exact claim remains unsupported, state that uncertainty after the supported answer in one sentence.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
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
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}
	if looksLikeTextualToolCall(retryReply) {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
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
	if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
		return annotateBudgetLimitedAnswer(answer), true
	}
	if output, ok := bestTaskResultOutput(results); ok && childPacketContainsGroundedAnswerEvidence(brief.Objective, output) {
		return annotateBudgetLimitedAnswer(output), true
	}
	if usefulFallbackResultCount(results) < 2 {
		return "", false
	}
	return synthesizeFallbackAnswer(agentName, results)
}

func synthesizeNoToolsFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}
	if !runnerBriefRequiresMutation(brief) && !runnerVerificationRequired(brief) {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
			return answer, true
		}
		if output, ok := bestTaskResultOutput(results); ok && childPacketContainsGroundedAnswerEvidence(brief.Objective, output) {
			if normalized := normalizeDelegatedReadOnlyPacket(output); strings.TrimSpace(normalized) != "" {
				return normalized, true
			}
		}
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
		result := normalizeFallbackResult(results[i])
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && firstSuccessful == nil {
			copy := result
			firstSuccessful = &copy
		}
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && isResultUsefulForFallback(result) {
			successfulNarrowing = append(successfulNarrowing, result)
		}
		if result.Error != "" && !isHarnessRejectionSummary(result.Summary) && !isHarnessRejectionErrorText(result.Error) {
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
			if output := compactOutput(sanitizeFallbackResultOutput(result), fallbackLineBudgetForTool(result.Tool)); output != "" {
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
	b.WriteString("Recommended next step:\n")
	if agentName == "explore" {
		b.WriteString("- answer from the verified evidence above; if resuming later, start from one exact missing claim and one exact probe rather than reopening broad discovery")
	} else {
		b.WriteString("- answer from the verified evidence above; if resuming later, name one exact missing claim and one exact probe before any additional reads")
	}

	return strings.TrimSpace(b.String()), true
}

func annotateBudgetLimitedAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	return answer
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

// rewriteVisibilityNote returns a short system-message body explaining that
// the requested tool was redirected to a different tool by harness routing
// rules. Generic across rewrite kinds (delegated bootstrap, verification
// bootstrap, debug-log preflight, etc.) — it states the fact without
// hard-picking the next tool, leaving the model free to act on the result.
func rewriteVisibilityNote(originalTool, rewrittenTool string) string {
	original := strings.TrimSpace(originalTool)
	rewritten := strings.TrimSpace(rewrittenTool)
	switch {
	case original != "" && rewritten != "" && original != rewritten:
		return fmt.Sprintf("Note: your `%s` call was redirected to `%s` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `%s`.", original, rewritten, original)
	case rewritten != "":
		return fmt.Sprintf("Note: your tool call was redirected to `%s` by lane routing. The rewritten call will execute and return its result.", rewritten)
	default:
		return "Note: your tool call was rewritten by lane routing. The rewritten call will execute and return its result."
	}
}

// normalizeFallbackResult prepares a tool result for fallback synthesis by
// removing nested fallback wrappers from a delegated child's Output. When a
// child lane force-completes, its answer is already a "Partial findings from
// this turn..." template; that wrapper bleeds into the parent's view of the
// task result and produces nested "Partial findings" headers in the final
// answer. We strip the outer template so the substance (Verified context
// bullets, observations) folds cleanly into the parent's own template.
func normalizeFallbackResult(result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Tool) != "task" {
		return result
	}
	stripped := stripNestedFallbackTemplate(result.Output)
	if stripped != result.Output {
		result.Output = stripped
	}
	return result
}

// stripNestedFallbackTemplate removes the outer scaffolding of a
// synthesizeFallbackAnswer output so it can be embedded inside another
// fallback answer without producing duplicated headers. Idempotent: if the
// input is not a fallback template, it is returned unchanged.
func stripNestedFallbackTemplate(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || !strings.HasPrefix(trimmed, "Partial findings from this turn") {
		return s
	}
	// Drop the leading "Partial findings from this turn (...):" line plus
	// the immediately-following blank line, leaving just the substance.
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		trimmed = strings.TrimLeft(trimmed[idx+1:], "\n")
	}
	// Drop the trailing "Recommended next step:" footer — that's
	// scaffolding for the user, not evidence.
	if idx := strings.Index(trimmed, "\nRecommended next step:"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	// Drop the inner "Verified context:" header. The outer fallback adds
	// its own header; nested verified-context labels make the answer read
	// like two stacked reports.
	trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "Verified context:"))
	// Drop a trailing "Final blocker:" if it's just a harness rejection —
	// that signal has already been recomputed by the outer template.
	if idx := strings.Index(trimmed, "\nFinal blocker:"); idx >= 0 {
		head := strings.TrimSpace(trimmed[:idx])
		tail := strings.TrimSpace(trimmed[idx+len("\nFinal blocker:"):])
		// Look one bullet ahead and drop the section if its content is a
		// known harness-only blocker; otherwise keep it.
		if firstBullet := firstBulletText(tail); isHarnessRejectionSummary(firstBullet) || isHarnessRejectionErrorText(tail) {
			trimmed = head
		}
	}
	return trimmed
}

// firstBulletText returns the text of the first bullet ("- ") in s, with the
// leading dash trimmed. Returns the empty string if no bullet is found.
func firstBulletText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if idx := strings.Index(line, ":"); idx >= 0 {
				return strings.TrimSpace(line[:idx])
			}
			return line
		}
	}
	return ""
}

// isHarnessRejectionSummary returns true for tool-result Summary strings that
// originate from harness contracts (post-readiness gap requirement, packet
// anchoring, etc.) rather than from real tool failures. These are not useful
// "Final blocker" lines — the user did nothing wrong and the message is
// internal to the harness's own enforcement loop.
func isHarnessRejectionSummary(summary string) bool {
	switch strings.TrimSpace(summary) {
	case "Post-readiness follow-up missing named gap",
		"Post-child follow-up missing named gap",
		"Post-readiness follow-up is not an exact probe",
		"Post-readiness follow-up is broader than the current evidence requires",
		"Post-readiness exact probe is not grounded in current evidence",
		"Post-child follow-up gap is contradicted by current evidence",
		"Post-child follow-up gap is not packet-anchored",
		"Post-child broad inspection blocked",
		"Post-child unrelated discovery blocked",
		"Post-child reread blocked",
		"Broad exploration after delegation nudge blocked",
		"Additional read_only child task blocked",
		"Broad inspection request rejected",
		"Chained list_files rejected",
		"Chained search rejected":
		return true
	}
	return false
}

// isHarnessRejectionErrorText returns true when the error text reads like a
// harness contract message rather than a real tool error. Used as a fallback
// when the Summary itself isn't enough to classify the rejection.
func isHarnessRejectionErrorText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"include `named_gap`",
		"include named_gap",
		"already contains sufficient evidence to bias toward answering",
		"answer readiness has already been reached",
		"file-level raw read is too broad",
		"file-level outline is broad orientation",
		"do not restart broad discovery",
	)
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

// buildRejectionRecoveryNudge gives the model one last closure-biased repair
// after repeated rejected tool calls. It should state the contract and avoid
// prescribing a copy-paste tool sequence unless a hard invariant requires it.
func buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult, mgr *workspace.Manager, brief workerruntime.WorkerRuntimeBrief, agent string, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) string {
	tool := strings.TrimSpace(req.Tool)
	var b strings.Builder
	b.WriteString("Recovery nudge — last chance before the harness ends the turn. Answer with the evidence already gathered. Only take one narrow next step if you can name the exact missing claim it closes. Do not repeat the rejected tool call.\n\n")

	if strings.EqualFold(strings.TrimSpace(brief.PermissionProfile), "read_only_verification_shell") {
		cmd := "go test ./..."
		if len(brief.VerificationPlan) > 0 && strings.TrimSpace(brief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(brief.VerificationPlan[0])
		}
		b.WriteString("This is a verification-only turn; the requested check has not yet run. Run it (the configured plan starts with `")
		b.WriteString(cmd)
		b.WriteString("`), then answer with the observed pass/fail. If the check has already run, answer with that result.\n")
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
			return b.String()
		}
	}
	if scope := postReadinessGapScope(
		agent,
		delegatedReadOnlyAnswerReady || delegatedReadOnlyClosureReady(agent, brief, results),
		postChildPacketActive || postChildPacketClosureReadyWithBrief(brief, results),
		req,
	); scope != "" {
		if nudge := buildClosureRecoveryNudge(scope, req, results, brief); nudge != "" {
			return nudge
		}
	}
	if isPrematureEvidenceFirstTargetRead(req, results, brief) {
		b.WriteString("This turn still needs source evidence before the named target file.\n")
		candidates := nonTargetRecoveryFileCandidates(results, brief.TargetRefs)
		if len(candidates) > 0 {
			b.WriteString("Candidate source paths:\n")
			for _, path := range candidates {
				b.WriteString("  - ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		} else {
			b.WriteString("Use a non-target implementation file or search result that can explain the change before returning to the target.\n")
		}
		b.WriteString("\nIf the source evidence is already sufficient in prior results, answer the user directly instead of calling another tool.\n")
		return b.String()
	}
	if evidenceFirstTargetWritePhaseActive(brief, results) {
		target := firstNonEmptyNonEmpty(brief.TargetRefs...)
		if target == "" {
			target = strings.TrimSpace(req.Path)
		}
		b.WriteString("This turn is now in target-write phase.\n")
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
			b.WriteString("Relevant target window: ")
			b.WriteString(target)
			b.WriteString(fmt.Sprintf(" lines %d-%d\n", start, end))
		}
		b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")
		return b.String()
	}

	switch {
	case tool == "list_files" || tool == "list_dir":
		candidates := rejectionRecoveryFileCandidates(results)
		if len(candidates) > 0 {
			b.WriteString("You already have a workspace overview. Choose one discovered path that can close a named gap:\n")
			for _, path := range candidates {
				b.WriteString("  - ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		} else {
			b.WriteString("You already have a workspace overview. Either answer now or take one focused search/read step tied to a named gap.\n")
		}
	case tool == "read_file":
		path := strings.TrimSpace(req.Path)
		if path != "" {
			b.WriteString("The repeated read should become a smaller window or a structured probe tied to a named gap on ")
			b.WriteString(path)
			b.WriteString(".\n")
		} else {
			b.WriteString("The repeated read needs a concrete discovered path and a named gap.\n")
		}
	case tool == "search_content":
		b.WriteString("A follow-up search should change the claim being tested or narrow to a specific discovered path.\n")
		if strings.TrimSpace(req.Path) != "" {
			b.WriteString("Current scope: ")
			b.WriteString(req.Path)
			b.WriteString("\n")
		}
	default:
		b.WriteString("Pick a materially different move that names the exact missing claim it closes.\n")
	}
	b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")
	return b.String()
}

func buildClosureRecoveryNudge(scope string, req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	var paths []string
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		paths = postChildClosureEvidencePaths(brief.Objective, results, 4)
	default:
		paths = delegatedClosureEvidencePaths(brief.Objective, results, 4)
	}
	var b strings.Builder
	b.WriteString("Recovery nudge — closure is already justified here. Answer now from the evidence already gathered.\n\n")
	if validity, diminishing := closureDecisionRationale(brief.Objective, results, paths); validity != "" {
		b.WriteString("Why answer now is valid: ")
		b.WriteString(validity)
		b.WriteString("\n")
		if diminishing != "" {
			b.WriteString("Lower-value next steps: ")
			b.WriteString(diminishing)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(paths) > 0 {
		b.WriteString("Grounded evidence paths:\n")
		for _, evidencePath := range paths {
			b.WriteString("  - ")
			b.WriteString(evidencePath)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("If one detail remains uncertain, state that uncertainty in prose; do not request more tools or return a partial-findings template.")
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
	output = stripFallbackJSONArtifacts(output)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

// sanitizeFallbackResultOutput returns a tool-result Output that's safe to
// embed under "Verified context" in a forced-completion fallback. Some
// tools produce Output that is fine for the model's tool-call loop but
// reads as noise in the final user-facing answer:
//
//   - search_content matches against test fixtures often span multi-line
//     quoted Go strings containing JSON, so the per-line strip can't
//     reach the JSON cleanly. The count summary ("Found N matching
//     lines.") is sufficient context for synthesis; drop the raw output.
//   - read_file and symbol-read outputs are kept — those are the actual
//     evidence the model needs to summarize.
//
// Generic across languages: applies to *any* tool whose output is a
// long quoted-string match list.
func sanitizeFallbackResultOutput(result workspace.ToolResult) string {
	switch strings.TrimSpace(result.Tool) {
	case "search_content", "search_files":
		return ""
	default:
		return result.Output
	}
}

// stripFallbackJSONArtifacts removes JSON-shaped object dumps from a tool
// result Output before it gets surfaced under "Verified context" in the
// forced-completion fallback. When a child lane's task result Output
// includes a discovery_query summary, the artifact metadata JSON often
// follows verbatim ({"id": "...", "purpose": "...", "layers_run": [...]
// }) and bleeds into the user-facing answer as opaque blocks. The
// fallback should show prose bullets; metadata blobs belong in the
// artifact file, not the answer. Generic across artifact JSON — any
// brace-delimited object whose first content line starts with a quoted
// key is treated as a metadata dump and dropped.
func stripFallbackJSONArtifacts(output string) string {
	if !strings.Contains(output, "{") {
		return output
	}
	lines := strings.Split(output, "\n")
	out := make([]string, 0, len(lines))
	skipUntilBaseline := false
	skipDepth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipUntilBaseline {
			// Track nested braces. We exit the skip when depth returns
			// to zero, when we see a blank line, or when a new bullet
			// starts (lines beginning with "- " mark the next section).
			skipDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				skipUntilBaseline = false
				skipDepth = 0
			}
			if trimmed == "" || strings.HasPrefix(trimmed, "- ") {
				skipUntilBaseline = false
				skipDepth = 0
				if strings.HasPrefix(trimmed, "- ") {
					out = append(out, line)
				}
			}
			continue
		}
		// A bare "{" line, or a "{...}" JSON one-liner whose first
		// inner content is a quoted key, marks the start of a metadata
		// dump. Drop the block.
		if trimmed == "{" || (strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\":")) {
			skipUntilBaseline = true
			skipDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				// One-liner JSON; consume just this line.
				skipUntilBaseline = false
				skipDepth = 0
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
