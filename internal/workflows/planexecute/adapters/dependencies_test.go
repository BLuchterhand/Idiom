package adapters

import (
	"context"
	"os"
	"path/filepath"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Plan-execute production adapters", func() {
	ginkgo.It("exposes the complete dependency set", func() {
		deps := Dependencies()

		gomega.Expect(deps.AcquireQueueSlot).NotTo(gomega.BeNil())
		gomega.Expect(deps.BuildWriterRoute).NotTo(gomega.BeNil())
		gomega.Expect(deps.RunPromptContextTool).NotTo(gomega.BeNil())
		gomega.Expect(deps.ApplyExecutorEdit).NotTo(gomega.BeNil())
		gomega.Expect(deps.LoadPlanArtifact).NotTo(gomega.BeNil())
		gomega.Expect(deps.SubmitAutoPlan).NotTo(gomega.BeNil())
		gomega.Expect(deps.SourceSanity).NotTo(gomega.BeNil())
		gomega.Expect(deps.SourceSanityCovers).NotTo(gomega.BeNil())
		gomega.Expect(deps.FindPlanArtifact).NotTo(gomega.BeNil())
		gomega.Expect(deps.CleanupPlanArtifacts).NotTo(gomega.BeNil())

		policy := deps.EditPreflight
		gomega.Expect(policy.UnsupportedProofClaimReason).NotTo(gomega.BeNil())
		gomega.Expect(policy.RejectExistingSourceWholeFileRewrite).NotTo(gomega.BeNil())
		gomega.Expect(policy.RejectExistingSourceDestructiveLineRangeRewrite).NotTo(gomega.BeNil())
		gomega.Expect(policy.RejectSourceLineRangeTopLevelConstructDrift).NotTo(gomega.BeNil())
		gomega.Expect(policy.NeedsAnchorProvenance).NotTo(gomega.BeNil())
		gomega.Expect(policy.RequiresSubmitPlanAnchorProvenance).NotTo(gomega.BeNil())
		gomega.Expect(policy.ValidateAnchorProvenance).NotTo(gomega.BeNil())
		gomega.Expect(policy.IsCompletedPlanningArtifact).NotTo(gomega.BeNil())
		gomega.Expect(policy.PreviewEditContent).NotTo(gomega.BeNil())
		gomega.Expect(policy.SuspiciousAdjacentDuplicateCodeLine).NotTo(gomega.BeNil())
	})

	ginkgo.It("honors an explicit model override in writer routing", func() {
		root := ginkgo.GinkgoT().TempDir()
		decision := BuildWriterRouteDecision(planexecute.WriterRouteDecisionInput{
			Config:            config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
			Options:           planexecute.Options{Model: "override-model"},
			RequiredEditPaths: []string{"pkg/target.go"},
		})

		gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)))
		gomega.Expect(decision.UserOverride).To(gomega.BeTrue())
		gomega.Expect(decision.PlannerModel).To(gomega.Equal("override-model"))
		gomega.Expect(decision.ExecutorModel).To(gomega.Equal("override-model"))
		gomega.Expect(decision.ReviewerModel).To(gomega.Equal("override-model"))
		gomega.Expect(decision.RequiredEditPaths).To(gomega.Equal([]string{"pkg/target.go"}))
	})

	ginkgo.It("forwards required edit paths to the prompt context tool", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(
			filepath.Join(root, "pkg", "target.go"),
			[]byte("package pkg\n\nfunc Target() string { return \"target\" }\n"),
			0o644,
		)).To(gomega.Succeed())

		result := RunPromptContextTool(context.Background(), config.Workspace{Root: root, CWD: root}, planexecute.PromptContextToolRequest{
			Tool:              "context_pack",
			Task:              "Plan a change to Target",
			Mode:              "plan",
			Language:          "go",
			Limit:             4,
			BudgetTokens:      800,
			RequiredEditPaths: []string{"pkg/target.go"},
		})

		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.Tool).To(gomega.Equal("context_pack"))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("pkg/target.go"))
	})

	ginkgo.It("applies executor edits through the workspace adapter", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "a.txt")
		gomega.Expect(os.WriteFile(path, []byte("before\n"), 0o644)).To(gomega.Succeed())

		result := ApplyExecutorEdit(context.Background(), config.Workspace{Root: root, CWD: root, EnableWrites: true}, planexecute.ExecutorEditRequest{
			Tool:    "edit_file",
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
		})

		gomega.Expect(result.Error).To(gomega.BeEmpty())
		body, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).To(gomega.Equal("after\n"))
	})
})
