package python

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/platform/textutil"
)

type PythonSymbolMatch struct {
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

type PythonCallMatch struct {
	Caller     PythonSymbolMatch
	CalleeName string
	Line       int
	Score      int
}

func (idx *PythonIndex) FindSymbols(pattern string, limit int) []PythonSymbolMatch {
	pattern = strings.ToLower(normalizePythonSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}

	matches := make([]PythonSymbolMatch, 0)

	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			score := scorePythonSymbolMatch(pattern, file, symbol)
			if score <= 0 {
				continue
			}

			matches = append(matches, PythonSymbolMatch{
				Path:      file.Path,
				Name:      symbol.Name,
				Qualified: firstPythonQualified(symbol),
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

	slices.SortFunc(matches, func(a, b PythonSymbolMatch) int {
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

var pythonSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
}

func normalizePythonSymbolPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range pythonSymbolPatternExtractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}

	return pattern
}

func (idx *PythonIndex) FindCallsFrom(pattern string, limit int) []PythonCallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}

	out := make([]PythonCallMatch, 0, 16)

	for _, match := range symbols {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Line != match.Line || symbol.Name != match.Name {
				continue
			}

			for _, call := range symbol.Calls {
				out = append(out, PythonCallMatch{
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

func (idx *PythonIndex) FindCallsInto(pattern string, limit int) []PythonCallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}

	names := make(map[string]PythonSymbolMatch, len(targets)*2)
	for _, target := range targets {
		names[strings.ToLower(target.Name)] = target
		if target.Qualified != "" {
			names[strings.ToLower(target.Qualified)] = target
		}
	}

	out := make([]PythonCallMatch, 0, 16)

	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				target, ok := names[strings.ToLower(call.Name)]
				if !ok {
					continue
				}

				out = append(out, PythonCallMatch{
					Caller: PythonSymbolMatch{
						Path:      file.Path,
						Name:      symbol.Name,
						Qualified: firstPythonQualified(symbol),
						Kind:      symbol.Kind,
						Scope:     symbol.Scope,
						Line:      symbol.Line,
						Column:    symbol.Column,
						StartLine: symbol.StartLine,
						EndLine:   symbol.EndLine,
					},
					CalleeName: textutil.FirstNonEmpty(target.Qualified, target.Name),
					Line:       call.Line,
					Score:      target.Score,
				})
			}
		}
	}

	slices.SortFunc(out, func(a, b PythonCallMatch) int {
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

func (idx *PythonIndex) FindReferences(pattern string, limit int) []PythonReference {
	pattern = normalizePythonSymbolPattern(pattern)
	if dot := strings.LastIndex(pattern, "."); dot >= 0 {
		pattern = pattern[dot+1:]
	}
	if idx == nil || pattern == "" {
		return nil
	}

	name := strings.ToLower(pattern)
	var matches []PythonReference
	for _, file := range idx.Files {
		for _, reference := range file.References {
			if strings.ToLower(reference.Name) == name {
				matches = append(matches, reference)
			}
		}
	}

	slices.SortFunc(matches, func(a, b PythonReference) int {
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

// Implementations returns subclass symbols of classes whose name
// matches pattern. Useful as a lightweight, AST-free implementations
// query before a real LSP responds.
func (idx *PythonIndex) Implementations(pattern string) []PythonSymbolMatch {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || idx == nil {
		return nil
	}

	// Collect classes whose short name matches pattern to get the base name.
	base := pattern
	if dot := strings.LastIndex(pattern, "."); dot >= 0 {
		base = pattern[dot+1:]
	}

	out := make([]PythonSymbolMatch, 0)

	for _, file := range idx.Files {
		// Scan raw source is expensive; use symbols + file path heuristics.
		for _, sym := range file.Symbols {
			if sym.Kind != "class" {
				continue
			}

			if sym.Name == base {
				continue
			}

			// Heuristic: a subclass declaration looks like
			// "class X(Base):" or "class X(a.b.Base):" on the symbol's first line.
			// We don't have the raw line here, so we approximate via file
			// content below (opened once per file via caller if needed).
			// Instead, emit candidates whose file imports or uses the base name.
			uses := false

			for _, imp := range file.RawImports {
				if strings.Contains(imp, base) {
					uses = true
					break
				}
			}

			if !uses {
				continue
			}

			out = append(out, PythonSymbolMatch{
				Path:      file.Path,
				Name:      sym.Name,
				Qualified: firstPythonQualified(sym),
				Kind:      sym.Kind,
				Scope:     sym.Scope,
				Line:      sym.Line,
				Column:    sym.Column,
				StartLine: sym.StartLine,
				EndLine:   sym.EndLine,
				Score:     10,
			})
		}
	}

	slices.SortFunc(out, func(a, b PythonSymbolMatch) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}

		return cmp.Compare(a.Line, b.Line)
	})

	return out
}

func scorePythonSymbolMatch(pattern string, file PythonFile, symbol PythonSymbol) int {
	nameLower := strings.ToLower(symbol.Name)
	qualifiedLower := strings.ToLower(firstPythonQualified(symbol))
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

	switch symbol.Kind {
	case "method", "async_method":
		score += 2
	case "class":
		score += 1
	}

	if file.IsTest {
		score -= 6
	}

	return score
}

func firstPythonQualified(symbol PythonSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}

	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "." + symbol.Name
	}

	return symbol.Name
}
