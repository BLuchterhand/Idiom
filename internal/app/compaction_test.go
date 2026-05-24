package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/llama"
)

// Fix 5 (context compaction) — exercises docs/opencode-fixes-design.md §5.

func TestMaybeCompactHistoryBelowThresholdIsNoop(t *testing.T) {
	m := &Model{}
	history := make([]llama.ChatMessage, 5)
	for i := range history {
		history[i] = llama.ChatMessage{Role: "user", Content: "noop"}
	}
	got := m.MaybeCompactHistory(context.Background(), history)
	if len(got) != len(history) {
		t.Fatalf("expected no-op for history below threshold, got %d (wanted %d)", len(got), len(history))
	}
}

func TestMaybeCompactHistoryFoldsOldMessagesIntoDigest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "User asked to refactor app.go. Read internal/app/app.go. No edits yet. Open question: which helper to extract."},
			}},
		})
	}))
	defer server.Close()

	m := &Model{
		client: llama.NewClient(server.URL, time.Second),
	}
	history := make([]llama.ChatMessage, 0, 40)
	history = append(history, llama.ChatMessage{Role: "user", Content: "original goal: refactor app.go"})
	for i := 0; i < 39; i++ {
		history = append(history, llama.ChatMessage{Role: "assistant", Content: "progress"})
	}

	got := m.MaybeCompactHistory(context.Background(), history)
	if calls != 1 {
		t.Fatalf("expected exactly 1 model call to summarize, got %d", calls)
	}
	if len(got) >= len(history) {
		t.Fatalf("expected compacted history to be shorter than input, got %d (input %d)", len(got), len(history))
	}
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "Session digest") {
		t.Fatalf("expected first message to be system digest, got %#v", got[0])
	}
}

func TestFallbackSessionDigestPreservesFirstUserGoal(t *testing.T) {
	oldest := []llama.ChatMessage{
		{Role: "user", Content: "goal: migrate the auth middleware"},
		{Role: "assistant", Content: "worked on it"},
		{Role: "system", Content: "tool result"},
	}
	digest := fallbackSessionDigest(oldest)
	if !strings.Contains(digest, "migrate the auth middleware") {
		t.Fatalf("fallback digest must preserve first user goal, got:\n%s", digest)
	}
	if !strings.Contains(digest, "1 assistant turns") {
		t.Fatalf("fallback digest should summarize counts, got:\n%s", digest)
	}
}

func TestMaybeCompactHistoryFallsBackWhenModelCallFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	m := &Model{
		client: llama.NewClient(server.URL, time.Second),
	}
	history := make([]llama.ChatMessage, 0, 40)
	history = append(history, llama.ChatMessage{Role: "user", Content: "goal: add telemetry"})
	for i := 0; i < 39; i++ {
		history = append(history, llama.ChatMessage{Role: "assistant", Content: "work"})
	}
	got := m.MaybeCompactHistory(context.Background(), history)
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "add telemetry") {
		t.Fatalf("expected deterministic fallback digest preserving goal, got:\n%#v", got[0])
	}
}

func TestSlashCompactCommandCompactsTranscript(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original goal: fix short follow-up answers"},
			{Role: "assistant", Content: "I inspected the runner."},
			{Role: "operator", Content: "recover status: secret packet path"},
			{Role: "user", Content: "what about bounded read scoping?"},
		},
	}

	if quit := m.handleSlashCommand("/compact"); quit {
		t.Fatal("compact command must not quit the TUI")
	}
	if len(m.messages) != 2 {
		t.Fatalf("expected compacted transcript with digest plus latest message, got %#v", m.messages)
	}
	if m.messages[0].Role != "system" || !strings.Contains(m.messages[0].Content, "short follow-up answers") {
		t.Fatalf("expected first message to be compact digest preserving original goal, got %#v", m.messages[0])
	}
	if m.messages[1].Content != "what about bounded read scoping?" {
		t.Fatalf("expected latest message to remain verbatim, got %#v", m.messages[1])
	}
	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "secret packet path") {
			t.Fatalf("operator-only content must not be compacted into model-visible transcript: %#v", m.messages)
		}
	}
	if !strings.Contains(m.status, "Compacted conversation") {
		t.Fatalf("expected compact status, got %q", m.status)
	}
}

func TestSlashCompactCommandRejectsArgsAndStreaming(t *testing.T) {
	m := &Model{}
	m.handleSlashCommand("/compact now")
	if m.status != "Usage: /compact" {
		t.Fatalf("expected usage for arguments, got %q", m.status)
	}

	m = &Model{streaming: true}
	m.handleSlashCommand("/compact")
	if m.status != "A response is already in progress." {
		t.Fatalf("expected streaming guard, got %q", m.status)
	}
}

func TestSlashHelpDocumentsCompactCommand(t *testing.T) {
	m := &Model{}
	m.slashHelp("/help", []string{"/help"})
	if !strings.Contains(m.status, "/compact") {
		t.Fatalf("help must document compact command, got %q", m.status)
	}
}
