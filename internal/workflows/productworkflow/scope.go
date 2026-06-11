package productworkflow

import (
	"slices"
	"strings"

	"idiom/internal/contracts/plan"
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

type AtomCoverageCheck struct {
	TaskID       string
	ChangedPaths []string
}

func ValidateLedgerCoverage(atoms []AtomCoverageCheck, commitChangedPaths []string, effortRootPrefix string) []LedgerCoverageFinding {
	var findings []LedgerCoverageFinding

	unionSet := make(map[string]struct{})
	for _, atom := range atoms {
		if len(atom.ChangedPaths) == 0 {
			findings = append(findings, LedgerCoverageFinding{
				Code:    "atom_empty_changed_paths",
				Path:    "",
				Message: "atom " + atom.TaskID + " has empty changed_paths",
			})
			continue
		}
		for _, p := range atom.ChangedPaths {
			unionSet[normalizePath(p)] = struct{}{}
		}
	}

	effortPrefix := normalizePath(effortRootPrefix)

	for _, p := range commitChangedPaths {
		norm := normalizePath(p)
		relevant := (effortPrefix != "" && strings.HasPrefix(norm, effortPrefix+"/")) ||
			(effortPrefix != "" && norm == effortPrefix)
		if relevant {
			if _, ok := unionSet[norm]; !ok {
				findings = append(findings, LedgerCoverageFinding{
					Code:    "ledger_under_reports_path",
					Path:    norm,
					Message: "commit changed " + norm + " but no atom in the implementation ledger records it",
				})
			}
		}
	}

	return findings
}

type AtomCommitCoverage struct {
	TaskID       string
	CommitHash   string
	ChangedPaths []string
}

func ValidatePerAtomCoverage(atoms []AtomCommitCoverage, commitPaths map[string][]string) []LedgerCoverageFinding {
	var findings []LedgerCoverageFinding
	for _, atom := range atoms {
		if atom.CommitHash == "" {
			findings = append(findings, LedgerCoverageFinding{
				Code:    "commit_hash_missing",
				Path:    "",
				Message: "atom " + atom.TaskID + " has no commit_hash; commit_hash is required for implementation_completed, fallback_completion, and partial_implementation_recorded records",
			})
			continue
		}
		paths, ok := commitPaths[atom.CommitHash]
		if !ok {
			findings = append(findings, LedgerCoverageFinding{
				Code:    "commit_hash_not_found",
				Path:    "",
				Message: "atom " + atom.TaskID + " references commit " + atom.CommitHash + " not found in commit history",
			})
			continue
		}
		atomSet := make(map[string]struct{})
		for _, p := range atom.ChangedPaths {
			atomSet[normalizePath(p)] = struct{}{}
		}
		for _, p := range paths {
			if _, ok := atomSet[normalizePath(p)]; !ok {
				findings = append(findings, LedgerCoverageFinding{
					Code:    "atom_under_reports_path",
					Path:    normalizePath(p),
					Message: "commit " + atom.CommitHash + " changed " + normalizePath(p) + " but atom " + atom.TaskID + " does not list it",
				})
			}
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
	p = plancontract.NormalizeWorkspacePath(p)
	p = normalizeMovedSourcePath(p)
	return p
}

func normalizeMovedSourcePath(p string) string {
	switch p {
	case "cmd/idiom/product_workflow.go":
		return "internal/surfaces/cli/product/product_workflow.go"
	case "cmd/idiom/product_workflow_test.go":
		return "internal/surfaces/cli/product/product_workflow_test.go"
	}
	for _, moved := range []struct {
		old string
		new string
	}{
		{old: "internal/productworkflow/", new: "internal/workflows/productworkflow/"},
		{old: "internal/cmd/product/", new: "internal/surfaces/cli/product/"},
	} {
		if strings.HasPrefix(p, moved.old) {
			return moved.new + strings.TrimPrefix(p, moved.old)
		}
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

	status := ProductWorkflowStatusPass
	if len(blockers) > 0 {
		status = ProductWorkflowStatusBlocked
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
