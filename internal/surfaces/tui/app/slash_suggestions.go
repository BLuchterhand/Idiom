package app

import "strings"

// slashSuggestionsState holds the state for the slash-command autocomplete popup.
type slashSuggestionsState struct {
	visible  bool                   // whether the popup is currently shown
	query    string                 // current partial text after the /
	matches  []slashSuggestionEntry // filtered commands matching the query
	selected int                    // index of the highlighted match (-1 = none)
}

// slashSuggestionEntry is a single autocomplete suggestion.
type slashSuggestionEntry struct {
	command string // e.g. "/feature-planner"
	usage   string // e.g. "<objective>"
}

// allSlashSuggestions returns every slash command the TUI recognizes,
// including the three explicit-plan commands handled outside the
// slashCommands map (/feature-planner, /plan-execute, /implement).
func allSlashSuggestions() []slashSuggestionEntry {
	return []slashSuggestionEntry{
		// Heavy lane commands (handled explicitly, not in slashCommands map)
		{"/feature-planner", "<objective>"},
		{"/plan-execute", "<objective>"},
		{"/implement", "<objective>"},

		// Product workflow
		{"/product-status", "[--effort-root <path>]"},
		{"/product-continue", "<action> [--effort-root <path>]"},
		{"/product-approve", "[--effort-root <path>]"},

		// Recovery
		{"/recover-status", "[--packet <path>|<path>]"},

		// Session & chat
		{"/quit", ""},
		{"/exit", ""},
		{"/help", ""},
		{"/clear", ""},
		{"/compact", ""},
		{"/save", ""},
		{"/context", ""},
		{"/workspace", ""},
		{"/pwd", ""},
		{"/resume", "[id]"},

		// Debug & trace
		{"/debuglog", "[mode redacted|full]"},
		{"/trace", "[compact|detail]"},

		// Agent control
		{"/mode", "[build|plan|general]"},
		{"/approve", ""},
		{"/deny", ""},
		{"/undo", ""},
		{"/panel", ""},
		{"/plan", ""},
		{"/system", "<prompt>"},
		{"/temp", "<value>"},

		// Configuration
		{"/preset", "[list|name]"},
		{"/server", "<start|stop|restart|status>"},
		{"/profile", "<list|add|remove>"},
		{"/model", ""},
	}
}

// updateSlashSuggestions reads the current input value and updates the
// slash-command suggestion popup state. Call after every input change.
func (m *Model) updateSlashSuggestions() {
	if m == nil || !m.ready {
		return
	}

	value := m.input.Value()

	// Check if the current input (possibly multi-line) has a line starting with /
	lines := strings.Split(value, "\n")
	lastLine := lines[len(lines)-1]
	trimmed := strings.TrimSpace(lastLine)

	// Activate whenever the current line starts with /.
	if !strings.HasPrefix(trimmed, "/") {
		m.slashSuggestions = slashSuggestionsState{}
		return
	}

	// Extract the query: text between the / and the first space (or end of line).
	query := strings.TrimPrefix(trimmed, "/")
	if idx := strings.Index(query, " "); idx >= 0 {
		query = query[:idx]
	}

	query = strings.TrimSpace(query)

	matches := filterSuggestions(query)
	if len(matches) == 0 {
		m.slashSuggestions = slashSuggestionsState{}
		return
	}

	// If the user has already typed a complete command name (exact match) and
	// then added a space/arguments, hide suggestions — they are typing args now.
	cmd := "/" + query
	for _, s := range matches {
		if s.command == cmd {
			m.slashSuggestions = slashSuggestionsState{}
			return
		}
	}

	selected := m.slashSuggestions.selected
	if selected < 0 || selected >= len(matches) {
		selected = 0
	}

	m.slashSuggestions = slashSuggestionsState{
		visible:  true,
		query:    query,
		matches:  matches,
		selected: selected,
	}
}

// renderSlashSuggestions builds the suggestion popup box. Returns empty string
// when there is nothing to show.
func (m *Model) renderSlashSuggestions() string {
	s := m.slashSuggestions
	if !s.visible || len(s.matches) == 0 {
		return ""
	}

	const maxEntries = 6

	entries := s.matches
	startOffset := 0

	if len(entries) > maxEntries {
		// Show a window around the current selection.
		start := s.selected - maxEntries/2
		if start < 0 {
			start = 0
		}

		if start+maxEntries > len(entries) {
			start = len(entries) - maxEntries
		}

		if start < 0 {
			start = 0
		}

		startOffset = start
		entries = entries[start : start+maxEntries]
	}

	var lines []string

	// Compute the max command width for alignment.
	maxCmd := 0
	for _, e := range entries {
		if len(e.command) > maxCmd {
			maxCmd = len(e.command)
		}
	}

	for i, e := range entries {
		cmdStyle := slashSuggestInactiveStyle
		usageStyle := slashSuggestUsageStyle

		if startOffset+i == s.selected {
			cmdStyle = slashSuggestActiveStyle
			usageStyle = slashSuggestActiveStyle
		}

		cmd := cmdStyle.Render(e.command)

		var usage string

		if e.usage != "" {
			usage = " " + usageStyle.Render(e.usage)
		}

		// Pad to align usages.
		pad := maxCmd - len(e.command)
		if pad > 0 {
			cmd += strings.Repeat(" ", pad)
		}

		lines = append(lines, cmd+usage)
	}

	// Add a hint line at the bottom.
	hint := slashSuggestHintStyle.Render("Tab to complete · ↑↓ to navigate · Esc to dismiss")

	lines = append(lines, hint)

	return slashSuggestBoxStyle.Render(strings.Join(lines, "\n"))
}

// filterSuggestions returns matching suggestions for a query string.
// The query is the text after the opening /, e.g. "feat" matches "/feature-planner".
func filterSuggestions(query string) []slashSuggestionEntry {
	if query == "" {
		return allSlashSuggestions()
	}

	lower := strings.ToLower(query)

	var matches []slashSuggestionEntry

	for _, s := range allSlashSuggestions() {
		if strings.Contains(strings.ToLower(s.command), lower) {
			matches = append(matches, s)
		}
	}

	return matches
}
