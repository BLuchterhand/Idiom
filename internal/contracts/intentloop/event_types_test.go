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
		if name != EventPrefix+string(eventType) {
			t.Fatalf("EventName(%q) = %q, want prefix form", eventType, name)
		}
		if !KnownEventType(eventType) {
			t.Fatalf("KnownEventType(%q) = false", eventType)
		}
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

func TestIntentLoopVocabularyParsersTrimButKnownEventTypeIsExact(t *testing.T) {
	if status, ok := ParseStatus(" \t" + string(StatusCompleted) + "\n"); !ok || status != StatusCompleted {
		t.Fatalf("ParseStatus trimmed completed = %q %v, want %q true", status, ok, StatusCompleted)
	}
	if status, ok := ParseCycleStatus(" \t" + string(CycleStatusFailed) + "\n"); !ok || status != CycleStatusFailed {
		t.Fatalf("ParseCycleStatus trimmed failed = %q %v, want %q true", status, ok, CycleStatusFailed)
	}
	if KnownEventType(EventType(EventName(EventStart))) {
		t.Fatalf("KnownEventType should reject prefixed event name %q", EventName(EventStart))
	}
	if KnownEventType(EventType(" " + string(EventStart))) {
		t.Fatalf("KnownEventType should not trim typed event values")
	}
}
