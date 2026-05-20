package app

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/llama"
	"idiom/internal/util"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading UI..."
	}

	w := max(0, m.width)

	// ── Header ────────────────────────────────────────────────────────────────
	serverState := "down"
	if m.serverUp {
		serverState = "up"
	}
	if m.bootingServer {
		serverState = m.spinner.View() + " booting"
	}
	leftTitle := titleStyle.Render("◆ " + m.cfg.AppName)
	rightInfo := statusStyle.Render(serverState + "  " + util.Fallback(m.modelID, "unknown"))
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

	footer := strings.Join(footerLines, "\n")

	// ── Content row: main viewport + optional side panel (tabbed) ────────────
	var contentRow string
	if m.history.panelOpen {
		h := m.viewport.Height
		vertBar := dividerStyle.Render(strings.Repeat("│\n", h-1) + "│")
		panelWidth := m.history.viewport.Width
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

	parts := []string{header, divider, contentRow, divider, m.input.View()}
	if footer != "" {
		parts = append(parts, footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
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
	history = modelPayloadHistory(history)
	// Fix 5 (context compaction) — fold the oldest model-visible messages into
	// a session digest before the hard-truncation below kicks in. Operator
	// messages are intentionally filtered first so human-only status surfaces
	// cannot be summarized back into model context.
	history = m.MaybeCompactHistory(context.Background(), history)
	history = modelPayloadHistory(history)

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
			Role:    normalizeHistoryRole(msg.Role),
			Content: msg.Content,
		})
	}
	return messages
}

func modelPayloadHistory(history []llama.ChatMessage) []llama.ChatMessage {
	if len(history) == 0 {
		return history
	}
	out := make([]llama.ChatMessage, 0, len(history))
	for _, msg := range history {
		if msg.Role == "operator" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (m *Model) renderMessages(forceBottom bool) {
	if !m.ready {
		return
	}
	start := time.Now()
	defer m.logUISlowPath("ui.render_messages", start, fmt.Sprintf("messages=%d streaming=%t width=%d", len(m.messages), m.streaming, m.viewport.Width))

	stickBottom := forceBottom || m.viewport.AtBottom()

	msgBodyStyle := bodyStyle.Width(max(1, m.viewport.Width))
	base := m.renderedMessageBody()

	// While a turn is in flight, render a live "Qwen is working" affordance
	// after the last message. The in-flight reply is NOT kept in m.messages
	// as an empty placeholder (that caused double-rendering via the reply
	// bookkeeping path); we draw the spinner inline instead.
	var b strings.Builder
	if base != "" {
		b.WriteString(base)
	}
	if m.streaming {
		label := assistantLabelStyle.Render("Qwen")
		content := m.spinner.View()
		if m.planPhaseText != "" {
			content += " " + m.planPhaseText
		} else {
			content += " Qwen is working on your request" + m.workingDots()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(msgBodyStyle.Render(content))
	}

	if base == "" && !m.streaming {
		b.WriteString(msgBodyStyle.Render("No messages yet. Type a prompt below or use /help."))
	}

	m.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) renderedMessageBody() string {
	width := max(1, m.viewport.Width)
	useMarkdown := m.mdRenderer != nil
	hash := messageRenderFingerprint(m.messages)
	if m.messageRenderWidth == width && m.messageRenderHash == hash && m.messageRenderMD == useMarkdown {
		if m.messageRenderCache != "" || len(m.messages) == 0 {
			return m.messageRenderCache
		}
	}

	if len(m.messages) == 0 {
		m.perMsgRenderCache = nil
		m.perMsgRenderHashes = nil
		m.perMsgRenderLen = 0
		m.messageRenderCache = ""
		m.messageRenderWidth = width
		m.messageRenderHash = hash
		m.messageRenderMD = useMarkdown
		return ""
	}
	if m.perMsgRenderLen > len(m.messages) || m.messageRenderWidth != width || m.messageRenderMD != useMarkdown {
		m.perMsgRenderCache = nil
		m.perMsgRenderHashes = nil
		m.perMsgRenderLen = 0
	}
	if len(m.perMsgRenderCache) < len(m.messages) {
		m.perMsgRenderCache = append(m.perMsgRenderCache, make([]string, len(m.messages)-len(m.perMsgRenderCache))...)
	}
	if len(m.perMsgRenderHashes) < len(m.messages) {
		m.perMsgRenderHashes = append(m.perMsgRenderHashes, make([]uint64, len(m.messages)-len(m.perMsgRenderHashes))...)
	}

	msgBodyStyle := bodyStyle.Width(width)
	var b strings.Builder
	for i, msg := range m.messages {
		msgHash := messageRenderFingerprint([]llama.ChatMessage{msg})
		if m.perMsgRenderCache[i] == "" || m.perMsgRenderHashes[i] != msgHash {
			m.perMsgRenderCache[i] = m.renderSingleMessage(msg, msgBodyStyle)
			m.perMsgRenderHashes[i] = msgHash
		}
		b.WriteString(m.perMsgRenderCache[i])
		if i < len(m.messages)-1 {
			b.WriteString("\n\n")
		}
	}
	m.perMsgRenderCache = m.perMsgRenderCache[:len(m.messages)]
	m.perMsgRenderHashes = m.perMsgRenderHashes[:len(m.messages)]
	m.perMsgRenderLen = len(m.messages)

	m.messageRenderCache = strings.TrimRight(b.String(), "\n")
	m.messageRenderWidth = width
	m.messageRenderHash = hash
	m.messageRenderMD = useMarkdown
	return m.messageRenderCache
}

func (m *Model) renderSingleMessage(msg llama.ChatMessage, msgBodyStyle lipgloss.Style) string {
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

	var b strings.Builder
	b.WriteString(label)
	b.WriteString("\n")
	if useGlamour && m.mdRenderer != nil {
		rendered, err := m.mdRenderer.Render(protectControlMarkersForMarkdown(content))
		if err == nil {
			b.WriteString(strings.TrimRight(rendered, "\n"))
		} else {
			b.WriteString(msgBodyStyle.Render(content))
		}
	} else {
		b.WriteString(msgBodyStyle.Render(content))
	}
	return strings.TrimRight(b.String(), "\n")
}

func protectControlMarkersForMarkdown(content string) string {
	replacer := strings.NewReplacer(
		"<<QUESTION>>", "`<<QUESTION>>`",
	)
	return replacer.Replace(content)
}

func messageRenderFingerprint(messages []llama.ChatMessage) uint64 {
	hasher := fnv.New64a()
	for _, msg := range messages {
		_, _ = hasher.Write([]byte(msg.Role))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(strings.TrimSpace(msg.Content)))
		_, _ = hasher.Write([]byte{'\n'})
	}
	return hasher.Sum64()
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
