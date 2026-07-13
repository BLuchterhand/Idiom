package agent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Workspace-owned mutation admission", func() {
	newRun := func(manager *workspace.Manager, brief workerruntime.WorkerRuntimeBrief) *runExecution {
		brief.TaskFamily = "implementation"
		brief.Contract = "implementation_result"
		brief.PermissionProfile = "guarded_workspace_write"
		run := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		return run
	}

	ginkgo.It("defers an authorized unbound canonical target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		result, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeTrue())
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionDeferredByPrerequisite))
		gomega.Expect(result.Facts.PendingSourceBodyPaths).To(gomega.Equal([]string{"target.go"}))
	})

	ginkgo.It("does not treat runtime brief path mentions as read-before-edit evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{
			TargetRefs:     []string{"target.go"},
			EvidencePaths:  []string{"target.go"},
			ChangedPaths:   []string{"target.go"},
			TargetPacket:   []string{"candidate path: target.go"},
			UserBoundaries: []string{"only target.go"},
		})
		gomega.Expect(manager.HasObservedPath("target.go")).To(gomega.BeFalse())

		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}
		blocked := manager.Execute(run.state.ctx, req)
		gomega.Expect(blocked.Error).To(gomega.ContainSubstring("observed target.go"))
		gomega.Expect(manager.HasObservedPath("target.go")).To(gomega.BeFalse())

		source := manager.Execute(run.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "target.go", StartLine: 1, EndLine: 1})
		gomega.Expect(source.Error).To(gomega.BeEmpty())
		gomega.Expect(manager.HasObservedPath("target.go")).To(gomega.BeTrue())
		committed := manager.Execute(run.state.ctx, req)
		gomega.Expect(committed.Error).To(gomega.BeEmpty())
		gomega.Expect(committed.CommitReceipt()).NotTo(gomega.BeNil())
	})

	ginkgo.It("uses physical canonical identity for an aliased target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "real", "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join("real", "target.go"), filepath.Join(root, "alias.go")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "alias.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		assessment, ok := run.mutationAdmissionForRequest(req)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(assessment.Disposition).To(gomega.Equal(workspace.MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(mutationRequestCanonicalTargetIDs(assessment.Analysis)).To(gomega.Equal([]string{"real/target.go"}))
		result, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeTrue())
		gomega.Expect(result.Facts.PendingSourceBodyPaths).To(gomega.Equal([]string{"real/target.go"}))
		gomega.Expect(result.Facts.RejectedWritePaths).To(gomega.Equal([]string{"real/target.go"}), "the admission analysis must own both prerequisite and rejected-intent identity")
	})

	ginkgo.It("uses one admitted canonical identity for history prerequisites and rejected intent", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(filepath.Join(root, ".git"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "real", "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join("real", "target.go"), filepath.Join(root, "alias.go")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{
			RequiredEvidence: []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "alias.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}
		source := manager.Execute(run.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "alias.go", StartLine: 1, EndLine: 1})
		gomega.Expect(source.Error).To(gomega.BeEmpty())

		result, deferred := run.implementationWritePrerequisiteDeferralResult(req, []workspace.ToolResult{source})
		gomega.Expect(deferred).To(gomega.BeTrue())
		gomega.Expect(result.Facts.PendingSourceBodyPaths).To(gomega.BeEmpty())
		gomega.Expect(result.Facts.PendingHistoryPaths).To(gomega.Equal([]string{"real/target.go"}))
		gomega.Expect(result.Facts.RejectedWritePaths).To(gomega.Equal([]string{"real/target.go"}))
	})

	ginkgo.It("requires current source evidence after an intervening mutation", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		source := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "target.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(source.Error).To(gomega.BeEmpty())
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package final", Description: "finish package rename",
		}
		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, []workspace.ToolResult{source})
		gomega.Expect(deferred).To(gomega.BeFalse(), "a current producer source window should satisfy the prerequisite")

		write := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: "target.go", Content: "package changed\n", Description: "apply an intervening mutation",
		})
		gomega.Expect(write.Error).To(gomega.BeEmpty())
		staleReq := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package changed", NewText: "package final", Description: "finish package rename",
		}
		result, deferred := run.implementationWritePrerequisiteDeferralResult(staleReq, []workspace.ToolResult{source, write})
		gomega.Expect(deferred).To(gomega.BeTrue(), "an old window must not authorize a later mutation epoch")
		gomega.Expect(result.Facts.PendingSourceBodyPaths).To(gomega.Equal([]string{"target.go"}))

		fresh := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "target.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(fresh.Error).To(gomega.BeEmpty())
		_, deferred = run.implementationWritePrerequisiteDeferralResult(staleReq, []workspace.ToolResult{source, write, fresh})
		gomega.Expect(deferred).To(gomega.BeFalse(), "a fresh producer window should satisfy the new mutation epoch")
	})

	ginkgo.It("does not retain malformed compound intent", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameApplyPatch,
			Content: "*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n" +
				"*** Update File: b.go\nmalformed\n*** End Patch",
			Description: "update files",
		}

		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
		failed := manager.Execute(run.state.ctx, req)
		gomega.Expect(failed.Error).NotTo(gomega.BeEmpty())
		recorded := resultWithRejectedRequestTarget(req, failed)
		gomega.Expect(recorded.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionNotRetained))
		gomega.Expect(recorded.Facts.RejectedWritePaths).To(gomega.BeEmpty())
	})

	ginkgo.It("rejects a symlink escape before retaining prerequisite intent", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package secret\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, "escape.go")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "escape.go", OldText: "package secret", NewText: "package changed", Description: "rename package",
		}

		assessment, assessed := run.mutationAdmissionForRequest(req)
		gomega.Expect(assessed).To(gomega.BeTrue())
		gomega.Expect(assessment.Disposition).NotTo(gomega.Equal(workspace.MutationAdmissionAdmittedWithoutApproval))
		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
	})

	ginkgo.It("preserves configured denial ahead of prerequisite retention", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
			ToolRules: map[string][]config.ToolRule{
				workspace.ToolNameEditFile: {{Pattern: "target.go", Decision: "deny"}},
			},
		})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
		failed := manager.Execute(run.state.ctx, req)
		gomega.Expect(failed.Error).NotTo(gomega.BeEmpty())
		recorded := resultWithRejectedRequestTarget(req, failed)
		gomega.Expect(recorded.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionPolicyDenied))
		gomega.Expect(recorded.Facts.RejectedWritePaths).To(gomega.BeEmpty())
	})

	ginkgo.It("applies configured denial in the runner before prerequisite retention", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
			ToolRules: map[string][]config.ToolRule{
				workspace.ToolNameEditFile: {{Pattern: "target.go", Decision: "deny"}},
			},
		})
		client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
		client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
		client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
		client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(llama.ChatMessage{
			Role:    "assistant",
			Content: `{"tool":"edit_file","path":"target.go","old_text":"package sample","new_text":"package changed","description":"rename package"}`,
		}, nil).Times(1)

		var results []ToolResultEvent
		runner := &Runner{
			Client: client, Workspace: manager,
			Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2, MaxModelCalls: 2},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				Lane: "implement", TaskFamily: "implementation", Contract: "implementation_result",
				Objective: "Update target.go.", PermissionProfile: "guarded_workspace_write",
				AllowedTools: []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile},
			},
		}
		_, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
			llama.ChatOptions{}, nil, nil,
			func(event ToolResultEvent) { results = append(results, event) },
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(results).To(gomega.HaveLen(1))
		gomega.Expect(results[0].Result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(results[0].Result.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionPolicyDenied))
		gomega.Expect(results[0].Result.Facts.PendingSourceBodyPaths).To(gomega.BeEmpty())
		gomega.Expect(results[0].Result.Facts.RejectedWritePaths).To(gomega.BeEmpty())
	})

	ginkgo.It("applies configured submit-plan denial before history prerequisite retention", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nconst value = 1\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
			ToolRules: map[string][]config.ToolRule{
				workspace.ToolNameSubmitPlan: {{Pattern: "*", Decision: "deny"}},
			},
		})
		source := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "target.go", StartLine: 1, EndLine: 3,
		})
		gomega.Expect(source.Error).To(gomega.BeEmpty())

		client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
		client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
		client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
		client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(llama.ChatMessage{
			Role:    "assistant",
			Content: `{"tool":"submit_plan","edits":[{"path":"target.go","old_text":"const value = 1","new_text":"const value = 2","purpose":"update the value"}]}`,
		}, nil).Times(1)

		var results []ToolResultEvent
		runner := &Runner{
			Client: client, Workspace: manager, SeedResults: []workspace.ToolResult{source},
			Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2, MaxModelCalls: 2},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				Lane: "plan_execute", TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: "implementation_result",
				Objective: "Plan the target.go update.", PermissionProfile: "guarded_workspace_write",
				AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameSubmitPlan},
				RequiredEditPaths: []string{"target.go"},
				RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
			},
		}
		_, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
			llama.ChatOptions{}, nil, nil,
			func(event ToolResultEvent) { results = append(results, event) },
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(results).To(gomega.HaveLen(1))
		gomega.Expect(results[0].Result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(results[0].Result.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionPolicyDenied))
		gomega.Expect(results[0].Result.Facts.DeferredPlanPaths).To(gomega.BeEmpty())
		gomega.Expect(results[0].Result.Facts.PendingHistoryPaths).To(gomega.BeEmpty())
		gomega.Expect(results[0].Result.Code).NotTo(gomega.Equal(planThenExecuteSubmitPlanHistoryDeferredCode))
	})

	ginkgo.It("retains an exact bound-scope prerequisite through workspace admission", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{RequiredEditPaths: []string{"target.go"}})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		gomega.Expect(run.state.mutation.bound).To(gomega.BeTrue())
		result, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeTrue())
		gomega.Expect(result.Facts.PendingSourceBodyPaths).To(gomega.Equal([]string{"target.go"}))
		gomega.Expect(result.Facts.RejectedWritePaths).To(gomega.Equal([]string{"target.go"}))
	})

	ginkgo.It("does not retain approval-required mutation intent", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
			ToolRules: map[string][]config.ToolRule{
				workspace.ToolNameEditFile: {{Pattern: "target.go", Decision: "ask"}},
			},
		})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		assessment := manager.AssessMutationAdmission(run.state.ctx, req)
		gomega.Expect(assessment.Disposition).To(gomega.Equal(workspace.MutationAdmissionApprovalRequired))
		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
	})

	ginkgo.It("fails closed when an alias retargets outside its bound canonical scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "first"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "second"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "first", "target.go"), []byte("package first\n"), 0o600)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "second", "target.go"), []byte("package second\n"), 0o600)).To(gomega.Succeed())
		alias := filepath.Join(root, "alias.go")
		if err := os.Symlink(filepath.Join("first", "target.go"), alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{RequiredEditPaths: []string{"alias.go"}})
		gomega.Expect(run.state.mutation.canonicalScopeProjection.RequiredCoverageIDs).To(gomega.Equal([]string{"first/target.go"}))

		gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join("second", "target.go"), alias)).To(gomega.Succeed())
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "alias.go", OldText: "package second", NewText: "package changed", Description: "rename package",
		}
		assessment := manager.AssessMutationAdmission(run.state.ctx, req)
		gomega.Expect(assessment.Disposition).To(gomega.Equal(workspace.MutationAdmissionDenied))
		gomega.Expect(mutationRequestCanonicalTargetIDs(assessment.Analysis)).To(gomega.Equal([]string{"second/target.go"}))
		_, deferred := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(deferred).To(gomega.BeFalse(), "a retargeted alias must not retain stale bound-scope intent")
	})

	ginkgo.It("binds one canonical admission per request turn and re-assesses a fresh turn", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "first"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "second"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "first", "target.go"), []byte("package first\n"), 0o600)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "second", "target.go"), []byte("package second\n"), 0o600)).To(gomega.Succeed())
		alias := filepath.Join(root, "alias.go")
		if err := os.Symlink(filepath.Join("first", "target.go"), alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRun(manager, workerruntime.WorkerRuntimeBrief{RequiredEditPaths: []string{"alias.go"}})
		req := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "alias.go", OldText: "package first", NewText: "package changed", Description: "rename package",
		}

		run.beginTurnRequest(req)
		bound, ok := run.mutationAdmissionForRequest(req)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(bound.Disposition).To(gomega.Equal(workspace.MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(mutationRequestCanonicalTargetIDs(bound.Analysis)).To(gomega.Equal([]string{"first/target.go"}))

		gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join("second", "target.go"), alias)).To(gomega.Succeed())
		deferred, retained := run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(retained).To(gomega.BeTrue())
		gomega.Expect(deferred.Facts.RejectedWritePaths).To(gomega.Equal([]string{"first/target.go"}), "one request turn must not retarget its canonical intent between consumers")

		run.beginTurnRequest(req)
		fresh, ok := run.mutationAdmissionForRequest(req)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(fresh.Disposition).To(gomega.Equal(workspace.MutationAdmissionDenied))
		gomega.Expect(mutationRequestCanonicalTargetIDs(fresh.Analysis)).To(gomega.Equal([]string{"second/target.go"}))
		_, retained = run.implementationWritePrerequisiteDeferralResult(req, nil)
		gomega.Expect(retained).To(gomega.BeFalse(), "a fresh model request must re-assess current live scope authority")
	})

	ginkgo.It("leaves directory descendants and authorized-new parents to workspace execution authority", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		directoryRun := newRun(manager, workerruntime.WorkerRuntimeBrief{RequiredEditPaths: []string{"pkg"}})
		write := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "pkg/child.go", Content: "package pkg\n", Description: "add child"}
		gomega.Expect(manager.Execute(directoryRun.state.ctx, write).Error).To(gomega.BeEmpty())

		newRoot := ginkgo.GinkgoT().TempDir()
		newManager := workspace.NewManager(config.Workspace{Root: newRoot, CWD: newRoot, EnableWrites: true})
		authorizedNewRun := newRun(newManager, workerruntime.WorkerRuntimeBrief{AuthorizedNewFiles: []string{"generated/new.go"}})
		mkdir := workspace.ToolRequest{Tool: workspace.ToolNameMkdir, Path: "generated", Description: "create generated parent"}
		gomega.Expect(newManager.Execute(authorizedNewRun.state.ctx, mkdir).Error).To(gomega.BeEmpty())
	})
})
