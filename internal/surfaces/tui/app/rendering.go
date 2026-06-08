package app

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
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
	rightInfo := statusStyle.Render(serverState + "  " + util.Fallback(m.activeModelDisplayID(), "unknown"))
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
	statusLine := m.operatorStatusLine(w)

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

	parts := []string{header, divider, contentRow, divider}
	if statusLine != "" {
		parts = append(parts, statusLine)
	}
	parts = append(parts, m.input.View())

	// Slash-command suggestion popup — floats above the input area.
	if m.slashSuggestions.visible && len(m.slashSuggestions.matches) > 0 {
		popup := m.renderSlashSuggestions()
		if popup != "" {
			parts = append(parts, popup)
		}
	}

	if footer != "" {
		parts = append(parts, footer)
	}
	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if popup := m.renderModelPopup(w, max(0, m.height)); popup != "" {
		view = lipgloss.JoinVertical(lipgloss.Left, view, popup)
	}
	return view
}

func (m *Model) operatorStatusLine(width int) string {
	text := m.operatorStatusText(width)
	if text == "" {
		return ""
	}
	return statusLineStyle.Render(text)
}

func (m *Model) operatorStatusText(width int) string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		return ""
	}
	available := max(1, width-2)
	hint := m.operatorHintText()
	if hint != "" && available >= 72 {
		status = status + " · " + hint
	}
	return util.Truncate(strings.TrimSpace(status), available)
}

func (m *Model) operatorHintText() string {
	if m.history.panelOpen {
		if m.history.focused {
			return "Esc unfocus · Ctrl+T tabs"
		}
		return "Tab focus · Ctrl+T tabs"
	}
	if m.streaming {
		return "working · Ctrl+C quits"
	}
	return "/help commands · Ctrl+B panel · Ctrl+S save"
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

// ── Evidence container rendering ─────────────────────────────────────────────

// evidenceDisplayName maps a raw tool name to a compact display name with an
// optional language tag suffix.
func evidenceDisplayName(rawTool string) string {
	tool := strings.TrimSpace(rawTool)
	// Language-prefixed tools: extract language tag.
	if idx := strings.Index(tool, "_"); idx > 0 {
		prefix := strings.ToLower(tool[:idx])
		suffix := tool[idx+1:]
		switch prefix {
		case "go", "cpp", "rust", "python", "java", "ts":
			// Map suffix to short display name.
			display := suffix
			switch suffix {
			case "read_symbol":
				display = "read"
			case "file_outline":
				display = "outline"
			case "find_symbol", "find_type", "find_const", "find_package":
				display = "find"
			case "related_files":
				display = "related"
			case "definition", "references", "implementations":
				display = suffix
			case "calls_from", "calls_into":
				display = "trace"
			case "extract_body":
				display = "body"
			case "importers", "who_includes":
				display = "importers"
			case "diagnostics":
				display = "diag"
			case "workspace_symbol":
				display = "symbol"
			case "blast_radius":
				display = "blast"
			case "dead_symbol":
				display = "dead"
			case "overview", "entrypoints":
				display = suffix
			default:
				display = suffix
			}
			return display + " [" + prefix + "]"
		}
	}
	// Generic tools.
	switch tool {
	case "read_file":
		return "read"
	case "edit_file", "write_file", "append_file", "apply_patch":
		return "edit"
	case "search_content", "search_files":
		return "search"
	case "list_files":
		return "list"
	case "shell":
		return "run"
	case "context_pack":
		return "context"
	case "discovery_query":
		return "discover"
	case "dependency_graph":
		return "deps"
	case "task_ledger":
		return "plan"
	case "child_task":
		return "delegate"
	case "submit_plan":
		return "submit"
	case "source_sanity":
		return "verify"
	default:
		return strings.ToLower(tool)
	}
}

// renderEvidenceSummary builds the collapsed summary line for an evidence
// container.
func renderEvidenceSummary(toolCount int, catBreakdown, verifyStatus, budget string, expanded bool) string {
	indicator := "▸"
	if expanded {
		indicator = "▾"
	}
	parts := []string{indicator, fmt.Sprintf("%d tools", toolCount)}
	if catBreakdown != "" {
		parts = append(parts, catBreakdown)
	}
	if verifyStatus != "" {
		vs := verifyStatus
		switch verifyStatus {
		case "tests passed":
			vs = lipgloss.NewStyle().Foreground(colorSuccess).Render(verifyStatus)
		case "tests failed", "tests mixed", "unverified":
			vs = lipgloss.NewStyle().Foreground(colorWarningFg).Render(verifyStatus)
		default:
			vs = lipgloss.NewStyle().Foreground(colorDim).Render(verifyStatus)
		}
		parts = append(parts, vs)
	}
	if budget != "" {
		parts = append(parts, budget)
	}
	return lipgloss.NewStyle().Foreground(colorDim).Render(strings.Join(parts, " · "))
}

// renderCompletedContainer renders a past-turn evidence container.
func renderCompletedContainer(c *completedEvidence, width, maxRows int) string {
	if c == nil || len(c.tools) == 0 {
		return ""
	}
	toolCount, catBreakdown, verifyStatus := evidenceStats(c.tools)
	budget := ""
	if c.budget != nil {
		budget = formatBudgetSummary(c.budget)
	}

	var b strings.Builder
	b.WriteString(renderEvidenceSummary(toolCount, catBreakdown, verifyStatus, budget, c.expanded))

	if c.expanded {
		start := c.scrollOff
		end := start + maxRows
		if end > len(c.tools) {
			end = len(c.tools)
		}
		for _, row := range c.tools[start:end] {
			b.WriteString("\n  ")
			b.WriteString(renderEvidenceRow(row))
		}
		if end < len(c.tools) {
			remaining := len(c.tools) - end
			b.WriteString("\n  ")
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(
				fmt.Sprintf("%d more…", remaining)))
		}
	}
	return b.String()
}

// renderActiveContainer renders the current turn's live evidence container.
func renderActiveContainer(te *turnEvidence, width, maxRows int, streaming bool) string {
	if te == nil || len(te.tools) == 0 {
		return ""
	}
	toolCount, catBreakdown, verifyStatus := evidenceStats(te.tools)
	budget := ""
	if te.finalized && len(te.tools) > 0 {
		// Budget not yet available during active rendering; comes via
		// budgetLedgerMsg after turn ends.
	}

	expanded := te.expanded && !te.userCollapsed
	if streaming && !te.userCollapsed {
		expanded = true
	}

	var b strings.Builder
	b.WriteString(renderEvidenceSummary(toolCount, catBreakdown, verifyStatus, budget, expanded))

	if expanded {
		start := te.scrollOff
		end := start + maxRows
		if end > len(te.tools) {
			end = len(te.tools)
		}
		for _, row := range te.tools[start:end] {
			b.WriteString("\n  ")
			b.WriteString(renderEvidenceRow(row))
		}
		if end < len(te.tools) {
			remaining := len(te.tools) - end
			b.WriteString("\n  ")
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(
				fmt.Sprintf("%d more…", remaining)))
		}
	}
	return b.String()
}

// renderEvidenceRow renders a single evidence row.
func renderEvidenceRow(row evidenceRow) string {
	toolStyle := lipgloss.NewStyle().Foreground(colorTool).Bold(true)
	targetStyle := lipgloss.NewStyle().Foreground(colorDim)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	var glyph string
	switch row.glyph {
	case "✔":
		glyph = lipgloss.NewStyle().Foreground(colorSuccess).Render("✔")
	case "✘":
		glyph = lipgloss.NewStyle().Foreground(colorErrorFg).Render("✘")
	default:
		glyph = dimStyle.Render(row.glyph)
	}

	if row.isThought {
		return dimStyle.Render("thought") + " " + dimStyle.Render(strings.TrimSpace(row.summary))
	}

	name := evidenceDisplayName(row.toolName)
	line := glyph + " " + toolStyle.Render(name)
	if row.target != "" {
		line += " " + targetStyle.Render(row.target)
	}
	if row.summary != "" && !row.running {
		line += "  " + dimStyle.Render(row.summary)
	}
	return line
}

// evidenceStats computes the count, category breakdown, and verification
// status for a set of evidence rows.
func evidenceStats(rows []evidenceRow) (count int, catBreakdown, verifyStatus string) {
	if len(rows) == 0 {
		return 0, "", ""
	}
	catCounts := map[string]int{}
	hasWrite := false
	hasPass := false
	hasFail := false
	hasVerify := false

	for _, r := range rows {
		if r.running {
			continue
		}
		catCounts[r.toolName]++
		switch r.auditKind {
		case "write":
			hasWrite = true
		case "verification", "shell":
			hasVerify = true
			switch {
			case strings.HasPrefix(strings.ToLower(r.outcome), "failed"):
				hasFail = true
			default:
				hasPass = true
			}
		}
	}
	count = len(rows)

	// Build category breakdown from the most common tool names.
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range catCounts {
		// Use display names for the breakdown.
		display := evidenceDisplayName(k)
		sorted = append(sorted, kv{display, v})
	}
	// Sort by count descending, then name.
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v || (sorted[j].v == sorted[i].v && sorted[j].k < sorted[i].k) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	parts := make([]string, 0, len(sorted))
	for _, s := range sorted {
		parts = append(parts, fmt.Sprintf("%d %s", s.v, s.k))
	}
	catBreakdown = strings.Join(parts, ", ")

	// Verification status.
	switch {
	case hasVerify && hasPass && !hasFail:
		verifyStatus = "tests passed"
	case hasVerify && hasFail && !hasPass:
		verifyStatus = "tests failed"
	case hasVerify && hasPass && hasFail:
		verifyStatus = "tests mixed"
	case hasWrite && !hasVerify:
		verifyStatus = "unverified"
	}
	return count, catBreakdown, verifyStatus
}

// formatBudgetSummary formats per-turn budget data for the summary line.
func formatBudgetSummary(b *budgetData) string {
	if b == nil {
		return ""
	}
	parts := []string{formatTokens(b.totalTokens)}
	if b.maxToolSteps > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d tools", b.usedToolResults, b.maxToolSteps))
	}
	if b.maxModelCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d calls", b.usedModelCalls, b.maxModelCalls))
	}
	return strings.Join(parts, " · ")
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk tokens", float64(n)/1000.0)
	}
	return fmt.Sprintf("%d tokens", n)
}

func (m *Model) renderMessages(forceBottom bool) {
	if !m.ready {
		return
	}
	start := time.Now()
	defer m.logUISlowPath("ui.render_messages", start, fmt.Sprintf("messages=%d streaming=%t width=%d", len(m.messages), m.streaming, m.viewport.Width))

	stickBottom := forceBottom || m.viewport.AtBottom()
	enabled := m.cfg.TUI.EvidencePanel
	width := max(1, m.viewport.Width)
	msgBodyStyle := bodyStyle.Width(width)
	const maxContainerRows = 15

	// If evidence panel is disabled, use the existing cached-body path.
	if !enabled {
		base := m.renderedMessageBody()
		var b strings.Builder
		if base != "" {
			b.WriteString(base)
		}
		if m.streaming {
			label := assistantLabelStyle.Render("Qwen")
			content := m.spinner.View() + " " + m.streamingBusyText()
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
		return
	}

	// Evidence panel enabled: render messages iteratively, inserting
	// evidence containers after each assistant message.
	containerIdx := 0
	var b strings.Builder
	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.cachedMessageRender(msg, msgBodyStyle))

		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) != "" {
			if containerIdx < len(m.pastTurnContainers) {
				b.WriteString("\n")
				c := &m.pastTurnContainers[containerIdx]
				b.WriteString(renderCompletedContainer(c, width, maxContainerRows))
				containerIdx++
			}
		}
	}

	// Active turn evidence container.
	if m.turnEvidence != nil && len(m.turnEvidence.tools) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderActiveContainer(m.turnEvidence, width, maxContainerRows, m.streaming))
	}

	// Streaming spinner below everything, unless the live evidence container
	// already has the turn's current progress. Keeping the extra assistant
	// block below tool rows makes sticky-bottom scrolling hide the useful
	// tool target behind generic "Qwen is..." text.
	if m.streaming && !m.activeEvidenceContainerVisible() {
		label := assistantLabelStyle.Render("Qwen")
		content := m.spinner.View() + " " + m.streamingBusyText()
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(msgBodyStyle.Render(content))
	}

	if b.Len() == 0 && !m.streaming {
		b.WriteString(msgBodyStyle.Render("No messages yet. Type a prompt below or use /help."))
	}

	m.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) activeEvidenceContainerVisible() bool {
	return m != nil && m.cfg.TUI.EvidencePanel && m.turnEvidence != nil && len(m.turnEvidence.tools) > 0
}

// cachedMessageRender returns the rendered body for a single message, using
// the per-message render cache.
func (m *Model) cachedMessageRender(msg llama.ChatMessage, msgBodyStyle lipgloss.Style) string {
	for i, existing := range m.messages {
		if i >= len(m.perMsgRenderCache) {
			break
		}
		if existing.Role == msg.Role && existing.Content == msg.Content && m.perMsgRenderCache[i] != "" {
			return m.perMsgRenderCache[i]
		}
	}
	return m.renderSingleMessage(msg, msgBodyStyle)
}

func (m *Model) streamingBusyText() string {
	if strings.TrimSpace(m.planPhaseText) != "" {
		return m.planPhaseText
	}
	status := strings.TrimSpace(m.currentTurnBusyStatus)
	if status == "" {
		status = defaultBusyStatus
	}
	return status + m.workingDots()
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
		label = systemLabelStyle.Render(titleDisplay(msg.Role))
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
