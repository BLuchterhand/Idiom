package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
}

type RunPolicy struct {
	MaxToolSteps       int
	MaxNoProgressTurns int
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
) (string, error) {
	turnMessages := append([]llama.ChatMessage(nil), messages...)
	policy := r.effectivePolicy()
	var lastToolKey string
	needsNarrowingAfterBroad := r.InitialNeedsNarrowing
	broadInspectionCount := r.InitialBroadInspects
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	completionGateRejections := 0
	consecutiveNoProgress := 0

	rejectTool := func(stepNumber int, req workspace.ToolRequest, summary string, errorText string) (string, error, bool) {
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

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		if onStatus != nil && step > 0 {
			if policy.unbounded() {
				onStatus(fmt.Sprintf("Qwen is still working (%d steps).", stepNumber))
			} else {
				onStatus(fmt.Sprintf("Qwen is still working (%d/%d).", stepNumber, policy.MaxToolSteps))
			}
		}

		reply, err := r.Client.CompleteChat(ctx, turnMessages, opts)
		if err != nil {
			return "", err
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
			if result, rejected := r.completionGateRejection(reply, collectedResults, stepNumber); rejected {
				if completionGateRejections >= 3 {
					return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Completion gate has rejected the final answer repeatedly. Do not request any more tools. Summarize the concrete blocker and the next action required.")
				}
				rejectCompletion(stepNumber, reply, result)
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
				}
				continue
			}
			return r.ensureNonEmptyAnswer(ctx, turnMessages, reply, opts, collectedResults)
		}

		turnMessages = append(turnMessages, llama.ChatMessage{Role: "assistant", Content: reply})
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
				return answer, err
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
				return answer, err
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
				return answer, err
			}
			continue
		}
		if shouldRejectFullReadAfterGoOutline(req, collectedResults) {
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Full file read rejected after Go outline",
				"go_file_outline already produced declaration boundaries and recommended move batches for this file. For a structural refactor, choose a destination batch and call go_move_declarations with pattern:\"tag:<responsibility>\" instead of reading the whole file.",
			)
			if done {
				return answer, err
			}
			continue
		}
		if shouldRejectRepeatedGoOutline(req, collectedResults) {
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Repeated Go outline rejected",
				"you already have the Go outline and its recommended move batches. The next structural-refactor step must be go_move_declarations with pattern:\"tag:<responsibility>\", not another outline.",
			)
			if done {
				return answer, err
			}
			continue
		}
		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
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
				return answer, err
			}
			continue
		}

		if r.PreExecute != nil {
			if guarded := r.PreExecute(stepNumber, req); guarded != nil {
				collectedResults = append(collectedResults, *guarded)
				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" {
					rejectedActions[actionClass]++
					consecutiveRejectedActions++
				}
				if onToolResult != nil {
					onToolResult(ToolResultEvent{Step: stepNumber, Agent: r.agentName(), Result: *guarded})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: workspace.FormatToolResultForModel(*guarded),
				})
				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					return r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
				}
				if strings.TrimSpace(guarded.Error) != "" && consecutiveRejectedActions >= 2 {
					return r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
				}
				continue
			}
		}

		result := r.Workspace.Execute(ctx, req)
		collectedResults = append(collectedResults, result)
		lastToolKey = toolKey
		consecutiveRejectedActions = 0
		if toolResultMadeProgress(req, result) {
			consecutiveNoProgress = 0
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
			return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Progress appears stalled. Do not request any more tools. Summarize the work completed, the last blocker, and the narrowest remaining next action.")
		}
	}

	return r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
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

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int) (workspace.ToolResult, bool) {
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
	default:
		return tool
	}
}

func shouldRejectProseAfterGoOutline(reply string, results []workspace.ToolResult) bool {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	if normalized == "" {
		return false
	}
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
