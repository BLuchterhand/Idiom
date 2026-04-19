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
	AgentName             string
	InitialNeedsNarrowing bool
	InitialBroadInspects  int
	SeedResults           []workspace.ToolResult
	PreExecute            func(step int, req workspace.ToolRequest) *workspace.ToolResult
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
	var lastToolKey string
	needsNarrowingAfterBroad := r.InitialNeedsNarrowing
	broadInspectionCount := r.InitialBroadInspects
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0

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

	for step := 0; step < r.MaxToolSteps; step++ {
		stepNumber := step + 1
		if onStatus != nil && step > 0 {
			onStatus(fmt.Sprintf("Qwen is still working (%d/%d).", stepNumber, r.MaxToolSteps))
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
		if toolKey != "" && toolKey == lastToolKey {
			answer, err, done := rejectTool(
				stepNumber,
				req,
				"Repeated tool request rejected",
				"you just ran the same tool request. Do not loop on broad inspection. Use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command to narrow, or answer with what you already know.",
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
	}

	return r.completeWithoutMoreTools(ctx, turnMessages, opts, collectedResults)
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
	case "go_find_symbol", "go_find_type", "go_find_const", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		target := firstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	default:
		return tool
	}
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
	turnMessages = append(turnMessages, llama.ChatMessage{
		Role: "system",
		Content: "Tool budget is exhausted. Do not request any more tools. " +
			"Answer with the evidence you already have, explain any missing file or uncertainty clearly, " +
			"and if needed ask the user for a more specific target instead of emitting another tool block.",
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
