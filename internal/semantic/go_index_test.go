package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGoIndexAndQueries(t *testing.T) {
	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
	writeSemanticFile(t, filepath.Join(root, "cmd", "demo", "main.go"), `package main

import "example.com/demo/internal/service"

func main() {
	service.Run()
}
`)
	writeSemanticFile(t, filepath.Join(root, "internal", "service", "service.go"), `package service

type Runner struct{}

const DefaultName = "demo"

func Run() {
	helper()
}

func helper() {}

func (Runner) Start() {
	Run()
}
`)
	writeSemanticFile(t, filepath.Join(root, "internal", "service", "service_test.go"), `package service

func TestRun() {
	Run()
}
`)

	idx, err := BuildGoIndex(root)
	if err != nil {
		t.Fatalf("BuildGoIndex: %v", err)
	}
	if idx == nil {
		t.Fatal("expected Go index")
	}
	if idx.Module != "example.com/demo" {
		t.Fatalf("expected module example.com/demo, got %q", idx.Module)
	}
	if len(idx.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(idx.Files))
	}
	if summary := idx.Summary(); !strings.Contains(summary, "Go module: example.com/demo") || !strings.Contains(summary, "Representative symbols") {
		t.Fatalf("unexpected summary:\n%s", summary)
	}

	symbols := idx.FindSymbols("Run", 5)
	if len(symbols) == 0 || symbols[0].Name != "Run" {
		t.Fatalf("expected Run symbol first, got %#v", symbols)
	}
	if types := idx.FindSymbolsByKind("Runner", "type", 2); len(types) != 1 || types[0].Kind != "type" {
		t.Fatalf("expected Runner type, got %#v", types)
	}
	if pkgs := idx.FindPackages("service", 2); len(pkgs) != 1 || pkgs[0].Path != "internal/service" {
		t.Fatalf("expected service package, got %#v", pkgs)
	}
	if calls := idx.FindCallsFrom("Run", 4); len(calls) == 0 || calls[0].CalleeName != "helper" {
		t.Fatalf("expected Run to call helper, got %#v", calls)
	}
	if calls := idx.FindCallsInto("Run", 8); len(calls) < 2 {
		t.Fatalf("expected calls into Run from main/test/method, got %#v", calls)
	}
	if defaults := idx.DefaultFiles(2); len(defaults) == 0 || defaults[0] != "cmd/demo/main.go" {
		t.Fatalf("expected main default file first, got %#v", defaults)
	}
	if relevant := idx.RelevantFiles([]string{"runner"}, 2); len(relevant) == 0 || relevant[0] != "internal/service/service.go" {
		t.Fatalf("expected service file relevant to runner, got %#v", relevant)
	}
}

func TestBuildGoIndexReturnsNilForEmptyRoot(t *testing.T) {
	idx, err := BuildGoIndex("")
	if err != nil {
		t.Fatalf("BuildGoIndex empty root: %v", err)
	}
	if idx != nil {
		t.Fatalf("expected nil index for empty root, got %#v", idx)
	}
}

func writeSemanticFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
