package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAndSummarizeLaneHistory(t *testing.T) {
	root := t.TempDir()
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "implement", Brief: "b.md", ExitCode: 1}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3 (re-dispatch): %v", err)
	}

	s, err := summarizeLaneHistory(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Total != 3 {
		t.Fatalf("total = %d, want 3", s.Total)
	}
	if s.ByMode["plan-execute"] != 2 {
		t.Fatalf("plan-execute count = %d, want 2", s.ByMode["plan-execute"])
	}
	if s.ByMode["implement"] != 1 {
		t.Fatalf("implement count = %d, want 1", s.ByMode["implement"])
	}
	if s.ByExitCode[0] != 2 {
		t.Fatalf("exit 0 count = %d, want 2", s.ByExitCode[0])
	}
	if s.ByExitCode[1] != 1 {
		t.Fatalf("exit 1 count = %d, want 1", s.ByExitCode[1])
	}
	if s.BriefsByPath["a.md"] != 2 {
		t.Fatalf("a.md count = %d, want 2", s.BriefsByPath["a.md"])
	}
}

func TestSummarizeLaneHistoryMissingFileReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	s, err := summarizeLaneHistory(root)
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if s.Total != 0 {
		t.Fatalf("total = %d, want 0", s.Total)
	}
}

func TestSummarizeLaneHistorySkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "implement", Brief: "x.md", ExitCode: 0}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Inject a malformed line directly into the file.
	path := filepath.Join(root, ".idiom", "lane-history.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for malformed inject: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	s, err := summarizeLaneHistory(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (malformed line skipped)", s.Total)
	}
}

func TestPrintLaneHistorySummaryEmpty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := printLaneHistorySummary(root, &buf); err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(buf.String(), "0 dispatch") {
		t.Fatalf("expected '0 dispatch' in output, got: %s", buf.String())
	}
}
