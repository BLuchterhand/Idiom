package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/runtime/llama"
)

// Fix 5 (context compaction) — when session history exceeds a threshold,
// summarize the oldest messages into a single `session_digest` system
// message so long-horizon intent survives window pressure. See
// docs/opencode-fixes-design.md §5.

// Compaction thresholds are now derived automatically from the model's
// context-window size (LlamaServer.CtxSize). See compactionContextSize()
// and compactionParams() below. No per-model tuning is needed — a model
// with a 32K context window will compact earlier than one with a 128K
// window, proportionally.

const compactionSystemPromptPreamble = "You are producing a compact session digest. Preserve only the facts that the next turn needs to act correctly: the user's original goals, any hard constraints or boundaries, files and paths that have been read or edited, outcomes of tool calls, open questions, and the last known blocker. Write plain prose in 12 lines or fewer. Do not invent facts. Do not request tools."

// compactionContextSize returns the context window size in tokens used for
// deriving compaction thresholds. It reads from the main model's LlamaServer
// config and falls back through available sources.
func (m *Model) compactionContextSize() int {
	if m == nil {
		return 65536
	}
	if sz := m.cfg.LlamaServer.CtxSize; sz > 0 {
		return sz
	}
	if sz := m.cfg.RouteClassifier.CtxSize; sz > 0 {
		return sz
	}
	return 65536
}

// compactionParams derives message-count thresholds from the model's context
// window size. Returns (triggerThreshold, recentMessagesToKeep).
//
// The derivation uses a conservative chars-per-token estimate so models with
// different context-window sizes get proportional thresholds automatically —
// no per-model tuning needed. A 32K model compacts earlier than a 128K model.
func compactionParams(ctxSize int) (threshold int, recentToKeep int) {
	// Rough estimate: ~4 chars per token, ~500 chars per message avg
	// => ~125 tokens per message.
	const estTokensPerMsg = 125

	// Fire compaction when history occupies ~40% of context window.
	// Keep ~20% of context window as recent verbatim messages.
	thresholdTokens := ctxSize * 40 / 100
	recentTokens := ctxSize * 20 / 100

	threshold = thresholdTokens / estTokensPerMsg
	recentToKeep = recentTokens / estTokensPerMsg

	// Clamp to sensible bounds.
	if threshold < 10 {
		threshold = 10
	}
	if recentToKeep < 3 {
		recentToKeep = 3
	}
	// recentToKeep must not exceed half the threshold — otherwise compaction
	// would fold almost nothing.
	if recentToKeep > threshold/2 {
		recentToKeep = threshold / 2
	}
	// Cap at reasonable maxima even for huge context windows.
	if threshold > 200 {
		threshold = 200
	}
	if recentToKeep > 40 {
		recentToKeep = 40
	}

	return threshold, recentToKeep
}

// runnerContextLimitsForConversation returns generous (MaxContextMessages,
// MaxContextChars) for conversation/read-only lanes where the model should not
// be artificially limited by message count or character budgets. Values are
// derived from the context window so they scale with model size.
func runnerContextLimitsForConversation(ctxSize int) (maxMsgs int, maxChars int) {
	if ctxSize <= 0 {
		ctxSize = 65536
	}
	// 80% of context window for messages, using the same token estimate as
	// compactionParams.
	const estTokensPerMsg = 125
	maxMsgs = (ctxSize * 80 / 100) / estTokensPerMsg
	// Character limit: tokens * 4 (rough chars-per-token), at 80% of window.
	maxChars = ctxSize * 4 * 80 / 100
	return maxMsgs, maxChars
}

type historyCompactionResult struct {
	Messages  []llama.ChatMessage
	Folded    int
	Kept      int
	Compacted bool
	Reason    string
}

type compactHistoryDoneMsg struct {
	result historyCompactionResult
}

// MaybeCompactHistory applies Fix 5 when the history length exceeds the
// configured threshold. It returns a new history whose leading entries have
// been replaced by a single `session_digest` system message. The most recent
// messages (count derived from context window) are preserved verbatim.
//
// Safe for nil clients: if the model call fails or the client is absent, a
// deterministic fallback digest (regex-shape extraction) is used so the
// compaction never silently loses the user's original intent.
func (m *Model) MaybeCompactHistory(ctx context.Context, history []llama.ChatMessage) []llama.ChatMessage {
	result := m.compactHistory(ctx, history, false)
	if !result.Compacted {
		return history
	}
	m.logMessage("session_digest", fmt.Sprintf("compacted %d messages into digest (%d chars); kept %d verbatim", result.Folded, len(result.Messages[0].Content), result.Kept))
	return result.Messages
}

func (m *Model) compactHistoryAsync() {
	history := append([]llama.ChatMessage(nil), m.messages...)
	m.status = "Compacting conversation."
	if m.events == nil {
		result := m.compactHistory(context.Background(), modelPayloadHistory(history), true)
		_, _ = m.handleCompactHistoryDone(compactHistoryDoneMsg{result: result})
		return
	}
	go func() {
		result := m.compactHistory(context.Background(), modelPayloadHistory(history), true)
		m.events <- compactHistoryDoneMsg{result: result}
	}()
}

func (m *Model) compactHistory(ctx context.Context, history []llama.ChatMessage, force bool) historyCompactionResult {
	result := historyCompactionResult{
		Messages: append([]llama.ChatMessage(nil), history...),
		Kept:     len(history),
	}
	if len(history) == 0 {
		result.Reason = "empty_history"
		return result
	}
	if !force && len(history) <= m.compactionThreshold() {
		result.Reason = "below_threshold"
		return result
	}
	budget := NewContextBudget(m.compactionContextSize())
	threshold, recentToKeep := budget.CompactionThreshold, budget.CompactionRecentKeep
	// When MaxHistory is the binding constraint (more restrictive than the
	// context-derived threshold), recentToKeep must scale down accordingly
	// so keepFrom stays positive and compaction actually folds messages
	// instead of silently falling through to hard truncation.
	if limit := m.cfg.Chat.MaxHistory; limit > 0 && limit < threshold {
		if recentToKeep > limit/2 {
			recentToKeep = limit / 2
		}
	}
	keepFrom := len(history) - recentToKeep
	if keepFrom <= 0 {
		if !force || len(history) < 2 {
			result.Reason = "too_short"
			return result
		}
		keepFrom = len(history) - 1
	}
	oldest := history[:keepFrom]
	if len(oldest) == 0 {
		result.Reason = "too_short"
		return result
	}
	digest := m.buildSessionDigest(ctx, oldest)
	if strings.TrimSpace(digest) == "" {
		result.Reason = "empty_digest"
		return result
	}
	compacted := make([]llama.ChatMessage, 0, 1+len(history)-keepFrom)
	compacted = append(compacted, llama.ChatMessage{Role: "system", Content: digest})
	compacted = append(compacted, history[keepFrom:]...)
	result.Messages = compacted
	result.Folded = len(oldest)
	result.Kept = len(history) - keepFrom
	result.Compacted = true
	result.Reason = "compacted"
	return result
}

func (m *Model) handleCompactHistoryDone(msg compactHistoryDoneMsg) (tea.Cmd, bool) {
	result := msg.result
	if !result.Compacted {
		switch result.Reason {
		case "empty_history":
			m.status = "Nothing to compact."
		case "too_short":
			m.status = "Conversation is too short to compact."
		case "empty_digest":
			m.status = "Compaction skipped: no digest could be produced."
		default:
			m.status = "Conversation does not need compaction."
		}
		return nil, false
	}
	m.messages = result.Messages
	m.logMessage("session_digest", fmt.Sprintf("manual compact folded %d messages; kept %d verbatim", result.Folded, result.Kept))
	m.status = fmt.Sprintf("Compacted conversation: folded %d message(s), kept %d.", result.Folded, result.Kept)
	m.renderMessages(true)
	return nil, false
}

func (m *Model) compactionThreshold() int {
	if m == nil {
		budget := NewContextBudget(65536)
		return budget.CompactionThreshold
	}
	budget := NewContextBudget(m.compactionContextSize())
	ctxThreshold := budget.CompactionThreshold
	// If MaxHistory is set and is more restrictive than the context-derived
	// threshold, use it so compaction fires before hard truncation.
	if limit := m.cfg.Chat.MaxHistory; limit > 0 && limit < ctxThreshold {
		return limit
	}
	return ctxThreshold
}

func (m *Model) buildSessionDigest(ctx context.Context, oldest []llama.ChatMessage) string {
	if m == nil {
		return fallbackSessionDigest(oldest)
	}
	prompt := renderCompactionPrompt(oldest)
	reply, err := m.completeChatWithFallback(ctx, []llama.ChatMessage{
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

// completeChatWithFallback sends messages to m.summaryClient first; on nil or
// error it falls back to m.client. Server auto-start is not triggered here —
// callers that want lazy startup should call ensureSummaryServer first.
// Returns an error only when both clients fail or neither is configured.
func (m *Model) completeChatWithFallback(ctx context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (string, error) {
	if m.summaryClient != nil {
		if text, err := m.summaryClient.CompleteChat(ctx, messages, opts); err == nil {
			return text, nil
		}
	}
	if m.client == nil {
		return "", fmt.Errorf("completeChatWithFallback: no model client available")
	}
	return m.client.CompleteChat(ctx, messages, opts)
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
