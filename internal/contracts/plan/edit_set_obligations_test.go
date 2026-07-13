package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Edit-set obligations", func() {
	It("normalizes and merges obligations by kind and path", func() {
		obligations := MergeEditSetObligations(
			EditSetObligations{
				{Kind: EditSetObligationKindImplementationEdit, Path: " ./pkg/feature.go ", Source: "impl"},
				{Kind: EditSetObligationKindTestEdit, Path: "pkg/feature_test.go", Source: "test"},
				{Kind: EditSetObligationKindTestEdit, Path: "pkg\\feature_test.go", Source: "later"},
				{Kind: EditSetObligationKindTestEdit},
				{Kind: "invalid"},
			}.WithDefaultSource("scope_contract"),
			EditSetObligations{
				{Kind: EditSetObligationKindImplementationEdit},
				{Kind: EditSetObligationKindTestEdit, Source: "later"},
			}.WithDefaultSource("options"),
		)
		Expect(obligations.Kinds()).To(Equal([]string{"test_edit_required", "implementation_edit_required"}))
		Expect(obligations).To(HaveLen(4))
		Expect([]string{obligations[0].Path, obligations[1].Path, obligations[2].Path, obligations[3].Path}).To(Equal([]string{
			"", "pkg/feature_test.go", "", "pkg/feature.go",
		}))
		Expect([]string{obligations[0].Source, obligations[1].Source, obligations[2].Source, obligations[3].Source}).To(Equal([]string{
			"scope_contract", "test", "options", "impl",
		}))
	})

	It("classifies changed paths against required edit kinds", func() {
		obligations := EditSetObligations{}.
			With(EditSetObligationKindTestEdit, "test").
			With(EditSetObligationKindImplementationEdit, "test")
		Expect(obligations.RejectionReason([]string{"pkg/feature.go", "pkg/feature_test.go"})).To(BeEmpty())
		Expect(obligations.RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"})).To(Equal(PlanExecuteFailureMissingRequestedImplementationEdit))
		Expect(obligations.MissingOpenObligations([]string{"pkg/feature.go"})).To(Equal([]string{"requested_test_edit_missing"}))
	})

	It("reports normalized open exact paths", func() {
		obligations := EditSetObligations{
			{Kind: EditSetObligationKindImplementationEdit, Path: "pkg/feature.go", Source: "required_scope"},
			{Kind: EditSetObligationKindTestEdit, Path: "pkg/feature_test.go", Source: "required_scope"},
		}.Normalized()

		open := obligations.Open([]string{"pkg/feature.go"})
		Expect(open).To(HaveLen(1))
		Expect(open[0].Code).To(Equal(EditSetOpenObligationRequestedTestEditMissing))
		Expect(open[0].Kind).To(Equal(EditSetObligationKindTestEdit))
		Expect(open[0].Path).To(Equal("pkg/feature_test.go"))
		Expect(open[0].Source).To(Equal("required_scope"))
	})
})
