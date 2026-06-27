package intentloop

import "strings"

type EventType string
type Status string
type CycleStatus string

const (
	EventPrefix = "idiom.intent_loop."

	EventStart               EventType = "start"
	EventResumeTerminal      EventType = "resume_terminal"
	EventCycleStart          EventType = "cycle_start"
	EventCycleComplete       EventType = "cycle_complete"
	EventConvergenceDetected EventType = "convergence_detected"
	EventCycleBudgetWarning  EventType = "cycle_budget_warning"
	EventBudgetExhausted     EventType = "budget_exhausted"
	EventComplete            EventType = "complete"
	EventBlocked             EventType = "blocked"
)

const (
	StatusRunning           Status = "running"
	StatusCompleted         Status = "completed"
	StatusBlocked           Status = "blocked"
	StatusNoProgress        Status = "no_progress"
	StatusBudgetExhausted   Status = "budget_exhausted"
	StatusMaxCyclesExceeded Status = "max_cycles_exceeded"
	StatusFailed            Status = "failed"
)

const (
	CycleStatusDispatched CycleStatus = "dispatched"
	CycleStatusCompleted  CycleStatus = "completed"
	CycleStatusFailed     CycleStatus = "failed"
)

func EventName(eventType EventType) string {
	return EventPrefix + string(eventType)
}

func EventTypes() []EventType {
	return []EventType{
		EventStart,
		EventResumeTerminal,
		EventCycleStart,
		EventCycleComplete,
		EventConvergenceDetected,
		EventCycleBudgetWarning,
		EventBudgetExhausted,
		EventComplete,
		EventBlocked,
	}
}

func KnownEventType(eventType EventType) bool {
	for _, known := range EventTypes() {
		if eventType == known {
			return true
		}
	}

	return false
}

func ParseEventName(name string) (EventType, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, EventPrefix) {
		return "", false
	}
	name = strings.TrimPrefix(name, EventPrefix)
	eventType := EventType(name)
	if !KnownEventType(eventType) {
		return "", false
	}
	return eventType, true
}

func EventNames() []string {
	eventTypes := EventTypes()
	out := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		out = append(out, EventName(eventType))
	}
	return out
}

func StatusTerminal(status Status) bool {
	switch status {
	case StatusCompleted,
		StatusBlocked,
		StatusNoProgress,
		StatusBudgetExhausted,
		StatusMaxCyclesExceeded,
		StatusFailed:
		return true
	default:
		return false
	}
}

func TerminalExitCode(status Status) int {
	if status == StatusCompleted {
		return 0
	}
	return 1
}

func ParseStatus(raw string) (Status, bool) {
	status := Status(strings.TrimSpace(raw))
	for _, known := range Statuses() {
		if status == known {
			return status, true
		}
	}
	return "", false
}

func Statuses() []Status {
	return []Status{
		StatusRunning,
		StatusCompleted,
		StatusBlocked,
		StatusNoProgress,
		StatusBudgetExhausted,
		StatusMaxCyclesExceeded,
		StatusFailed,
	}
}

func CycleStatuses() []CycleStatus {
	return []CycleStatus{
		CycleStatusDispatched,
		CycleStatusCompleted,
		CycleStatusFailed,
	}
}

func ParseCycleStatus(raw string) (CycleStatus, bool) {
	status := CycleStatus(strings.TrimSpace(raw))
	for _, known := range CycleStatuses() {
		if status == known {
			return status, true
		}
	}
	return "", false
}
