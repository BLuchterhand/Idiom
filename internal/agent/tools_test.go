package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestParseToolCall_BuiltinTypedFields(t *testing.T) {
	rawArgs := `{"pattern":"foo","path":"src","glob":"*.go"}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: rawArgs,
		},
	}
	req, ok, err := parseToolCall(call)
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if req.Tool != "search_content" {
		t.Fatalf("Tool = %q", req.Tool)
	}
	if req.Pattern != "foo" || req.Path != "src" || req.Glob != "*.go" {
		t.Fatalf("typed fields = pattern=%q path=%q glob=%q", req.Pattern, req.Path, req.Glob)
	}
	// Args is also preserved verbatim for non-MCP tools so it can feed the
	// dedup key (toolRequestKey) — external tools have empty typed fields
	// and need Args to distinguish materially-different calls. Built-ins
	// don't need it for dedup (their typed fields differ), but populating
	// uniformly keeps the contract simple.
	if string(req.Args) != rawArgs {
		t.Fatalf("expected Args to be raw arguments verbatim, got %q", string(req.Args))
	}
}

func TestParseToolCall_BuiltinRepairsNumericStringFields(t *testing.T) {
	rawArgs := `{"path":"README.md","start_line":"10","end_line":"12","content":"updated\n","description":"update range"}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "edit_file",
			Arguments: rawArgs,
		},
	}
	req, ok, err := parseToolCall(call)
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if req.Tool != "edit_file" || req.StartLine != 10 || req.EndLine != 12 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if string(req.Args) != rawArgs {
		t.Fatalf("Args should preserve raw model arguments, got %q", string(req.Args))
	}
}

func TestParseToolCall_BuiltinRepairsStringifiedMultilineEdits(t *testing.T) {
	inner := `[{"path":"a.go","old_text":"line one
line two","new_text":"line one
line three","purpose":"update a"}]`
	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rawArgs := string(payload)
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "submit_plan",
			Arguments: rawArgs,
		},
	}
	req, ok, err := parseToolCall(call)
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if !ok {
		t.Fatal("expected parsed tool call")
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "a.go" {
		t.Fatalf("expected stringified submit_plan edits to repair into typed edits, got %#v", req.Edits)
	}
}

func TestParseToolCall_MCPRoutesArgsRaw(t *testing.T) {
	rawArgs := `{"query":"go 1.24 release notes","numResults":5,"livecrawl":"preferred","customField":42}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "mcp:exa/web_search_exa",
			Arguments: rawArgs,
		},
	}
	req, ok, err := parseToolCall(call)
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if req.Tool != "mcp:exa/web_search_exa" {
		t.Fatalf("Tool = %q", req.Tool)
	}
	if string(req.Args) != rawArgs {
		t.Fatalf("Args = %q, want byte-for-byte %q", string(req.Args), rawArgs)
	}
	// The typed fields should be untouched even though `query` exists on
	// both ToolRequest and the model's MCP arg JSON. We never tried to
	// unmarshal into them.
	if req.Query != "" {
		t.Fatalf("Query field unexpectedly populated: %q (should be empty for mcp:* calls)", req.Query)
	}
	// And Args is still valid JSON.
	var probe map[string]any
	if err := json.Unmarshal(req.Args, &probe); err != nil {
		t.Fatalf("Args is not valid JSON: %v", err)
	}
	if probe["customField"] != float64(42) {
		t.Fatalf("expected customField preserved, got %v", probe["customField"])
	}
}

func TestParseToolCall_MCPEmptyArgsBecomesEmptyObject(t *testing.T) {
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "mcp:fetch/fetch",
			Arguments: "",
		},
	}
	req, ok, err := parseToolCall(call)
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if string(req.Args) != "{}" {
		t.Fatalf("Args = %q, want empty-object {}", string(req.Args))
	}
}

func TestParseToolCall_EmptyNameReturnsNotOk(t *testing.T) {
	_, ok, err := parseToolCall(llama.ToolCall{})
	if err != nil {
		t.Fatalf("parseToolCall: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for empty name")
	}
}

func TestParseToolCall_BuiltinBadJSONReturnsError(t *testing.T) {
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: `{not json}`,
		},
	}
	_, _, err := parseToolCall(call)
	if err == nil {
		t.Fatalf("expected JSON error")
	}
}

// TestLanePolicyClassifiersIgnoreMCP pins the lane-policy classifiers
// against `mcp:*` calls. Both helpers are intentionally hard-coded
// allow-lists keyed on built-in tool names, so MCP tools never:
//
//   - count as verification (so the verification-only lane keeps
//     redirecting to a real verification command instead of letting
//     web_search short-circuit it), nor
//   - count as post-readiness "gap" tools (so we never ask the model
//     to attach `named_gap` / `why_not_answering` to an MCP call —
//     external retrieval is not workspace narrowing and the contract
//     would be nonsensical).
//
// A future refactor that switches either function to a prefix check
// would silently regress this; this test catches that.
func TestLanePolicyClassifiersIgnoreMCP(t *testing.T) {
	if isPostReadinessGapTool("mcp:exa/web_search_exa") {
		t.Errorf("isPostReadinessGapTool(mcp:*) = true, want false")
	}
	if isPostReadinessGapTool("mcp:fetch/fetch") {
		t.Errorf("isPostReadinessGapTool(mcp:fetch/fetch) = true, want false")
	}
}

// TestToolResultMessageSyntheticIDForEmptyCallID verifies that
// toolResultMessage generates a synthetic tool_call_id when the
// call's ID is empty, so strict-protocol backends (DeepSeek) see a
// proper role:"tool" message with tool_call_id.
func TestToolResultMessageSyntheticIDForEmptyCallID(t *testing.T) {
	call := &llama.ToolCall{
		ID:   "", // empty — simulates textual-tool fallback
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: `{"pattern":"test"}`,
		},
	}
	result := workspace.ToolResult{
		Tool:    "search_content",
		Summary: "Rejected by retry budget",
		Error:   "test rejection",
	}

	msg := toolResultMessage(call, result)

	if msg.Role != "tool" {
		t.Fatalf("expected role=tool for non-nil call even with empty ID, got role=%q", msg.Role)
	}
	if msg.ToolCallID == "" {
		t.Fatal("expected non-empty ToolCallID (synthetic), got empty")
	}
	if !strings.Contains(msg.ToolCallID, "rejected_search_content_") {
		t.Fatalf("expected synthetic ID to contain 'rejected_search_content_', got %q", msg.ToolCallID)
	}
	if msg.Name != "search_content" {
		t.Fatalf("expected Name=search_content, got %q", msg.Name)
	}
	t.Logf("synthetic ID: %s", msg.ToolCallID)
}

// TestToolResultMessageHonorsRealCallID verifies that when a real
// call ID is present, it is used directly (no synthetic generation).
func TestToolResultMessageHonorsRealCallID(t *testing.T) {
	call := &llama.ToolCall{
		ID:   "call_abc123",
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "read_file",
			Arguments: `{"path":"main.go"}`,
		},
	}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read main.go",
	}

	msg := toolResultMessage(call, result)

	if msg.Role != "tool" {
		t.Fatalf("expected role=tool, got %q", msg.Role)
	}
	if msg.ToolCallID != "call_abc123" {
		t.Fatalf("expected ToolCallID=call_abc123, got %q", msg.ToolCallID)
	}
}

// TestToolResultMessageNilCallReturnsSystem verifies that when call
// is nil, the legacy system-role fallback is preserved.
func TestToolResultMessageNilCallReturnsSystem(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read main.go",
	}

	msg := toolResultMessage(nil, result)

	if msg.Role != "system" {
		t.Fatalf("expected role=system for nil call, got %q", msg.Role)
	}
}
