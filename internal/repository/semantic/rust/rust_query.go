package rust

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/platform/util"
)

// RustSymbolMatch is the public shape returned by FindSymbols: a flat
// record that carries enough info for workspace-layer code to render a
// handle, jump to a line, or hand-off to rust-analyzer.
type RustSymbolMatch struct {
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

type RustCallMatch struct {
	Caller     RustSymbolMatch
	CalleeName string
	Line       int
	Score      int
}

func (idx *RustIndex) FindSymbols(pattern string, limit int) []RustSymbolMatch {
	pattern = strings.ToLower(normalizeRustSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}
	matches := make([]RustSymbolMatch, 0)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			score := scoreRustSymbolMatch(pattern, file, symbol)
			if score <= 0 {
				continue
			}
			matches = append(matches, RustSymbolMatch{
				Path:      file.Path,
				Name:      symbol.Name,
				Qualified: firstRustQualified(symbol),
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
	slices.SortFunc(matches, func(a, b RustSymbolMatch) int {
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

var rustSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+|const\s+|unsafe\s+|extern\s+(?:"[^"]+"\s+)?)*fn\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum)\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?type\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?const\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?static\s+(?:mut\s+)?([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?macro_rules!\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^impl(?:<[^>]*>)?\s+([A-Za-z_]\w*)`),
}

func normalizeRustSymbolPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range rustSymbolPatternExtractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return pattern
}

func (idx *RustIndex) FindCallsFrom(pattern string, limit int) []RustCallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}
	out := make([]RustCallMatch, 0, 16)
	for _, match := range symbols {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Line != match.Line || symbol.Name != match.Name {
				continue
			}
			if firstRustQualified(symbol) != match.Qualified && symbol.Qualified != match.Qualified {
				continue
			}
			for _, call := range symbol.Calls {
				out = append(out, RustCallMatch{
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

func (idx *RustIndex) FindCallsInto(pattern string, limit int) []RustCallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	names := make(map[string]RustSymbolMatch, len(targets)*2)
	for _, target := range targets {
		names[strings.ToLower(target.Name)] = target
		if target.Qualified != "" {
			names[strings.ToLower(target.Qualified)] = target
		}
	}
	out := make([]RustCallMatch, 0, 16)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				target, ok := names[strings.ToLower(call.Name)]
				if !ok {
					continue
				}
				out = append(out, RustCallMatch{
					Caller: RustSymbolMatch{
						Path:      file.Path,
						Name:      symbol.Name,
						Qualified: firstRustQualified(symbol),
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
	slices.SortFunc(out, func(a, b RustCallMatch) int {
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

// TraitImplementers returns the files + impl symbols that declare
// `impl <traitName> for <Type>` anywhere in the workspace. Used by the
// `implementations` capability as a fallback when rust-analyzer is not
// available.
func (idx *RustIndex) TraitImplementers(traitName string, limit int) []RustSymbolMatch {
	traitName = strings.TrimSpace(traitName)
	if idx == nil || traitName == "" {
		return nil
	}
	out := make([]RustSymbolMatch, 0)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			if symbol.Kind != "impl" {
				continue
			}
			// Qualified is "TraitName for Type" for trait impls; bare
			// "Type" for inherent impls. Only trait impls count here.
			if !strings.Contains(symbol.Qualified, " for ") {
				continue
			}
			parts := strings.SplitN(symbol.Qualified, " for ", 2)
			if len(parts) != 2 {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(parts[0]), traitName) {
				// Allow fuzzy match on the leading segment (e.g.
				// `MyTrait<'a>` should match `MyTrait`).
				if !strings.HasPrefix(strings.TrimSpace(parts[0]), traitName) {
					continue
				}
			}
			out = append(out, RustSymbolMatch{
				Path:      file.Path,
				Name:      strings.TrimSpace(parts[1]),
				Qualified: symbol.Qualified,
				Kind:      "impl",
				Line:      symbol.Line,
				Column:    symbol.Column,
				StartLine: symbol.StartLine,
				EndLine:   symbol.EndLine,
			})
		}
	}
	slices.SortFunc(out, func(a, b RustSymbolMatch) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.Line, b.Line)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ModuleSiblings returns files under the same `mod` path as `path` —
// i.e. files that the parent module either declares via `mod X;` or that
// sit in the same directory. Used by `related_files`.
func (idx *RustIndex) ModuleSiblings(path string, limit int) []string {
	path = strings.TrimSpace(path)
	if idx == nil || path == "" {
		return nil
	}
	out := make([]string, 0)
	seen := make(map[string]struct{})

	// Same-directory siblings first.
	dir := path
	if idx := strings.LastIndex(dir, "/"); idx >= 0 {
		dir = dir[:idx]
	} else {
		dir = ""
	}
	for _, file := range idx.Files {
		if file.Path == path {
			continue
		}
		fileDir := file.Path
		if idx := strings.LastIndex(fileDir, "/"); idx >= 0 {
			fileDir = fileDir[:idx]
		} else {
			fileDir = ""
		}
		if fileDir != dir {
			continue
		}
		if _, ok := seen[file.Path]; ok {
			continue
		}
		seen[file.Path] = struct{}{}
		out = append(out, file.Path)
	}

	// Follow any mod declarations in this file to find children.
	file := idx.FileByPath(path)
	if file != nil {
		for _, mod := range file.Modules {
			candidates := []string{
				dir + "/" + mod + ".rs",
				dir + "/" + mod + "/mod.rs",
			}
			for _, candidate := range candidates {
				candidate = strings.TrimPrefix(candidate, "/")
				if idx.FileByPath(candidate) != nil {
					if _, ok := seen[candidate]; !ok {
						seen[candidate] = struct{}{}
						out = append(out, candidate)
					}
				}
			}
		}
	}

	slices.Sort(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func scoreRustSymbolMatch(pattern string, file RustFile, symbol RustSymbol) int {
	nameLower := strings.ToLower(symbol.Name)
	qualifiedLower := strings.ToLower(firstRustQualified(symbol))
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
	case "method":
		score += 2
	case "trait":
		score += 2
	case "impl":
		// Slight deprioritize of impls in generic symbol search so
		// struct/enum/trait declarations surface first.
		score -= 2
	}
	return score
}

func firstRustQualified(symbol RustSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "::" + symbol.Name
	}
	return symbol.Name
}
