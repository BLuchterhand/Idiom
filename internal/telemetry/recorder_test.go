package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecorderWritesProcessTelemetryAndRedactsSensitiveArgs(t *testing.T) {
	dir := t.TempDir()
	rec, err := Open(Options{
		Dir:           dir,
		Command:       "implement",
		Args:          []string{"implement", "--prompt", "change the secret thing", "--config", "config.json"},
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if rec == nil {
		t.Fatal("recorder disabled unexpectedly")
	}
	if err := rec.Log("custom", map[string]any{"ok": true}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := rec.Close(0); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	records := readTelemetryRecords(t, filepath.Join(dir, entries[0].Name()))
	if len(records) != 4 {
		t.Fatalf("expected session_start, process_start, custom, process_exit; got %d records", len(records))
	}
	if records[0]["type"] != "session_start" || records[1]["type"] != "process_start" || records[3]["type"] != "process_exit" {
		t.Fatalf("unexpected record sequence: %#v", records)
	}
	args, ok := records[1]["args"].([]any)
	if !ok {
		t.Fatalf("process_start args missing: %#v", records[1])
	}
	renderedArgs := strings.TrimSpace(toJSON(t, args))
	if strings.Contains(renderedArgs, "secret thing") || !strings.Contains(renderedArgs, "redacted len=23") {
		t.Fatalf("sensitive prompt arg was not redacted: %s", renderedArgs)
	}
}

func TestRecorderCloseWithFieldsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	rec, err := Open(Options{Dir: dir, Command: "implement", WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := rec.CloseWithFields(143, map[string]any{"reason": "signal", "signal": "terminated"}); err != nil {
		t.Fatalf("CloseWithFields: %v", err)
	}
	if err := rec.Close(0); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	records := readTelemetryRecords(t, filepath.Join(dir, entries[0].Name()))
	processExits := 0
	for _, record := range records {
		if record["type"] != "process_exit" {
			continue
		}
		processExits++
		if record["exit_code"] != float64(143) || record["reason"] != "signal" || record["signal"] != "terminated" {
			t.Fatalf("unexpected process_exit record: %#v", record)
		}
	}
	if processExits != 1 {
		t.Fatalf("expected one process_exit, got %d in %#v", processExits, records)
	}
}

func TestRecorderHonorsDisabledEnv(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	rec, err := Open(Options{Dir: t.TempDir(), Command: "tool"})
	if err != nil {
		t.Fatalf("Open disabled: %v", err)
	}
	if rec != nil {
		t.Fatal("expected nil recorder when telemetry is disabled")
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
			t.Fatalf("parse %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}
