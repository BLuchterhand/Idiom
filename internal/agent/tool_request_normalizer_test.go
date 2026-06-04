package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"idiom/internal/llama"
)

func TestNormalizeToolRequestSyntaxNativeToolCall(t *testing.T) {
	rawArgs := `{"pattern":"needle","path":"internal","glob":"*.go"}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		ToolCalls: []llama.ToolCall{{
			Function: llama.ToolCallFunc{
				Name:      "search_content",
				Arguments: rawArgs,
			},
		}},
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected native tool call to normalize")
	}
	if result.Request.Tool != "search_content" || result.Request.Pattern != "needle" || result.Request.Path != "internal" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.Raw != rawArgs {
		t.Fatalf("Raw = %q, want %q", result.Raw, rawArgs)
	}
	if result.ActiveToolCall == nil {
		t.Fatal("expected active native tool call context")
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportNative {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxTextualJSON(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.ActiveToolCall != nil {
		t.Fatal("textual normalization must not create native tool-call context")
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxReadFileLinesAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"logs/conversations/recent.jsonl","lines":"1-51"}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "logs/conversations/recent.jsonl" || result.Request.StartLine != 1 || result.Request.EndLine != 51 {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if !result.Repaired {
		t.Fatal("expected lines alias repair to be marked repaired")
	}
	for _, want := range []string{`"start_line":1`, `"end_line":51`} {
		if !strings.Contains(result.Raw, want) {
			t.Fatalf("raw repair missing %q:\n%s", want, result.Raw)
		}
	}
}

func TestNormalizeToolRequestSyntaxReadFileLinesAliasPreservesExplicitRange(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"logs/conversations/recent.jsonl","lines":"1-51","start_line":52,"end_line":131}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.StartLine != 52 || result.Request.EndLine != 131 {
		t.Fatalf("explicit range should win, got %d-%d", result.Request.StartLine, result.Request.EndLine)
	}
}

func TestNormalizeToolRequestSyntaxTextualJSONEditFile(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"	return \"Save changes\"","new_text":"	return \"Save\"","description":"Update ShortLabel"}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatalf("expected textual edit_file JSON tool request to normalize: %#v", result)
	}
	if result.Request.Tool != "edit_file" || result.Request.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.Request.OldText != "\treturn \"Save changes\"" || result.Request.NewText != "\treturn \"Save\"" {
		t.Fatalf("unexpected replacement text: old=%q new=%q", result.Request.OldText, result.Request.NewText)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxBareToolJSONObject(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"submit_plan",
			"",
			"{",
			`  "rationale": "Verification Checklist:\n- source_sanity failed for target.txt, so submit a corrective replacement.",`,
			`  "edits": [{"path":"target.txt","old_text":"old","new_text":"new","purpose":"repair target"}]`,
			"}",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected bare submit_plan JSON request to normalize")
	}
	if result.Request.Tool != "submit_plan" || result.Request.Rationale == "" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "target.txt" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxFencedJSON(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "```json\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":20}\n```",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected exact fenced JSON tool request to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFencedJSON {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxKeyValue(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: README.md\nstart_line: 1\nend_line: 20\nnamed_gap: inspect intro",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key/value tool request to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 || result.Request.NamedGap != "inspect intro" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueSearchScopeAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: search_content\npattern: implementation detection\nscope: .",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatalf("expected key/value search request to normalize: %#v", result)
	}
	if result.Request.Tool != "search_content" || result.Request.Pattern != "implementation detection" || result.Request.Path != "." {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueActionAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\naction: read_symbol\npath: internal/productworkflow/coverage_gate.go\nsymbol: EvaluateCoverageGate",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key/value tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" || result.Request.Capability != "read_symbol" || result.Request.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueLinesAliasOnlyAppliesToReadFile(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\npath: internal/productworkflow/coverage_gate.go\nsymbol: EvaluateCoverageGate\nlines: 1-20",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key/value tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		t.Fatalf("non-read_file lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine)
	}

	result = normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"old","new_text":"new","lines":"1-20","description":"negative alias test"}`,
	})
	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected edit request to normalize")
	}
	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		t.Fatalf("mutation request lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine)
	}

	result = normalizeToolRequestSyntax(llama.ChatMessage{
		ToolCalls: []llama.ToolCall{{
			Function: llama.ToolCallFunc{
				Name:      "edit_file",
				Arguments: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"old","new_text":"new","lines":"1-20","description":"negative native alias test"}`,
			},
		}},
	})
	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected native edit request to normalize")
	}
	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		t.Fatalf("native mutation request lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueReadFileLinesAliasRejectsMalformedRange(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: logs/conversations/recent.jsonl\nlines: 1-bad",
	})

	if result.OK {
		t.Fatalf("malformed lines alias should not normalize: %#v", result.Request)
	}
}

func TestNormalizeToolRequestSyntaxCodeNavigateDefaultsMissingCapabilityWithSymbol(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\npath: cmd/idiom/product_workflow.go\nsymbol: Run\nstart_line: 1\nend_line: 100",
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key/value tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "cmd/idiom/product_workflow.go" ||
		result.Request.Pattern != "Run" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if !result.Repaired {
		t.Fatal("expected missing capability repair to be marked repaired")
	}
}

func TestNormalizeToolRequestSyntaxCodeNavigateDefaultsMissingCapabilityWithPathOnly(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","path":"cmd/idiom/product_workflow.go","symbol":"read_symbol"}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "file_outline" ||
		result.Request.Path != "cmd/idiom/product_workflow.go" ||
		result.Request.Pattern != "" ||
		result.Request.Symbol != "" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if !result.Repaired {
		t.Fatal("expected missing capability repair to be marked repaired")
	}
}

func TestNormalizeToolRequestSyntaxCodeReadAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_read","path":"internal/productworkflow/coverage_gate.go","start_line":1,"end_line":125}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.Tool != "read_file" ||
		result.Request.Path != "internal/productworkflow/coverage_gate.go" ||
		result.Request.StartLine != 1 ||
		result.Request.EndLine != 125 {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if !result.Repaired {
		t.Fatal("expected code_read alias repair to be marked repaired")
	}
}

func TestNormalizeToolRequestSyntaxResolutionAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","path":"internal/productworkflow/coverage_gate.go","pattern":"func EvaluateCoverageGate","resolution":"definition"}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected textual JSON tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "definition" ||
		result.Request.Path != "internal/productworkflow/coverage_gate.go" ||
		result.Request.Pattern != "func EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxCodeNavigateFindReferencesAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","capability":"find_references","path":"internal/app","symbol":"input"}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected missing-tool code_navigate request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "find_references" ||
		result.Request.Path != "internal/app" ||
		result.Request.Pattern != "input" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized {
		t.Fatalf("unexpected event outcome: %q", result.EventOutcome)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueArgsJSON(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool_call:code:navigate",
			"summary: Looking up symbol definition for RunAudit",
			"tool: code_navigate",
			"args:",
			"{",
			`  "path": "cmd/idiom/audit.go",`,
			`  "symbol": "RunAudit",`,
			`  "kind": "definition"`,
			"}",
		}, "\n"),
	})

	if result.OK {
		t.Fatalf("function-call-like multi-line text should not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValueArgs {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxKeyValueArgsJSONRejectsToolMismatch(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\nargs:\n{\"tool\":\"read_file\",\"path\":\"cmd/idiom/audit.go\"}",
	})

	if result.OK {
		t.Fatalf("mismatched key/value args request must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValueArgs {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxFunctionCall(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(capability="read_symbol", path="internal/productworkflow/coverage_gate.go", language="go", symbol="EvaluateCoverageGate")`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected function-call-shaped tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "internal/productworkflow/coverage_gate.go" ||
		result.Request.Language != "go" ||
		result.Request.Symbol != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxFunctionCallOperationAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(operation="read_symbol", path="internal/productworkflow/coverage_gate.go", symbol="EvaluateCoverageGate")`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected function-call-shaped tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "internal/productworkflow/coverage_gate.go" ||
		result.Request.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxFunctionCallActionAlias(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(action="read_symbol", path="internal/productworkflow/coverage_gate.go", symbol="EvaluateCoverageGate")`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected function-call-shaped tool request to normalize")
	}
	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "internal/productworkflow/coverage_gate.go" ||
		result.Request.Pattern != "EvaluateCoverageGate" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxRejectsUnsafeFunctionCallFields(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: edit_file(path="README.md", old_text="old", new_text="new")`,
	})

	if result.OK {
		t.Fatalf("function-call edit body shape must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
	tool, transport, ok := unsafeTextualWriteAttempt(`tool: edit_file(path="README.md", old_text="old", new_text="new")`)
	if !ok || tool != "edit_file" || transport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("expected non-executing unsafe function-call write classification, got tool=%q transport=%q ok=%v", tool, transport, ok)
	}
}

func TestNormalizeToolRequestSyntaxRejectsNonJSONTextualWriteTools(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		tool      string
		transport string
	}{
		{
			name:      "key value write_file",
			content:   "tool: write_file\npath: README.md\ncontent: hello\ndescription: update readme",
			tool:      "write_file",
			transport: toolRequestNormalizerTransportKeyValue,
		},
		{
			name:      "key value append_file",
			content:   "tool: append_file\npath: README.md\ncontent: hello\ndescription: append readme",
			tool:      "append_file",
			transport: toolRequestNormalizerTransportKeyValue,
		},
		{
			name:      "key value args write_file",
			content:   "tool: write_file\nargs:\n{\"path\":\"README.md\",\"content\":\"hello\",\"description\":\"update readme\"}",
			tool:      "write_file",
			transport: toolRequestNormalizerTransportKeyValueArgs,
		},
		{
			name:      "function call write_file",
			content:   `tool: write_file(path="README.md", content="hello", description="update readme")`,
			tool:      "write_file",
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "function call append_file",
			content:   `tool: append_file(path="README.md", content="hello", description="append readme")`,
			tool:      "append_file",
			transport: toolRequestNormalizerTransportFunctionCall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: tt.content})
			if result.OK {
				t.Fatalf("non-JSON textual %s body must not normalize: %#v", tt.tool, result.Request)
			}
			if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != tt.transport {
				t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
			}
			tool, transport, ok := unsafeTextualWriteAttempt(tt.content)
			if !ok || tool != tt.tool || transport != tt.transport {
				t.Fatalf("expected non-executing unsafe textual write classification, got tool=%q transport=%q ok=%v", tool, transport, ok)
			}
		})
	}
}

func TestNormalizeToolRequestSyntaxBareJSONMissingToolInferred(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"path":"README.md","start_line":1,"end_line":20}`,
	})

	if result.ParseErr != nil {
		t.Fatalf("NormalizeToolRequestSyntax: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected safe bare JSON read shape to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" {
		t.Fatalf("unexpected request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeMissingToolInferred || result.EventTransport != toolRequestNormalizerTransportTextual {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONEdit(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"path":"README.md","old_text":"old","new_text":"new"}`,
	})

	if result.OK {
		t.Fatalf("unsafe missing-tool edit shape must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONShell(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"command":"go test ./..."}`,
	})

	if result.OK {
		t.Fatalf("unsafe missing-tool shell shape must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONTask(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"agent":"worker","prompt":"edit README.md"}`,
	})

	if result.OK {
		t.Fatalf("unsafe missing-tool task shape must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxMalformedOutputFixtureMatrix(t *testing.T) {
	stringifiedEdits, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`,
	})
	if err != nil {
		t.Fatalf("marshal stringified edits fixture: %v", err)
	}
	cases := []struct {
		name      string
		content   string
		wantOK    bool
		wantTool  string
		outcome   string
		transport string
		repaired  bool
	}{
		{
			name:      "stringified submit_plan edits repair",
			content:   string(stringifiedEdits),
			wantOK:    true,
			wantTool:  "submit_plan",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportStringifiedSubmitRepair,
			repaired:  true,
		},
		{
			name:      "exact fenced read-only JSON",
			content:   "```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
			wantOK:    true,
			wantTool:  "read_file",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportFencedJSON,
		},
		{
			name:      "narrative fenced JSON rejected",
			content:   "Use this:\n```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFencedJSON,
		},
		{
			name:      "safe bare read-only shape inferred",
			content:   `{"path":"README.md","start_line":1,"end_line":20}`,
			wantOK:    true,
			wantTool:  "read_file",
			outcome:   toolRequestNormalizerOutcomeMissingToolInferred,
			transport: toolRequestNormalizerTransportTextual,
		},
		{
			name:      "unsafe bare edit shape rejected",
			content:   `{"path":"README.md","old_text":"old","new_text":"new"}`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "unsafe function-call edit shape rejected",
			content:   `tool: edit_file(path="README.md", old_text="old", new_text="new")`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "nested read-only arguments repair",
			content:   `{"arguments":{"tool":"read_file","path":"cmd/idiom/audit.go","start_line":1,"end_line":100}}`,
			wantOK:    true,
			wantTool:  "read_file",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportNestedArguments,
			repaired:  true,
		},
		{
			name:      "unsafe missing-tool shell rejected",
			content:   `{"command":"go test ./..."}`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: tc.content})
			if result.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v; request=%#v parseErr=%v", result.OK, tc.wantOK, result.Request, result.ParseErr)
			}
			if tc.wantTool != "" && result.Request.Tool != tc.wantTool {
				t.Fatalf("Tool = %q, want %q; request=%#v", result.Request.Tool, tc.wantTool, result.Request)
			}
			if result.EventOutcome != tc.outcome || result.EventTransport != tc.transport {
				t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
			}
			if result.Repaired != tc.repaired {
				t.Fatalf("Repaired = %v, want %v", result.Repaired, tc.repaired)
			}
		})
	}
}

func TestNormalizeToolRequestSyntaxDoesNotInferNarrativeFencedJSON(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "Please run this:\n```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
	})

	if result.OK {
		t.Fatalf("narrative-wrapped fenced JSON must stay out of the syntax normalizer: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFencedJSON {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxRejectsUnsafeKeyValueFields(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: edit_file\npath: README.md\nold_text: old\nnew_text: new\ndescription: update readme",
	})

	if result.OK {
		t.Fatalf("key/value edit body shape must not normalize: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
	tool, transport, ok := unsafeTextualWriteAttempt("tool: edit_file\npath: README.md\nold_text: old\nnew_text: new\ndescription: update readme")
	if !ok || tool != "edit_file" || transport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("expected non-executing unsafe textual write classification, got tool=%q transport=%q ok=%v", tool, transport, ok)
	}
}

func TestRepairToolRequestSyntaxStringifiedSubmitPlanEdits(t *testing.T) {
	inner := `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`
	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	call := llama.ToolCall{Function: llama.ToolCallFunc{Name: "submit_plan", Arguments: string(payload)}}
	result := repairToolRequestSyntax(toolRequestSyntaxNormalization{
		Raw:            string(payload),
		ParseErr:       errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits"),
		ActiveToolCall: &call,
	})

	if result.ParseErr != nil {
		t.Fatalf("repair should clear parse error: %v", result.ParseErr)
	}
	if !result.OK || !result.Repaired {
		t.Fatalf("expected repaired normalization, got ok=%v repaired=%v", result.OK, result.Repaired)
	}
	if result.Request.Tool != "submit_plan" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "a.go" {
		t.Fatalf("unexpected repaired request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportStringifiedSubmitRepair {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestNormalizeToolRequestSyntaxRepairsTextualStringifiedSubmitPlanEditsBeforeReportingParse(t *testing.T) {
	inner := `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`
	payload, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: string(payload)})

	if result.ParseErr != nil {
		t.Fatalf("textual stringified edits repair should clear parse error: %v", result.ParseErr)
	}
	if !result.OK || !result.Repaired {
		t.Fatalf("expected repaired textual normalization, got ok=%v repaired=%v", result.OK, result.Repaired)
	}
	if result.Request.Tool != "submit_plan" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "a.go" {
		t.Fatalf("unexpected repaired request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportStringifiedSubmitRepair {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
	if result.ParserFailureClass != "" {
		t.Fatalf("repaired parse failure should not remain classified, got %q", result.ParserFailureClass)
	}
}

func TestRepairToolRequestSyntaxNestedArgumentsObject(t *testing.T) {
	payload := `{"arguments":{"tool":"read_file","path":"cmd/idiom/audit.go","start_line":1,"end_line":100}}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: payload})

	if result.ParseErr != nil {
		t.Fatalf("nested arguments repair should clear parse error: %v", result.ParseErr)
	}
	if !result.OK || !result.Repaired {
		t.Fatalf("expected repaired normalization, got ok=%v repaired=%v", result.OK, result.Repaired)
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "cmd/idiom/audit.go" || result.Request.StartLine != 1 || result.Request.EndLine != 100 {
		t.Fatalf("unexpected repaired request: %#v", result.Request)
	}
	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportNestedArguments {
		t.Fatalf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport)
	}
}

func TestRepairToolRequestSyntaxNestedArgumentsRejectsMixedWrapper(t *testing.T) {
	payload := `{"tool":"read_file","arguments":{"tool":"write_file","path":"README.md","content":"unsafe"}}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: payload})

	if result.OK && result.EventTransport == toolRequestNormalizerTransportNestedArguments {
		t.Fatalf("mixed wrapper must not be repaired through nested arguments: %#v", result.Request)
	}
}

func TestParseFunctionCallToolRequestSyntaxEmptyString(t *testing.T) {
	// Fix: empty content must not be classified as "unsafe_to_infer".
	// An empty string is a model producing no visible output, not a
	// failed function-call attempt. Classifying it as unsafe skips
	// the reasoning-content salvage paths that could recover the answer.
	req, ok, unsafe := parseFunctionCallToolRequestSyntax("")
	if ok {
		t.Fatalf("empty string must not parse as a valid tool request")
	}
	if unsafe {
		t.Fatalf("empty string must not be classified as unsafe: got unsafe=true, want unsafe=false")
	}
	if req.Tool != "" {
		t.Fatalf("empty string must produce zero-value ToolRequest, got tool=%q", req.Tool)
	}
}

func TestParseFunctionCallToolRequestSyntaxWhitespaceOnly(t *testing.T) {
	req, ok, unsafe := parseFunctionCallToolRequestSyntax("   \n  \t  ")
	if ok {
		t.Fatalf("whitespace-only must not parse as a valid tool request")
	}
	if unsafe {
		t.Fatalf("whitespace-only must not be classified as unsafe: got unsafe=true, want unsafe=false")
	}
	if req.Tool != "" {
		t.Fatalf("whitespace-only must produce zero-value ToolRequest, got tool=%q", req.Tool)
	}
}
