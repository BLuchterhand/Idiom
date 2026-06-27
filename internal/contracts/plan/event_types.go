package plan

import "strings"

// PlanExecuteEventType is the unprefixed plan-execute event vocabulary.
type PlanExecuteEventType string

const (
	// PlanExecuteEventPrefix is prepended to event types emitted on the JSON event stream.
	PlanExecuteEventPrefix = "idiom.plan_execute."
	// PlanExecuteInvalidEventSuffix marks invalid-attempt events derived from a base event type.
	PlanExecuteInvalidEventSuffix                                         = "_invalid"
	PlanExecuteEventAutoExactPlanPartiallySatisfied  PlanExecuteEventType = "auto_exact_plan_partially_satisfied"
	PlanExecuteEventAutoExactPlanSkipped             PlanExecuteEventType = "auto_exact_plan_skipped"
	PlanExecuteEventAutoExactPlanSourceSatisfied     PlanExecuteEventType = "auto_exact_plan_source_satisfied"
	PlanExecuteEventAutoExactPlanSubmitted           PlanExecuteEventType = "auto_exact_plan_submitted"
	PlanExecuteEventBlocked                          PlanExecuteEventType = "blocked"
	PlanExecuteEventCompleted                        PlanExecuteEventType = "completed"
	PlanExecuteEventExecutorComplete                 PlanExecuteEventType = "executor_complete"
	PlanExecuteEventExecutorEdit                     PlanExecuteEventType = "executor_edit"
	PlanExecuteEventExecutorHandoffPacket            PlanExecuteEventType = "executor_handoff_packet"
	PlanExecuteEventExecutorInheritsPack             PlanExecuteEventType = "executor_inherits_pack"
	PlanExecuteEventExecutorPackMiss                 PlanExecuteEventType = "executor_pack_miss"
	PlanExecuteEventExecutorPacketOverridden         PlanExecuteEventType = "executor_packet_overridden"
	PlanExecuteEventExecutorPacketStale              PlanExecuteEventType = "executor_packet_stale"
	PlanExecuteEventExecutorStart                    PlanExecuteEventType = "executor_start"
	PlanExecuteEventExecutorVerificationRepairNeeded PlanExecuteEventType = "executor_verification_repair_required"
	PlanExecuteEventParentChildInvocation            PlanExecuteEventType = "parent_child_invocation"
	PlanExecuteEventChildTerminal                    PlanExecuteEventType = "child_terminal"
	PlanExecuteEventChildAcceptance                  PlanExecuteEventType = "child_acceptance"
	PlanExecuteEventCandidatePatchArtifact           PlanExecuteEventType = "candidate_patch_artifact"
	PlanExecuteEventPlanArtifactInvalid              PlanExecuteEventType = "plan_artifact_invalid"
	PlanExecuteEventPlanArtifactLoaded               PlanExecuteEventType = "plan_artifact_loaded"
	PlanExecuteEventPlanArtifactMissing              PlanExecuteEventType = "plan_artifact_missing"
	PlanExecuteEventPlanArtifactPackInheritFailed    PlanExecuteEventType = "plan_artifact_pack_inherit_persist_failed"
	PlanExecuteEventPlanArtifactPackInherited        PlanExecuteEventType = "plan_artifact_pack_inherited"
	PlanExecuteEventPlanScopeDrift                   PlanExecuteEventType = "plan_scope_drift"
	PlanExecuteEventPlannerBlocked                   PlanExecuteEventType = "planner_blocked"
	PlanExecuteEventPlannerPlanRejected              PlanExecuteEventType = "planner_plan_rejected"
	PlanExecuteEventPreflightFailed                  PlanExecuteEventType = "preflight_failed"
	PlanExecuteEventPreflightOK                      PlanExecuteEventType = "preflight_ok"
	PlanExecuteEventRecoveryDecompositionFailed      PlanExecuteEventType = "recovery_decomposition_packet_failed"
	PlanExecuteEventRecoveryDecompositionWritten     PlanExecuteEventType = "recovery_decomposition_packet_written"
	PlanExecuteEventRecoveryFailureRecordFailed      PlanExecuteEventType = "recovery_failure_record_failed"
	PlanExecuteEventRecoveryFailureRecordWritten     PlanExecuteEventType = "recovery_failure_record_written"
	PlanExecuteEventRequiredEditPathsResolved        PlanExecuteEventType = "required_edit_paths_resolved"
	PlanExecuteEventResumeStateSaved                 PlanExecuteEventType = "resume_state_saved"
	PlanExecuteEventResumeStateLoaded                PlanExecuteEventType = "resume_state_loaded"
	PlanExecuteEventReviewRepairAutoPatchHygiene     PlanExecuteEventType = "review_repair_auto_patch_hygiene"
	PlanExecuteEventReviewRepairAutoPatchFailed      PlanExecuteEventType = "review_repair_auto_patch_hygiene_failed"
	PlanExecuteEventReviewRepairBlocked              PlanExecuteEventType = "review_repair_blocked"
	PlanExecuteEventReviewRepairComplete             PlanExecuteEventType = "review_repair_complete"
	PlanExecuteEventReviewRepairDegraded             PlanExecuteEventType = "review_repair_degraded"
	PlanExecuteEventReviewRepairFailed               PlanExecuteEventType = "review_repair_failed"
	PlanExecuteEventReviewRepairPassRejected         PlanExecuteEventType = "review_repair_pass_rejected"
	PlanExecuteEventReviewRepairPlanRejected         PlanExecuteEventType = "review_repair_plan_rejected"
	PlanExecuteEventReviewRepairRetry                PlanExecuteEventType = "review_repair_retry"
	PlanExecuteEventReviewRepairStart                PlanExecuteEventType = "review_repair_start"
	PlanExecuteEventSourceSanity                     PlanExecuteEventType = "source_sanity"
	PlanExecuteEventSourceSatisfied                  PlanExecuteEventType = "source_satisfied"
	PlanExecuteEventSourceSatisfiedRejected          PlanExecuteEventType = "source_satisfied_rejected"
	PlanExecuteEventTerminalCompletion               PlanExecuteEventType = "terminal_completion"
	PlanExecuteEventTerminalCompletionSaveFailed     PlanExecuteEventType = "terminal_completion_save_failed"
	PlanExecuteEventVerification                     PlanExecuteEventType = "verification"
	PlanExecuteEventVerificationAcceptance           PlanExecuteEventType = "verification_acceptance"
	PlanExecuteEventWriterRouteDecision              PlanExecuteEventType = "writer_route_decision"
)

// PlanExecuteTerminalCompletionRecordType is the fully qualified terminal
// completion event record type.
const PlanExecuteTerminalCompletionRecordType = PlanExecuteEventPrefix + string(PlanExecuteEventTerminalCompletion)

// PlanExecuteEventName returns the fully qualified JSON event name for eventType.
func PlanExecuteEventName(eventType PlanExecuteEventType) string {
	return PlanExecuteEventPrefix + string(eventType)
}

// PlanExecuteInvalidEventName returns the fully qualified invalid-attempt event name for eventType.
func PlanExecuteInvalidEventName(eventType PlanExecuteEventType) string {
	return PlanExecuteEventName(eventType) + PlanExecuteInvalidEventSuffix
}

// KnownPlanExecuteEventType reports whether eventType is in the plan-execute contract vocabulary.
func KnownPlanExecuteEventType(eventType PlanExecuteEventType) bool {
	_, ok := planExecuteEventTypeSet[eventType]
	return ok
}

var planExecuteEventTypeSet = func() map[PlanExecuteEventType]struct{} {
	out := make(map[PlanExecuteEventType]struct{}, len(PlanExecuteEventTypes()))
	for _, eventType := range PlanExecuteEventTypes() {
		out[eventType] = struct{}{}
	}
	return out
}()

func PlanExecuteEventTypes() []PlanExecuteEventType {
	return []PlanExecuteEventType{
		PlanExecuteEventAutoExactPlanPartiallySatisfied,
		PlanExecuteEventAutoExactPlanSkipped,
		PlanExecuteEventAutoExactPlanSourceSatisfied,
		PlanExecuteEventAutoExactPlanSubmitted,
		PlanExecuteEventBlocked,
		PlanExecuteEventCompleted,
		PlanExecuteEventExecutorComplete,
		PlanExecuteEventExecutorEdit,
		PlanExecuteEventExecutorHandoffPacket,
		PlanExecuteEventExecutorInheritsPack,
		PlanExecuteEventExecutorPackMiss,
		PlanExecuteEventExecutorPacketOverridden,
		PlanExecuteEventExecutorPacketStale,
		PlanExecuteEventExecutorStart,
		PlanExecuteEventExecutorVerificationRepairNeeded,
		PlanExecuteEventParentChildInvocation,
		PlanExecuteEventChildTerminal,
		PlanExecuteEventChildAcceptance,
		PlanExecuteEventCandidatePatchArtifact,
		PlanExecuteEventPlanArtifactInvalid,
		PlanExecuteEventPlanArtifactLoaded,
		PlanExecuteEventPlanArtifactMissing,
		PlanExecuteEventPlanArtifactPackInheritFailed,
		PlanExecuteEventPlanArtifactPackInherited,
		PlanExecuteEventPlanScopeDrift,
		PlanExecuteEventPlannerBlocked,
		PlanExecuteEventPlannerPlanRejected,
		PlanExecuteEventPreflightFailed,
		PlanExecuteEventPreflightOK,
		PlanExecuteEventRecoveryDecompositionFailed,
		PlanExecuteEventRecoveryDecompositionWritten,
		PlanExecuteEventRecoveryFailureRecordFailed,
		PlanExecuteEventRecoveryFailureRecordWritten,
		PlanExecuteEventRequiredEditPathsResolved,
		PlanExecuteEventResumeStateSaved,
		PlanExecuteEventResumeStateLoaded,
		PlanExecuteEventReviewRepairAutoPatchHygiene,
		PlanExecuteEventReviewRepairAutoPatchFailed,
		PlanExecuteEventReviewRepairBlocked,
		PlanExecuteEventReviewRepairComplete,
		PlanExecuteEventReviewRepairDegraded,
		PlanExecuteEventReviewRepairFailed,
		PlanExecuteEventReviewRepairPassRejected,
		PlanExecuteEventReviewRepairPlanRejected,
		PlanExecuteEventReviewRepairRetry,
		PlanExecuteEventReviewRepairStart,
		PlanExecuteEventSourceSanity,
		PlanExecuteEventSourceSatisfied,
		PlanExecuteEventSourceSatisfiedRejected,
		PlanExecuteEventTerminalCompletion,
		PlanExecuteEventTerminalCompletionSaveFailed,
		PlanExecuteEventVerification,
		PlanExecuteEventVerificationAcceptance,
		PlanExecuteEventWriterRouteDecision,
	}
}

// ParsePlanExecuteEventName parses a fully qualified plan-execute event name.
func ParsePlanExecuteEventName(name string) (PlanExecuteEventType, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, PlanExecuteEventPrefix) {
		return "", false
	}

	eventType := strings.TrimPrefix(name, PlanExecuteEventPrefix)
	if eventType == "" {
		return "", false
	}

	typed := PlanExecuteEventType(eventType)
	if !KnownPlanExecuteEventType(typed) {
		return "", false
	}

	return typed, true
}

// ParsePlanExecuteEventType parses either a fully qualified event name or a
// legacy unprefixed event type.
func ParsePlanExecuteEventType(name string) (PlanExecuteEventType, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if eventType, ok := ParsePlanExecuteEventName(name); ok {
		return eventType, true
	}
	if strings.HasPrefix(name, PlanExecuteEventPrefix) {
		return "", false
	}

	eventType := PlanExecuteEventType(name)
	if !KnownPlanExecuteEventType(eventType) {
		return "", false
	}

	return eventType, true
}

// ParsePlanExecuteInvalidEventName parses a fully qualified invalid-attempt event name.
func ParsePlanExecuteInvalidEventName(name string) (PlanExecuteEventType, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, PlanExecuteEventPrefix) {
		return "", false
	}

	raw := strings.TrimPrefix(name, PlanExecuteEventPrefix)
	if !strings.HasSuffix(raw, PlanExecuteInvalidEventSuffix) {
		return "", false
	}

	raw = strings.TrimSuffix(raw, PlanExecuteInvalidEventSuffix)
	if raw == "" {
		return "", false
	}

	eventType := PlanExecuteEventType(raw)
	if !KnownPlanExecuteEventType(eventType) {
		return "", false
	}

	return eventType, true
}
