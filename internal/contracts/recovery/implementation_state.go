package recovery

func ImplementationStatePresent(state ImplementationState) bool {
	return state.Phase != "" ||
		state.StopReason != "" ||
		len(state.RemainingRequiredEditPaths) > 0 ||
		len(state.MissingRequiredEditSnippetIDs) > 0 ||
		len(state.MissingRequiredEditSnippetPaths) > 0 ||
		len(state.UnresolvedRejectedMutationPaths) > 0 ||
		len(state.OpenTaskLedgerObligationIDs) > 0 ||
		len(state.BlockedTaskLedgerItems) > 0 ||
		state.TerminalDisposition != "" ||
		len(state.SourceSatisfiedPaths) > 0 ||
		len(state.ChangedPaths) > 0 ||
		len(state.SourcedEvidencePaths) > 0 ||
		len(state.SourceWindows) > 0 ||
		state.VerificationPending ||
		state.VerificationFailed ||
		state.SemanticVerificationPending ||
		state.WriteReadyNudgeCount > 0 ||
		state.CanonicalMutationScope != nil ||
		len(state.CommitReceipts) > 0 ||
		state.CompletionRecovery != nil
}
