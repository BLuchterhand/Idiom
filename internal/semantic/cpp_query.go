package semantic

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/util"
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
			matches = append(matches, CppSymbolMatch{
				Path:      file.Path,
				Name:      symbol.Name,
				Qualified: firstCppQualified(symbol),
				Kind:      symbol.Kind,
				Scope:     symbol.Scope,
				Line:      symbol.Line,
				Column:    symbol.Column,
				StartLine: symbol.StartLine,
				EndLine:   symbol.EndLine,
				Score:     score,
			})
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
	return NormalizeSymbolPattern(pattern, cppSymbolPatternExtractors)
}

func (idx *CppIndex) FindCallsFrom(pattern string, limit int) []CppCallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}
	out := make([]CppCallMatch, 0, 16)
	for _, match := range symbols {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Line != match.Line || symbol.Name != match.Name || firstCppQualified(symbol) != firstCppQualified(CppSymbol{Qualified: match.Qualified, Scope: match.Scope, Name: match.Name}) {
				continue
			}
			for _, call := range symbol.Calls {
				out = append(out, CppCallMatch{
					Caller:     match,
					CalleeName: call.Name,
					Line:       call.Line,
					Score:      match.Score,
				})
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (idx *CppIndex) FindCallsInto(pattern string, limit int) []CppCallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	names := make(map[string]CppSymbolMatch, len(targets)*2)
	for _, target := range targets {
		names[strings.ToLower(target.Name)] = target
		if target.Qualified != "" {
			names[strings.ToLower(target.Qualified)] = target
		}
	}
	out := make([]CppCallMatch, 0, 16)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				target, ok := names[strings.ToLower(call.Name)]
				if !ok {
					continue
				}
				out = append(out, CppCallMatch{
					Caller: CppSymbolMatch{
						Path:      file.Path,
						Name:      symbol.Name,
						Qualified: firstCppQualified(symbol),
						Kind:      symbol.Kind,
						Scope:     symbol.Scope,
						Line:      symbol.Line,
						Column:    symbol.Column,
						StartLine: symbol.StartLine,
						EndLine:   symbol.EndLine,
					},
					CalleeName: util.FirstNonEmpty(target.Qualified, target.Name),
					Line:       call.Line,
					Score:      target.Score,
				})
			}
		}
	}
	slices.SortFunc(out, func(a, b CppCallMatch) int {
		if c := cmp.Compare(a.Caller.Path, b.Caller.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.Line, b.Line)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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
	targets := idx.findFiles(pattern)
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

func (idx *CppIndex) BlastRadius(pattern string) []string {
	files := idx.WhoIncludes(pattern)
	if len(files) == 0 {
		return nil
	}
	out := make([]string, 0, len(files))
	for _, file := range files {
		if isCppHeader(file) {
			continue
		}
		out = append(out, file)
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
	return FirstQualified(symbol.Scope, symbol.Name, "::")
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
