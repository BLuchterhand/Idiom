package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
)

func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Cmd, bool) {
	m.width = msg.Width
	m.height = msg.Height
	m.resize()

	return nil, false
}

func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.mouseSelectionMode {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlO:
			m.mouseSelectionMode = false
			m.status = "Selection mode off. Mouse support restored."
			return tea.EnableMouseCellMotion, true
		default:
			return nil, false
		}
	}

	if m.laneSwitchPopup.open {
		if cmd, handled := m.handleLaneSwitchPopupKey(msg); handled {
			return cmd, true
		}
	}

	if m.inputHistoryIndex >= 0 && inputHistoryNavigationKey(msg) {
		if cmd, handled := m.handleInputHistoryNavigation(msg); handled {
			return cmd, true
		}
	}

	// Slash-command suggestion navigation takes priority when the popup is visible.
	if m.slashSuggestions.visible && msg.Type == tea.KeyEnter && slashSuggestionInputReadyToSubmit(m.input.Value(), m.slashSuggestions.matches) {
		m.slashSuggestions = slashSuggestionsState{}
	} else if m.slashSuggestions.visible {
		switch msg.Type {
		case tea.KeyEsc:
			m.slashSuggestions = slashSuggestionsState{}
			return m.waitForEventCmd(), true
		case tea.KeyTab, tea.KeyEnter:
			if m.slashSuggestions.selected >= 0 && m.slashSuggestions.selected < len(m.slashSuggestions.matches) {
				sel := m.slashSuggestions.matches[m.slashSuggestions.selected].command
				// Preserve any text on prior lines (multi-line input).
				oldVal := m.input.Value()
				if idx := strings.LastIndex(oldVal, "\n"); idx >= 0 {
					m.input.SetValue(oldVal[:idx+1] + sel + " ")
				} else {
					m.input.SetValue(sel + " ")
				}

				m.input.CursorEnd()
			}

			m.slashSuggestions = slashSuggestionsState{}

			return m.waitForEventCmd(), true
		case tea.KeyUp:
			if m.slashSuggestions.selected > 0 {
				m.slashSuggestions.selected--
			}

			return m.waitForEventCmd(), true
		case tea.KeyDown:
			if m.slashSuggestions.selected < len(m.slashSuggestions.matches)-1 {
				m.slashSuggestions.selected++
			}

			return m.waitForEventCmd(), true
		}
	}

	if m.history.panelOpen && m.history.focused && m.panelTab == panelTabPlan {
		if handled, cmd := m.handlePlanPanelKey(msg); handled {
			return cmd, true
		}
	}

	if m.history.panelOpen && m.history.focused && m.panelTab == panelTabDocViewer {
		if handled, cmd := m.handleDocViewerPanelKey(msg); handled {
			return cmd, true
		}
	}

	if m.history.panelOpen && m.history.focused && m.panelTab == panelTabSessions {
		if handled, cmd := m.handleSessionsPanelKey(msg); handled {
			return cmd, true
		}
	}

	if m.history.panelOpen && m.history.focused {
		switch msg.Type {
		case tea.KeyUp:
			if m.history.selected > 0 {
				m.history.selected--

				m.history.expanded = false
				m.renderHistoryPanelContent()
			}

			return m.waitForEventCmd(), true
		case tea.KeyDown:
			if m.history.selected < len(m.history.entries)-1 {
				m.history.selected++

				m.history.expanded = false
				m.renderHistoryPanelContent()
			}

			return m.waitForEventCmd(), true
		case tea.KeyEnter:
			m.history.expanded = !m.history.expanded
			m.renderHistoryPanelContent()

			return m.waitForEventCmd(), true
		case tea.KeyEsc:
			m.history.focused = false
			m.renderHistoryPanelContent()

			return m.waitForEventCmd(), true
		case tea.KeyPgUp, tea.KeyPgDown:
			m.history.viewport, _ = m.history.viewport.Update(msg)
			return m.waitForEventCmd(), true
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "j":
				m.history.viewport.ScrollDown(1)

				return m.waitForEventCmd(), true
			case "k":
				m.history.viewport.ScrollUp(1)

				return m.waitForEventCmd(), true
			}
		}
	}

	if cmd, handled := m.handleInputHistoryNavigation(msg); handled {
		return cmd, true
	}

	if inputKeyResetsHistoryNavigation(msg) {
		m.resetInputHistoryNavigation()
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit, true
	case tea.KeyCtrlS:
		m.saveTranscriptAsync()
	case tea.KeyCtrlY:
		return m.copyConversationTarget("last")
	case tea.KeyCtrlO:
		return m.enableMouseSelectionMode(), true
	case tea.KeyCtrlL:
		m.messages = nil
		m.status = "Cleared chat."
		m.renderMessages(true)
	case tea.KeyCtrlR:
		m.restartServerAsync()
	case tea.KeyCtrlB:
		if !m.history.panelOpen {
			m.panelTab = panelTabHistory
		}

		m.setHistoryPanelOpen(!m.history.panelOpen)

		if m.history.panelOpen {
			m.status = "History panel open. Tab focuses it. Ctrl+T cycles tabs."
		} else {
			m.status = "Panel closed."
		}

		return m.waitForEventCmd(), true
	case tea.KeyCtrlT:
		if !m.history.panelOpen {
			m.setHistoryPanelOpen(true)
		}

		m.panelTab = (m.panelTab + 1) % panelTabCount
		if m.panelTab == panelTabSessions {
			m.refreshSessionsList()
		}

		m.rerenderActivePanel()

		return m.waitForEventCmd(), true
	case tea.KeyTab:
		if m.history.panelOpen {
			m.history.focused = !m.history.focused
			m.rerenderActivePanel()
		}

		return m.waitForEventCmd(), true
	case tea.KeyEnter:
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			if m.thoughts.visible() {
				m.thoughts.expanded = !m.thoughts.expanded
				m.renderMessages(false)

				return m.waitForEventCmd(), true
			}

			// Empty Enter with evidence panel: toggle the last completed
			// container's expand/collapse state.
			if m.cfg.TUI.EvidencePanel && len(m.pastTurnContainers) > 0 {
				last := &m.pastTurnContainers[len(m.pastTurnContainers)-1]

				last.expanded = !last.expanded

				if !last.expanded {
					last.scrollOff = 0 // reset scroll on collapse
				}

				m.renderMessages(false)
			}

			return m.waitForEventCmd(), true
		}

		if !m.streaming {
			m.recordInputSubmission(text)
		}

		m.input.SetValue("")

		if strings.HasPrefix(text, "/") {
			if !m.handleFeaturePlannerCommand(text) && !m.handlePlanExecuteCommand(text) && !m.handleImplementCommand(text) {
				if quit := m.handleSlashCommand(text); quit {
					return tea.Quit, true
				}
			}

			if m.pendingExec != nil {
				cmd := m.pendingExec

				m.pendingExec = nil
				m.renderMessages(true)

				return cmd, true
			}
		} else if m.directPlanExecuteApprovalShouldConsumeOperatorInput(text) {
			m.appendHistoryMessage("user", text, nil)
			answer, err := m.runDirectPlanExecuteApprovalAnswerOnce(text)
			if err != nil {
				m.status = err.Error()
			} else {
				m.status = answer
				m.insertBeforePendingAssistant(llama.ChatMessage{Role: "assistant", Content: answer})
			}
			m.renderPlanPanelContent()
		} else if !m.streaming {
			if m.featurePlannerPhase != "" {
				m.appendHistoryMessage("user", text, nil)
				m.sendProductFeaturePlannerTurnAsync(text)
			} else if m.directFeaturePlannerShouldConsumeOperatorInput(text) {
				m.sendDirectFeaturePlannerAnswerAsync(text)
				m.status = "Dispatching to feature-planner..."
			} else {
				m.appendHistoryMessage("user", text, nil)
				m.updateTUIRequestStatus(text)
				m.sendPromptAsync(text)
			}
		}

		m.renderMessages(true)

		return m.waitForEventCmd(), true
	}

	return nil, false
}

func slashSuggestionInputReadyToSubmit(value string, matches []slashSuggestionEntry) bool {
	lines := strings.Split(value, "\n")
	current := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(current, "/") {
		return false
	}

	fields := strings.Fields(current)
	if len(fields) < 2 {
		return false
	}

	for _, match := range matches {
		if fields[0] == match.command {
			return true
		}
	}

	return false
}

func (m *Model) handleInputHistoryNavigation(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m == nil || (m.slashSuggestions.visible && m.inputHistoryIndex < 0) || (m.history.panelOpen && m.history.focused) {
		return nil, false
	}

	switch msg.Type {
	case tea.KeyUp:
		if len(m.inputHistory) == 0 {
			return nil, false
		}

		if m.inputHistoryIndex < 0 {
			m.inputHistoryDraft = m.input.Value()
			m.inputHistoryIndex = len(m.inputHistory) - 1
		} else if m.inputHistoryIndex > 0 {
			m.inputHistoryIndex--
		}

		m.setInputHistoryNavigationValue(m.inputHistory[m.inputHistoryIndex])

		return m.waitForEventCmd(), true
	case tea.KeyDown:
		if m.inputHistoryIndex < 0 {
			return nil, false
		}

		m.inputHistoryIndex++
		if m.inputHistoryIndex >= len(m.inputHistory) {
			m.inputHistoryIndex = -1
			m.setInputHistoryNavigationValue(m.inputHistoryDraft)

			m.inputHistoryDraft = ""
		} else {
			m.setInputHistoryNavigationValue(m.inputHistory[m.inputHistoryIndex])
		}

		return m.waitForEventCmd(), true
	default:
		return nil, false
	}
}

func inputHistoryNavigationKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyUp || msg.Type == tea.KeyDown
}

func (m *Model) setInputHistoryNavigationValue(value string) {
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.updateSlashSuggestions()

	oldInputHeight := m.input.Height()
	if m.ready && m.syncInputHeight() != oldInputHeight {
		m.resize()
	}
}

func (m *Model) resetInputHistoryNavigation() {
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
}

func inputKeyResetsHistoryNavigation(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete:
		return true
	default:
		return false
	}
}

func (m *Model) handlePlanExecuteCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/plan-execute" {
		return false
	}

	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/plan-execute"))
	if objective == "" {
		m.status = "Usage: /plan-execute <objective>"
		return true
	}

	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}

	m.appendHistoryMessage("user", objective, nil)
	m.sendPlanExecuteCommandAsync(objective)

	m.status = "Dispatching to plan-execute..."

	return true
}

func (m *Model) handleFeaturePlannerCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/feature-planner" {
		return false
	}

	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/feature-planner"))
	if objective == "" {
		m.status = "Usage: /feature-planner <objective>"
		return true
	}

	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}

	m.appendHistoryMessage("user", raw, nil)
	m.sendPromptAsync(raw)

	m.status = "Dispatching to feature-planner..."

	return true
}

func (m *Model) handleImplementCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/implement" {
		return false
	}

	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/implement"))
	if objective == "" {
		m.status = "Usage: /implement <objective>"
		return true
	}

	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}

	m.appendHistoryMessage("user", objective, nil)
	m.sendImplementCommandAsync(objective)

	m.status = "Dispatching to implement..."

	return true
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.mouseSelectionMode {
		return m.waitForEventCmd(), true
	}

	if m.laneSwitchPopup.open {
		if m.handleLaneSwitchPopupMouse(msg) {
			return m.waitForEventCmd(), true
		}
	}

	if m.history.panelOpen && msg.X >= m.panelStartX() {
		if isPrimaryMousePress(msg) {
			if m.handlePanelClick(msg) {
				return m.waitForEventCmd(), true
			}
		}

		m.updateActivePanelViewport(msg)
	} else {
		if m.handleMainViewportThoughtPanelMouse(msg) {
			return m.waitForEventCmd(), true
		}

		if isPrimaryMousePress(msg) {
			m.history.focused = false
			m.rerenderActivePanel()
		}

		m.updateViewport(msg)
	}

	return m.waitForEventCmd(), true
}

func (m *Model) filterTerminalMouseReportFragmentKey(msg tea.Msg) (tea.Msg, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok || keyMsg.Type != tea.KeyRunes || keyMsg.Paste {
		return msg, false
	}

	filtered, changed := m.stripLeakedTerminalMouseReports(string(keyMsg.Runes))
	if !changed {
		return msg, false
	}
	if filtered == "" {
		return nil, true
	}

	keyMsg.Runes = []rune(filtered)
	return keyMsg, true
}

func (m *Model) stripLeakedTerminalMouseReports(value string) (string, bool) {
	if value == "" {
		return value, false
	}

	var out strings.Builder
	changed := false

	// Parsed tea.MouseMsg values are consumed before the textarea. This catches
	// terminal mouse protocol tails that arrive as printable runes instead.
	for value != "" {
		if mouseReportPrefixFragment(value) {
			return out.String(), true
		}

		if n := m.sgrMouseReportLen(value); n > 0 {
			value = value[n:]
			changed = true
			continue
		}

		if n := m.urxvtMouseReportLen(value); n > 0 {
			value = value[n:]
			changed = true
			continue
		}

		if n := m.x10WheelMouseReportLen(value); n > 0 {
			value = value[n:]
			changed = true
			continue
		}

		r, size := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteRune(r)
		value = value[size:]
	}

	if !changed {
		return value, false
	}

	return out.String(), true
}

func mouseReportPrefixFragment(value string) bool {
	switch {
	case strings.HasPrefix(value, "[<"):
		return semicolonNumberMousePrefix(value[2:])
	case strings.HasPrefix(value, "["):
		return semicolonNumberMousePrefix(value[1:])
	default:
		return false
	}
}

func semicolonNumberMousePrefix(value string) bool {
	if value == "" {
		return false
	}

	fields := 0
	digitsInField := 0
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digitsInField++
		case r == ';':
			if digitsInField == 0 {
				return false
			}
			fields++
			digitsInField = 0
		default:
			return false
		}
	}

	return fields > 0
}

func (m *Model) sgrMouseReportLen(value string) int {
	if !strings.HasPrefix(value, "[<") {
		return 0
	}

	i := 2
	var fields [3]int
	for field := 0; field < 3; field++ {
		start := i
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			fields[field] = fields[field]*10 + int(value[i]-'0')
			i++
		}
		if i == start {
			return 0
		}
		if field < 2 {
			if i >= len(value) || value[i] != ';' {
				return 0
			}
			i++
		}
	}

	if i >= len(value) || (value[i] != 'M' && value[i] != 'm') {
		return 0
	}

	width, height := m.mouseReportBounds()
	if width > 0 && height > 0 {
		x := fields[1] - 1
		y := fields[2] - 1
		if x < 0 || x >= width || y < 0 || y >= height {
			return 0
		}
	}

	return i + 1
}

func (m *Model) urxvtMouseReportLen(value string) int {
	if !strings.HasPrefix(value, "[") || strings.HasPrefix(value, "[<") {
		return 0
	}

	i := 1
	var fields [3]int
	for field := 0; field < 3; field++ {
		start := i
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			fields[field] = fields[field]*10 + int(value[i]-'0')
			i++
		}
		if i == start {
			return 0
		}
		if field < 2 {
			if i >= len(value) || value[i] != ';' {
				return 0
			}
			i++
		}
	}

	if i >= len(value) || value[i] != 'M' {
		return 0
	}

	width, height := m.mouseReportBounds()
	if width <= 0 || height <= 0 {
		return 0
	}

	x := fields[1] - 1
	y := fields[2] - 1
	if x < 0 || x >= width || y < 0 || y >= height {
		return 0
	}

	return i + 1
}

func (m *Model) x10WheelMouseReportLen(value string) int {
	const reportLen = 5
	if !strings.HasPrefix(value, "[M") {
		return 0
	}

	report := []rune(value)
	if len(report) < reportLen {
		return 0
	}

	width, height := m.mouseReportBounds()
	if width <= 0 || height <= 0 {
		return 0
	}

	encodedButton := int(report[2]) - 32
	if encodedButton < 0 || encodedButton&64 == 0 {
		return 0
	}

	x := int(report[3]) - 33
	y := int(report[4]) - 33
	if x < 0 || x >= width || y < 0 || y >= height {
		return 0
	}

	return len(string(report[:reportLen]))
}

func (m *Model) mouseReportBounds() (int, int) {
	if m == nil {
		return 0, 0
	}

	width, height := m.width, m.height
	if width <= 0 {
		width = m.viewport.Width
	}
	if height <= 0 {
		height = m.viewport.Height
	}

	return width, height
}

func (m *Model) handleMainViewportThoughtPanelMouse(msg tea.MouseMsg) bool {
	if m == nil || msg.X < 0 || msg.X >= m.viewport.Width {
		return false
	}

	row := msg.Y - 2 // header row and top divider sit above the content row.
	if row < 0 || row >= m.viewport.Height {
		return false
	}

	headerRow, bodyStart, bodyEnd, ok := thoughtPanelViewportRows(m.viewport.View())
	if !ok || row < headerRow || row > bodyEnd {
		return false
	}

	if isPrimaryMousePress(msg) && row == headerRow {
		m.thoughts.expanded = !m.thoughts.expanded
		m.renderMessages(false)

		return true
	}

	if row < bodyStart {
		return false
	}

	switch msg.Type {
	case tea.MouseWheelUp:
		if m.thoughts.scroll(-3) {
			m.renderMessages(false)
		}

		return true
	case tea.MouseWheelDown:
		if m.thoughts.scroll(3) {
			m.renderMessages(false)
		}

		return true
	}

	return false
}

func isPrimaryMousePress(msg tea.MouseMsg) bool {
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		return true
	}

	return msg.Type == tea.MouseLeft
}

func thoughtPanelViewportRows(view string) (headerRow, bodyStart, bodyEnd int, ok bool) {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "thoughts ·") {
			headerRow = i
			bodyStart = i + 1
			bodyEnd = i

			for j := bodyStart; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "" {
					break
				}

				bodyEnd = j
			}

			return headerRow, bodyStart, bodyEnd, true
		}
	}

	return 0, 0, 0, false
}

func (m *Model) panelStartX() int {
	if m == nil {
		return 0
	}

	return m.viewport.Width + 1
}

func (m *Model) handlePanelClick(msg tea.MouseMsg) bool {
	panelX := msg.X - m.panelStartX()
	panelY := msg.Y - 2 // header row and top divider sit above the content row.

	if panelX < 0 || panelY < 0 {
		return false
	}

	if panelY == 0 {
		tab, ok := m.panelTabAtX(panelX)
		if !ok {
			return false
		}

		m.panelTab = tab
		m.history.focused = true

		if m.panelTab == panelTabSessions {
			m.refreshSessionsList()
		}

		m.rerenderActivePanel()

		return true
	}

	m.history.focused = true

	return m.handleActivePanelContentClick(panelY - 1)
}

func (m *Model) panelTabAtX(x int) (int, bool) {
	if x < 0 {
		return 0, false
	}

	labels := m.panelTabLabels()
	offset := 0

	for _, tab := range labels {
		width := lipgloss.Width(" " + tab.label + " ")
		if x >= offset && x < offset+width {
			return tab.idx, true
		}

		offset += width
	}

	return 0, false
}

type panelTabLabel struct {
	idx   int
	label string
}

func (m *Model) panelTabLabels() []panelTabLabel {
	planLabel := "Plan"
	if checked, total := m.planCheckboxCounts(); total > 0 {
		planLabel = fmt.Sprintf("Plan · %d/%d", checked, total)
	}

	return []panelTabLabel{
		{panelTabHistory, "History"},
		{panelTabPlan, planLabel},
		{panelTabDocViewer, "Doc Viewer"},
		{panelTabSessions, "Sessions"},
	}
}

func (m *Model) handleActivePanelContentClick(row int) bool {
	if row < 0 {
		return false
	}

	switch m.panelTab {
	case panelTabPlan:
		return m.handlePlanPanelClick(row)
	case panelTabDocViewer:
		return m.handleDocViewerPanelClick(row)
	case panelTabSessions:
		return m.handleSessionsPanelClick(row)
	default:
		return m.handleHistoryPanelClick(row)
	}
}

func (m *Model) updateActivePanelViewport(msg tea.Msg) {
	switch m.panelTab {
	case panelTabPlan:
		m.planViewport, _ = m.planViewport.Update(msg)
	case panelTabDocViewer:
		m.docViewer.viewport, _ = m.docViewer.viewport.Update(msg)
	case panelTabSessions:
		m.sessions.viewport, _ = m.sessions.viewport.Update(msg)
	default:
		m.history.viewport, _ = m.history.viewport.Update(msg)
	}
}

func (m *Model) handleHistoryPanelClick(row int) bool {
	return false
}

func (m *Model) handlePlanPanelClick(row int) bool {
	if m.plan.overlayID != "" {
		return false
	}

	sourceLine, ok := m.planSourceLineForRenderedRow(row + m.planViewport.YOffset)
	if !ok {
		return false
	}

	rows := m.currentPlanSelectableRows()
	idx := -1

	for i, selectableRow := range rows {
		if selectableRow.LineIdx == sourceLine {
			idx = i
			break
		}
	}

	if idx < 0 {
		return false
	}

	m.plan.cursor = idx
	m.activateSelectedPlanRow()

	return true
}

func (m *Model) planSourceLineForRenderedRow(viewportRow int) (int, bool) {
	if viewportRow < 0 {
		return 0, false
	}

	if viewportRow < len(m.plan.renderedRows) {
		sourceLine := m.plan.renderedRows[viewportRow].SourceLineIdx
		if sourceLine < 0 {
			return 0, false
		}

		return sourceLine, true
	}

	renderedLine := viewportRow - 1 // plan viewport row 0 is the panel title.
	displayLines := m.currentPlanDisplayLines()
	if renderedLine < 0 || renderedLine >= len(displayLines) {
		return 0, false
	}

	return displayLines[renderedLine].LineIdx, true
}

func (m *Model) handleDocViewerPanelClick(row int) bool {
	line := viewportLineAt(m.docViewer.viewport.View(), row)
	idx, ok := docViewerEditIndexFromLine(line)

	if !ok {
		return false
	}

	artifact, _, _ := m.loadDocViewerArtifact()
	if idx < 0 || idx >= len(artifact.Edits) {
		return false
	}

	if m.docViewer.expanded == nil {
		m.docViewer.expanded = make(map[int]bool)
	}

	m.docViewer.cursor = idx
	m.docViewer.expanded[idx] = !m.docViewer.expanded[idx]
	m.renderDocViewerPanelContent()

	return true
}

func (m *Model) handleSessionsPanelClick(row int) bool {
	line := viewportLineAt(m.sessions.viewport.View(), row)
	idx := -1

	for i, session := range m.sessions.list {
		if session.ID != "" && strings.Contains(line, session.ID) {
			idx = i
			break
		}
	}

	if idx < 0 {
		return false
	}

	m.sessions.cursor = idx
	m.resumeSelectedSession()
	m.renderSessionsPanelContent()

	return true
}

func docViewerEditIndexFromLine(line string) (int, bool) {
	start := strings.Index(line, "Edit ")
	if start < 0 {
		return 0, false
	}

	rest := line[start+len("Edit "):]
	end := 0

	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}

	if end == 0 {
		return 0, false
	}

	n, err := strconv.Atoi(rest[:end])
	if err != nil || n <= 0 {
		return 0, false
	}

	return n - 1, true
}

func viewportLineAt(view string, row int) string {
	if row < 0 {
		return ""
	}

	lines := strings.Split(view, "\n")
	if row >= len(lines) {
		return ""
	}

	return lines[row]
}

func (m *Model) handleSpinnerTick(msg spinner.TickMsg) (tea.Cmd, bool) {
	var cmd tea.Cmd

	m.spinner, cmd = m.spinner.Update(msg)

	if m.streaming {
		m.workingFrame = (m.workingFrame + 1) % 4
		m.renderMessages(false)
	}

	return cmd, false
}

func (m *Model) handleServerStatus(msg serverStatusMsg) (tea.Cmd, bool) {
	m.bootingServer = false
	m.logServerEvent(msg)

	if msg.err != nil {
		m.serverUp = false
		m.setRuntimeError("Server error: " + msg.err.Error())
	} else {
		m.serverUp = msg.running
		if !msg.running {
			m.modelID = ""
			m.modelDisplayID = ""
		}

		m.modelID = msg.modelID
		m.modelDisplayID = textutil.Fallback(strings.TrimSpace(msg.displayID), strings.TrimSpace(msg.modelID))
		m.managedServer = m.managedServer || msg.owned
		m.clearRuntimeErrorIfServerRelated()

		if msg.running {
			m.status = fmt.Sprintf("Server ready on %s. Model: %s", m.plannerSpec.EndpointBaseURL(), m.activeModelDisplayID())
		} else {
			m.status = "Server stopped."
		}
	}

	return nil, false
}

func (m *Model) handleStreamDone(msg streamDoneMsg) (tea.Cmd, bool) {
	m.streaming = false
	m.workingFrame = 0
	m.planPhaseText = ""
	m.currentTurnBusyStatus = ""

	if msg.err != nil {
		m.failAgentHistoryTurn(msg.err)
		m.setRuntimeError(formatModelFailureStatus(msg.err))
	} else {
		m.clearRuntimeError()

		m.status = "Response complete."
		m.recordAgentTurn()
	}

	// Finalize the evidence container and move to past containers.
	if m.turnEvidence != nil && len(m.turnEvidence.tools) > 0 {
		m.turnEvidence.finalized = true

		// Deep-copy rows into a completedEvidence struct.
		rows := make([]evidenceRow, len(m.turnEvidence.tools))
		copy(rows, m.turnEvidence.tools)

		_, catBreakdown, verifyStatus := evidenceStats(rows)
		budgetStr := ""

		if m.lastBudget != nil {
			budgetStr = formatBudgetSummary(m.lastBudget)
		}

		summary := renderEvidenceSummary(len(rows), catBreakdown, verifyStatus, budgetStr, false)

		completed := completedEvidence{
			tools:    rows,
			summary:  summary,
			expanded: msg.err != nil, // stay expanded on error
			budget:   m.lastBudget,
		}

		m.lastBudget = nil
		m.pastTurnContainers = append(m.pastTurnContainers, completed)
		m.turnEvidence = nil

		// Start 2s auto-collapse timer unless there was an error (stays open).
		if msg.err == nil {
			m.renderMessages(false)

			return func() tea.Msg {
				time.Sleep(2 * time.Second)

				return evidenceCollapseMsg{}
			}, false
		}
	} else {
		m.turnEvidence = nil
	}

	m.renderMessages(false)

	return nil, false
}

func (m *Model) handleThought(msg thoughtMsg) (tea.Cmd, bool) {
	if strings.TrimSpace(msg.text) == "" {
		return nil, false
	}

	m.thoughts.append(msg.step, msg.text)
	m.renderMessages(false)

	return nil, false
}

func (m *Model) handleTranscriptSaved(msg transcriptSavedMsg) (tea.Cmd, bool) {
	if msg.err != nil {
		m.setRuntimeError("Save failed: " + msg.err.Error())
	} else {
		m.clearRuntimeError()

		m.status = "Transcript saved to " + m.displayPath(msg.path)
		m.logArtifact("transcript", msg.path)
	}

	return nil, false
}

func (m *Model) handleClipboardCopyDone(msg clipboardCopyDoneMsg) (tea.Cmd, bool) {
	if msg.err != nil {
		m.status = "Copy failed: " + msg.err.Error()
		return nil, false
	}

	m.status = fmt.Sprintf("Copied %s to clipboard (%d bytes).", msg.target, msg.bytes)
	return nil, false
}

func (m *Model) handleInfoMessage(msg infoMsg) (tea.Cmd, bool) {
	m.status = msg.text
	return nil, false
}

func (m *Model) handleToolTrace(msg toolTraceMsg) (tea.Cmd, bool) {
	if strings.TrimSpace(msg.busyStatus) != "" {
		m.currentTurnBusyStatus = msg.busyStatus
	}

	if m.traceMode == traceDisplayDetail && !msg.running {
		m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: msg.text})
		m.logMessage("tool", msg.text)
	}

	m.recordToolTraceInHistory(msg.step, msg.text, msg.intent, msg.running, msg.auditKind, msg.outcome, msg.auditEvents)
	m.renderMessages(false)

	return nil, false
}

// evidenceCollapseMsg is sent by the auto-collapse timer after streamDoneMsg.
type evidenceCollapseMsg struct{}

func (m *Model) handleBudgetLedger(msg budgetLedgerMsg) (tea.Cmd, bool) {
	m.lastBudget = &budgetData{
		totalTokens:     msg.totalTokens,
		usedToolResults: msg.usedToolResults,
		maxToolSteps:    msg.maxToolSteps,
		usedModelCalls:  msg.usedModelCalls,
		maxModelCalls:   msg.maxModelCalls,
		lane:            msg.lane,
	}
	// If the turn already finalized, update the last past container's budget
	// and re-render so the summary line reflects the budget.
	if n := len(m.pastTurnContainers); n > 0 {
		m.pastTurnContainers[n-1].budget = m.lastBudget
		// Rebuild the summary line with budget included.
		rows := m.pastTurnContainers[n-1].tools
		_, catBreakdown, verifyStatus := evidenceStats(rows)
		budgetStr := formatBudgetSummary(m.lastBudget)

		m.pastTurnContainers[n-1].summary = renderEvidenceSummary(len(rows), catBreakdown, verifyStatus, budgetStr, m.pastTurnContainers[n-1].expanded)
	}

	m.renderMessages(false)

	return nil, false
}

func (m *Model) handleEvidenceCollapse(msg evidenceCollapseMsg) (tea.Cmd, bool) {
	// Auto-collapse the last completed container if it was untouched.
	// The timer only fires if no user interaction cancelled it.
	if n := len(m.pastTurnContainers); n > 0 {
		m.pastTurnContainers[n-1].expanded = false
	}

	m.renderMessages(false)

	return nil, false
}

func (m *Model) handleHistoryAudit(msg historyAuditMsg) (tea.Cmd, bool) {
	m.recordHistoryAuditEvent(msg.event)

	return nil, false
}

func (m *Model) handleHistorySummary(msg historySummaryMsg) (tea.Cmd, bool) {
	if msg.index >= 0 && msg.index < len(m.history.entries) {
		m.history.entries[msg.index].summary = msg.summary
		m.requestHistoryPanelRender(false, false)
	}

	return nil, false
}

func (m *Model) handleHistoryPanelRefresh(msg historyPanelRefreshMsg) (tea.Cmd, bool) {
	if m.history.renderPending {
		m.flushHistoryPanelRender()
	}

	return nil, false
}

func (m *Model) handlePlanEditorDone(msg planEditorDoneMsg) (tea.Cmd, bool) {
	if msg.err != nil {
		m.status = "Editor exited with error: " + msg.err.Error()
	} else {
		m.status = "Plan updated: " + m.displayPath(m.planPath)
	}

	m.renderPlanPanelContent()

	return nil, false
}

func (m *Model) handlePlanRefresh(msg planRefreshMsg) (tea.Cmd, bool) {
	if !m.history.panelOpen && !m.plan.autoShown {
		if m.planPanelAutoShowNeeded() {
			m.panelTab = panelTabPlan
			m.setHistoryPanelOpen(true)

			m.plan.autoShown = true
		}
	}

	m.renderPlanPanelContent()

	return nil, false
}

func (m *Model) handlePlanPhase(msg planPhaseMsg) (tea.Cmd, bool) {
	if msg.phase == "complete" {
		m.planPhaseText = ""
	} else {
		m.planPhaseText = formatPlanPhaseText(msg.phase, msg.detail)
	}

	m.recordHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "plan-execute " + strings.TrimSpace(msg.phase), detail: msg.detail, status: msg.phase})
	m.renderMessages(false)

	return nil, false
}
