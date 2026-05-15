package semantic

import (
	"regexp"
	"sort"
	"strings"
)

type SymbolMatch struct {
	Path      string
	Package   string
	Name      string
	Kind      string
	Receiver  string
	Line      int
	Column    int
	StartLine int
	EndLine   int
	Score     int
}

type PackageMatch struct {
	Path    string
	Name    string
	Files   int
	Funcs   int
	Methods int
	Types   int
	Score   int
}

func (idx *GoIndex) FindSymbols(pattern string, limit int) []SymbolMatch {
	pattern = strings.ToLower(normalizeGoSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}

	matches := make([]SymbolMatch, 0)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			score := scoreSymbolMatch(pattern, file, symbol)
			if score <= 0 {
				continue
			}
			matches = append(matches, SymbolMatch{
				Path:      file.Path,
				Package:   file.Package,
				Name:      symbol.Name,
				Kind:      symbol.Kind,
				Receiver:  symbol.Receiver,
				Line:      symbol.Line,
				Column:    symbol.Column,
				StartLine: symbol.StartLine,
				EndLine:   symbol.EndLine,
				Score:     score,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Name < matches[j].Name
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

var goSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^func\s*\([^)]*\)\s*([A-Za-z_]\w*)`),
	regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:const|var)\s+([A-Za-z_]\w*)`),
}

func normalizeGoSymbolPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range goSymbolPatternExtractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return pattern
}

func (idx *GoIndex) FindSymbolsByKind(pattern string, kind string, limit int) []SymbolMatch {
	matches := idx.FindSymbols(pattern, 0)
	if kind == "" {
		if limit > 0 && len(matches) > limit {
			return matches[:limit]
		}
		return matches
	}
	filtered := make([]SymbolMatch, 0, len(matches))
	for _, match := range matches {
		if match.Kind == kind {
			filtered = append(filtered, match)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

func (idx *GoIndex) FindPackages(pattern string, limit int) []PackageMatch {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	if idx == nil || pattern == "" {
		return nil
	}

	matches := make([]PackageMatch, 0)
	for path, pkg := range idx.Package {
		score := 0
		pathLower := strings.ToLower(path)
		nameLower := strings.ToLower(pkg.Name)
		switch {
		case pathLower == pattern || nameLower == pattern:
			score += 40
		case strings.Contains(pathLower, pattern):
			score += 20
		case strings.Contains(nameLower, pattern):
			score += 16
		default:
			continue
		}
		score += min(pkg.Funcs+pkg.Methods+pkg.Types, 20)
		matches = append(matches, PackageMatch{
			Path:    path,
			Name:    pkg.Name,
			Files:   pkg.Files,
			Funcs:   pkg.Funcs,
			Methods: pkg.Methods,
			Types:   pkg.Types,
			Score:   score,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Path < matches[j].Path
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

type CallMatch struct {
	Caller       SymbolMatch
	CalleeName   string
	CalleeTarget SymbolMatch
	Line         int
	Score        int
}

func (idx *GoIndex) FindCallsFrom(pattern string, limit int) []CallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}
	matches := make([]CallMatch, 0)
	for _, symbol := range symbols {
		source := idx.fileByPath(symbol.Path)
		if source == nil {
			continue
		}
		for _, fileSymbol := range source.Symbols {
			if fileSymbol.Name != symbol.Name || fileSymbol.Kind != symbol.Kind || fileSymbol.Line != symbol.Line {
				continue
			}
			for _, call := range fileSymbol.Calls {
				match := CallMatch{
					Caller:     symbol,
					CalleeName: qualifiedCallName(call),
					Line:       call.Line,
					Score:      symbol.Score,
				}
				if target := idx.bestSymbolForCall(call); target != nil {
					match.CalleeTarget = SymbolMatch{
						Path:      target.SourceFile,
						Package:   target.Package,
						Name:      target.Name,
						Kind:      target.Kind,
						Receiver:  target.Receiver,
						Line:      target.Line,
						Column:    target.Column,
						StartLine: target.StartLine,
						EndLine:   target.EndLine,
						Score:     0,
					}
				}
				matches = append(matches, match)
			}
		}
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func (idx *GoIndex) FindCallsInto(pattern string, limit int) []CallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	targetNames := make(map[string]SymbolMatch, len(targets))
	for _, target := range targets {
		targetNames[strings.ToLower(goQualifiedName(target.Receiver, target.Name))] = target
		targetNames[strings.ToLower(target.Name)] = target
	}

	matches := make([]CallMatch, 0)
	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			if len(symbol.Calls) == 0 {
				continue
			}
			for _, call := range symbol.Calls {
				target, ok := targetNames[strings.ToLower(qualifiedCallName(call))]
				if !ok {
					target, ok = targetNames[strings.ToLower(call.Name)]
				}
				if !ok {
					continue
				}
				matches = append(matches, CallMatch{
					Caller: SymbolMatch{
						Path:      file.Path,
						Package:   file.Package,
						Name:      symbol.Name,
						Kind:      symbol.Kind,
						Receiver:  symbol.Receiver,
						Line:      symbol.Line,
						Column:    symbol.Column,
						StartLine: symbol.StartLine,
						EndLine:   symbol.EndLine,
					},
					CalleeName:   qualifiedCallName(call),
					CalleeTarget: target,
					Line:         call.Line,
					Score:        target.Score,
				})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Caller.Path != matches[j].Caller.Path {
			return matches[i].Caller.Path < matches[j].Caller.Path
		}
		return matches[i].Line < matches[j].Line
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func scoreSymbolMatch(pattern string, file GoFile, symbol GoSymbol) int {
	nameLower := strings.ToLower(symbol.Name)
	receiverLower := strings.ToLower(symbol.Receiver)
	pathLower := strings.ToLower(file.Path)
	score := 0

	switch {
	case nameLower == pattern:
		score += 60
	case receiverLower != "" && receiverLower+"."+nameLower == pattern:
		score += 64
	case strings.Contains(nameLower, pattern):
		score += 32
	case receiverLower != "" && strings.Contains(receiverLower, pattern):
		score += 18
	case strings.Contains(pathLower, pattern):
		score += 12
	default:
		return 0
	}

	switch symbol.Kind {
	case "type":
		score += 10
	case "method":
		score += 8
	default:
		score += 6
	}
	score += sourceDirPriority(file.SourceDir)
	if file.IsTest {
		score -= 20
	}
	return score
}

func (idx *GoIndex) bestSymbolForCall(call GoCall) *GoSymbol {
	callNameLower := strings.ToLower(call.Name)
	qualifiedLower := strings.ToLower(qualifiedCallName(call))
	for _, file := range idx.Files {
		for i := range file.Symbols {
			symbol := &file.Symbols[i]
			if symbol.Kind != "func" && symbol.Kind != "method" {
				continue
			}
			if strings.ToLower(symbol.Name) == callNameLower {
				return symbol
			}
			if qualifiedLower != "" && strings.ToLower(goQualifiedName(symbol.Receiver, symbol.Name)) == qualifiedLower {
				return symbol
			}
		}
	}
	return nil
}

func qualifiedCallName(call GoCall) string {
	if call.Qualifier == "" {
		return call.Name
	}
	return call.Qualifier + "." + call.Name
}

func goQualifiedName(receiver, name string) string {
	if strings.TrimSpace(receiver) == "" {
		return name
	}
	return receiver + "." + name
}
