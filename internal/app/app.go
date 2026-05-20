package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/util"
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

func normalizeGuardedPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}
