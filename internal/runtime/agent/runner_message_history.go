package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"strings"
)

func allowRecoverablePostReadinessRepairExecution(req workspace.ToolRequest, lastToolResult workspace.ToolResult) bool {
	if util.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) == "" {
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
	if strings.TrimSpace(reason) == "completion_gate_reply_preserved" {
		trace.ContentPreview = util.Truncate(strings.TrimSpace(message.Content), 500)
	}

	data, err := json.Marshal(trace)
	if err != nil {
		return
	}

	r.OnEvent(RunEvent{Role: "thinking_trace", Content: string(data)})
}

// stripOrphanedToolCalls returns a copy of messages with any assistant
// messages removed that have tool_calls but no matching role:"tool"
// responses. When the budget gate or post-readiness nudge continues
// the loop without executing a pending tool, the orphaned tool_calls
// must not reach the model — strict backends (DeepSeek) reject the
// request with "insufficient tool messages following tool_calls".
func stripOrphanedToolCalls(messages []llama.ChatMessage) []llama.ChatMessage {
	// Build lookup: tool_call_id -> has matching tool response
	hasToolResponse := make(map[string]bool)

	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			hasToolResponse[msg.ToolCallID] = true
		}
	}

	out := make([]llama.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			orphaned := false

			for _, tc := range msg.ToolCalls {
				if tc.ID != "" && !hasToolResponse[tc.ID] {
					orphaned = true
					break
				}
			}

			if orphaned {
				continue // drop this assistant message entirely
			}
		}

		out = append(out, msg)
	}

	return out
}

func chatMessagesForOptions(messages []llama.ChatMessage, opts llama.ChatOptions) []llama.ChatMessage {
	if opts.PreserveThinking {
		return messages
	}

	out := make([]llama.ChatMessage, len(messages))
	copy(out, messages)

	for i := range out {
		out[i].ReasoningContent = ""
	}

	return out
}

func assistantHistoryMessageForOptions(message llama.ChatMessage, opts llama.ChatOptions) llama.ChatMessage {
	if !opts.PreserveThinking {
		message.ReasoningContent = ""
	}

	return message
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

func retryableModelTransportError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}

	if text == "eof" || strings.HasSuffix(text, ": eof") {
		return true
	}

	for _, pattern := range []string{
		"transport failed",
		"connection reset",
		"connection refused",
		"deadline exceeded",
		"i/o timeout",
		"network is unreachable",
		"no such host",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}

	return false
}
