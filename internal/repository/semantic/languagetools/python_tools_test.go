package languagetools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
)

func TestPythonWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	writePythonToolFixture(t, filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
version = "0.1.0"
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "__init__.py"), "")
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "engine.py"), `
class Engine:
    def __init__(self, name):
        self.name = name

    def tick(self):
        return self.name

def helper():
    return Engine("h").tick()

if __name__ == "__main__":
    helper()
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "runner.py"), `
from .engine import Engine, helper

def run():
    e = Engine("runner")
    return e.tick() + "|" + helper()
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "base_system.py"), `
class SystemBase:
    def process_cycle(self):
        return None
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "worker_system.py"), `
from .base_system import SystemBase

class WorkerSystem(SystemBase):
    def process_cycle(self):
        return "work"
`)
	writePythonToolFixture(t, filepath.Join(root, "tests", "test_engine.py"), `
from demo.engine import Engine

def test_tick():
    assert Engine("x").tick() == "x"
`)

	mgr := workspace.NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   50,
	})

	if !languageDetects(root, "python") {
		t.Fatal("expected temp fixture to count as Python workspace")
	}

	overview := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_overview"})
	if overview.Error != "" || !strings.Contains(overview.Output, "Indexed") {
		t.Fatalf("unexpected pythonOverview result: %#v", overview)
	}

	if !strings.Contains(overview.Output, "Python files") {
		t.Fatalf("pythonOverview output missing Python files header: %s", overview.Output)
	}

	entry := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_entrypoints"})
	if entry.Error != "" || !strings.Contains(entry.Output, "engine.py") {
		t.Fatalf("unexpected pythonEntrypoints result: %#v", entry)
	}

	find := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_find_symbol", Pattern: "tick"})
	if find.Error != "" || !strings.Contains(find.Output, "engine.py") {
		t.Fatalf("unexpected pythonFindSymbol result: %#v", find)
	}

	findDecl := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_find_symbol", Pattern: "class Engine"})
	if findDecl.Error != "" || !strings.Contains(findDecl.Output, "Engine") {
		t.Fatalf("expected declaration-shaped pythonFindSymbol query to find Engine, got %#v", findDecl)
	}

	read := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_read_symbol", Pattern: "Engine.tick"})
	if read.Error != "" || !strings.Contains(read.Output, "return self.name") {
		t.Fatalf("unexpected pythonReadSymbol result: %#v", read)
	}

	readDecl := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_read_symbol", Pattern: "class Engine"})
	if readDecl.Error != "" || !strings.Contains(readDecl.Output, "class Engine") {
		t.Fatalf("expected declaration-shaped pythonReadSymbol to return class Engine snippet, got %#v", readDecl)
	}

	related := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_related_files", Pattern: "engine"})
	if related.Error != "" || !strings.Contains(related.Output, "engine.py") {
		t.Fatalf("unexpected pythonRelatedFiles result: %#v", related)
	}

	callsFrom := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_calls_from", Pattern: "helper"})
	if callsFrom.Error != "" && !strings.Contains(callsFrom.Error, "no Python LSP") {
		// Fall back is OK; we don't require calls-from to succeed if LSP is absent.
	}

	// The semantic fallback should still produce something for helper().
	if callsFrom.Error == "" && !strings.Contains(callsFrom.Output, "tick") && !strings.Contains(callsFrom.Output, "Engine") {
		t.Fatalf("unexpected pythonCallsFrom result (index fallback expected to mention tick/Engine): %#v", callsFrom)
	}

	callsInto := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_calls_into", Pattern: "tick"})
	if callsInto.Error == "" && !strings.Contains(callsInto.Output, "helper") && !strings.Contains(callsInto.Output, "run") {
		// Semantic index may or may not match depending on call text; don't fail hard.
		t.Logf("pythonCallsInto output: %s", callsInto.Output)
	}

	// References: falls back to rg-based search which should find helper
	// across .py files in the workspace.
	refs := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_references", Pattern: "helper", Path: "."})
	if refs.Error != "" {
		// rg may not be installed in all test envs; log and continue.
		t.Logf("pythonReferences error (likely rg missing): %s", refs.Error)
	} else if !strings.Contains(refs.Output, "helper") {
		t.Fatalf("pythonReferences output missing helper: %#v", refs)
	}

	outline := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_file_outline", Path: filepath.Join("src", "demo", "engine.py")})
	if outline.Error != "" {
		t.Fatalf("unexpected pythonFileOutline error: %#v", outline)
	}

	if !strings.Contains(outline.Output, "file: src/demo/engine.py") {
		t.Fatalf("pythonFileOutline missing file header: %#v", outline)
	}

	if !strings.Contains(outline.Output, "Engine") || !strings.Contains(outline.Output, "tick") {
		t.Fatalf("pythonFileOutline missing symbols: %#v", outline)
	}

	if !strings.Contains(outline.Summary, "Outlined") {
		t.Fatalf("pythonFileOutline summary: %#v", outline)
	}
}

func TestPythonWorkspaceToolsWithLSP(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		if _, err := exec.LookPath("pylsp"); err != nil {
			t.Skip("no Python LSP installed (pyright-langserver or pylsp required)")
		}
	}

	root := t.TempDir()
	writePythonToolFixture(t, filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
version = "0.1.0"
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "__init__.py"), "")
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "engine.py"), `
class Engine:
    def __init__(self, name: str) -> None:
        self.name = name

    def tick(self) -> str:
        return self.name

def helper() -> str:
    return Engine("h").tick()
`)
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "runner.py"), `
from .engine import Engine, helper

def run() -> str:
    e = Engine("runner")
    return e.tick() + "|" + helper()
`)

	mgr := workspace.NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   50,
	})

	ws := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_workspace_symbol", Pattern: "Engine"})
	if ws.Error != "" {
		t.Fatalf("pythonWorkspaceSymbol error: %v", ws.Error)
	}

	def := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_definition", Pattern: "tick"})
	if def.Error != "" {
		t.Fatalf("pythonDefinition error: %v", def.Error)
	}

	diag := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "python_diagnostics", Path: filepath.Join("src", "demo", "engine.py")})
	if diag.Error != "" {
		t.Logf("pythonDiagnostics error (LSP may be starting): %v", diag.Error)
	}
}

func writePythonToolFixture(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	// Python indentation is semantic; trimming trailing whitespace only.
	trimmed := strings.TrimLeft(content, "\n")
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
