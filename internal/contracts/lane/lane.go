package lane

import "strings"

type LaneID string
type EventType string
type EventField string

const (
	LanePlanExecute LaneID = "plan-execute"
	LaneImplement   LaneID = "implement"

	ScopeRefPlanExecute = "lane:" + string(LanePlanExecute)

	EventPrefix               = "idiom.lane."
	EventStart      EventType = "start"
	EventRunLedger  EventType = "run_ledger"
	EventDegraded   EventType = "degraded"
	EventAtomIntake EventType = "atom_intake"

	EventFieldKind    EventField = "kind"
	EventFieldDetails EventField = "details"
	EventFieldMode    EventField = "mode"
)

func KnownLaneID(value LaneID) bool {
	for _, lane := range LaneIDs() {
		if value == lane {
			return true
		}
	}
	return false
}

func LaneIDs() []LaneID {
	return []LaneID{
		LanePlanExecute,
		LaneImplement,
	}
}

func EventTypes() []EventType {
	return []EventType{
		EventStart,
		EventRunLedger,
		EventDegraded,
		EventAtomIntake,
	}
}

func KnownEventType(value EventType) bool {
	for _, eventType := range EventTypes() {
		if value == eventType {
			return true
		}
	}
	return false
}

func EventName(eventType EventType) string {
	eventType = EventType(strings.TrimSpace(string(eventType)))
	if eventType == "" {
		return ""
	}
	return EventPrefix + string(eventType)
}
