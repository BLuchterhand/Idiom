package implementationdispatch

import (
	"path"
	"sort"
	"strings"

	"idiom/internal/contracts/plan"
)

type DirtyAllowance struct {
	AllowAll bool
	Paths    []string
}

func PlanDocumentsWithDirtyAllowance(prompt string, docs []PlanDocument) []PlanDocument {
	allowance := DirtyAtomScopeAllowance(prompt)
	if !allowance.AllowAll && len(allowance.Paths) == 0 {
		return docs
	}

	out := append([]PlanDocument(nil), docs...)
	for i := range out {
		if allowance.AllowAll || documentMatchesAllowedDirtyPath(out[i], allowance.Paths) {
			out[i].AllowDirtyAtomScope = true
		}
	}

	return out
}

func DirtyAtomScopeAllowance(prompt string) DirtyAllowance {
	return DirtyAllowance{
		AllowAll: broadDirtyAtomScopeAllowance(prompt),
		Paths:    dirtyAllowancePaths(prompt),
	}
}

func broadDirtyAtomScopeAllowance(prompt string) bool {
	lower := strings.ToLower(prompt)
	markers := []string{
		"--allow-dirty-scope",
		"--allow-dirty",
		"allow-dirty-scope",
		"allow dirty atom scope",
		"allow all dirty scoped",
	}

	for _, marker := range markers {
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], marker)
			if idx < 0 {
				break
			}

			start := searchFrom + idx
			if broadDirtyAllowanceClauseIsAffirmative(lower, start, len(marker)) {
				return true
			}

			searchFrom = start + len(marker)
		}
	}

	return false
}

func broadDirtyAllowanceClauseIsAffirmative(lower string, markerStart, markerLen int) bool {
	clauseStart := 0
	for _, sep := range []string{"\n", "\r", ". ", "!", "?", ";"} {
		if idx := strings.LastIndex(lower[:markerStart], sep); idx >= 0 && idx+len(sep) > clauseStart {
			clauseStart = idx + len(sep)
		}
	}

	before := strings.TrimSpace(lower[clauseStart:markerStart])
	clauseEnd := len(lower)

	for _, sep := range []string{"\n", "\r", ". ", "!", "?", ";"} {
		if idx := strings.Index(lower[markerStart+markerLen:], sep); idx >= 0 && markerStart+markerLen+idx < clauseEnd {
			clauseEnd = markerStart + markerLen + idx
		}
	}

	clause := strings.TrimSpace(lower[clauseStart:clauseEnd])
	for _, negation := range dirtyAllowanceNegationMarkers() {
		if strings.Contains(before, negation) || strings.Contains(clause, negation) {
			return false
		}
	}

	if strings.Contains(before, ":") {
		return false
	}

	for _, affirmative := range []string{"explicitly", "allow", "with", "use", "using", "pass", "rerun", "run", "enable"} {
		if strings.Contains(before, affirmative) {
			return true
		}
	}

	return strings.Contains(clause, "allow dirty atom scope") || strings.Contains(clause, "allow all dirty scoped")
}

func dirtyAllowancePaths(prompt string) []string {
	lower := strings.ToLower(prompt)
	markers := []string{
		"explicitly allow the dirty scoped path",
		"explicitly allow dirty scoped path",
		"explicitly allow the dirty scoped paths",
		"explicitly allow dirty scoped paths",
		"allow the dirty scoped path",
		"allow dirty scoped path",
		"allow the dirty scoped paths",
		"allow dirty scoped paths",
		"allow dirty path",
		"allow-dirty path",
	}
	seen := make(map[string]struct{})

	var out []string

	for _, marker := range markers {
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], marker)
			if idx < 0 {
				break
			}

			markerStart := searchFrom + idx
			if !dirtyAllowanceMarkerContextAllows(lower, markerStart, len(marker)) {
				searchFrom = markerStart + len(marker)
				continue
			}

			start := markerStart + len(marker)
			segment := dirtyAllowanceClause(prompt[start:])

			for _, raw := range strings.Fields(segment) {
				token := strings.Trim(raw, "`'\"“”‘’.,;:()[]{}<>")

				token = strings.TrimSpace(token)

				if token == "" || strings.Contains(token, "://") {
					continue
				}

				normalized := plancontract.NormalizeWorkspacePath(token)
				if normalized == "" {
					continue
				}

				if !strings.Contains(normalized, "/") {
					continue
				}

				ext := strings.ToLower(path.Ext(normalized))
				if ext == "" || len(ext) > 8 {
					continue
				}

				if _, ok := seen[normalized]; ok {
					continue
				}

				seen[normalized] = struct{}{}
				out = append(out, normalized)
			}

			searchFrom = start
		}
	}

	sort.Strings(out)

	return out
}

func dirtyAllowanceMarkerContextAllows(lower string, markerStart, markerLen int) bool {
	clauseStart := 0
	for _, sep := range []string{"\n", "\r", ". ", "!", "?", ";"} {
		if idx := strings.LastIndex(lower[:markerStart], sep); idx >= 0 && idx+len(sep) > clauseStart {
			clauseStart = idx + len(sep)
		}
	}

	before := strings.TrimSpace(lower[clauseStart:markerStart])
	clauseEnd := len(lower)

	for _, sep := range []string{"\n", "\r", ". ", "!", "?", ";"} {
		if idx := strings.Index(lower[markerStart+markerLen:], sep); idx >= 0 && markerStart+markerLen+idx < clauseEnd {
			clauseEnd = markerStart + markerLen + idx
		}
	}

	clause := strings.TrimSpace(lower[clauseStart:clauseEnd])
	for _, negation := range dirtyAllowanceNegationMarkers() {
		if strings.Contains(before, negation) || strings.Contains(clause, negation) {
			return false
		}
	}

	return !strings.Contains(before, ":")
}

func dirtyAllowanceNegationMarkers() []string {
	return []string{"do not", "don't", "dont", "don’t", "cannot", "can't", "can’t", "cant", "without", "never", "not ", "avoid", "disallow"}
}

func dirtyAllowanceClause(text string) string {
	stop := len(text)
	for _, sep := range []string{"\n", "\r", ". ", "!", "?", ";"} {
		if idx := strings.Index(text, sep); idx >= 0 && idx < stop {
			stop = idx
		}
	}

	return text[:stop]
}

func documentMatchesAllowedDirtyPath(doc PlanDocument, allowedPaths []string) bool {
	if len(allowedPaths) == 0 {
		return false
	}

	scope := append([]string(nil), doc.EditPaths...)
	if strings.TrimSpace(doc.Path) != "" {
		scope = append(scope, doc.Path)
	}

	allowed := make(map[string]struct{}, len(allowedPaths))
	for _, path := range allowedPaths {
		if normalized := plancontract.NormalizeWorkspacePath(path); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}

	for _, path := range scope {
		normalized := plancontract.NormalizeWorkspacePath(path)
		if _, ok := allowed[normalized]; ok {
			return true
		}
	}

	return false
}
