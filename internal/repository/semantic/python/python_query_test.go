package python

import (
	"path/filepath"
	"testing"
)

func TestPythonFindSymbolsNormalizesDeclarationPatterns(t *testing.T) {
	root := t.TempDir()
	writePythonFixture(t, filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
`)
	writePythonFixture(t, filepath.Join(root, "src", "demo", "core.py"), `
class Engine:
    def tick(self):
        return 1

async def load_engine():
    return Engine()
`)

	index, err := BuildPythonIndex(root)
	if err != nil {
		t.Fatalf("BuildPythonIndex error: %v", err)
	}
	if matches := index.FindSymbols("class Engine", 8); len(matches) == 0 || matches[0].Name != "Engine" {
		t.Fatalf("expected declaration-shaped class search to find Engine, got %#v", matches)
	}
	if matches := index.FindSymbols("async def load_engine", 8); len(matches) == 0 || matches[0].Name != "load_engine" {
		t.Fatalf("expected declaration-shaped async def search to find load_engine, got %#v", matches)
	}
}
