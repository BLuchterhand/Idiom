package agent

import (
	"encoding/json"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
)

const completionGateRecoveryEnvelopePayloadType = "agent.completion_gate_recovery.v1"

const (
	completionGateInvariantFinalAnswerAcceptance  = "final_answer.acceptance"
	completionGateInvariantMutationWrite          = "completion_gate.mutation.write"
	completionGateInvariantMutationRequiredPaths  = "completion_gate.mutation.required_paths"
	completionGateInvariantMutationSnippets       = "completion_gate.mutation.required_snippets"
	completionGateInvariantPostWriteVerification  = "completion_gate.mutation.post_write_verification"
	completionGateInvariantSemanticVerification   = "completion_gate.mutation.semantic_verification"
	completionGateInvariantSilentTestRemoval      = "completion_gate.mutation.silent_test_removal"
	completionGateInvariantPlanSubmission         = "completion_gate.plan_submission"
	completionGateInvariantPlanAnchorRefresh      = "completion_gate.plan.anchor_refresh"
	completionGateInvariantSourcePrerequisite     = "completion_gate.source_prerequisite"
	completionGateInvariantTaskLedger             = "completion_gate.task_ledger"
	completionGateInvariantGeneric                = "completion_gate.unclassified"
	completionGateObligationReadOnlyAnswerability = "read_only:objective_answerability"
	completionGateViolationPlanAnchorRefresh      = "source_refresh_pending"
	completionGateViolationGeneric                = "unclassified"
)

// completionGateRecoveryEnvelope is the producer-owned recovery contract for a
// rejected final answer. Recovery state consumes this payload instead of
// reconstructing invariant identity or pending obligations from rendered prose.
type completionGateRecoveryEnvelope struct {
	SchemaVersion                string                            `json:"schema_version"`
	InvariantID                  string                            `json:"invariant_id"`
	ViolationID                  string                            `json:"violation_id"`
	RecoveryKind                 completionGateViolationClass      `json:"recovery_kind"`
	PendingPaths                 []string                          `json:"pending_paths,omitempty"`
	PendingObligationIDs         []string                          `json:"pending_obligation_ids,omitempty"`
	AcceptedVerificationCommands []string                          `json:"accepted_verification_commands,omitempty"`
	SourceGrounding              *sourceGroundingAssessmentPayload `json:"source_grounding,omitempty"`
}

func newCompletionGateRecoveryEnvelope(invariantID string, violationID string, kind completionGateViolationClass, pendingPaths []string) completionGateRecoveryEnvelope {
	return completionGateRecoveryEnvelope{
		SchemaVersion: completionGateRecoveryEnvelopePayloadType,
		InvariantID:   strings.TrimSpace(invariantID),
		ViolationID:   strings.TrimSpace(violationID),
		RecoveryKind:  kind,
		PendingPaths:  uniqueSortedRunnerPaths(pendingPaths),
	}
}

func annotateCompletionGateRecoveryEnvelope(result workspace.ToolResult, envelope completionGateRecoveryEnvelope) workspace.ToolResult {
	if strings.TrimSpace(result.Tool) != completionGateToolName ||
		strings.TrimSpace(envelope.InvariantID) == "" ||
		strings.TrimSpace(envelope.ViolationID) == "" {

		return result
	}

	envelope.SchemaVersion = completionGateRecoveryEnvelopePayloadType
	envelope.InvariantID = strings.TrimSpace(envelope.InvariantID)
	envelope.ViolationID = strings.TrimSpace(envelope.ViolationID)
	envelope.PendingPaths = uniqueSortedRunnerPaths(envelope.PendingPaths)
	envelope.PendingObligationIDs = normalizeCompletionGateObligationIDs(envelope.PendingObligationIDs)
	envelope.AcceptedVerificationCommands = normalizeCompletionGateVerificationCommands(envelope.AcceptedVerificationCommands)
	payload, err := json.Marshal(envelope)
	if err != nil {
		return result
	}

	result.PayloadType = completionGateRecoveryEnvelopePayloadType
	result.Payload = payload
	return result
}

func completionGateRecoveryEnvelopeFromResult(result workspace.ToolResult) (completionGateRecoveryEnvelope, bool) {
	if strings.TrimSpace(result.Tool) != completionGateToolName ||
		strings.TrimSpace(result.PayloadType) != completionGateRecoveryEnvelopePayloadType ||
		len(result.Payload) == 0 {

		return completionGateRecoveryEnvelope{}, false
	}

	var envelope completionGateRecoveryEnvelope
	if err := json.Unmarshal(result.Payload, &envelope); err != nil {
		return completionGateRecoveryEnvelope{}, false
	}
	if strings.TrimSpace(envelope.SchemaVersion) != completionGateRecoveryEnvelopePayloadType {
		return completionGateRecoveryEnvelope{}, false
	}
	if strings.TrimSpace(envelope.InvariantID) == "" || strings.TrimSpace(envelope.ViolationID) == "" {
		return completionGateRecoveryEnvelope{}, false
	}
	if envelope.RecoveryKind == "" {
		envelope.RecoveryKind = completionGateViolationContract
	} else if !completionGateViolationClassKnown(envelope.RecoveryKind) {
		return completionGateRecoveryEnvelope{}, false
	}

	envelope.InvariantID = strings.TrimSpace(envelope.InvariantID)
	envelope.ViolationID = strings.TrimSpace(envelope.ViolationID)
	envelope.PendingPaths = uniqueSortedRunnerPaths(envelope.PendingPaths)
	envelope.PendingObligationIDs = normalizeCompletionGateObligationIDs(envelope.PendingObligationIDs)
	envelope.AcceptedVerificationCommands = normalizeCompletionGateVerificationCommands(envelope.AcceptedVerificationCommands)
	return envelope, true
}

func completionGateViolationClassKnown(kind completionGateViolationClass) bool {
	switch kind {
	case completionGateViolationContract,
		completionGateViolationSourceGrounding,
		completionGateViolationSourceEvidence,
		completionGateViolationMutation,
		completionGateViolationVerification,
		completionGateViolationPlanSubmission,
		completionGateViolationTaskLedger:
		return true
	default:
		return false
	}
}

func normalizeCompletionGateVerificationCommands(commands []string) []string {
	normalized := make([]string, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		normalized = append(normalized, command)
	}
	slices.Sort(normalized)
	return normalized
}

func normalizeCompletionGateObligationIDs(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	slices.Sort(normalized)
	return normalized
}

func completionGateRejectionWithRecovery(summary string, errorText string, envelope completionGateRecoveryEnvelope) workspace.ToolResult {
	return annotateCompletionGateRecoveryEnvelope(workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: strings.TrimSpace(summary),
		Error:   strings.TrimSpace(errorText),
	}, envelope)
}
