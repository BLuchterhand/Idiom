package planexecute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/plan"
)

const (
	PlanExecuteTerminalCompletionSchemaVersion = "idiom.plan_execute.terminal_completion.v1"
	PlanExecuteTerminalCompletionRecordType    = "idiom.plan_execute.terminal_completion"
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

type PlanExecuteTerminalCompletionInput struct {
	ProgressPath                 string
	ExitCode                     int
	SourceSatisfied              bool
	SourceSatisfiedEvidencePaths []string
	SourceSatisfiedEvidence      []plancontract.SourceSatisfiedEvidenceRef
	AcceptanceEvidenceIDs        []string
	FailureID                    string
	RecordPath                   string
	PacketPath                   string
	NextAction                   string
	CompletedAt                  time.Time
}

type PlanExecuteTerminalCompletionRecord struct {
	SchemaVersion                 string                                  `json:"schema_version"`
	Type                          string                                  `json:"type"`
	TS                            string                                  `json:"ts,omitempty"`
	ProgressPath                  string                                  `json:"progress_path,omitempty"`
	SessionID                     string                                  `json:"session_id,omitempty"`
	Phase                         string                                  `json:"phase,omitempty"`
	Status                        string                                  `json:"status,omitempty"`
	ReviewStatus                  string                                  `json:"review_status,omitempty"`
	TerminalClass                 PlanExecuteTerminalClass                `json:"terminal_class"`
	Outcome                       PlanExecuteTerminalOutcome              `json:"outcome"`
	ExitCode                      int                                     `json:"exit_code"`
	ReviewRepairSkipped           bool                                    `json:"review_repair_skipped,omitempty"`
	FailureKind                   string                                  `json:"failure_kind,omitempty"`
	FailureReason                 string                                  `json:"failure_reason,omitempty"`
	FailureID                     string                                  `json:"failure_id,omitempty"`
	RecordPath                    string                                  `json:"record_path,omitempty"`
	PacketPath                    string                                  `json:"packet_path,omitempty"`
	NextAction                    string                                  `json:"next_valid_action,omitempty"`
	ContractID                    string                                  `json:"contract_id,omitempty"`
	SnapshotHash                  string                                  `json:"snapshot_hash,omitempty"`
	ContractHash                  string                                  `json:"contract_hash,omitempty"`
	RequiredEditPaths             []string                                `json:"required_edit_paths,omitempty"`
	AllowedEditPaths              []string                                `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths             []string                                `json:"write_allowed_paths,omitempty"`
	ScopeContractID               string                                  `json:"scope_contract_id,omitempty"`
	ScopeContractHash             string                                  `json:"scope_contract_hash,omitempty"`
	SourceSatisfiedEvidencePaths  []string                                `json:"source_satisfied_evidence_paths,omitempty"`
	SourceSatisfiedEvidence       []PlanExecuteSourceSatisfiedEvidenceRef `json:"source_satisfied_evidence,omitempty"`
	ChangedPaths                  []string                                `json:"changed_paths,omitempty"`
	RequiredPathCoverage          []PlanExecuteRequiredPathCoverage       `json:"required_path_coverage,omitempty"`
	SourceSanity                  []PhaseEvidenceCheck                    `json:"source_sanity,omitempty"`
	VerificationCommands          []VerificationCommand                   `json:"verification_commands,omitempty"`
	Verification                  []PhaseEvidenceCheck                    `json:"verification,omitempty"`
	VerificationSkips             []VerificationSkip                      `json:"verification_skips,omitempty"`
	VerificationRejects           []VerificationReject                    `json:"verification_rejects,omitempty"`
	RequiredAcceptanceEvidenceIDs []string                                `json:"required_acceptance_evidence_ids,omitempty"`
	AcceptanceEvidenceIDs         []string                                `json:"acceptance_evidence_ids,omitempty"`
	FailureInvariants             []PlanExecuteTerminalInvariant          `json:"failure_invariants,omitempty"`
	ValidationFailures            []string                                `json:"validation_failures,omitempty"`
}

type PlanExecuteSourceSatisfiedEvidenceRef struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type PlanExecuteRequiredPathCoverage struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type PlanExecuteTerminalInvariant struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func NewPlanExecuteTerminalCompletionRecord(state ProgressState, input PlanExecuteTerminalCompletionInput) PlanExecuteTerminalCompletionRecord {
	acceptanceEvidenceIDs := input.AcceptanceEvidenceIDs
	if len(acceptanceEvidenceIDs) == 0 {
		acceptanceEvidenceIDs = state.AcceptanceEvidenceIDs
	}
	record := PlanExecuteTerminalCompletionRecord{
		SchemaVersion:                 PlanExecuteTerminalCompletionSchemaVersion,
		Type:                          PlanExecuteTerminalCompletionRecordType,
		ProgressPath:                  input.ProgressPath,
		SessionID:                     state.SessionID,
		Phase:                         state.Phase,
		Status:                        state.Status,
		ReviewStatus:                  state.Review.Status,
		ExitCode:                      input.ExitCode,
		ReviewRepairSkipped:           state.Review.Status == ProgressStatusSkipped,
		FailureKind:                   state.FailureKind,
		FailureReason:                 state.FailureReason,
		FailureID:                     input.FailureID,
		RecordPath:                    input.RecordPath,
		PacketPath:                    input.PacketPath,
		NextAction:                    input.NextAction,
		RequiredEditPaths:             append([]string(nil), state.RequiredEditPaths...),
		AllowedEditPaths:              append([]string(nil), state.AllowedEditPaths...),
		WriteAllowedPaths:             append([]string(nil), state.WriteAllowedPaths...),
		ScopeContractID:               state.ScopeContractID,
		ScopeContractHash:             state.ScopeContractHash,
		SourceSatisfiedEvidencePaths:  append([]string(nil), input.SourceSatisfiedEvidencePaths...),
		SourceSatisfiedEvidence:       planExecuteTerminalSourceSatisfiedEvidenceRefs(input.SourceSatisfiedEvidence, input.SourceSatisfiedEvidencePaths),
		ChangedPaths:                  append([]string(nil), state.ChangedPaths...),
		SourceSanity:                  append([]PhaseEvidenceCheck(nil), state.Executor.SourceSanity...),
		VerificationCommands:          append([]VerificationCommand(nil), state.Executor.VerificationCommands...),
		Verification:                  append([]PhaseEvidenceCheck(nil), state.Executor.Verification...),
		VerificationSkips:             append([]VerificationSkip(nil), state.Executor.VerificationSkips...),
		VerificationRejects:           append([]VerificationReject(nil), state.Executor.VerificationRejects...),
		RequiredAcceptanceEvidenceIDs: append([]string(nil), state.RequiredAcceptanceEvidenceIDs...),
		AcceptanceEvidenceIDs:         append([]string(nil), acceptanceEvidenceIDs...),
	}
	if !input.CompletedAt.IsZero() {
		record.TS = input.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if input.SourceSatisfied {
		record.Status = "source-satisfied"
		record.Outcome = PlanExecuteTerminalOutcomeSourceSatisfied
		record.TerminalClass = PlanExecuteTerminalClassSuccess
	}
	return NormalizePlanExecuteTerminalCompletionRecord(record)
}

func NormalizePlanExecuteTerminalCompletionRecord(record PlanExecuteTerminalCompletionRecord) PlanExecuteTerminalCompletionRecord {
	record.SchemaVersion = firstPlanExecuteTerminalString(record.SchemaVersion, PlanExecuteTerminalCompletionSchemaVersion)
	record.Type = firstPlanExecuteTerminalString(record.Type, PlanExecuteTerminalCompletionRecordType)
	record.TS = strings.TrimSpace(record.TS)
	record.ProgressPath = strings.TrimSpace(record.ProgressPath)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.Phase = strings.TrimSpace(record.Phase)
	record.Status = strings.TrimSpace(record.Status)
	record.ReviewStatus = strings.TrimSpace(record.ReviewStatus)
	record.FailureKind = strings.TrimSpace(record.FailureKind)
	record.FailureReason = sanitizeProgressReason(record.FailureReason)
	record.FailureID = strings.TrimSpace(record.FailureID)
	record.RecordPath = strings.TrimSpace(record.RecordPath)
	record.PacketPath = strings.TrimSpace(record.PacketPath)
	record.NextAction = strings.TrimSpace(record.NextAction)
	record.ContractID = strings.TrimSpace(record.ContractID)
	record.SnapshotHash = strings.TrimSpace(record.SnapshotHash)
	record.ContractHash = strings.TrimSpace(record.ContractHash)
	record.RequiredEditPaths = normalizePlanExecuteTerminalPaths(record.RequiredEditPaths)
	record.AllowedEditPaths = normalizePlanExecuteTerminalPaths(record.AllowedEditPaths)
	record.WriteAllowedPaths = normalizePlanExecuteTerminalPaths(record.WriteAllowedPaths)
	record.ScopeContractID = strings.TrimSpace(record.ScopeContractID)
	record.ScopeContractHash = strings.TrimSpace(record.ScopeContractHash)
	record.SourceSatisfiedEvidencePaths = normalizePlanExecuteTerminalPaths(record.SourceSatisfiedEvidencePaths)
	record.SourceSatisfiedEvidence = normalizePlanExecuteTerminalSourceSatisfiedEvidenceRefs(record.SourceSatisfiedEvidence, record.SourceSatisfiedEvidencePaths)
	record.ChangedPaths = normalizePlanExecuteTerminalPaths(record.ChangedPaths)
	record.RequiredPathCoverage = planExecuteTerminalRequiredPathCoverage(record.RequiredEditPaths, record.ChangedPaths, record.SourceSatisfiedEvidencePaths)
	record.SourceSanity = normalizePlanExecuteTerminalChecks(record.SourceSanity)
	record.VerificationCommands = normalizePlanExecuteTerminalVerificationCommands(record.VerificationCommands)
	record.Verification = normalizePlanExecuteTerminalChecks(record.Verification)
	record.VerificationSkips = normalizePlanExecuteTerminalVerificationSkips(record.VerificationSkips)
	record.VerificationRejects = normalizePlanExecuteTerminalVerificationRejects(record.VerificationRejects)
	record.RequiredAcceptanceEvidenceIDs = normalizePlanExecuteTerminalStableIDs(record.RequiredAcceptanceEvidenceIDs)
	record.AcceptanceEvidenceIDs = normalizePlanExecuteTerminalStableIDs(record.AcceptanceEvidenceIDs)
	record.TerminalClass, record.Outcome = ClassifyPlanExecuteTerminalCompletion(record)
	record.FailureInvariants = planExecuteTerminalFailureInvariants(record)
	record.ValidationFailures = PlanExecuteTerminalCompletionValidationFailures(record)
	return record
}

func ClassifyPlanExecuteTerminalCompletion(record PlanExecuteTerminalCompletionRecord) (PlanExecuteTerminalClass, PlanExecuteTerminalOutcome) {
	status := canonicalPlanExecuteTerminalToken(record.Status)
	reviewStatus := canonicalPlanExecuteTerminalToken(record.ReviewStatus)
	phase := canonicalPlanExecuteTerminalToken(record.Phase)
	outcome := canonicalPlanExecuteTerminalToken(string(record.Outcome))
	terminalClass := canonicalPlanExecuteTerminalClassToken(record.TerminalClass)

	if status == ProgressStatusFailed || strings.Contains(status, "failed") || terminalClass == string(PlanExecuteTerminalClassFailed) {
		return PlanExecuteTerminalClassFailed, PlanExecuteTerminalOutcomeFailed
	}
	if status == ProgressStatusDegraded || strings.Contains(reviewStatus, "degraded") || strings.Contains(outcome, "degraded") {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeReviewDegraded
	}
	if terminalClass == "recovery-required" || status == ProgressStatusBlocked || reviewStatus == ProgressStatusBlocked || strings.Contains(status, "blocked") || strings.Contains(reviewStatus, "blocked") || strings.Contains(outcome, "blocked") {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
	}
	if status == ProgressStatusRunning || phase == ProgressStatusRunning || reviewStatus == ProgressStatusRunning {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
	}
	if record.ExitCode != 0 || record.FailureKind != "" || record.FailureReason != "" || strings.Contains(outcome, "failed") || strings.Contains(outcome, "failure") {
		return PlanExecuteTerminalClassFailed, PlanExecuteTerminalOutcomeFailed
	}
	if outcome == "source-satisfied" || status == "source-satisfied" {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeSourceSatisfied
	}
	if record.ReviewRepairSkipped || reviewStatus == ProgressStatusSkipped {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeReviewSkippedByDesign
	}
	if terminalClass == string(PlanExecuteTerminalClassSuccess) {
		return PlanExecuteTerminalClassSuccess, firstPlanExecuteTerminalOutcome(record.Outcome, PlanExecuteTerminalOutcomeCompleted)
	}
	if status == ProgressStatusCompleted || status == ProgressPhaseComplete || phase == ProgressPhaseComplete {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeCompleted
	}
	return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
}

func PlanExecuteTerminalCompletionValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	if record.TerminalClass != PlanExecuteTerminalClassSuccess {
		return nil
	}
	var failures []string
	for _, violation := range plancontract.ValidateAtomTerminalEvidenceInput(planExecuteTerminalAtomContract(record), planExecuteTerminalEvidenceInput(record)) {
		failure := violation.Code
		if strings.TrimSpace(violation.Path) != "" {
			failure += ":" + violation.Path
		}
		failures = append(failures, failure)
	}
	if record.ExitCode != 0 {
		failures = append(failures, "exit_code_nonzero")
	}
	if strings.TrimSpace(record.FailureKind) != "" {
		failures = append(failures, "failure_kind_present")
	}
	if strings.TrimSpace(record.FailureReason) != "" {
		failures = append(failures, "failure_reason_present")
	}
	if len(record.RequiredEditPaths) > 0 {
		for _, coverage := range record.RequiredPathCoverage {
			if coverage.Status == "missing" {
				failures = append(failures, "required_path_missing:"+coverage.Path)
			}
		}
	}
	if record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied && len(record.ChangedPaths) == 0 {
		failures = append(failures, "changed_paths_missing")
	}
	if record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied {
		failures = append(failures, planExecuteTerminalSourceSatisfiedValidationFailures(record)...)
	}
	failures = append(failures, planExecuteTerminalSourceSanityValidationFailures(record)...)
	failures = append(failures, planExecuteTerminalVerificationValidationFailures(record)...)
	if failure := planExecuteTerminalReviewValidationFailure(record); failure != "" {
		failures = append(failures, failure)
	}
	return planExecuteTerminalUniqueStrings(failures)
}

func planExecuteTerminalAtomContract(record PlanExecuteTerminalCompletionRecord) plancontract.AtomExecutionContract {
	allowedEditPaths := append([]string(nil), record.AllowedEditPaths...)
	if len(record.RequiredEditPaths) == 0 && len(record.AllowedEditPaths) == 0 && len(record.WriteAllowedPaths) == 0 && len(record.ChangedPaths) > 0 {
		allowedEditPaths = append(allowedEditPaths, record.ChangedPaths...)
	}
	scope := plancontract.NewPlanScopeContract(record.RequiredEditPaths, allowedEditPaths, nil)
	if len(record.WriteAllowedPaths) > 0 {
		scope = scope.WithWriteAllowedPaths(record.WriteAllowedPaths)
	}
	atom := plancontract.AtomExecutionContract{
		AtomID: "plan-execute",
		Scope:  scope,
		TerminalEvidence: plancontract.TerminalEvidenceRequirements{
			Explicit:                    true,
			RequireSourceEvidence:       record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied,
			RequireChangedPathCoverage:  record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied,
			RequireRequiredPathCoverage: len(record.RequiredEditPaths) > 0,
			RequireSourceSanity:         len(record.ChangedPaths) > 0 && planExecuteTerminalAnySourceSanityPath(record.ChangedPaths),
			RequireVerification:         len(record.VerificationCommands) > 0,
			RequireReview:               !record.ReviewRepairSkipped && record.Outcome != PlanExecuteTerminalOutcomeSourceSatisfied,
		},
		SourceSatisfied: plancontract.SourceSatisfiedRequirements{
			Explicit:                    true,
			Allowed:                     true,
			RequireCurrentStateEvidence: true,
			RequireRequiredScope:        len(record.RequiredEditPaths) > 0,
			RequireNoChangedPaths:       true,
		},
	}
	for _, id := range record.RequiredAcceptanceEvidenceIDs {
		atom.AcceptanceObligations = append(atom.AcceptanceObligations, plancontract.AcceptanceEvidenceRequired{
			ID:       id,
			Required: true,
			Source:   "planexecute.terminal_completion",
		})
	}
	for _, command := range record.VerificationCommands {
		if strings.TrimSpace(command.Command) != "" {
			atom.RequiredTests = append(atom.RequiredTests, plancontract.RequiredTest{Command: command.Command, Source: command.Source})
		}
	}
	return atom.Normalized()
}

func planExecuteTerminalEvidenceInput(record PlanExecuteTerminalCompletionRecord) plancontract.TerminalEvidenceInput {
	sourceEvidence := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(record.SourceSatisfiedEvidence))
	for _, ref := range record.SourceSatisfiedEvidence {
		sourceEvidence = append(sourceEvidence, plancontract.SourceSatisfiedEvidenceRef{Path: ref.Path, Kind: ref.Kind})
	}
	requiredTests := make([]plancontract.RequiredTestEvidence, 0, len(record.Verification))
	for _, check := range record.Verification {
		requiredTests = append(requiredTests, plancontract.RequiredTestEvidence{
			Command: check.Command,
			Passed:  planExecuteTerminalCheckPassed(check),
		})
	}
	return plancontract.TerminalEvidenceInput{
		AtomID:                    "plan-execute",
		TerminalStatus:            string(record.TerminalClass),
		ChangedPaths:              record.ChangedPaths,
		RequiredPathCoveragePaths: planExecuteTerminalCoveredPaths(record.RequiredPathCoverage),
		SourceEvidence:            sourceEvidence,
		SourceSatisfied:           record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied,
		SourceSatisfiedPaths:      record.SourceSatisfiedEvidencePaths,
		SourceSanityPassed:        len(planExecuteTerminalSourceSanityValidationFailures(record)) == 0,
		VerificationPassed:        len(planExecuteTerminalVerificationValidationFailures(record)) == 0,
		ReviewPassed:              planExecuteTerminalReviewValidationFailure(record) == "",
		RequiredTestResults:       requiredTests,
		AcceptanceEvidenceIDs:     record.AcceptanceEvidenceIDs,
	}
}

func planExecuteTerminalCoveredPaths(coverage []PlanExecuteRequiredPathCoverage) []string {
	var paths []string
	for _, item := range coverage {
		if item.Status != "missing" {
			paths = append(paths, item.Path)
		}
	}
	return normalizePlanExecuteTerminalPaths(paths)
}

func planExecuteTerminalAnySourceSanityPath(paths []string) bool {
	for _, path := range paths {
		if workspace.SourceSanityCoversPath(path) {
			return true
		}
	}
	return false
}

func planExecuteTerminalSourceSatisfiedValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	refs := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(record.SourceSatisfiedEvidence))
	for _, ref := range record.SourceSatisfiedEvidence {
		refs = append(refs, plancontract.SourceSatisfiedEvidenceRef{Path: ref.Path, Kind: ref.Kind})
	}
	violations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        record.RequiredEditPaths,
		SourceSatisfiedPaths: record.SourceSatisfiedEvidencePaths,
		Evidence:             refs,
		RequireEvidence:      true,
	})
	failures := make([]string, 0, len(violations))
	for _, violation := range violations {
		failures = append(failures, violation.Code+":"+violation.Path)
	}
	if len(record.SourceSatisfiedEvidencePaths) == 0 {
		failures = append(failures, "source_satisfied_evidence_missing")
	}
	return failures
}

func planExecuteTerminalSourceSanityValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	byPath := map[string]PhaseEvidenceCheck{}
	for _, check := range record.SourceSanity {
		path := plancontract.NormalizeScopePath(check.Path)
		if path != "" {
			byPath[path] = check
		}
		if !planExecuteTerminalCheckPassed(check) {
			failures := []string{"source_sanity_failed:" + firstPlanExecuteTerminalString(path, "(unknown)")}
			return failures
		}
		if planExecuteTerminalSourceSanityCheckIsDegraded(path, check) {
			failures := []string{"source_sanity_degraded:" + firstPlanExecuteTerminalString(path, "(unknown)")}
			return failures
		}
	}
	var failures []string
	for _, path := range record.ChangedPaths {
		if !workspace.SourceSanityCoversPath(path) {
			continue
		}
		if _, ok := byPath[path]; !ok {
			failures = append(failures, "source_sanity_missing:"+path)
		}
	}
	return failures
}

func planExecuteTerminalSourceSanityCheckIsDegraded(path string, check PhaseEvidenceCheck) bool {
	if !workspace.SourceSanityCoversPath(path) || !planExecuteTerminalPathLooksCpp(path) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(check.Summary + "\n" + check.Error))
	if text == "" {
		return false
	}
	return strings.Contains(text, "generic_source_sanity") ||
		strings.Contains(text, "generic fallback") ||
		strings.Contains(text, "generic delimiter sanity")
}

func planExecuteTerminalPathLooksCpp(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return true
	default:
		return false
	}
}

func planExecuteTerminalVerificationValidationFailures(record PlanExecuteTerminalCompletionRecord) []string {
	var failures []string
	for _, reject := range record.VerificationRejects {
		failures = append(failures, "verification_rejected:"+firstPlanExecuteTerminalString(reject.Command, reject.Reason))
	}
	for _, check := range record.Verification {
		if !planExecuteTerminalCheckPassed(check) {
			failures = append(failures, "verification_failed:"+firstPlanExecuteTerminalString(check.Command, "(unknown)"))
		}
	}
	evidenceByCommand := map[string]PhaseEvidenceCheck{}
	for _, check := range record.Verification {
		key := planExecuteTerminalVerificationKey(check.Command, check.WorkingDir)
		if key != "" {
			evidenceByCommand[key] = check
		}
	}
	for _, command := range record.VerificationCommands {
		key := planExecuteTerminalVerificationKey(command.Command, command.WorkingDir)
		if key == "" {
			failures = append(failures, "verification_command_invalid")
			continue
		}
		check, ok := evidenceByCommand[key]
		if !ok {
			failures = append(failures, "verification_evidence_missing:"+command.Command)
			continue
		}
		if !planExecuteTerminalCheckPassed(check) {
			failures = append(failures, "verification_not_passed:"+command.Command)
		}
	}
	return failures
}

func planExecuteTerminalReviewValidationFailure(record PlanExecuteTerminalCompletionRecord) string {
	status := canonicalPlanExecuteTerminalToken(record.ReviewStatus)
	if record.ReviewRepairSkipped || status == ProgressStatusSkipped || record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied {
		return ""
	}
	raw := strings.ToLower(strings.TrimSpace(record.ReviewStatus))
	if status == ProgressStatusCompleted || strings.HasPrefix(raw, "pass ") || strings.HasPrefix(raw, "repaired") {
		return ""
	}
	return "review_status_missing_or_not_passed"
}

func planExecuteTerminalRequiredPathCoverage(required []string, changed []string, sourceSatisfied []string) []PlanExecuteRequiredPathCoverage {
	changedSet := planExecuteTerminalPathSet(changed)
	sourceSet := planExecuteTerminalPathSet(sourceSatisfied)
	out := make([]PlanExecuteRequiredPathCoverage, 0, len(required))
	for _, path := range normalizePlanExecuteTerminalPaths(required) {
		coverage := PlanExecuteRequiredPathCoverage{Path: path, Status: "missing"}
		if _, ok := changedSet[path]; ok {
			coverage.Status = "changed"
			coverage.Evidence = "changed_paths"
		} else if _, ok := sourceSet[path]; ok {
			coverage.Status = "source_satisfied"
			coverage.Evidence = "source_satisfied_evidence"
		}
		out = append(out, coverage)
	}
	return out
}

func planExecuteTerminalFailureInvariants(record PlanExecuteTerminalCompletionRecord) []PlanExecuteTerminalInvariant {
	invariants := []PlanExecuteTerminalInvariant{
		planExecuteTerminalInvariant("exit_code_zero", record.ExitCode == 0, fmt.Sprintf("exit_code=%d", record.ExitCode)),
		planExecuteTerminalInvariant("no_failure_kind", strings.TrimSpace(record.FailureKind) == "", record.FailureKind),
		planExecuteTerminalInvariant("no_failure_reason", strings.TrimSpace(record.FailureReason) == "", record.FailureReason),
	}
	for _, coverage := range record.RequiredPathCoverage {
		invariants = append(invariants, planExecuteTerminalInvariant("required_path_covered:"+coverage.Path, coverage.Status != "missing", coverage.Status))
	}
	for _, failure := range planExecuteTerminalSourceSanityValidationFailures(record) {
		invariants = append(invariants, PlanExecuteTerminalInvariant{Code: failure, Status: "failed"})
	}
	for _, failure := range planExecuteTerminalVerificationValidationFailures(record) {
		invariants = append(invariants, PlanExecuteTerminalInvariant{Code: failure, Status: "failed"})
	}
	if failure := planExecuteTerminalReviewValidationFailure(record); failure != "" {
		invariants = append(invariants, PlanExecuteTerminalInvariant{Code: failure, Status: "failed"})
	}
	return invariants
}

func planExecuteTerminalInvariant(code string, passed bool, reason string) PlanExecuteTerminalInvariant {
	status := "passed"
	if !passed {
		status = "failed"
	}
	invariant := PlanExecuteTerminalInvariant{Code: strings.TrimSpace(code), Status: status}
	if !passed {
		invariant.Reason = sanitizeProgressReason(reason)
	}
	return invariant
}

func planExecuteTerminalSourceSatisfiedEvidenceRefs(refs []plancontract.SourceSatisfiedEvidenceRef, _ []string) []PlanExecuteSourceSatisfiedEvidenceRef {
	out := make([]PlanExecuteSourceSatisfiedEvidenceRef, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		path := plancontract.NormalizeScopePath(ref.Path)
		kind := strings.TrimSpace(ref.Kind)
		if path == "" || kind == "" {
			continue
		}
		key := path + "\x00" + kind
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, PlanExecuteSourceSatisfiedEvidenceRef{Path: path, Kind: kind})
	}
	return out
}

func normalizePlanExecuteTerminalSourceSatisfiedEvidenceRefs(refs []PlanExecuteSourceSatisfiedEvidenceRef, paths []string) []PlanExecuteSourceSatisfiedEvidenceRef {
	contractRefs := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(refs))
	for _, ref := range refs {
		contractRefs = append(contractRefs, plancontract.SourceSatisfiedEvidenceRef{Path: ref.Path, Kind: ref.Kind})
	}
	return planExecuteTerminalSourceSatisfiedEvidenceRefs(contractRefs, paths)
}

func planExecuteHarnessObservedSourceEvidence(paths []string) []plancontract.SourceSatisfiedEvidenceRef {
	out := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(paths))
	for _, path := range normalizePlanExecuteTerminalPaths(paths) {
		out = append(out, plancontract.SourceSatisfiedEvidenceRef{Path: path, Kind: "harness_observed_source"})
	}
	return out
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
		record.ProgressPath = planExecuteTerminalFieldString(fields, "state_path")
	}
	return NormalizePlanExecuteTerminalCompletionRecord(record)
}

func ProgressStateHasTerminalCompletion(state ProgressState) bool {
	status := canonicalPlanExecuteTerminalToken(state.Status)
	phase := canonicalPlanExecuteTerminalToken(state.Phase)
	reviewStatus := canonicalPlanExecuteTerminalToken(state.Review.Status)
	if phase == ProgressPhaseComplete {
		return true
	}
	switch status {
	case ProgressStatusCompleted, ProgressStatusFailed, ProgressStatusDegraded, ProgressStatusBlocked, ProgressStatusSkipped:
		return true
	}
	switch reviewStatus {
	case ProgressStatusCompleted, ProgressStatusFailed, ProgressStatusDegraded, ProgressStatusBlocked, ProgressStatusSkipped:
		return true
	default:
		return false
	}
}

func PlanExecuteTerminalCompletionRecordFromProgressState(state ProgressState, input PlanExecuteTerminalCompletionInput) (PlanExecuteTerminalCompletionRecord, bool) {
	if !ProgressStateHasTerminalCompletion(state) {
		return PlanExecuteTerminalCompletionRecord{}, false
	}
	return NewPlanExecuteTerminalCompletionRecord(state, input), true
}

func canonicalPlanExecuteTerminalToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "complete", "success", "successful", "succeeded":
		return ProgressStatusCompleted
	default:
		return value
	}
}

func canonicalPlanExecuteTerminalClassToken(value PlanExecuteTerminalClass) string {
	return strings.ReplaceAll(strings.TrimSpace(strings.ToLower(string(value))), "_", "-")
}

func normalizePlanExecuteTerminalPaths(values []string) []string {
	return plancontract.UniqueScopePaths(values)
}

func normalizePlanExecuteTerminalStableIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = plancontract.NormalizeStableContractIDPart(value)
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

func firstPlanExecuteTerminalString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func firstPlanExecuteTerminalOutcome(value PlanExecuteTerminalOutcome, fallback PlanExecuteTerminalOutcome) PlanExecuteTerminalOutcome {
	if strings.TrimSpace(string(value)) != "" {
		return PlanExecuteTerminalOutcome(strings.TrimSpace(string(value)))
	}
	return fallback
}

func planExecuteTerminalFieldString(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizePlanExecuteTerminalChecks(checks []PhaseEvidenceCheck) []PhaseEvidenceCheck {
	out := make([]PhaseEvidenceCheck, 0, len(checks))
	for _, check := range checks {
		check.Kind = strings.TrimSpace(check.Kind)
		check.Path = plancontract.NormalizeScopePath(check.Path)
		check.Command = strings.TrimSpace(check.Command)
		workingDir, ok := cleanVerificationWorkingDir(check.WorkingDir)
		if !ok {
			workingDir = ""
		}
		check.WorkingDir = workingDir
		check.Status = strings.TrimSpace(check.Status)
		check.Summary = sanitizeProgressReason(check.Summary)
		check.Error = sanitizeProgressReason(check.Error)
		if check.Kind == "" && check.Path == "" && check.Command == "" && check.Status == "" {
			continue
		}
		out = append(out, check)
	}
	return out
}

func normalizePlanExecuteTerminalVerificationCommands(commands []VerificationCommand) []VerificationCommand {
	out := make([]VerificationCommand, 0, len(commands))
	seen := map[string]struct{}{}
	for _, command := range commands {
		command.Command = strings.TrimSpace(command.Command)
		command.Argv = normalizeVerificationArgv(command.Argv)
		if len(command.Argv) == 0 {
			if args, ok := VerificationCommandArgs(command.Command); ok {
				command.Argv = args
			}
		}
		if len(command.Argv) == 0 || !VerificationArgvAllowed(command.Argv) {
			continue
		}
		if command.Command == "" {
			command.Command = strings.Join(command.Argv, " ")
		}
		workingDir, ok := cleanVerificationWorkingDir(command.WorkingDir)
		if !ok {
			continue
		}
		command.WorkingDir = workingDir
		command.Source = strings.TrimSpace(command.Source)
		command.Path = plancontract.NormalizeScopePath(command.Path)
		command.Reason = strings.TrimSpace(command.Reason)
		key := planExecuteTerminalVerificationKey(command.Command, command.WorkingDir)
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

func normalizePlanExecuteTerminalVerificationSkips(skips []VerificationSkip) []VerificationSkip {
	out := make([]VerificationSkip, 0, len(skips))
	for _, skip := range skips {
		skip.Path = plancontract.NormalizeScopePath(skip.Path)
		skip.Reason = strings.TrimSpace(skip.Reason)
		skip.Detail = sanitizeProgressReason(skip.Detail)
		if skip.Reason == "" {
			continue
		}
		out = append(out, skip)
	}
	return out
}

func normalizePlanExecuteTerminalVerificationRejects(rejects []VerificationReject) []VerificationReject {
	out := make([]VerificationReject, 0, len(rejects))
	for _, reject := range rejects {
		reject.Command = strings.TrimSpace(reject.Command)
		reject.Source = strings.TrimSpace(reject.Source)
		reject.Reason = strings.TrimSpace(reject.Reason)
		if reject.Reason == "" {
			continue
		}
		out = append(out, reject)
	}
	return out
}

func planExecuteTerminalPathSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range normalizePlanExecuteTerminalPaths(paths) {
		out[path] = struct{}{}
	}
	return out
}

func planExecuteTerminalCheckPassed(check PhaseEvidenceCheck) bool {
	status := canonicalPlanExecuteTerminalToken(check.Status)
	return strings.TrimSpace(check.Error) == "" && (status == "ok" || status == "passed")
}

func planExecuteTerminalVerificationKey(command string, workingDir string) string {
	command = strings.TrimSpace(command)
	workingDir, ok := cleanVerificationWorkingDir(workingDir)
	if !ok || command == "" {
		return ""
	}
	return workingDir + "\x00" + command
}

func planExecuteTerminalUniqueStrings(values []string) []string {
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
	return out
}

func SavePlanExecuteTerminalCompletionRecord(root string, record PlanExecuteTerminalCompletionRecord) (string, error) {
	record = NormalizePlanExecuteTerminalCompletionRecord(record)
	if strings.TrimSpace(record.SessionID) == "" {
		return "", fmt.Errorf("plan-execute terminal completion missing session id")
	}
	fullPath, relPath, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir plan-execute terminal completion dir: %w", err)
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plan-execute terminal completion: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write plan-execute terminal completion: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replace plan-execute terminal completion: %w", err)
	}
	return relPath, nil
}

func TryLoadPlanExecuteTerminalCompletionRecord(root string, sessionID string) (PlanExecuteTerminalCompletionRecord, string, bool, error) {
	fullPath, relPath, err := planExecuteTerminalCompletionPath(root, sessionID)
	if err != nil {
		return PlanExecuteTerminalCompletionRecord{}, "", false, err
	}
	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return PlanExecuteTerminalCompletionRecord{}, relPath, false, nil
	}
	if err != nil {
		return PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("read plan-execute terminal completion %s: %w", relPath, err)
	}
	var record PlanExecuteTerminalCompletionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("decode plan-execute terminal completion %s: %w", relPath, err)
	}
	if record.SchemaVersion != "" && record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion {
		return PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("unsupported plan-execute terminal completion schema %q in %s", record.SchemaVersion, relPath)
	}
	if strings.TrimSpace(record.SessionID) == "" {
		record.SessionID = strings.TrimSpace(sessionID)
	}
	return NormalizePlanExecuteTerminalCompletionRecord(record), relPath, true, nil
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
		fields["completion_path"] = strings.TrimSpace(completionPath)
	}
	return fields
}

func planExecuteTerminalCompletionPath(root string, sessionID string) (string, string, error) {
	root = strings.TrimSpace(root)
	sessionID = strings.TrimSpace(sessionID)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	if !validProgressSessionID(sessionID) {
		return "", "", fmt.Errorf("invalid plan-execute terminal completion session id %q", sessionID)
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", sessionID+".terminal.json"))
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}
