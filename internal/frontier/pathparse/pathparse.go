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
		return true
	}
	switch candidate {
	case "README.md", "go.mod", "go.sum", "config.json", "Makefile":
		return true
	}
	return HasWorkspaceFileExtension(candidate)
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
		".md", ".mdx", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".xml", ".ini", ".cfg", ".conf", ".env", ".txt", ".csv", ".tsv":
		return true
	default:
		return false
	}
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
	briefBodyStartMarker = "<!-- IDIOM:BRIEF-BODY-START -->"
	briefBodyEndMarker   = "<!-- IDIOM:BRIEF-BODY-END -->"
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

func MentionedWorkspacePaths(text string) []string {
	text = ScopeToBriefBody(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
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
	for _, token := range strings.Fields(text) {
		addCandidate(token)
	}
	for _, token := range WorkspacePathFieldCandidates(text) {
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
	candidate := strings.TrimSpace(token)
	candidate = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(candidate)
	candidate = strings.TrimSpace(candidate)
	candidate = strings.Trim(candidate, "`'\"\\()[]{}<>,:;!?")
	candidate = strings.TrimPrefix(candidate, "@")
	candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
	candidate = strings.TrimRight(candidate, ".")
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
				candidate = strings.TrimSpace(candidate[colon+1:])
				candidate = strings.Trim(candidate, "`'\"\\()[]{}<>,:;!?")
				candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
				candidate = strings.TrimRight(candidate, ".")
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
