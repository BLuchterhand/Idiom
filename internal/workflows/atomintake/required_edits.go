package atomintake

import (
	"fmt"
	"os"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
)

type atomRequiredEditEvaluation struct {
	SnippetCount int
	ExactMatches int
	NearMatches  int
	Missing      int
	Evidence     []string
}

func (e atomRequiredEditEvaluation) SourceSatisfied() bool {
	return e.SnippetCount > 0 && e.ExactMatches == e.SnippetCount
}

func (e atomRequiredEditEvaluation) StaleNeedsRebase() bool {
	if e.SnippetCount == 0 {
		return false
	}

	if e.SourceSatisfied() {
		return false
	}

	return e.ExactMatches > 0 || e.NearMatches > 0
}
func atomEvaluateRequiredEditSnippets(specBody, projectRoot string, existingPaths, scopePaths []string) atomRequiredEditEvaluation {
	snippets := plancontract.RequiredEditSnippetsFromPrompt(specBody, scopePaths)
	eval := atomRequiredEditEvaluation{SnippetCount: len(snippets)}

	if len(snippets) == 0 || len(existingPaths) == 0 {
		return eval
	}

	existing := make(map[string]bool, len(existingPaths))
	for _, path := range existingPaths {
		existing[path] = true
	}

	contentCache := make(map[string]string, len(existingPaths))

	for i, snippet := range snippets {
		content := strings.TrimSpace(snippet.Content)
		if content == "" {
			eval.SnippetCount--
			continue
		}

		candidates := existingPaths

		if snippet.Path != "" {
			if !existing[snippet.Path] {
				eval.Missing++

				eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s is missing for required_edit_%d", snippet.Path, i+1))

				continue
			}

			candidates = []string{snippet.Path}
		}

		best := atomRequiredEditMatch{}

		for _, candidate := range candidates {
			body, ok := contentCache[candidate]
			if !ok {
				fullPath, valid := atomScopeFullPath(projectRoot, candidate)
				if !valid {
					continue
				}

				data, err := os.ReadFile(fullPath)
				if err != nil {
					continue
				}

				body = string(data)
				contentCache[candidate] = body
			}

			best = best.Better(atomRequiredEditSnippetMatch(body, content, snippet.Anchor, candidate))
		}

		switch best.Kind {
		case atomSnippetMatchExact:
			eval.ExactMatches++

			eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s contains required_edit_%d", best.Path, i+1))
		case atomSnippetMatchNear:
			eval.NearMatches++

			eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s near-matches required_edit_%d (%d/%d non-empty lines); rebase before dispatch", best.Path, i+1, best.MatchedLines, best.TotalLines))
		default:
			eval.Missing++
		}
	}

	return eval
}

type atomSnippetMatchKind int

const (
	atomSnippetMatchMissing atomSnippetMatchKind = iota
	atomSnippetMatchNear
	atomSnippetMatchExact
)

type atomRequiredEditMatch struct {
	Kind         atomSnippetMatchKind
	Path         string
	MatchedLines int
	TotalLines   int
}

func (m atomRequiredEditMatch) Better(other atomRequiredEditMatch) atomRequiredEditMatch {
	if other.Kind > m.Kind {
		return other
	}

	if other.Kind == m.Kind && other.MatchedLines > m.MatchedLines {
		return other
	}

	return m
}

func atomRequiredEditSnippetMatch(content, snippet, anchor, path string) atomRequiredEditMatch {
	if atomEditSnippetContiguousMatchesContent(content, snippet) {
		lines := textutil.NonEmptyTrimmedLines(snippet)

		return atomRequiredEditMatch{
			Kind:         atomSnippetMatchExact,
			Path:         path,
			MatchedLines: len(lines),
			TotalLines:   len(lines),
		}
	}

	contentLines := textutil.NonEmptyTrimmedLines(content)
	snippetLines := textutil.NonEmptyTrimmedLines(snippet)
	matchedLines := atomLineMultisetMatchCount(contentLines, snippetLines)

	if atomIsNearRequiredEditMatch(matchedLines, len(snippetLines)) {
		if strings.TrimSpace(anchor) != "" && atomEditSnippetMatchesContent(content, anchor) {
			return atomRequiredEditMatch{
				Kind:         atomSnippetMatchMissing,
				Path:         path,
				MatchedLines: matchedLines,
				TotalLines:   len(snippetLines),
			}
		}

		return atomRequiredEditMatch{
			Kind:         atomSnippetMatchNear,
			Path:         path,
			MatchedLines: matchedLines,
			TotalLines:   len(snippetLines),
		}
	}

	return atomRequiredEditMatch{
		Kind:         atomSnippetMatchMissing,
		Path:         path,
		MatchedLines: matchedLines,
		TotalLines:   len(snippetLines),
	}
}

func atomLineMultisetMatchCount(contentLines, snippetLines []string) int {
	if len(contentLines) == 0 || len(snippetLines) == 0 {
		return 0
	}

	counts := make(map[string]int, len(contentLines))
	for _, line := range contentLines {
		counts[line]++
	}

	matched := 0

	for _, line := range snippetLines {
		if counts[line] <= 0 {
			continue
		}

		counts[line]--

		matched++
	}

	return matched
}

func atomIsNearRequiredEditMatch(matchedLines, totalLines int) bool {
	if totalLines < 3 {
		return false
	}

	required := (totalLines*3 + 3) / 4
	if required < 3 {
		required = 3
	}

	return matchedLines >= required
}

func atomEditSnippetMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")

	if snippet == "" {
		return true
	}

	if strings.Contains(content, snippet) {
		return true
	}

	snippetLines := textutil.NonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}

	contentLines := textutil.NonEmptyTrimmedLines(content)
	next := 0

	for _, line := range contentLines {
		if line != snippetLines[next] {
			continue
		}

		next++
		if next == len(snippetLines) {
			return true
		}
	}

	return false
}

func atomEditSnippetContiguousMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")

	if snippet == "" {
		return true
	}

	if strings.Contains(content, snippet) {
		return true
	}

	snippetLines := textutil.NonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}

	contentLines := textutil.NonEmptyTrimmedLines(content)
	if len(snippetLines) > len(contentLines) {
		return false
	}

	for start := 0; start <= len(contentLines)-len(snippetLines); start++ {
		matched := true

		for offset, line := range snippetLines {
			if contentLines[start+offset] != line {
				matched = false
				break
			}
		}

		if matched {
			return true
		}
	}

	return false
}
