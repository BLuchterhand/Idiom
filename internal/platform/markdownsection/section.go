package markdownsection

import "strings"

func ByHeading(markdown string, headingName string) string {
	want := normalizeHeadingText(headingName)
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	inFence := false
	fence := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, ok := fenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := heading(trimmed)
		if !ok || !headingMatches(normalizeHeadingText(text), want) {
			continue
		}
		start = i + 1
		startLevel = level
		break
	}

	if start < 0 {
		return ""
	}

	end := len(lines)
	inFence = false
	fence = ""
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if marker, ok := fenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, _, ok := heading(trimmed)
		if ok && level <= startLevel {
			end = i
			break
		}
	}

	return strings.Join(lines[start:end], "\n")
}

func heading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[level+1:]), "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func normalizeHeadingText(text string) string {
	text = strings.Trim(strings.ToLower(strings.TrimSpace(text)), ": ")
	return strings.Join(strings.Fields(text), " ")
}

func headingMatches(candidate string, want string) bool {
	return trimHeadingQualifier(candidate) == trimHeadingQualifier(want)
}

func trimHeadingQualifier(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, marker := range []string{" (", " [", " - ", " -- "} {
		if idx := strings.Index(heading, marker); idx > 0 {
			return strings.TrimSpace(heading[:idx])
		}
	}
	return heading
}

func fenceOpen(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", true
	default:
		return "", false
	}
}
