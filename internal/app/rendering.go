package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/llama"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading UI..."
	}

	w := maxInt(0, m.width)

	// ── Header ────────────────────────────────────────────────────────────────
	serverState := "down"
	if m.serverUp {
		serverState = "up"
	}
	if m.bootingServer {
		serverState = m.spinner.View() + " booting"
	}
	leftTitle := titleStyle.Render("◆ " + m.cfg.AppName)
	rightInfo := statusStyle.Render(serverState + "  " + fallback(m.modelID, "unknown"))
	headerGap := w - lipgloss.Width(leftTitle) - lipgloss.Width(rightInfo)
	if headerGap < 0 {
		headerGap = 0
	}
	header := leftTitle + strings.Repeat(" ", headerGap) + rightInfo

	divider := dividerStyle.Render(strings.Repeat("─", w))

	// ── Footer ────────────────────────────────────────────────────────────────
	var footerLines []string

	if pending := m.currentApproval(); pending != nil {
		footerLines = append(footerLines,
			approvalStyle.Width(w).Render("▲  APPROVAL REQUIRED ("+pending.mode+"):  "+pending.summary()+"  →  /approve or /deny"))
	}

	if banner := m.errorBanner(); banner != "" {
		footerLines = append(footerLines, errorStyle.Render(banner))
	}

	// Compact status bar: context info left, status + char count right.
	infoLeft := contextStyle.Render(
		"mode: " + m.modeSummary() + "  ·  trace: " + m.traceSummary() +
			"  ·  ctx: " + m.contextSummary() + "  ·  " + m.workspaceSummary())
	var rightParts []string
	if m.status != "" {
		rightParts = append(rightParts, m.status)
	}
	if n := len([]rune(m.input.Value())); n > 0 {
		rightParts = append(rightParts, fmt.Sprintf("%d", n))
	}
	infoRight := statusStyle.Render(strings.Join(rightParts, "  ·  "))
	barGap := w - lipgloss.Width(infoLeft) - lipgloss.Width(infoRight)
	if barGap < 1 {
		barGap = 1
	}
	footerLines = append(footerLines, infoLeft+strings.Repeat(" ", barGap)+infoRight)

	footer := strings.Join(footerLines, "\n")

	// ── Content row: main viewport + optional side panel (tabbed) ────────────
	var contentRow string
	if m.historyPanelOpen {
		h := m.viewport.Height
		vertBar := dividerStyle.Render(strings.Repeat("│\n", h-1) + "│")
		panelWidth := m.historyViewport.Width
		activeVP := m.activePanelViewport()
		panel := lipgloss.JoinVertical(lipgloss.Left,
			m.renderPanelTabs(panelWidth),
			activeVP.View(),
		)
		contentRow = lipgloss.JoinHorizontal(lipgloss.Top,
			m.viewport.View(),
			vertBar,
			panel,
		)
	} else {
		contentRow = m.viewport.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, divider, contentRow, divider, m.input.View(), footer)
}

func (m *Model) requestMessages() []llama.ChatMessage {
	var messages []llama.ChatMessage
	if prompt := strings.TrimSpace(m.systemPrompt()); prompt != "" {
		messages = append(messages, llama.ChatMessage{Role: "system", Content: prompt})
	}
	// OpenCode-aligned: no static workspace-overview injection. The model
	// discovers the layout by calling list_files / glob / search tools on
	// demand, exactly like it does in OpenCode. Injecting a static tree
	// every turn inflated prompt tokens and taught the model that the
	// harness always pre-answers orientation questions — which trained it
	// to skip real discovery.
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

	msgBodyStyle := bodyStyle.Width(maxInt(1, m.viewport.Width))

	var b strings.Builder
	for _, msg := range m.messages {
		var label string
		useGlamour := false
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "user":
			label = userLabelStyle.Render("You")
		case "assistant":
			label = assistantLabelStyle.Render("Qwen")
			if content != "" {
				useGlamour = true
			}
		case "tool":
			// Surface the tool name from the first line: "tool_name | summary..."
			toolName := "tool"
			if idx := strings.Index(content, " | "); idx > 0 {
				toolName = content[:idx]
			} else if fields := strings.Fields(content); len(fields) > 0 {
				toolName = fields[0]
			}
			label = toolLabelStyle.Render("▶ " + toolName)
		default:
			label = systemLabelStyle.Render(strings.Title(msg.Role))
		}
		b.WriteString(label)
		b.WriteString("\n")
		if useGlamour && m.mdRenderer != nil {
			rendered, err := m.mdRenderer.Render(content)
			if err == nil {
				b.WriteString(strings.TrimRight(rendered, "\n"))
			} else {
				b.WriteString(msgBodyStyle.Render(content))
			}
		} else {
			b.WriteString(msgBodyStyle.Render(content))
		}
		b.WriteString("\n\n")
	}

	// While a turn is in flight, render a live "Qwen is working" affordance
	// after the last message. The in-flight reply is NOT kept in m.messages
	// as an empty placeholder (that caused double-rendering via the reply
	// bookkeeping path); we draw the spinner inline instead.
	if m.streaming {
		label := assistantLabelStyle.Render("Qwen")
		content := m.spinner.View() + " Qwen is working on your request" + m.workingDots()
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(msgBodyStyle.Render(content))
		b.WriteString("\n\n")
	}

	if len(m.messages) == 0 && !m.streaming {
		b.WriteString(msgBodyStyle.Render("No messages yet. Type a prompt below or use /help."))
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
