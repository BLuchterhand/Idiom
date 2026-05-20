package pathparse

import (
	"path/filepath"
	"regexp"
	"strings"
)

var workspacePathFieldPattern = regexp.MustCompile(`(?i)(?:^|[\s,{])\\?["']?(?:path|file|source)\\?["']?\s*[:=]\s*\\?["']?([^"'\s,}\]]+)`)

func LooksLikeWorkspacePathToken(candidate string) bool {
	if candidate == "" || strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "/") {
		return false
	}
	if strings.ContainsAny(candidate, "*?") {
		return false
	}
	// Backticks inside a path token mean the token came from inside a
	// markdown code span (e.g. the planner preamble's prose `start_line`/
	// `end_line` becomes the candidate `start_line\`/\`end_line` after
	// outer-trim; the inner backticks are not stripped and are never
	// valid in workspace paths). Rejecting them here defends every
	// downstream consumer (required_edit_paths, discovery handles, etc.)
	// from picking up prose tokens as fake paths.
	if strings.ContainsAny(candidate, "`") {
		return false
	}
	if strings.Contains(candidate, "/") {
		if !HasWorkspaceFileExtension(candidate) && looksLikeSlashSeparatedSymbolToken(candidate) {
			return false
		}
		return true
	}
	switch candidate {
	case "README.md", "go.mod", "go.sum", "config.json", "Makefile":
		return true
	}
	return HasWorkspaceFileExtension(candidate)
}

func looksLikeSlashSeparatedSymbolToken(candidate string) bool {
	parts := strings.Split(candidate, "/")
	if len(parts) < 2 {
		return false
	}
	allSymbolChars := true
	sawUppercase := false
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			switch {
			case r >= 'A' && r <= 'Z':
				sawUppercase = true
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '_' || r == '-':
			default:
				allSymbolChars = false
			}
		}
	}
	if !allSymbolChars {
		return false
	}
	if sawUppercase {
		return true
	}
	return len(parts) == 2 && !knownExtensionlessWorkspaceRoot(parts[0])
}

func knownExtensionlessWorkspaceRoot(root string) bool {
	switch strings.ToLower(root) {
	case "app", "apps", "backend", "bin", "cmd", "client", "docs", "examples", "frontend", "internal", "lib", "native", "package", "packages", "pkg", "script", "scripts", "server", "service", "services", "src", "test", "tests", "tool", "tools", "vendor", "web":
		return true
	default:
		return false
	}
}

func HasWorkspaceFileExtension(candidate string) bool {
	switch filepath.Ext(candidate) {
	case ".go",
		".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp",
		".gd", ".tscn", ".tres",
		".py", ".pyi",
		".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".d.ts",
		".rs",
		".java", ".kt", ".kts",
		".sh", ".bash", ".zsh", ".fish", ".ps1",
		".md", ".mdx", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".xml", ".ini", ".cfg", ".conf", ".env", ".txt", ".csv", ".tsv":
		return true
	default:
		return false
	}
}

func LooksLikeWorkspaceFilePath(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if HasWorkspaceFileExtension(candidate) {
		return true
	}
	switch filepath.Base(candidate) {
	case "BUILD", "BUCK", "Dockerfile", "Gemfile", "LICENSE", "Makefile", "NOTICE", "Procfile", "README", "WORKSPACE", "go.mod", "go.sum":
		return true
	default:
		return false
	}
}

func MentionedWorkspaceFiles(text string) []string {
	return FilterWorkspaceFilePaths(MentionedWorkspacePaths(text))
}

func MentionedWorkspaceFilesInInlineList(text string) []string {
	return FilterWorkspaceFilePaths(MentionedWorkspacePathsInInlineList(text))
}

func MentionedWorkspaceFilesInMarkdownTableFirstCell(line string) []string {
	return FilterWorkspaceFilePaths(MentionedWorkspacePathsInMarkdownTableFirstCell(line))
}

func FilterWorkspaceFilePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if !LooksLikeWorkspaceFilePath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func MentionedWorkspacePathsUnderHeadings(text string, headings ...string) []string {
	if strings.TrimSpace(text) == "" || len(headings) == 0 {
		return nil
	}
	headingSet := make(map[string]struct{}, len(headings))
	for _, heading := range headings {
		normalized := normalizeSectionHeading(heading)
		if normalized != "" {
			headingSet[normalized] = struct{}{}
		}
	}
	if len(headingSet) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	active := false
	sawPath := false
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		normalized := normalizeSectionHeading(line)
		if _, ok := headingSet[normalized]; ok {
			active = true
			sawPath = false
			continue
		}
		if !active {
			continue
		}
		if line == "" {
			if sawPath {
				break
			}
			continue
		}
		if sawPath && looksLikeMarkdownHeading(line) {
			break
		}
		if looksLikeMarkdownTableRow(line) {
			paths := MentionedWorkspacePathsInMarkdownTableFirstCell(line)
			if len(paths) == 0 {
				continue
			}
			sawPath = true
			for _, path := range paths {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				out = append(out, path)
			}
			continue
		}
		paths := MentionedWorkspacePaths(line)
		if len(paths) == 0 {
			continue
		}
		sawPath = true
		for _, path := range paths {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			out = append(out, path)
		}
	}
	return out
}

func MentionedWorkspacePathsInMarkdownTableFirstCell(line string) []string {
	cells, ok := markdownTableCells(line)
	if !ok || len(cells) == 0 {
		return nil
	}
	if markdownTableSeparatorCell(cells[0]) {
		return nil
	}
	return MentionedWorkspacePaths(cells[0])
}

func looksLikeMarkdownTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

func markdownTableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !looksLikeMarkdownTableRow(trimmed) {
		return nil, false
	}
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells, len(cells) > 0
}

func markdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}
	for _, r := range cell {
		if r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return strings.Contains(cell, "-")
}

func normalizeSectionHeading(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "#*: `\"'")
	value = strings.TrimSuffix(value, ":")
	value = strings.ToLower(value)
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func looksLikeMarkdownHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "#") ||
		(strings.HasPrefix(trimmed, "**") && strings.Contains(trimmed, "**")) ||
		strings.HasSuffix(trimmed, ":")
}

// briefBodyStartMarker / briefBodyEndMarker mirror the markers that
// cmd/idiom/lane.go's composeLanePromptToFile wraps around the project
// brief inside the composed lane prompt. When MentionedWorkspacePaths
// (and its callers) see these markers in the input, they scope their
// scan to the marked region — that's how the harness avoids picking
// up prose tokens like `start_line`/`end_line` from the embedded
// preamble. The markers are not present in non-lane prompts, so the
// scan-everything fallback preserves behavior for chat / direct API.
const (
	BriefBodyStartMarker = "<!-- IDIOM:BRIEF-BODY-START -->"
	BriefBodyEndMarker   = "<!-- IDIOM:BRIEF-BODY-END -->"
	briefBodyStartMarker = BriefBodyStartMarker
	briefBodyEndMarker   = BriefBodyEndMarker
)

// ScopeToBriefBody returns the substring between the brief-body
// markers when both are present in order; otherwise returns the
// input unchanged. Centralized so every extractor that scans
// composed prompts can opt in via one call. Exported so callers
// outside pathparse (notably internal/app's required-edit-paths
// extractor) can reuse the same scoping rule.
func ScopeToBriefBody(text string) string {
	start := strings.Index(text, briefBodyStartMarker)
	if start < 0 {
		return text
	}
	bodyStart := start + len(briefBodyStartMarker)
	end := strings.Index(text[bodyStart:], briefBodyEndMarker)
	if end < 0 {
		return text
	}
	return text[bodyStart : bodyStart+end]
}

func WrapBriefBody(text string) string {
	return BriefBodyStartMarker + "\n" + strings.TrimSpace(text) + "\n" + BriefBodyEndMarker
}

func MentionedWorkspacePaths(text string) []string {
	text = ScopeToBriefBody(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return mentionedWorkspacePathsFromTokens(strings.Fields(text), WorkspacePathFieldCandidates(text))
}

func MentionedWorkspacePathsInInlineList(text string) []string {
	text = ScopeToBriefBody(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var tokens []string
	sawPath := false
	for _, token := range strings.Fields(text) {
		tokens = append(tokens, token)
		if len(mentionedWorkspacePathsFromTokens([]string{token}, nil)) > 0 {
			sawPath = true
		}
		if sawPath && inlineListTokenEndsPathSentence(token) {
			return mentionedWorkspacePathsFromTokens(tokens, nil)
		}
	}
	return mentionedWorkspacePathsFromTokens(tokens, nil)
}

func inlineListTokenEndsPathSentence(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return strings.HasSuffix(token, ".") || strings.HasSuffix(token, ";")
}

func mentionedWorkspacePathsFromTokens(tokens []string, fieldCandidates []string) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	addCandidate := func(token string) {
		candidate, ok := NormalizeWorkspacePathToken(token)
		if !ok {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	for _, token := range tokens {
		addCandidate(token)
	}
	for _, token := range fieldCandidates {
		addCandidate(token)
	}
	return paths
}

func WorkspacePathFieldCandidates(text string) []string {
	matches := workspacePathFieldPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			candidates = append(candidates, match[1])
		}
	}
	return candidates
}

func NormalizeWorkspacePathToken(token string) (string, bool) {
	candidate := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(token)
	candidate = trimWorkspacePathTokenEdges(candidate)
	candidate = strings.TrimPrefix(candidate, "@")
	candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
	candidate = trimWorkspacePathTokenEdges(candidate)
	if candidate == "" {
		return "", false
	}
	lowerCandidate := strings.ToLower(candidate)
	if strings.HasPrefix(lowerCandidate, "destination:") {
		return "", false
	}
	if colon := strings.Index(candidate, ":"); colon >= 0 {
		slash := strings.Index(candidate, "/")
		if slash < 0 || colon < slash {
			lowerPrefix := strings.ToLower(strings.Trim(candidate[:colon], "`'\"\\ "))
			switch lowerPrefix {
			case "path", "file", "source":
				candidate = trimWorkspacePathTokenEdges(candidate[colon+1:])
				candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
				candidate = trimWorkspacePathTokenEdges(candidate)
			default:
				return "", false
			}
		} else {
			candidate = StripPathLocationSuffix(candidate)
		}
	}
	candidate = StripPathLocationSuffix(candidate)
	if strings.ContainsAny(candidate, "\"'\\{}[]") || strings.Contains(candidate, ",") {
		return "", false
	}
	if !LooksLikeWorkspacePathToken(candidate) {
		return "", false
	}
	candidate = filepath.Clean(candidate)
	if candidate == "." {
		return "", false
	}
	return candidate, true
}

func trimWorkspacePathTokenEdges(token string) string {
	candidate := strings.TrimSpace(token)
	for {
		before := candidate
		candidate = strings.TrimSpace(candidate)
		candidate = strings.Trim(candidate, "`'\"\\()[]{}<>,:;!?")
		candidate = strings.TrimRight(candidate, ".")
		candidate = strings.Trim(candidate, "`'\"\\()[]{}<>,:;!?")
		if candidate == before {
			return candidate
		}
	}
}

func StripPathLocationSuffix(candidate string) string {
	for {
		colon := strings.LastIndex(candidate, ":")
		if colon < 0 {
			return candidate
		}
		suffix := candidate[colon+1:]
		if suffix == "" || !AllASCIIDigits(suffix) {
			return candidate
		}
		candidate = candidate[:colon]
	}
}

func AllASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func QuotedFragments(text string) []string {
	fragments := make([]string, 0, 4)
	for i := 0; i < len(text); i++ {
		quote := text[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		start := i + 1
		end := start
		for end < len(text) && text[end] != quote {
			end++
		}
		if end >= len(text) {
			break
		}
		fragment := strings.TrimSpace(text[start:end])
		if fragment != "" {
			fragments = append(fragments, fragment)
		}
		i = end
	}
	return fragments
}

func PrimaryLocateLiteral(prompt string) string {
	for _, fragment := range QuotedFragments(prompt) {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" || LooksLikeWorkspacePathToken(trimmed) {
			continue
		}
		return trimmed
	}

	lower := strings.ToLower(prompt)
	for _, marker := range []string{"shows ", "showing ", "says ", "text ", "label "} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		candidate := TrimLocateLiteralTail(prompt[idx+len(marker):])
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func TrimLocateLiteralTail(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	stopWords := map[string]struct{}{
		"and":    {},
		"but":    {},
		"please": {},
		"so":     {},
		"that":   {},
		"which":  {},
	}
	parts := make([]string, 0, 4)
	for _, field := range strings.Fields(trimmed) {
		word := strings.Trim(field, "`'\"()[]{}<>,:;!?")
		if word == "" {
			continue
		}
		if _, stop := stopWords[strings.ToLower(word)]; stop {
			break
		}
		parts = append(parts, word)
		if len(parts) >= 4 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
