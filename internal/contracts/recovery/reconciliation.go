package recovery

const ReconciliationSchemaVersion = "idiom.recovery.reconciliation.v1"

const (
	ReconciliationStatusPartial   = "partial"
	ReconciliationStatusRecovered = "recovered"
)

const (
	ReconciliationVerificationPass        = "pass"
	ReconciliationVerificationFail        = "fail"
	ReconciliationVerificationNotRecorded = "not_recorded"
)

type Reconciliation struct {
	SchemaVersion   string                     `json:"schema_version"`
	FailureID       string                     `json:"failure_id"`
	PacketID        string                     `json:"packet_id"`
	PacketDigest    string                     `json:"packet_digest"`
	CreatedAt       string                     `json:"created_at"`
	UpdatedAt       string                     `json:"updated_at"`
	LockedContract  *ContractSnapshot          `json:"locked_contract,omitempty"`
	AuthorityDeltas []ScopeAuthorityDelta      `json:"authority_deltas,omitempty"`
	Status          string                     `json:"status"`
	CompletedAtoms  []ReconciledAtom           `json:"completed_atoms,omitempty"`
	RemainingAtoms  []string                   `json:"remaining_atoms,omitempty"`
	Blockers        []string                   `json:"blockers,omitempty"`
	Verification    ReconciliationVerification `json:"verification"`
	NextAction      NextAction                 `json:"next_valid_action"`
}

type ReconciledAtom struct {
	ID              string   `json:"id"`
	Title           string   `json:"title,omitempty"`
	ResidualWorkIDs []string `json:"residual_work_ids,omitempty"`
	RequiredPaths   []string `json:"required_paths,omitempty"`
	RequiredTests   []string `json:"required_tests,omitempty"`
	InvariantIDs    []string `json:"invariant_ids,omitempty"`
}

type ScopeAuthorityDelta struct {
	DeltaID           string   `json:"delta_id"`
	ContractID        string   `json:"contract_id,omitempty"`
	ContractHash      string   `json:"contract_hash,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	RequiredEditPaths []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths  []string `json:"allowed_edit_paths,omitempty"`
}

type ReconciliationVerification struct {
	Status        string                        `json:"status"`
	RequiredTests []string                      `json:"required_tests,omitempty"`
	Commands      []ReconciliationCommandResult `json:"commands,omitempty"`
}

type ReconciliationCommandResult struct {
	Command     string `json:"command"`
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type ReconciliationValidationFinding struct {
	Code    string
	Message string
}
