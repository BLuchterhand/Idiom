package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/promptstack"
	"idiom/internal/transcript"
	"idiom/internal/util"
)

type slashCommandFunc func(m *Model, raw string, fields []string) bool

var slashCommands = map[string]slashCommandFunc{
	"/quit":             (*Model).slashQuit,
	"/exit":             (*Model).slashQuit,
	"/help":             (*Model).slashHelp,
	"/clear":            (*Model).slashClear,
	"/compact":          (*Model).slashCompact,
	"/save":             (*Model).slashSave,
	"/context":          (*Model).slashContext,
	"/workspace":        (*Model).slashWorkspace,
	"/pwd":              (*Model).slashPwd,
	"/debuglog":         (*Model).slashDebugLog,
	"/trace":            (*Model).slashTrace,
	"/mode":             (*Model).slashMode,
	"/approve":          (*Model).slashApprove,
	"/deny":             (*Model).slashDeny,
	"/undo":             (*Model).slashUndo,
	"/panel":            (*Model).slashPanel,
	"/resume":           (*Model).slashResume,
	"/plan":             (*Model).slashPlan,
	"/system":           (*Model).slashSystem,
	"/temp":             (*Model).slashTemp,
	"/preset":           (*Model).slashPreset,
	"/server":           (*Model).slashServer,
	"/profile":          (*Model).slashProfile,
	"/model":            (*Model).slashModel,
	"/product-create":   (*Model).slashProductCreate,
	"/product-status":   (*Model).slashProductStatus,
	"/product-continue": (*Model).slashProductContinue,
	"/product-approve":  (*Model).slashProductApprove,
	"/recover-status":   (*Model).slashRecoverStatus,
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
	b.WriteString("Commands: /save /clear /compact /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /product-create [--effort-root <path>] /product-status [--effort-root <path>] /product-continue <action> [--effort-root <path>] /product-approve [--effort-root <path>] /recover-status [--packet <path>|<path>] /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /mode [build|plan|general] /approve /deny /undo /panel /plan /feature-planner <objective> /implement <objective> /plan-execute <objective> /resume [id] /help")
	b.WriteString("\nNavigation: Ctrl+T cycles panel tabs | transcript: PgUp/PgDn or mouse wheel")
	if strings.TrimSpace(m.featurePlannerPhase) != "" {
		b.WriteString("\nProduct workflow: phase=")
		b.WriteString(strings.TrimSpace(m.featurePlannerPhase))
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
	m.history.entries = nil
	m.history.selected = 0
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
		m.status = "Debug log: " + m.displayPath(m.debugLogPath) + " | mode: " + util.FirstNonEmpty(m.cfg.Chat.DebugLogMode, "redacted")
	case len(fields) == 3 && fields[1] == "mode":
		switch fields[2] {
		case "redacted", "full":
			m.cfg.Chat.DebugLogMode = fields[2]
			if m.debugLogger != nil {
				m.debugLogger.SetMode(transcript.DebugLogMode(fields[2]))
			}
			m.status = "Debug log mode set to " + fields[2] + "."
		default:
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
		name := strings.ToLower(strings.TrimSpace(fields[1]))
		mode, ok := agent.ModeByName(name)
		if !ok {
			m.status = "Usage: /mode [build|plan|general]"
			return false
		}
		m.agentModeName = mode.Name
		m.invalidateComposedPrompt()
		m.applyModePermissions(mode)
		m.status = "Agent mode set to " + mode.Name + "."
	default:
		m.status = "Usage: /mode [build|plan|general]"
	}
	return false
}

func (m *Model) slashApprove(_ string, _ []string) bool {
	if summary, ok := m.resolveApproval(true); ok {
		m.status = "Approved: " + summary
	} else {
		m.status = "No pending tool approval."
	}
	return false
}

func (m *Model) slashDeny(_ string, _ []string) bool {
	if summary, ok := m.resolveApproval(false); ok {
		m.status = "Denied: " + summary
	} else {
		m.status = "No pending tool approval."
	}
	return false
}

func (m *Model) slashUndo(_ string, _ []string) bool {
	summary, err := m.undoLast()
	if err != nil {
		m.status = "Undo failed: " + err.Error()
	} else {
		m.status = summary
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
		if err := m.reloadPromptStack(); err != nil {
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
		m.status = "Current preset: " + util.Fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom") + " | " + m.samplingSummary()
	case len(fields) == 2 && fields[1] == "list":
		m.status = "Presets: " + joinOrNone(config.BuiltinChatPresetNames()) + " | current: " + util.Fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom")
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
		m.status = fmt.Sprintf("Server %s on %s. Model: %s", state, m.plannerSpec.EndpointBaseURL(), util.Fallback(m.activeModelDisplayID(), "unknown"))
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
		m.clearContextError()
		m.status = fmt.Sprintf("Removed profile %q. Context: %s", name, m.contextSummary())
	default:
		m.status = "Usage: /profile <list|add|remove>"
	}
	return false
}
