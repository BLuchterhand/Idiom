package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("scope", func() {
	ginkgo.It("ScopeGatePassesWhenActualMatchesExpected", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles:    []string{"internal/workflows/productworkflow/transition.go"},
			ForbiddenPatterns: []string{"cmd/**"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q with blockers %+v", result.Status, result.Blockers))
		gomega.Expect(len(result.Blockers) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero blockers, got %d", len(result.Blockers)))

	})
	ginkgo.It("ScopeGatePassesWithAllowedDurableBookkeepingPaths", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{"internal/workflows/productworkflow/scope.go"},
			AllowedFiles:  []string{"reports/impl-0016-implementation.json"},
			ForbiddenFiles: []string{
				"internal/workflows/productworkflow/transition.go",
				"cmd/idiom/product_workflow.go",
			},
			ForbiddenPatterns: []string{"docs/**"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"reports/impl-0016-implementation.json",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q with blockers %+v", result.Status, result.Blockers))
		gomega.Expect(len(result.MissingExpected) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no missing, got %v", result.MissingExpected))
		gomega.Expect(len(result.UnexpectedPaths) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no unexpected, got %v", result.UnexpectedPaths))

	})
	ginkgo.It("ScopeGateAllowedFilesBlockForbiddenSpillover", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{"internal/workflows/productworkflow/scope.go"},
			AllowedFiles:  []string{"reports/impl-0016-implementation.json"},
			ForbiddenFiles: []string{
				"internal/workflows/productworkflow/transition.go",
			},
			ForbiddenPatterns: []string{"docs/**"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"internal/workflows/productworkflow/transition.go",
				"reports/impl-0016-implementation.json",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_path" && b.Path == "internal/workflows/productworkflow/transition.go" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_path blocker for transition.go, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGateForbiddenExactPathProducesBlocker", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles: []string{
				"internal/workflows/productworkflow/types.go",
				"cmd/idiom/product_workflow.go",
			},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"internal/workflows/productworkflow/types.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected at least one blocker"))

		b := result.Blockers[0]
		gomega.Expect(b.Code != "forbidden_path").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code forbidden_path, got %q", b.Code))
		gomega.Expect(b.Path != "internal/workflows/productworkflow/types.go").To(gomega.BeFalse(), fmt.Sprintf("expected blocker path internal/workflows/productworkflow/types.go, got %q", b.Path))

	})
	ginkgo.It("ScopeGateForbiddenPatternProducesBlocker", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles: []string{
				"internal/workflows/productworkflow/transition.go",
			},
			ForbiddenPatterns: []string{"cmd/**", "docs/**/*.md"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"cmd/idiom/main.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_pattern" {
				found = true
				gomega.Expect(b.Path != "cmd/idiom/main.go").To(gomega.BeFalse(), fmt.Sprintf("expected blocker path cmd/idiom/main.go, got %q", b.Path))
				gomega.Expect(b.Pattern != "cmd/**").To(gomega.BeFalse(), fmt.Sprintf("expected blocker pattern cmd/**, got %q", b.Pattern))

			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_pattern blocker, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGateUnexpectedPathProducesBlocker", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles: []string{
				"internal/workflows/productworkflow/transition.go",
			},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"internal/workflows/productworkflow/surprise.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "unexpected_path" && b.Path == "internal/workflows/productworkflow/surprise.go" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected unexpected_path blocker for surprise.go, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGateMissingExpectedPathProducesBlocker", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{
				"internal/workflows/productworkflow/scope.go",
				"internal/workflows/productworkflow/scope_test.go",
			},
			ForbiddenFiles: []string{"internal/workflows/productworkflow/transition.go"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "missing_expected_path" && b.Path == "internal/workflows/productworkflow/scope_test.go" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected missing_expected_path blocker for scope_test.go, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGateNormalizedPathVariants", func() {
		input := ScopeGateInput{
			ExpectedFiles:  []string{"./internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles: []string{".\\internal\\workflows\\productworkflow\\types.go"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/./scope.go",
				"internal/workflows/productworkflow/types.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		foundForbidden := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_path" {
				foundForbidden = true
				gomega.Expect(b.Path != "internal/workflows/productworkflow/types.go").To(gomega.BeFalse(), fmt.Sprintf("expected normalized path internal/workflows/productworkflow/types.go, got %q", b.Path))

			}
		}
		gomega.Expect(!foundForbidden).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_path blocker after normalization, got %+v", result.Blockers))
		gomega.Expect(len(result.MissingExpected) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no missing expected after normalization, got %v", result.MissingExpected))

	})
	ginkgo.It("ScopeGateNormalizesMovedProductWorkflowPaths", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{
				"internal/surfaces/cli/product/product_workflow.go",
				"internal/workflows/productworkflow/scope.go",
			},
			ActualChangedPaths: []string{
				"cmd/idiom/product_workflow.go",
				"internal/productworkflow/scope.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusPass), fmt.Sprintf("expected moved product workflow paths to match, got %#v", result))
		gomega.Expect(result.Blockers).To(gomega.BeEmpty(), fmt.Sprintf("expected moved product workflow paths to match, got %#v", result))

	})
	ginkgo.It("PerAtomCoverageNormalizesMovedProductWorkflowPaths", func() {
		findings := ValidatePerAtomCoverage([]AtomCommitCoverage{{
			TaskID:     "impl-move",
			CommitHash: "abc123",
			ChangedPaths: []string{
				"internal/surfaces/cli/product/product_workflow.go",
				"internal/workflows/productworkflow/scope.go",
			},
		}}, map[string][]string{
			"abc123": {
				"cmd/idiom/product_workflow.go",
				"internal/productworkflow/scope.go",
			},
		})
		gomega.Expect(len(findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected moved product workflow paths to satisfy per-atom coverage, got %#v", findings))

	})
	ginkgo.It("ScopeGateBackslashNormalization", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles:     []string{"internal/workflows/productworkflow/transition.go"},
			ActualChangedPaths: []string{"internal\\workflows\\productworkflow\\scope.go"},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass after backslash normalization, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateDuplicateSlashesNormalized", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles:     []string{"internal/workflows/productworkflow/transition.go"},
			ActualChangedPaths: []string{"internal//workflows//productworkflow///scope.go"},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass after duplicate slash normalization, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateDotSegmentNormalization", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles:     []string{"internal/workflows/productworkflow/transition.go"},
			ActualChangedPaths: []string{"./internal/./workflows/productworkflow/scope.go"},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass after dot segment normalization, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateEmptyPathsSkipped", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{"internal/workflows/productworkflow/scope.go"},
			ForbiddenFiles:     []string{"internal/workflows/productworkflow/transition.go"},
			ActualChangedPaths: []string{"", "  ", "internal/workflows/productworkflow/scope.go"},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass with empty paths skipped, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateResultSlicesStableSorted", func() {
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
		gomega.Expect(!slices.IsSorted(result.ExpectedFiles)).To(gomega.BeFalse(), fmt.Sprintf("ExpectedFiles not sorted: %v", result.ExpectedFiles))
		gomega.Expect(!slices.IsSorted(result.ActualChangedPaths)).To(gomega.BeFalse(), fmt.Sprintf("ActualChangedPaths not sorted: %v", result.ActualChangedPaths))
		gomega.Expect(!slices.IsSorted(result.MissingExpected)).To(gomega.BeFalse(), fmt.Sprintf("MissingExpected not sorted: %v", result.MissingExpected))
		gomega.Expect(!slices.IsSorted(result.UnexpectedPaths)).To(gomega.BeFalse(), fmt.Sprintf("UnexpectedPaths not sorted: %v", result.UnexpectedPaths))
		gomega.Expect(!slices.IsSorted(result.ForbiddenPaths)).To(gomega.BeFalse(), fmt.Sprintf("ForbiddenPaths not sorted: %v", result.ForbiddenPaths))

		prevCode := ""
		prevPath := ""

		for _, b := range result.Blockers {
			gomega.Expect(b.Code < prevCode).To(gomega.BeFalse(), fmt.Sprintf("Blocker codes not sorted: %q < %q", b.Code, prevCode))
			if b.Code == prevCode {
				gomega.Expect(strings.Compare(b.Path, prevPath)).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("Blocker paths not sorted within code group: %q < %q", b.Path, prevPath))
			}

			prevCode = b.Code
			prevPath = b.Path
		}
	})
	ginkgo.It("ScopeGateBlockerCodesAreSorted", func() {
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
		gomega.Expect(!slices.IsSorted(codes)).To(gomega.BeFalse(), fmt.Sprintf("Blocker codes not sorted: %v", codes))

	})
	ginkgo.It("ScopeGateMultipleBlockers", func() {
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
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		codeSet := map[string]bool{}
		for _, b := range result.Blockers {
			codeSet[b.Code] = true
		}

		for _, want := range []string{"forbidden_path", "forbidden_pattern", "unexpected_path", "missing_expected_path"} {
			gomega.Expect(!codeSet[want]).To(gomega.BeFalse(), fmt.Sprintf("expected blocker code %q, got codes %v", want, codeSet))

		}
		gomega.Expect(result.MissingExpected).To(gomega.HaveLen(1), fmt.Sprintf("expected missing [b.go], got %v", result.MissingExpected))
		gomega.Expect(result.MissingExpected[0]).To(gomega.Equal("b.go"), fmt.Sprintf("expected missing [b.go], got %v", result.MissingExpected))
		gomega.Expect(result.UnexpectedPaths).To(gomega.HaveLen(1), fmt.Sprintf("expected unexpected [unexpected.go], got %v", result.UnexpectedPaths))
		gomega.Expect(result.UnexpectedPaths[0]).To(gomega.Equal("unexpected.go"), fmt.Sprintf("expected unexpected [unexpected.go], got %v", result.UnexpectedPaths))
		gomega.Expect(len(result.ForbiddenPaths) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 forbidden paths, got %v", result.ForbiddenPaths))

	})
	ginkgo.It("ScopeGatePatternMatchesDeepPath", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"a.go"},
			ForbiddenPatterns: []string{"cmd/**"},
			ActualChangedPaths: []string{
				"a.go",
				"cmd/idiom/sub/deep.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked for deep forbidden pattern match, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_pattern" && b.Path == "cmd/idiom/sub/deep.go" && b.Pattern == "cmd/**" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_pattern match for deep path, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGatePatternMatchesSingleSegment", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"a.go"},
			ForbiddenPatterns: []string{"*.test.go"},
			ActualChangedPaths: []string{
				"a.go",
				"foo.test.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked for glob *.test.go, got %q", result.Status))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_pattern" && b.Pattern == "*.test.go" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_pattern for *.test.go, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGatePatternNoMatchPasses", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"a.go"},
			ForbiddenPatterns: []string{"cmd/**"},
			ActualChangedPaths: []string{
				"a.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass when no pattern matches, got %q", result.Status))

	})
	ginkgo.It("ScopeGateExpectedAsAllowed", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{"a.go", "b.go"},
			ForbiddenFiles:     []string{"f.go"},
			ActualChangedPaths: []string{"a.go", "b.go"},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass when all expected present, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateReportPathsAllowedAsExpected", func() {
		input := ScopeGateInput{
			ExpectedFiles: []string{
				"internal/workflows/productworkflow/scope.go",
				"reports/impl-0016-implementation.json",
			},
			ForbiddenFiles:    []string{"internal/workflows/productworkflow/transition.go"},
			ForbiddenPatterns: []string{"docs/**"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"reports/impl-0016-implementation.json",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass with report path as expected, got %q with blockers %+v", result.Status, result.Blockers))

	})
	ginkgo.It("ScopeGateDurableBookkeepingAllowedBlocksForbiddenSpillover", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"internal/workflows/productworkflow/scope.go"},
			AllowedFiles:      []string{"reports/impl.json", "tdd.json"},
			ForbiddenFiles:    []string{"internal/workflows/productworkflow/types.go"},
			ForbiddenPatterns: []string{"internal/runtime/agent/**"},
			ActualChangedPaths: []string{
				"internal/workflows/productworkflow/scope.go",
				"reports/impl.json",
				"tdd.json",
				"internal/runtime/agent/runner.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", result.Status))

		foundPattern := false

		for _, b := range result.Blockers {
			if b.Code == "forbidden_pattern" && b.Path == "internal/runtime/agent/runner.go" {
				foundPattern = true
			}
		}
		gomega.Expect(!foundPattern).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden_pattern for internal/runtime/agent/runner.go, got %+v", result.Blockers))

	})
	ginkgo.It("ScopeGateEmptyInputPasses", func() {
		input := ScopeGateInput{
			ExpectedFiles:      []string{},
			ForbiddenFiles:     []string{},
			ActualChangedPaths: []string{},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass for empty input, got %q", result.Status))

	})
	ginkgo.It("ScopeGateForbiddenPatternMatchesExactPrefix", func() {
		input := ScopeGateInput{
			ExpectedFiles:     []string{"a.go"},
			ForbiddenPatterns: []string{"internal/runtime/agent/**"},
			ActualChangedPaths: []string{
				"a.go",
				"internal/runtime/agent/session.go",
			},
		}
		result := EvaluateScopeGate(input)
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked for agent path, got %q", result.Status))

	})
	ginkgo.It("ScopeGateSourceSanityPurity", func() {
		root := repoRoot()

		source, err := os.ReadFile(filepath.Join(root, "internal/workflows/productworkflow/scope.go"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scope.go: %v", err))

		text := string(source)
		for _, forbidden := range []string{
			"os.",
			"exec.",
			"git",
			"telemetry",
			"flag.",
			"command-line",
		} {
			gomega.Expect(strings.Contains(text, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("scope.go contains forbidden string %q", forbidden))

		}

		for _, forbidden := range []string{
			`"os"`,
			`"os/"`,
			`"exec"`,
			`"flag"`,
			`"runtime"`,
		} {
			gomega.Expect(strings.Contains(text, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("scope.go imports forbidden package %s", forbidden))

		}
	})
	ginkgo.It("ValidateLedgerCoverageDetectsUnderReporting", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"cmd/foo.go", "internal/bar.go"}},
		}
		git := []string{"cmd/foo.go", "internal/bar.go", "docs/effort/baz.md"}
		findings := ValidateLedgerCoverage(atoms, git, "docs/effort")
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding, got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "ledger_under_reports_path").To(gomega.BeFalse(), fmt.Sprintf("expected ledger_under_reports_path, got %s", findings[0].Code))
		gomega.Expect(!strings.Contains(findings[0].Path, "docs/effort/baz.md")).To(gomega.BeFalse(), fmt.Sprintf("expected path docs/effort/baz.md, got %s", findings[0].Path))

	})
	ginkgo.It("ValidateLedgerCoveragePassesWhenComplete", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"cmd/foo.go", "internal/bar.go", "docs/effort/baz.md"}},
		}
		git := []string{"cmd/foo.go", "internal/bar.go", "docs/effort/baz.md"}
		findings := ValidateLedgerCoverage(atoms, git, "docs/effort")
		gomega.Expect(len(findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings, got %d: %#v", len(findings), findings))

	})
	ginkgo.It("ValidateLedgerCoveragePassesWhenLedgerOverReports", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"cmd/foo.go", "internal/bar.go", "extra.go"}},
		}
		git := []string{"cmd/foo.go", "internal/bar.go"}
		findings := ValidateLedgerCoverage(atoms, git, "docs/effort")
		gomega.Expect(len(findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings when ledger over-reports, got %d", len(findings)))

	})
	ginkgo.It("ValidateLedgerCoverageEmptyGitReturnsEmpty", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"cmd/foo.go"}},
		}
		findings := ValidateLedgerCoverage(atoms, nil, "docs/effort")
		gomega.Expect(len(findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings with empty git paths, got %d", len(findings)))

	})
	ginkgo.It("ValidateLedgerCoverageDetectsEmptyChangedPaths", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: nil},
		}
		findings := ValidateLedgerCoverage(atoms, nil, "docs/effort")
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding, got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "atom_empty_changed_paths").To(gomega.BeFalse(), fmt.Sprintf("expected atom_empty_changed_paths, got %s", findings[0].Code))

	})
	ginkgo.It("ValidateLedgerCoverageIgnoresUnrelatedGitPaths", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"docs/effort/readme.md"}},
		}
		git := []string{"docs/effort/readme.md", "unrelated/package.go"}
		findings := ValidateLedgerCoverage(atoms, git, "docs/effort")

		var underReport []LedgerCoverageFinding

		for _, f := range findings {
			if f.Code == "ledger_under_reports_path" {
				underReport = append(underReport, f)
			}
		}
		gomega.Expect(len(underReport) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 under-report findings for unrelated path, got %d: %#v", len(underReport), underReport))

	})
	ginkgo.It("ValidateLedgerCoverageMultipleAtomsUnion", func() {
		atoms := []AtomCoverageCheck{
			{TaskID: "impl-001", ChangedPaths: []string{"cmd/foo.go"}},
			{TaskID: "impl-002", ChangedPaths: []string{"docs/effort/readme.md"}},
		}
		git := []string{"cmd/foo.go", "docs/effort/readme.md", "docs/effort/extra.md"}
		findings := ValidateLedgerCoverage(atoms, git, "docs/effort")

		var underReport []LedgerCoverageFinding

		for _, f := range findings {
			if f.Code == "ledger_under_reports_path" {
				underReport = append(underReport, f)
			}
		}
		gomega.Expect(len(underReport) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 under-report finding for extra.md, got %d: %#v", len(underReport), underReport))
		gomega.Expect(!strings.Contains(underReport[0].Path, "docs/effort/extra.md")).To(gomega.BeFalse(), fmt.Sprintf("expected finding for docs/effort/extra.md, got %s", underReport[0].Path))

	})
	ginkgo.It("ValidatePerAtomCoverageMatchingPathsNoFindings", func() {
		atoms := []AtomCommitCoverage{
			{
				TaskID:       "impl-001",
				CommitHash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				ChangedPaths: []string{"cmd/foo.go", "internal/bar.go"},
			},
		}
		commitPaths := map[string][]string{
			"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2": {"cmd/foo.go", "internal/bar.go"},
		}
		findings := ValidatePerAtomCoverage(atoms, commitPaths)
		gomega.Expect(len(findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings, got %d: %#v", len(findings), findings))

	})
	ginkgo.It("ValidatePerAtomCoverageUnderReportingDetectsMissingPath", func() {
		atoms := []AtomCommitCoverage{
			{
				TaskID:       "impl-001",
				CommitHash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				ChangedPaths: []string{"cmd/foo.go"},
			},
		}
		commitPaths := map[string][]string{
			"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2": {"cmd/foo.go", "internal/bar.go"},
		}
		findings := ValidatePerAtomCoverage(atoms, commitPaths)
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding, got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "atom_under_reports_path").To(gomega.BeFalse(), fmt.Sprintf("expected atom_under_reports_path, got %s", findings[0].Code))
		gomega.Expect(!strings.Contains(findings[0].Message, "internal/bar.go")).To(gomega.BeFalse(), fmt.Sprintf("expected message to mention internal/bar.go, got %s", findings[0].Message))
		gomega.Expect(!strings.Contains(findings[0].Message, "impl-001")).To(gomega.BeFalse(), fmt.Sprintf("expected message to mention impl-001, got %s", findings[0].Message))

	})
	ginkgo.It("ValidatePerAtomCoverageMissingCommitHash", func() {
		atoms := []AtomCommitCoverage{
			{
				TaskID:       "impl-001",
				CommitHash:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				ChangedPaths: []string{"cmd/foo.go"},
			},
		}
		commitPaths := map[string][]string{}
		findings := ValidatePerAtomCoverage(atoms, commitPaths)
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding, got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "commit_hash_not_found").To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash_not_found, got %s", findings[0].Code))
		gomega.Expect(!strings.Contains(findings[0].Message, "impl-001")).To(gomega.BeFalse(), fmt.Sprintf("expected message to mention impl-001, got %s", findings[0].Message))

	})
	ginkgo.It("ValidatePerAtomCoverageEmptyCommitHashRequired", func() {
		atoms := []AtomCommitCoverage{
			{
				TaskID:       "impl-001",
				CommitHash:   "",
				ChangedPaths: []string{"cmd/foo.go"},
			},
		}
		commitPaths := map[string][]string{}
		findings := ValidatePerAtomCoverage(atoms, commitPaths)
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding for empty commit hash, got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "commit_hash_missing").To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash_missing, got %q", findings[0].Code))

	})
	ginkgo.It("ValidatePerAtomCoverageMixedAtoms", func() {
		atoms := []AtomCommitCoverage{
			{
				TaskID:       "impl-001",
				CommitHash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				ChangedPaths: []string{"cmd/foo.go"},
			},
			{
				TaskID:       "impl-002",
				CommitHash:   "",
				ChangedPaths: []string{"internal/bar.go"},
			},
		}
		commitPaths := map[string][]string{
			"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2": {"cmd/foo.go"},
		}
		findings := ValidatePerAtomCoverage(atoms, commitPaths)
		gomega.Expect(len(findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 finding (commit_hash_missing for impl-002), got %d: %#v", len(findings), findings))
		gomega.Expect(findings[0].Code != "commit_hash_missing").To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash_missing, got %q", findings[0].Code))
		gomega.Expect(findings[0].Message != "atom impl-002 has no commit_hash; commit_hash is required for implementation_completed, fallback_completion, and partial_implementation_recorded records").To(gomega.BeFalse(), fmt.Sprintf("unexpected message: %q", findings[0].Message))

	})
})
