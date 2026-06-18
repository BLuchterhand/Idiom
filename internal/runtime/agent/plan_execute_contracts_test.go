package agent

import (
	"strings"
	"testing"
)

// --- plan-execute coverage decision tests ---
func TestDecisionComplete(t *testing.T) {
	if !(Decision{RequiredPaths: []string{"a.go"}, CoveredPaths: []string{"a.go"}}).Complete() {
		t.Fatal("expected complete decision")
	}
	if (Decision{RequiredPaths: []string{"a.go"}, CoveredPaths: nil}).Complete() {
		t.Fatal("expected incomplete decision")
	}
	if (Decision{RequiredPaths: []string{"a.go"}, CoveredPaths: []string{"b.go"}}).Complete() {
		t.Fatal("different covered path must not satisfy required path")
	}
	if !(Decision{RequiredPaths: []string{"./a.go", "b.go"}, CoveredPaths: []string{"b.go", "a.go", "extra.go"}}).Complete() {
		t.Fatal("expected required paths covered despite order, prefix, and extra path")
	}
}

// --- submit_plan coverage validation tests ---
func TestValidateCoverageAcceptsCompleteRequiredPlan(t *testing.T) {
	decision := ValidateCoverage(CoverageInput{
		Scope: Scope{Coverage: []string{"a.go"}, DirectoryScopes: []string{"dir"}},
		Edits: []Edit{
			{Path: "a.go", OldText: "old", NewText: "new"},
			{Path: "dir/b.go", OldText: "old", NewText: "new"},
		},
	})

	if decision.Rejected {
		t.Fatalf("expected complete required plan, got %#v", decision.Details)
	}
}

func TestValidateCoverageRejectsMissingAndOutsidePaths(t *testing.T) {
	decision := ValidateCoverage(CoverageInput{
		Scope: Scope{Coverage: []string{"a.go"}},
		Edits: []Edit{{Path: "b.go", OldText: "old", NewText: "new"}},
	})

	if !decision.Rejected {
		t.Fatal("expected coverage rejection")
	}
	if !contains(decision.Details.MissingRequiredPaths, "a.go") {
		t.Fatalf("expected missing required path, got %#v", decision.Details.MissingRequiredPaths)
	}
	if !contains(decision.Details.OutsideRequiredPaths, "b.go") {
		t.Fatalf("expected outside path, got %#v", decision.Details.OutsideRequiredPaths)
	}
	if !strings.Contains(decision.ErrorText(), "submit_plan must use edits[]") {
		t.Fatalf("expected owned requirement text, got %q", decision.ErrorText())
	}
}

func TestValidateCoverageRejectsMissingEditPathAndPlaceholder(t *testing.T) {
	decision := ValidateCoverage(CoverageInput{
		Edits: []Edit{{Content: "... existing code ..."}},
	})

	if !decision.Rejected {
		t.Fatal("expected rejection")
	}
	if !contains(decision.Details.MissingPathEdits, "edits[0]") {
		t.Fatalf("expected missing path detail, got %#v", decision.Details.MissingPathEdits)
	}
	if len(decision.Details.PlaceholderEditBodies) != 1 {
		t.Fatalf("expected placeholder detail, got %#v", decision.Details.PlaceholderEditBodies)
	}
}

func TestCoverageDetailsRoundTrip(t *testing.T) {
	details := CoverageDetails{
		Code:                 CoverageMismatchCode,
		MissingRequiredPaths: []string{"a.go"},
	}
	raw := CoverageDetailsJSON(details)
	decoded, ok := CoverageDetailsFromJSON(raw)
	if !ok {
		t.Fatalf("expected round trip parse, raw=%s", raw)
	}
	if !contains(decoded.MissingRequiredPaths, "a.go") {
		t.Fatalf("expected decoded missing path, got %#v", decoded.MissingRequiredPaths)
	}
}

func TestRequiredScopeDerivesCoverageAllowedAndDirectories(t *testing.T) {
	scope := RequiredScope(
		[]string{"a.go", "pkg", "pkg/b.go"},
		[]string{"tools", "tools/c.go", "extra.go"},
		func(path string) bool { return path == "pkg" || path == "tools" },
	)

	if !contains(scope.Coverage, "a.go") || !contains(scope.Coverage, "pkg/b.go") {
		t.Fatalf("expected file coverage paths, got %#v", scope.Coverage)
	}
	if !contains(scope.DirectoryScopes, "pkg") {
		t.Fatalf("expected required directory scope, got %#v", scope.DirectoryScopes)
	}
	if !contains(scope.AllowedDirectoryScopes, "tools") {
		t.Fatalf("expected allowed directory scope, got %#v", scope.AllowedDirectoryScopes)
	}
	if !contains(scope.AllowedExact, "extra.go") {
		t.Fatalf("expected allowed exact path, got %#v", scope.AllowedExact)
	}
}

func TestRequiredScopeFallsBackToAllowedOnly(t *testing.T) {
	scope := RequiredScope(nil, []string{"allowed", "allowed/a.go"}, func(path string) bool { return path == "allowed" })

	if !scope.AllowedOnly {
		t.Fatalf("expected allowed-only scope: %#v", scope)
	}
	if len(scope.Coverage) != 0 {
		t.Fatalf("allowed-only scope should not create required coverage, got %#v", scope.Coverage)
	}
	if !contains(scope.DirectoryScopes, "allowed") {
		t.Fatalf("expected directory scope, got %#v", scope.DirectoryScopes)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
