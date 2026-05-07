package semantic

import (
	"regexp"
	"sort"
	"strings"
)

type JavaSymbolMatch struct {
	Path        string
	Name        string
	Qualified   string
	Kind        string
	Scope       string
	Modifiers   []string
	Annotations []string
	Extends     string
	Implements  []string
	Line        int
	Column      int
	StartLine   int
	EndLine     int
	Score       int
}

type JavaCallMatch struct {
	Caller     JavaSymbolMatch
	CalleeName string
	Line       int
	Score      int
}

// FindSymbols returns up to `limit` symbols whose name, qualified name,
// or path contains `pattern`. Ranking is identical in spirit to the
// C++ scorer: exact-name wins big, contains-qualified next, etc.
func (idx *JavaIndex) FindSymbols(pattern string, limit int) []JavaSymbolMatch {
	pattern = strings.ToLower(normalizeJavaSymbolPattern(pattern))
	if idx == nil || pattern == "" {
		return nil
	}
	matches := make([]JavaSymbolMatch, 0)
	for _, file := range idx.Files {
		for _, sym := range file.Symbols {
			score := scoreJavaSymbolMatch(pattern, file, sym)
			if score <= 0 {
				continue
			}
			matches = append(matches, JavaSymbolMatch{
				Path:        file.Path,
				Name:        sym.Name,
				Qualified:   firstJavaQualified(sym),
				Kind:        sym.Kind,
				Scope:       sym.Scope,
				Modifiers:   sym.Modifiers,
				Annotations: sym.Annotations,
				Extends:     sym.Extends,
				Implements:  sym.Implements,
				Line:        sym.Line,
				Column:      sym.Column,
				StartLine:   sym.StartLine,
				EndLine:     sym.EndLine,
				Score:       score,
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
		return matches[i].Line < matches[j].Line
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

var javaSymbolPatternExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*class\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*interface\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*enum\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*record\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*@interface\s+([A-Za-z_]\w*)`),
}

func normalizeJavaSymbolPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	for _, re := range javaSymbolPatternExtractors {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return pattern
}

// FindCallsFrom returns outgoing call sites from methods matching
// `pattern` — direct mirror of FindCallsFrom on CppIndex.
func (idx *JavaIndex) FindCallsFrom(pattern string, limit int) []JavaCallMatch {
	symbols := idx.FindSymbols(pattern, 4)
	if len(symbols) == 0 {
		return nil
	}
	out := make([]JavaCallMatch, 0, 16)
	for _, match := range symbols {
		file := idx.FileByPath(match.Path)
		if file == nil {
			continue
		}
		for _, sym := range file.Symbols {
			if sym.Line != match.Line || sym.Name != match.Name {
				continue
			}
			for _, call := range sym.Calls {
				out = append(out, JavaCallMatch{
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

// FindCallsInto returns call sites that invoke something matching
// `pattern`. This is intentionally approximate — it matches by name,
// not by resolved target.
func (idx *JavaIndex) FindCallsInto(pattern string, limit int) []JavaCallMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	names := make(map[string]JavaSymbolMatch, len(targets)*2)
	for _, target := range targets {
		names[strings.ToLower(target.Name)] = target
		if target.Qualified != "" {
			names[strings.ToLower(target.Qualified)] = target
		}
	}
	out := make([]JavaCallMatch, 0, 16)
	for _, file := range idx.Files {
		for _, sym := range file.Symbols {
			for _, call := range sym.Calls {
				target, ok := names[strings.ToLower(call.Name)]
				if !ok {
					continue
				}
				out = append(out, JavaCallMatch{
					Caller: JavaSymbolMatch{
						Path:      file.Path,
						Name:      sym.Name,
						Qualified: firstJavaQualified(sym),
						Kind:      sym.Kind,
						Scope:     sym.Scope,
						Line:      sym.Line,
						Column:    sym.Column,
						StartLine: sym.StartLine,
						EndLine:   sym.EndLine,
					},
					CalleeName: firstNonEmptyJava(target.Qualified, target.Name),
					Line:       call.Line,
					Score:      target.Score,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Caller.Path != out[j].Caller.Path {
			return out[i].Caller.Path < out[j].Caller.Path
		}
		return out[i].Line < out[j].Line
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Implementations returns files that contain concrete classes
// extending/implementing any of the interfaces/abstract classes
// matching `pattern`.
func (idx *JavaIndex) Implementations(pattern string, limit int) []JavaSymbolMatch {
	targets := idx.FindSymbols(pattern, 8)
	if len(targets) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(targets)*2)
	for _, target := range targets {
		names[target.Name] = struct{}{}
		if target.Qualified != "" {
			names[target.Qualified] = struct{}{}
		}
	}
	out := make([]JavaSymbolMatch, 0)
	for _, file := range idx.Files {
		for _, sym := range file.Symbols {
			if sym.Kind != "class" && sym.Kind != "record" && sym.Kind != "enum" {
				continue
			}
			hit := false
			if sym.Extends != "" {
				if _, ok := names[sym.Extends]; ok {
					hit = true
				}
			}
			if !hit {
				for _, impl := range sym.Implements {
					if _, ok := names[impl]; ok {
						hit = true
						break
					}
				}
			}
			if !hit {
				continue
			}
			out = append(out, JavaSymbolMatch{
				Path:      file.Path,
				Name:      sym.Name,
				Qualified: firstJavaQualified(sym),
				Kind:      sym.Kind,
				Scope:     sym.Scope,
				Line:      sym.Line,
				Column:    sym.Column,
				StartLine: sym.StartLine,
				EndLine:   sym.EndLine,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Dependencies returns files that `pattern`'s files (transitively)
// import from, following in-workspace FQN edges only.
func (idx *JavaIndex) Dependencies(pattern string) []string {
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
	sort.Strings(out)
	return out
}

// WhoImports is the reverse of Dependencies.
func (idx *JavaIndex) WhoImports(pattern string) []string {
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
	sort.Strings(out)
	return out
}

func scoreJavaSymbolMatch(pattern string, file JavaFile, sym JavaSymbol) int {
	nameLower := strings.ToLower(sym.Name)
	qualifiedLower := strings.ToLower(firstJavaQualified(sym))
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
	switch sym.Kind {
	case "class", "interface", "record", "enum":
		score += 2
	case "method", "constructor":
		score += 1
	}
	return score
}

func firstJavaQualified(sym JavaSymbol) string {
	if strings.TrimSpace(sym.Qualified) != "" {
		return sym.Qualified
	}
	if strings.TrimSpace(sym.Scope) != "" {
		return sym.Scope + "." + sym.Name
	}
	return sym.Name
}

func firstNonEmptyJava(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
