package agent

import (
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/workspace"
)

func searchContentTargetsConcreteFile(req workspace.ToolRequest) bool {
	path := strings.TrimSpace(req.Path)
	if path == "" || strings.ContainsAny(path, "*?[]{}") {
		return false
	}
	return filepath.Ext(filepath.Base(path)) != ""
}

// languageForSearchRequest infers a language for a generic search_content
// request from the strongest available signal. It deliberately avoids
// guessing: ambiguous syntax in a polyglot workspace falls through to the
// original text search instead of redirecting to the wrong language tool.
func languageForSearchRequest(req workspace.ToolRequest, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	if lang, ok := languageFromConcretePath(req.Path); ok && patternCompatibleWithLanguage(req.Pattern, lang) {
		return lang, true
	}
	if lang, ok := languageFromGlob(req.Glob); ok && patternCompatibleWithLanguage(req.Pattern, lang) {
		return lang, true
	}
	return languageFromSyntaxPattern(req.Pattern, ws)
}

func patternCompatibleWithLanguage(pattern string, lang workspace.LanguageSupport) bool {
	return looksLikeSymbolIdentifier(pattern) || languageSyntaxMatches(lang.Name, pattern)
}

func languageFromConcretePath(path string) (workspace.LanguageSupport, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return workspace.LanguageSupport{}, false
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return workspace.LanguageSupport{}, false
	}
	return workspace.LanguageForExtension(ext)
}

func languageActive(ws *workspace.Manager, lang workspace.LanguageSupport) bool {
	for _, active := range ws.ActiveLanguages() {
		if active.Name == lang.Name {
			return true
		}
	}
	return false
}

// looksLikeIdentifier returns true when the pattern is a bare identifier
// the model is almost certainly trying to locate -- the redirect should
// only fire for symbol-shaped queries, not for free-form regex.
func looksLikeIdentifier(pattern string) bool {
	if len(pattern) < 2 {
		return false
	}
	for _, r := range pattern {
		switch {
		case r == '_' || r == ':':
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

func looksLikeSymbolIdentifier(pattern string) bool {
	if !looksLikeIdentifier(pattern) {
		return false
	}
	hasSymbolShape := false
	for _, r := range pattern {
		switch {
		case r == '_' || r == ':':
			hasSymbolShape = true
		case r >= '0' && r <= '9':
			hasSymbolShape = true
		case r >= 'A' && r <= 'Z':
			hasSymbolShape = true
		}
	}
	return hasSymbolShape
}

// languageFromGlob derives a language from a filename glob like
// "*.go", "*.{cpp,h}", "**/*.cc". Only language-specific globs
// redirect; a generic "*" matches nothing here.
func languageFromGlob(glob string) (workspace.LanguageSupport, bool) {
	glob = strings.ToLower(strings.TrimSpace(glob))
	if glob == "" {
		return workspace.LanguageSupport{}, false
	}
	base := filepath.Base(glob)
	// Handle brace-expansion patterns like "*.{cpp,h}".
	if strings.Contains(base, "{") && strings.Contains(base, "}") {
		open := strings.Index(base, "{")
		close := strings.Index(base, "}")
		if open >= 0 && close > open {
			inner := base[open+1 : close]
			for _, ext := range strings.Split(inner, ",") {
				ext = strings.TrimSpace(ext)
				if ext == "" {
					continue
				}
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				if lang, ok := workspace.LanguageForExtension(ext); ok {
					return lang, true
				}
			}
			return workspace.LanguageSupport{}, false
		}
	}
	ext := filepath.Ext(base)
	if ext == "" {
		return workspace.LanguageSupport{}, false
	}
	return workspace.LanguageForExtension(ext)
}

func languageFromSyntaxPattern(pattern string, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || ws == nil {
		return workspace.LanguageSupport{}, false
	}
	var matches []workspace.LanguageSupport
	for _, lang := range ws.ActiveLanguages() {
		if languageSyntaxMatches(lang.Name, pattern) {
			matches = append(matches, lang)
		}
	}
	if len(matches) != 1 {
		return workspace.LanguageSupport{}, false
	}
	return matches[0], true
}

func languageSyntaxMatches(language string, pattern string) bool {
	_, ok := extractSyntaxIdentifierForLanguage(language, pattern)
	return ok
}

func extractSyntaxIdentifierForLanguage(language string, pattern string) (string, bool) {
	for _, re := range syntaxExtractors[strings.ToLower(strings.TrimSpace(language))] {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1], true
		}
	}
	return "", false
}

var syntaxExtractors = map[string][]*regexp.Regexp{
	"go": {
		regexp.MustCompile(`^func\s*\([^)]*\)\s*([A-Za-z_]\w*)`),
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^func[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^const\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^var\s+([A-Za-z_]\w*)`),
	},
	"cpp": {
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^class[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^namespace\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^enum(?:\s+class)?\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^union\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^union[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^template\s*<[^>]*>\s*(?:class|struct|typename)?\s*([A-Za-z_]\w*)`),
	},
	"python": {
		regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
	},
	"typescript": {
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:async\s+)?function(?:\s+\*?|\s*\*)\s*([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:namespace|module)\s+([A-Za-z_$][\w$]*)`),
	},
	"rust": {
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+|const\s+|unsafe\s+|extern\s+(?:"[^"]+"\s+)?)*fn\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum)\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?type\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?const\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?static\s+(?:mut\s+)?([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?macro_rules!\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^impl(?:<[^>]*>)?\s+([A-Za-z_]\w*)`),
	},
	"java": {
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*interface\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*enum\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*record\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*@interface\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|synchronized|native|default|strictfp|\s)*(?:<[^>]+>\s+)?(?:[A-Za-z_][\w<>\[\].?,\s]*\s+)?([A-Za-z_]\w*)\s*\(`),
	},
}

// isStructuredTool reports whether a tool name is already a
// language-specific structured tool; we never redirect one structured
// tool to another.
func isStructuredTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	if t == "" {
		return false
	}
	for _, prefix := range structuredToolPrefixes() {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func structuredToolPrefixes() []string {
	seen := make(map[string]bool)
	var prefixes []string
	for _, lang := range workspace.LanguageRegistry() {
		for _, tool := range lang.Tools {
			prefix, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(tool)), "_")
			if !ok || prefix == "" || seen[prefix+"_"] {
				continue
			}
			seen[prefix+"_"] = true
			prefixes = append(prefixes, prefix+"_")
		}
	}
	return prefixes
}
