package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("producer-owned source windows", ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"), func() {
	ginkgo.It("binds exact displayed bodies to canonical revision and mutation generation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package physical\n\nfunc Run() {}\n")
		if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 2, EndLine: 3})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("read result: %#v", result))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.SourceWindows).To(gomega.HaveLen(1))
		windows := result.SourceWindows()
		gomega.Expect(windows).To(gomega.HaveLen(1))
		window := windows[0]
		gomega.Expect(window.Evidence.CanonicalID).To(gomega.Equal("physical/source.go"))
		gomega.Expect(window.Evidence.StartLine).To(gomega.Equal(2))
		gomega.Expect(window.Evidence.EndLine).To(gomega.Equal(3))
		gomega.Expect(window.Evidence.TotalLines).To(gomega.Equal(3))
		gomega.Expect(window.Content).To(gomega.Equal("\nfunc Run() {}"))
		gomega.Expect(window.Evidence.WindowSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(window.Evidence.RevisionSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(window.Evidence.MutationEpoch).To(gomega.Equal(manager.MutationEpoch()))
		current, err := manager.SourceWindowCurrent(context.Background(), window.Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())

		windows[0].Content = "mutated clone"
		gomega.Expect(result.SourceWindows()[0].Content).To(gomega.Equal("\nfunc Run() {}"))
		encoded, err := json.Marshal(result.SourceWindows()[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("func Run"))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(manager.Root()))

		write := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameWriteFile, Path: "physical/source.go", Content: "package physical\n\nfunc Changed() {}\n"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), fmt.Sprintf("write result: %#v", write))
		current, err = manager.SourceWindowCurrent(context.Background(), window.Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeFalse())
	})

	ginkgo.It("keeps an unchanged presentation alias current", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package source\n")
		if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))

		current, err := manager.SourceWindowCurrent(context.Background(), result.SourceWindows()[0].Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())
	})

	ginkgo.It("rejects an alias that now resolves to another canonical source before approval", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := "package source\n"
		writeTestFile(filepath.Join(root, "physical", "source.go"), body)
		writeTestFile(filepath.Join(root, "other", "source.go"), body)
		alias := filepath.Join(root, "alias")
		if err := os.Symlink("physical", alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "alias/**", Decision: toolpolicy.Ask}},
		})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
		gomega.Expect(os.Symlink("other", alias)).To(gomega.Succeed())

		current, err := manager.SourceWindowCurrent(context.Background(), result.SourceWindows()[0].Evidence)
		gomega.Expect(current).To(gomega.BeFalse())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("policy subject no longer resolves to the recorded canonical identity")))
	})

	ginkgo.It("rejects a removed presentation alias while the canonical source remains", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package source\n")
		alias := filepath.Join(root, "alias")
		if err := os.Symlink("physical", alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		gomega.Expect(os.Remove(alias)).To(gomega.Succeed())

		current, err := manager.SourceWindowCurrent(context.Background(), result.SourceWindows()[0].Evidence)
		gomega.Expect(current).To(gomega.BeFalse())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("policy subject no longer resolves to the recorded canonical identity")))
	})

	ginkgo.It("rejects a presentation alias retargeted while opening the source", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := "package source\n"
		writeTestFile(filepath.Join(root, "physical", "source.go"), body)
		writeTestFile(filepath.Join(root, "other", "source.go"), body)
		alias := filepath.Join(root, "alias")
		if err := os.Symlink("physical", alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		ctx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage != workspaceReadBeforeOpen {
				return
			}
			gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
			gomega.Expect(os.Symlink("other", alias)).To(gomega.Succeed())
		})

		current, err := manager.SourceWindowCurrent(ctx, result.SourceWindows()[0].Evidence)
		gomega.Expect(current).To(gomega.BeFalse())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("changed before open")))
	})

	ginkgo.It("keeps a direct canonical source path current", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package source\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "physical/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("canonical read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		evidence := result.SourceWindows()[0].Evidence
		gomega.Expect(evidence.Authority.PolicySubject).To(gomega.Equal(evidence.CanonicalID))

		current, err := manager.SourceWindowCurrent(context.Background(), evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())
	})

	ginkgo.It("preserves stricter alias authority across durable resume and policy changes", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "physical", "source.go"), "package physical\n")
		if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		producer := NewManager(config.Workspace{Root: root, CWD: root})
		producer.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "alias/**", Decision: toolpolicy.Ask}},
		})
		result := producer.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias read: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		evidence := result.SourceWindows()[0].Evidence
		gomega.Expect(evidence.Authority).To(gomega.Equal(toolfacts.SourceWindowAuthorityEvidence{
			PolicySubject: "alias/source.go", PolicyDecision: toolpolicy.Ask,
		}))
		encoded, err := json.Marshal(evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(root))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(".."))
		var resumedEvidence toolfacts.SourceWindowEvidence
		gomega.Expect(json.Unmarshal(encoded, &resumedEvidence)).To(gomega.Succeed())

		resumed := NewManager(config.Workspace{Root: root, CWD: root})
		_, err = resumed.SourceWindowCurrent(context.Background(), resumedEvidence)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("requires an invocation-bound approver")))

		approver := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), WorkspaceSnapshotReadApprovalRequest{
			SessionID: "resume-session", Purpose: WorkspaceSnapshotSourceWindow,
			CanonicalID: "physical/source.go", PolicyDecision: toolpolicy.Ask,
		}).Return(WorkspaceSnapshotReadApprovalDecision{Approved: true}, nil)
		approved := WithExecutionSessionID(context.Background(), "resume-session")
		approved = WithWorkspaceSnapshotReadApprover(approved, approver)
		current, err := resumed.SourceWindowCurrent(approved, resumedEvidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())

		resumed.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "alias/**", Decision: toolpolicy.Deny}},
		})
		_, err = resumed.SourceWindowCurrent(context.Background(), resumedEvidence)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace policy denies")))

		denyProducer := NewManager(config.Workspace{Root: root, CWD: root})
		denyProducer.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "alias/**", Decision: toolpolicy.Deny}},
		})
		deniedRead := denyProducer.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "alias/source.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(deniedRead.SourceWindows()).To(gomega.HaveLen(1))
		gomega.Expect(deniedRead.SourceWindows()[0].Evidence.Authority.PolicyDecision).To(gomega.Equal(toolpolicy.Deny))
		_, err = NewManager(config.Workspace{Root: root, CWD: root}).SourceWindowCurrent(context.Background(), deniedRead.SourceWindows()[0].Evidence)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace policy denies")))
	})

	ginkgo.It("does not authorize hidden log tails or truncated prefetch line tails", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "events.log"), strings.Repeat("x", defaultLogLineCharCap+200)+"hidden-tail\n")
		writeTestFile(filepath.Join(root, "large.md"), strings.Repeat("a", DefaultPrefetchMaxBytesPerFile+200)+"hidden-tail\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})

		logResult := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "events.log"})
		gomega.Expect(logResult.Error).To(gomega.BeEmpty())
		gomega.Expect(logResult.Output).NotTo(gomega.ContainSubstring("hidden-tail"))
		gomega.Expect(logResult.SourceWindows()).To(gomega.BeEmpty())

		prefetch := manager.PrefetchReadFiles(context.Background(), []string{"large.md"}, PrefetchReadOptions{})
		gomega.Expect(prefetch.Results).To(gomega.HaveLen(1))
		gomega.Expect(prefetch.Results[0].Output).NotTo(gomega.ContainSubstring("hidden-tail"))
		gomega.Expect(prefetch.Results[0].SourceWindows()).To(gomega.BeEmpty())
	})

	ginkgo.It("canonicalizes and revalidates language source windows", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/sourcewindow\n\ngo 1.24\n")
		writeTestFile(filepath.Join(root, "sample.go"), "package sample\n\nfunc Run() {\n\tprintln(\"ok\")\n}\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 8})

		result := manager.Execute(context.Background(), ToolRequest{Tool: "go_read_symbol", Pattern: "Run", Path: "sample.go"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("language read result: %#v", result))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		window := result.SourceWindows()[0]
		gomega.Expect(window.Evidence.CanonicalID).To(gomega.Equal("sample.go"))
		gomega.Expect(window.Content).To(gomega.ContainSubstring("func Run()"))
		gomega.Expect(window.Content).NotTo(gomega.ContainSubstring("3:func"))
		current, err := manager.SourceWindowCurrent(context.Background(), window.Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())
	})

	ginkgo.It("binds snapshot-backed authority to the original nested-workdir presentation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "subdir", "physical", "source.go"), "package source\n\nfunc Run() {}\n")
		if err := os.Symlink("physical", filepath.Join(root, "subdir", "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		producer := NewManager(config.Workspace{Root: root, CWD: root})
		producer.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "subdir/alias/**", Decision: toolpolicy.Ask}},
		})
		approver := NewMockWorkspaceSnapshotReadApprover(gomock.NewController(ginkgo.GinkgoT()))
		approval := WorkspaceSnapshotReadApprovalRequest{
			SessionID: "nested-session", Purpose: WorkspaceSnapshotSourceWindow,
			CanonicalID: "subdir/physical/source.go", PolicyDecision: toolpolicy.Ask,
		}
		approver.EXPECT().ApproveWorkspaceSnapshotRead(gomock.Any(), approval).
			Return(WorkspaceSnapshotReadApprovalDecision{Approved: true}, nil).Times(2)
		ctx := WithExecutionSessionID(context.Background(), "nested-session")
		ctx = WithWorkspaceSnapshotReadApprover(ctx, approver)

		snapshot, err := producer.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow, Paths: []string{"alias/source.go"}, Workdir: "subdir",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(snapshot.Entries).To(gomega.HaveLen(1))
		entry := snapshot.Entries[0]
		gomega.Expect(entry.Metadata.PolicySubject).To(gomega.Equal("subdir/alias/source.go"))
		gomega.Expect(entry.Metadata.PolicyDecision).To(gomega.Equal(toolpolicy.Ask))
		window, err := workspaceSourceWindow(entry.Identity, toolfacts.SourceWindowAuthorityEvidence{
			PolicySubject: entry.Metadata.PolicySubject, PolicyDecision: entry.Metadata.PolicyDecision,
		}, entry.Body(), 1, 3, false, producer.MutationEpoch())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		evidence := window.Evidence
		gomega.Expect(evidence.Authority).To(gomega.Equal(toolfacts.SourceWindowAuthorityEvidence{
			PolicySubject: "subdir/alias/source.go", PolicyDecision: toolpolicy.Ask,
		}))

		resumed := NewManager(config.Workspace{Root: root, CWD: root})
		current, err := resumed.SourceWindowCurrent(ctx, evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())

		resumed.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "subdir/alias/**", Decision: toolpolicy.Deny}},
		})
		_, err = resumed.SourceWindowCurrent(context.Background(), evidence)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace policy denies")))
	})

	ginkgo.It("keeps durable facts and ephemeral attachments in identical deduplicated order", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "source.txt"), "first\nsecond\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "source.txt"})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))

		duplicate := result.SourceWindows()[0]
		duplicate.Evidence.Truncated = true
		attachWorkspaceSourceWindows(&result, duplicate, duplicate)

		attachments := result.SourceWindows()
		gomega.Expect(attachments).To(gomega.HaveLen(1))
		gomega.Expect(result.Facts.SourceWindows).To(gomega.Equal([]toolfacts.SourceWindowEvidence{attachments[0].Evidence}))
		gomega.Expect(attachments[0].Evidence.Truncated).To(gomega.BeTrue())
	})

	ginkgo.It("streams late ranges beyond the whole-file cap with full-revision currentness", func() {
		root := ginkgo.GinkgoT().TempDir()
		large := filepath.Join(root, "large.txt")
		body := strings.Repeat("padding\n", 400_000)
		writeTestFile(large, body)
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 32})
		auditor := NewMockWorkspaceSnapshotAuditor(gomock.NewController(ginkgo.GinkgoT()))
		var auditEvents []WorkspaceSnapshotAuditEvent
		auditor.EXPECT().RecordWorkspaceSnapshot(gomock.Any(), gomock.Any()).Times(4).Do(func(_ context.Context, event WorkspaceSnapshotAuditEvent) {
			auditEvents = append(auditEvents, event)
		})
		currentCtx := WithWorkspaceSnapshotAuditor(context.Background(), auditor)
		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "large.txt", StartLine: 399_999, EndLine: 400_000,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("late streaming read: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("399999:padding"))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("400000:padding"))
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		window := result.SourceWindows()[0]
		gomega.Expect(window.Content).To(gomega.Equal("padding\npadding"))
		gomega.Expect(window.Evidence.RevisionSHA256).To(gomega.Equal(toolfacts.SourceWindowSHA256([]byte(body))))
		gomega.Expect(window.Evidence.TotalLines).To(gomega.Equal(400_000))
		current, err := manager.SourceWindowCurrent(currentCtx, window.Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())

		file, err := os.OpenFile(large, os.O_WRONLY, 0)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = file.WriteAt([]byte("P"), 0)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(file.Close()).To(gomega.Succeed())
		current, err = manager.SourceWindowCurrent(currentCtx, window.Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeFalse(), "outside-window mutation did not invalidate the full revision")
		gomega.Expect(auditEvents).To(gomega.ContainElements(
			gomega.And(gomega.HaveField("Kind", WorkspaceSnapshotAuditRead), gomega.HaveField("Outcome", WorkspaceSnapshotOutcomeCurrent), gomega.HaveField("WindowSHA256", window.Evidence.WindowSHA256)),
			gomega.And(gomega.HaveField("Kind", WorkspaceSnapshotAuditRead), gomega.HaveField("Outcome", WorkspaceSnapshotOutcomeStale)),
		))
	})

	ginkgo.It("bounds selected lines and cancels or rejects growth during streaming scans", func() {
		root := ginkgo.GinkgoT().TempDir()
		large := filepath.Join(root, "large.txt")
		writeTestFile(large, strings.Repeat("line\n", 5_000))
		growing := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 64})
		var once sync.Once
		growCtx := withWorkspaceReadCheckpoint(context.Background(), func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage != workspaceReadDuringLimitedRead {
				return
			}
			once.Do(func() {
				file, err := os.OpenFile(large, os.O_APPEND|os.O_WRONLY, 0)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				_, err = file.WriteString("external\n")
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(file.Close()).To(gomega.Succeed())
			})
		})
		grew := growing.Execute(growCtx, ToolRequest{Tool: ToolNameReadFile, Path: "large.txt", StartLine: 4_000, EndLine: 4_001})
		gomega.Expect(grew.Error).To(gomega.ContainSubstring("changed"))
		gomega.Expect(grew.SourceWindows()).To(gomega.BeEmpty())

		writeTestFile(large, strings.Repeat("line\n", 5_000))
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancelCtx = withWorkspaceReadCheckpoint(cancelCtx, func(checkpoint workspaceReadCheckpoint) {
			if checkpoint.Stage == workspaceReadDuringLimitedRead {
				cancel()
			}
		})
		canceled := growing.Execute(cancelCtx, ToolRequest{Tool: ToolNameReadFile, Path: "large.txt", StartLine: 4_000, EndLine: 4_001})
		gomega.Expect(canceled.Error).To(gomega.ContainSubstring("canceled"))
		gomega.Expect(canceled.SourceWindows()).To(gomega.BeEmpty())
		gomega.Expect(canceled.Facts).To(gomega.BeNil())

		writeTestFile(large, strings.Repeat("x", 128*1024))
		giantLine := growing.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "large.txt", StartLine: 1, EndLine: 1})
		gomega.Expect(giantLine.Error).To(gomega.ContainSubstring("requested line window exceeds"))
		gomega.Expect(giantLine.SourceWindows()).To(gomega.BeEmpty())
		gomega.Expect(giantLine.Facts).To(gomega.BeNil())
	})

	ginkgo.It("rejects formatted line windows that exceed the independent output bound", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "blank-lines.txt"), strings.Repeat("\n", 120))
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 512, MaxOutputBytes: 64})

		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "blank-lines.txt", StartLine: 1, EndLine: 120,
		})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("formatted line window exceeds configured output byte limit 64"))
		gomega.Expect(result.Output).To(gomega.BeEmpty())
		gomega.Expect(result.Paths).To(gomega.BeEmpty())
		gomega.Expect(result.Facts).To(gomega.BeNil())
		gomega.Expect(result.SourceWindows()).To(gomega.BeEmpty())
	})

	ginkgo.It("clamps omitted-end reads beyond EOF to the final line for source and log files", func() {
		root := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 50)
		for index := range lines {
			lines[index] = fmt.Sprintf("line-%d", index+1)
		}
		body := strings.Join(lines, "\n") + "\n"
		writeTestFile(filepath.Join(root, "source.txt"), body)
		writeTestFile(filepath.Join(root, "events.log"), body)
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 64, MaxOutputBytes: 4096})

		source := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "source.txt", StartLine: 99})
		gomega.Expect(source.Error).To(gomega.BeEmpty(), fmt.Sprintf("source EOF read: %#v", source))
		gomega.Expect(source.Output).To(gomega.Equal("50:line-50"))
		gomega.Expect(source.SourceWindows()).To(gomega.HaveLen(1))
		gomega.Expect(source.SourceWindows()[0].Evidence.StartLine).To(gomega.Equal(50))
		gomega.Expect(source.SourceWindows()[0].Evidence.EndLine).To(gomega.Equal(50))

		logResult := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "events.log", StartLine: 99})
		gomega.Expect(logResult.Error).To(gomega.BeEmpty(), fmt.Sprintf("log EOF read: %#v", logResult))
		gomega.Expect(logResult.Output).To(gomega.Equal("50:line-50"))
		gomega.Expect(logResult.SourceWindows()).To(gomega.BeEmpty())
	})

	ginkgo.It("preserves empty, trailing-newline, clamped, reversed, and alias range parity", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "empty.txt"), "")
		writeTestFile(filepath.Join(root, "physical", "lines.txt"), "first\nsecond\n")
		if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 64})

		empty := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "empty.txt", StartLine: 1, EndLine: 1})
		gomega.Expect(empty.Error).To(gomega.BeEmpty())
		gomega.Expect(empty.Summary).To(gomega.ContainSubstring("lines 0-0"))
		gomega.Expect(empty.SourceWindows()).To(gomega.HaveLen(1))
		current, err := manager.SourceWindowCurrent(context.Background(), empty.SourceWindows()[0].Evidence)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(current).To(gomega.BeTrue())

		trailing := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "alias/lines.txt", StartLine: 2, EndLine: 2})
		gomega.Expect(trailing.Error).To(gomega.BeEmpty())
		gomega.Expect(trailing.Output).To(gomega.Equal("2:second"))
		gomega.Expect(trailing.SourceWindows()[0].Evidence.CanonicalID).To(gomega.Equal("physical/lines.txt"))

		clamped := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "alias/lines.txt", StartLine: 99, EndLine: 100})
		gomega.Expect(clamped.Error).To(gomega.BeEmpty())
		gomega.Expect(clamped.Output).To(gomega.Equal("1:first\n2:second"))
		gomega.Expect(clamped.Summary).To(gomega.ContainSubstring("clamped"))

		reversed := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "alias/lines.txt", StartLine: 2, EndLine: 1})
		gomega.Expect(reversed.Error).To(gomega.BeEmpty())
		gomega.Expect(reversed.Output).To(gomega.Equal("1:first\n2:second"))
		gomega.Expect(reversed.Summary).To(gomega.ContainSubstring("auto-swapped"))

		logLines := make([]string, 200)
		for index := range logLines {
			logLines[index] = fmt.Sprintf("event-%d", index+1)
		}
		writeTestFile(filepath.Join(root, "events.log"), strings.Join(logLines, "\n")+"\n")
		lateLog := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "events.log", StartLine: 150, EndLine: 152})
		gomega.Expect(lateLog.Error).To(gomega.BeEmpty())
		gomega.Expect(lateLog.Output).To(gomega.ContainSubstring("150:event-150"))
		gomega.Expect(lateLog.Output).To(gomega.ContainSubstring("152:event-152"))
		gomega.Expect(lateLog.SourceWindows()).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps read-only shell verification on the current mutation generation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "source.txt"), "source\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true})
		before := manager.MutationEpoch()

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "pwd"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("read-only shell result: %#v", result))
		gomega.Expect(manager.MutationEpoch()).To(gomega.Equal(before))
	})

	ginkgo.It("advances the mutation generation for an executed additive shell that later fails", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true})
		before := manager.MutationEpoch()

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "touch partial.txt; false"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(filepath.Join(root, "partial.txt")).To(gomega.BeAnExistingFile())
		gomega.Expect(manager.MutationEpoch()).To(gomega.Equal(before + 1))
	})

	ginkgo.It("advances once for executed unknown shell commands on success and partial failure", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true})

		beforeSuccess := manager.MutationEpoch()
		success := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "printf unknown"})
		gomega.Expect(success.Error).To(gomega.BeEmpty(), fmt.Sprintf("unknown successful shell result: %#v", success))
		gomega.Expect(manager.MutationEpoch()).To(gomega.Equal(beforeSuccess + 1))

		beforeFailure := manager.MutationEpoch()
		failure := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "printf partial > unknown.txt; false"})
		gomega.Expect(failure.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(filepath.Join(root, "unknown.txt")).To(gomega.BeAnExistingFile())
		gomega.Expect(manager.MutationEpoch()).To(gomega.Equal(beforeFailure + 1))
	})
})
