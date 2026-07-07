package agent

import (
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

type turnDiagnosticKind string
type turnViolationKind string
type turnReasonCode string

const (
	turnDiagnosticObjectiveRelevance turnDiagnosticKind = "objective_relevance"
)

const answerObjectiveUnmetInsufficientEvidenceClaimed = "answer_objective_unmet:insufficient_evidence_claimed"

const (
	turnReasonPlanExecuteAnchorRefreshPending     turnReasonCode = "plan_execute_anchor_refresh_pending"
	turnReasonGateSatisfactionMetaFinalAnswer     turnReasonCode = "gate_satisfaction_meta_final_answer"
	turnReasonNonSelfContainedFinalAnswer         turnReasonCode = "non_self_contained_final_answer"
	turnReasonEmbeddedQuestionProtocolMarker      turnReasonCode = "embedded_question_protocol_marker"
	turnReasonUnexecutedToolRequestAnswer         turnReasonCode = "unexecuted_tool_request_final_answer"
	turnReasonRequiredEditPathsRemaining          turnReasonCode = "required_edit_paths_remaining"
	turnReasonRejectedMutationPathsRemaining      turnReasonCode = "rejected_mutation_paths_remaining"
	turnReasonUserVisibleBlocker                  turnReasonCode = "user_visible_blocker"
	turnReasonReadOnlyProtocolBlocker             turnReasonCode = "read_only_protocol_blocker"
	turnReasonAnswerableEvidenceInsufficient      turnReasonCode = "answerable_evidence_reported_as_insufficient"
	turnReasonInsufficientEvidenceAnswer          turnReasonCode = "insufficient_evidence_answer"
	turnReasonLeakedImplementationBlocker         turnReasonCode = "leaked_implementation_blocker_template"
	turnReasonNonAnswerProse                      turnReasonCode = "non_answer_prose"
	turnReasonSourceGroundingUnmet                turnReasonCode = "source_grounding_unmet"
	turnReasonRequiredEvidenceUnmet               turnReasonCode = "required_evidence_unmet"
	turnReasonTransportProtocolOutput             turnReasonCode = "transport_protocol_output"
	turnReasonCompletionGateRepeatedRejection     turnReasonCode = "completion_gate_repeated_rejection"
	turnReasonValidationStatusContradictsEvidence turnReasonCode = "validation_status_contradicts_evidence"
)

const (
	turnViolationPlanExecuteAnchorRefreshPending                    turnViolationKind = "plan_execute_anchor_refresh_pending"
	turnViolationProtocolMetaFinalAnswer                            turnViolationKind = "protocol_meta_final_answer"
	turnViolationUnexecutedToolRequestAnswer                        turnViolationKind = "unexecuted_tool_request_final_answer"
	turnViolationRequiredEditPathsRemaining                         turnViolationKind = "required_edit_paths_remaining"
	turnViolationRejectedMutationPathsRemaining                     turnViolationKind = "rejected_mutation_paths_remaining"
	turnViolationUserVisibleBlocker                                 turnViolationKind = "user_visible_blocker"
	turnViolationReadOnlyProtocolBlocker                            turnViolationKind = "read_only_protocol_blocker"
	turnViolationNonAnswerProse                                     turnViolationKind = "non_answer_prose"
	turnViolationSourceGroundingUnmet                               turnViolationKind = "source_grounding_unmet"
	turnViolationRequiredEvidenceUnmet                              turnViolationKind = "required_evidence_unmet"
	turnViolationTransportProtocolOutput                            turnViolationKind = "transport_protocol_output"
	turnViolationCompletionGateRepeatedRejection                    turnViolationKind = "completion_gate_repeated_rejection"
	turnViolationCompletionGateRequiredSourceReadsRemainingRepeated turnViolationKind = "completion_gate_required_source_reads_remaining_repeated"
	turnViolationCompletionGateSubmitPlanInvalidRepeated            turnViolationKind = "completion_gate_submit_plan_invalid_repeated"
	turnViolationCompletionGateSubmitPlanMissingRepeated            turnViolationKind = "completion_gate_submit_plan_missing_repeated"
	turnViolationCompletionGateRequiredPathsRemainingRepeated       turnViolationKind = "completion_gate_required_paths_remaining_repeated"
	turnViolationCompletionGateRequiredSnippetsMissingRepeated      turnViolationKind = "completion_gate_required_snippets_missing_repeated"
	turnViolationCompletionGateLaneSanityRepeated                   turnViolationKind = "completion_gate_lane_sanity_repeated"
	turnViolationCompletionGateVerificationRepeated                 turnViolationKind = "completion_gate_verification_repeated"
	turnViolationToolAuthorizationDenied                            turnViolationKind = "tool_authorization_denied"
	turnViolationToolAuthorizationApprovalRequired                  turnViolationKind = "tool_authorization_approval_required"
	turnViolationPostWriteVerificationRequired                      turnViolationKind = "post_write_verification_required"
	turnViolationPostWriteVerificationFailed                        turnViolationKind = "post_write_verification_failed"
	turnViolationValidationStatusContradictsEvidence                turnViolationKind = "validation_status_contradicts_evidence"
)

type turnEvidenceRef struct {
	Path string
}

type turnDiagnostic struct {
	Kind       turnDiagnosticKind
	ReasonCode turnReasonCode
	Evidence   []turnEvidenceRef
	Message    string
}

type turnViolation struct {
	Kind        turnViolationKind
	PackID      autonomy.ContractPackID
	InvariantID string
	ReasonCode  turnReasonCode
	Evidence    []turnEvidenceRef
	Message     string
}

type turnContractAssessment struct {
	Diagnostics []turnDiagnostic
	Violations  []turnViolation
}

type turnViolationDefinition struct {
	Kind        turnViolationKind
	Retryable   bool
	Recoverable bool
}

var turnViolationDefinitions = []turnViolationDefinition{
	{Kind: turnViolationPlanExecuteAnchorRefreshPending},
	{Kind: turnViolationProtocolMetaFinalAnswer, Retryable: true, Recoverable: true},
	{Kind: turnViolationUnexecutedToolRequestAnswer},
	{Kind: turnViolationRequiredEditPathsRemaining},
	{Kind: turnViolationRejectedMutationPathsRemaining},
	{Kind: turnViolationUserVisibleBlocker},
	{Kind: turnViolationReadOnlyProtocolBlocker, Retryable: true},
	{Kind: turnViolationNonAnswerProse, Recoverable: true},
	{Kind: turnViolationSourceGroundingUnmet, Retryable: true},
	{Kind: turnViolationRequiredEvidenceUnmet},
	{Kind: turnViolationTransportProtocolOutput},
	{Kind: turnViolationCompletionGateRepeatedRejection, Recoverable: true},
	{Kind: turnViolationCompletionGateRequiredSourceReadsRemainingRepeated},
	{Kind: turnViolationCompletionGateSubmitPlanInvalidRepeated},
	{Kind: turnViolationCompletionGateSubmitPlanMissingRepeated},
	{Kind: turnViolationCompletionGateRequiredPathsRemainingRepeated},
	{Kind: turnViolationCompletionGateRequiredSnippetsMissingRepeated},
	{Kind: turnViolationCompletionGateLaneSanityRepeated},
	{Kind: turnViolationCompletionGateVerificationRepeated},
	{Kind: turnViolationToolAuthorizationDenied},
	{Kind: turnViolationToolAuthorizationApprovalRequired},
	{Kind: turnViolationPostWriteVerificationRequired},
	{Kind: turnViolationPostWriteVerificationFailed},
	{Kind: turnViolationValidationStatusContradictsEvidence, Recoverable: true},
}

type finalAnswerAcceptanceDecision string

const (
	finalAnswerAcceptanceAccepted finalAnswerAcceptanceDecision = "accept"
	finalAnswerAcceptanceRejected finalAnswerAcceptanceDecision = "reject"
)

const finalAnswerAcceptanceSchema = "final_answer_acceptance.v1"
const finalAnswerPolicyInvariantAcceptance = "final_answer.acceptance"

type finalAnswerAcceptance struct {
	SchemaVersion            string
	Decision                 finalAnswerAcceptanceDecision
	BlockerKind              string
	Reason                   string
	EvidencePaths            []string
	ContractAssessment       turnContractAssessment
	ObjectiveRelevanceStatus string
	RelevantSourceBodyPaths  []string
	AnswerObjectiveTerms     []string
	PackID                   autonomy.ContractPackID
	InvariantID              string
	PolicyDecision           autonomy.ToolPolicyDecision
	ParityReport             ParityReport
}

func rejectFinalAnswerAcceptance(acceptance finalAnswerAcceptance, kind turnViolationKind, reason turnReasonCode) finalAnswerAcceptance {
	return rejectFinalAnswerAcceptanceWithMessage(acceptance, kind, reason, string(reason))
}

func rejectFinalAnswerAcceptanceWithMessage(acceptance finalAnswerAcceptance, kind turnViolationKind, reason turnReasonCode, message string) finalAnswerAcceptance {
	acceptance.Decision = finalAnswerAcceptanceRejected
	acceptance.BlockerKind = string(kind)
	acceptance.Reason = strings.TrimSpace(textutil.FirstNonEmpty(message, string(reason), string(kind)))
	acceptance.ContractAssessment.Violations = nil
	acceptance.ContractAssessment = acceptance.ContractAssessment.withViolation(turnViolation{
		Kind:        kind,
		PackID:      autonomy.ContractPackFinalAnswerAcceptance,
		InvariantID: finalAnswerPolicyInvariantAcceptance,
		ReasonCode:  reason,
		Evidence:    turnEvidenceRefsFromPaths(acceptance.EvidencePaths),
		Message:     acceptance.Reason,
	})
	return finalAnswerAcceptanceWithPolicy(acceptance)
}

func rejectFinalAnswerAcceptanceFromLegacyBlocker(acceptance finalAnswerAcceptance, kind string, reason string) finalAnswerAcceptance {
	violation, ok := turnViolationFromLegacyBlocker(kind, reason, acceptance.EvidencePaths)
	if !ok {
		return finalAnswerAcceptanceWithPolicy(acceptance)
	}
	acceptance.Decision = finalAnswerAcceptanceRejected
	acceptance.BlockerKind = string(violation.Kind)
	acceptance.Reason = strings.TrimSpace(textutil.FirstNonEmpty(violation.Message, reason, string(violation.ReasonCode), string(violation.Kind)))
	acceptance.ContractAssessment.Violations = nil
	acceptance.ContractAssessment = acceptance.ContractAssessment.withViolation(violation)
	return finalAnswerAcceptanceWithPolicy(acceptance)
}

func finalAnswerAcceptanceWithPolicy(acceptance finalAnswerAcceptance) finalAnswerAcceptance {
	if acceptance.Decision == "" {
		acceptance.Decision = finalAnswerAcceptanceAccepted
	}
	acceptance.SchemaVersion = finalAnswerAcceptanceSchema
	acceptance.PackID = autonomy.ContractPackFinalAnswerAcceptance
	acceptance.InvariantID = finalAnswerPolicyInvariantAcceptance
	if acceptance.Decision == finalAnswerAcceptanceRejected {
		acceptance.PolicyDecision = autonomy.PolicyReject
	} else {
		acceptance.PolicyDecision = autonomy.PolicyAllow
	}
	acceptance.ParityReport = CompareParity(ParityInput{
		SurfaceID:   "final_answer_acceptance",
		InvariantID: finalAnswerPolicyInvariantAcceptance,
		OldDecision: string(acceptance.Decision),
		NewDecision: string(acceptance.Decision),
	})
	return acceptance
}

func newFinalAnswerAcceptance() finalAnswerAcceptance {
	return finalAnswerAcceptanceWithPolicy(finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
	})
}

func completionGateRepeatedRejectionAcceptance() finalAnswerAcceptance {
	return rejectFinalAnswerAcceptance(
		newFinalAnswerAcceptance(),
		turnViolationCompletionGateRepeatedRejection,
		turnReasonCompletionGateRepeatedRejection,
	)
}

func finalAnswerAcceptanceWithTerminalOverride(acceptance finalAnswerAcceptance, decision finalAnswerAcceptanceDecision, blockerKind string, reason string) finalAnswerAcceptance {
	acceptance.Decision = decision
	acceptance.BlockerKind = strings.TrimSpace(blockerKind)
	acceptance.Reason = strings.TrimSpace(reason)
	acceptance.ContractAssessment.Violations = nil
	if decision == finalAnswerAcceptanceAccepted {
		acceptance.BlockerKind = ""
	} else if violation, ok := turnViolationFromLegacyBlocker(blockerKind, reason, acceptance.EvidencePaths); ok {
		acceptance.ContractAssessment = acceptance.ContractAssessment.withViolation(violation)
	}
	return finalAnswerAcceptanceWithPolicy(acceptance)
}

func finalAnswerAcceptanceWithDirectAnswerRelevance(acceptance finalAnswerAcceptance, relevance readOnlyDirectAnswerRelevance) finalAnswerAcceptance {
	if relevance.Status != "" {
		acceptance.ObjectiveRelevanceStatus = relevance.Status
		acceptance.RelevantSourceBodyPaths = append([]string(nil), relevance.RelevantSourceBodyPaths...)
		acceptance.AnswerObjectiveTerms = append([]string(nil), relevance.AnswerObjectiveTerms...)
	}
	if diagnostic, ok := turnDiagnosticFromLegacyBlocker(relevance.BlockerKind, relevance.Reason, relevance.RelevantSourceBodyPaths); ok {
		acceptance.ContractAssessment = acceptance.ContractAssessment.withDiagnostic(diagnostic)
	}
	return acceptance
}

func (a turnContractAssessment) withDiagnostic(diagnostic turnDiagnostic) turnContractAssessment {
	if diagnostic.Kind == "" {
		return a
	}
	if a.HasDiagnostic(diagnostic.Kind) {
		return a
	}
	a.Diagnostics = append(a.Diagnostics, diagnostic)
	return a
}

func (a turnContractAssessment) withViolation(violation turnViolation) turnContractAssessment {
	if violation.Kind == "" {
		return a
	}
	if a.HasViolation(violation.Kind) {
		return a
	}
	a.Violations = append(a.Violations, violation)
	return a
}

func (a turnContractAssessment) HasDiagnostic(kind turnDiagnosticKind) bool {
	for _, diagnostic := range a.Diagnostics {
		if diagnostic.Kind == kind {
			return true
		}
	}
	return false
}

func (a turnContractAssessment) HasViolation(kind turnViolationKind) bool {
	for _, violation := range a.Violations {
		if violation.Kind == kind {
			return true
		}
	}
	return false
}

func finalAnswerObjectiveRelevanceAdvisoryKind(kind string) bool {
	trimmed := strings.TrimSpace(kind)
	return trimmed == "answer_objective_unmet" || strings.HasPrefix(trimmed, "answer_objective_unmet:")
}

func turnViolationDefinitionForKind(kind turnViolationKind) (turnViolationDefinition, bool) {
	for _, definition := range turnViolationDefinitions {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return turnViolationDefinition{}, false
}

func turnDiagnosticFromLegacyBlocker(kind string, reason string, evidencePaths []string) (turnDiagnostic, bool) {
	kind = strings.TrimSpace(kind)
	if !finalAnswerObjectiveRelevanceAdvisoryKind(kind) {
		return turnDiagnostic{}, false
	}
	return turnDiagnostic{
		Kind:       turnDiagnosticObjectiveRelevance,
		ReasonCode: turnReasonCode(kind),
		Evidence:   turnEvidenceRefsFromPaths(evidencePaths),
		Message:    strings.TrimSpace(reason),
	}, true
}

func turnViolationFromLegacyBlocker(kind string, reason string, evidencePaths []string) (turnViolation, bool) {
	violationKind, ok := turnViolationKindFromLegacyBlocker(kind)
	if !ok {
		return turnViolation{}, false
	}
	return turnViolation{
		Kind:        violationKind,
		PackID:      autonomy.ContractPackFinalAnswerAcceptance,
		InvariantID: finalAnswerPolicyInvariantAcceptance,
		ReasonCode:  turnReasonCodeForLegacyBlocker(kind, reason),
		Evidence:    turnEvidenceRefsFromPaths(evidencePaths),
		Message:     strings.TrimSpace(reason),
	}, true
}

func turnReasonCodeForLegacyBlocker(kind string, reason string) turnReasonCode {
	reason = strings.TrimSpace(reason)
	switch turnReasonCode(reason) {
	case turnReasonPlanExecuteAnchorRefreshPending,
		turnReasonGateSatisfactionMetaFinalAnswer,
		turnReasonEmbeddedQuestionProtocolMarker,
		turnReasonUnexecutedToolRequestAnswer,
		turnReasonRequiredEditPathsRemaining,
		turnReasonRejectedMutationPathsRemaining,
		turnReasonUserVisibleBlocker,
		turnReasonReadOnlyProtocolBlocker,
		turnReasonAnswerableEvidenceInsufficient,
		turnReasonInsufficientEvidenceAnswer,
		turnReasonLeakedImplementationBlocker,
		turnReasonNonAnswerProse,
		turnReasonRequiredEvidenceUnmet:
		return turnReasonCode(reason)
	default:
		if kind := strings.TrimSpace(kind); kind != "" {
			return turnReasonCode(kind)
		}
		return turnReasonCode(reason)
	}
}

func turnViolationKindFromLegacyBlocker(kind string) (turnViolationKind, bool) {
	if finalAnswerObjectiveRelevanceAdvisoryKind(kind) {
		return "", false
	}
	trimmed := strings.TrimSpace(kind)
	if trimmed == "" {
		return "", false
	}
	for _, definition := range turnViolationDefinitions {
		if string(definition.Kind) == trimmed {
			return definition.Kind, true
		}
	}
	return "", false
}

type turnViolationCapabilities struct {
	Retryable   bool
	Recoverable bool
}

type turnFinalAnswerRetryPolicy struct {
	Retryable                bool
	RequiresReadOnlyEvidence bool
}

func turnViolationCapabilitiesForKind(kind turnViolationKind) turnViolationCapabilities {
	definition, ok := turnViolationDefinitionForKind(kind)
	if !ok {
		return turnViolationCapabilities{}
	}
	return turnViolationCapabilities{Retryable: definition.Retryable, Recoverable: definition.Recoverable}
}

func turnContractAssessmentCapabilities(assessment turnContractAssessment) turnViolationCapabilities {
	violationKind, ok := turnContractAssessmentPrimaryViolationKind(assessment)
	if !ok {
		return turnViolationCapabilities{}
	}
	return turnViolationCapabilitiesForKind(violationKind)
}

func turnContractAssessmentPrimaryViolationKind(assessment turnContractAssessment) (turnViolationKind, bool) {
	if len(assessment.Violations) == 0 || assessment.Violations[0].Kind == "" {
		return "", false
	}
	return assessment.Violations[0].Kind, true
}

func turnContractAssessmentPrimaryViolationReasonCode(assessment turnContractAssessment) turnReasonCode {
	if len(assessment.Violations) == 0 {
		return ""
	}
	return assessment.Violations[0].ReasonCode
}

func turnContractAssessmentPrimaryViolationReasonCodeMatches(assessment turnContractAssessment, codes ...turnReasonCode) bool {
	reasonCode := turnContractAssessmentPrimaryViolationReasonCode(assessment)
	if reasonCode == "" {
		return false
	}
	for _, code := range codes {
		if reasonCode == code {
			return true
		}
	}
	return false
}

func finalAnswerAcceptanceViolationKind(acceptance finalAnswerAcceptance) (turnViolationKind, bool) {
	return turnContractAssessmentPrimaryViolationKind(acceptance.ContractAssessment)
}

func turnFinalAnswerAcceptanceRetryable(acceptance finalAnswerAcceptance) bool {
	return turnContractAssessmentCapabilities(acceptance.ContractAssessment).Retryable
}

func turnFinalAnswerAcceptanceRecoverable(acceptance finalAnswerAcceptance) bool {
	if turnContractAssessmentPrimaryViolationReasonCodeMatches(
		acceptance.ContractAssessment,
		turnReasonAnswerableEvidenceInsufficient,
		turnReasonInsufficientEvidenceAnswer,
	) {
		return false
	}
	return turnContractAssessmentCapabilities(acceptance.ContractAssessment).Recoverable
}

func turnFinalAnswerRejectionRetryPolicyForBrief(brief workerruntime.WorkerRuntimeBrief, acceptance finalAnswerAcceptance) turnFinalAnswerRetryPolicy {
	violationKind, hasViolation := finalAnswerAcceptanceViolationKind(acceptance)
	if hasViolation &&
		violationKind == turnViolationNonAnswerProse &&
		turnContractAssessmentPrimaryViolationReasonCode(acceptance.ContractAssessment) == turnReasonLeakedImplementationBlocker {

		return turnFinalAnswerRetryPolicy{Retryable: true}
	}
	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		retryable := false
		if hasViolation {
			retryable = turnViolationCapabilitiesForKind(violationKind).Retryable
		}
		return turnFinalAnswerRetryPolicy{
			Retryable:                retryable,
			RequiresReadOnlyEvidence: true,
		}
	}

	if !workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) {
		return turnFinalAnswerRetryPolicy{}
	}

	return turnFinalAnswerRetryPolicy{
		Retryable: hasViolation &&
			violationKind == turnViolationNonAnswerProse &&
			turnContractAssessmentPrimaryViolationReasonCodeMatches(
				acceptance.ContractAssessment,
				turnReasonInsufficientEvidenceAnswer,
				turnReasonAnswerableEvidenceInsufficient,
			),
		RequiresReadOnlyEvidence: true,
	}
}

func turnEvidenceRefsFromPaths(paths []string) []turnEvidenceRef {
	if len(paths) == 0 {
		return nil
	}
	refs := make([]turnEvidenceRef, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		refs = append(refs, turnEvidenceRef{Path: path})
	}
	return refs
}

func finalAnswerAssessmentForAcceptance(acceptance finalAnswerAcceptance) turnContractAssessment {
	assessment := acceptance.ContractAssessment
	if diagnostic, ok := turnDiagnosticFromLegacyBlocker(acceptance.BlockerKind, acceptance.Reason, acceptance.EvidencePaths); ok {
		assessment = assessment.withDiagnostic(diagnostic)
	}
	if acceptance.Decision != finalAnswerAcceptanceRejected {
		assessment.Violations = nil
	}
	return assessment
}

func turnCompletionGateRepeatedViolation(kind turnViolationKind) turnViolation {
	if _, ok := turnViolationDefinitionForKind(kind); !ok {
		kind = turnViolationCompletionGateRepeatedRejection
	}
	return turnViolation{
		Kind:        kind,
		PackID:      autonomy.ContractPackFinalAnswerAcceptance,
		InvariantID: finalAnswerPolicyInvariantAcceptance,
		ReasonCode:  turnReasonCode(kind),
	}
}

func turnAssessmentFromToolAuthorizationPolicy(policy ToolAuthorizationPolicyEvent) turnContractAssessment {
	assessment := turnContractAssessment{}
	switch policy.Decision {
	case autonomy.PolicyReject, autonomy.PolicyDeny:
		assessment = assessment.withViolation(turnViolation{
			Kind:        turnViolationToolAuthorizationDenied,
			PackID:      policy.PackID,
			InvariantID: policy.InvariantID,
			ReasonCode:  turnReasonCode(policy.ReasonCode),
		})
	case autonomy.PolicyAsk:
		assessment = assessment.withViolation(turnViolation{
			Kind:        turnViolationToolAuthorizationApprovalRequired,
			PackID:      policy.PackID,
			InvariantID: policy.InvariantID,
			ReasonCode:  turnReasonCode(policy.ReasonCode),
		})
	}
	return assessment
}

func turnAssessmentFromVerificationDecision(decision VerificationDecision) turnContractAssessment {
	if !decision.Handled || decision.Decision == autonomy.PolicyAllow {
		return turnContractAssessment{}
	}

	kind := turnViolationPostWriteVerificationRequired
	if decision.Status == "failed" {
		kind = turnViolationPostWriteVerificationFailed
	}

	return turnContractAssessment{}.withViolation(turnViolation{
		Kind:        kind,
		PackID:      autonomy.ContractPackVerificationPostWrite,
		InvariantID: decision.InvariantID,
		ReasonCode:  turnReasonCode(decision.ReasonCode),
	})
}
