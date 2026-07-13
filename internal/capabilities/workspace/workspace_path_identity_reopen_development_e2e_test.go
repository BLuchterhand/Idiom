package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
)

var _ = ginkgo.Describe(
	"workspace path identity reopened contracts",
	ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"),
	func() {
		ginkgo.It("rejects target replacement at the final atomic-write boundary", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, "target.txt")
			original := filepath.Join(root, "target-original.txt")
			writeTestFile(target, "approved\n")

			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.Path == "target.txt" {
					once.Do(func() {
						gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
						writeTestFile(target, "replacement\n")
					})
				}
			})

			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "target.txt", Content: "new body\n", Description: "final target binding"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target identity changed since preflight"), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(target)).To(gomega.Equal("replacement\n"))
			gomega.Expect(readTestFile(original)).To(gomega.Equal("approved\n"))
		})

		ginkgo.It("rejects target replacement at the final delete boundary", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, "victim.txt")
			original := filepath.Join(root, "victim-original.txt")
			writeTestFile(target, "approved victim\n")

			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.Path == "victim.txt" {
					once.Do(func() {
						gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
						writeTestFile(target, "replacement victim\n")
					})
				}
			})

			patch := "*** Begin Patch\n*** Delete File: victim.txt\n*** End Patch"
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: patch, Description: "final delete binding"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target identity changed since preflight"), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(target)).To(gomega.Equal("replacement victim\n"))
			gomega.Expect(readTestFile(original)).To(gomega.Equal("approved victim\n"))
		})

		ginkgo.It("rejects a workspace-root replacement at the final delete boundary", func() {
			parent := ginkgo.GinkgoT().TempDir()
			root := filepath.Join(parent, "workspace")
			originalRoot := filepath.Join(parent, "workspace-original")
			target := filepath.Join(root, "victim.txt")
			writeTestFile(target, "approved victim\n")

			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.Path == "victim.txt" {
					once.Do(func() {
						gomega.Expect(os.Rename(root, originalRoot)).To(gomega.Succeed())
						writeTestFile(filepath.Join(root, "victim.txt"), "replacement victim\n")
					})
				}
			})

			patch := "*** Begin Patch\n*** Delete File: victim.txt\n*** End Patch"
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: patch, Description: "final root binding"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("root path no longer names"), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(filepath.Join(root, "victim.txt"))).To(gomega.Equal("replacement victim\n"))
			gomega.Expect(readTestFile(filepath.Join(originalRoot, "victim.txt"))).To(gomega.Equal("approved victim\n"))
		})

		ginkgo.It("does not leak a replacement body through selected-file approval", func() {
			parent := ginkgo.GinkgoT().TempDir()
			root := filepath.Join(parent, "workspace")
			target := filepath.Join(parent, "selected.txt")
			original := filepath.Join(parent, "selected-original.txt")
			writeTestFile(filepath.Join(root, "README.md"), "workspace\n")
			writeTestFile(target, "approved body\n")

			manager := NewManager(config.Workspace{Root: root, CWD: root})
			approver := newSystemSearchApproverMock(manager)
			expectSystemSearchApproval(manager, approver, target, "read selected identity", SystemSearchAccessScopeSelected)
			approval := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSystemSearchAccess, Path: target, Purpose: "read selected identity"})
			gomega.Expect(approval.Error).To(gomega.BeEmpty())

			var once sync.Once
			ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
				if checkpoint.Stage == workspaceReadBeforeOpen {
					once.Do(func() {
						gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
						writeTestFile(target, "selected replacement secret\n")
					})
				}
			})
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameReadFile, Path: target})
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("selected replacement secret"))
		})

		ginkgo.It("does not leak a replaced descendant through contents approval", func() {
			parent := ginkgo.GinkgoT().TempDir()
			root := filepath.Join(parent, "workspace")
			approvedRoot := filepath.Join(parent, "approved")
			target := filepath.Join(approvedRoot, "evidence.txt")
			original := filepath.Join(approvedRoot, "evidence-original.txt")
			writeTestFile(filepath.Join(root, "README.md"), "workspace\n")
			writeTestFile(target, "approved evidence\n")

			manager := NewManager(config.Workspace{Root: root, CWD: root})
			approver := newSystemSearchApproverMock(manager)
			expectSystemSearchApproval(manager, approver, approvedRoot, "read approved contents", SystemSearchAccessScopeContents)
			approval := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSystemSearchAccess, Path: approvedRoot, Purpose: "read approved contents"})
			gomega.Expect(approval.Error).To(gomega.BeEmpty())

			var once sync.Once
			ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
				if checkpoint.Stage == workspaceReadBeforeOpen {
					once.Do(func() {
						gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
						writeTestFile(target, "contents replacement secret\n")
					})
				}
			})
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameReadFile, Path: target})
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("contents replacement secret"))
		})

		ginkgo.It("does not leak an ordinary workspace-file replacement", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, "ordinary.txt")
			original := filepath.Join(root, "ordinary-original.txt")
			writeTestFile(target, "ordinary approved\n")
			manager := NewManager(config.Workspace{Root: root, CWD: root})

			var once sync.Once
			ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
				if checkpoint.Stage == workspaceReadBeforeOpen {
					once.Do(func() {
						gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
						writeTestFile(target, "ordinary replacement secret\n")
					})
				}
			})
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameReadFile, Path: "ordinary.txt"})
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("ordinary replacement secret"))
		})

		ginkgo.It("does not leak a workspace symlink retarget", func() {
			requireWorkspaceSymlinks()
			root := ginkgo.GinkgoT().TempDir()
			approved := filepath.Join(root, "approved.txt")
			replacement := filepath.Join(root, "replacement.txt")
			alias := filepath.Join(root, "alias.txt")
			writeTestFile(approved, "approved through alias\n")
			writeTestFile(replacement, "symlink replacement secret\n")
			gomega.Expect(os.Symlink("approved.txt", alias)).To(gomega.Succeed())
			manager := NewManager(config.Workspace{Root: root, CWD: root})

			var once sync.Once
			ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
				if checkpoint.Stage == workspaceReadBeforeOpen {
					once.Do(func() {
						gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
						gomega.Expect(os.Symlink("replacement.txt", alias)).To(gomega.Succeed())
					})
				}
			})
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameReadFile, Path: "alias.txt"})
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("symlink replacement secret"))
		})

		ginkgo.It("cancels a mutation waiting for authority without a later commit", func(specCtx ginkgo.SpecContext) {
			root := ginkgo.GinkgoT().TempDir()
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			firstHeld := make(chan struct{})
			releaseFirst := make(chan struct{})
			var heldOnce sync.Once
			firstCtx := withWorkspaceMutationCheckpoint(specCtx, func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationAfterPreflight && checkpoint.Path == "first.txt" {
					heldOnce.Do(func() { close(firstHeld) })
					<-releaseFirst
				}
			})
			firstResult := make(chan ToolResult, 1)
			go func() {
				firstResult <- manager.Execute(firstCtx, ToolRequest{Tool: ToolNameWriteFile, Path: "first.txt", Content: "first\n", Description: "hold mutation authority"})
			}()
			select {
			case <-firstHeld:
			case <-specCtx.Done():
				ginkgo.Fail("first mutation never reached the held preflight checkpoint")
			}

			waiting := make(chan struct{})
			secondBase, cancelSecond := context.WithCancel(specCtx)
			var waitingOnce sync.Once
			secondCtx := withWorkspaceMutationCheckpoint(secondBase, func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationWaitingForGate {
					waitingOnce.Do(func() { close(waiting) })
					cancelSecond()
				}
			})
			secondResult := make(chan ToolResult, 1)
			go func() {
				secondResult <- manager.Execute(secondCtx, ToolRequest{Tool: ToolNameWriteFile, Path: "second.txt", Content: "second\n", Description: "cancel queued mutation"})
			}()
			select {
			case <-waiting:
			case <-specCtx.Done():
				ginkgo.Fail("second mutation never waited for authority")
			}
			var second ToolResult
			select {
			case second = <-secondResult:
			case <-specCtx.Done():
				ginkgo.Fail("canceled waiter did not return")
			}
			close(releaseFirst)
			var first ToolResult
			select {
			case first = <-firstResult:
			case <-specCtx.Done():
				ginkgo.Fail("first mutation did not finish after release")
			}

			gomega.Expect(first.Error).To(gomega.BeEmpty(), fmt.Sprintf("first result: %#v", first))
			gomega.Expect(failures.CodeOf(second.Failure)).To(gomega.Equal(failures.Canceled), fmt.Sprintf("second result: %#v", second))
			gomega.Expect(second.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(filepath.Join(root, "second.txt")).NotTo(gomega.BeAnExistingFile())
		}, ginkgo.NodeTimeout(10*time.Second))

		ginkgo.It("cancels after preflight without committing", func() {
			root := ginkgo.GinkgoT().TempDir()
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			baseCtx, cancel := context.WithCancel(context.Background())
			ctx := withWorkspaceMutationCheckpoint(baseCtx, func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationAfterPreflight && checkpoint.Path == "canceled.txt" {
					cancel()
				}
			})

			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "canceled.txt", Content: "must not commit\n", Description: "cancel after preflight"})
			gomega.Expect(failures.CodeOf(result.Failure)).To(gomega.Equal(failures.Canceled), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(filepath.Join(root, "canceled.txt")).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("reports exact partial state when cancellation stops the second patch operation", func() {
			root := ginkgo.GinkgoT().TempDir()
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				writeTestFile(filepath.Join(root, name), "before\n")
			}
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			baseCtx, cancel := context.WithCancel(context.Background())
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(baseCtx, func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.OperationIndex == 2 && checkpoint.Path == "b.txt" {
					once.Do(cancel)
				}
			})

			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: threeFileUpdatePatch("a.txt", "b.txt", "c.txt"), Description: "cancel partial patch"})
			gomega.Expect(failures.CodeOf(result.Failure)).To(gomega.Equal(failures.Canceled), fmt.Sprintf("result: %#v", result))
			gomega.Expect(result.Summary).To(gomega.Equal("Patch partially applied"))
			gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"a.txt"}))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("unattempted after failure:"))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("update c.txt"))
			gomega.Expect(readTestFile(filepath.Join(root, "a.txt"))).To(gomega.Equal("after\n"))
			gomega.Expect(readTestFile(filepath.Join(root, "b.txt"))).To(gomega.Equal("before\n"))
			gomega.Expect(readTestFile(filepath.Join(root, "c.txt"))).To(gomega.Equal("before\n"))
		})

		ginkgo.It("preserves inode links and an external-process append record", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, "events.log")
			linked := filepath.Join(root, "events-linked.log")
			writeTestFile(target, "base\n")
			gomega.Expect(os.Link(target, linked)).To(gomega.Succeed())
			before, err := os.Stat(target)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.Path == "events.log" {
					once.Do(func() {
						cmd := exec.Command(os.Args[0], "-test.run=^TestWorkspaceExternalAppendHelper$")
						cmd.Env = append(os.Environ(),
							"IDIOM_WORKSPACE_APPEND_HELPER=1",
							"IDIOM_WORKSPACE_APPEND_PATH="+target,
							"IDIOM_WORKSPACE_APPEND_CONTENT=external-process\\n",
						)
						output, runErr := cmd.CombinedOutput()
						gomega.Expect(runErr).NotTo(gomega.HaveOccurred(), string(output))
					})
				}
			})

			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameAppendFile, Path: "events.log", Content: "manager\n", Description: "append manager record"})
			gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("result: %#v", result))
			after, err := os.Stat(target)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			linkedInfo, err := os.Stat(linked)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(os.SameFile(before, after)).To(gomega.BeTrue())
			gomega.Expect(os.SameFile(after, linkedInfo)).To(gomega.BeTrue())
			gomega.Expect(readTestFile(target)).To(gomega.Equal("base\nexternal-process\nmanager\n"))
			gomega.Expect(readTestFile(linked)).To(gomega.Equal("base\nexternal-process\nmanager\n"))
		})
	},
)

// TestWorkspaceExternalAppendHelper runs only in the subprocess launched by
// the append contract. Keeping it in-process with Go's test binary makes the
// regression portable without shell-dependent quoting.
func TestWorkspaceExternalAppendHelper(t *testing.T) {
	if os.Getenv("IDIOM_WORKSPACE_APPEND_HELPER") != "1" {
		return
	}
	path := os.Getenv("IDIOM_WORKSPACE_APPEND_PATH")
	content := strings.ReplaceAll(os.Getenv("IDIOM_WORKSPACE_APPEND_CONTENT"), `\n`, "\n")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
