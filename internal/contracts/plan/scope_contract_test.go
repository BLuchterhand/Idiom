package plancontract

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

func TestOpenEditSetObligationsUseRequiredPathsNotAllowedCandidates(t *testing.T) {
	contract := NewPlanScopeContract(
		[]string{"pkg/required.go", "pkg/required_test.go"},
		[]string{"pkg/optional.go", "pkg/optional_test.go"},
		nil,
	)

	open := contract.OpenEditSetObligations([]string{"pkg/optional.go", "pkg/optional_test.go"})
	if len(open) != 2 {
		t.Fatalf("open obligations = %#v, want required implementation and test paths", open)
	}

	want := []EditSetOpenObligation{
		{
			Code:   "requested_test_edit_missing",
			Kind:   EditSetObligationKindTestEdit,
			Path:   "pkg/required_test.go",
			Source: "required_coverage_path",
		},
		{
			Code:   "requested_implementation_edit_missing",
			Kind:   EditSetObligationKindImplementationEdit,
			Path:   "pkg/required.go",
			Source: "required_coverage_path",
		},
	}
	if !slices.Equal(open, want) {
		t.Fatalf("open obligations = %#v, want %#v", open, want)
	}
}

func TestOpenEditSetObligationsDoNotDemoteRequiredPaths(t *testing.T) {
	contract := NewPlanScopeContract(
		[]string{"pkg/feature.go", "./pkg/feature.go"},
		[]string{"pkg/feature.go", "pkg/optional.go"},
		nil,
	)

	if got, want := contract.RequiredCoveragePaths, []string{"pkg/feature.go"}; !slices.Equal(got, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", got, want)
	}

	if got, want := contract.AllowedCandidatePaths, []string{"pkg/optional.go"}; !slices.Equal(got, want) {
		t.Fatalf("allowed candidate paths = %#v, want %#v", got, want)
	}

	open := contract.OpenEditSetObligations([]string{"pkg/optional.go"})
	if len(open) != 1 || open[0].Path != "pkg/feature.go" || open[0].Kind != EditSetObligationKindImplementationEdit {
		t.Fatalf("required path should remain an open implementation obligation, got %#v", open)
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
