package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("workspace execution session identity", ginkgo.Label("race-contract"), func() {
	ginkgo.It("reports whether an execution identity is explicitly bound", func() {
		sessionID, bound := ExecutionSessionIDFromContext(context.Background())
		gomega.Expect(bound).To(gomega.BeFalse())
		gomega.Expect(sessionID).To(gomega.BeEmpty())

		sessionID, bound = ExecutionSessionIDFromContext(WithExecutionSessionID(context.Background(), " nested-session "))
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(sessionID).To(gomega.Equal("nested-session"))

		sessionID, bound = ExecutionSessionIDFromContext(WithExecutionSessionID(context.Background(), ""))
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(sessionID).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps a fallback snapshot after the manager returns to its outer session", func() {
		mgr := NewManager(config.Workspace{SessionID: "nested-session"})
		invocationCtx := mgr.snapshotExecutionSession(context.Background())

		started := make(chan struct{})
		release := make(chan struct{})
		observed := make(chan string, 1)
		go func() {
			close(started)
			<-release
			identity, _ := executionSessionIdentityFromContext(invocationCtx)
			observed <- identity.sessionID
		}()

		gomega.Eventually(started).Should(gomega.BeClosed())
		mgr.SetSessionID("outer-session")
		close(release)

		gomega.Eventually(observed).Should(gomega.Receive(gomega.Equal("nested-session")))
		gomega.Expect(mgr.SessionID()).To(gomega.Equal("outer-session"))
	})

	ginkgo.It("persists submit plan attribution from the invocation context", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
			SessionID:    "outer-session",
		})

		result := mgr.Execute(WithExecutionSessionID(context.Background(), "nested-session"), ToolRequest{
			Tool:      ToolNameSubmitPlan,
			Rationale: "Create the session attribution fixture.",
			Edits: []planartifact.PlanEdit{{
				Path:       "session-attribution.txt",
				NewText:    "nested session\n",
				ReplaceAll: true,
				Purpose:    "Create a fixture that records the nested plan owner.",
			}},
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty())

		body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var artifact planartifact.PlanArtifact
		gomega.Expect(json.Unmarshal(body, &artifact)).To(gomega.Succeed())
		gomega.Expect(artifact.SessionID).To(gomega.Equal("nested-session"))
		gomega.Expect(mgr.SessionID()).To(gomega.Equal("outer-session"))
	})

	ginkgo.It("does not replace an explicitly empty invocation identity with the manager fallback", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
			SessionID:    "outer-session",
		})

		result := mgr.Execute(WithExecutionSessionID(context.Background(), ""), ToolRequest{
			Tool:      ToolNameSubmitPlan,
			Rationale: "Reject an unattributed plan rather than assigning it to the outer session.",
			Edits: []planartifact.PlanEdit{{
				Path:       "unattributed.txt",
				NewText:    "must not persist\n",
				ReplaceAll: true,
				Purpose:    "Exercise explicit empty invocation identity handling.",
			}},
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		_, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	})

	ginkgo.It("uses the invocation identity for system search approval", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, SessionID: "outer-session"})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		var approvalRequest SystemSearchAccessRequest
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
				approvalRequest = req
				return SystemSearchAccessDecision{Approved: false}, nil
			}).
			Times(1)

		_ = mgr.Execute(WithExecutionSessionID(context.Background(), "nested-session"), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "verify nested approval attribution",
		})

		gomega.Expect(approvalRequest.SessionID).To(gomega.Equal("nested-session"))
		gomega.Expect(mgr.SessionID()).To(gomega.Equal("outer-session"))
	})

	ginkgo.It("isolates system search grants between nested and outer sessions", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(sibling, "README.md"), []byte("sibling\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, SessionID: "outer-session"})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
				gomega.Expect(req.SessionID).To(gomega.Equal("nested-session"))
				return SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil
			}).
			Times(1)

		nestedCtx := WithExecutionSessionID(context.Background(), "nested-session")
		approval := mgr.Execute(nestedCtx, ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "approve nested sibling inspection",
		})
		gomega.Expect(approval.Error).To(gomega.BeEmpty())

		nestedRead := mgr.Execute(nestedCtx, ToolRequest{Tool: ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(nestedRead.Error).To(gomega.BeEmpty())
		gomega.Expect(nestedRead.Output).To(gomega.ContainSubstring("README.md"))

		outerRead := mgr.Execute(WithExecutionSessionID(context.Background(), "outer-session"), ToolRequest{
			Tool: ToolNameListFiles,
			Path: "../sibling",
		})
		gomega.Expect(outerRead.Error).To(gomega.ContainSubstring("has not been approved"))
	})

	ginkgo.It("keeps explicit empty identity separate from fallback empty grants", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			Return(SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil).
			Times(1)

		fallbackApproval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "approve the manager fallback session",
		})
		gomega.Expect(fallbackApproval.Error).To(gomega.BeEmpty())

		explicitEmptyCtx := WithExecutionSessionID(context.Background(), "")
		explicitEmptyRead := mgr.Execute(explicitEmptyCtx, ToolRequest{Tool: ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(explicitEmptyRead.Error).To(gomega.ContainSubstring("has not been approved"))

		explicitEmptyApproval := mgr.Execute(explicitEmptyCtx, ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "must not create an unattributed grant",
		})
		gomega.Expect(explicitEmptyApproval.Error).To(gomega.ContainSubstring("execution session id is required"))
	})

	ginkgo.It("does not commit an approval returned after the invocation is canceled", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(sibling, "README.md"), []byte("sibling\n"), 0o644)).To(gomega.Succeed())

		started := make(chan struct{})
		release := make(chan struct{})
		returned := make(chan struct{})
		var releaseOnce sync.Once
		ginkgo.DeferCleanup(func() {
			releaseOnce.Do(func() { close(release) })
		})

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			DoAndReturn(func(context.Context, SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
				close(started)
				<-release
				close(returned)

				return SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil
			}).
			Times(1)
		mgr.SetSystemSearchAccessApprover(approver)
		invocationCtx, cancelInvocation := context.WithCancel(WithExecutionSessionID(context.Background(), "canceled-session"))
		ginkgo.DeferCleanup(cancelInvocation)

		resultCh := make(chan ToolResult, 1)
		go func() {
			resultCh <- mgr.Execute(invocationCtx, ToolRequest{
				Tool:    ToolNameSystemSearchAccess,
				Path:    "../sibling",
				Purpose: "exercise late approval disposal",
			})
		}()

		gomega.Eventually(started).Should(gomega.BeClosed())
		cancelInvocation()
		var canceledResult ToolResult
		gomega.Eventually(resultCh).Should(gomega.Receive(&canceledResult))
		gomega.Expect(canceledResult.Error).NotTo(gomega.BeEmpty())

		releaseOnce.Do(func() { close(release) })
		gomega.Eventually(returned).Should(gomega.BeClosed())

		freshCtx := WithExecutionSessionID(context.Background(), "canceled-session")
		read := mgr.Execute(freshCtx, ToolRequest{Tool: ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"))
	})

	ginkgo.It("synchronizes ordinary manager session access", func() {
		mgr := NewManager(config.Workspace{SessionID: "session-0"})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				mgr.SetSessionID("session-write")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = mgr.SessionID()
			}
		}()
		wg.Wait()
	})
})
