package featureplanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/plan"
	"idiom/internal/platform/util"
	coreplanexecute "idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recovery"
)

var planExecuteAcceptanceEvidenceTrustRegistry = struct {
	sync.Mutex
	fingerprints map[string]struct{}
}{fingerprints: map[string]struct{}{}}

// PlanExecuteCompletionFields extracts the nested plan-execute terminal
// contract from JSONL output. Both TUI and headless feature-planner adapters
// use this parser so degraded review/model-error evidence is interpreted once.
func PlanExecuteCompletionFields(output string) map[string]any {
	summary := PlanExecuteCompletionSummary{}
	sequence := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var record planExecuteCompletionRecord

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		if !planExecuteCompletionRecordRelevant(record.Type, record.Role, record.Content, record.StopReason) {
			continue
		}

		sequence++

		recordSummary := planExecuteCompletionSummaryFromRecord(record)

		recordSummary.sequence = sequence
		summary = MergePlanExecuteCompletionSummaries(summary, recordSummary)
	}

	return summary.Fields()
}

func PlanExecuteRecoveryRetryPrompt(prompt string, completion map[string]any) (string, bool) {
	requiredPaths := completionFieldStringSlice(completion, completionFieldRequiredEditPaths)
	refresh, ok := recovery.PlanExecutePlannerBlockedRetryRefreshResult(
		CompletionFieldString(completion, completionFieldFailureKind),
		CompletionFieldString(completion, completionFieldFailureReason)+"\n"+CompletionFieldString(completion, completionFieldSummary),
		requiredPaths,
		CompletionFieldString(completion, completionFieldNextValidAction),
	)
	if !ok {
		return "", false
	}
	instruction := recovery.PlanExecuteSourceRefreshRetryInstruction(refresh, requiredPaths)
	if instruction == "" {
		return "", false
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n## Plan-execute Recovery Addendum\n\n")
	if len(refresh.Paths) > 1 {
		fmt.Fprintf(&b, "The previous nested plan-execute attempt requires bounded source-body refresh for candidate anchor paths (%s) before a corrected `submit_plan` can be accepted.\n\n", strings.Join(refresh.Paths, ", "))
	} else {
		fmt.Fprintf(&b, "The previous nested plan-execute attempt requires a bounded source-body refresh for `%s` before a corrected `submit_plan` can be accepted.\n\n", refresh.Path)
	}
	b.WriteString(instruction)

	return b.String(), true
}

func TrustPlanExecuteCompletionFields(fields map[string]any) {
	if fields == nil {
		return
	}

	if len(completionFieldStableIDSlice(fields, completionFieldAcceptanceEvidenceIDs)) == 0 {
		delete(fields, completionFieldAcceptanceEvidenceTrusted)

		return
	}

	if fields[completionFieldAcceptanceEvidenceTrusted] != true {
		return
	}

	registerPlanExecuteAcceptanceEvidenceTrust(fields)
}

func TrustObservedPlanExecuteCompletionFields(fields map[string]any) {
	if fields == nil {
		return
	}

	if len(completionFieldStableIDSlice(fields, completionFieldAcceptanceEvidenceIDs)) == 0 {
		delete(fields, completionFieldAcceptanceEvidenceTrusted)

		return
	}

	if PlanExecuteCompletionTerminalClass(fields) != PlanExecuteTerminalCompleted {
		delete(fields, completionFieldAcceptanceEvidenceTrusted)

		return
	}

	fields[completionFieldAcceptanceEvidenceTrusted] = true
	registerPlanExecuteAcceptanceEvidenceTrust(fields)
}

func planExecuteAcceptanceEvidenceTrustRegistered(fields map[string]any) bool {
	fingerprint := planExecuteAcceptanceEvidenceTrustFingerprint(fields)
	if fingerprint == "" {
		return false
	}

	planExecuteAcceptanceEvidenceTrustRegistry.Lock()
	defer planExecuteAcceptanceEvidenceTrustRegistry.Unlock()

	if _, ok := planExecuteAcceptanceEvidenceTrustRegistry.fingerprints[fingerprint]; !ok {
		return false
	}

	delete(planExecuteAcceptanceEvidenceTrustRegistry.fingerprints, fingerprint)

	return true
}

func registerPlanExecuteAcceptanceEvidenceTrust(fields map[string]any) {
	fingerprint := planExecuteAcceptanceEvidenceTrustFingerprint(fields)
	if fingerprint == "" {
		return
	}

	planExecuteAcceptanceEvidenceTrustRegistry.Lock()
	defer planExecuteAcceptanceEvidenceTrustRegistry.Unlock()

	planExecuteAcceptanceEvidenceTrustRegistry.fingerprints[fingerprint] = struct{}{}
}

func planExecuteAcceptanceEvidenceTrustFingerprint(fields map[string]any) string {
	summary := PlanExecuteCompletionSummaryFromFields(fields)
	if len(summary.AcceptanceEvidenceIDs) == 0 || !summary.AcceptanceEvidenceTrusted {
		return ""
	}

	body, err := json.Marshal(summary.Fields())
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

type planExecuteCompletionRecord struct {
	Type                         string                                  `json:"type"`
	Role                         string                                  `json:"role"`
	Content                      string                                  `json:"content"`
	StatePath                    string                                  `json:"state_path"`
	ProgressPath                 string                                  `json:"progress_path"`
	TS                           string                                  `json:"ts"`
	Phase                        string                                  `json:"phase"`
	Status                       string                                  `json:"status"`
	ReviewStatus                 string                                  `json:"review_status"`
	TerminalClass                string                                  `json:"terminal_class"`
	Outcome                      string                                  `json:"outcome"`
	ExitCode                     int                                     `json:"exit_code"`
	ReviewSkipped                bool                                    `json:"review_repair_skipped"`
	FailureKind                  string                                  `json:"failure_kind"`
	FailureReason                string                                  `json:"failure_reason"`
	FailureID                    string                                  `json:"failure_id"`
	RecordPath                   string                                  `json:"record_path"`
	PacketPath                   string                                  `json:"packet_path"`
	NextAction                   string                                  `json:"next_valid_action"`
	ReasonClass                  string                                  `json:"reason_class"`
	StopReason                   string                                  `json:"stop_reason"`
	ContractID                   string                                  `json:"contract_id"`
	ContractHash                 string                                  `json:"contract_hash"`
	SnapshotHash                 string                                  `json:"snapshot_hash"`
	ScopeContractID              string                                  `json:"scope_contract_id"`
	ScopeContractHash            string                                  `json:"scope_contract_hash"`
	ValidationFailures           []string                                `json:"validation_failures"`
	RequiredEditPaths            []string                                `json:"required_edit_paths"`
	AllowedEditPaths             []string                                `json:"allowed_edit_paths"`
	SourceSatisfiedEvidencePaths []string                                `json:"source_satisfied_evidence_paths"`
	SourceSatisfiedEvidence      []PlanExecuteSourceSatisfiedEvidenceRef `json:"source_satisfied_evidence"`
	ChangedPaths                 []string                                `json:"changed_paths"`
	SourceSanity                 []coreplanexecute.PhaseEvidenceCheck    `json:"source_sanity"`
	Verification                 []coreplanexecute.PhaseEvidenceCheck    `json:"verification"`
	VerificationCommands         []coreplanexecute.VerificationCommand   `json:"verification_commands"`
	VerificationSkips            []coreplanexecute.VerificationSkip      `json:"verification_skips"`
	VerificationRejects          []coreplanexecute.VerificationReject    `json:"verification_rejects"`
	AcceptanceEvidenceIDs        []string                                `json:"acceptance_evidence_ids"`
	AcceptanceEvidenceTrusted    bool                                    `json:"acceptance_evidence_trusted"`
}

type PlanExecuteSourceSatisfiedEvidenceRef struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type PlanExecuteCompletionSummary struct {
	LaneFamily                   string
	Summary                      string
	ProgressPath                 string
	Status                       string
	Phase                        string
	ReviewStatus                 string
	TerminalClass                string
	Outcome                      string
	ExitCode                     int
	FailureKind                  string
	FailureReason                string
	FailureID                    string
	RecordPath                   string
	PacketPath                   string
	NextAction                   string
	CompletionSource             string
	ReviewRepairSkipped          bool
	ContractID                   string
	ContractHash                 string
	ScopeContractID              string
	ScopeContractHash            string
	SnapshotHash                 string
	RequiredEditPaths            []string
	AllowedEditPaths             []string
	SourceSatisfiedEvidencePaths []string
	SourceSatisfiedEvidence      []PlanExecuteSourceSatisfiedEvidenceRef
	ChangedPaths                 []string
	SourceSanity                 []coreplanexecute.PhaseEvidenceCheck
	Verification                 []coreplanexecute.PhaseEvidenceCheck
	VerificationCommands         []coreplanexecute.VerificationCommand
	VerificationSkips            []coreplanexecute.VerificationSkip
	VerificationRejects          []coreplanexecute.VerificationReject
	AcceptanceEvidenceIDs        []string
	AcceptanceEvidenceTrusted    bool
	ValidationFailures           []string

	timestamp    time.Time
	hasTimestamp bool
	sequence     int
}

const (
	completionFieldAcceptanceEvidenceIDs     = "acceptance_evidence_ids"
	completionFieldAcceptanceEvidenceTrusted = "acceptance_evidence_trusted"
	completionFieldActiveContractHash        = "active_contract_hash"
	completionFieldActiveContractID          = "active_contract_id"
	completionFieldAllowedEditPaths          = "allowed_edit_paths"
	completionFieldChangedPaths              = "changed_paths"
	completionFieldCompletionSource          = "completion_source"
	completionFieldContractHash              = "contract_hash"
	completionFieldContractID                = "contract_id"
	completionFieldContractSnapshotHash      = "contract_snapshot_hash"
	completionFieldExitCode                  = "exit_code"
	completionFieldFailureID                 = "failure_id"
	completionFieldFailureKind               = "failure_kind"
	completionFieldFailureReason             = "failure_reason"
	completionFieldLaneFamily                = "lane_family"
	completionFieldNextValidAction           = "next_valid_action"
	completionFieldOutcome                   = "outcome"
	completionFieldPacketPath                = "packet_path"
	completionFieldPhase                     = "phase"
	completionFieldProgressPath              = "progress_path"
	completionFieldRecordPath                = "record_path"
	completionFieldRequiredEditPaths         = "required_edit_paths"
	completionFieldReviewRepairSkipped       = "review_repair_skipped"
	completionFieldReviewStatus              = "review_status"
	completionFieldScopeContractHash         = "scope_contract_hash"
	completionFieldScopeContractID           = "scope_contract_id"
	completionFieldSnapshotHash              = "snapshot_hash"
	completionFieldSourceSanity              = "source_sanity"
	completionFieldSourceSatisfiedEvidence   = "source_satisfied_evidence"
	completionFieldSourceSatisfiedPaths      = "source_satisfied_evidence_paths"
	completionFieldStatePath                 = "state_path"
	completionFieldStatus                    = "status"
	completionFieldSummary                   = "summary"
	completionFieldTerminalClass             = "terminal_class"
	completionFieldTool                      = "tool"
	completionFieldTS                        = "ts"
	completionFieldValidationFailures        = "validation_failures"
	completionFieldVerification              = "verification"
	completionFieldVerificationCommandsKey   = "verification_commands"
	completionFieldVerificationRejectsKey    = "verification_rejects"
	completionFieldVerificationSkipsKey      = "verification_skips"
	completionFieldWriteAllowedPaths         = "write_allowed_paths"
	completionFieldCompletionPath            = "completion_path"
)

const (
	PlanExecuteCompletionFieldCompletionSource          = completionFieldCompletionSource
	PlanExecuteCompletionFieldAcceptanceEvidenceIDs     = completionFieldAcceptanceEvidenceIDs
	PlanExecuteCompletionFieldAcceptanceEvidenceTrusted = completionFieldAcceptanceEvidenceTrusted
	PlanExecuteCompletionFieldContractID                = completionFieldContractID
	PlanExecuteCompletionFieldExitCode                  = completionFieldExitCode
	PlanExecuteCompletionFieldFailureKind               = completionFieldFailureKind
	PlanExecuteCompletionFieldFailureID                 = completionFieldFailureID
	PlanExecuteCompletionFieldFailureReason             = completionFieldFailureReason
	PlanExecuteCompletionFieldLaneFamily                = completionFieldLaneFamily
	PlanExecuteCompletionFieldNextValidAction           = completionFieldNextValidAction
	PlanExecuteCompletionFieldPacketPath                = completionFieldPacketPath
	PlanExecuteCompletionFieldPhase                     = completionFieldPhase
	PlanExecuteCompletionFieldProgressPath              = completionFieldProgressPath
	PlanExecuteCompletionFieldRecordPath                = completionFieldRecordPath
	PlanExecuteCompletionFieldRequiredEditPaths         = completionFieldRequiredEditPaths
	PlanExecuteCompletionFieldReviewStatus              = completionFieldReviewStatus
	PlanExecuteCompletionFieldScopeContractID           = completionFieldScopeContractID
	PlanExecuteCompletionFieldScopeContractHash         = completionFieldScopeContractHash
	PlanExecuteCompletionFieldSnapshotHash              = completionFieldSnapshotHash
	PlanExecuteCompletionFieldStatePath                 = completionFieldStatePath
	PlanExecuteCompletionFieldStatus                    = completionFieldStatus
	PlanExecuteCompletionFieldSummary                   = completionFieldSummary
	PlanExecuteCompletionFieldTool                      = completionFieldTool
	PlanExecuteCompletionFieldReviewRepairSkipped       = completionFieldReviewRepairSkipped
)

const (
	PlanExecuteCompletionLaneFamily                 = "plan-execute"
	PlanExecuteCompletionSourceHeadlessReturned     = "headless_plan_execute_returned"
	PlanExecuteCompletionSourceTUIReturned          = "tui_plan_execute_returned"
	PlanExecuteCompletionSourceDurableTerminal      = "durable_plan_execute_terminal_completion"
	PlanExecuteCompletionSourceDurableProgress      = "durable_plan_execute_progress"
	PlanExecuteCompletionStatusAmbiguous            = "ambiguous"
	PlanExecuteCompletionMissingStatusFailureKind   = "missing_nested_plan_execute_status"
	PlanExecuteCompletionMissingStatusFailureReason = "nested plan-execute returned without typed completion status"
)

var PlanExecuteProgressPreservedFieldKeys = []string{
	completionFieldRequiredEditPaths,
	completionFieldAllowedEditPaths,
	completionFieldChangedPaths,
	completionFieldSourceSanity,
}

var PlanExecuteTerminalPreservedFieldKeys = []string{
	completionFieldRequiredEditPaths,
	completionFieldAllowedEditPaths,
	completionFieldWriteAllowedPaths,
	completionFieldChangedPaths,
	completionFieldSourceSanity,
	completionFieldSourceSatisfiedPaths,
	completionFieldSourceSatisfiedEvidence,
	completionFieldContractHash,
	completionFieldCompletionPath,
}

var planExecuteCompletionOverlayFieldKeys = []string{
	completionFieldStatus,
	completionFieldPhase,
	completionFieldReviewStatus,
	completionFieldTerminalClass,
	completionFieldOutcome,
	completionFieldExitCode,
	completionFieldFailureKind,
	completionFieldFailureReason,
	completionFieldFailureID,
	completionFieldRecordPath,
	completionFieldPacketPath,
	completionFieldNextValidAction,
	completionFieldProgressPath,
	completionFieldReviewRepairSkipped,
	completionFieldChangedPaths,
	completionFieldLaneFamily,
	completionFieldSummary,
	completionFieldCompletionSource,
	completionFieldContractID,
	completionFieldContractHash,
	completionFieldScopeContractID,
	completionFieldScopeContractHash,
	completionFieldSnapshotHash,
	completionFieldRequiredEditPaths,
	completionFieldAllowedEditPaths,
	completionFieldSourceSatisfiedPaths,
	completionFieldSourceSatisfiedEvidence,
	completionFieldSourceSanity,
	completionFieldVerification,
	completionFieldVerificationCommandsKey,
	completionFieldVerificationSkipsKey,
	completionFieldVerificationRejectsKey,
	completionFieldAcceptanceEvidenceIDs,
	completionFieldAcceptanceEvidenceTrusted,
	completionFieldValidationFailures,
}

func PlanExecuteCompletionSummaryFromFields(fields map[string]any) PlanExecuteCompletionSummary {
	if fields == nil {
		return PlanExecuteCompletionSummary{}
	}

	summary := PlanExecuteCompletionSummary{
		LaneFamily:                   CompletionFieldString(fields, completionFieldLaneFamily),
		Summary:                      CompletionFieldString(fields, completionFieldSummary),
		ProgressPath:                 util.FirstNonEmpty(CompletionFieldString(fields, completionFieldProgressPath), CompletionFieldString(fields, completionFieldStatePath)),
		Status:                       CompletionFieldString(fields, completionFieldStatus),
		Phase:                        CompletionFieldString(fields, completionFieldPhase),
		ReviewStatus:                 CompletionFieldString(fields, completionFieldReviewStatus),
		TerminalClass:                CompletionFieldString(fields, completionFieldTerminalClass),
		Outcome:                      CompletionFieldString(fields, completionFieldOutcome),
		ExitCode:                     completionFieldInt(fields, completionFieldExitCode),
		FailureKind:                  CompletionFieldString(fields, completionFieldFailureKind),
		FailureReason:                CompletionFieldString(fields, completionFieldFailureReason),
		FailureID:                    CompletionFieldString(fields, completionFieldFailureID),
		RecordPath:                   CompletionFieldString(fields, completionFieldRecordPath),
		PacketPath:                   CompletionFieldString(fields, completionFieldPacketPath),
		NextAction:                   CompletionFieldString(fields, completionFieldNextValidAction),
		CompletionSource:             CompletionFieldString(fields, completionFieldCompletionSource),
		ReviewRepairSkipped:          completionFieldBool(fields, completionFieldReviewRepairSkipped),
		ContractID:                   util.FirstNonEmpty(CompletionFieldString(fields, completionFieldContractID), CompletionFieldString(fields, completionFieldActiveContractID)),
		ContractHash:                 util.FirstNonEmpty(CompletionFieldString(fields, completionFieldContractHash), CompletionFieldString(fields, completionFieldActiveContractHash)),
		ScopeContractID:              CompletionFieldString(fields, completionFieldScopeContractID),
		ScopeContractHash:            CompletionFieldString(fields, completionFieldScopeContractHash),
		SnapshotHash:                 util.FirstNonEmpty(CompletionFieldString(fields, completionFieldSnapshotHash), CompletionFieldString(fields, completionFieldContractSnapshotHash)),
		RequiredEditPaths:            completionFieldStringSlice(fields, completionFieldRequiredEditPaths),
		AllowedEditPaths:             completionFieldStringSlice(fields, completionFieldAllowedEditPaths),
		SourceSatisfiedEvidencePaths: completionFieldStringSlice(fields, completionFieldSourceSatisfiedPaths),
		SourceSatisfiedEvidence:      completionFieldSourceSatisfiedEvidenceRefs(fields, completionFieldSourceSatisfiedEvidence),
		ChangedPaths:                 completionFieldStringSlice(fields, completionFieldChangedPaths),
		SourceSanity:                 completionFieldPhaseEvidenceChecks(fields, completionFieldSourceSanity),
		Verification:                 completionFieldPhaseEvidenceChecks(fields, completionFieldVerification),
		VerificationCommands:         completionFieldVerificationCommands(fields, completionFieldVerificationCommandsKey),
		VerificationSkips:            completionFieldVerificationSkips(fields, completionFieldVerificationSkipsKey),
		VerificationRejects:          completionFieldVerificationRejects(fields, completionFieldVerificationRejectsKey),
		AcceptanceEvidenceIDs:        completionFieldStableIDSlice(fields, completionFieldAcceptanceEvidenceIDs),
		AcceptanceEvidenceTrusted:    completionFieldBool(fields, completionFieldAcceptanceEvidenceTrusted),
		ValidationFailures:           completionFieldStringSlice(fields, completionFieldValidationFailures),
	}
	if ts := CompletionFieldString(fields, completionFieldTS); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			summary.timestamp = parsed
			summary.hasTimestamp = true
		}
	}

	return summary.normalized()
}

func MergePlanExecuteCompletionSummaries(current PlanExecuteCompletionSummary, candidate PlanExecuteCompletionSummary) PlanExecuteCompletionSummary {
	current = current.normalized()
	candidate = candidate.normalized()

	if !candidate.hasConcreteCompletionState() {
		return current
	}

	if !current.hasConcreteCompletionState() {
		return candidate
	}

	if candidate.hasFailureOutcome() && !current.hasFailureOutcome() {
		return candidate.withInheritedContext(current)
	}

	if candidate.planExecuteCompletionTerminal() != current.planExecuteCompletionTerminal() {
		if candidate.planExecuteCompletionTerminal() {
			return candidate.withInheritedContext(current)
		}

		return current.withInheritedContext(candidate)
	}

	if candidate.hasTimestamp && current.hasTimestamp && !candidate.timestamp.Equal(current.timestamp) {
		if candidate.timestamp.After(current.timestamp) {
			return candidate.withInheritedContext(current)
		}

		return current.withInheritedContext(candidate)
	}

	if candidate.completionSpecificityScore() > current.completionSpecificityScore() {
		return candidate.withInheritedContext(current)
	}

	if candidate.completionSpecificityScore() < current.completionSpecificityScore() {
		return current.withInheritedContext(candidate)
	}

	if candidate.sequence >= current.sequence {
		return candidate.withInheritedContext(current)
	}

	return current.withInheritedContext(candidate)
}

func OverlayPlanExecuteCompletionFields(fields map[string]any, candidateFields map[string]any) {
	if fields == nil {
		return
	}

	merged := MergePlanExecuteCompletionSummaries(
		PlanExecuteCompletionSummaryFromFields(fields),
		PlanExecuteCompletionSummaryFromFields(candidateFields),
	)
	for _, key := range planExecuteCompletionOverlayFieldKeys {
		delete(fields, key)
	}

	for key, value := range merged.Fields() {
		fields[key] = value
	}
}

func (summary PlanExecuteCompletionSummary) Fields() map[string]any {
	summary = summary.normalized()

	fields := map[string]any{}

	if summary.LaneFamily != "" {
		fields[completionFieldLaneFamily] = summary.LaneFamily
	}

	if summary.Summary != "" {
		fields[completionFieldSummary] = summary.Summary
	}

	if summary.ProgressPath != "" {
		fields[completionFieldProgressPath] = summary.ProgressPath
	}

	if summary.Status != "" {
		fields[completionFieldStatus] = summary.Status
	}

	if summary.Phase != "" {
		fields[completionFieldPhase] = summary.Phase
	}

	if summary.ReviewStatus != "" {
		fields[completionFieldReviewStatus] = summary.ReviewStatus
	}

	if summary.TerminalClass != "" {
		fields[completionFieldTerminalClass] = summary.TerminalClass
	}

	if summary.Outcome != "" {
		fields[completionFieldOutcome] = summary.Outcome
	}

	if summary.ExitCode != 0 {
		fields[completionFieldExitCode] = summary.ExitCode
	}

	if summary.FailureKind != "" {
		fields[completionFieldFailureKind] = summary.FailureKind
	}

	if summary.FailureReason != "" {
		fields[completionFieldFailureReason] = summary.FailureReason
	}

	if summary.FailureID != "" {
		fields[completionFieldFailureID] = summary.FailureID
	}

	if summary.RecordPath != "" {
		fields[completionFieldRecordPath] = summary.RecordPath
	}

	if summary.PacketPath != "" {
		fields[completionFieldPacketPath] = summary.PacketPath
	}

	if summary.NextAction != "" {
		fields[completionFieldNextValidAction] = summary.NextAction
	}

	if summary.CompletionSource != "" {
		fields[completionFieldCompletionSource] = summary.CompletionSource
	}

	if summary.ReviewRepairSkipped {
		fields[completionFieldReviewRepairSkipped] = true
	}

	if summary.ContractID != "" {
		fields[completionFieldContractID] = summary.ContractID
	}

	if summary.ScopeContractID != "" {
		fields[completionFieldScopeContractID] = summary.ScopeContractID
	}

	if summary.ScopeContractHash != "" {
		fields[completionFieldScopeContractHash] = summary.ScopeContractHash
	}

	if summary.SnapshotHash != "" {
		fields[completionFieldSnapshotHash] = summary.SnapshotHash
	}

	if len(summary.RequiredEditPaths) > 0 {
		fields[completionFieldRequiredEditPaths] = append([]string(nil), summary.RequiredEditPaths...)
	}

	if len(summary.AllowedEditPaths) > 0 {
		fields[completionFieldAllowedEditPaths] = append([]string(nil), summary.AllowedEditPaths...)
	}

	if len(summary.SourceSatisfiedEvidencePaths) > 0 {
		fields[completionFieldSourceSatisfiedPaths] = append([]string(nil), summary.SourceSatisfiedEvidencePaths...)
	}

	if len(summary.SourceSatisfiedEvidence) > 0 {
		fields[completionFieldSourceSatisfiedEvidence] = append([]PlanExecuteSourceSatisfiedEvidenceRef(nil), summary.SourceSatisfiedEvidence...)
	}

	if len(summary.ChangedPaths) > 0 {
		fields[completionFieldChangedPaths] = append([]string(nil), summary.ChangedPaths...)
	}

	if len(summary.SourceSanity) > 0 {
		fields[completionFieldSourceSanity] = append([]coreplanexecute.PhaseEvidenceCheck(nil), summary.SourceSanity...)
	}

	if len(summary.Verification) > 0 {
		fields[completionFieldVerification] = append([]coreplanexecute.PhaseEvidenceCheck(nil), summary.Verification...)
	}

	if len(summary.VerificationCommands) > 0 {
		fields[completionFieldVerificationCommandsKey] = append([]coreplanexecute.VerificationCommand(nil), summary.VerificationCommands...)
	}

	if len(summary.VerificationSkips) > 0 {
		fields[completionFieldVerificationSkipsKey] = append([]coreplanexecute.VerificationSkip(nil), summary.VerificationSkips...)
	}

	if len(summary.VerificationRejects) > 0 {
		fields[completionFieldVerificationRejectsKey] = append([]coreplanexecute.VerificationReject(nil), summary.VerificationRejects...)
	}

	if len(summary.AcceptanceEvidenceIDs) > 0 {
		fields[completionFieldAcceptanceEvidenceIDs] = append([]string(nil), summary.AcceptanceEvidenceIDs...)
	}

	if summary.AcceptanceEvidenceTrusted {
		fields[completionFieldAcceptanceEvidenceTrusted] = true
	}

	if len(summary.ValidationFailures) > 0 {
		fields[completionFieldValidationFailures] = append([]string(nil), summary.ValidationFailures...)
	}

	return fields
}

func planExecuteCompletionSummaryFromRecord(record planExecuteCompletionRecord) PlanExecuteCompletionSummary {
	summary := PlanExecuteCompletionSummary{
		ProgressPath:                 util.FirstNonEmpty(record.ProgressPath, record.StatePath),
		Status:                       record.Status,
		Phase:                        record.Phase,
		ReviewStatus:                 record.ReviewStatus,
		TerminalClass:                record.TerminalClass,
		Outcome:                      record.Outcome,
		ExitCode:                     record.ExitCode,
		ReviewRepairSkipped:          record.ReviewSkipped,
		FailureKind:                  util.FirstNonEmpty(record.FailureKind, record.ReasonClass),
		FailureReason:                record.FailureReason,
		FailureID:                    record.FailureID,
		RecordPath:                   record.RecordPath,
		PacketPath:                   record.PacketPath,
		NextAction:                   record.NextAction,
		ContractID:                   record.ContractID,
		ContractHash:                 record.ContractHash,
		ScopeContractID:              record.ScopeContractID,
		ScopeContractHash:            record.ScopeContractHash,
		SnapshotHash:                 record.SnapshotHash,
		RequiredEditPaths:            record.RequiredEditPaths,
		AllowedEditPaths:             record.AllowedEditPaths,
		SourceSatisfiedEvidencePaths: record.SourceSatisfiedEvidencePaths,
		SourceSatisfiedEvidence:      append([]PlanExecuteSourceSatisfiedEvidenceRef(nil), record.SourceSatisfiedEvidence...),
		ChangedPaths:                 append([]string(nil), record.ChangedPaths...),
		SourceSanity:                 append([]coreplanexecute.PhaseEvidenceCheck(nil), record.SourceSanity...),
		Verification:                 append([]coreplanexecute.PhaseEvidenceCheck(nil), record.Verification...),
		VerificationCommands:         append([]coreplanexecute.VerificationCommand(nil), record.VerificationCommands...),
		VerificationSkips:            append([]coreplanexecute.VerificationSkip(nil), record.VerificationSkips...),
		VerificationRejects:          append([]coreplanexecute.VerificationReject(nil), record.VerificationRejects...),
		AcceptanceEvidenceIDs:        append([]string(nil), record.AcceptanceEvidenceIDs...),
		AcceptanceEvidenceTrusted:    len(record.AcceptanceEvidenceIDs) > 0 && planExecuteCompletionRecordTrustsAcceptanceEvidence(record.Type),
		ValidationFailures:           append([]string(nil), record.ValidationFailures...),
	}
	if ts := strings.TrimSpace(record.TS); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			summary.timestamp = parsed
			summary.hasTimestamp = true
		}
	}

	if record.Type == "idiom.plan_execute.review_repair_degraded" {
		summary.Status = "degraded"
		summary.ReviewStatus = util.FirstNonEmpty(record.ReviewStatus, record.Status, "degraded")
		summary.FailureKind = util.FirstNonEmpty(record.FailureKind, record.ReasonClass, "review_repair_degraded")
	}

	if record.Type == "idiom.plan_execute.source_satisfied" || record.Type == "idiom.plan_execute.auto_exact_plan_source_satisfied" {
		summary.Status = "source-satisfied"
		summary.Outcome = "source_satisfied"
	}

	if planExecuteCompletionModelError(record.StopReason, record.Content) {
		summary.Status = util.FirstNonEmpty(summary.Status, "failed")
		summary.FailureKind = util.FirstNonEmpty(summary.FailureKind, "model_error")
		summary.FailureReason = util.FirstNonEmpty(summary.FailureReason, "nested plan-execute stopped with model_error")
	}

	return summary.normalized()
}

func (summary PlanExecuteCompletionSummary) normalized() PlanExecuteCompletionSummary {
	summary.LaneFamily = strings.TrimSpace(summary.LaneFamily)
	summary.Summary = strings.TrimSpace(summary.Summary)
	summary.ProgressPath = strings.TrimSpace(summary.ProgressPath)
	summary.Status = strings.TrimSpace(summary.Status)
	summary.Phase = strings.TrimSpace(summary.Phase)
	summary.ReviewStatus = strings.TrimSpace(summary.ReviewStatus)
	summary.TerminalClass = strings.TrimSpace(summary.TerminalClass)
	summary.Outcome = strings.TrimSpace(summary.Outcome)

	if strings.EqualFold(summary.Status, "source-satisfied") && summary.Outcome == "" {
		summary.Outcome = "source_satisfied"
	}

	summary.FailureKind = strings.TrimSpace(summary.FailureKind)
	summary.FailureReason = strings.TrimSpace(summary.FailureReason)

	if strings.EqualFold(summary.Outcome, "source_satisfied") && summary.FailureKind == "" && summary.FailureReason == "" {
		summary.Status = "source-satisfied"
	}

	summary.FailureID = strings.TrimSpace(summary.FailureID)
	summary.RecordPath = strings.TrimSpace(summary.RecordPath)
	summary.PacketPath = strings.TrimSpace(summary.PacketPath)
	summary.NextAction = strings.TrimSpace(summary.NextAction)
	summary.CompletionSource = strings.TrimSpace(summary.CompletionSource)
	summary.ContractID = strings.TrimSpace(summary.ContractID)
	summary.ContractHash = strings.TrimSpace(summary.ContractHash)
	summary.SnapshotHash = strings.TrimSpace(summary.SnapshotHash)

	if summary.SnapshotHash == "" {
		summary.SnapshotHash = summary.ContractHash
	}

	summary.ScopeContractID = strings.TrimSpace(summary.ScopeContractID)
	summary.ScopeContractHash = strings.TrimSpace(summary.ScopeContractHash)
	summary.RequiredEditPaths = normalizeCompletionStringList(summary.RequiredEditPaths)
	summary.AllowedEditPaths = normalizeCompletionStringList(summary.AllowedEditPaths)
	summary.SourceSatisfiedEvidencePaths = normalizeCompletionStringList(summary.SourceSatisfiedEvidencePaths)
	summary.SourceSatisfiedEvidence = normalizePlanExecuteSourceSatisfiedEvidenceRefs(summary.SourceSatisfiedEvidence)
	summary.ChangedPaths = normalizeCompletionStringList(summary.ChangedPaths)
	summary.SourceSanity = normalizeCompletionPhaseEvidenceChecks(summary.SourceSanity)
	summary.Verification = normalizeCompletionPhaseEvidenceChecks(summary.Verification)
	summary.VerificationCommands = normalizeCompletionVerificationCommands(summary.VerificationCommands)
	summary.VerificationSkips = normalizeCompletionVerificationSkips(summary.VerificationSkips)
	summary.VerificationRejects = normalizeCompletionVerificationRejects(summary.VerificationRejects)
	summary.AcceptanceEvidenceIDs = normalizeCompletionStableIDList(summary.AcceptanceEvidenceIDs)

	if len(summary.AcceptanceEvidenceIDs) == 0 {
		summary.AcceptanceEvidenceTrusted = false
	}

	summary.ValidationFailures = normalizeCompletionStringList(summary.ValidationFailures)

	return summary
}

func (summary PlanExecuteCompletionSummary) withInheritedContext(other PlanExecuteCompletionSummary) PlanExecuteCompletionSummary {
	if summary.Status == "" {
		summary.Status = other.Status
	}

	if summary.Phase == "" {
		summary.Phase = other.Phase
	}

	if summary.ReviewStatus == "" {
		summary.ReviewStatus = other.ReviewStatus
	}

	if summary.Outcome == "" {
		summary.Outcome = other.Outcome
	}

	if summary.TerminalClass == "" {
		summary.TerminalClass = other.TerminalClass
	}

	if summary.hasFailureOutcome() {
		if summary.FailureKind == "" {
			summary.FailureKind = other.FailureKind
		}

		if summary.FailureReason == "" {
			summary.FailureReason = other.FailureReason
		}

		if summary.FailureID == "" {
			summary.FailureID = other.FailureID
		}

		if summary.RecordPath == "" {
			summary.RecordPath = other.RecordPath
		}

		if summary.PacketPath == "" {
			summary.PacketPath = other.PacketPath
		}

		if summary.NextAction == "" {
			summary.NextAction = other.NextAction
		}
	}

	if summary.LaneFamily == "" {
		summary.LaneFamily = other.LaneFamily
	}

	if summary.Summary == "" {
		summary.Summary = other.Summary
	}

	if summary.CompletionSource == "" {
		summary.CompletionSource = other.CompletionSource
	}

	if summary.ProgressPath == "" {
		summary.ProgressPath = other.ProgressPath
	}

	if summary.ContractID == "" {
		summary.ContractID = other.ContractID
	}

	if summary.ContractHash == "" {
		summary.ContractHash = other.ContractHash
	}

	if summary.ScopeContractID == "" {
		summary.ScopeContractID = other.ScopeContractID
	}

	if summary.ScopeContractHash == "" {
		summary.ScopeContractHash = other.ScopeContractHash
	}

	if summary.SnapshotHash == "" {
		summary.SnapshotHash = other.SnapshotHash
	}

	if len(summary.RequiredEditPaths) == 0 {
		summary.RequiredEditPaths = append([]string(nil), other.RequiredEditPaths...)
	}

	if len(summary.AllowedEditPaths) == 0 {
		summary.AllowedEditPaths = append([]string(nil), other.AllowedEditPaths...)
	}

	if len(summary.SourceSatisfiedEvidencePaths) == 0 {
		summary.SourceSatisfiedEvidencePaths = append([]string(nil), other.SourceSatisfiedEvidencePaths...)
	}

	if len(summary.SourceSatisfiedEvidence) == 0 {
		summary.SourceSatisfiedEvidence = append([]PlanExecuteSourceSatisfiedEvidenceRef(nil), other.SourceSatisfiedEvidence...)
	}

	if len(summary.ChangedPaths) == 0 {
		summary.ChangedPaths = append([]string(nil), other.ChangedPaths...)
	}

	if planExecuteCompletionStatusSkipped(summary.Status) || planExecuteCompletionStatusSkipped(summary.ReviewStatus) {
		summary.ReviewRepairSkipped = summary.ReviewRepairSkipped || other.ReviewRepairSkipped
	}

	return summary
}

func (summary PlanExecuteCompletionSummary) hasConcreteCompletionState() bool {
	return summary.ProgressPath != "" ||
		summary.Status != "" ||
		summary.Phase != "" ||
		summary.ReviewStatus != "" ||
		summary.TerminalClass != "" ||
		summary.Outcome != "" ||
		summary.ExitCode != 0 ||
		summary.FailureKind != "" ||
		summary.FailureReason != "" ||
		summary.FailureID != "" ||
		summary.RecordPath != "" ||
		summary.PacketPath != "" ||
		summary.NextAction != "" ||
		summary.ReviewRepairSkipped ||
		summary.ContractID != "" ||
		summary.ContractHash != "" ||
		summary.ScopeContractID != "" ||
		summary.ScopeContractHash != "" ||
		summary.SnapshotHash != "" ||
		len(summary.RequiredEditPaths) > 0 ||
		len(summary.AllowedEditPaths) > 0 ||
		len(summary.SourceSatisfiedEvidencePaths) > 0 ||
		len(summary.SourceSatisfiedEvidence) > 0 ||
		len(summary.ChangedPaths) > 0 ||
		len(summary.SourceSanity) > 0 ||
		len(summary.Verification) > 0 ||
		len(summary.VerificationCommands) > 0 ||
		len(summary.VerificationSkips) > 0 ||
		len(summary.VerificationRejects) > 0 ||
		len(summary.AcceptanceEvidenceIDs) > 0 ||
		summary.AcceptanceEvidenceTrusted ||
		len(summary.ValidationFailures) > 0
}

func (summary PlanExecuteCompletionSummary) planExecuteCompletionTerminal() bool {
	if summary.TerminalClass != "" {
		return true
	}

	return planExecuteCompletionStatusTerminal(summary.Status) ||
		planExecuteCompletionStatusTerminal(summary.ReviewStatus) ||
		summary.ExitCode != 0 ||
		summary.FailureKind != "" ||
		summary.FailureReason != "" ||
		len(summary.SourceSanity) > 0 ||
		len(summary.Verification) > 0 ||
		len(summary.VerificationCommands) > 0 ||
		len(summary.VerificationRejects) > 0 ||
		len(summary.ValidationFailures) > 0
}

func (summary PlanExecuteCompletionSummary) hasFailureOutcome() bool {
	if !summary.hasConcreteCompletionState() {
		return false
	}

	if len(summary.ValidationFailures) > 0 {
		return true
	}

	if len(planExecuteCompletionSourceSanityValidationFailures(summary)) > 0 {
		return true
	}

	if len(planExecuteCompletionVerificationValidationFailures(summary)) > 0 {
		return true
	}

	record := coreplanexecute.PlanExecuteTerminalCompletionRecordFromFields(summary.Fields())

	return record.TerminalClass != coreplanexecute.PlanExecuteTerminalClassSuccess
}

func planExecuteCompletionSourceSanityValidationFailures(summary PlanExecuteCompletionSummary) []string {
	record := coreplanexecute.PlanExecuteTerminalCompletionRecordFromFields(summary.Fields())

	return normalizeCompletionStringList(coreplanexecute.PlanExecuteTerminalSourceSanityValidationFailures(record))
}

func planExecuteCompletionVerificationValidationFailures(summary PlanExecuteCompletionSummary) []string {
	summary = summary.normalized()
	var failures []string

	for _, reject := range summary.VerificationRejects {
		failures = append(failures, "verification_rejected:"+firstCompletionString(reject.Command, reject.Reason))
	}

	evidenceByCommand := map[string]coreplanexecute.PhaseEvidenceCheck{}
	for _, check := range summary.Verification {
		if !planExecuteCompletionVerificationCheckPassed(check) {
			failures = append(failures, "verification_failed:"+firstCompletionString(check.Command, "(unknown)"))
		}

		key := plancontract.VerificationCommandEvidenceKey(check.Command, nil, check.WorkingDir)
		if key != "" {
			evidenceByCommand[key] = check
		}
	}

	for _, command := range summary.VerificationCommands {
		key := plancontract.VerificationCommandEvidenceKey(command.Command, command.Argv, command.WorkingDir)
		if key == "" {
			failures = append(failures, "verification_command_invalid")
			continue
		}

		check, ok := evidenceByCommand[key]
		if !ok {
			failures = append(failures, "verification_evidence_missing:"+command.Command)
			continue
		}

		if !planExecuteCompletionVerificationCheckPassed(check) {
			failures = append(failures, "verification_not_passed:"+command.Command)
		}
	}

	return normalizeCompletionStringList(failures)
}

func planExecuteCompletionVerificationCheckPassed(check coreplanexecute.PhaseEvidenceCheck) bool {
	status := strings.ToLower(strings.TrimSpace(check.Status))
	return strings.TrimSpace(check.Error) == "" && (status == "ok" || status == "passed")
}

func firstCompletionString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func (summary PlanExecuteCompletionSummary) completionSpecificityScore() int {
	score := planExecuteCompletionPhaseRank(summary.Phase) * 10
	if summary.planExecuteCompletionTerminal() {
		score += 1000
	} else if summary.Status != "" {
		score += 100
	}

	if summary.ProgressPath != "" {
		score += 5
	}

	if summary.PacketPath != "" || summary.RecordPath != "" {
		score += 4
	}

	if len(summary.ChangedPaths) > 0 {
		score += 3
	}

	if len(summary.SourceSanity) > 0 {
		score += 3
	}

	if len(summary.Verification) > 0 || len(summary.VerificationCommands) > 0 {
		score += 3
	}

	if summary.ContractID != "" || summary.ContractHash != "" || summary.ScopeContractID != "" || summary.ScopeContractHash != "" || summary.SnapshotHash != "" {
		score += 3
	}

	if summary.ReviewStatus != "" {
		score += 2
	}

	return score
}

func planExecuteCompletionStatusTerminal(status string) bool {
	status = canonicalPlanExecuteCompletionStatus(status)
	switch status {
	case "completed", "success", "succeeded", "skipped", "source-satisfied", "failed", "failure", "blocked":
		return true
	default:
		return containsBlockingPlanExecuteCompletionSignal(status) && status != coreplanexecute.ProgressStatusRunning
	}
}

func planExecuteCompletionStatusSkipped(status string) bool {
	return canonicalPlanExecuteCompletionStatus(status) == "skipped"
}

func planExecuteCompletionPhaseRank(phase string) int {
	switch canonicalPlanExecuteCompletionStatus(phase) {
	case "complete", "completed":
		return 4
	case "review-repair":
		return 3
	case "executor", "execute", "execution":
		return 2
	case "planning", "planner":
		return 1
	default:
		return 0
	}
}

type PlanExecuteTerminalClass string

const (
	PlanExecuteTerminalCompleted PlanExecuteTerminalClass = "completed"
	PlanExecuteTerminalBlocked   PlanExecuteTerminalClass = "blocked"
)

type PlanExecuteCompletionDecision struct {
	Class  PlanExecuteTerminalClass
	Reason string
}

func PlanExecuteCompletionTerminalClass(fields map[string]any) PlanExecuteTerminalClass {
	return PlanExecuteCompletionTerminalDecision(fields).Class
}

func PlanExecuteCompletionTerminalDecision(fields map[string]any) PlanExecuteCompletionDecision {
	record := coreplanexecute.PlanExecuteTerminalCompletionRecordFromFields(fields)
	status := canonicalPlanExecuteCompletionStatus(CompletionFieldString(fields, completionFieldStatus))
	reviewStatus := canonicalPlanExecuteCompletionStatus(CompletionFieldString(fields, completionFieldReviewStatus))
	terminalClass := strings.TrimSpace(CompletionFieldString(fields, completionFieldTerminalClass))

	if status == "" && reviewStatus == "" && terminalClass == "" && CompletionFieldString(fields, completionFieldOutcome) == "" {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "missing required nested plan-execute status"}
	}

	if status == "skipped" && !completionFieldBool(fields, completionFieldReviewRepairSkipped) {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design"}
	}

	if reviewStatus == "skipped" && !completionFieldBool(fields, completionFieldReviewRepairSkipped) {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute skipped review is not explicitly marked by design"}
	}

	summary := PlanExecuteCompletionSummaryFromFields(fields)
	derivedFailures := append(planExecuteCompletionSourceSanityValidationFailures(summary), planExecuteCompletionVerificationValidationFailures(summary)...)
	if failures := normalizeCompletionStringList(append(completionFieldStringSlice(fields, completionFieldValidationFailures), derivedFailures...)); len(failures) > 0 {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute terminal validation failed: " + strings.Join(failures, "; ")}
	}

	class, _ := coreplanexecute.ClassifyPlanExecuteTerminalCompletion(record)
	if class == coreplanexecute.PlanExecuteTerminalClassSuccess {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalCompleted}
	}

	if record.ExitCode != 0 {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute reported non-zero exit code"}
	}

	if CompletionFieldString(fields, completionFieldFailureKind) != "" || CompletionFieldString(fields, completionFieldFailureReason) != "" {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute reported failure details"}
	}

	switch terminalClass {
	case "recovery_required", "failed":
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute terminal class requires recovery"}
	}

	if containsBlockingPlanExecuteCompletionSignal(status) ||
		containsBlockingPlanExecuteCompletionSignal(CompletionFieldString(fields, completionFieldPhase)) ||
		containsBlockingPlanExecuteCompletionSignal(CompletionFieldString(fields, completionFieldReviewStatus)) ||
		containsBlockingPlanExecuteCompletionSignal(CompletionFieldString(fields, completionFieldOutcome)) {

		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute status is not success-shaped"}
	}

	if status != "" {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: fmt.Sprintf("ambiguous nested plan-execute status %q", CompletionFieldString(fields, completionFieldStatus))}
	}

	return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute status is not success-shaped"}
}

func canonicalPlanExecuteCompletionStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	status = strings.ReplaceAll(status, "_", "-")

	switch status {
	case "complete":
		return "completed"
	case "successful":
		return "success"
	default:
		return status
	}
}

func containsBlockingPlanExecuteCompletionSignal(value string) bool {
	value = canonicalPlanExecuteCompletionStatus(value)
	for _, signal := range []string{
		"failed",
		"failure",
		"model-error",
		"review-model-error",
		"recovery-open",
		"blocked",
		"blocker",
		"degraded",
		coreplanexecute.ProgressStatusRunning,
	} {
		if strings.Contains(value, signal) {
			return true
		}
	}

	return false
}

func completionFieldBool(fields map[string]any, key string) bool {
	value, ok := fields[key]
	if !ok || value == nil {
		return false
	}

	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return util.EqualTrimFold(typed, "true")
	default:
		return util.EqualTrimFold(fmt.Sprint(value), "true")
	}
}

func completionFieldInt(fields map[string]any, key string) int {
	value, ok := fields[key]
	if !ok || value == nil {
		return 0
	}

	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}

	parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil {
		return 0
	}

	return parsed
}

func completionFieldStringSlice(fields map[string]any, key string) []string {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	switch typed := value.(type) {
	case []string:
		return normalizeCompletionStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				out = append(out, text)
			}
		}

		return normalizeCompletionStringList(out)
	default:
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			return []string{text}
		}

		return nil
	}
}

func completionFieldSourceSatisfiedEvidenceRefs(fields map[string]any, key string) []PlanExecuteSourceSatisfiedEvidenceRef {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var refs []PlanExecuteSourceSatisfiedEvidenceRef

	if err := json.Unmarshal(body, &refs); err != nil {
		return nil
	}

	return normalizePlanExecuteSourceSatisfiedEvidenceRefs(refs)
}

func completionFieldPhaseEvidenceChecks(fields map[string]any, key string) []coreplanexecute.PhaseEvidenceCheck {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var checks []coreplanexecute.PhaseEvidenceCheck

	if err := json.Unmarshal(body, &checks); err != nil {
		return nil
	}

	return normalizeCompletionPhaseEvidenceChecks(checks)
}

func completionFieldVerificationCommands(fields map[string]any, key string) []coreplanexecute.VerificationCommand {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var commands []coreplanexecute.VerificationCommand
	if err := json.Unmarshal(body, &commands); err != nil {
		return nil
	}

	return normalizeCompletionVerificationCommands(commands)
}

func completionFieldVerificationSkips(fields map[string]any, key string) []coreplanexecute.VerificationSkip {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var skips []coreplanexecute.VerificationSkip
	if err := json.Unmarshal(body, &skips); err != nil {
		return nil
	}

	return normalizeCompletionVerificationSkips(skips)
}

func completionFieldVerificationRejects(fields map[string]any, key string) []coreplanexecute.VerificationReject {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var rejects []coreplanexecute.VerificationReject
	if err := json.Unmarshal(body, &rejects); err != nil {
		return nil
	}

	return normalizeCompletionVerificationRejects(rejects)
}

func normalizeCompletionPhaseEvidenceChecks(checks []coreplanexecute.PhaseEvidenceCheck) []coreplanexecute.PhaseEvidenceCheck {
	out := make([]coreplanexecute.PhaseEvidenceCheck, 0, len(checks))
	seen := map[string]bool{}

	for _, check := range checks {
		check.Kind = strings.TrimSpace(check.Kind)
		check.Path = plancontract.NormalizeScopePath(check.Path)
		check.Status = strings.TrimSpace(check.Status)
		check.Summary = strings.TrimSpace(check.Summary)
		check.Error = strings.TrimSpace(check.Error)
		check.Command = strings.TrimSpace(check.Command)

		if check.Kind == "" && check.Path == "" && check.Status == "" && check.Command == "" {
			continue
		}

		key := strings.Join([]string{check.Kind, check.Path, check.Command, check.Status}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, check)
	}

	return out
}

func normalizeCompletionVerificationCommands(commands []coreplanexecute.VerificationCommand) []coreplanexecute.VerificationCommand {
	out := make([]coreplanexecute.VerificationCommand, 0, len(commands))
	seen := map[string]bool{}

	for _, command := range commands {
		command.Command = plancontract.CanonicalVerificationCommand(command.Command, command.Argv)
		command.WorkingDir = strings.TrimSpace(command.WorkingDir)
		command.Source = strings.TrimSpace(command.Source)
		command.Path = plancontract.NormalizeScopePath(command.Path)
		command.Reason = strings.TrimSpace(command.Reason)
		command.Argv = plancontract.NormalizeVerificationArgv(command.Argv)
		if len(command.Argv) == 0 {
			if parsed, ok := plancontract.VerificationCommandArgs(command.Command); ok {
				command.Argv = parsed
			}
		}

		if command.Command == "" && len(command.Argv) == 0 {
			continue
		}

		key := plancontract.VerificationCommandEvidenceKey(command.Command, command.Argv, command.WorkingDir)
		if key == "" {
			continue
		}
		key = strings.Join([]string{key, command.Source, command.Path}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, command)
	}

	return out
}

func normalizeCompletionVerificationSkips(skips []coreplanexecute.VerificationSkip) []coreplanexecute.VerificationSkip {
	out := make([]coreplanexecute.VerificationSkip, 0, len(skips))
	seen := map[string]bool{}

	for _, skip := range skips {
		skip.Path = plancontract.NormalizeScopePath(skip.Path)
		skip.Reason = strings.TrimSpace(skip.Reason)
		skip.Detail = strings.TrimSpace(skip.Detail)

		if skip.Path == "" && skip.Reason == "" && skip.Detail == "" {
			continue
		}

		key := strings.Join([]string{skip.Path, skip.Reason, skip.Detail}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, skip)
	}

	return out
}

func normalizeCompletionVerificationRejects(rejects []coreplanexecute.VerificationReject) []coreplanexecute.VerificationReject {
	out := make([]coreplanexecute.VerificationReject, 0, len(rejects))
	seen := map[string]bool{}

	for _, reject := range rejects {
		reject.Command = strings.TrimSpace(reject.Command)
		reject.Source = strings.TrimSpace(reject.Source)
		reject.Reason = strings.TrimSpace(reject.Reason)

		if reject.Command == "" && reject.Source == "" && reject.Reason == "" {
			continue
		}

		key := strings.Join([]string{reject.Command, reject.Source, reject.Reason}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, reject)
	}

	return out
}

func normalizePlanExecuteSourceSatisfiedEvidenceRefs(refs []PlanExecuteSourceSatisfiedEvidenceRef) []PlanExecuteSourceSatisfiedEvidenceRef {
	out := make([]PlanExecuteSourceSatisfiedEvidenceRef, 0, len(refs))
	seen := map[string]bool{}

	for _, ref := range refs {
		ref.Path = plancontract.NormalizeScopePath(ref.Path)
		ref.Kind = strings.TrimSpace(ref.Kind)

		if ref.Path == "" || ref.Kind == "" {
			continue
		}

		key := ref.Path + "\x00" + ref.Kind
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, ref)
	}

	return out
}

func normalizeCompletionStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		out = append(out, value)
	}

	return out
}

func completionFieldStableIDSlice(fields map[string]any, key string) []string {
	return normalizeCompletionStableIDList(completionFieldStringSlice(fields, key))
}

func normalizeCompletionStableIDList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}

	for _, value := range values {
		value = plancontract.NormalizeStableContractIDPart(value)
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		out = append(out, value)
	}

	return out
}

func planExecuteCompletionRecordRelevant(recordType string, role string, content string, stopReason string) bool {
	recordType = strings.TrimSpace(recordType)
	if strings.HasPrefix(recordType, "idiom.plan_execute.") {
		return true
	}

	if recordType == "session_summary" || strings.TrimSpace(role) == "session_summary" {
		return true
	}

	return planExecuteCompletionModelError(stopReason, content)
}

func planExecuteCompletionRecordTrustsAcceptanceEvidence(recordType string) bool {
	return strings.TrimSpace(recordType) == coreplanexecute.PlanExecuteTerminalCompletionRecordType
}

func planExecuteCompletionModelError(stopReason string, content string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(stopReason)), "model_error") {
		return true
	}

	return strings.Contains(strings.ToLower(content), "stop_reason=model_error")
}

func CompletionFieldString(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprint(value))
}
