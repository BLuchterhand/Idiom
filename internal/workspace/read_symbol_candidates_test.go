package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestReadSymbolPathMissReportsCandidateAnchorsAcrossLanguages(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T) (*Manager, ToolRequest)
		wantPath   string
		forbidBody string
	}{
		{
			name: "go",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				root := t.TempDir()
				writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/pathmiss\n\ngo 1.22\n")
				writeTestFile(t, filepath.Join(root, "internal", "app", "model_state.go"), "package app\n\ntype Model struct{}\n")
				writeTestFile(t, filepath.Join(root, "internal", "app", "lifecycle.go"), "package app\n\nfunc (m Model) Update() int {\n\treturn 42\n}\n")
				return NewManager(config.Workspace{Root: root, CWD: root}), ToolRequest{Tool: "go_read_symbol", Pattern: "Model.Update", Path: filepath.Join("internal", "app", "model_state.go")}
			},
			wantPath:   "internal/app/lifecycle.go",
			forbidBody: "return 42",
		},
		{
			name: "python",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				root := t.TempDir()
				writePythonToolFixture(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"pathmiss\"\nversion = \"0.1.0\"\n")
				writePythonToolFixture(t, filepath.Join(root, "src", "demo", "empty.py"), "# no symbols here\n")
				writePythonToolFixture(t, filepath.Join(root, "src", "demo", "engine.py"), "class Engine:\n    def tick(self):\n        return \"tick-body\"\n")
				return NewManager(config.Workspace{Root: root, CWD: root}), ToolRequest{Tool: "python_read_symbol", Pattern: "Engine.tick", Path: filepath.Join("src", "demo", "empty.py")}
			},
			wantPath:   "src/demo/engine.py",
			forbidBody: "tick-body",
		},
		{
			name: "cpp",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				root := t.TempDir()
				writeCppFixture(t, filepath.Join(root, "include", "entity_manager.h"), "struct EntityManager {\n    void tick();\n};\n")
				writeCppFixture(t, filepath.Join(root, "src", "entity_manager.cpp"), "#include \"entity_manager.h\"\nvoid EntityManager::tick() {\n    int tick_body = 1;\n}\n")
				writeCppFixture(t, filepath.Join(root, "src", "main.cpp"), "int main() { return 0; }\n")
				return NewManager(config.Workspace{Root: root, CWD: root}), ToolRequest{Tool: "cpp_read_symbol", Pattern: "EntityManager::tick", Path: filepath.Join("src", "main.cpp")}
			},
			wantPath:   "src/entity_manager.cpp",
			forbidBody: "tick_body",
		},
		{
			name: "typescript",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				root := t.TempDir()
				writeTsTestFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs"}}`)
				writeTsTestFixture(t, filepath.Join(root, "src", "other.ts"), "export const other = 1;\n")
				writeTsTestFixture(t, filepath.Join(root, "src", "greeter.ts"), "export class Shouter {\n  shout(name: string): void {\n    console.log(name.toUpperCase());\n  }\n}\n")
				return NewManager(config.Workspace{Root: root, CWD: root}), ToolRequest{Tool: "ts_read_symbol", Pattern: "Shouter.shout", Path: filepath.Join("src", "other.ts")}
			},
			wantPath:   "src/greeter.ts",
			forbidBody: "toUpperCase",
		},
		{
			name: "rust",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				mgr, _ := newRustFixtureManager(t)
				return mgr, ToolRequest{Tool: "rust_read_symbol", Pattern: "spawn", Path: filepath.Join("src", "main.rs")}
			},
			wantPath:   "src/worker.rs",
			forbidBody: "Worker { id }",
		},
		{
			name: "java",
			setup: func(t *testing.T) (*Manager, ToolRequest) {
				root := buildJavaTempWorkspace(t)
				writeJavaFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "package-info.java"), "package com.example;\n")
				return NewManager(config.Workspace{Root: root, CWD: root}), ToolRequest{Tool: "java_read_symbol", Pattern: "private int compute()", Path: filepath.Join("src", "main", "java", "com", "example", "package-info.java")}
			},
			wantPath:   "src/main/java/com/example/util/Helper.java",
			forbidBody: "return 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, req := tt.setup(t)
			result := mgr.Execute(context.Background(), req)
			assertReadSymbolCandidateMiss(t, result, tt.wantPath, tt.forbidBody)
		})
	}
}

func TestCodeNavigateReadSymbolPathMissReportsCandidateAnchor(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/pathmiss\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "internal", "app", "model_state.go"), "package app\n\ntype Model struct{}\n")
	writeTestFile(t, filepath.Join(root, "internal", "app", "lifecycle.go"), "package app\n\nfunc (m Model) Update() int {\n\treturn 42\n}\n")

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Pattern:    "Model.Update",
		Path:       filepath.Join("internal", "app", "model_state.go"),
	})
	assertReadSymbolCandidateMiss(t, result, "internal/app/lifecycle.go", "return 42")
	if !strings.Contains(result.Output, "routed_tool: go_read_symbol") {
		t.Fatalf("expected code_navigate route metadata, got:\n%s", result.Output)
	}
}

func assertReadSymbolCandidateMiss(t *testing.T, result ToolResult, wantPath string, forbidBody string) {
	t.Helper()
	if result.Error != "" {
		t.Fatalf("expected candidate-bearing path miss, got error result: %#v", result)
	}
	if !strings.Contains(result.Summary, "path-filter miss") && !strings.Contains(result.Output, "path-filter miss") {
		t.Fatalf("expected path-filter miss summary, got %#v", result)
	}
	for _, want := range []string{
		"candidate_anchors:",
		"candidate_requests:",
		"anchors only; read one candidate path to inspect source body",
		wantPath,
		`"tool":"code_navigate"`,
		`"capability":"read_symbol"`,
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("candidate miss output missing %q:\nsummary=%s\noutput=\n%s", want, result.Summary, result.Output)
		}
	}
	if forbidBody != "" && strings.Contains(result.Output, forbidBody) {
		t.Fatalf("candidate miss output must not include source body %q:\n%s", forbidBody, result.Output)
	}
}
