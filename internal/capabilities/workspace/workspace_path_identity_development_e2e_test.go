package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe(
	"workspace path identity development workflow",
	ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"),
	func() {
		ginkgo.It("rejects selected-file approval when the approved object is replaced before grant commit", func() {
			parent := ginkgo.GinkgoT().TempDir()
			workspaceRoot := filepath.Join(parent, "workspace")
			target := filepath.Join(parent, "selected.txt")
			approvedObject := filepath.Join(parent, "selected-approved.txt")
			writeTestFile(filepath.Join(workspaceRoot, "README.md"), "workspace\n")
			writeTestFile(target, "approved object\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot})
			approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
			manager.SetSystemSearchAccessApprover(approver)
			approver.EXPECT().ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
					gomega.Expect(request.Path).To(gomega.Equal(canonicalTestPath(target)))
					gomega.Expect(os.Rename(target, approvedObject)).To(gomega.Succeed())
					gomega.Expect(os.WriteFile(target, []byte("replacement\n"), 0o600)).To(gomega.Succeed())

					return SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeSelected}, nil
				})

			approval := manager.Execute(context.Background(), ToolRequest{
				Tool:    ToolNameSystemSearchAccess,
				Path:    target,
				Purpose: "read selected external evidence",
			})
			gomega.Expect(approval.Error).To(gomega.ContainSubstring("changed before the grant was recorded"), fmt.Sprintf("stale approval result: %#v", approval))
			gomega.Expect(approval.ChangedPaths).To(gomega.BeEmpty())

			read := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: target})
			gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("replacement inherited stale grant: %#v", read))
			gomega.Expect(read.Output).NotTo(gomega.ContainSubstring("replacement"))
		})

		ginkgo.It("rejects directory-contents approval when the approved root is replaced before grant commit", func() {
			parent := ginkgo.GinkgoT().TempDir()
			workspaceRoot := filepath.Join(parent, "workspace")
			target := filepath.Join(parent, "approved-directory")
			approvedObject := filepath.Join(parent, "approved-directory-original")
			writeTestFile(filepath.Join(workspaceRoot, "README.md"), "workspace\n")
			writeTestFile(filepath.Join(target, "approved.txt"), "approved object\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot})
			approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
			manager.SetSystemSearchAccessApprover(approver)
			approver.EXPECT().ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
					gomega.Expect(os.Rename(target, approvedObject)).To(gomega.Succeed())
					writeTestFile(filepath.Join(target, "replacement.txt"), "replacement\n")

					return SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil
				})

			approval := manager.Execute(context.Background(), ToolRequest{
				Tool:    ToolNameSystemSearchAccess,
				Path:    target,
				Purpose: "inspect external directory evidence",
			})
			gomega.Expect(approval.Error).To(gomega.ContainSubstring("changed before the grant was recorded"), fmt.Sprintf("stale approval result: %#v", approval))

			list := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: target})
			gomega.Expect(list.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("replacement directory inherited stale grant: %#v", list))
			gomega.Expect(list.Output).NotTo(gomega.ContainSubstring("replacement.txt"))
		})

		ginkgo.It("revokes an accepted directory grant when later use sees a replacement root", func() {
			parent := ginkgo.GinkgoT().TempDir()
			workspaceRoot := filepath.Join(parent, "workspace")
			target := filepath.Join(parent, "approved-directory")
			approvedObject := filepath.Join(parent, "approved-directory-original")
			writeTestFile(filepath.Join(workspaceRoot, "README.md"), "workspace\n")
			writeTestFile(filepath.Join(target, "approved.txt"), "approved object\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot})
			approver := newSystemSearchApproverMock(manager)
			expectSystemSearchApproval(manager, approver, target, "inspect stable external directory", SystemSearchAccessScopeContents)
			approval := manager.Execute(context.Background(), ToolRequest{
				Tool:    ToolNameSystemSearchAccess,
				Path:    target,
				Purpose: "inspect stable external directory",
			})
			gomega.Expect(approval.Error).To(gomega.BeEmpty(), fmt.Sprintf("initial approval failed: %#v", approval))

			gomega.Expect(os.Rename(target, approvedObject)).To(gomega.Succeed())
			writeTestFile(filepath.Join(target, "replacement.txt"), "replacement\n")

			list := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: target})
			gomega.Expect(list.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("replacement directory inherited accepted grant: %#v", list))
			gomega.Expect(list.Output).NotTo(gomega.ContainSubstring("replacement.txt"))
		})

		ginkgo.It("contains a write when its checked parent is replaced by an outside symlink", func() {
			requireWorkspaceSymlinks()
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			outside := ginkgo.GinkgoT().TempDir()
			parent := filepath.Join(workspaceRoot, "safe")
			movedParent := filepath.Join(workspaceRoot, "safe-original")
			gomega.Expect(os.Mkdir(parent, 0o755)).To(gomega.Succeed())

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterPreflight || checkpoint.Path != "safe/escape.txt" {
					return
				}
				once.Do(func() {
					gomega.Expect(os.Rename(parent, movedParent)).To(gomega.Succeed())
					gomega.Expect(os.Symlink(outside, parent)).To(gomega.Succeed())
				})
			})

			result := manager.Execute(ctx, ToolRequest{
				Tool:        ToolNameWriteFile,
				Path:        "safe/escape.txt",
				Content:     "must remain contained\n",
				Description: "exercise parent identity containment",
			})
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("parent replacement unexpectedly succeeded: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty(), fmt.Sprintf("precommit rejection claimed changes: %#v", result))
			gomega.Expect(filepath.Join(outside, "escape.txt")).NotTo(gomega.BeAnExistingFile())
			gomega.Expect(filepath.Join(movedParent, "escape.txt")).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("rejects a root swap at the final precommit boundary", func() {
			parent := ginkgo.GinkgoT().TempDir()
			workspaceRoot := filepath.Join(parent, "workspace")
			movedRoot := filepath.Join(parent, "workspace-original")
			gomega.Expect(os.MkdirAll(filepath.Join(workspaceRoot, "bound"), 0o755)).To(gomega.Succeed())

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationBeforeCommit || checkpoint.Path != "bound/result.txt" {
					return
				}
				once.Do(func() {
					gomega.Expect(os.Rename(workspaceRoot, movedRoot)).To(gomega.Succeed())
					gomega.Expect(os.Mkdir(workspaceRoot, 0o755)).To(gomega.Succeed())
				})
			})

			result := manager.Execute(ctx, ToolRequest{
				Tool:        ToolNameWriteFile,
				Path:        "bound/result.txt",
				Content:     "must not commit through stale root\n",
				Description: "exercise bound-parent containment",
			})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("root path no longer names"), fmt.Sprintf("root swap result: %#v", result))
			gomega.Expect(result.Summary).To(gomega.Equal("Failed to write file"))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(filepath.Join(movedRoot, "bound", "result.txt")).NotTo(gomega.BeAnExistingFile())
			gomega.Expect(filepath.Join(workspaceRoot, "bound", "result.txt")).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("rejects an existing target replacement after binding its parent", func() {
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(workspaceRoot, "target.txt")
			approvedPreimage := filepath.Join(workspaceRoot, "target-preimage.txt")
			writeTestFile(target, "before\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.Path != "target.txt" {
					return
				}
				once.Do(func() {
					gomega.Expect(os.Rename(target, approvedPreimage)).To(gomega.Succeed())
					gomega.Expect(os.WriteFile(target, []byte("replacement\n"), 0o644)).To(gomega.Succeed())
				})
			})

			result := manager.Execute(ctx, ToolRequest{
				Tool:        ToolNameWriteFile,
				Path:        "target.txt",
				Content:     "requested\n",
				Description: "exercise target preimage identity",
			})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target identity changed since preflight"), fmt.Sprintf("target replacement result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(target)).To(gomega.Equal("replacement\n"))
			gomega.Expect(readTestFile(approvedPreimage)).To(gomega.Equal("before\n"))
		})

		ginkgo.It("distinguishes committed parent creation from an uncommitted file replacement", func() {
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(workspaceRoot, "generated", "nested", "result.txt")
			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.Path != "generated/nested/result.txt" {
					return
				}
				once.Do(func() {
					writeTestFile(target, "concurrent file\n")
				})
			})

			result := manager.Execute(ctx, ToolRequest{
				Tool:        ToolNameWriteFile,
				Path:        "generated/nested/result.txt",
				Content:     "requested file\n",
				Description: "exercise directory-only partial state",
			})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target existence changed since preflight"), fmt.Sprintf("directory partial result: %#v", result))
			gomega.Expect(result.Summary).To(gomega.Equal("Write partially applied"))
			gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"generated", "generated/nested"}))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("created_directory: generated"))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("created_directory: generated/nested"))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("replaced_file: generated/nested/result.txt"))
			gomega.Expect(readTestFile(target)).To(gomega.Equal("concurrent file\n"))
		})

		ginkgo.It("does not overwrite a move destination that appears after patch preflight", func() {
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			writeTestFile(filepath.Join(workspaceRoot, "source.txt"), "before\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.Path != "destination.txt" {
					return
				}
				once.Do(func() {
					writeTestFile(filepath.Join(workspaceRoot, "destination.txt"), "concurrent destination\n")
				})
			})

			patch := "*** Begin Patch\n" +
				"*** Update File: source.txt\n" +
				"*** Move to: destination.txt\n" +
				"@@\n" +
				"-before\n" +
				"+after\n" +
				"*** End Patch"
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: patch, Description: "exercise move destination identity"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target existence changed since preflight"), fmt.Sprintf("destination replacement result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(filepath.Join(workspaceRoot, "source.txt"))).To(gomega.Equal("before\n"))
			gomega.Expect(readTestFile(filepath.Join(workspaceRoot, "destination.txt"))).To(gomega.Equal("concurrent destination\n"))
		})

		ginkgo.It("does not delete a replacement that appears after patch preflight", func() {
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(workspaceRoot, "victim.txt")
			original := filepath.Join(workspaceRoot, "victim-original.txt")
			writeTestFile(target, "approved victim\n")

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.Path != "victim.txt" {
					return
				}
				once.Do(func() {
					gomega.Expect(os.Rename(target, original)).To(gomega.Succeed())
					writeTestFile(target, "replacement victim\n")
				})
			})

			patch := "*** Begin Patch\n*** Delete File: victim.txt\n*** End Patch"
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: patch, Description: "exercise delete identity"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target identity changed since preflight"), fmt.Sprintf("delete replacement result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			gomega.Expect(readTestFile(target)).To(gomega.Equal("replacement victim\n"))
			gomega.Expect(readTestFile(original)).To(gomega.Equal("approved victim\n"))
		})

		ginkgo.It("reports exact partial state and unattempted work after the second patch operation becomes stale", func() {
			workspaceRoot := ginkgo.GinkgoT().TempDir()
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				writeTestFile(filepath.Join(workspaceRoot, name), "before\n")
			}

			manager := NewManager(config.Workspace{Root: workspaceRoot, CWD: workspaceRoot, EnableWrites: true})
			var once sync.Once
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.OperationIndex != 2 || checkpoint.Path != "b.txt" {
					return
				}
				once.Do(func() {
					gomega.Expect(os.Rename(filepath.Join(workspaceRoot, "b.txt"), filepath.Join(workspaceRoot, "b-original.txt"))).To(gomega.Succeed())
					writeTestFile(filepath.Join(workspaceRoot, "b.txt"), "concurrent replacement\n")
				})
			})

			patch := threeFileUpdatePatch("a.txt", "b.txt", "c.txt")
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Content: patch, Description: "exercise honest partial patch state"})
			gomega.Expect(result.Error).To(gomega.ContainSubstring("update b.txt"), fmt.Sprintf("partial patch result: %#v", result))
			gomega.Expect(result.Error).To(gomega.ContainSubstring("target identity changed since preflight"))
			gomega.Expect(result.Summary).To(gomega.Equal("Patch partially applied"))
			gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"a.txt"}))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("replaced_file: a.txt"))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("unattempted after failure:"))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("update c.txt"))
			gomega.Expect(readTestFile(filepath.Join(workspaceRoot, "a.txt"))).To(gomega.Equal("after\n"))
			gomega.Expect(readTestFile(filepath.Join(workspaceRoot, "b.txt"))).To(gomega.Equal("concurrent replacement\n"))
			gomega.Expect(readTestFile(filepath.Join(workspaceRoot, "c.txt"))).To(gomega.Equal("before\n"))
		})
	},
)

func requireWorkspaceSymlinks() {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	gomega.Expect(os.Mkdir(target, 0o755)).To(gomega.Succeed())
	if err := os.Symlink(target, link); err != nil {
		ginkgo.Skip("symlinks unavailable: " + err.Error())
	}
	gomega.Expect(os.Remove(link)).To(gomega.Succeed())
}

func threeFileUpdatePatch(paths ...string) string {
	var patch string
	patch += "*** Begin Patch\n"
	for _, path := range paths {
		patch += "*** Update File: " + path + "\n"
		patch += "@@\n"
		patch += "-before\n"
		patch += "+after\n"
	}
	patch += "*** End Patch"

	return patch
}
