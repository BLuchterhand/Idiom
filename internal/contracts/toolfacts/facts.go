package toolfacts

// ToolResultFacts carries producer-owned evidence facts that consumers should
// not re-derive from model-facing result text.
type ToolResultFacts struct {
	DiagnosticEvidencePaths []string `json:"diagnostic_evidence_paths,omitempty"`
	CandidatePaths          []string `json:"candidate_paths,omitempty"`
	CandidateSymbols        []string `json:"candidate_symbols,omitempty"`
	HasDiscoveryTargets     bool     `json:"has_discovery_targets,omitempty"`
	SourceOwnerFound        bool     `json:"source_owner_found,omitempty"`
	LocalizationConfidence  string   `json:"localization_confidence,omitempty"`
}
