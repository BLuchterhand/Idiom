package recovery

import "idiom/internal/contracts/workflowstate"

const DecompositionPacketSchemaVersion = "idiom.recovery.decomposition_packet.v1"

const (
	GateBriefLint      = "brief_lint"
	GateAtomIntake     = "atom_intake"
	GateScope          = "scope_gate"
	GateWorkspaceState = "workspace_state"
	GateRequiredTests  = "required_tests"
)

type GateStatus string

const (
	GateStatusPass  GateStatus = "pass"
	GateStatusBlock GateStatus = "block"
	GateStatusError GateStatus = "error"
)

type PromotionNextAction string

const (
	PromotionNextActionFixGate      PromotionNextAction = "fix_promotion_gate"
	PromotionNextActionDispatchAtom PromotionNextAction = "dispatch_atom"
)

type DecompositionPacket struct {
	SchemaVersion  string            `json:"schema_version"`
	PacketID       string            `json:"packet_id"`
	FailureID      string            `json:"failure_id"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	SourceRun      SourceRun         `json:"source_run"`
	LockedContract *ContractSnapshot `json:"locked_contract,omitempty"`
	Failure        Failure           `json:"failure"`
	PartialState   PartialState      `json:"partial_state"`
	ProductLinks   ProductLinks      `json:"product_links,omitempty"`
	ResidualWork   []ResidualWork    `json:"residual_work,omitempty"`
	CandidateAtoms []CandidateAtom   `json:"candidate_atoms,omitempty"`
	EvidenceRefs   []EvidenceRef     `json:"evidence_refs,omitempty"`
	NextAction     NextAction        `json:"next_valid_action"`
}

type ProductLinks struct {
	PRDPaths    []string `json:"prd_paths,omitempty"`
	ADRPaths    []string `json:"adr_paths,omitempty"`
	TDDPaths    []string `json:"tdd_paths,omitempty"`
	LedgerPaths []string `json:"ledger_paths,omitempty"`
}

type ResidualWork struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	Reason            string             `json:"reason"`
	Paths             []string           `json:"paths,omitempty"`
	EvidenceRefs      []EvidenceRef      `json:"evidence_refs,omitempty"`
	InvariantFailures []InvariantFailure `json:"invariant_failures,omitempty"`
}

type CandidateAtom struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Complexity      string   `json:"complexity"`
	RequiredPaths   []string `json:"required_paths,omitempty"`
	ForbiddenPaths  []string `json:"forbidden_paths,omitempty"`
	RequiredTests   []string `json:"required_tests,omitempty"`
	Gates           []string `json:"gates,omitempty"`
	ResidualWorkIDs []string `json:"residual_work_ids,omitempty"`
	InvariantIDs    []string `json:"invariant_ids,omitempty"`
	ParentAtomID    string   `json:"parent_atom_id,omitempty"`
	OriginalTitle   string   `json:"original_title,omitempty"`
}

type PacketStatus string

const (
	PacketStatusReady           PacketStatus = "ready"
	PacketStatusNeedsInspection PacketStatus = "needs_inspection"
	PacketStatusBlocked         PacketStatus = "blocked"
	PacketStatusPartial         PacketStatus = "partial"
	PacketStatusRecovered       PacketStatus = "recovered"
)

type PacketStatusProjection struct {
	Status                 PacketStatus
	WorkflowID             workflowstate.WorkflowID
	WorkflowState          workflowstate.StateID
	WorkflowTerminalState  workflowstate.TerminalState
	Dispatchable           bool
	NextAction             NextAction
	Blockers               []string
	ResidualWork           []ResidualWork
	CandidateAtoms         []CandidateAtom
	Reconciliation         *Reconciliation
	ReconciliationFindings []ReconciliationValidationFinding
}
