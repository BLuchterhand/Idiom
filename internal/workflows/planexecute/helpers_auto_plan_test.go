package planexecute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
)

var _ = ginkgo.It("plan execute exact plan edits parses blockquoted required behavior new file body", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir writerroute: %v", err))
	}

	prompt := []string{
		"# Objective",
		"",
		"## Referenced Plan Documents",
		"",
		"> # Refined Atom",
		">",
		"> ## Files To Modify",
		">",
		"> | File | Change |",
		"> |---|---|",
		"> | `internal/contracts/writerroute/decision.go` | NEW FILE: add route decision type |",
		">",
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/contracts/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"internal/contracts/writerroute/decision.go"}, root)
	gomega.Expect(reason != "").To(gomega.BeFalse(), fmt.Sprintf("unexpected reason: %s", reason))
	gomega.Expect(len(edits) != 1).To(gomega.BeFalse(), fmt.Sprintf("edits = %d, want 1: %#v", len(edits), edits))

	edit := edits[0]
	gomega.Expect(edit.Path).To(gomega.Equal("internal/contracts/writerroute/decision.go"), fmt.Sprintf("unexpected edit shape: %#v", edit))
	gomega.Expect(edit.ReplaceAll).To(gomega.BeTrue(), fmt.Sprintf("unexpected edit shape: %#v", edit))
	gomega.Expect(edit.OldText).To(gomega.Equal(""), fmt.Sprintf("unexpected edit shape: %#v", edit))
	gomega.Expect(edit.NewText != "package writerroute\n\nconst SchemaVersion = \"idiom.writer_route.v1\"").To(gomega.BeFalse(), fmt.Sprintf("unexpected new_text: %q", edit.NewText))
})

var _ = ginkgo.It("plan execute exact plan edits binds implicit body to nearest path mention", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir writerroute: %v", err))
	}

	prompt := []string{
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/contracts/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
		">",
		"> 2. Create `internal/contracts/writerroute/decision_test.go` with tests:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> import \"testing\"",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"internal/contracts/writerroute/decision.go"}, root)
	gomega.Expect(reason != "").To(gomega.BeFalse(), fmt.Sprintf("unexpected reason: %s", reason))
	gomega.Expect(len(edits) != 1).To(gomega.BeFalse(), fmt.Sprintf("edits = %d, want 1: %#v", len(edits), edits))
	gomega.Expect(strings.Contains(edits[0].NewText, "testing")).To(gomega.BeFalse(), fmt.Sprintf("implicit body parser captured out-of-scope test block: %q", edits[0].NewText))
})

var _ = ginkgo.It("plan execute exact plan edits does not infer implicit body for existing path", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir cmd/idiom: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "cmd", "idiom", "lane.go"), []byte("package main\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write lane.go: %v", err))
	}

	prompt := []string{
		"## Referenced Plan Documents",
		"",
		"> ## Required Behavior",
		">",
		"> 1. Update `cmd/idiom/lane.go` with this illustrative insertion shape:",
		">",
		"> ```text",
		"> package writerroute",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"cmd/idiom/lane.go"}, root)
	gomega.Expect(len(edits) != 0).To(gomega.BeFalse(), fmt.Sprintf("implicit body parser must not replace existing paths: %#v", edits))
	gomega.Expect(reason == "").To(gomega.BeFalse(), fmt.Sprintf("expected skip reason for non-exact prompt"))
})

var _ = ginkgo.It("plan execute classify auto exact plan edits detects already applied and pending", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "applied.txt"), []byte("new\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write applied: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pending.txt"), []byte("old\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write pending: %v", err))
	}

	result := planExecuteClassifyAutoExactPlanEdits(root, []planartifact.PlanEdit{
		{Path: "applied.txt", OldText: "old\n", NewText: "new\n"},
		{Path: "pending.txt", OldText: "old\n", NewText: "new\n"},
	})
	gomega.Expect(result.AppliedCount).To(gomega.Equal(1), fmt.Sprintf("unexpected classification: %#v", result))
	gomega.Expect(result.PendingCount).To(gomega.Equal(1), fmt.Sprintf("unexpected classification: %#v", result))
	gomega.Expect(result.UnknownCount).To(gomega.Equal(0), fmt.Sprintf("unexpected classification: %#v", result))
	gomega.Expect(result.AppliedPaths).To(gomega.HaveLen(1), fmt.Sprintf("applied paths = %#v", result.AppliedPaths))
	gomega.Expect(result.AppliedPaths[0]).To(gomega.Equal("applied.txt"), fmt.Sprintf("applied paths = %#v", result.AppliedPaths))
	gomega.Expect(result.PendingPaths).To(gomega.HaveLen(1), fmt.Sprintf("pending paths = %#v", result.PendingPaths))
	gomega.Expect(result.PendingPaths[0]).To(gomega.Equal("pending.txt"), fmt.Sprintf("pending paths = %#v", result.PendingPaths))
})

var _ = ginkgo.It("plan execute try auto exact plan source satisfied when all edits already applied", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write a: %v", err))
	}

	cfg := testPlanExecuteConfig(root)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"

	result := planExecuteTryAutoExactPlan(context.Background(), cfg, prompt, []string{"a.txt"}, planExecuteTestDependencies().SubmitAutoPlan)
	gomega.Expect(result.SourceSatisfied).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied without submission, got %#v", result))
	gomega.Expect(result.Submitted).To(gomega.BeFalse(), fmt.Sprintf("expected source-satisfied without submission, got %#v", result))
	gomega.Expect(result.AppliedCount).To(gomega.Equal(1), fmt.Sprintf("unexpected applied classification: %#v", result))
	gomega.Expect(result.AppliedPaths).To(gomega.HaveLen(1), fmt.Sprintf("unexpected applied classification: %#v", result))
	gomega.Expect(result.AppliedPaths[0]).To(gomega.Equal("a.txt"), fmt.Sprintf("unexpected applied classification: %#v", result))
})

func testPlanExecuteConfig(root string) config.Config {
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	return cfg
}
