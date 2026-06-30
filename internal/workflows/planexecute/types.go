package planexecute

import (
	"context"
	"fmt"
	"io"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
)

type PromptRunner func(config.Config, string, RunOptions, io.Writer) (string, error)

type RunOptions struct {
	JSONEvents                  bool
	Resume                      string
	TaskFamily                  string
	PlannerOwnedVerification    bool
	MaxTokensOverride           int
	DisableThinking             bool
	ReviewRepairOpenObligations []string
	UseActiveModel              bool
	Telemetry                   *telemetry.Recorder
	ConfigSourcePath            string
	ScopeContract               plancontract.PlanScopeContract
	AuthorizedNewFiles          []string
}

type MultiFlag []string

func (m *MultiFlag) String() string {
	if m == nil {
		return ""
	}

	return strings.Join(*m, ",")
}

func (m *MultiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

type PhaseFunc func(phase, detail string)

type EditProgressFunc func(step, total int, path string, succeeded bool)

type PlanApprovalPhase string

const (
	PlanApprovalPhaseInitial      PlanApprovalPhase = "initial"
	PlanApprovalPhaseReviewRepair PlanApprovalPhase = "review_repair"
)

type PlanApprovalRequest struct {
	Phase                   PlanApprovalPhase
	PlanPath                string
	Plan                    planartifact.PlanArtifact
	Paths                   []string
	PreviouslyApprovedPaths []string
}

type PlanApprovalDecision struct {
	Approved      bool
	Blanket       bool
	ApprovedPaths []string
	ApprovedBy    string
	Reason        string
}

type PlanApprovalFunc func(context.Context, PlanApprovalRequest) PlanApprovalDecision

type ScopeExtractionPolicy string

const (
	ScopeExtractionFromPrompt ScopeExtractionPolicy = "from_prompt"
	ScopeExtractionFrozen     ScopeExtractionPolicy = "frozen"
)

type Options struct {
	Context                          context.Context
	ConfigPath                       string
	Prompt                           string
	PromptFile                       string
	Model                            string
	Resume                           string
	ReviewSessionID                  string
	JSONEvents                       bool
	DisableThinking                  bool
	UseActiveModel                   bool
	MaxTokens                        int
	RequiredEditPaths                MultiFlag
	AllowedEditPaths                 MultiFlag
	WriteAllowedPaths                MultiFlag
	WriteAllowedPathsExplicit        bool
	EditSetObligations               plancontract.EditSetObligations
	ScopeContract                    plancontract.PlanScopeContract
	ScopeExtractionPolicy            ScopeExtractionPolicy
	AuthorizedNewFiles               []string
	VerificationPlanExplicitCommands []string
	PlannerOwnedVerification         bool
	AllowBuildTests                  bool
	SkipReviewRepair                 bool
	SessionID                        string
	OnPhase                          PhaseFunc
	OnEditProgress                   EditProgressFunc
	OnPlanApproval                   PlanApprovalFunc
}

type ExecutorHandoffPacket struct {
	State                   plancontract.ExecutorHandoffState
	Reason                  string
	HandoffCreated          bool
	PacketPresent           bool
	PacketNotUsedByDesign   bool
	PacketFreshnessState    plancontract.ExecutorPacketFreshnessState
	PlanID                  string
	HandoffID               string
	HandoffStatus           string
	ApprovalScopeID         string
	ApprovalStatus          string
	ApprovalPermission      string
	PackID                  string
	DeclaredPackID          string
	ArtifactPacketStatus    string
	ArtifactPacketFreshness string
	IndexID                 string
	Fresh                   bool
	CandidateFiles          int
	CandidateSymbols        int
	Tests                   int
	Quality                 PackQuality
	ApprovedPathCount       int
	PendingPathCount        int
}

type PackQuality struct {
	LocalizationConfidence string   `json:"localization_confidence"`
	SourceOwnerFound       bool     `json:"source_owner_found"`
	EvidencePaths          []string `json:"evidence_paths"`
	Reasons                []string `json:"reasons"`
}

type ExecutorPhaseReport struct {
	Total                int
	Succeeded            int
	Failed               int
	Failures             []string
	SourceSanity         []plancontract.PhaseEvidenceCheck
	Verification         []plancontract.PhaseEvidenceCheck
	VerificationCommands []plancontract.VerificationCommand
	VerificationSkips    []plancontract.VerificationSkip
	VerificationRejects  []plancontract.VerificationReject
	Handoff              ExecutorHandoffPacket
}

type ExecutorPhaseOptions struct {
	Context              context.Context
	RequiredEditPaths    []string
	AllowedEditPaths     []string
	WriteAllowedPaths    []string
	WriteAllowedExplicit bool
	AuthorizedNewFiles   []string
	VerificationCommands []string
	VerificationPlan     *VerificationPlan
	JSONEvents           bool
	Stdout               io.Writer
	Progress             []EditProgressFunc
}

type ExecutorScope struct {
	RequiredEditPaths    []string
	AllowedEditPaths     []string
	WriteAllowedPaths    []string
	WriteAllowedExplicit bool
	AuthorizedNewFiles   []string
}

func ExecutorScopeFromOptions(opts ExecutorPhaseOptions) ExecutorScope {
	return ExecutorScope{
		RequiredEditPaths:    append([]string(nil), opts.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), opts.AllowedEditPaths...),
		WriteAllowedPaths:    append([]string(nil), opts.WriteAllowedPaths...),
		WriteAllowedExplicit: opts.WriteAllowedExplicit,
		AuthorizedNewFiles:   append([]string(nil), opts.AuthorizedNewFiles...),
	}
}

func ExecutorSourceSanityFailed(report ExecutorPhaseReport, err error) bool {
	if err == nil || report.Total == 0 || report.Succeeded != report.Total || report.Failed != 0 || len(report.Failures) > 0 {
		return false
	}

	hasFailedSourceSanity := false

	for _, check := range report.SourceSanity {
		if strings.TrimSpace(check.Error) != "" || (check.Status != "" && !plancontract.PhaseEvidenceStatusIsPassed(check.Status)) {
			hasFailedSourceSanity = true
			break
		}
	}

	if !hasFailedSourceSanity {
		return false
	}

	return strings.Contains(strings.ToLower(err.Error()), "source_sanity failed")
}

func ExecutorVerificationFailed(report ExecutorPhaseReport, err error) bool {
	if err == nil || report.Total == 0 || report.Succeeded != report.Total || report.Failed != 0 || len(report.Failures) > 0 {
		return false
	}

	for _, check := range report.Verification {
		if strings.TrimSpace(check.Error) != "" || (check.Status != "" && !plancontract.PhaseEvidenceStatusIsPassed(check.Status)) {
			return strings.Contains(strings.ToLower(err.Error()), "verification failed")
		}
	}

	return false
}

func (r ExecutorPhaseReport) String() string {
	header := fmt.Sprintf("executor: %d edits, %d succeeded, %d failed", r.Total, r.Succeeded, r.Failed)
	if len(r.Failures) == 0 {
		return header
	}

	return header + "\n  " + strings.Join(r.Failures, "\n  ")
}

type ReviewRepairFileSnapshot struct {
	Before string `json:"before"`
	Exists bool   `json:"exists"`
}

type ReviewRepairBaseline struct {
	Files map[string]ReviewRepairFileSnapshot `json:"files,omitempty"`
}

type PatchHygieneAutoRepairReport struct {
	Repaired bool
	Paths    []string
	Lines    int
}

type ReviewRepairPhaseReport struct {
	Skipped              bool
	Status               plancontract.PlanExecuteReviewStatus
	StatusDetail         string
	FailureKind          plancontract.PlanExecuteFailureKind
	FailureReason        string
	Repairs              int
	AutoRepairs          int
	ReviewPlan           string
	RepairPaths          []string
	CombinedChangedPaths []string
	ChecklistFound       bool
	ReasonClass          string
	SourceSanity         []plancontract.PhaseEvidenceCheck
	Verification         []plancontract.PhaseEvidenceCheck
	VerificationCommands []plancontract.VerificationCommand
	VerificationSkips    []plancontract.VerificationSkip
	VerificationRejects  []plancontract.VerificationReject
}

func (r ReviewRepairPhaseReport) String() string {
	if r.Skipped {
		return "review-repair: skipped"
	}

	status := strings.TrimSpace(string(r.Status))
	if status == "" {
		status = "not run"
	}
	if detail := strings.TrimSpace(r.StatusDetail); detail != "" {
		status += " (" + detail + ")"
	}

	if r.Repairs == 0 {
		if r.AutoRepairs == 0 {
			return fmt.Sprintf("review-repair: %s", status)
		}

		return fmt.Sprintf("review-repair: %s, %d mechanical repair(s)", status, r.AutoRepairs)
	}

	if r.AutoRepairs == 0 {
		return fmt.Sprintf("review-repair: %s, %d corrective edit(s)", status, r.Repairs)
	}

	return fmt.Sprintf("review-repair: %s, %d corrective edit(s), %d mechanical repair(s)", status, r.Repairs, r.AutoRepairs)
}

func ReviewRepairResolvedVerificationFailure(report ReviewRepairPhaseReport) bool {
	return report.Status == plancontract.PlanExecuteReviewStatusRepaired
}

type ReviewRepairEvidence struct {
	Diff          string
	DiffCheck     string
	DiffCheckExit int
	PhaseEvidence PhaseEvidenceBundle
}

const PhaseEvidenceSchemaVersion = "idiom.phase_evidence.v1"

type PhaseEvidencePhase string
type PhaseEvidencePacketStatus string
type PhaseEvidenceSummaryKind string

const (
	PhaseEvidencePhaseUnknown      PhaseEvidencePhase = "unknown"
	PhaseEvidencePhasePlanning     PhaseEvidencePhase = PhaseEvidencePhase(plancontract.PlanExecuteProgressPhasePlanning)
	PhaseEvidencePhaseExecutor     PhaseEvidencePhase = PhaseEvidencePhase(plancontract.PlanExecuteProgressPhaseExecutor)
	PhaseEvidencePhaseReviewRepair PhaseEvidencePhase = PhaseEvidencePhase(plancontract.PlanExecuteProgressPhaseReviewRepair)
	PhaseEvidencePhaseComplete     PhaseEvidencePhase = PhaseEvidencePhase(plancontract.PlanExecuteProgressPhaseComplete)

	PhaseEvidencePacketStatusNotProvided    PhaseEvidencePacketStatus = "not_provided"
	PhaseEvidencePacketStatusInvalidPackID  PhaseEvidencePacketStatus = "invalid_pack_id"
	PhaseEvidencePacketStatusError          PhaseEvidencePacketStatus = "error"
	PhaseEvidencePacketStatusAvailable      PhaseEvidencePacketStatus = "available"
	PhaseEvidencePacketStatusAvailableStale PhaseEvidencePacketStatus = "available_stale"
	PhaseEvidencePacketStatusMiss           PhaseEvidencePacketStatus = "miss"

	PhaseEvidenceSummaryKindContextPacket PhaseEvidenceSummaryKind = "context_packet"
	PhaseEvidenceSummaryKindPatchHygiene  PhaseEvidenceSummaryKind = "patch_hygiene"
	PhaseEvidenceSummaryKindSubmittedPlan PhaseEvidenceSummaryKind = "submitted_plan"
	PhaseEvidenceSummaryKindSourceSanity  PhaseEvidenceSummaryKind = "source_sanity"
	PhaseEvidenceSummaryKindVerification  PhaseEvidenceSummaryKind = "verification"
)

type PhaseEvidenceBundle struct {
	SchemaVersion      string                            `json:"schema_version"`
	Phase              PhaseEvidencePhase                `json:"phase"`
	GeneratedAt        string                            `json:"generated_at"`
	ChangedPaths       []string                          `json:"changed_paths,omitempty"`
	RequiredPaths      []string                          `json:"required_paths,omitempty"`
	AllowedPaths       []string                          `json:"allowed_paths,omitempty"`
	EditSetObligations plancontract.EditSetObligations   `json:"edit_set_obligations,omitempty"`
	Packet             PhaseEvidencePacket               `json:"packet"`
	ExecutorHandoff    phaseEvidenceHandoff              `json:"executor_handoff"`
	SubmittedPlan      []plancontract.PhaseEvidenceCheck `json:"submitted_plan_state,omitempty"`
	SourceSanity       []plancontract.PhaseEvidenceCheck `json:"source_sanity,omitempty"`
	Verification       []plancontract.PhaseEvidenceCheck `json:"verification,omitempty"`
	PatchHygiene       plancontract.PhaseEvidenceCheck   `json:"patch_hygiene"`
	SuggestedTests     []PhaseEvidenceTest               `json:"suggested_tests,omitempty"`
	UnresolvedGaps     []string                          `json:"unresolved_gaps,omitempty"`
	OpenObligations    []string                          `json:"open_obligations,omitempty"`
	DecisionEvidence   []phaseEvidenceSummary            `json:"decision_evidence,omitempty"`
}

type PhaseEvidencePacket struct {
	PackID           string                    `json:"pack_id,omitempty"`
	Status           PhaseEvidencePacketStatus `json:"status"`
	Error            string                    `json:"error,omitempty"`
	IndexID          string                    `json:"index_id,omitempty"`
	Fresh            bool                      `json:"fresh,omitempty"`
	Confidence       string                    `json:"confidence,omitempty"`
	Quality          PackQuality               `json:"quality"`
	StoreKind        string                    `json:"store_kind,omitempty"`
	CandidateFiles   []PhaseEvidenceFile       `json:"candidate_files,omitempty"`
	CandidateSymbols []PhaseEvidenceSymbol     `json:"candidate_symbols,omitempty"`
	SuggestedTests   []PhaseEvidenceTest       `json:"suggested_tests,omitempty"`
	UnresolvedGaps   []string                  `json:"unresolved_gaps,omitempty"`
}

type phaseEvidenceHandoff struct {
	PacketState      plancontract.ExecutorHandoffState         `json:"packet_state"`
	Reason           string                                    `json:"reason,omitempty"`
	HandoffCreated   bool                                      `json:"handoff_created,omitempty"`
	PacketPresent    bool                                      `json:"packet_present,omitempty"`
	PacketNotUsed    bool                                      `json:"packet_not_used_by_design,omitempty"`
	PacketFreshness  plancontract.ExecutorPacketFreshnessState `json:"packet_freshness_state,omitempty"`
	PlanID           string                                    `json:"plan_id,omitempty"`
	HandoffID        string                                    `json:"handoff_id,omitempty"`
	HandoffStatus    planartifact.HandoffStatus                `json:"handoff_status,omitempty"`
	PackID           string                                    `json:"pack_id,omitempty"`
	DeclaredPackID   string                                    `json:"declared_pack_id,omitempty"`
	IndexID          string                                    `json:"index_id,omitempty"`
	Fresh            bool                                      `json:"fresh,omitempty"`
	Quality          PackQuality                               `json:"quality"`
	CandidateFiles   int                                       `json:"candidate_files,omitempty"`
	CandidateSymbols int                                       `json:"candidate_symbols,omitempty"`
	Tests            int                                       `json:"tests,omitempty"`
}

type PhaseEvidenceFile struct {
	Path       string `json:"path"`
	Role       string `json:"role,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	Hub        bool   `json:"hub,omitempty"`
}

type PhaseEvidenceSymbol struct {
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Path       string `json:"path,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
}

type PhaseEvidenceTest struct {
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type phaseEvidenceSummary struct {
	Kind    PhaseEvidenceSummaryKind                `json:"kind"`
	Status  plancontract.PhaseEvidenceSummaryStatus `json:"status"`
	Summary string                                  `json:"summary,omitempty"`
}

type PlanEditPreflightMismatch struct {
	Index  int
	Path   string
	Reason string
}
