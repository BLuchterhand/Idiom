package localops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Activation packet evidence", func() {
	It("combines structured and fallback evidence counts", func() {
		packet := ActivationPacketEvidence{
			CandidateFileCount:   2,
			CandidateSymbolCount: 1,
			CandidateTestCount:   -1,
			UsefulEvidence: []string{
				"candidate_files:3",
				"candidate_symbols=0",
				"candidate_tests=bad",
				"other",
			},
		}
		Expect(ActivationPacketUsefulEvidenceCount(packet)).To(Equal(6))
		Expect(ActivationPacketHasUsefulEvidence(packet)).To(BeTrue())
		Expect(ActivationPacketHasUsefulEvidence(ActivationPacketEvidence{
			UsefulEvidence: []string{"candidate_files:0"},
		})).To(BeFalse())
	})
})
