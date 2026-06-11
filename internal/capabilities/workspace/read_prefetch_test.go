package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestPrefetchReadFilesReadsNamedSourceAndSkipsUnsafeKinds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom keeps source evidence fresh.\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "logs", "session.log"), []byte("private log\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("x", 128)), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 128})
	report := mgr.PrefetchReadFiles([]string{
		"README.md",
		"README.md",
		"logs/session.log",
		"binary.dat",
		"large.md",
	}, PrefetchReadOptions{MaxFiles: 6, MaxBytes: 256, MaxBytesPerFile: 64})

	if len(report.Results) != 2 {
		t.Fatalf("expected two prefetched results, got %#v", report)
	}
	if report.Results[0].Tool != ToolNamePrefetch {
		t.Fatalf("tool = %q", report.Results[0].Tool)
	}
	if report.Results[0].Summary != "Read f1 README.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("prefetch summary should preserve read_file shape, got %q", report.Results[0].Summary)
	}
	if !strings.Contains(report.Results[0].Output, "1:Idiom keeps source evidence fresh.") {
		t.Fatalf("prefetch output missing line-numbered source body:\n%s", report.Results[0].Output)
	}
	if !strings.Contains(report.Results[1].Summary, "large.md lines 1-1 (excerpt lines: 1; total lines: 1; bytes: 64/128; source: prefetch; truncated=true)") {
		t.Fatalf("large file should return truncated excerpt summary, got %q", report.Results[1].Summary)
	}
	if !strings.Contains(report.Results[1].Output, "[prefetch excerpt truncated: omitted content after line 1; full file not included in prefetch]") {
		t.Fatalf("large file output missing neutral truncation footer:\n%s", report.Results[1].Output)
	}
	if report.DuplicatePaths != 1 {
		t.Fatalf("duplicate paths = %d, want 1", report.DuplicatePaths)
	}
	if report.TruncatedFiles != 1 {
		t.Fatalf("truncated files = %d, want 1", report.TruncatedFiles)
	}
	reasons := map[string]bool{}
	for _, skipped := range report.SkippedPaths {
		reasons[skipped.Reason] = true
	}
	for _, want := range []string{"excluded_logs", "binary"} {
		if !reasons[want] {
			t.Fatalf("missing skip reason %q in %#v", want, report.SkippedPaths)
		}
	}
}

func TestPrefetchReadFilesUsesWorkspacePathPolicy(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	report := mgr.PrefetchReadFiles([]string{filepath.Join(outside, "secret.md"), "README.md"}, PrefetchReadOptions{})
	if len(report.Results) != 1 || report.SelectedPaths[0] != "README.md" {
		t.Fatalf("expected only workspace-local README prefetch, got %#v", report)
	}
	if len(report.SkippedPaths) != 1 || report.SkippedPaths[0].Reason != "resolve_failed" {
		t.Fatalf("expected outside path resolve failure, got %#v", report.SkippedPaths)
	}
}

func TestPrefetchReadFilesTruncatesMultipleLargeFilesAndKeepsSmallFilesStable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "small.md"), []byte("small\n"), 0o644); err != nil {
		t.Fatalf("write small: %v", err)
	}
	largeA := strings.Join([]string{"alpha", strings.Repeat("a", 80), "omega"}, "\n")
	largeB := strings.Join([]string{"bravo", strings.Repeat("b", 80), "done"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "large-a.md"), []byte(largeA), 0o644); err != nil {
		t.Fatalf("write large-a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large-b.md"), []byte(largeB), 0o644); err != nil {
		t.Fatalf("write large-b: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"small.md", "large-a.md", "large-b.md"}, PrefetchReadOptions{
		MaxFiles:        3,
		MaxBytes:        256,
		MaxBytesPerFile: 128,
		MaxExcerptBytes: 16,
	})

	if len(report.Results) != 3 {
		t.Fatalf("expected three prefetched results, got %#v", report)
	}
	if report.Results[0].Summary != "Read f1 small.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("small file summary changed: %q", report.Results[0].Summary)
	}
	for i, result := range report.Results[1:] {
		if !strings.Contains(result.Summary, "truncated=true") {
			t.Fatalf("large result %d was not truncated: %q", i, result.Summary)
		}
		if !strings.Contains(result.Output, "[prefetch excerpt truncated: omitted lines 3-3; full file not included in prefetch]") {
			t.Fatalf("large result %d missing neutral footer:\n%s", i, result.Output)
		}
	}
	if report.TruncatedFiles != 2 {
		t.Fatalf("truncated files = %d, want 2", report.TruncatedFiles)
	}
}

func TestPrefetchReadFilesAllowsReadFileFollowupAfterTruncatedExcerpt(t *testing.T) {
	root := t.TempDir()
	body := strings.Join([]string{"first", "middle", "last"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"large.md"}, PrefetchReadOptions{
		MaxFiles:        1,
		MaxBytes:        128,
		MaxBytesPerFile: 128,
		MaxExcerptBytes: 8,
	})
	if len(report.Results) != 1 || report.TruncatedFiles != 1 {
		t.Fatalf("expected truncated prefetch, got %#v", report)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "large.md", StartLine: 2, EndLine: 3})
	if result.Error != "" {
		t.Fatalf("read_file follow-up failed: %#v", result)
	}
	if !strings.Contains(result.Output, "2:middle") || !strings.Contains(result.Output, "3:last") {
		t.Fatalf("read_file follow-up missing requested lines:\n%s", result.Output)
	}
}

func TestPrefetchReadFilesReportsBudgetSkips(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.md", "two.md", "three.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	t.Run("max files", func(t *testing.T) {
		report := mgr.PrefetchReadFiles([]string{"one.md", "two.md", "three.md"}, PrefetchReadOptions{
			MaxFiles:        1,
			MaxBytes:        128,
			MaxBytesPerFile: 128,
			MaxExcerptBytes: 128,
		})
		if len(report.Results) != 1 {
			t.Fatalf("results = %d, want 1: %#v", len(report.Results), report)
		}
		if got := skippedReasons(report); got["max_files_exceeded"] != 2 {
			t.Fatalf("skip reasons = %#v, want two max_files_exceeded", got)
		}
	})

	t.Run("max bytes", func(t *testing.T) {
		report := mgr.PrefetchReadFiles([]string{"one.md", "two.md"}, PrefetchReadOptions{
			MaxFiles:        2,
			MaxBytes:        len("one.md\n"),
			MaxBytesPerFile: 128,
			MaxExcerptBytes: 128,
		})
		if len(report.Results) != 1 {
			t.Fatalf("results = %d, want 1: %#v", len(report.Results), report)
		}
		if got := skippedReasons(report); got["max_bytes_exceeded"] != 1 {
			t.Fatalf("skip reasons = %#v, want one max_bytes_exceeded", got)
		}
	})
}

func TestPrefetchReadFilesNormalizesCrossConstrainedBudgets(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("x", 100)
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	report := mgr.PrefetchReadFiles([]string{"large.md"}, PrefetchReadOptions{
		MaxFiles:        1,
		MaxBytes:        20,
		MaxBytesPerFile: 50,
		MaxExcerptBytes: 40,
	})
	if len(report.Results) != 1 {
		t.Fatalf("expected one result, got %#v", report)
	}
	if report.Bytes != 20 {
		t.Fatalf("bytes = %d, want clamp to MaxBytes=20", report.Bytes)
	}
	if !strings.Contains(report.Results[0].Summary, "bytes: 20/100") {
		t.Fatalf("summary did not reflect clamped excerpt budget: %q", report.Results[0].Summary)
	}
	if !strings.Contains(report.Results[0].Output, "[prefetch excerpt truncated: omitted content after line 1; full file not included in prefetch]") {
		t.Fatalf("output missing truncation footer:\n%s", report.Results[0].Output)
	}
}

func TestPrefetchReadFilesUnavailableWorkspaceReportsEveryRequestedPath(t *testing.T) {
	report := (*Manager)(nil).PrefetchReadFiles([]string{"one.md", "two.md"}, PrefetchReadOptions{})
	if len(report.Results) != 0 {
		t.Fatalf("nil manager should not return results: %#v", report.Results)
	}
	if got := skippedReasons(report); got["workspace_unavailable"] != 2 {
		t.Fatalf("skip reasons = %#v, want every path marked workspace_unavailable", got)
	}
}

func TestPrefetchReadFilesHandlesExcerptEdges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.md"), nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "exact.md"), []byte("12345678"), 0o644); err != nil {
		t.Fatalf("write exact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "long-line.md"), []byte("123456789"), 0o644); err != nil {
		t.Fatalf("write long-line: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large-binary.bin"), append([]byte("123456789"), 0x00, 'x'), 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"empty.md", "exact.md", "long-line.md", "large-binary.bin"}, PrefetchReadOptions{
		MaxFiles:        4,
		MaxBytes:        128,
		MaxBytesPerFile: 32,
		MaxExcerptBytes: 8,
	})

	if len(report.Results) != 3 {
		t.Fatalf("expected three text results, got %#v", report)
	}
	if report.Results[0].Summary != "Read f1 empty.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("empty summary = %q", report.Results[0].Summary)
	}
	if strings.Contains(report.Results[1].Summary, "truncated=true") {
		t.Fatalf("exact-budget file should not be truncated: %q", report.Results[1].Summary)
	}
	if !strings.Contains(report.Results[2].Summary, "truncated=true") {
		t.Fatalf("long single-line file should be truncated: %q", report.Results[2].Summary)
	}
	if report.TruncatedFiles != 1 {
		t.Fatalf("truncated files = %d, want 1", report.TruncatedFiles)
	}
	if len(report.SkippedPaths) != 1 || report.SkippedPaths[0].Reason != "binary" {
		t.Fatalf("binary should be rejected before excerpt construction, got %#v", report.SkippedPaths)
	}
}

func skippedReasons(report PrefetchReadReport) map[string]int {
	reasons := make(map[string]int, len(report.SkippedPaths))
	for _, skipped := range report.SkippedPaths {
		reasons[skipped.Reason]++
	}
	return reasons
}
