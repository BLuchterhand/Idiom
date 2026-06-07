package localops

import (
	"strconv"
	"strings"
)

func ActivationPacketHasUsefulEvidence(packet ActivationPacketEvidence) bool {
	return ActivationPacketUsefulEvidenceCount(packet) > 0
}

func ActivationPacketUsefulEvidenceCount(packet ActivationPacketEvidence) int {
	total := 0
	for _, count := range []int{packet.CandidateFileCount, packet.CandidateSymbolCount, packet.CandidateTestCount} {
		if count > 0 {
			total += count
		}
	}
	for _, evidence := range packet.UsefulEvidence {
		if count, ok := activationUsefulEvidencePositiveCount(evidence); ok {
			total += count
		}
	}
	return total
}

func activationUsefulEvidencePositiveCount(evidence string) (int, bool) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return 0, false
	}
	for _, separator := range []string{":", "="} {
		_, rawCount, ok := strings.Cut(evidence, separator)
		if !ok {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(rawCount))
		return count, err == nil && count > 0
	}
	return 0, false
}
