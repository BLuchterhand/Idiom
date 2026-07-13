package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("read prefetch", func() {
	ginkgo.It("prefetch read files reads named source and skips unsafe kinds", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "logs"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom keeps source evidence fresh.\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "logs", "session.log"), []byte("private log\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0x00, 0x01}, 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("x", 128)), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 128})
		report := mgr.PrefetchReadFiles(context.Background(), []string{
			"README.md",
			"README.md",
			"logs/session.log",
			"binary.dat",
			"large.md",
		}, PrefetchReadOptions{MaxFiles: 6, MaxBytes: 256, MaxBytesPerFile: 64})
		gomega.Expect(report.Results).To(gomega.HaveLen(2), fmt.Sprintf("expected two prefetched results, got %#v", report))
		gomega.Expect(report.Results[0].Tool).To(gomega.Equal(ToolNamePrefetch), fmt.Sprintf("tool = %q", report.Results[0].Tool))
		gomega.Expect(report.Results[0].Summary).To(gomega.Equal("Read f1 README.md lines 1-1 (total lines: 1; source: prefetch)"), fmt.Sprintf("prefetch summary should preserve read_file shape, got %q", report.Results[0].Summary))

		if got, want := report.Results[0].Paths, []string{"README.md"}; len(got) != 1 || got[0] != want[0] {
			ginkgo.Fail(fmt.Sprintf("prefetch result should report typed read path %v, got %#v", want, got))
		}
		gomega.Expect(report.Results[0].Output).To(gomega.ContainSubstring("1:Idiom keeps source evidence fresh."), fmt.Sprintf("prefetch output missing line-numbered source body:\n%s", report.Results[0].Output))
		gomega.Expect(report.Results[1].Summary).To(gomega.ContainSubstring("large.md lines 1-1 (excerpt lines: 1; total lines: 1; bytes: 64/128; source: prefetch; truncated=true)"), fmt.Sprintf("large file should return truncated excerpt summary, got %q", report.Results[1].Summary))
		gomega.Expect(report.Results[1].Output).To(gomega.ContainSubstring("[prefetch excerpt truncated: omitted content after line 1; full file not included in prefetch]"), fmt.Sprintf("large file output missing neutral truncation footer:\n%s", report.Results[1].Output))
		gomega.Expect(report.DuplicatePaths).To(gomega.BeNumerically("==", 1), fmt.Sprintf("duplicate paths = %d, want 1", report.DuplicatePaths))
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 1), fmt.Sprintf("truncated files = %d, want 1", report.TruncatedFiles))

		reasons := map[string]bool{}
		for _, skipped := range report.SkippedPaths {
			reasons[skipped.Reason] = true
		}

		for _, want := range []string{"excluded_logs", "binary"} {
			gomega.Expect(reasons[want]).To(gomega.BeTrue(), fmt.Sprintf("missing skip reason %q in %#v", want, report.SkippedPaths))

		}
	})
	ginkgo.It("prefetch read files uses workspace path policy", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("ok\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		report := mgr.PrefetchReadFiles(context.Background(), []string{filepath.Join(outside, "secret.md"), "README.md"}, PrefetchReadOptions{})
		gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("expected only workspace-local README prefetch, got %#v", report))
		gomega.Expect(report.SelectedPaths[0]).To(gomega.Equal("README.md"), fmt.Sprintf("expected only workspace-local README prefetch, got %#v", report))
		gomega.Expect(report.SkippedPaths).To(gomega.HaveLen(1), fmt.Sprintf("expected outside path resolve failure, got %#v", report.SkippedPaths))
		gomega.Expect(report.SkippedPaths[0].Reason).To(gomega.Equal("resolve_failed"), fmt.Sprintf("expected outside path resolve failure, got %#v", report.SkippedPaths))

	})
	ginkgo.It("prefetch read files truncates multiple large files and keeps small files stable", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "small.md"), []byte("small\n"), 0o644)).To(gomega.Succeed())

		largeA := strings.Join([]string{"alpha", strings.Repeat("a", 160), "omega"}, "\n")
		largeB := strings.Join([]string{"bravo", strings.Repeat("b", 160), "done"}, "\n")
		gomega.Expect(os.WriteFile(filepath.Join(root, "large-a.md"), []byte(largeA), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "large-b.md"), []byte(largeB), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		report := mgr.PrefetchReadFiles(context.Background(), []string{"small.md", "large-a.md", "large-b.md"}, PrefetchReadOptions{
			MaxFiles:        3,
			MaxBytes:        256,
			MaxBytesPerFile: 128,
			MaxExcerptBytes: 16,
		})
		gomega.Expect(report.Results).To(gomega.HaveLen(3), fmt.Sprintf("expected three prefetched results, got %#v", report))
		gomega.Expect(report.Results[0].Summary).To(gomega.Equal("Read f1 small.md lines 1-1 (total lines: 1; source: prefetch)"), fmt.Sprintf("small file summary changed: %q", report.Results[0].Summary))

		for i, result := range report.Results[1:] {
			gomega.Expect(result.Summary).To(gomega.ContainSubstring("truncated=true"), fmt.Sprintf("large result %d was not truncated: %q", i, result.Summary))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("[prefetch excerpt truncated: omitted lines 3-3; full file not included in prefetch]"), fmt.Sprintf("large result %d missing neutral footer:\n%s", i, result.Output))

		}
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 2), fmt.Sprintf("truncated files = %d, want 2", report.TruncatedFiles))

	})
	ginkgo.It("prefetch read files allows read file followup after truncated excerpt", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := strings.Join([]string{"first", strings.Repeat("middle", 40), "last"}, "\n")
		gomega.Expect(os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		report := mgr.PrefetchReadFiles(context.Background(), []string{"large.md"}, PrefetchReadOptions{
			MaxFiles:        1,
			MaxBytes:        128,
			MaxBytesPerFile: 128,
			MaxExcerptBytes: 8,
		})
		gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("expected truncated prefetch, got %#v", report))
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected truncated prefetch, got %#v", report))

		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "large.md", StartLine: 2, EndLine: 3})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file follow-up failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("2:middle"), fmt.Sprintf("read_file follow-up missing requested lines:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("3:last"), fmt.Sprintf("read_file follow-up missing requested lines:\n%s", result.Output))

	})
	ginkgo.Describe("prefetch read files reports budget skips", func() {
		newManager := func() *Manager {
			root := ginkgo.GinkgoT().TempDir()
			for _, name := range []string{"one.md", "two.md", "three.md"} {
				gomega.Expect(os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o644)).To(gomega.Succeed())
			}
			return NewManager(config.Workspace{Root: root, CWD: root})
		}

		ginkgo.It("max files", func() {
			mgr := newManager()
			report := mgr.PrefetchReadFiles(context.Background(), []string{"one.md", "two.md", "three.md"}, PrefetchReadOptions{
				MaxFiles:        1,
				MaxBytes:        128,
				MaxBytesPerFile: 128,
				MaxExcerptBytes: 128,
			})
			gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("results = %d, want 1: %#v", len(report.Results), report))

			if got := skippedReasons(report); got["max_files_exceeded"] != 2 {
				ginkgo.Fail(fmt.Sprintf("skip reasons = %#v, want two max_files_exceeded", got))
			}
		})

		ginkgo.It("max bytes", func() {
			mgr := newManager()
			report := mgr.PrefetchReadFiles(context.Background(), []string{"one.md", "two.md"}, PrefetchReadOptions{
				MaxFiles:        2,
				MaxBytes:        len("one.md\n"),
				MaxBytesPerFile: 128,
				MaxExcerptBytes: 128,
			})
			gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("results = %d, want 1: %#v", len(report.Results), report))

			if got := skippedReasons(report); got["max_bytes_exceeded"] != 1 {
				ginkgo.Fail(fmt.Sprintf("skip reasons = %#v, want one max_bytes_exceeded", got))
			}
		})
	})
	ginkgo.It("prefetch read files normalizes cross constrained budgets", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := strings.Repeat("x", 100)
		gomega.Expect(os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		report := mgr.PrefetchReadFiles(context.Background(), []string{"large.md"}, PrefetchReadOptions{
			MaxFiles:        1,
			MaxBytes:        20,
			MaxBytesPerFile: 50,
			MaxExcerptBytes: 40,
		})
		gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("expected one result, got %#v", report))
		gomega.Expect(report.Bytes).To(gomega.BeNumerically("==", 20), fmt.Sprintf("bytes = %d, want clamp to MaxBytes=20", report.Bytes))
		gomega.Expect(report.Results[0].Summary).To(gomega.ContainSubstring("bytes: 20/100"), fmt.Sprintf("summary did not reflect clamped excerpt budget: %q", report.Results[0].Summary))
		gomega.Expect(report.Results[0].Output).To(gomega.ContainSubstring("[prefetch excerpt truncated: omitted content after line 1; full file not included in prefetch]"), fmt.Sprintf("output missing truncation footer:\n%s", report.Results[0].Output))

	})
	ginkgo.It("prefetch read files unavailable workspace reports every requested path", func() {
		report := (*Manager)(nil).PrefetchReadFiles(context.Background(), []string{"one.md", "two.md"}, PrefetchReadOptions{})
		gomega.Expect(report.Results).To(gomega.BeEmpty(), fmt.Sprintf("nil manager should not return results: %#v", report.Results))

		if got := skippedReasons(report); got["workspace_unavailable"] != 2 {
			ginkgo.Fail(fmt.Sprintf("skip reasons = %#v, want every path marked workspace_unavailable", got))
		}
	})
	ginkgo.It("prefetch read files handles excerpt edges", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "empty.md"), nil, 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "exact.md"), []byte("12345678"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "long-line.md"), []byte("123456789"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "large-binary.bin"), append([]byte{0x00}, []byte("123456789x")...), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		report := mgr.PrefetchReadFiles(context.Background(), []string{"empty.md", "exact.md", "long-line.md", "large-binary.bin"}, PrefetchReadOptions{
			MaxFiles:        4,
			MaxBytes:        128,
			MaxBytesPerFile: 8,
			MaxExcerptBytes: 8,
		})
		gomega.Expect(report.Results).To(gomega.HaveLen(3), fmt.Sprintf("expected three text results, got %#v", report))
		gomega.Expect(report.Results[0].Summary).To(gomega.Equal("Read f1 empty.md lines 0-0 (total lines: 0; source: prefetch)"), fmt.Sprintf("empty summary = %q", report.Results[0].Summary))
		gomega.Expect(report.Results[1].Summary).NotTo(gomega.ContainSubstring("truncated=true"), fmt.Sprintf("exact-budget file should not be truncated: %q", report.Results[1].Summary))
		gomega.Expect(report.Results[2].Summary).To(gomega.ContainSubstring("truncated=true"), fmt.Sprintf("long single-line file should be truncated: %q", report.Results[2].Summary))
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 1), fmt.Sprintf("truncated files = %d, want 1", report.TruncatedFiles))
		gomega.Expect(report.SkippedPaths).To(gomega.HaveLen(1), fmt.Sprintf("binary should be rejected before excerpt construction, got %#v", report.SkippedPaths))
		gomega.Expect(report.SkippedPaths[0].Reason).To(gomega.Equal("binary"), fmt.Sprintf("binary should be rejected before excerpt construction, got %#v", report.SkippedPaths))

	})
	ginkgo.It("prefetch read files includes full file when it fits budget", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := "package app\n" + strings.Repeat("var x = 1\n", 1200)
		gomega.Expect(len(body) >= DefaultPrefetchMaxBytesPerFile).To(gomega.BeFalse(), fmt.Sprintf("test fixture should fit default per-file prefetch budget, got %d", len(body)))
		gomega.Expect(len(body) <= DefaultPrefetchMaxExcerptBytes).To(gomega.BeFalse(), fmt.Sprintf("test fixture should exceed old excerpt-only prefetch cap, got %d", len(body)))
		gomega.Expect(os.WriteFile(filepath.Join(root, "catalog.go"), []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		report := mgr.PrefetchReadFiles(context.Background(), []string{"catalog.go"}, PrefetchReadOptions{})
		gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("expected one result, got %#v", report))
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 0), fmt.Sprintf("file fits prefetch budget and should not be truncated: %#v", report))
		gomega.Expect(report.Results[0].Summary).NotTo(gomega.ContainSubstring("truncated=true"), fmt.Sprintf("summary should not report truncation for budget-fitting file: %q", report.Results[0].Summary))
		gomega.Expect(report.Results[0].Output).To(gomega.ContainSubstring("1201:var x = 1"), fmt.Sprintf("prefetch should include the full file body when it fits budget, got output tail:\n%s", report.Results[0].Output))

	})
	ginkgo.It("prefetch read files does not truncate long lines when full file fits budget", func() {
		root := ginkgo.GinkgoT().TempDir()
		longLine := "var catalog = \"" + strings.Repeat("x", DefaultPrefetchMaxExcerptBytes+512) + "\""
		body := "package app\n" + longLine + "\n"
		gomega.Expect(len(body) >= DefaultPrefetchMaxBytesPerFile).To(gomega.BeFalse(), fmt.Sprintf("test fixture should fit default per-file prefetch budget, got %d", len(body)))
		gomega.Expect(os.WriteFile(filepath.Join(root, "catalog.go"), []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		report := mgr.PrefetchReadFiles(context.Background(), []string{"catalog.go"}, PrefetchReadOptions{})
		gomega.Expect(report.Results).To(gomega.HaveLen(1), fmt.Sprintf("expected one result, got %#v", report))
		gomega.Expect(report.TruncatedFiles).To(gomega.BeNumerically("==", 0), fmt.Sprintf("file fits prefetch budget and should not be truncated: %#v", report))
		gomega.Expect(report.Results[0].Output).NotTo(gomega.ContainSubstring(lineTruncationMarker), fmt.Sprintf("full-file prefetch should not truncate long source lines, got output: %q", report.Results[0].Output))
		gomega.Expect(report.Results[0].Output).To(gomega.ContainSubstring("2:"+longLine), fmt.Sprintf("prefetch should include the full long line when file fits budget, got %d chars", len(report.Results[0].Output)))

	})
})

func skippedReasons(report PrefetchReadReport) map[string]int {
	reasons := make(map[string]int, len(report.SkippedPaths))
	for _, skipped := range report.SkippedPaths {
		reasons[skipped.Reason]++
	}

	return reasons
}
