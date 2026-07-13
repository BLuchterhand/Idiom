package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/projectartifacts"
)

func (m *promptExecutionSession) ensurePlanPath() string {
	if m == nil {
		return ""
	}
	if m.planPath != "" {
		return m.planPath
	}

	root := ""
	if m.workspace != nil {
		root = m.workspace.Root()
	}
	path := planningArtifactPath(root, sessionlog.SessionIDFromPath(m.debugLogPath), ".md")
	if path == "" {
		return ""
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	m.planPath = path

	return m.planPath
}

func (m *promptExecutionSession) invalidatePlanCache() {
	m.queueUIEvent(planRefreshMsg{})
}

func (m *promptExecutionSession) isHeadless() bool {
	return m != nil && (m.eventStream != nil || m.suppressPromptTranscript || m.nonInteractive)
}

func (m *promptExecutionSession) openLaneSwitchPopup(objective, recommended string) {
	m.queueUIEvent(laneSwitchPopupMsg{objective: objective, recommended: recommended})
}

func (m *promptExecutionSession) enqueueHistoryAuditEvent(event historyAuditEvent) {
	event = cleanHistoryAuditEvent(event)
	if event.label == "" || m == nil || m.events == nil {
		return
	}
	m.tryQueueUIEvent(historyAuditMsg{event: event})
}

func (m *promptExecutionSession) takeLastAssistantReasoningContent() string {
	reasoning := strings.TrimSpace(m.lastAssistantReasoningContent)
	m.lastAssistantReasoningContent = ""

	return reasoning
}

func (m *promptExecutionSession) pinnedFilesRegistry() *PinnedFileRegistry {
	if m == nil {
		return nil
	}
	if m.pinnedFiles == nil {
		m.pinnedFiles = newPinnedFileRegistry()
	}

	return m.pinnedFiles
}

func (m *promptExecutionSession) compactionExecution() compactionExecution {
	options := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)
	options.Tools = append([]llama.Tool(nil), options.Tools...)

	return compactionExecution{
		summaryClient:  m.summaryClient,
		fallbackClient: m.client,
		contextSize:    compactionContextSize(m.cfg.LlamaServer.CtxSize, m.cfg.RouteClassifier.CtxSize),
		maxHistory:     m.cfg.Chat.MaxHistory,
		chatOptions:    options,
	}
}

func (m *promptExecutionSession) maybeCompactHistory(ctx context.Context, history []llama.ChatMessage) []llama.ChatMessage {
	result := compactHistory(ctx, history, false, m.compactionExecution())
	if !result.Compacted {
		return history
	}

	m.logMessage("session_digest", fmt.Sprintf("compacted %d messages into digest (%d chars); kept %d verbatim", result.Folded, len(result.Messages[0].Content), result.Kept))

	return result.Messages
}

func (m *promptExecutionSession) completeChatWithFallback(ctx context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (string, error) {
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

func (m *promptExecutionSession) requestMessages(ctx context.Context) []llama.ChatMessage {
	var messages []llama.ChatMessage
	if prompt := strings.TrimSpace(m.systemPrompt()); prompt != "" {
		messages = append(messages, llama.ChatMessage{Role: "system", Content: prompt})
	}

	history := m.messages
	if m.requestHistoryOverride != nil {
		history = *m.requestHistoryOverride
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && last.Content == "" {
			history = history[:len(history)-1]
		}
	}
	history = modelPayloadHistory(history)
	history = m.maybeCompactHistory(ctx, history)
	history = modelPayloadHistory(history)

	if m.workspace != nil && m.pinnedFiles != nil {
		if snapshot := m.pinnedFiles.BuildSnapshot(m.workspace.Root()); snapshot != "" {
			messages = append(messages, llama.ChatMessage{Role: "system", Content: snapshot})
			m.logMessage("pinned_files_snapshot", fmt.Sprintf("injected %d chars across %d files", len(snapshot), len(m.pinnedFiles.Pins())))
		}
	}

	start := 0
	if limit := m.cfg.Chat.MaxHistory; limit > 0 && len(history) > limit {
		start = len(history) - limit
	}
	for _, message := range history[start:] {
		messages = append(messages, llama.ChatMessage{Role: normalizeHistoryRole(message.Role), Content: message.Content})
	}

	return messages
}

func (m *promptExecutionSession) systemPrompt() string {
	if m == nil {
		return ""
	}

	derivation := derivePromptComposition(
		m.agentModeName,
		m.cfg.Chat.AgentMode,
		m.cfg.Chat.ModelFamily,
		m.plannerSpec.Model,
		m.cfg.LlamaServer.Model,
	)
	result := m.promptRuntimeCompletionState.resolveComposedPrompt(
		m.promptStack,
		derivation,
		func(existing []promptstack.Source) systemPromptFragments {
			return loadModeSystemPromptFragments(
				derivation.mode,
				resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace),
				existing,
			)
		},
	)

	return result.text
}

func (m *promptExecutionSession) reloadPromptStack() error {
	chat := m.cfg.Chat
	if strings.TrimSpace(chat.ProjectRoot) != "" {
		if _, err := projectartifacts.Ensure(chat.ProjectRoot); err != nil {
			return err
		}
	}

	stack, err := buildPromptStack(chat, m.activeProfiles)
	if err != nil {
		return err
	}
	m.promptRuntimeCompletionState.applyPromptStackBuild(stack)
	if len(stack.Warnings) > 0 {
		m.setContextError(strings.Join(stack.Warnings, "; "))
	}

	return nil
}

func (m *promptExecutionSession) setContextError(message string) {
	message = strings.TrimSpace(message)
	if message != "" {
		m.contextError = message
	}
}

func (m *promptExecutionSession) logError(kind, message string) {
	m.eventStream.emitError(kind, message)
	if m.debugLogger != nil {
		_ = m.debugLogger.LogError(kind, message)
	}
}
