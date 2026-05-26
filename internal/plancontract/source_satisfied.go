package plancontract

import (
	"sort"
	"strings"

	"idiom/internal/frontier/pathparse"
)

func SourceSatisfiedAnswerCoversPaths(answer string, requiredPaths []string) bool {
	if len(sourceSatisfiedPathSet(requiredPaths)) == 0 || !SourceSatisfiedAnswerIntent(answer) {
		return false
	}
	return len(SourceSatisfiedMissingCoveragePaths(answer, requiredPaths)) == 0
}

func SourceSatisfiedMissingCoveragePaths(answer string, requiredPaths []string) []string {
	required := uniqueSourceSatisfiedPaths(requiredPaths)
	if len(required) == 0 || !SourceSatisfiedAnswerIntent(answer) {
		return required
	}
	evidence := sourceSatisfiedCoveragePathSet(answer, sourceSatisfiedPathSet(required))
	var missing []string
	for _, path := range required {
		if _, ok := evidence[path]; !ok {
			missing = append(missing, path)
		}
	}
	return missing
}

func SourceSatisfiedEvidencePaths(answer string) []string {
	if !SourceSatisfiedAnswerIntent(answer) {
		return nil
	}
	return uniqueSourceSatisfiedPaths(pathparse.MentionedWorkspacePaths(answer))
}

func sourceSatisfiedCoveragePathSet(answer string, requiredPaths map[string]struct{}) map[string]struct{} {
	evidence := sourceSatisfiedPathSet(SourceSatisfiedEvidencePaths(answer))
	for path := range requiredPaths {
		if sourceSatisfiedTextMentionsExactPath(answer, path) {
			evidence[path] = struct{}{}
		}
	}
	return evidence
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

func sourceSatisfiedTextMentionsExactPath(text string, path string) bool {
	path = normalizeSourceSatisfiedPath(path)
	if path == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*+")
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "`'\"*")
		for _, candidate := range []string{path, "./" + path} {
			if !strings.HasPrefix(line, candidate) {
				continue
			}
			if sourceSatisfiedPathBoundary(line, len(candidate)) {
				return true
			}
		}
	}
	return false
}

func sourceSatisfiedPathBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	b := text[index]
	return !((b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '/' ||
		b == '.' ||
		b == '_' ||
		b == '-')
}

func containsAnySourceSatisfiedIntent(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
