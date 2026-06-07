package semantic

import (
	"regexp"

	"idiom/internal/repository/semantic/internal/indexsupport"
)

func NormalizeSymbolPattern(pattern string, extractors []*regexp.Regexp) string {
	return indexsupport.NormalizeSymbolPattern(pattern, extractors)
}

func StripLineComment(line string) string {
	return indexsupport.StripLineComment(line)
}

func StripLineCommentStringAware(line string) string {
	return indexsupport.StripLineCommentStringAware(line)
}

func FirstQualified(scope, name, sep string) string {
	return indexsupport.FirstQualified(scope, name, sep)
}
