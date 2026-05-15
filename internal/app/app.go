package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/frontier"
	"idiom/internal/frontier/pathparse"
)

func containsAny(text string, needles ...string) bool {
	return frontier.ContainsAny(text, needles...)
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func uniqueNonEmptyStrings(values []string) []string {
	return frontier.UniqueNonEmptyStrings(values)
}

func uniqueLimitedStrings(values []string, limit int) []string {
	return frontier.LimitStrings(values, limit)
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}

// normalizeGuardedPath strips a leading "./" and any trailing whitespace.
// Kept as a thin helper for places that format paths for display.
func normalizeGuardedPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

// promptMentionedWorkspacePaths returns workspace-relative paths the user
// literally named in the prompt. Pure delegation to pathparse.
func promptMentionedWorkspacePaths(prompt string) []string {
	return pathparse.MentionedWorkspacePaths(prompt)
}
