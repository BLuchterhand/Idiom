package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"idiom/internal/contracts/writerroute"
)

func TestPlanExecuteEventNameRoundTrip(t *testing.T) {
	name := PlanExecuteEventName(PlanExecuteEventVerification)
	if name != "idiom.plan_execute.verification" {
		t.Fatalf("PlanExecuteEventName = %q", name)
	}

	eventType, ok := ParsePlanExecuteEventName(name)
	if !ok || eventType != PlanExecuteEventVerification {
		t.Fatalf("ParsePlanExecuteEventName = %q %v", eventType, ok)
	}
}

func TestPlanExecuteEventTypesAllRoundTrip(t *testing.T) {
	seen := map[PlanExecuteEventType]struct{}{}
	for _, eventType := range PlanExecuteEventTypes() {
		if _, ok := seen[eventType]; ok {
			t.Fatalf("duplicate PlanExecuteEventTypes entry %q", eventType)
		}
		seen[eventType] = struct{}{}

		if !KnownPlanExecuteEventType(eventType) {
			t.Fatalf("KnownPlanExecuteEventType(%q) = false", eventType)
		}
		if parsed, ok := ParsePlanExecuteEventName(PlanExecuteEventName(eventType)); !ok || parsed != eventType {
			t.Fatalf("ParsePlanExecuteEventName(%q) = %q %v", PlanExecuteEventName(eventType), parsed, ok)
		}
		if parsed, ok := ParsePlanExecuteEventType(string(eventType)); !ok || parsed != eventType {
			t.Fatalf("ParsePlanExecuteEventType(%q) = %q %v", eventType, parsed, ok)
		}
		if parsed, ok := ParsePlanExecuteInvalidEventName(PlanExecuteInvalidEventName(eventType)); !ok || parsed != eventType {
			t.Fatalf("ParsePlanExecuteInvalidEventName(%q) = %q %v", PlanExecuteInvalidEventName(eventType), parsed, ok)
		}
	}
}

func TestPlanExecuteEventTypesCoverDeclaredConstants(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sourcePath := filepath.Join(filepath.Dir(filename), "event_types.go")
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read %s: %v", sourcePath, err)
	}

	declared := map[PlanExecuteEventType]string{}
	declRE := regexp.MustCompile(`(?m)(PlanExecuteEvent[A-Za-z0-9]+)\s+PlanExecuteEventType\s*=\s*"([^"]+)"`)
	for _, match := range declRE.FindAllStringSubmatch(string(body), -1) {
		declared[PlanExecuteEventType(match[2])] = match[1]
	}
	if len(declared) == 0 {
		t.Fatal("no PlanExecuteEventType constants found")
	}

	known := map[PlanExecuteEventType]struct{}{}
	for _, eventType := range PlanExecuteEventTypes() {
		known[eventType] = struct{}{}
	}

	var missing []string
	for eventType, name := range declared {
		if _, ok := known[eventType]; !ok {
			missing = append(missing, name+"="+string(eventType))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("PlanExecuteEventTypes missing declared constants:\n%s", strings.Join(missing, "\n"))
	}
}

func TestPlanExecuteTerminalOutcomeEventNameRoundTrip(t *testing.T) {
	for _, eventType := range []PlanExecuteEventType{
		PlanExecuteEventCompleted,
		PlanExecuteEventBlocked,
	} {
		if parsed, ok := ParsePlanExecuteEventName(PlanExecuteEventName(eventType)); !ok || parsed != eventType {
			t.Fatalf("ParsePlanExecuteEventName(%q) = %q %v", PlanExecuteEventName(eventType), parsed, ok)
		}
	}
}

func TestParsePlanExecuteEventNameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{
		"",
		"verification",
		"idiom.feature_planner.verification",
		PlanExecuteEventPrefix,
		"idiom.plan_execute.typo_event",
	} {
		if eventType, ok := ParsePlanExecuteEventName(name); ok {
			t.Fatalf("ParsePlanExecuteEventName(%q) = %q, true", name, eventType)
		}
	}
}

func TestParsePlanExecuteEventTypeAcceptsQualifiedAndBareNames(t *testing.T) {
	for _, name := range []string{
		PlanExecuteEventName(PlanExecuteEventExecutorEdit),
		string(PlanExecuteEventExecutorEdit),
	} {
		eventType, ok := ParsePlanExecuteEventType(name)
		if !ok || eventType != PlanExecuteEventExecutorEdit {
			t.Fatalf("ParsePlanExecuteEventType(%q) = %q %v", name, eventType, ok)
		}
	}

	if eventType, ok := ParsePlanExecuteEventType("idiom.plan_execute.not_real"); ok {
		t.Fatalf("ParsePlanExecuteEventType accepted unknown qualified event %q", eventType)
	}
	if eventType, ok := ParsePlanExecuteEventType("not_real"); ok {
		t.Fatalf("ParsePlanExecuteEventType accepted unknown bare event %q", eventType)
	}
}

func TestPlanExecuteInvalidEventNameRoundTrip(t *testing.T) {
	name := PlanExecuteInvalidEventName(PlanExecuteEventChildTerminal)
	if name != "idiom.plan_execute.child_terminal_invalid" {
		t.Fatalf("PlanExecuteInvalidEventName = %q", name)
	}

	eventType, ok := ParsePlanExecuteInvalidEventName(name)
	if !ok || eventType != PlanExecuteEventChildTerminal {
		t.Fatalf("ParsePlanExecuteInvalidEventName = %q %v", eventType, ok)
	}
}

func TestParsePlanExecuteInvalidEventNameRejectsNonInvalidNames(t *testing.T) {
	for _, name := range []string{
		PlanExecuteEventName(PlanExecuteEventChildTerminal),
		"idiom.plan_execute._invalid",
		"idiom.plan_execute.typo_event_invalid",
		"idiom.feature_planner.child_terminal_invalid",
	} {
		if eventType, ok := ParsePlanExecuteInvalidEventName(name); ok {
			t.Fatalf("ParsePlanExecuteInvalidEventName(%q) = %q, true", name, eventType)
		}
	}
}

func TestPlanExecuteWriterRouteEventMatchesWriterRouteContract(t *testing.T) {
	if string(PlanExecuteEventWriterRouteDecision) != string(writerroute.EventWriterRouteDecision) {
		t.Fatalf("plan-execute writer route event %q drifted from writerroute contract %q", PlanExecuteEventWriterRouteDecision, writerroute.EventWriterRouteDecision)
	}
}
