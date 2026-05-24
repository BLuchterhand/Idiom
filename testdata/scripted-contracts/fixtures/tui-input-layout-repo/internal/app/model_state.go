package app

import "strings"

func desiredInputHeight(inputWidth int, prompt string, value string) int {
	width := inputWidth
	rows := 1
	if value != "" {
		rows = 0
		for _, line := range strings.Split(value, "\n") {
			rows += max(1, len(wrapLines(line, width)))
		}
	}
	return rows
}

func wrapLines(s string, width int) []string {
	if width <= 0 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	return append(lines, s)
}
