package app

import (
	"context"
	"fmt"
	"strings"

	"qwen-bubbles/internal/llama"
)

// Fix 5 (context compaction) — when session history exceeds a threshold,
// summarize the oldest messages into a single `session_digest` system
// message so long-horizon intent survives window pressure. See
// docs/opencode-fixes-design.md §5.

// defaultCompactionThreshold is the message count that triggers compaction
// when m.cfg.Chat.MaxHistory leaves no lower bound. Chosen to keep
// compaction from firing on short sessions yet triggering well before
// Qwen's 65k ctx fills.
const defaultCompactionThreshold = 30

// recentMessagesToKeepVerbatim is the number of most-recent messages that
// are never folded into the digest — the model still sees them word-for-
// word so immediate local context is preserved.
const recentMessagesToKeepVerbatim = 12

const compactionSystemPromptPreamble = "You are producing a compact session digest. Preserve only the facts that the next turn needs to act correctly: the user's original goals, any hard constraints or boundaries, files and paths that have been read or edited, outcomes of tool calls, open questions, and the last known blocker. Write plain prose in 12 lines or fewer. Do not invent facts. Do not request tools."

// MaybeCompactHistory applies Fix 5 when the history length exceeds the
// configured threshold. It returns a new history whose leading entries have
// been replaced by a single `session_digest` system message. The most recent
// recentMessagesToKeepVerbatim messages are preserved verbatim.
//
// Safe for nil clients: if the model call fails or the client is absent, a
// deterministic fallback digest (regex-shape extraction) is used so the
// compaction never silently loses the user's original intent.
func (m *Model) MaybeCompactHistory(ctx context.Context, history []llama.ChatMessage) []llama.ChatMessage {
	threshold := m.compactionThreshold()
	if len(history) <= threshold {
		return history
	}
	keepFrom := len(history) - recentMessagesToKeepVerbatim
	if keepFrom < 0 {
		keepFrom = 0
	}
	oldest := history[:keepFrom]
	if len(oldest) == 0 {
		return history
	}
	digest := m.buildSessionDigest(ctx, oldest)
	if strings.TrimSpace(digest) == "" {
		return history
	}
	compacted := make([]llama.ChatMessage, 0, 1+len(history)-keepFrom)
	compacted = append(compacted, llama.ChatMessage{Role: "system", Content: digest})
	compacted = append(compacted, history[keepFrom:]...)
	m.logMessage("session_digest", fmt.Sprintf("compacted %d messages into digest (%d chars); kept %d verbatim", len(oldest), len(digest), len(history)-keepFrom))
	return compacted
}

func (m *Model) compactionThreshold() int {
	if m == nil {
		return defaultCompactionThreshold
	}
	// If MaxHistory is set, stay above it so compaction kicks in before
	// requestMessages' hard-truncate would have. Otherwise use the default.
	if limit := m.cfg.Chat.MaxHistory; limit > 0 {
		if limit > defaultCompactionThreshold {
			return limit
		}
		return defaultCompactionThreshold
	}
	return defaultCompactionThreshold
}

func (m *Model) buildSessionDigest(ctx context.Context, oldest []llama.ChatMessage) string {
	if m == nil || m.client == nil {
		return fallbackSessionDigest(oldest)
	}
	prompt := renderCompactionPrompt(oldest)
	reply, err := m.client.CompleteChat(ctx, []llama.ChatMessage{
		{Role: "system", Content: compactionSystemPromptPreamble},
		{Role: "user", Content: prompt},
	}, m.chatOptions())
	if err != nil {
		return fallbackSessionDigest(oldest)
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallbackSessionDigest(oldest)
	}
	return "Session digest (prior history compacted):\n" + reply
}

func renderCompactionPrompt(oldest []llama.ChatMessage) string {
	var b strings.Builder
	b.WriteString("Summarize this prior conversation into a compact digest. Follow the system preamble rules.\n\n")
	for _, msg := range oldest {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if len(content) > 800 {
			content = content[:800] + "…"
		}
		fmt.Fprintf(&b, "[%s] %s\n", role, content)
	}
	return b.String()
}

// fallbackSessionDigest produces a deterministic digest when the model call
// is unavailable. It preserves the first user prompt verbatim (the
// load-bearing intent) plus a one-line count of everything else.
func fallbackSessionDigest(oldest []llama.ChatMessage) string {
	var firstUser string
	assistantCount := 0
	toolResultCount := 0
	for _, msg := range oldest {
		switch strings.TrimSpace(msg.Role) {
		case "user":
			if firstUser == "" {
				firstUser = strings.TrimSpace(msg.Content)
			}
		case "assistant":
			assistantCount++
		case "system", "tool_result":
			toolResultCount++
		}
	}
	if firstUser == "" {
		return ""
	}
	if len(firstUser) > 600 {
		firstUser = firstUser[:600] + "…"
	}
	return fmt.Sprintf("Session digest (model summarization unavailable — deterministic fallback):\nOriginal user goal: %s\nEarlier conversation: %d assistant turns, %d tool/system messages (content dropped to stay within context).", firstUser, assistantCount, toolResultCount)
}
