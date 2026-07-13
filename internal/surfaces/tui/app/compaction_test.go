package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// Fix 5 (context compaction) — exercises docs/opencode-fixes-design.md §5.

var _ = ginkgo.It("maybe compact history below threshold is noop", func() {
	m := &Model{}
	history := make([]llama.ChatMessage, 5)

	for i := range history {
		history[i] = llama.ChatMessage{Role: "user", Content: "noop"}
	}

	got := m.MaybeCompactHistory(context.Background(), history)
	gomega.Expect(len(got)).To(gomega.Equal(len(history)), fmt.Sprintf("expected no-op for history below threshold, got %d (wanted %d)", len(got), len(history)))
})

var _ = ginkgo.It("maybe compact history folds old messages into digest", ginkgo.Label("development-scenario", "integration", "contract"), func() {
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

	gomega.Expect(calls).To(gomega.Equal(1), fmt.Sprintf("expected exactly 1 model call to summarize, got %d", calls))

	gomega.Expect(len(got) >= len(history)).To(gomega.BeFalse(), fmt.Sprintf("expected compacted history to be shorter than input, got %d (input %d)", len(got), len(history)))

	gomega.Expect(got[0].Role).To(gomega.Equal("system"), fmt.Sprintf("expected first message to be system digest, got %#v", got[0]))
	gomega.Expect(got[0].Content).To(gomega.ContainSubstring("Session digest"), fmt.Sprintf("expected first message to be system digest, got %#v", got[0]))
})

var _ = ginkgo.It("fallback session digest preserves first user goal", func() {
	oldest := []llama.ChatMessage{
		{Role: "user", Content: "goal: migrate the auth middleware"},
		{Role: "assistant", Content: "worked on it"},
		{Role: "system", Content: "tool result"},
	}
	digest := fallbackSessionDigest(oldest)

	gomega.Expect(strings.Contains(digest, "migrate the auth middleware")).To(gomega.BeTrue(), fmt.Sprintf("fallback digest must preserve first user goal, got:\n%s", digest))

	gomega.Expect(strings.Contains(digest, "1 assistant turns")).To(gomega.BeTrue(), fmt.Sprintf("fallback digest should summarize counts, got:\n%s", digest))
})

var _ = ginkgo.It("maybe compact history falls back when model call fails", func() {
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
	gomega.Expect(got[0].Role).To(gomega.Equal("system"), fmt.Sprintf("expected deterministic fallback digest preserving goal, got:\n%#v", got[0]))
	gomega.Expect(got[0].Content).To(gomega.ContainSubstring("add telemetry"), fmt.Sprintf("expected deterministic fallback digest preserving goal, got:\n%#v", got[0]))
})

var _ = ginkgo.It("slash compact command compacts transcript", func() {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original goal: fix short follow-up answers"},
			{Role: "assistant", Content: "I inspected the runner."},
			{Role: "operator", Content: "recover status: secret packet path"},
			{Role: "user", Content: "what about bounded read scoping?"},
		},
	}

	{
		quit := m.handleSlashCommand("/compact")
		gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("compact command must not quit the TUI"))
	}

	gomega.Expect(len(m.messages)).To(gomega.Equal(2), fmt.Sprintf("expected compacted transcript with digest plus latest message, got %#v", m.messages))

	gomega.Expect(m.messages[0].Role).To(gomega.Equal("system"), fmt.Sprintf("expected first message to be compact digest preserving original goal, got %#v", m.messages[0]))
	gomega.Expect(m.messages[0].Content).To(gomega.ContainSubstring("short follow-up answers"), fmt.Sprintf("expected first message to be compact digest preserving original goal, got %#v", m.messages[0]))

	gomega.Expect(m.messages[1].Content).To(gomega.Equal("what about bounded read scoping?"), fmt.Sprintf("expected latest message to remain verbatim, got %#v", m.messages[1]))

	for _, msg := range m.messages {
		gomega.Expect(strings.Contains(msg.Content, "secret packet path")).To(gomega.BeFalse(), fmt.Sprintf("operator-only content must not be compacted into model-visible transcript: %#v", m.messages))
	}

	gomega.Expect(strings.Contains(m.status, "Compacted conversation")).To(gomega.BeTrue(), fmt.Sprintf("expected compact status, got %q", m.status))
})

var _ = ginkgo.It("slash compact command rejects args and streaming", func() {
	m := &Model{}
	m.handleSlashCommand("/compact now")

	gomega.Expect(m.status).To(gomega.Equal("Usage: /compact"), fmt.Sprintf("expected usage for arguments, got %q", m.status))

	m = &Model{streaming: true}
	m.handleSlashCommand("/compact")

	gomega.Expect(m.status).To(gomega.Equal("A response is already in progress."), fmt.Sprintf("expected streaming guard, got %q", m.status))
})

var _ = ginkgo.It("context budget compaction scales with context size", func() {
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
		gomega.Expect(budget.CompactionThreshold).To(gomega.Equal(tc.wantThreshold), fmt.Sprintf("ctxSize=%d: threshold = %d, want %d", tc.ctxSize, budget.CompactionThreshold, tc.wantThreshold))

		gomega.Expect(budget.CompactionRecentKeep).To(gomega.Equal(tc.wantRecent), fmt.Sprintf("ctxSize=%d: recentToKeep = %d, want %d", tc.ctxSize, budget.CompactionRecentKeep, tc.wantRecent))

		// Invariants: recentToKeep must be at least 3 and at most threshold/2
		gomega.Expect(budget.CompactionRecentKeep < 3).To(gomega.BeFalse(), fmt.Sprintf("ctxSize=%d: recentToKeep %d < 3 (minimum)", tc.ctxSize, budget.CompactionRecentKeep))

		gomega.Expect(budget.CompactionRecentKeep > budget.CompactionThreshold/2).To(gomega.BeFalse(), fmt.Sprintf("ctxSize=%d: recentToKeep %d > threshold/2 (%d)", tc.ctxSize, budget.CompactionRecentKeep, budget.CompactionThreshold/2))
	}
})

var _ = ginkgo.It("compaction params for nil model", func() {
	// An execution with no model-derived context uses the 65536 default.
	threshold := compactionThreshold(compactionExecution{})
	wantThreshold := NewContextBudget(65536).CompactionThreshold

	gomega.Expect(threshold).To(gomega.Equal(wantThreshold), fmt.Sprintf("empty execution threshold = %d, want %d", threshold, wantThreshold))
})

var _ = ginkgo.It("compaction with restrictive max history", func() {
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
	gomega.Expect(len(got) >= len(history)).To(gomega.BeFalse(), fmt.Sprintf("expected compaction with restrictive MaxHistory, got same length %d", len(got)))

	gomega.Expect(got[0].Role).To(gomega.Equal("system"), fmt.Sprintf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content))
	gomega.Expect(got[0].Content).To(gomega.ContainSubstring("Session digest"), fmt.Sprintf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content))

	// Verify the digest actually contains content from the folded messages
	gomega.Expect(strings.Contains(got[0].Content, "refactor")).To(gomega.BeTrue(), fmt.Sprintf("digest must reference original goal, got:\n%s", got[0].Content))

	// MaxHistory=20, CtxSize=65536: context-derived recentToKeep=40 must scale
	// down to MaxHistory/2=10. Compacted result = 1 digest + 10 recent = 11.
	expectedLen := 1 + 10
	gomega.Expect(len(got)).To(gomega.Equal(expectedLen), fmt.Sprintf("expected %d messages (1 digest + 10 recent), got %d", expectedLen, len(got)))
})

var _ = ginkgo.It("compaction max history edge case", func() {
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
	gomega.Expect(len(got) >= len(history)).To(gomega.BeFalse(), fmt.Sprintf("expected compaction even with small overshoot, got same length %d", len(got)))

	gomega.Expect(got[0].Role).To(gomega.Equal("system"), fmt.Sprintf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content))
	gomega.Expect(got[0].Content).To(gomega.ContainSubstring("Session digest"), fmt.Sprintf("expected system digest, got role=%s content=%q", got[0].Role, got[0].Content))

	// 22 messages, MaxHistory=20, recentToKeep=10: 1 digest + 10 recent = 11.
	gomega.Expect(len(got)).To(gomega.Equal(11), fmt.Sprintf("expected 11 messages (1 digest + 10 recent), got %d", len(got)))
})

var _ = ginkgo.It("context budget conversation limits scale", func() {
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
		gomega.Expect(budget.ConvMaxMessages).To(gomega.Equal(tc.wantMsgs), fmt.Sprintf("ctxSize=%d: maxMsgs = %d, want %d", tc.ctxSize, budget.ConvMaxMessages, tc.wantMsgs))

		gomega.Expect(budget.ConvMaxChars).To(gomega.Equal(tc.wantChars), fmt.Sprintf("ctxSize=%d: maxChars = %d, want %d", tc.ctxSize, budget.ConvMaxChars, tc.wantChars))

		// Invariant: values must never exceed raw context capacity
		rawMsgs := tc.ctxSize / 125
		rawChars := tc.ctxSize * 4

		gomega.Expect(budget.ConvMaxMessages > rawMsgs).To(gomega.BeFalse(), fmt.Sprintf("ctxSize=%d: maxMsgs %d exceeds raw capacity %d", tc.ctxSize, budget.ConvMaxMessages, rawMsgs))

		gomega.Expect(budget.ConvMaxChars > rawChars).To(gomega.BeFalse(), fmt.Sprintf("ctxSize=%d: maxChars %d exceeds raw capacity %d", tc.ctxSize, budget.ConvMaxChars, rawChars))
	}
})

var _ = ginkgo.It("compaction params recent to keep clamped by threshold", func() {
	// Verify the recentToKeep <= threshold/2 invariant holds across realistic
	// context window sizes. The clamp is a defensive guard.
	for _, tc := range []struct{ ctxSize int }{
		{2000}, {4096}, {8192}, {16384}, {32768}, {65536}, {131072},
	} {
		budget := NewContextBudget(tc.ctxSize)
		gomega.Expect(budget.CompactionRecentKeep > budget.CompactionThreshold/2).To(gomega.BeFalse(), fmt.Sprintf("ctxSize=%d: recentToKeep %d > threshold/2 %d — clamp failed",
			tc.ctxSize, budget.CompactionRecentKeep, budget.CompactionThreshold/2))
	}
})

var _ = ginkgo.It("slash help documents compact command", func() {
	m := &Model{}
	m.slashHelp("/help", []string{"/help"})

	gomega.Expect(strings.Contains(m.status, "/compact")).To(gomega.BeTrue(), fmt.Sprintf("help must document compact command, got %q", m.status))
})
