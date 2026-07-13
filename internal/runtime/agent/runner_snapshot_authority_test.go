package agent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Runner workspace snapshot authority", func() {
	newManager := func() *workspace.Manager {
		ginkgo.GinkgoHelper()
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "protected.txt"), []byte("protected\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, SessionID: "manager-session"})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			workspace.ToolNameReadFile: {{Pattern: "protected.txt", Decision: toolpolicy.Ask}},
		})
		return manager
	}
	request := workspace.WorkspaceSnapshotRequest{
		Purpose: workspace.WorkspaceSnapshotSourceWindow,
		Paths:   []string{"protected.txt"},
	}
	newRun := func(ctx context.Context, manager *workspace.Manager, gate *ReadApprovalGate) *runExecution {
		ginkgo.GinkgoHelper()
		return newRunExecution(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", PermissionProfile: "guarded_workspace_write",
			},
			ReadApprovalGate: gate,
		}, ctx, nil, llama.ChatOptions{})
	}

	ginkgo.It("binds an active decider and cache to the immutable execution session", func() {
		manager := newManager()
		controller := gomock.NewController(ginkgo.GinkgoT())
		decider := NewMockReadApprovalDecider(controller)
		cache := NewMockReadApprovalCache(controller)
		approvalRequest := readapproval.Request{
			SessionID: "session-a", Path: "protected.txt", Tool: workspace.ToolNameReadFile, Lane: "implementation",
		}
		approved := readapproval.Decision{
			Outcome: readapproval.OutcomeApproved, Reason: readapproval.ReasonOperatorApprovedLive, Source: readapproval.SourceOperator,
		}
		cached := approved
		cached.Path = "protected.txt"
		gomock.InOrder(
			cache.EXPECT().Lookup("session-a", "protected.txt").Return(readapproval.Decision{}, false, nil),
			decider.EXPECT().DecideRead(gomock.Any(), approvalRequest).Return(approved, nil),
			cache.EXPECT().Record("session-a", gomock.Any()).Return(nil),
			cache.EXPECT().Lookup("session-a", "protected.txt").Return(cached, true, nil),
		)

		run := newRun(workspace.WithExecutionSessionID(context.Background(), "session-a"), manager, &ReadApprovalGate{
			Decider: decider, Cache: cache,
		})
		first, err := manager.SnapshotWorkspace(run.state.ctx, request)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(first.Entries[0].Body())).To(gomega.Equal("protected\n"))
		_, err = manager.SnapshotWorkspace(run.state.ctx, request)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		wrongSession := workspace.WithExecutionSessionID(run.state.ctx, "session-b")
		_, err = manager.SnapshotWorkspace(wrongSession, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("another execution session")))
	})

	ginkgo.It("binds inactive approval explicitly and fails closed", func() {
		manager := newManager()
		run := newRun(workspace.WithExecutionSessionID(context.Background(), "session-a"), manager, nil)
		_, err := manager.SnapshotWorkspace(run.state.ctx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("requires an invocation-bound approver")))
	})

	ginkgo.It("rejects explicit-empty identity and cancellation before operator routing", func() {
		manager := newManager()
		controller := gomock.NewController(ginkgo.GinkgoT())
		decider := NewMockReadApprovalDecider(controller)
		gate := &ReadApprovalGate{Decider: decider}

		empty := newRun(workspace.WithExecutionSessionID(context.Background(), ""), manager, gate)
		_, err := manager.SnapshotWorkspace(empty.state.ctx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("non-empty execution session id")))

		ctx, cancel := context.WithCancel(workspace.WithExecutionSessionID(context.Background(), "session-a"))
		canceled := newRun(ctx, manager, gate)
		cancel()
		_, err = manager.SnapshotWorkspace(canceled.state.ctx, request)
		gomega.Expect(err).To(gomega.MatchError(context.Canceled))
	})

	ginkgo.It("propagates a live operator denial", func() {
		manager := newManager()
		controller := gomock.NewController(ginkgo.GinkgoT())
		decider := NewMockReadApprovalDecider(controller)
		decider.EXPECT().DecideRead(gomock.Any(), readapproval.Request{
			SessionID: "manager-session", Path: "protected.txt", Tool: workspace.ToolNameReadFile, Lane: "implementation",
		}).Return(readapproval.Decision{
			Outcome: readapproval.OutcomeDenied, Reason: readapproval.ReasonOperatorDeniedLive, Source: readapproval.SourceOperator,
		}, nil)

		run := newRun(context.Background(), manager, &ReadApprovalGate{Decider: decider})
		_, err := manager.SnapshotWorkspace(run.state.ctx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("approval denied")))
	})
})
