package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
)

func testWorkspaceConfig() config.Workspace {
	return config.Workspace{
		Root:                  filepath.Clean("../.."),
		CWD:                   filepath.Clean("../.."),
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	}
}

func TestGoDiagnosticTargetsIncludesWorkspaceRootFiles(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())

	files, err := mgr.goDiagnosticTargets(ToolRequest{Tool: "go_diagnostics", Path: "."})
	if err != nil {
		t.Fatalf("goDiagnosticTargets returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected workspace-root Go diagnostics to include indexed files")
	}
}

func TestExecuteReadWriteAndExploreWriteRejection(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableShell:           true,
		EnableWrites:          true,
	}
	mgr := NewManager(cfg)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "write_file", Path: "notes/todo.txt", Content: "hello\nworld\n"})
	if result.Error != "" {
		t.Fatalf("write_file failed: %#v", result)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "notes", "todo.txt")); err != nil || string(data) != "hello\nworld\n" {
		t.Fatalf("unexpected written file data=%q err=%v", string(data), err)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
	if result.Error != "" || !strings.Contains(result.Output, "2:world") {
		t.Fatalf("expected exact existing raw path read to auto-discover, got %#v", result)
	}
	mgr.PrimeDiscoveredPaths([]string{"notes/todo.txt"})
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
	if result.Error != "" || !strings.Contains(result.Output, "2:world") {
		t.Fatalf("read_file did not return requested line: %#v", result)
	}

	if err := mgr.SetMode(ModeExplore); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "write_file", Path: "blocked.txt", Content: "nope"})
	if result.Error == "" || !strings.Contains(result.Error, "explore mode is read-only") {
		t.Fatalf("expected explore write rejection, got %#v", result)
	}
}

func TestListFilesSkipsNoisyDirsFromRootListing(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{
		".git/config",
		".cache/model.bin",
		"internal/app/app.go",
		"README.md",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 100,
		EnableShell:         true,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: ".", Recursive: true, MaxDepth: 3})
	if result.Error != "" {
		t.Fatalf("list_files failed: %#v", result)
	}
	if strings.Contains(result.Output, ".git/") || strings.Contains(result.Output, ".cache/") {
		t.Fatalf("root listing should skip noisy directories, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "internal/app/app.go") {
		t.Fatalf("expected source path in root listing, got:\n%s", result.Output)
	}
}

func TestExecuteRejectsPathTraversalAndUnsupportedTools(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableWrites:          true,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "../outside.txt"})
	if result.Error == "" {
		t.Fatalf("expected path traversal read to fail: %#v", result)
	}
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "definitely_not_a_tool"})
	if result.Error == "" || !strings.Contains(result.Error, "unsupported tool") {
		t.Fatalf("expected unsupported tool error, got %#v", result)
	}
}
