package agent

import (
	"strings"
	"testing"

	"idiom/internal/llama"
)

// TestQwenKeyValueSimpleFormat verifies the exact format Qwen emits
// (from live curl test) parses through the normalizer.
func TestQwenKeyValueSimpleFormat(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: internal/comprehension/pack.go\n",
	})
	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value tool request to normalize")
	}
	if result.Request.Tool != "read_file" || result.Request.Path != "internal/comprehension/pack.go" {
		t.Fatalf("unexpected request: tool=%q path=%q", result.Request.Tool, result.Request.Path)
	}
	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("expected key_value transport, got %q", result.EventTransport)
	}
}

func TestQwenKeyValueEditFileFormat(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			`old_text: return "Save changes"`,
			`new_text: return "Save"`,
			"description: update label",
		}, "\n"),
	})
	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value edit_file request to normalize")
	}
	if result.Request.Tool != "edit_file" || result.Request.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected request: tool=%q path=%q", result.Request.Tool, result.Request.Path)
	}
	if result.Request.OldText != `return "Save changes"` || result.Request.NewText != `return "Save"` {
		t.Fatalf("edit anchors not parsed: old=%q new=%q", result.Request.OldText, result.Request.NewText)
	}
	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("expected key_value transport, got %q", result.EventTransport)
	}
}

func TestQwenKeyValueEditFilePreservesBracesAndIndentation(t *testing.T) {
	oldText := strings.Join([]string{
		"func label() {",
		"    return \"Save changes\"",
		"}",
	}, "\n")
	newText := strings.Join([]string{
		"func label() {",
		"    return \"Save\"",
		"}",
	}, "\n")
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			"old_text: func label() {",
			"    return \"Save changes\"",
			"}",
			"new_text: func label() {",
			"    return \"Save\"",
			"}",
			"description: update label",
		}, "\n"),
	})
	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value edit_file request with braces to normalize")
	}
	if result.Request.OldText != oldText {
		t.Fatalf("old_text not preserved:\nwant: %q\ngot:  %q", oldText, result.Request.OldText)
	}
	if result.Request.NewText != newText {
		t.Fatalf("new_text not preserved:\nwant: %q\ngot:  %q", newText, result.Request.NewText)
	}
}

func TestQwenKeyValueEditFilePreservesAllowedKeyLookingAnchorLines(t *testing.T) {
	oldText := strings.Join([]string{
		"metadata := []string{",
		"path: internal/foo",
		"content: old value",
		"}",
	}, "\n")
	newText := strings.Join([]string{
		"metadata := []string{",
		"path: internal/foo",
		"content: new value",
		"}",
	}, "\n")
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			"old_text: metadata := []string{",
			"path: internal/foo",
			"content: old value",
			"}",
			"new_text: metadata := []string{",
			"path: internal/foo",
			"content: new value",
			"}",
			"description: update metadata",
		}, "\n"),
	})
	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value edit_file request with key-looking anchor lines to normalize")
	}
	if result.Request.OldText != oldText {
		t.Fatalf("old_text not preserved:\nwant: %q\ngot:  %q", oldText, result.Request.OldText)
	}
	if result.Request.NewText != newText {
		t.Fatalf("new_text not preserved:\nwant: %q\ngot:  %q", newText, result.Request.NewText)
	}
}

func TestQwenKeyValueEditFileAllowsEmptyNewTextDeletion(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			`old_text: return "Save changes"`,
			"new_text:",
			"description: delete label",
		}, "\n"),
	})
	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value edit_file deletion request to normalize")
	}
	if result.Request.OldText != `return "Save changes"` {
		t.Fatalf("old_text not parsed: %q", result.Request.OldText)
	}
	if result.Request.NewText != "" {
		t.Fatalf("empty new_text should be preserved, got %q", result.Request.NewText)
	}
	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		t.Fatalf("expected key_value transport, got %q", result.EventTransport)
	}
}

// TestQwenKeyValueMultiCallParsesFirst verifies that when Qwen emits
// two tool: blocks in one reply, only the first is parsed.
func TestQwenKeyValueMultiCallParsesFirst(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: read_file",
			"path: internal/comprehension/pack.go",
			"lines: 1-30",
			"",
			"tool: search_content",
			"pattern: Packet",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value tool request to normalize (first tool only)")
	}
	if result.Request.Tool != "read_file" {
		t.Fatalf("expected first tool (read_file), got %q", result.Request.Tool)
	}
	if result.Request.Path != "internal/comprehension/pack.go" {
		t.Fatalf("expected path from first tool, got %q", result.Request.Path)
	}
	if result.Request.StartLine != 1 || result.Request.EndLine != 30 {
		t.Fatalf("expected lines 1-30, got start=%d end=%d", result.Request.StartLine, result.Request.EndLine)
	}
}

// TestQwenKeyValuePurposeField verifies the purpose field (which
// Qwen naturally emits) is accepted and mapped.
func TestQwenKeyValuePurposeField(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: context_pack",
			"task: find handlers",
			"mode: plan",
			"limit: 10",
			"purpose: architecture",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value tool request to normalize")
	}
	if result.Request.Tool != "context_pack" || result.Request.Purpose != "architecture" {
		t.Fatalf("unexpected request: tool=%q purpose=%q", result.Request.Tool, result.Request.Purpose)
	}
	if result.Request.Task != "find handlers" || result.Request.Mode != "plan" || result.Request.Limit != 10 {
		t.Fatalf("unexpected fields: task=%q mode=%q limit=%d", result.Request.Task, result.Request.Mode, result.Request.Limit)
	}
}

// TestQwenKeyValuePromptWithColon verifies that a prompt value
// containing a colon (e.g. "Evidence": ...) preserves bounded
// continuation lines for known long-text fields.
func TestQwenKeyValuePromptWithColon(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: task",
			"agent: explorer",
			"mode: read_only",
			`prompt: Inspect the idiom harness to define "Evidence": where it is declared`,
			"and how it is consumed",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value tool request to normalize despite continuation lines")
	}
	if result.Request.Tool != "task" || result.Request.Agent != "explorer" || result.Request.Mode != "read_only" {
		t.Fatalf("unexpected request: tool=%q agent=%q mode=%q", result.Request.Tool, result.Request.Agent, result.Request.Mode)
	}
	if !strings.Contains(result.Request.Prompt, `Inspect the idiom harness to define "Evidence"`) {
		t.Fatalf("prompt not captured correctly: %q", result.Request.Prompt)
	}
	if !strings.Contains(result.Request.Prompt, "and how it is consumed") {
		t.Fatalf("prompt continuation not captured correctly: %q", result.Request.Prompt)
	}
}

func TestQwenKeyValueScalarContinuationIsIgnored(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: read_file",
			"path: internal/comprehension/pack.go",
			"this line must not be appended to the path",
			"start_line: 1",
			"end_line: 10",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected key-value tool request to normalize")
	}
	if result.Request.Path != "internal/comprehension/pack.go" {
		t.Fatalf("scalar continuation changed path: %q", result.Request.Path)
	}
	if result.Request.StartLine != 1 || result.Request.EndLine != 10 {
		t.Fatalf("line range not parsed: start=%d end=%d", result.Request.StartLine, result.Request.EndLine)
	}
}

func TestQwenKeyValueNotePreservesKeyAndMultilineContent(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: note",
			"key: source-finding",
			"content: First source fact.",
			"Second source fact.",
			"internal/agent/runner.go:2039 shows note handling.",
			"Evidence: note content can contain colon-bearing labels.",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected note key-value request to normalize")
	}
	if result.Request.Tool != "note" || result.Request.Key != "source-finding" {
		t.Fatalf("unexpected note request: %#v", result.Request)
	}
	if !strings.Contains(result.Raw, `"key":"source-finding"`) {
		t.Fatalf("raw request dropped note key: %s", result.Raw)
	}
	if !strings.Contains(result.Request.Content, "Second source fact.") {
		t.Fatalf("note content continuation missing: %q", result.Request.Content)
	}
	if !strings.Contains(result.Request.Content, "internal/agent/runner.go:2039") {
		t.Fatalf("file:line continuation missing: %q", result.Request.Content)
	}
	if !strings.Contains(result.Request.Content, "Evidence: note content") {
		t.Fatalf("colon label continuation missing: %q", result.Request.Content)
	}
}

func TestQwenKeyValuePromptPreservesUnknownColonContinuation(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: task",
			"agent: explorer",
			"mode: read_only",
			"prompt: Inspect the completion gate.",
			"Evidence: include the current rejection order.",
			"internal/agent/runner.go:6658 is the call site.",
		}, "\n"),
	})

	if result.ParseErr != nil {
		t.Fatalf("parse error: %v", result.ParseErr)
	}
	if !result.OK {
		t.Fatal("expected task key-value request to normalize")
	}
	if !strings.Contains(result.Request.Prompt, "Evidence: include") {
		t.Fatalf("prompt dropped evidence label continuation: %q", result.Request.Prompt)
	}
	if !strings.Contains(result.Request.Prompt, "internal/agent/runner.go:6658") {
		t.Fatalf("prompt dropped file:line continuation: %q", result.Request.Prompt)
	}
}
