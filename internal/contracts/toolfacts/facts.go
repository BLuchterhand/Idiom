package toolfacts

// ToolResultFacts carries producer-owned evidence facts that consumers should
// not re-derive from model-facing result text.
type ToolResultFacts struct {
	DiagnosticEvidencePaths []string `json:"diagnostic_evidence_paths,omitempty"`
}
