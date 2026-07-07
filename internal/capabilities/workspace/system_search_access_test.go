package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

type testSystemSearchApprover struct {
	approved bool
	scope    SystemSearchAccessScope
	requests []SystemSearchAccessRequest
}

func (a *testSystemSearchApprover) ApproveSystemSearchAccess(_ context.Context, req SystemSearchAccessRequest) (SystemSearchAccessDecision, error) {
	a.requests = append(a.requests, req)
	return SystemSearchAccessDecision{Approved: a.approved, Scope: a.scope}, nil
}

func TestSystemSearchAccessRequiresApproval(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")

	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "find sibling repo",
	})

	if result.Error == "" || !strings.Contains(result.Summary, "requires approval") {
		t.Fatalf("system search access without approver should require approval, got %#v", result)
	}

	list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
	if list.Error == "" || !strings.Contains(list.Error, "has not been approved") {
		t.Fatalf("list_files outside root should remain denied before approval, got %#v", list)
	}
}

func TestSystemSearchAccessApprovalEnablesReadOnlyDiscovery(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")
	writeTestFile(t, filepath.Join(idiom, "internal", "bug.go"), "package internal\nconst Bug = true\n")

	approver := &testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents}
	mgr := NewManager(config.Workspace{
		Root:                website,
		CWD:                 website,
		MaxDirectoryEntries: 20,
	})
	mgr.SetSystemSearchAccessApprover(approver)

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "find sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}
	wantPath := canonicalTestPath(t, idiom)
	if len(approver.requests) != 1 || approver.requests[0].Path != wantPath {
		t.Fatalf("approver request = %#v, want path %q", approver.requests, wantPath)
	}

	list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
	if list.Error != "" || !strings.Contains(list.Output, "go.mod") {
		t.Fatalf("list_files should read approved sibling repo, got %#v", list)
	}

	search := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSearchFiles,
		Path:    "../idiom",
		Pattern: "bug",
	})
	if search.Error != "" || !strings.Contains(search.Output, "bug.go") {
		t.Fatalf("search_files should read approved sibling repo, got %#v", search)
	}

	read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error != "" || !strings.Contains(read.Output, "module idiom") {
		t.Fatalf("read_file should read approved sibling repo file, got %#v", read)
	}
	if got, want := read.Paths, []string{canonicalTestPath(t, filepath.Join(idiom, "go.mod"))}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("read_file should report typed read path %v, got %#v", want, got)
	}
}

func TestSystemSearchAccessSelectedFileOnlyAllowsThatFile(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")
	writeTestFile(t, filepath.Join(idiom, "README.md"), "sibling\n")

	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeSelected})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    filepath.Join("..", "idiom", "go.mod"),
		Purpose: "read sibling module metadata",
	})
	if approval.Error != "" {
		t.Fatalf("selected file access should be approved, got %#v", approval)
	}

	read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error != "" || !strings.Contains(read.Output, "module idiom") {
		t.Fatalf("read_file should read the selected file, got %#v", read)
	}

	sibling := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "README.md")})
	if sibling.Error == "" || !strings.Contains(sibling.Error, "has not been approved") {
		t.Fatalf("selected file access should not allow sibling reads, got %#v", sibling)
	}
}

func TestSystemSearchAccessSelectedDirectoryDoesNotAllowDescendantInspection(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")
	writeTestFile(t, filepath.Join(idiom, "internal", "bug.go"), "package internal\nconst Bug = true\n")

	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeSelected})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "inspect sibling repo boundary before broader access",
	})
	if approval.Error != "" {
		t.Fatalf("selected directory access should be approved, got %#v", approval)
	}

	list := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom"})
	if list.Error != "" || !strings.Contains(list.Output, "go.mod") {
		t.Fatalf("list_files should enumerate selected directory only, got %#v", list)
	}

	recursiveList := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "../idiom", Recursive: true})
	if recursiveList.Error == "" || !strings.Contains(recursiveList.Error, "selected directory") {
		t.Fatalf("recursive list should require contents approval, got %#v", recursiveList)
	}

	search := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchFiles, Path: "../idiom", Pattern: "bug"})
	if search.Error == "" || !strings.Contains(search.Error, "selected directory") {
		t.Fatalf("search_files should require contents approval, got %#v", search)
	}

	content := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "../idiom", Pattern: "Bug"})
	if content.Error == "" || !strings.Contains(content.Error, "selected directory") {
		t.Fatalf("search_content should require contents approval, got %#v", content)
	}

	stats := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameFileStats, Path: "../idiom"})
	if stats.Error == "" || !strings.Contains(stats.Error, "selected directory") {
		t.Fatalf("file_stats should require contents approval, got %#v", stats)
	}

	read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error == "" || !strings.Contains(read.Error, "has not been approved") {
		t.Fatalf("selected directory access should not allow descendant reads, got %#v", read)
	}
}

func TestSystemSearchAccessSelectedDirectoryCanBeUpgradedToContents(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")

	approver := &testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeSelected}
	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSystemSearchAccessApprover(approver)

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "inspect selected sibling directory",
	})
	if approval.Error != "" {
		t.Fatalf("selected directory access should be approved, got %#v", approval)
	}

	read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error == "" {
		t.Fatalf("selected directory access should not allow descendant read before upgrade, got %#v", read)
	}

	approver.scope = SystemSearchAccessScopeContents
	approval = mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "read files under sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("contents upgrade should be approved, got %#v", approval)
	}
	if len(approver.requests) != 2 {
		t.Fatalf("expected second approval request to upgrade scope, got %#v", approver.requests)
	}

	read = mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error != "" || !strings.Contains(read.Output, "module idiom") {
		t.Fatalf("contents approval should allow descendant read, got %#v", read)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}

	return filepath.Clean(resolved)
}

func TestSystemSearchAccessDoesNotPermitOutsideWorkspaceWrites(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")

	mgr := NewManager(config.Workspace{
		Root:         website,
		CWD:          website,
		EnableWrites: true,
	})
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "find sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}

	write := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameWriteFile,
		Path:    filepath.Join("..", "idiom", "new.txt"),
		Content: "nope\n",
	})
	if write.Error == "" || !strings.Contains(write.Error, "workspace root") {
		t.Fatalf("write_file outside workspace should remain denied, got %#v", write)
	}
}

func TestSystemSearchAccessClearsApprovalsWhenSessionChanges(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")

	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSessionID("session-one")
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "inspect sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}

	read := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error != "" {
		t.Fatalf("read_file should read approved sibling repo file, got %#v", read)
	}

	mgr.SetSessionID("session-two")
	read = mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: filepath.Join("..", "idiom", "go.mod")})
	if read.Error == "" || !strings.Contains(read.Error, "has not been approved") {
		t.Fatalf("read_file should require a fresh approval after session changes, got %#v", read)
	}
}

func TestSystemSearchAccessRejectsSymlinkReadEscapingApprovedRoot(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	secret := filepath.Join(parent, "secret.txt")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")
	writeTestFile(t, secret, "secret\n")
	if err := os.Symlink(secret, filepath.Join(idiom, "secret-link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "inspect sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}

	read := mgr.Execute(context.Background(), ToolRequest{
		Tool: ToolNameReadFile,
		Path: filepath.Join("..", "idiom", "secret-link.txt"),
	})
	if read.Error == "" || !strings.Contains(read.Error, "has not been approved") {
		t.Fatalf("symlink read escaping approved root should be denied, got %#v", read)
	}
}

func TestSystemSearchAccessCanonicalizesApprovedSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	alias := filepath.Join(parent, "idiom-alias")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")
	if err := os.Symlink(idiom, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	approver := &testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents}
	mgr := NewManager(config.Workspace{Root: website, CWD: website, MaxDirectoryEntries: 20})
	mgr.SetSystemSearchAccessApprover(approver)

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom-alias",
		Purpose: "inspect sibling repo through alias",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}

	wantRoot := canonicalTestPath(t, idiom)
	if len(approver.requests) != 1 || approver.requests[0].Path != wantRoot {
		t.Fatalf("approver should see canonical target root, got %#v want %q", approver.requests, wantRoot)
	}

	read := mgr.Execute(context.Background(), ToolRequest{
		Tool: ToolNameReadFile,
		Path: filepath.Join("..", "idiom-alias", "go.mod"),
	})
	if read.Error != "" || !strings.Contains(read.Output, "module idiom") {
		t.Fatalf("read_file through approved symlink root should resolve to canonical approved root, got %#v", read)
	}
}

func TestSystemSearchAccessDoesNotPermitShellOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	website := filepath.Join(parent, "Website")
	idiom := filepath.Join(parent, "idiom")
	writeTestFile(t, filepath.Join(website, "README.md"), "website\n")
	writeTestFile(t, filepath.Join(idiom, "go.mod"), "module idiom\n")

	mgr := NewManager(config.Workspace{
		Root:                  website,
		CWD:                   website,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        4096,
		EnableShell:           true,
	})
	mgr.SetSystemSearchAccessApprover(&testSystemSearchApprover{approved: true, scope: SystemSearchAccessScopeContents})

	approval := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameSystemSearchAccess,
		Path:    "../idiom",
		Purpose: "inspect sibling repo",
	})
	if approval.Error != "" {
		t.Fatalf("system search access should be approved, got %#v", approval)
	}

	readOnly := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameShell,
		Workdir: "../idiom",
		Command: "pwd",
	})
	if readOnly.Error == "" || !strings.Contains(readOnly.Error, "escapes workspace root") {
		t.Fatalf("shell in approved sibling repo should remain denied, got %#v", readOnly)
	}

	mutating := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameShell,
		Workdir: "../idiom",
		Command: "touch marker",
	})
	if mutating.Error == "" || !strings.Contains(mutating.Error, "escapes workspace root") {
		t.Fatalf("mutating shell in approved sibling repo should be denied, got %#v", mutating)
	}
}
