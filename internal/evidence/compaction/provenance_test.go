package compaction

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("compaction provenance", func() {
	It("verifies retained required paths and records hashes", func() {
		provenance := New("source", "retained", "summary", 5, 3, []string{"b.go", "a.go"}, []string{"a.go"}, "evidence_weighted", "context_budget")

		Expect(provenance.Acceptable()).To(BeTrue())
		Expect(provenance.EvictedMessages).To(Equal(2))
		Expect(provenance.RetainedPaths).To(Equal([]string{"a.go", "b.go"}))
		Expect(provenance.SourceHash).To(HaveLen(64))
	})

	It("rejects compaction that loses a required path", func() {
		provenance := New("source", "retained", "summary", 5, 3, []string{"a.go"}, []string{"missing.go"}, "evidence_weighted", "context_budget")

		Expect(provenance.Acceptable()).To(BeFalse())
		Expect(provenance.ReconstructionStatus).To(Equal(ReconstructionRejected))
	})
})
