package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/frontier/pathparse"
	"qwen-bubbles/internal/frontier/workerruntime"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

type Runner struct {
	Client                *llama.Client
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
	DisableTaskTool       bool
	LastAssistantMessage  llama.ChatMessage
}

type RunPolicy struct {
	MaxToolSteps       int
	MaxNoProgressTurns int
	MaxContextMessages int
	MaxContextChars    int
	MaxModelCalls      int
	MaxDuration        time.Duration
}

type TaskLedgerPolicy struct {
	Disabled bool
	Required bool
}

type CompletionGate func(CompletionGateInput) CompletionGateResult

type CompletionGateInput struct {
	AgentName string
	Reply     string
	Results   []workspace.ToolResult
	Steps     int
}

type CompletionGateResult struct {
	Accept  bool
	Summary string
	Error   string
}

type RunEvent struct {
	Role    string
	Content string
}

type ToolRequestEvent struct {
	Step      int
	Agent     string
	Raw       string
	Request   workspace.ToolRequest
	Mode      workspace.Mode
	Broad     bool
	Narrowing bool
	Action    string
}

type ToolResultEvent struct {
	Step   int
	Agent  string
	Result workspace.ToolResult
}

type runnerFailureFeedbackPacket struct {
	Lane               string   `json:"lane,omitempty"`
	Step               int      `json:"step,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	FailedTool         string   `json:"failed_tool,omitempty"`
	FailedTarget       string   `json:"failed_target,omitempty"`
	FailureClass       string   `json:"failure_class,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	ResultError        string   `json:"result_error,omitempty"`
	DoNotRepeat        []string `json:"do_not_repeat,omitempty"`
	RequiredNext       []string `json:"required_next,omitempty"`
	InvalidNext        []string `json:"invalid_next,omitempty"`
	ValidNext          []string `json:"valid_next,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker,omitempty"`
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
	var lastToolRequest workspace.ToolRequest
	var lastToolResult *workspace.ToolResult
	replayedToolRequests := make(map[string]int)
	needsNarrowingAfterBroad := r.InitialNeedsNarrowing
	broadInspectionCount := r.InitialBroadInspects
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	declarationMovesForbidden := declarationMovesForbiddenByMessages(turnMessages)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	consecutiveNoProgress := 0
	consecutiveMissingPathListFailures := 0
	// Soft-failure tracking: when a tool RAN but returned an error (e.g.
	// read_file on a path that doesn't exist), the hard-rejection counter
	// above does NOT increment. Log forensics showed the model happily
	// retrying five hallucinated filenames in a fabricated directory before
	// the budget ran out. Track the same-path-stem failure streak here and
	// fire the same recovery nudge once we see 3 in a row.
	consecutiveToolFailures := 0
	lastFailedPathStem := ""
	softFailureNudgeUsed := false
	taskLedger := newTaskLedgerState(r.TaskLedger)
	checkpointCreated := false
	r.RuntimeBrief = r.runtimeBriefWithLiveState(policy, turnMessages, collectedResults)
	if content := workerruntime.RenderInitialRuntimeBrief(r.RuntimeBrief); content != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: content})
		r.emitEvent(RunEvent{Role: "runtime_brief", Content: content})
	}

	emitNextActionState := func(stepNumber int, reason string, req workspace.ToolRequest, result workspace.ToolResult) {
		r.emitNextActionState(stepNumber, reason, req, result, collectedResults, declarationMovesForbidden)
	}
	appendFailureFeedback := func(stepNumber int, reason string, req workspace.ToolRequest, result workspace.ToolResult) {
		packet := buildRunnerFailureFeedbackPacket(r.agentName(), stepNumber, reason, req, result, collectedResults, declarationMovesForbidden)
		if len(packet.DoNotRepeat) == 0 && len(packet.RequiredNext) == 0 && strings.TrimSpace(packet.ResultError) == "" {
			return
		}
		content := renderRunnerFailureFeedbackPacket(packet)
		if content == "" {
			return
		}
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: content})
		if data, err := json.Marshal(packet); err == nil {
			r.emitEvent(RunEvent{Role: "failure_feedback", Content: string(data)})
		}
	}

	rejectTool := func(stepNumber int, req workspace.ToolRequest, summary string, errorText string) (string, error, bool) {
		metrics.RejectedActions++
		result := workspace.ToolResult{
			Tool:    req.Tool,
			Summary: summary,
			Error:   errorText,
		}
		collectedResults = append(collectedResults, result)
		lastToolKey = toolRequestKey(req)
		lastToolRequest = req
		resultCopy := result
		lastToolResult = &resultCopy
		if action := toolActionClass(req); action != "" {
			rejectedActions[action]++
			consecutiveRejectedActions++
		}
		if onToolResult != nil {
			onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
		}
		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		emitNextActionState(stepNumber, "tool_rejected", req, result)
		appendFailureFeedback(stepNumber, "tool_rejected", req, result)
		if consecutiveRejectedActions >= 2 {
			// OpenCode-style retry-with-nudge: before bailing out with
			// `repeated_dead_end_budget_exhausted`, give the model one more
			// chance with a concrete, copy-paste-ready next action. Small
			// local models often ignore the generic "don't repeat" feedback
			// but recover if handed a specific tool call to try instead.
			// One-shot: the flag ensures we only nudge once per turn so we
			// can't loop forever on a stuck model.
			if !rejectionRecoveryNudgeUsed {
				if nudge := buildRejectionRecoveryNudge(req, collectedResults, r.Workspace); nudge != "" {
					rejectionRecoveryNudgeUsed = true
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})
					consecutiveRejectedActions = 0
					return "", nil, false
				}
			}
			answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults, &metrics)
			return answer, err, true
		}
		return "", nil, false
	}

	rejectCompletion := func(stepNumber int, reply llama.ChatMessage, result workspace.ToolResult) {
		completionGateRejections++
		metrics.CompletionGateRejections++
		consecutiveNoProgress++
		collectedResults = append(collectedResults, result)
		lastToolRequest = workspace.ToolRequest{Tool: "completion_gate"}
		resultCopy := result
		lastToolResult = &resultCopy
		if onToolResult != nil {
			onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
		}
		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:             firstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		})
		r.emitThinkingTrace(stepNumber, "completion_gate_reply_preserved", reply, opts, true)
		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		emitNextActionState(stepNumber, "completion_gate_rejected", workspace.ToolRequest{Tool: "completion_gate"}, result)
		appendFailureFeedback(stepNumber, "completion_gate_rejected", workspace.ToolRequest{Tool: "completion_gate"}, result)
	}

	replayRepeatedToolRequest := func(req workspace.ToolRequest, source workspace.ToolResult) workspace.ToolResult {
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

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		if policy.shouldStopForDuration(metrics.startedAt) {
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Run duration budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.", &metrics)
			return finish(answer, err, "duration_budget_exhausted", true)
		}
		if policy.shouldStopForModelCalls(metrics.ModelCalls) {
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Model-call budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.", &metrics)
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
		if step > 0 && lastToolResult != nil {
			if content := r.renderStepInstruction(policy, turnMessages, stepNumber, lastToolRequest, *lastToolResult, collectedResults, declarationMovesForbidden); content != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: content})
				r.emitEvent(RunEvent{Role: "step_instruction", Content: content})
			}
		}

		metrics.ModelCalls++
		assistantMessage, err := r.Client.CompleteChatMessage(ctx, turnMessages, opts)
		if err != nil {
			return finish("", err, "model_error", false)
		}
		metrics.addUsage(assistantMessage.Usage)
		reply := assistantMessage.Content
		r.emitThinkingTrace(stepNumber, "model_reply", assistantMessage, opts, false)

		req, raw, ok, err := workspace.ParseToolRequest(reply)
		if err != nil {
			result := workspace.ToolResult{
				Tool:    "tool_parse",
				Summary: "Malformed tool request rejected",
				Error:   err.Error() + ". Emit exactly one valid JSON tool object or answer normally.",
				Output:  strings.TrimSpace(raw),
			}
			collectedResults = append(collectedResults, result)
			lastToolRequest = workspace.ToolRequest{Tool: "tool_parse"}
			resultCopy := result
			lastToolResult = &resultCopy
			metrics.RejectedActions++
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			emitNextActionState(stepNumber, "tool_parse_rejected", workspace.ToolRequest{Tool: "tool_parse"}, result)
			appendFailureFeedback(stepNumber, "tool_parse_rejected", workspace.ToolRequest{Tool: "tool_parse"}, result)
			continue
		}
		if !ok {
			if result, rejected := r.completionGateRejection(reply, collectedResults, stepNumber, taskLedger, declarationMovesForbidden); rejected {
				if completionGateRejections >= 3 {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Completion gate has rejected the final answer repeatedly. Do not request any more tools. Summarize the concrete blocker and the next action required.", &metrics)
					return finish(answer, err, "completion_gate_repeated_rejection", true)
				}
				rejectCompletion(stepNumber, assistantMessage, result)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.", &metrics)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, opts, collectedResults, &metrics)
			return finish(answer, err, "model_final", false)
		}

		turnMessages = append(turnMessages, assistantMessage)
		r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantMessage, opts, true)
		metrics.ToolCalls++
		actionClass := toolActionClass(req)
		if onToolRequest != nil {
			onToolRequest(ToolRequestEvent{
				Step:      stepNumber,
				Agent:     r.agentName(),
				Raw:       raw,
				Request:   req,
				Mode:      r.Workspace.Mode(),
				Broad:     workspace.IsBroadInspectionRequest(req),
				Narrowing: workspace.IsNarrowingRequest(req),
				Action:    actionClass,
			})
		}

		toolKey := toolRequestKey(req)
		if summary, errorText, reject := declarationMoveConstraintRejection(req, declarationMovesForbidden); reject {
			answer, err, done := rejectTool(stepNumber, req, summary, errorText)
			if done {
				return finish(answer, err, "go_refactor_guard_exhausted", true)
			}
			continue
		}
		if actionClass != "" && rejectedActions[actionClass] > 0 {
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Repeated dead-end action rejected",
				"this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
			)
			if done {
				return finish(answer, err, "repeated_dead_end_budget_exhausted", true)
			}
			continue
		}
		if r.Workspace.IsExploreMode() && workspace.IsBroadInspectionRequest(req) && broadInspectionCount >= 1 {
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Additional broad inspection rejected",
				"explore mode already has a workspace overview for this turn. Use the existing overview plus search_files, search_content, or read_file on discovered handles instead of requesting another broad listing.",
			)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if needsNarrowingAfterBroad && workspace.IsBroadInspectionRequest(req) {
			errorText := "you already have a broad workspace overview. Next use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
			if r.Workspace.IsExploreMode() {
				errorText = "you already have a broad workspace overview. In explore mode you must narrow next with search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
			}
			answer, err, done := rejectTool(stepNumber, req, "Broad inspection request rejected", errorText)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if summary, errorText, reject := postWriteFocusRejection(req, collectedResults); reject {
			answer, err, done := rejectTool(stepNumber, req, summary, errorText)
			if done {
				return finish(answer, err, "post_write_focus_budget_exhausted", true)
			}
			continue
		}
		if !declarationMovesForbidden && shouldRejectBroadInspectionAfterGoOutline(req, collectedResults) {
			if fallback, guarded, ok := r.recommendedGoMoveAfterOutline(req, collectedResults, stepNumber); ok {
				if guarded != nil {
					if strings.TrimSpace(req.Tool) == "read_file" {
						collectedResults = append(collectedResults, *guarded)
						lastToolRequest = fallback
						resultCopy := *guarded
						lastToolResult = &resultCopy
						turnMessages = append(turnMessages, llama.ChatMessage{
							Role:    "system",
							Content: workspace.FormatToolResultForModel(*guarded),
						})
						emitNextActionState(stepNumber, "deterministic_tool_fallback_skipped", fallback, *guarded)
						r.emitEvent(marshalRunEvent("deterministic_tool_fallback_skipped", map[string]string{
							"from":          "broad_after_go_outline_guard",
							"tool":          fallback.Tool,
							"path":          fallback.Path,
							"target":        fallback.Destination,
							"guard_summary": guarded.Summary,
							"guard_error":   guarded.Error,
							"original_tool": req.Tool,
							"original_path": req.Path,
						}))
					} else {
						answer, err, done := rejectTool(
							stepNumber,
							fallback,
							"Recommended declaration move rejected by guard",
							guarded.Error+". Choose a declaration move that stays within approved editable targets, request a target-scope update, or report this blocker.",
						)
						if done {
							return finish(answer, err, "go_refactor_guard_exhausted", true)
						}
						continue
					}
				} else {
					req = fallback
					raw = marshalToolRequestRaw(req)
					toolKey = toolRequestKey(req)
					actionClass = toolActionClass(req)
					r.emitEvent(marshalRunEvent("deterministic_tool_fallback", map[string]string{
						"from":   "broad_after_go_outline_guard",
						"tool":   req.Tool,
						"path":   req.Path,
						"target": req.Destination,
					}))
					if onToolRequest != nil {
						onToolRequest(ToolRequestEvent{
							Step:      stepNumber,
							Agent:     r.agentName(),
							Raw:       raw,
							Request:   req,
							Mode:      r.Workspace.Mode(),
							Broad:     workspace.IsBroadInspectionRequest(req),
							Narrowing: workspace.IsNarrowingRequest(req),
							Action:    actionClass,
						})
					}
				}
			} else {
				answer, err, done := rejectTool(
					stepNumber,
					req,
					"Broad inspection rejected after Go outline",
					"go_file_outline already produced declaration boundaries. Continue with go_move_declarations or report the concrete blocker instead of restarting broad discovery.",
				)
				if done {
					return finish(answer, err, "go_refactor_guard_exhausted", true)
				}
				continue
			}
		}
		if !declarationMovesForbidden && shouldRejectFullReadAfterGoOutline(req, collectedResults) {
			if fallback, guarded, ok := r.recommendedGoMoveAfterOutline(req, collectedResults, stepNumber); ok {
				if guarded != nil {
					collectedResults = append(collectedResults, *guarded)
					lastToolRequest = fallback
					resultCopy := *guarded
					lastToolResult = &resultCopy
					turnMessages = append(turnMessages, llama.ChatMessage{
						Role:    "system",
						Content: workspace.FormatToolResultForModel(*guarded),
					})
					emitNextActionState(stepNumber, "deterministic_tool_fallback_skipped", fallback, *guarded)
					r.emitEvent(marshalRunEvent("deterministic_tool_fallback_skipped", map[string]string{
						"from":          "go_file_outline_guard",
						"tool":          fallback.Tool,
						"path":          fallback.Path,
						"target":        fallback.Destination,
						"guard_summary": guarded.Summary,
						"guard_error":   guarded.Error,
						"original_tool": req.Tool,
						"original_path": req.Path,
					}))
				} else {
					req = fallback
					raw = marshalToolRequestRaw(req)
					toolKey = toolRequestKey(req)
					actionClass = toolActionClass(req)
					r.emitEvent(marshalRunEvent("deterministic_tool_fallback", map[string]string{
						"from":   "go_file_outline_guard",
						"tool":   req.Tool,
						"path":   req.Path,
						"target": req.Destination,
					}))
					if onToolRequest != nil {
						onToolRequest(ToolRequestEvent{
							Step:      stepNumber,
							Agent:     r.agentName(),
							Raw:       raw,
							Request:   req,
							Mode:      r.Workspace.Mode(),
							Broad:     workspace.IsBroadInspectionRequest(req),
							Narrowing: workspace.IsNarrowingRequest(req),
							Action:    actionClass,
						})
					}
				}
			} else {
				answer, err, done := rejectTool(
					stepNumber,
					req,
					"Full file read rejected after Go outline",
					"go_file_outline already produced declaration boundaries and recommended move batches for this file. For a structural refactor, choose a destination batch and call go_move_declarations with pattern:\"tag:<responsibility>\" instead of reading the whole file.",
				)
				if done {
					return finish(answer, err, "go_refactor_guard_exhausted", true)
				}
				continue
			}
		}
		if !declarationMovesForbidden && shouldRejectRepeatedGoOutline(req, collectedResults) {
			if repeatedGoOutlineAfterFailedMove(req, collectedResults) {
				answer, err, done := rejectTool(
					stepNumber,
					req,
					"Repeated Go outline rejected after failed move",
					"a recommended declaration move already failed and a follow-up outline was already available. Stop repeating go_file_outline; use edit_file inside approved target paths, request a target-scope update, or report the concrete blocker.",
				)
				if done {
					return finish(answer, err, "go_refactor_guard_exhausted", true)
				}
				continue
			}
			if fallback, guarded, ok := r.recommendedGoMoveAfterOutline(req, collectedResults, stepNumber); ok {
				if guarded != nil {
					answer, err, done := rejectTool(
						stepNumber,
						fallback,
						"Recommended declaration move rejected by guard",
						guarded.Error+". Choose a declaration move that stays within approved editable targets, request a target-scope update, or report this blocker.",
					)
					if done {
						return finish(answer, err, "go_refactor_guard_exhausted", true)
					}
					continue
				}
				req = fallback
				raw = marshalToolRequestRaw(req)
				toolKey = toolRequestKey(req)
				actionClass = toolActionClass(req)
				r.emitEvent(marshalRunEvent("deterministic_tool_fallback", map[string]string{
					"from":   "repeated_go_outline_guard",
					"tool":   req.Tool,
					"path":   req.Path,
					"target": req.Destination,
				}))
				if onToolRequest != nil {
					onToolRequest(ToolRequestEvent{
						Step:      stepNumber,
						Agent:     r.agentName(),
						Raw:       raw,
						Request:   req,
						Mode:      r.Workspace.Mode(),
						Broad:     workspace.IsBroadInspectionRequest(req),
						Narrowing: workspace.IsNarrowingRequest(req),
						Action:    actionClass,
					})
				}
			} else {
				answer, err, done := rejectTool(
					stepNumber,
					req,
					"Repeated Go outline rejected",
					"you already have the Go outline and its recommended move batches. The next structural-refactor step must be go_move_declarations with pattern:\"tag:<responsibility>\", not another outline.",
				)
				if done {
					return finish(answer, err, "go_refactor_guard_exhausted", true)
				}
				continue
			}
		}
		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
			if lastToolResult != nil {
				if replayedToolRequests[toolKey] > 0 {
					answer, err, done := rejectTool(
						stepNumber,
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
				collectedResults = append(collectedResults, replayed)
				consecutiveRejectedActions = 0
				consecutiveNoProgress++
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: replayed})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(replayed),
				})
				lastToolRequest = req
				lastToolResult = &replayed
				emitNextActionState(stepNumber, "tool_replayed", req, replayed)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Do not request any more tools. Summarize the blocker and the narrowest remaining next action.", &metrics)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			errorText := "you just ran the same tool request. Do not loop on broad inspection. Use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command to narrow, or answer with what you already know."
			if strings.TrimSpace(req.Tool) == "go_move_declarations" {
				errorText = "you just ran the same declaration move. Choose a different destination/declaration batch, run verification, or answer with the current blocker."
			}
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Repeated tool request rejected",
				errorText,
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}
			continue
		}
		if decision := r.PermissionPolicy.Decide(req); decision.Decision != PermissionAllow {
			r.emitEvent(marshalRunEvent("permission_decision", decision))
			result := decision.ToolResult()
			collectedResults = append(collectedResults, result)
			lastToolKey = toolKey
			lastToolRequest = req
			resultCopy := result
			lastToolResult = &resultCopy
			metrics.RejectedActions++
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			emitNextActionState(stepNumber, "permission_denied", req, result)
			appendFailureFeedback(stepNumber, "permission_denied", req, result)
			if decision.Decision == PermissionAsk {
				answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults, &metrics)
				return finish(answer, err, "permission_approval_required", false)
			}
			continue
		}
		r.emitEvent(marshalRunEvent("permission_decision", r.PermissionPolicy.Decide(req)))
		if strings.TrimSpace(req.Tool) == "task" {
			if r.DisableTaskTool {
				answer, err, done := rejectTool(
					stepNumber,
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
			collectedResults = append(collectedResults, result)
			lastToolKey = toolKey
			lastToolRequest = req
			resultCopy := result
			lastToolResult = &resultCopy
			consecutiveRejectedActions = 0
			if toolResultMadeProgress(req, result) {
				consecutiveNoProgress = 0
				metrics.ProgressTurns++
			} else {
				consecutiveNoProgress++
			}
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			emitNextActionState(stepNumber, "child_task_result", req, result)
			if strings.TrimSpace(result.Error) != "" {
				appendFailureFeedback(stepNumber, "child_task_result", req, result)
			}
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled after the child task. Do not request any more tools. Summarize the work completed, the child-task result, and the narrowest remaining next action.", &metrics)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "task_ledger" {
			result := taskLedger.apply(req)
			collectedResults = append(collectedResults, result)
			lastToolKey = toolKey
			lastToolRequest = req
			resultCopy := result
			lastToolResult = &resultCopy
			consecutiveRejectedActions = 0
			if toolResultMadeProgress(req, result) {
				consecutiveNoProgress = 0
				metrics.ProgressTurns++
			} else {
				consecutiveNoProgress++
			}
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			emitNextActionState(stepNumber, "task_ledger_result", req, result)
			if strings.TrimSpace(result.Error) != "" {
				appendFailureFeedback(stepNumber, "task_ledger_result", req, result)
			}
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.", &metrics)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}

		if r.PreExecute != nil {
			if guarded := r.PreExecute(stepNumber, req); guarded != nil {
				collectedResults = append(collectedResults, *guarded)
				lastToolKey = toolKey
				lastToolRequest = req
				resultCopy := *guarded
				lastToolResult = &resultCopy
				bootstrapReadReminder := isBootstrappedTargetReadReminder(req, *guarded)
				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder {
					rejectedActions[actionClass]++
					consecutiveRejectedActions++
					metrics.RejectedActions++
				} else if strings.TrimSpace(guarded.Error) != "" {
					metrics.RejectedActions++
					consecutiveNoProgress++
				}
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: *guarded})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(*guarded),
				})
				emitNextActionState(stepNumber, "pre_execute_guard", req, *guarded)
				if strings.TrimSpace(guarded.Error) != "" {
					appendFailureFeedback(stepNumber, "pre_execute_guard", req, *guarded)
				}
				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults, &metrics)
					return finish(answer, err, "write_approval_required", false)
				}
				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && consecutiveRejectedActions >= 2 {
					answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults, &metrics)
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}
				if bootstrapReadReminder && policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Do not request more tools. Summarize the blocker and the narrowest remaining action.", &metrics)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
		}

		if r.CheckpointPolicy.Enabled && !checkpointCreated && isWriteTool(req) {
			checkpoint, checkpointErr := CaptureGitCheckpoint(ctx, r.Workspace.Root())
			if checkpointErr != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Checkpoint creation failed",
					Error:   checkpointErr.Error(),
				}
				collectedResults = append(collectedResults, result)
				lastToolKey = toolKey
				lastToolRequest = req
				resultCopy := result
				lastToolResult = &resultCopy
				metrics.RejectedActions++
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(result),
				})
				emitNextActionState(stepNumber, "checkpoint_failed", req, result)
				appendFailureFeedback(stepNumber, "checkpoint_failed", req, result)
				continue
			}
			checkpointCreated = true
			r.emitEvent(marshalRunEvent("checkpoint", checkpoint))
		}

		result := r.Workspace.Execute(ctx, req)
		collectedResults = append(collectedResults, result)
		lastToolKey = toolKey
		lastToolRequest = req
		resultCopy := result
		lastToolResult = &resultCopy
		consecutiveRejectedActions = 0
		if toolResultMadeProgress(req, result) {
			consecutiveNoProgress = 0
			metrics.ProgressTurns++
		} else {
			consecutiveNoProgress++
		}
		switch {
		case workspace.IsBroadInspectionRequest(req):
			needsNarrowingAfterBroad = true
			broadInspectionCount++
		case r.Workspace.ShouldClearBroadInspectionLock(req, result):
			needsNarrowingAfterBroad = false
		}
		if onToolResult != nil {
			onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
		}

		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		emitNextActionState(stepNumber, "tool_result", req, result)
		if strings.TrimSpace(result.Error) != "" {
			appendFailureFeedback(stepNumber, "tool_result", req, result)
			// Track same-stem tool failures so we can inject a recovery
			// nudge before burning the whole budget on hallucinated paths.
			stem := toolFailurePathStem(req)
			if stem != "" && stem == lastFailedPathStem {
				consecutiveToolFailures++
			} else {
				consecutiveToolFailures = 1
				lastFailedPathStem = stem
			}
			if !softFailureNudgeUsed && consecutiveToolFailures >= 3 {
				if nudge := buildRejectionRecoveryNudge(req, collectedResults, r.Workspace); nudge != "" {
					softFailureNudgeUsed = true
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})
					consecutiveToolFailures = 0
				}
			}
		} else {
			consecutiveToolFailures = 0
			lastFailedPathStem = ""
		}
		missingPathListFailure := r.Workspace.IsExploreMode() && isMissingPathListFailure(req, result)
		if missingPathListFailure {
			consecutiveMissingPathListFailures++
			if consecutiveMissingPathListFailures >= 2 {
				answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Exploration is probing conventional paths that do not exist in this repository. Do not request any more tools. Answer from the bootstrapped evidence and state the narrowest remaining validation step.", &metrics)
				return finish(answer, err, "missing_path_probe_budget_exhausted", true)
			}
		} else {
			consecutiveMissingPathListFailures = 0
		}
		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.", &metrics)
			return finish(answer, err, "no_progress_budget_exhausted", true)
		}
	}

	answer, err = r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, maxToolStepsExhaustedReason(), &metrics)
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

func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Content)
	}
	if prompt == "" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "task prompt is required",
		}
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "read_only"
	}
	if isWriteChildMode(mode) {
		return r.runWorkbranchChildTask(ctx, req, prompt, opts)
	}
	if mode != "read_only" && mode != "readonly" && mode != "explore" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "only read_only child tasks are currently supported",
		}
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "child"
	}
	childPolicy := RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 2, MaxModelCalls: 6, MaxDuration: 90 * time.Second}
	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}
	child := &Runner{
		Client:           r.Client,
		Workspace:        r.Workspace,
		Policy:           childPolicy,
		AgentName:        "child:" + agentName,
		PermissionPolicy: readOnlyChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Disabled: true},
		DisableTaskTool:  true,
		RuntimeBrief:     workerruntime.RuntimeBriefForRole("child_read_only", "child:"+agentName, prompt),
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}
			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	workspaceInstructions := ""
	if r.Workspace != nil {
		workspaceInstructions = r.Workspace.Instructions()
	}
	answer, err := child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a bounded read-only child task.",
					"Answer the delegated question with concise evidence.",
					"Do not edit files, create files, install packages, push, publish, deploy, or spawn more tasks.",
					"Prefer search_files, search_content, read_file, and read-only symbol tools before shell.",
					workspaceInstructions,
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	if err != nil {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task failed",
			Error:   err.Error(),
		}
	}
	return workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  strings.TrimSpace(answer),
	}
}

func isWriteChildMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "write", "workbranch", "workbranch_write", "isolated_write", "patch":
		return true
	default:
		return false
	}
}

func (r *Runner) runWorkbranchChildTask(ctx context.Context, req workspace.ToolRequest, prompt string, opts llama.ChatOptions) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(r.Workspace.Root()) == "" {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task rejected", Error: "parent workspace root is required for isolated write child tasks"}
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "worker"
	}
	manager := WorkbranchManager{Root: r.Workspace.Root()}
	checkout, err := manager.Create(ctx, agentName)
	if err != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: err.Error()}
	}
	defer func() {
		if err := manager.Remove(context.Background(), checkout); err != nil {
			r.emitEvent(marshalRunEvent("child_workbranch_cleanup_error", map[string]string{
				"path":  checkout.Path,
				"error": err.Error(),
			}))
		}
	}()
	r.emitEvent(marshalRunEvent("child_workbranch", checkout))

	childWorkspace := workspace.NewManager(config.Workspace{
		Root:                  checkout.Path,
		CWD:                   checkout.Path,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 120,
		MaxOutputBytes:        131072,
		MaxFileBytes:          262144,
		MaxDirectoryEntries:   2000,
		EnableShell:           true,
		EnableWrites:          true,
	})
	childPolicy := RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 4, MaxModelCalls: 16, MaxDuration: 4 * time.Minute}
	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}
	child := &Runner{
		Client:           r.Client,
		Workspace:        childWorkspace,
		Policy:           childPolicy,
		AgentName:        "child_write:" + agentName,
		PermissionPolicy: workbranchChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Required: true},
		DisableTaskTool:  true,
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		RuntimeBrief:     workerruntime.RuntimeBriefForRole("child_write", "child_write:"+agentName, prompt),
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}
			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	answer, runErr := child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a bounded isolated implementation child task running in a temporary git worktree.",
					"Make only the delegated change. Do not push, publish, deploy, or spawn more tasks.",
					"Keep task_ledger current. Verify the changed surface when practical.",
					"Final answer must include changed files, verification status, and confidence.",
					childWorkspace.Instructions(),
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	diff, diffErr := manager.Diff(ctx, checkout)
	if runErr != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: runErr.Error(), Output: renderWorkbranchChildOutput(checkout, diff, answer)}
	}
	if diffErr != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child diff failed", Error: diffErr.Error(), Output: strings.TrimSpace(answer)}
	}
	return workspace.ToolResult{
		Tool:    "task",
		Summary: fmt.Sprintf("Workbranch child task completed with %d changed path(s)", len(diff.ChangedPaths)),
		Output:  renderWorkbranchChildOutput(checkout, diff, answer),
	}
}

func renderWorkbranchChildOutput(checkout WorkbranchCheckout, diff WorkbranchDiff, answer string) string {
	var b strings.Builder
	b.WriteString("Isolated workbranch result\n")
	b.WriteString("branch: " + checkout.Branch + "\n")
	b.WriteString("base: " + checkout.BaseSHA + "\n")
	if len(diff.ChangedPaths) > 0 {
		b.WriteString("changed paths:\n")
		for _, path := range diff.ChangedPaths {
			b.WriteString("- " + path + "\n")
		}
	}
	if strings.TrimSpace(diff.Stat) != "" {
		b.WriteString("\nstat:\n")
		b.WriteString(diff.Stat + "\n")
	}
	if strings.TrimSpace(answer) != "" {
		b.WriteString("\nchild answer:\n")
		b.WriteString(strings.TrimSpace(answer) + "\n")
	}
	if strings.TrimSpace(diff.Patch) != "" {
		b.WriteString("\npatch:\n```diff\n")
		b.WriteString(strings.TrimSpace(diff.Patch))
		b.WriteString("\n```\n")
	}
	if len(diff.ChangedPaths) > 0 {
		b.WriteString("\nParent must inspect this diff and explicitly apply or reject it; the main workspace was not mutated by the child.\n")
	}
	return strings.TrimSpace(b.String())
}

func readOnlyChildTaskPermissionPolicy() ToolPermissionPolicy {
	allowTool := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionAllow}
	}
	allowShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionAllow}
	}
	rules := make([]ToolPermissionRule, 0, len(childAllowedTools())+20)
	for _, tool := range childAllowedTools() {
		if tool == "shell" {
			continue
		}
		rules = append(rules, allowTool(tool))
	}
	rules = append(rules,
		allowShellPrefix("rg "),
		allowShellPrefix("grep "),
		allowShellPrefix("awk "),
		allowShellPrefix("sed -n"),
		allowShellPrefix("cat "),
		allowShellPrefix("head "),
		allowShellPrefix("tail "),
		allowShellPrefix("wc "),
		allowShellPrefix("pwd"),
		allowShellPrefix("ls "),
		allowShellPrefix("find "),
		allowShellPrefix("git status"),
		allowShellPrefix("git diff"),
		allowShellPrefix("git log"),
		allowShellPrefix("git show"),
		allowShellPrefix("git grep"),
		allowShellPrefix("go test"),
		allowShellPrefix("go list"),
		allowShellPrefix("go env"),
	)
	return ToolPermissionPolicy{
		Default: PermissionDeny,
		Rules:   rules,
	}
}

func workbranchChildTaskPermissionPolicy() ToolPermissionPolicy {
	deny := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionDeny}
	}
	denyShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionDeny}
	}
	rules := []ToolPermissionRule{deny("task")}
	for _, prefix := range workbranchDeniedShellPrefixes(workerruntime.RoleSpecFor("child_write")) {
		rules = append(rules, denyShellPrefix(prefix))
	}
	return ToolPermissionPolicy{
		Default: PermissionAllow,
		Rules:   rules,
	}
}

func childAllowedTools() []string {
	return workerruntime.RoleSpecFor("child_read_only").AllowedTools
}

func workbranchDeniedShellPrefixes(spec workerruntime.RoleSpec) []string {
	denied := []string{"gh pr create"}
	for _, forbidden := range spec.ForbiddenTools {
		switch strings.TrimSpace(forbidden) {
		case "git_push":
			denied = append(denied, "git push", "gh pr merge")
		case "git_history_mutation":
			denied = append(denied, "git commit", "git reset", "git checkout", "git switch", "git worktree", "git branch", "git clean")
		case "git_index_mutation":
			denied = append(denied, "git add", "git restore")
		case "publish":
			denied = append(denied, "npm publish", "pnpm publish", "yarn publish", "docker push")
		case "deploy":
			denied = append(denied, "kubectl ", "deploy", "release", "gh release")
		}
	}
	return mergeRunnerStateStrings(denied)
}

func (r *Runner) effectivePolicy() RunPolicy {
	policy := r.Policy
	if policy.MaxToolSteps == 0 && r.MaxToolSteps != 0 {
		policy.MaxToolSteps = r.MaxToolSteps
	}
	if policy.MaxNoProgressTurns <= 0 {
		policy.MaxNoProgressTurns = 6
	}
	return policy
}

func (p RunPolicy) unbounded() bool {
	return p.MaxToolSteps <= 0
}

func (p RunPolicy) shouldStopForNoProgress(consecutiveNoProgress int) bool {
	return p.MaxNoProgressTurns > 0 && consecutiveNoProgress >= p.MaxNoProgressTurns
}

func (p RunPolicy) shouldStopForModelCalls(used int) bool {
	return p.MaxModelCalls > 0 && used >= p.MaxModelCalls
}

func (p RunPolicy) shouldStopForDuration(startedAt time.Time) bool {
	return p.MaxDuration > 0 && time.Since(startedAt) >= p.MaxDuration
}

type runnerMetrics struct {
	startedAt                time.Time
	ModelCalls               int `json:"used_model_calls"`
	ToolCalls                int `json:"used_tool_results"`
	PromptTokens             int `json:"prompt_tokens,omitempty"`
	CompletionTokens         int `json:"completion_tokens,omitempty"`
	TotalTokens              int `json:"total_tokens,omitempty"`
	ProgressTurns            int `json:"progress_turns"`
	RejectedActions          int `json:"rejected_actions"`
	CompletionGateRejections int `json:"completion_gate_rejections"`
	Compactions              int `json:"context_compactions"`
}

type runnerBudgetLedger struct {
	Lane                     string   `json:"lane"`
	UsedModelCalls           int      `json:"used_model_calls"`
	MaxModelCalls            int      `json:"max_model_calls,omitempty"`
	UsedToolResults          int      `json:"used_tool_results"`
	MaxToolSteps             int      `json:"max_tool_steps,omitempty"`
	PromptTokens             int      `json:"prompt_tokens,omitempty"`
	CompletionTokens         int      `json:"completion_tokens,omitempty"`
	TotalTokens              int      `json:"total_tokens,omitempty"`
	ProgressTurns            int      `json:"progress_turns"`
	RejectedActions          int      `json:"rejected_actions"`
	CompletionGateRejections int      `json:"completion_gate_rejections"`
	ContextCompactions       int      `json:"context_compactions"`
	DurationMillis           int64    `json:"duration_millis"`
	StopReason               string   `json:"stop_reason"`
	BudgetExhausted          bool     `json:"budget_exhausted"`
	NextAllowedActions       []string `json:"next_allowed_actions,omitempty"`
	UserVisibleUncertainty   string   `json:"user_visible_uncertainty,omitempty"`
}

func (m *runnerMetrics) addUsage(usage llama.Usage) {
	if m == nil {
		return
	}
	m.PromptTokens += nonNegativeInt(usage.PromptTokens)
	m.CompletionTokens += nonNegativeInt(usage.CompletionTokens)
	total := usage.TotalTokens
	if total <= 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	m.TotalTokens += nonNegativeInt(total)
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

type runnerNextActionState struct {
	Lane               string   `json:"lane"`
	Step               int      `json:"step"`
	Reason             string   `json:"reason"`
	LastTool           string   `json:"last_tool,omitempty"`
	LastTarget         string   `json:"last_target,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	ResultError        string   `json:"result_error,omitempty"`
	ResultOutputChars  int      `json:"result_output_chars,omitempty"`
	EvidencePaths      []string `json:"evidence_paths,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	InvalidNextActions []string `json:"invalid_next_actions,omitempty"`
	NextValidActions   []string `json:"next_valid_actions,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
}

type runnerThinkingTrace struct {
	Lane               string `json:"lane"`
	Step               int    `json:"step"`
	Reason             string `json:"reason"`
	EnableThinking     string `json:"enable_thinking"`
	PreserveThinking   bool   `json:"preserve_thinking"`
	ContentChars       int    `json:"content_chars"`
	ReasoningChars     int    `json:"reasoning_chars"`
	PreservedInHistory bool   `json:"preserved_in_history"`
}

type runnerContextCompaction struct {
	Lane             string   `json:"lane"`
	DroppedMessages  int      `json:"dropped_messages"`
	RetainedMessages int      `json:"retained_messages"`
	MaxHistory       int      `json:"max_history,omitempty"`
	MaxChars         int      `json:"max_chars,omitempty"`
	PinnedPaths      []string `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string   `json:"pinned_memory_kind"`
	Strategy         string   `json:"strategy"`
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
	state := buildRunnerNextActionState(r.agentName(), step, reason, req, result, results, declarationMovesForbidden)
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
	})
	failureClass := ""
	if strings.TrimSpace(result.Error) != "" {
		failureClass = workerruntime.ClassifyFailure("current_state", req, result)
	}
	state := workerruntime.RunState{
		Lane:           r.agentName(),
		Phase:          workerruntime.ClassifyPhase(req, result),
		Step:           step,
		EvidencePaths:  pinnedPathsFromToolResults(results, 6),
		ChangedPaths:   mapKeys(changedPathsFromToolResults(results)),
		Constraints:    guidance.Constraints,
		LastTool:       firstNonEmpty(strings.TrimSpace(req.Tool), strings.TrimSpace(result.Tool)),
		LastResult:     workerruntime.SummarizeToolResult(result),
		FailureClass:   failureClass,
		NextActions:    guidance.ValidNext,
		InvalidActions: guidance.InvalidNext,
	}
	// After step 3 the static scaffolding (tool_manifest, allowed/forbidden
	// tools, readiness gate, search policy, stop conditions, final critic,
	// target packet) is already in the model's context — repeating it each
	// step just burns tokens. Emit only the mutable fields from step 4 on.
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
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func buildRunnerNextActionState(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) runnerNextActionState {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		DeclarationMovesForbidden: declarationMovesForbidden,
	})
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          tool,
		LastTarget:        workerruntime.ToolRequestTarget(req),
		ResultSummary:     truncateRunnerStateText(result.Summary, 220),
		ResultError:       truncateRunnerStateText(result.Error, 260),
		ResultOutputChars: len(result.Output),
		EvidencePaths:     pinnedPathsFromToolResults(results, 6),
	}
	state.Constraints = guidance.Constraints
	state.InvalidNextActions = guidance.InvalidNext
	state.NextValidActions = guidance.ValidNext
	state.StateSummary = runnerStateSummary(state)
	return state
}

func buildRunnerFailureFeedbackPacket(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) runnerFailureFeedbackPacket {
	state := buildRunnerNextActionState(agent, step, reason, req, result, results, declarationMovesForbidden)
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		EvidencePaths:             state.EvidencePaths,
		DeclarationMovesForbidden: declarationMovesForbidden,
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
		DurationMillis:           time.Since(metrics.startedAt).Milliseconds(),
		StopReason:               strings.TrimSpace(stopReason),
		BudgetExhausted:          exhausted,
	}
	if policy.MaxToolSteps > 0 {
		ledger.MaxToolSteps = policy.MaxToolSteps
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

type taskLedgerState struct {
	policy  TaskLedgerPolicy
	created bool
	items   []workspace.TaskLedgerItem
}

func newTaskLedgerState(policy TaskLedgerPolicy) *taskLedgerState {
	return &taskLedgerState{policy: policy}
}

func (s *taskLedgerState) apply(req workspace.ToolRequest) workspace.ToolResult {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "task_ledger is disabled for this runner",
		}
	}
	items := taskLedgerItemsFromRequest(req)
	if len(items) == 0 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "provide a non-empty todos array with content and status for each item",
		}
	}
	normalized := make([]workspace.TaskLedgerItem, 0, len(items))
	active := 0
	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		item.Status = normalizeTaskLedgerStatus(item.Status)
		item.Evidence = strings.TrimSpace(item.Evidence)
		if item.ID == "" {
			item.ID = fmt.Sprintf("item-%d", i+1)
		}
		if item.Content == "" {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s is missing content", item.ID),
			}
		}
		if !validTaskLedgerStatus(item.Status) {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s has unsupported status %q; use pending, in_progress, completed, blocked, or cancelled", item.ID, item.Status),
			}
		}
		if item.Status == "in_progress" {
			active++
		}
		normalized = append(normalized, item)
	}
	if active > 1 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "only one task ledger item may be in_progress at a time",
		}
	}

	s.created = true
	s.items = normalized
	return workspace.ToolResult{
		Tool:    "task_ledger",
		Summary: "Task ledger updated: " + s.statusSummary(),
		Output:  s.render(),
	}
}

func (s *taskLedgerState) completionRejection(reply string) (workspace.ToolResult, bool) {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{}, false
	}
	if s.policy.Required && !s.created && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "this is complex multi-step work, but no task_ledger has been created. Create a task ledger, continue the work, or answer with an explicit blocker/no-op reason.",
		}, true
	}
	if !s.created {
		return workspace.ToolResult{}, false
	}
	if unfinished := s.unfinishedItems(); len(unfinished) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger still has unfinished required work: " + strings.Join(unfinished, "; ") + ". Continue with the next tool call or update the ledger with a concrete blocker.",
		}, true
	}
	if blocked := s.blockedItems(); len(blocked) > 0 && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger contains blocked work: " + strings.Join(blocked, "; ") + ". The final answer must state the concrete blocker.",
		}, true
	}
	return workspace.ToolResult{}, false
}

func (s *taskLedgerState) unfinishedItems() []string {
	if s == nil {
		return nil
	}
	var unfinished []string
	for _, item := range s.items {
		switch item.Status {
		case "pending", "in_progress":
			unfinished = append(unfinished, item.ID+": "+item.Content+" ["+item.Status+"]")
		}
	}
	return unfinished
}

func (s *taskLedgerState) blockedItems() []string {
	if s == nil {
		return nil
	}
	var blocked []string
	for _, item := range s.items {
		if item.Status != "blocked" {
			continue
		}
		text := item.ID + ": " + item.Content
		if item.Evidence != "" {
			text += " (" + item.Evidence + ")"
		}
		blocked = append(blocked, text)
	}
	return blocked
}

func (s *taskLedgerState) statusSummary() string {
	counts := make(map[string]int)
	for _, item := range s.items {
		counts[item.Status]++
	}
	parts := make([]string, 0, 5)
	for _, status := range []string{"completed", "in_progress", "pending", "blocked", "cancelled"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func (s *taskLedgerState) render() string {
	if s == nil || len(s.items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range s.items {
		b.WriteString("- [")
		b.WriteString(item.Status)
		b.WriteString("] ")
		b.WriteString(item.ID)
		b.WriteString(": ")
		b.WriteString(item.Content)
		if item.Evidence != "" {
			b.WriteString(" (evidence: ")
			b.WriteString(item.Evidence)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func taskLedgerItemsFromRequest(req workspace.ToolRequest) []workspace.TaskLedgerItem {
	if len(req.Todos) > 0 {
		return append([]workspace.TaskLedgerItem(nil), req.Todos...)
	}
	return append([]workspace.TaskLedgerItem(nil), req.Items...)
}

func normalizeTaskLedgerStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	status = strings.ReplaceAll(status, " ", "_")
	return status
}

func validTaskLedgerStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked", "cancelled":
		return true
	default:
		return false
	}
}

func taskLedgerFinalHasTerminalReason(reply string) bool {
	return containsAny(strings.ToLower(strings.TrimSpace(reply)),
		"blocked",
		"blocker",
		"cannot proceed",
		"could not proceed",
		"unable to",
		"no changes needed",
		"no change needed",
		"already complete",
		"already satisfied",
		"permission",
		"approval",
	)
}

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) (workspace.ToolResult, bool) {
	if taskLedger != nil {
		if result, rejected := taskLedger.completionRejection(reply); rejected {
			return result, true
		}
	}
	if !declarationMovesForbidden && shouldRejectProseAfterGoOutline(reply, results) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Final answer rejected",
			Error:   "go_file_outline produced declaration boundaries, but no declaration move has succeeded. Continue with go_move_declarations or report a concrete blocker from a failed move.",
		}, true
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

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(data)
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
		return "edit_file:" + normalizeActionToken(req.Path)
	case "search_files", "search_content", "rg":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
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

func shouldRejectProseAfterGoOutline(reply string, results []workspace.ToolResult) bool {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	if normalized == "" {
		return false
	}
	if goMoveDeclarationFailed(results) && containsAny(normalized, "blocker", "blocked", "cannot", "unable", "not approved", "approval", "scope") {
		return false
	}
	return goOutlineWithoutMove(results)
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

func shouldRejectRepeatedGoOutline(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "go_file_outline" || strings.TrimSpace(req.Path) == "" {
		return false
	}
	if repeatedGoOutlineAfterFailedMove(req, results) {
		return true
	}
	if goMoveDeclarationFailed(results) {
		return false
	}
	count := 0
	for _, result := range results {
		if result.Error != "" || result.Tool != "go_file_outline" {
			continue
		}
		if strings.Contains(result.Summary, req.Path) {
			count++
		}
	}
	return count >= 1
}

func repeatedGoOutlineAfterFailedMove(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "go_file_outline" || strings.TrimSpace(req.Path) == "" {
		return false
	}
	lastFailedMove := -1
	for i, result := range results {
		if result.Tool == "go_move_declarations" && strings.TrimSpace(result.Error) != "" {
			lastFailedMove = i
		}
	}
	if lastFailedMove < 0 {
		return false
	}
	path := strings.TrimSpace(req.Path)
	outlinesAfterFailure := 0
	for _, result := range results[lastFailedMove+1:] {
		if result.Error != "" || result.Tool != "go_file_outline" {
			continue
		}
		if strings.Contains(result.Summary, path) {
			outlinesAfterFailure++
		}
	}
	return outlinesAfterFailure >= 1
}

func shouldRejectFullReadAfterGoOutline(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if goMoveDeclarationFailed(results) {
		return false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || strings.TrimSpace(req.ID) != "" || req.StartLine != 0 || req.EndLine != 0 {
		return false
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if result.Error != "" || result.Tool != "go_file_outline" {
			continue
		}
		if strings.Contains(result.Summary, path) {
			return true
		}
	}
	return false
}

func shouldRejectBroadInspectionAfterGoOutline(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if goMoveDeclarationFailed(results) {
		return false
	}
	if !workspace.IsBroadInspectionRequest(req) {
		return false
	}
	return goOutlineWithoutMove(results)
}

func goOutlineWithoutMove(results []workspace.ToolResult) bool {
	hasOutline := false
	hasMove := false
	for _, result := range results {
		if result.Error == "" && result.Tool == "go_file_outline" {
			hasOutline = true
		}
		if result.Error == "" && result.Tool == "go_move_declarations" {
			hasMove = true
		}
	}
	return hasOutline && !hasMove
}

func recommendedGoMoveAfterOutline(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	tool := strings.TrimSpace(req.Tool)
	if tool != "read_file" && tool != "go_file_outline" && !workspace.IsBroadInspectionRequest(req) {
		return workspace.ToolRequest{}, false
	}
	path := strings.TrimSpace(req.Path)
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if result.Error != "" || result.Tool != "go_file_outline" {
			continue
		}
		if path != "" && !strings.Contains(result.Summary, path) && !strings.Contains(result.Output, path) {
			continue
		}
		if move, ok := firstGoMoveDeclarationRequest(result.Output); ok {
			return move, true
		}
	}
	return workspace.ToolRequest{}, false
}

func (r *Runner) recommendedGoMoveAfterOutline(req workspace.ToolRequest, results []workspace.ToolResult, step int) (workspace.ToolRequest, *workspace.ToolResult, bool) {
	move, ok := recommendedGoMoveAfterOutline(req, results)
	if !ok {
		return workspace.ToolRequest{}, nil, false
	}
	if r != nil && r.PreExecute != nil {
		if guarded := r.PreExecute(step, move); guarded != nil {
			return move, guarded, true
		}
	}
	return move, nil, true
}

func firstGoMoveDeclarationRequest(output string) (workspace.ToolRequest, bool) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, `"tool":"go_move_declarations"`) && !strings.Contains(line, `"tool": "go_move_declarations"`) {
			continue
		}
		start := strings.Index(line, "{")
		if start < 0 {
			continue
		}
		var req workspace.ToolRequest
		if err := json.Unmarshal([]byte(line[start:]), &req); err != nil {
			continue
		}
		if strings.TrimSpace(req.Tool) == "go_move_declarations" && strings.TrimSpace(req.Path) != "" {
			return req, true
		}
	}
	return workspace.ToolRequest{}, false
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

func (r *Runner) agentName() string {
	if strings.TrimSpace(r.AgentName) == "" {
		return "main"
	}
	return r.AgentName
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
	assistantMessage, err := r.Client.CompleteChatMessage(ctx, turnMessages, opts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}
	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, opts, false)

	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	return r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, opts, collectedResults, metrics)
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

func postWriteFocusRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	changedPaths := changedPathsFromToolResults(results)
	if len(changedPaths) == 0 {
		return "", "", false
	}
	changedList := strings.Join(mapKeys(changedPaths), ", ")
	if workspace.IsBroadInspectionRequest(req) {
		return "Post-edit broad discovery rejected",
			"a workspace edit already landed in " + changedList + ". Continue with targeted verification, a narrow read of the changed file, task_ledger, or a final blocker; do not restart broad discovery.",
			true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || strings.TrimSpace(req.Path) == "" {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if _, ok := changedPaths[path]; ok {
		return "", "", false
	}
	return "Post-edit unrelated read rejected",
		"a workspace edit already landed in " + changedList + ". Read the changed file, run verification, update task_ledger, or answer with the concrete blocker instead of inspecting unrelated files.",
		true
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
	retryMessage, err := r.Client.CompleteChatMessage(ctx, retryMessages, opts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}
	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, opts, false)
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

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func isResultUsefulForFallback(result workspace.ToolResult) bool {
	switch result.Tool {
	case "read_file", "search_content", "search_files":
		return true
	default:
		return false
	}
}

func limitFallbackResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}
	return results[:max]
}

func fallbackLineBudgetForTool(tool string) int {
	switch tool {
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
func buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult, mgr *workspace.Manager) string {
	tool := strings.TrimSpace(req.Tool)
	var b strings.Builder
	b.WriteString("Recovery nudge — this is your last chance before the harness ends the turn. Pick ONE of the concrete next steps below (you can copy-paste the JSON). Do NOT repeat the rejected tool call.\n\n")

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
			path := strings.TrimSpace(line)
			if path == "" || strings.HasPrefix(path, "/") {
				continue
			}
			if strings.Contains(path, " ") {
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
