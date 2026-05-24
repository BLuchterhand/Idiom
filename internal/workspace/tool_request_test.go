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

func TestParseToolRequestAcceptsBareJSONWithSafeMissingToolAlias(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`{"path":"internal/example/target.go","start_line":1,"end_line":300}`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected bare JSON read request to be accepted")
	}
	if req.Tool != "read_file" || req.Path != "internal/example/target.go" || req.StartLine != 1 || req.EndLine != 300 {
		t.Fatalf("unexpected inferred request: %#v", req)
	}
	if !strings.Contains(raw, `"path":"internal/example/target.go"`) {
		t.Fatalf("unexpected raw payload: %q", raw)
	}
}

func TestParseToolRequestRejectsBareJSONSideEffectAliases(t *testing.T) {
	cases := []string{
		`{"command":"go test ./..."}`,
		`{"prompt":"inspect this","agent":"reviewer"}`,
		`{"path":"target.go","old_text":"before","new_text":"after"}`,
		`{"rationale":"Verification Checklist:\n- Failing item: needs repair","edits":[{"path":"target.go"}]}`,
		`{"id":"REQ-1","status":"ok"}`,
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, _, ok, err := ParseToolRequest(tc)
			if err != nil {
				t.Fatalf("ParseToolRequest: %v", err)
			}
			if ok {
				t.Fatalf("bare JSON side-effect or answer-shaped payload should remain answer content: %s", tc)
			}
		})
	}
}

func TestParseToolRequestIgnoresBareJSONWithoutToolShape(t *testing.T) {
	_, _, ok, err := ParseToolRequest(`{"status":"ok","summary":"done"}`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if ok {
		t.Fatal("ordinary bare JSON should remain answer content")
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

func TestParseToolRequestRepairsRawControlCharsInsideStringValues(t *testing.T) {
	raw := "{\n" +
		"  \"tool\":\"edit_file\",\n" +
		"  \"path\":\"internal/labels/labels.go\",\n" +
		"  \"old_text\":\"func ShortLabel() string {\\n\treturn \\\"Save changes\\\"\\n}\",\n" +
		"  \"new_text\":\"func ShortLabel() string {\\n\treturn \\\"Save\\\"\\n}\",\n" +
		"  \"description\":\"shorten label\"\n" +
		"}"
	req, _, ok, err := ParseToolRequest("```tool\n" + raw + "\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest should repair raw tabs in JSON strings: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "edit_file" || req.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(req.OldText, "\treturn \"Save changes\"") || !strings.Contains(req.NewText, "\treturn \"Save\"") {
		t.Fatalf("expected repaired text fields to preserve tabs, old=%q new=%q", req.OldText, req.NewText)
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

func TestParseToolRequestTreatsSearchQueryAsPatternAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"path\":\"internal/productworkflow\",\"query\":\"lint audit authority\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "search_content" {
		t.Fatalf("tool = %q, want search_content", req.Tool)
	}
	if req.Pattern != "lint audit authority" {
		t.Fatalf("pattern = %q, want query alias", req.Pattern)
	}
}

func TestParseToolRequestTreatsOperationAsCapabilityAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"operation\":\"read_symbol\",\"path\":\"internal/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseToolRequestTreatsActionAsCodeNavigateCapabilityAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"action\":\"read_symbol\",\"extract_body\":true,\"path\":\"internal/productworkflow/coverage_gate.go\",\"symbol\":\"CoverageResult\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Pattern != "CoverageResult" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseToolRequestTreatsResolutionAsCodeNavigateCapabilityAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"resolution\":\"definition\",\"path\":\"internal/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "definition" || req.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseToolRequestAcceptsLineAnchoredToolJSONObjectAfterProse(t *testing.T) {
	req, raw, ok, err := ParseToolRequest("I need to inspect the source first.\n\ntool: read_file {\"path\":\"cmd/idiom/product_workflow.go\",\"start_line\":1,\"end_line\":80}")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected line-anchored tool request")
	}
	if req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow.go" || req.StartLine != 1 || req.EndLine != 80 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsTaggedToolCodeJSONObjectAfterProse(t *testing.T) {
	req, _, ok, err := ParseToolRequest("I need to inspect the source.\n\n<tool_code>\n{\"tool\":\"read_file\",\"path\":\"internal/productworkflow/coverage_gate.go\"}\n</tool_code>")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tagged tool_code request")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/coverage_gate.go" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseToolRequestRejectsMismatchedLineAnchoredToolJSONObject(t *testing.T) {
	_, _, ok, err := ParseToolRequest("tool: read_file {\"tool\":\"submit_plan\",\"path\":\"target.go\"}")
	if err == nil || !ok {
		t.Fatalf("expected mismatched line-anchored tool request to surface a parse error, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestAcceptsBareToolJSONObject(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`submit_plan

{
  "rationale": "Verification Checklist:\n- source_sanity failed for target.txt, so submit a corrective replacement.",
  "edits": [{"path":"target.txt","old_text":"old","new_text":"new","purpose":"repair target"}]
}`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected bare tool JSON request")
	}
	if req.Tool != "submit_plan" || req.Rationale == "" || len(req.Edits) != 1 || req.Edits[0].Path != "target.txt" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("expected normalized raw payload to include tool, got %q", raw)
	}
}

func TestParseToolRequestRejectsMismatchedBareToolJSONObject(t *testing.T) {
	_, _, ok, err := ParseToolRequest("read_file\n{\"tool\":\"submit_plan\",\"path\":\"target.go\"}")
	if err == nil || !ok {
		t.Fatalf("expected mismatched bare tool request to surface a parse error, ok=%t err=%v", ok, err)
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

func TestParseToolRequestRepairsUnclosedPrefixedQwenXMLTool(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`<tool_call>
<tool_code_navigate>
<parameter=action>
read_symbol
</parameter>
<parameter=path>
internal/productworkflow/coverage_gate.go
</parameter>
<parameter=symbol>
CoverageGate
</parameter>
</tool_call>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected repaired prefixed XML tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestRepairsSplitQwenNamedToolTags(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source.

<tool_call>
<
tool_name>
read_file
</
tool_name>
<
arguments>
{"path":"internal/productworkflow/coverage_gate.go","start_line":1,"end_line":120}
</
arguments>
<
tool_name>
>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected repaired split-tag tool request")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/coverage_gate.go" || req.StartLine != 1 || req.EndLine != 120 {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"path":"internal/productworkflow/coverage_gate.go"`) {
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

func TestParseToolRequestAcceptsQwenPrefixedToolTag(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I need one bounded symbol read.

<tool_code_navigate>
<parameter=path>
internal/productworkflow/coverage_gate.go
</parameter>
<parameter=operation>
read_symbol
</parameter>
<parameter=symbol>
EvaluateCoverageGate
</parameter>
</tool_code_navigate>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected prefixed XML tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/productworkflow/coverage_gate.go" || req.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenPrefixedToolTagJSONArguments(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/productworkflow/coverage_gate.go","capability":"read_symbol","symbol":"CoverageGate"}
</tool_code_navigate>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected prefixed XML tool request with JSON arguments")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenPrefixedToolTagJSONArgumentsWithoutCapability(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/productworkflow/coverage_gate.go","precision_tier":"syntax-index","symbol":"CoverageGate"}
</tool_code_navigate>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected prefixed XML tool request with JSON arguments to remain a tool request")
	}
	if req.Tool != "code_navigate" || req.Capability != "" || req.Path != "internal/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestRejectsQwenPrefixedToolTagJSONToolMismatch(t *testing.T) {
	_, _, ok, err := ParseToolRequest(`<tool_code_navigate>
{"tool":"read_file","path":"internal/productworkflow/coverage_gate.go"}
</tool_code_navigate>`)
	if !ok || err == nil || !strings.Contains(err.Error(), "does not match JSON tool") {
		t.Fatalf("expected prefixed XML tool mismatch error, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestIgnoresProsePrefixedToolReference(t *testing.T) {
	_, _, ok, err := ParseToolRequest("Use `<tool_read_file>` only when emitting an actual tool envelope.")
	if err != nil || ok {
		t.Fatalf("expected prose prefixed-tool reference to remain answer content, ok=%t err=%v", ok, err)
	}
}

func TestParseToolRequestPreservesAngleTextInsideQwenParameters(t *testing.T) {
	req, _, ok, err := ParseToolRequest(`<tool_call>
<function=submit_plan>
<parameter=rationale>
Replace comparison examples.
</parameter>
<parameter=edits>
[{"path":"target.go","old_text":"if a < name > args {\n\treturn old\n}","new_text":"if a < name > args {\n\treturn next\n}","purpose":"replace comparison branch"}]
</parameter>
</function>
</tool_call>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected qwen XML submit_plan request")
	}
	if req.Tool != "submit_plan" || len(req.Edits) != 1 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if req.Edits[0].OldText != "if a < name > args {\n\treturn old\n}" || req.Edits[0].NewText != "if a < name > args {\n\treturn next\n}" {
		t.Fatalf("angle comparison text was not preserved: %#v", req.Edits[0])
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
