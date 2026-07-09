package workspace

import (
	"regexp"
	"strings"
)

// LooksLikeIdentifier returns true when the pattern is a bare identifier shape
// that can safely route to symbol-oriented language tools.
func LooksLikeIdentifier(pattern string) bool {
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

func LooksLikeSymbolIdentifier(pattern string) bool {
	if !LooksLikeIdentifier(pattern) {
		return false
	}

	for _, r := range pattern {
		switch {
		case r == '_' || r == ':':
			return true
		case r >= '0' && r <= '9':
			return true
		case r >= 'A' && r <= 'Z':
			return true
		}
	}

	return false
}

func SyntaxIdentifierForLanguage(language string, pattern string) (string, bool) {
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
