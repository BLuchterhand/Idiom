package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
)

func turnContractAssessmentEventFields(assessment turnContractAssessment) map[string]any {
	fields := map[string]any{}
	if len(assessment.Diagnostics) > 0 {
		diagnostics := make([]map[string]any, 0, len(assessment.Diagnostics))
		for _, diagnostic := range assessment.Diagnostics {
			diagnostics = append(diagnostics, turnDiagnosticEventFields(diagnostic))
		}
		fields["contract_diagnostics"] = diagnostics
	}
	if len(assessment.Violations) > 0 {
		violations := make([]map[string]any, 0, len(assessment.Violations))
		for _, violation := range assessment.Violations {
			violations = append(violations, turnViolationEventFields(violation))
		}
		fields["contract_violations"] = violations
	}
	return fields
}

func turnDiagnosticEventFields(diagnostic turnDiagnostic) map[string]any {
	fields := map[string]any{
		"kind": string(diagnostic.Kind),
	}
	if diagnostic.ReasonCode != "" {
		fields["reason_code"] = string(diagnostic.ReasonCode)
	}
	if len(diagnostic.EvidencePaths) > 0 {
		fields["evidence_paths"] = append([]string(nil), diagnostic.EvidencePaths...)
	}
	return fields
}

func turnViolationEventFields(violation turnViolation) map[string]any {
	fields := map[string]any{
		"kind": string(violation.Kind),
	}
	if violation.PackID != "" {
		fields["policy_pack_id"] = string(violation.PackID)
	}
	if violation.InvariantID != "" {
		fields["invariant_id"] = violation.InvariantID
	}
	if violation.ReasonCode != "" {
		fields["reason_code"] = string(violation.ReasonCode)
	}
	if message := strings.TrimSpace(violation.Message); message != "" {
		fields["message"] = message
	}
	if violation.Kind == turnViolationSourceGroundingUnmet {
		if violation.SourceGrounding.Kind != "" {
			fields["source_grounding_kind"] = string(violation.SourceGrounding.Kind)
		}
		if len(violation.SourceGrounding.References) > 0 {
			fields["source_grounding_references"] = violation.SourceGrounding.References
		}
		if len(violation.SourceGrounding.Resolutions) > 0 {
			fields["source_grounding_resolutions"] = violation.SourceGrounding.Resolutions
		}
	}
	if len(violation.EvidencePaths) > 0 {
		fields["evidence_paths"] = append([]string(nil), violation.EvidencePaths...)
	}
	return fields
}

func finalAnswerCompletionGateProjection(acceptance finalAnswerAcceptance, summary string, errorText string) (workspace.ToolResult, bool) {
	violation, ok := turnContractAssessmentPrimaryViolation(acceptance.ContractAssessment)
	if !ok {
		return workspace.ToolResult{}, false
	}
	if strings.TrimSpace(summary) == "" {
		summary = "Final answer rejected by blocker check"
	}
	result := workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: strings.TrimSpace(summary),
		Error:   strings.TrimSpace(errorText),
	}
	if violation.Kind == turnViolationSourceGroundingUnmet {
		return annotateSourceGroundingAssessment(result, violation.SourceGrounding), true
	}
	invariantID := strings.TrimSpace(violation.InvariantID)
	if invariantID == "" {
		invariantID = completionGateInvariantFinalAnswerAcceptance
	}
	envelope := newCompletionGateRecoveryEnvelope(
		invariantID,
		string(violation.Kind),
		completionGateViolationContract,
		nil,
	)
	return annotateCompletionGateRecoveryEnvelope(result, envelope), true
}
