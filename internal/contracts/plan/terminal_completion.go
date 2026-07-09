package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	lanecontract "idiom/internal/contracts/lane"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/sourcekind"
	workflowstatecontract "idiom/internal/contracts/workflowstate"
)

const PlanExecuteTerminalCompletionSchemaVersion = "idiom.plan_execute.terminal_completion.v1"

type PlanExecuteProgressPhase string

const (
	PlanExecuteProgressPhasePlanning     PlanExecuteProgressPhase = "planning"
	PlanExecuteProgressPhaseExecutor     PlanExecuteProgressPhase = "executor"
	PlanExecuteProgressPhaseReviewRepair PlanExecuteProgressPhase = "review_repair"
	PlanExecuteProgressPhaseComplete     PlanExecuteProgressPhase = "complete"
)

type PlanExecuteProgressStatus string

const (
	PlanExecuteProgressStatusRunning   PlanExecuteProgressStatus = "running"
	PlanExecuteProgressStatusCompleted PlanExecuteProgressStatus = "completed"
	PlanExecuteProgressStatusFailed    PlanExecuteProgressStatus = "failed"
	PlanExecuteProgressStatusDegraded  PlanExecuteProgressStatus = "degraded"
	PlanExecuteProgressStatusBlocked   PlanExecuteProgressStatus = "blocked"
	PlanExecuteProgressStatusSkipped   PlanExecuteProgressStatus = "skipped"
)

func PlanExecuteProgressPhases() []PlanExecuteProgressPhase {
	return []PlanExecuteProgressPhase{
		PlanExecuteProgressPhasePlanning,
		PlanExecuteProgressPhaseExecutor,
		PlanExecuteProgressPhaseReviewRepair,
		PlanExecuteProgressPhaseComplete,
	}
}

func ParsePlanExecuteProgressPhase(raw string) (PlanExecuteProgressPhase, bool) {
	phase := PlanExecuteProgressPhase(strings.TrimSpace(raw))
	for _, known := range PlanExecuteProgressPhases() {
		if phase == known {
			return phase, true
		}
	}
	return "", false
}

func PlanExecuteProgressStatuses() []PlanExecuteProgressStatus {
	return []PlanExecuteProgressStatus{
		PlanExecuteProgressStatusRunning,
		PlanExecuteProgressStatusCompleted,
		PlanExecuteProgressStatusFailed,
		PlanExecuteProgressStatusDegraded,
		PlanExecuteProgressStatusBlocked,
		PlanExecuteProgressStatusSkipped,
	}
}

func ParsePlanExecuteProgressStatus(raw string) (PlanExecuteProgressStatus, bool) {
	status := PlanExecuteProgressStatus(strings.TrimSpace(raw))
	for _, known := range PlanExecuteProgressStatuses() {
		if status == known {
			return status, true
		}
	}
	return "", false
}

type PlanExecuteReviewStatus string

const (
	PlanExecuteReviewStatusRunning   PlanExecuteReviewStatus = "running"
	PlanExecuteReviewStatusCompleted PlanExecuteReviewStatus = "completed"
	PlanExecuteReviewStatusPassed    PlanExecuteReviewStatus = "passed"
	PlanExecuteReviewStatusRepaired  PlanExecuteReviewStatus = "repaired"
	PlanExecuteReviewStatusFailed    PlanExecuteReviewStatus = "failed"
	PlanExecuteReviewStatusDegraded  PlanExecuteReviewStatus = "degraded"
	PlanExecuteReviewStatusBlocked   PlanExecuteReviewStatus = "blocked"
	PlanExecuteReviewStatusSkipped   PlanExecuteReviewStatus = "skipped"
)

func PlanExecuteReviewStatuses() []PlanExecuteReviewStatus {
	return []PlanExecuteReviewStatus{
		PlanExecuteReviewStatusRunning,
		PlanExecuteReviewStatusCompleted,
		PlanExecuteReviewStatusPassed,
		PlanExecuteReviewStatusRepaired,
		PlanExecuteReviewStatusFailed,
		PlanExecuteReviewStatusDegraded,
		PlanExecuteReviewStatusBlocked,
		PlanExecuteReviewStatusSkipped,
	}
}

func ParsePlanExecuteReviewStatus(raw string) (PlanExecuteReviewStatus, bool) {
	status := PlanExecuteReviewStatus(strings.TrimSpace(raw))
	for _, known := range PlanExecuteReviewStatuses() {
		if status == known {
			return status, true
		}
	}
	return "", false
}

type PlanExecuteTerminalStatus string

const (
	PlanExecuteTerminalStatusCompleted       PlanExecuteTerminalStatus = "completed"
	PlanExecuteTerminalStatusFailed          PlanExecuteTerminalStatus = "failed"
	PlanExecuteTerminalStatusDegraded        PlanExecuteTerminalStatus = "degraded"
	PlanExecuteTerminalStatusBlocked         PlanExecuteTerminalStatus = "blocked"
	PlanExecuteTerminalStatusSkipped         PlanExecuteTerminalStatus = "skipped"
	PlanExecuteTerminalStatusSourceSatisfied PlanExecuteTerminalStatus = "source-satisfied"
)

const (
	planExecuteTerminalStatusRunning   = string(PlanExecuteProgressStatusRunning)
	planExecuteTerminalStatusComplete  = string(PlanExecuteProgressPhaseComplete)
	planExecuteTerminalStatusFailure   = "failure"
	planExecuteTerminalStatusSuccess   = "success"
	planExecuteTerminalStatusSucceeded = "succeeded"

	planExecuteReviewStatusPassed = "passed"
	planExecuteReviewStatusOK     = "ok"

	planExecuteTerminalOutcomeReviewDegraded = "review-degraded"
	planExecuteTerminalClassRecoveryRequired = "recovery-required"
	planExecuteTerminalNextActionNone        = "none"
)

func PlanExecuteTerminalStatusIsTerminal(status string) bool {
	status = canonicalTerminalToken(status)
	switch status {
	case string(PlanExecuteTerminalStatusCompleted),
		planExecuteTerminalStatusSuccess,
		planExecuteTerminalStatusSucceeded,
		string(PlanExecuteTerminalStatusSkipped),
		string(PlanExecuteTerminalStatusSourceSatisfied),
		string(PlanExecuteTerminalStatusFailed),
		planExecuteTerminalStatusFailure,
		string(PlanExecuteTerminalStatusBlocked):
		return true
	default:
		return PlanExecuteTerminalStatusContainsBlockingSignal(status) && status != planExecuteTerminalStatusRunning
	}
}

func PlanExecuteTerminalStatusIsSkipped(status string) bool {
	return canonicalTerminalToken(status) == string(PlanExecuteTerminalStatusSkipped)
}

func PlanExecuteTerminalStatusContainsBlockingSignal(status string) bool {
	status = canonicalTerminalToken(status)
	for _, signal := range []string{
		string(PlanExecuteTerminalStatusFailed),
		planExecuteTerminalStatusFailure,
		"model-error",
		"review-model-error",
		"recovery-open",
		string(PlanExecuteTerminalStatusBlocked),
		"blocker",
		string(PlanExecuteTerminalStatusDegraded),
		planExecuteTerminalStatusRunning,
	} {
		if strings.Contains(status, signal) {
			return true
		}
	}

	return false
}

const VerificationRejectUnsupportedCmd = "unsupported_verification_command"

const (
	VerificationCommandSourcePrompt   = "prompt"
	VerificationCommandSourceExplicit = "explicit"
	VerificationCommandSourceInferred = "touched_path"

	VerificationSkipEmptyPath       = "empty_path"
	VerificationSkipAbsolutePath    = "absolute_path"
	VerificationSkipParentTraversal = "parent_traversal"
	VerificationSkipEmptySegment    = "empty_path_segment"
	VerificationSkipVendorPath      = "vendor_path"
	VerificationSkipGeneratedPath   = "generated_path"
	VerificationSkipGeneratedFile   = "generated_file"
	VerificationSkipNonGoPath       = "non_go_path"
	VerificationSkipNoGoModuleRoot  = "no_go_module_root"
)

const (
	PlanExecuteTerminalFieldAcceptanceEvidenceIDs        = "acceptance_evidence_ids"
	PlanExecuteTerminalFieldAllowedEditPaths             = "allowed_edit_paths"
	PlanExecuteTerminalFieldChangedPaths                 = "changed_paths"
	PlanExecuteTerminalFieldCompletionPath               = "completion_path"
	PlanExecuteTerminalFieldContractHash                 = "contract_hash"
	PlanExecuteTerminalFieldContractID                   = "contract_id"
	PlanExecuteTerminalFieldExitCode                     = "exit_code"
	PlanExecuteTerminalFieldFailureID                    = "failure_id"
	PlanExecuteTerminalFieldFailureKind                  = "failure_kind"
	PlanExecuteTerminalFieldFailureReason                = "failure_reason"
	PlanExecuteTerminalFieldNextAction                   = "next_valid_action"
	PlanExecuteTerminalFieldOutcome                      = "outcome"
	PlanExecuteTerminalFieldPacketPath                   = "packet_path"
	PlanExecuteTerminalFieldPhase                        = "phase"
	PlanExecuteTerminalFieldProgressPath                 = "progress_path"
	PlanExecuteTerminalFieldRecordPath                   = "record_path"
	PlanExecuteTerminalFieldRequiredEditPaths            = "required_edit_paths"
	PlanExecuteTerminalFieldReviewRepairSkipped          = "review_repair_skipped"
	PlanExecuteTerminalFieldReviewStatus                 = "review_status"
	PlanExecuteTerminalFieldScopeContractHash            = "scope_contract_hash"
	PlanExecuteTerminalFieldScopeContractID              = "scope_contract_id"
	PlanExecuteTerminalFieldSnapshotHash                 = "snapshot_hash"
	PlanExecuteTerminalFieldSourceSanity                 = "source_sanity"
	PlanExecuteTerminalFieldSourceSatisfiedEvidence      = "source_satisfied_evidence"
	PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths = "source_satisfied_evidence_paths"
	PlanExecuteTerminalFieldStatePath                    = "state_path"
	PlanExecuteTerminalFieldStatus                       = "status"
	PlanExecuteTerminalFieldTerminalClass                = "terminal_class"
	PlanExecuteTerminalFieldTS                           = "ts"
	PlanExecuteTerminalFieldValidationFailures           = "validation_failures"
	PlanExecuteTerminalFieldVerification                 = "verification"
	PlanExecuteTerminalFieldVerificationCommands         = "verification_commands"
	PlanExecuteTerminalFieldVerificationRejects          = "verification_rejects"
	PlanExecuteTerminalFieldVerificationSkips            = "verification_skips"
	PlanExecuteTerminalFieldWriteAllowedPaths            = "write_allowed_paths"
)

type PlanExecuteTerminalClass string

const (
	PlanExecuteTerminalClassSuccess          PlanExecuteTerminalClass = "success"
	PlanExecuteTerminalClassRecoveryRequired PlanExecuteTerminalClass = "recovery_required"
	PlanExecuteTerminalClassFailed           PlanExecuteTerminalClass = "failed"
)

type PlanExecuteTerminalOutcome string

const (
	PlanExecuteTerminalOutcomeCompleted             PlanExecuteTerminalOutcome = "completed"
	PlanExecuteTerminalOutcomeSourceSatisfied       PlanExecuteTerminalOutcome = "source_satisfied"
	PlanExecuteTerminalOutcomeReviewSkippedByDesign PlanExecuteTerminalOutcome = "review_skipped_by_design"
	PlanExecuteTerminalOutcomeReviewDegraded        PlanExecuteTerminalOutcome = "review_degraded"
	PlanExecuteTerminalOutcomeBlocked               PlanExecuteTerminalOutcome = "blocked"
	PlanExecuteTerminalOutcomeFailed                PlanExecuteTerminalOutcome = "failed"
)

type PlanExecuteTerminalDecisionClass string

const (
	PlanExecuteTerminalDecisionCompleted PlanExecuteTerminalDecisionClass = "completed"
	PlanExecuteTerminalDecisionBlocked   PlanExecuteTerminalDecisionClass = "blocked"
)

type PlanExecuteTerminalDecision struct {
	Class  PlanExecuteTerminalDecisionClass
	Reason string
}

type PhaseEvidenceCheck struct {
	Kind       PhaseEvidenceKind   `json:"kind"`
	Path       string              `json:"path,omitempty"`
	Command    string              `json:"command,omitempty"`
	WorkingDir string              `json:"working_dir,omitempty"`
	Status     PhaseEvidenceStatus `json:"status"`
	Summary    string              `json:"summary,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type PhaseEvidenceKind string

const (
	PhaseEvidenceKindSourceSanity  PhaseEvidenceKind = "source_sanity"
	PhaseEvidenceKindVerification  PhaseEvidenceKind = "verification"
	PhaseEvidenceKindPatchHygiene  PhaseEvidenceKind = "git_diff_check"
	PhaseEvidenceKindSubmittedEdit PhaseEvidenceKind = "submitted_edit"
)

func ParsePhaseEvidenceKind(raw string) (PhaseEvidenceKind, bool) {
	kind := PhaseEvidenceKind(strings.TrimSpace(raw))
	if KnownPhaseEvidenceKind(kind) {
		return kind, true
	}

	return "", false
}

func KnownPhaseEvidenceKind(kind PhaseEvidenceKind) bool {
	switch kind {
	case PhaseEvidenceKindSourceSanity,
		PhaseEvidenceKindVerification,
		PhaseEvidenceKindPatchHygiene,
		PhaseEvidenceKindSubmittedEdit:
		return true
	default:
		return false
	}
}

type PhaseEvidenceStatus string

const (
	PhaseEvidenceStatusOK          PhaseEvidenceStatus = "ok"
	PhaseEvidenceStatusPassed      PhaseEvidenceStatus = "passed"
	PhaseEvidenceStatusFailed      PhaseEvidenceStatus = "failed"
	PhaseEvidenceStatusFailure     PhaseEvidenceStatus = "failure"
	PhaseEvidenceStatusError       PhaseEvidenceStatus = "error"
	PhaseEvidenceStatusMissing     PhaseEvidenceStatus = "missing"
	PhaseEvidenceStatusSkipped     PhaseEvidenceStatus = "skipped"
	PhaseEvidenceStatusDegraded    PhaseEvidenceStatus = "degraded"
	PhaseEvidenceStatusBlocked     PhaseEvidenceStatus = "blocked"
	PhaseEvidenceStatusUnavailable PhaseEvidenceStatus = "unavailable"
	PhaseEvidenceStatusUnknown     PhaseEvidenceStatus = "unknown"
	PhaseEvidenceStatusApplied     PhaseEvidenceStatus = "applied"
	PhaseEvidenceStatusPending     PhaseEvidenceStatus = "pending"
)

type PhaseEvidenceSummaryStatus string

const (
	PhaseEvidenceSummaryStatusNotApplicable PhaseEvidenceSummaryStatus = "not_applicable"
	PhaseEvidenceSummaryStatusApplied       PhaseEvidenceSummaryStatus = "applied"
	PhaseEvidenceSummaryStatusOpen          PhaseEvidenceSummaryStatus = "open"
	PhaseEvidenceSummaryStatusPassed        PhaseEvidenceSummaryStatus = "passed"
	PhaseEvidenceSummaryStatusFailed        PhaseEvidenceSummaryStatus = "failed"
)

func ParsePhaseEvidenceStatus(raw string) (PhaseEvidenceStatus, bool) {
	status := PhaseEvidenceStatus(canonicalTerminalToken(raw))
	if KnownPhaseEvidenceStatus(status) {
		return status, true
	}

	return "", false
}

func KnownPhaseEvidenceStatus(status PhaseEvidenceStatus) bool {
	switch status {
	case PhaseEvidenceStatusOK,
		PhaseEvidenceStatusPassed,
		PhaseEvidenceStatusFailed,
		PhaseEvidenceStatusFailure,
		PhaseEvidenceStatusError,
		PhaseEvidenceStatusMissing,
		PhaseEvidenceStatusSkipped,
		PhaseEvidenceStatusDegraded,
		PhaseEvidenceStatusBlocked,
		PhaseEvidenceStatusUnavailable,
		PhaseEvidenceStatusUnknown,
		PhaseEvidenceStatusApplied,
		PhaseEvidenceStatusPending:
		return true
	default:
		return false
	}
}

func PhaseEvidenceStatusIsPassed(status PhaseEvidenceStatus) bool {
	switch parsed, ok := ParsePhaseEvidenceStatus(string(status)); {
	case ok:
		return parsed == PhaseEvidenceStatusOK || parsed == PhaseEvidenceStatusPassed
	default:
		return false
	}
}

type ExecutorHandoffState string

const (
	ExecutorHandoffStateInherited  ExecutorHandoffState = "inherited"
	ExecutorHandoffStateMissing    ExecutorHandoffState = "missing"
	ExecutorHandoffStateStale      ExecutorHandoffState = "stale"
	ExecutorHandoffStateOverridden ExecutorHandoffState = "overridden"
)

type ExecutorPacketFreshnessState string

const (
	ExecutorPacketFreshnessFresh           ExecutorPacketFreshnessState = "fresh"
	ExecutorPacketFreshnessStale           ExecutorPacketFreshnessState = "stale"
	ExecutorPacketFreshnessMissing         ExecutorPacketFreshnessState = "missing"
	ExecutorPacketFreshnessNotUsedByDesign ExecutorPacketFreshnessState = "not_used_by_design"
	ExecutorPacketFreshnessUnknown         ExecutorPacketFreshnessState = "unknown"
)

type VerificationCommand struct {
	Command    string   `json:"command"`
	Argv       []string `json:"argv,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	Source     string   `json:"source,omitempty"`
	Path       string   `json:"path,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type VerificationSkip struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type VerificationReject struct {
	Command string `json:"command,omitempty"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason"`
}

type PlanExecuteRequiredPathCoverage struct {
	Path     string                                `json:"path"`
	Status   PlanExecuteRequiredPathCoverageStatus `json:"status"`
	Evidence string                                `json:"evidence,omitempty"`
}

type PlanExecuteRequiredPathCoverageStatus string

const (
	PlanExecuteRequiredPathCoverageMissing         PlanExecuteRequiredPathCoverageStatus = "missing"
	PlanExecuteRequiredPathCoverageChanged         PlanExecuteRequiredPathCoverageStatus = "changed"
	PlanExecuteRequiredPathCoverageSourceSatisfied PlanExecuteRequiredPathCoverageStatus = "source_satisfied"
)

type PlanExecuteTerminalInvariant struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type PlanExecuteTerminalCompletionRecord struct {
	SchemaVersion                 string                              `json:"schema_version"`
	Type                          string                              `json:"type"`
	TS                            string                              `json:"ts,omitempty"`
	ProgressPath                  string                              `json:"progress_path,omitempty"`
	SessionID                     string                              `json:"session_id,omitempty"`
	Phase                         PlanExecuteProgressPhase            `json:"phase,omitempty"`
	Status                        PlanExecuteTerminalStatus           `json:"status,omitempty"`
	ReviewStatus                  PlanExecuteReviewStatus             `json:"review_status,omitempty"`
	TerminalClass                 PlanExecuteTerminalClass            `json:"terminal_class"`
	Outcome                       PlanExecuteTerminalOutcome          `json:"outcome"`
	WorkflowID                    workflowstatecontract.WorkflowID    `json:"workflow_id,omitempty"`
	WorkflowTerminalState         workflowstatecontract.TerminalState `json:"workflow_terminal_state,omitempty"`
	Dispatchable                  bool                                `json:"dispatchable"`
	ExitCode                      int                                 `json:"exit_code"`
	ReviewRepairSkipped           bool                                `json:"review_repair_skipped,omitempty"`
	FailureKind                   PlanExecuteFailureKind              `json:"failure_kind,omitempty"`
	FailureReason                 string                              `json:"failure_reason,omitempty"`
	FailureID                     string                              `json:"failure_id,omitempty"`
	RecordPath                    string                              `json:"record_path,omitempty"`
	PacketPath                    string                              `json:"packet_path,omitempty"`
	NextAction                    recoverycontract.NextAction         `json:"next_valid_action,omitempty"`
	ContractID                    string                              `json:"contract_id,omitempty"`
	ProtocolHash                  string                              `json:"protocol_hash,omitempty"`
	SnapshotHash                  string                              `json:"snapshot_hash,omitempty"`
	ContractHash                  string                              `json:"contract_hash,omitempty"`
	RequiredEditPaths             []string                            `json:"required_edit_paths,omitempty"`
	AllowedEditPaths              []string                            `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths             []string                            `json:"write_allowed_paths,omitempty"`
	ScopeContractID               string                              `json:"scope_contract_id,omitempty"`
	ScopeContractHash             string                              `json:"scope_contract_hash,omitempty"`
	SourceSatisfiedEvidencePaths  []string                            `json:"source_satisfied_evidence_paths,omitempty"`
	SourceSatisfiedEvidence       []SourceSatisfiedEvidenceRef        `json:"source_satisfied_evidence,omitempty"`
	ChangedPaths                  []string                            `json:"changed_paths,omitempty"`
	RequiredPathCoverage          []PlanExecuteRequiredPathCoverage   `json:"required_path_coverage,omitempty"`
	SourceSanity                  []PhaseEvidenceCheck                `json:"source_sanity,omitempty"`
	VerificationCommands          []VerificationCommand               `json:"verification_commands,omitempty"`
	Verification                  []PhaseEvidenceCheck                `json:"verification,omitempty"`
	VerificationSkips             []VerificationSkip                  `json:"verification_skips,omitempty"`
	VerificationRejects           []VerificationReject                `json:"verification_rejects,omitempty"`
	RequiredAcceptanceEvidence    []AcceptanceEvidenceRequired        `json:"required_acceptance_evidence,omitempty"`
	RequiredAcceptanceEvidenceIDs []string                            `json:"required_acceptance_evidence_ids,omitempty"`
	AcceptanceEvidenceIDs         []string                            `json:"acceptance_evidence_ids,omitempty"`
	AcceptanceEvidenceClaims      []AcceptanceEvidenceClaim           `json:"acceptance_evidence_claims,omitempty"`
	FailureInvariants             []PlanExecuteTerminalInvariant      `json:"failure_invariants,omitempty"`
	ValidationFailures            []string                            `json:"validation_failures,omitempty"`
}

func PlanExecuteTerminalCompletionRecordFromFields(fields map[string]any) PlanExecuteTerminalCompletionRecord {
	if fields == nil {
		return NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{})
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{})
	}
	var record PlanExecuteTerminalCompletionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{})
	}
	if strings.TrimSpace(record.ProgressPath) == "" {
		record.ProgressPath = strings.TrimSpace(fmt.Sprint(fields[PlanExecuteTerminalFieldStatePath]))
	}
	return NormalizePlanExecuteTerminalCompletionRecord(record)
}

func rawPlanExecuteTerminalCompletionRecordFromFields(fields map[string]any) PlanExecuteTerminalCompletionRecord {
	if fields == nil {
		return PlanExecuteTerminalCompletionRecord{}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return PlanExecuteTerminalCompletionRecord{}
	}
	var record PlanExecuteTerminalCompletionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return PlanExecuteTerminalCompletionRecord{}
	}
	if strings.TrimSpace(record.ProgressPath) == "" {
		record.ProgressPath = strings.TrimSpace(fmt.Sprint(fields[PlanExecuteTerminalFieldStatePath]))
	}
	return record
}

func PlanExecuteTerminalCompletionEventFields(record PlanExecuteTerminalCompletionRecord, completionPath string) map[string]any {
	record = NormalizePlanExecuteTerminalCompletionRecord(record)
	body, err := json.Marshal(record)
	if err != nil {
		return map[string]any{}
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return map[string]any{}
	}
	if strings.TrimSpace(completionPath) != "" {
		fields[PlanExecuteTerminalFieldCompletionPath] = strings.TrimSpace(completionPath)
	}
	return fields
}

func NormalizePlanExecuteTerminalCompletionRecord(record PlanExecuteTerminalCompletionRecord) PlanExecuteTerminalCompletionRecord {
	record.SchemaVersion = firstTerminalString(record.SchemaVersion, PlanExecuteTerminalCompletionSchemaVersion)
	record.Type = firstTerminalString(record.Type, PlanExecuteTerminalCompletionRecordType)
	record.TS = strings.TrimSpace(record.TS)
	record.ProgressPath = strings.TrimSpace(record.ProgressPath)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.Phase = PlanExecuteProgressPhase(strings.TrimSpace(string(record.Phase)))
	record.Status = PlanExecuteTerminalStatus(strings.TrimSpace(string(record.Status)))
	record.ReviewStatus = PlanExecuteReviewStatus(strings.TrimSpace(string(record.ReviewStatus)))
	record.FailureKind = PlanExecuteFailureKind(strings.TrimSpace(string(record.FailureKind)))
	record.FailureReason = strings.TrimSpace(record.FailureReason)
	record.FailureID = strings.TrimSpace(record.FailureID)
	record.RecordPath = strings.TrimSpace(record.RecordPath)
	record.PacketPath = strings.TrimSpace(record.PacketPath)
	if action, ok := recoverycontract.ParseNextAction(string(record.NextAction)); ok {
		record.NextAction = action
	} else {
		record.NextAction = recoverycontract.NextAction(strings.TrimSpace(string(record.NextAction)))
	}
	record.ContractID = strings.TrimSpace(record.ContractID)
	record.ProtocolHash = strings.TrimSpace(record.ProtocolHash)
	record.SnapshotHash = strings.TrimSpace(record.SnapshotHash)
	record.ContractHash = strings.TrimSpace(record.ContractHash)
	record.ScopeContractID = strings.TrimSpace(record.ScopeContractID)
	record.ScopeContractHash = strings.TrimSpace(record.ScopeContractHash)
	record.RequiredEditPaths = UniqueScopePaths(record.RequiredEditPaths)
	record.AllowedEditPaths = UniqueScopePaths(record.AllowedEditPaths)
	record.WriteAllowedPaths = UniqueScopePaths(record.WriteAllowedPaths)
	record.SourceSatisfiedEvidencePaths = UniqueScopePaths(record.SourceSatisfiedEvidencePaths)
	record.SourceSatisfiedEvidence = NormalizeSourceSatisfiedEvidenceRefs(record.SourceSatisfiedEvidence)
	record.ChangedPaths = UniqueScopePaths(record.ChangedPaths)
	record.RequiredPathCoverage = planExecuteTerminalRequiredPathCoverage(record.RequiredEditPaths, record.ChangedPaths, record.SourceSatisfiedEvidencePaths)
	record.SourceSanity = normalizeTerminalChecks(record.SourceSanity)
	record.VerificationCommands = normalizeTerminalVerificationCommands(record.VerificationCommands)
	record.Verification = normalizeTerminalChecks(record.Verification)
	record.VerificationSkips = normalizeTerminalVerificationSkips(record.VerificationSkips)
	record.VerificationRejects = normalizeTerminalVerificationRejects(record.VerificationRejects)
	record.RequiredAcceptanceEvidence = normalizeTerminalAcceptanceEvidence(record.RequiredAcceptanceEvidence)
	record.RequiredAcceptanceEvidenceIDs = normalizeTerminalStableIDs(record.RequiredAcceptanceEvidenceIDs)
	for _, obligation := range record.RequiredAcceptanceEvidence {
		if obligation.Required && obligation.ID != "" {
			record.RequiredAcceptanceEvidenceIDs = append(record.RequiredAcceptanceEvidenceIDs, obligation.ID)
		}
	}
	record.RequiredAcceptanceEvidenceIDs = normalizeTerminalStableIDs(record.RequiredAcceptanceEvidenceIDs)
	record.AcceptanceEvidenceIDs = normalizeTerminalStableIDs(record.AcceptanceEvidenceIDs)
	record.AcceptanceEvidenceClaims = normalizeTerminalAcceptanceClaims(record.AcceptanceEvidenceClaims)
	record.TerminalClass, record.Outcome = ClassifyPlanExecuteTerminalCompletion(record)
	record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable = planExecuteTerminalWorkflowProjection(record)
	record.FailureInvariants = planExecuteTerminalFailureInvariants(record)
	if record.TerminalClass == PlanExecuteTerminalClassSuccess {
		record.ValidationFailures = PlanExecuteTerminalCompletionValidationFailures(record)
	} else {
		record.ValidationFailures = uniqueTerminalStrings(record.ValidationFailures)
	}
	return record
}

func ValidatePlanExecuteTerminalCompletionRecord(record PlanExecuteTerminalCompletionRecord) error {
	record = NormalizePlanExecuteTerminalCompletionRecord(record)

	if record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion {
		return fmt.Errorf("unsupported plan-execute terminal completion schema %q", record.SchemaVersion)
	}
	if strings.TrimSpace(string(record.Phase)) != "" {
		if _, ok := ParsePlanExecuteProgressPhase(string(record.Phase)); !ok {
			return fmt.Errorf("invalid plan-execute terminal completion phase %q", record.Phase)
		}
	}
	if strings.TrimSpace(string(record.Status)) != "" {
		if !knownPlanExecuteTerminalStatus(string(record.Status)) {
			return fmt.Errorf("invalid plan-execute terminal completion status %q", record.Status)
		}
	}
	if phase, phaseOK := ParsePlanExecuteProgressPhase(string(record.Phase)); phaseOK {
		if status, statusOK := ParsePlanExecuteProgressStatus(string(record.Status)); statusOK && !planExecuteTerminalPhaseAllowsStatus(phase, status) {
			return fmt.Errorf("invalid plan-execute terminal completion lifecycle phase=%q status=%q", record.Phase, record.Status)
		}
	}
	if strings.TrimSpace(string(record.ReviewStatus)) != "" && !knownPlanExecuteReviewStatus(string(record.ReviewStatus)) {
		return fmt.Errorf("invalid plan-execute terminal completion review status %q", record.ReviewStatus)
	}
	if strings.TrimSpace(string(record.FailureKind)) != "" {
		if _, ok := ParsePlanExecuteFailureKind(string(record.FailureKind)); !ok {
			return fmt.Errorf("invalid plan-execute terminal completion failure kind %q", record.FailureKind)
		}
	}
	if strings.TrimSpace(string(record.NextAction)) != "" {
		if !knownPlanExecuteTerminalNextAction(record.NextAction) {
			return fmt.Errorf("invalid plan-execute terminal completion next action %q", record.NextAction)
		}
	}
	if !knownPlanExecuteTerminalClass(record.TerminalClass) {
		return fmt.Errorf("invalid plan-execute terminal completion terminal class %q", record.TerminalClass)
	}
	if !knownPlanExecuteTerminalOutcome(record.Outcome) {
		return fmt.Errorf("invalid plan-execute terminal completion outcome %q", record.Outcome)
	}
	if err := workflowstatecontract.ValidateTerminalProjection(record.WorkflowID, record.WorkflowTerminalState); err != nil {
		return fmt.Errorf("invalid plan-execute terminal completion workflow projection: %w", err)
	}
	for i, check := range append(append([]PhaseEvidenceCheck(nil), record.SourceSanity...), record.Verification...) {
		if strings.TrimSpace(string(check.Kind)) != "" {
			if _, ok := ParsePhaseEvidenceKind(string(check.Kind)); !ok {
				return fmt.Errorf("invalid plan-execute terminal completion evidence kind %q at check[%d]", check.Kind, i)
			}
		}
		if strings.TrimSpace(string(check.Status)) != "" && !knownPlanExecutePhaseEvidenceStatus(string(check.Status)) {
			return fmt.Errorf("invalid plan-execute terminal completion evidence status %q at check[%d]", check.Status, i)
		}
	}

	return nil
}

func ClassifyPlanExecuteTerminalCompletion(record PlanExecuteTerminalCompletionRecord) (PlanExecuteTerminalClass, PlanExecuteTerminalOutcome) {
	status := canonicalTerminalToken(string(record.Status))
	reviewStatus := canonicalTerminalToken(string(record.ReviewStatus))
	phase := canonicalTerminalToken(string(record.Phase))
	outcome := canonicalTerminalToken(string(record.Outcome))
	terminalClass := canonicalTerminalClassToken(record.TerminalClass)

	if status == string(PlanExecuteTerminalStatusFailed) ||
		status == planExecuteTerminalStatusFailure ||
		reviewStatus == string(PlanExecuteTerminalStatusFailed) ||
		reviewStatus == planExecuteTerminalStatusFailure ||
		terminalClass == string(PlanExecuteTerminalClassFailed) {
		return PlanExecuteTerminalClassFailed, PlanExecuteTerminalOutcomeFailed
	}
	if status == string(PlanExecuteTerminalStatusDegraded) || reviewStatus == string(PlanExecuteTerminalStatusDegraded) || outcome == planExecuteTerminalOutcomeReviewDegraded {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeReviewDegraded
	}
	if terminalClass == planExecuteTerminalClassRecoveryRequired || status == string(PlanExecuteTerminalStatusBlocked) || reviewStatus == string(PlanExecuteTerminalStatusBlocked) || outcome == string(PlanExecuteTerminalOutcomeBlocked) {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
	}
	if status == planExecuteTerminalStatusRunning || phase == planExecuteTerminalStatusRunning || reviewStatus == planExecuteTerminalStatusRunning {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
	}
	if record.ExitCode != 0 || record.FailureKind != "" || record.FailureReason != "" ||
		strings.Contains(outcome, string(PlanExecuteTerminalStatusFailed)) ||
		strings.Contains(outcome, planExecuteTerminalStatusFailure) {
		return PlanExecuteTerminalClassFailed, PlanExecuteTerminalOutcomeFailed
	}
	if outcome == string(PlanExecuteTerminalStatusSourceSatisfied) || status == string(PlanExecuteTerminalStatusSourceSatisfied) {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeSourceSatisfied
	}
	if record.ReviewRepairSkipped || reviewStatus == string(PlanExecuteTerminalStatusSkipped) {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeReviewSkippedByDesign
	}
	if terminalClass == string(PlanExecuteTerminalClassSuccess) {
		return PlanExecuteTerminalClassSuccess, firstTerminalOutcome(record.Outcome, PlanExecuteTerminalOutcomeCompleted)
	}
	if status == string(PlanExecuteTerminalStatusCompleted) || phase == planExecuteTerminalStatusComplete {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeCompleted
	}
	return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
}

func DecidePlanExecuteTerminalCompletion(record PlanExecuteTerminalCompletionRecord, validationFailures []string) PlanExecuteTerminalDecision {
	rawStatus := canonicalTerminalToken(string(record.Status))
	rawReviewStatus := canonicalTerminalToken(string(record.ReviewStatus))
	rawTerminalClass := canonicalTerminalClassToken(record.TerminalClass)
	rawOutcome := canonicalTerminalToken(string(record.Outcome))
	failures := append([]string(nil), validationFailures...)
	failures = append(failures, record.ValidationFailures...)

	if rawStatus == "" && rawReviewStatus == "" && rawTerminalClass == "" && rawOutcome == "" {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "missing required nested plan-execute status"}
	}

	if rawStatus == string(PlanExecuteTerminalStatusSkipped) && !record.ReviewRepairSkipped {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design"}
	}

	if rawReviewStatus == string(PlanExecuteTerminalStatusSkipped) && !record.ReviewRepairSkipped {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute skipped review is not explicitly marked by design"}
	}

	if err := ValidatePlanExecuteTerminalCompletionRecord(record); err != nil {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute terminal record invalid: " + err.Error()}
	}

	record = NormalizePlanExecuteTerminalCompletionRecord(record)
	failures = append(failures, PlanExecuteTerminalEvidenceValidationFailures(record)...)
	if failures := uniqueTerminalStrings(failures); len(failures) > 0 {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute terminal validation failed: " + strings.Join(failures, "; ")}
	}

	if record.TerminalClass == PlanExecuteTerminalClassSuccess {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionCompleted}
	}

	if record.ExitCode != 0 {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute reported non-zero exit code"}
	}

	if record.FailureKind != "" || record.FailureReason != "" {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute reported failure details"}
	}

	switch rawTerminalClass {
	case canonicalTerminalClassToken(PlanExecuteTerminalClassRecoveryRequired), canonicalTerminalClassToken(PlanExecuteTerminalClassFailed):
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute terminal class requires recovery"}
	}

	if PlanExecuteTerminalStatusContainsBlockingSignal(string(record.Status)) ||
		PlanExecuteTerminalStatusContainsBlockingSignal(string(record.Phase)) ||
		PlanExecuteTerminalStatusContainsBlockingSignal(string(record.ReviewStatus)) ||
		PlanExecuteTerminalStatusContainsBlockingSignal(string(record.Outcome)) {

		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute status is not success-shaped"}
	}

	if strings.TrimSpace(string(record.Status)) != "" {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: fmt.Sprintf("ambiguous nested plan-execute status %q", record.Status)}
	}

	return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute status is not success-shaped"}
}

func DecidePlanExecuteTerminalCompletionFields(fields map[string]any, validationFailures []string) PlanExecuteTerminalDecision {
	if planExecuteTerminalFieldsAreStatusOnlySuccess(fields) {
		return PlanExecuteTerminalDecision{Class: PlanExecuteTerminalDecisionBlocked, Reason: "nested plan-execute terminal record missing typed success evidence"}
	}
	return DecidePlanExecuteTerminalCompletion(rawPlanExecuteTerminalCompletionRecordFromFields(fields), validationFailures)
}

func planExecuteTerminalFieldsAreStatusOnlySuccess(fields map[string]any) bool {
	if len(fields) == 0 {
		return false
	}
	status := canonicalTerminalToken(firstTerminalString(terminalFieldString(fields[PlanExecuteTerminalFieldStatus]), terminalFieldString(fields["status"])))
	if status != string(PlanExecuteTerminalStatusCompleted) && status != planExecuteTerminalStatusComplete && status != planExecuteTerminalStatusSuccess && status != planExecuteTerminalStatusSucceeded {
		return false
	}
	for _, key := range []string{
		PlanExecuteTerminalFieldPhase,
		PlanExecuteTerminalFieldOutcome,
		PlanExecuteTerminalFieldTerminalClass,
		PlanExecuteTerminalFieldReviewStatus,
		PlanExecuteTerminalFieldReviewRepairSkipped,
		PlanExecuteTerminalFieldChangedPaths,
		PlanExecuteTerminalFieldSourceSanity,
		PlanExecuteTerminalFieldVerification,
		PlanExecuteTerminalFieldVerificationCommands,
		PlanExecuteTerminalFieldProgressPath,
		PlanExecuteTerminalFieldRecordPath,
		"completion_source",
	} {
		if _, ok := fields[key]; ok {
			return false
		}
	}
	return true
}

func terminalFieldString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func planExecuteTerminalWorkflowProjection(record PlanExecuteTerminalCompletionRecord) (workflowstatecontract.WorkflowID, workflowstatecontract.TerminalState, bool) {
	phase := canonicalTerminalToken(string(record.Phase))
	status := canonicalTerminalToken(string(record.Status))
	reviewStatus := canonicalTerminalToken(string(record.ReviewStatus))
	terminalClass := canonicalTerminalClassToken(record.TerminalClass)
	failedClass := canonicalTerminalClassToken(PlanExecuteTerminalClassFailed)
	recoveryRequiredClass := canonicalTerminalClassToken(PlanExecuteTerminalClassRecoveryRequired)
	successClass := canonicalTerminalClassToken(PlanExecuteTerminalClassSuccess)
	outcome := canonicalTerminalToken(string(record.Outcome))
	failureKind := canonicalTerminalToken(string(record.FailureKind))
	failureReason := strings.TrimSpace(record.FailureReason)
	nextAction := canonicalTerminalToken(string(record.NextAction))
	if nextAction == planExecuteTerminalNextActionNone {
		nextAction = ""
	}

	workflowID := workflowstatecontract.WorkflowPlanExecuteExecutor
	if phase == string(PlanExecuteProgressPhasePlanning) ||
		outcome == string(PlanExecuteTerminalStatusSourceSatisfied) ||
		status == string(PlanExecuteTerminalStatusSourceSatisfied) ||
		planExecutePlannerOwnsTerminalFailureKind(failureKind) {
		workflowID = workflowstatecontract.WorkflowPlanExecutePlanner
	}

	terminalState := workflowstatecontract.TerminalStateBlocked
	hardFailure := record.ExitCode != 0 ||
		terminalClass == failedClass ||
		status == string(PlanExecuteTerminalStatusFailed) ||
		status == planExecuteTerminalStatusFailure ||
		reviewStatus == string(PlanExecuteTerminalStatusFailed) ||
		reviewStatus == planExecuteTerminalStatusFailure
	switch {
	case hardFailure:
		if state, ok := planExecuteTerminalStateForFailureKind(failureKind, failureReason); ok {
			terminalState = state
		}
	case outcome == string(PlanExecuteTerminalStatusSourceSatisfied) || status == string(PlanExecuteTerminalStatusSourceSatisfied):
		terminalState = workflowstatecontract.TerminalStateSourceSatisfied
	case failureKind != "":
		if state, ok := planExecuteTerminalStateForFailureKind(failureKind, failureReason); ok {
			terminalState = state
		} else if terminalClass == recoveryRequiredClass ||
			reviewStatus == string(PlanExecuteTerminalStatusBlocked) ||
			reviewStatus == string(PlanExecuteTerminalStatusDegraded) ||
			nextAction != "" {
			terminalState = workflowstatecontract.TerminalStateRecoveryOpen
		}
	case terminalClass == recoveryRequiredClass ||
		reviewStatus == string(PlanExecuteTerminalStatusBlocked) ||
		reviewStatus == string(PlanExecuteTerminalStatusDegraded) ||
		nextAction != "":
		terminalState = workflowstatecontract.TerminalStateRecoveryOpen
	case status == string(PlanExecuteTerminalStatusCompleted) ||
		status == planExecuteTerminalStatusComplete ||
		phase == planExecuteTerminalStatusComplete ||
		terminalClass == successClass:
		terminalState = workflowstatecontract.TerminalStateComplete
	}

	return workflowID, terminalState, false
}

func knownPlanExecuteTerminalStatus(status string) bool {
	switch canonicalTerminalToken(status) {
	case planExecuteTerminalStatusRunning,
		string(PlanExecuteTerminalStatusCompleted),
		string(PlanExecuteTerminalStatusFailed),
		planExecuteTerminalStatusFailure,
		string(PlanExecuteTerminalStatusDegraded),
		string(PlanExecuteTerminalStatusBlocked),
		string(PlanExecuteTerminalStatusSkipped),
		string(PlanExecuteTerminalStatusSourceSatisfied):
		return true
	default:
		return false
	}
}

func planExecuteTerminalPhaseAllowsStatus(phase PlanExecuteProgressPhase, status PlanExecuteProgressStatus) bool {
	switch phase {
	case PlanExecuteProgressPhasePlanning:
		return status == PlanExecuteProgressStatusFailed
	case PlanExecuteProgressPhaseExecutor:
		return status == PlanExecuteProgressStatusCompleted ||
			status == PlanExecuteProgressStatusFailed ||
			status == PlanExecuteProgressStatusDegraded ||
			status == PlanExecuteProgressStatusBlocked
	case PlanExecuteProgressPhaseReviewRepair:
		return status == PlanExecuteProgressStatusCompleted ||
			status == PlanExecuteProgressStatusFailed ||
			status == PlanExecuteProgressStatusDegraded ||
			status == PlanExecuteProgressStatusBlocked ||
			status == PlanExecuteProgressStatusSkipped
	case PlanExecuteProgressPhaseComplete:
		return status == PlanExecuteProgressStatusCompleted ||
			status == PlanExecuteProgressStatusFailed ||
			status == PlanExecuteProgressStatusDegraded ||
			status == PlanExecuteProgressStatusBlocked ||
			status == PlanExecuteProgressStatusSkipped
	default:
		return false
	}
}

func knownPlanExecuteReviewStatus(status string) bool {
	token := canonicalTerminalToken(status)
	if _, ok := ParsePlanExecuteReviewStatus(token); ok {
		return true
	}
	switch token {
	case planExecuteReviewStatusOK:
		return true
	default:
		return false
	}
}

func knownPlanExecuteTerminalClass(class PlanExecuteTerminalClass) bool {
	switch class {
	case PlanExecuteTerminalClassSuccess, PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalClassFailed:
		return true
	default:
		return false
	}
}

func knownPlanExecuteTerminalOutcome(outcome PlanExecuteTerminalOutcome) bool {
	switch outcome {
	case PlanExecuteTerminalOutcomeCompleted,
		PlanExecuteTerminalOutcomeSourceSatisfied,
		PlanExecuteTerminalOutcomeReviewSkippedByDesign,
		PlanExecuteTerminalOutcomeReviewDegraded,
		PlanExecuteTerminalOutcomeBlocked,
		PlanExecuteTerminalOutcomeFailed:
		return true
	default:
		return false
	}
}

func knownPlanExecuteTerminalNextAction(action recoverycontract.NextAction) bool {
	_, ok := recoverycontract.ParseNextAction(string(action))
	return ok
}

func knownPlanExecutePhaseEvidenceStatus(status string) bool {
	_, ok := ParsePhaseEvidenceStatus(status)
	return ok
}

func planExecutePlannerOwnsTerminalFailureKind(kind string) bool {
	switch kind {
	case canonicalTerminalToken(string(PlanExecuteFailurePlannerBlocked)),
		canonicalTerminalToken(string(PlanExecuteFailurePlannerFailed)),
		canonicalTerminalToken(string(PlanExecuteFailurePlanArtifactMissing)),
		canonicalTerminalToken(string(PlanExecuteFailurePlanArtifactInvalid)),
		canonicalTerminalToken(string(PlanExecuteFailureMissingRequestedTestEdit)),
		canonicalTerminalToken(string(PlanExecuteFailureMissingRequestedImplementationEdit)),
		canonicalTerminalToken(string(PlanExecuteFailureSourceSatisfiedUnsupportedEvidence)),
		canonicalTerminalToken(string(PlanExecuteFailureMissingEditPathWithoutNewFileAuth)):
		return true
	default:
		return false
	}
}

func planExecuteTerminalStateForFailureKind(kind string, reason string) (workflowstatecontract.TerminalState, bool) {
	switch kind {
	case canonicalTerminalToken(string(PlanExecuteFailureMissingEditPathWithoutNewFileAuth)):
		return workflowstatecontract.TerminalStateMissingScope, true
	case canonicalTerminalToken(string(PlanExecuteFailureVerificationFailed)),
		canonicalTerminalToken(string(PlanExecuteFailureSourceSanityFailed)):
		return workflowstatecontract.TerminalStateVerificationFailed, true
	}

	switch {
	case strings.Contains(reason, "old_text not found"):
		return workflowstatecontract.TerminalStateStaleNeedsRebase, true
	default:
		return "", false
	}
}

func PlanExecuteTerminalSourceSanityValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	byPath := map[string]PhaseEvidenceCheck{}
	for _, check := range record.SourceSanity {
		path := NormalizeScopePath(check.Path)
		if path != "" {
			byPath[path] = check
		}
		if !terminalCheckPassed(check) {
			return []string{"source_sanity_failed:" + firstTerminalString(path, "(unknown)")}
		}
		if planExecuteTerminalSourceSanityCheckIsDegraded(path, check) {
			return []string{"source_sanity_degraded:" + firstTerminalString(path, "(unknown)")}
		}
	}
	var failures []string
	for _, path := range record.ChangedPaths {
		if !sourcekind.CoversSourceSanity(path) {
			continue
		}
		if _, ok := byPath[path]; !ok {
			failures = append(failures, "source_sanity_missing:"+path)
		}
	}
	return failures
}

func PlanExecuteTerminalCompletionValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	if record.TerminalClass != PlanExecuteTerminalClassSuccess {
		return nil
	}

	var failures []string
	for _, violation := range ValidateAtomTerminalEvidenceInput(planExecuteTerminalAtomContract(record), planExecuteTerminalEvidenceInput(record)) {
		failure := violation.Code
		if strings.TrimSpace(violation.Path) != "" {
			failure += ":" + violation.Path
		}
		failures = append(failures, failure)
	}
	if record.ExitCode != 0 {
		failures = append(failures, "exit_code_nonzero")
	}
	if strings.TrimSpace(string(record.FailureKind)) != "" {
		failures = append(failures, "failure_kind_present")
	}
	if strings.TrimSpace(record.FailureReason) != "" {
		failures = append(failures, "failure_reason_present")
	}
	for _, coverage := range record.RequiredPathCoverage {
		if coverage.Status == PlanExecuteRequiredPathCoverageMissing {
			failures = append(failures, "required_path_missing:"+coverage.Path)
		}
	}
	if record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied && len(record.ChangedPaths) == 0 {
		failures = append(failures, "changed_paths_missing")
	}
	if record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied {
		failures = append(failures, sourceSatisfiedTerminalValidationFailures(record)...)
	}
	failures = append(failures, PlanExecuteTerminalEvidenceValidationFailures(record)...)
	if failure := planExecuteTerminalReviewValidationFailure(record); failure != "" {
		failures = append(failures, failure)
	}
	return uniqueTerminalStrings(failures)
}

// PlanExecuteTerminalEvidenceValidationFailures derives evidence failures that
// block an otherwise success-shaped plan-execute terminal completion.
func PlanExecuteTerminalEvidenceValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	failures := append([]string(nil), PlanExecuteTerminalSourceSanityValidationFailures(record)...)
	failures = append(failures, verificationTerminalValidationFailures(record)...)
	return uniqueTerminalStrings(failures)
}

func planExecuteTerminalAtomContract(record PlanExecuteTerminalCompletionRecord) AtomExecutionContract {
	allowedEditPaths := append([]string(nil), record.AllowedEditPaths...)
	if len(record.RequiredEditPaths) == 0 && len(record.AllowedEditPaths) == 0 && len(record.WriteAllowedPaths) == 0 && len(record.ChangedPaths) > 0 {
		allowedEditPaths = append(allowedEditPaths, record.ChangedPaths...)
	}
	scope := NewPlanScopeContract(record.RequiredEditPaths, allowedEditPaths, nil)
	if len(record.WriteAllowedPaths) > 0 {
		scope = scope.WithWriteAllowedPaths(record.WriteAllowedPaths)
	}
	atom := AtomExecutionContract{
		AtomID: string(lanecontract.LanePlanExecute),
		Scope:  scope,
		TerminalEvidence: TerminalEvidenceRequirements{
			Explicit:                    true,
			RequireSourceEvidence:       record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied,
			RequireChangedPathCoverage:  record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied,
			RequireRequiredPathCoverage: len(record.RequiredEditPaths) > 0,
			RequireSourceSanity:         len(record.ChangedPaths) > 0 && planExecuteTerminalAnySourceSanityPath(record.ChangedPaths),
			RequireVerification:         len(record.VerificationCommands) > 0,
			RequireReview:               !record.ReviewRepairSkipped && record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied,
		},
		SourceSatisfied: SourceSatisfiedRequirements{
			Explicit:                    true,
			Allowed:                     true,
			RequireCurrentStateEvidence: true,
			RequireRequiredScope:        len(record.RequiredEditPaths) > 0,
			RequireNoChangedPaths:       record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied,
		},
	}
	knownAcceptanceObligations := map[string]struct{}{}
	for _, obligation := range record.RequiredAcceptanceEvidence {
		atom.AcceptanceObligations = append(atom.AcceptanceObligations, obligation)
		if obligation.ID != "" {
			knownAcceptanceObligations[obligation.ID] = struct{}{}
		}
	}
	for _, id := range record.RequiredAcceptanceEvidenceIDs {
		if _, ok := knownAcceptanceObligations[id]; ok {
			continue
		}
		atom.AcceptanceObligations = append(atom.AcceptanceObligations, AcceptanceEvidenceRequired{
			ID:       id,
			Required: true,
			Source:   "planexecute.terminal_completion",
		})
	}
	for _, command := range record.VerificationCommands {
		if strings.TrimSpace(command.Command) != "" {
			atom.RequiredTests = append(atom.RequiredTests, RequiredTest{Command: command.Command, Source: command.Source})
		}
	}
	return atom.Normalized()
}

func planExecuteTerminalEvidenceInput(record PlanExecuteTerminalCompletionRecord) TerminalEvidenceInput {
	requiredTests := make([]RequiredTestEvidence, 0, len(record.Verification))
	for _, check := range record.Verification {
		requiredTests = append(requiredTests, RequiredTestEvidence{
			Command: check.Command,
			Passed:  terminalCheckPassed(check),
		})
	}
	return TerminalEvidenceInput{
		AtomID:                    string(lanecontract.LanePlanExecute),
		TerminalStatus:            string(record.TerminalClass),
		ChangedPaths:              record.ChangedPaths,
		RequiredPathCoveragePaths: planExecuteTerminalCoveredPaths(record.RequiredPathCoverage),
		SourceEvidence:            record.SourceSatisfiedEvidence,
		SourceSatisfied:           record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied,
		SourceSatisfiedPaths:      record.SourceSatisfiedEvidencePaths,
		SourceSanityPassed:        len(PlanExecuteTerminalSourceSanityValidationFailures(record)) == 0,
		VerificationPassed:        len(verificationTerminalValidationFailures(record)) == 0,
		ReviewPassed:              planExecuteTerminalReviewValidationFailure(record) == "",
		RequiredTestResults:       requiredTests,
		AcceptanceEvidenceIDs:     record.AcceptanceEvidenceIDs,
		AcceptanceEvidenceClaims:  record.AcceptanceEvidenceClaims,
	}
}

func planExecuteTerminalCoveredPaths(coverage []PlanExecuteRequiredPathCoverage) []string {
	var paths []string
	for _, item := range coverage {
		if item.Status != PlanExecuteRequiredPathCoverageMissing {
			paths = append(paths, item.Path)
		}
	}
	return UniqueScopePaths(paths)
}

func planExecuteTerminalAnySourceSanityPath(paths []string) bool {
	for _, path := range paths {
		if sourcekind.CoversSourceSanity(path) {
			return true
		}
	}
	return false
}

func planExecuteTerminalReviewValidationFailure(record PlanExecuteTerminalCompletionRecord) string {
	status := canonicalTerminalToken(string(record.ReviewStatus))
	if record.ReviewRepairSkipped || status == "skipped" || record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied {
		return ""
	}
	switch PlanExecuteReviewStatus(status) {
	case PlanExecuteReviewStatusCompleted, PlanExecuteReviewStatusPassed, PlanExecuteReviewStatusRepaired:
		return ""
	}
	return "review_status_missing_or_not_passed"
}

func sourceSatisfiedTerminalValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	refs := make([]SourceSatisfiedEvidenceRef, 0, len(record.SourceSatisfiedEvidence))
	for _, ref := range record.SourceSatisfiedEvidence {
		refs = append(refs, SourceSatisfiedEvidenceRef{Path: ref.Path, Kind: ref.Kind})
	}
	violations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        record.RequiredEditPaths,
		SourceSatisfiedPaths: record.SourceSatisfiedEvidencePaths,
		Evidence:             refs,
		RequireEvidence:      true,
	})
	failures := make([]string, 0, len(violations)+1)
	for _, violation := range violations {
		failures = append(failures, violation.Code+":"+violation.Path)
	}
	if len(record.SourceSatisfiedEvidencePaths) == 0 {
		failures = append(failures, "source_satisfied_evidence_missing")
	}
	if len(record.ChangedPaths) > 0 {
		failures = append(failures, "source_satisfied_changed_paths_present")
	}
	return failures
}

func verificationTerminalValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	var failures []string
	for _, reject := range record.VerificationRejects {
		failures = append(failures, "verification_rejected:"+firstTerminalString(reject.Command, reject.Reason))
	}
	evidenceByCommand := map[string]PhaseEvidenceCheck{}
	for _, check := range record.Verification {
		key := VerificationCommandEvidenceKey(check.Command, nil, check.WorkingDir)
		if key != "" {
			evidenceByCommand[key] = check
		}
		if !terminalCheckPassed(check) {
			failures = append(failures, "verification_failed:"+firstTerminalString(check.Command, "(unknown)"))
		}
	}
	for _, command := range record.VerificationCommands {
		key := VerificationCommandEvidenceKey(command.Command, nil, command.WorkingDir)
		if key == "" {
			failures = append(failures, "verification_command_invalid")
			continue
		}
		check, ok := evidenceByCommand[key]
		if !ok {
			failures = append(failures, "verification_evidence_missing:"+command.Command)
			continue
		}
		if !terminalCheckPassed(check) {
			failures = append(failures, "verification_not_passed:"+command.Command)
		}
	}
	return failures
}

func planExecuteTerminalRequiredPathCoverage(required []string, changed []string, sourceSatisfied []string) []PlanExecuteRequiredPathCoverage {
	changedSet := pathSet(changed)
	sourceSet := pathSet(sourceSatisfied)
	out := make([]PlanExecuteRequiredPathCoverage, 0, len(required))
	for _, path := range UniqueScopePaths(required) {
		coverage := PlanExecuteRequiredPathCoverage{Path: path, Status: PlanExecuteRequiredPathCoverageMissing}
		if _, ok := changedSet[path]; ok {
			coverage.Status = PlanExecuteRequiredPathCoverageChanged
			coverage.Evidence = "changed_paths"
		} else if _, ok := sourceSet[path]; ok {
			coverage.Status = PlanExecuteRequiredPathCoverageSourceSatisfied
			coverage.Evidence = "source_satisfied_evidence_paths"
		}
		out = append(out, coverage)
	}
	return out
}

func pathSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range UniqueScopePaths(paths) {
		out[path] = struct{}{}
	}
	return out
}

func planExecuteTerminalFailureInvariants(record PlanExecuteTerminalCompletionRecord) []PlanExecuteTerminalInvariant {
	return []PlanExecuteTerminalInvariant{
		terminalInvariant("exit_code_zero", record.ExitCode == 0, fmt.Sprintf("exit_code=%d", record.ExitCode)),
		terminalInvariant("no_failure_kind", strings.TrimSpace(string(record.FailureKind)) == "", string(record.FailureKind)),
		terminalInvariant("no_failure_reason", strings.TrimSpace(record.FailureReason) == "", record.FailureReason),
	}
}

func terminalInvariant(code string, passed bool, reason string) PlanExecuteTerminalInvariant {
	status := "passed"
	if !passed {
		status = "failed"
	}
	invariant := PlanExecuteTerminalInvariant{Code: strings.TrimSpace(code), Status: status}
	if !passed {
		invariant.Reason = strings.TrimSpace(reason)
	}
	return invariant
}

func normalizeTerminalChecks(checks []PhaseEvidenceCheck) []PhaseEvidenceCheck {
	out := make([]PhaseEvidenceCheck, 0, len(checks))
	for _, check := range checks {
		check.Kind = PhaseEvidenceKind(strings.TrimSpace(string(check.Kind)))
		check.Path = NormalizeScopePath(check.Path)
		check.Command = strings.TrimSpace(check.Command)
		check.WorkingDir = normalizeTerminalWorkingDir(check.WorkingDir)
		check.Status = PhaseEvidenceStatus(strings.TrimSpace(string(check.Status)))
		check.Summary = strings.TrimSpace(check.Summary)
		check.Error = strings.TrimSpace(check.Error)
		if check.Kind == "" && check.Path == "" && check.Command == "" && check.Status == "" {
			continue
		}
		out = append(out, check)
	}
	return out
}

func normalizeTerminalVerificationCommands(commands []VerificationCommand) []VerificationCommand {
	out := make([]VerificationCommand, 0, len(commands))
	seen := map[string]struct{}{}
	for _, command := range commands {
		command.Argv = NormalizeVerificationArgv(command.Argv)
		if len(command.Argv) == 0 {
			if args, ok := VerificationCommandArgs(command.Command); ok {
				command.Argv = args
			}
		}
		if len(command.Argv) == 0 || !VerificationArgvAllowed(command.Argv) {
			continue
		}
		command.Command = CanonicalVerificationCommand(command.Command, command.Argv)
		command.WorkingDir = normalizeTerminalWorkingDir(command.WorkingDir)
		command.Source = strings.TrimSpace(command.Source)
		command.Path = NormalizeScopePath(command.Path)
		command.Reason = strings.TrimSpace(command.Reason)
		key := VerificationCommandEvidenceKey(command.Command, nil, command.WorkingDir)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, command)
	}
	return out
}

func normalizeTerminalVerificationSkips(skips []VerificationSkip) []VerificationSkip {
	out := make([]VerificationSkip, 0, len(skips))
	for _, skip := range skips {
		skip.Path = NormalizeScopePath(skip.Path)
		skip.Reason = strings.TrimSpace(skip.Reason)
		skip.Detail = strings.TrimSpace(skip.Detail)
		if skip.Reason != "" {
			out = append(out, skip)
		}
	}
	return out
}

func normalizeTerminalVerificationRejects(rejects []VerificationReject) []VerificationReject {
	out := make([]VerificationReject, 0, len(rejects))
	for _, reject := range rejects {
		reject.Command = strings.TrimSpace(reject.Command)
		reject.Source = strings.TrimSpace(reject.Source)
		reject.Reason = strings.TrimSpace(reject.Reason)
		if reject.Reason != "" {
			out = append(out, reject)
		}
	}
	return out
}

func normalizeTerminalAcceptanceEvidence(obligations []AcceptanceEvidenceRequired) []AcceptanceEvidenceRequired {
	out := make([]AcceptanceEvidenceRequired, 0, len(obligations))
	seen := map[string]struct{}{}
	for _, obligation := range obligations {
		obligation.ID = NormalizeStableContractIDPart(obligation.ID)
		obligation.Description = strings.TrimSpace(obligation.Description)
		obligation.Source = strings.TrimSpace(obligation.Source)
		obligation.Threshold = strings.TrimSpace(obligation.Threshold)
		if obligation.ID == "" && obligation.Description == "" && obligation.Threshold == "" {
			continue
		}
		if !obligation.Required {
			obligation.Required = true
		}
		key := obligation.ID + "\x00" + obligation.Description + "\x00" + obligation.Threshold
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, obligation)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID+"\x00"+out[i].Threshold < out[j].ID+"\x00"+out[j].Threshold
	})
	return out
}

func normalizeTerminalAcceptanceClaims(claims []AcceptanceEvidenceClaim) []AcceptanceEvidenceClaim {
	out := make([]AcceptanceEvidenceClaim, 0, len(claims))
	seen := map[string]struct{}{}
	for _, claim := range claims {
		claim.ID = NormalizeStableContractIDPart(claim.ID)
		claim.Value = strings.TrimSpace(claim.Value)
		if claim.ID == "" {
			continue
		}
		if _, ok := seen[claim.ID]; ok {
			continue
		}
		seen[claim.ID] = struct{}{}
		out = append(out, claim)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeTerminalStableIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = NormalizeStableContractIDPart(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeTerminalWorkingDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return ""
	}
	return NormalizeScopePath(value)
}

func terminalCheckPassed(check PhaseEvidenceCheck) bool {
	status := canonicalTerminalToken(string(check.Status))
	return strings.TrimSpace(check.Error) == "" && (status == "ok" || status == "passed")
}

func planExecuteTerminalSourceSanityCheckIsDegraded(path string, check PhaseEvidenceCheck) bool {
	if !sourcekind.CoversSourceSanity(path) || !sourcekind.RequiresConcreteSourceSanityDiagnostics(path) {
		return false
	}
	return sourcekind.IsGenericSourceSanityFallback(check.Summary, check.Error)
}

func canonicalTerminalToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "complete", "success", "successful", "succeeded":
		return "completed"
	default:
		return value
	}
}

func canonicalTerminalClassToken(value PlanExecuteTerminalClass) string {
	return strings.ReplaceAll(strings.TrimSpace(strings.ToLower(string(value))), "_", "-")
}

func firstTerminalString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func firstTerminalOutcome(value PlanExecuteTerminalOutcome, fallback PlanExecuteTerminalOutcome) PlanExecuteTerminalOutcome {
	if strings.TrimSpace(string(value)) != "" {
		return PlanExecuteTerminalOutcome(strings.TrimSpace(string(value)))
	}
	return fallback
}

func uniqueTerminalStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
