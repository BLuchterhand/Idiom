package toolfacts

// DiscoveryStrength describes how strongly a discovery tool localized a source
// owner before any source-body read.
type DiscoveryStrength string

const (
	// DiscoveryStrengthCandidateOnly means the result returned candidate paths
	// but did not establish a source owner.
	DiscoveryStrengthCandidateOnly DiscoveryStrength = "candidate_only"
	// DiscoveryStrengthSourceOwner means the result established a source owner
	// that should be consumed with source-body evidence before broad rediscovery.
	DiscoveryStrengthSourceOwner DiscoveryStrength = "source_owner"
)

// ToolResultFacts carries producer-owned evidence facts that consumers should
// not re-derive from model-facing result text.
type ToolResultFacts struct {
	DiagnosticEvidencePaths []string          `json:"diagnostic_evidence_paths,omitempty"`
	CandidatePaths          []string          `json:"candidate_paths,omitempty"`
	CandidateSymbols        []string          `json:"candidate_symbols,omitempty"`
	HasDiscoveryTargets     bool              `json:"has_discovery_targets,omitempty"`
	DiscoveryStrength       DiscoveryStrength `json:"discovery_strength,omitempty"`
	SourceOwnerFound        bool              `json:"source_owner_found,omitempty"`
	LocalizationConfidence  string            `json:"localization_confidence,omitempty"`
}
