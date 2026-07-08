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

type SourceSatisfiedScopeOptions struct {
	ProjectRoot string
	SpecPath    string
	SpecBody    string
	ScopePath   string
	ScopeText   string
}

type SourceSatisfiedScopeReport struct {
	Path     string
	Evidence []string
}

func (e atomCurrentStateEvaluation) SourceSatisfied() bool {
	return e.PathCount > 0 && e.Satisfied == e.PathCount
}

func SourceScopeRowText(specBody string) string {
	lines := strings.Split(specBody, "\n")
	for i, raw := range lines {
		if pathparse.NormalizeMarkdownHeading(raw) != "source scope row" {
			continue
		}

		var out []string
		for _, line := range lines[i+1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				if len(out) > 0 {
					break
				}

				continue
			}

			if pathparse.LooksLikeMarkdownHeading(trimmed) {
				break
			}

			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
		}

		return strings.Join(out, "\n")
	}

	return ""
}

func EvaluateSourceSatisfiedScope(opts SourceSatisfiedScopeOptions) (SourceSatisfiedScopeReport, bool) {
	path := strings.TrimSpace(opts.ScopePath)
	if path == "" {
		return SourceSatisfiedScopeReport{}, false
	}

	scopePath, fullPath, ok := atomScopePath(opts.ProjectRoot, path)
	if !ok {
		return SourceSatisfiedScopeReport{}, false
	}

	body, err := os.ReadFile(fullPath)
	if err != nil {
		return SourceSatisfiedScopeReport{}, false
	}

	content := string(body)
	scopeText := strings.TrimSpace(opts.ScopeText)
	if scopeText == "" {
		scopeText = scopePath
	}

	if evidence := atomSourceSatisfiedLiteralEvidence(scopePath, content, scopeText, opts.SpecPath); len(evidence) > 0 {
		return SourceSatisfiedScopeReport{Path: scopePath, Evidence: evidence}, true
	}

	if evidence := atomCurrentStateAnchorEvidence(scopePath, content, scopeText, opts.SpecBody); len(evidence) > 0 {
		return SourceSatisfiedScopeReport{Path: scopePath, Evidence: evidence}, true
	}

	if !atomScopeRowIsOptional(scopeText) {
		if evidence := atomSourceBodyConceptEvidence(scopePath, content, scopeText); len(evidence) > 0 {
			return SourceSatisfiedScopeReport{Path: scopePath, Evidence: evidence}, true
		}
	}

	return SourceSatisfiedScopeReport{}, false
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

func atomSourceSatisfiedLiteralEvidence(path string, content string, sourceText string, skipPaths ...string) []string {
	for _, literal := range atomBacktickLiterals(sourceText) {
		literal = strings.TrimSpace(literal)
		if literal == "" || atomLiteralIsSkippedPath(literal, skipPaths...) {
			continue
		}

		if evidence, ok := LiteralSourceEvidence(path, content, literal); ok {
			return []string{evidence}
		}
	}

	return nil
}

func atomLiteralIsSkippedPath(literal string, skipPaths ...string) bool {
	normalizedLiteral, ok := pathparse.NormalizeWorkspacePathToken(literal)
	if !ok {
		return false
	}

	for _, skip := range skipPaths {
		normalizedSkip, ok := pathparse.NormalizeWorkspacePathToken(skip)
		if ok && normalizedLiteral == normalizedSkip {
			return true
		}
	}

	return atomLooksLikeConcreteFile(normalizedLiteral)
}

func LiteralSourceEvidence(path string, content string, literal string) (string, bool) {
	literal = strings.TrimSpace(literal)
	if literal == "" {
		return "", false
	}

	lowerContent := strings.ToLower(content)
	lowerLiteral := strings.ToLower(literal)

	if strings.Contains(lowerContent, lowerLiteral) {
		return atomLineEvidence(path, content, literal, fmt.Sprintf("current source contains `%s`", literal)), true
	}

	if strings.HasPrefix(lowerLiteral, "idiom.") && strings.Count(lowerLiteral, ".") >= 2 {
		parts := strings.Split(lowerLiteral, ".")
		prefix := strings.Join(parts[:len(parts)-1], ".") + "."
		leaf := parts[len(parts)-1]

		if leaf != "" && strings.Contains(lowerContent, leaf) && strings.Contains(lowerContent, prefix) {
			return atomLineEvidence(path, content, leaf, fmt.Sprintf("current source contains event leaf `%s` and namespace prefix `%s`", leaf, prefix)), true
		}
	}

	return "", false
}

func TermCoverageLineEvidence(path string, content string, sourceText string, description string, minimumTerms int) (string, bool) {
	terms := atomSatisfactionTermSet(atomStripWorkspacePathLiterals(sourceText))
	if len(terms) == 0 {
		return "", false
	}

	required := minimumTerms
	if required <= 0 || len(terms) < required {
		required = len(terms)
	}

	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		lineTerms := atomSatisfactionTermSet(trimmed)
		matches := 0

		for term := range terms {
			if _, ok := lineTerms[term]; ok {
				matches++
			}
		}

		if matches >= required {
			return fmt.Sprintf("`%s:%d`: %s (`%s`)", path, i+1, description, strings.ReplaceAll(trimmed, "`", "'")), true
		}
	}

	return "", false
}

func atomLineEvidence(path string, content string, needle string, description string) string {
	needle = strings.ToLower(strings.TrimSpace(needle))
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			return fmt.Sprintf("`%s:%d`: %s (`%s`)", path, i+1, description, strings.ReplaceAll(strings.TrimSpace(line), "`", "'"))
		}
	}

	return fmt.Sprintf("`%s`: %s", path, description)
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

			evidence = append(evidence, fmt.Sprintf("`%s:%d`: optional current-state anchor remains current (`%s`)", path, anchorLine, strings.ReplaceAll(text, "`", "'")))
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
			evidence = append(evidence, fmt.Sprintf("`%s:%d`: current-state anchor covers requested source-scope term(s) (`%s`)", path, anchorLine, strings.ReplaceAll(text, "`", "'")))
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
			evidence = append(evidence, fmt.Sprintf("`%s:%d`: current source covers requested source-scope term(s) (`%s`)", path, i+1, strings.ReplaceAll(trimmed, "`", "'")))
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

	path, ok := pathparse.NormalizeWorkspacePathToken(strings.TrimSpace(ref[:idx]))
	if !ok {
		return "", 0, false
	}

	lineText := strings.TrimSpace(ref[idx+1:])
	line := 0

	for _, r := range lineText {
		if r < '0' || r > '9' {
			return "", 0, false
		}

		line = line*10 + int(r-'0')
	}

	return path, line, line > 0
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
		normalized, ok := pathparse.NormalizeWorkspacePathToken(literal)
		if !ok || !atomLooksLikeConcreteFile(normalized) {
			continue
		}

		text = strings.ReplaceAll(text, "`"+literal+"`", "")
	}

	return text
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
	if len(term) < 3 && term != "id" {
		return true
	}

	if atomIgnoredAnchorTerm(term) {
		return true
	}

	switch term {
	case "already", "around", "after", "before", "build", "current", "existing", "needed", "needs", "new", "only", "preserving", "provided", "requested", "runs", "row", "state", "without":
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

func atomLooksLikeConcreteFile(path string) bool {
	if pathparse.HasWorkspaceFileExtension(path) {
		return true
	}

	switch filepath.Base(path) {
	case "Makefile", "go.mod", "go.sum", "README.md":
		return true
	default:
		return false
	}
}

func atomIgnoredAnchorTerm(term string) bool {
	switch term {
	case "the", "and", "for", "with", "this", "that", "from", "into", "file", "files", "path", "paths", "source", "design", "document", "doc", "scope", "slice", "atom", "edit", "edits", "modify", "change", "changes", "implementation", "implement":
		return true
	default:
		return false
	}
}
