package planexecute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/sessionid"
	"idiom/internal/workflows/recovery"
)

const ProgressStateSchemaVersion = "idiom.plan_execute.progress.v1"

type ProgressState struct {
	SchemaVersion                 string                                 `json:"schema_version"`
	SessionID                     string                                 `json:"session_id"`
	CreatedAt                     string                                 `json:"created_at"`
	UpdatedAt                     string                                 `json:"updated_at"`
	PromptSHA256                  string                                 `json:"prompt_sha256,omitempty"`
	PromptFile                    string                                 `json:"prompt_file,omitempty"`
	Phase                         plancontract.PlanExecuteProgressPhase  `json:"phase"`
	Status                        plancontract.PlanExecuteProgressStatus `json:"status"`
	FailureKind                   plancontract.PlanExecuteFailureKind    `json:"failure_kind,omitempty"`
	FailureReason                 string                                 `json:"failure_reason,omitempty"`
	PlanPath                      string                                 `json:"plan_path,omitempty"`
	PlanID                        string                                 `json:"plan_id,omitempty"`
	PackID                        string                                 `json:"pack_id,omitempty"`
	ParentAtomID                  string                                 `json:"parent_atom_id,omitempty"`
	ParentAtomTitle               string                                 `json:"parent_atom_title,omitempty"`
	RequiredEditPaths             []string                               `json:"required_edit_paths,omitempty"`
	AllowedEditPaths              []string                               `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths             []string                               `json:"write_allowed_paths,omitempty"`
	WriteAllowedExplicit          bool                                   `json:"write_allowed_explicit,omitempty"`
	ScopeContractID               string                                 `json:"scope_contract_id,omitempty"`
	ScopeContractHash             string                                 `json:"scope_contract_hash,omitempty"`
	RequiredAcceptanceEvidenceIDs []string                               `json:"required_acceptance_evidence_ids,omitempty"`
	AcceptanceEvidenceIDs         []string                               `json:"acceptance_evidence_ids,omitempty"`
	EditSetObligations            plancontract.EditSetObligations        `json:"edit_set_obligations,omitempty"`
	ChangedPaths                  []string                               `json:"changed_paths,omitempty"`
	TotalEdits                    int                                    `json:"total_edits,omitempty"`
	Edits                         []ProgressEdit                         `json:"edits,omitempty"`
	Executor                      ProgressExecutor                       `json:"executor,omitempty"`
	Review                        ProgressReviewRepair                   `json:"review_repair,omitempty"`
	ReviewBaseline                ReviewRepairBaseline                   `json:"review_baseline,omitempty"`
}

type ProgressEdit struct {
	Step      int                                    `json:"step"`
	Total     int                                    `json:"total"`
	Path      string                                 `json:"path"`
	Status    plancontract.PlanExecuteProgressStatus `json:"status"`
	UpdatedAt string                                 `json:"updated_at"`
}

type ProgressExecutor struct {
	Status               plancontract.PlanExecuteProgressStatus `json:"status,omitempty"`
	Total                int                                    `json:"total,omitempty"`
	Succeeded            int                                    `json:"succeeded,omitempty"`
	Failed               int                                    `json:"failed,omitempty"`
	Failures             []string                               `json:"failures,omitempty"`
	SourceSanity         []plancontract.PhaseEvidenceCheck      `json:"source_sanity,omitempty"`
	Verification         []plancontract.PhaseEvidenceCheck      `json:"verification,omitempty"`
	VerificationCommands []plancontract.VerificationCommand     `json:"verification_commands,omitempty"`
	VerificationSkips    []plancontract.VerificationSkip        `json:"verification_skips,omitempty"`
	VerificationRejects  []plancontract.VerificationReject      `json:"verification_rejects,omitempty"`
	Handoff              ExecutorHandoffPacket                  `json:"handoff,omitempty"`
}

type ProgressReviewRepair struct {
	SessionID      string                               `json:"session_id,omitempty"`
	Status         plancontract.PlanExecuteReviewStatus `json:"status,omitempty"`
	StatusDetail   string                               `json:"status_detail,omitempty"`
	ReviewPlan     string                               `json:"review_plan,omitempty"`
	Repairs        int                                  `json:"repairs,omitempty"`
	AutoRepairs    int                                  `json:"auto_repairs,omitempty"`
	ChecklistFound bool                                 `json:"checklist_found,omitempty"`
	ReasonClass    string                               `json:"reason_class,omitempty"`
	Error          string                               `json:"error,omitempty"`
	StartedAt      string                               `json:"started_at,omitempty"`
	CompletedAt    string                               `json:"completed_at,omitempty"`
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
		Phase:             plancontract.PlanExecuteProgressPhasePlanning,
		Status:            plancontract.PlanExecuteProgressStatusRunning,
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

func (s *ProgressState) SetWriteAllowedEnvelope(paths []string, explicit bool) {
	if s == nil {
		return
	}

	s.WriteAllowedPaths = mergePlanExecutePaths(paths)
	s.WriteAllowedExplicit = explicit
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func (s *ProgressState) SetScopeContractID(contractID string) {
	if s == nil {
		return
	}

	contractID = strings.TrimSpace(contractID)
	s.ScopeContractID = contractID
	s.ScopeContractHash = contractID
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

	if err := validateProgressLifecycle(*state, ""); err != nil {
		return "", err
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

	if err := atomicfile.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		return relPath, fmt.Errorf("write plan-execute progress state: %w", err)
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
	} else if strings.TrimSpace(state.SessionID) != strings.TrimSpace(resumeID) {
		return ProgressState{}, relPath, false, fmt.Errorf("plan-execute progress session id %q does not match requested session %q in %s", state.SessionID, resumeID, relPath)
	}

	if !validProgressSessionID(state.SessionID) {
		return ProgressState{}, relPath, false, fmt.Errorf("invalid plan-execute progress session id %q in %s", state.SessionID, relPath)
	}

	if state.SchemaVersion == "" {
		state.SchemaVersion = ProgressStateSchemaVersion
	}

	if err := validateProgressLifecycle(state, relPath); err != nil {
		return ProgressState{}, relPath, false, err
	}

	return state, relPath, true, nil
}

func validateProgressLifecycle(state ProgressState, relPath string) error {
	phase, ok := plancontract.ParsePlanExecuteProgressPhase(string(state.Phase))
	if !ok {
		if strings.TrimSpace(relPath) == "" {
			return fmt.Errorf("invalid plan-execute progress phase %q", state.Phase)
		}
		return fmt.Errorf("invalid plan-execute progress phase %q in %s", state.Phase, relPath)
	}
	status, ok := plancontract.ParsePlanExecuteProgressStatus(string(state.Status))
	if !ok {
		if strings.TrimSpace(relPath) == "" {
			return fmt.Errorf("invalid plan-execute progress status %q", state.Status)
		}
		return fmt.Errorf("invalid plan-execute progress status %q in %s", state.Status, relPath)
	}
	if !progressPhaseAllowsStatus(phase, status) {
		if strings.TrimSpace(relPath) == "" {
			return fmt.Errorf("invalid plan-execute progress lifecycle phase=%q status=%q", state.Phase, state.Status)
		}
		return fmt.Errorf("invalid plan-execute progress lifecycle phase=%q status=%q in %s", state.Phase, state.Status, relPath)
	}
	if strings.TrimSpace(string(state.FailureKind)) != "" {
		if _, ok := plancontract.ParsePlanExecuteFailureKind(string(state.FailureKind)); !ok {
			if strings.TrimSpace(relPath) == "" {
				return fmt.Errorf("invalid plan-execute progress failure_kind %q", state.FailureKind)
			}
			return fmt.Errorf("invalid plan-execute progress failure_kind %q in %s", state.FailureKind, relPath)
		}
	}
	if strings.TrimSpace(string(state.Review.Status)) != "" {
		if _, ok := plancontract.ParsePlanExecuteReviewStatus(string(state.Review.Status)); !ok {
			if strings.TrimSpace(relPath) == "" {
				return fmt.Errorf("invalid plan-execute progress review status %q", state.Review.Status)
			}
			return fmt.Errorf("invalid plan-execute progress review status %q in %s", state.Review.Status, relPath)
		}
	}
	return nil
}

func progressPhaseAllowsStatus(phase plancontract.PlanExecuteProgressPhase, status plancontract.PlanExecuteProgressStatus) bool {
	switch phase {
	case plancontract.PlanExecuteProgressPhasePlanning:
		return status == plancontract.PlanExecuteProgressStatusRunning ||
			status == plancontract.PlanExecuteProgressStatusFailed
	case plancontract.PlanExecuteProgressPhaseExecutor:
		return status == plancontract.PlanExecuteProgressStatusRunning ||
			status == plancontract.PlanExecuteProgressStatusCompleted ||
			status == plancontract.PlanExecuteProgressStatusFailed
	case plancontract.PlanExecuteProgressPhaseReviewRepair:
		return status == plancontract.PlanExecuteProgressStatusRunning ||
			status == plancontract.PlanExecuteProgressStatusCompleted ||
			status == plancontract.PlanExecuteProgressStatusFailed ||
			status == plancontract.PlanExecuteProgressStatusDegraded ||
			status == plancontract.PlanExecuteProgressStatusBlocked ||
			status == plancontract.PlanExecuteProgressStatusSkipped
	case plancontract.PlanExecuteProgressPhaseComplete:
		return status == plancontract.PlanExecuteProgressStatusCompleted ||
			status == plancontract.PlanExecuteProgressStatusFailed ||
			status == plancontract.PlanExecuteProgressStatusDegraded ||
			status == plancontract.PlanExecuteProgressStatusBlocked ||
			status == plancontract.PlanExecuteProgressStatusSkipped
	default:
		return false
	}
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

func (s *ProgressState) MarkPlanLoaded(root string, planPath string, plan planartifact.PlanArtifact, baseline ReviewRepairBaseline) {
	s.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	s.Status = plancontract.PlanExecuteProgressStatusRunning
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
	s.Phase = plancontract.PlanExecuteProgressPhasePlanning
	s.Status = plancontract.PlanExecuteProgressStatusFailed
	s.FailureKind = plancontract.PlanExecuteFailureKind(strings.TrimSpace(kind))
	s.FailureReason = sanitizeProgressReason(reason)
}

func (s *ProgressState) MarkEditProgress(step int, total int, path string, succeeded bool) {
	if step <= 0 {
		return
	}

	s.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	s.Status = plancontract.PlanExecuteProgressStatusRunning
	s.Executor.Status = plancontract.PlanExecuteProgressStatusRunning
	s.TotalEdits = total
	s.Executor.Total = total

	status := plancontract.PlanExecuteProgressStatusCompleted

	if !succeeded {
		status = plancontract.PlanExecuteProgressStatusFailed
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
	s.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	s.Executor = ProgressExecutor{
		Status:               plancontract.PlanExecuteProgressStatusCompleted,
		Total:                report.Total,
		Succeeded:            report.Succeeded,
		Failed:               report.Failed,
		Failures:             sanitizeProgressReasons(report.Failures),
		SourceSanity:         append([]plancontract.PhaseEvidenceCheck(nil), report.SourceSanity...),
		Verification:         append([]plancontract.PhaseEvidenceCheck(nil), report.Verification...),
		VerificationCommands: append([]plancontract.VerificationCommand(nil), report.VerificationCommands...),
		VerificationSkips:    append([]plancontract.VerificationSkip(nil), report.VerificationSkips...),
		VerificationRejects:  append([]plancontract.VerificationReject(nil), report.VerificationRejects...),
		Handoff:              report.Handoff,
	}

	if runErr != nil {
		s.Status = plancontract.PlanExecuteProgressStatusFailed
		if isPlanExecuteJSONEventWriteFailure(runErr) {
			s.Executor.Status = plancontract.PlanExecuteProgressStatusFailed
			s.FailureKind = plancontract.PlanExecuteFailureJSONEventWriteFailed
		} else if ExecutorSourceSanityFailed(report, runErr) {
			s.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
			s.FailureKind = plancontract.PlanExecuteFailureSourceSanityFailed
		} else if ExecutorVerificationFailed(report, runErr) {
			s.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
			s.FailureKind = plancontract.PlanExecuteFailureVerificationFailed
		} else {
			s.Executor.Status = plancontract.PlanExecuteProgressStatusFailed
			s.FailureKind = plancontract.PlanExecuteFailureExecutorFailed
		}

		s.FailureReason = sanitizeProgressReason(runErr.Error())

		return
	}

	s.Status = plancontract.PlanExecuteProgressStatusCompleted
	s.FailureKind = ""
	s.FailureReason = ""
}

func (s *ProgressState) MarkReviewStarted(reviewSessionID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	s.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
	s.Status = plancontract.PlanExecuteProgressStatusRunning
	s.Review.SessionID = strings.TrimSpace(reviewSessionID)
	s.Review.Status = plancontract.PlanExecuteReviewStatusRunning
	s.Review.StatusDetail = ""
	s.Review.StartedAt = now
	s.Review.CompletedAt = ""
	s.Review.Error = ""
}

func (s *ProgressState) MarkReviewComplete(report ReviewRepairPhaseReport, reviewErr error) {
	s.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
	s.Review.ReviewPlan = progressRelPath("", report.ReviewPlan)
	s.Review.Repairs = report.Repairs
	s.Review.AutoRepairs = report.AutoRepairs
	s.Review.ChecklistFound = report.ChecklistFound
	s.Review.ReasonClass = report.ReasonClass
	s.Review.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.Review.Status = report.Status
	s.Review.StatusDetail = sanitizeProgressReason(report.StatusDetail)
	if len(report.CombinedChangedPaths) > 0 {
		s.ChangedPaths = mergePlanExecutePaths(append(append([]string(nil), s.ChangedPaths...), report.CombinedChangedPaths...))
	} else if len(report.RepairPaths) > 0 {
		s.ChangedPaths = mergePlanExecutePaths(append(append([]string(nil), s.ChangedPaths...), report.RepairPaths...))
	}

	if s.Review.Status == "" {
		s.Review.Status = plancontract.PlanExecuteReviewStatusCompleted
	}

	if reviewErr != nil {
		s.Status = plancontract.PlanExecuteProgressStatusFailed
		s.Review.Status = plancontract.PlanExecuteReviewStatusFailed
		s.Review.Error = sanitizeProgressReason(reviewErr.Error())
		if isPlanExecuteJSONEventWriteFailure(reviewErr) {
			s.FailureKind = plancontract.PlanExecuteFailureJSONEventWriteFailed
		} else {
			s.FailureKind = plancontract.PlanExecuteFailureReviewRepairFailed
		}
		s.FailureReason = sanitizeProgressReason(reviewErr.Error())

		return
	}

	switch report.Status {
	case plancontract.PlanExecuteReviewStatusDegraded:
		s.Status = plancontract.PlanExecuteProgressStatusDegraded
		s.FailureKind = firstProgressFailureKind(report.FailureKind, plancontract.PlanExecuteFailureReviewRepairDegraded)
		s.FailureReason = sanitizeProgressReason(firstProgressNonEmptyString(report.FailureReason, report.StatusDetail, string(s.Review.Status)))

		return
	case plancontract.PlanExecuteReviewStatusBlocked:
		s.Status = plancontract.PlanExecuteProgressStatusBlocked
		s.FailureKind = firstProgressFailureKind(report.FailureKind, plancontract.PlanExecuteFailureReviewRepairBlocked)
		s.FailureReason = sanitizeProgressReason(firstProgressNonEmptyString(report.FailureReason, report.StatusDetail, string(s.Review.Status)))

		return
	}

	if report.Skipped {
		s.Status = plancontract.PlanExecuteProgressStatusSkipped
		return
	}

	s.Status = plancontract.PlanExecuteProgressStatusCompleted
	s.FailureKind = ""
	s.FailureReason = ""
}

func (s *ProgressState) MarkSkippedReview() {
	s.Phase = plancontract.PlanExecuteProgressPhaseComplete
	s.Status = plancontract.PlanExecuteProgressStatusCompleted
	s.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
}

func (s ProgressState) PromptMatches(prompt string) bool {
	hash := strings.TrimSpace(s.PromptSHA256)
	return hash == "" || hash == ProgressPromptSHA256(prompt)
}

func (s ProgressState) ReviewResumeReady() bool {
	if strings.TrimSpace(s.PlanPath) == "" {
		return false
	}

	if s.Executor.Status != plancontract.PlanExecuteProgressStatusCompleted {
		return false
	}

	if len(s.ChangedPaths) == 0 && s.TotalEdits == 0 {
		return false
	}

	switch s.Phase {
	case plancontract.PlanExecuteProgressPhaseExecutor:
		return s.Status == plancontract.PlanExecuteProgressStatusCompleted ||
			(s.Status == plancontract.PlanExecuteProgressStatusFailed &&
				s.FailureKind == plancontract.PlanExecuteFailureSourceSanityFailed &&
				s.Executor.Status == plancontract.PlanExecuteProgressStatusCompleted) ||
			(s.Status == plancontract.PlanExecuteProgressStatusFailed &&
				s.FailureKind == plancontract.PlanExecuteFailureVerificationFailed &&
				s.Executor.Status == plancontract.PlanExecuteProgressStatusCompleted)
	case plancontract.PlanExecuteProgressPhaseReviewRepair:
		switch s.Status {
		case plancontract.PlanExecuteProgressStatusRunning, plancontract.PlanExecuteProgressStatusFailed, plancontract.PlanExecuteProgressStatusDegraded, plancontract.PlanExecuteProgressStatusBlocked:
			return true
		}
	}

	return false
}

func (s ProgressState) ExecutorPhaseReport() ExecutorPhaseReport {
	return ExecutorPhaseReport{
		Total:                s.Executor.Total,
		Succeeded:            s.Executor.Succeeded,
		Failed:               s.Executor.Failed,
		Failures:             append([]string(nil), s.Executor.Failures...),
		SourceSanity:         append([]plancontract.PhaseEvidenceCheck(nil), s.Executor.SourceSanity...),
		Verification:         append([]plancontract.PhaseEvidenceCheck(nil), s.Executor.Verification...),
		VerificationCommands: append([]plancontract.VerificationCommand(nil), s.Executor.VerificationCommands...),
		VerificationSkips:    append([]plancontract.VerificationSkip(nil), s.Executor.VerificationSkips...),
		VerificationRejects:  append([]plancontract.VerificationReject(nil), s.Executor.VerificationRejects...),
		Handoff:              s.Executor.Handoff,
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
		plancontract.PlanExecuteEventFieldSessionID:    state.SessionID,
		plancontract.PlanExecuteTerminalFieldPhase:     state.Phase,
		plancontract.PlanExecuteTerminalFieldStatus:    state.Status,
		plancontract.PlanExecuteTerminalFieldStatePath: statePath,
	}
	if state.PlanPath != "" {
		fields[plancontract.PlanExecuteEventFieldPlanPath] = state.PlanPath
	}

	if state.FailureKind != "" {
		fields[plancontract.PlanExecuteTerminalFieldFailureKind] = string(state.FailureKind)
	}

	if state.FailureReason != "" {
		fields[plancontract.PlanExecuteTerminalFieldFailureReason] = state.FailureReason
	}

	if len(state.RequiredEditPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldRequiredEditPaths] = append([]string(nil), state.RequiredEditPaths...)
	}

	if len(state.AllowedEditPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldAllowedEditPaths] = append([]string(nil), state.AllowedEditPaths...)
	}

	if len(state.WriteAllowedPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldWriteAllowedPaths] = append([]string(nil), state.WriteAllowedPaths...)
	}

	if state.WriteAllowedExplicit {
		fields["write_allowed_explicit"] = true
	}

	if state.ScopeContractID != "" {
		fields[plancontract.PlanExecuteTerminalFieldScopeContractID] = state.ScopeContractID
	}

	if state.ScopeContractHash != "" {
		fields[plancontract.PlanExecuteTerminalFieldScopeContractHash] = state.ScopeContractHash
	}

	if len(state.AcceptanceEvidenceIDs) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldAcceptanceEvidenceIDs] = append([]string(nil), state.AcceptanceEvidenceIDs...)
	}

	if len(state.ChangedPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldChangedPaths] = append([]string(nil), state.ChangedPaths...)
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
		fields[plancontract.PlanExecuteTerminalFieldReviewStatus] = string(state.Review.Status)
	}

	if state.Review.Status == plancontract.PlanExecuteReviewStatusSkipped {
		fields[plancontract.PlanExecuteTerminalFieldReviewRepairSkipped] = true
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
	return sessionid.Valid(sessionID)
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
	state.RequiredEditPaths = mergePlanExecutePaths(state.RequiredEditPaths)
	state.AllowedEditPaths = mergePlanExecutePaths(state.AllowedEditPaths)
	state.WriteAllowedPaths = mergePlanExecutePaths(state.WriteAllowedPaths)
	state.ScopeContractID = strings.TrimSpace(state.ScopeContractID)
	state.ScopeContractHash = strings.TrimSpace(state.ScopeContractHash)
	state.RequiredAcceptanceEvidenceIDs = normalizePlanExecuteTerminalStableIDs(state.RequiredAcceptanceEvidenceIDs)
	state.AcceptanceEvidenceIDs = normalizePlanExecuteTerminalStableIDs(state.AcceptanceEvidenceIDs)
	state.ChangedPaths = mergePlanExecutePaths(state.ChangedPaths)
	state.EditSetObligations = state.EditSetObligations.Normalized()
	state.Executor.Failures = sanitizeProgressReasons(state.Executor.Failures)
	state.Review.Error = sanitizeProgressReason(state.Review.Error)

	for i := range state.Executor.SourceSanity {
		state.Executor.SourceSanity[i].Summary = sanitizeProgressReason(state.Executor.SourceSanity[i].Summary)
		state.Executor.SourceSanity[i].Error = sanitizeProgressReason(state.Executor.SourceSanity[i].Error)
	}

	for i := range state.Executor.Verification {
		state.Executor.Verification[i].Summary = sanitizeProgressReason(state.Executor.Verification[i].Summary)
		state.Executor.Verification[i].Error = sanitizeProgressReason(state.Executor.Verification[i].Error)
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
	return truncateProgressReason(recovery.RedactText(reason))
}

func firstProgressNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func firstProgressFailureKind(values ...plancontract.PlanExecuteFailureKind) plancontract.PlanExecuteFailureKind {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}

	return ""
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
		return mergePlanExecutePaths(fallback)
	}

	return mergePlanExecutePaths(primary)
}
