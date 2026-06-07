package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/platform/util"
)

func truncateText(text string, limit int) string {
	return util.Truncate(strings.TrimSpace(text), limit)
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func normalizeHistoryRole(role string) string {
	switch role {
	case "user", "assistant", "system":
		return role
	case "tool":
		return "system"
	default:
		return "system"
	}
}

func titleDisplay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

type eventMsg struct {
	msg tea.Msg
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return eventMsg{msg: <-ch}
	}
}

func (m *Model) waitForEventCmd() tea.Cmd {
	if m == nil || m.eventWaitArmed {
		return nil
	}
	m.eventWaitArmed = true
	return waitForEvent(m.events)
}
