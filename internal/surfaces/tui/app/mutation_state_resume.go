package app

import (
	"encoding/json"
	"fmt"
	"strings"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/evidence/transcript"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/recovery"
)

const mutationStateResumeEventRole = "mutation_state_resume_validation"

type mutationStateResumeAudit struct {
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	SessionID          string `json:"session_id,omitempty"`
	FailureID          string `json:"failure_id,omitempty"`
	RecordPath         string `json:"record_path,omitempty"`
	ScopePresent       bool   `json:"scope_present"`
	CommitReceiptCount int    `json:"commit_receipt_count"`
}

func (m *Model) loadMutationStateForResume(sessionPath string) (*recoverycontract.ImplementationState, mutationStateResumeAudit, error) {
	sessionID := strings.TrimSpace(sessionlog.SessionIDFromPath(sessionPath))
	audit := mutationStateResumeAudit{Status: "missing", SessionID: sessionID}
	if sessionID == "" {
		audit.Status = "rejected"
		audit.Reason = "session_id_missing"
		return nil, audit, fmt.Errorf("restore durable mutation state: resumed session id is empty")
	}

	root := strings.TrimSpace(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace))
	if root == "" {
		audit.Reason = "workspace_root_unavailable"
		return nil, audit, nil
	}

	record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
	audit.RecordPath = strings.TrimSpace(recordPath)
	if err != nil {
		audit.Status = "rejected"
		audit.Reason = "record_invalid"
		return nil, audit, fmt.Errorf("restore durable mutation state for session %q: %w", sessionID, err)
	}
	if !found {
		audit.Reason = "failure_record_not_found"
		return nil, audit, nil
	}

	audit.FailureID = strings.TrimSpace(record.FailureID)
	if strings.TrimSpace(record.SourceRun.SessionID) != sessionID {
		audit.Status = "rejected"
		audit.Reason = "session_mismatch"
		return nil, audit, fmt.Errorf(
			"restore durable mutation state: failure record session %q does not match resumed session %q",
			record.SourceRun.SessionID,
			sessionID,
		)
	}

	implementation := record.Implementation
	if implementation == nil || (implementation.CanonicalMutationScope == nil && len(implementation.CommitReceipts) == 0) {
		audit.Reason = "durable_mutation_state_not_recorded"
		return nil, audit, nil
	}
	audit.Status = "loaded"
	audit.Reason = "failure_record_validated"
	audit.ScopePresent = implementation.CanonicalMutationScope != nil
	audit.CommitReceiptCount = len(implementation.CommitReceipts)
	return cloneRecoveryImplementationState(implementation), audit, nil
}

func (m *Model) emitMutationStateResumeAudit(audit mutationStateResumeAudit) {
	if m == nil {
		return
	}
	if m.eventStream == nil && m.nonInteractive {
		pending := audit
		m.pendingMutationStateResumeAudit = &pending
	}
	emitMutationStateResumeAudit(m.eventStream, m.debugLogger, audit)
}

func (m *promptExecutionSession) emitMutationStateResumeAudit(audit mutationStateResumeAudit) {
	if m == nil {
		return
	}
	body, err := json.Marshal(audit)
	if err != nil {
		return
	}
	m.logRunEvent(agent.RunEvent{Role: mutationStateResumeEventRole, Content: string(body)})
}

func emitMutationStateResumeAudit(stream *jsonEventStream, logger *transcript.DebugLogger, audit mutationStateResumeAudit) {
	body, err := json.Marshal(audit)
	if err != nil {
		return
	}
	if stream != nil {
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			payload["type"] = mutationStateResumeEventRole
			stream.emit(payload)
		}
	}
	if logger != nil {
		_ = logger.LogRunEvent(mutationStateResumeEventRole, string(body))
	}
}

func cloneRecoveryImplementationState(state *recoverycontract.ImplementationState) *recoverycontract.ImplementationState {
	if state == nil {
		return nil
	}

	cloned := *state
	cloned.RemainingRequiredEditPaths = append([]string(nil), state.RemainingRequiredEditPaths...)
	cloned.MissingRequiredEditSnippetIDs = append([]string(nil), state.MissingRequiredEditSnippetIDs...)
	cloned.MissingRequiredEditSnippetPaths = append([]string(nil), state.MissingRequiredEditSnippetPaths...)
	cloned.UnresolvedRejectedMutationPaths = append([]string(nil), state.UnresolvedRejectedMutationPaths...)
	cloned.OpenTaskLedgerObligationIDs = append([]string(nil), state.OpenTaskLedgerObligationIDs...)
	cloned.BlockedTaskLedgerItems = make([]recoverycontract.ImplementationBlockedTaskItem, len(state.BlockedTaskLedgerItems))
	for index, item := range state.BlockedTaskLedgerItems {
		item.Paths = append([]string(nil), item.Paths...)
		cloned.BlockedTaskLedgerItems[index] = item
	}
	cloned.ChangedPaths = append([]string(nil), state.ChangedPaths...)
	cloned.SourceSatisfiedPaths = append([]string(nil), state.SourceSatisfiedPaths...)
	cloned.SourcedEvidencePaths = append([]string(nil), state.SourcedEvidencePaths...)
	cloned.CanonicalMutationScope = cloneRecoveryCanonicalMutationScope(state.CanonicalMutationScope)
	cloned.CommitReceipts = make([]recoverycontract.MutationCommitProjection, len(state.CommitReceipts))
	for index, receipt := range state.CommitReceipts {
		receipt.Scope = cloneRecoveryCanonicalMutationScope(receipt.Scope)
		receipt.Effects = append([]recoverycontract.MutationCommitEffectProjection(nil), receipt.Effects...)
		receipt.PostimageFileIDs = append([]string(nil), receipt.PostimageFileIDs...)
		receipt.AffectedFileIDs = append([]string(nil), receipt.AffectedFileIDs...)
		receipt.FailedTargets = append([]recoverycontract.MutationTargetProjection(nil), receipt.FailedTargets...)
		receipt.UnattemptedTargets = append([]recoverycontract.MutationTargetProjection(nil), receipt.UnattemptedTargets...)
		cloned.CommitReceipts[index] = receipt
	}

	return &cloned
}

func cloneRecoveryCanonicalMutationScope(scope *recoverycontract.CanonicalMutationScopeProjection) *recoverycontract.CanonicalMutationScopeProjection {
	if scope == nil {
		return nil
	}

	cloned := *scope
	cloned.RequiredCoverageIDs = append([]string(nil), scope.RequiredCoverageIDs...)
	cloned.Rules = append([]recoverycontract.CanonicalMutationScopeRuleProjection(nil), scope.Rules...)
	cloned.Presentations = append([]recoverycontract.CanonicalMutationScopePresentationProjection(nil), scope.Presentations...)
	return &cloned
}
