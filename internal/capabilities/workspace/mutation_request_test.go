package workspace

import (
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("workspace mutation request analysis", func() {
	newManager := func() *Manager {
		root := ginkgo.GinkgoT().TempDir()
		return NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	}

	ginkgo.It("classifies every authorized workspace write tool as a mutation", func() {
		manager := newManager()
		for _, tool := range WorkspaceWriteToolNames() {
			analysis, _ := manager.AnalyzeMutationRequest(ToolRequest{Tool: tool})
			gomega.Expect(analysis.Mutation).To(gomega.BeTrue(), tool)
			gomega.Expect(analysis.Tool).To(gomega.Equal(tool))
		}
	})

	ginkgo.It("binds direct file and directory targets to canonical roles", func() {
		manager := newManager()
		for _, test := range []struct {
			tool     string
			role     MutationTargetRole
			preimage MutationPreimageClass
		}{
			{ToolNameEditFile, MutationTargetFile, MutationPreimageRequired},
			{ToolNameWriteFile, MutationTargetFile, MutationPreimageOptional},
			{ToolNameAppendFile, MutationTargetFile, MutationPreimageOptional},
			{ToolNameMkdir, MutationTargetDirectory, MutationPreimageDirectory},
		} {
			analysis, err := manager.AnalyzeMutationRequest(ToolRequest{Tool: test.tool, Path: "nested/target.txt"})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), test.tool)
			gomega.Expect(analysis.Targets).To(gomega.HaveLen(1), test.tool)
			target := analysis.Targets[0]
			gomega.Expect(target.RequestedPath).To(gomega.Equal("nested/target.txt"))
			gomega.Expect(target.Identity.CanonicalID).To(gomega.Equal("nested/target.txt"))
			gomega.Expect(target.Identity.AbsolutePath).To(gomega.Equal(filepath.Join(manager.cfg.Root, "nested", "target.txt")))
			gomega.Expect(target.Role).To(gomega.Equal(test.role))
			gomega.Expect(target.Preimage).To(gomega.Equal(test.preimage))
		}
	})

	ginkgo.It("owns apply-patch source and destination preimage semantics", func() {
		manager := newManager()
		analysis, err := manager.AnalyzeMutationRequest(ToolRequest{Tool: ToolNameApplyPatch, Content: "*** Begin Patch\n*** Update File: a.txt\n*** Move to: moved/a.txt\n@@\n-before\n+after\n*** Add File: b.txt\n+new\n*** Delete File: c.txt\n*** End Patch"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(analysis.Targets).To(gomega.HaveLen(4))
		gomega.Expect(analysis.Targets).To(gomega.ConsistOf(
			gomega.SatisfyAll(gomega.HaveField("RequestedPath", "a.txt"), gomega.HaveField("Role", MutationTargetSource), gomega.HaveField("Preimage", MutationPreimageRequired)),
			gomega.SatisfyAll(gomega.HaveField("RequestedPath", "moved/a.txt"), gomega.HaveField("Role", MutationTargetDestination), gomega.HaveField("Preimage", MutationPreimageAbsent)),
			gomega.SatisfyAll(gomega.HaveField("RequestedPath", "b.txt"), gomega.HaveField("Role", MutationTargetDestination), gomega.HaveField("Preimage", MutationPreimageAbsent)),
			gomega.SatisfyAll(gomega.HaveField("RequestedPath", "c.txt"), gomega.HaveField("Role", MutationTargetSource), gomega.HaveField("Preimage", MutationPreimageRequired)),
		))
	})

	ginkgo.It("sorts and preserves declaration-move operation targets under the canonical owner", func() {
		manager := newManager()
		analysis, err := manager.AnalyzeMutationRequest(ToolRequest{
			Tool:  ToolNameGoMoveDeclarations,
			Path:  "source.go",
			Moves: []GoDeclarationMove{{Destination: "z.go"}, {Destination: "a.go"}, {Destination: "z.go"}},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(analysis.Targets).To(gomega.HaveLen(4))
		gomega.Expect([]string{analysis.Targets[0].Identity.CanonicalID, analysis.Targets[1].Identity.CanonicalID, analysis.Targets[2].Identity.CanonicalID, analysis.Targets[3].Identity.CanonicalID}).To(gomega.Equal([]string{"a.go", "source.go", "z.go", "z.go"}))
	})
})
