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
	if strings.Contains(candidate, "/") {
		return strings.Contains(candidate, ".")
	}
	switch candidate {
	case "README.md", "go.mod", "go.sum", "config.json", "Makefile":
		return true
	}
	return HasWorkspaceFileExtension(candidate)
}

func HasWorkspaceFileExtension(candidate string) bool {
	switch filepath.Ext(candidate) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt":
		return true
	default:
		return false
	}
}

func MentionedWorkspacePaths(text string) []string {
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
