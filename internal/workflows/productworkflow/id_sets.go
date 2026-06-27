package productworkflow

import "strings"

func EvidenceCardIDSet(cards []EvidenceCard) map[string]bool {
	ids := make(map[string]bool, len(cards))
	for _, card := range cards {
		if id := strings.TrimSpace(card.CardID); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func PRDRequirementIDSet(requirements []PRDRequirement) map[string]bool {
	ids := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		if id := strings.TrimSpace(requirement.ID); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func ADRIndexIDSet(index ADRIndex) map[string]bool {
	ids := make(map[string]bool, len(index.ADRs))
	for _, adr := range index.ADRs {
		if id := strings.TrimSpace(adr.ADRID); id != "" {
			ids[id] = true
		}
	}
	return ids
}
