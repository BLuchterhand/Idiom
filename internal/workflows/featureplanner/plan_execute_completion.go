package featureplanner

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/textutil"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/recovery"
)

// PlanExecuteCompletionSummaryFromOutput extracts the nested plan-execute
// terminal contract from JSONL output. Both TUI and headless feature-planner
// adapters use this parser so degraded review/model-error evidence is
// interpreted once.
func PlanExecuteCompletionSummaryFromOutput(output string) PlanExecuteCompletionSummary {
	summary := PlanExecuteCompletionSummary{}
	sequence := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
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

		recordSummary := planExecuteCompletionSummaryFromRecordFields(record, fields)

		recordSummary.sequence = sequence
		summary = MergePlanExecuteCompletionSummaries(summary, recordSummary)
	}

	return summary.normalized()
}

func PlanExecuteRecoveryRetryPromptForSummary(prompt string, completion PlanExecuteCompletionSummary) (string, bool) {
	completion = completion.normalized()
	requiredPaths := append([]string(nil), completion.RequiredEditPaths...)
	refresh, ok := recovery.PlanExecutePlannerBlockedRetryRefreshResult(
		completion.FailureKind,
		completion.FailureReason+"\n"+completion.Summary,
		requiredPaths,
		recoverycontract.NextAction(strings.TrimSpace(completion.NextAction)),
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

func TrustObservedPlanExecuteCompletionFields(fields map[string]any) {
	if fields == nil {
		return
	}

	trusted := TrustObservedPlanExecuteCompletionSummary(PlanExecuteCompletionSummaryFromFields(fields))
	for key := range fields {
		delete(fields, key)
	}
	for key, value := range trusted.Fields() {
		fields[key] = value
	}
}

func TrustObservedPlanExecuteCompletionSummary(summary PlanExecuteCompletionSummary) PlanExecuteCompletionSummary {
	summary = summary.normalized()

	if len(summary.AcceptanceEvidenceIDs) == 0 {
		summary.AcceptanceEvidenceTrusted = false
		return summary
	}

	if PlanExecuteCompletionTerminalDecision(summary.Fields()).Class != plancontract.PlanExecuteTerminalDecisionCompleted {
		summary.AcceptanceEvidenceTrusted = false
		return summary
	}

	summary.AcceptanceEvidenceTrusted = true

	return summary
}

type planExecuteCompletionRecord struct {
	Type        string `json:"type"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	ReasonClass string `json:"reason_class"`
	StopReason  string `json:"stop_reason"`
}

type PlanExecuteCompletionSummary struct {
	Tool                         string                                    `json:"tool,omitempty"`
	LaneFamily                   string                                    `json:"lane_family,omitempty"`
	Summary                      string                                    `json:"summary,omitempty"`
	ProgressPath                 string                                    `json:"progress_path,omitempty"`
	Status                       string                                    `json:"status,omitempty"`
	Phase                        string                                    `json:"phase,omitempty"`
	ReviewStatus                 string                                    `json:"review_status,omitempty"`
	TerminalClass                string                                    `json:"terminal_class,omitempty"`
	Outcome                      string                                    `json:"outcome,omitempty"`
	ExitCode                     int                                       `json:"exit_code,omitempty"`
	FailureKind                  string                                    `json:"failure_kind,omitempty"`
	FailureReason                string                                    `json:"failure_reason,omitempty"`
	FailureID                    string                                    `json:"failure_id,omitempty"`
	RecordPath                   string                                    `json:"record_path,omitempty"`
	PacketPath                   string                                    `json:"packet_path,omitempty"`
	NextAction                   string                                    `json:"next_valid_action,omitempty"`
	CompletionSource             string                                    `json:"completion_source,omitempty"`
	ReviewRepairSkipped          bool                                      `json:"review_repair_skipped,omitempty"`
	ContractID                   string                                    `json:"contract_id,omitempty"`
	ContractHash                 string                                    `json:"contract_hash,omitempty"`
	ScopeContractID              string                                    `json:"scope_contract_id,omitempty"`
	ScopeContractHash            string                                    `json:"scope_contract_hash,omitempty"`
	SnapshotHash                 string                                    `json:"snapshot_hash,omitempty"`
	RequiredEditPaths            []string                                  `json:"required_edit_paths,omitempty"`
	AllowedEditPaths             []string                                  `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths            []string                                  `json:"write_allowed_paths,omitempty"`
	SourceSatisfiedEvidencePaths []string                                  `json:"source_satisfied_evidence_paths,omitempty"`
	SourceSatisfiedEvidence      []plancontract.SourceSatisfiedEvidenceRef `json:"source_satisfied_evidence,omitempty"`
	ChangedPaths                 []string                                  `json:"changed_paths,omitempty"`
	SourceSanity                 []plancontract.PhaseEvidenceCheck         `json:"source_sanity,omitempty"`
	Verification                 []plancontract.PhaseEvidenceCheck         `json:"verification,omitempty"`
	VerificationCommands         []plancontract.VerificationCommand        `json:"verification_commands,omitempty"`
	VerificationSkips            []plancontract.VerificationSkip           `json:"verification_skips,omitempty"`
	VerificationRejects          []plancontract.VerificationReject         `json:"verification_rejects,omitempty"`
	AcceptanceEvidenceIDs        []string                                  `json:"acceptance_evidence_ids,omitempty"`
	AcceptanceEvidenceTrusted    bool                                      `json:"acceptance_evidence_trusted,omitempty"`
	ValidationFailures           []string                                  `json:"validation_failures,omitempty"`

	timestamp      time.Time                                         `json:"-"`
	hasTimestamp   bool                                              `json:"-"`
	sequence       int                                               `json:"-"`
	terminalRecord *plancontract.PlanExecuteTerminalCompletionRecord `json:"-"`
}

const (
	PlanExecuteCompletionStatusAmbiguous            = "ambiguous"
	PlanExecuteCompletionMissingStatusFailureKind   = string(plancontract.PlanExecuteFailureMissingNestedStatus)
	PlanExecuteCompletionMissingStatusFailureReason = "nested plan-execute returned without typed completion status"
)

var PlanExecuteProgressPreservedFieldKeys = []string{
	plancontract.PlanExecuteTerminalFieldRequiredEditPaths,
	plancontract.PlanExecuteTerminalFieldAllowedEditPaths,
	plancontract.PlanExecuteTerminalFieldChangedPaths,
	plancontract.PlanExecuteTerminalFieldSourceSanity,
}

var PlanExecuteTerminalPreservedFieldKeys = []string{
	plancontract.PlanExecuteTerminalFieldRequiredEditPaths,
	plancontract.PlanExecuteTerminalFieldAllowedEditPaths,
	plancontract.PlanExecuteTerminalFieldWriteAllowedPaths,
	plancontract.PlanExecuteTerminalFieldChangedPaths,
	plancontract.PlanExecuteTerminalFieldSourceSanity,
	plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths,
	plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidence,
	plancontract.PlanExecuteTerminalFieldContractHash,
	plancontract.PlanExecuteTerminalFieldCompletionPath,
}

func PlanExecuteCompletionSummaryFromFields(fields map[string]any) PlanExecuteCompletionSummary {
	if fields == nil {
		return PlanExecuteCompletionSummary{}
	}

	summary := PlanExecuteCompletionSummary{
		LaneFamily:                   CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldLaneFamily),
		Tool:                         CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldTool),
		Summary:                      CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldSummary),
		ProgressPath:                 textutil.FirstNonEmpty(CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldProgressPath), CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatePath)),
		Status:                       CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatus),
		Phase:                        CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldPhase),
		ReviewStatus:                 CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldReviewStatus),
		TerminalClass:                CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldTerminalClass),
		Outcome:                      CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldOutcome),
		ExitCode:                     completionFieldInt(fields, plancontract.PlanExecuteTerminalFieldExitCode),
		FailureKind:                  CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldFailureKind),
		FailureReason:                CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldFailureReason),
		FailureID:                    CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldFailureID),
		RecordPath:                   CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldRecordPath),
		PacketPath:                   CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldPacketPath),
		NextAction:                   CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldNextAction),
		CompletionSource:             CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldCompletionSource),
		ReviewRepairSkipped:          completionFieldBool(fields, plancontract.PlanExecuteTerminalFieldReviewRepairSkipped),
		ContractID:                   textutil.FirstNonEmpty(CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldContractID), CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldActiveContractID)),
		ContractHash:                 textutil.FirstNonEmpty(CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldContractHash), CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldActiveContractHash)),
		ScopeContractID:              CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldScopeContractID),
		ScopeContractHash:            CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldScopeContractHash),
		SnapshotHash:                 textutil.FirstNonEmpty(CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldSnapshotHash), CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldContractSnapshotHash)),
		RequiredEditPaths:            completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldRequiredEditPaths),
		AllowedEditPaths:             completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldAllowedEditPaths),
		WriteAllowedPaths:            completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldWriteAllowedPaths),
		SourceSatisfiedEvidencePaths: completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths),
		SourceSatisfiedEvidence:      completionFieldSourceSatisfiedEvidenceRefs(fields, plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidence),
		ChangedPaths:                 completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldChangedPaths),
		SourceSanity:                 completionFieldPhaseEvidenceChecks(fields, plancontract.PlanExecuteTerminalFieldSourceSanity),
		Verification:                 completionFieldPhaseEvidenceChecks(fields, plancontract.PlanExecuteTerminalFieldVerification),
		VerificationCommands:         completionFieldVerificationCommands(fields, plancontract.PlanExecuteTerminalFieldVerificationCommands),
		VerificationSkips:            completionFieldVerificationSkips(fields, plancontract.PlanExecuteTerminalFieldVerificationSkips),
		VerificationRejects:          completionFieldVerificationRejects(fields, plancontract.PlanExecuteTerminalFieldVerificationRejects),
		AcceptanceEvidenceIDs:        completionFieldStableIDSlice(fields, plancontract.PlanExecuteTerminalFieldAcceptanceEvidenceIDs),
		AcceptanceEvidenceTrusted:    completionFieldBool(fields, featureplannercontract.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted),
		ValidationFailures:           completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldValidationFailures),
	}
	if ts := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldTS); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			summary.timestamp = parsed
			summary.hasTimestamp = true
		}
	}
	if record, ok := planExecuteTerminalCompletionRecordFromFields(fields); ok {
		summary.terminalRecord = &record
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

func (summary PlanExecuteCompletionSummary) Fields() map[string]any {
	summary = summary.normalized()

	fields := map[string]any{}
	if summary.terminalRecord != nil {
		for key, value := range plancontract.PlanExecuteTerminalCompletionEventFields(*summary.terminalRecord, "") {
			fields[key] = value
		}
	}

	if summary.Tool != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldTool] = summary.Tool
	}

	if summary.LaneFamily != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldLaneFamily] = summary.LaneFamily
	}

	if summary.Summary != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldSummary] = summary.Summary
	}

	if summary.CompletionSource != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldCompletionSource] = summary.CompletionSource
	}

	if summary.terminalRecord == nil {
		if summary.ProgressPath != "" {
			fields[plancontract.PlanExecuteTerminalFieldProgressPath] = summary.ProgressPath
		}

		if summary.Status != "" {
			fields[plancontract.PlanExecuteTerminalFieldStatus] = summary.Status
		}

		if summary.Phase != "" {
			fields[plancontract.PlanExecuteTerminalFieldPhase] = summary.Phase
		}

		if summary.ReviewStatus != "" {
			fields[plancontract.PlanExecuteTerminalFieldReviewStatus] = summary.ReviewStatus
		}

		if summary.TerminalClass != "" {
			fields[plancontract.PlanExecuteTerminalFieldTerminalClass] = summary.TerminalClass
		}

		if summary.Outcome != "" {
			fields[plancontract.PlanExecuteTerminalFieldOutcome] = summary.Outcome
		}

		if summary.ExitCode != 0 {
			fields[plancontract.PlanExecuteTerminalFieldExitCode] = summary.ExitCode
		}

		if summary.FailureKind != "" {
			fields[plancontract.PlanExecuteTerminalFieldFailureKind] = summary.FailureKind
		}

		if summary.FailureReason != "" {
			fields[plancontract.PlanExecuteTerminalFieldFailureReason] = summary.FailureReason
		}

		if summary.FailureID != "" {
			fields[plancontract.PlanExecuteTerminalFieldFailureID] = summary.FailureID
		}

		if summary.RecordPath != "" {
			fields[plancontract.PlanExecuteTerminalFieldRecordPath] = summary.RecordPath
		}

		if summary.PacketPath != "" {
			fields[plancontract.PlanExecuteTerminalFieldPacketPath] = summary.PacketPath
		}

		if summary.NextAction != "" {
			fields[plancontract.PlanExecuteTerminalFieldNextAction] = summary.NextAction
		}

		if summary.ReviewRepairSkipped {
			fields[plancontract.PlanExecuteTerminalFieldReviewRepairSkipped] = true
		}

		if summary.ContractID != "" {
			fields[plancontract.PlanExecuteTerminalFieldContractID] = summary.ContractID
		}

		if summary.ScopeContractID != "" {
			fields[plancontract.PlanExecuteTerminalFieldScopeContractID] = summary.ScopeContractID
		}

		if summary.ScopeContractHash != "" {
			fields[plancontract.PlanExecuteTerminalFieldScopeContractHash] = summary.ScopeContractHash
		}

		if summary.SnapshotHash != "" {
			fields[plancontract.PlanExecuteTerminalFieldSnapshotHash] = summary.SnapshotHash
		}

		if len(summary.RequiredEditPaths) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldRequiredEditPaths] = append([]string(nil), summary.RequiredEditPaths...)
		}

		if len(summary.AllowedEditPaths) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldAllowedEditPaths] = append([]string(nil), summary.AllowedEditPaths...)
		}

		if len(summary.WriteAllowedPaths) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldWriteAllowedPaths] = append([]string(nil), summary.WriteAllowedPaths...)
		}

		if len(summary.SourceSatisfiedEvidencePaths) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths] = append([]string(nil), summary.SourceSatisfiedEvidencePaths...)
		}

		if len(summary.SourceSatisfiedEvidence) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidence] = append([]plancontract.SourceSatisfiedEvidenceRef(nil), summary.SourceSatisfiedEvidence...)
		}

		if len(summary.ChangedPaths) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldChangedPaths] = append([]string(nil), summary.ChangedPaths...)
		}

		if len(summary.SourceSanity) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldSourceSanity] = append([]plancontract.PhaseEvidenceCheck(nil), summary.SourceSanity...)
		}

		if len(summary.Verification) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldVerification] = append([]plancontract.PhaseEvidenceCheck(nil), summary.Verification...)
		}

		if len(summary.VerificationCommands) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldVerificationCommands] = append([]plancontract.VerificationCommand(nil), summary.VerificationCommands...)
		}

		if len(summary.VerificationSkips) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldVerificationSkips] = append([]plancontract.VerificationSkip(nil), summary.VerificationSkips...)
		}

		if len(summary.VerificationRejects) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldVerificationRejects] = append([]plancontract.VerificationReject(nil), summary.VerificationRejects...)
		}

		if len(summary.AcceptanceEvidenceIDs) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldAcceptanceEvidenceIDs] = append([]string(nil), summary.AcceptanceEvidenceIDs...)
		}

		if len(summary.ValidationFailures) > 0 {
			fields[plancontract.PlanExecuteTerminalFieldValidationFailures] = append([]string(nil), summary.ValidationFailures...)
		}
	}

	if summary.AcceptanceEvidenceTrusted {
		fields[featureplannercontract.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted] = true
	}

	return fields
}

func (summary PlanExecuteCompletionSummary) FeaturePlannerPayloadFields() map[string]any {
	summary = summary.normalized()
	fields := summary.Fields()
	if strings.TrimSpace(summary.ContractHash) != "" {
		fields[plancontract.PlanExecuteTerminalFieldContractHash] = strings.TrimSpace(summary.ContractHash)
	}
	return fields
}

func planExecuteCompletionSummaryFromRecordFields(record planExecuteCompletionRecord, fields map[string]any) PlanExecuteCompletionSummary {
	summary := PlanExecuteCompletionSummaryFromFields(fields)
	if terminalRecord, ok := planExecuteTerminalCompletionRecordFromFields(fields); ok {
		summary.terminalRecord = &terminalRecord
	}

	if len(summary.AcceptanceEvidenceIDs) > 0 && planExecuteCompletionRecordTrustsAcceptanceEvidence(record.Type) {
		summary.AcceptanceEvidenceTrusted = true
	}

	if eventType, ok := plancontract.ParsePlanExecuteInvalidEventName(record.Type); ok {
		summary.Status = textutil.FirstNonEmpty(summary.Status, string(plancontract.PlanExecuteTerminalStatusFailed))
		summary.TerminalClass = textutil.FirstNonEmpty(summary.TerminalClass, string(plancontract.PlanExecuteTerminalClassFailed))
		summary.FailureKind = textutil.FirstNonEmpty(summary.FailureKind, string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed))
		summary.ValidationFailures = append(summary.ValidationFailures, "invalid_plan_execute_event:"+string(eventType))
	}

	if record.Type == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairDegraded) {
		reviewStatus := summary.Status
		summary.Status = string(plancontract.PlanExecuteTerminalStatusDegraded)
		summary.ReviewStatus = textutil.FirstNonEmpty(summary.ReviewStatus, reviewStatus, string(plancontract.PlanExecuteTerminalStatusDegraded))
		summary.FailureKind = textutil.FirstNonEmpty(summary.FailureKind, record.ReasonClass, string(plancontract.PlanExecuteFailureReviewRepairDegraded))
	}

	if planExecuteCompletionSourceSatisfiedEvent(record.Type) && summary.terminalRecord == nil {
		summary.Status = ""
		summary.Outcome = ""
	}

	if planExecuteCompletionModelError(record.StopReason, record.Content) {
		summary.Status = textutil.FirstNonEmpty(summary.Status, string(plancontract.PlanExecuteTerminalStatusFailed))
		summary.FailureKind = textutil.FirstNonEmpty(summary.FailureKind, string(plancontract.PlanExecuteFailureModelError))
		summary.FailureReason = textutil.FirstNonEmpty(summary.FailureReason, "nested plan-execute stopped with model_error")
	}

	return summary.normalized()
}

func planExecuteCompletionSourceSatisfiedEvent(recordType string) bool {
	return recordType == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfied) ||
		recordType == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventAutoExactPlanSourceSatisfied)
}

func planExecuteTerminalCompletionRecordFromFields(fields map[string]any) (plancontract.PlanExecuteTerminalCompletionRecord, bool) {
	if fields == nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false
	}
	recordType := CompletionFieldString(fields, "type")
	if recordType != plancontract.PlanExecuteTerminalCompletionRecordType {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false
	}

	record := plancontract.PlanExecuteTerminalCompletionRecordFromFields(fields)
	return record, true
}

func (summary PlanExecuteCompletionSummary) normalized() PlanExecuteCompletionSummary {
	summary.Tool = strings.TrimSpace(summary.Tool)
	summary.LaneFamily = strings.TrimSpace(summary.LaneFamily)
	summary.Summary = strings.TrimSpace(summary.Summary)
	summary.ProgressPath = strings.TrimSpace(summary.ProgressPath)
	summary.Status = strings.TrimSpace(summary.Status)
	summary.Phase = strings.TrimSpace(summary.Phase)
	summary.ReviewStatus = strings.TrimSpace(summary.ReviewStatus)
	summary.TerminalClass = strings.TrimSpace(summary.TerminalClass)
	summary.Outcome = strings.TrimSpace(summary.Outcome)

	if strings.EqualFold(summary.Status, string(plancontract.PlanExecuteTerminalStatusSourceSatisfied)) && summary.Outcome == "" {
		summary.Outcome = string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied)
	}

	summary.FailureKind = strings.TrimSpace(summary.FailureKind)
	summary.FailureReason = strings.TrimSpace(summary.FailureReason)

	if strings.EqualFold(summary.Outcome, string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied)) && summary.FailureKind == "" && summary.FailureReason == "" {
		summary.Status = string(plancontract.PlanExecuteTerminalStatusSourceSatisfied)
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
	summary.WriteAllowedPaths = normalizeCompletionStringList(summary.WriteAllowedPaths)
	summary.SourceSatisfiedEvidencePaths = normalizeCompletionStringList(summary.SourceSatisfiedEvidencePaths)
	summary.SourceSatisfiedEvidence = plancontract.NormalizeSourceSatisfiedEvidenceRefs(summary.SourceSatisfiedEvidence)
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
	if summary.terminalRecord != nil {
		normalized := plancontract.NormalizePlanExecuteTerminalCompletionRecord(*summary.terminalRecord)
		summary.terminalRecord = &normalized
	}

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

	if len(summary.WriteAllowedPaths) == 0 {
		summary.WriteAllowedPaths = append([]string(nil), other.WriteAllowedPaths...)
	}

	if len(summary.SourceSatisfiedEvidencePaths) == 0 {
		summary.SourceSatisfiedEvidencePaths = append([]string(nil), other.SourceSatisfiedEvidencePaths...)
	}

	if len(summary.SourceSatisfiedEvidence) == 0 {
		summary.SourceSatisfiedEvidence = append([]plancontract.SourceSatisfiedEvidenceRef(nil), other.SourceSatisfiedEvidence...)
	}

	if len(summary.ChangedPaths) == 0 {
		summary.ChangedPaths = append([]string(nil), other.ChangedPaths...)
	}

	if plancontract.PlanExecuteTerminalStatusIsSkipped(summary.Status) || plancontract.PlanExecuteTerminalStatusIsSkipped(summary.ReviewStatus) {
		summary.ReviewRepairSkipped = summary.ReviewRepairSkipped || other.ReviewRepairSkipped
	}
	if summary.terminalRecord == nil && other.terminalRecord != nil {
		record := *other.terminalRecord
		summary.terminalRecord = &record
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

	return plancontract.PlanExecuteTerminalStatusIsTerminal(summary.Status) ||
		plancontract.PlanExecuteTerminalStatusIsTerminal(summary.ReviewStatus) ||
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

	record := plancontract.PlanExecuteTerminalCompletionRecordFromFields(summary.Fields())

	return record.TerminalClass != plancontract.PlanExecuteTerminalClassSuccess
}

func planExecuteCompletionSourceSanityValidationFailures(summary PlanExecuteCompletionSummary) []string {
	record := plancontract.PlanExecuteTerminalCompletionRecordFromFields(summary.Fields())

	return normalizeCompletionStringList(plancontract.PlanExecuteTerminalSourceSanityValidationFailures(record))
}

func planExecuteCompletionVerificationValidationFailures(summary PlanExecuteCompletionSummary) []string {
	summary = summary.normalized()
	var failures []string

	for _, reject := range summary.VerificationRejects {
		failures = append(failures, "verification_rejected:"+firstCompletionString(reject.Command, reject.Reason))
	}

	evidenceByCommand := map[string]plancontract.PhaseEvidenceCheck{}
	for _, check := range summary.Verification {
		if !plancontract.PhaseEvidenceCheckIsPassed(check) {
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

		if !plancontract.PhaseEvidenceCheckIsPassed(check) {
			failures = append(failures, "verification_not_passed:"+command.Command)
		}
	}

	return normalizeCompletionStringList(failures)
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

func PlanExecuteCompletionEventTypeForSummary(summary PlanExecuteCompletionSummary) featureplannercontract.NestedPlanExecuteCompletionEventName {
	return PlanExecuteCompletionEventTypeForFields(summary.Fields())
}

func PlanExecuteCompletionEventTypeForFields(fields map[string]any) featureplannercontract.NestedPlanExecuteCompletionEventName {
	if CompletionFieldString(fields, featureplannercontract.PlanExecuteCompletionFieldTool) != string(featureplannercontract.ToolPlanExecuteComplete) {
		return featureplannercontract.NestedPlanExecuteCompletionEventBlocked
	}

	switch PlanExecuteCompletionTerminalDecision(fields).Class {
	case plancontract.PlanExecuteTerminalDecisionCompleted:
		return featureplannercontract.NestedPlanExecuteCompletionEventCompleted
	default:
		return featureplannercontract.NestedPlanExecuteCompletionEventBlocked
	}
}

func PlanExecuteCompletionTerminalDecision(fields map[string]any) plancontract.PlanExecuteTerminalDecision {
	summary := PlanExecuteCompletionSummaryFromFields(fields)
	derivedFailures := append(planExecuteCompletionSourceSanityValidationFailures(summary), planExecuteCompletionVerificationValidationFailures(summary)...)
	failures := normalizeCompletionStringList(append(completionFieldStringSlice(fields, plancontract.PlanExecuteTerminalFieldValidationFailures), derivedFailures...))
	return plancontract.DecidePlanExecuteTerminalCompletionFields(fields, failures)
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

func completionFieldBool(fields map[string]any, key string) bool {
	value, ok := fields[key]
	if !ok || value == nil {
		return false
	}

	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return textutil.EqualTrimFold(typed, "true")
	default:
		return textutil.EqualTrimFold(fmt.Sprint(value), "true")
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

func completionFieldSourceSatisfiedEvidenceRefs(fields map[string]any, key string) []plancontract.SourceSatisfiedEvidenceRef {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var refs []plancontract.SourceSatisfiedEvidenceRef

	if err := json.Unmarshal(body, &refs); err != nil {
		return nil
	}

	return plancontract.NormalizeSourceSatisfiedEvidenceRefs(refs)
}

func completionFieldPhaseEvidenceChecks(fields map[string]any, key string) []plancontract.PhaseEvidenceCheck {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var checks []plancontract.PhaseEvidenceCheck

	if err := json.Unmarshal(body, &checks); err != nil {
		return nil
	}

	return normalizeCompletionPhaseEvidenceChecks(checks)
}

func completionFieldVerificationCommands(fields map[string]any, key string) []plancontract.VerificationCommand {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var commands []plancontract.VerificationCommand
	if err := json.Unmarshal(body, &commands); err != nil {
		return nil
	}

	return normalizeCompletionVerificationCommands(commands)
}

func completionFieldVerificationSkips(fields map[string]any, key string) []plancontract.VerificationSkip {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var skips []plancontract.VerificationSkip
	if err := json.Unmarshal(body, &skips); err != nil {
		return nil
	}

	return normalizeCompletionVerificationSkips(skips)
}

func completionFieldVerificationRejects(fields map[string]any, key string) []plancontract.VerificationReject {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	var rejects []plancontract.VerificationReject
	if err := json.Unmarshal(body, &rejects); err != nil {
		return nil
	}

	return normalizeCompletionVerificationRejects(rejects)
}

func normalizeCompletionPhaseEvidenceChecks(checks []plancontract.PhaseEvidenceCheck) []plancontract.PhaseEvidenceCheck {
	out := make([]plancontract.PhaseEvidenceCheck, 0, len(checks))
	seen := map[string]bool{}

	for _, check := range checks {
		check.Kind = plancontract.PhaseEvidenceKind(strings.TrimSpace(string(check.Kind)))
		check.Path = plancontract.NormalizeScopePath(check.Path)
		check.Status = plancontract.PhaseEvidenceStatus(strings.TrimSpace(string(check.Status)))
		check.Summary = strings.TrimSpace(check.Summary)
		check.Error = strings.TrimSpace(check.Error)
		check.Command = strings.TrimSpace(check.Command)

		if check.Kind == "" && check.Path == "" && check.Status == "" && check.Command == "" {
			continue
		}

		key := strings.Join([]string{string(check.Kind), check.Path, check.Command, string(check.Status)}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, check)
	}

	return out
}

func normalizeCompletionVerificationCommands(commands []plancontract.VerificationCommand) []plancontract.VerificationCommand {
	out := make([]plancontract.VerificationCommand, 0, len(commands))
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

func normalizeCompletionVerificationSkips(skips []plancontract.VerificationSkip) []plancontract.VerificationSkip {
	out := make([]plancontract.VerificationSkip, 0, len(skips))
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

func normalizeCompletionVerificationRejects(rejects []plancontract.VerificationReject) []plancontract.VerificationReject {
	out := make([]plancontract.VerificationReject, 0, len(rejects))
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
	if _, ok := plancontract.ParsePlanExecuteEventName(recordType); ok {
		return true
	}
	if _, ok := plancontract.ParsePlanExecuteInvalidEventName(recordType); ok {
		return true
	}

	if recordType == "session_summary" || strings.TrimSpace(role) == "session_summary" {
		return true
	}

	return planExecuteCompletionModelError(stopReason, content)
}

func planExecuteCompletionRecordTrustsAcceptanceEvidence(recordType string) bool {
	return strings.TrimSpace(recordType) == plancontract.PlanExecuteTerminalCompletionRecordType
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
