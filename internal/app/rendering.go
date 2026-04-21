package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading UI..."
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("109")).Padding(0, 1)
	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160")).Padding(0, 1)

	serverState := "down"
	if m.serverUp {
		serverState = "up"
	}
	if m.bootingServer {
		serverState = m.spinner.View() + " booting"
	}

	header := titleStyle.Render(m.cfg.AppName) +
		statusStyle.Render(fmt.Sprintf("server:%s model:%s", serverState, fallback(m.modelID, "unknown")))
	body := m.viewport.View()
	contextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Padding(0, 1)
	footer := helpStyle.Render("Enter send | PgUp/PgDn scroll | Mouse wheel scroll | Ctrl+S save | Ctrl+L clear | Ctrl+R restart | Ctrl+C quit | /help") + "\n" +
		contextStyle.Render("context: "+m.contextSummary()) + "\n" +
		contextStyle.Render("mode: "+m.modeSummary()) + "\n" +
		contextStyle.Render("trace: "+m.traceSummary()) + "\n" +
		contextStyle.Render("workspace: "+m.workspaceSummary()) + "\n" +
		statusStyle.Render(m.status)
	if banner := m.errorBanner(); banner != "" {
		footer += "\n" + errorStyle.Render(banner)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)
}

func (m *Model) requestMessages() []llama.ChatMessage {
	var messages []llama.ChatMessage
	if prompt := strings.TrimSpace(m.systemPrompt()); prompt != "" {
		messages = append(messages, llama.ChatMessage{Role: "system", Content: prompt})
	}
	// Fix 4 (workspace overview) — inject a cheap top-level layout snapshot
	// exactly once per session so the model doesn't guess at paths.
	if m.workspace != nil {
		if !m.workspaceOverviewInjected {
			if overview := strings.TrimSpace(workspace.OverviewSystemPrompt(m.workspace.Root())); overview != "" {
				messages = append(messages, llama.ChatMessage{Role: "system", Content: overview})
				m.workspaceOverviewInjected = true
				m.logMessage("workspace_overview", fmt.Sprintf("injected %d chars (root=%s)", len(overview), m.workspace.Root()))
			} else {
				m.logMessage("workspace_overview", fmt.Sprintf("skipped_empty (root=%s)", m.workspace.Root()))
				m.workspaceOverviewInjected = true
			}
		}
	}

	history := m.messages
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && last.Content == "" {
			history = history[:len(history)-1]
		}
	}
	// Fix 5 (context compaction) — fold the oldest messages into a session
	// digest before the hard-truncation below kicks in.
	history = m.MaybeCompactHistory(context.Background(), history)

	// Pinned-file reference snapshot — keep recently-read files visible even
	// after their raw tool results have been compacted out of history.
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
	for _, msg := range history[start:] {
		messages = append(messages, llama.ChatMessage{
			Role:             normalizeHistoryRole(msg.Role),
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
		})
	}
	return messages
}

func (m *Model) renderMessages(forceBottom bool) {
	if !m.ready {
		return
	}

	stickBottom := forceBottom || m.viewport.AtBottom()

	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("63")).Padding(0, 1).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("31")).Padding(0, 1).Bold(true)
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238")).Padding(0, 1).Bold(true)
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94")).Padding(0, 1).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Padding(0, 1).Width(maxInt(1, m.viewport.Width))

	var b strings.Builder
	for _, msg := range m.messages {
		var label string
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "user":
			label = userStyle.Render("You")
		case "assistant":
			label = assistantStyle.Render("Qwen")
			if m.streaming && content == "" {
				content = m.spinner.View() + " Qwen is working on your request" + m.workingDots()
			}
		case "tool":
			label = toolStyle.Render("Tool")
		default:
			label = systemStyle.Render(strings.Title(msg.Role))
		}
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(bodyStyle.Render(content))
		b.WriteString("\n\n")
	}

	if len(m.messages) == 0 {
		b.WriteString(bodyStyle.Render("No messages yet. Type a prompt below or use /help."))
	}

	m.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) updateViewport(msg tea.Msg) {
	if !m.ready {
		return
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	_ = cmd
}

func scrollableViewportKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	switch keyMsg.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return true
	default:
		return false
	}
}
