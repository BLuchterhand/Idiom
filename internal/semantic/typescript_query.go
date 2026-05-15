package semantic

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/util"
)

type TsSymbolMatch struct {
	Path      string
	Name      string
	Qualified string
	Kind      string
	Scope     string
	Line      int
	Column    int
	StartLine int
	EndLine   int
	Exported  bool
	Score     int
}

type TsCallMatch struct {
	Caller     TsSymbolMatch
	CalleeName string
	Line       int
	Score      int
}

func (idx *TsIndex) FindSymbols(pattern string, limit int) []TsSymbolMatch {
	pattern = strings.ToLower(normalizeTsSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}
	matches := make([]TsSymbolMatch, 0)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			score := scoreTsSymbolMatch(pattern, file, symbol)
			if score <= 0 {
				continue
			}
			matches = append(matches, TsSymbolMatch{
				Path:      file.Path,
				Name:      symbol.Name,
				Qualified: firstTsQualified(symbol),
				Kind:      symbol.Kind,
				Scope:     symbol.Scope,
				Line:      symbol.Line,
				Column:    symbol.Column,
				StartLine: symbol.StartLine,
				EndLine:   symbol.EndLine,
				Exported:  symbol.Exported,
				Score:     score,
			})
		}
	}
	slices.SortFunc(matches, func(a, b TsSymbolMatch) int {
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

var tsSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:async\s+)?function(?:\s+\*?|\s*\*)\s*([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:namespace|module)\s+([A-Za-z_$][\w$]*)`),
}

func normalizeTsSymbolPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range tsSymbolPatternExtractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return pattern
}

func (idx *TsIndex) FindCallsFrom(pattern string, limit int) []TsCallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}
	out := make([]TsCallMatch, 0, 16)
	for _, match := range symbols {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Line != match.Line || symbol.Name != match.Name || firstTsQualified(symbol) != firstTsQualified(TsSymbol{Qualified: match.Qualified, Scope: match.Scope, Name: match.Name}) {
				continue
			}
			for _, call := range symbol.Calls {
				out = append(out, TsCallMatch{
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

func (idx *TsIndex) FindCallsInto(pattern string, limit int) []TsCallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	names := make(map[string]TsSymbolMatch, len(targets)*2)
	for _, target := range targets {
		names[strings.ToLower(target.Name)] = target
		if target.Qualified != "" {
			names[strings.ToLower(target.Qualified)] = target
		}
	}
	out := make([]TsCallMatch, 0, 16)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				target, ok := names[strings.ToLower(call.Name)]
				if !ok {
					continue
				}
				out = append(out, TsCallMatch{
					Caller: TsSymbolMatch{
						Path:      file.Path,
						Name:      symbol.Name,
						Qualified: firstTsQualified(symbol),
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
	slices.SortFunc(out, func(a, b TsCallMatch) int {
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

// Dependencies returns the transitive closure of imports starting from the
// files whose path matches the given pattern.
func (idx *TsIndex) Dependencies(pattern string) []string {
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

// WhoImports is the reverse of Dependencies.
func (idx *TsIndex) WhoImports(pattern string) []string {
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

func scoreTsSymbolMatch(pattern string, file TsFile, symbol TsSymbol) int {
	nameLower := strings.ToLower(symbol.Name)
	qualifiedLower := strings.ToLower(firstTsQualified(symbol))
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
	if symbol.Exported {
		score += 4
	}
	if symbol.Kind == "method" {
		score += 2
	}
	return score
}

func firstTsQualified(symbol TsSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "." + symbol.Name
	}
	return symbol.Name
}
