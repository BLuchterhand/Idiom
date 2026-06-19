package semantic

import (
	"regexp"

	"idiom/internal/repository/semantic/internal/indexsupport"
)

func RankFiles[T any](files []T, scoreFn func(T) int, limit int) []T {
	return indexsupport.RankFiles(files, func(file T) string { return AnyPath(file) }, scoreFn, limit)
}

func UniqueRankedPaths[T any](files []T, limit int) []string {
	return indexsupport.UniqueRankedPaths(files, func(file T) string { return AnyPath(file) }, limit)
}

func FormatIndexSummary(fileCount int, language string, anchors []string, describeAnchor func(string) (string, bool)) string {
	return indexsupport.FormatIndexSummary(fileCount, language, anchors, describeAnchor)
}

func RelevantFileScore(terms []string, path string, related []string, symbolTerms []string, sourceDirPriority int, isTest bool) int {
	return indexsupport.RelevantFileScore(terms, path, related, symbolTerms, sourceDirPriority, isTest)
}

func ExtractLineCalls[T any](lines []string, startLine int, endLine int, callPattern *regexp.Regexp, stripComment func(string) string, isKeyword func(string) bool, buildCall func(string, int) T) []T {
	return indexsupport.ExtractLineCalls(lines, startLine, endLine, callPattern, stripComment, isKeyword, buildCall)
}

func FindFilesExactPartial[T any](files []T, pattern string) []string {
	return indexsupport.FindFilesExactPartial(files, func(file T) string { return AnyPath(file) }, pattern)
}

func AnyPath(v any) string {
	switch f := v.(type) {
	case GoFile:
		return f.Path
	case CppFile:
		return f.Path
	case JavaFile:
		return f.Path
	case RustFile:
		return f.Path
	case TsFile:
		return f.Path
	case PythonFile:
		return f.Path
	case string:
		return f
	default:
		return ""
	}
}
