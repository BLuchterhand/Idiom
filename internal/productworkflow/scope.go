package productworkflow

import (
	"path"
	"slices"
	"strings"
)

type ScopeGateInput struct {
	ExpectedFiles      []string
	AllowedFiles       []string
	ForbiddenFiles     []string
	ForbiddenPatterns  []string
	ActualChangedPaths []string
}

type LedgerCoverageFinding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func ValidateLedgerCoverage(ledgerChangedPaths []string, commitChangedPaths []string) []LedgerCoverageFinding {
	var findings []LedgerCoverageFinding
	ledgerSet := make(map[string]struct{}, len(ledgerChangedPaths))
	for _, p := range ledgerChangedPaths {
		ledgerSet[normalizePath(p)] = struct{}{}
	}
	for _, p := range commitChangedPaths {
		norm := normalizePath(p)
		if _, ok := ledgerSet[norm]; !ok {
			findings = append(findings, LedgerCoverageFinding{
				Code:    "ledger_under_reports_path",
				Path:    norm,
				Message: "commit changed " + norm + " but implementation ledger does not record it",
			})
		}
	}
	return findings
}

type ScopeGateResult struct {
	Status             string
	Blockers           []ScopeGateBlocker
	ExpectedFiles      []string
	ActualChangedPaths []string
	MissingExpected    []string
	UnexpectedPaths    []string
	ForbiddenPaths     []string
}

type ScopeGateBlocker struct {
	Code    string
	Path    string
	Pattern string
	Message string
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

func normalizePaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		n := normalizePath(p)
		if n == "" || n == "." {
			continue
		}
		out = append(out, n)
	}
	return out
}

func globMatch(pattern, candidate string) bool {
	if pattern == "" || candidate == "" {
		return false
	}
	return globMatchSegments(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
}

func globMatchSegments(patternParts, pathParts []string) bool {
	for len(patternParts) > 0 {
		if patternParts[0] == "**" {
			patternParts = patternParts[1:]
			if len(patternParts) == 0 {
				return true
			}
			for i := range len(pathParts) + 1 {
				if globMatchSegments(patternParts, pathParts[i:]) {
					return true
				}
			}
			return false
		}
		if len(pathParts) == 0 {
			return false
		}
		if !segmentMatch(patternParts[0], pathParts[0]) {
			return false
		}
		patternParts = patternParts[1:]
		pathParts = pathParts[1:]
	}
	return len(pathParts) == 0
}

func segmentMatch(pattern, segment string) bool {
	px := 0
	sx := 0
	for px < len(pattern) {
		if pattern[px] == '*' {
			px++
			if px >= len(pattern) {
				return true
			}
			for sx <= len(segment) {
				if segmentMatch(pattern[px:], segment[sx:]) {
					return true
				}
				sx++
			}
			return false
		}
		if sx >= len(segment) {
			return false
		}
		if pattern[px] == '?' || pattern[px] == segment[sx] {
			px++
			sx++
			continue
		}
		return false
	}
	return sx == len(segment)
}

func EvaluateScopeGate(input ScopeGateInput) ScopeGateResult {
	expected := normalizePaths(input.ExpectedFiles)
	allowed := normalizePaths(input.AllowedFiles)
	forbidden := normalizePaths(input.ForbiddenFiles)
	patterns := normalizePaths(input.ForbiddenPatterns)
	actual := normalizePaths(input.ActualChangedPaths)

	slices.Sort(expected)
	slices.Sort(allowed)
	slices.Sort(forbidden)
	slices.Sort(patterns)
	slices.Sort(actual)
	expected = slices.Compact(expected)
	allowed = slices.Compact(allowed)
	forbidden = slices.Compact(forbidden)
	patterns = slices.Compact(patterns)
	actual = slices.Compact(actual)

	permittedSet := make(map[string]struct{}, len(expected)+len(allowed))
	for _, p := range expected {
		permittedSet[p] = struct{}{}
	}
	for _, p := range allowed {
		permittedSet[p] = struct{}{}
	}
	forbiddenSet := make(map[string]struct{}, len(forbidden))
	for _, p := range forbidden {
		forbiddenSet[p] = struct{}{}
	}

	var blockers []ScopeGateBlocker
	var forbiddenPaths []string
	var unexpectedPaths []string

	actualSet := make(map[string]struct{}, len(actual))
	for _, p := range actual {
		actualSet[p] = struct{}{}
		if _, isForbidden := forbiddenSet[p]; isForbidden {
			forbiddenPaths = append(forbiddenPaths, p)
			blockers = append(blockers, ScopeGateBlocker{
				Code:    "forbidden_path",
				Path:    p,
				Message: "forbidden path edited: " + p,
			})
			continue
		}
		matched := false
		for _, pat := range patterns {
			if globMatch(pat, p) {
				forbiddenPaths = append(forbiddenPaths, p)
				blockers = append(blockers, ScopeGateBlocker{
					Code:    "forbidden_pattern",
					Path:    p,
					Pattern: pat,
					Message: "path " + p + " matches forbidden pattern " + pat,
				})
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if _, isPermitted := permittedSet[p]; !isPermitted {
			unexpectedPaths = append(unexpectedPaths, p)
			blockers = append(blockers, ScopeGateBlocker{
				Code:    "unexpected_path",
				Path:    p,
				Message: "unexpected changed path: " + p,
			})
		}
	}

	var missingExpected []string
	for _, p := range expected {
		if _, found := actualSet[p]; !found {
			missingExpected = append(missingExpected, p)
			blockers = append(blockers, ScopeGateBlocker{
				Code:    "missing_expected_path",
				Path:    p,
				Message: "missing expected changed path: " + p,
			})
		}
	}

	slices.SortFunc(blockers, func(a, b ScopeGateBlocker) int {
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	slices.Sort(forbiddenPaths)
	slices.Sort(unexpectedPaths)
	slices.Sort(missingExpected)

	status := "pass"
	if len(blockers) > 0 {
		status = "blocked"
	}

	return ScopeGateResult{
		Status:             status,
		Blockers:           blockers,
		ExpectedFiles:      expected,
		ActualChangedPaths: actual,
		MissingExpected:    missingExpected,
		UnexpectedPaths:    unexpectedPaths,
		ForbiddenPaths:     forbiddenPaths,
	}
}
