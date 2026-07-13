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

// Compaction thresholds are derived automatically from the model's
// context-window size (LlamaServer.CtxSize) through ContextBudget.

const compactionSystemPromptPreamble = "You are producing a compact session digest. Preserve only the facts that the next turn needs to act correctly: the user's original goals, any hard constraints or boundaries, files and paths that have been read or edited, outcomes of tool calls, open questions, and the last known blocker. Write plain prose in 12 lines or fewer. Do not invent facts. Do not request tools."

type historyCompactionResult struct {
	Messages  []llama.ChatMessage
	Folded    int
	Kept      int
	Compacted bool
	Reason    string
}

type compactHistoryDoneMsg struct {
	revision uint64
	result   historyCompactionResult
}

// compactionExecution is the immutable dependency/configuration snapshot used
// by one compaction. Background work must not read the live Bubble Tea Model.
type compactionExecution struct {
	summaryClient  llama.LLMClient
	fallbackClient llama.LLMClient
	contextSize    int
	maxHistory     int
	chatOptions    llama.ChatOptions
}

func snapshotCompactionExecution(model *Model) compactionExecution {
	options := chatOptions(model.cfg.Chat, model.modelID, model.modelThinkingFloor, model.disableThinkingOverride)
	options.Tools = append([]llama.Tool(nil), options.Tools...)

	return compactionExecution{
		summaryClient:  model.summaryClient,
		fallbackClient: model.client,
		contextSize:    compactionContextSize(model.cfg.LlamaServer.CtxSize, model.cfg.RouteClassifier.CtxSize),
		maxHistory:     model.cfg.Chat.MaxHistory,
		chatOptions:    options,
	}
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
	result := compactHistory(ctx, history, false, snapshotCompactionExecution(m))
	if !result.Compacted {
		return history
	}

	m.logMessage("session_digest", fmt.Sprintf("compacted %d messages into digest (%d chars); kept %d verbatim", result.Folded, len(result.Messages[0].Content), result.Kept))

	return result.Messages
}

func (m *Model) compactHistoryAsync() {
	history := cloneChatMessages(m.messages)
	execution := snapshotCompactionExecution(m)
	revision := m.messagesRevision

	m.status = "Compacting conversation."

	if m.events == nil {
		result := compactHistory(context.Background(), modelPayloadHistory(history), true, execution)

		_, _ = m.handleCompactHistoryDone(compactHistoryDoneMsg{revision: revision, result: result})

		return
	}

	events := m.events
	_ = m.ensureBackgroundOperations().start(func(ctx context.Context) {
		result := compactHistory(ctx, modelPayloadHistory(history), true, execution)
		select {
		case events <- compactHistoryDoneMsg{revision: revision, result: result}:
		case <-ctx.Done():
		}
	})
}

func compactHistory(ctx context.Context, history []llama.ChatMessage, force bool, execution compactionExecution) historyCompactionResult {
	result := historyCompactionResult{
		Messages: cloneChatMessages(history),
		Kept:     len(history),
	}
	if len(history) == 0 {
		result.Reason = "empty_history"
		return result
	}

	if !force && len(history) <= compactionThreshold(execution) {
		result.Reason = "below_threshold"
		return result
	}

	budget := NewContextBudget(execution.contextSize)
	threshold, recentToKeep := budget.CompactionThreshold, budget.CompactionRecentKeep
	// When MaxHistory is the binding constraint (more restrictive than the
	// context-derived threshold), recentToKeep must scale down accordingly
	// so keepFrom stays positive and compaction actually folds messages
	// instead of silently falling through to hard truncation.
	if limit := execution.maxHistory; limit > 0 && limit < threshold {
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

	digest := buildSessionDigest(ctx, oldest, execution)
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
	if msg.revision != m.messagesRevision {
		return nil, false
	}

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
	m.messagesRevision++
	m.logMessage("session_digest", fmt.Sprintf("manual compact folded %d messages; kept %d verbatim", result.Folded, result.Kept))

	m.status = fmt.Sprintf("Compacted conversation: folded %d message(s), kept %d.", result.Folded, result.Kept)
	m.renderMessages(true)

	return nil, false
}

func compactionThreshold(execution compactionExecution) int {
	budget := NewContextBudget(execution.contextSize)
	ctxThreshold := budget.CompactionThreshold
	// If MaxHistory is set and is more restrictive than the context-derived
	// threshold, use it so compaction fires before hard truncation.
	if limit := execution.maxHistory; limit > 0 && limit < ctxThreshold {
		return limit
	}

	return ctxThreshold
}

func buildSessionDigest(ctx context.Context, oldest []llama.ChatMessage, execution compactionExecution) string {
	prompt := renderCompactionPrompt(oldest)

	reply, err := completeCompactionChat(ctx, execution, []llama.ChatMessage{
		{Role: "system", Content: compactionSystemPromptPreamble},
		{Role: "user", Content: prompt},
	}, execution.chatOptions)
	if err != nil {
		return fallbackSessionDigest(oldest)
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallbackSessionDigest(oldest)
	}

	return "Session digest (prior history compacted):\n" + reply
}

func completeCompactionChat(ctx context.Context, execution compactionExecution, messages []llama.ChatMessage, options llama.ChatOptions) (string, error) {
	if execution.summaryClient != nil {
		if text, err := execution.summaryClient.CompleteChat(ctx, messages, options); err == nil {
			return text, nil
		}
	}

	if execution.fallbackClient == nil {
		return "", fmt.Errorf("compact history: no model client available")
	}

	return execution.fallbackClient.CompleteChat(ctx, messages, options)
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
