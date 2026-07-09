package agent

import (
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/textutil"
)

var (
	sourceCitationInlineCodePathCandidatePattern = regexp.MustCompile("`([^`\\n]+)`")
	sourceCitationLinePathPattern                = regexp.MustCompile("(?i)(?:^|[\\s`'\"(])([a-z0-9_.-]+\\.(?:go|py|ts|tsx|js|jsx|rs|java|c|cc|cpp|h|hpp|sh|zsh|gd|astro|md|mdx|markdown|json|jsonl|yaml|yml|toml|txt)):\\d+")
	sourceCitationRootLinePathPattern            = regexp.MustCompile("(?i)(?:^|[\\s`'\"(])((?:go\\.mod|go\\.sum|makefile|dockerfile|readme\\.md|agents\\.md|package\\.json|package-lock\\.json|pnpm-lock\\.yaml|yarn\\.lock|pyproject\\.toml|poetry\\.lock|cargo\\.toml|cargo\\.lock)):\\d+")
)

type sourceCitationAnalysis struct {
	Answer         string
	MentionedPaths []string
	EscapingPaths  []string
}

func analyzeSourceCitations(answer string) sourceCitationAnalysis {
	scanText := stripQuotedAndFencedSourceExamples(answer)
	mentioned := sourceCitationMentionedPaths(scanText)

	var repositoryPaths []string
	for _, path := range mentioned {
		if sourceCitationIsSchemaPlaceholder(answer, path) {
			continue
		}
		repositoryPaths = append(repositoryPaths, path)
	}

	return sourceCitationAnalysis{
		Answer:         answer,
		MentionedPaths: uniqueSortedRunnerPaths(repositoryPaths),
		EscapingPaths:  sourceCitationEscapingPaths(scanText),
	}
}

func sourceCitationMentionedPaths(scanText string) []string {
	var paths []string
	for _, path := range pathparse.MentionedWorkspaceFiles(scanText) {
		if sourceCitationMentionedPathLooksPlausible(path) {
			paths = append(paths, path)
		}
	}

	quotedPaths := sourceCitationQuotedPathCandidates(scanText)
	if len(quotedPaths) > 0 {
		paths = sourceCitationDropQuotedPathFragments(paths, quotedPaths)
		paths = append(paths, quotedPaths...)
	}

	for _, match := range sourceCitationLinePathPattern.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 && sourceCitationMentionedPathLooksPlausible(match[1]) {
			paths = append(paths, match[1])
		}
	}

	for _, match := range sourceCitationRootLinePathPattern.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 && sourceCitationMentionedPathLooksPlausible(match[1]) {
			paths = append(paths, match[1])
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceCitationQuotedPathCandidates(text string) []string {
	var paths []string
	for _, match := range sourceCitationInlineCodePathCandidatePattern.FindAllStringSubmatch(text, -1) {
		if len(match) <= 1 {
			continue
		}

		path := strings.TrimSpace(strings.Trim(match[1], "`'\"()[]{}<>,;"))
		if !sourceCitationQuotedPathCandidateLooksLikeCitation(path) {
			continue
		}

		paths = append(paths, sourceCitationPathWithoutLineSuffix(path))
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceCitationQuotedPathCandidateLooksLikeCitation(path string) bool {
	path = sourceCitationPathWithoutLineSuffix(strings.TrimSpace(path))
	if !sourceCitationPathLooksLikePath(path) {
		return false
	}

	if strings.ContainsAny(path, " \t") {
		return filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path)
	}

	return true
}

func sourceCitationPathLooksLikePath(token string) bool {
	token = strings.TrimSpace(strings.Trim(token, "`'\" "))
	if token == "" {
		return false
	}

	path := sourceCitationPathWithoutLineSuffix(token)
	if path == "" {
		return false
	}

	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path) {
		return pathparse.HasWorkspaceFileExtension(path)
	}

	return pathparse.LooksLikeWorkspacePathToken(path)
}

func sourceCitationPathWithoutLineSuffix(token string) string {
	token = strings.TrimSpace(token)
	idx := strings.LastIndex(token, ":")
	if idx <= 0 || idx == len(token)-1 {
		return token
	}

	if !sourceCitationLineSuffix(token[idx+1:]) {
		return token
	}

	return token[:idx]
}

func sourceCitationLineSuffix(suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return false
	}

	start, end, ranged := strings.Cut(suffix, "-")
	if start == "" || strings.Trim(start, "0123456789") != "" {
		return false
	}
	if !ranged {
		return true
	}

	return end != "" && strings.Trim(end, "0123456789") == ""
}

func sourceCitationDropQuotedPathFragments(paths []string, quotedPaths []string) []string {
	if len(paths) == 0 || len(quotedPaths) == 0 {
		return paths
	}

	var out []string
	for _, path := range paths {
		if sourceCitationPathCoveredByQuotedCandidate(path, quotedPaths) {
			continue
		}

		out = append(out, path)
	}

	return out
}

func sourceCitationPathCoveredByQuotedCandidate(path string, quotedPaths []string) bool {
	path = filepath.ToSlash(strings.TrimSpace(strings.Trim(path, "`'\"()[]{}<>,;")))
	if path == "" {
		return false
	}

	pathLower := strings.ToLower(path)
	for _, quotedPath := range quotedPaths {
		quotedPath = filepath.ToSlash(strings.TrimSpace(strings.Trim(quotedPath, "`'\"()[]{}<>,;")))
		if quotedPath == "" || strings.EqualFold(quotedPath, path) {
			continue
		}

		quotedLower := strings.ToLower(quotedPath)
		if strings.HasSuffix(quotedLower, "/"+pathLower) ||
			strings.Contains(quotedLower, " "+pathLower) {

			return true
		}
	}

	return false
}

func sourceCitationEscapingPaths(scanText string) []string {
	seen := map[string]struct{}{}
	var paths []string

	var candidates []string
	for _, match := range sourceCitationInlineCodePathCandidatePattern.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 {
			candidates = append(candidates, match[1])
		}
	}
	candidates = append(candidates, strings.Fields(scanText)...)

	for _, raw := range candidates {
		raw = strings.Trim(raw, "`'\"()[]{}<>,;")
		if !readOnlyPathEscapesWorkspace(raw) {
			continue
		}

		path := sourceCitationEscapingPathDisplay(raw)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return textutil.Sorted(paths)
}

func sourceCitationEscapingPathDisplay(raw string) string {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, "`'\"()[]{}<>,;")
	path = strings.TrimRight(path, ".,:;!?")
	if path == "" {
		return ""
	}

	return sourceCitationPathWithoutLineSuffix(path)
}

func stripQuotedAndFencedSourceExamples(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	var out []string
	inFence := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}

		if inFence || strings.HasPrefix(trimmed, ">") {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func sourceCitationMentionedPathLooksPlausible(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	base := filepath.Base(filepath.ToSlash(path))
	if base == "." || base == "/" || base == "" {
		return false
	}

	ext := filepath.Ext(base)
	if ext != "" && base == ext {
		return false
	}

	return true
}

func sourceCitationMentionedPathIsSchemaPlaceholder(path string) bool {
	path = strings.ToLower(strings.Trim(filepath.ToSlash(NormalizeRunnerPath(path)), "/"))
	if path == "" {
		return false
	}

	switch path {
	case "path/file.go", "path/to/file.go", "path/to/source.go",
		"file/path.go", "some/path.go", "example/file.go",
		"example/path.go", "src/path/file.go":
		return true
	default:
		return false
	}
}

func sourceCitationIsSchemaPlaceholder(answer string, path string) bool {
	if !sourceCitationMentionedPathIsSchemaPlaceholder(path) {
		return false
	}

	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return ContainsAny(lower,
		"schema placeholder",
		"schema example",
		"example placeholder",
		"placeholder path",
		"placeholder source",
		"tool schema",
		"recovery prompt",
		"not a repository citation",
		"not a missing source file",
	)
}

func (a sourceCitationAnalysis) textWithoutPathCitations() string {
	answer := strings.TrimSpace(a.Answer)
	if answer == "" {
		return ""
	}

	var b strings.Builder

	for i := 0; i < len(answer); {
		if answer[i] != '`' {
			b.WriteByte(answer[i])
			i++
			continue
		}

		end := strings.IndexByte(answer[i+1:], '`')
		if end < 0 {
			b.WriteByte(' ')
			i++
			continue
		}

		span := answer[i+1 : i+1+end]
		if !sourceCitationPathLooksLikePath(span) {
			b.WriteByte(' ')
			b.WriteString(span)
			b.WriteByte(' ')
		} else {
			b.WriteByte(' ')
		}

		i += end + 2
	}

	return sourceCitationTextWithoutPathTokens(b.String())
}

func sourceCitationTextWithoutPathTokens(text string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	kept := make([]string, 0, len(words))
	for _, word := range words {
		trimmed := strings.Trim(word, ",:;!?()[]{}\"'")
		trimmed = strings.TrimRight(trimmed, ".")
		if sourceCitationPathLooksLikePath(trimmed) {
			continue
		}

		kept = append(kept, trimmed)
	}

	return strings.Join(kept, " ")
}
