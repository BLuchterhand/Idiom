package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	maxHarnessFeedbackText     = 1200
	maxHarnessFeedbackEvidence = 20
)

type harnessFeedbackReport struct {
	FeedbackID    string   `json:"feedback_id"`
	GeneratedAt   string   `json:"generated_at"`
	WorkspaceRoot string   `json:"workspace_root,omitempty"`
	Category      string   `json:"category"`
	Component     string   `json:"component"`
	Expected      string   `json:"expected"`
	Observed      string   `json:"observed"`
	Workaround    string   `json:"workaround,omitempty"`
	Impact        string   `json:"impact"`
	Evidence      []string `json:"evidence,omitempty"`
	Confidence    string   `json:"confidence"`
	Severity      string   `json:"severity"`
	SuggestedFix  string   `json:"suggested_fix,omitempty"`
	Path          string   `json:"path,omitempty"`
	TaskID        string   `json:"task_id,omitempty"`
}

func (m *Manager) harnessFeedback(req ToolRequest) ToolResult {
	now := time.Now().UTC()
	report := harnessFeedbackReport{
		FeedbackID:    "feedback-" + now.Format("20060102T150405.000000000Z"),
		GeneratedAt:   now.Format(time.RFC3339Nano),
		WorkspaceRoot: m.cfg.Root,
		Category:      normalizeHarnessFeedbackCategory(req.Category),
		Component:     cleanHarnessFeedbackText(req.Component),
		Expected:      cleanHarnessFeedbackText(req.Expected),
		Observed:      cleanHarnessFeedbackText(req.Observed),
		Workaround:    cleanHarnessFeedbackText(req.Workaround),
		Impact:        cleanHarnessFeedbackText(req.Impact),
		Evidence:      cleanHarnessFeedbackEvidence(req.Evidence),
		Confidence:    normalizeHarnessFeedbackConfidence(req.Confidence),
		Severity:      normalizeHarnessFeedbackSeverity(req.Severity),
		SuggestedFix:  cleanHarnessFeedbackText(req.SuggestedFix),
		Path:          strings.TrimSpace(req.Path),
		TaskID:        strings.TrimSpace(req.TaskID),
	}

	if err := validateHarnessFeedback(report); err != nil {
		return ToolResult{
			Tool:    "harness_feedback",
			Summary: "Harness feedback rejected",
			Error:   err.Error(),
		}
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{
			Tool:    "harness_feedback",
			Summary: "Harness feedback failed",
			Error:   err.Error(),
		}
	}
	return ToolResult{
		Tool:    "harness_feedback",
		Summary: fmt.Sprintf("Recorded %s feedback for %s (%s, %s)", report.Category, report.Component, report.Confidence, report.Severity),
		Output:  string(data),
	}
}

func validateHarnessFeedback(report harnessFeedbackReport) error {
	var missing []string
	if report.Component == "" {
		missing = append(missing, "component")
	}
	if report.Expected == "" {
		missing = append(missing, "expected")
	}
	if report.Observed == "" {
		missing = append(missing, "observed")
	}
	if report.Impact == "" {
		missing = append(missing, "impact")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required feedback field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func normalizeHarnessFeedbackCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "tool_gap", "tool", "tooling":
		return "tool_gap"
	case "prompt_gap", "prompt_confusion", "prompt":
		return "prompt_gap"
	case "workflow_gap", "workflow":
		return "workflow_gap"
	case "context_gap", "context":
		return "context_gap"
	case "stale_context", "stale":
		return "stale_context"
	case "language_support", "language", "language_gap":
		return "language_support"
	case "routing_gap", "edit_routing", "routing":
		return "routing_gap"
	case "verification_gap", "verification_contract", "verification":
		return "verification_gap"
	case "telemetry_gap", "telemetry":
		return "telemetry_gap"
	case "audit_gap", "audit":
		return "audit_gap"
	case "other", "":
		return "other"
	default:
		return "other"
	}
}

func normalizeHarnessFeedbackConfidence(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "verified", "observed", "fact", "factual":
		return "verified"
	case "inferred", "suspected", "likely", "":
		return "inferred"
	default:
		return "inferred"
	}
}

func normalizeHarnessFeedbackSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "high", "critical", "blocker", "blocked":
		return "high"
	case "medium", "moderate":
		return "medium"
	case "low", "minor", "":
		return "low"
	default:
		return "low"
	}
}

func cleanHarnessFeedbackEvidence(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanHarnessFeedbackText(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= maxHarnessFeedbackEvidence {
			break
		}
	}
	return out
}

func cleanHarnessFeedbackText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHarnessFeedbackText {
		return value
	}
	return value[:maxHarnessFeedbackText]
}
