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
// containing a colon (e.g. "Evidence": ...) is handled gracefully.
// Unparseable continuation lines after the first colon are skipped
// rather than causing unsafe_to_infer.
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
}
