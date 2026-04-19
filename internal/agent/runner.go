package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	OnEvent               func(RunEvent)
	DisableTaskTool       bool
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
	var lastToolResult *workspace.ToolResult
	needsNarrowingAfterBroad := r.InitialNeedsNarrowing
	broadInspectionCount := r.InitialBroadInspects
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	completionGateRejections := 0
	consecutiveNoProgress := 0
	taskLedger := newTaskLedgerState(r.TaskLedger)
	checkpointCreated := false

	rejectTool := func(stepNumber int, req workspace.ToolRequest, summary string, errorText string) (string, error, bool) {
		metrics.RejectedActions++
		result := workspace.ToolResult{
			Tool:    req.Tool,
			Summary: summary,
			Error:   errorText,
		}
		collectedResults = append(collectedResults, result)
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
		if consecutiveRejectedActions >= 2 {
			answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
			return answer, err, true
		}
		return "", nil, false
	}

	rejectCompletion := func(stepNumber int, reply string, result workspace.ToolResult) {
		completionGateRejections++
		metrics.CompletionGateRejections++
		consecutiveNoProgress++
		collectedResults = append(collectedResults, result)
		if onToolResult != nil {
			onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
		}
		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:    "assistant",
			Content: reply,
		})
		turnMessages = append(turnMessages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
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
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Run duration budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.")
			return finish(answer, err, "duration_budget_exhausted", true)
		}
		if policy.shouldStopForModelCalls(metrics.ModelCalls) {
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Model-call budget is exhausted. Do not request any more tools. Summarize the work completed, unresolved gaps, and the narrowest next action.")
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
		reply, err := r.Client.CompleteChat(ctx, turnMessages, opts)
		if err != nil {
			return finish("", err, "model_error", false)
		}

		req, raw, ok, err := workspace.ParseToolRequest(reply)
		if err != nil {
			result := workspace.ToolResult{
				Tool:    "tool_parse",
				Summary: "Malformed tool request rejected",
				Error:   err.Error() + ". Emit exactly one valid JSON tool object or answer normally.",
				Output:  strings.TrimSpace(raw),
			}
			collectedResults = append(collectedResults, result)
			metrics.RejectedActions++
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			continue
		}
		if !ok {
			if result, rejected := r.completionGateRejection(reply, collectedResults, stepNumber, taskLedger); rejected {
				if completionGateRejections >= 3 {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Completion gate has rejected the final answer repeatedly. Do not request any more tools. Summarize the concrete blocker and the next action required.")
					return finish(answer, err, "completion_gate_repeated_rejection", true)
				}
				rejectCompletion(stepNumber, reply, result)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, reply, opts, collectedResults)
			return finish(answer, err, "model_final", false)
		}

		turnMessages = append(turnMessages, llama.ChatMessage{Role: "assistant", Content: reply})
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
		if shouldRejectBroadInspectionAfterGoOutline(req, collectedResults) {
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
		if shouldRejectFullReadAfterGoOutline(req, collectedResults) {
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
		if shouldRejectRepeatedGoOutline(req, collectedResults) {
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
				replayed := replayRepeatedToolRequest(req, *lastToolResult)
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
				lastToolResult = &replayed
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Do not request any more tools. Summarize the blocker and the narrowest remaining next action.")
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
			metrics.RejectedActions++
			if onToolResult != nil {
				onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
			}
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: workspace.FormatToolResultForModel(result),
			})
			if decision.Decision == PermissionAsk {
				answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
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
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled after the child task. Do not request any more tools. Summarize the work completed, the child-task result, and the narrowest remaining next action.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "task_ledger" {
			result := taskLedger.apply(req)
			collectedResults = append(collectedResults, result)
			lastToolKey = toolKey
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
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}

		if r.PreExecute != nil {
			if guarded := r.PreExecute(stepNumber, req); guarded != nil {
				collectedResults = append(collectedResults, *guarded)
				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" {
					rejectedActions[actionClass]++
					consecutiveRejectedActions++
					metrics.RejectedActions++
				}
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: *guarded})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(*guarded),
				})
				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
					return finish(answer, err, "write_approval_required", false)
				}
				if strings.TrimSpace(guarded.Error) != "" && consecutiveRejectedActions >= 2 {
					answer, err := r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
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
				metrics.RejectedActions++
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: result})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(result),
				})
				continue
			}
			checkpointCreated = true
			r.emitEvent(marshalRunEvent("checkpoint", checkpoint))
		}

		result := r.Workspace.Execute(ctx, req)
		collectedResults = append(collectedResults, result)
		lastToolKey = toolKey
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
		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			answer, err := r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
			return finish(answer, err, "no_progress_budget_exhausted", true)
		}
	}

	answer, err = r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
	return finish(answer, err, "tool_step_budget_exhausted", true)
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

func (r *Runner) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	ledger := runnerBudgetLedger{
		Lane:                     r.agentName(),
		UsedModelCalls:           metrics.ModelCalls,
		MaxModelCalls:            policy.MaxModelCalls,
		UsedToolResults:          metrics.ToolCalls,
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
		Content: r.runnerCompactionSummary(ledger, results),
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

func (r *Runner) runnerCompactionSummary(ledger *taskLedgerState, results []workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("[runner context compaction]\n")
	b.WriteString("agent: ")
	b.WriteString(r.agentName())
	b.WriteString("\nstrategy: older runner turns were compacted; preserve this state when deciding the next action.\n")
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
		total += len(msg.Role) + len(msg.Content)
	}
	return total
}

func tailToolResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}
	return results[len(results)-max:]
}

func pinnedPathsFromToolResults(results []workspace.ToolResult, max int) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range results {
		for _, field := range []string{result.Summary, result.Output, result.Error} {
			for _, token := range strings.Fields(field) {
				token = strings.Trim(token, " \t\r\n`'\".,;:()[]{}")
				if !looksLikeWorkspacePath(token) {
					continue
				}
				if _, ok := seen[token]; ok {
					continue
				}
				seen[token] = struct{}{}
				paths = append(paths, token)
				if max > 0 && len(paths) >= max {
					return paths
				}
			}
		}
	}
	return paths
}

func looksLikeWorkspacePath(token string) bool {
	if token == "" || strings.HasPrefix(token, "/") || strings.Contains(token, "://") {
		return false
	}
	if !strings.Contains(token, "/") {
		return false
	}
	return strings.Contains(token, ".")
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

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState) (workspace.ToolResult, bool) {
	if taskLedger != nil {
		if result, rejected := taskLedger.completionRejection(reply); rejected {
			return result, true
		}
	}
	if shouldRejectProseAfterGoOutline(reply, results) {
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

func shouldRejectRepeatedGoOutline(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "go_file_outline" || strings.TrimSpace(req.Path) == "" {
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

func shouldRejectFullReadAfterGoOutline(req workspace.ToolRequest, results []workspace.ToolResult) bool {
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
) (string, error) {
	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Do not request any more tools. Answer with the evidence you already have, explain any missing file or uncertainty clearly, and if needed ask the user for a more specific target instead of emitting another tool block.")
}

func (r *Runner) completeWithoutMoreToolsWithReason(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Do not request any more tools. Answer with the evidence you already have and explain any remaining blocker clearly."
	}
	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	reply, err := r.Client.CompleteChat(ctx, turnMessages, opts)
	if err != nil {
		return "", err
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := synthesizeFallbackAnswer(r.agentName(), collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	return r.ensureNonEmptyAnswer(ctx, turnMessages, reply, opts, collectedResults)
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "task_ledger":
		return strings.TrimSpace(result.Output) != ""
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations":
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

func isWriteTool(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations":
		return true
	default:
		return false
	}
}

func (r *Runner) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply string,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
) (string, error) {
	trimmed := strings.TrimSpace(reply)
	if trimmed != "" {
		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your last response was empty. Reply now with a concise, non-empty message. " +
			"Do not emit a tool block, and do not leave the response blank.",
	})

	retryReply, err := r.Client.CompleteChat(ctx, retryMessages, opts)
	if err != nil {
		return "", err
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

	var b strings.Builder
	if agentName == "explore" {
		b.WriteString("Exploration did not complete cleanly, but here is the evidence gathered.\n\n")
	} else {
		b.WriteString("The agent did not complete cleanly, but here is the evidence gathered.\n\n")
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

	b.WriteString("Recommended next step:\n")
	if agentName == "explore" {
		b.WriteString("- run a targeted `search_content` or `read_file` on one of the discovered files instead of another broad listing")
	} else {
		b.WriteString("- retry with a more specific file or symptom so the agent can narrow immediately")
	}

	return strings.TrimSpace(b.String()), true
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
