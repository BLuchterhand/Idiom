package agent

import (
	"encoding/json"
	"testing"

	"idiom/internal/runtime/llama"
)

func TestModelCallPrefixDiagnosticsHashIsStableForSameInputs(t *testing.T) {
	prompt := []llama.ChatMessage{
		{Role: "system", Content: "Use the workspace tools."},
		{Role: "user", Content: "Please edit README."},
	}
	opts := llama.ChatOptions{
		ToolChoice: "auto",
		Tools: []llama.Tool{{
			Type: "function",
			Function: llama.ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	}

	first := modelCallPrefixDiagnostics(prompt, opts, "textual_tool")
	second := modelCallPrefixDiagnostics(prompt, opts, "textual_tool")

	if first["prefix_hash"] != second["prefix_hash"] {
		t.Fatalf("prefix hash changed for identical inputs: %#v vs %#v", first, second)
	}

	if first["prefix_cache_metadata_scope"] != "telemetry_only" || first["prefix_cache_signal_present"] != false {
		t.Fatalf("cache diagnostics must remain bounded telemetry, got %#v", first)
	}
}

func TestModelCallPrefixDiagnosticsHashChangesForPrefixAndToolSchema(t *testing.T) {
	basePrompt := []llama.ChatMessage{{Role: "system", Content: "Use tools."}, {Role: "user", Content: "Edit."}}
	baseOpts := llama.ChatOptions{
		Tools: []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
	}
	base := modelCallPrefixDiagnostics(basePrompt, baseOpts, "textual_tool")

	promptChanged := modelCallPrefixDiagnostics(
		[]llama.ChatMessage{{Role: "system", Content: "Use tools carefully."}, {Role: "user", Content: "Edit."}},
		baseOpts,
		"textual_tool",
	)
	if base["prefix_hash"] == promptChanged["prefix_hash"] {
		t.Fatalf("prefix hash did not change after system prompt changed")
	}

	toolChangedOpts := llama.ChatOptions{
		Tools: []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
	}
	toolChanged := modelCallPrefixDiagnostics(basePrompt, toolChangedOpts, "textual_tool")

	if base["prefix_hash"] == toolChanged["prefix_hash"] {
		t.Fatalf("prefix hash did not change after tool schema changed")
	}
}

func TestModelCallTelemetryIncludesPrefixDiagnostics(t *testing.T) {
	var event RunEvent

	runner := &Runner{
		OnEvent: func(e RunEvent) {
			event = e
		},
	}
	runner.emitModelCallTraceWithOptions(
		1,
		"main_loop",
		[]llama.ChatMessage{{Role: "system", Content: "Use tools."}, {Role: "user", Content: "Edit."}},
		llama.ChatMessage{Content: "done"},
		12,
		llama.ChatOptions{},
		false,
		nil,
	)

	if event.Role != "model_call" {
		t.Fatalf("event role = %q, want model_call", event.Role)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode model_call payload: %v", err)
	}

	for _, key := range []string{"prefix_hash", "prefix_hash_version", "tool_schema_hash", "prefix_hash_components", "prefix_cache_metadata_scope"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("model_call telemetry missing %q: %#v", key, payload)
		}
	}

	if payload["prefix_cache_metadata_scope"] != "telemetry_only" {
		t.Fatalf("prefix cache metadata should remain telemetry-only: %#v", payload)
	}
}
