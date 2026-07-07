package cmdtelemetry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOpenRecorderUsesCommandMetadataName(t *testing.T) {
	dir := t.TempDir()

	rec := OpenRecorder([]string{"tool", "pwd"}, RecorderOptions{Dir: dir})
	if rec == nil {
		t.Fatal("recorder disabled unexpectedly")
	}

	if err := rec.CloseWithFields(0, nil); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readTelemetryRecords(t, rec.Path())
	if len(records) < 2 {
		t.Fatalf("expected session_start and process_start records, got %#v", records)
	}

	for _, record := range records[:2] {
		if record["command"] != "tool:pwd" {
			t.Fatalf("record command = %q, want tool:pwd in %#v", record["command"], record)
		}
	}
}

func readTelemetryRecords(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("parse telemetry line %q: %v", line, err)
		}

		records = append(records, record)
	}

	return records
}
