package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Plan-execute source-window grounding", func() {
	newRun := func(path string, body string) (*runExecution, *workspace.Manager) {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(writeTestFile(root, path, body)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{path},
				AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan},
			},
		})
		return run, manager
	}

	ginkgo.It("rejects a stale anchor after mutation and accepts the freshly exposed replacement", func() {
		const path = "internal/feature/target.go"
		run, manager := newRun(path, "package feature\n\nconst target = \"before\"\n")
		initial := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 3,
		})
		gomega.Expect(initial.Error).To(gomega.BeEmpty())

		mutation := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: path,
			OldText: `target = "before"`, NewText: `target = "after"`, Description: "advance mutation epoch",
		})
		gomega.Expect(mutation.Error).To(gomega.BeEmpty())
		gomega.Expect(mutation.CommitReceipt()).NotTo(gomega.BeNil())

		stalePlan := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan, Edits: []planartifact.PlanEdit{{
			Path: path, OldText: `target = "before"`, NewText: `target = "final"`, Purpose: "replace target value",
		}}}
		stale := run.preflightPlanThenExecuteSubmitPlanGroundingGuard(stalePlan, []workspace.ToolResult{initial, mutation})
		gomega.Expect(stale.Intercepted).To(gomega.BeTrue())
		gomega.Expect(stale.Result.Error).To(gomega.ContainSubstring(path))

		fresh := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 3,
		})
		gomega.Expect(fresh.Error).To(gomega.BeEmpty())
		freshPlan := stalePlan
		freshPlan.Edits = []planartifact.PlanEdit{{
			Path: path, OldText: `target = "after"`, NewText: `target = "final"`, Purpose: "replace current target value",
		}}
		accepted := run.preflightPlanThenExecuteSubmitPlanGroundingGuard(freshPlan, []workspace.ToolResult{initial, mutation, fresh})
		gomega.Expect(accepted.Intercepted).To(gomega.BeFalse(), fmt.Sprintf("fresh plan was rejected: %#v", accepted.Result))
	})

	ginkgo.It("requires one current producer window to cover the complete edit anchor", func() {
		const path = "pkg/target.go"
		body := strings.Join([]string{"package target", "one", "two", "three", "four", "five"}, "\n")
		run, manager := newRun(path, body)
		lineTwo := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 2, EndLine: 2,
		})
		lineThree := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 3, EndLine: 3,
		})
		gomega.Expect(lineTwo.Error).To(gomega.BeEmpty())
		gomega.Expect(lineThree.Error).To(gomega.BeEmpty())

		disjoint := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan, Edits: []planartifact.PlanEdit{{
			Path: path, OldText: "one\ntwo", NewText: "ONE\nTWO", Purpose: "replace two adjacent lines",
		}}}
		guard := run.preflightPlanThenExecuteSubmitPlanGroundingGuard(disjoint, []workspace.ToolResult{lineTwo, lineThree})
		gomega.Expect(guard.Intercepted).To(gomega.BeTrue(), "disjoint windows must not fabricate contiguous source exposure")

		contiguous := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 2, EndLine: 3,
		})
		gomega.Expect(contiguous.Error).To(gomega.BeEmpty())
		guard = run.preflightPlanThenExecuteSubmitPlanGroundingGuard(disjoint, []workspace.ToolResult{lineTwo, lineThree, contiguous})
		gomega.Expect(guard.Intercepted).To(gomega.BeFalse())

		outsideRange := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan, Edits: []planartifact.PlanEdit{{
			Path: path, StartLine: 4, EndLine: 5, Content: "FOUR\nFIVE", Purpose: "replace a later range",
		}}}
		guard = run.preflightPlanThenExecuteSubmitPlanGroundingGuard(outsideRange, []workspace.ToolResult{contiguous})
		gomega.Expect(guard.Intercepted).To(gomega.BeTrue())

		later := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 4, EndLine: 5,
		})
		gomega.Expect(later.Error).To(gomega.BeEmpty())
		guard = run.preflightPlanThenExecuteSubmitPlanGroundingGuard(outsideRange, []workspace.ToolResult{later})
		gomega.Expect(guard.Intercepted).To(gomega.BeFalse(), "non-1-based exact range should be accepted when fully exposed")
	})

	ginkgo.It("does not trust a source-window attachment minted for another workspace identity", func() {
		const path = "pkg/target.go"
		run, _ := newRun(path, "package target\n\nconst value = 1\n")
		foreign := readFileSourceWindowResult(path, 1, 3, 3, "1:package target\n2:\n3:const value = 1\n")
		request := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan, Edits: []planartifact.PlanEdit{{
			Path: path, OldText: "const value = 1", NewText: "const value = 2", Purpose: "update value",
		}}}
		guard := run.preflightPlanThenExecuteSubmitPlanGroundingGuard(request, []workspace.ToolResult{foreign})
		gomega.Expect(guard.Intercepted).To(gomega.BeTrue())
	})

	ginkgo.It("keeps a bounded first window on a large source file strategy-neutral", func() {
		fact := toolfacts.SourceWindowEvidence{CanonicalID: "large.go", StartLine: 1, EndLine: 80, TotalLines: 2000}
		level, detail := planThenExecuteReadFileEvidenceLevel(fact)
		gomega.Expect(level).To(gomega.Equal(planThenExecuteEvidenceAnchorReady))
		gomega.Expect(detail).To(gomega.Equal("bounded source range"))
	})

	ginkgo.It("attaches source-satisfied evidence only from a current producer window", func() {
		const path = "pkg/target.go"
		run, manager := newRun(path, "package target\n\nconst value = 1\n")
		read := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 3,
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty())

		request := workspace.ToolRequest{
			Tool: workspace.ToolNameSubmitPlan, SourceSatisfiedPaths: []string{path},
			Rationale: "The requested final state is already present.",
		}
		current := run.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(request, []workspace.ToolResult{read})
		gomega.Expect(current.PlanArtifact).NotTo(gomega.BeNil())
		gomega.Expect(current.PlanArtifact.Evidence).To(gomega.ContainElement(gomega.WithTransform(
			func(evidence planartifact.PlanArtifactEvidence) string {
				return evidence.Path + "|" + string(evidence.Kind)
			},
			gomega.Equal(path+"|"+string(planartifact.EvidenceKindHarnessObservedSource)),
		)))

		mutation := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: path,
			OldText: "const value = 1", NewText: "const value = 2", Description: "advance the source revision",
		})
		gomega.Expect(mutation.Error).To(gomega.BeEmpty())
		stale := run.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(request, []workspace.ToolResult{read, mutation})
		gomega.Expect(stale.PlanArtifact).NotTo(gomega.BeNil())
		gomega.Expect(stale.PlanArtifact.Evidence).To(gomega.BeEmpty(), "a stale pre-mutation window must not mint current-state plan evidence")

		fresh := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 3,
		})
		gomega.Expect(fresh.Error).To(gomega.BeEmpty())
		refreshed := run.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(request, []workspace.ToolResult{read, mutation, fresh})
		gomega.Expect(refreshed.PlanArtifact.Evidence).To(gomega.HaveLen(1))
	})

	ginkgo.It("projects a presented alias to canonical source-window identity without accepting siblings", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "real", "target.go"), []byte("package target\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: "implementation_result",
				PermissionProfile: "guarded_workspace_write", RequiredEditPaths: []string{"alias/target.go"},
				AllowedTools: []string{workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(run.requiredEditCoveragePaths()).To(gomega.Equal([]string{"real/target.go"}))

		read := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "alias/target.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		answer := "Decision: source-satisfied\n- alias/target.go: requested final state is already present."
		paths, accepted := run.planThenExecuteAcceptedSourceSatisfiedPaths(answer, []workspace.ToolResult{read})
		gomega.Expect(accepted).To(gomega.BeTrue())
		gomega.Expect(paths).To(gomega.Equal([]string{"real/target.go"}))

		request := workspace.ToolRequest{
			Tool: workspace.ToolNameSubmitPlan, SourceSatisfiedPaths: []string{"alias/target.go"},
			Rationale: "The requested final state is already present.",
		}
		bridged := run.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(request, []workspace.ToolResult{read})
		gomega.Expect(bridged.PlanArtifact.Evidence).To(gomega.ContainElement(gomega.WithTransform(
			func(evidence planartifact.PlanArtifactEvidence) string {
				return evidence.Path + "|" + string(evidence.Kind)
			},
			gomega.Equal("alias/target.go|"+string(planartifact.EvidenceKindHarnessObservedSource)),
		)))

		sibling := "Decision: source-satisfied\n- alias/sibling.go: requested final state is already present."
		_, accepted = run.planThenExecuteAcceptedSourceSatisfiedPaths(sibling, []workspace.ToolResult{read})
		gomega.Expect(accepted).To(gomega.BeFalse())
	})
})
