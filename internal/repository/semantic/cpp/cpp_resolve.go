package cpp

import (
	"cmp"
	"slices"
	"strings"
)

// Resolution statuses reported by ResolveSymbol.
const (
	ResolutionResolved  = "resolved"
	ResolutionAmbiguous = "ambiguous"
	ResolutionNotFound  = "not_found"
)

// CppResolution is the exact answer to "which symbol did the caller mean".
// Unlike FindSymbols it never guesses: a name shared by several owners is
// reported as ambiguous and a name that matches nothing exactly is not_found.
type CppResolution struct {
	Requested string
	Status    string
	// Owner is the qualified entity (class, namespace member or free function)
	// the request resolved to. Constructors resolve to their class.
	Owner string
	// Name is the unqualified symbol name call sites use.
	Name string
	// Definitions lists every indexed declaration or definition of Owner.
	Definitions []CppSymbolMatch
	// Candidates holds one representative per owner when ambiguous, or the
	// nearest fuzzy matches when not found.
	Candidates []CppSymbolMatch
}

// ResolveSymbol maps a requested name or qualified name onto exactly one owner.
// A non-empty pathFilter restricts candidates to paths under that prefix.
func (idx *CppIndex) ResolveSymbol(pattern string, pathFilter string) CppResolution {
	resolution := CppResolution{Requested: strings.TrimSpace(pattern), Status: ResolutionNotFound}
	if idx == nil || resolution.Requested == "" {
		return resolution
	}

	normalized := normalizeCppSymbolPattern(resolution.Requested)
	name := normalized
	qualified := false

	if cut := strings.LastIndex(normalized, "::"); cut >= 0 {
		name = normalized[cut+2:]
		qualified = true
	}

	resolution.Name = name

	groups := idx.exactOwnerGroups(normalized, name, qualified, pathFilter)
	if len(groups) == 0 {
		resolution.Candidates = idx.nearestOwners(normalized, pathFilter, 5)

		return resolution
	}

	// Entries with no scope are in-class declarations or free declarations whose
	// owner the syntax index cannot see; they never create ambiguity by themselves.
	if len(groups) > 1 {
		if bare, ok := groups[name]; ok && !qualified {
			scoped := false

			for owner := range groups {
				if owner != name && !isTypeOwner(groups[owner]) {
					scoped = true
				}
			}

			if scoped && !isTypeOwner(bare) {
				delete(groups, name)
			}
		}
	}

	owners := make([]string, 0, len(groups))
	for owner := range groups {
		owners = append(owners, owner)
	}

	slices.Sort(owners)

	if len(owners) > 1 {
		resolution.Status = ResolutionAmbiguous

		for _, owner := range owners {
			resolution.Candidates = append(resolution.Candidates, groups[owner][0])
		}

		return resolution
	}

	resolution.Status = ResolutionResolved
	resolution.Owner = owners[0]
	resolution.Definitions = groups[owners[0]]

	return resolution
}

// CallsIntoName returns call sites whose callee is exactly name, plus the total
// found before limit truncation. The syntax index records bare callee names, so
// receiver types are not distinguished.
func (idx *CppIndex) CallsIntoName(name string, limit int) ([]CppCallMatch, int) {
	if idx == nil || name == "" {
		return nil, 0
	}

	out := make([]CppCallMatch, 0, 16)

	for _, file := range idx.Files {
		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				if call.Name != name {
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
					CalleeName: name,
					Line:       call.Line,
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

	total := len(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out, total
}

func (idx *CppIndex) exactOwnerGroups(normalized string, name string, qualified bool, pathFilter string) map[string][]CppSymbolMatch {
	groups := make(map[string][]CppSymbolMatch)

	for _, file := range idx.Files {
		if pathFilter != "" && !strings.HasPrefix(file.Path, pathFilter) {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Name != name {
				continue
			}

			key := cppOwnerKey(symbol)
			if qualified && key != normalized && !strings.HasSuffix(key, "::"+normalized) {
				continue
			}

			groups[key] = append(groups[key], symbolMatch(file.Path, symbol))
		}
	}

	return groups
}

func (idx *CppIndex) nearestOwners(normalized string, pathFilter string, limit int) []CppSymbolMatch {
	out := make([]CppSymbolMatch, 0, limit)
	seen := make(map[string]struct{}, limit)

	for _, match := range idx.FindSymbols(normalized, 40) {
		if pathFilter != "" && !strings.HasPrefix(match.Path, pathFilter) {
			continue
		}

		key := cppOwnerKey(CppSymbol{Name: match.Name, Qualified: match.Qualified, Scope: match.Scope})
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, match)
		if len(out) >= limit {
			break
		}
	}

	return out
}

// cppOwnerKey identifies the entity a symbol belongs to. A constructor shares
// its class's key so a class and its constructors never read as two owners.
func cppOwnerKey(symbol CppSymbol) string {
	scope := strings.TrimSpace(symbol.Scope)
	if scope != "" {
		last := scope
		if cut := strings.LastIndex(scope, "::"); cut >= 0 {
			last = scope[cut+2:]
		}

		if symbol.Name == last {
			return scope
		}
	}

	return firstCppQualified(symbol)
}

func isTypeOwner(group []CppSymbolMatch) bool {
	for _, match := range group {
		switch match.Kind {
		case "class", "struct", "enum", "union", "namespace":
			return true
		}
	}

	return false
}

func symbolMatch(path string, symbol CppSymbol) CppSymbolMatch {
	return CppSymbolMatch{
		Path:      path,
		Name:      symbol.Name,
		Qualified: firstCppQualified(symbol),
		Kind:      symbol.Kind,
		Scope:     symbol.Scope,
		Line:      symbol.Line,
		Column:    symbol.Column,
		StartLine: symbol.StartLine,
		EndLine:   symbol.EndLine,
	}
}
