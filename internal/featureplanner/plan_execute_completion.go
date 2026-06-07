package featureplanner

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

type planExecuteCompletionRecord struct {
	Type          string   `json:"type"`
	Role          string   `json:"role"`
	Content       string   `json:"content"`
	StatePath     string   `json:"state_path"`
	ProgressPath  string   `json:"progress_path"`
	TS            string   `json:"ts"`
	Phase         string   `json:"phase"`
	Status        string   `json:"status"`
	ReviewStatus  string   `json:"review_status"`
	ReviewSkipped bool     `json:"review_repair_skipped"`
	FailureKind   string   `json:"failure_kind"`
	FailureReason string   `json:"failure_reason"`
	ReasonClass   string   `json:"reason_class"`
	StopReason    string   `json:"stop_reason"`
	ChangedPaths  []string `json:"changed_paths"`
}

type PlanExecuteCompletionSummary struct {
	LaneFamily          string
	Summary             string
	ProgressPath        string
	Status              string
	Phase               string
	ReviewStatus        string
	FailureKind         string
	FailureReason       string
	CompletionSource    string
	ReviewRepairSkipped bool
	ChangedPaths        []string

	timestamp    time.Time
	hasTimestamp bool
	sequence     int
}

func PlanExecuteCompletionSummaryFromFields(fields map[string]any) PlanExecuteCompletionSummary {
	if fields == nil {
		return PlanExecuteCompletionSummary{}
	}
	summary := PlanExecuteCompletionSummary{
		LaneFamily:          CompletionFieldString(fields, "lane_family"),
		Summary:             CompletionFieldString(fields, "summary"),
		ProgressPath:        FirstNonEmptyString(CompletionFieldString(fields, "progress_path"), CompletionFieldString(fields, "state_path")),
		Status:              CompletionFieldString(fields, "status"),
		Phase:               CompletionFieldString(fields, "phase"),
		ReviewStatus:        CompletionFieldString(fields, "review_status"),
		FailureKind:         CompletionFieldString(fields, "failure_kind"),
		FailureReason:       CompletionFieldString(fields, "failure_reason"),
		CompletionSource:    CompletionFieldString(fields, "completion_source"),
		ReviewRepairSkipped: completionFieldBool(fields, "review_repair_skipped"),
		ChangedPaths:        completionFieldStringSlice(fields, "changed_paths"),
	}
	if ts := CompletionFieldString(fields, "ts"); ts != "" {
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
	for _, key := range []string{
		"status",
		"phase",
		"review_status",
		"failure_kind",
		"failure_reason",
		"progress_path",
		"review_repair_skipped",
		"changed_paths",
		"lane_family",
		"summary",
		"completion_source",
	} {
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
		fields["lane_family"] = summary.LaneFamily
	}
	if summary.Summary != "" {
		fields["summary"] = summary.Summary
	}
	if summary.ProgressPath != "" {
		fields["progress_path"] = summary.ProgressPath
	}
	if summary.Status != "" {
		fields["status"] = summary.Status
	}
	if summary.Phase != "" {
		fields["phase"] = summary.Phase
	}
	if summary.ReviewStatus != "" {
		fields["review_status"] = summary.ReviewStatus
	}
	if summary.FailureKind != "" {
		fields["failure_kind"] = summary.FailureKind
	}
	if summary.FailureReason != "" {
		fields["failure_reason"] = summary.FailureReason
	}
	if summary.CompletionSource != "" {
		fields["completion_source"] = summary.CompletionSource
	}
	if summary.ReviewRepairSkipped {
		fields["review_repair_skipped"] = true
	}
	if len(summary.ChangedPaths) > 0 {
		fields["changed_paths"] = append([]string(nil), summary.ChangedPaths...)
	}
	return fields
}

func planExecuteCompletionSummaryFromRecord(record planExecuteCompletionRecord) PlanExecuteCompletionSummary {
	summary := PlanExecuteCompletionSummary{
		ProgressPath:        FirstNonEmptyString(record.ProgressPath, record.StatePath),
		Status:              record.Status,
		Phase:               record.Phase,
		ReviewStatus:        record.ReviewStatus,
		ReviewRepairSkipped: record.ReviewSkipped,
		FailureKind:         FirstNonEmptyString(record.FailureKind, record.ReasonClass),
		FailureReason:       record.FailureReason,
		ChangedPaths:        append([]string(nil), record.ChangedPaths...),
	}
	if ts := strings.TrimSpace(record.TS); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			summary.timestamp = parsed
			summary.hasTimestamp = true
		}
	}
	if record.Type == "idiom.plan_execute.review_repair_degraded" {
		summary.Status = "degraded"
		summary.ReviewStatus = FirstNonEmptyString(record.ReviewStatus, record.Status, "degraded")
		summary.FailureKind = FirstNonEmptyString(record.FailureKind, record.ReasonClass, "review_repair_degraded")
	}
	if planExecuteCompletionModelError(record.StopReason, record.Content) {
		summary.Status = FirstNonEmptyString(summary.Status, "failed")
		summary.FailureKind = FirstNonEmptyString(summary.FailureKind, "model_error")
		summary.FailureReason = FirstNonEmptyString(summary.FailureReason, "nested plan-execute stopped with model_error")
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
	summary.FailureKind = strings.TrimSpace(summary.FailureKind)
	summary.FailureReason = strings.TrimSpace(summary.FailureReason)
	summary.CompletionSource = strings.TrimSpace(summary.CompletionSource)
	summary.ChangedPaths = normalizeCompletionStringList(summary.ChangedPaths)
	return summary
}

func (summary PlanExecuteCompletionSummary) withInheritedContext(other PlanExecuteCompletionSummary) PlanExecuteCompletionSummary {
	if summary.LaneFamily == "" {
		summary.LaneFamily = other.LaneFamily
	}
	if summary.Summary == "" {
		summary.Summary = other.Summary
	}
	if summary.CompletionSource == "" {
		summary.CompletionSource = other.CompletionSource
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
		summary.FailureKind != "" ||
		summary.FailureReason != "" ||
		summary.ReviewRepairSkipped ||
		len(summary.ChangedPaths) > 0
}

func (summary PlanExecuteCompletionSummary) planExecuteCompletionTerminal() bool {
	return planExecuteCompletionStatusTerminal(summary.Status) ||
		planExecuteCompletionStatusTerminal(summary.ReviewStatus) ||
		summary.FailureKind != "" ||
		summary.FailureReason != ""
}

func (summary PlanExecuteCompletionSummary) hasFailureOutcome() bool {
	if summary.FailureKind != "" || summary.FailureReason != "" {
		return true
	}
	return containsBlockingPlanExecuteCompletionSignal(summary.Status) ||
		containsBlockingPlanExecuteCompletionSignal(summary.ReviewStatus)
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
	if len(summary.ChangedPaths) > 0 {
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
	case "completed", "success", "succeeded", "degraded", "skipped", "failed", "failure", "blocked":
		return true
	default:
		return containsBlockingPlanExecuteCompletionSignal(status) && status != "running"
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
	status := canonicalPlanExecuteCompletionStatus(CompletionFieldString(fields, "status"))
	if status == "" {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "missing required nested plan-execute status"}
	}
	if CompletionFieldString(fields, "failure_kind") != "" || CompletionFieldString(fields, "failure_reason") != "" {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute reported failure details"}
	}
	if containsBlockingPlanExecuteCompletionSignal(status) ||
		containsBlockingPlanExecuteCompletionSignal(CompletionFieldString(fields, "phase")) ||
		containsBlockingPlanExecuteCompletionSignal(CompletionFieldString(fields, "review_status")) {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute status is not success-shaped"}
	}
	if status == "skipped" && !completionFieldBool(fields, "review_repair_skipped") {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design"}
	}
	if canonicalPlanExecuteCompletionStatus(CompletionFieldString(fields, "review_status")) == "skipped" && !completionFieldBool(fields, "review_repair_skipped") {
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: "nested plan-execute skipped review is not explicitly marked by design"}
	}
	switch status {
	case "completed", "success", "succeeded", "degraded", "skipped":
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalCompleted}
	default:
		return PlanExecuteCompletionDecision{Class: PlanExecuteTerminalBlocked, Reason: fmt.Sprintf("ambiguous nested plan-execute status %q", CompletionFieldString(fields, "status"))}
	}
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
		"running",
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
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
	}
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
