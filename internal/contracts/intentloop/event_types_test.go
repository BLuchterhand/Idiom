package intentloop

import "testing"

func TestEventTypesAllRoundTrip(t *testing.T) {
	seen := map[EventType]struct{}{}
	for _, eventType := range EventTypes() {
		if _, ok := seen[eventType]; ok {
			t.Fatalf("duplicate EventTypes entry %q", eventType)
		}
		seen[eventType] = struct{}{}

		name := EventName(eventType)
		parsed, ok := ParseEventName(name)
		if !ok || parsed != eventType {
			t.Fatalf("ParseEventName(%q) = %q %v", name, parsed, ok)
		}
	}
}

func TestParseEventNameRejectsUnknown(t *testing.T) {
	if eventType, ok := ParseEventName("idiom.intent_loop.not_real"); ok {
		t.Fatalf("ParseEventName accepted %q", eventType)
	}
}

func TestParseEventNameRejectsBareKnownType(t *testing.T) {
	if eventType, ok := ParseEventName(string(EventStart)); ok {
		t.Fatalf("ParseEventName accepted bare event type %q", eventType)
	}
}

func TestTerminalStatusMatrix(t *testing.T) {
	for _, tt := range []struct {
		status   Status
		terminal bool
		exitCode int
	}{
		{status: StatusRunning, terminal: false, exitCode: 1},
		{status: StatusCompleted, terminal: true, exitCode: 0},
		{status: StatusBlocked, terminal: true, exitCode: 1},
		{status: StatusNoProgress, terminal: true, exitCode: 1},
		{status: StatusBudgetExhausted, terminal: true, exitCode: 1},
		{status: StatusMaxCyclesExceeded, terminal: true, exitCode: 1},
		{status: StatusFailed, terminal: true, exitCode: 1},
	} {
		if got := StatusTerminal(tt.status); got != tt.terminal {
			t.Fatalf("StatusTerminal(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
		if got := TerminalExitCode(tt.status); got != tt.exitCode {
			t.Fatalf("TerminalExitCode(%q) = %d, want %d", tt.status, got, tt.exitCode)
		}
	}
}

func TestIntentLoopStatusesParseDeclaredValues(t *testing.T) {
	for _, status := range Statuses() {
		parsed, ok := ParseStatus(string(status))
		if !ok || parsed != status {
			t.Fatalf("ParseStatus(%q) = %q %v", status, parsed, ok)
		}
	}

	for _, raw := range []string{"", "completed with notes", "running-ish", "not_real"} {
		if status, ok := ParseStatus(raw); ok {
			t.Fatalf("ParseStatus(%q) = %q, true", raw, status)
		}
	}
}

func TestIntentLoopCycleStatusesParseDeclaredValues(t *testing.T) {
	for _, status := range CycleStatuses() {
		parsed, ok := ParseCycleStatus(string(status))
		if !ok || parsed != status {
			t.Fatalf("ParseCycleStatus(%q) = %q %v", status, parsed, ok)
		}
	}

	for _, raw := range []string{"", "completed with notes", "dispatching", "not_real"} {
		if status, ok := ParseCycleStatus(raw); ok {
			t.Fatalf("ParseCycleStatus(%q) = %q, true", raw, status)
		}
	}
}
