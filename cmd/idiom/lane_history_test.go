package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSummarizeLaneHistoryByMode(t *testing.T) {
	root := t.TempDir()
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "b.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "implement", Brief: "c.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3: %v", err)
	}
	s, err := summarizeLaneHistoryByMode(root, "plan-execute")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if s.Total != 2 {
		t.Fatalf("total = %d, want 2", s.Total)
	}
	if s.ByMode["plan-execute"] != 2 {
		t.Fatalf("plan-execute count = %d, want 2", s.ByMode["plan-execute"])
	}
	if s.ByMode["implement"] != 0 {
		t.Fatalf("implement count = %d, want 0 (filtered out)", s.ByMode["implement"])
	}
}

func TestDetectBriefProvenance(t *testing.T) {
	root := t.TempDir()
	refineDrafted := filepath.Join(root, "refine.md")
	if err := os.WriteFile(refineDrafted, []byte("<!-- idiom:refine-drafted title=foo iter=2 reason=ship-as-is -->\n# Brief body\n"), 0o644); err != nil {
		t.Fatalf("write refine: %v", err)
	}
	claudeDirect := filepath.Join(root, "claude.md")
	if err := os.WriteFile(claudeDirect, []byte("# Direct brief, no marker\n"), 0o644); err != nil {
		t.Fatalf("write direct: %v", err)
	}
	cases := map[string]string{
		refineDrafted:                     "refine-drafted",
		claudeDirect:                      "claude-direct",
		"":                                "unknown",
		filepath.Join(root, "missing.md"): "unknown",
	}
	for path, want := range cases {
		got := detectBriefProvenance(path)
		if got != want {
			t.Errorf("detectBriefProvenance(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestPrintLaneHistorySummaryEmpty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := printLaneHistorySummary(root, &buf, laneHistoryFilter{}); err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(buf.String(), "0 dispatch") {
		t.Fatalf("expected '0 dispatch' in output, got: %s", buf.String())
	}
}

func TestSummarizeLaneHistoryWithFilterSince(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := laneHistoryEvent{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := laneHistoryEvent{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := laneHistoryEvent{Timestamp: now.Add(-10 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}
	for _, evt := range []laneHistoryEvent{evt1, evt2, evt3} {
		if err := appendLaneHistory(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	filter := laneHistoryFilter{Since: now.Add(-1 * time.Hour)}
	s, err := summarizeLaneHistoryWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with since: %v", err)
	}
	if s.Total != 2 {
		t.Fatalf("total = %d, want 2 (dropped 2h event)", s.Total)
	}
}

func TestSummarizeLaneHistoryWithFilterFailed(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := laneHistoryEvent{Timestamp: now.Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := laneHistoryEvent{Timestamp: now.Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := laneHistoryEvent{Timestamp: now.Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}
	for _, evt := range []laneHistoryEvent{evt1, evt2, evt3} {
		if err := appendLaneHistory(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	filter := laneHistoryFilter{FailedOnly: true}
	s, err := summarizeLaneHistoryWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with failed: %v", err)
	}
	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (dropped 2 successes)", s.Total)
	}
	if s.ByExitCode[0] != 0 {
		t.Fatalf("exit 0 count = %d, want 0", s.ByExitCode[0])
	}
	if s.ByExitCode[1] != 1 {
		t.Fatalf("exit 1 count = %d, want 1", s.ByExitCode[1])
	}
}

func TestSummarizeLaneHistoryWithFilterCombined(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := laneHistoryEvent{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := laneHistoryEvent{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := laneHistoryEvent{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}
	evt4 := laneHistoryEvent{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "implement", Brief: "d.md", ExitCode: 2}
	for _, evt := range []laneHistoryEvent{evt1, evt2, evt3, evt4} {
		if err := appendLaneHistory(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	filter := laneHistoryFilter{Since: now.Add(-1 * time.Hour), FailedOnly: true}
	s, err := summarizeLaneHistoryWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with combined: %v", err)
	}
	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (only evt4)", s.Total)
	}
	if s.ByExitCode[2] != 1 {
		t.Fatalf("exit 2 count = %d, want 1", s.ByExitCode[2])
	}
}

func TestSummarizeLaneHistoryWithFilterNoFilterEqualsOriginal(t *testing.T) {
	root := t.TempDir()
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "implement", Brief: "b.md", ExitCode: 1}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := appendLaneHistory(root, laneHistoryEvent{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3: %v", err)
	}
	sOrig, err := summarizeLaneHistory(root)
	if err != nil {
		t.Fatalf("summarize original: %v", err)
	}
	sFiltered, err := summarizeLaneHistoryWithFilter(root, laneHistoryFilter{})
	if err != nil {
		t.Fatalf("summarize with zero filter: %v", err)
	}
	if sOrig.Total != sFiltered.Total {
		t.Fatalf("total: original=%d, filtered=%d", sOrig.Total, sFiltered.Total)
	}
	for mode, n := range sOrig.ByMode {
		if sFiltered.ByMode[mode] != n {
			t.Fatalf("ByMode[%q]: original=%d, filtered=%d", mode, n, sFiltered.ByMode[mode])
		}
	}
	for code, n := range sOrig.ByExitCode {
		if sFiltered.ByExitCode[code] != n {
			t.Fatalf("ByExitCode[%d]: original=%d, filtered=%d", code, n, sFiltered.ByExitCode[code])
		}
	}
	for path, n := range sOrig.BriefsByPath {
		if sFiltered.BriefsByPath[path] != n {
			t.Fatalf("BriefsByPath[%q]: original=%d, filtered=%d", path, n, sFiltered.BriefsByPath[path])
		}
	}
}
