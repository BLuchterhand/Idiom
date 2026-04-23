package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// planCommentRe strips HTML-comment anchors (`<!-- id:... -->`,
// `<!-- tid:... -->`) from a plan line before display. The anchors are
// load-bearing in the file (they tie a line to its ledger item / diff
// record) but render as noise in the TUI.
var planCommentRe = regexp.MustCompile(`\s*<!--[^>]*-->`)

const (
	panelTabHistory  = 0
	panelTabPlan     = 1
	panelTabSessions = 2
	panelTabCount    = 3
)

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
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			m.planLoadError = err.Error()
		}
		return ""
	}
	m.planLoadError = ""
	info, err := os.Stat(path)
	if err == nil {
		m.planMTime = info.ModTime()
	}
	return string(data)
}

// renderPlanPanelContent rebuilds the plan viewport from the current plan file.
func (m *Model) renderPlanPanelContent() {
	if !m.historyPanelOpen {
		return
	}
	w := maxInt(8, m.planViewport.Width)

	// Diff overlay takes precedence — when the user picks an entry,
	// we swap the entire panel content for the colored diff so the
	// familiar j/k scrolling applies to the diff payload directly.
	if m.planOverlayID != "" && m.planOverlayBody != "" {
		var b strings.Builder
		title := "◇ Plan · diff · Esc to close"
		b.WriteString(panelTitleStyle.Width(w).Render(title))
		b.WriteString("\n")
		b.WriteString(m.planOverlayBody)
		m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}

	var b strings.Builder
	title := "◇ Plan"
	if m.historyFocused && m.panelTab == panelTabPlan {
		// When the panel has focus, surface the keybinds so the user
		// knows ↑/↓ navigates and Enter opens a diff. Without this hint
		// the interactivity is invisible.
		title = "◇ Plan · ↑/↓ select · Enter view diff"
	}
	b.WriteString(panelTitleStyle.Width(w).Render(title))
	b.WriteString("\n")

	content := m.loadPlanContent()
	path := m.ensurePlanPath()

	if strings.TrimSpace(content) == "" {
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
			for _, wl := range wrapLines(line, maxInt(1, w-2)) {
				b.WriteString(panelDetailStyle.Width(w).Render(wl))
				b.WriteString("\n")
			}
		}
		m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}

	// Render markdown with checkbox highlighting. Use glamour for body, but
	// pre-process checkbox lines for a tighter TUI look. When the plan
	// panel has focus and there are clickable items, we highlight the
	// cursor row so the user sees which Enter will open.
	items := parsePlanItems(content)
	var cursorLineIdx = -1
	if m.panelTab == panelTabPlan && m.historyFocused && len(items) > 0 {
		if m.planCursor < 0 {
			m.planCursor = 0
		}
		if m.planCursor >= len(items) {
			m.planCursor = len(items) - 1
		}
		cursorLineIdx = items[m.planCursor].LineIdx
	}
	for i, raw := range strings.Split(content, "\n") {
		rendered := renderPlanLine(raw, w)
		if i == cursorLineIdx {
			rendered = panelFocusedStyle.Width(w).Render("▸ " + strings.TrimLeft(planCommentRe.ReplaceAllString(raw, ""), " \t"))
		}
		b.WriteString(rendered)
		b.WriteString("\n")
	}

	m.planViewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

// currentPlanItems returns the parsed clickable items of the plan.
// Exposed as a helper for tests and key handlers so the parsing path
// is shared between rendering and navigation.
func (m *Model) currentPlanItems() []planItemMeta {
	return parsePlanItems(m.loadPlanContent())
}

// openPlanDiffOverlay loads the diff entry referenced by the plan item
// under the cursor and switches the panel into overlay mode. Returns
// false when the selected item has no diff id (e.g. an unattached
// ledger stub) so the caller can surface a nudge instead.
func (m *Model) openPlanDiffOverlay() bool {
	items := m.currentPlanItems()
	if len(items) == 0 || m.planCursor < 0 || m.planCursor >= len(items) {
		return false
	}
	item := items[m.planCursor]
	if item.DiffID == "" {
		return false
	}
	entry, err := m.loadPlanDiffEntry(item.DiffID)
	if err != nil {
		m.planOverlayID = item.DiffID
		m.planOverlayBody = diffHeaderStyle.Render(fmt.Sprintf("(could not load diff %s: %v)", item.DiffID, err))
		return true
	}
	m.planOverlayID = item.DiffID
	m.planOverlayBody = renderPlanDiffOverlay(entry)
	return true
}

// closePlanDiffOverlay switches back to the plan list view.
func (m *Model) closePlanDiffOverlay() {
	m.planOverlayID = ""
	m.planOverlayBody = ""
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
	if m.planOverlayID != "" {
		if msg.Type == tea.KeyEsc {
			m.closePlanDiffOverlay()
			m.renderPlanPanelContent()
			return true, waitForEvent(m.events)
		}
		return false, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		if m.planCursor > 0 {
			m.planCursor--
			m.renderPlanPanelContent()
		}
		return true, waitForEvent(m.events)
	case tea.KeyDown:
		items := m.currentPlanItems()
		if m.planCursor < len(items)-1 {
			m.planCursor++
			m.renderPlanPanelContent()
		}
		return true, waitForEvent(m.events)
	case tea.KeyEnter:
		if m.openPlanDiffOverlay() {
			m.renderPlanPanelContent()
			m.planViewport.GotoTop()
			return true, waitForEvent(m.events)
		}
		m.status = "No diff attached to this item."
		return true, waitForEvent(m.events)
	case tea.KeyEsc:
		m.historyFocused = false
		m.renderPlanPanelContent()
		return true, waitForEvent(m.events)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			items := m.currentPlanItems()
			if m.planCursor < len(items)-1 {
				m.planCursor++
				m.renderPlanPanelContent()
			}
			return true, waitForEvent(m.events)
		case "k":
			if m.planCursor > 0 {
				m.planCursor--
				m.renderPlanPanelContent()
			}
			return true, waitForEvent(m.events)
		}
	}
	return false, nil
}

// renderPlanLine applies lightweight styling to a single plan line: checkboxes
// get colored indicators, headings get emphasis, other lines render plain.
func renderPlanLine(raw string, w int) string {
	// Strip HTML-comment anchors before display. They remain in the
	// file (that's how click-to-open-diff works), but the user should
	// never see the raw `<!-- id:abc123 -->` noise in the panel.
	visible := planCommentRe.ReplaceAllString(raw, "")
	trimmed := strings.TrimLeft(visible, " \t")
	indent := visible[:len(visible)-len(trimmed)]

	switch {
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
	case strings.HasPrefix(trimmed, "# "):
		return planH1Style.Width(w).Render(strings.TrimPrefix(trimmed, "# "))
	case strings.HasPrefix(trimmed, "## "):
		return planH2Style.Width(w).Render(strings.TrimPrefix(trimmed, "## "))
	case strings.HasPrefix(trimmed, "### "):
		return planH3Style.Width(w).Render(strings.TrimPrefix(trimmed, "### "))
	default:
		return panelDetailStyle.Width(w).Render(visible)
	}
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

	editor := firstNonEmptyString(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	c := exec.Command("sh", "-c", fmt.Sprintf("%s %q", editor, path))
	teaCmd := tea.ExecProcess(c, func(err error) tea.Msg {
		return planEditorDoneMsg{err: err}
	})
	return teaCmd, "Opening plan in editor: " + m.displayPath(path), nil
}

// planEditorDoneMsg fires when the external editor exits; Update handles it
// to refresh the plan viewport and surface any error.
type planEditorDoneMsg struct {
	err error
}

// planLastModifiedLabel returns a short human-readable staleness label for
// the footer, e.g. "plan · just now" or "plan · 3m".
func (m *Model) planLastModifiedLabel() string {
	if m.planMTime.IsZero() {
		return "plan · empty"
	}
	age := time.Since(m.planMTime)
	switch {
	case age < 30*time.Second:
		return "plan · just now"
	case age < time.Minute:
		return "plan · <1m"
	case age < time.Hour:
		return fmt.Sprintf("plan · %dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("plan · %dh", int(age.Hours()))
	default:
		return fmt.Sprintf("plan · %dd", int(age.Hours()/24))
	}
}

// renderPanelTabs builds the header strip that sits above the active tab's
// content. Active tab is highlighted; inactive tabs render dim.
func (m *Model) renderPanelTabs(width int) string {
	tabs := []struct {
		idx   int
		label string
	}{
		{panelTabHistory, "History"},
		{panelTabPlan, "Plan"},
		{panelTabSessions, "Sessions"},
	}

	var rendered []string
	for _, t := range tabs {
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
	case panelTabSessions:
		return m.sessionsViewport
	default:
		return m.historyViewport
	}
}

// rerenderActivePanel refreshes whichever tab is currently shown. Use this
// after a state change that could affect the rendered content.
func (m *Model) rerenderActivePanel() {
	switch m.panelTab {
	case panelTabPlan:
		m.renderPlanPanelContent()
	case panelTabSessions:
		m.refreshSessionsList()
		m.renderSessionsPanelContent()
	default:
		m.renderHistoryPanelContent()
	}
}
