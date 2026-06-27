package planexecute

import (
	"path/filepath"
	"strings"
)

func atomMarkdownFenceOpen(line string) (marker string, info string, ok bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	default:
		return "", "", false
	}
}

func atomRequiredEditPathsFromLabel(label string, scopePaths []string) []string {
	label = strings.ReplaceAll(strings.TrimSpace(label), "\\", "/")
	if label == "" {
		return nil
	}

	baseCounts := make(map[string]int, len(scopePaths))
	for _, path := range scopePaths {
		baseCounts[filepath.Base(filepath.FromSlash(path))]++
	}

	matched := make(map[string]struct{}, len(scopePaths))
	for _, path := range scopePaths {
		normalized := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
		if normalized == "" {
			continue
		}

		if strings.Contains(label, "`"+normalized+"`") || strings.Contains(label, normalized) {
			matched[path] = struct{}{}
		}

		base := filepath.Base(filepath.FromSlash(normalized))
		if base != "" && baseCounts[base] == 1 && strings.Contains(label, "`"+base+"`") {
			matched[path] = struct{}{}
		}
	}

	if len(matched) == 0 {
		return nil
	}

	out := make([]string, 0, len(matched))
	for _, path := range scopePaths {
		if _, ok := matched[path]; ok {
			out = append(out, path)
		}
	}

	return out
}
