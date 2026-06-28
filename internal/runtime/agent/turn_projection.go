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
	if paths := turnEvidencePaths(diagnostic.Evidence); len(paths) > 0 {
		fields["evidence_paths"] = paths
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
		if violation.Kind == turnViolationSourceGroundingUnmet {
			if paths := sourceGroundingRejectionPaths(message); len(paths) > 0 {
				fields["source_grounding_paths"] = paths
			}
		}
	}
	if paths := turnEvidencePaths(violation.Evidence); len(paths) > 0 {
		fields["evidence_paths"] = paths
	}
	return fields
}

func turnEvidencePaths(refs []turnEvidenceRef) []string {
	if len(refs) == 0 {
		return nil
	}
	paths := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Path != "" {
			paths = append(paths, ref.Path)
		}
	}
	return paths
}

func finalAnswerCompletionGateProjection(acceptance finalAnswerAcceptance, summary string, errorText string) (workspace.ToolResult, bool) {
	if _, ok := turnContractAssessmentPrimaryViolationKind(acceptance.ContractAssessment); !ok {
		return workspace.ToolResult{}, false
	}
	if strings.TrimSpace(summary) == "" {
		summary = "Final answer rejected by blocker check"
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: strings.TrimSpace(summary),
		Error:   strings.TrimSpace(errorText),
	}, true
}
