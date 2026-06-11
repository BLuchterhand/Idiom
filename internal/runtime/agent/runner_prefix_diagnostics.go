package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"idiom/internal/runtime/llama"
)

const modelCallPrefixHashVersion = "v1-leading-system-tools-parser"

func modelCallPrefixDiagnostics(prompt []llama.ChatMessage, opts llama.ChatOptions, parserContract string) map[string]any {
	prefix := leadingSystemPromptPrefix(prompt)
	toolSchemaHash := stableHash(opts.Tools)
	payload := map[string]any{
		"prefix_hash_version":          modelCallPrefixHashVersion,
		"prefix_hash":                  stableHash([]any{modelCallPrefixHashVersion, prefix, toolSchemaHash, opts.ToolChoice, parserContract}),
		"prompt_prefix_message_count":  len(prefix),
		"tool_schema_hash":             toolSchemaHash,
		"prefix_hash_components":       []string{"leading_system_messages", "tool_schemas", "tool_choice", "parser_contract"},
		"prefix_change_reason_fields":  []string{"system_prompt_changed", "tool_schema_changed", "tool_choice_changed", "parser_contract_changed"},
		"prefix_cache_metadata_scope":  "telemetry_only",
		"prefix_cache_signal_present":  false,
		"prefix_cache_signal_provider": "",
	}
	return payload
}

func leadingSystemPromptPrefix(prompt []llama.ChatMessage) []llama.ChatMessage {
	var prefix []llama.ChatMessage
	for _, msg := range prompt {
		if msg.Role != "system" {
			break
		}
		prefix = append(prefix, llama.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	return prefix
}

func stableHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte("{}")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
