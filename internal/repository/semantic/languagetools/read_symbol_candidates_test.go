package languagetools_test

import (
	"context"
	"path/filepath"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Read-symbol path misses", func() {
	DescribeTable("reports candidate anchors without leaking source bodies",
		func(setup func() (*workspace.Manager, workspace.ToolRequest), expectedPath, forbiddenBody string) {
			manager, request := setup()
			result := manager.Execute(context.Background(), request)
			assertReadSymbolCandidateMiss(result, expectedPath, forbiddenBody)
		},
		Entry("Go", func() (*workspace.Manager, workspace.ToolRequest) {
			root := GinkgoT().TempDir()
			writeTestFile(filepath.Join(root, "go.mod"), "module example.com/pathmiss\n\ngo 1.22\n")
			writeTestFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "model_state.go"), "package app\n\ntype Model struct{}\n")
			writeTestFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "lifecycle.go"), "package app\n\nfunc (m Model) Update() int {\n\treturn 42\n}\n")
			return workspace.NewManager(config.Workspace{Root: root, CWD: root}), workspace.ToolRequest{
				Tool: "go_read_symbol", Pattern: "Model.Update", Path: filepath.Join("internal", "surfaces", "tui", "app", "model_state.go"),
			}
		}, "internal/surfaces/tui/app/lifecycle.go", "return 42"),
		Entry("Python", func() (*workspace.Manager, workspace.ToolRequest) {
			root := GinkgoT().TempDir()
			writePythonToolFixture(filepath.Join(root, "pyproject.toml"), "[project]\nname = \"pathmiss\"\nversion = \"0.1.0\"\n")
			writePythonToolFixture(filepath.Join(root, "src", "demo", "empty.py"), "# no symbols here\n")
			writePythonToolFixture(filepath.Join(root, "src", "demo", "engine.py"), "class Engine:\n    def tick(self):\n        return \"tick-body\"\n")
			return workspace.NewManager(config.Workspace{Root: root, CWD: root}), workspace.ToolRequest{
				Tool: "python_read_symbol", Pattern: "Engine.tick", Path: filepath.Join("src", "demo", "empty.py"),
			}
		}, "src/demo/engine.py", "tick-body"),
		Entry("C++", func() (*workspace.Manager, workspace.ToolRequest) {
			root := GinkgoT().TempDir()
			writeCppFixture(filepath.Join(root, "include", "entity_manager.h"), "struct EntityManager {\n    void tick();\n};\n")
			writeCppFixture(filepath.Join(root, "src", "entity_manager.cpp"), "#include \"entity_manager.h\"\nvoid EntityManager::tick() {\n    int tick_body = 1;\n}\n")
			writeCppFixture(filepath.Join(root, "src", "main.cpp"), "int main() { return 0; }\n")
			return workspace.NewManager(config.Workspace{Root: root, CWD: root}), workspace.ToolRequest{
				Tool: "cpp_read_symbol", Pattern: "EntityManager::tick", Path: filepath.Join("src", "main.cpp"),
			}
		}, "src/entity_manager.cpp", "tick_body"),
		Entry("TypeScript", func() (*workspace.Manager, workspace.ToolRequest) {
			root := GinkgoT().TempDir()
			writeTsTestFixture(filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs"}}`)
			writeTsTestFixture(filepath.Join(root, "src", "other.ts"), "export const other = 1;\n")
			writeTsTestFixture(filepath.Join(root, "src", "greeter.ts"), "export class Shouter {\n  shout(name: string): void {\n    console.log(name.toUpperCase());\n  }\n}\n")
			return workspace.NewManager(config.Workspace{Root: root, CWD: root}), workspace.ToolRequest{
				Tool: "ts_read_symbol", Pattern: "Shouter.shout", Path: filepath.Join("src", "other.ts"),
			}
		}, "src/greeter.ts", "toUpperCase"),
		Entry("Rust", func() (*workspace.Manager, workspace.ToolRequest) {
			manager, _ := newRustFixtureManager()
			return manager, workspace.ToolRequest{
				Tool: "rust_read_symbol", Pattern: "spawn", Path: filepath.Join("src", "main.rs"),
			}
		}, "src/worker.rs", "Worker { id }"),
		Entry("Java", func() (*workspace.Manager, workspace.ToolRequest) {
			root := buildJavaTempWorkspace()
			writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "package-info.java"), "package com.example;\n")
			return workspace.NewManager(config.Workspace{Root: root, CWD: root}), workspace.ToolRequest{
				Tool: "java_read_symbol", Pattern: "private int compute()", Path: filepath.Join("src", "main", "java", "com", "example", "package-info.java"),
			}
		}, "src/main/java/com/example/util/Helper.java", "return 42"),
	)

	It("preserves candidate anchors through code_navigate", func() {
		root := GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/pathmiss\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "model_state.go"), "package app\n\ntype Model struct{}\n")
		writeTestFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "lifecycle.go"), "package app\n\nfunc (m Model) Update() int {\n\treturn 42\n}\n")

		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "code_navigate", Capability: "read_symbol", Pattern: "Model.Update",
			Path: filepath.Join("internal", "surfaces", "tui", "app", "model_state.go"),
		})
		assertReadSymbolCandidateMiss(result, "internal/surfaces/tui/app/lifecycle.go", "return 42")
		Expect(result.Output).To(ContainSubstring("routed_tool: go_read_symbol"))
	})
})

func assertReadSymbolCandidateMiss(result workspace.ToolResult, expectedPath, forbiddenBody string) {
	GinkgoHelper()
	Expect(result.Error).To(BeEmpty())
	Expect(result.Summary + "\n" + result.Output).To(ContainSubstring("path-filter miss"))
	Expect(result.Output).To(And(
		ContainSubstring("candidate_anchors:"),
		ContainSubstring("candidate_requests:"),
		ContainSubstring("anchors only; read one candidate path to inspect source body"),
		ContainSubstring(expectedPath),
		ContainSubstring(`"tool":"code_navigate"`),
		ContainSubstring(`"capability":"read_symbol"`),
	))
	if forbiddenBody != "" {
		Expect(result.Output).NotTo(ContainSubstring(forbiddenBody))
	}
}
