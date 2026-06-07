package localops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReplayReportWithoutLogsIsUnsupported(t *testing.T) {
	report, err := BuildReplayReport("", "")
	if err != nil {
		t.Fatalf("BuildReplayReport: %v", err)
	}
	if report.Status != "unsupported" || len(report.Blockers) != 1 {
		t.Fatalf("expected unsupported replay blocker, got %#v", report)
	}
}

func TestBuildReplayReportSummarizesMetadataLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	body := `{"type":"tool_result","tool":"read_file","summary":"metadata only"}` + "\n" +
		`{"type":"turn_summary","stop_reason":"budget_exhausted","duration_ms":1000}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	report, err := BuildReplayReport(path, "")
	if err != nil {
		t.Fatalf("BuildReplayReport: %v", err)
	}
	if report.Status != "pass" || report.LogCount != 1 || report.Aggregate == nil {
		t.Fatalf("expected pass replay aggregate, got %#v", report)
	}
	if report.Signals.ToolCalls == 0 || report.Signals.BudgetExhausted == 0 {
		t.Fatalf("expected replay signals, got %#v", report.Signals)
	}
}
