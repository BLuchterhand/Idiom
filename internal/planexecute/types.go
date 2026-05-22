package planexecute

import (
	"fmt"
	"io"
	"strings"

	"idiom/internal/config"
	"idiom/internal/telemetry"
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
	ScopeContract               PlanScopeContract
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

type Options struct {
	ConfigPath               string
	Prompt                   string
	PromptFile               string
	Model                    string
	Resume                   string
	ReviewSessionID          string
	JSONEvents               bool
	DisableThinking          bool
	UseActiveModel           bool
	MaxTokens                int
	RequiredEditPaths        MultiFlag
	AllowedEditPaths         MultiFlag
	EditSetObligations       EditSetObligations
	ScopeContract            PlanScopeContract
	PlannerOwnedVerification bool
	AllowBuildTests          bool
	SkipReviewRepair         bool
	SessionID                string
	OnPhase                  PhaseFunc
	OnEditProgress           EditProgressFunc
}

const (
	ExecutorHandoffStateInherited  = "inherited"
	ExecutorHandoffStateMissing    = "missing"
	ExecutorHandoffStateStale      = "stale"
	ExecutorHandoffStateOverridden = "overridden"
)

type ExecutorHandoffPacket struct {
	State                   string
	Reason                  string
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
	ApprovedPathCount       int
	PendingPathCount        int
}

type ExecutorPhaseReport struct {
	Total        int
	Succeeded    int
	Failed       int
	Failures     []string
	SourceSanity []PhaseEvidenceCheck
	Verification []PhaseEvidenceCheck
	Handoff      ExecutorHandoffPacket
}

func ExecutorSourceSanityFailed(report ExecutorPhaseReport, err error) bool {
	if err == nil || report.Total == 0 || report.Succeeded != report.Total || report.Failed != 0 || len(report.Failures) > 0 {
		return false
	}
	hasFailedSourceSanity := false
	for _, check := range report.SourceSanity {
		status := strings.ToLower(strings.TrimSpace(check.Status))
		if strings.TrimSpace(check.Error) != "" || (status != "" && status != "ok" && status != "passed") {
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
		status := strings.ToLower(strings.TrimSpace(check.Status))
		if strings.TrimSpace(check.Error) != "" || (status != "" && status != "ok" && status != "passed") {
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

type PhaseEvidenceCheck struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ReviewRepairPhaseReport struct {
	Skipped        bool
	Status         string
	Repairs        int
	AutoRepairs    int
	ReviewPlan     string
	ChecklistFound bool
	ReasonClass    string
}

func (r ReviewRepairPhaseReport) String() string {
	if r.Skipped {
		return "review-repair: skipped"
	}
	status := strings.TrimSpace(r.Status)
	if status == "" {
		status = "not run"
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
	return strings.TrimSpace(report.Status) == "repaired"
}

type ReviewRepairEvidence struct {
	Diff          string
	DiffCheck     string
	DiffCheckExit int
	PhaseEvidence PhaseEvidenceBundle
}

const PhaseEvidenceSchemaVersion = "idiom.phase_evidence.v1"

type PhaseEvidenceBundle struct {
	SchemaVersion      string                 `json:"schema_version"`
	Phase              string                 `json:"phase"`
	GeneratedAt        string                 `json:"generated_at"`
	ChangedPaths       []string               `json:"changed_paths,omitempty"`
	RequiredPaths      []string               `json:"required_paths,omitempty"`
	AllowedPaths       []string               `json:"allowed_paths,omitempty"`
	EditSetObligations EditSetObligations     `json:"edit_set_obligations,omitempty"`
	Packet             PhaseEvidencePacket    `json:"packet"`
	ExecutorHandoff    phaseEvidenceHandoff   `json:"executor_handoff"`
	SubmittedPlan      []PhaseEvidenceCheck   `json:"submitted_plan_state,omitempty"`
	SourceSanity       []PhaseEvidenceCheck   `json:"source_sanity,omitempty"`
	Verification       []PhaseEvidenceCheck   `json:"verification,omitempty"`
	PatchHygiene       PhaseEvidenceCheck     `json:"patch_hygiene"`
	SuggestedTests     []PhaseEvidenceTest    `json:"suggested_tests,omitempty"`
	UnresolvedGaps     []string               `json:"unresolved_gaps,omitempty"`
	OpenObligations    []string               `json:"open_obligations,omitempty"`
	DecisionEvidence   []phaseEvidenceSummary `json:"decision_evidence,omitempty"`
}

type PhaseEvidencePacket struct {
	PackID           string                `json:"pack_id,omitempty"`
	Status           string                `json:"status"`
	Error            string                `json:"error,omitempty"`
	IndexID          string                `json:"index_id,omitempty"`
	Fresh            bool                  `json:"fresh,omitempty"`
	Confidence       string                `json:"confidence,omitempty"`
	StoreKind        string                `json:"store_kind,omitempty"`
	CandidateFiles   []PhaseEvidenceFile   `json:"candidate_files,omitempty"`
	CandidateSymbols []PhaseEvidenceSymbol `json:"candidate_symbols,omitempty"`
	SuggestedTests   []PhaseEvidenceTest   `json:"suggested_tests,omitempty"`
	UnresolvedGaps   []string              `json:"unresolved_gaps,omitempty"`
}

type phaseEvidenceHandoff struct {
	PacketState      string `json:"packet_state"`
	Reason           string `json:"reason,omitempty"`
	PlanID           string `json:"plan_id,omitempty"`
	HandoffID        string `json:"handoff_id,omitempty"`
	HandoffStatus    string `json:"handoff_status,omitempty"`
	PackID           string `json:"pack_id,omitempty"`
	DeclaredPackID   string `json:"declared_pack_id,omitempty"`
	IndexID          string `json:"index_id,omitempty"`
	Fresh            bool   `json:"fresh,omitempty"`
	CandidateFiles   int    `json:"candidate_files,omitempty"`
	CandidateSymbols int    `json:"candidate_symbols,omitempty"`
	Tests            int    `json:"tests,omitempty"`
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
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type PlanEditPreflightMismatch struct {
	Index  int
	Path   string
	Reason string
}
