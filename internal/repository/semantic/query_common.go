package semantic

import (
	"regexp"
	"strings"
)

func NormalizeSymbolPattern(pattern string, extractors []*regexp.Regexp) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range extractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return pattern
}

func StripLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func StripLineCommentStringAware(line string) string {
	inString := false
	for i := 0; i < len(line)-1; i++ {
		r := line[i]
		if r == '\\' {
			i++
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && line[i] == '/' && line[i+1] == '/' {
			return line[:i]
		}
	}
	return line
}

func FirstQualified(scope, name, sep string) string {
	if strings.TrimSpace(scope) != "" {
		return scope + sep + name
	}
	return name
}
