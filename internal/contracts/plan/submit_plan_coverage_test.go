package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Submit-plan coverage", func() {
	It("accepts a plan covering every required file and directory", func() {
		decision := ValidateCoverage(CoverageInput{
			Scope: SubmitPlanCoverageScope{Coverage: []string{"a.go"}, DirectoryScopes: []string{"dir"}},
			Edits: []SubmitPlanCoverageEdit{
				{Path: "a.go", OldText: "old", NewText: "new"},
				{Path: "dir/b.go", OldText: "old", NewText: "new"},
			},
		})
		Expect(decision.Rejected).To(BeFalse())
	})

	It("rejects missing required paths and out-of-scope edits", func() {
		decision := ValidateCoverage(CoverageInput{
			Scope: SubmitPlanCoverageScope{Coverage: []string{"a.go"}},
			Edits: []SubmitPlanCoverageEdit{{Path: "b.go", OldText: "old", NewText: "new"}},
		})
		Expect(decision.Rejected).To(BeTrue())
		Expect(decision.Details.MissingRequiredPaths).To(ContainElement("a.go"))
		Expect(decision.Details.OutsideRequiredPaths).To(ContainElement("b.go"))
		Expect(decision.ErrorText()).To(ContainSubstring("submit_plan must use edits[]"))
	})

	It("rejects missing edit paths and placeholder bodies", func() {
		decision := ValidateCoverage(CoverageInput{
			Edits: []SubmitPlanCoverageEdit{{Content: "... existing code ..."}},
		})
		Expect(decision.Rejected).To(BeTrue())
		Expect(decision.Details.MissingPathEdits).To(ContainElement("edits[0]"))
		Expect(decision.Details.PlaceholderEditBodies).To(HaveLen(1))
	})

	It("round-trips coverage details through JSON", func() {
		details := CoverageDetails{
			Code:                 CoverageMismatchCode,
			MissingRequiredPaths: []string{"a.go"},
		}
		decoded, ok := CoverageDetailsFromJSON(CoverageDetailsJSON(details))
		Expect(ok).To(BeTrue())
		Expect(decoded.MissingRequiredPaths).To(ContainElement("a.go"))
	})

	It("derives required and allowed exact and directory scope", func() {
		scope := RequiredScope(
			[]string{"a.go", "pkg", "pkg/b.go"},
			[]string{"tools", "tools/c.go", "extra.go"},
			func(path string) bool { return path == "pkg" || path == "tools" },
		)
		Expect(scope.Coverage).To(And(ContainElement("a.go"), ContainElement("pkg/b.go")))
		Expect(scope.DirectoryScopes).To(ContainElement("pkg"))
		Expect(scope.AllowedDirectoryScopes).To(ContainElement("tools"))
		Expect(scope.AllowedExact).To(ContainElement("extra.go"))
	})

	It("falls back to allowed-only directory scope", func() {
		scope := RequiredScope(nil, []string{"allowed", "allowed/a.go"}, func(path string) bool { return path == "allowed" })
		Expect(scope.AllowedOnly).To(BeTrue())
		Expect(scope.Coverage).To(BeEmpty())
		Expect(scope.DirectoryScopes).To(ContainElement("allowed"))
	})
})
