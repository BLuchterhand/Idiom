package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
)

func gitWorkspace() (*Manager, string) {
	ginkgo.GinkgoHelper()

	if _, err := exec.LookPath("git"); err != nil {
		ginkgo.Skip("git binary not available")
	}

	dir := ginkgo.GinkgoT().TempDir()

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v\n%s", args, err, out))
		}
	}
	gomega.Expect(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("hello\n"), 0o644)).To(gomega.Succeed())

	for _, args := range [][]string{
		{"add", "tracked.txt"},
		{"commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v\n%s", args, err, out))
		}
	}

	cfg := config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableShell:           true,
		EnableWrites:          true,
	}

	return NewManager(cfg), dir
}

var _ = ginkgo.Describe("git tools", func() {
	ginkgo.It("git status reports modified and untracked", func() {
		mgr, dir := gitWorkspace()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644)).To(gomega.Succeed())

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_status"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("git_status error: %#v", result))
		gomega.Expect(result.Tool).To(gomega.Equal(ToolNameGitStatus), fmt.Sprintf("git_status identity/summary = %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Read git status in "), fmt.Sprintf("git_status identity/summary = %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("tracked.txt"), fmt.Sprintf("git_status output missing modified file: %q", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("new.txt"), fmt.Sprintf("git_status output missing untracked file: %q", result.Output))

	})
	ginkgo.It("git diff shows working tree change", func() {
		mgr, dir := gitWorkspace()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644)).To(gomega.Succeed())

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("git_diff error: %#v", result))
		gomega.Expect(result.Tool).To(gomega.Equal(ToolNameGitDiff), fmt.Sprintf("git_diff identity/summary = %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Read git diff for working tree in "), fmt.Sprintf("git_diff identity/summary = %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("-hello"), fmt.Sprintf("git_diff missing expected hunk: %q", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("+changed"), fmt.Sprintf("git_diff missing expected hunk: %q", result.Output))

	})
	ginkgo.It("git diff scoped to path", func() {
		mgr, dir := gitWorkspace()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "other.txt"), []byte("untracked\n"), 0o644)).To(gomega.Succeed())

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "tracked.txt"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("git_diff error: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("tracked.txt"), fmt.Sprintf("git_diff scoped output missing target: %q", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("other.txt"), fmt.Sprintf("git_diff scoped output should not include other.txt: %q", result.Output))

	})
	ginkgo.It("git diff rejects path traversal", func() {
		mgr, _ := gitWorkspace()
		mgr.writeGuard.NoteEditRecoveryRequired(filepath.Join(mgr.Root(), "tracked.txt"), 1, "needs recovery")

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "../escape"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), "expected path-traversal rejection")

		if _, ok := mgr.writeGuard.PendingEditRecovery(filepath.Join(mgr.Root(), "tracked.txt")); !ok {
			ginkgo.Fail("path-rejected git_diff must not clear edit recovery")
		}
	})
	ginkgo.It("git diff clears edit recovery after successful diff", func() {
		mgr, dir := gitWorkspace()
		otherPath := filepath.Join(dir, "other.txt")
		gomega.Expect(os.WriteFile(otherPath, []byte("other\n"), 0o644)).To(gomega.Succeed())

		runWorkspaceGit(dir, "add", "other.txt")
		runWorkspaceGit(dir, "commit", "-q", "-m", "other")

		trackedPath, err := mgr.pathAccess.ResolvePath("tracked.txt", "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve tracked path: %v", err))

		otherResolved, err := mgr.pathAccess.ResolvePath("other.txt", "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve other path: %v", err))

		mgr.writeGuard.NoteEditRecoveryRequired(trackedPath, 1, "tracked needs recovery")
		mgr.writeGuard.NoteEditRecoveryRequired(otherResolved, 1, "other needs recovery")
		gomega.Expect(os.WriteFile(trackedPath, []byte("changed\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(otherPath, []byte("changed other\n"), 0o644)).To(gomega.Succeed())

		scoped := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "tracked.txt"})
		gomega.Expect(scoped.Error).To(gomega.Equal(""), fmt.Sprintf("scoped git_diff error: %#v", scoped))

		if _, ok := mgr.writeGuard.PendingEditRecovery(trackedPath); ok {
			ginkgo.Fail("successful scoped git_diff should clear target edit recovery")
		}
		if _, ok := mgr.writeGuard.PendingEditRecovery(otherResolved); !ok {
			ginkgo.Fail("successful scoped git_diff must not clear unrelated edit recovery")
		}

		full := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff"})
		gomega.Expect(full.Error).To(gomega.Equal(""), fmt.Sprintf("full git_diff error: %#v", full))

		if _, ok := mgr.writeGuard.PendingEditRecovery(otherResolved); ok {
			ginkgo.Fail("successful full git_diff should clear all edit recovery")
		}
	})

	ginkgo.It("reports only tracked working-tree paths actually inspected by git diff check", func() {
		mgr, dir := gitWorkspace()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0o644)).To(gomega.Succeed())

		unscoped := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --check"})
		gomega.Expect(unscoped.Error).To(gomega.BeEmpty(), fmt.Sprintf("git diff --check failed: %#v", unscoped))
		gomega.Expect(unscoped.Facts).NotTo(gomega.BeNil())
		gomega.Expect(unscoped.Facts.VerificationEvidence).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
			Paths: []string{"tracked.txt"},
		}}))

		scoped := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --check -- tracked.txt untracked.txt"})
		gomega.Expect(scoped.Error).To(gomega.BeEmpty(), fmt.Sprintf("scoped git diff --check failed: %#v", scoped))
		gomega.Expect(scoped.Facts.VerificationEvidence).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
			Paths: []string{"tracked.txt"},
		}}))
		observed := AnalyzePostWriteVerificationResult(scoped, []string{"tracked.txt", "untracked.txt"})
		gomega.Expect(observed.CoveredPendingPaths).To(gomega.Equal([]string{"tracked.txt"}))

		untrackedOnly := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --check -- untracked.txt"})
		gomega.Expect(untrackedOnly.Error).To(gomega.BeEmpty(), fmt.Sprintf("untracked-only git diff --check failed: %#v", untrackedOnly))
		gomega.Expect(untrackedOnly.Facts.VerificationEvidence).To(gomega.BeEmpty())
		observed = AnalyzePostWriteVerificationResult(untrackedOnly, []string{"untracked.txt"})
		gomega.Expect(observed.Attempted).To(gomega.BeTrue())
		gomega.Expect(observed.CoveredPendingPaths).To(gomega.BeEmpty())
		gomega.Expect(observed.Successful).To(gomega.BeFalse())

		runWorkspaceGit(dir, "add", "tracked.txt")
		stagedOnly := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --check -- tracked.txt"})
		gomega.Expect(stagedOnly.Error).To(gomega.BeEmpty(), fmt.Sprintf("staged-only git diff --check failed: %#v", stagedOnly))
		gomega.Expect(stagedOnly.Facts.VerificationEvidence).To(gomega.BeEmpty())
		cached := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --cached --check -- tracked.txt"})
		gomega.Expect(cached.Error).To(gomega.BeEmpty(), fmt.Sprintf("cached git diff --check failed: %#v", cached))
		gomega.Expect(cached.Facts.VerificationEvidence).To(gomega.BeEmpty())

		runWorkspaceGit(dir, "commit", "-q", "-m", "changed tracked file")
		historical := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --check HEAD~1 HEAD -- tracked.txt"})
		gomega.Expect(historical.Error).To(gomega.BeEmpty(), fmt.Sprintf("historical git diff --check failed: %#v", historical))
		gomega.Expect(historical.Facts.VerificationEvidence).To(gomega.BeEmpty())
	})
})

func runWorkspaceGit(dir string, args ...string) {
	ginkgo.GinkgoHelper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		ginkgo.Fail(fmt.Sprintf("git %v: %v\n%s", args, err, out))
	}
}
