package recovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/textutil"
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
	ParentAtomID      string
	ParentAtomTitle   string
	LockedContract    *recoverycontract.ContractSnapshot
	RequiredEditPaths []string
	AllowedEditPaths  []string
	ChangedPaths      []string
	TotalEdits        int
	ExecutorStatus    string
	ExecutorFailures  []string
	ReviewStatus      string
	ReviewReasonClass string
	ReviewError       string
	InvariantFailures []recoverycontract.InvariantFailure
}

type FailureRecordInput struct {
	FailureID         string
	SourceRun         recoverycontract.SourceRun
	LockedContract    *recoverycontract.ContractSnapshot
	FailureClass      recoverycontract.FailureClass
	FailureKind       string
	FailurePhase      string
	FailureSummary    string
	EvidenceRefs      []recoverycontract.EvidenceRef
	InvariantFailures []recoverycontract.InvariantFailure
	PartialState      recoverycontract.PartialState
	Implementation    *recoverycontract.ImplementationState
	NextAction        recoverycontract.NextAction
}

type PlanExecutePlannerBlockedSourceRefresh struct {
	ActionClass recoverycontract.NextAction
	Path        string
	Paths       []string
	Reason      string
}

func BuildFailureRecord(input FailureRecordInput, now time.Time) (recoverycontract.FailureRecord, error) {
	failureID := strings.TrimSpace(input.FailureID)
	if !recoverycontract.ValidFailureRecordComponent(failureID) {
		return recoverycontract.FailureRecord{}, fmt.Errorf("invalid recovery failure id %q", input.FailureID)
	}

	source := cleanSourceRun(input.SourceRun)
	if strings.TrimSpace(source.Lane) == "" {
		return recoverycontract.FailureRecord{}, fmt.Errorf("recovery source lane is empty")
	}

	if !recoverycontract.ValidFailureRecordComponent(source.SessionID) {
		return recoverycontract.FailureRecord{}, fmt.Errorf("invalid recovery session id %q", input.SourceRun.SessionID)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	stamp := now.UTC().Format(time.RFC3339Nano)
	failureClass := input.FailureClass

	if failureClass == "" {
		failureClass = recoverycontract.FailureClassUnknown
	}

	if !recoverycontract.KnownFailureClass(failureClass) {
		return recoverycontract.FailureRecord{}, fmt.Errorf("invalid recovery failure class %q", input.FailureClass)
	}

	partial := input.PartialState

	partial.ChangedPaths = cleanPathList(partial.ChangedPaths)

	if partial.DirtyStatus == "" {
		partial.DirtyStatus = recoverycontract.DirtyStatusNotChecked
	}

	if partial.ScopeStatus == "" {
		partial.ScopeStatus = scopeStatus(partial.ChangedPaths, source.RequiredEditPaths, source.AllowedEditPaths)
	}

	nextAction := recoverycontract.NextAction(strings.TrimSpace(string(input.NextAction)))
	if nextAction == "" {
		nextAction = nextActionForFailureClass(failureClass)
	}

	if _, ok := recoverycontract.ParseNextAction(string(nextAction)); !ok {
		return recoverycontract.FailureRecord{}, fmt.Errorf("invalid recovery next action %q", input.NextAction)
	}

	summary := sanitizeAndTruncate(input.FailureSummary, 2000)
	if summary == "" {
		summary = strings.TrimSpace(source.Lane) + " failed"
	}

	return recoverycontract.FailureRecord{
		SchemaVersion:  recoverycontract.FailureRecordSchemaVersion,
		FailureID:      failureID,
		CreatedAt:      stamp,
		UpdatedAt:      stamp,
		SourceRun:      source,
		LockedContract: cleanContractSnapshot(input.LockedContract, source),
		Failure: recoverycontract.Failure{
			Class:             failureClass,
			Kind:              strings.TrimSpace(input.FailureKind),
			Phase:             strings.TrimSpace(input.FailurePhase),
			Summary:           summary,
			EvidenceRefs:      cleanEvidenceRefs(input.EvidenceRefs),
			InvariantFailures: cleanInvariantFailures(input.InvariantFailures),
		},
		PartialState:   partial,
		Implementation: cleanImplementationState(input.Implementation),
		NextAction:     nextAction,
	}, nil
}

func BuildPlanExecuteFailureRecord(input PlanExecuteFailureInput, now time.Time) (recoverycontract.FailureRecord, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if !recoverycontract.ValidFailureRecordComponent(sessionID) {
		return recoverycontract.FailureRecord{}, fmt.Errorf("invalid plan-execute session id %q", input.SessionID)
	}

	failureClass := ClassifyPlanExecuteFailure(input.FailureKind, input.FailureReason, input.ReviewReasonClass)

	return BuildFailureRecord(FailureRecordInput{
		FailureID: PlanExecuteFailureID(sessionID),
		SourceRun: recoverycontract.SourceRun{
			Lane:              string(recoverycontract.SourceLanePlanExecute),
			SessionID:         sessionID,
			ProgressStatePath: cleanRelPath(input.ProgressStatePath),
			PromptSHA256:      strings.TrimSpace(input.PromptSHA256),
			PromptFile:        cleanRelPath(input.PromptFile),
			PlanPath:          cleanRelPath(input.PlanPath),
			PlanID:            strings.TrimSpace(input.PlanID),
			PackID:            strings.TrimSpace(input.PackID),
			ParentAtomID:      strings.TrimSpace(input.ParentAtomID),
			ParentAtomTitle:   strings.TrimSpace(input.ParentAtomTitle),
			RequiredEditPaths: cleanPathList(input.RequiredEditPaths),
			AllowedEditPaths:  cleanPathList(input.AllowedEditPaths),
		},
		LockedContract:    input.LockedContract,
		FailureClass:      failureClass,
		FailureKind:       input.FailureKind,
		FailurePhase:      input.Phase,
		FailureSummary:    failureSummary(input),
		EvidenceRefs:      planExecuteEvidenceRefs(input),
		InvariantFailures: input.InvariantFailures,
		PartialState: recoverycontract.PartialState{
			DirtyStatus:  recoverycontract.DirtyStatusNotChecked,
			ChangedPaths: cleanPathList(input.ChangedPaths),
			TotalEdits:   input.TotalEdits,
			ScopeStatus:  scopeStatus(input.ChangedPaths, input.RequiredEditPaths, input.AllowedEditPaths),
		},
		NextAction: nextActionForPlanExecuteFailure(input, failureClass),
	}, now)
}

func PartialStateForDirtyPaths(dirtyPaths []string, changedPaths []string, requiredPaths []string, allowedPaths []string, totalEdits int) recoverycontract.PartialState {
	changed := cleanPathList(changedPaths)
	dirty := cleanPathList(dirtyPaths)
	partial := recoverycontract.PartialState{
		DirtyStatus:  recoverycontract.DirtyStatusClean,
		ChangedPaths: changed,
		TotalEdits:   totalEdits,
		ScopeStatus:  scopeStatus(changed, requiredPaths, allowedPaths),
	}

	if len(dirty) == 0 {
		return partial
	}

	partial.ChangedPaths = cleanPathList(append(changed, dirty...))

	dirtyScope := scopeStatus(dirty, append(requiredPaths, changed...), allowedPaths)

	if dirtyScope == recoverycontract.ScopeStatusOutOfScope || dirtyScope == recoverycontract.ScopeStatusNotChecked {
		partial.DirtyStatus = recoverycontract.DirtyStatusDirtyOutScope
		partial.ScopeStatus = recoverycontract.ScopeStatusOutOfScope

		return partial
	}

	partial.DirtyStatus = recoverycontract.DirtyStatusDirtyInScope
	if partial.ScopeStatus != recoverycontract.ScopeStatusOutOfScope {
		partial.ScopeStatus = recoverycontract.ScopeStatusInScope
	}

	return partial
}

func NextActionForPartialState(record recoverycontract.FailureRecord, partial recoverycontract.PartialState) recoverycontract.NextAction {
	if partial.DirtyStatus == recoverycontract.DirtyStatusDirtyOutScope || partial.ScopeStatus == recoverycontract.ScopeStatusOutOfScope {
		return recoverycontract.NextActionHumanIntervention
	}

	return record.NextAction
}

func ClassifyPlanExecuteFailure(kind string, reason string, reviewReasonClass string) recoverycontract.FailureClass {
	reasonLower := strings.ToLower(reason)

	if failureKind, ok := plancontract.ParsePlanExecuteFailureKind(kind); ok {
		switch failureKind {
		case plancontract.PlanExecuteFailurePlannerFailed:
			if looksScopeTooLarge(reasonLower) {
				return recoverycontract.FailureClassScopeTooLarge
			}

			if looksBudgetExhausted(reasonLower) {
				return recoverycontract.FailureClassBudgetExhausted
			}

			if looksStaleContext(reasonLower) {
				return recoverycontract.FailureClassStaleContext
			}

			return recoverycontract.FailureClassPlannerFailed
		case plancontract.PlanExecuteFailurePlannerBlocked:
			return recoverycontract.FailureClassPlannerBlocked
		case plancontract.PlanExecuteFailurePlanApprovalDenied:
			return recoverycontract.FailureClassNeedsHumanAnswer
		case plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth,
			plancontract.PlanExecuteFailurePlanArtifactInvalid,
			plancontract.PlanExecuteFailureTerminalCompletionContractFailed,
			plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence,
			plancontract.PlanExecuteFailureMissingRequestedTestEdit,
			plancontract.PlanExecuteFailureMissingRequestedImplementationEdit:
			return recoverycontract.FailureClassPlanInvalid
		case plancontract.PlanExecuteFailureTerminalCompletionSaveFailed:
			return recoverycontract.FailureClassUnknown
		case plancontract.PlanExecuteFailurePlanArtifactMissing:
			return recoverycontract.FailureClassPlanMissing
		case plancontract.PlanExecuteFailureVerificationFailed,
			plancontract.PlanExecuteFailureSourceSanityFailed:
			return recoverycontract.FailureClassVerification
		case plancontract.PlanExecuteFailureExecutorFailed:
			if looksStaleContext(reasonLower) {
				return recoverycontract.FailureClassStaleContext
			}

			if looksVerificationFailed(reasonLower) {
				return recoverycontract.FailureClassVerification
			}

			if looksScopeTooLarge(reasonLower) {
				return recoverycontract.FailureClassScopeTooLarge
			}

			return recoverycontract.FailureClassExecutorEdit
		case plancontract.PlanExecuteFailureReviewRepairFailed:
			if normalized := normalizeReviewFailureClass(reviewReasonClass); normalized != "" {
				return normalized
			}

			return recoverycontract.FailureClassReviewRepair
		case plancontract.PlanExecuteFailureReviewRepairBlocked,
			plancontract.PlanExecuteFailureReviewModelError,
			plancontract.PlanExecuteFailureReviewRepairDegraded:
			if normalized := normalizeReviewFailureClass(reviewReasonClass); normalized != "" {
				return normalized
			}

			if looksRepairScopeContractMismatch(reasonLower) {
				return recoverycontract.FailureClassPlanInvalid
			}

			if looksScopeTooLarge(reasonLower) {
				return recoverycontract.FailureClassScopeTooLarge
			}

			return recoverycontract.FailureClassReviewRepair
		}
	}

	switch strings.TrimSpace(kind) {
	case "scope_too_large":
		if looksScopeTooLarge(reasonLower) {
			return recoverycontract.FailureClassScopeTooLarge
		}
		return recoverycontract.FailureClassScopeTooLarge
	case "dirty_workspace":
		return recoverycontract.FailureClassDirtyWorkspace
	case "unsafe_policy_block":
		return recoverycontract.FailureClassUnsafePolicyBlock
	}

	if looksBudgetExhausted(reasonLower) {
		return recoverycontract.FailureClassBudgetExhausted
	}

	if looksScopeTooLarge(reasonLower) {
		return recoverycontract.FailureClassScopeTooLarge
	}

	if looksVerificationFailed(reasonLower) {
		return recoverycontract.FailureClassVerification
	}

	if looksStaleContext(reasonLower) {
		return recoverycontract.FailureClassStaleContext
	}

	return recoverycontract.FailureClassUnknown
}

func ClassifyImplementFailure(summary string) recoverycontract.FailureClass {
	summaryLower := strings.ToLower(summary)

	switch {
	case looksBudgetExhausted(summaryLower):
		return recoverycontract.FailureClassBudgetExhausted
	case looksVerificationFailed(summaryLower):
		return recoverycontract.FailureClassVerification
	case looksStaleContext(summaryLower):
		return recoverycontract.FailureClassStaleContext
	case looksScopeTooLarge(summaryLower):
		return recoverycontract.FailureClassScopeTooLarge
	default:
		return recoverycontract.FailureClassExecutorEdit
	}
}

func WritePlanExecuteFailureRecord(root string, input PlanExecuteFailureInput, now time.Time) (recoverycontract.FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	record, err := BuildPlanExecuteFailureRecord(input, now)
	if err != nil {
		return recoverycontract.FailureRecord{}, "", err
	}

	return writeFailureRecord(root, record, now)
}

func WriteFailureRecord(root string, input FailureRecordInput, now time.Time) (recoverycontract.FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	record, err := BuildFailureRecord(input, now)
	if err != nil {
		return recoverycontract.FailureRecord{}, "", err
	}

	return writeFailureRecord(root, record, now)
}

func writeFailureRecord(root string, record recoverycontract.FailureRecord, now time.Time) (recoverycontract.FailureRecord, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	fullPath, relPath, err := FailureRecordPath(root, record.FailureID)
	if err != nil {
		return recoverycontract.FailureRecord{}, "", err
	}

	if err := ensureRecoveryWritePath(root, fullPath); err != nil {
		return recoverycontract.FailureRecord{}, "", fmt.Errorf("recovery failure record path rejected: %w", err)
	}

	if existing, _, found, err := TryLoadFailureRecord(root, record.FailureID); err == nil && found && strings.TrimSpace(existing.CreatedAt) != "" {
		record.CreatedAt = existing.CreatedAt
	}

	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return recoverycontract.FailureRecord{}, "", fmt.Errorf("mkdir recovery failure dir: %w", err)
	}

	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return recoverycontract.FailureRecord{}, "", fmt.Errorf("marshal recovery failure record: %w", err)
	}

	if err := atomicfile.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		return record, relPath, fmt.Errorf("write recovery failure record: %w", err)
	}

	return record, relPath, nil
}

func TryLoadFailureRecordForSession(root string, sessionID string) (recoverycontract.FailureRecord, string, bool, error) {
	for _, failureID := range FailureIDCandidatesForSession(sessionID) {
		record, path, found, err := TryLoadFailureRecord(root, failureID)
		if err != nil || found {
			return record, path, found, err
		}
	}

	return recoverycontract.FailureRecord{}, "", false, nil
}

func TryLoadFailureRecord(root string, failureID string) (recoverycontract.FailureRecord, string, bool, error) {
	fullPath, relPath, err := FailureRecordPath(root, failureID)
	if err != nil {
		return recoverycontract.FailureRecord{}, "", false, err
	}

	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return recoverycontract.FailureRecord{}, relPath, false, nil
	}

	if err != nil {
		return recoverycontract.FailureRecord{}, relPath, false, fmt.Errorf("read recovery failure record %s: %w", relPath, err)
	}

	var record recoverycontract.FailureRecord

	if err := json.Unmarshal(body, &record); err != nil {
		return recoverycontract.FailureRecord{}, relPath, false, fmt.Errorf("decode recovery failure record %s: %w", relPath, err)
	}

	if record.SchemaVersion != recoverycontract.FailureRecordSchemaVersion {
		return recoverycontract.FailureRecord{}, relPath, false, fmt.Errorf("unsupported recovery failure record schema %q in %s", record.SchemaVersion, relPath)
	}
	if err := recoverycontract.ValidateFailureRecord(record); err != nil {
		return recoverycontract.FailureRecord{}, relPath, false, fmt.Errorf("invalid recovery failure record %s: %w", relPath, err)
	}

	return record, relPath, true, nil
}

func FailureRecordPath(root string, failureID string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}

	failureID = strings.TrimSpace(failureID)
	if !recoverycontract.ValidFailureRecordComponent(failureID) {
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

func cleanSourceRun(source recoverycontract.SourceRun) recoverycontract.SourceRun {
	return recoverycontract.SourceRun{
		Lane:              strings.TrimSpace(source.Lane),
		SessionID:         strings.TrimSpace(source.SessionID),
		ProgressStatePath: cleanRelPath(source.ProgressStatePath),
		PromptSHA256:      strings.TrimSpace(source.PromptSHA256),
		PromptFile:        cleanRelPath(source.PromptFile),
		PlanPath:          cleanRelPath(source.PlanPath),
		PlanID:            strings.TrimSpace(source.PlanID),
		PackID:            strings.TrimSpace(source.PackID),
		ParentAtomID:      strings.TrimSpace(source.ParentAtomID),
		ParentAtomTitle:   strings.TrimSpace(source.ParentAtomTitle),
		RequiredEditPaths: cleanPathList(source.RequiredEditPaths),
		AllowedEditPaths:  cleanPathList(source.AllowedEditPaths),
	}
}

func cleanEvidenceRefs(refs []recoverycontract.EvidenceRef) []recoverycontract.EvidenceRef {
	var out []recoverycontract.EvidenceRef

	for _, ref := range refs {
		clean := recoverycontract.EvidenceRef{
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

func cleanImplementationState(state *recoverycontract.ImplementationState) *recoverycontract.ImplementationState {
	if state == nil {
		return nil
	}

	clean := recoverycontract.ImplementationState{
		Phase:                      strings.TrimSpace(state.Phase),
		StopReason:                 strings.TrimSpace(state.StopReason),
		RemainingRequiredEditPaths: cleanPathList(state.RemainingRequiredEditPaths),
		ChangedPaths:               cleanPathList(state.ChangedPaths),
		SourcedEvidencePaths:       cleanPathList(state.SourcedEvidencePaths),
		PendingRedirects:           cleanImplementationPendingRedirects(state.PendingRedirects),
		WriteReadyNudgeCount:       state.WriteReadyNudgeCount,
	}
	if clean.WriteReadyNudgeCount < 0 {
		clean.WriteReadyNudgeCount = 0
	}
	if !recoverycontract.ImplementationStatePresent(clean) {
		return nil
	}

	return &clean
}

func cleanImplementationPendingRedirects(redirects []recoverycontract.ImplementationPendingRedirect) []recoverycontract.ImplementationPendingRedirect {
	seen := map[string]bool{}

	var out []recoverycontract.ImplementationPendingRedirect
	for _, redirect := range redirects {
		clean := recoverycontract.ImplementationPendingRedirect{
			Path:        cleanRelPath(redirect.Path),
			PendingTool: strings.TrimSpace(redirect.PendingTool),
			Source:      strings.TrimSpace(redirect.Source),
			Detail:      sanitizeAndTruncate(redirect.Detail, 240),
		}
		if clean.Path == "" && clean.PendingTool == "" && clean.Source == "" && clean.Detail == "" {
			continue
		}

		key := strings.Join([]string{clean.Path, clean.PendingTool, clean.Source, clean.Detail}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, clean)
	}

	return out
}

func cleanContractSnapshot(snapshot *recoverycontract.ContractSnapshot, source recoverycontract.SourceRun) *recoverycontract.ContractSnapshot {
	if snapshot == nil {
		return nil
	}

	clean := recoverycontract.ContractSnapshot{
		ContractID:           strings.TrimSpace(snapshot.ContractID),
		ContractHash:         strings.TrimSpace(snapshot.ContractHash),
		SnapshotID:           strings.TrimSpace(snapshot.SnapshotID),
		SnapshotPath:         cleanRelPath(snapshot.SnapshotPath),
		RuntimeContractID:    strings.TrimSpace(snapshot.RuntimeContractID),
		ProtocolHash:         strings.TrimSpace(snapshot.ProtocolHash),
		RequiredEditPaths:    cleanPathList(snapshot.RequiredEditPaths),
		AllowedEditPaths:     cleanPathList(snapshot.AllowedEditPaths),
		WriteAllowedPaths:    cleanPathList(snapshot.WriteAllowedPaths),
		WriteAllowedExplicit: snapshot.WriteAllowedExplicit,
	}
	if len(clean.RequiredEditPaths) == 0 {
		clean.RequiredEditPaths = append([]string(nil), source.RequiredEditPaths...)
	}

	if len(clean.AllowedEditPaths) == 0 {
		clean.AllowedEditPaths = append([]string(nil), source.AllowedEditPaths...)
	}

	if contractSnapshotIsEmpty(clean) {
		return nil
	}

	return &clean
}

func contractSnapshotIsEmpty(snapshot recoverycontract.ContractSnapshot) bool {
	return snapshot.ContractID == "" &&
		snapshot.ContractHash == "" &&
		snapshot.SnapshotID == "" &&
		snapshot.SnapshotPath == "" &&
		snapshot.RuntimeContractID == "" &&
		snapshot.ProtocolHash == "" &&
		len(snapshot.RequiredEditPaths) == 0 &&
		len(snapshot.AllowedEditPaths) == 0 &&
		len(snapshot.WriteAllowedPaths) == 0 &&
		!snapshot.WriteAllowedExplicit
}

func contractSnapshotsEqual(a *recoverycontract.ContractSnapshot, b *recoverycontract.ContractSnapshot) bool {
	left := cleanContractSnapshot(a, recoverycontract.SourceRun{})
	right := cleanContractSnapshot(b, recoverycontract.SourceRun{})

	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.ContractID == right.ContractID &&
		left.ContractHash == right.ContractHash &&
		left.SnapshotID == right.SnapshotID &&
		left.SnapshotPath == right.SnapshotPath &&
		left.RuntimeContractID == right.RuntimeContractID &&
		left.ProtocolHash == right.ProtocolHash &&
		stringSlicesSameOrder(left.RequiredEditPaths, right.RequiredEditPaths) &&
		stringSlicesSameOrder(left.AllowedEditPaths, right.AllowedEditPaths) &&
		stringSlicesSameOrder(left.WriteAllowedPaths, right.WriteAllowedPaths) &&
		left.WriteAllowedExplicit == right.WriteAllowedExplicit
}

func cleanInvariantFailures(failures []recoverycontract.InvariantFailure) []recoverycontract.InvariantFailure {
	seen := map[string]bool{}

	var out []recoverycontract.InvariantFailure

	for _, failure := range failures {
		clean := recoverycontract.InvariantFailure{
			InvariantID:            strings.TrimSpace(failure.InvariantID),
			FieldPath:              strings.TrimSpace(failure.FieldPath),
			Path:                   cleanRelPath(failure.Path),
			Expected:               sanitizeAndTruncate(failure.Expected, 500),
			Actual:                 sanitizeAndTruncate(failure.Actual, 500),
			RequiredRepairEvidence: cleanRecoveryTextList(failure.RequiredRepairEvidence, 500),
		}
		if clean.InvariantID == "" {
			continue
		}

		key := strings.Join([]string{clean.InvariantID, clean.FieldPath, clean.Path, clean.Expected, clean.Actual, strings.Join(clean.RequiredRepairEvidence, "\x00")}, "\x00")
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, clean)
	}

	return out
}

func cleanRecoveryTextList(values []string, limit int) []string {
	seen := map[string]bool{}

	var out []string

	for _, value := range values {
		value = sanitizeAndTruncate(value, limit)
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		out = append(out, value)
	}

	return out
}

func stringSlicesSameOrder(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
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

func planExecuteEvidenceRefs(input PlanExecuteFailureInput) []recoverycontract.EvidenceRef {
	var refs []recoverycontract.EvidenceRef

	if path := cleanRelPath(input.ProgressStatePath); path != "" {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "progress_state", Path: path})
	}

	if path := cleanRelPath(input.PlanPath); path != "" {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "plan_artifact", Path: path})
	}

	for _, path := range cleanPathList(input.ChangedPaths) {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "changed_path", Path: path})
	}

	for _, failure := range input.ExecutorFailures {
		if failure = strings.TrimSpace(failure); failure != "" {
			refs = append(refs, recoverycontract.EvidenceRef{Kind: "executor_failure", Summary: sanitizeAndTruncate(failure, 500)})
		}
	}

	if status := strings.TrimSpace(input.ReviewStatus); status != "" {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "review_repair_status", ID: status})
	}

	if class := strings.TrimSpace(input.ReviewReasonClass); class != "" {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "review_repair_reason", ID: class})
	}

	return refs
}

func nextActionForFailureClass(failureClass recoverycontract.FailureClass) recoverycontract.NextAction {
	switch failureClass {
	case recoverycontract.FailureClassPlannerBlocked, recoverycontract.FailureClassNeedsHumanAnswer:
		return recoverycontract.NextActionAnswerBlocker
	case recoverycontract.FailureClassDirtyWorkspace, recoverycontract.FailureClassUnsafePolicyBlock:
		return recoverycontract.NextActionHumanIntervention
	case recoverycontract.FailureClassUnknown:
		return recoverycontract.NextActionInspectFailure
	default:
		return recoverycontract.NextActionReviewDecomposition
	}
}

func nextActionForPlanExecuteFailure(input PlanExecuteFailureInput, failureClass recoverycontract.FailureClass) recoverycontract.NextAction {
	if planExecutePlannerBlockedRequiredToolMissing(input.FailureKind, input.FailureReason) {
		return recoverycontract.NextActionRetryPlanExecute
	}

	if _, ok := PlanExecutePlannerBlockedSourceRefreshResult(input.FailureKind, input.FailureReason, input.RequiredEditPaths); ok {
		return recoverycontract.NextActionRetryPlanExecute
	}

	return nextActionForFailureClass(failureClass)
}

func planExecutePlannerBlockedRequiredToolMissing(kind string, reason string) bool {
	failureKind, ok := plancontract.ParsePlanExecuteFailureKind(kind)
	if !ok || failureKind != plancontract.PlanExecuteFailurePlannerBlocked {
		return false
	}

	text := strings.ToLower(reason)
	return strings.Contains(text, "submit_plan is not in the current allowed tool surface") ||
		strings.Contains(text, "submit_plan is not available because the current runtime contract exposes no tools")
}

func PlanExecutePlannerBlockedSourceRefreshResult(kind string, reason string, requiredPaths []string) (PlanExecutePlannerBlockedSourceRefresh, bool) {
	failureKind, ok := plancontract.ParsePlanExecuteFailureKind(kind)
	if !ok || failureKind != plancontract.PlanExecuteFailurePlannerBlocked || len(requiredPaths) == 0 {
		return PlanExecutePlannerBlockedSourceRefresh{}, false
	}

	text := strings.ToLower(reason)
	reasonClass := planExecutePlannerBlockedSourceRefreshReason(text)
	if reasonClass == "" {
		return PlanExecutePlannerBlockedSourceRefresh{}, false
	}

	var paths []string
	for _, path := range cleanPathList(requiredPaths) {
		if strings.Contains(text, strings.ToLower(path)) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		if reasonClass != "anchor_refresh" {
			return PlanExecutePlannerBlockedSourceRefresh{}, false
		}
		paths = cleanPathList(requiredPaths)
		if len(paths) == 0 {
			return PlanExecutePlannerBlockedSourceRefresh{}, false
		}
	}

	return PlanExecutePlannerBlockedSourceRefresh{
		ActionClass: recoverycontract.NextActionRetryPlanExecute,
		Path:        paths[0],
		Paths:       paths,
		Reason:      reasonClass,
	}, true
}

func PlanExecutePlannerBlockedRetryRefreshResult(kind string, reason string, requiredPaths []string, nextAction recoverycontract.NextAction) (PlanExecutePlannerBlockedSourceRefresh, bool) {
	if result, ok := PlanExecutePlannerBlockedSourceRefreshResult(kind, reason, requiredPaths); ok {
		return result, true
	}

	paths := cleanPathList(requiredPaths)
	failureKind, failureKindOK := plancontract.ParsePlanExecuteFailureKind(kind)
	parsedNextAction, nextActionOK := recoverycontract.ParseNextAction(string(nextAction))
	if !failureKindOK || failureKind != plancontract.PlanExecuteFailurePlannerBlocked ||
		!nextActionOK || parsedNextAction != recoverycontract.NextActionRetryPlanExecute ||
		len(paths) == 0 {
		return PlanExecutePlannerBlockedSourceRefresh{}, false
	}

	return PlanExecutePlannerBlockedSourceRefresh{
		ActionClass: recoverycontract.NextActionRetryPlanExecute,
		Path:        paths[0],
		Paths:       paths,
		Reason:      "retry_action",
	}, true
}

func PlanExecuteSourceRefreshRetryInstruction(refresh PlanExecutePlannerBlockedSourceRefresh, requiredPaths []string) string {
	refreshPaths := cleanPathList(refresh.Paths)
	if len(refreshPaths) == 0 {
		if path := strings.TrimSpace(refresh.Path); path != "" {
			refreshPaths = []string{path}
		}
	}
	if len(refreshPaths) == 0 {
		return ""
	}

	requiredPaths = cleanPathList(requiredPaths)

	var b strings.Builder
	if len(refreshPaths) == 1 {
		fmt.Fprintf(&b, "First read source for `%s` using `code_navigate` `read_symbol` or a bounded `read_file` range", refreshPaths[0])
	} else {
		fmt.Fprintf(&b, "First refresh source-body evidence for each candidate anchor path (%s), one bounded `read_file` range or `code_navigate` `read_symbol`/`extract_body` request at a time", strings.Join(refreshPaths, ", "))
	}
	if len(requiredPaths) > 0 {
		fmt.Fprintf(&b, "; then satisfy runtime-required git history evidence for every existing required path listed above (%s); then produce one valid `submit_plan` call whose edits include at least one real edit for each required path, with no outside paths. Do not treat a source read or history read as plan coverage.", strings.Join(requiredPaths, ", "))
	} else {
		b.WriteString("; then satisfy any runtime-required git history evidence for selected existing edit paths; then produce one valid `submit_plan` call grounded in the objective and target references. Do not treat a source read or history read as plan coverage.")
	}

	return b.String()
}

func planExecutePlannerBlockedSourceRefreshReason(text string) string {
	for _, marker := range []string{"plan-execute anchor refresh is still required", "remaining bounded refresh paths", "bounded read_file or code_navigate source-body request", "bounded submit_plan anchor refresh required", "bounded source-body refresh remains required", "corrected plan still needs bounded source-body refresh"} {
		if strings.Contains(text, marker) {
			return "anchor_refresh"
		}
	}

	for _, marker := range []string{"required edit path source read required", "not every required path has source evidence", "before attempting to submit_plan"} {
		if strings.Contains(text, marker) {
			return "source_read"
		}
	}

	return ""
}

func normalizeReviewFailureClass(value string) recoverycontract.FailureClass {
	switch strings.TrimSpace(value) {
	case "stale_context":
		return recoverycontract.FailureClassStaleContext
	case "budget_exhausted":
		return recoverycontract.FailureClassBudgetExhausted
	case "needs_human_answer":
		return recoverycontract.FailureClassNeedsHumanAnswer
	case "unsafe_policy_block":
		return recoverycontract.FailureClassUnsafePolicyBlock
	case "scope_too_large":
		return recoverycontract.FailureClassScopeTooLarge
	case "verification_failed":
		return recoverycontract.FailureClassVerification
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

func looksRepairScopeContractMismatch(reasonLower string) bool {
	return strings.Contains(reasonLower, "outside the allowed repair scope") ||
		strings.Contains(reasonLower, "outside allowed repair scope") ||
		strings.Contains(reasonLower, "not in allowed repair paths") ||
		strings.Contains(reasonLower, "not in the allowed repair paths")
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

func scopeStatus(changedPaths []string, requiredPaths []string, allowedPaths []string) recoverycontract.ScopeStatus {
	changed := cleanPathList(changedPaths)
	if len(changed) == 0 {
		return recoverycontract.ScopeStatusNone
	}

	required := cleanPathList(requiredPaths)
	allowed := cleanPathList(allowedPaths)
	if len(required) == 0 && len(allowed) == 0 {
		return recoverycontract.ScopeStatusNotChecked
	}

	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      required,
		AllowedCandidatePaths:  allowed,
		CandidateCoveragePaths: changed,
		Source:                 "recovery_scope_status",
	})
	if err != nil {
		return legacyScopeStatus(changed, required, allowed)
	}

	covered := make(map[string]bool, len(coverage.CandidateCoverage))
	for _, candidate := range coverage.CandidateCoverage {
		path := cleanRelPath(firstNonEmpty(candidate.Path, candidate.OriginalPath))
		if path == "" {
			continue
		}

		covered[path] = candidate.SatisfiesRequiredCoverage || candidate.WithinAllowedCandidatePath
	}

	for _, path := range changed {
		normalized, ok := recoveryScopeCoveragePath(path, coverage)
		if !ok {
			return recoverycontract.ScopeStatusOutOfScope
		}
		if !covered[normalized] {
			return recoverycontract.ScopeStatusOutOfScope
		}
	}

	return recoverycontract.ScopeStatusInScope
}

func recoveryScopeCoveragePath(path string, coverage autonomy.PlanScopePathRoleObligationCoverage) (string, bool) {
	path = cleanRelPath(path)
	if path == "" {
		return "", false
	}

	for _, candidate := range coverage.CandidateCoverage {
		if cleanRelPath(candidate.OriginalPath) == path {
			if normalized := cleanRelPath(candidate.Path); normalized != "" {
				return normalized, true
			}
		}
		if cleanRelPath(candidate.Path) == path {
			return path, true
		}
	}

	return path, false
}

func legacyScopeStatus(changed []string, required []string, allowedPaths []string) recoverycontract.ScopeStatus {
	allowed := map[string]bool{}
	for _, path := range required {
		allowed[path] = true
	}

	for _, path := range allowedPaths {
		allowed[path] = true
	}

	if len(allowed) == 0 {
		return recoverycontract.ScopeStatusNotChecked
	}

	for _, path := range changed {
		if !allowed[path] {
			return recoverycontract.ScopeStatusOutOfScope
		}
	}

	return recoverycontract.ScopeStatusInScope
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func cleanPathList(paths []string) []string {
	seen := map[string]bool{}

	var out []string

	for _, path := range paths {
		path = plancontract.NormalizeScopePath(path)
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

	path = strings.ReplaceAll(path, "\\", "/")

	return filepath.ToSlash(filepath.Clean(path))
}

func sanitizeAndTruncate(value string, limit int) string {
	return textutil.Truncate(strings.TrimSpace(RedactText(value)), limit)
}
