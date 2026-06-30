package recovery

func ImplementationStatePresent(state ImplementationState) bool {
	return state.Phase != "" ||
		state.StopReason != "" ||
		len(state.RemainingRequiredEditPaths) > 0 ||
		len(state.ChangedPaths) > 0 ||
		len(state.SourcedEvidencePaths) > 0 ||
		len(state.PendingRedirects) > 0 ||
		state.WriteReadyNudgeCount > 0
}
