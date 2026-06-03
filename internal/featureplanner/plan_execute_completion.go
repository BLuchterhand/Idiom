package featureplanner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PlanExecuteCompletionFields extracts the nested plan-execute terminal
// contract from JSONL output. Both TUI and headless feature-planner adapters
// use this parser so degraded review/model-error evidence is interpreted once.
func PlanExecuteCompletionFields(output string) map[string]any {
	fields := map[string]any{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var record struct {
			Type          string `json:"type"`
			Role          string `json:"role"`
			Content       string `json:"content"`
			StatePath     string `json:"state_path"`
			Phase         string `json:"phase"`
			Status        string `json:"status"`
			ReviewStatus  string `json:"review_status"`
			FailureKind   string `json:"failure_kind"`
			FailureReason string `json:"failure_reason"`
			ReasonClass   string `json:"reason_class"`
			StopReason    string `json:"stop_reason"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if !planExecuteCompletionRecordRelevant(record.Type, record.Role, record.Content, record.StopReason) {
			continue
		}
		if strings.TrimSpace(record.StatePath) != "" {
			fields["progress_path"] = strings.TrimSpace(record.StatePath)
		}
		if strings.TrimSpace(record.Phase) != "" {
			fields["phase"] = strings.TrimSpace(record.Phase)
		}
		if strings.TrimSpace(record.Status) != "" {
			fields["status"] = strings.TrimSpace(record.Status)
		}
		if strings.TrimSpace(record.ReviewStatus) != "" {
			fields["review_status"] = strings.TrimSpace(record.ReviewStatus)
		}
		if strings.TrimSpace(record.FailureKind) != "" {
			fields["failure_kind"] = strings.TrimSpace(record.FailureKind)
		}
		if strings.TrimSpace(record.ReasonClass) != "" && CompletionFieldString(fields, "failure_kind") == "" {
			fields["failure_kind"] = strings.TrimSpace(record.ReasonClass)
		}
		if strings.TrimSpace(record.FailureReason) != "" {
			fields["failure_reason"] = strings.TrimSpace(record.FailureReason)
		}
		if record.Type == "idiom.plan_execute.review_repair_degraded" {
			fields["status"] = "degraded"
			fields["review_status"] = FirstNonEmptyString(record.ReviewStatus, record.Status, "degraded")
			fields["failure_kind"] = FirstNonEmptyString(record.FailureKind, record.ReasonClass, "review_repair_degraded")
		}
		if planExecuteCompletionModelError(record.StopReason, record.Content) {
			fields["status"] = FirstNonEmptyString(CompletionFieldString(fields, "status"), "failed")
			fields["failure_kind"] = FirstNonEmptyString(CompletionFieldString(fields, "failure_kind"), "model_error")
			fields["failure_reason"] = FirstNonEmptyString(CompletionFieldString(fields, "failure_reason"), "nested plan-execute stopped with model_error")
		}
	}
	return fields
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

func FirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
