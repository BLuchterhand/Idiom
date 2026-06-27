package textutil

import "strings"

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}

	return ""
}

func SplitWorkspaceFileLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}

	return lines
}

// ClampRequestedLineRange clamps a line range [start, end] to the actual
// file bounds [1, totalLines]. The clamped flag reports whether any
// adjustment was needed. The requestedStart/requestedEnd preserve the
// caller's original intent when the range exceeds file bounds.
func ClampRequestedLineRange(totalLines int, start int, end int, requestedStart int, requestedEnd int) (int, int, bool) {
	if totalLines <= 0 {
		return start, end, false
	}

	clamped := false

	if end > totalLines {
		end = totalLines
		clamped = true
	}

	if start > totalLines {
		span := 0
		if requestedStart > 0 && requestedEnd >= requestedStart {
			span = requestedEnd - requestedStart
		} else if end >= start {
			span = end - start
		}

		start = totalLines - span
		if start < 1 {
			start = 1
		}

		clamped = true
	}

	if start > end {
		start = end
		clamped = true
	}

	return start, end, clamped
}

func ContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}

	return false
}

func UniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}

		if _, ok := seen[v]; ok {
			continue
		}

		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}

func EqualTrimFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func NonEmptyTrimmedLines(text string) []string {
	var lines []string

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lines = append(lines, line)
	}

	return lines
}

func Fallback(value, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}

	return value
}

// ContainsFold reports whether substr is within haystack under Unicode case-folding.
// An empty substr returns false (consistent with strings.Contains semantics).
func ContainsFold(haystack, substr string) bool {
	if strings.TrimSpace(substr) == "" {
		return false
	}

	return strings.Contains(strings.ToLower(haystack), strings.ToLower(strings.TrimSpace(substr)))
}
