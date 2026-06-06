package planexecute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/recovery"
	"idiom/internal/workspace"
)

const ProgressStateSchemaVersion = "idiom.plan_execute.progress.v1"

const (
	ProgressPhasePlanning     = "planning"
	ProgressPhaseExecutor     = "executor"
	ProgressPhaseReviewRepair = "review_repair"
	ProgressPhaseComplete     = "complete"

	ProgressStatusRunning   = "running"
	ProgressStatusCompleted = "completed"
	ProgressStatusFailed    = "failed"
	ProgressStatusDegraded  = "degraded"
	ProgressStatusBlocked   = "blocked"
	ProgressStatusSkipped   = "skipped"
)

type ProgressState struct {
	SchemaVersion      string               `json:"schema_version"`
	SessionID          string               `json:"session_id"`
	CreatedAt          string               `json:"created_at"`
	UpdatedAt          string               `json:"updated_at"`
	PromptSHA256       string               `json:"prompt_sha256,omitempty"`
	PromptFile         string               `json:"prompt_file,omitempty"`
	Phase              string               `json:"phase"`
	Status             string               `json:"status"`
	FailureKind        string               `json:"failure_kind,omitempty"`
	FailureReason      string               `json:"failure_reason,omitempty"`
	PlanPath           string               `json:"plan_path,omitempty"`
	PlanID             string               `json:"plan_id,omitempty"`
	PackID             string               `json:"pack_id,omitempty"`
	RequiredEditPaths  []string             `json:"required_edit_paths,omitempty"`
	AllowedEditPaths   []string             `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths  []string             `json:"write_allowed_paths,omitempty"`
	EditSetObligations EditSetObligations   `json:"edit_set_obligations,omitempty"`
	ChangedPaths       []string             `json:"changed_paths,omitempty"`
	TotalEdits         int                  `json:"total_edits,omitempty"`
	Edits              []ProgressEdit       `json:"edits,omitempty"`
	Executor           ProgressExecutor     `json:"executor,omitempty"`
	Review             ProgressReviewRepair `json:"review_repair,omitempty"`
	ReviewBaseline     ReviewRepairBaseline `json:"review_baseline,omitempty"`
}

type ProgressEdit struct {
	Step      int    `json:"step"`
	Total     int    `json:"total"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type ProgressExecutor struct {
	Status       string                `json:"status,omitempty"`
	Total        int                   `json:"total,omitempty"`
	Succeeded    int                   `json:"succeeded,omitempty"`
	Failed       int                   `json:"failed,omitempty"`
	Failures     []string              `json:"failures,omitempty"`
	SourceSanity []PhaseEvidenceCheck  `json:"source_sanity,omitempty"`
	Verification []PhaseEvidenceCheck  `json:"verification,omitempty"`
	Handoff      ExecutorHandoffPacket `json:"handoff,omitempty"`
}

type ProgressReviewRepair struct {
	SessionID      string `json:"session_id,omitempty"`
	Status         string `json:"status,omitempty"`
	ReviewPlan     string `json:"review_plan,omitempty"`
	Repairs        int    `json:"repairs,omitempty"`
	AutoRepairs    int    `json:"auto_repairs,omitempty"`
	ChecklistFound bool   `json:"checklist_found,omitempty"`
	ReasonClass    string `json:"reason_class,omitempty"`
	Error          string `json:"error,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
}

func NewProgressState(sessionID string, prompt string, promptFile string, requiredEditPaths []string, allowedEditPaths []string, now time.Time) ProgressState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return ProgressState{
		SchemaVersion:     ProgressStateSchemaVersion,
		SessionID:         strings.TrimSpace(sessionID),
		CreatedAt:         stamp,
		UpdatedAt:         stamp,
		PromptSHA256:      ProgressPromptSHA256(prompt),
		PromptFile:        strings.TrimSpace(promptFile),
		Phase:             ProgressPhasePlanning,
		Status:            ProgressStatusRunning,
		RequiredEditPaths: append([]string(nil), requiredEditPaths...),
		AllowedEditPaths:  append([]string(nil), allowedEditPaths...),
	}
}

func (s *ProgressState) SetWriteAllowedPaths(paths []string) {
	if s == nil {
		return
	}
	s.WriteAllowedPaths = mergePlanExecutePaths(paths)
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func ProgressPromptSHA256(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func SaveProgressState(root string, state *ProgressState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("plan-execute progress state is nil")
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	if !validProgressSessionID(state.SessionID) {
		return "", fmt.Errorf("invalid plan-execute progress session id %q", state.SessionID)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = ProgressStateSchemaVersion
	}
	if state.SchemaVersion != ProgressStateSchemaVersion {
		return "", fmt.Errorf("unsupported plan-execute progress schema %q", state.SchemaVersion)
	}
	sanitizeProgressState(state)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(state.CreatedAt) == "" {
		state.CreatedAt = now
	}
	state.UpdatedAt = now
	fullPath, relPath, err := progressStatePath(root, state.SessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir plan-execute progress dir: %w", err)
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plan-execute progress state: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write plan-execute progress state: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replace plan-execute progress state: %w", err)
	}
	return relPath, nil
}

func TryLoadProgressState(root string, resumeID string) (ProgressState, string, bool, error) {
	fullPath, relPath, err := progressStatePath(root, resumeID)
	if err != nil {
		return ProgressState{}, "", false, err
	}
	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return ProgressState{}, relPath, false, nil
	}
	if err != nil {
		return ProgressState{}, relPath, false, fmt.Errorf("read plan-execute progress state %s: %w", relPath, err)
	}
	var state ProgressState
	if err := json.Unmarshal(body, &state); err != nil {
		return ProgressState{}, relPath, false, fmt.Errorf("decode plan-execute progress state %s: %w", relPath, err)
	}
	if state.SchemaVersion != "" && state.SchemaVersion != ProgressStateSchemaVersion {
		return ProgressState{}, relPath, false, fmt.Errorf("unsupported plan-execute progress schema %q in %s", state.SchemaVersion, relPath)
	}
	if strings.TrimSpace(state.SessionID) == "" {
		state.SessionID = strings.TrimSpace(resumeID)
	}
	if !validProgressSessionID(state.SessionID) {
		return ProgressState{}, relPath, false, fmt.Errorf("invalid plan-execute progress session id %q in %s", state.SessionID, relPath)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = ProgressStateSchemaVersion
	}
	return state, relPath, true, nil
}

func LoadProgressState(root string, resumeID string) (ProgressState, string, error) {
	state, relPath, found, err := TryLoadProgressState(root, resumeID)
	if err != nil {
		return ProgressState{}, relPath, err
	}
	if !found {
		return ProgressState{}, relPath, fmt.Errorf("plan-execute progress state %s does not exist", relPath)
	}
	return state, relPath, nil
}

func (s *ProgressState) MarkPlanLoaded(root string, planPath string, plan workspace.PlanArtifact, baseline ReviewRepairBaseline) {
	s.Phase = ProgressPhaseExecutor
	s.Status = ProgressStatusRunning
	s.PlanPath = progressRelPath(root, planPath)
	s.PlanID = strings.TrimSpace(plan.PlanID)
	s.PackID = strings.TrimSpace(plan.PackID)
	s.ChangedPaths = planChangedPaths(plan)
	s.TotalEdits = len(plan.Edits)
	s.ReviewBaseline = baseline
	if s.Executor.Total == 0 {
		s.Executor.Total = len(plan.Edits)
	}
}

func (s *ProgressState) MarkPlanningFailed(kind string, reason string) {
	s.Phase = ProgressPhasePlanning
	s.Status = ProgressStatusFailed
	s.FailureKind = strings.TrimSpace(kind)
	s.FailureReason = sanitizeProgressReason(reason)
}

func (s *ProgressState) MarkEditProgress(step int, total int, path string, succeeded bool) {
	if step <= 0 {
		return
	}
	s.Phase = ProgressPhaseExecutor
	s.Status = ProgressStatusRunning
	s.Executor.Status = ProgressStatusRunning
	s.TotalEdits = total
	s.Executor.Total = total
	status := ProgressStatusCompleted
	if !succeeded {
		status = ProgressStatusFailed
	}
	edit := ProgressEdit{
		Step:      step,
		Total:     total,
		Path:      strings.TrimSpace(path),
		Status:    status,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	index := step - 1
	for len(s.Edits) <= index {
		s.Edits = append(s.Edits, ProgressEdit{Step: len(s.Edits) + 1, Total: total})
	}
	s.Edits[index] = edit
}

func (s *ProgressState) MarkExecutorComplete(report ExecutorPhaseReport, runErr error) {
	s.Phase = ProgressPhaseExecutor
	s.Executor = ProgressExecutor{
		Status:       ProgressStatusCompleted,
		Total:        report.Total,
		Succeeded:    report.Succeeded,
		Failed:       report.Failed,
		Failures:     sanitizeProgressReasons(report.Failures),
		SourceSanity: append([]PhaseEvidenceCheck(nil), report.SourceSanity...),
		Verification: append([]PhaseEvidenceCheck(nil), report.Verification...),
		Handoff:      report.Handoff,
	}
	if runErr != nil {
		s.Status = ProgressStatusFailed
		if ExecutorSourceSanityFailed(report, runErr) {
			s.Executor.Status = ProgressStatusCompleted
			s.FailureKind = RunFailureSourceSanityFailed
		} else if ExecutorVerificationFailed(report, runErr) {
			s.Executor.Status = ProgressStatusCompleted
			s.FailureKind = RunFailureVerificationRequired
		} else {
			s.Executor.Status = ProgressStatusFailed
			s.FailureKind = "executor_failed"
		}
		s.FailureReason = sanitizeProgressReason(runErr.Error())
		return
	}
	s.Status = ProgressStatusCompleted
	s.FailureKind = ""
	s.FailureReason = ""
}

func (s *ProgressState) MarkReviewStarted(reviewSessionID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.Phase = ProgressPhaseReviewRepair
	s.Status = ProgressStatusRunning
	s.Review.SessionID = strings.TrimSpace(reviewSessionID)
	s.Review.Status = ProgressStatusRunning
	s.Review.StartedAt = now
	s.Review.CompletedAt = ""
	s.Review.Error = ""
}

func (s *ProgressState) MarkReviewComplete(report ReviewRepairPhaseReport, reviewErr error) {
	s.Phase = ProgressPhaseReviewRepair
	s.Review.ReviewPlan = progressRelPath("", report.ReviewPlan)
	s.Review.Repairs = report.Repairs
	s.Review.AutoRepairs = report.AutoRepairs
	s.Review.ChecklistFound = report.ChecklistFound
	s.Review.ReasonClass = report.ReasonClass
	s.Review.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.Review.Status = strings.TrimSpace(report.Status)
	if s.Review.Status == "" {
		s.Review.Status = ProgressStatusCompleted
	}
	if reviewErr != nil {
		s.Status = ProgressStatusFailed
		s.Review.Status = ProgressStatusFailed
		s.Review.Error = sanitizeProgressReason(reviewErr.Error())
		s.FailureKind = "review_repair_failed"
		s.FailureReason = sanitizeProgressReason(reviewErr.Error())
		return
	}
	if strings.Contains(strings.ToLower(report.Status), "degraded") {
		s.Status = ProgressStatusDegraded
		return
	}
	if strings.Contains(strings.ToLower(report.Status), "blocked") {
		s.Status = ProgressStatusBlocked
		s.FailureKind = "review_repair_blocked"
		s.FailureReason = sanitizeProgressReason(s.Review.Status)
		return
	}
	if report.Skipped {
		s.Status = ProgressStatusSkipped
		return
	}
	s.Status = ProgressStatusCompleted
	s.FailureKind = ""
	s.FailureReason = ""
}

func (s *ProgressState) MarkSkippedReview() {
	s.Phase = ProgressPhaseComplete
	s.Status = ProgressStatusCompleted
	s.Review.Status = ProgressStatusSkipped
}

func (s ProgressState) PromptMatches(prompt string) bool {
	hash := strings.TrimSpace(s.PromptSHA256)
	return hash == "" || hash == ProgressPromptSHA256(prompt)
}

func (s ProgressState) ReviewResumeReady() bool {
	if strings.TrimSpace(s.PlanPath) == "" {
		return false
	}
	if s.Executor.Status != ProgressStatusCompleted {
		return false
	}
	if len(s.ChangedPaths) == 0 && s.TotalEdits == 0 {
		return false
	}
	switch s.Phase {
	case ProgressPhaseExecutor:
		return s.Status == ProgressStatusCompleted ||
			(s.Status == ProgressStatusFailed &&
				s.FailureKind == RunFailureSourceSanityFailed &&
				s.Executor.Status == ProgressStatusCompleted) ||
			(s.Status == ProgressStatusFailed &&
				s.FailureKind == RunFailureVerificationRequired &&
				s.Executor.Status == ProgressStatusCompleted)
	case ProgressPhaseReviewRepair:
		switch s.Status {
		case ProgressStatusRunning, ProgressStatusFailed, ProgressStatusDegraded, ProgressStatusBlocked:
			return true
		}
	}
	return false
}

func (s ProgressState) ExecutorPhaseReport() ExecutorPhaseReport {
	return ExecutorPhaseReport{
		Total:        s.Executor.Total,
		Succeeded:    s.Executor.Succeeded,
		Failed:       s.Executor.Failed,
		Failures:     append([]string(nil), s.Executor.Failures...),
		SourceSanity: append([]PhaseEvidenceCheck(nil), s.Executor.SourceSanity...),
		Verification: append([]PhaseEvidenceCheck(nil), s.Executor.Verification...),
		Handoff:      s.Executor.Handoff,
	}
}

func (s ProgressState) ResolvedPlanPath(root string) string {
	planPath := strings.TrimSpace(s.PlanPath)
	if planPath == "" || filepath.IsAbs(planPath) {
		return planPath
	}
	return filepath.Join(root, filepath.FromSlash(planPath))
}

func ProgressStateEventFields(state ProgressState, statePath string) map[string]any {
	fields := map[string]any{
		"session_id": state.SessionID,
		"phase":      state.Phase,
		"status":     state.Status,
		"state_path": statePath,
	}
	if state.PlanPath != "" {
		fields["plan_path"] = state.PlanPath
	}
	if state.FailureKind != "" {
		fields["failure_kind"] = state.FailureKind
	}
	if state.FailureReason != "" {
		fields["failure_reason"] = state.FailureReason
	}
	if len(state.RequiredEditPaths) > 0 {
		fields["required_edit_paths"] = append([]string(nil), state.RequiredEditPaths...)
	}
	if len(state.AllowedEditPaths) > 0 {
		fields["allowed_edit_paths"] = append([]string(nil), state.AllowedEditPaths...)
	}
	if len(state.WriteAllowedPaths) > 0 {
		fields["write_allowed_paths"] = append([]string(nil), state.WriteAllowedPaths...)
	}
	if len(state.ChangedPaths) > 0 {
		fields["changed_paths"] = append([]string(nil), state.ChangedPaths...)
	}
	if state.TotalEdits > 0 {
		fields["total_edits"] = state.TotalEdits
	}
	if state.Executor.Succeeded > 0 {
		fields["executor_succeeded"] = state.Executor.Succeeded
	}
	if state.Executor.Failed > 0 {
		fields["executor_failed"] = state.Executor.Failed
	}
	if state.Review.Status != "" {
		fields["review_status"] = state.Review.Status
	}
	if state.Review.Repairs > 0 {
		fields["review_repairs"] = state.Review.Repairs
	}
	if obligations := state.EditSetObligations.Normalized(); len(obligations) > 0 {
		fields["edit_set_obligations"] = obligations
		fields["edit_set_obligation_count"] = len(obligations)
	}
	return fields
}

func progressStatePath(root string, sessionID string) (string, string, error) {
	root = strings.TrimSpace(root)
	sessionID = strings.TrimSpace(sessionID)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	if !validProgressSessionID(sessionID) {
		return "", "", fmt.Errorf("invalid plan-execute progress session id %q", sessionID)
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", sessionID+".json"))
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}

func validProgressSessionID(sessionID string) bool {
	if sessionID == "" || len(sessionID) > 128 {
		return false
	}
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return !strings.Contains(sessionID, "..")
}

func progressRelPath(root string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(filepath.Clean(rel))
		}
	}
	if filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func sanitizeProgressState(state *ProgressState) {
	state.FailureReason = sanitizeProgressReason(state.FailureReason)
	state.EditSetObligations = state.EditSetObligations.Normalized()
	state.Executor.Failures = sanitizeProgressReasons(state.Executor.Failures)
	state.Review.Error = sanitizeProgressReason(state.Review.Error)
	for i := range state.Executor.SourceSanity {
		state.Executor.SourceSanity[i].Summary = sanitizeProgressReason(state.Executor.SourceSanity[i].Summary)
		state.Executor.SourceSanity[i].Error = sanitizeProgressReason(state.Executor.SourceSanity[i].Error)
	}
}

func sanitizeProgressReasons(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if sanitized := sanitizeProgressReason(value); sanitized != "" {
			out = append(out, sanitized)
		}
	}
	return out
}

func sanitizeProgressReason(reason string) string {
	return truncateProgressReason(recovery.RedactRecoveryText(reason))
}

func truncateProgressReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) <= 2000 {
		return reason
	}
	for i := range reason {
		if i >= 2000 {
			return strings.TrimSpace(reason[:i]) + fmt.Sprintf(" [truncated %d bytes]", len(reason)-i)
		}
	}
	return reason
}

func mergeProgressPaths(primary []string, fallback []string) []string {
	if len(primary) == 0 {
		return append([]string(nil), fallback...)
	}
	out := append([]string(nil), primary...)
	slices.Sort(out)
	return out
}
