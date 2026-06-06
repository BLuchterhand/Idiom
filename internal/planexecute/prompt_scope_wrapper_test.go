package planexecute

import (
	"slices"
	"testing"

	"idiom/internal/plancontract"
)

func TestPromptScopeWrappersMatchPlanContract(t *testing.T) {
	prompt := `
## Files To Modify

- pkg/required.go

## Allowed Edit Paths

- pkg/allowed.go
`
	if got, want := RequiredEditPathsFromPrompt(prompt), plancontract.RequiredEditPathsFromPrompt(prompt); !slices.Equal(got, want) {
		t.Fatalf("required wrapper = %#v, want %#v", got, want)
	}
	if got, want := AllowedEditPathsFromPrompt(prompt), plancontract.AllowedEditPathsFromPrompt(prompt); !slices.Equal(got, want) {
		t.Fatalf("allowed wrapper = %#v, want %#v", got, want)
	}
}

func TestPlanContractCompatibilityAliasesMatchPlanContract(t *testing.T) {
	var contract plancontract.PlanScopeContract = NewPlanScopeContract(
		[]string{"pkg/a.go"},
		nil,
		EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
	)
	if contract.ContractID == "" || !contract.EditSetObligations.Has(plancontract.EditSetObligationKindTestEdit) {
		t.Fatalf("scope contract wrapper did not return plancontract-owned value: %#v", contract)
	}

	var obligations plancontract.EditSetObligations = MergeEditSetObligations(
		EditSetObligations{{Kind: EditSetObligationKindImplementationEdit}},
	)
	if !obligations.Has(plancontract.EditSetObligationKindImplementationEdit) {
		t.Fatalf("edit-set wrapper did not return plancontract-owned obligations: %#v", obligations)
	}
}

func TestPlanScopeContractResolvesAllowedCandidateWithoutRequiredCoverage(t *testing.T) {
	opts := Options{
		RequiredEditPaths: MultiFlag{"pkg/required.go"},
		AllowedEditPaths:  MultiFlag{"pkg/candidate.go", "../escape.go", "/tmp/outside.go"},
	}
	prompt := `
## Allowed Edit Paths

- pkg/prompt-candidate.go
`
	contract := planExecuteScopeContract(opts, prompt)
	if want := []string{"pkg/required.go"}; !slices.Equal(contract.RequiredCoveragePaths, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", contract.RequiredCoveragePaths, want)
	}
	for _, want := range []string{"pkg/candidate.go", "pkg/prompt-candidate.go"} {
		if !slices.Contains(contract.AllowedCandidatePaths, want) {
			t.Fatalf("missing allowed candidate path %q in %#v", want, contract.AllowedCandidatePaths)
		}
	}
	for _, notWant := range []string{"../escape.go", "/tmp/outside.go"} {
		if slices.Contains(contract.AllowedCandidatePaths, notWant) {
			t.Fatalf("invalid candidate path must not survive scope resolution: %#v", contract.AllowedCandidatePaths)
		}
	}
	if slices.Contains(contract.RequiredCoveragePaths, "pkg/candidate.go") || slices.Contains(contract.RequiredCoveragePaths, "pkg/prompt-candidate.go") {
		t.Fatalf("allowed candidates must not become required coverage: %#v", contract.RequiredCoveragePaths)
	}
}

func TestPlanScopeContractWriteEnvelopeIsNotExpandedByPromptScope(t *testing.T) {
	opts := Options{
		ScopeContract: NewPlanScopeContract(
			[]string{"pkg/approved.go"},
			nil,
			nil,
		).WithWriteAllowedPaths([]string{"pkg/approved.go"}),
	}
	prompt := `
## Files To Modify

- pkg/generated.go

## Allowed Edit Paths

- pkg/generated_ui.go
`
	contract := planExecuteScopeContract(opts, prompt)
	if got, want := contract.WriteAllowedPaths, []string{"pkg/approved.go"}; !slices.Equal(got, want) {
		t.Fatalf("write envelope = %#v, want %#v", got, want)
	}
	for _, want := range []string{"pkg/generated.go", "pkg/generated_ui.go"} {
		if !slices.Contains(append(append([]string{}, contract.RequiredCoveragePaths...), contract.AllowedCandidatePaths...), want) {
			t.Fatalf("prompt path %q should remain planning scope/evidence, contract=%#v", want, contract)
		}
	}
}

func TestPlanExecutePathCompatibilityWrappersMatchPlanContract(t *testing.T) {
	paths := []string{
		" ./pkg/a.go ",
		"pkg/a.go",
		"pkg\\b.go",
		"../escape.go",
		"/tmp/outside.go",
		"pkg//bad.go",
		"pkg/./bad.go",
		"pkg/../bad.go",
		"",
	}
	if got, want := PlanExecuteUniqueRequiredPaths(paths), plancontract.UniqueScopePaths(paths); !slices.Equal(got, want) {
		t.Fatalf("unique required paths = %#v, want %#v", got, want)
	}
	for _, path := range paths {
		if got, want := NormalizePlanExecutePath(path), plancontract.NormalizeScopePath(path); got != want {
			t.Fatalf("NormalizePlanExecutePath(%q) = %q, want %q", path, got, want)
		}
	}
	classificationPaths := []string{
		"pkg/feature.go",
		"pkg/feature_test.go",
		"docs/feature.md",
		"prompts/agent.md",
		"tests/feature.ts",
		"pkg/feature.spec.ts",
	}
	if got, want := planExecuteChangedPathsIncludeTest(classificationPaths), plancontract.ChangedPathsIncludeTest(classificationPaths); got != want {
		t.Fatalf("test path classification = %t, want %t", got, want)
	}
	if got, want := planExecuteChangedPathsIncludeImplementation(classificationPaths), plancontract.ChangedPathsIncludeImplementation(classificationPaths); got != want {
		t.Fatalf("implementation path classification = %t, want %t", got, want)
	}
	for _, path := range classificationPaths {
		if got, want := planExecutePathLooksLikeTest(path), plancontract.PathLooksLikeTest(path); got != want {
			t.Fatalf("test predicate for %q = %t, want %t", path, got, want)
		}
		if got, want := planExecutePathLooksLikeImplementation(path), plancontract.PathLooksLikeImplementation(path); got != want {
			t.Fatalf("implementation predicate for %q = %t, want %t", path, got, want)
		}
	}
}
