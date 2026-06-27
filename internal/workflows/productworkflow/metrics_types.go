package productworkflow

type ProductWorkflowMetrics struct {
	CurrentPhase          string         `json:"current_phase"`
	CurrentState          string         `json:"current_state"`
	StateRevision         int            `json:"state_revision"`
	NextValidActionCount  int            `json:"next_valid_action_count"`
	BlockerCount          int            `json:"blocker_count"`
	OpenObligationCount   int            `json:"open_obligation_count"`
	GateStatusCounts      map[string]int `json:"gate_status_counts"`
	TDDObligationTotal    int            `json:"tdd_obligation_total"`
	TDDStatusCounts       map[string]int `json:"tdd_status_counts"`
	TDDTypeCounts         map[string]int `json:"tdd_type_counts"`
	LedgerRecordCount     int            `json:"ledger_record_count"`
	LedgerAtomRecordCount int            `json:"ledger_atom_record_count"`
	LedgerTypeCounts      map[string]int `json:"ledger_type_counts"`
	ReportTypeCounts      map[string]int `json:"report_type_counts"`
	ReportStatusCounts    map[string]int `json:"report_status_counts"`
	ArtifactPathCount     int            `json:"artifact_path_count"`
	EvidenceCardCount     int            `json:"evidence_card_count"`
	TraceabilityRowCount  int            `json:"traceability_row_count"`
	PRDRequirementCount   int            `json:"prd_requirement_count"`
	ADRCount              int            `json:"adr_count"`
	EvidenceLinkCounts    map[string]int `json:"evidence_link_counts"`
}
