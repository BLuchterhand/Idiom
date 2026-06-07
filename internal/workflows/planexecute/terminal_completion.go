package planexecute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/plancontract"
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
	FailureID                    string
	RecordPath                   string
	PacketPath                   string
	NextAction                   string
	CompletedAt                  time.Time
}

type PlanExecuteTerminalCompletionRecord struct {
	SchemaVersion                string                     `json:"schema_version"`
	Type                         string                     `json:"type"`
	TS                           string                     `json:"ts,omitempty"`
	ProgressPath                 string                     `json:"progress_path,omitempty"`
	SessionID                    string                     `json:"session_id,omitempty"`
	Phase                        string                     `json:"phase,omitempty"`
	Status                       string                     `json:"status,omitempty"`
	ReviewStatus                 string                     `json:"review_status,omitempty"`
	TerminalClass                PlanExecuteTerminalClass   `json:"terminal_class"`
	Outcome                      PlanExecuteTerminalOutcome `json:"outcome"`
	ExitCode                     int                        `json:"exit_code"`
	ReviewRepairSkipped          bool                       `json:"review_repair_skipped,omitempty"`
	FailureKind                  string                     `json:"failure_kind,omitempty"`
	FailureReason                string                     `json:"failure_reason,omitempty"`
	FailureID                    string                     `json:"failure_id,omitempty"`
	RecordPath                   string                     `json:"record_path,omitempty"`
	PacketPath                   string                     `json:"packet_path,omitempty"`
	NextAction                   string                     `json:"next_valid_action,omitempty"`
	RequiredEditPaths            []string                   `json:"required_edit_paths,omitempty"`
	AllowedEditPaths             []string                   `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths            []string                   `json:"write_allowed_paths,omitempty"`
	SourceSatisfiedEvidencePaths []string                   `json:"source_satisfied_evidence_paths,omitempty"`
	ChangedPaths                 []string                   `json:"changed_paths,omitempty"`
}

func NewPlanExecuteTerminalCompletionRecord(state ProgressState, input PlanExecuteTerminalCompletionInput) PlanExecuteTerminalCompletionRecord {
	record := PlanExecuteTerminalCompletionRecord{
		SchemaVersion:                PlanExecuteTerminalCompletionSchemaVersion,
		Type:                         PlanExecuteTerminalCompletionRecordType,
		ProgressPath:                 input.ProgressPath,
		SessionID:                    state.SessionID,
		Phase:                        state.Phase,
		Status:                       state.Status,
		ReviewStatus:                 state.Review.Status,
		ExitCode:                     input.ExitCode,
		ReviewRepairSkipped:          state.Review.Status == ProgressStatusSkipped,
		FailureKind:                  state.FailureKind,
		FailureReason:                state.FailureReason,
		FailureID:                    input.FailureID,
		RecordPath:                   input.RecordPath,
		PacketPath:                   input.PacketPath,
		NextAction:                   input.NextAction,
		RequiredEditPaths:            append([]string(nil), state.RequiredEditPaths...),
		AllowedEditPaths:             append([]string(nil), state.AllowedEditPaths...),
		WriteAllowedPaths:            append([]string(nil), state.WriteAllowedPaths...),
		SourceSatisfiedEvidencePaths: append([]string(nil), input.SourceSatisfiedEvidencePaths...),
		ChangedPaths:                 append([]string(nil), state.ChangedPaths...),
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
	record.RequiredEditPaths = normalizePlanExecuteTerminalPaths(record.RequiredEditPaths)
	record.AllowedEditPaths = normalizePlanExecuteTerminalPaths(record.AllowedEditPaths)
	record.WriteAllowedPaths = normalizePlanExecuteTerminalPaths(record.WriteAllowedPaths)
	record.SourceSatisfiedEvidencePaths = normalizePlanExecuteTerminalPaths(record.SourceSatisfiedEvidencePaths)
	record.ChangedPaths = normalizePlanExecuteTerminalPaths(record.ChangedPaths)
	if record.Outcome == "" || record.TerminalClass == "" {
		class, outcome := classifyPlanExecuteTerminalCompletion(record)
		if record.TerminalClass == "" {
			record.TerminalClass = class
		}
		if record.Outcome == "" {
			record.Outcome = outcome
		}
	}
	return record
}

func classifyPlanExecuteTerminalCompletion(record PlanExecuteTerminalCompletionRecord) (PlanExecuteTerminalClass, PlanExecuteTerminalOutcome) {
	status := canonicalPlanExecuteTerminalToken(record.Status)
	reviewStatus := canonicalPlanExecuteTerminalToken(record.ReviewStatus)
	phase := canonicalPlanExecuteTerminalToken(record.Phase)

	if record.Outcome == PlanExecuteTerminalOutcomeSourceSatisfied || status == "source-satisfied" {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeSourceSatisfied
	}
	if status == ProgressStatusBlocked || reviewStatus == ProgressStatusBlocked || strings.Contains(status, "blocked") || strings.Contains(reviewStatus, "blocked") {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
	}
	if status == ProgressStatusDegraded || strings.Contains(reviewStatus, "degraded") {
		return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeReviewDegraded
	}
	if record.ExitCode != 0 || record.FailureKind != "" || record.FailureReason != "" || status == ProgressStatusFailed || strings.Contains(status, "failed") {
		return PlanExecuteTerminalClassFailed, PlanExecuteTerminalOutcomeFailed
	}
	if record.ReviewRepairSkipped || reviewStatus == ProgressStatusSkipped {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeReviewSkippedByDesign
	}
	if status == ProgressStatusCompleted || status == ProgressPhaseComplete || phase == ProgressPhaseComplete {
		return PlanExecuteTerminalClassSuccess, PlanExecuteTerminalOutcomeCompleted
	}
	return PlanExecuteTerminalClassRecoveryRequired, PlanExecuteTerminalOutcomeBlocked
}

func canonicalPlanExecuteTerminalToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	if value == "complete" {
		return ProgressStatusCompleted
	}
	return value
}

func normalizePlanExecuteTerminalPaths(values []string) []string {
	return plancontract.UniqueScopePaths(values)
}

func firstPlanExecuteTerminalString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
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
