package recovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const FailureRecordSchemaVersion = "idiom.recovery.failure_record.v1"

const (
	SourceLanePlanExecute = "plan-execute"
	SourceLaneImplement   = "implement"

	FailureClassPlannerFailed     = "planner_failed"
	FailureClassPlannerBlocked    = "planner_blocked"
	FailureClassPlanMissing       = "plan_missing"
	FailureClassPlanInvalid       = "plan_invalid"
	FailureClassScopeTooLarge     = "scope_too_large"
	FailureClassExecutorEdit      = "executor_edit_failed"
	FailureClassVerification      = "verification_failed"
	FailureClassReviewRepair      = "review_repair_failed"
	FailureClassStaleContext      = "stale_context"
	FailureClassBudgetExhausted   = "budget_exhausted"
	FailureClassDirtyWorkspace    = "dirty_workspace"
	FailureClassNeedsHumanAnswer  = "needs_human_answer"
	FailureClassUnsafePolicyBlock = "unsafe_policy_block"
	FailureClassUnknown           = "unknown_failure"

	NextActionReviewDecomposition = "review_decomposition"
	NextActionAnswerBlocker       = "answer_blocker"
	NextActionInspectFailure      = "inspect_failure"
	NextActionHumanIntervention   = "human_intervention"
	NextActionNone                = "none"

	DirtyStatusNotChecked    = "not_checked"
	DirtyStatusClean         = "clean"
	DirtyStatusDirtyInScope  = "dirty_in_scope"
	DirtyStatusDirtyOutScope = "dirty_out_of_scope"

	ScopeStatusNotChecked = "not_checked"
	ScopeStatusNone       = "none"
	ScopeStatusInScope    = "in_scope"
	ScopeStatusOutOfScope = "out_of_scope"
)

type PlanExecuteFailureInput struct {
	SessionID         string
	ProgressStatePath string
	Phase             string
	Status            string
	FailureKind       string
	FailureReason     string
	PromptSHA256      string
	PromptFile        string
	PlanPath          string
	PlanID            string
	PackID            string
	RequiredEditPaths []string
	AllowedEditPaths  []string
	ChangedPaths      []string
	TotalEdits        int
	ExecutorStatus    string
	ExecutorFailures  []string
	ReviewStatus      string
	ReviewReasonClass string
	ReviewError       string
}

type FailureRecordInput struct {
	FailureID      string
	SourceRun      SourceRun
	FailureClass   string
	FailureKind    string
	FailurePhase   string
	FailureSummary string
	EvidenceRefs   []EvidenceRef
	PartialState   PartialState
	NextAction     string
}

type FailureRecord struct {
	SchemaVersion string       `json:"schema_version"`
	FailureID     string       `json:"failure_id"`
	CreatedAt     string       `json:"created_at"`
	UpdatedAt     string       `json:"updated_at"`
	SourceRun     SourceRun    `json:"source_run"`
	Failure       Failure      `json:"failure"`
	PartialState  PartialState `json:"partial_state"`
	NextAction    string       `json:"next_valid_action"`
}

type SourceRun struct {
	Lane              string   `json:"lane"`
	SessionID         string   `json:"session_id"`
	ProgressStatePath string   `json:"progress_state_path,omitempty"`
	PromptSHA256      string   `json:"prompt_sha256,omitempty"`
	PromptFile        string   `json:"prompt_file,omitempty"`
	PlanPath          string   `json:"plan_path,omitempty"`
	PlanID            string   `json:"plan_id,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	RequiredEditPaths []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths  []string `json:"allowed_edit_paths,omitempty"`
}

type Failure struct {
	Class        string        `json:"class"`
	Kind         string        `json:"kind,omitempty"`
	Phase        string        `json:"phase,omitempty"`
	Summary      string        `json:"summary"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type EvidenceRef struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	ID      string `json:"id,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type PartialState struct {
	DirtyStatus  string   `json:"dirty_status"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
	TotalEdits   int      `json:"total_edits,omitempty"`
	ScopeStatus  string   `json:"scope_status"`
}

func BuildFailureRecord(input FailureRecordInput, now time.Time) (FailureRecord, error) {
	failureID := strings.TrimSpace(input.FailureID)
	if !validRecoveryComponent(failureID) {
		return FailureRecord{}, fmt.Errorf("invalid recovery failure id %q", input.FailureID)
	}
	source := cleanSourceRun(input.SourceRun)
	if strings.TrimSpace(source.Lane) == "" {
		return FailureRecord{}, fmt.Errorf("recovery source lane is empty")
	}
	if !validRecoveryComponent(source.SessionID) {
		return FailureRecord{}, fmt.Errorf("invalid recovery session id %q", input.SourceRun.SessionID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	failureClass := strings.TrimSpace(input.FailureClass)
	if failureClass == "" {
		failureClass = FailureClassUnknown
	}
	if !validFailureClass(failureClass) {
		return FailureRecord{}, fmt.Errorf("invalid recovery failure class %q", input.FailureClass)
	}
	partial := input.PartialState
	partial.ChangedPaths = cleanPathList(partial.ChangedPaths)
	if partial.DirtyStatus == "" {
		partial.DirtyStatus = DirtyStatusNotChecked
	}
	if partial.ScopeStatus == "" {
		partial.ScopeStatus = scopeStatus(partial.ChangedPaths, source.RequiredEditPaths, source.AllowedEditPaths)
	}
	nextAction := strings.TrimSpace(input.NextAction)
	if nextAction == "" {
		nextAction = nextActionForFailureClass(failureClass)
	}
	if !validNextAction(nextAction) {
		return FailureRecord{}, fmt.Errorf("invalid recovery next action %q", input.NextAction)
	}
	summary := sanitizeAndTruncate(input.FailureSummary, 2000)
	if summary == "" {
		summary = strings.TrimSpace(source.Lane) + " failed"
	}
	return FailureRecord{
		SchemaVersion: FailureRecordSchemaVersion,
		FailureID:     failureID,
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
		SourceRun:     source,
		Failure: Failure{
			Class:        failureClass,
			Kind:         strings.TrimSpace(input.FailureKind),
			Phase:        strings.TrimSpace(input.FailurePhase),
			Summary:      summary,
			EvidenceRefs: cleanEvidenceRefs(input.EvidenceRefs),
		},
		PartialState: partial,
		NextAction:   nextAction,
	}, nil
}

func BuildPlanExecuteFailureRecord(input PlanExecuteFailureInput, now time.Time) (FailureRecord, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if !validRecoveryComponent(sessionID) {
		return FailureRecord{}, fmt.Errorf("invalid plan-execute session id %q", input.SessionID)
	}
	failureClass := ClassifyPlanExecuteFailure(input.FailureKind, input.FailureReason, input.ReviewReasonClass)
	return BuildFailureRecord(FailureRecordInput{
		FailureID: PlanExecuteFailureID(sessionID),
		SourceRun: SourceRun{
			Lane:              SourceLanePlanExecute,
			SessionID:         sessionID,
			ProgressStatePath: cleanRelPath(input.ProgressStatePath),
			PromptSHA256:      strings.TrimSpace(input.PromptSHA256),
			PromptFile:        cleanRelPath(input.PromptFile),
			PlanPath:          cleanRelPath(input.PlanPath),
			PlanID:            strings.TrimSpace(input.PlanID),
			PackID:            strings.TrimSpace(input.PackID),
			RequiredEditPaths: cleanPathList(input.RequiredEditPaths),
			AllowedEditPaths:  cleanPathList(input.AllowedEditPaths),
		},
		FailureClass:   failureClass,
		FailureKind:    input.FailureKind,
		FailurePhase:   input.Phase,
		FailureSummary: failureSummary(input),
		EvidenceRefs:   planExecuteEvidenceRefs(input),
		PartialState: PartialState{
			DirtyStatus:  DirtyStatusNotChecked,
			ChangedPaths: cleanPathList(input.ChangedPaths),
			TotalEdits:   input.TotalEdits,
			ScopeStatus:  scopeStatus(input.ChangedPaths, input.RequiredEditPaths, input.AllowedEditPaths),
		},
		NextAction: nextActionForFailureClass(failureClass),
	}, now)
}

func PartialStateForDirtyPaths(dirtyPaths []string, changedPaths []string, requiredPaths []string, allowedPaths []string, totalEdits int) PartialState {
	changed := cleanPathList(changedPaths)
	dirty := cleanPathList(dirtyPaths)
	partial := PartialState{
		DirtyStatus:  DirtyStatusClean,
		ChangedPaths: changed,
		TotalEdits:   totalEdits,
		ScopeStatus:  scopeStatus(changed, requiredPaths, allowedPaths),
	}
	if len(dirty) == 0 {
		return partial
	}
	partial.ChangedPaths = cleanPathList(append(changed, dirty...))
	dirtyScope := scopeStatus(dirty, append(requiredPaths, changed...), allowedPaths)
	if dirtyScope == ScopeStatusOutOfScope || dirtyScope == ScopeStatusNotChecked {
		partial.DirtyStatus = DirtyStatusDirtyOutScope
		partial.ScopeStatus = ScopeStatusOutOfScope
		return partial
	}
	partial.DirtyStatus = DirtyStatusDirtyInScope
	if partial.ScopeStatus != ScopeStatusOutOfScope {
		partial.ScopeStatus = ScopeStatusInScope
	}
	return partial
}

func NextActionForPartialState(record FailureRecord, partial PartialState) string {
	if partial.DirtyStatus == DirtyStatusDirtyOutScope || partial.ScopeStatus == ScopeStatusOutOfScope {
		return NextActionHumanIntervention
	}
	return record.NextAction
}

func ClassifyPlanExecuteFailure(kind string, reason string, reviewReasonClass string) string {
	kind = strings.TrimSpace(kind)
	reasonLower := strings.ToLower(reason)
	switch kind {
	case "planner_failed":
		if looksScopeTooLarge(reasonLower) {
			return FailureClassScopeTooLarge
		}
		if looksBudgetExhausted(reasonLower) {
			return FailureClassBudgetExhausted
		}
		if looksStaleContext(reasonLower) {
			return FailureClassStaleContext
		}
		return FailureClassPlannerFailed
	case "planner_blocked":
		return FailureClassPlannerBlocked
	case "plan_artifact_missing":
		return FailureClassPlanMissing
	case "plan_artifact_invalid":
		return FailureClassPlanInvalid
	case "scope_too_large":
		return FailureClassScopeTooLarge
	case "verification_failed":
		return FailureClassVerification
	case "source_sanity_failed":
		return FailureClassVerification
	case "executor_failed":
		if looksStaleContext(reasonLower) {
			return FailureClassStaleContext
		}
		if looksVerificationFailed(reasonLower) {
			return FailureClassVerification
		}
		if looksScopeTooLarge(reasonLower) {
			return FailureClassScopeTooLarge
		}
		return FailureClassExecutorEdit
	case "review_repair_failed":
		if normalized := normalizeReviewFailureClass(reviewReasonClass); normalized != "" {
			return normalized
		}
		return FailureClassReviewRepair
	case "dirty_workspace":
		return FailureClassDirtyWorkspace
	case "unsafe_policy_block":
		return FailureClassUnsafePolicyBlock
	}
	if looksBudgetExhausted(reasonLower) {
		return FailureClassBudgetExhausted
	}
	if looksScopeTooLarge(reasonLower) {
		return FailureClassScopeTooLarge
	}
	if looksVerificationFailed(reasonLower) {
		return FailureClassVerification
	}
	if looksStaleContext(reasonLower) {
		return FailureClassStaleContext
	}
	return FailureClassUnknown
}

func WritePlanExecuteFailureRecord(root string, input PlanExecuteFailureInput, now time.Time) (FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record, err := BuildPlanExecuteFailureRecord(input, now)
	if err != nil {
		return FailureRecord{}, "", err
	}
	return writeFailureRecord(root, record, now)
}

func WriteFailureRecord(root string, input FailureRecordInput, now time.Time) (FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record, err := BuildFailureRecord(input, now)
	if err != nil {
		return FailureRecord{}, "", err
	}
	return writeFailureRecord(root, record, now)
}

func writeFailureRecord(root string, record FailureRecord, now time.Time) (FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	fullPath, relPath, err := FailureRecordPath(root, record.FailureID)
	if err != nil {
		return FailureRecord{}, "", err
	}
	if err := ensureRecoveryWritePath(root, fullPath); err != nil {
		return FailureRecord{}, "", fmt.Errorf("recovery failure record path rejected: %w", err)
	}
	if existing, _, found, err := TryLoadFailureRecord(root, record.FailureID); err == nil && found && strings.TrimSpace(existing.CreatedAt) != "" {
		record.CreatedAt = existing.CreatedAt
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return FailureRecord{}, "", fmt.Errorf("mkdir recovery failure dir: %w", err)
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return FailureRecord{}, "", fmt.Errorf("marshal recovery failure record: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := writeRecoveryTempFileExclusive(tmpPath, append(body, '\n')); err != nil {
		return FailureRecord{}, "", fmt.Errorf("write recovery failure record: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return FailureRecord{}, "", fmt.Errorf("replace recovery failure record: %w", err)
	}
	return record, relPath, nil
}

func TryLoadPlanExecuteFailureRecord(root string, sessionID string) (FailureRecord, string, bool, error) {
	return TryLoadFailureRecord(root, PlanExecuteFailureID(strings.TrimSpace(sessionID)))
}

func TryLoadImplementFailureRecord(root string, sessionID string) (FailureRecord, string, bool, error) {
	return TryLoadFailureRecord(root, ImplementFailureID(strings.TrimSpace(sessionID)))
}

func TryLoadFailureRecordForSession(root string, sessionID string) (FailureRecord, string, bool, error) {
	for _, failureID := range FailureIDCandidatesForSession(sessionID) {
		record, path, found, err := TryLoadFailureRecord(root, failureID)
		if err != nil || found {
			return record, path, found, err
		}
	}
	return FailureRecord{}, "", false, nil
}

func TryLoadFailureRecord(root string, failureID string) (FailureRecord, string, bool, error) {
	fullPath, relPath, err := FailureRecordPath(root, failureID)
	if err != nil {
		return FailureRecord{}, "", false, err
	}
	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return FailureRecord{}, relPath, false, nil
	}
	if err != nil {
		return FailureRecord{}, relPath, false, fmt.Errorf("read recovery failure record %s: %w", relPath, err)
	}
	var record FailureRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return FailureRecord{}, relPath, false, fmt.Errorf("decode recovery failure record %s: %w", relPath, err)
	}
	if record.SchemaVersion != FailureRecordSchemaVersion {
		return FailureRecord{}, relPath, false, fmt.Errorf("unsupported recovery failure record schema %q in %s", record.SchemaVersion, relPath)
	}
	return record, relPath, true, nil
}

func PlanExecuteFailureRecordPath(root string, sessionID string) (string, string, error) {
	return FailureRecordPath(root, PlanExecuteFailureID(strings.TrimSpace(sessionID)))
}

func FailureRecordPath(root string, failureID string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	failureID = strings.TrimSpace(failureID)
	if !validRecoveryComponent(failureID) {
		return "", "", fmt.Errorf("invalid recovery failure id %q", failureID)
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "failure.json"))
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}

func PlanExecuteFailureID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if strings.HasPrefix(sessionID, "plan-execute-") {
		return sessionID
	}
	return "plan-execute-" + sessionID
}

func ImplementFailureID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if strings.HasPrefix(sessionID, "implement-") {
		return sessionID
	}
	return "implement-" + sessionID
}

func FailureIDCandidatesForSession(sessionID string) []string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if strings.HasPrefix(sessionID, "plan-execute-") || strings.HasPrefix(sessionID, "implement-") {
		return []string{sessionID}
	}
	return []string{PlanExecuteFailureID(sessionID), ImplementFailureID(sessionID)}
}

func cleanSourceRun(source SourceRun) SourceRun {
	return SourceRun{
		Lane:              strings.TrimSpace(source.Lane),
		SessionID:         strings.TrimSpace(source.SessionID),
		ProgressStatePath: cleanRelPath(source.ProgressStatePath),
		PromptSHA256:      strings.TrimSpace(source.PromptSHA256),
		PromptFile:        cleanRelPath(source.PromptFile),
		PlanPath:          cleanRelPath(source.PlanPath),
		PlanID:            strings.TrimSpace(source.PlanID),
		PackID:            strings.TrimSpace(source.PackID),
		RequiredEditPaths: cleanPathList(source.RequiredEditPaths),
		AllowedEditPaths:  cleanPathList(source.AllowedEditPaths),
	}
}

func cleanEvidenceRefs(refs []EvidenceRef) []EvidenceRef {
	var out []EvidenceRef
	for _, ref := range refs {
		clean := EvidenceRef{
			Kind:    strings.TrimSpace(ref.Kind),
			Path:    cleanRelPath(ref.Path),
			ID:      strings.TrimSpace(ref.ID),
			Summary: sanitizeAndTruncate(ref.Summary, 500),
		}
		if clean.Kind == "" {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func validFailureClass(value string) bool {
	switch strings.TrimSpace(value) {
	case FailureClassPlannerFailed,
		FailureClassPlannerBlocked,
		FailureClassPlanMissing,
		FailureClassPlanInvalid,
		FailureClassScopeTooLarge,
		FailureClassExecutorEdit,
		FailureClassVerification,
		FailureClassReviewRepair,
		FailureClassStaleContext,
		FailureClassBudgetExhausted,
		FailureClassDirtyWorkspace,
		FailureClassNeedsHumanAnswer,
		FailureClassUnsafePolicyBlock,
		FailureClassUnknown:
		return true
	default:
		return false
	}
}

func validNextAction(value string) bool {
	switch strings.TrimSpace(value) {
	case NextActionReviewDecomposition,
		NextActionAnswerBlocker,
		NextActionInspectFailure,
		NextActionHumanIntervention,
		NextActionNone:
		return true
	default:
		return false
	}
}

func validRecoveryComponent(value string) bool {
	if value == "" || len(value) > 160 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return !strings.Contains(value, "..")
}

func failureSummary(input PlanExecuteFailureInput) string {
	if reason := strings.TrimSpace(input.FailureReason); reason != "" {
		return sanitizeAndTruncate(reason, 2000)
	}
	if err := strings.TrimSpace(input.ReviewError); err != "" {
		return sanitizeAndTruncate(err, 2000)
	}
	if kind := strings.TrimSpace(input.FailureKind); kind != "" {
		return kind
	}
	return "plan-execute failed"
}

func planExecuteEvidenceRefs(input PlanExecuteFailureInput) []EvidenceRef {
	var refs []EvidenceRef
	if path := cleanRelPath(input.ProgressStatePath); path != "" {
		refs = append(refs, EvidenceRef{Kind: "progress_state", Path: path})
	}
	if path := cleanRelPath(input.PlanPath); path != "" {
		refs = append(refs, EvidenceRef{Kind: "plan_artifact", Path: path})
	}
	for _, path := range cleanPathList(input.ChangedPaths) {
		refs = append(refs, EvidenceRef{Kind: "changed_path", Path: path})
	}
	for _, failure := range input.ExecutorFailures {
		if failure = strings.TrimSpace(failure); failure != "" {
			refs = append(refs, EvidenceRef{Kind: "executor_failure", Summary: sanitizeAndTruncate(failure, 500)})
		}
	}
	if status := strings.TrimSpace(input.ReviewStatus); status != "" {
		refs = append(refs, EvidenceRef{Kind: "review_repair_status", ID: status})
	}
	if class := strings.TrimSpace(input.ReviewReasonClass); class != "" {
		refs = append(refs, EvidenceRef{Kind: "review_repair_reason", ID: class})
	}
	return refs
}

func nextActionForFailureClass(failureClass string) string {
	switch failureClass {
	case FailureClassPlannerBlocked, FailureClassNeedsHumanAnswer:
		return NextActionAnswerBlocker
	case FailureClassDirtyWorkspace, FailureClassUnsafePolicyBlock:
		return NextActionHumanIntervention
	case FailureClassUnknown:
		return NextActionInspectFailure
	default:
		return NextActionReviewDecomposition
	}
}

func normalizeReviewFailureClass(value string) string {
	switch strings.TrimSpace(value) {
	case "stale_context":
		return FailureClassStaleContext
	case "budget_exhausted":
		return FailureClassBudgetExhausted
	case "needs_human_answer":
		return FailureClassNeedsHumanAnswer
	case "unsafe_policy_block":
		return FailureClassUnsafePolicyBlock
	case "scope_too_large":
		return FailureClassScopeTooLarge
	case "verification_failed":
		return FailureClassVerification
	}
	return ""
}

func looksBudgetExhausted(reasonLower string) bool {
	return strings.Contains(reasonLower, "budget") ||
		strings.Contains(reasonLower, "max_tokens") ||
		strings.Contains(reasonLower, "maximum tokens") ||
		strings.Contains(reasonLower, "context length") ||
		strings.Contains(reasonLower, "truncat")
}

func looksStaleContext(reasonLower string) bool {
	return strings.Contains(reasonLower, "stale") ||
		strings.Contains(reasonLower, "anchor") ||
		strings.Contains(reasonLower, "old_text not found") ||
		strings.Contains(reasonLower, "does not match current")
}

func looksScopeTooLarge(reasonLower string) bool {
	return strings.Contains(reasonLower, "scope too large") ||
		strings.Contains(reasonLower, "too much scope") ||
		strings.Contains(reasonLower, "atom too large") ||
		strings.Contains(reasonLower, "atom-size") ||
		strings.Contains(reasonLower, "single purpose") ||
		strings.Contains(reasonLower, "single-purpose") ||
		strings.Contains(reasonLower, "too many required paths") ||
		strings.Contains(reasonLower, "too many files")
}

func looksVerificationFailed(reasonLower string) bool {
	return strings.Contains(reasonLower, "verification failed") ||
		strings.Contains(reasonLower, "verification command failed") ||
		strings.Contains(reasonLower, "source_sanity failed") ||
		strings.Contains(reasonLower, "source sanity failed") ||
		strings.Contains(reasonLower, "compile failed") ||
		strings.Contains(reasonLower, "go test compile failed") ||
		strings.Contains(reasonLower, "required test failed") ||
		strings.Contains(reasonLower, "required tests failed") ||
		strings.Contains(reasonLower, "test failed") ||
		strings.Contains(reasonLower, "tests failed") ||
		strings.Contains(reasonLower, "go test failed") ||
		strings.Contains(reasonLower, "failing test") ||
		strings.Contains(reasonLower, "failing package")
}

func scopeStatus(changedPaths []string, requiredPaths []string, allowedPaths []string) string {
	changed := cleanPathList(changedPaths)
	if len(changed) == 0 {
		return ScopeStatusNone
	}
	allowed := map[string]bool{}
	for _, path := range cleanPathList(requiredPaths) {
		allowed[path] = true
	}
	for _, path := range cleanPathList(allowedPaths) {
		allowed[path] = true
	}
	if len(allowed) == 0 {
		return ScopeStatusNotChecked
	}
	for _, path := range changed {
		if !allowed[path] {
			return ScopeStatusOutOfScope
		}
	}
	return ScopeStatusInScope
}

func cleanPathList(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = cleanRelPath(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func cleanRelPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}

func sanitizeAndTruncate(value string, limit int) string {
	return truncate(RedactRecoveryText(value), limit)
}

func RedactRecoveryText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = redactFencedRecoveryBlocks(value)
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if looksSensitiveRecoveryLine(line) {
			lines[i] = "[redacted sensitive recovery detail]"
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func redactFencedRecoveryBlocks(value string) string {
	lines := strings.Split(value, "\n")
	inFence := false
	redactedFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inFence {
				inFence = true
				redactedFence = false
				continue
			}
			inFence = false
			continue
		}
		if inFence {
			if !redactedFence {
				lines[i] = "[redacted fenced recovery detail]"
				redactedFence = true
			} else {
				lines[i] = ""
			}
		}
	}
	return strings.Join(lines, "\n")
}

func looksSensitiveRecoveryLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, "private key") {
		return true
	}
	if strings.Contains(lower, "raw_prompt") ||
		strings.Contains(lower, "secret_prompt") ||
		strings.Contains(lower, "transcript") ||
		strings.Contains(lower, "file_body") ||
		strings.Contains(lower, "file body") ||
		strings.Contains(lower, "protected content") {
		return true
	}
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "sk-") {
		return true
	}
	hasAssignment := strings.Contains(line, "=") || strings.Contains(line, ":")
	if !hasAssignment {
		return false
	}
	for _, indicator := range []string{
		"api_key",
		"api key",
		"apikey",
		"access_token",
		"refresh_token",
		"auth_token",
		"password",
		"passwd",
		"credential",
		"secret",
		"old_text",
		"new_text",
		"prompt",
	} {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}
