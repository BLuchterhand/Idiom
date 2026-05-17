package productworkflow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestScopeGatePassesWhenActualMatchesExpected(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles:    []string{"internal/productworkflow/transition.go"},
		ForbiddenPatterns: []string{"cmd/**"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass, got %q with blockers %+v", result.Status, result.Blockers)
	}
	if len(result.Blockers) != 0 {
		t.Fatalf("expected zero blockers, got %d", len(result.Blockers))
	}
}

func TestScopeGatePassesWithAllowedDurableBookkeepingPaths(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"internal/productworkflow/scope.go"},
		AllowedFiles:  []string{"reports/impl-0016-implementation.json"},
		ForbiddenFiles: []string{
			"internal/productworkflow/transition.go",
			"cmd/idiom/product_workflow.go",
		},
		ForbiddenPatterns: []string{"docs/**"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"reports/impl-0016-implementation.json",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass, got %q with blockers %+v", result.Status, result.Blockers)
	}
	if len(result.MissingExpected) != 0 {
		t.Fatalf("expected no missing, got %v", result.MissingExpected)
	}
	if len(result.UnexpectedPaths) != 0 {
		t.Fatalf("expected no unexpected, got %v", result.UnexpectedPaths)
	}
}

func TestScopeGateAllowedFilesBlockForbiddenSpillover(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"internal/productworkflow/scope.go"},
		AllowedFiles:  []string{"reports/impl-0016-implementation.json"},
		ForbiddenFiles: []string{
			"internal/productworkflow/transition.go",
		},
		ForbiddenPatterns: []string{"docs/**"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"internal/productworkflow/transition.go",
			"reports/impl-0016-implementation.json",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_path" && b.Path == "internal/productworkflow/transition.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected forbidden_path blocker for transition.go, got %+v", result.Blockers)
	}
}

func TestScopeGateForbiddenExactPathProducesBlocker(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles: []string{
			"internal/productworkflow/types.go",
			"cmd/idiom/product_workflow.go",
		},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"internal/productworkflow/types.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	if len(result.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}
	b := result.Blockers[0]
	if b.Code != "forbidden_path" {
		t.Fatalf("expected blocker code forbidden_path, got %q", b.Code)
	}
	if b.Path != "internal/productworkflow/types.go" {
		t.Fatalf("expected blocker path internal/productworkflow/types.go, got %q", b.Path)
	}
}

func TestScopeGateForbiddenPatternProducesBlocker(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles: []string{
			"internal/productworkflow/transition.go",
		},
		ForbiddenPatterns: []string{"cmd/**", "docs/**/*.md"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"cmd/idiom/main.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_pattern" {
			found = true
			if b.Path != "cmd/idiom/main.go" {
				t.Fatalf("expected blocker path cmd/idiom/main.go, got %q", b.Path)
			}
			if b.Pattern != "cmd/**" {
				t.Fatalf("expected blocker pattern cmd/**, got %q", b.Pattern)
			}
		}
	}
	if !found {
		t.Fatalf("expected forbidden_pattern blocker, got %+v", result.Blockers)
	}
}

func TestScopeGateUnexpectedPathProducesBlocker(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles: []string{
			"internal/productworkflow/transition.go",
		},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"internal/productworkflow/surprise.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "unexpected_path" && b.Path == "internal/productworkflow/surprise.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unexpected_path blocker for surprise.go, got %+v", result.Blockers)
	}
}

func TestScopeGateMissingExpectedPathProducesBlocker(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{
			"internal/productworkflow/scope.go",
			"internal/productworkflow/scope_test.go",
		},
		ForbiddenFiles: []string{"internal/productworkflow/transition.go"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "missing_expected_path" && b.Path == "internal/productworkflow/scope_test.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_expected_path blocker for scope_test.go, got %+v", result.Blockers)
	}
}

func TestScopeGateNormalizedPathVariants(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:  []string{"./internal/productworkflow/scope.go"},
		ForbiddenFiles: []string{".\\internal\\productworkflow\\types.go"},
		ActualChangedPaths: []string{
			"internal/productworkflow/./scope.go",
			"internal/productworkflow/types.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	foundForbidden := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_path" {
			foundForbidden = true
			if b.Path != "internal/productworkflow/types.go" {
				t.Fatalf("expected normalized path internal/productworkflow/types.go, got %q", b.Path)
			}
		}
	}
	if !foundForbidden {
		t.Fatalf("expected forbidden_path blocker after normalization, got %+v", result.Blockers)
	}
	if len(result.MissingExpected) != 0 {
		t.Fatalf("expected no missing expected after normalization, got %v", result.MissingExpected)
	}
}

func TestScopeGateBackslashNormalization(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles:     []string{"internal/productworkflow/transition.go"},
		ActualChangedPaths: []string{"internal\\productworkflow\\scope.go"},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass after backslash normalization, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateDuplicateSlashesNormalized(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles:     []string{"internal/productworkflow/transition.go"},
		ActualChangedPaths: []string{"internal//productworkflow///scope.go"},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass after duplicate slash normalization, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateDotSegmentNormalization(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles:     []string{"internal/productworkflow/transition.go"},
		ActualChangedPaths: []string{"./internal/./productworkflow/scope.go"},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass after dot segment normalization, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateEmptyPathsSkipped(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{"internal/productworkflow/scope.go"},
		ForbiddenFiles:     []string{"internal/productworkflow/transition.go"},
		ActualChangedPaths: []string{"", "  ", "internal/productworkflow/scope.go"},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass with empty paths skipped, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateResultSlicesStableSorted(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"b.go", "a.go"},
		ForbiddenFiles: []string{
			"forbidden_b.go",
			"forbidden_a.go",
		},
		ActualChangedPaths: []string{
			"a.go",
			"z1.go",
			"z2.go",
		},
	}
	result := EvaluateScopeGate(input)

	if !slices.IsSorted(result.ExpectedFiles) {
		t.Fatalf("ExpectedFiles not sorted: %v", result.ExpectedFiles)
	}
	if !slices.IsSorted(result.ActualChangedPaths) {
		t.Fatalf("ActualChangedPaths not sorted: %v", result.ActualChangedPaths)
	}
	if !slices.IsSorted(result.MissingExpected) {
		t.Fatalf("MissingExpected not sorted: %v", result.MissingExpected)
	}
	if !slices.IsSorted(result.UnexpectedPaths) {
		t.Fatalf("UnexpectedPaths not sorted: %v", result.UnexpectedPaths)
	}
	if !slices.IsSorted(result.ForbiddenPaths) {
		t.Fatalf("ForbiddenPaths not sorted: %v", result.ForbiddenPaths)
	}

	prevCode := ""
	prevPath := ""
	for _, b := range result.Blockers {
		if b.Code < prevCode {
			t.Fatalf("Blocker codes not sorted: %q < %q", b.Code, prevCode)
		}
		if b.Code == prevCode && b.Path < prevPath {
			t.Fatalf("Blocker paths not sorted within code group: %q < %q", b.Path, prevPath)
		}
		prevCode = b.Code
		prevPath = b.Path
	}
}

func TestScopeGateBlockerCodesAreSorted(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{"a.go"},
		ForbiddenFiles: []string{
			"forbidden.go",
		},
		ForbiddenPatterns: []string{"pat/**"},
		ActualChangedPaths: []string{
			"forbidden.go",
			"pat/sub.go",
			"unexpected.go",
		},
	}
	result := EvaluateScopeGate(input)

	codes := make([]string, 0, len(result.Blockers))
	for _, b := range result.Blockers {
		codes = append(codes, b.Code)
	}
	if !slices.IsSorted(codes) {
		t.Fatalf("Blocker codes not sorted: %v", codes)
	}
}

func TestScopeGateMultipleBlockers(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"a.go", "b.go"},
		ForbiddenFiles:    []string{"f1.go"},
		ForbiddenPatterns: []string{"pat/**"},
		ActualChangedPaths: []string{
			"a.go",
			"f1.go",
			"pat/x.go",
			"unexpected.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}

	codeSet := map[string]bool{}
	for _, b := range result.Blockers {
		codeSet[b.Code] = true
	}
	for _, want := range []string{"forbidden_path", "forbidden_pattern", "unexpected_path", "missing_expected_path"} {
		if !codeSet[want] {
			t.Fatalf("expected blocker code %q, got codes %v", want, codeSet)
		}
	}

	if len(result.MissingExpected) != 1 || result.MissingExpected[0] != "b.go" {
		t.Fatalf("expected missing [b.go], got %v", result.MissingExpected)
	}
	if len(result.UnexpectedPaths) != 1 || result.UnexpectedPaths[0] != "unexpected.go" {
		t.Fatalf("expected unexpected [unexpected.go], got %v", result.UnexpectedPaths)
	}
	if len(result.ForbiddenPaths) != 2 {
		t.Fatalf("expected 2 forbidden paths, got %v", result.ForbiddenPaths)
	}
}

func TestScopeGatePatternMatchesDeepPath(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"a.go"},
		ForbiddenPatterns: []string{"cmd/**"},
		ActualChangedPaths: []string{
			"a.go",
			"cmd/idiom/sub/deep.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked for deep forbidden pattern match, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_pattern" && b.Path == "cmd/idiom/sub/deep.go" && b.Pattern == "cmd/**" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected forbidden_pattern match for deep path, got %+v", result.Blockers)
	}
}

func TestScopeGatePatternMatchesSingleSegment(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"a.go"},
		ForbiddenPatterns: []string{"*.test.go"},
		ActualChangedPaths: []string{
			"a.go",
			"foo.test.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked for glob *.test.go, got %q", result.Status)
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_pattern" && b.Pattern == "*.test.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected forbidden_pattern for *.test.go, got %+v", result.Blockers)
	}
}

func TestScopeGatePatternNoMatchPasses(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"a.go"},
		ForbiddenPatterns: []string{"cmd/**"},
		ActualChangedPaths: []string{
			"a.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass when no pattern matches, got %q", result.Status)
	}
}

func TestScopeGateExpectedAsAllowed(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{"a.go", "b.go"},
		ForbiddenFiles:     []string{"f.go"},
		ActualChangedPaths: []string{"a.go", "b.go"},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass when all expected present, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateReportPathsAllowedAsExpected(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles: []string{
			"internal/productworkflow/scope.go",
			"reports/impl-0016-implementation.json",
		},
		ForbiddenFiles:    []string{"internal/productworkflow/transition.go"},
		ForbiddenPatterns: []string{"docs/**"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"reports/impl-0016-implementation.json",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass with report path as expected, got %q with blockers %+v", result.Status, result.Blockers)
	}
}

func TestScopeGateDurableBookkeepingAllowedBlocksForbiddenSpillover(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"internal/productworkflow/scope.go"},
		AllowedFiles:      []string{"reports/impl.json", "tdd.json"},
		ForbiddenFiles:    []string{"internal/productworkflow/types.go"},
		ForbiddenPatterns: []string{"internal/agent/**"},
		ActualChangedPaths: []string{
			"internal/productworkflow/scope.go",
			"reports/impl.json",
			"tdd.json",
			"internal/agent/runner.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", result.Status)
	}
	foundPattern := false
	for _, b := range result.Blockers {
		if b.Code == "forbidden_pattern" && b.Path == "internal/agent/runner.go" {
			foundPattern = true
		}
	}
	if !foundPattern {
		t.Fatalf("expected forbidden_pattern for internal/agent/runner.go, got %+v", result.Blockers)
	}
}

func TestScopeGateEmptyInputPasses(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:      []string{},
		ForbiddenFiles:     []string{},
		ActualChangedPaths: []string{},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "pass" {
		t.Fatalf("expected pass for empty input, got %q", result.Status)
	}
}

func TestScopeGateForbiddenPatternMatchesExactPrefix(t *testing.T) {
	input := ScopeGateInput{
		ExpectedFiles:     []string{"a.go"},
		ForbiddenPatterns: []string{"internal/agent/**"},
		ActualChangedPaths: []string{
			"a.go",
			"internal/agent/session.go",
		},
	}
	result := EvaluateScopeGate(input)
	if result.Status != "blocked" {
		t.Fatalf("expected blocked for agent path, got %q", result.Status)
	}
}

func TestScopeGateSourceSanityPurity(t *testing.T) {
	root := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal/productworkflow/scope.go"))
	if err != nil {
		t.Fatalf("read scope.go: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"os.",
		"exec.",
		"git",
		"telemetry",
		"flag.",
		"command-line",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("scope.go contains forbidden string %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		`"os"`,
		`"os/"`,
		`"exec"`,
		`"flag"`,
		`"runtime"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("scope.go imports forbidden package %s", forbidden)
		}
	}
}
