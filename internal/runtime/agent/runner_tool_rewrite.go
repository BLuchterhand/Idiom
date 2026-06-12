package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
)

// rewriteToolRequest records a tool-request rewrite and returns the redirected
// request/raw pair. Call sites still assign req/raw explicitly so loop-state
// mutation stays visible in Runner.Run.
func (r *Runner) rewriteToolRequest(
	messages *[]llama.ChatMessage,
	original workspace.ToolRequest,
	originalRaw string,
	redirected workspace.ToolRequest,
	redirectedRaw string,
	reason string,
) (workspace.ToolRequest, string) {
	r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
		"agent":          r.agentName(),
		"original_tool":  strings.TrimSpace(original.Tool),
		"rewritten_tool": strings.TrimSpace(redirected.Tool),
		"original_raw":   strings.TrimSpace(originalRaw),
		"rewritten_raw":  strings.TrimSpace(redirectedRaw),
		"reason":         reason,
	}))
	*messages = append(*messages, llama.ChatMessage{
		Role:    "system",
		Content: rewriteVisibilityNote(original.Tool, redirected.Tool),
	})
	return redirected, redirectedRaw
}
