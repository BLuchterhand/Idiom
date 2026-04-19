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

func TestGoFileOutlineReportsDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "widget.go"), `package sample

import "fmt"

type Model struct{}

func (m *Model) View() string {
	return fmt.Sprint("ok")
}

func helper() {}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_file_outline", Path: "widget.go"})
	if result.Error != "" {
		t.Fatalf("go_file_outline failed: %#v", result)
	}
	for _, want := range []string{"Model.View", "helper", "imports:", "tags: rendering"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected outline to contain %q, got:\n%s", want, result.Output)
		}
	}
}

func TestGoMoveDeclarationsMovesSelectedBlocksAndRepairsImports(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

import (
	"fmt"
	"strings"
)

type Model struct{}

func (m *Model) View() string {
	return fmt.Sprint("ok")
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:         "go_move_declarations",
		Path:         "source.go",
		Destination:  "rendering.go",
		Declarations: []string{"Model.View"},
	})
	if result.Error != "" {
		t.Fatalf("go_move_declarations failed: %#v", result)
	}

	source := readTestFile(t, filepath.Join(dir, "source.go"))
	dest := readTestFile(t, filepath.Join(dir, "rendering.go"))
	if strings.Contains(source, "func (m *Model) View") {
		t.Fatalf("source still contains moved method:\n%s", source)
	}
	if !strings.Contains(source, `"strings"`) || strings.Contains(source, `"fmt"`) {
		t.Fatalf("source imports were not repaired:\n%s", source)
	}
	if !strings.Contains(dest, "func (m *Model) View") || !strings.Contains(dest, `"fmt"`) {
		t.Fatalf("destination missing moved method or import:\n%s", dest)
	}
	if strings.Contains(dest, `"strings"`) {
		t.Fatalf("destination retained unused import:\n%s", dest)
	}
}

func TestGoMoveDeclarationsAcceptsResponsibilityTagPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

import "fmt"

func renderView() string {
	return fmt.Sprint("ok")
}

func coreValue() int {
	return 1
}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:        "go_move_declarations",
		Path:        "source.go",
		Destination: "rendering.go",
		Pattern:     "tag:rendering",
	})
	if result.Error != "" {
		t.Fatalf("go_move_declarations tag pattern failed: %#v", result)
	}
	source := readTestFile(t, filepath.Join(dir, "source.go"))
	dest := readTestFile(t, filepath.Join(dir, "rendering.go"))
	if strings.Contains(source, "renderView") || !strings.Contains(dest, "renderView") {
		t.Fatalf("tag move did not move rendering declaration\nsource:\n%s\ndest:\n%s", source, dest)
	}
	if !strings.Contains(source, "coreValue") {
		t.Fatalf("tag move should leave core declaration in source:\n%s", source)
	}
}

func TestGoMoveDeclarationsAcceptsBatchMoves(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

import (
	"fmt"
	"strings"
)

func renderView() string {
	return fmt.Sprint("ok")
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}

func coreValue() int {
	return 1
}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "go_move_declarations",
		Path: "source.go",
		Moves: []GoDeclarationMove{
			{Destination: "rendering.go", Pattern: "tag:rendering"},
			{Destination: "task_memory.go", Declarations: []string{"normalize"}},
		},
	})
	if result.Error != "" {
		t.Fatalf("go_move_declarations batch failed: %#v", result)
	}
	if source := readTestFile(t, filepath.Join(dir, "source.go")); strings.Contains(source, "renderView") || strings.Contains(source, "normalize") || !strings.Contains(source, "coreValue") {
		t.Fatalf("batch move left unexpected source content:\n%s", source)
	}
	if dest := readTestFile(t, filepath.Join(dir, "rendering.go")); !strings.Contains(dest, "renderView") {
		t.Fatalf("rendering destination missing moved declaration:\n%s", dest)
	}
	if dest := readTestFile(t, filepath.Join(dir, "task_memory.go")); !strings.Contains(dest, "normalize") {
		t.Fatalf("task memory destination missing moved declaration:\n%s", dest)
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

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
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
