package workspace

import (
	"encoding/json"
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

func TestParseToolRequestUnwrapsNestedRequestPayload(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"request\":\"{\\\"tool\\\":\\\"code_navigate\\\",\\\"capability\\\":\\\"read_symbol\\\",\\\"path\\\":\\\"cmd/idiom/main.go\\\",\\\"pattern\\\":\\\"Run\\\"}\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/main.go" || req.Pattern != "Run" {
		t.Fatalf("unexpected nested request repair: %#v", req)
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

func TestParseToolRequestRecoversEmbeddedQwenParametersInRationale(t *testing.T) {
	req, _, ok, err := ParseToolRequest(`{"tool":"submit_plan","rationale":"Add the required policy gate.\n<parameter=edits>\n[{\"path\":\"target.txt\",\"old_text\":\"old\",\"new_text\":\"new\",\"purpose\":\"replace target\"}]\n</parameter>"}`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "submit_plan" {
		t.Fatalf("unexpected tool: %q", req.Tool)
	}
	if req.Rationale != "Add the required policy gate." {
		t.Fatalf("rationale = %q", req.Rationale)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "target.txt" || req.Edits[0].OldText != "old" || req.Edits[0].NewText != "new" {
		t.Fatalf("embedded edits were not recovered: %#v", req.Edits)
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
    pass"}
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
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_pack\",\"task\":\"plan tests\",\"required_tests\":\"[\\\"go test ./cmd/idiom\\\"]\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "context_pack" || len(req.RequiredTests) != 1 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if req.RequiredTests[0] != "go test ./cmd/idiom" {
		t.Fatalf("unexpected repaired tests: %#v", req.RequiredTests)
	}
}

func TestDecodeToolRequestJSONRepairsNativeStringifiedSubmitPlanEdits(t *testing.T) {
	inner := `[{"path":"cmd/idiom/product_workflow.go","old_text":"func runProductTDD() {
	return 0
}","new_text":"func runProductTDD() {
	return 1
}","purpose":"replace the confirmed function body"}]`
	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("expected stringified submit_plan edits to repair into typed edits, got %#v", req.Edits)
	}
}

func TestDecodeToolRequestJSONRepairsStringifiedSubmitPlanEditsWithTrailingProse(t *testing.T) {
	inner := `[{"path":"cmd/idiom/product_workflow.go","start_line":10,"end_line":12,"content":"updated\n","purpose":"replace the confirmed line range"}]

This is only explanatory prose after the structured edit array.`
	payload, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "cmd/idiom/product_workflow.go" || req.Edits[0].Content != "updated\n" {
		t.Fatalf("expected trailing prose repair to preserve the edit array, got %#v", req.Edits)
	}
}

func TestDecodeToolRequestJSONRepairsStringifiedEmptySubmitPlanEdits(t *testing.T) {
	req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","rationale":"empty plan","edits":"[]"}`))
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should parse stringified empty edits and leave validation to submit_plan: %v", err)
	}
	if req.Tool != "submit_plan" {
		t.Fatalf("unexpected tool after repair: %#v", req)
	}
	if len(req.Edits) != 0 {
		t.Fatalf("expected empty repaired edits, got %#v", req.Edits)
	}
}

func TestDecodeToolRequestJSONRepairsStringifiedEmptyStructuredArrays(t *testing.T) {
	payload := []byte(`{"tool":"context_pack","task":"localize","evidence":"[]","required_tests":"[]","required_edit_paths":"[]","forbidden_paths":"[]","declarations":"[]","moves":"[]","todos":"[]","items":"[]"}`)
	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should repair stringified empty arrays: %v", err)
	}
	if req.Tool != "context_pack" || req.Task != "localize" {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
	if len(req.Evidence) != 0 ||
		len(req.RequiredTests) != 0 ||
		len(req.RequiredEditPaths) != 0 ||
		len(req.ForbiddenPaths) != 0 ||
		len(req.Declarations) != 0 ||
		len(req.Moves) != 0 ||
		len(req.Todos) != 0 ||
		len(req.Items) != 0 {
		t.Fatalf("expected all stringified empty arrays to decode as empty typed fields: %#v", req)
	}
}

func TestDecodeToolRequestJSONRejectsMalformedStringifiedSubmitPlanEdits(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits": `[{"path":"cmd/idiom/product_workflow.go","old_text":"fs := flag.NewFlagSet("idiom product tdd", flag.ContinueOnError)
","new_text":"fs := flag.NewFlagSet("idiom product tdd", flag.ContinueOnError)
// updated
","purpose":"replace the confirmed function body"}]`,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	_, err = DecodeToolRequestJSON(payload)
	if err == nil || !strings.Contains(err.Error(), "ToolRequest.edits") {
		t.Fatalf("expected malformed stringified edits to remain a decode error, got %v", err)
	}
}

func TestDecodeToolRequestJSONRejectsMalformedStringifiedSubmitPlanEditsWithoutDroppingTail(t *testing.T) {
	inner := `[{"path":"a.go","old_text":"func a() {
	return 1
}","new_text":"func a() {
	return 2
}","purpose":"replace a"},{"path":"b.go","old_text":"fmt.Println("badly escaped")
","new_text":"fmt.Println("still badly escaped")
","purpose":"replace b"}]`
	payload, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	_, err = DecodeToolRequestJSON(payload)
	if err == nil || !strings.Contains(err.Error(), "ToolRequest.edits") {
		t.Fatalf("expected malformed stringified edits to remain a decode error, got %v", err)
	}
}

func TestParseToolRequestRepairsStringifiedMultilineStructuredFields(t *testing.T) {
	inner := `["old line
second line"]`
	payload, err := json.Marshal(map[string]any{
		"tool":     "harness_feedback",
		"category": "bug",
		"evidence": inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, _, ok, err := ParseToolRequest("```tool\n" + string(payload) + "\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "harness_feedback" || len(req.Evidence) != 1 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if req.Evidence[0] != "old line\nsecond line" {
		t.Fatalf("unexpected repaired evidence: %#v", req.Evidence)
	}
}

func TestParseToolRequestRepairsBoolStringFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_query\",\"query\":\"what calls Run\",\"resolution\":\"symbol\",\"include_evidence\":\"true\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if !req.IncludeEvidence {
		t.Fatalf("include_evidence was not repaired: %#v", req)
	}
}

func TestParseToolRequestRepairsCombinedNumericAndBoolStringFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"read_file\",\"path\":\"a.py\",\"start_line\":\"10\",\"end_line\":\"12\",\"include_source\":\"true\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "read_file" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if req.Path != "a.py" || req.StartLine != 10 || req.EndLine != 12 || !req.IncludeSource {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
}

func TestParseToolRequestRepairsSingletonArrayFields(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"submit_plan\",\"rationale\":\"plan\",\"required_edit_paths\":\"a.py\",\"edits\":{\"path\":\"a.py\",\"old_text\":\"old\",\"new_text\":\"new\",\"purpose\":\"update a\"}}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if len(req.RequiredEditPaths) != 1 || req.RequiredEditPaths[0] != "a.py" {
		t.Fatalf("unexpected required edit paths: %#v", req.RequiredEditPaths)
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "a.py" {
		t.Fatalf("unexpected singleton edit repair: %#v", req.Edits)
	}
}

func TestParseToolRequestRepairsEmbeddedStringifiedSubmitPlanEdits(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"submit_plan\",\"rationale\":\"plan\",\"edits\":\"Here are the edits:\\n```json\\n[{\\\"path\\\":\\\"a.py\\\",\\\"old_text\\\":\\\"old\\\",\\\"new_text\\\":\\\"new\\\",\\\"purpose\\\":\\\"update a\\\"}]\\n```\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if len(req.Edits) != 1 || req.Edits[0].Path != "a.py" {
		t.Fatalf("expected embedded stringified edits to repair into typed edits, got %#v", req.Edits)
	}
}

func TestParseToolRequestRejectsUnsafeNumericStringFields(t *testing.T) {
	_, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":\"ten\"}\n```")
	if !ok || err == nil || !strings.Contains(err.Error(), "cannot unmarshal string") {
		t.Fatalf("expected original decode error for unsafe numeric string, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestRejectsUnsafeBoolStringFields(t *testing.T) {
	_, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_query\",\"query\":\"what calls Run\",\"include_evidence\":\"maybe\"}\n```")
	if !ok || err == nil || !strings.Contains(err.Error(), "cannot unmarshal string") {
		t.Fatalf("expected original decode error for unsafe bool string, ok=%t err=%v", ok, err)
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
