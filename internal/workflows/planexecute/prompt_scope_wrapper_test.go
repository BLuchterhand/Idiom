package planexecute

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

var _ = ginkgo.It("plan scope contract resolves allowed candidate without required coverage", func() {
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
		ginkgo.Fail(fmt.Sprintf("required coverage paths = %#v, want %#v", contract.RequiredCoveragePaths, want))
	}

	for _, want := range []string{"pkg/candidate.go", "pkg/prompt-candidate.go"} {
		gomega.Expect(slices.Contains(contract.AllowedCandidatePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed candidate path %q in %#v", want, contract.AllowedCandidatePaths))

	}

	for _, notWant := range []string{"../escape.go", "/tmp/outside.go"} {
		gomega.Expect(slices.Contains(contract.AllowedCandidatePaths, notWant)).To(gomega.BeFalse(), fmt.Sprintf("invalid candidate path must not survive scope resolution: %#v", contract.AllowedCandidatePaths))

	}
	gomega.Expect(contract.RequiredCoveragePaths).NotTo(gomega.ContainElement("pkg/candidate.go"), fmt.Sprintf("allowed candidates must not become required coverage: %#v", contract.RequiredCoveragePaths))
	gomega.Expect(contract.RequiredCoveragePaths).NotTo(gomega.ContainElement("pkg/prompt-candidate.go"), fmt.Sprintf("allowed candidates must not become required coverage: %#v", contract.RequiredCoveragePaths))
})

var _ = ginkgo.It("plan scope contract write envelope is not expanded by prompt scope", func() {
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
		ginkgo.Fail(fmt.Sprintf("write envelope = %#v, want %#v", got, want))
	}

	for _, want := range []string{"pkg/generated.go", "pkg/generated_ui.go"} {
		gomega.Expect(slices.Contains(append(append([]string{}, contract.RequiredCoveragePaths...), contract.AllowedCandidatePaths...), want)).To(gomega.BeTrue(), fmt.Sprintf("prompt path %q should remain planning scope/evidence, contract=%#v", want, contract))

	}
})

var _ = ginkgo.It("plan scope contract write allowed flag is not expanded by prompt scope", func() {
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
		ginkgo.Fail(fmt.Sprintf("write envelope = %#v, want %#v", got, want))
	}

	for _, want := range []string{"pkg/generated.go", "pkg/generated_ui.go"} {
		gomega.Expect(slices.Contains(append(append([]string{}, contract.RequiredCoveragePaths...), contract.AllowedCandidatePaths...), want)).To(gomega.BeTrue(), fmt.Sprintf("prompt path %q should remain planning scope/evidence, contract=%#v", want, contract))

	}
})

var _ = ginkgo.It("plan scope contract frozen skips prompt derived scope", func() {
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
		ginkgo.Fail(fmt.Sprintf("required coverage paths = %#v, want %#v", got, want))
	}

	if got, want := contract.AllowedCandidatePaths, []string{"pkg/allowed.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("allowed candidate paths = %#v, want %#v", got, want))
	}
})

var _ = ginkgo.It("plan scope contract frozen ignores context pack candidates", func() {
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
		ginkgo.Fail(fmt.Sprintf("required coverage paths = %#v, want %#v", got, want))
	}
	gomega.Expect(len(contract.AllowedCandidatePaths) != 0).To(gomega.BeFalse(), fmt.Sprintf("context_pack candidates should not be extracted when scope is frozen: %#v", contract.AllowedCandidatePaths))

	if got, want := contract.WriteAllowedPaths, []string{"pkg/target.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("write envelope = %#v, want %#v", got, want))
	}
})

var _ = ginkgo.It("plan scope contract default extracts prompt scope", func() {
	opts := Options{}
	prompt := `
## Files To Modify

- pkg/prompt-required.go

## Allowed Edit Paths

- pkg/prompt-allowed.go
`
	contract := planExecuteScopeContract(opts, prompt)
	gomega.Expect(slices.Contains(contract.RequiredCoveragePaths, "pkg/prompt-required.go")).To(gomega.BeTrue(), fmt.Sprintf("default scope extraction should include prompt required path, contract=%#v", contract))
	gomega.Expect(slices.Contains(contract.AllowedCandidatePaths, "pkg/prompt-allowed.go")).To(gomega.BeTrue(), fmt.Sprintf("default scope extraction should include prompt allowed path, contract=%#v", contract))
})

var _ = ginkgo.It("plan scope contract default extracts mentioned path fallback", func() {
	contract := planExecuteScopeContract(Options{}, "Please update pkg/mentioned.go.")
	gomega.Expect(slices.Contains(contract.AllowedCandidatePaths, "pkg/mentioned.go")).To(gomega.BeTrue(), fmt.Sprintf("default scope extraction should include mentioned path fallback, contract=%#v", contract))
})

var _ = ginkgo.It("plan scope contract explicit empty write envelope disables fallback", func() {
	opts := Options{
		WriteAllowedPathsExplicit: true,
	}
	prompt := `
## Files To Modify

- pkg/generated.go
`
	contract := planExecuteScopeContract(opts, prompt)
	gomega.Expect(contract.WriteAllowedExplicit).To(gomega.BeTrue(), fmt.Sprintf("write envelope should be explicit: %#v", contract))
	gomega.Expect(len(contract.WriteAllowedPaths) != 0).To(gomega.BeFalse(), fmt.Sprintf("write envelope = %#v, want empty", contract.WriteAllowedPaths))
	gomega.Expect(slices.Contains(contract.RequiredCoveragePaths, "pkg/generated.go")).To(gomega.BeTrue(), fmt.Sprintf("prompt path should remain planning coverage, contract=%#v", contract))
})

var _ = ginkgo.It("preflight explicit empty write envelope rejects edits", func() {
	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "pkg/generated.go", NewText: "package pkg\n"}},
	}

	mismatch := preflightPlanWriteAllowedPaths(nil, true, plan)
	gomega.Expect(mismatch).NotTo(gomega.BeNil(), fmt.Sprint("expected explicit empty write envelope to reject edits"))
	gomega.Expect(mismatch.Path != "pkg/generated.go").To(gomega.BeFalse(), fmt.Sprintf("mismatch path = %q, want pkg/generated.go", mismatch.Path))
})
