package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan scope contracts", func() {
	It("normalizes paths, obligations, and contract identity", func() {
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
		Expect(contract.RequiredCoveragePaths).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(contract.AllowedCandidatePaths).To(Equal([]string{"pkg/c.go"}))
		Expect(contract.EditSetObligations).To(HaveLen(2))
		Expect(contract.EditSetObligations[0].Source).To(Equal("typed"))
		Expect(contract.EditSetObligations[1].Source).To(Equal("retry"))
		Expect(contract.ContractID).NotTo(BeEmpty())
	})

	It("regenerates identity from normalized content", func() {
		contract := NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil)
		originalID := contract.ContractID
		contract.ContractID = "stale"
		contract.RequiredCoveragePaths = []string{"pkg/b.go"}
		contract = contract.Normalized()

		Expect(contract.ContractID).NotTo(BeEmpty())
		Expect(contract.ContractID).NotTo(Equal("stale"))
		Expect(contract.ContractID).NotTo(Equal(originalID))
	})

	It("preserves an explicitly empty write envelope", func() {
		contract := NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil).WithWriteAllowedPaths(nil)
		Expect(contract.WriteAllowedExplicit).To(BeTrue())
		Expect(contract.WriteAllowedPaths).To(BeEmpty())
		Expect(contract.ContractID).NotTo(BeEmpty())
	})

	It("removes required paths from allowed candidates", func() {
		contract := NewPlanScopeContract(
			[]string{"pkg/required.go", "pkg/test.go"},
			[]string{"pkg/test.go", "pkg/optional.go"},
			nil,
		)
		Expect(contract.AllowedCandidatePaths).To(Equal([]string{"pkg/optional.go"}))
	})
})
