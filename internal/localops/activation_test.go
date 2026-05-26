package localops

import "testing"

func TestActivationPacketUsefulEvidenceCountCombinesStructuredAndFallbackCounts(t *testing.T) {
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
	if got := ActivationPacketUsefulEvidenceCount(packet); got != 6 {
		t.Fatalf("useful evidence count = %d, want 6", got)
	}
	if !ActivationPacketHasUsefulEvidence(packet) {
		t.Fatalf("packet with positive evidence should be useful")
	}
	if ActivationPacketHasUsefulEvidence(ActivationPacketEvidence{UsefulEvidence: []string{"candidate_files:0"}}) {
		t.Fatalf("zero-only evidence should not be useful")
	}
}
