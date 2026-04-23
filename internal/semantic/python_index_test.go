package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPythonIndex(t *testing.T) {
	root := t.TempDir()
	writePythonFixture(t, filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
`)
	writePythonFixture(t, filepath.Join(root, "src", "demo", "__init__.py"), "")
	writePythonFixture(t, filepath.Join(root, "src", "demo", "core.py"), `
import math

class Engine:
    def __init__(self, name):
        self.name = name

    def tick(self):
        return math.floor(1.5)

def helper(x):
    return Engine(x).tick()

if __name__ == "__main__":
    helper("demo")
`)
	writePythonFixture(t, filepath.Join(root, "src", "demo", "app.py"), `
from .core import Engine, helper

def run():
    e = Engine("app")
    return e.tick() + helper("inner")
`)
	writePythonFixture(t, filepath.Join(root, "tests", "test_core.py"), `
from demo.core import Engine

def test_engine():
    assert Engine("x").tick() is not None
`)

	index, err := BuildPythonIndex(root)
	if err != nil {
		t.Fatalf("BuildPythonIndex error: %v", err)
	}
	if index == nil || len(index.Files) == 0 {
		t.Fatal("expected non-empty python index")
	}
	if len(index.Files) < 4 {
		t.Fatalf("expected at least 4 files, got %d", len(index.Files))
	}

	core := index.FileByPath("src/demo/core.py")
	if core == nil {
		t.Fatal("expected src/demo/core.py indexed")
	}
	if !core.HasMain {
		t.Fatal("expected core.py HasMain = true")
	}
	if core.Classes != 1 {
		t.Fatalf("expected 1 class in core.py, got %d", core.Classes)
	}
	if core.Funcs < 1 {
		t.Fatalf("expected at least 1 top-level func in core.py, got %d", core.Funcs)
	}
	if core.Methods < 2 {
		t.Fatalf("expected at least 2 methods in core.py, got %d", core.Methods)
	}

	matches := index.FindSymbols("tick", 10)
	if len(matches) == 0 {
		t.Fatal("expected FindSymbols to match tick")
	}
	foundTick := false
	for _, m := range matches {
		if m.Name == "tick" && strings.Contains(m.Path, "core.py") {
			foundTick = true
			break
		}
	}
	if !foundTick {
		t.Fatalf("did not find Engine.tick among matches: %+v", matches)
	}

	classMatches := index.FindSymbols("Engine", 10)
	if len(classMatches) == 0 {
		t.Fatal("expected to find Engine class")
	}

	entry := index.DefaultFiles(4)
	if len(entry) == 0 {
		t.Fatal("expected DefaultFiles to return entrypoints")
	}
	foundAnchor := false
	for _, p := range entry {
		if strings.Contains(p, "core.py") || strings.Contains(p, "app.py") {
			foundAnchor = true
			break
		}
	}
	if !foundAnchor {
		t.Fatalf("DefaultFiles did not surface core/app, got %v", entry)
	}

	related := index.RelevantFiles([]string{"engine"}, 4)
	if len(related) == 0 {
		t.Fatalf("expected RelevantFiles to return candidates for 'engine'")
	}

	if _, ok := index.Forward["src/demo/app.py"]; !ok {
		t.Fatalf("expected app.py to have import edges, forward=%v", index.Forward)
	}

	callsFrom := index.FindCallsFrom("helper", 16)
	if len(callsFrom) == 0 {
		t.Fatalf("expected helper() to have outgoing calls")
	}

	callsInto := index.FindCallsInto("tick", 16)
	if len(callsInto) == 0 {
		t.Fatalf("expected tick to have incoming calls")
	}

	sum := index.Summary()
	if !strings.Contains(sum, "Indexed") {
		t.Fatalf("summary missing 'Indexed': %s", sum)
	}
}

func TestPythonImportResolution(t *testing.T) {
	byModule := map[string]string{
		"demo":          "src/demo/__init__.py",
		"demo.core":     "src/demo/core.py",
		"demo.app":      "src/demo/app.py",
	}
	byRel := map[string]string{
		"src/demo/__init__.py": "src/demo/__init__.py",
		"src/demo/core.py":     "src/demo/core.py",
		"src/demo/app.py":      "src/demo/app.py",
	}

	if got := resolvePythonModule("src/demo/app.py", ".core", byModule, byRel); got != "src/demo/core.py" {
		t.Fatalf("relative import failed, got %q", got)
	}
	if got := resolvePythonModule("src/demo/app.py", "demo.core", byModule, byRel); got != "src/demo/core.py" {
		t.Fatalf("absolute dotted import failed, got %q", got)
	}
	if got := resolvePythonModule("src/demo/app.py", "math", byModule, byRel); got != "" {
		t.Fatalf("stdlib import should resolve to empty, got %q", got)
	}
}

func TestPythonStripComment(t *testing.T) {
	cases := map[string]string{
		`x = 1 # trailing`:      `x = 1 `,
		`s = "has # inside"`:    `s = "has # inside"`,
		`s = 'also # inside'`:   `s = 'also # inside'`,
		`# whole line comment`:  ``,
		`plain code no comment`: `plain code no comment`,
	}
	for input, want := range cases {
		if got := stripPythonComment(input); got != want {
			t.Fatalf("stripPythonComment(%q) = %q, want %q", input, got, want)
		}
	}
}

func writePythonFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	trimmed := strings.TrimLeft(content, "\n")
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
