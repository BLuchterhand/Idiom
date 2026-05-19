package agent

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

type runnerTurnController struct {
	runner                     *Runner
	ctx                        context.Context
	opts                       *llama.ChatOptions
	metrics                    *runnerMetrics
	requestBudget              *runnerRequestBudgetState
	recorder                   *runnerTurnRecorder
	turnMessages               *[]llama.ChatMessage
	collectedResults           *[]workspace.ToolResult
	rejectedActions            map[string]int
	rejectionRecoveryNudgeUsed *bool
	completionGateRejections   *int
	consecutiveRejected        *int
	consecutiveNoProgress      *int
	currentFingerprint         *string
}

func (c *runnerTurnController) rejectTool(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, summary string, errorText string) (string, error, bool) {
	if c == nil || c.recorder == nil {
		return "", nil, false
	}
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Error:   errorText,
	}
	if c.requestBudget != nil && c.currentFingerprint != nil && strings.TrimSpace(*c.currentFingerprint) != "" {
		c.requestBudget.fingerprint.rejected[*c.currentFingerprint] = truncForRetryReason(summary)
	}
	countAsDeadEnd := !isRecoverablePostReadinessGapRejection(summary)
	if countAsDeadEnd {
		if action := toolActionClass(req); action != "" && c.rejectedActions != nil {
			c.rejectedActions[action]++
		}
		if c.consecutiveRejected != nil {
			*c.consecutiveRejected++
		}
	}
	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, "tool_rejected", runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})
	if c.consecutiveRejected != nil && *c.consecutiveRejected >= 2 {
		if c.rejectionRecoveryNudgeUsed != nil && !*c.rejectionRecoveryNudgeUsed {
			if nudge := buildRejectionRecoveryNudge(req, c.snapshotResults(), c.runner.Workspace, c.runner.RuntimeBrief, c.runner.agentName(), false, false); nudge != "" {
				*c.rejectionRecoveryNudgeUsed = true
				if c.turnMessages != nil {
					*c.turnMessages = append(*c.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				}
				c.runner.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})
				*c.consecutiveRejected = 0
				return "", nil, false
			}
		}
		answer, err := c.runner.completeWithoutMoreTools(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), c.metrics)
		return answer, err, true
	}
	return "", nil, false
}

func (c *runnerTurnController) rejectCompletion(step int, activeToolCall *llama.ToolCall, reply llama.ChatMessage, result workspace.ToolResult) {
	if c == nil || c.recorder == nil {
		return
	}
	if c.completionGateRejections != nil {
		*c.completionGateRejections++
	}
	if c.metrics != nil {
		c.metrics.CompletionGateRejections++
	}
	historyReply := reply
	if !c.currentOpts().PreserveThinking {
		historyReply.ReasoningContent = ""
	}
	if c.turnMessages != nil {
		*c.turnMessages = append(*c.turnMessages, completionGateRejectedHistoryMessage(c.runner, historyReply, result, c.snapshotResults()))
	}
	c.runner.emitThinkingTrace(step, "completion_gate_reply_preserved", historyReply, c.currentOpts(), true)
	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, workspace.ToolRequest{Tool: "completion_gate"}, result, "completion_gate_rejected", runnerRecordOptions{
		IncrementNoProgress: true,
		SetLastToolKey:      false,
	})
}

func completionGateRejectedHistoryMessage(r *Runner, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) llama.ChatMessage {
	message := llama.ChatMessage{
		Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          reply.Content,
		ReasoningContent: reply.ReasoningContent,
	}
	if !shouldCompactRejectedCompletionReply(r, reply, result, results) {
		return message
	}
	content := strings.TrimSpace(reply.Content)
	message.Content = fmt.Sprintf(
		"[omitted rejected final answer: %d chars. The completion gate rejected it before required workspace changes were recorded: %s. Next action must satisfy the gate with an allowed tool call or a concrete blocker. Preview: %s]",
		len(content),
		truncateRunnerStateText(result.Error, 240),
		truncateRunnerStateText(content, 500),
	)
	message.ReasoningContent = ""
	return message
}

func shouldCompactRejectedCompletionReply(r *Runner, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Tool) != "completion_gate" {
		return false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	content := strings.TrimSpace(reply.Content)
	if len(content) < 4000 {
		return false
	}
	return strings.TrimSpace(result.Error) != ""
}

func (c *runnerTurnController) recordParseFailure(step int, activeToolCall *llama.ToolCall, raw string, parseErr error) {
	if c == nil || c.recorder == nil || parseErr == nil {
		return
	}
	result := workspace.ToolResult{
		Tool:    "tool_parse",
		Summary: "Malformed tool request rejected",
		Error:   parseErr.Error() + ". Emit exactly one valid tool call or answer normally.",
		Output:  strings.TrimSpace(raw),
	}
	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, workspace.ToolRequest{Tool: "tool_parse"}, result, "tool_parse_rejected", runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: false,
	})
}

func (c *runnerTurnController) recordRejectedResult(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, result workspace.ToolResult, reason string) {
	if c == nil || c.recorder == nil {
		return
	}
	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, reason, runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})
}

func (c *runnerTurnController) snapshotResults() []workspace.ToolResult {
	if c == nil || c.collectedResults == nil {
		return nil
	}
	return *c.collectedResults
}

func (c *runnerTurnController) currentMessages() []llama.ChatMessage {
	if c == nil || c.turnMessages == nil {
		return nil
	}
	return *c.turnMessages
}

func (c *runnerTurnController) currentOpts() llama.ChatOptions {
	if c == nil || c.opts == nil {
		return llama.ChatOptions{}
	}
	return *c.opts
}

func (c *runnerTurnController) completeWithoutMoreTools() (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	return c.runner.completeWithoutMoreTools(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), c.metrics)
}

func (c *runnerTurnController) completeWithoutMoreToolsWithReason(prompt string) (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	return c.runner.completeWithoutMoreToolsWithReason(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), prompt, c.metrics)
}
