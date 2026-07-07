package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
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

func TestParseToolRequestRepairsMalformedToolNameObjectPayload(t *testing.T) {
	req, _, ok, err := ParseToolRequest(`{"tool":"code_navigate":{"capability":"read_symbol","path":"cmd/idiom/audit.go","symbol":"runAuditWatchMain"}}`)
	if err != nil {
		t.Fatalf("ParseToolRequest should repair malformed tool-name object: %v", err)
	}

	if !ok {
		t.Fatal("expected repaired tool request")
	}

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/audit.go" || req.Symbol != "runAuditWatchMain" {
		t.Fatalf("unexpected request: %#v", req)
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

func TestParseToolRequestUnwrapsNestedRequestObjectPayload(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"request\":{\"pattern\":\"redacted\",\"path\":\"internal\"}}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request")
	}

	if req.Tool != "search_content" || req.Pattern != "redacted" || req.Path != "internal" {
		t.Fatalf("unexpected nested object request repair: %#v", req)
	}
}

func TestParseToolRequestNestedCodeNavigateGraphResolutionDoesNotBecomeCapability(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"request\":{\"tool\":\"code_navigate\",\"query\":\"task tool implementation\",\"resolution\":\"symbol\",\"include_evidence\":\"true\"}}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request")
	}

	if req.Tool != "code_navigate" || req.Query != "task tool implementation" || req.Resolution != "symbol" || !req.IncludeEvidence {
		t.Fatalf("unexpected nested object request repair: %#v", req)
	}

	if req.Capability != "" {
		t.Fatalf("graph resolution must not be treated as code_navigate capability: %#v", req)
	}
}

func TestParseToolRequestUnwrapsNestedInputObjectPayload(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"input\":{\"tool\":\"read_symbol\",\"path\":\"internal/frontier/discovery.go\",\"symbol\":\"DiscoveryQuery\"}}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request")
	}

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/frontier/discovery.go" || req.Symbol != "DiscoveryQuery" {
		t.Fatalf("unexpected nested input object request repair: %#v", req)
	}
}

func TestCodeNavigateAcceptsFindReferencesAlias(t *testing.T) {
	spec, err := codeNavigationSpecFor("find_references")
	if err != nil {
		t.Fatalf("codeNavigationSpecFor(find_references): %v", err)
	}

	if spec.Name != "references" || spec.Capability != semanticlanguage.CapReferences {
		t.Fatalf("unexpected references alias spec: %#v", spec)
	}
}

func TestCodeNavigateFileOutlineDirectoryReturnsCandidates(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "internal/surfaces/tui/app/lifecycle.go"), "package app\n")
	writeTestFile(t, filepath.Join(dir, "internal/surfaces/tui/app/rendering.go"), "package app\n")

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "file_outline",
		Path:       "internal/surfaces/tui/app",
	})

	if result.Error != "" {
		t.Fatalf("directory outline should return candidate evidence, got error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "routed_tool: directory_candidates") ||
		!strings.Contains(result.Output, "candidate_file: internal/surfaces/tui/app/lifecycle.go") ||
		!strings.Contains(result.Output, "candidate_file: internal/surfaces/tui/app/rendering.go") {

		t.Fatalf("directory outline missing candidate files:\n%s", result.Output)
	}
}

func TestCodeNavigateRejectsGraphResolutionWithoutNavigationCapability(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "internal/surfaces/tui/app/lifecycle.go"), "package app\nfunc Run() {}\n")

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:            "code_navigate",
		Query:           "task tool implementation",
		Resolution:      "symbol",
		IncludeEvidence: true,
	})

	if result.Error == "" {
		t.Fatalf("expected graph-resolution-shaped code_navigate request to be rejected, got %#v", result)
	}

	if !strings.Contains(result.Error, "use context_query") || !strings.Contains(result.Error, "set capability") {
		t.Fatalf("rejection should explain the contract mismatch, got %q", result.Error)
	}
}

func TestParseToolRequestTreatsSymbolAsPatternAlias(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"go_read_symbol\",\"path\":\"internal/runtime/agent/runner.go\",\"symbol\":\"Runner.Run\"}\n```")
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
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"path\":\"internal/workflows/productworkflow\",\"query\":\"lint audit authority\"}\n```")
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
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"operation\":\"read_symbol\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
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
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"action\":\"read_symbol\",\"extract_body\":true,\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"CoverageResult\"}\n```")
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
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"resolution\":\"definition\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
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

func TestParseToolRequestDoesNotTreatGraphResolutionAsCodeNavigateCapability(t *testing.T) {
	req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"resolution\":\"symbol\",\"query\":\"task tool implementation\"}\n```")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request")
	}

	if req.Tool != "code_navigate" || req.Resolution != "symbol" || req.Query != "task tool implementation" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if req.Capability != "" {
		t.Fatalf("graph resolution must not become capability: %#v", req)
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
	req, _, ok, err := ParseToolRequest("I need to inspect the source.\n\n<tool_code>\n{\"tool\":\"read_file\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\"}\n</tool_code>")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tagged tool_code request")
	}

	if req.Tool != "read_file" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" {
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
internal/workflows/productworkflow/coverage_gate.go
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

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
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
{"path":"internal/workflows/productworkflow/coverage_gate.go","start_line":1,"end_line":120}
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

	if req.Tool != "read_file" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.StartLine != 1 || req.EndLine != 120 {
		t.Fatalf("unexpected repaired request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"path":"internal/workflows/productworkflow/coverage_gate.go"`) {
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
internal/workflows/productworkflow/coverage_gate.go
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

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenPrefixedToolTagJSONArguments(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","capability":"read_symbol","symbol":"CoverageGate"}
</tool_code_navigate>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected prefixed XML tool request with JSON arguments")
	}

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsQwenPrefixedToolTagJSONArgumentsWithoutCapability(t *testing.T) {
	req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","precision_tier":"syntax-index","symbol":"CoverageGate"}
</tool_code_navigate>`)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected prefixed XML tool request with JSON arguments to remain a tool request")
	}

	if req.Tool != "code_navigate" || req.Capability != "" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.Pattern != "CoverageGate" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("expected normalized JSON raw payload, got %q", raw)
	}
}

func TestParseToolRequestRejectsQwenPrefixedToolTagJSONToolMismatch(t *testing.T) {
	_, _, ok, err := ParseToolRequest(`<tool_code_navigate>
{"tool":"read_file","path":"internal/workflows/productworkflow/coverage_gate.go"}
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

func TestParseToolRequestIgnoresProseProtocolTerms(t *testing.T) {
	for _, text := range []string{
		"Chat Completions returns `tool_calls` when the assistant requests a tool.",
		"DSML is a protocol label, not a tool request by itself.",
		"The prior response was invoked from a child runner.",
	} {
		_, _, ok, err := ParseToolRequest(text)
		if err != nil || ok {
			t.Fatalf("expected prose protocol term to remain a final answer, ok=%t err=%v text=%q", ok, err, text)
		}
	}
}

func TestLooksLikeXMLToolCallMarkupIgnoresProseProtocolTerms(t *testing.T) {
	for _, text := range []string{
		"Chat Completions returns `tool_calls` when the assistant requests a tool.",
		"DSML is a protocol label, not a tool request by itself.",
		"Use `<function=submit_plan>` only inside a tool envelope.",
	} {
		if LooksLikeXMLToolCallMarkup(text) {
			t.Fatalf("prose protocol term should not look like XML tool markup: %q", text)
		}
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
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/analysis_agents.go\"}\n```",
			want: "go_file_outline",
		},
		{
			name: "move declaration tag",
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/app.go\",\"destination\":\"internal/surfaces/tui/app/rendering.go\",\"pattern\":\"tag:rendering\"}\n```",
			want: "go_move_declarations",
		},
		{
			name: "shell command",
			text: "```tool\n{\"command\":\"go test ./internal/surfaces/tui/app\"}\n```",
			want: "shell",
		},
		{
			name: "child task",
			text: "```tool\n{\"agent\":\"explorer\",\"prompt\":\"inspect routing\"}\n```",
			want: "task",
		},
		{
			name: "line edit",
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/app.go\",\"start_line\":10,\"end_line\":10,\"content\":\"replacement\"}\n```",
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

func TestDecodeToolRequestJSONRepairsObjectEvidenceForSubmitPlan(t *testing.T) {
	payload := []byte(`{"tool":"submit_plan","rationale":"plan","evidence":{"native/tests/test_quality_inventory_bindings.cpp":"read lines 1-180"},"edits":[{"path":"native/tests/test_quality_inventory_bindings.cpp","old_text":"old","new_text":"new","purpose":"replace test expectation"}]}`)

	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should tolerate object evidence metadata: %v", err)
	}

	if req.Tool != "submit_plan" || len(req.Edits) != 1 {
		t.Fatalf("unexpected repaired submit_plan request: %#v", req)
	}

	if len(req.Evidence) != 1 || !strings.Contains(req.Evidence[0], "native/tests/test_quality_inventory_bindings.cpp") {
		t.Fatalf("object evidence should be preserved as compact evidence text, got %#v", req.Evidence)
	}
}

func TestDecodeToolRequestJSONRepairsStructuredSourceSatisfiedPaths(t *testing.T) {
	payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":[{"path":"b.txt","evidence":"current source already final"},{"file":"c.txt"}],"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)

	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should repair structured source_satisfied_paths: %v", err)
	}

	if req.Tool != "submit_plan" || len(req.Edits) != 1 {
		t.Fatalf("unexpected repaired submit_plan request: %#v", req)
	}

	if got, want := req.SourceSatisfiedPaths, []string{"b.txt", "c.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("source_satisfied_paths = %#v, want %#v", got, want)
	}
}

func TestDecodeToolRequestJSONRepairsObjectSourceSatisfiedPaths(t *testing.T) {
	payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":{"b.txt":"current source already final","c.txt":"current source already final"},"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)

	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should repair object source_satisfied_paths: %v", err)
	}

	if got, want := req.SourceSatisfiedPaths, []string{"b.txt", "c.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("source_satisfied_paths = %#v, want %#v", got, want)
	}
}

func TestDecodeToolRequestJSONRejectsMetadataOnlySourceSatisfiedPathObjects(t *testing.T) {
	payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":[{"evidence":"current source already final"}],"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)
	_, err := DecodeToolRequestJSON(payload)

	if err == nil || !strings.Contains(err.Error(), "source_satisfied_paths") {
		t.Fatalf("metadata-only source_satisfied_paths object should remain a decode error, got %v", err)
	}
}

func TestDecodeToolRequestJSONRepairsStringifiedSourceSatisfiedPathsWithTrailingProse(t *testing.T) {
	inner := `["b.txt"]

These are already final.`

	payload, err := json.Marshal(map[string]any{
		"tool":                   "submit_plan",
		"rationale":              "mixed plan",
		"source_satisfied_paths": inner,
		"edits":                  []map[string]any{{"path": "a.txt", "old_text": "old", "new_text": "new", "purpose": "replace fixture"}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should repair stringified source_satisfied_paths: %v", err)
	}

	if got, want := req.SourceSatisfiedPaths, []string{"b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("source_satisfied_paths = %#v, want %#v", got, want)
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

// TestParseToolRequestRejectsProseJSONInsideQwenPrefixedTags ensures that
// XML-tagged JSON that describes tool behaviour rather than invoking a tool
// is not misclassified as a tool request. Qwen in thinking mode may produce
// prose like <tool_note>{"key":"evidence","content":"found X"}</tool_note>
// when it is explaining what it would do, not actually calling note.
func TestParseToolRequestRejectsProseJSONInsideQwenPrefixedTags(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			name: "note tool descriptive JSON",
			text: `<tool_note>
{"key":"evidence","content":"Found the runner gate in internal/runtime/agent/runner.go"}
</tool_note>`,
		},
		{
			name: "note tool descriptive JSON without tool fields",
			text: `<tool_note>
{"key":"plan-cache","content":"The context_pack cache uses content-addressed keys with workspace fingerprints"}
</tool_note>`,
		},
		{
			name: "prose inside arbitrary tool tag",
			text: `<tool_read_file>
{"status":"ok","summary":"done"}
</tool_read_file>`,
		},
		{
			name: "descriptive JSON with only generic fields",
			text: `<tool_context_pack>
{"description":"I need to find the runner source","confidence":"high"}
</tool_context_pack>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok, err := ParseToolRequest(tc.text)
			if err != nil {
				t.Fatalf("ParseToolRequest returned unexpected error: %v", err)
			}

			if ok {
				t.Fatalf("expected prose JSON inside prefixed tool tags to remain answer content, got ok=true for: %s", tc.name)
			}
		})
	}
}

// TestParseToolRequestAcceptsDeepSeekNativeJSONFormat verifies that the
// standard {"tool":"name",...} JSON format used by DeepSeek and other
// native-tool-call backends parses correctly after the Qwen XML guard.
func TestParseToolRequestAcceptsDeepSeekNativeJSONFormat(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "read_file bare JSON",
			text: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`,
			want: "read_file",
		},
		{
			name: "search_content bare JSON",
			text: `{"tool":"search_content","pattern":"ParseToolRequest","path":"internal/capabilities/workspace"}`,
			want: "search_content",
		},
		{
			name: "code_navigate bare JSON",
			text: `{"tool":"code_navigate","capability":"read_symbol","path":"internal/runtime/agent/runner.go","symbol":"Runner.Run"}`,
			want: "code_navigate",
		},
		{
			name: "context_pack bare JSON",
			text: `{"tool":"context_pack","task":"find the runner gate","mode":"plan","limit":8}`,
			want: "context_pack",
		},
		{
			name: "fenced tool JSON (markdown style)",
			text: "```tool\n" + `{"tool":"read_file","path":"internal/runtime/agent/runner.go"}` + "\n```",
			want: "read_file",
		},
		{
			name: "line-anchored tool JSON",
			text: "tool: read_file {\"path\":\"README.md\",\"start_line\":1,\"end_line\":80}",
			want: "read_file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _, ok, err := ParseToolRequest(tc.text)
			if err != nil {
				t.Fatalf("ParseToolRequest: %v", err)
			}

			if !ok {
				t.Fatalf("expected DeepSeek-native tool request to be accepted: %s", tc.name)
			}

			if req.Tool != tc.want {
				t.Fatalf("tool = %q, want %q", req.Tool, tc.want)
			}
		})
	}
}

func TestParseToolRequestAcceptsDeepSeekXMLReadTools(t *testing.T) {
	text := `<｜tool_calls_begin｜><｜tool_call_begin｜><function name="read_file">
<parameter name="path">internal/runtime/agent/runner.go</parameter>
<parameter name="start_line">1</parameter>
<parameter name="end_line">20</parameter>
</function><｜tool_call_end｜><｜tool_calls_end｜>`

	req, _, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected DeepSeek XML read_file request to parse")
	}

	if req.Tool != "read_file" || req.Path != "internal/runtime/agent/runner.go" || req.StartLine != 1 || req.EndLine != 20 {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseToolRequestAcceptsOpenDeepSeekXMLReadTool(t *testing.T) {
	text := `<n_tool_calls>
<n_invoke name="read_file">
<n_parameter name="path" string="true">internal/runtime/agent/runner.go</n_parameter>
</n_tool_calls>`

	req, raw, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected open DeepSeek XML read_file request to parse")
	}

	if req.Tool != "read_file" || req.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"path":"internal/runtime/agent/runner.go"`) {
		t.Fatalf("expected canonical raw request with tool/path, got %s", raw)
	}
}

func TestParseToolRequestAcceptsOpenDeepSeekXMLToolNameForNormalValidation(t *testing.T) {
	text := `<n_tool_calls>
<n_invoke name="search_content">
</n_tool_calls>`

	req, raw, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected open DeepSeek XML search_content request to parse")
	}

	if req.Tool != "search_content" || req.Pattern != "" || req.Path != "" {
		t.Fatalf("unexpected request: %#v", req)
	}

	if raw != `{"tool":"search_content"}` {
		t.Fatalf("expected canonical raw request with only tool name, got %s", raw)
	}
}

func TestParseToolRequestRejectsDeepSeekXMLWriteTools(t *testing.T) {
	text := `<｜tool_calls_begin｜><｜tool_call_begin｜><function name="submit_plan">
<parameter name="rationale">submit edits</parameter>
</function><｜tool_call_end｜><｜tool_calls_end｜>`
	_, _, ok, err := ParseToolRequest(text)

	if !ok || err == nil {
		t.Fatalf("expected DeepSeek XML write tool to be rejected, ok=%t err=%v", ok, err)
	}

	if !strings.Contains(err.Error(), "not a read/search/discovery tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestParseToolRequestAcceptsAllQwenFormatsAfterGuard verifies that every
// Qwen tool-call format continues to parse correctly after the XML guard
// that rejects descriptive prose.
func TestParseToolRequestAcceptsAllQwenFormatsAfterGuard(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "Qwen JSON envelope",
			text: `<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}
</tool_call>`,
			want: "read_file",
		},
		{
			name: "Qwen name+arguments envelope",
			text: `<tool_call>
{"name":"submit_plan","arguments":{"rationale":"plan","edits":[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update"}]}}
</tool_call>`,
			want: "submit_plan",
		},
		{
			name: "Qwen function envelope",
			text: `<tool_call>
<function=submit_plan>
<parameter=rationale>Add the gate.</parameter>
<parameter=edits>[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update"}]</parameter>
</function>
</tool_call>`,
			want: "submit_plan",
		},
		{
			name: "Qwen prefixed tag with parameters",
			text: `<tool_code_navigate>
<parameter=path>internal/surfaces/tui/app/lifecycle.go</parameter>
<parameter=operation>read_symbol</parameter>
<parameter=symbol>Lifecycle.Run</parameter>
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen prefixed tag with JSON arguments",
			text: `<tool_code_navigate>
{"path":"internal/workflows/productworkflow/coverage_gate.go","capability":"read_symbol","symbol":"CoverageGate"}
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen prefixed tag with JSON without capability",
			text: `<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","precision_tier":"syntax-index","symbol":"CoverageGate"}
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen unclosed prefixed tag",
			text: `<tool_call>
<tool_code_navigate>
<parameter=action>read_symbol</parameter>
<parameter=path>internal/workflows/productworkflow/coverage_gate.go</parameter>
<parameter=symbol>CoverageGate</parameter>
</tool_call>`,
			want: "code_navigate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _, ok, err := ParseToolRequest(tc.text)
			if err != nil {
				t.Fatalf("ParseToolRequest: %v", err)
			}

			if !ok {
				t.Fatalf("expected Qwen tool request to be accepted after guard: %s", tc.name)
			}

			if req.Tool != tc.want {
				t.Fatalf("tool = %q, want %q", req.Tool, tc.want)
			}
		})
	}
}

// TestParseToolRequestRejectsProseWithToolPrefixNoJSON ensures that prose
// containing <tool_ references without a valid tool-call body is still
// treated as a final answer.
func TestParseToolRequestRejectsProseWithToolPrefixNoJSON(t *testing.T) {
	cases := []string{
		"Use `<tool_read_file>` only when emitting an actual tool envelope.",
		"After the <tool_code_navigate> step, inspect the results.",
		"Do not write <tool_note> tags in your final answer prose.",
	}
	for _, tc := range cases {
		_, _, ok, err := ParseToolRequest(tc)
		if err != nil {
			t.Fatalf("ParseToolRequest returned unexpected error for prose: %v (text=%q)", err, tc)
		}

		if ok {
			t.Fatalf("expected prose with <tool_ references to remain answer content: %q", tc)
		}
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
	mgr := NewManager(testWorkspaceConfig(t))
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

func TestStructuredNarrowingUsesSharedRegistryPredicate(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))

	for _, lang := range semanticlanguage.LanguageRegistry() {
		for capability, tool := range lang.Tools {
			req := ToolRequest{Tool: tool}
			if !IsNarrowingRequest(req) {
				t.Fatalf("%s tool %s for %s should be narrowing", lang.Name, tool, capability)
			}

			if !mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"}) {
				t.Fatalf("%s tool %s for %s should clear broad inspection lock", lang.Name, tool, capability)
			}
		}
	}

	for _, specs := range supplementalLanguageToolSpecs {
		for _, spec := range specs {
			tool := spec.Tool
			req := ToolRequest{Tool: tool}

			if !IsNarrowingRequest(req) {
				t.Fatalf("legacy language helper %s should be narrowing", tool)
			}

			if !mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"}) {
				t.Fatalf("legacy language helper %s should clear broad inspection lock", tool)
			}
		}
	}

	for _, tool := range []string{"mcp:semantic_search", "recovery_inspect", "context_unregistered"} {
		req := ToolRequest{Tool: tool}
		if IsNarrowingRequest(req) {
			t.Fatalf("%s should not be narrowing", tool)
		}

		if mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"}) {
			t.Fatalf("%s should not clear broad inspection lock", tool)
		}
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

func TestRuntimeInspectionRequestUsesToolSurfaceMetadata(t *testing.T) {
	for _, req := range []ToolRequest{
		{Tool: ToolNameContextQuery, Query: "callers of Run"},
		{Tool: ToolNameFileStats, Path: "internal"},
		{Tool: ToolNameTask, Agent: "explorer", Mode: "read_only"},
		{Tool: ToolNameSearchContent, Pattern: "Run", Path: "internal"},
		{Tool: ToolNameShell, Command: "rg Run internal"},
	} {
		if !IsRuntimeInspectionRequest(req) {
			t.Fatalf("%s should be runtime inspection", req.Tool)
		}
	}

	for _, req := range []ToolRequest{
		{Tool: ToolNameSourceSanity, Path: "internal/runtime/agent/runner.go"},
		{Tool: ToolNameHarnessFeedback},
		{Tool: ToolNameEditFile, Path: "internal/runtime/agent/runner.go"},
		{Tool: ToolNameSubmitPlan},
		{Tool: ToolNameGitDiff},
	} {
		if IsRuntimeInspectionRequest(req) {
			t.Fatalf("%s should not be runtime inspection", req.Tool)
		}
	}
}

func TestParseToolRequestAcceptsCallingToolEnvelope(t *testing.T) {
	// Exact format emitted by Qwen 3.6b-35b in session 20260530-052348.879
	text := `[Calling tool: search_content({"glob":"*.go","pattern":"type.*Evidence","named_gap":"exact definition of Evidence type/struct/interface","path":".","tool":"search_content","why_not_answering":"Without the Evidence type definition, I cannot describe how evidence is structured in the idiom harness."})]`

	req, raw, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request for [Calling tool: ...] format")
	}

	if req.Tool != "search_content" {
		t.Fatalf("expected tool=search_content, got %q", req.Tool)
	}

	if req.Pattern != "type.*Evidence" {
		t.Fatalf("expected pattern=type.*Evidence, got %q", req.Pattern)
	}

	if req.Path != "." {
		t.Fatalf("expected path=., got %q", req.Path)
	}

	if req.NamedGap != "exact definition of Evidence type/struct/interface" {
		t.Fatalf("expected named_gap to be preserved, got %q", req.NamedGap)
	}

	if !strings.HasPrefix(raw, `{"`) {
		t.Fatalf("expected JSON object payload, got %q", raw)
	}
}

func TestParseToolRequestAcceptsCallingToolEnvelopeLowercase(t *testing.T) {
	text := `[calling tool: search_content({"glob":"*.go","pattern":"type.*Evidence","path":".","tool":"search_content"})]`

	req, _, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request for [calling tool: ...] format (lowercase)")
	}

	if req.Tool != "search_content" {
		t.Fatalf("expected tool=search_content, got %q", req.Tool)
	}
}

func TestParseToolRequestRejectsCallingToolWithoutJSON(t *testing.T) {
	text := `[Calling tool: search_content not valid json]`
	_, _, ok, _ := ParseToolRequest(text)

	if ok {
		t.Fatal("expected rejection for [Calling tool: ...] without JSON object")
	}
}

func TestParseToolRequestRejectsCallingToolWithoutToolKey(t *testing.T) {
	text := `[Calling tool: search_content({"pattern":"type.*Evidence","path":"."})]`
	_, _, ok, _ := ParseToolRequest(text)

	if ok {
		t.Fatal("expected rejection for [Calling tool: ...] without tool key in JSON")
	}
}

func TestParseToolRequestAcceptsCallingToolEnvelopeWithWhitespace(t *testing.T) {
	text := `[Calling tool:   search_content ( {"glob":"*.go","pattern":"type.*Evidence","path":".","tool":"search_content"} ) ]`

	req, _, ok, err := ParseToolRequest(text)
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}

	if !ok {
		t.Fatal("expected tool request for [Calling tool: ...] with whitespace")
	}

	if req.Tool != "search_content" {
		t.Fatalf("expected tool=search_content, got %q", req.Tool)
	}
}

func TestParseToolRequestRejectsCallingToolProseWithoutJSON(t *testing.T) {
	for _, text := range []string{
		`I am [Calling tool: the workshop about tooling] today`,
		`[Calling tool: read_file not a json object]`,
		`Use [Calling tool: read_file] to read the file`,
		`[Calling conventions: we use {"status":"ok"} for responses]`,
	} {
		_, _, ok, _ := ParseToolRequest(text)
		if ok {
			t.Fatalf("expected rejection for prose: %q", text)
		}
	}
}
