package agent

import (
	"encoding/json"
	"strings"

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

// postReadinessRequiredFields lists the JSON Schema properties that become
// required on read-class tool calls once the lane has reached answer
// readiness or received a child packet. Schema-level enforcement is the
// load-bearing piece: the model's tool-call decoder rejects calls that
// omit a required field, so the failure mode "model knows the contract,
// still emits a no-gap call" closes at the protocol layer instead of
// being chased through prose rejections.
var postReadinessRequiredFields = []string{"named_gap", "why_not_answering"}

// applyPostReadinessSchemaRequirements rewrites the JSON Schema of every
// read-class tool in tools to mark `named_gap` and `why_not_answering` as
// required and to ensure both properties are declared. Generic across
// tools — anything that is_read_class returns true for picks up the same
// schema constraint. Non-read-class tools are returned unchanged.
//
// For `read_file`, also require a concrete path and line range. After
// readiness, an unanchored or path-wide file read is nearly always an
// inefficient substitute for either answering or choosing a symbol/range probe,
// so push that choice into the tool contract instead of spending a model turn
// on a runtime rejection.
//
// Called from the runner's rebuildTools when delegatedReadOnlyAnswerReady
// or postChildPacketActive is true. Idempotent: calling twice in a row
// produces the same schema.
func applyPostReadinessSchemaRequirements(tools []llama.Tool) []llama.Tool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]llama.Tool, len(tools))
	for i, tool := range tools {
		if !isPostReadinessGapTool(tool.Function.Name) {
			out[i] = tool
			continue
		}
		mutated, ok := mutateSchemaRequireFields(tool.Function.Parameters, postReadinessRequiredFields)
		if !ok {
			out[i] = tool
			continue
		}
		if tool.Function.Name == "read_file" {
			if narrowed, narrowOK := mutateSchemaRequireLineRange(mutated); narrowOK {
				mutated = narrowed
			}
		} else if isPostReadinessLineRangeTool(tool.Function.Name) {
			if exposed, exposeOK := mutateSchemaExposeLineRange(mutated); exposeOK {
				mutated = exposed
			}
		}
		tool.Function.Parameters = mutated
		out[i] = tool
	}
	return out
}

func mutateSchemaExposeLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}
	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "optional 1-indexed line number for an exact follow-up range after readiness",
			}
		}
		entry["minimum"] = 1
		props[name] = entry
	}
	schema["properties"] = props
	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(updated), true
}

func mutateSchemaRequireLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}
	if _, ok := props["path"]; !ok {
		props["path"] = map[string]any{
			"type":        "string",
			"description": "repository-relative file path; required after readiness so read_file has a concrete target",
		}
	}
	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "1-indexed line number; required after readiness so read_file remains a narrow probe",
			}
		}
		entry["minimum"] = 1
		props[name] = entry
	}
	schema["properties"] = props

	requiredSet := make(map[string]struct{})
	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}
	for _, name := range []string{"path", "start_line", "end_line"} {
		requiredSet[name] = struct{}{}
	}
	required := make([]string, 0, len(requiredSet))
	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}
	for _, name := range []string{"path", "start_line", "end_line"} {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}
	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(updated), true
}

// isPostReadinessGapTool reports whether a tool name is read-class — i.e.
// the kind of call that, after readiness or a child packet, must carry a
// named_gap. Mirrors workspace.IsReadClassRequest's tool list at the name
// level so we don't need to reconstruct ToolRequest shapes here.
func isPostReadinessGapTool(name string) bool {
	switch name {
	case "read_file", "search_content", "search_files", "list_files",
		"discovery_query", "structural_search", "ctags_symbols",
		"go_read_symbol", "go_file_outline", "go_extract_body",
		"go_workspace_symbol", "go_references", "go_definition",
		"go_calls_from", "go_calls_into", "go_related_files",
		"cpp_read_symbol", "cpp_file_outline", "cpp_extract_body",
		"cpp_workspace_symbol", "cpp_references", "cpp_definition",
		"cpp_calls_from", "cpp_calls_into", "cpp_related_files",
		"python_read_symbol", "python_file_outline",
		"python_workspace_symbol", "python_references", "python_definition",
		"python_calls_from", "python_calls_into", "python_related_files",
		"ts_read_symbol", "ts_file_outline",
		"ts_workspace_symbol", "ts_references", "ts_definition",
		"ts_calls_from", "ts_calls_into", "ts_related_files",
		"rust_read_symbol", "rust_file_outline",
		"rust_workspace_symbol", "rust_references", "rust_definition",
		"rust_calls_from", "rust_calls_into", "rust_related_files",
		"java_read_symbol", "java_file_outline",
		"java_workspace_symbol", "java_references", "java_definition",
		"java_calls_from", "java_calls_into", "java_related_files",
		"dependency_graph", "history_range", "history_symbol", "blame_range",
		"semgrep_scan", "joern_scan":
		return true
	default:
		return false
	}
}

func isPostReadinessLineRangeTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, "_read_symbol") || strings.HasSuffix(name, "_extract_body")
}

// mutateSchemaRequireFields takes a raw JSON Schema (object) and adds the
// given names to its `required` array and `properties` map. Returns the
// new raw schema and ok=true. Returns ok=false when the input is not an
// object schema we can sensibly extend.
func mutateSchemaRequireFields(raw json.RawMessage, fields []string) (json.RawMessage, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}
	if t, _ := schema["type"].(string); t != "" && t != "object" {
		return nil, false
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}
	for _, name := range fields {
		if _, exists := props[name]; !exists {
			props[name] = map[string]any{
				"type":        "string",
				"description": postReadinessFieldDescription(name),
			}
		}
	}
	schema["properties"] = props

	requiredSet := make(map[string]struct{})
	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}
	for _, name := range fields {
		requiredSet[name] = struct{}{}
	}
	required := make([]string, 0, len(requiredSet))
	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}
	for _, name := range fields {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}
	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(updated), true
}

func postReadinessFieldDescription(name string) string {
	switch name {
	case "named_gap":
		return "after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; reference a specific symbol, file path, or narrow line range"
	case "why_not_answering":
		return "after answer-readiness or a child packet, state what would be materially wrong or missing if you answered now"
	default:
		return ""
	}
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
// for built-in tools the arguments JSON decodes directly into
// ToolRequest's field set (they overlap by design). For
// `mcp:<server>/<tool>` calls the raw JSON is preserved verbatim in
// ToolRequest.Args — MCP tools have arbitrary JSON-Schema parameters
// that do not map onto this struct's typed fields, and forcing them
// through the typed unmarshaller would silently drop fields. Returns
// ok=false when the call has no function name.
func parseToolCall(call llama.ToolCall) (workspace.ToolRequest, bool, error) {
	if call.Function.Name == "" {
		return workspace.ToolRequest{}, false, nil
	}
	req := workspace.ToolRequest{Tool: call.Function.Name}
	if strings.HasPrefix(call.Function.Name, "mcp:") {
		if call.Function.Arguments != "" {
			req.Args = json.RawMessage(call.Function.Arguments)
		} else {
			req.Args = json.RawMessage(`{}`)
		}
		return req, true, nil
	}
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &req); err != nil {
			return workspace.ToolRequest{}, false, err
		}
		req.Args = json.RawMessage(call.Function.Arguments)
	} else {
		req.Args = json.RawMessage(`{}`)
	}
	req.Tool = call.Function.Name
	return req, true, nil
}
