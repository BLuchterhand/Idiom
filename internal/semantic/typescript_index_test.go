package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTsIndex(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020"}}`)
	writeTsFixture(t, filepath.Join(root, "src", "index.ts"), `
import { greet } from "./greeter";

export function main(): void {
  greet("world");
}
`)
	writeTsFixture(t, filepath.Join(root, "src", "greeter.ts"), `
export interface Greeter { name: string }

export function greet(name: string): void {
  console.log(name);
}

export class Shouter {
  shout(name: string): void {
    console.log(name.toUpperCase());
  }
}
`)

	idx, err := BuildTsIndex(root)
	if err != nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil index")
	}
	if len(idx.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(idx.Files))
	}

	greeter := idx.FileByPath("src/greeter.ts")
	if greeter == nil {
		t.Fatal("missing greeter.ts")
	}
	if greeter.Funcs < 1 || greeter.Classes < 1 || greeter.Types < 1 {
		t.Fatalf("greeter.ts counts: %#v", greeter)
	}

	// Import graph: index.ts -> greeter.ts
	if deps := idx.Forward["src/index.ts"]; len(deps) == 0 || deps[0] != "src/greeter.ts" {
		t.Fatalf("expected src/index.ts to import src/greeter.ts, got %v", deps)
	}

	// Symbol search
	matches := idx.FindSymbols("greet", 8)
	if len(matches) == 0 {
		t.Fatal("expected FindSymbols to return greet")
	}

	// Call graph: main -> greet
	outgoing := idx.FindCallsFrom("main", 8)
	foundGreet := false
	for _, call := range outgoing {
		if call.CalleeName == "greet" {
			foundGreet = true
			break
		}
	}
	if !foundGreet {
		t.Fatalf("expected main to call greet, got %+v", outgoing)
	}

	// Incoming: greet is called by main
	incoming := idx.FindCallsInto("greet", 8)
	if len(incoming) == 0 {
		t.Fatalf("expected at least one caller of greet")
	}

	// WhoImports: greeter.ts is imported by index.ts
	importers := idx.WhoImports("greeter.ts")
	if len(importers) == 0 || !strings.Contains(strings.Join(importers, ","), "src/index.ts") {
		t.Fatalf("expected greeter.ts to be imported by src/index.ts, got %v", importers)
	}
}

func TestTsIndexSkipsNodeModules(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "src", "app.ts"), "export function app() {}\n")
	writeTsFixture(t, filepath.Join(root, "node_modules", "lodash", "index.js"), "module.exports = {};\n")

	idx, err := BuildTsIndex(root)
	if err != nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	if idx == nil || len(idx.Files) != 1 {
		t.Fatalf("expected 1 indexed file, got %d", len(idx.Files))
	}
	if idx.Files[0].Path != "src/app.ts" {
		t.Fatalf("indexed wrong file: %s", idx.Files[0].Path)
	}
}

func TestTsIndexDetectsClassMethod(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "cls.ts"), `
export class Foo {
  bar(): number { return 1; }
  baz(): number { return this.bar(); }
}
`)
	idx, err := BuildTsIndex(root)
	if err != nil || idx == nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	file := idx.FileByPath("cls.ts")
	if file == nil {
		t.Fatal("missing cls.ts")
	}
	var names []string
	for _, sym := range file.Symbols {
		names = append(names, sym.Qualified)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "Foo.bar") || !strings.Contains(joined, "Foo.baz") {
		t.Fatalf("expected Foo.bar and Foo.baz methods, got %s", joined)
	}
}

func writeTsFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
