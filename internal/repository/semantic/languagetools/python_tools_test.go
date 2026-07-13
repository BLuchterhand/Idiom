package languagetools_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Python workspace tools", func() {
	It("provides syntax-index discovery and navigation", func() {
		root := buildPythonWorkspace()
		manager := newLanguageWorkspaceManager(root, 30)
		Expect(languageDetects(root, "python")).To(BeTrue())

		overview := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_overview"})
		expectToolSuccess(overview, "Indexed", "Python files")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_entrypoints"}), "engine.py")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_find_symbol", Pattern: "tick"}), "engine.py")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_find_symbol", Pattern: "class Engine"}), "Engine")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_read_symbol", Pattern: "Engine.tick"}), "return self.name")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_read_symbol", Pattern: "class Engine"}), "class Engine")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_related_files", Pattern: "engine"}), "engine.py")

		callsFrom := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_calls_from", Pattern: "helper"})
		if callsFrom.Error != "" {
			Expect(callsFrom.Error).To(ContainSubstring("no Python LSP"))
		} else {
			Expect(callsFrom.Output).To(Or(ContainSubstring("tick"), ContainSubstring("Engine")))
		}

		references := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "python_references", Pattern: "helper", Path: "."})
		if references.Error == "" {
			Expect(references.Output).To(ContainSubstring("helper"))
		} else {
			fmt.Fprintf(GinkgoWriter, "python references unavailable: %s\n", references.Error)
		}

		outline := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "python_file_outline", Path: filepath.Join("src", "demo", "engine.py"),
		})
		expectToolSuccess(outline, "file: src/demo/engine.py", "Engine", "tick")
		Expect(outline.Summary).To(ContainSubstring("Outlined"))
	})

	It("uses an installed Python LSP for precise operations", func() {
		if _, err := exec.LookPath("pyright-langserver"); err != nil {
			if _, err := exec.LookPath("pylsp"); err != nil {
				Skip("no Python LSP installed")
			}
		}

		root := buildPythonWorkspace()
		manager := newLanguageWorkspaceManager(root, 30)
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "python_workspace_symbol", Pattern: "Engine",
		}).Error).To(BeEmpty())
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "python_definition", Pattern: "tick",
		}).Error).To(BeEmpty())

		diagnostics := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "python_diagnostics", Path: filepath.Join("src", "demo", "engine.py"),
		})
		if diagnostics.Error != "" {
			fmt.Fprintf(GinkgoWriter, "python diagnostics unavailable while LSP starts: %s\n", diagnostics.Error)
		}
	})
})

func buildPythonWorkspace() string {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writePythonToolFixture(filepath.Join(root, "pyproject.toml"), "[project]\nname = \"demo\"\nversion = \"0.1.0\"\n")
	writePythonToolFixture(filepath.Join(root, "src", "demo", "__init__.py"), "")
	writePythonToolFixture(filepath.Join(root, "src", "demo", "engine.py"), `
class Engine:
    def __init__(self, name: str) -> None:
        self.name = name

    def tick(self) -> str:
        return self.name

def helper() -> str:
    return Engine("h").tick()

if __name__ == "__main__":
    helper()
`)
	writePythonToolFixture(filepath.Join(root, "src", "demo", "runner.py"), `
from .engine import Engine, helper

def run() -> str:
    e = Engine("runner")
    return e.tick() + "|" + helper()
`)
	writePythonToolFixture(filepath.Join(root, "src", "demo", "base_system.py"), "class SystemBase:\n    def process_cycle(self):\n        return None\n")
	writePythonToolFixture(filepath.Join(root, "src", "demo", "worker_system.py"), "from .base_system import SystemBase\n\nclass WorkerSystem(SystemBase):\n    def process_cycle(self):\n        return \"work\"\n")
	writePythonToolFixture(filepath.Join(root, "tests", "test_engine.py"), "from demo.engine import Engine\n\ndef test_tick():\n    assert Engine(\"x\").tick() == \"x\"\n")
	return root
}

func newLanguageWorkspaceManager(root string, commandTimeoutSeconds int) *workspace.Manager {
	return workspace.NewManager(config.Workspace{
		Root: root, CWD: root, EnableShell: true, EnableWrites: true,
		CommandTimeoutSeconds: commandTimeoutSeconds, MaxFileBytes: 65536, MaxDirectoryEntries: 50,
	})
}

func writePythonToolFixture(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644)).To(Succeed())
}
