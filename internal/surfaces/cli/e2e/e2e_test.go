package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestE2EPromptMetadataDoesNotLeakRawPrompt(t *testing.T) {
	const secret = "RAW_PROMPT_SHOULD_NOT_LEAK"

	payload := map[string]any{
		"turns": e2ePromptMetadataList([]string{"first", secret}),
		"user":  e2ePromptMetadata(secret),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	if strings.Contains(string(data), secret) {
		t.Fatalf("metadata leaked raw prompt: %s", data)
	}
	if !strings.Contains(string(data), "sha256:") {
		t.Fatalf("metadata should include prompt hash: %s", data)
	}
	if !strings.Contains(string(data), `"content_length":`) {
		t.Fatalf("metadata should include prompt length: %s", data)
	}
}
