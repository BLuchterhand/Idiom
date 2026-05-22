package plancontract

import (
	"sort"
	"strings"

	"idiom/internal/frontier/pathparse"
)

func SourceSatisfiedAnswerCoversPaths(answer string, requiredPaths []string) bool {
	if !SourceSatisfiedAnswerIntent(answer) {
		return false
	}
	required := sourceSatisfiedPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}
	lowerAnswer := strings.ToLower(answer)
	for path := range required {
		if !strings.Contains(lowerAnswer, strings.ToLower(path)) {
			return false
		}
	}
	return true
}

func SourceSatisfiedEvidencePaths(answer string) []string {
	if !SourceSatisfiedAnswerIntent(answer) {
		return nil
	}
	return uniqueSourceSatisfiedPaths(pathparse.MentionedWorkspacePaths(answer))
}

func SourceSatisfiedAnswerIntent(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}
	firstLine := trimmed
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.ToLower(strings.TrimSpace(firstLine))
	if firstLine != "decision: source-satisfied" && firstLine != "decision: source satisfied" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return containsAnySourceSatisfiedIntent(lower,
		"already contains",
		"already implemented",
		"already present",
		"already satisf",
		"current-state",
		"current state",
		"final state",
		"no change",
		"no changes",
		"no-op",
		"no op",
		"nothing to change",
		"stale anchor",
		"stale old_text",
		"stale spec",
	)
}

func sourceSatisfiedPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized := normalizeSourceSatisfiedPath(path)
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func uniqueSourceSatisfiedPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeSourceSatisfiedPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func normalizeSourceSatisfiedPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func containsAnySourceSatisfiedIntent(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
