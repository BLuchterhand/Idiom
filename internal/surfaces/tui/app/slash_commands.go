package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/promptstack"
)

type slashCommandFunc func(m *Model, raw string, fields []string) bool

type slashCommandSpec struct {
	command         string
	helpUsage       string
	suggestionUsage string
	helpOrder       int
	handler         slashCommandFunc
}

// slashCommandSpecs is the authority for slash-command dispatch and discovery.
// Slice order preserves the autocomplete order; helpOrder preserves the
// established /help presentation order.
func slashCommandSpecs() []slashCommandSpec {
	return []slashCommandSpec{
		{command: "/feature-planner", helpUsage: "<objective>", suggestionUsage: "<objective>", helpOrder: 29, handler: (*Model).slashFeaturePlanner},
		{command: "/plan-execute", helpUsage: "<objective>", suggestionUsage: "<objective>", helpOrder: 31, handler: (*Model).slashPlanExecute},
		{command: "/implement", helpUsage: "<objective>", suggestionUsage: "<objective>", helpOrder: 30, handler: (*Model).slashImplement},
		{command: "/product-create", helpUsage: "[--effort-root <path>]", suggestionUsage: "[--effort-root <path>]", helpOrder: 13, handler: (*Model).slashProductCreate},
		{command: "/product-status", helpUsage: "[--effort-root <path>]", suggestionUsage: "[--effort-root <path>]", helpOrder: 14, handler: (*Model).slashProductStatus},
		{command: "/product-continue", helpUsage: "<action> [--effort-root <path>]", suggestionUsage: "<action> [--effort-root <path>]", helpOrder: 15, handler: (*Model).slashProductContinue},
		{command: "/product-approve", helpUsage: "[--effort-root <path>]", suggestionUsage: "[--effort-root <path>]", helpOrder: 16, handler: (*Model).slashProductApprove},
		{command: "/recover-status", helpUsage: "[--packet <path>|<path>]", suggestionUsage: "[--packet <path>|<path>]", helpOrder: 17, handler: (*Model).slashRecoverStatus},
		{command: "/quit", helpOrder: 33, handler: (*Model).slashQuit},
		{command: "/exit", helpOrder: 34, handler: (*Model).slashQuit},
		{command: "/help", helpOrder: 35, handler: (*Model).slashHelp},
		{command: "/clear", helpOrder: 4, handler: (*Model).slashClear},
		{command: "/new", helpOrder: 5, handler: (*Model).slashClear},
		{command: "/compact", helpOrder: 6, handler: (*Model).slashCompact},
		{command: "/save", helpOrder: 1, handler: (*Model).slashSave},
		{command: "/copy", helpUsage: "[last|all|visible|selected]", suggestionUsage: "[last|all|visible|selected]", helpOrder: 2, handler: (*Model).slashCopy},
		{command: "/select", helpOrder: 3, handler: (*Model).slashSelect},
		{command: "/context", helpOrder: 18, handler: (*Model).slashContext},
		{command: "/workspace", helpOrder: 19, handler: (*Model).slashWorkspace},
		{command: "/pwd", helpOrder: 20, handler: (*Model).slashPwd},
		{command: "/resume", helpUsage: "[id]", suggestionUsage: "[id]", helpOrder: 32, handler: (*Model).slashResume},
		{command: "/debuglog", helpUsage: "[mode redacted|full]", suggestionUsage: "[mode redacted|full]", helpOrder: 21, handler: (*Model).slashDebugLog},
		{command: "/trace", helpUsage: "[compact|detail]", suggestionUsage: "[compact|detail]", helpOrder: 22, handler: (*Model).slashTrace},
		{command: "/mode", helpUsage: "[build|plan|general]", suggestionUsage: "[build|plan|general]", helpOrder: 23, handler: (*Model).slashMode},
		{command: "/approve", helpUsage: "[all]", helpOrder: 24, handler: (*Model).slashApprove},
		{command: "/deny", helpOrder: 25, handler: (*Model).slashDeny},
		{command: "/undo", helpOrder: 26, handler: (*Model).slashUndo},
		{command: "/panel", helpOrder: 27, handler: (*Model).slashPanel},
		{command: "/plan", helpOrder: 28, handler: (*Model).slashPlan},
		{command: "/system", helpUsage: "<text>", suggestionUsage: "<prompt>", helpOrder: 7, handler: (*Model).slashSystem},
		{command: "/temp", helpUsage: "<value>", suggestionUsage: "<value>", helpOrder: 8, handler: (*Model).slashTemp},
		{command: "/preset", helpUsage: "[list|name]", suggestionUsage: "[list|name]", helpOrder: 9, handler: (*Model).slashPreset},
		{command: "/server", helpUsage: "<start|stop|restart|status>", suggestionUsage: "<start|stop|restart|status>", helpOrder: 10, handler: (*Model).slashServer},
		{command: "/profile", helpUsage: "<list|add|remove>", suggestionUsage: "<list|add|remove>", helpOrder: 11, handler: (*Model).slashProfile},
		{command: "/model", helpOrder: 12, handler: (*Model).slashModel},
	}
}

func slashCommandSpecFor(command string) (slashCommandSpec, bool) {
	for _, spec := range slashCommandSpecs() {
		if spec.command == command {
			return spec, true
		}
	}

	return slashCommandSpec{}, false
}

func (m *Model) slashQuit(_ string, _ []string) bool {
	return true
}

func (m *Model) slashHelp(_ string, _ []string) bool {
	help := m.helpText()

	m.status = strings.ReplaceAll(help, "\n", " | ")
	m.insertBeforePendingAssistant(llama.ChatMessage{Role: "operator", Content: help})

	return false
}

func (m *Model) helpText() string {
	var b strings.Builder

	helpSpecs := slashCommandSpecs()
	slices.SortFunc(helpSpecs, func(left slashCommandSpec, right slashCommandSpec) int {
		return left.helpOrder - right.helpOrder
	})

	commands := make([]string, 0, len(helpSpecs))
	for _, spec := range helpSpecs {
		command := spec.command
		if spec.helpUsage != "" {
			command += " " + spec.helpUsage
		}

		commands = append(commands, command)
	}

	b.WriteString("Commands: ")
	b.WriteString(strings.Join(commands, " "))
	b.WriteString("\nNavigation: Ctrl+T cycles panel tabs | Ctrl+Y copies last assistant response | Ctrl+O toggles terminal selection | transcript: PgUp/PgDn or mouse wheel")

	if strings.TrimSpace(string(m.featurePlannerPhase)) != "" {
		b.WriteString("\nProduct workflow: phase=")
		b.WriteString(strings.TrimSpace(string(m.featurePlannerPhase)))

		if strings.TrimSpace(m.featurePlannerEffortRoot) != "" {
			b.WriteString(" effort=")
			b.WriteString(m.displayPath(m.featurePlannerEffortRoot))
		}

		b.WriteString(" next=/product-approve or continue chatting to refine")
	}

	return b.String()
}

func (m *Model) slashClear(_ string, _ []string) bool {
	m.messages = nil
	m.messagesRevision++
	m.history.entries = nil
	m.history.revision++
	m.history.selected = 0
	m.pinnedFiles = nil
	m.ClearLastTurnSourceResults()
	m.status = "Cleared chat."

	return false
}

func (m *Model) slashCompact(_ string, fields []string) bool {
	if len(fields) != 1 {
		m.status = "Usage: /compact"
		return false
	}

	if m.streaming {
		m.status = "A response is already in progress."
		return false
	}

	m.compactHistoryAsync()

	return false
}

func (m *Model) slashSave(_ string, _ []string) bool {
	m.saveTranscriptAsync()

	return false
}

func (m *Model) slashCopy(_ string, fields []string) bool {
	m.pendingExec = nil

	target := "last"
	if len(fields) > 2 {
		m.status = "Usage: /copy [last|all|visible|selected]"
		return false
	}

	if len(fields) == 2 {
		target = fields[1]
	}

	cmd, handled := m.copyConversationTarget(target)
	if handled {
		m.pendingExec = cmd
	}

	return false
}

func (m *Model) slashSelect(_ string, fields []string) bool {
	if len(fields) != 1 {
		m.status = "Usage: /select"
		return false
	}

	m.pendingExec = m.enableMouseSelectionMode()
	return false
}

func (m *Model) slashContext(_ string, _ []string) bool {
	m.status = "Active context: " + m.contextSummary()
	return false
}

func (m *Model) slashWorkspace(_ string, _ []string) bool {
	m.status = "Workspace: " + m.workspaceSummary()
	return false
}

func (m *Model) slashPwd(_ string, _ []string) bool {
	m.status = "Current workspace directory: " + m.displayPath(m.workspace.CWD())
	return false
}

func (m *Model) slashDebugLog(_ string, fields []string) bool {
	switch {
	case len(fields) == 1 && m.debugLogPath == "":
		m.status = "Debug conversation logging is unavailable."
	case len(fields) == 1:
		mode := m.cfg.Chat.DebugLogMode
		if mode == "" {
			mode = runprofile.DebugLogModeRedacted
		}
		m.status = "Debug log: " + m.displayPath(m.debugLogPath) + " | mode: " + string(mode)
	case len(fields) == 3 && fields[1] == "mode":
		mode, ok := runprofile.ParseDebugLogMode(fields[2])
		if ok {
			m.cfg.Chat.DebugLogMode = mode
			if m.debugLogger != nil {
				m.debugLogger.SetMode(mode)
			}

			m.status = "Debug log mode set to " + string(mode) + "."
		} else {
			m.status = "Usage: /debuglog [mode redacted|full]"
		}
	default:
		m.status = "Usage: /debuglog [mode redacted|full]"
	}

	return false
}

func (m *Model) slashTrace(_ string, fields []string) bool {
	switch {
	case len(fields) == 1:
		m.status = "Current trace mode: " + m.traceSummary()
	case len(fields) == 2:
		switch fields[1] {
		case string(traceDisplayCompact):
			m.traceMode = traceDisplayCompact
			m.status = "Trace mode set to compact."
		case string(traceDisplayDetail):
			m.traceMode = traceDisplayDetail
			m.status = "Trace mode set to detail."
		default:
			m.status = "Usage: /trace [compact|detail]"
		}
	default:
		m.status = "Usage: /trace [compact|detail]"
	}

	return false
}

func (m *Model) slashMode(_ string, fields []string) bool {
	switch {
	case len(fields) == 1:
		m.status = "Current mode: " + m.modeSummary()
	case len(fields) == 2:
		mode, ok := modepolicy.ModeByName(fields[1])

		if !ok {
			m.status = "Usage: /mode [build|plan|general]"
			return false
		}

		m.agentModeName = mode.Name
		m.invalidateComposedPrompt()
		m.applyModePermissions(mode)

		m.status = "Agent mode set to " + string(mode.Name) + "."
	default:
		m.status = "Usage: /mode [build|plan|general]"
	}

	return false
}

func (m *Model) slashApprove(_ string, fields []string) bool {
	if len(fields) > 2 || (len(fields) == 2 && !strings.EqualFold(fields[1], "all")) {
		m.status = "Usage: /approve [all]"
		return false
	}

	blanket := len(fields) == 2 && strings.EqualFold(fields[1], "all")

	if summary, ok := m.resolvePlanApproval(true, blanket); ok {
		if blanket {
			m.status = "Approved all plan-execute edits: " + summary
		} else {
			m.status = "Approved planned edits: " + summary
		}
		m.renderPlanPanelContent()

		return false
	}

	if blanket {
		if pending := m.currentApproval(); pending != nil && pending.mode == systemSearchApprovalModeName {
			if summary, ok := m.resolveApprovalWithScope(true, workspace.SystemSearchAccessScopeContents); ok {
				m.status = "Approved folder contents: " + summary
			} else {
				m.status = "No pending approval."
			}

			return false
		}

		m.status = "No pending plan approval."
		return false
	}

	if summary, ok := m.resolveApproval(true); ok {
		m.status = "Approved: " + summary
	} else {
		m.status = "No pending approval."
	}

	return false
}

func (m *Model) slashDeny(_ string, _ []string) bool {
	if summary, ok := m.resolvePlanApproval(false, false); ok {
		m.status = "Denied planned edits: " + summary
		m.renderPlanPanelContent()

		return false
	}

	if summary, ok := m.resolveApproval(false); ok {
		m.status = "Denied: " + summary
	} else {
		m.status = "No pending approval."
	}

	return false
}

func (m *Model) slashUndo(_ string, _ []string) bool {
	if err := m.startUndo(); err != nil {
		m.status = "Undo failed: " + err.Error()
	} else {
		m.status = "Undoing latest workspace mutation..."
	}

	return false
}

func (m *Model) slashPanel(_ string, _ []string) bool {
	m.setHistoryPanelOpen(!m.history.panelOpen)

	if m.history.panelOpen {
		m.status = "Panel open. Ctrl+T cycles tabs · Tab focuses · ↑↓ select · Enter expand · PgUp/PgDn or j/k scroll"
	} else {
		m.status = "Panel closed."
	}

	return false
}

func (m *Model) slashResume(_ string, fields []string) bool {
	if len(fields) < 2 {
		m.setHistoryPanelOpen(true)

		m.panelTab = panelTabSessions
		m.refreshSessionsList()

		m.history.focused = true
		m.renderSessionsPanelContent()

		if len(m.sessions.list) == 0 {
			m.status = "No prior sessions found."
		} else {
			m.status = "Pick a session: ↑/↓ select · Enter resume · Esc cancel."
		}

		return false
	}

	id := fields[1]
	if err := m.ResumeSession(id); err != nil {
		m.status = "Resume failed: " + err.Error()
	} else {
		m.status = "Resumed session " + id + "."
		m.rerenderActivePanel()
	}

	return false
}

func (m *Model) slashPlan(_ string, _ []string) bool {
	m.setHistoryPanelOpen(true)

	m.panelTab = panelTabPlan

	cmd, status, err := m.prepareEditPlanCmd()
	m.rerenderActivePanel()

	if err != nil {
		m.status = "Plan editor failed: " + err.Error()
	} else {
		m.status = status
		m.pendingExec = cmd
	}

	return false
}

func (m *Model) slashSystem(raw string, _ []string) bool {
	prompt := strings.TrimSpace(strings.TrimPrefix(raw, "/system"))
	switch prompt {
	case "":
		m.status = "Usage: /system <prompt>"
	default:
		m.cfg.Chat.SystemPrompt = prompt
		m.promptConfigurationRevision++
		if err := m.reloadPromptStackWithProfiles(m.activeProfiles); err != nil {
			m.setContextError("Prompt context failed: " + err.Error())
		} else {
			m.clearContextError()

			m.status = "Updated inline system prompt."
		}
	}

	return false
}

func (m *Model) slashTemp(_ string, fields []string) bool {
	if len(fields) != 2 {
		m.status = "Usage: /temp <value>"
		return false
	}

	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		m.status = "Temperature must be a number."
		return false
	}

	m.cfg.Chat.Temperature = value
	m.cfg.Chat.Preset = "custom"
	m.status = fmt.Sprintf("Temperature set to %.2f", value)

	return false
}

func (m *Model) slashPreset(_ string, fields []string) bool {
	switch {
	case len(fields) == 1:
		m.status = "Current preset: " + textutil.Fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom") + " | " + m.samplingSummary()
	case len(fields) == 2 && fields[1] == "list":
		m.status = "Presets: " + joinOrNone(config.BuiltinChatPresetNames()) + " | current: " + textutil.Fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom")
	case len(fields) == 2:
		if err := config.ApplyBuiltinChatPreset(&m.cfg.Chat, fields[1]); err != nil {
			m.status = err.Error()
			return false
		}

		m.status = fmt.Sprintf("Applied preset %q. %s", m.cfg.Chat.Preset, m.samplingSummary())
	default:
		m.status = "Usage: /preset [list|name]"
	}

	return false
}

func (m *Model) slashServer(_ string, fields []string) bool {
	if len(fields) < 2 {
		m.status = "Usage: /server <start|stop|restart|status>"
		return false
	}

	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return false
	}

	switch fields[1] {
	case "start":
		m.startServerAsync()
	case "stop":
		m.stopServerAsync()
	case "restart":
		m.restartServerAsync()
	case "status":
		state := "down"
		if m.serverUp {
			state = "up"
		}

		m.status = fmt.Sprintf("Server %s on %s. Model: %s", state, m.plannerSpec.EndpointBaseURL(), textutil.Fallback(activeModelDisplayID(m.modelDisplayID, m.modelID), "unknown"))
	default:
		m.status = "Usage: /server <start|stop|restart|status>"
	}

	return false
}

func (m *Model) slashProfile(_ string, fields []string) bool {
	if len(fields) < 2 {
		m.status = "Usage: /profile <list|add|remove>"
		return false
	}

	switch fields[1] {
	case "list":
		names, err := promptstack.ListProfiles(m.cfg.Chat.ProfilesDir)
		if err != nil {
			m.setContextError("Profile listing failed: " + err.Error())

			return false
		}

		m.clearContextError()

		m.status = fmt.Sprintf("Profiles available: %s | active: %s", joinOrNone(names), joinOrNone(m.activeProfiles))
	case "add":
		if len(fields) != 3 {
			m.status = "Usage: /profile add <name>"
			return false
		}

		name := fields[2]
		if slices.Contains(m.activeProfiles, name) {
			m.status = "Profile already active: " + name
			return false
		}

		nextProfiles := append(append([]string(nil), m.activeProfiles...), name)
		if err := m.reloadPromptStackWithProfiles(nextProfiles); err != nil {
			m.setContextError("Profile add failed: " + err.Error())

			return false
		}

		m.activeProfiles = nextProfiles
		m.promptConfigurationRevision++
		m.clearContextError()

		m.status = fmt.Sprintf("Activated profile %q. Context: %s", name, m.contextSummary())
	case "remove":
		if len(fields) != 3 {
			m.status = "Usage: /profile remove <name>"
			return false
		}

		name := fields[2]
		if !slices.Contains(m.activeProfiles, name) {
			m.status = "Profile is not active: " + name
			return false
		}

		nextProfiles := make([]string, 0, len(m.activeProfiles)-1)
		for _, profile := range m.activeProfiles {
			if profile != name {
				nextProfiles = append(nextProfiles, profile)
			}
		}

		if err := m.reloadPromptStackWithProfiles(nextProfiles); err != nil {
			m.setContextError("Profile remove failed: " + err.Error())

			return false
		}

		m.activeProfiles = nextProfiles
		m.promptConfigurationRevision++
		m.clearContextError()

		m.status = fmt.Sprintf("Removed profile %q. Context: %s", name, m.contextSummary())
	default:
		m.status = "Usage: /profile <list|add|remove>"
	}

	return false
}
