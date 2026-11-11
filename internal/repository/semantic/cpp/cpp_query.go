package cpp

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/repository/semantic/internal/indexsupport"
)

type CppSymbolMatch struct {
	Path      string
	Name      string
	Qualified string
	Kind      string
	Scope     string
	Line      int
	Column    int
	StartLine int
	EndLine   int
	Score     int
	Explicit  bool
}

type CppCallMatch struct {
	Caller     CppSymbolMatch
	CalleeName string
	Line       int
	Score      int
}

func (idx *CppIndex) FindSymbols(pattern string, limit int) []CppSymbolMatch {
	pattern = strings.ToLower(normalizeCppSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}

	matches := make([]CppSymbolMatch, 0)

	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			score := scoreCppSymbolMatch(pattern, file, symbol)
			if score <= 0 {
				continue
			}

			match := symbolMatch(file.Path, symbol)
			match.Score = score
			matches = append(matches, match)
		}
	}

	slices.SortFunc(matches, func(a, b CppSymbolMatch) int {
		if c := -cmp.Compare(a.Score, b.Score); c != 0 {
			return c
		}

		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}

		return cmp.Compare(a.Line, b.Line)
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	return matches
}

var cppSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^namespace\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^enum(?:\s+class)?\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^union\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^template\s*<[^>]*>\s*(?:class|struct|typename)?\s*([A-Za-z_]\w*)`),
}

func normalizeCppSymbolPattern(pattern string) string {
	return indexsupport.NormalizeSymbolPattern(pattern, cppSymbolPatternExtractors)
}

// CallsFromDefinitions returns the call sites inside the given definitions
// (as returned by ResolveSymbol) plus the total before limit truncation.
func (idx *CppIndex) CallsFromDefinitions(definitions []CppSymbolMatch, limit int) ([]CppCallMatch, int) {
	out := make([]CppCallMatch, 0, 16)

	for _, match := range definitions {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Line != match.Line || symbol.Name != match.Name || firstCppQualified(symbol) != match.Qualified {
				continue
			}

			for _, call := range symbol.Calls {
				out = append(out, CppCallMatch{Caller: match, CalleeName: call.Name, Line: call.Line})
			}
		}
	}

	total := len(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out, total
}

func (idx *CppIndex) Dependencies(pattern string) []string {
	targets := idx.findFiles(pattern)
	if len(targets) == 0 {
		return nil
	}

	out := make([]string, 0)
	seen := make(map[string]struct{})

	for _, target := range targets {
		for _, dep := range transitiveStringClosure(idx.Forward, target) {
			if _, ok := seen[dep]; ok {
				continue
			}

			seen[dep] = struct{}{}
			out = append(out, dep)
		}
	}

	slices.Sort(out)

	return out
}

func (idx *CppIndex) WhoIncludes(pattern string) []string {
	return idx.includersOf(idx.findFiles(pattern))
}

// MatchFiles returns the indexed files a path pattern names.
func (idx *CppIndex) MatchFiles(pattern string) []string {
	return idx.findFiles(pattern)
}

// BlastRadius returns the translation units (non-header files) that include the
// files the pattern names, directly or transitively.
func (idx *CppIndex) BlastRadius(pattern string) []string {
	return idx.BlastRadiusOfFiles(idx.findFiles(pattern), false)
}

// BlastRadiusOfFiles returns the translation units affected by changing the
// given files. With includeSelf the non-header targets themselves are included,
// which is what a change to a symbol defined in a .cpp file affects.
func (idx *CppIndex) BlastRadiusOfFiles(targets []string, includeSelf bool) []string {
	files := idx.includersOf(targets)
	if includeSelf {
		files = append(files, targets...)
	}

	out := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))

	for _, file := range files {
		if _, ok := seen[file]; ok || isCppHeader(file) {
			continue
		}

		seen[file] = struct{}{}

		out = append(out, file)
	}

	slices.Sort(out)

	return out
}

func (idx *CppIndex) includersOf(targets []string) []string {
	if len(targets) == 0 {
		return nil
	}

	out := make([]string, 0)
	seen := make(map[string]struct{})

	for _, target := range targets {
		for _, dep := range transitiveStringClosure(idx.Reverse, target) {
			if _, ok := seen[dep]; ok {
				continue
			}

			seen[dep] = struct{}{}
			out = append(out, dep)
		}
	}

	slices.Sort(out)

	return out
}

func scoreCppSymbolMatch(pattern string, file CppFile, symbol CppSymbol) int {
	nameLower := strings.ToLower(symbol.Name)
	qualifiedLower := strings.ToLower(firstCppQualified(symbol))
	pathLower := strings.ToLower(file.Path)
	score := 0

	switch {
	case nameLower == pattern || qualifiedLower == pattern:
		score += 48
	case strings.Contains(qualifiedLower, pattern):
		score += 24
	case strings.Contains(nameLower, pattern):
		score += 20
	}

	if strings.Contains(pathLower, pattern) {
		score += 8
	}

	if score > 0 && symbol.Kind == "method" {
		score += 2
	}

	return score
}

func firstCppQualified(symbol CppSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}

	return indexsupport.FirstQualified(symbol.Scope, symbol.Name, "::")
}

func transitiveStringClosure(graph map[string][]string, start string) []string {
	if start == "" {
		return nil
	}

	queue := []string{start}
	seen := make(map[string]struct{})
	out := make([]string, 0)

	for len(queue) > 0 {
		current := queue[0]

		queue = queue[1:]

		for _, next := range graph[current] {
			if _, ok := seen[next]; ok {
				continue
			}

			seen[next] = struct{}{}
			out = append(out, next)
			queue = append(queue, next)
		}
	}

	return out
}
