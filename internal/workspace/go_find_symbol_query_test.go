package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestGoFindSymbolAcceptsDeclarationPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "runner.go"), `package sample

type Runner struct{}
`)

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_symbol", Pattern: "type Runner struct"})
	if result.Error != "" {
		t.Fatalf("go_find_symbol failed: %#v", result)
	}
	if !strings.Contains(result.Output, "Runner") {
		t.Fatalf("expected Runner match, got:\n%s", result.Output)
	}
}

func TestGoReadSymbolAcceptsDeclarationPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "runner.go"), `package sample

type Runner struct{}

func (r *Runner) Run() {}
`)

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_read_symbol", Pattern: "type Runner struct"})
	if result.Error != "" {
		t.Fatalf("go_read_symbol failed: %#v", result)
	}
	if !strings.Contains(result.Output, "type Runner struct") {
		t.Fatalf("expected declaration 'type Runner struct' in output, got:\n%s", result.Output)
	}
}
