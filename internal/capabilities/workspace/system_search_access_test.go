package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"idiom/internal/platform/config"
)

func newSystemSearchApproverMock(mgr *Manager) *MockSystemSearchAccessApprover {
	ginkgo.GinkgoHelper()

	approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
	mgr.SetSystemSearchAccessApprover(approver)

	return approver
}

func expectSystemSearchApproval(
	mgr *Manager,
	approver *MockSystemSearchAccessApprover,
	target string,
	reason string,
	scope SystemSearchAccessScope,
) *MockSystemSearchAccessApproverApproveSystemSearchAccessCall {
	ginkgo.GinkgoHelper()

	target = canonicalTestPath(target)
	info, err := os.Stat(target)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	targetKind := "file"
	if info.IsDir() {
		targetKind = "directory"
	}

	call := approver.EXPECT().ApproveSystemSearchAccess(gomock.Any(), SystemSearchAccessRequest{
		SessionID:     mgr.SessionID(),
		Path:          target,
		DisplayPath:   mgr.pathAccess.DisplayPath(target),
		TargetKind:    targetKind,
		RequestTool:   ToolNameSystemSearchAccess,
		RequestReason: reason,
	}).Return(SystemSearchAccessDecision{Approved: true, Scope: scope}, nil)
	call.Times(1)

	return call
}

var _ = ginkgo.Describe("system search access", func() {
	ginkgo.It("system search access requires approval", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "find sibling repo",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("system search access without approver should require approval, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("requires approval"), fmt.Sprintf("system search access without approver should require approval, got %#v", result))

		list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
		gomega.Expect(list.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("list_files outside root should remain denied before approval, got %#v", list))
		gomega.Expect(list.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("list_files outside root should remain denied before approval, got %#v", list))

	})
	ginkgo.It("system search access approval enables read only discovery", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")
		writeTestFile(filepath.Join(idiom, "internal", "bug.go"), "package internal\nconst Bug = true\n")

		mgr := NewManager(config.Workspace{
			Root:                website,
			CWD:                 website,
			MaxDirectoryEntries: 20,
		})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "find sibling repo", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "find sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
		gomega.Expect(list.Error).To(gomega.BeEmpty(), fmt.Sprintf("list_files should read approved sibling repo, got %#v", list))
		gomega.Expect(list.Output).To(gomega.ContainSubstring("go.mod"), fmt.Sprintf("list_files should read approved sibling repo, got %#v", list))

		search := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSearchFiles,
			Path:    "../idiom",
			Pattern: "bug",
		})
		gomega.Expect(search.Error).To(gomega.BeEmpty(), fmt.Sprintf("search_files should read approved sibling repo, got %#v", search))
		gomega.Expect(search.Output).To(gomega.ContainSubstring("bug.go"), fmt.Sprintf("search_files should read approved sibling repo, got %#v", search))

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).To(gomega.BeEmpty(), fmt.Sprintf("read_file should read approved sibling repo file, got %#v", read))
		gomega.Expect(read.Output).To(gomega.ContainSubstring("module idiom"), fmt.Sprintf("read_file should read approved sibling repo file, got %#v", read))

		if got, want := read.Paths, []string{canonicalTestPath(filepath.Join(idiom, "go.mod"))}; len(got) != 1 || got[0] != want[0] {
			ginkgo.Fail(fmt.Sprintf("read_file should report typed read path %v, got %#v", want, got))
		}
	})
	ginkgo.It("system search access selected file only allows that file", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")
		writeTestFile(filepath.Join(idiom, "README.md"), "sibling\n")

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, filepath.Join(idiom, "go.mod"), "read sibling module metadata", SystemSearchAccessScopeSelected)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    filepath.Join("..", "idiom", "go.mod"),
			Purpose: "read sibling module metadata",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("selected file access should be approved, got %#v", approval))

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).To(gomega.BeEmpty(), fmt.Sprintf("read_file should read the selected file, got %#v", read))
		gomega.Expect(read.Output).To(gomega.ContainSubstring("module idiom"), fmt.Sprintf("read_file should read the selected file, got %#v", read))

		sibling := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "README.md")})
		gomega.Expect(sibling.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("selected file access should not allow sibling reads, got %#v", sibling))
		gomega.Expect(sibling.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("selected file access should not allow sibling reads, got %#v", sibling))

	})
	ginkgo.It("system search access selected directory does not allow descendant inspection", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")
		writeTestFile(filepath.Join(idiom, "internal", "bug.go"), "package internal\nconst Bug = true\n")

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "inspect sibling repo boundary before broader access", SystemSearchAccessScopeSelected)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "inspect sibling repo boundary before broader access",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("selected directory access should be approved, got %#v", approval))

		list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
		gomega.Expect(list.Error).To(gomega.BeEmpty(), fmt.Sprintf("list_files should enumerate selected directory only, got %#v", list))
		gomega.Expect(list.Output).To(gomega.ContainSubstring("go.mod"), fmt.Sprintf("list_files should enumerate selected directory only, got %#v", list))

		recursiveList := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom", Recursive: true})
		gomega.Expect(recursiveList.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("recursive list should require contents approval, got %#v", recursiveList))
		gomega.Expect(recursiveList.Error).To(gomega.ContainSubstring("selected directory"), fmt.Sprintf("recursive list should require contents approval, got %#v", recursiveList))

		search := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchFiles, Path: "../idiom", Pattern: "bug"})
		gomega.Expect(search.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("search_files should require contents approval, got %#v", search))
		gomega.Expect(search.Error).To(gomega.ContainSubstring("selected directory"), fmt.Sprintf("search_files should require contents approval, got %#v", search))

		content := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "../idiom", Pattern: "Bug"})
		gomega.Expect(content.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("search_content should require contents approval, got %#v", content))
		gomega.Expect(content.Error).To(gomega.ContainSubstring("selected directory"), fmt.Sprintf("search_content should require contents approval, got %#v", content))

		stats := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameFileStats, Path: "../idiom"})
		gomega.Expect(stats.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("file_stats should require contents approval, got %#v", stats))
		gomega.Expect(stats.Error).To(gomega.ContainSubstring("selected directory"), fmt.Sprintf("file_stats should require contents approval, got %#v", stats))

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("selected directory access should not allow descendant reads, got %#v", read))
		gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("selected directory access should not allow descendant reads, got %#v", read))

	})
	ginkgo.It("system search access selected directory can be upgraded to contents", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		approver := newSystemSearchApproverMock(mgr)
		selected := expectSystemSearchApproval(mgr, approver, idiom, "inspect selected sibling directory", SystemSearchAccessScopeSelected)
		contents := expectSystemSearchApproval(mgr, approver, idiom, "read files under sibling repo", SystemSearchAccessScopeContents)
		gomock.InOrder(selected.Call, contents.Call)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "inspect selected sibling directory",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("selected directory access should be approved, got %#v", approval))

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).NotTo(gomega.Equal(""), fmt.Sprintf("selected directory access should not allow descendant read before upgrade, got %#v", read))

		approval = mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "read files under sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("contents upgrade should be approved, got %#v", approval))

		read = mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).To(gomega.BeEmpty(), fmt.Sprintf("contents approval should allow descendant read, got %#v", read))
		gomega.Expect(read.Output).To(gomega.ContainSubstring("module idiom"), fmt.Sprintf("contents approval should allow descendant read, got %#v", read))

	})
})

func canonicalTestPath(path string) string {
	ginkgo.GinkgoHelper()

	resolved, err := filepath.EvalSymlinks(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("EvalSymlinks(%q): %v", path, err))

	return filepath.Clean(resolved)
}

var _ = ginkgo.Describe("system search access", func() {
	ginkgo.It("system search access does not permit outside workspace writes", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")

		mgr := NewManager(config.Workspace{
			Root:         website,
			CWD:          website,
			EnableWrites: true,
		})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "find sibling repo", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "find sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		write := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameWriteFile,
			Path:    filepath.Join("..", "idiom", "new.txt"),
			Content: "nope\n",
		})
		gomega.Expect(write.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("write_file outside workspace should remain denied, got %#v", write))
		gomega.Expect(write.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("write_file outside workspace should remain denied, got %#v", write))

	})
	ginkgo.It("system search access clears approvals when session changes", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		mgr.SetSessionID("session-one")
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "inspect sibling repo", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "inspect sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).To(gomega.Equal(""), fmt.Sprintf("read_file should read approved sibling repo file, got %#v", read))

		mgr.SetSessionID("session-two")
		read = mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
		gomega.Expect(read.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("read_file should require a fresh approval after session changes, got %#v", read))
		gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("read_file should require a fresh approval after session changes, got %#v", read))

	})
	ginkgo.It("system search access rejects symlink read escaping approved root", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		secret := filepath.Join(parent, "secret.txt")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")
		writeTestFile(secret, "secret\n")
		if err := os.Symlink(secret, filepath.Join(idiom, "secret-link.txt")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "inspect sibling repo", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "inspect sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		read := mgr.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile,
			Path: filepath.Join("..", "idiom", "secret-link.txt"),
		})
		gomega.Expect(read.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("symlink read escaping approved root should be denied, got %#v", read))
		gomega.Expect(read.Error).To(gomega.ContainSubstring("has not been approved"), fmt.Sprintf("symlink read escaping approved root should be denied, got %#v", read))

	})
	ginkgo.It("system search access canonicalizes approved symlink root", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		alias := filepath.Join(parent, "idiom-alias")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")
		if err := os.Symlink(idiom, alias); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "inspect sibling repo through alias", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom-alias",
			Purpose: "inspect sibling repo through alias",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		read := mgr.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile,
			Path: filepath.Join("..", "idiom-alias", "go.mod"),
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty(), fmt.Sprintf("read_file through approved symlink root should resolve to canonical approved root, got %#v", read))
		gomega.Expect(read.Output).To(gomega.ContainSubstring("module idiom"), fmt.Sprintf("read_file through approved symlink root should resolve to canonical approved root, got %#v", read))

	})
	ginkgo.It("system search access does not permit shell outside workspace", func() {
		parent := ginkgo.GinkgoT().TempDir()
		website := filepath.Join(parent, "Website")
		idiom := filepath.Join(parent, "idiom")
		writeTestFile(filepath.Join(website, "README.md"), "website\n")
		writeTestFile(filepath.Join(idiom, "go.mod"), "module idiom\n")

		mgr := NewManager(config.Workspace{
			Root:                  website,
			CWD:                   website,
			Shell:                 "/bin/sh",
			CommandTimeoutSeconds: 5,
			MaxOutputBytes:        4096,
			EnableShell:           true,
		})
		approver := newSystemSearchApproverMock(mgr)
		expectSystemSearchApproval(mgr, approver, idiom, "inspect sibling repo", SystemSearchAccessScopeContents)

		approval := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSystemSearchAccess,
			Path:    "../idiom",
			Purpose: "inspect sibling repo",
		})
		gomega.Expect(approval.Error).To(gomega.Equal(""), fmt.Sprintf("system search access should be approved, got %#v", approval))

		readOnly := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameShell,
			Workdir: "../idiom",
			Command: "pwd",
		})
		gomega.Expect(readOnly.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("shell in approved sibling repo should remain denied, got %#v", readOnly))
		gomega.Expect(readOnly.Error).To(gomega.ContainSubstring("escapes workspace root"), fmt.Sprintf("shell in approved sibling repo should remain denied, got %#v", readOnly))

		mutating := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameShell,
			Workdir: "../idiom",
			Command: "touch marker",
		})
		gomega.Expect(mutating.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("mutating shell in approved sibling repo should be denied, got %#v", mutating))
		gomega.Expect(mutating.Error).To(gomega.ContainSubstring("escapes workspace root"), fmt.Sprintf("mutating shell in approved sibling repo should be denied, got %#v", mutating))

	})
})
