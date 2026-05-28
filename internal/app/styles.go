package app

import "github.com/charmbracelet/lipgloss"

// Palette — Catppuccin Mocha. Hex values come from mocha_style.go so the
// markdown renderer and the surrounding TUI chrome stay in visual lockstep.
var (
	colorPrimary   = lipgloss.Color(mochaMauve)    // brand accent
	colorAccent    = lipgloss.Color(mochaBlue)     // info / context
	colorDim       = lipgloss.Color(mochaOverlay1) // secondary text
	colorMuted     = lipgloss.Color(mochaSurface1) // subtle backgrounds
	colorText      = lipgloss.Color(mochaText)     // body text
	colorOnPrimary = lipgloss.Color(mochaBase)     // text on colored backgrounds

	colorUser      = lipgloss.Color(mochaBlue)     // user message label
	colorAssistant = lipgloss.Color(mochaTeal)     // assistant message label
	colorTool      = lipgloss.Color(mochaPeach)    // tool message label
	colorSystem    = lipgloss.Color(mochaSurface1) // system message label

	colorWarningFg = lipgloss.Color(mochaBase)  // approval / warning text
	colorWarningBg = lipgloss.Color(mochaPeach) // approval / warning background
	colorErrorFg   = lipgloss.Color(mochaBase)  // error text
	colorErrorBg   = lipgloss.Color(mochaRed)   // error background — softer red
	colorSuccess   = lipgloss.Color(mochaGreen) // help / positive indicators
	colorSpinner   = lipgloss.Color(mochaMauve) // spinner animation
)

// ── Header ────────────────────────────────────────────────────────────────────

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorPrimary).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Foreground(colorDim).Padding(0, 1)
)

// ── Divider ───────────────────────────────────────────────────────────────────

var dividerStyle = lipgloss.NewStyle().Foreground(colorMuted)

// ── Message labels ────────────────────────────────────────────────────────────

var (
	userLabelStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorUser).Padding(0, 1)
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorAssistant).Padding(0, 1)
	toolLabelStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorTool).Padding(0, 1)
	systemLabelStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorSystem).Padding(0, 1)
	bodyStyle           = lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
)

// ── Footer ────────────────────────────────────────────────────────────────────

var (
	contextStyle  = lipgloss.NewStyle().Foreground(colorAccent).Padding(0, 1)
	approvalStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWarningFg).Background(colorWarningBg).Padding(0, 1)
	errorStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorErrorFg).Background(colorErrorBg).Padding(0, 1)
)

// ── History panel ─────────────────────────────────────────────────────────────

var (
	// panelTitleStyle: header bar of the history panel.
	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorMuted).Padding(0, 1)
	// panelEntryStyle: unselected history entry.
	panelEntryStyle = lipgloss.NewStyle().Foreground(colorDim).PaddingLeft(1)
	// panelFocusedStyle: selected entry when panel has keyboard focus.
	panelFocusedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText).Background(colorMuted).PaddingLeft(1)
	// panelUnfocusedStyle: selected entry when panel is open but not focused.
	panelUnfocusedStyle = lipgloss.NewStyle().Foreground(colorAccent).PaddingLeft(1)
	// panelDetailStyle: expanded entry detail lines (user msg, tools, response).
	panelDetailStyle = lipgloss.NewStyle().Foreground(colorDim).PaddingLeft(2)

	// Tab header inside the side panel.
	panelTabActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorPrimary)
	panelTabInactiveStyle = lipgloss.NewStyle().Foreground(colorDim).Background(colorMuted)

	// Plan tab styles.
	planTodoStyle       = lipgloss.NewStyle().Foreground(colorText).PaddingLeft(1)
	planDoneStyle       = lipgloss.NewStyle().Foreground(colorDim).PaddingLeft(1).Strikethrough(true)
	planInProgressStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).PaddingLeft(1)
	planH1Style         = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorPrimary).Padding(0, 1)
	planH2Style         = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).PaddingLeft(1)
	planH3Style         = lipgloss.NewStyle().Bold(true).Foreground(colorText).PaddingLeft(1)
)

// Model picker popup.
var (
	popupTitleStyle         = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorPrimary).Padding(0, 1)
	popupHintStyle          = lipgloss.NewStyle().Foreground(colorDim).Padding(0, 1)
	popupRoleActiveStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorText).Background(colorMuted)
	popupRoleInactiveStyle  = lipgloss.NewStyle().Foreground(colorDim)
	popupModelActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	popupModelInactiveStyle = lipgloss.NewStyle().Foreground(colorText)
	popupModelRowStyle      = lipgloss.NewStyle().Foreground(colorText)
	popupDescStyle          = lipgloss.NewStyle().Foreground(colorDim)
	popupBoxStyle           = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorPrimary).Padding(1, 2)
)

// ── Slash-command suggestion popup ────────────────────────────────────────────

var (
	slashSuggestBoxStyle     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colorMuted).Padding(0, 1)
	slashSuggestActiveStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorOnPrimary).Background(colorPrimary).Padding(0, 1)
	slashSuggestInactiveStyle = lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
	slashSuggestUsageStyle   = lipgloss.NewStyle().Foreground(colorDim)
	slashSuggestHintStyle    = lipgloss.NewStyle().Foreground(colorDim)
)
