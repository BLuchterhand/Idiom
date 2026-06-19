package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// laneSwitchOption is a single selectable entry in the lane-switch popup.
// recommended flags the option derived from the route classifier so the
// popup can pre-select and badge it without reordering the list.
type laneSwitchOption struct {
	key         string // one of laneSwitch* constants
	label       string // short label, e.g. "implement"
	command     string // slash command, e.g. "/implement"
	description string // one-line help text
	recommended bool
}

// laneSwitchPopupState is the interactive state for the lane-switch popup.
// optionRowYs records the absolute screen Y of each option's rendered row
// during the most recent renderLaneSwitchPopup call so the mouse handler can
// hit-test clicks without re-deriving layout geometry.
type laneSwitchPopupState struct {
	open          bool
	objective     string
	options       []laneSwitchOption
	cursor        int
	boxLeft       int
	boxTop        int
	boxWidth      int
	optionRowYs   []int
	lastRenderedW int
	lastRenderedH int
}

// openLaneSwitchPopup builds the four fixed options (implement,
// plan-execute, feature-planner, no) and pre-selects the recommended one.
// The objective is carried through so a confirmed selection can dispatch
// against the original user prompt.
func (m *Model) openLaneSwitchPopup(objective, recommended string) {
	recommended = laneSwitchCanonicalKey(recommended)

	options := []laneSwitchOption{
		{
			key:         laneSwitchImplement,
			label:       "implement",
			command:     "/implement",
			description: "Direct implementation lane — edit files and run focused verification.",
		},
		{
			key:         laneSwitchPlanExecute,
			label:       "plan-execute",
			command:     "/plan-execute",
			description: "Plan-then-execute planner — submit_plan artifact before edits.",
		},
		{
			key:         laneSwitchFeaturePlanner,
			label:       "feature-planner",
			command:     "/feature-planner",
			description: "Feature planner — product-workflow atom dispatch.",
		},
		{
			key:         laneSwitchNone,
			label:       "no",
			command:     "",
			description: "Stay read-only — answer from the current lane without writing.",
		},
	}

	cursor := 0
	for i, opt := range options {
		if opt.key == recommended {
			options[i].recommended = true
			cursor = i
		}
	}

	m.laneSwitchPopup = laneSwitchPopupState{
		open:      true,
		objective: objective,
		options:   options,
		cursor:    cursor,
	}

	m.status = "Lane switch required: pick a write-capable lane."
}

func (m *Model) closeLaneSwitchPopup() {
	m.laneSwitchPopup = laneSwitchPopupState{}
}

// handleLaneSwitchPopupKey returns (cmd, handled). When handled is true the
// caller short-circuits the normal key dispatch. Number keys 1-4 both move
// the cursor and confirm, matching the user-facing hint.
func (m *Model) handleLaneSwitchPopupKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	p := &m.laneSwitchPopup
	if !p.open {
		return nil, false
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.closeLaneSwitchPopup()
		m.status = "Lane switch dismissed."
		return m.waitForEventCmd(), true
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
		return m.waitForEventCmd(), true
	case tea.KeyDown:
		if p.cursor < len(p.options)-1 {
			p.cursor++
		}
		return m.waitForEventCmd(), true
	case tea.KeyEnter:
		m.applyLaneSwitchSelection(p.cursor)
		return m.waitForEventCmd(), true
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "1", "2", "3", "4":
			idx := int(msg.Runes[0] - '1')
			if idx >= 0 && idx < len(p.options) {
				m.applyLaneSwitchSelection(idx)
			}
			return m.waitForEventCmd(), true
		}
	}

	return nil, false
}

// handleLaneSwitchPopupMouse hit-tests a left-click against the option rows
// recorded during the last render. Returns true when the click was consumed
// (inside the popup footprint), so the caller can short-circuit the default
// mouse handler. The popup is modal: any left-click while it is open is
// consumed; a click landing on an option row dispatches that option.
func (m *Model) handleLaneSwitchPopupMouse(msg tea.MouseMsg) bool {
	p := &m.laneSwitchPopup
	if !p.open {
		return false
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return false
	}

	for i, rowY := range p.optionRowYs {
		if msg.Y == rowY && msg.X >= p.boxLeft && msg.X < p.boxLeft+p.boxWidth {
			m.applyLaneSwitchSelection(i)
			return true
		}
	}

	return true
}

// applyLaneSwitchSelection dispatches the chosen lane command via the same
// async helpers the slash-command handlers use, then closes the popup. The
// "no" option simply closes the popup so the user can continue read-only.
//
// Dispatch is guarded on a non-nil client/runner pair so the popup remains
// safe in stub test contexts; the chosen key is always recorded on
// lastLaneSwitchSelection for assertions and telemetry regardless of
// whether the dispatch actually fires.
func (m *Model) applyLaneSwitchSelection(idx int) {
	p := &m.laneSwitchPopup
	if idx < 0 || idx >= len(p.options) {
		m.closeLaneSwitchPopup()
		return
	}

	opt := p.options[idx]
	objective := strings.TrimSpace(p.objective)

	m.lastLaneSwitchSelection = opt.key
	m.closeLaneSwitchPopup()

	if opt.key == laneSwitchNone {
		m.status = "Staying in read-only lane."
		return
	}

	if m.streaming {
		m.status = "A response is already in progress."
		return
	}

	if m.client == nil || m.runner == nil {
		// Stub context (tests, dry-run helpers): record the selection and
		// skip the async dispatch so we never panic on a nil server.
		m.status = "Lane switch selected: " + opt.label
		return
	}

	switch opt.key {
	case laneSwitchImplement:
		m.dispatchLaneSwitchImplement(objective)
	case laneSwitchPlanExecute:
		m.dispatchLaneSwitchPlanExecute(objective)
	case laneSwitchFeaturePlanner:
		m.dispatchLaneSwitchFeaturePlanner(objective)
	}
}

func (m *Model) dispatchLaneSwitchImplement(objective string) {
	if objective == "" {
		m.status = "Usage: /implement <objective>"
		return
	}

	m.appendHistoryMessage("user", "/implement "+objective, nil)
	m.sendImplementCommandAsync(objective)
	m.status = "Dispatching to implement..."
}

func (m *Model) dispatchLaneSwitchPlanExecute(objective string) {
	if objective == "" {
		m.status = "Usage: /plan-execute <objective>"
		return
	}

	m.appendHistoryMessage("user", "/plan-execute "+objective, nil)
	m.sendPlanExecuteCommandAsync(objective)
	m.status = "Dispatching to plan-execute..."
}

func (m *Model) dispatchLaneSwitchFeaturePlanner(objective string) {
	if objective == "" {
		m.status = "Usage: /feature-planner <objective>"
		return
	}

	raw := "/feature-planner " + objective
	m.appendHistoryMessage("user", raw, nil)
	m.logMessage("user", raw)
	m.sendFeaturePlannerCommandAsync(objective)
	m.status = "Dispatching to feature-planner..."
}

// renderLaneSwitchPopup renders the centered modal overlay. It records the
// absolute screen geometry (boxLeft, boxTop, optionRowYs) on the popup state
// so the mouse handler can hit-test without re-deriving layout.
func (m *Model) renderLaneSwitchPopup(width, height int) string {
	p := &m.laneSwitchPopup
	if !p.open || len(p.options) == 0 {
		return ""
	}

	popupW := min(58, max(44, width*3/4))

	var b strings.Builder

	b.WriteString(popupTitleStyle.Width(popupW).Render("◆ Switch lane"))
	b.WriteString("\n")
	b.WriteString(popupHintStyle.Width(popupW).Render("↑↓ navigate · 1-4 select · Enter confirm · Esc cancel · click a row"))
	b.WriteString("\n\n")

	if obj := strings.TrimSpace(p.objective); obj != "" {
		avail := popupW - 4
		if avail < 8 {
			avail = 8
		}
		b.WriteString(popupDescStyle.Width(popupW).Render("  Objective: " + truncatePopupLine(obj, avail)))
		b.WriteString("\n\n")
	}

	optionRowYs := make([]int, 0, len(p.options))

	for i, opt := range p.options {
		selected := i == p.cursor

		var nameStyle lipgloss.Style
		if selected {
			nameStyle = popupRoleActiveStyle
		} else {
			nameStyle = popupRoleInactiveStyle
		}

		prefix := "  "
		if selected {
			prefix = "▸ "
		}

		numeral := fmt.Sprintf("%d. ", i+1)
		label := numeral + opt.command
		if opt.command == "" {
			label = numeral + "No, stay read-only"
		}

		badge := ""
		if opt.recommended {
			badge = "  (recommended)"
		}

		b.WriteString(nameStyle.Width(popupW).Render(prefix + label + badge))
		b.WriteString("\n")
		b.WriteString(popupDescStyle.Width(popupW).Render("    " + opt.description))
		b.WriteString("\n")
	}

	content := b.String()

	boxW := popupW + 4
	popupBox := popupBoxStyle.Width(boxW).Render(content)

	lines := strings.Split(popupBox, "\n")
	boxH := len(lines)

	padTop := max(0, (height-boxH)/2)
	padLeft := max(0, (width-lipgloss.Width(popupBox))/2)

	indent := strings.Repeat(" ", padLeft)
	vertPad := strings.Repeat("\n", padTop)

	// Record geometry for mouse hit-testing. The popup box has a 1-row
	// border plus 1 row of top padding (popupBoxStyle Padding(1,2)). Content
	// rows begin at boxTop + 2; option rows start after title(1) + blank(1)
	// + hint(1) + blank(1) (+ optional objective block).
	contentStartY := padTop + 2
	optionBlockStartY := contentStartY + 3 // title + blank + hint

	if strings.TrimSpace(p.objective) != "" {
		optionBlockStartY += 2 // objective line + blank
	}

	// Each option occupies two rendered lines (label + description); the
	// hit-test targets the label row.
	for i := range p.options {
		optionRowYs = append(optionRowYs, optionBlockStartY+i*2)
	}

	p.boxLeft = padLeft
	p.boxTop = padTop
	p.boxWidth = boxW
	p.optionRowYs = optionRowYs
	p.lastRenderedW = width
	p.lastRenderedH = height

	var overlay strings.Builder

	overlay.WriteString(vertPad)

	for _, line := range lines {
		overlay.WriteString(indent)
		overlay.WriteString(line)
		overlay.WriteString("\n")
	}

	return overlay.String()
}

func truncatePopupLine(s string, limit int) string {
	if limit <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	if limit <= 1 {
		return string(runes[:limit])
	}

	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
