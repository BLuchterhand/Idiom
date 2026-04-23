package app

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/llama"
)

// promptExecutionPlan is the minimal OpenCode-aligned carrier for a turn.
// All the classification fields from the prior lane-based shape are gone;
// the Mode the user picked is the only routing signal the harness cares
// about.
type promptExecutionPlan struct {
	mode agent.Mode
}

// RunPromptOnce is the single entry point every caller uses to send one
// prompt and get one reply. There are no resume variants, no lane
// dispatch, no task-memory gates — OpenCode's shape is one loop.
func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	m.emitJSONStart(prompt)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	return m.runStandardPromptOnce(ctx, prompt)
}

func (m *Model) runStandardPromptOnce(ctx context.Context, prompt string) (string, error) {
	m.lastAssistantReasoningContent = ""
	plan := promptExecutionPlan{mode: m.currentMode()}
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.logMessage("user", prompt)
	m.logMessage("thinking_config", m.samplingSummary())
	reply, err := m.runAgentTurn(ctx, prompt, m.requestMessages(), m.chatOptions(), plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) finishPromptOnceReply(reply string) string {
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply, ReasoningContent: m.takeLastAssistantReasoningContent()})
	m.logMessage("assistant", reply)
	m.emitJSONFinal(reply)
	return reply
}

// currentMode resolves the Mode selected for this session. Defaults to
// BuildMode if the user hasn't picked one.
func (m *Model) currentMode() agent.Mode {
	if strings.TrimSpace(m.agentModeName) != "" {
		return agent.ModeOrDefault(m.agentModeName)
	}
	return agent.ModeOrDefault(m.cfg.Chat.AgentMode)
}
