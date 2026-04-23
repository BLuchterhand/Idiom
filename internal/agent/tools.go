package agent

import (
	"encoding/json"

	"idiom/internal/llama"
	"idiom/internal/workspace"
)

// buildLlamaTools converts workspace tool definitions into the shape the
// llama client sends to the server — i.e. OpenAI function-calling specs.
// Called once per turn when the runner sets up ChatOptions, so the server
// (llama.cpp with --jinja + a Qwen tool-call template) can route the
// model's tool_calls through the native channel.
func buildLlamaTools(defs []workspace.ToolDefinition) []llama.Tool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]llama.Tool, 0, len(defs))
	for _, def := range defs {
		params := json.RawMessage(def.ParametersJSON)
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, llama.Tool{
			Type: "function",
			Function: llama.ToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

// toolResultMessage builds the message we append to turnMessages after
// a tool executes. When the call came in via native tool_calls (call is
// non-nil), we return a role:"tool" message linked by tool_call_id —
// OpenAI's documented shape, which Qwen was trained on. If the call came
// from the legacy fenced-block fallback path (call == nil), we fall back
// to a role:"system" message rendered by the workspace helper so the
// model can still parse it. The llama.cpp jinja Qwen template treats
// role:"tool" as the authoritative channel; the fallback is only there
// to keep headless/test paths working if someone forgets --jinja.
func toolResultMessage(call *llama.ToolCall, result workspace.ToolResult) llama.ChatMessage {
	if call != nil && call.ID != "" {
		return llama.ChatMessage{
			Role:       "tool",
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Content:    workspace.FormatToolResultForModel(result),
		}
	}
	return llama.ChatMessage{
		Role:    "system",
		Content: workspace.FormatToolResultForModel(result),
	}
}

// toolResultTouchedPaths returns the set of workspace paths a tool call
// affected (or tried to read), so the InstructionAttacher can walk up
// from those directories and surface nearby AGENTS.md / CLAUDE.md once.
// Kept intentionally shallow: we only care about tools that name a
// single file or a patch envelope.
func toolResultTouchedPaths(req workspace.ToolRequest, _ workspace.ToolResult) []string {
	switch req.Tool {
	case "read_file", "edit_file", "write_file", "append_file", "go_read_symbol", "go_extract_body", "go_diagnostics", "go_file_outline", "cpp_read_symbol", "cpp_extract_body", "cpp_diagnostics":
		if req.Path != "" {
			return []string{req.Path}
		}
	case "apply_patch":
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}
		return workspace.PatchOpPaths(ops)
	}
	return nil
}

// parseToolCall unmarshals the arguments JSON the model produced in a
// ToolCall into a typed ToolRequest. The function name is the tool;
// arguments JSON decodes directly into ToolRequest's field set (they
// overlap by design). Returns ok=false for unsupported tool names so the
// runner can surface an error back to the model.
func parseToolCall(call llama.ToolCall) (workspace.ToolRequest, bool, error) {
	if call.Function.Name == "" {
		return workspace.ToolRequest{}, false, nil
	}
	req := workspace.ToolRequest{Tool: call.Function.Name}
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &req); err != nil {
			return workspace.ToolRequest{}, false, err
		}
	}
	req.Tool = call.Function.Name
	return req, true, nil
}
