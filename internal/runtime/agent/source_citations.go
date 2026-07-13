package agent

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/contracts/pathparse"
)

var (
	sourceCitationInlineCodePathCandidatePattern = regexp.MustCompile("`([^`\\n]+)`")
	sourceCitationLinePathPattern                = regexp.MustCompile("(?i)(?:^|[\\s`'\"(])([^\\s`'\"()\\[\\]{}<>,;]+:\\d+(?:-\\d+)?)")
)

type sourceCitationAnalysis struct {
	Answer     string
	References []sourceReference
}

type sourceReferenceKind string

const (
	sourceReferenceSourceFile       sourceReferenceKind = "source_file"
	sourceReferenceDirectoryLabel   sourceReferenceKind = "directory_label"
	sourceReferenceExternalDocument sourceReferenceKind = "external_document"
)

type sourceReference struct {
	Kind             sourceReferenceKind  `json:"kind"`
	Path             string               `json:"path"`
	LineRange        *sourceCitationRange `json:"line_range,omitempty"`
	EscapesWorkspace bool                 `json:"escapes_workspace,omitempty"`
}

type sourceCitationRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (lineRange sourceCitationRange) Valid() bool {
	return lineRange.Start > 0 && lineRange.End >= lineRange.Start
}

func (reference sourceReference) BodyReadable() bool {
	return reference.Kind == sourceReferenceSourceFile ||
		reference.Kind == sourceReferenceExternalDocument
}

func (analysis sourceCitationAnalysis) BodyReadableReferences() []sourceReference {
	return filterSourceReferences(analysis.References, func(reference sourceReference) bool {
		return reference.BodyReadable()
	})
}

func (analysis sourceCitationAnalysis) BodyReadablePaths() []string {
	return sourceReferencePaths(analysis.BodyReadableReferences())
}

func (analysis sourceCitationAnalysis) EscapingReferences() []sourceReference {
	return filterSourceReferences(analysis.References, func(reference sourceReference) bool {
		return reference.EscapesWorkspace
	})
}

func (analysis sourceCitationAnalysis) ReferencePaths() []string {
	return sourceReferencePaths(analysis.References)
}

func analyzeSourceCitations(answer string) sourceCitationAnalysis {
	scanText := stripQuotedAndFencedSourceExamples(answer)
	references := sourceCitationMentionedReferences(scanText)
	references = append(references, sourceCitationEscapingReferences(scanText)...)

	var repositoryReferences []sourceReference
	for _, reference := range references {
		if sourceCitationIsSchemaPlaceholder(answer, reference.Path) {
			continue
		}
		repositoryReferences = append(repositoryReferences, reference)
	}

	return sourceCitationAnalysis{
		Answer:     answer,
		References: normalizeSourceReferences(repositoryReferences),
	}
}

func sourceCitationMentionedReferences(scanText string) []sourceReference {
	var references []sourceReference
	for _, path := range pathparse.MentionedWorkspaceFiles(scanText) {
		if sourceCitationMentionedPathLooksPlausible(path) {
			references = append(references, sourceReferenceForPath(path))
		}
	}

	quotedReferences := sourceCitationQuotedReferences(scanText)
	if len(quotedReferences) > 0 {
		references = sourceCitationDropQuotedPathFragments(references, quotedReferences)
		references = append(references, quotedReferences...)
	}

	for _, match := range sourceCitationLinePathPattern.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 {
			reference := sourceReferenceForPath(match[1])
			if sourceCitationMentionedPathLooksPlausible(reference.Path) {
				references = append(references, reference)
			}
		}
	}

	return normalizeSourceReferences(references)
}

func sourceCitationQuotedReferences(text string) []sourceReference {
	var references []sourceReference
	for _, match := range sourceCitationInlineCodePathCandidatePattern.FindAllStringSubmatch(text, -1) {
		if len(match) <= 1 {
			continue
		}

		path := strings.TrimSpace(strings.Trim(match[1], "`'\"()[]{}<>,;"))
		if !sourceCitationQuotedPathCandidateLooksLikeCitation(path) {
			continue
		}

		references = append(references, sourceReferenceForPath(path))
	}

	return normalizeSourceReferences(references)
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
	path, _ := sourceCitationPathAndRange(token)
	return path
}

func sourceCitationPathAndRange(token string) (string, *sourceCitationRange) {
	token = strings.TrimSpace(token)
	idx := strings.LastIndex(token, ":")
	if idx <= 0 || idx == len(token)-1 {
		return token, nil
	}

	suffix := token[idx+1:]
	if !sourceCitationLineSuffix(suffix) {
		return token, nil
	}

	startText, endText, ranged := strings.Cut(strings.TrimSpace(suffix), "-")
	start, _ := strconv.Atoi(startText)
	end := start
	if ranged {
		end, _ = strconv.Atoi(endText)
	}
	return token[:idx], &sourceCitationRange{Start: start, End: end}
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

func sourceCitationDropQuotedPathFragments(references []sourceReference, quotedReferences []sourceReference) []sourceReference {
	if len(references) == 0 || len(quotedReferences) == 0 {
		return references
	}

	var out []sourceReference
	for _, reference := range references {
		if sourceCitationPathCoveredByQuotedCandidate(reference.Path, quotedReferences) {
			continue
		}

		out = append(out, reference)
	}

	return out
}

func sourceCitationPathCoveredByQuotedCandidate(path string, quotedReferences []sourceReference) bool {
	path = filepath.ToSlash(strings.TrimSpace(strings.Trim(path, "`'\"()[]{}<>,;")))
	if path == "" {
		return false
	}

	pathLower := strings.ToLower(path)
	for _, reference := range quotedReferences {
		quotedPath := reference.Path
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

func sourceCitationEscapingReferences(scanText string) []sourceReference {
	var references []sourceReference
	for _, raw := range sourceCitationEscapingPathCandidates(scanText) {
		raw = strings.Trim(raw, "`'\"()[]{}<>,;")
		if !runnerPathEscapesWorkspace(raw) {
			continue
		}

		path := sourceCitationEscapingPathDisplay(raw)
		if path == "" || !pathparse.LooksLikeWorkspaceFilePath(path) {
			continue
		}

		reference := sourceReferenceForPath(path)
		reference.Kind = sourceReferenceExternalDocument
		reference.EscapesWorkspace = true
		references = append(references, reference)
	}

	return normalizeSourceReferences(references)
}

func sourceCitationEscapingPathCandidates(text string) []string {
	var candidates []string
	for _, match := range sourceCitationInlineCodePathCandidatePattern.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			candidates = append(candidates, match[1])
		}
	}
	return append(candidates, strings.Fields(text)...)
}

func sourceReferenceForPath(rawPath string) sourceReference {
	path, lineRange := sourceCitationPathAndRange(strings.TrimSpace(strings.Trim(rawPath, "`'\"()[]{}<>,;")))
	path = filepath.ToSlash(path)

	reference := sourceReference{Path: NormalizeRunnerPath(path), LineRange: lineRange}
	switch {
	case filepath.IsAbs(path), filepath.VolumeName(path) != "", pathparse.LooksLikeWindowsAbsolutePath(path), runnerPathEscapesWorkspace(path):
		reference.Kind = sourceReferenceExternalDocument
		reference.EscapesWorkspace = true
	case pathparse.LooksLikeWorkspaceFilePath(path):
		reference.Kind = sourceReferenceSourceFile
	default:
		reference.Kind = sourceReferenceDirectoryLabel
		reference.Path = strings.TrimRight(reference.Path, "/")
	}

	return reference
}

func normalizeSourceReferences(references []sourceReference) []sourceReference {
	seen := make(map[string]struct{}, len(references))
	rangedPaths := make(map[string]struct{})
	for _, reference := range references {
		if reference.LineRange != nil {
			rangedPaths[string(reference.Kind)+"\x00"+strings.TrimSpace(reference.Path)] = struct{}{}
		}
	}
	out := make([]sourceReference, 0, len(references))

	for _, reference := range references {
		reference.Path = strings.TrimSpace(reference.Path)
		if reference.Path == "" || reference.Kind == "" {
			continue
		}
		pathKey := string(reference.Kind) + "\x00" + reference.Path
		if reference.LineRange == nil {
			if _, ranged := rangedPaths[pathKey]; ranged {
				continue
			}
		}

		key := pathKey
		if reference.LineRange != nil {
			key += "\x00" + strconv.Itoa(reference.LineRange.Start) + "-" + strconv.Itoa(reference.LineRange.End)
		}
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, reference)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			if out[i].LineRange != nil && out[j].LineRange != nil && out[i].LineRange.Start != out[j].LineRange.Start {
				return out[i].LineRange.Start < out[j].LineRange.Start
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})

	return out
}

func filterSourceReferences(references []sourceReference, keep func(sourceReference) bool) []sourceReference {
	var out []sourceReference
	for _, reference := range references {
		if keep != nil && keep(reference) {
			out = append(out, reference)
		}
	}
	return normalizeSourceReferences(out)
}

func sourceReferencePaths(references []sourceReference) []string {
	paths := make([]string, 0, len(references))
	seen := make(map[string]struct{}, len(references))
	for _, reference := range normalizeSourceReferences(references) {
		if _, duplicate := seen[reference.Path]; duplicate {
			continue
		}
		seen[reference.Path] = struct{}{}
		paths = append(paths, reference.Path)
	}
	return paths
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
