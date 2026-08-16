package workspace

import "idiom/internal/contracts/toolfacts"

func capabilityEvidenceForTool(tool string, status toolfacts.CapabilityEvidenceStatus, source string) *toolfacts.ToolResultFacts {
	return &toolfacts.ToolResultFacts{
		CapabilityEvidence: toolfacts.NormalizeCapabilityEvidence([]toolfacts.CapabilityEvidence{{
			Tool:   tool,
			Status: status,
			Source: source,
		}}),
	}
}

func exposedCapabilityEvidence(tools []string, source string) *toolfacts.ToolResultFacts {
	evidence := make([]toolfacts.CapabilityEvidence, 0, len(tools))
	for _, tool := range tools {
		evidence = append(evidence, toolfacts.CapabilityEvidence{
			Tool:   tool,
			Status: toolfacts.CapabilityEvidenceExposed,
			Source: source,
		})
	}

	normalized := toolfacts.NormalizeCapabilityEvidence(evidence)
	if len(normalized) == 0 {
		return nil
	}

	return &toolfacts.ToolResultFacts{CapabilityEvidence: normalized}
}
