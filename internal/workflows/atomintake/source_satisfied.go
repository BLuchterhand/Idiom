package atomintake

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/markdownsection"
)

type atomCurrentStateEvaluation struct {
	PathCount int
	Satisfied int
	Evidence  []string
}

func (e atomCurrentStateEvaluation) SourceSatisfied() bool {
	return e.PathCount > 0 && e.Satisfied == e.PathCount
}

func atomEvaluateCurrentStateSourceSatisfied(specBody, projectRoot string, existingPaths, scopePaths []string) atomCurrentStateEvaluation {
	eval := atomCurrentStateEvaluation{}
	if !atomSpecHasCurrentStateAnchors(specBody) || len(scopePaths) == 0 || len(existingPaths) != len(scopePaths) {
		return eval
	}

	existing := make(map[string]bool, len(existingPaths))
	for _, path := range existingPaths {
		existing[path] = true
	}

	changeTextByPath := atomFilesToModifyChangeTextByPath(specBody)

	for _, path := range scopePaths {
		path = strings.TrimSpace(path)
		if path == "" || !existing[path] {
			return atomCurrentStateEvaluation{}
		}

		sourceText := strings.TrimSpace(changeTextByPath[path])
		if sourceText == "" {
			sourceText = path
		}

		fullPath, valid := atomScopeFullPath(projectRoot, path)
		if !valid {
			return atomCurrentStateEvaluation{}
		}

		body, err := os.ReadFile(fullPath)
		if err != nil {
			return atomCurrentStateEvaluation{}
		}

		eval.PathCount++

		evidence := atomCurrentStateAnchorEvidence(path, string(body), sourceText, specBody)

		if len(evidence) == 0 && !atomScopeRowIsOptional(sourceText) {
			evidence = atomSourceBodyConceptEvidence(path, string(body), sourceText)
		}

		if len(evidence) == 0 {
			continue
		}

		eval.Satisfied++

		eval.Evidence = append(eval.Evidence, evidence...)
	}

	return eval
}

func atomScopeRowIsOptional(scopeText string) bool {
	lower := strings.ToLower(scopeText)
	return strings.Contains(lower, "if needed") || strings.Contains(lower, "if necessary")
}

func atomSpecHasCurrentStateAnchors(specBody string) bool {
	lower := strings.ToLower(specBody)
	return strings.Contains(lower, "host current-state anchors") || strings.Contains(lower, "current-state anchor")
}

func atomFilesToModifyChangeTextByPath(specBody string) map[string]string {
	out := map[string]string{}
	section := markdownsection.ByHeading(specBody, "files to modify")

	for _, rawLine := range strings.Split(section, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}

		cells := strings.Split(line, "|")
		if len(cells) < 4 || atomMarkdownTableSeparatorCell(cells[1]) {
			continue
		}

		path := atomPathFromTableCell(cells[1])
		if path == "" {
			continue
		}

		out[path] = strings.TrimSpace(cells[2])
	}

	return out
}

func atomMarkdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}

	for _, r := range cell {
		if r != '-' && r != ':' && r != ' ' && r != '\t' {
			return false
		}
	}

	return strings.Contains(cell, "-")
}

func atomPathFromTableCell(cell string) string {
	cell = strings.TrimSpace(cell)

	literals := atomBacktickLiterals(cell)

	if len(literals) > 0 {
		cell = literals[0]
	}

	cell = strings.Trim(strings.TrimSpace(cell), "`*_ ")
	if cell == "" {
		return ""
	}

	return filepath.ToSlash(filepath.Clean(cell))
}

func atomCurrentStateAnchorEvidence(path string, content string, sourceText string, specBody string) []string {
	if atomScopeRowIsOptional(sourceText) {
		var evidence []string

		for _, line := range strings.Split(specBody, "\n") {
			literals := atomBacktickLiterals(line)
			if len(literals) < 2 {
				continue
			}

			anchorPath, anchorLine, ok := atomParseCurrentStateAnchorRef(literals[0])
			if !ok || anchorPath != path {
				continue
			}

			text := strings.TrimSpace(literals[1])
			if text == "" || !atomAnchorTextStillCurrent(content, anchorLine, text) {
				continue
			}

			evidence = append(evidence, fmt.Sprintf("%s:%d optional current-state anchor remains current", path, anchorLine))
		}

		if len(evidence) > 3 {
			evidence = evidence[:3]
		}

		return evidence
	}

	sourceTerms := atomSatisfactionTermSet(atomStripWorkspacePathLiterals(sourceText))
	if len(sourceTerms) == 0 {
		return nil
	}

	covered := map[string]struct{}{}

	var evidence []string

	for _, line := range strings.Split(specBody, "\n") {
		literals := atomBacktickLiterals(line)
		if len(literals) < 2 {
			continue
		}

		anchorPath, anchorLine, ok := atomParseCurrentStateAnchorRef(literals[0])
		if !ok || anchorPath != path {
			continue
		}

		text := strings.TrimSpace(literals[1])
		if text == "" || !atomAnchorTextStillCurrent(content, anchorLine, text) {
			continue
		}

		lineTerms := atomSatisfactionTermSet(text)
		matched := false

		for term := range sourceTerms {
			if _, ok := lineTerms[term]; ok {
				covered[term] = struct{}{}
				matched = true
			}
		}

		if matched {
			evidence = append(evidence, fmt.Sprintf("%s:%d current-state anchor covers requested term(s)", path, anchorLine))
		}
	}

	if !atomSatisfactionCoverageSatisfies(sourceTerms, covered) {
		return nil
	}

	if len(evidence) > 3 {
		evidence = evidence[:3]
	}

	return evidence
}

func atomSourceBodyConceptEvidence(path string, content string, sourceText string) []string {
	sourceTerms := atomSatisfactionTermSet(atomStripWorkspacePathLiterals(sourceText))
	if len(sourceTerms) == 0 {
		return nil
	}

	covered := map[string]struct{}{}

	var evidence []string

	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		lineTerms := atomSatisfactionTermSet(trimmed)
		matched := false

		for term := range sourceTerms {
			if _, ok := lineTerms[term]; ok {
				covered[term] = struct{}{}
				matched = true
			}
		}

		if matched {
			evidence = append(evidence, fmt.Sprintf("%s:%d current source covers requested term(s)", path, i+1))
		}
	}

	if !atomSatisfactionCoverageSatisfies(sourceTerms, covered) {
		return nil
	}

	if len(evidence) > 3 {
		evidence = evidence[:3]
	}

	return evidence
}

func atomParseCurrentStateAnchorRef(ref string) (string, int, bool) {
	ref = strings.TrimSpace(ref)

	idx := strings.LastIndex(ref, ":")

	if idx <= 0 || idx == len(ref)-1 {
		return "", 0, false
	}

	path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(ref[:idx])))
	lineText := strings.TrimSpace(ref[idx+1:])
	line := 0

	for _, r := range lineText {
		if r < '0' || r > '9' {
			return "", 0, false
		}

		line = line*10 + int(r-'0')
	}

	return path, line, path != "" && line > 0
}

func atomAnchorTextStillCurrent(content string, line int, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	lines := strings.Split(content, "\n")
	if line > 0 && line <= len(lines) && strings.TrimSpace(lines[line-1]) == text {
		return true
	}

	return strings.Contains(content, text)
}

func atomBacktickLiterals(text string) []string {
	var literals []string

	for {
		start := strings.Index(text, "`")
		if start < 0 {
			break
		}

		text = text[start+1:]

		end := strings.Index(text, "`")

		if end < 0 {
			break
		}

		literal := strings.TrimSpace(text[:end])
		if literal != "" {
			literals = append(literals, literal)
		}

		text = text[end+1:]
	}

	return literals
}

func atomStripWorkspacePathLiterals(text string) string {
	for _, literal := range atomBacktickLiterals(text) {
		if pathLooksLikeFile(literal) {
			text = strings.ReplaceAll(text, "`"+literal+"`", "")
		}
	}

	return text
}

func pathLooksLikeFile(path string) bool {
	return pathparse.PathLooksLikeFile(path)
}

func atomSatisfactionTermSet(text string) map[string]struct{} {
	terms := map[string]struct{}{}

	for _, token := range atomSatisfactionTokens(text) {
		if atomIgnoredSatisfactionTerm(token) {
			continue
		}

		terms[token] = struct{}{}
	}

	return terms
}

func atomSatisfactionTokens(text string) []string {
	var (
		tokens  []string
		current []rune
	)

	flush := func() {
		if len(current) == 0 {
			return
		}

		tokens = append(tokens, strings.ToLower(string(current)))
		current = current[:0]
	}

	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			if len(current) > 0 {
				last := current[len(current)-1]
				if last >= 'a' && last <= 'z' {
					flush()
				}
			}

			current = append(current, r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			current = append(current, r)
		default:
			flush()
		}
	}

	flush()

	return tokens
}

func atomIgnoredSatisfactionTerm(term string) bool {
	if len(term) <= 1 {
		return true
	}

	switch term {
	case "a", "an", "and", "for", "in", "of", "or", "the", "to", "with",
		"add", "change", "file", "implement", "modify", "only", "path", "scope",
		"source", "this":
		return true
	default:
		return false
	}
}

func atomSatisfactionCoverageSatisfies(sourceTerms map[string]struct{}, covered map[string]struct{}) bool {
	if len(covered) == 0 {
		return false
	}

	required := 3
	if len(sourceTerms) < required {
		required = len(sourceTerms)
	}

	if required < 2 {
		required = 2
	}

	return len(covered) >= required
}
