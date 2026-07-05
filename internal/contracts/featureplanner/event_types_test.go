package featureplanner

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestFeaturePlannerEventTypesAllRoundTrip(t *testing.T) {
	seen := map[FeaturePlannerEventType]struct{}{}
	for _, eventType := range EventTypes() {
		if _, ok := seen[eventType]; ok {
			t.Fatalf("duplicate EventTypes entry %q", eventType)
		}
		seen[eventType] = struct{}{}

		if !KnownEventType(eventType) {
			t.Fatalf("KnownEventType(%q) = false", eventType)
		}
		name := EventName(eventType)
		parsed, ok := ParseEventName(name)
		if !ok || parsed != eventType {
			t.Fatalf("ParseEventName(%q) = %q %v", name, parsed, ok)
		}
	}
}

func TestFeaturePlannerEventTypesCoverDeclaredConstants(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sourcePath := filepath.Join(filepath.Dir(filename), "event_types.go")
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read %s: %v", sourcePath, err)
	}

	declared := map[FeaturePlannerEventType]string{}
	declRE := regexp.MustCompile(`(?m)(Event[A-Za-z0-9]+)\s+FeaturePlannerEventType\s*=\s*"([^"]+)"`)
	for _, match := range declRE.FindAllStringSubmatch(string(body), -1) {
		declared[FeaturePlannerEventType(match[2])] = match[1]
	}
	if len(declared) == 0 {
		t.Fatal("no FeaturePlannerEventType constants found")
	}

	known := map[FeaturePlannerEventType]struct{}{}
	for _, eventType := range EventTypes() {
		known[eventType] = struct{}{}
	}

	var missing []string
	for eventType, name := range declared {
		if _, ok := known[eventType]; !ok {
			missing = append(missing, name+"="+string(eventType))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("EventTypes missing declared constants:\n%s", strings.Join(missing, "\n"))
	}
}

func TestParseFeaturePlannerEventNameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{
		"",
		string(EventStart),
		EventPrefix,
		"idiom.plan_execute.start",
		"idiom.feature_planner.not_real",
	} {
		if eventType, ok := ParseEventName(name); ok {
			t.Fatalf("ParseEventName(%q) = %q, true", name, eventType)
		}
	}
}

func TestKnownDurableEventNameAcceptsCurrentAndLegacyNames(t *testing.T) {
	for _, name := range EventNames() {
		if !KnownDurableEventName(name) {
			t.Fatalf("KnownDurableEventName(%q) = false", name)
		}
	}

	for _, name := range LegacyEventNames() {
		if !KnownDurableEventName(string(name)) || !KnownLegacyEventName(string(name)) {
			t.Fatalf("legacy event %q should be accepted", name)
		}
	}

	for _, name := range []string{"", "idiom.feature.created", "idiom.feature_planner.not_real"} {
		if KnownDurableEventName(name) {
			t.Fatalf("KnownDurableEventName(%q) = true", name)
		}
	}
}

func TestNestedPlanExecuteCompletionEventNamesRoundTrip(t *testing.T) {
	for _, name := range NestedPlanExecuteCompletionEventNames() {
		parsed, ok := ParseNestedPlanExecuteCompletionEventName(name)
		if !ok || string(parsed) != name {
			t.Fatalf("ParseNestedPlanExecuteCompletionEventName(%q) = %q %v", name, parsed, ok)
		}
		if !KnownNestedPlanExecuteCompletionEventName(name) {
			t.Fatalf("KnownNestedPlanExecuteCompletionEventName(%q) = false", name)
		}
	}

	if got, ok := ParseNestedPlanExecuteCompletionEventName("internal_idiom.plan_execute.unknown"); ok {
		t.Fatalf("accepted unknown nested event %q", got)
	}
}

func TestPlanExecuteCompletionSourceTrustPolicy(t *testing.T) {
	for _, source := range PlanExecuteCompletionSources() {
		parsed, ok := ParsePlanExecuteCompletionSource(string(source))
		if !ok || parsed != source {
			t.Fatalf("ParsePlanExecuteCompletionSource(%q) = %q %v, want %q true", source, parsed, ok, source)
		}
		if !CompletionSourceTrustsAcceptanceEvidence(string(source)) {
			t.Fatalf("source %q should be trusted for acceptance evidence", source)
		}
	}

	for _, source := range []PlanExecuteCompletionSource{
		PlanExecuteCompletionSourceDurableTerminal,
		PlanExecuteCompletionSourceDurableProgress,
	} {
		if !CompletionSourceCarriesDurableAcceptanceEvidence(string(source)) {
			t.Fatalf("source %q should carry durable acceptance evidence", source)
		}
	}

	if CompletionSourceCarriesDurableAcceptanceEvidence(string(PlanExecuteCompletionSourceHeadlessReturned)) {
		t.Fatal("headless return should not carry durable acceptance evidence by itself")
	}
	if CompletionSourceTrustsAcceptanceEvidence("test_plan_execute_adapter") {
		t.Fatal("unknown completion source should not be trusted")
	}
}

func TestFeaturePlannerEventStringPredicatesTrimInput(t *testing.T) {
	if eventType, ok := ParseEventName(" \t" + EventName(EventStart) + "\n"); !ok || eventType != EventStart {
		t.Fatalf("ParseEventName trimmed start = %q %v, want %q true", eventType, ok, EventStart)
	}
	if !KnownLegacyEventName(" \t" + string(LegacyEventAtomStarted) + "\n") {
		t.Fatalf("KnownLegacyEventName should trim %q", LegacyEventAtomStarted)
	}
	if source, ok := ParsePlanExecuteCompletionSource(" \t" + string(PlanExecuteCompletionSourceDurableTerminal) + "\n"); !ok || source != PlanExecuteCompletionSourceDurableTerminal {
		t.Fatalf("ParsePlanExecuteCompletionSource trimmed durable_terminal = %q %v", source, ok)
	}
	if !CompletionSourceCarriesDurableAcceptanceEvidence(" \t" + string(PlanExecuteCompletionSourceDurableProgress) + "\n") {
		t.Fatalf("CompletionSourceCarriesDurableAcceptanceEvidence should trim %q", PlanExecuteCompletionSourceDurableProgress)
	}
	if !CompletionSourceTrustsAcceptanceEvidence(" \t" + string(PlanExecuteCompletionSourceTUIReturned) + "\n") {
		t.Fatalf("CompletionSourceTrustsAcceptanceEvidence should trim %q", PlanExecuteCompletionSourceTUIReturned)
	}
	if eventName, ok := ParseNestedPlanExecuteCompletionEventName(" \t" + string(NestedPlanExecuteCompletionEventBlocked) + "\n"); !ok || eventName != NestedPlanExecuteCompletionEventBlocked {
		t.Fatalf("ParseNestedPlanExecuteCompletionEventName trimmed blocked = %q %v", eventName, ok)
	}

	for _, value := range []string{
		"idiom.feature_planner.not_real",
		"idiom.feature.atom_unknown",
		"test_plan_execute_adapter",
		"internal_idiom.plan_execute.unknown",
	} {
		if KnownDurableEventName(value) {
			t.Fatalf("KnownDurableEventName(%q) = true", value)
		}
	}
}
