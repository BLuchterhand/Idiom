package indexsupport

import (
	"cmp"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type ScoredEntry[T any] struct {
	File  T
	Score int
}

func RankFiles[T any](files []T, pathOf func(T) string, scoreFn func(T) int, limit int) []T {
	scored := make([]ScoredEntry[T], 0, len(files))
	for _, file := range files {
		score := scoreFn(file)
		if score <= 0 {
			continue
		}

		scored = append(scored, ScoredEntry[T]{File: file, Score: score})
	}

	slices.SortFunc(scored, func(a, b ScoredEntry[T]) int {
		if c := -cmp.Compare(a.Score, b.Score); c != 0 {
			return c
		}

		return cmp.Compare(pathOf(a.File), pathOf(b.File))
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	out := make([]T, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.File)
	}

	return out
}

func UniqueRankedPaths[T any](files []T, pathOf func(T) string, limit int) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(files))

	for _, file := range files {
		p := pathOf(file)
		if _, ok := seen[p]; ok {
			continue
		}

		seen[p] = struct{}{}
		out = append(out, p)

		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func FormatIndexSummary(fileCount int, language string, anchors []string, describeAnchor func(string) (string, bool)) string {
	if fileCount == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Indexed ")
	b.WriteString(strconv.Itoa(fileCount))
	b.WriteString(" ")
	b.WriteString(language)
	b.WriteString(" files\n")

	if len(anchors) > 0 {
		b.WriteString("Likely anchors:\n")

		for _, anchor := range anchors {
			description, ok := describeAnchor(anchor)
			if !ok {
				continue
			}

			b.WriteString("- ")
			b.WriteString(anchor)
			b.WriteString(" (")
			b.WriteString(description)
			b.WriteString(")\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func RelevantFileScore(terms []string, path string, related []string, symbolTerms []string, sourceDirPriority int, isTest bool) int {
	score := 0
	pathLower := strings.ToLower(path)

	for _, term := range terms {
		if strings.Contains(pathLower, term) {
			score += 16
		}

		for _, item := range related {
			if strings.Contains(strings.ToLower(item), term) {
				score += 6
			}
		}

		for _, symbol := range symbolTerms {
			if strings.Contains(strings.ToLower(symbol), term) {
				score += 10
			}
		}
	}

	score += sourceDirPriority
	if isTest {
		score -= 20
	}

	return score
}

func ExtractLineCalls[T any](lines []string, startLine int, endLine int, callPattern *regexp.Regexp, stripComment func(string) string, isKeyword func(string) bool, buildCall func(string, int) T) []T {
	if startLine <= 0 || endLine < startLine || endLine > len(lines) {
		return nil
	}

	calls := make([]T, 0, 16)

	for line := startLine; line <= endLine; line++ {
		text := stripComment(lines[line-1])
		for _, match := range callPattern.FindAllStringSubmatch(text, -1) {
			if len(match) != 2 {
				continue
			}

			name := match[1]
			if isKeyword(name) {
				continue
			}

			calls = append(calls, buildCall(name, line))
		}
	}

	return calls
}

func FindFilesExactPartial[T any](files []T, pathOf func(T) string, pattern string) []string {
	pattern = normalizeSearchPath(pattern)
	if pattern == "" {
		return nil
	}

	exact := make([]string, 0)
	partial := make([]string, 0)

	for _, file := range files {
		p := pathOf(file)
		full := normalizeSearchPath(p)
		base := path.Base(full)

		switch {
		case base == pattern || full == pattern:
			exact = append(exact, p)
		case strings.Contains(base, pattern) || strings.Contains(full, pattern):
			partial = append(partial, p)
		}
	}

	if len(exact) > 0 {
		slices.Sort(exact)

		return exact
	}

	slices.Sort(partial)

	return partial
}

func NormalizeLookupPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")

	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}

	return filepath.ToSlash(value)
}

func TopLevelDir(path string) string {
	path = filepath.ToSlash(path)
	if idx := strings.Index(path, "/"); idx >= 0 {
		return path[:idx]
	}

	return "."
}

func NormalizeTerms(terms []string) []string {
	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(terms))

	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if len(term) < 2 {
			continue
		}

		if _, ok := seen[term]; ok {
			continue
		}

		seen[term] = struct{}{}
		normalized = append(normalized, term)
	}

	return normalized
}

func normalizeSearchPath(value string) string {
	return strings.ToLower(NormalizeLookupPath(value))
}
