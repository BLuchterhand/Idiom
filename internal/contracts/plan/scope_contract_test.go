package plan

import (
	"slices"
	"testing"
)

func TestPlanScopeContractNormalizesPathsObligationsAndID(t *testing.T) {
	contract := NewPlanScopeContract(
		[]string{" ./pkg/a.go ", "pkg/a.go", "../escape.go", "pkg\\b.go"},
		[]string{"pkg/c.go", "./pkg/c.go", "/tmp/outside.go"},
		EditSetObligations{
			{Kind: EditSetObligationKindTestEdit},
			{Kind: "unknown"},
			{Kind: EditSetObligationKindTestEdit, Source: "duplicate"},
			{Kind: EditSetObligationKindImplementationEdit, Source: "retry"},
		},
	)

	if want := []string{"pkg/a.go", "pkg/b.go"}; !slices.Equal(contract.RequiredCoveragePaths, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", contract.RequiredCoveragePaths, want)
	}

	if want := []string{"pkg/c.go"}; !slices.Equal(contract.AllowedCandidatePaths, want) {
		t.Fatalf("allowed candidate paths = %#v, want %#v", contract.AllowedCandidatePaths, want)
	}

	if got := contract.EditSetObligations; len(got) != 2 || got[0].Source != "typed" || got[1].Source != "retry" {
		t.Fatalf("edit-set obligations not normalized with owned defaults: %#v", got)
	}

	if contract.ContractID == "" {
		t.Fatalf("non-empty scope contract must have an id")
	}
}

func TestPlanScopeContractIDTracksNormalizedContent(t *testing.T) {
	contract := NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil)
	originalID := contract.ContractID

	contract.ContractID = "stale"
	contract.RequiredCoveragePaths = []string{"pkg/b.go"}
	contract = contract.Normalized()

	if contract.ContractID == "" || contract.ContractID == "stale" || contract.ContractID == originalID {
		t.Fatalf("contract id should be regenerated from normalized content, got %q original=%q", contract.ContractID, originalID)
	}
}

func TestPlanScopeContractPreservesExplicitEmptyWriteEnvelope(t *testing.T) {
	contract := NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil).WithWriteAllowedPaths(nil)

	if !contract.WriteAllowedExplicit {
		t.Fatalf("explicit empty write envelope was not preserved: %+v", contract)
	}

	if len(contract.WriteAllowedPaths) != 0 {
		t.Fatalf("write allowed paths = %#v, want empty", contract.WriteAllowedPaths)
	}

	if contract.ContractID == "" {
		t.Fatalf("explicit empty write envelope should contribute contract content")
	}
}

func TestPlanScopeContractRequiredPathsWinOverAllowedCandidates(t *testing.T) {
	contract := NewPlanScopeContract(
		[]string{"pkg/required.go", "pkg/test.go"},
		[]string{"pkg/test.go", "pkg/optional.go"},
		nil,
	)

	if want := []string{"pkg/optional.go"}; !slices.Equal(contract.AllowedCandidatePaths, want) {
		t.Fatalf("allowed candidate paths = %#v, want %#v", contract.AllowedCandidatePaths, want)
	}
}

func TestNormalizeWorkspacePathCleansComparisonVariants(t *testing.T) {
	cases := map[string]string{
		` ./pkg\feature.go `: "pkg/feature.go",
		"pkg//feature.go":    "pkg/feature.go",
		"pkg/./feature.go":   "pkg/feature.go",
		"pkg/feature.go/":    "pkg/feature.go",
	}
	for input, want := range cases {
		if got := NormalizeWorkspacePath(input); got != want {
			t.Fatalf("NormalizeWorkspacePath(%q) = %q, want %q", input, got, want)
		}
	}

	for _, input := range []string{"", ".", "..", "../escape.go", "/tmp/abs.go", "pkg/\x00bad.go"} {
		if got := NormalizeWorkspacePath(input); got != "" {
			t.Fatalf("NormalizeWorkspacePath(%q) = %q, want empty invalid path", input, got)
		}
	}
}
