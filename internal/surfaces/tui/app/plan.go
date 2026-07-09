package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

// planCommentRe strips HTML-comment anchors (`<!-- id:... -->`,
// `<!-- tid:... -->`) from a plan line before display. The anchors are
// load-bearing in the file (they tie a line to its ledger item / diff
// record) but render as noise in the TUI.
var planCommentRe = regexp.MustCompile(`\s*<!--[^>]*-->`)

const (
	panelTabHistory   = 0
	panelTabPlan      = 1
	panelTabDocViewer = 2
	panelTabSessions  = 3
	panelTabCount     = 4
)

type planSelectableKind int

const (
	planSelectableHeading planSelectableKind = iota + 1
	planSelectableItem
)

type planSelectableRow struct {
	Kind       planSelectableKind
	LineIdx    int
	HeadingKey string
	Heading    string
	Item       planItemMeta
}

type planDisplayLine struct {
	LineIdx int
}

type planRenderedRow struct {
	SourceLineIdx int
}

// planDirName is the directory (relative to workspace root) where per-session
// plan markdown files live.
const planDirName = ".planning"

// ensurePlanPath derives the plan file path for this session from the active
// debug-log path. The plan file mirrors the session id so the plan and the
// conversation stay linked.
func (m *Model) ensurePlanPath() string {
	if m.planPath != "" {
		return m.planPath
	}

	id := m.sessionID()
	if id == "" {
		return ""
	}

	dir := planDirName
	if root := m.workspace.Root(); root != "" {
		dir = filepath.Join(root, planDirName)
	}

	_ = os.MkdirAll(dir, 0o755)
	m.planPath = filepath.Join(dir, id+".md")

	return m.planPath
}

// sessionID extracts the session id from the debug-log path. Returns "" if
// the debug logger hasn't been initialized yet.
func (m *Model) sessionID() string {
	path := strings.TrimSpace(m.debugLogPath)
	if path == "" {
		return ""
	}

	base := filepath.Base(path)

	return strings.TrimSuffix(base, ".jsonl")
}

// loadPlanContent re-reads the plan file from disk. Safe to call anytime;
// returns "" if the file doesn't exist yet.
func (m *Model) loadPlanContent() string {
	path := m.ensurePlanPath()
	if path == "" {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			m.planLoadError = ""
		} else {
			m.planLoadError = err.Error()
		}

		m.planMTime = time.Time{}
		m.planContent = ""

		return ""
	}

	if info.ModTime().Equal(m.planMTime) && m.planLoadError == "" {
		return m.planContent
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			m.planLoadError = ""
		} else {
			m.planLoadError = err.Error()
		}

		m.planMTime = time.Time{}
		m.planContent = ""

		return ""
	}

	m.planLoadError = ""
	m.planMTime = info.ModTime()
	m.planContent = string(data)

	return m.planContent
}

func (m *Model) invalidatePlanCache() {
	if m == nil {
		return
	}

	m.planMTime = time.Time{}
	m.planContent = ""
}

// renderPlanPanelContent rebuilds the plan viewport from the current plan file.
func (m *Model) renderPlanPanelContent() {
	if !m.history.panelOpen {
		return
	}

	w := max(8, m.planViewport.Width)

	// Diff overlay takes precedence — when the user picks an entry,
	// we swap the entire panel content for the colored diff so the
	// familiar j/k scrolling applies to the diff payload directly.
	if m.plan.overlayID != "" && m.plan.overlayBody != "" {
		var b strings.Builder

		title := "◇ Plan · diff · Esc to close"
		b.WriteString(panelTitleStyle.Width(w).Render(title))
		b.WriteString("\n")
		b.WriteString(m.plan.overlayBody)
		m.plan.renderedRows = nil
		m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))

		return
	}

	var b strings.Builder
	var renderedRows []planRenderedRow

	title := "◇ Plan"

	if m.history.focused && m.panelTab == panelTabPlan {
		// When the panel has focus, surface the keybinds so the user
		// knows ↑/↓ navigates and Enter opens a diff. Without this hint
		// the interactivity is invisible.
		title = "◇ Plan · ↑/↓ select · Enter diff/fold"
		if m.currentPlanApproval() != nil {
			title = "◇ Plan · ↑/↓ select · Enter approve/diff/fold"
		}
	}

	renderedTitle := panelTitleStyle.Width(w).Render(title)
	b.WriteString(renderedTitle)
	b.WriteString("\n")
	renderedRows = appendPlanRenderedRows(renderedRows, renderedTitle, -1)

	content := m.loadPlanContent()
	path := m.ensurePlanPath()

	if strings.TrimSpace(content) == "" {
		m.plan.renderedRows = nil
		lines := []string{
			"No plan yet.",
			"",
			"Create or edit:",
			"  " + m.displayPath(path),
			"",
			"Or run /plan to open it in $EDITOR.",
			"",
			"Use markdown checkboxes to track work:",
			"  - [ ] step one",
			"  - [x] done step",
		}
		if m.planLoadError != "" {
			lines = append(lines, "", "Load error: "+m.planLoadError)
		}

		for _, line := range lines {
			for _, wl := range wrapLines(line, max(1, w-2)) {
				b.WriteString(panelDetailStyle.Width(w).Render(wl))
				b.WriteString("\n")
			}
		}

		m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))

		return
	}

	// Render markdown with checkbox highlighting and collapsible heading
	// sections. When the plan panel has focus and there are selectable
	// rows, highlight the cursor row so the user sees which Enter will
	// activate.
	if approvalStatus := m.planApprovalStatusText(); approvalStatus != "" {
		rendered := panelDetailStyle.Width(w).Render(approvalStatus)
		b.WriteString(rendered)
		b.WriteString("\n\n")
		renderedRows = appendPlanRenderedRows(renderedRows, rendered, -1)
		renderedRows = append(renderedRows, planRenderedRow{SourceLineIdx: -1})
	}

	selectableRows := m.currentPlanSelectableRowsForContent(content)

	var cursorLineIdx = -1

	if m.panelTab == panelTabPlan && m.history.focused && len(selectableRows) > 0 {
		m.normalizePlanCursor(selectableRows)
		cursorLineIdx = selectableRows[m.plan.cursor].LineIdx
	}

	lines := strings.Split(content, "\n")
	for _, displayLine := range m.currentPlanDisplayLinesForContent(content) {
		if displayLine.LineIdx < 0 || displayLine.LineIdx >= len(lines) {
			continue
		}

		raw := lines[displayLine.LineIdx]
		rendered := m.renderPlanLine(raw, displayLine.LineIdx, w)
		if displayLine.LineIdx == cursorLineIdx {
			prefix := "▸ "
			if _, _, ok := parsePlanHeading(raw); ok {
				prefix = ""
			}
			rendered = panelFocusedStyle.Width(w).Render(prefix + m.planFocusedLineText(raw, displayLine.LineIdx))
		}

		b.WriteString(rendered)
		b.WriteString("\n")
		renderedRows = appendPlanRenderedRows(renderedRows, rendered, displayLine.LineIdx)
	}

	m.plan.renderedRows = renderedRows
	m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

func appendPlanRenderedRows(rows []planRenderedRow, rendered string, sourceLineIdx int) []planRenderedRow {
	height := max(1, lipgloss.Height(rendered))
	for range height {
		rows = append(rows, planRenderedRow{SourceLineIdx: sourceLineIdx})
	}

	return rows
}

func (m *Model) currentPlanSelectableRows() []planSelectableRow {
	return m.currentPlanSelectableRowsForContent(m.loadPlanContent())
}

func (m *Model) currentPlanSelectableRowsForContent(content string) []planSelectableRow {
	var rows []planSelectableRow

	m.visitVisiblePlanLines(content, func(i int, raw string) {
		if _, title, ok := parsePlanHeading(raw); ok {
			rows = append(rows, planSelectableRow{
				Kind:       planSelectableHeading,
				LineIdx:    i,
				HeadingKey: planHeadingKey(i, raw),
				Heading:    title,
			})

			return
		}

		if item, ok := parsePlanItemLine(i, raw); ok {
			rows = append(rows, planSelectableRow{
				Kind:    planSelectableItem,
				LineIdx: i,
				Item:    item,
			})
		}
	})

	return rows
}

func (m *Model) currentPlanDisplayLines() []planDisplayLine {
	return m.currentPlanDisplayLinesForContent(m.loadPlanContent())
}

func (m *Model) currentPlanDisplayLinesForContent(content string) []planDisplayLine {
	var lines []planDisplayLine

	m.visitVisiblePlanLines(content, func(i int, raw string) {
		lines = append(lines, planDisplayLine{LineIdx: i})
	})

	return lines
}

func (m *Model) visitVisiblePlanLines(content string, visit func(lineIdx int, raw string)) {
	hiddenLevel := 0

	for i, raw := range strings.Split(content, "\n") {
		if level, _, ok := parsePlanHeading(raw); ok {
			if hiddenLevel > 0 && level <= hiddenLevel {
				hiddenLevel = 0
			}

			if hiddenLevel > 0 {
				continue
			}

			visit(i, raw)
			if m.planHeadingCollapsed(i, raw) {
				hiddenLevel = level
			}

			continue
		}

		if hiddenLevel > 0 {
			continue
		}

		visit(i, raw)
	}
}

func (m *Model) normalizePlanCursor(rows []planSelectableRow) {
	if len(rows) == 0 {
		m.plan.cursor = 0

		return
	}

	if m.plan.cursor < 0 {
		m.plan.cursor = 0
	}

	if m.plan.cursor >= len(rows) {
		m.plan.cursor = len(rows) - 1
	}
}

func (m *Model) openPlanDiffOverlayForItem(item planItemMeta) bool {
	if item.DiffID == "" {
		return false
	}

	entry, err := m.loadPlanDiffEntry(item.DiffID)
	if err != nil {
		m.plan.overlayID = item.DiffID
		m.plan.overlayBody = diffHeaderStyle.Render(fmt.Sprintf("(could not load diff %s: %v)", item.DiffID, err))

		return true
	}

	m.plan.overlayID = item.DiffID
	m.plan.overlayBody = renderPlanDiffOverlay(entry)

	return true
}

// closePlanDiffOverlay switches back to the plan list view.
func (m *Model) closePlanDiffOverlay() {
	m.plan.overlayID = ""
	m.plan.overlayBody = ""
}

// handlePlanPanelKey consumes a key press when the plan panel has
// keyboard focus. Returns (true, cmd) if the key was handled; returns
// (false, nil) to let the shared panel handler take over (e.g. j/k
// scrolling within the overlay, which re-uses the generic path).
//
// In list mode: j/k moves the cursor, Enter opens the selected
// entry's diff. In overlay mode: Esc closes the overlay; j/k fall
// through so the shared viewport scrolling handles the diff text.
func (m *Model) handlePlanPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	// Overlay mode: Esc closes; scroll keys fall through.
	if m.plan.overlayID != "" {
		if msg.Type == tea.KeyEsc {
			m.closePlanDiffOverlay()
			m.renderPlanPanelContent()

			return true, m.waitForEventCmd()
		}

		return false, nil
	}

	switch msg.Type {
	case tea.KeyUp:
		if m.plan.cursor > 0 {
			m.plan.cursor--
			m.renderPlanPanelContent()
		}

		return true, m.waitForEventCmd()
	case tea.KeyDown:
		rows := m.currentPlanSelectableRows()
		if m.plan.cursor < len(rows)-1 {
			m.plan.cursor++
			m.renderPlanPanelContent()
		}

		return true, m.waitForEventCmd()
	case tea.KeyEnter:
		m.activateSelectedPlanRow()

		return true, m.waitForEventCmd()
	case tea.KeyEsc:
		m.history.focused = false
		m.renderPlanPanelContent()

		return true, m.waitForEventCmd()
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			rows := m.currentPlanSelectableRows()
			if m.plan.cursor < len(rows)-1 {
				m.plan.cursor++
				m.renderPlanPanelContent()
			}

			return true, m.waitForEventCmd()
		case "k":
			if m.plan.cursor > 0 {
				m.plan.cursor--
				m.renderPlanPanelContent()
			}

			return true, m.waitForEventCmd()
		}
	}

	return false, nil
}

func (m *Model) activateSelectedPlanRow() {
	rows := m.currentPlanSelectableRows()
	if len(rows) == 0 {
		return
	}

	m.normalizePlanCursor(rows)
	row := rows[m.plan.cursor]

	switch row.Kind {
	case planSelectableHeading:
		if m.plan.collapsedHeadings == nil {
			m.plan.collapsedHeadings = make(map[string]bool)
		}

		m.plan.collapsedHeadings[row.HeadingKey] = !m.plan.collapsedHeadings[row.HeadingKey]
		if m.plan.collapsedHeadings[row.HeadingKey] {
			m.status = "Collapsed " + row.Heading + "."
		} else {
			m.status = "Expanded " + row.Heading + "."
		}
		m.renderPlanPanelContent()
	case planSelectableItem:
		if m.activatePlanApprovalAction(row.Item.PlanApprovalAction) {
			return
		}

		if m.openPlanDiffOverlayForItem(row.Item) {
			m.renderPlanPanelContent()
			m.planViewport.GotoTop()
		} else {
			m.status = "No diff attached to this item."
			m.renderPlanPanelContent()
		}
	}
}

func (m *Model) renderPlanLine(raw string, lineIdx int, w int) string {
	// Strip HTML-comment anchors before display. They remain in the
	// file (that's how click-to-open-diff works), but the user should
	// never see the raw `<!-- id:abc123 -->` noise in the panel.
	visible := planCommentRe.ReplaceAllString(raw, "")
	trimmed := strings.TrimLeft(visible, " \t")
	indent := visible[:len(visible)-len(trimmed)]

	switch {
	case strings.HasPrefix(trimmed, "- [~]") || strings.HasPrefix(trimmed, "* [~]"):
		rest := strings.TrimSpace(trimmed[5:])
		box := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("[~]")
		line := indent + box + " " + rest

		return planInProgressStyle.Width(w).Render(line)
	case strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]"):
		rest := strings.TrimSpace(trimmed[5:])
		box := lipgloss.NewStyle().Foreground(colorMuted).Render("[ ]")
		line := indent + box + " " + rest

		return planTodoStyle.Width(w).Render(line)
	case strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") ||
		strings.HasPrefix(trimmed, "* [x]") || strings.HasPrefix(trimmed, "* [X]"):
		rest := strings.TrimSpace(trimmed[5:])
		// Render the box and the text separately so strikethrough
		// applies only to the text. A strikethrough checkbox glyph
		// was invisible on some fonts, which is what made checked
		// items look boxless.
		box := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("[x]")
		text := planDoneStyle.Render(strings.TrimRight(rest, " "))
		line := indent + box + " " + text

		return lipgloss.NewStyle().Width(w).Render(line)
	case strings.HasPrefix(trimmed, "#"):
		return m.renderPlanHeadingLine(raw, lineIdx, w)
	default:
		return panelDetailStyle.Width(w).Render(visible)
	}
}

func (m *Model) renderPlanHeadingLine(raw string, lineIdx int, w int) string {
	level, title, ok := parsePlanHeading(raw)
	if !ok {
		return panelDetailStyle.Width(w).Render(planCommentRe.ReplaceAllString(raw, ""))
	}

	line := planHeadingControlText(title, m.planHeadingCollapsed(lineIdx, raw))
	switch level {
	case 1:
		return planH1Style.Width(w).Render(line)
	case 2:
		return planH2Style.Width(w).Render(line)
	default:
		return planH3Style.Width(w).Render(line)
	}
}

func (m *Model) planFocusedLineText(raw string, lineIdx int) string {
	if _, title, ok := parsePlanHeading(raw); ok {
		return planHeadingControlText(title, m.planHeadingCollapsed(lineIdx, raw))
	}

	return strings.TrimLeft(planCommentRe.ReplaceAllString(raw, ""), " \t")
}

func (m *Model) planHeadingCollapsed(lineIdx int, raw string) bool {
	if m == nil || m.plan.collapsedHeadings == nil {
		return false
	}

	return m.plan.collapsedHeadings[planHeadingKey(lineIdx, raw)]
}

func planHeadingControlText(title string, collapsed bool) string {
	if collapsed {
		return "▸ " + title
	}

	return "▾ " + title
}

func parsePlanHeading(raw string) (int, string, bool) {
	visible := planCommentRe.ReplaceAllString(raw, "")
	trimmed := strings.TrimLeft(visible, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}

	level := 0
	for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
		level++
	}

	if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}

	title := strings.TrimSpace(trimmed[level+1:])
	if title == "" {
		return 0, "", false
	}

	return level, title, true
}

func planHeadingKey(lineIdx int, raw string) string {
	level, title, ok := parsePlanHeading(raw)
	if !ok {
		return fmt.Sprintf("%d:", lineIdx)
	}

	return fmt.Sprintf("%d:%d:%s", lineIdx, level, title)
}

// prepareEditPlanCmd returns a tea.Cmd (tea.ExecProcess) that opens the plan
// file in $EDITOR ($VISUAL, or vi). It creates the file with a starter
// template if missing. The caller stores the returned cmd on the Model and
// lets Update dispatch it, so Bubble Tea can yield the terminal cleanly.
func (m *Model) prepareEditPlanCmd() (tea.Cmd, string, error) {
	path := m.ensurePlanPath()
	if path == "" {
		return nil, "", fmt.Errorf("plan path is not available (debug log not initialized)")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := fmt.Sprintf("# Plan — %s\n\n## Goals\n- [ ] \n\n## Steps\n- [ ] \n\n## Notes\n\n", m.sessionID())
		if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
			return nil, "", fmt.Errorf("create plan: %w", err)
		}
	}

	editor := textutil.FirstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	editorArgs, err := splitEditorCommand(editor)
	if err != nil {
		return nil, "", err
	}
	c, err := commandexec.PrepareForegroundCommand(context.Background(), commandexec.Spec{
		Name: editorArgs[0],
		Args: append(editorArgs[1:], path),
	}, commandexec.ForegroundIO{})
	if err != nil {
		return nil, "", err
	}
	teaCmd := tea.ExecProcess(c, func(err error) tea.Msg {
		return planEditorDoneMsg{err: err}
	})

	return teaCmd, "Opening plan in editor: " + m.displayPath(path), nil
}

func splitEditorCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return []string{"vi"}, nil
	}

	var (
		args    []string
		current strings.Builder
		quote   rune
		escape  bool
	)

	for _, r := range command {
		switch {
		case escape:
			current.WriteRune(r)
			escape = false
		case r == '\\':
			escape = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if escape {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("editor command has unterminated quote")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("editor command is empty")
	}
	return args, nil
}

// planEditorDoneMsg fires when the external editor exits; Update handles it
// to refresh the plan viewport and surface any error.
type planEditorDoneMsg struct {
	err error
}

// renderPanelTabs builds the header strip that sits above the active tab's
// content. Active tab is highlighted; inactive tabs render dim.
func (m *Model) renderPanelTabs(width int) string {
	var rendered []string

	for _, t := range m.panelTabLabels() {
		label := " " + t.label + " "
		if t.idx == m.panelTab {
			rendered = append(rendered, panelTabActiveStyle.Render(label))
		} else {
			rendered = append(rendered, panelTabInactiveStyle.Render(label))
		}
	}

	bar := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	pad := width - lipgloss.Width(bar)

	if pad > 0 {
		bar = bar + strings.Repeat(" ", pad)
	}

	return bar
}

// activePanelViewport returns the viewport the renderer should display for
// the currently-selected tab.
func (m *Model) activePanelViewport() viewport.Model {
	switch m.panelTab {
	case panelTabPlan:
		return m.planViewport
	case panelTabDocViewer:
		return m.docViewer.viewport
	case panelTabSessions:
		return m.sessions.viewport
	default:
		return m.history.viewport
	}
}

// rerenderActivePanel refreshes whichever tab is currently shown. Use this
// after a state change that could affect the rendered content.
func (m *Model) rerenderActivePanel() {
	switch m.panelTab {
	case panelTabPlan:
		m.renderPlanPanelContent()
	case panelTabDocViewer:
		m.renderDocViewerPanelContent()
	case panelTabSessions:
		m.renderSessionsPanelContent()
	default:
		m.renderHistoryPanelContent()
	}
}

var planCheckboxRe = regexp.MustCompile(`(?m)^[\-\*]\s+\[[ xX~]\]`)

// planPanelAutoShowNeeded returns true when the plan file has at least
// one checkbox line and the panel should be auto-opened. Checks the
// in-memory cache first (fast path); reads from disk only when the
// cache is empty and the mtime differs.
func (m *Model) planPanelAutoShowNeeded() bool {
	if m == nil {
		return false
	}

	family := strings.TrimSpace(m.forcedTaskFamily)
	isPlanExecute := workerruntime.TaskFamilyIsPlanThenExecutePlanner(family) || workerruntime.TaskFamilyIsPlanThenExecuteReview(family)
	content := m.loadPlanContent()

	if strings.TrimSpace(content) == "" {
		return false
	}

	if isPlanExecute {
		return true
	}

	return planCheckboxRe.MatchString(content)
}

// planCheckboxCounts returns (checked, total) counts of checkbox lines
// in the current plan content. Used by the tab label to show progress.
func (m *Model) planCheckboxCounts() (checked, total int) {
	content := m.loadPlanContent()
	if strings.TrimSpace(content) == "" {
		return 0, 0
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "- [") && !strings.HasPrefix(trimmed, "* [") {
			continue
		}

		if len(trimmed) < 5 {
			continue
		}

		total++

		box := trimmed[3]

		if box == 'x' || box == 'X' {
			checked++
		}
	}

	return
}
