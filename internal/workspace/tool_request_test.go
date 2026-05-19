package workspace

import (
	"strings"
	"testing"
)

func TestParseToolRequestExtractsFirstJSONObjectFromToolFence(t *testing.T) {
	req, raw, ok, err := ParseToolRequest("before\n```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```\nafter")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "read_file" || req.Path != "README.md" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if raw != `{"tool":"read_file","path":"README.md"}` {
		t.Fatalf("unexpected raw payload: %q", raw)
	}
}

func TestParseToolRequestAcceptsPlainToolPrefixJSONObject(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "newline", text: "tool\n{\"tool\":\"context_pack\",\"task\":\"explain debug logs\",\"mode\":\"plan\",\"limit\":8}"},
		{name: "adjacent", text: "tool{\"tool\":\"context_pack\",\"task\":\"explain subagents\",\"mode\":\"plan\",\"limit\":10}"},
		{name: "colon", text: "tool: {\"tool\":\"read_file\",\"path\":\"README.md\"}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, raw, ok, err := ParseToolRequest(tc.text)
			if err != nil {
				t.Fatalf("ParseToolRequest: %v", err)
			}
			if !ok {
				t.Fatal("expected tool request")
			}
			if req.Tool == "" {
				t.Fatalf("missing parsed tool: %#v raw=%q", req, raw)
			}
			if !strings.HasPrefix(raw, `{"tool":`) {
				t.Fatalf("expected JSON object payload, got %q", raw)
			}
		})
	}
}

func TestParseToolRequestDoesNotTreatToolProseAsRequest(t *testing.T) {
	_, _, ok, err := ParseToolRequest("tool usage should stay prose unless followed by a JSON object")
	if err != nil || ok {
		t.Fatalf("expected prose to remain a final answer, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestPreservesPostReadinessGapFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"pattern\":\"runner\",\"path\":\"internal\",\"named_gap\":\"find the exact stop-condition branch\",\"why_not_answering\":\"I still need the deciding condition\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.NamedGap != "find the exact stop-condition branch" {
		t.Fatalf("unexpected named gap: %#v", req)
	}
	if req.WhyNotAnswering != "I still need the deciding condition" {
		t.Fatalf("unexpected why_not_answering: %#v", req)
	}
}

func TestParseToolRequestTreatsSymbolAsPatternAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"go_read_symbol\",\"path\":\"internal/agent/runner.go\",\"symbol\":\"Runner.Run\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Pattern != "Runner.Run" {
		t.Fatalf("pattern = %q, want symbol alias", req.Pattern)
	}
}

func TestParseToolRequestAcceptsQwenXMLFunctionEnvelope(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`<tool_call>
<function=submit_plan>
<parameter=rationale>
Add the required policy gate.
</parameter>
<parameter=edits>
[{"path":"scripts/affected_suites.py","start_line":"10","end_line":"12","content":"new","purpose":"update inventory"}]
</parameter>
</function>
</tool_call>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "submit_plan" || req.Rationale != "Add the required policy gate." {
		t.Fatalf("unexpected request: %#v", req)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "scripts/affected_suites.py" || req.Edits[0].StartLine != 10 || req.Edits[0].EndLine != 12 {
		t.Fatalf("unexpected edits: %#v", req.Edits)
	}
	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenJSONToolCallEnvelope(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}
</tool_call>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "read_file" || req.Path != "README.md" || req.StartLine != 1 || req.EndLine != 20 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if raw != `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}` {
		t.Fatalf("unexpected raw payload: %q", raw)
	}
}

func TestParseToolRequestAcceptsUnterminatedQwenJSONToolCallEnvelope(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "read_file" || req.Path != "README.md" || req.StartLine != 1 || req.EndLine != 20 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if raw != `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}` {
		t.Fatalf("unexpected raw payload: %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenJSONNameArgumentsEnvelope(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`<tool_call>
{"name":"submit_plan","arguments":{"rationale":"Update target.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace target"}]}}
</tool_call>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "submit_plan" || req.Rationale != "Update target." {
		t.Fatalf("unexpected request: %#v", req)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "target.txt" || req.Edits[0].OldText != "old\n" || req.Edits[0].NewText != "new\n" {
		t.Fatalf("unexpected edits: %#v", req.Edits)
	}
	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestIgnoresProseQwenXMLFunctionReference(t *testing.T) {
	_, _, ok, err := ParseToolRequest("Use `<function=submit_plan>` only inside a tool envelope.")
	if err != nil || ok {
		t.Fatalf("expected prose reference to remain a final answer, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestReportsMalformedQwenXMLObjectParameter(t *testing.T) {
	_, _, ok, err := ParseToolRequest(`<function=submit_plan>
<parameter=edits>
[{"path":"a.py","old_text":"def f():
    pass"}]
</parameter>
</function>`)
	if !ok || err == nil || !strings.Contains(err.Error(), "edits") {
		t.Fatalf("expected malformed edits error, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestRejectsMalformedOrMissingTool(t *testing.T) {
	_, _, ok, err := ParseToolRequest("no tool here")
	if err != nil || ok {
		t.Fatalf("expected no request, ok=%t err=%v", ok, err)
	}
	_, _, ok, err = ParseToolRequest("```tool\n{\"path\":\"README.md\"}\n```")
	if !ok || err == nil || !strings.Contains(err.Error(), "missing tool") {
		t.Fatalf("expected missing tool error, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestRepairsSafeMissingToolAliases(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "go outline path only",
			text: "```tool\n{\"path\":\"internal/app/analysis_agents.go\"}\n```",
			want: "go_file_outline",
		},
		{
			name: "move declaration tag",
			text: "```tool\n{\"path\":\"internal/app/app.go\",\"destination\":\"internal/app/rendering.go\",\"pattern\":\"tag:rendering\"}\n```",
			want: "go_move_declarations",
		},
		{
			name: "shell command",
			text: "```tool\n{\"command\":\"go test ./internal/app\"}\n```",
			want: "shell",
		},
		{
			name: "child task",
			text: "```tool\n{\"agent\":\"explorer\",\"prompt\":\"inspect routing\"}\n```",
			want: "task",
		},
		{
			name: "line edit",
			text: "```tool\n{\"path\":\"internal/app/app.go\",\"start_line\":10,\"end_line\":10,\"content\":\"replacement\"}\n```",
			want: "edit_file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _, ok, err := ParseToolRequest(tc.text)
			if err != nil {
				t.Fatalf("ParseToolRequest: %v", err)
			}
			if !ok {
				t.Fatal("expected tool request")
			}
			if req.Tool != tc.want {
				t.Fatalf("tool = %q, want %q", req.Tool, tc.want)
			}
		})
	}
}

func TestParseToolRequestRepairsNumericStringFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":\"10\",\"end_line\":\"12\",\"content\":\"updated\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.StartLine != 10 || req.EndLine != 12 {
		t.Fatalf("line range = %d-%d, want 10-12", req.StartLine, req.EndLine)
	}
}

func TestParseToolRequestRepairsStringifiedStructuredFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"submit_plan\",\"rationale\":\"plan\",\"edits\":\"[{\\\"path\\\":\\\"a.py\\\",\\\"start_line\\\":\\\"10\\\",\\\"end_line\\\":\\\"12\\\",\\\"content\\\":\\\"updated\\\\n\\\",\\\"purpose\\\":\\\"update a\\\"}]\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "submit_plan" || len(req.Edits) != 1 {
		t.Fatalf("unexpected request: %#v", req)
	}
	edit := req.Edits[0]
	if edit.Path != "a.py" || edit.StartLine != 10 || edit.EndLine != 12 || edit.Content != "updated\n" {
		t.Fatalf("unexpected repaired edit: %#v", edit)
	}
}

func TestParseToolRequestRejectsUnsafeNumericStringFields(t *testing.T) {
	_, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":\"ten\"}\n```")
	if !ok || err == nil || !strings.Contains(err.Error(), "cannot unmarshal string") {
		t.Fatalf("expected original decode error for unsafe numeric string, ok=%t err=%v", ok, err)
	}
}

func TestExploreShellClassificationAndValidation(t *testing.T) {
	if !IsBroadInspectionRequest(ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"}) {
		t.Fatal("expected find shell command to be broad inspection")
	}
	if !IsNarrowingRequest(ToolRequest{Tool: "shell", Command: "rg TODO internal"}) {
		t.Fatal("expected rg shell command to be narrowing")
	}
	if err := validateExploreShellCommand("git status --short"); err != nil {
		t.Fatalf("expected git status to be allowed: %v", err)
	}
	for _, command := range []string{"git checkout main", "python3 script.py", "rg TODO > out.txt", "cat $(pwd)"} {
		if err := validateExploreShellCommand(command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
	}
}

func TestShouldClearBroadInspectionLockRequiresRelevantSuccessfulNarrowing(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())
	if !mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content", Pattern: "route"}, ToolResult{Summary: "ok"}) {
		t.Fatal("search_content should clear broad inspection lock")
	}
	if !mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}, ToolResult{Summary: "ok"}) {
		t.Fatal("scoped list_files should clear broad inspection lock")
	}
	if mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content"}, ToolResult{Error: "failed"}) {
		t.Fatal("failed tool result should not clear broad inspection lock")
	}
	if mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files"}, ToolResult{Summary: "ok"}) {
		t.Fatal("broad inspection should not clear broad inspection lock")
	}
}

func TestScopedListFilesIsNarrowingNotBroadInspection(t *testing.T) {
	if !IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "."}) {
		t.Fatal("root list_files should be broad inspection")
	}
	if IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}) {
		t.Fatal("scoped list_files should not be broad inspection")
	}
	if !IsNarrowingRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}) {
		t.Fatal("scoped list_files should be a narrowing request")
	}
}
