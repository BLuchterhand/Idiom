package featureplanner

import "strings"

type FeaturePlannerEventType string
type LegacyEventName string

const (
	EventPrefix = "idiom.feature_planner."

	EventAtomCompleted                       FeaturePlannerEventType = "atom_completed"
	EventAtomDispatched                      FeaturePlannerEventType = "atom_dispatched"
	EventAtomFailed                          FeaturePlannerEventType = "atom_failed"
	EventAtomSourceSatisfied                 FeaturePlannerEventType = "atom_source_satisfied"
	EventAtomsCompiled                       FeaturePlannerEventType = "atoms_compiled"
	EventBlocked                             FeaturePlannerEventType = "blocked"
	EventComplete                            FeaturePlannerEventType = "complete"
	EventDispatchReady                       FeaturePlannerEventType = "dispatch_ready"
	EventError                               FeaturePlannerEventType = "error"
	EventFailed                              FeaturePlannerEventType = "failed"
	EventImplementorQuestionOpened           FeaturePlannerEventType = "implementor_question_opened"
	EventMalformedStructuredOutput           FeaturePlannerEventType = "malformed_structured_output"
	EventPlanAccepted                        FeaturePlannerEventType = "plan_accepted"
	EventPlanApproved                        FeaturePlannerEventType = "plan_approved"
	EventPlanCandidateRejected               FeaturePlannerEventType = "plan_candidate_rejected"
	EventPlanContractBlocked                 FeaturePlannerEventType = "plan_contract_blocked"
	EventPlanExecuteCompletionRejected       FeaturePlannerEventType = "plan_execute_completion_rejected"
	EventPlanExecuteGitHistoryRequirement    FeaturePlannerEventType = "plan_execute_git_history_requirement"
	EventPlanExecuteRetryPending             FeaturePlannerEventType = "plan_execute_retry_pending"
	EventPlanExecuteRetryResumed             FeaturePlannerEventType = "plan_execute_retry_resumed"
	EventPlanTabUpdated                      FeaturePlannerEventType = "plan_tab_updated"
	EventPlanReviewed                        FeaturePlannerEventType = "plan_reviewed"
	EventProductContext                      FeaturePlannerEventType = "product_context"
	EventPlannerQuestionAnswerRejected       FeaturePlannerEventType = "planner_question_answer_rejected"
	EventPlannerQuestionAnswered             FeaturePlannerEventType = "planner_question_answered"
	EventPlannerQuestionOpened               FeaturePlannerEventType = "planner_question_opened"
	EventPlannerQuestionRejected             FeaturePlannerEventType = "planner_question_rejected"
	EventPlannerQuestionSupersededByRevision FeaturePlannerEventType = "planner_question_superseded_by_revision"
	EventPlannerSourceSatisfied              FeaturePlannerEventType = "planner_source_satisfied"
	EventPlanningStuck                       FeaturePlannerEventType = "planning_stuck"
	EventProductContextBlocked               FeaturePlannerEventType = "product_context_blocked"
	EventProductSourceSatisfied              FeaturePlannerEventType = "product_source_satisfied"
	EventQuestionOpen                        FeaturePlannerEventType = "question_open"
	EventRejectedCandidateRevalidated        FeaturePlannerEventType = "rejected_candidate_revalidated"
	EventReleaseBlocked                      FeaturePlannerEventType = "release_blocked"
	EventReleaseReady                        FeaturePlannerEventType = "release_ready"
	EventResumeActiveRun                     FeaturePlannerEventType = "resume_active_run"
	EventRecoveryOpen                        FeaturePlannerEventType = "recovery_open"
	EventSourceSatisfiedRejected             FeaturePlannerEventType = "source_satisfied_rejected"
	EventStart                               FeaturePlannerEventType = "start"
	EventStandaloneContextBlocked            FeaturePlannerEventType = "standalone_context_blocked"
	EventStandaloneContextRepairRetry        FeaturePlannerEventType = "standalone_context_repair_retry"
	EventTDDPreExecutionBlocked              FeaturePlannerEventType = "tdd_pre_execution_blocked"
	EventTDDPreExecutionChecked              FeaturePlannerEventType = "tdd_pre_execution_checked"
	EventTerminal                            FeaturePlannerEventType = "terminal"
	EventUnverifiedCompletionRejected        FeaturePlannerEventType = "unverified_completion_rejected"
)

const (
	LegacyEventAtomStarted           LegacyEventName = "idiom.feature.atom_started"
	LegacyEventAtomProgressed        LegacyEventName = "idiom.feature.atom_progressed"
	LegacyEventAtomReviewed          LegacyEventName = "idiom.feature.atom_reviewed"
	LegacyEventAtomVerified          LegacyEventName = "idiom.feature.atom_verified"
	LegacyEventPlannerQuestionOpened LegacyEventName = "idiom.feature.planner_question_opened"
)

type NestedPlanExecuteCompletionEventName string
type PlanExecuteCompletionSource string

const (
	NestedPlanExecuteCompletionEventCompleted NestedPlanExecuteCompletionEventName = "internal_idiom.plan_execute.completed"
	NestedPlanExecuteCompletionEventBlocked   NestedPlanExecuteCompletionEventName = "internal_idiom.plan_execute.blocked"
)

const (
	PlanExecuteCompletionSourceHeadlessReturned PlanExecuteCompletionSource = "headless_plan_execute_returned"
	PlanExecuteCompletionSourceTUIReturned      PlanExecuteCompletionSource = "tui_plan_execute_returned"
	PlanExecuteCompletionSourceDurableTerminal  PlanExecuteCompletionSource = "durable_plan_execute_terminal_completion"
	PlanExecuteCompletionSourceDurableProgress  PlanExecuteCompletionSource = "durable_plan_execute_progress"
)

func EventName(eventType FeaturePlannerEventType) string {
	return EventPrefix + string(eventType)
}

func EventTypes() []FeaturePlannerEventType {
	return []FeaturePlannerEventType{
		EventAtomCompleted,
		EventAtomDispatched,
		EventAtomFailed,
		EventAtomSourceSatisfied,
		EventAtomsCompiled,
		EventBlocked,
		EventComplete,
		EventDispatchReady,
		EventError,
		EventFailed,
		EventImplementorQuestionOpened,
		EventMalformedStructuredOutput,
		EventPlanAccepted,
		EventPlanApproved,
		EventPlanCandidateRejected,
		EventPlanContractBlocked,
		EventPlanExecuteCompletionRejected,
		EventPlanExecuteGitHistoryRequirement,
		EventPlanExecuteRetryPending,
		EventPlanExecuteRetryResumed,
		EventPlanTabUpdated,
		EventPlanReviewed,
		EventProductContext,
		EventPlannerQuestionAnswerRejected,
		EventPlannerQuestionAnswered,
		EventPlannerQuestionOpened,
		EventPlannerQuestionRejected,
		EventPlannerQuestionSupersededByRevision,
		EventPlannerSourceSatisfied,
		EventPlanningStuck,
		EventProductContextBlocked,
		EventProductSourceSatisfied,
		EventQuestionOpen,
		EventRejectedCandidateRevalidated,
		EventReleaseBlocked,
		EventReleaseReady,
		EventResumeActiveRun,
		EventRecoveryOpen,
		EventSourceSatisfiedRejected,
		EventStart,
		EventStandaloneContextBlocked,
		EventStandaloneContextRepairRetry,
		EventTDDPreExecutionBlocked,
		EventTDDPreExecutionChecked,
		EventTerminal,
		EventUnverifiedCompletionRejected,
	}
}

func KnownEventType(eventType FeaturePlannerEventType) bool {
	return knownValue(eventType, EventTypes())
}

func ParseEventName(name string) (FeaturePlannerEventType, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, EventPrefix) {
		return "", false
	}
	eventType := FeaturePlannerEventType(strings.TrimPrefix(name, EventPrefix))
	if !KnownEventType(eventType) {
		return "", false
	}
	return eventType, true
}

func LegacyEventNames() []LegacyEventName {
	return []LegacyEventName{
		LegacyEventAtomStarted,
		LegacyEventAtomProgressed,
		LegacyEventAtomReviewed,
		LegacyEventAtomVerified,
		LegacyEventPlannerQuestionOpened,
	}
}

func KnownLegacyEventName(name string) bool {
	return knownValue(LegacyEventName(strings.TrimSpace(name)), LegacyEventNames())
}

func KnownDurableEventName(name string) bool {
	if _, ok := ParseEventName(name); ok {
		return true
	}
	return KnownLegacyEventName(name)
}

func PlanExecuteCompletionSources() []PlanExecuteCompletionSource {
	return []PlanExecuteCompletionSource{
		PlanExecuteCompletionSourceHeadlessReturned,
		PlanExecuteCompletionSourceTUIReturned,
		PlanExecuteCompletionSourceDurableTerminal,
		PlanExecuteCompletionSourceDurableProgress,
	}
}

func KnownPlanExecuteCompletionSource(source PlanExecuteCompletionSource) bool {
	return knownValue(source, PlanExecuteCompletionSources())
}

func ParsePlanExecuteCompletionSource(source string) (PlanExecuteCompletionSource, bool) {
	typed := PlanExecuteCompletionSource(strings.TrimSpace(source))
	if !KnownPlanExecuteCompletionSource(typed) {
		return "", false
	}
	return typed, true
}

func CompletionSourceCarriesDurableAcceptanceEvidence(source string) bool {
	typed := PlanExecuteCompletionSource(strings.TrimSpace(source))
	return typed == PlanExecuteCompletionSourceDurableTerminal ||
		typed == PlanExecuteCompletionSourceDurableProgress
}

func CompletionSourceTrustsAcceptanceEvidence(source string) bool {
	_, ok := ParsePlanExecuteCompletionSource(source)
	return ok
}

func EventNames() []string {
	eventTypes := EventTypes()
	out := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		out = append(out, EventName(eventType))
	}
	return out
}

func KnownNestedPlanExecuteCompletionEventName(name string) bool {
	_, ok := ParseNestedPlanExecuteCompletionEventName(name)
	return ok
}

func ParseNestedPlanExecuteCompletionEventName(name string) (NestedPlanExecuteCompletionEventName, bool) {
	switch NestedPlanExecuteCompletionEventName(strings.TrimSpace(name)) {
	case NestedPlanExecuteCompletionEventCompleted:
		return NestedPlanExecuteCompletionEventCompleted, true
	case NestedPlanExecuteCompletionEventBlocked:
		return NestedPlanExecuteCompletionEventBlocked, true
	default:
		return "", false
	}
}

func knownValue[T comparable](value T, known []T) bool {
	for _, candidate := range known {
		if value == candidate {
			return true
		}
	}
	return false
}
