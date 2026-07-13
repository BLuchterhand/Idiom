package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/capabilities/workspace"
	contractcompile "idiom/internal/contracts/compile"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
)

func (r *runExecution) implementationLifecycleSnapshot(results []workspace.ToolResult, stopReason string, writeReadyNudges int, currentAllowedTools []string, acceptedTerminalAnswer string) (recoverycontract.ImplementationState, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return recoverycontract.ImplementationState{}, false
	}

	completionState := r.implementationCompletionState(results)
	if strings.TrimSpace(acceptedTerminalAnswer) != "" {
		terminalState := r.implementationCompletionStateForTerminalAnswer(acceptedTerminalAnswer, results)
		if terminalState.CloseoutReady() {
			completionState = terminalState
		}
	}
	canonicalScope, commitReceipts := r.state.durableMutationState()
	snapshot := recoverycontract.ImplementationState{
		Phase:                           r.implementationLifecyclePhase(results, currentAllowedTools),
		StopReason:                      strings.TrimSpace(stopReason),
		RemainingRequiredEditPaths:      append([]string(nil), completionState.MissingRequiredPaths...),
		MissingRequiredEditSnippetIDs:   append([]string(nil), completionState.MissingRequiredSnippetIDs...),
		MissingRequiredEditSnippetPaths: append([]string(nil), completionState.MissingRequiredSnippetPaths...),
		UnresolvedRejectedMutationPaths: append([]string(nil), completionState.UnresolvedRejectedMutationPaths...),
		OpenTaskLedgerObligationIDs:     append([]string(nil), completionState.OpenTaskLedgerObligationIDs...),
		BlockedTaskLedgerItems:          implementationLifecycleBlockedTaskItems(completionState.BlockedTaskLedgerItems),
		TerminalDisposition:             string(completionState.TerminalDisposition),
		SourceSatisfiedPaths:            append([]string(nil), completionState.SourceSatisfiedPaths...),
		ChangedPaths:                    append([]string(nil), completionState.ChangedPaths...),
		SourcedEvidencePaths:            uniqueSortedRunnerPaths(newToolResultEvidenceProjection(results).SourceWindowPaths()),
		SourceWindows:                   implementationLifecycleSourceWindows(results),
		VerificationPending:             completionState.VerificationPending,
		VerificationFailed:              completionState.VerificationFailed,
		SemanticVerificationPending:     completionState.SemanticVerificationPending,
		WriteReadyNudgeCount:            writeReadyNudges,
		CanonicalMutationScope:          canonicalScope,
		CommitReceipts:                  commitReceipts,
		CompletionRecovery:              implementationLifecycleCompletionRecovery(r.state.completionGateRecovery),
	}

	return snapshot, recoverycontract.ImplementationStatePresent(snapshot)
}

func implementationLifecycleSourceWindows(results []workspace.ToolResult) []toolfacts.SourceWindowEvidence {
	var windows []toolfacts.SourceWindowEvidence
	for _, result := range results {
		if result.Facts == nil {
			continue
		}
		windows = append(windows, result.Facts.SourceWindows...)
	}
	return toolfacts.NormalizeSourceWindows(windows)
}

func implementationLifecycleCompletionRecovery(state completionGateRecoveryState) *recoverycontract.ImplementationCompletionRecovery {
	if state.TotalAttempts == 0 && !state.hasActiveViolation() {
		return nil
	}
	projection := &recoverycontract.ImplementationCompletionRecovery{TotalAttempts: state.TotalAttempts}
	if !state.hasActiveViolation() {
		return projection
	}
	active := state.ActiveViolation
	projection.ActiveViolation = &recoverycontract.ImplementationCompletionActiveViolation{
		InvariantID:                  active.InvariantID,
		ViolationID:                  active.ViolationID,
		Class:                        string(active.Class),
		PendingSourceBodyPaths:       append([]string(nil), active.PendingSourceBodyPaths...),
		PendingSourceBodyReferences:  implementationLifecycleSourceBodyReferences(active.PendingSourceBodyReferences),
		PendingContractPaths:         append([]string(nil), active.PendingContractPaths...),
		PendingObligationIDs:         append([]string(nil), active.PendingObligationIDs...),
		AcceptedVerificationCommands: append([]string(nil), active.AcceptedVerificationCommands...),
		AttemptsSinceProgress:        active.AttemptsSinceProgress,
	}
	return projection
}

func implementationLifecycleSourceBodyReferences(references []sourceReference) []recoverycontract.ImplementationSourceBodyReference {
	if len(references) == 0 {
		return nil
	}
	out := make([]recoverycontract.ImplementationSourceBodyReference, 0, len(references))
	for _, reference := range normalizeSourceReferences(references) {
		projected := recoverycontract.ImplementationSourceBodyReference{Path: reference.Path}
		if reference.LineRange != nil {
			projected.StartLine = reference.LineRange.Start
			projected.EndLine = reference.LineRange.End
		}
		out = append(out, projected)
	}
	return out
}

func (r *runExecution) emitCompletionGateRecoveryState() {
	if r == nil || r.runner.OnEvent == nil {
		return
	}
	recovery := implementationLifecycleCompletionRecovery(r.state.completionGateRecovery)
	if recovery == nil {
		return
	}
	body, err := json.Marshal(map[string]any{"completion_recovery": recovery})
	if err != nil {
		return
	}
	r.emitEvent(RunEvent{Role: "completion_gate_recovery_state", Content: string(body)})
}

func implementationLifecycleBlockedTaskItems(items []implementationBlockedTaskLedgerItem) []recoverycontract.ImplementationBlockedTaskItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]recoverycontract.ImplementationBlockedTaskItem, 0, len(items))
	for _, item := range items {
		out = append(out, recoverycontract.ImplementationBlockedTaskItem{
			ID:               item.ID,
			Kind:             string(item.Kind),
			SubjectNamespace: string(item.SubjectNamespace),
			SubjectID:        item.SubjectID,
			Reason:           item.Reason,
			Paths:            append([]string(nil), item.Paths...),
		})
	}
	return out
}

func (r *runExecution) implementationLifecyclePhase(results []workspace.ToolResult, currentAllowedTools []string) string {
	if r == nil {
		return ""
	}

	completionState := r.implementationCompletionState(results)
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    r.state.runtimeBrief,
		Results:                  results,
		ImplementationCompletion: &completionState,
		mutationAuthority:        r.planThenExecutePathAuthority(),
		InspectionAuthority:      r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()),
		BaseAllowedTools:         runnerCurrentAllowedOrBriefTools(currentAllowedTools, r.state.runtimeBrief.AllowedTools),
	})
	if strings.TrimSpace(string(contract.PhaseID)) == "" || contract.PhaseID == contractcompile.TurnPhaseGeneric {
		return ""
	}

	return strings.TrimSpace(string(contract.PhaseID))
}

func (r *runExecution) emitImplementationLifecycleState(results []workspace.ToolResult, stopReason string, writeReadyNudges int, currentAllowedTools []string, acceptedTerminalAnswer string) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	snapshot, ok := r.implementationLifecycleSnapshot(results, stopReason, writeReadyNudges, currentAllowedTools, acceptedTerminalAnswer)
	if !ok {
		return
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "implementation_lifecycle_state", Content: string(body)})
}

func (r *runExecution) attachImplementationLifecycleStatus(payload *runnerCompletionStatus, results []workspace.ToolResult, stopReason string, writeReadyNudges int, currentAllowedTools []string) {
	if payload == nil {
		return
	}

	snapshot, ok := r.implementationLifecycleSnapshot(results, stopReason, writeReadyNudges, currentAllowedTools, "")
	if !ok {
		return
	}

	payload.StopReason = snapshot.StopReason
	payload.Phase = snapshot.Phase
	payload.RemainingRequiredEditPaths = snapshot.RemainingRequiredEditPaths
	payload.MissingRequiredEditSnippetIDs = snapshot.MissingRequiredEditSnippetIDs
	payload.MissingRequiredEditSnippetPaths = snapshot.MissingRequiredEditSnippetPaths
	payload.UnresolvedRejectedMutationPaths = snapshot.UnresolvedRejectedMutationPaths
	payload.OpenTaskLedgerObligationIDs = snapshot.OpenTaskLedgerObligationIDs
	payload.BlockedTaskLedgerItems = snapshot.BlockedTaskLedgerItems
	payload.TerminalDisposition = snapshot.TerminalDisposition
	payload.SourceSatisfiedPaths = snapshot.SourceSatisfiedPaths
	payload.ChangedPaths = snapshot.ChangedPaths
	payload.SourcedEvidencePaths = snapshot.SourcedEvidencePaths
	payload.SourceWindows = snapshot.SourceWindows
	payload.VerificationPending = snapshot.VerificationPending
	payload.VerificationFailed = snapshot.VerificationFailed
	payload.SemanticVerificationPending = snapshot.SemanticVerificationPending
	payload.WriteReadyNudgeCount = snapshot.WriteReadyNudgeCount
	payload.CanonicalMutationScope = snapshot.CanonicalMutationScope
	payload.CommitReceipts = snapshot.CommitReceipts
	payload.CompletionRecovery = snapshot.CompletionRecovery
}
