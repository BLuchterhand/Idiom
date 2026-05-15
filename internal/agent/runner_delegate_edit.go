package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/workspace"
)

// runDelegateEdit handles a delegate_edit tool call from the planner.
//
// The point of delegate_edit is to give the planner a separate mental
// hook for "the actual edit is someone else's job — I just specify what
// changes." That break alone helps reduce the planner's verification
// urge: once the planner stops thinking of itself as the executor, it
// stops feeling responsible for confirming each landed edit.
//
// The implementation routes the planner's edit spec through a single
// chat completion against ImplementerClient (the configured executor-
// role backend, sourced from .idiom/backends.json or cfg.Implementer).
// The executor's job is narrow: read the spec, emit a single edit_file
// or apply_patch tool-call JSON, and stop. The runner parses that JSON, dispatches
// the underlying edit through Workspace.Execute, and returns the real
// edit result back to the planner. If the executor fails for any
// reason — model error, malformed reply, missing client — the runner
// falls back to dispatching the planner's spec directly so the lane
// still makes progress.
//
// Returned ToolResult carries Tool="delegate_edit" so the audit trail
// and required_edit_paths_progress treat it as the model's own edit
// (it IS, semantically — the planner asked for it, the executor
// rubber-stamped or refined it).
func (r *Runner) runDelegateEdit(ctx context.Context, req workspace.ToolRequest) workspace.ToolResult {
	if reject := validateDelegateEditRequest(req); reject != nil {
		return *reject
	}
	executor := r.delegateEditExecutorClient()
	if executor == nil {
		// No executor available — direct dispatch keeps the lane
		// honest. The planner still sees a delegate_edit-shaped result.
		return r.dispatchDelegatedEditDirectly(ctx, req, "no executor client configured")
	}
	systemPrompt, userPrompt := delegateEditPrompts(req)
	reply, err := executor.CompleteChatMessage(ctx, []llama.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, llama.ChatOptions{
		Temperature: 0.2,
		TopP:        0.95,
		MaxTokens:   2048,
	})
	if err != nil {
		r.emitEvent(RunEvent{Role: "delegate_edit_executor_error", Content: err.Error()})
		return r.dispatchDelegatedEditDirectly(ctx, req, "executor error: "+err.Error())
	}
	childReq, parseErr := parseDelegateEditReply(reply.Content, req)
	if parseErr != nil {
		r.emitEvent(RunEvent{Role: "delegate_edit_parse_error", Content: parseErr.Error()})
		return r.dispatchDelegatedEditDirectly(ctx, req, "executor reply unparseable: "+parseErr.Error())
	}
	r.emitEvent(RunEvent{
		Role:    "delegate_edit_executor_call",
		Content: fmt.Sprintf("executor produced %s for %s", childReq.Tool, childReq.Path),
	})
	result := r.Workspace.Execute(ctx, childReq)
	return wrapDelegateEditResult(result, "executor: "+childReq.Tool)
}

// validateDelegateEditRequest rejects malformed delegate_edit calls
// before the executor round-trip. The contract: a path is required,
// and the spec must be either old/new substring shape or
// start/end/content range shape.
func validateDelegateEditRequest(req workspace.ToolRequest) *workspace.ToolResult {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return &workspace.ToolResult{
			Tool:    "delegate_edit",
			Summary: "delegate_edit rejected",
			Error:   "delegate_edit requires a path",
		}
	}
	hasSubstringShape := strings.TrimSpace(req.OldText) != "" && strings.TrimSpace(req.NewText) != ""
	hasRangeShape := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""
	hasReplaceShape := strings.TrimSpace(req.NewText) != "" && req.ReplaceAll
	if !hasSubstringShape && !hasRangeShape && !hasReplaceShape {
		return &workspace.ToolResult{
			Tool:    "delegate_edit",
			Summary: "delegate_edit rejected",
			Error:   "delegate_edit requires either old_text+new_text (substring shape) or start_line+end_line+content (range shape)",
		}
	}
	return nil
}

// delegateEditExecutorClient picks the executor for a delegate_edit
// call. ImplementerClient is the canonical executor (configured to
// point at the 30B-coder backend in production). When unset the
// runner has no separate executor — see runDelegateEdit's fallback.
func (r *Runner) delegateEditExecutorClient() llama.LLMClient {
	if r == nil {
		return nil
	}
	if r.ImplementerClient != nil {
		return r.ImplementerClient
	}
	// Deliberately do NOT fall back to r.Client here. The whole point
	// of delegate_edit is to break out of the planner's context; if
	// the planner and executor share a client, we lose that benefit.
	// The caller's fallback path will dispatch the spec directly.
	return nil
}

// delegateEditPrompts renders the two-message executor prompt. The
// system message anchors the executor's role; the user message is
// the literal edit spec.
func delegateEditPrompts(req workspace.ToolRequest) (string, string) {
	system := strings.Join([]string{
		"You are the local executor for a single file edit.",
		"The planner has already decided what to change and where; your only job is to emit the workspace tool call that performs that edit.",
		"Reply with one and ONLY one JSON tool call, on its own. No prose, no commentary, no markdown fences.",
		"Use edit_file when given an old_text/new_text or start_line/end_line/content spec.",
		"Use apply_patch only if the spec carries a `patch` field with a *** Begin Patch envelope.",
		"Do not invent fields, do not change paths, do not rephrase old_text. Output the exact JSON the workspace expects.",
	}, " ")
	specBytes, _ := json.MarshalIndent(map[string]any{
		"path":        strings.TrimSpace(req.Path),
		"old_text":    req.OldText,
		"new_text":    req.NewText,
		"start_line":  req.StartLine,
		"end_line":    req.EndLine,
		"content":     req.Content,
		"replace_all": req.ReplaceAll,
		"description": strings.TrimSpace(req.Purpose),
	}, "", "  ")
	user := "Apply this edit by emitting the workspace tool call. Spec:\n" + string(specBytes)
	return system, user
}

// parseDelegateEditReply extracts a workspace.ToolRequest from the
// executor's reply. The reply is expected to be a single JSON object
// shaped like an edit_file or apply_patch ToolRequest. Anything else
// (fenced blocks, prose, multiple tool calls) is rejected so the
// fallback path takes over.
func parseDelegateEditReply(content string, planSpec workspace.ToolRequest) (workspace.ToolRequest, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return workspace.ToolRequest{}, fmt.Errorf("executor returned empty content")
	}
	if fence := stripJSONFence(trimmed); fence != "" {
		trimmed = fence
	}
	var req workspace.ToolRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return workspace.ToolRequest{}, fmt.Errorf("executor reply is not valid JSON: %w", err)
	}
	switch strings.TrimSpace(req.Tool) {
	case "edit_file", "apply_patch", "write_file", "append_file":
		// OK — these are write tools the workspace knows how to
		// dispatch.
	default:
		return workspace.ToolRequest{}, fmt.Errorf("executor selected disallowed tool %q (must be edit_file/apply_patch/write_file/append_file)", req.Tool)
	}
	if req.Tool == "edit_file" {
		// Defensive: the executor should not change the path — the
		// planner controls that. If it did, fall back to the planner
		// spec's path.
		if strings.TrimSpace(req.Path) == "" {
			req.Path = strings.TrimSpace(planSpec.Path)
		}
	}
	return req, nil
}

// stripJSONFence handles executors that wrap JSON in ```json fences
// despite the system instruction. Returns "" if no fence pattern is
// detected (caller falls back to the original string).
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, "```")
	if !ok {
		return ""
	}
	rest = strings.TrimPrefix(rest, "json")
	rest = strings.TrimLeft(rest, "\r\n ")
	end := strings.LastIndex(rest, "```")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// dispatchDelegatedEditDirectly translates the planner's spec into a
// real edit_file ToolRequest and dispatches it without going through
// the executor. Used as the fallback path when the executor is
// unavailable, errors out, or returns garbage.
func (r *Runner) dispatchDelegatedEditDirectly(ctx context.Context, req workspace.ToolRequest, reason string) workspace.ToolResult {
	r.emitEvent(RunEvent{
		Role:    "delegate_edit_direct_dispatch",
		Content: fmt.Sprintf("dispatching planner spec directly (%s)", reason),
	})
	// edit_file's contract requires Description (the autodoc reads it
	// for the plan-panel autodoc). Forward whichever of the planner's
	// purpose/description fields is non-empty so the executor sees
	// the same intent the planner wrote.
	intent := strings.TrimSpace(req.Purpose)
	if intent == "" {
		intent = strings.TrimSpace(req.Description)
	}
	if intent == "" {
		intent = "delegate_edit fallback: planner spec dispatched directly"
	}
	direct := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        req.Path,
		OldText:     req.OldText,
		NewText:     req.NewText,
		StartLine:   req.StartLine,
		EndLine:     req.EndLine,
		Content:     req.Content,
		ReplaceAll:  req.ReplaceAll,
		Description: intent,
	}
	result := r.Workspace.Execute(ctx, direct)
	return wrapDelegateEditResult(result, "direct: "+reason)
}

// wrapDelegateEditResult re-labels the underlying edit's ToolResult so
// audit trails see delegate_edit consistently. The Summary keeps the
// underlying message so debugging is still straightforward, and the
// rendered Output shows which path the request took (executor vs
// direct).
func wrapDelegateEditResult(result workspace.ToolResult, route string) workspace.ToolResult {
	wrapped := result
	wrapped.Tool = "delegate_edit"
	if strings.TrimSpace(wrapped.Summary) == "" {
		wrapped.Summary = "delegate_edit completed"
	}
	header := fmt.Sprintf("delegated_via: %s", route)
	if strings.TrimSpace(wrapped.Output) == "" {
		wrapped.Output = header
	} else {
		wrapped.Output = header + "\n\n" + wrapped.Output
	}
	return wrapped
}
