package planexecute

import (
	"slices"
	"testing"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

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
		ScopeContract: plancontract.NewPlanScopeContract(
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

func TestPlanScopeContractWriteAllowedFlagIsNotExpandedByPromptScope(t *testing.T) {
	opts := Options{
		WriteAllowedPaths: MultiFlag{"pkg/approved.go"},
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

func TestPlanScopeContractFrozenSkipsPromptDerivedScope(t *testing.T) {
	opts := Options{
		ScopeContract: plancontract.NewPlanScopeContract(
			[]string{"pkg/frozen.go"},
			[]string{"pkg/allowed.go"},
			nil,
		),
		ScopeExtractionPolicy: ScopeExtractionFrozen,
	}
	prompt := `
## Prior Cycles

Changed pkg/history.go.

## Remaining Requirements

Failure mentions pkg/failure.go.

## Files To Modify

- pkg/prompt-required.go

## Allowed Edit Paths

- pkg/prompt-allowed.go
`
	contract := planExecuteScopeContract(opts, prompt)

	if got, want := contract.RequiredCoveragePaths, []string{"pkg/frozen.go"}; !slices.Equal(got, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", got, want)
	}

	if got, want := contract.AllowedCandidatePaths, []string{"pkg/allowed.go"}; !slices.Equal(got, want) {
		t.Fatalf("allowed candidate paths = %#v, want %#v", got, want)
	}
}

func TestPlanScopeContractFrozenIgnoresContextPackCandidates(t *testing.T) {
	opts := Options{
		ScopeContract: plancontract.NewPlanScopeContract(
			[]string{"pkg/target.go"},
			nil,
			nil,
		).WithWriteAllowedPaths([]string{"pkg/target.go"}),
		ScopeExtractionPolicy: ScopeExtractionFrozen,
	}
	prompt := `
Implement the target.

## Context Pack

### Candidate Files

- pkg/discovered.go (editable): matched task terms
- pkg/discovered_test.go (editable): matched task terms
`
	contract := planExecuteScopeContract(opts, prompt)

	if got, want := contract.RequiredCoveragePaths, []string{"pkg/target.go"}; !slices.Equal(got, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", got, want)
	}

	if len(contract.AllowedCandidatePaths) != 0 {
		t.Fatalf("context_pack candidates should not be extracted when scope is frozen: %#v", contract.AllowedCandidatePaths)
	}

	if got, want := contract.WriteAllowedPaths, []string{"pkg/target.go"}; !slices.Equal(got, want) {
		t.Fatalf("write envelope = %#v, want %#v", got, want)
	}
}

func TestPlanScopeContractDefaultExtractsPromptScope(t *testing.T) {
	opts := Options{}
	prompt := `
## Files To Modify

- pkg/prompt-required.go

## Allowed Edit Paths

- pkg/prompt-allowed.go
`
	contract := planExecuteScopeContract(opts, prompt)

	if !slices.Contains(contract.RequiredCoveragePaths, "pkg/prompt-required.go") {
		t.Fatalf("default scope extraction should include prompt required path, contract=%#v", contract)
	}

	if !slices.Contains(contract.AllowedCandidatePaths, "pkg/prompt-allowed.go") {
		t.Fatalf("default scope extraction should include prompt allowed path, contract=%#v", contract)
	}
}

func TestPlanScopeContractDefaultExtractsMentionedPathFallback(t *testing.T) {
	contract := planExecuteScopeContract(Options{}, "Please update pkg/mentioned.go.")

	if !slices.Contains(contract.AllowedCandidatePaths, "pkg/mentioned.go") {
		t.Fatalf("default scope extraction should include mentioned path fallback, contract=%#v", contract)
	}
}

func TestPlanScopeContractExplicitEmptyWriteEnvelopeDisablesFallback(t *testing.T) {
	opts := Options{
		WriteAllowedPathsExplicit: true,
	}
	prompt := `
## Files To Modify

- pkg/generated.go
`
	contract := planExecuteScopeContract(opts, prompt)

	if !contract.WriteAllowedExplicit {
		t.Fatalf("write envelope should be explicit: %#v", contract)
	}

	if len(contract.WriteAllowedPaths) != 0 {
		t.Fatalf("write envelope = %#v, want empty", contract.WriteAllowedPaths)
	}

	if !slices.Contains(contract.RequiredCoveragePaths, "pkg/generated.go") {
		t.Fatalf("prompt path should remain planning coverage, contract=%#v", contract)
	}
}

func TestPreflightExplicitEmptyWriteEnvelopeRejectsEdits(t *testing.T) {
	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "pkg/generated.go", NewText: "package pkg\n"}},
	}

	mismatch := preflightPlanWriteAllowedPaths(nil, true, plan)
	if mismatch == nil {
		t.Fatal("expected explicit empty write envelope to reject edits")
	}

	if mismatch.Path != "pkg/generated.go" {
		t.Fatalf("mismatch path = %q, want pkg/generated.go", mismatch.Path)
	}
}
