package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func writeRustFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newRustFixtureManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()

	writeRustFixture(t, filepath.Join(root, "Cargo.toml"), `
[package]
name = "fixture"
version = "0.1.0"
edition = "2021"
`)

	writeRustFixture(t, filepath.Join(root, "src", "main.rs"), `
mod worker;
mod utils;

fn helper() -> u32 {
    42
}

fn main() {
    let value = helper();
    worker::Worker::spawn(value).run();
}
`)

	writeRustFixture(t, filepath.Join(root, "src", "worker.rs"), `
pub struct Worker {
    pub id: u32,
}

pub trait Runnable {
    fn run(&self);
}

impl Runnable for Worker {
    fn run(&self) {
        do_work(self.id);
    }
}

impl Worker {
    pub fn spawn(id: u32) -> Worker {
        Worker { id }
    }
}

fn do_work(id: u32) {
    let _ = id;
}
`)

	writeRustFixture(t, filepath.Join(root, "src", "utils.rs"), `
pub const DEFAULT_MAX: u32 = 64;

#[test]
fn trivial() {
    assert_eq!(2 + 2, 4);
}
`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   64,
	})
	return mgr, root
}

func TestRustWorkspaceDetect(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	if !mgr.isRustWorkspace() {
		t.Fatal("expected fixture to count as a Rust workspace")
	}
}

func TestRustOverview(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustOverview()
	if result.Error != "" {
		t.Fatalf("rustOverview returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "Indexed 3 Rust files") {
		t.Fatalf("unexpected rustOverview output: %q", result.Output)
	}
}

func TestRustFindSymbol(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustFindSymbol(ToolRequest{Tool: "rust_find_symbol", Pattern: "Worker"})
	if result.Error != "" {
		t.Fatalf("rustFindSymbol returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "worker.rs") {
		t.Fatalf("expected worker.rs in output, got: %q", result.Output)
	}
	decl := mgr.rustFindSymbol(ToolRequest{Tool: "rust_find_symbol", Pattern: "pub struct Worker"})
	if decl.Error != "" || !strings.Contains(decl.Output, "Worker") {
		t.Fatalf("expected declaration-shaped rustFindSymbol query to find Worker, got %#v", decl)
	}
}

func TestRustFindSymbolEmptyPattern(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustFindSymbol(ToolRequest{Tool: "rust_find_symbol"})
	if result.Error == "" {
		t.Fatalf("expected error for empty pattern, got success: %#v", result)
	}
}

func TestRustReadSymbol(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustReadSymbol(ToolRequest{Tool: "rust_read_symbol", Pattern: "spawn", Path: filepath.Join("src", "worker.rs")})
	if result.Error != "" {
		t.Fatalf("rustReadSymbol returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "spawn") {
		t.Fatalf("expected output to include spawn, got: %q", result.Output)
	}

	decl := mgr.rustReadSymbol(ToolRequest{Tool: "rust_read_symbol", Pattern: "pub struct Worker", Path: filepath.Join("src", "worker.rs")})
	if decl.Error != "" {
		t.Fatalf("rustReadSymbol with declaration-shaped pattern returned error: %v", decl.Error)
	}
	if !strings.Contains(decl.Output, "pub struct Worker") {
		t.Fatalf("expected declaration-shaped rustReadSymbol to return struct snippet, got: %q", decl.Output)
	}
}

func TestRustFileOutline(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustFileOutline(ToolRequest{Tool: "rust_file_outline", Path: filepath.Join("src", "worker.rs")})
	if result.Error != "" {
		t.Fatalf("rustFileOutline returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "file: src/worker.rs") {
		t.Fatalf("rustFileOutline missing file header: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Worker") {
		t.Fatalf("rustFileOutline missing Worker: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Runnable") {
		t.Fatalf("rustFileOutline missing Runnable trait: %q", result.Output)
	}
}

func TestRustEntrypoints(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustEntrypoints(ToolRequest{Tool: "rust_entrypoints"})
	if result.Error != "" {
		t.Fatalf("rustEntrypoints returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "main.rs") {
		t.Fatalf("expected main.rs in entrypoints, got: %q", result.Output)
	}
}

func TestRustRelatedFiles(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustRelatedFiles(ToolRequest{Tool: "rust_related_files", Pattern: "worker"})
	if result.Error != "" {
		t.Fatalf("rustRelatedFiles returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "worker.rs") {
		t.Fatalf("expected worker.rs in output, got: %q", result.Output)
	}
}

func TestRustImplementationsFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	// rust-analyzer is almost certainly unavailable in CI, so this exercises
	// the TraitImplementers fallback path.
	result := mgr.rustImplementations(context.Background(), ToolRequest{Tool: "rust_implementations", Pattern: "Runnable"})
	if result.Error != "" {
		t.Fatalf("rustImplementations returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "worker.rs") {
		t.Fatalf("expected worker.rs in impls output, got: %q", result.Output)
	}
}

func TestRustCallsFromFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustCallsFrom(context.Background(), ToolRequest{Tool: "rust_calls_from", Pattern: "main"})
	if result.Error != "" {
		t.Fatalf("rustCallsFrom returned error: %v", result.Error)
	}
	// main calls helper -> expect helper in the output.
	if !strings.Contains(result.Output, "helper") {
		t.Fatalf("expected helper in calls-from output, got: %q", result.Output)
	}
}

func TestRustCallsIntoFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustCallsInto(context.Background(), ToolRequest{Tool: "rust_calls_into", Pattern: "helper"})
	if result.Error != "" {
		t.Fatalf("rustCallsInto returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "main") {
		t.Fatalf("expected main caller, got: %q", result.Output)
	}
}

func TestRustReferencesFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustReferences(context.Background(), ToolRequest{Tool: "rust_references", Pattern: "helper", Path: "."})
	if result.Error != "" {
		t.Fatalf("rustReferences returned error: %v", result.Error)
	}
	// Either clangd-shape ("Resolved Rust references via rust-analyzer")
	// or the rg fallback; the rg fallback should include helper callsites.
	if !strings.Contains(result.Summary, "rust") && !strings.Contains(result.Output, "helper") {
		t.Fatalf("expected references output, got: %#v", result)
	}
}

func TestRustDefinitionFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustDefinition(context.Background(), ToolRequest{Tool: "rust_definition", Pattern: "do_work"})
	if result.Error != "" {
		t.Fatalf("rustDefinition returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "worker.rs") {
		t.Fatalf("expected worker.rs in definition, got: %q", result.Output)
	}
}

func TestRustWorkspaceSymbolFallback(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.rustWorkspaceSymbol(context.Background(), ToolRequest{Tool: "rust_workspace_symbol", Pattern: "Worker"})
	if result.Error != "" {
		t.Fatalf("rustWorkspaceSymbol returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "worker.rs") {
		t.Fatalf("expected worker.rs in workspace-symbol, got: %q", result.Output)
	}
}

func TestRustDiagnosticsNoCargo(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	if _, err := exec.LookPath("cargo"); err == nil {
		t.Skip("cargo is installed; diagnostics is exercised elsewhere")
	}
	result := mgr.rustDiagnostics(context.Background(), ToolRequest{Tool: "rust_diagnostics"})
	if result.Error == "" {
		t.Fatal("expected error when cargo is absent")
	}
	if !strings.Contains(result.Error, "cargo") {
		t.Fatalf("expected cargo capability error, got: %q", result.Error)
	}
}

func TestRustToolsWithRustAnalyzer(t *testing.T) {
	if _, err := exec.LookPath("rust-analyzer"); err != nil {
		t.Skip("rust-analyzer not installed")
	}
	mgr, _ := newRustFixtureManager(t)

	ws := mgr.rustWorkspaceSymbol(context.Background(), ToolRequest{Tool: "rust_workspace_symbol", Pattern: "Worker"})
	if ws.Error != "" {
		t.Fatalf("rustWorkspaceSymbol returned error: %v", ws.Error)
	}
	if !strings.Contains(ws.Output, "Worker") {
		t.Fatalf("rustWorkspaceSymbol missing Worker, got: %q", ws.Output)
	}

	def := mgr.rustDefinition(context.Background(), ToolRequest{Tool: "rust_definition", Pattern: "spawn", Path: filepath.Join("src", "worker.rs")})
	if def.Error != "" {
		t.Fatalf("rustDefinition returned error: %v", def.Error)
	}
}

func TestExecuteRustToolDispatch(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	for _, name := range RustToolNames() {
		if name == "rust_diagnostics" {
			continue // cargo may not be installed
		}
		req := ToolRequest{Tool: name}
		switch name {
		case "rust_overview", "rust_entrypoints":
			// no extra params needed
		case "rust_file_outline":
			req.Path = filepath.Join("src", "worker.rs")
		case "rust_read_symbol":
			req.Pattern = "spawn"
			req.Path = filepath.Join("src", "worker.rs")
		default:
			req.Pattern = "Worker"
		}
		result := mgr.ExecuteRustTool(context.Background(), req)
		if result.Tool == "" {
			t.Fatalf("ExecuteRustTool(%q) returned blank Tool name", name)
		}
	}
}

func TestExecuteRustToolUnknown(t *testing.T) {
	mgr, _ := newRustFixtureManager(t)
	result := mgr.ExecuteRustTool(context.Background(), ToolRequest{Tool: "rust_bogus"})
	if result.Error == "" {
		t.Fatal("expected error for unknown rust tool")
	}
}
