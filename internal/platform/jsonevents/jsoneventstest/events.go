package jsoneventstest

import (
	"encoding/json"
	"strings"
)

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func DecodeLines(t testingT, stdout string) []map[string]any {
	t.Helper()

	var events []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}

		events = append(events, event)
	}

	return events
}

func FindByType(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}

	return nil, false
}
