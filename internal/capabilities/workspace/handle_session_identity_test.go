package workspace

import (
	"context"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/platform/config"
)

func listedHandleForPath(output string, wantPath string) string {
	ginkgo.GinkgoHelper()
	wantPath = filepath.Clean(wantPath)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		displayPath := strings.Join(fields[1:], " ")
		if metadata := strings.Index(displayPath, " (modified "); metadata >= 0 {
			displayPath = displayPath[:metadata]
		}
		if resolved, err := filepath.EvalSymlinks(displayPath); err == nil {
			displayPath = resolved
		}
		if resolved, err := filepath.EvalSymlinks(wantPath); err == nil {
			wantPath = resolved
		}
		if filepath.Clean(displayPath) == filepath.Clean(wantPath) {
			return fields[0]
		}
	}

	return ""
}

var _ = ginkgo.Describe("session-bound handles and prefetch", ginkgo.Label("race-contract"), func() {
	ginkgo.It("reauthorizes external file handles for each invocation session", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		externalFile := filepath.Join(sibling, "external.go")
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/handle-session\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "main.go"), "package main\n\nfunc WorkspaceOnly() {}\n")
		writeTestFile(externalFile, "package sibling\n\nfunc ExternalOnly() {}\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 20})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
				gomega.Expect(req.SessionID).To(gomega.Equal("session-a"))
				return SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil
			}).
			Times(1)

		sessionA := WithExecutionSessionID(context.Background(), "session-a")
		sessionB := WithExecutionSessionID(context.Background(), "session-b")
		approval := mgr.Execute(sessionA, ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "inspect sibling source",
		})
		gomega.Expect(approval.Error).To(gomega.BeEmpty())

		listed := mgr.Execute(sessionA, ToolRequest{Tool: ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(listed.Error).To(gomega.BeEmpty())
		handle := listedHandleForPath(listed.Output, externalFile)
		gomega.Expect(handle).NotTo(gomega.BeEmpty(), listed.Output)

		crossSessionRead := mgr.Execute(sessionB, ToolRequest{Tool: ToolNameReadFile, ID: handle})
		gomega.Expect(crossSessionRead.Error).To(gomega.ContainSubstring("has not been approved"))

		ownerRead := mgr.Execute(sessionA, ToolRequest{Tool: ToolNameReadFile, ID: handle})
		gomega.Expect(ownerRead.Error).To(gomega.BeEmpty())
		gomega.Expect(ownerRead.Output).To(gomega.ContainSubstring("ExternalOnly"))

		crossSessionLanguage := mgr.Execute(sessionB, ToolRequest{Tool: "go_read_symbol", ID: handle, Pattern: "ExternalOnly"})
		gomega.Expect(crossSessionLanguage.Error).To(gomega.ContainSubstring("has not been approved"))

		ownerLanguage := mgr.Execute(sessionA, ToolRequest{Tool: "go_read_symbol", ID: handle, Pattern: "ExternalOnly"})
		gomega.Expect(ownerLanguage.Error).To(gomega.BeEmpty())
	})

	ginkgo.It("does not let a selected-directory handle authorize a child read", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		externalFile := filepath.Join(sibling, "secret.txt")
		writeTestFile(filepath.Join(root, "README.md"), "workspace\n")
		writeTestFile(externalFile, "secret\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 20})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			Return(SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeSelected}, nil).
			Times(1)

		session := WithExecutionSessionID(context.Background(), "selected-session")
		approval := mgr.Execute(session, ToolRequest{Tool: ToolNameSystemSearchAccess, Path: "../sibling", Purpose: "inspect selected directory"})
		gomega.Expect(approval.Error).To(gomega.BeEmpty())
		listed := mgr.Execute(session, ToolRequest{Tool: ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(listed.Error).To(gomega.BeEmpty())
		handle := listedHandleForPath(listed.Output, externalFile)
		gomega.Expect(handle).NotTo(gomega.BeEmpty(), listed.Output)

		read := mgr.Execute(session, ToolRequest{Tool: ToolNameReadFile, ID: handle})
		gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"))
	})

	ginkgo.It("preserves default-readable global log handles", func() {
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		root := ginkgo.GinkgoT().TempDir()
		logDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "12")
		logPath := filepath.Join(logDir, "rollout-session.jsonl")
		writeTestFile(logPath, "{\"event\":\"safe\"}\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 20})
		searched := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSearchFiles,
			Path:    logDir,
			Pattern: ".jsonl",
		})
		gomega.Expect(searched.Error).To(gomega.BeEmpty())
		handle := listedHandleForPath(searched.Output, logPath)
		gomega.Expect(handle).NotTo(gomega.BeEmpty(), searched.Output)

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, ID: handle})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		gomega.Expect(read.Output).To(gomega.ContainSubstring(`"event":"safe"`))
	})

	ginkgo.It("keeps objective prefetch workspace-only even after an external grant", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		externalFile := filepath.Join(sibling, "external.txt")
		writeTestFile(filepath.Join(root, "inside.txt"), "inside\n")
		writeTestFile(externalFile, "outside\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		mgr.SetSystemSearchAccessApprover(approver)
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			Return(SystemSearchAccessDecision{Approved: true, Scope: SystemSearchAccessScopeContents}, nil).
			Times(1)
		approval := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameSystemSearchAccess, Path: "../sibling", Purpose: "ordinary interactive read"})
		gomega.Expect(approval.Error).To(gomega.BeEmpty())

		report := mgr.PrefetchReadFiles(context.Background(), []string{"inside.txt", externalFile}, PrefetchReadOptions{})
		gomega.Expect(report.SelectedPaths).To(gomega.ConsistOf("inside.txt"))
		gomega.Expect(report.SkippedPaths).To(gomega.ContainElement(PrefetchSkippedPath{Path: externalFile, Reason: "resolve_failed"}))
	})
})
