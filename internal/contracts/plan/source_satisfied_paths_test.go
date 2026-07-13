package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Source-satisfied path coverage", func() {
	It("requires declared scope and current-state evidence", func() {
		violations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
			RequiredPaths:        []string{"a.go", "b.go"},
			SourceSatisfiedPaths: []string{"a.go"},
			RequireEvidence:      true,
		})
		Expect(violations).To(HaveLen(1))
		Expect(violations[0].Code).To(Equal("missing_current_state_evidence"))
		Expect(violations[0].Path).To(Equal("a.go"))

		violations = ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
			SourceSatisfiedPaths: []string{"a.go"},
			Evidence:             []SourceSatisfiedEvidenceRef{{Path: "a.go", Kind: "harness_observed_source"}},
			RequireEvidence:      true,
		})
		Expect(violations).To(HaveLen(1))
		Expect(violations[0].Code).To(Equal("missing_required_scope"))
	})

	It("accepts canonical current-state source evidence", func() {
		violations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
			RequiredPaths:        []string{"a.go", "b.go"},
			SourceSatisfiedPaths: []string{"a.go"},
			Evidence: []SourceSatisfiedEvidenceRef{
				{Path: "a.go", Kind: "harness_observed_source"},
				{Path: "b.go", Kind: "context_packet"},
			},
			RequireEvidence: true,
		})
		Expect(violations).To(BeEmpty())
	})

	It("rejects pending paths outside the approved subset", func() {
		violations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
			RequiredPaths:        []string{"a.go"},
			SourceSatisfiedPaths: []string{"a.go"},
			Evidence:             []SourceSatisfiedEvidenceRef{{Path: "a.go", Kind: "harness_observed_source"}},
			ApprovedPaths:        []string{"b.go"},
			PendingPaths:         []string{"a.go"},
			RequireEvidence:      true,
		})
		Expect(violations).To(HaveLen(2))
		Expect(violations[0].Code).To(Equal("pending_approval"))
		Expect(violations[1].Code).To(Equal("outside_approved_scope"))
	})

	DescribeTable("rejects non-canonical evidence kinds",
		func(kind string) {
			violations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
				RequiredPaths:        []string{"a.go"},
				SourceSatisfiedPaths: []string{"a.go"},
				Evidence:             []SourceSatisfiedEvidenceRef{{Path: "a.go", Kind: SourceSatisfiedEvidenceKind(kind)}},
				RequireEvidence:      true,
			})
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Code).To(Equal("missing_current_state_evidence"))
		},
		Entry("generic source", "source"),
		Entry("read-file tool", "read_file"),
		Entry("observed current state", "harness_observed_current_state"),
		Entry("verified current state", "verified_current_state"),
		Entry("verified current source", "verified_current_source"),
	)
})
