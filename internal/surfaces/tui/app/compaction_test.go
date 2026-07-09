package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
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
				"message": map[string]string{"content": "User asked to refactor app.go. Read internal/surfaces/tui/app/app.go. No edits yet. Open question: which helper to extract."},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		client: llama.NewClient(server.URL, time.Second),
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 10000},
		},
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
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 10000},
		},
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

func TestContextBudgetCompactionScalesWithContextSize(t *testing.T) {
	tests := []struct {
		ctxSize       int
		wantThreshold int
		wantRecent    int
	}{
		{ctxSize: 32768, wantThreshold: 104, wantRecent: 40},  // 32K model (recent capped at 40)
		{ctxSize: 65536, wantThreshold: 200, wantRecent: 40},  // 64K model (both capped)
		{ctxSize: 131072, wantThreshold: 200, wantRecent: 40}, // 128K model (capped at max)
		{ctxSize: 8192, wantThreshold: 26, wantRecent: 13},    // 8K model
		{ctxSize: 4096, wantThreshold: 13, wantRecent: 6},     // 4K model
		{ctxSize: 2000, wantThreshold: 10, wantRecent: 3},     // tiny model (clamped at floor)
	}

	for _, tc := range tests {
		budget := NewContextBudget(tc.ctxSize)
		if budget.CompactionThreshold != tc.wantThreshold {
			t.Errorf("ctxSize=%d: threshold = %d, want %d", tc.ctxSize, budget.CompactionThreshold, tc.wantThreshold)
		}

		if budget.CompactionRecentKeep != tc.wantRecent {
			t.Errorf("ctxSize=%d: recentToKeep = %d, want %d", tc.ctxSize, budget.CompactionRecentKeep, tc.wantRecent)
		}

		// Invariants: recentToKeep must be at least 3 and at most threshold/2
		if budget.CompactionRecentKeep < 3 {
			t.Errorf("ctxSize=%d: recentToKeep %d < 3 (minimum)", tc.ctxSize, budget.CompactionRecentKeep)
		}

		if budget.CompactionRecentKeep > budget.CompactionThreshold/2 {
			t.Errorf("ctxSize=%d: recentToKeep %d > threshold/2 (%d)", tc.ctxSize, budget.CompactionRecentKeep, budget.CompactionThreshold/2)
		}
	}
}

func TestCompactionParamsForNilModel(t *testing.T) {
	// A nil Model should get default thresholds from 65536 ctx
	var m *Model

	threshold := m.compactionThreshold()
	wantThreshold := NewContextBudget(65536).CompactionThreshold

	if threshold != wantThreshold {
		t.Errorf("nil Model threshold = %d, want %d", threshold, wantThreshold)
	}
}

func TestCompactionWithRestrictiveMaxHistory(t *testing.T) {
	// Simulate the common case: a large-context model (64K+) with the
	// default MaxHistory=20. When MaxHistory is more restrictive than
	// the context-derived threshold, compaction must still succeed —
	// recentToKeep must scale down so keepFrom stays positive and the
	// digest actually folds messages instead of silently returning "too_short".
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Prior conversation: user asked to refactor."},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		client: llama.NewClient(server.URL, time.Second),
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 65536},
			Chat:        config.Chat{MaxHistory: 20},
		},
	}
	// 25 messages: above MaxHistory=20, below context-derived threshold of 200.
	// With the fix, recentToKeep scales to 10, so keepFrom = 25-10 = 15 > 0.
	history := make([]llama.ChatMessage, 0, 25)

	history = append(history, llama.ChatMessage{Role: "user", Content: "goal: refactor"})

	for i := 0; i < 24; i++ {
		history = append(history, llama.ChatMessage{Role: "assistant", Content: "turn"})
	}

	got := m.MaybeCompactHistory(context.Background(), history)
	// The history should be compacted, not silently returned unchanged.
	if len(got) >= len(history) {
		t.Fatalf("expected compaction with restrictive MaxHistory, got same length %d", len(got))
	}

	if got[0].Role != "system" || !strings.Contains(got[0].Content, "Session digest") {
		t.Fatalf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content)
	}

	// Verify the digest actually contains content from the folded messages
	if !strings.Contains(got[0].Content, "refactor") {
		t.Fatalf("digest must reference original goal, got:\n%s", got[0].Content)
	}

	// MaxHistory=20, CtxSize=65536: context-derived recentToKeep=40 must scale
	// down to MaxHistory/2=10. Compacted result = 1 digest + 10 recent = 11.
	expectedLen := 1 + 10
	if len(got) != expectedLen {
		t.Fatalf("expected %d messages (1 digest + 10 recent), got %d", expectedLen, len(got))
	}
}

func TestCompactionMaxHistoryEdgeCase(t *testing.T) {
	// Small history just above MaxHistory: verify keepFrom stays positive
	// even when recentToKeep would otherwise exceed the history length.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Prior conversation: user asked to refactor."},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		client: llama.NewClient(server.URL, time.Second),
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 65536},
			Chat:        config.Chat{MaxHistory: 20},
		},
	}
	// 22 messages: just above MaxHistory. recentToKeep should scale to 10,
	// giving keepFrom = 22-10 = 12 > 0. Compaction should succeed.
	history := make([]llama.ChatMessage, 0, 22)

	history = append(history, llama.ChatMessage{Role: "user", Content: "goal: refactor"})

	for i := 0; i < 21; i++ {
		history = append(history, llama.ChatMessage{Role: "assistant", Content: "turn"})
	}

	got := m.MaybeCompactHistory(context.Background(), history)
	if len(got) >= len(history) {
		t.Fatalf("expected compaction even with small overshoot, got same length %d", len(got))
	}

	if got[0].Role != "system" || !strings.Contains(got[0].Content, "Session digest") {
		t.Fatalf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content)
	}

	// 22 messages, MaxHistory=20, recentToKeep=10: 1 digest + 10 recent = 11.
	if len(got) != 11 {
		t.Fatalf("expected 11 messages (1 digest + 10 recent), got %d", len(got))
	}
}

func TestContextBudgetConversationLimitsScale(t *testing.T) {
	tests := []struct {
		ctxSize   int
		wantMsgs  int
		wantChars int
	}{
		{ctxSize: 32768, wantMsgs: 209, wantChars: 104857},
		{ctxSize: 65536, wantMsgs: 419, wantChars: 209715},
		{ctxSize: 8192, wantMsgs: 52, wantChars: 26214},
		{ctxSize: 4096, wantMsgs: 26, wantChars: 13107},
		{ctxSize: 2000, wantMsgs: 12, wantChars: 6400},
	}
	for _, tc := range tests {
		budget := NewContextBudget(tc.ctxSize)
		if budget.ConvMaxMessages != tc.wantMsgs {
			t.Errorf("ctxSize=%d: maxMsgs = %d, want %d", tc.ctxSize, budget.ConvMaxMessages, tc.wantMsgs)
		}

		if budget.ConvMaxChars != tc.wantChars {
			t.Errorf("ctxSize=%d: maxChars = %d, want %d", tc.ctxSize, budget.ConvMaxChars, tc.wantChars)
		}

		// Invariant: values must never exceed raw context capacity
		rawMsgs := tc.ctxSize / 125
		rawChars := tc.ctxSize * 4

		if budget.ConvMaxMessages > rawMsgs {
			t.Errorf("ctxSize=%d: maxMsgs %d exceeds raw capacity %d", tc.ctxSize, budget.ConvMaxMessages, rawMsgs)
		}

		if budget.ConvMaxChars > rawChars {
			t.Errorf("ctxSize=%d: maxChars %d exceeds raw capacity %d", tc.ctxSize, budget.ConvMaxChars, rawChars)
		}
	}
}

func TestCompactionParamsRecentToKeepClampedByThreshold(t *testing.T) {
	// Verify the recentToKeep <= threshold/2 invariant holds across realistic
	// context window sizes. The clamp is a defensive guard.
	for _, tc := range []struct{ ctxSize int }{
		{2000}, {4096}, {8192}, {16384}, {32768}, {65536}, {131072},
	} {
		budget := NewContextBudget(tc.ctxSize)
		if budget.CompactionRecentKeep > budget.CompactionThreshold/2 {
			t.Errorf("ctxSize=%d: recentToKeep %d > threshold/2 %d — clamp failed",
				tc.ctxSize, budget.CompactionRecentKeep, budget.CompactionThreshold/2)
		}
	}
}

func TestSlashHelpDocumentsCompactCommand(t *testing.T) {
	m := &Model{}
	m.slashHelp("/help", []string{"/help"})

	if !strings.Contains(m.status, "/compact") {
		t.Fatalf("help must document compact command, got %q", m.status)
	}
}
