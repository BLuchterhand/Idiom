package languagetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"idiom/internal/platform/commandexec"
)

func TestGoDiagnosticTargetsIncludesWorkspaceRootFiles(t *testing.T) {
	root := t.TempDir()
	writeOwnerTestFile(t, filepath.Join(root, "go.mod"), "module example.com/diagnostics\n\ngo 1.22\n")
	writeOwnerTestFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")

	mgr := NewManager(ownerTestHost{root: root, maxDirectoryEntries: 16})

	files, err := mgr.goDiagnosticTargets(ToolRequest{Tool: "go_diagnostics", Path: "."})
	if err != nil {
		t.Fatalf("goDiagnosticTargets returned error: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("expected workspace-root Go diagnostics to include indexed files")
	}

	if files[0] != "main.go" {
		t.Fatalf("expected main.go as diagnostic target, got %#v", files)
	}
}

func TestFindGoToolUsesGoEnvGOPATHBin(t *testing.T) {
	root := t.TempDir()
	gopath := filepath.Join(root, "gopath")
	binDir := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir GOPATH/bin: %v", err)
	}
	toolPath := filepath.Join(binDir, "gopls")
	writeOwnerTestFile(t, toolPath, "#!/bin/sh\n")

	t.Setenv("GOPATH", gopath)

	mgr := NewManager(ownerTestHost{root: root})
	if got := mgr.findGoTool("gopls"); got != toolPath {
		t.Fatalf("findGoTool = %q, want %q", got, toolPath)
	}
}

func TestLanguageToolBinaryLookupsUseHostPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	for _, name := range []string{"clangd", "rust-analyzer", "jdtls", "typescript-language-server"} {
		writeOwnerTestExecutable(t, filepath.Join(binDir, name))
	}
	t.Setenv("PATH", binDir)

	mgr := NewManager(ownerTestHost{root: root})
	for name, got := range map[string]string{
		"clangd":                     mgr.clangdBinary(),
		"rust-analyzer":              mgr.rustAnalyzerBinary(),
		"jdtls":                      mgr.jdtlsBinary(),
		"typescript-language-server": mgr.tsserverBinary(),
	} {
		want := filepath.Join(binDir, name)
		if got != want {
			t.Fatalf("%s lookup = %q, want %q", name, got, want)
		}
	}
}

func TestPythonLSPBinaryPrefersPyrightOverPylsp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	writeOwnerTestExecutable(t, filepath.Join(binDir, "pylsp"))
	writeOwnerTestExecutable(t, filepath.Join(binDir, "pyright-langserver"))
	t.Setenv("PATH", binDir)

	mgr := NewManager(ownerTestHost{root: root})
	path, server := mgr.pythonLSPBinary()
	if path != filepath.Join(binDir, "pyright-langserver") || server != "pyright" {
		t.Fatalf("pythonLSPBinary = %q/%q, want pyright-langserver/pyright", path, server)
	}
}

func TestPylspOutgoingCallFormatterReturnsStructuredRelationships(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app.py")
	lib := filepath.Join(root, "lib.py")

	writeOwnerTestFile(t, app, "def run():\n    return helper()\n")
	writeOwnerTestFile(t, lib, "def helper():\n    return 'ok'\n")

	mgr := NewManager(ownerTestHost{root: root, maxDirectoryEntries: 16})

	payload := mgr.formatPylspOutgoingCalls(app, "run", []LSPOutgoingCall{{
		To: LSPCallHierarchyItem{
			Name: "helper",
			URI:  LSPFileURI(lib),
			SelectionRange: LSPRange{
				Start: LSPPosition{Line: 0, Character: 4},
			},
		},
		FromRanges: []LSPRange{{
			Start: LSPPosition{Line: 1, Character: 11},
		}},
	}}, "")
	if strings.TrimSpace(payload.Output) == "" || len(payload.Relationships) != 1 {
		t.Fatalf("expected output and structured relationship payload, got %#v", payload)
	}

	relationship := payload.Relationships[0]
	if relationship.SourcePath != "app.py" || relationship.SourceLine != 2 || relationship.SourceSymbol != "run" || relationship.TargetPath != "lib.py" || relationship.TargetLine != 1 || relationship.TargetSymbol != "helper" {
		t.Fatalf("unexpected LSP relationship payload: %#v", relationship)
	}
}

type ownerTestHost struct {
	root                string
	maxDirectoryEntries int
}

func (h ownerTestHost) Enabled() bool {
	return true
}

func (h ownerTestHost) Root() string {
	return h.root
}

func (h ownerTestHost) MaxDirectoryEntries() int {
	return h.maxDirectoryEntries
}

func (h ownerTestHost) MaxOutputBytes() int {
	return 1 << 20
}

func (h ownerTestHost) WritesEnabled() bool {
	return false
}

func (h ownerTestHost) ResolvePath(path string, workdir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	base := h.root
	if strings.TrimSpace(workdir) != "" {
		base = workdir
	}

	return filepath.Clean(filepath.Join(base, path)), nil
}

func (h ownerTestHost) DisplayPath(path string) string {
	rel, err := filepath.Rel(h.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(rel)
}

func (h ownerTestHost) HandleDisplay(abs string) string {
	return h.DisplayPath(abs)
}

func (h ownerTestHost) RememberPath(string) {}

func (h ownerTestHost) PrimeDiscoveredPaths([]string) {}

func (h ownerTestHost) PathForHandle(handle string) (string, error) {
	return "", fmt.Errorf("unknown handle %q", handle)
}

func (h ownerTestHost) Execute(context.Context, ToolRequest) ToolResult {
	return ToolResult{Error: "not implemented"}
}

func (h ownerTestHost) RunCommand(_ context.Context, _ string, _ []string, name string, args ...string) commandexec.Result {
	if name == "go" && len(args) == 2 && args[0] == "env" {
		switch args[1] {
		case "GOBIN", "GOPATH":
			return commandexec.Result{Stdout: os.Getenv(args[1])}
		}
	}

	return commandexec.Result{ExitCode: 1, Err: fmt.Errorf("not implemented")}
}

func writeOwnerTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeOwnerTestExecutable(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
