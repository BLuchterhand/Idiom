package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/config"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const goMoveTransactionalSource = `package sample

import (
	"fmt"
	"strings"
)

func renderView() string {
	return fmt.Sprint("ok")
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}

func coreValue() int {
	return 1
}
`

func goMoveTransactionalManager(root string) *Manager {
	writeTestFile(filepath.Join(root, "source.go"), goMoveTransactionalSource)
	return NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
}

func goMoveSingleRequest() ToolRequest {
	return ToolRequest{
		Tool:         ToolNameGoMoveDeclarations,
		Path:         "source.go",
		Destination:  "rendering.go",
		Declarations: []string{"renderView"},
	}
}

var _ = ginkgo.Describe("rooted Go declaration move transaction", ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"), func() {
	ginkgo.It("commits a destination creation and source rewrite with receipt-authored ChangedPaths", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := goMoveTransactionalManager(root)
		generation := manager.languageTools.Load()

		result := manager.Execute(context.Background(), goMoveSingleRequest())
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("go move result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"source.go", "rendering.go"}))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt).NotTo(gomega.BeNil())
		gomega.Expect(receipt.Commits).To(gomega.HaveLen(2))
		gomega.Expect(receipt.FailedTargets).To(gomega.BeEmpty())
		gomega.Expect(receipt.UnattemptedTargets).To(gomega.BeEmpty())
		gomega.Expect(receipt.ChangedPaths()).To(gomega.Equal(result.ChangedPaths))
		gomega.Expect(manager.languageTools.Load()).NotTo(gomega.BeIdenticalTo(generation))
		gomega.Expect(readTestFile(filepath.Join(root, "source.go"))).NotTo(gomega.ContainSubstring("renderView"))
		gomega.Expect(readTestFile(filepath.Join(root, "rendering.go"))).To(gomega.ContainSubstring("renderView"))
	})

	ginkgo.It("plans and commits multiple destinations as one batch transaction", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := goMoveTransactionalManager(root)
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameGoMoveDeclarations,
			Path: "source.go",
			Moves: []GoDeclarationMove{
				{Destination: "rendering.go", Declarations: []string{"renderView"}},
				{Destination: "task_memory.go", Declarations: []string{"normalize"}},
			},
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("batch result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"rendering.go", "source.go", "task_memory.go"}))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt.Commits).To(gomega.HaveLen(3))
		gomega.Expect(receipt.ChangedPaths()).To(gomega.Equal(result.ChangedPaths))
		gomega.Expect(readTestFile(filepath.Join(root, "source.go"))).To(gomega.ContainSubstring("coreValue"))
		gomega.Expect(readTestFile(filepath.Join(root, "source.go"))).NotTo(gomega.ContainSubstring("renderView"))
		gomega.Expect(readTestFile(filepath.Join(root, "source.go"))).NotTo(gomega.ContainSubstring("normalize"))
	})

	ginkgo.DescribeTable("rejects stale rooted planner preimages before any commit",
		func(seedDestination bool, mutate func(string)) {
			root := ginkgo.GinkgoT().TempDir()
			manager := goMoveTransactionalManager(root)
			if seedDestination {
				writeTestFile(filepath.Join(root, "rendering.go"), "package sample\n\nfunc existing() {}\n")
			}
			generation := manager.languageTools.Load()
			called := false
			ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
				if called || checkpoint.Stage != workspaceMutationAfterPreflight {
					return
				}
				called = true
				mutate(root)
			})

			result := manager.Execute(ctx, goMoveSingleRequest())
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("stale result: %#v", result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty(), "planner target paths leaked as commit evidence")
			gomega.Expect(manager.languageTools.Load()).To(gomega.BeIdenticalTo(generation), "cache invalidated without a commit")
			receipt := result.CommitReceipt()
			gomega.Expect(receipt).NotTo(gomega.BeNil())
			gomega.Expect(receipt.Commits).To(gomega.BeEmpty())
			gomega.Expect(receipt.FailedTargets).To(gomega.HaveLen(1))
			gomega.Expect(receipt.UnattemptedTargets).To(gomega.HaveLen(1))
		},
		ginkgo.Entry("source", false, func(root string) {
			writeTestFile(filepath.Join(root, "source.go"), "package sample\n\nfunc externallyChanged() {}\n")
		}),
		ginkgo.Entry("destination", true, func(root string) {
			writeTestFile(filepath.Join(root, "rendering.go"), "package sample\n\nfunc destinationChanged() {}\n")
		}),
	)

	ginkgo.It("reports an exact partial when the source changes after the destination commits", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := goMoveTransactionalManager(root)
		generation := manager.languageTools.Load()
		mutationEpoch := manager.MutationEpoch()
		ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
			if checkpoint.Stage == workspaceMutationBeforeCommit && checkpoint.OperationIndex == 2 {
				writeTestFile(filepath.Join(root, "source.go"), "package sample\n\nfunc externallyChanged() {}\n")
			}
		})

		result := manager.Execute(ctx, goMoveSingleRequest())
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("partial result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"rendering.go"}))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt.Commits).To(gomega.HaveLen(1))
		gomega.Expect(receipt.Commits[0].Identity.CanonicalID).To(gomega.Equal("rendering.go"))
		gomega.Expect(receipt.FailedTargets).To(gomega.HaveLen(1))
		gomega.Expect(receipt.FailedTargets[0].Identity.CanonicalID).To(gomega.Equal("source.go"))
		gomega.Expect(receipt.UnattemptedTargets).To(gomega.BeEmpty())
		gomega.Expect(receipt.DurableProjection()).To(gomega.Equal(MutationCommitProjection{
			Effects:          []MutationCommitEffectProjection{{CanonicalID: "rendering.go", Kind: MutationCommitReplacedFile, Role: MutationTargetDestination}},
			PostimageFileIDs: []string{"rendering.go"},
			AffectedFileIDs:  []string{"rendering.go"},
			FailedTargets:    []MutationTargetProjection{{CanonicalID: "source.go", Role: MutationTargetSource, PreimageClass: MutationPreimageRequired}},
		}))
		gomega.Expect(manager.languageTools.Load()).NotTo(gomega.BeIdenticalTo(generation), "partial commit did not invalidate language caches")
		gomega.Expect(manager.MutationEpoch()).To(gomega.Equal(mutationEpoch+1), "partial commit did not advance the mutation generation exactly once")
		gomega.Expect(readTestFile(filepath.Join(root, "rendering.go"))).To(gomega.ContainSubstring("renderView"))
		gomega.Expect(readTestFile(filepath.Join(root, "source.go"))).To(gomega.ContainSubstring("externallyChanged"))
	})

	ginkgo.It("returns exact canceled and deadline preflight receipts without commits", func() {
		for _, deadline := range []bool{false, true} {
			root := ginkgo.GinkgoT().TempDir()
			manager := goMoveTransactionalManager(root)
			generation := manager.languageTools.Load()
			base := context.Background()
			var cancel context.CancelFunc
			if deadline {
				base, cancel = context.WithTimeout(base, time.Second)
			} else {
				base, cancel = context.WithCancel(base)
			}
			ctx := withWorkspaceMutationCheckpoint(base, func(checkpoint workspaceMutationCheckpoint) {
				if checkpoint.Stage != workspaceMutationAfterPreflight {
					return
				}
				if deadline {
					<-base.Done()
				} else {
					cancel()
				}
			})

			result := manager.Execute(ctx, goMoveSingleRequest())
			cancel()
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("deadline=%v result: %#v", deadline, result))
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			receipt := result.CommitReceipt()
			gomega.Expect(receipt).NotTo(gomega.BeNil())
			gomega.Expect(receipt.Commits).To(gomega.BeEmpty())
			gomega.Expect(receipt.FailedTargets).To(gomega.HaveLen(1))
			gomega.Expect(receipt.UnattemptedTargets).To(gomega.HaveLen(1))
			gomega.Expect(manager.languageTools.Load()).To(gomega.BeIdenticalTo(generation))
			_, statErr := os.Stat(filepath.Join(root, "rendering.go"))
			gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
		}
	})

	ginkgo.It("rejects a root path swap after planning without leaking commits or cache invalidation", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := goMoveTransactionalManager(root)
		generation := manager.languageTools.Load()
		movedRoot := root + "-opened"
		swapped := false
		ctx := withWorkspaceMutationCheckpoint(context.Background(), func(checkpoint workspaceMutationCheckpoint) {
			if swapped || checkpoint.Stage != workspaceMutationAfterPreflight {
				return
			}
			swapped = true
			gomega.Expect(os.Rename(root, movedRoot)).To(gomega.Succeed())
			gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())
		})
		defer func() {
			if swapped {
				_ = os.RemoveAll(root)
				_ = os.Rename(movedRoot, root)
			}
		}()

		result := manager.Execute(ctx, goMoveSingleRequest())
		gomega.Expect(result.Error).To(gomega.ContainSubstring("root"), fmt.Sprintf("root swap result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
		gomega.Expect(manager.languageTools.Load()).To(gomega.BeIdenticalTo(generation))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt.Commits).To(gomega.BeEmpty())
		_, statErr := os.Stat(filepath.Join(movedRoot, "rendering.go"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
		gomega.Expect(readTestFile(filepath.Join(movedRoot, "source.go"))).To(gomega.Equal(goMoveTransactionalSource))
	})

	ginkgo.It("keeps planner display output non-authoritative when a move is rejected", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := goMoveTransactionalManager(root)
		result := manager.Execute(context.Background(), ToolRequest{
			Tool:         ToolNameGoMoveDeclarations,
			Path:         "source.go",
			Destination:  "rendering.go",
			Declarations: []string{"doesNotExist"},
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
		gomega.Expect(strings.ToLower(result.Output)).NotTo(gomega.ContainSubstring("committed files"))
	})
})
