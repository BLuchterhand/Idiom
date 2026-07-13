package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("workspace invocation snapshots", ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"), func() {
	ginkgo.It("captures canonical source evidence and invocation metadata", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package physical\n")
		if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetProtectedWritePaths([]string{"physical/source.go"})
		ctx := WithExecutionSessionID(context.Background(), "snapshot-session")

		snapshot, err := manager.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSubmitPlanAnchor,
			Paths:   []string{"alias/source.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(snapshot.Purpose).To(gomega.Equal(WorkspaceSnapshotSubmitPlanAnchor))
		gomega.Expect(snapshot.Root).To(gomega.Equal(manager.Root()))
		gomega.Expect(snapshot.Entries).To(gomega.HaveLen(1))
		entry := snapshot.Entries[0]
		gomega.Expect(entry.PresentedPath).To(gomega.Equal("alias/source.go"))
		gomega.Expect(entry.Identity.CanonicalID).To(gomega.Equal("physical/source.go"))
		gomega.Expect(entry.Kind).To(gomega.Equal(WorkspaceSnapshotFile))
		gomega.Expect(string(entry.Body())).To(gomega.Equal("package physical\n"))
		gomega.Expect(entry.Evidence.DigestSHA256).To(gomega.HavePrefix("sha256:"))
		gomega.Expect(entry.Evidence.SizeBytes).To(gomega.Equal(int64(len(entry.Body()))))
		gomega.Expect(entry.Metadata.Protected).To(gomega.BeTrue())
	})

	ginkgo.It("rejects configured read policy denies", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "secret.txt"), "secret\n")
		manager := NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				ToolNameReadFile: {{Pattern: "secret.txt", Decision: "deny"}},
			},
		})

		_, err := manager.SnapshotWorkspace(context.Background(), WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotCitation,
			Paths:   []string{"secret.txt"},
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace policy denies")))
	})

	ginkgo.It("captures a missing canonical identity only after rooted absence revalidation", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "generated"), 0o755)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root})

		snapshot, err := manager.SnapshotWorkspace(context.Background(), WorkspaceSnapshotRequest{
			Purpose:      WorkspaceSnapshotNewFileWaiver,
			Paths:        []string{"generated/new.go"},
			AllowMissing: true,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(snapshot.Entries).To(gomega.HaveLen(1))
		entry := snapshot.Entries[0]
		gomega.Expect(entry.Identity.CanonicalID).To(gomega.Equal("generated/new.go"))
		gomega.Expect(entry.Kind).To(gomega.Equal(WorkspaceSnapshotMissing))
		gomega.Expect(entry.Body()).To(gomega.BeNil())
	})

	ginkgo.It("rejects a missing-path alias that retargets before absence is recorded", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "first"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "second"), 0o755)).To(gomega.Succeed())
		if err := os.Symlink("first", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{
			Root: root, CWD: root,
			ToolRules: map[string][]config.ToolRule{
				ToolNameReadFile: {{Pattern: "second/**", Decision: "deny"}},
			},
		})
		manager.SetProtectedWritePaths([]string{"second/source.go"})
		var once sync.Once
		ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage != workspaceReadBeforeOpen || filepath.Clean(checkpoint.Path) != filepath.Join(manager.Root(), "first", "new.go") {
				return
			}
			once.Do(func() {
				gomega.Expect(os.Remove(filepath.Join(root, "alias"))).To(gomega.Succeed())
				gomega.Expect(os.Symlink("second", filepath.Join(root, "alias"))).To(gomega.Succeed())
			})
		})

		_, err := manager.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose:      WorkspaceSnapshotNewFileWaiver,
			Paths:        []string{"alias/new.go"},
			AllowMissing: true,
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("identity changed")), fmt.Sprintf("retargeted missing snapshot error: %v", err))
	})

	ginkgo.It("rejects an existing alias that retargets after policy binding but before open", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "first", "source.go"), "package first\n")
		writeTestFile(filepath.Join(root, "second", "source.go"), "package second\n")
		if err := os.Symlink("first", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Ask}},
		})
		approver := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), WorkspaceSnapshotReadApprovalRequest{
			SessionID: "retarget-session", Purpose: WorkspaceSnapshotSourceWindow,
			CanonicalID: "first/source.go", PolicyDecision: toolpolicy.Ask,
		}).Return(WorkspaceSnapshotReadApprovalDecision{Approved: true}, nil)
		var once sync.Once
		ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage != workspaceReadBeforeOpen {
				return
			}
			once.Do(func() {
				gomega.Expect(os.Remove(filepath.Join(root, "alias"))).To(gomega.Succeed())
				gomega.Expect(os.Symlink("second", filepath.Join(root, "alias"))).To(gomega.Succeed())
			})
		})
		ctx = WithExecutionSessionID(ctx, "retarget-session")
		ctx = WithWorkspaceSnapshotReadApprover(ctx, approver)

		snapshot, err := manager.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"alias/source.go"},
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("changed before open")))
		gomega.Expect(snapshot.AuditEvents()).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("routes Ask through a session-bound host approver", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "protected.txt"), "protected\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "protected.txt", Decision: toolpolicy.Ask}},
		})
		session := WithExecutionSessionID(context.Background(), "snapshot-ask")
		request := WorkspaceSnapshotRequest{Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"protected.txt"}}

		_, absentErr := manager.SnapshotWorkspace(session, request)
		gomega.Expect(absentErr).To(gomega.MatchError(gomega.ContainSubstring("requires an invocation-bound approver")))

		approver := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		approvalRequest := WorkspaceSnapshotReadApprovalRequest{
			SessionID: "snapshot-ask", Purpose: WorkspaceSnapshotSourceWindow,
			CanonicalID: "protected.txt", PolicyDecision: toolpolicy.Ask,
		}
		approver.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), approvalRequest).
			Return(WorkspaceSnapshotReadApprovalDecision{Approved: true}, nil)
		approved := WithWorkspaceSnapshotReadApprover(session, approver)
		snapshot, err := manager.SnapshotWorkspace(approved, request)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(snapshot.Entries[0].Body())).To(gomega.Equal("protected\n"))
		gomega.Expect(snapshot.AuditEvents()).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Reason", WorkspaceSnapshotReasonApprovalGranted),
			gomega.HaveField("ApprovalMatched", true),
		)))
	})

	ginkgo.It("fails closed on approver denial, error, late cancellation, and wrong session", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "protected.txt"), "protected\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "protected.txt", Decision: toolpolicy.Ask}},
		})
		request := WorkspaceSnapshotRequest{Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"protected.txt"}}
		approvalRequest := WorkspaceSnapshotReadApprovalRequest{
			SessionID: "snapshot-ask", Purpose: WorkspaceSnapshotSourceWindow,
			CanonicalID: "protected.txt", PolicyDecision: toolpolicy.Ask,
		}

		denier := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		denier.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), approvalRequest).
			Return(WorkspaceSnapshotReadApprovalDecision{}, nil)
		deniedCtx := WithWorkspaceSnapshotReadApprover(WithExecutionSessionID(context.Background(), "snapshot-ask"), denier)
		_, err := manager.SnapshotWorkspace(deniedCtx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("approval denied")))

		failing := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		failing.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), approvalRequest).
			Return(WorkspaceSnapshotReadApprovalDecision{}, errors.New("operator transport failed"))
		failingCtx := WithWorkspaceSnapshotReadApprover(WithExecutionSessionID(context.Background(), "snapshot-ask"), failing)
		_, err = manager.SnapshotWorkspace(failingCtx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("operator transport failed")))

		cancelCtx, cancel := context.WithCancel(WithExecutionSessionID(context.Background(), "snapshot-ask"))
		late := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		late.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), approvalRequest).
			DoAndReturn(func(context.Context, WorkspaceSnapshotReadApprovalRequest) (WorkspaceSnapshotReadApprovalDecision, error) {
				cancel()
				return WorkspaceSnapshotReadApprovalDecision{Approved: true}, nil
			})
		cancelCtx = WithWorkspaceSnapshotReadApprover(cancelCtx, late)
		_, err = manager.SnapshotWorkspace(cancelCtx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("approval canceled")))

		wrongSessionApprover := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		ownerCtx := WithWorkspaceSnapshotReadApprover(WithExecutionSessionID(context.Background(), "session-a"), wrongSessionApprover)
		wrongCtx := WithExecutionSessionID(ownerCtx, "session-b")
		_, err = manager.SnapshotWorkspace(wrongCtx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("another execution session")))

		emptyIdentityApprover := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		emptyIdentityCtx := WithWorkspaceSnapshotReadApprover(WithExecutionSessionID(context.Background(), ""), emptyIdentityApprover)
		_, err = manager.SnapshotWorkspace(emptyIdentityCtx, request)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("non-empty execution session id")))
	})

	ginkgo.It("bounds body reads while citation snapshots remain metadata-only", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "large.txt"), "0123456789\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})

		_, err := manager.SnapshotWorkspace(context.Background(), WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"large.txt"}, MaxBodyBytes: 4,
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("exceeds remaining byte limit")))

		citation, err := manager.SnapshotWorkspace(context.Background(), WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotCitation, Paths: []string{"large.txt"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(citation.Entries[0].Body()).To(gomega.BeNil())
		gomega.Expect(citation.Entries[0].Evidence.SizeBytes).To(gomega.Equal(int64(11)))
		gomega.Expect(citation.Entries[0].Evidence.DigestSHA256).To(gomega.BeEmpty())
		gomega.Expect(citation.AuditEvents()).NotTo(gomega.BeEmpty(), "snapshot without an auditor did not return persistable audit records")
	})

	ginkgo.It("rejects file growth and cancellation during a bounded opened-handle read", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := filepath.Join(root, "growing.txt")
		writeTestFile(target, strings.Repeat("a", 20*1024))
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		var once sync.Once
		growCtx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage == workspaceReadDuringLimitedRead {
				once.Do(func() {
					file, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					_, err = file.WriteString(strings.Repeat("b", 20*1024))
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					gomega.Expect(file.Close()).To(gomega.Succeed())
				})
			}
		})
		failed, err := manager.SnapshotWorkspace(growCtx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"growing.txt"}, MaxBodyBytes: 30 * 1024,
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("exceeds remaining byte limit")))
		gomega.Expect(failed.AuditEvents()).NotTo(gomega.BeEmpty())

		writeTestFile(target, strings.Repeat("c", 20*1024))
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancelCtx = withWorkspaceReadCheckpoint(cancelCtx, func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage == workspaceReadDuringLimitedRead {
				cancel()
			}
		})
		_, err = manager.SnapshotWorkspace(cancelCtx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"growing.txt"}, MaxBodyBytes: 30 * 1024,
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("context canceled")))
	})

	ginkgo.It("serializes only the durable projection and emits typed audit events", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "source.go"), "package source\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		auditor := NewMockWorkspaceSnapshotAuditor(gomock.NewController(ginkgo.GinkgoT()))
		var events []WorkspaceSnapshotAuditEvent
		auditor.EXPECT().RecordWorkspaceSnapshot(gomock.Any(), gomock.Any()).Times(2).Do(func(_ context.Context, event WorkspaceSnapshotAuditEvent) {
			events = append(events, event)
		})
		ctx := WithWorkspaceSnapshotAuditor(context.Background(), auditor)

		snapshot, err := manager.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"source.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		encoded, err := json.Marshal(snapshot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(root))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("package source"))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("absolute"))
		gomega.Expect(string(encoded)).To(gomega.ContainSubstring("source.go"))
		gomega.Expect(events).To(gomega.HaveLen(2))
		gomega.Expect(events[0].Kind).To(gomega.Equal(WorkspaceSnapshotAuditAuthority))
		gomega.Expect(events[1].Kind).To(gomega.Equal(WorkspaceSnapshotAuditRead))
	})

	ginkgo.It("audits fallback, explicit-empty, and explicit-nonempty execution identity presence", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "source.go"), "package source\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		for _, test := range []struct {
			ctx  context.Context
			want WorkspaceSnapshotExecutionIdentityState
		}{
			{ctx: context.Background(), want: WorkspaceSnapshotIdentityFallback},
			{ctx: WithExecutionSessionID(context.Background(), ""), want: WorkspaceSnapshotIdentityExplicitEmpty},
			{ctx: WithExecutionSessionID(context.Background(), "session-present"), want: WorkspaceSnapshotIdentityExplicitNonempty},
		} {
			snapshot, err := manager.SnapshotWorkspace(test.ctx, WorkspaceSnapshotRequest{
				Purpose: WorkspaceSnapshotCitation, Paths: []string{"source.go"},
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(snapshot.AuditEvents()).NotTo(gomega.BeEmpty())
			gomega.Expect(snapshot.AuditEvents()[0].ExecutionIdentityState).To(gomega.Equal(test.want))
		}
	})
})
