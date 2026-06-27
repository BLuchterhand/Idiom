package contractcompile

type TurnPhaseID string

const (
	TurnPhaseRequiredSource              TurnPhaseID = "plan_execute.required_source"
	TurnPhaseCandidateSource             TurnPhaseID = "plan_execute.candidate_source"
	TurnPhaseHistoryPending              TurnPhaseID = "plan_execute.history_pending"
	TurnPhaseSubmitReadyNativeRequired   TurnPhaseID = "plan_execute.submit_ready.native_required"
	TurnPhaseSubmitReadyTextual          TurnPhaseID = "plan_execute.submit_ready.textual"
	TurnPhaseSourceSatisfiedTerminal     TurnPhaseID = "plan_execute.source_satisfied_terminal"
	TurnPhaseMalformedSubmitPlanRecovery TurnPhaseID = "plan_execute.malformed_submit_plan_recovery"
	TurnPhaseStaleAnchorRefresh          TurnPhaseID = "plan_execute.stale_anchor_refresh"
	TurnPhaseReviewRepairTerminal        TurnPhaseID = "plan_execute.review_repair_terminal"
	TurnPhaseImplementationTargetSource  TurnPhaseID = "implementation.target_source"
	TurnPhaseImplementationWriteReady    TurnPhaseID = "implementation.write_ready"
	TurnPhaseImplementationVerifyReady   TurnPhaseID = "implementation.verify_ready"
	TurnPhaseProductAcceptanceCloseout   TurnPhaseID = "product.acceptance_closeout"
	TurnPhaseArtifactFinalAnswer         TurnPhaseID = "artifact.final_answer"
	TurnPhaseGeneric                     TurnPhaseID = "generic"
)

func TurnPhaseIDs() []TurnPhaseID {
	return []TurnPhaseID{
		TurnPhaseRequiredSource,
		TurnPhaseCandidateSource,
		TurnPhaseHistoryPending,
		TurnPhaseSubmitReadyNativeRequired,
		TurnPhaseSubmitReadyTextual,
		TurnPhaseSourceSatisfiedTerminal,
		TurnPhaseMalformedSubmitPlanRecovery,
		TurnPhaseStaleAnchorRefresh,
		TurnPhaseReviewRepairTerminal,
		TurnPhaseImplementationTargetSource,
		TurnPhaseImplementationWriteReady,
		TurnPhaseImplementationVerifyReady,
		TurnPhaseProductAcceptanceCloseout,
		TurnPhaseArtifactFinalAnswer,
		TurnPhaseGeneric,
	}
}

func KnownTurnPhaseID(value TurnPhaseID) bool {
	for _, phaseID := range TurnPhaseIDs() {
		if value == phaseID {
			return true
		}
	}
	return false
}
