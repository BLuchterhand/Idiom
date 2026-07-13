package agent

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Plan-execute authorized new-file submission", func() {
	ginkgo.It("keeps an authorized new implementation source path write-ready without source preimage evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		brief := workerruntime.WorkerRuntimeBrief{
			Lane: "implement", TaskFamily: "implementation", Contract: "implementation_result",
			PermissionProfile:  "guarded_workspace_write",
			RequiredEditPaths:  []string{"generated/new.go"},
			AuthorizedNewFiles: []string{"generated/new.go"},
			AllowedTools:       []string{workspace.ToolNameReadFile, workspace.ToolNameWriteFile},
		}
		run := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())

		gomega.Expect(run.pendingRequiredSourceBodyPaths(nil)).To(gomega.BeEmpty())
		gomega.Expect(run.scopedImplementationPathsRequireExistingSourceEvidence([]string{"generated/new.go"})).To(gomega.BeFalse())
		prompt := run.implementationRequiredPathCompletionRecoveryPrompt([]string{"generated/new.go"}, nil, false)
		gomega.Expect(prompt).To(gomega.ContainSubstring("authorized new-file paths do not require an impossible preimage read"))
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Source-body evidence remains pending"))

		request := workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: "generated/new.go",
			Content:     "package generated\n\nfunc New() string { return \"new\" }\n",
			Description: "create the authorized implementation source",
		}
		assessment := manager.AssessMutationAdmission(run.state.ctx, request)
		gomega.Expect(assessment.Disposition).To(gomega.Equal(workspace.MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(assessment.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(assessment.Analysis.Targets[0].Preimage).To(gomega.Equal(workspace.MutationPreimageAbsent))
		_, deferred := run.implementationWritePrerequisiteDeferralResult(request, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
		result := manager.Execute(run.state.ctx, request)
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.CommitReceipt()).NotTo(gomega.BeNil())
	})

	ginkgo.It("accepts a complete plan for an authorized new file without source evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:         PlanThenExecutePlannerTaskFamily,
				Contract:           workspace.ToolNameSubmitPlan,
				PermissionProfile:  "guarded_workspace_write",
				RequiredEditPaths:  []string{"generated/new.go"},
				AuthorizedNewFiles: []string{"generated/new.go"},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		run.state.ctx = workspace.WithExecutionSessionID(run.state.ctx, "authorized-new-only")
		gomega.Expect(run.planThenExecutePathAuthority().authorizedNewFile("generated")).To(gomega.BeFalse(), "authorized parent creation is not an authorized file body")

		req := workspace.ToolRequest{
			Tool:      workspace.ToolNameSubmitPlan,
			Rationale: "Create the explicitly authorized generated source file.",
			Edits: []planartifact.PlanEdit{{
				Path:       "generated/new.go",
				NewText:    "package generated\n\nfunc New() string { return \"new\" }\n",
				ReplaceAll: true,
				Purpose:    "add the generated implementation",
			}},
		}

		gomega.Expect(run.preflightPlanThenExecuteSubmitPlanCoverageGuard(req).Intercepted).To(gomega.BeFalse())
		gomega.Expect(run.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, nil).Intercepted).To(gomega.BeFalse())
		gomega.Expect(run.ungroundedSubmitPlanEditPaths(req, nil)).To(gomega.BeEmpty())
		gomega.Expect(manager.Execute(run.state.ctx, req).Error).To(gomega.BeEmpty())
	})

	ginkgo.It("requires evidence only for the existing edit in a mixed existing and authorized-new plan", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "pkg", "existing.go"), []byte("package pkg\n\nfunc Existing() string { return \"old\" }\n"), 0o600)).To(gomega.Succeed())

		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:         PlanThenExecutePlannerTaskFamily,
				Contract:           workspace.ToolNameSubmitPlan,
				PermissionProfile:  "guarded_workspace_write",
				RequiredEditPaths:  []string{"pkg/existing.go", "pkg/generated.go"},
				AuthorizedNewFiles: []string{"pkg/generated.go"},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		run.state.ctx = workspace.WithExecutionSessionID(run.state.ctx, "authorized-new-mixed")

		req := workspace.ToolRequest{
			Tool:      workspace.ToolNameSubmitPlan,
			Rationale: "Update the existing implementation and add its authorized generated companion.",
			Edits: []planartifact.PlanEdit{
				{
					Path:    "pkg/existing.go",
					OldText: `return "old"`,
					NewText: `return "new"`,
					Purpose: "update the existing implementation",
				},
				{
					Path:       "pkg/generated.go",
					NewText:    "package pkg\n\nfunc Generated() string { return \"generated\" }\n",
					ReplaceAll: true,
					Purpose:    "add the generated companion",
				},
			},
		}

		gomega.Expect(run.ungroundedSubmitPlanEditPaths(req, nil)).To(gomega.Equal([]string{"pkg/existing.go"}))

		read := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "pkg/existing.go", StartLine: 1, EndLine: 40,
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		gomega.Expect(read.SourceWindows()).To(gomega.HaveLen(1))

		gomega.Expect(run.preflightPlanThenExecuteSubmitPlanCoverageGuard(req).Intercepted).To(gomega.BeFalse())
		gomega.Expect(run.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, []workspace.ToolResult{read}).Intercepted).To(gomega.BeFalse())
		gomega.Expect(run.ungroundedSubmitPlanEditPaths(req, []workspace.ToolResult{read})).To(gomega.BeEmpty())
		gomega.Expect(manager.Execute(run.state.ctx, req).Error).To(gomega.BeEmpty())
	})

	ginkgo.It("does not infer new-file authority from the replace-all edit shape", func() {
		edit := planartifact.PlanEdit{
			Path:       "generated/unscoped.go",
			NewText:    "package generated\n",
			ReplaceAll: true,
		}
		run := newRunnerRunFixture(&Runner{})

		gomega.Expect(run.submitPlanEditPathGrounding(edit.Path, edit, nil).Level).To(gomega.Equal(planThenExecuteEvidenceNone))
	})
})
