package queue

import "strings"

type AdmissionState string

const (
	AdmissionStateAcquired            AdmissionState = "acquired"
	AdmissionStateGranted             AdmissionState = "granted"
	AdmissionStateUnavailable         AdmissionState = "unavailable"
	AdmissionStateDisabled            AdmissionState = "disabled"
	AdmissionStateDegraded            AdmissionState = "degraded"
	AdmissionStateError               AdmissionState = "error"
	AdmissionStateFailed              AdmissionState = "failed"
	AdmissionStateNestedSamePool      AdmissionState = "nested_same_pool"
	AdmissionStateNestedCrossPool     AdmissionState = "nested_cross_pool"
	AdmissionStateFailedBeforeGrant   AdmissionState = "failed_before_grant"
	AdmissionStateCanceledBeforeGrant AdmissionState = "canceled_before_grant"
)

type AdmissionEventType string
type CommandEventPrefix string

const (
	AdmissionEventQueueAcquired    AdmissionEventType = "queue_acquired"
	AdmissionEventQueueUnavailable AdmissionEventType = "queue_unavailable"
	AdmissionEventQueueDisabled    AdmissionEventType = "queue_disabled"
	AdmissionEventQueueDegraded    AdmissionEventType = "queue_degraded"
	AdmissionEventQueueError       AdmissionEventType = "queue_error"

	CommandEventPrefixImplement   CommandEventPrefix = "idiom.implement"
	CommandEventPrefixPlanExecute CommandEventPrefix = "idiom.plan_execute"
)

type ReasonClass string
type AdmissionMode string

const (
	ReasonClassPoolDisabled      ReasonClass = "pool_disabled"
	ReasonClassNestedSamePool    ReasonClass = "nested_same_pool"
	ReasonClassNestedCrossPool   ReasonClass = "nested_cross_pool"
	ReasonClassUnknownPool       ReasonClass = "unknown_pool"
	ReasonClassDaemonUnreachable ReasonClass = "daemon_unreachable"
	ReasonClassTimeoutOrCanceled ReasonClass = "timeout_or_canceled"
	ReasonClassNestedLease       ReasonClass = "nested_lease"
	ReasonClassUnknown           ReasonClass = "unknown"
	ReasonClassQueueError        ReasonClass = "queue_error"
	ReasonClassQueueUnavailable  ReasonClass = "queue_unavailable"

	AdmissionModeOpportunistic AdmissionMode = "opportunistic"
	AdmissionModeRequired      AdmissionMode = "required"
)

func NormalizeAdmissionMode(mode AdmissionMode) AdmissionMode {
	switch mode {
	case AdmissionModeRequired:
		return AdmissionModeRequired
	default:
		return AdmissionModeOpportunistic
	}
}

func AdmissionEventName(state AdmissionState) AdmissionEventType {
	switch state {
	case AdmissionStateAcquired,
		AdmissionStateGranted,
		AdmissionStateNestedSamePool:
		return AdmissionEventQueueAcquired
	case AdmissionStateUnavailable:
		return AdmissionEventQueueUnavailable
	case AdmissionStateDisabled:
		return AdmissionEventQueueDisabled
	case AdmissionStateDegraded,
		AdmissionStateNestedCrossPool:
		return AdmissionEventQueueDegraded
	default:
		return AdmissionEventQueueError
	}
}

func AdmissionStateAllowsDispatch(state AdmissionState) bool {
	switch state {
	case AdmissionStateAcquired,
		AdmissionStateGranted,
		AdmissionStateDisabled,
		AdmissionStateNestedSamePool:
		return true
	default:
		return false
	}
}

func CommandAdmissionEventName(prefix CommandEventPrefix, event AdmissionEventType) string {
	prefixText := strings.TrimSuffix(strings.TrimSpace(string(prefix)), ".")
	if prefixText == "" {
		return string(event)
	}
	return prefixText + "." + string(event)
}

func ClassifyReason(reason string) ReasonClass {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case reason == "":
		return ""
	case strings.Contains(reason, string(ReasonClassPoolDisabled)):
		return ReasonClassPoolDisabled
	case strings.Contains(reason, string(ReasonClassNestedSamePool)):
		return ReasonClassNestedSamePool
	case strings.Contains(reason, string(ReasonClassNestedCrossPool)):
		return ReasonClassNestedCrossPool
	case strings.Contains(reason, "unknown pool"):
		return ReasonClassUnknownPool
	case strings.Contains(reason, string(ReasonClassQueueUnavailable)):
		return ReasonClassQueueUnavailable
	case strings.Contains(reason, "daemon_unreachable"), strings.Contains(reason, "unreachable"), strings.Contains(reason, "connection refused"):
		return ReasonClassDaemonUnreachable
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "deadline"):
		return ReasonClassTimeoutOrCanceled
	case strings.Contains(reason, "held"):
		return ReasonClassNestedLease
	default:
		return ReasonClassQueueError
	}
}

func ClassifyFailureCode(code string) ReasonClass {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "unavailable":
		return ReasonClassQueueUnavailable
	case "deadline_exceeded", "canceled":
		return ReasonClassTimeoutOrCanceled
	case "invalid_argument":
		return ReasonClassUnknownPool
	case "failed_precondition":
		return ReasonClassQueueError
	default:
		return ""
	}
}
