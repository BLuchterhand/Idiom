package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
)

func requireContentSearchBackend() {
	ginkgo.GinkgoHelper()

	if _, err := resolveContentSearchBackend(); err != nil {
		ginkgo.Skip("no supported content search backend installed on this machine")
	}
}

var _ = ginkgo.Describe("manager", func() {
	ginkgo.It("source sanity falls back when language diagnostics unavailable", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "native", "tests", "test_energy.cpp")
		gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(path, []byte("TEST_CASE(\"broken\" {\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: "native/tests/test_energy.cpp"})
		gomega.Expect(result.Tool).To(gomega.Equal("source_sanity"), fmt.Sprintf("tool = %q, want source_sanity", result.Tool))
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected generic fallback to catch malformed source, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("cpp_diagnostics unavailable"), fmt.Sprintf("expected diagnostics-unavailable note and generic fallback in output, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("generic source sanity failed"), fmt.Sprintf("expected diagnostics-unavailable note and generic fallback in output, got:\n%s", result.Output))
		if result.Facts != nil {
			gomega.Expect(result.Facts.DiagnosticEvidencePaths).To(gomega.BeEmpty(), fmt.Sprintf("generic source_sanity fallback must not emit diagnostic facts: %#v", result.Facts))
		}
		gomega.Expect(result.Error).To(gomega.Or(
			gomega.ContainSubstring("unclosed"),
			gomega.ContainSubstring("mismatched"),
		), fmt.Sprintf("expected delimiter failure, got %q", result.Error))

	})
	ginkgo.It("source sanity rejects cpp generic fallback as terminal evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "native", "src", "machine.cpp")
		gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(path, []byte("int value() {\n    return 1;\n}\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: "native/src/machine.cpp"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("C++ generic fallback should be insufficient terminal evidence, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("cpp_diagnostics unavailable"), fmt.Sprintf("expected diagnostics-unavailable note and advisory generic fallback, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("generic delimiter sanity passed"), fmt.Sprintf("expected diagnostics-unavailable note and advisory generic fallback, got:\n%s", result.Output))
		if result.Facts != nil {
			gomega.Expect(result.Facts.DiagnosticEvidencePaths).To(gomega.BeEmpty(), fmt.Sprintf("generic source_sanity fallback must not emit diagnostic facts: %#v", result.Facts))
		}
		gomega.Expect(result.Error).To(gomega.ContainSubstring("generic delimiter sanity is advisory"), fmt.Sprintf("expected insufficient C++ diagnostics error, got %q", result.Error))

	})
	ginkgo.It("go definition no match is empty evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nomatch\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		path := filepath.Join(root, "pkg", "feature.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(path, []byte("package pkg\n\nfunc Present() {}\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "definition",
			Path:       "pkg/feature.go",
			Pattern:    "Missing",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("symbol no-match should be empty discovery evidence, got error %q", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("Go location query found no matching symbol"), fmt.Sprintf("unexpected no-match result: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`no Go symbols matched "Missing"`), fmt.Sprintf("unexpected no-match result: %#v", result))

	})
	ginkgo.It("code navigate generic fallback allows no path when pattern is bounded", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fallback\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "internal", "runtime", "agent", "runner.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("package agent\n\nfunc reasoning_answer_salvage() {}\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "helper.py"), []byte("def unrelated():\n    pass\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "find_references",
			Pattern:    "reasoning_answer_salvage",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("pathless bounded fallback should not error, got %q", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/runtime/agent/runner.go"), fmt.Sprintf("expected bounded navigation match, got summary=%q output=%q", result.Summary, result.Output))

	})
	ginkgo.It("source sanity routes known languages through diagnostics tool", func() {
		if got := sourceSanityDiagnosticToolForPath("native/tests/test_energy.cpp"); got != "cpp_diagnostics" {
			ginkgo.Fail(fmt.Sprintf("cpp source_sanity tool = %q, want cpp_diagnostics", got))
		}

		if got := sourceSanityDiagnosticToolForPath("internal/runtime/agent/runner.go"); got != "go_diagnostics" {
			ginkgo.Fail(fmt.Sprintf("go source_sanity tool = %q, want go_diagnostics", got))
		}

		if got := sourceSanityDiagnosticToolForPath("scripts/demo.gd"); got != "" {
			ginkgo.Fail(fmt.Sprintf("gdscript source_sanity tool = %q, want generic fallback", got))
		}
		gomega.Expect(sourcekind.CoversSourceSanity("scripts/demo.gd")).To(gomega.BeTrue(), "source_sanity should cover gdscript through generic fallback")

	})
	ginkgo.It("source sanity compiles go test files", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/source-sanity\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "pkg", "feature.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644)).To(gomega.Succeed())

		testSource := filepath.Join(root, "pkg", "feature_test.go")
		gomega.Expect(os.WriteFile(testSource, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tmissingHelper()\n}\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("pkg", "feature_test.go")})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected source_sanity to fail on _test.go compile error, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("go test compile failed"), fmt.Sprintf("expected go test compile diagnostics, output=\n%s\nerror=\n%s", result.Output, result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("missingHelper"), fmt.Sprintf("expected go test compile diagnostics, output=\n%s\nerror=\n%s", result.Output, result.Error))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("source_sanity diagnostic facts = %#v, want pkg/feature_test.go", result.Facts))
		gomega.Expect(result.Facts.DiagnosticEvidencePaths).To(gomega.ContainElement("pkg/feature_test.go"), fmt.Sprintf("source_sanity diagnostic facts = %#v, want pkg/feature_test.go", result.Facts))

	})
	ginkgo.It("source sanity catches cross file compile error in non test package", func() {
		root := ginkgo.GinkgoT().TempDir()
		goMod := []byte("module example.com/source-sanity\n\ngo 1.22\n")
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), goMod, 0o644)).To(gomega.Succeed())

		pkgDir := filepath.Join(root, "pkg")
		gomega.Expect(os.MkdirAll(pkgDir, 0o755)).To(gomega.Succeed())

		a := filepath.Join(pkgDir, "a.go")
		gomega.Expect(os.WriteFile(a, []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644)).To(gomega.Succeed())

		b := filepath.Join(pkgDir, "b.go")
		gomega.Expect(os.WriteFile(b, []byte("package pkg\n\nfunc Double() int { return missingFunc() * 2 }\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("pkg", "b.go")})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected source_sanity to fail on cross-file compile error in non-test package, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("go test compile failed"), fmt.Sprintf("expected go test compile diagnostics with missingFunc, output=\n%s\nerror=\n%s", result.Output, result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("missingFunc"), fmt.Sprintf("expected go test compile diagnostics with missingFunc, output=\n%s\nerror=\n%s", result.Output, result.Error))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("source_sanity diagnostic facts = %#v, want pkg/b.go", result.Facts))
		gomega.Expect(result.Facts.DiagnosticEvidencePaths).To(gomega.ContainElement("pkg/b.go"), fmt.Sprintf("source_sanity diagnostic facts = %#v, want pkg/b.go", result.Facts))

	})
	ginkgo.It("source sanity catches gd script function without body", func() {
		root := ginkgo.GinkgoT().TempDir()
		source := filepath.Join(root, "scripts", "panel_test.gd")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("extends Node\n\nfunc _assert_gte(value, minimum, label):\n\nfunc _run_case():\n\tpass\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("scripts", "panel_test.gd")})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected gdscript structural sanity failure, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("GDScript function declaration has no indented body"), fmt.Sprintf("expected missing-body diagnostic, got %q", result.Error))

	})
	ginkgo.It("source sanity accepts gd script function body", func() {
		root := ginkgo.GinkgoT().TempDir()
		source := filepath.Join(root, "scripts", "panel_test.gd")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("extends Node\n\nfunc _assert_gte(value, minimum, label):\n\tif value < minimum:\n\t\tpush_error(label)\n\nfunc _run_case():\n\tpass\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("scripts", "panel_test.gd")})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected gdscript structural sanity to pass, got error=%v output=\n%s", result.Error, result.Output))

	})
	ginkgo.It("code navigate routes by path language", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "pkg", "runner.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "read_symbol",
			Path:       filepath.Join("pkg", "runner.go"),
			Pattern:    "Run",
		})
		gomega.Expect(result.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("tool = %q, want code_navigate", result.Tool))
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("code_navigate returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("go_read_symbol"), fmt.Sprintf("expected Go read-symbol route, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("routed_tool: go_read_symbol"), fmt.Sprintf("expected Go read-symbol route, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("func Run() string"), fmt.Sprintf("expected symbol body in output, got:\n%s", result.Output))

	})
	ginkgo.It("code navigate routes go extract body", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "pkg", "runner.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())

		body := "package pkg\n\nfunc Run() string {\n\tstep := \"first\"\n\treturn step + \" ok\"\n}\n"
		gomega.Expect(os.WriteFile(source, []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "extract_body",
			Path:       filepath.Join("pkg", "runner.go"),
			Pattern:    "Run",
		})
		gomega.Expect(result.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("tool = %q, want code_navigate", result.Tool))
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("code_navigate extract_body returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("go_extract_body"), fmt.Sprintf("expected Go extract-body route, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("routed_tool: go_extract_body"), fmt.Sprintf("expected Go extract-body route, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("return step + \" ok\""), fmt.Sprintf("expected full symbol body in output, got:\n%s", result.Output))

	})
	ginkgo.It("code navigate path language beats conflicting override", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "pkg", "runner.go")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "read_symbol",
			Path:       filepath.Join("pkg", "runner.go"),
			Language:   "python",
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("code_navigate returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("go_read_symbol"), fmt.Sprintf("expected path-derived Go route to beat python override, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("language: go"), fmt.Sprintf("expected path-derived Go route to beat python override, got summary=%q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("code navigate generic fallback for unregistered source", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

		source := filepath.Join(root, "scripts", "player.gd")
		gomega.Expect(os.MkdirAll(filepath.Dir(source), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(source, []byte("extends Node\n\nfunc _ready():\n\tpass\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "definition",
			Path:       filepath.Join("scripts", "player.gd"),
			Pattern:    "_ready",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("code_navigate fallback returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("generic fallback"), fmt.Sprintf("expected generic fallback, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("routed_tool: generic_source_text"), fmt.Sprintf("expected generic fallback, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("func _ready():"), fmt.Sprintf("expected fallback match in output, got:\n%s", result.Output))

	})
	ginkgo.It("go read symbol ignores max file bytes for targeted lines", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module example.com/target\n\ngo 1.22\n")

		var b strings.Builder

		b.WriteString("package test\n\n")

		for i := 0; i < 400; i++ {
			fmt.Fprintf(&b, "// filler %03d keeps the target beyond MaxFileBytes\n", i)
		}

		b.WriteString("func TargetSymbol() string {\n\treturn \"ok\"\n}\n")
		writeTestFile(filepath.Join(dir, "target.go"), b.String())

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 128})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_read_symbol", Pattern: "TargetSymbol"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_read_symbol returned error: %v", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("func TargetSymbol() string"), fmt.Sprintf("expected targeted symbol body beyond MaxFileBytes, got summary=%q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("go workspace symbol falls back after gopls failure", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "worker.go"), "package test\n\nfunc ExampleWorker() {}\n")

		gopls := filepath.Join(dir, "gopls")
		writeTestFile(gopls, "#!/bin/sh\necho simulated gopls failure >&2\nexit 1\n")
		gomega.Expect(os.Chmod(gopls, 0o755)).To(gomega.Succeed())
		ginkgo.GinkgoT().Setenv("PATH", dir)

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_workspace_symbol", Pattern: "ExampleWorker"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected semantic fallback, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("semantic fallback after gopls failure"), fmt.Sprintf("expected fallback summary, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("ExampleWorker"), fmt.Sprintf("expected fallback output to mention symbol, got %#v", result))

	})
	ginkgo.It("go workspace symbol falls back after empty gopls result", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "worker.go"), "package test\n\nfunc ExampleWorker() {}\n")

		gopls := filepath.Join(dir, "gopls")
		writeTestFile(gopls, "#!/bin/sh\nexit 0\n")
		gomega.Expect(os.Chmod(gopls, 0o755)).To(gomega.Succeed())
		ginkgo.GinkgoT().Setenv("PATH", dir)

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, CommandTimeoutSeconds: 5})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_workspace_symbol", Pattern: "func ExampleWorker"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected semantic fallback, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("semantic fallback after empty gopls result"), fmt.Sprintf("expected empty-result fallback summary, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("ExampleWorker"), fmt.Sprintf("expected fallback output to mention symbol, got %#v", result))

	})
	ginkgo.It("search content missing scope returns graceful miss", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal", "runtime", "agent", "runner.go"), "package agent\n\nfunc ExampleWorker() {}\n")

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "search_content", Pattern: "ExampleWorker", Path: "missing/runner.go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected graceful missing-scope miss, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Search scope not found"), fmt.Sprintf("expected missing-scope summary, got %#v", result))

	})
})

func testWorkspaceConfig() config.Workspace {
	ginkgo.GinkgoHelper()

	root := workspaceTestRepoRoot()

	return config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	}
}

var _ = ginkgo.Describe("manager", func() {
	ginkgo.It("instructions prefer structured narrowing", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})
		instructions := mgr.Instructions()

		for _, want := range []string{
			"Resolve a path-discovery capability, discovered handle, or structured symbol/code navigation route",
			"narrow with structured language-aware routes or discovered handles first",
			"either answer directly or take one exact evidence step tied to a named gap",
		} {
			gomega.Expect(instructions).To(gomega.ContainSubstring(want), fmt.Sprintf("expected instructions to contain %q, got:\n%s", want, instructions))

		}
		gomega.Expect(instructions).NotTo(gomega.ContainSubstring("search_files / search_content / read_file instead of repeating broad listings"), fmt.Sprintf("instructions should not prefer generic text tools after orientation:\n%s", instructions))

	})
	ginkgo.It("explore instructions prefer structured narrowing", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})
		gomega.Expect(mgr.SetMode(ModeExplore)).To(gomega.Succeed())

		instructions := mgr.Instructions()
		for _, want := range []string{
			"narrow with structured language-aware routes or discovered handles first",
			"Continue only for one exact missing claim and one exact probe",
		} {
			gomega.Expect(instructions).To(gomega.ContainSubstring(want), fmt.Sprintf("expected explore instructions to contain %q, got:\n%s", want, instructions))

		}
		gomega.Expect(instructions).NotTo(gomega.ContainSubstring("search_files / search_content / read_file on discovered handles"), fmt.Sprintf("explore instructions should not prefer generic text tools first:\n%s", instructions))

	})
	ginkgo.It("resolve read path missing file suggests structured discovery", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})

		_, _, err := mgr.resolveReadTarget(ToolRequest{Tool: "read_file", Path: "missing.go"})
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected missing read path error")

		msg := err.Error()
		gomega.Expect(msg).NotTo(gomega.ContainSubstring("list_files"), fmt.Sprintf("missing read path error must not prescribe list_files, got %q", msg))

		if count := strings.Count(msg, "Available recovery capabilities"); count != 1 {
			ginkgo.Fail(fmt.Sprintf("missing read path error should include one capability sentence, got %d in %q", count, msg))
		}

		for _, want := range []string{"file-name search", "structured symbol/code navigation", "exact text search"} {
			gomega.Expect(msg).To(gomega.ContainSubstring(want), fmt.Sprintf("expected missing path error to mention %q capability, got %q", want, msg))

		}
	})
	ginkgo.It("resolve read path missing file without name match uses generic capabilities", func() {
		dir := ginkgo.GinkgoT().TempDir()
		subdir := filepath.Join(dir, "notes")
		gomega.Expect(os.MkdirAll(subdir, 0o755)).To(gomega.Succeed())

		writeTestFile(filepath.Join(subdir, "alpha.txt"), "ok\n")

		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})

		_, _, err := mgr.resolveReadTarget(ToolRequest{Tool: "read_file", Path: "notes/zzzzzzzz.go"})
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected missing read path error")

		msg := err.Error()
		gomega.Expect(msg).NotTo(gomega.ContainSubstring("list_files"), fmt.Sprintf("missing read path helper must not prescribe list_files, got %q", msg))

		if count := strings.Count(msg, "Available recovery capabilities"); count != 1 {
			ginkgo.Fail(fmt.Sprintf("missing read path helper should include one capability sentence, got %d in %q", count, msg))
		}

		for _, want := range []string{"file-name search", "structured symbol/code navigation", "exact text search"} {
			gomega.Expect(msg).To(gomega.ContainSubstring(want), fmt.Sprintf("expected missing path helper to mention %q capability, got %q", want, msg))

		}
	})
	ginkgo.It("read file directory error uses generic capabilities", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "notes"), 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected read_file directory error, got %#v", result))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("list_files"), fmt.Sprintf("read_file directory error must not prescribe list_files, got %q", result.Error))

		if count := strings.Count(result.Error, "Available recovery capabilities"); count != 1 {
			ginkgo.Fail(fmt.Sprintf("read_file directory error should include one capability sentence, got %d in %q", count, result.Error))
		}

		for _, want := range []string{"file-name search", "structured symbol/code navigation", "exact text search"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected read_file directory error to mention %q capability, got %q", want, result.Error))

		}
	})
	ginkgo.It("read file missing parent error uses generic capabilities", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
			MaxFileBytes:        1 << 20,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/llm/llm.go"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected read_file missing-parent error, got %#v", result))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("list_files"), fmt.Sprintf("read_file missing-parent error must not prescribe list_files, got %q", result.Error))

		if count := strings.Count(result.Error, "Available recovery capabilities"); count != 1 {
			ginkgo.Fail(fmt.Sprintf("read_file missing-parent error should include one capability sentence, got %d in %q", count, result.Error))
		}

		for _, want := range []string{"file-name search", "structured symbol/code navigation", "exact text search"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected read_file missing-parent error to mention %q capability, got %q", want, result.Error))

		}
	})
	ginkgo.It("read file os missing error uses generic capabilities", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})

		missingPath, err := mgr.pathAccess.ResolvePath("stale.txt", "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve missing path: %v", err))

		mgr.rememberPath(missingPath)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "stale.txt"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected read_file os missing error, got %#v", result))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("list_files"), fmt.Sprintf("read_file os missing error must not prescribe list_files, got %q", result.Error))

		if count := strings.Count(result.Error, "Available recovery capabilities"); count != 1 {
			ginkgo.Fail(fmt.Sprintf("read_file os missing error should include one capability sentence, got %d in %q", count, result.Error))
		}

		for _, want := range []string{"file-name search", "structured symbol/code navigation", "exact text search"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected read_file os missing error to mention %q capability, got %q", want, result.Error))

		}
	})
	ginkgo.It("read file recovers unique normalized closest path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "internal", "evidence", "transcript"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "internal", "evidence", "transcript", "debuglog.go"), []byte("package transcript\n\nfunc DebugLog() {}\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "internal", "evidence", "transcript", "debuglog_test.go"), []byte("package transcript\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
			MaxFileBytes:        65536,
			MaxOutputBytes:      65536,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/evidence/transcript/debug_log.go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected normalized closest path recovery, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring(`Recovered missing read_file path "internal/evidence/transcript/debug_log.go" as "internal/evidence/transcript/debuglog.go"`), fmt.Sprintf("expected recovery summary to name requested and recovered paths, got %q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("func DebugLog()"), fmt.Sprintf("expected recovered source body, got %#v", result))

	})
	ginkgo.It("read file does not recover ambiguous normalized closest path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "internal", "evidence", "transcript"), 0o755)).To(gomega.Succeed())

		for _, name := range []string{"debuglog.go", "debug-log.go"} {
			gomega.Expect(os.WriteFile(filepath.Join(dir, "internal", "evidence", "transcript", name), []byte("package transcript\n"), 0o644)).To(gomega.Succeed())

		}

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
			MaxFileBytes:        65536,
			MaxOutputBytes:      65536,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/evidence/transcript/debug_log.go"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("ambiguous normalized matches must remain model-visible suggestions, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("Did you mean"), fmt.Sprintf("expected suggestions to remain available, got %#v", result))

	})
	ginkgo.It("read file does not recover closest path from different parent or extension", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "internal", "evidence", "transcript"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "internal", "other"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "internal", "other", "debuglog.go"), []byte("package other\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "internal", "evidence", "transcript", "debuglog.md"), []byte("# debug\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
			MaxFileBytes:        65536,
			MaxOutputBytes:      65536,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/evidence/transcript/debug_log.go"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("different parent or extension must not be auto-read, got %#v", result))
		gomega.Expect(result.Summary).NotTo(gomega.ContainSubstring("Recovered missing read_file path"), fmt.Sprintf("unexpected recovery summary: %#v", result))

	})
	ginkgo.It("resolve read target prefers explicit path over stale handle", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "wanted.txt"), []byte("wanted\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
		mgr.PrimeDiscoveredPaths([]string{"old.txt"})

		oldPath, err := mgr.ResolveHandlePath("f1")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResolveHandlePath f1: %v", err))
		gomega.Expect(filepath.Base(oldPath)).To(gomega.Equal("old.txt"), fmt.Sprintf("test setup expected f1 to point at old.txt, got %q", oldPath))

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", ID: "f1", Path: "wanted.txt"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file should prefer explicit path over stale handle, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("wanted"), fmt.Sprintf("read_file should read explicit path content, got %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("old"), fmt.Sprintf("read_file should read explicit path content, got %#v", result))

	})
	ginkgo.It("execute read write and explore write rejection", func() {
		dir := ginkgo.GinkgoT().TempDir()
		cfg := config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			Shell:                 "/bin/sh",
			CommandTimeoutSeconds: 5,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   100,
			EnableShell:           true,
			EnableWrites:          true,
		}
		mgr := NewManager(cfg)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "write_file", Path: "notes/todo.txt", Content: "hello\nworld\n"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("write_file failed: %#v", result))

		if data, err := os.ReadFile(filepath.Join(dir, "notes", "todo.txt")); err != nil || string(data) != "hello\nworld\n" {
			ginkgo.Fail(fmt.Sprintf("unexpected written file data=%q err=%v", string(data), err))
		}

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("expected exact existing raw path read to auto-discover, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("2:world"), fmt.Sprintf("expected exact existing raw path read to auto-discover, got %#v", result))

		mgr.PrimeDiscoveredPaths([]string{"notes/todo.txt"})

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("read_file did not return requested line: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("2:world"), fmt.Sprintf("read_file did not return requested line: %#v", result))
		gomega.Expect(mgr.SetMode(ModeExplore)).To(gomega.Succeed())

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "write_file", Path: "blocked.txt", Content: "nope"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected explore write rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("explore mode is read-only"), fmt.Sprintf("expected explore write rejection, got %#v", result))

	})
	ginkgo.It("read file line count matches submit plan line range", func() {
		dir := ginkgo.GinkgoT().TempDir()
		body := "package sample\n\nfunc Value() int {\n\treturn 1\n}\n"
		gomega.Expect(os.WriteFile(filepath.Join(dir, "value.go"), []byte(body), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
		read := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "value.go"})
		gomega.Expect(read.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", read))
		gomega.Expect(read.Summary).To(gomega.ContainSubstring("total lines: 5"), fmt.Sprintf("read_file should report editable line spans without a synthetic trailing blank, got summary=%q output=\n%s", read.Summary, read.Output))
		gomega.Expect(read.Output).NotTo(gomega.ContainSubstring("\n6:"), fmt.Sprintf("read_file should report editable line spans without a synthetic trailing blank, got summary=%q output=\n%s", read.Summary, read.Output))

		plan := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "update value",
			Edits: []planartifact.PlanEdit{{
				Path:      "value.go",
				StartLine: 3,
				EndLine:   5,
				Content:   "func Value() int {\n\treturn 2\n}\n",
				Purpose:   "update returned value",
			}},
		})
		gomega.Expect(plan.Error).To(gomega.Equal(""), fmt.Sprintf("submit_plan should accept the line range advertised by read_file, got: %s", plan.Error))
		gomega.Expect(os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o644)).To(gomega.Succeed())

		empty := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "empty.txt"})
		gomega.Expect(empty.Error).To(gomega.Equal(""), fmt.Sprintf("empty read_file failed: %#v", empty))
		gomega.Expect(empty.Summary).To(gomega.ContainSubstring("total lines: 0"), fmt.Sprintf("empty read_file should not advertise editable line 1, got summary=%q output=%q", empty.Summary, empty.Output))
		gomega.Expect(empty.Output).NotTo(gomega.ContainSubstring("1:"), fmt.Sprintf("empty read_file should not advertise editable line 1, got summary=%q output=%q", empty.Summary, empty.Output))

	})
	ginkgo.It("unsupported tool error suggests closest names", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})

		// Typo of "edit_file" — error should suggest the right name back.
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "edit_files",
			Path: "anything.md",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected unsupported-tool error, got %#v", result))

		for _, want := range []string{"unsupported tool", "edit_file"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected error to contain %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("not found recovery hint mentions parent entries", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		})
		// Create a real file in a subdir so the hint has something to suggest.
		subdir := filepath.Join(dir, "notes")
		gomega.Expect(os.MkdirAll(subdir, 0o755)).To(gomega.Succeed())

		writeTestFile(filepath.Join(subdir, "real.md"), "ok\n")

		// Ask to read a NEAR-MISS path — error should mention the real file.
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "read_file",
			Path: "notes/reel.md", // typo of real.md
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected read_file not-found error, got %#v", result))

		for _, want := range []string{"was not found", "real.md"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected error to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.Describe("edit file rejects empty content line range", func() {
		for _, tc := range []struct {
			name    string
			content string
		}{
			{name: "empty", content: ""},
			{name: "newline", content: "\n"},
			{name: "spaces", content: "   \n"},
		} {
			ginkgo.It(tc.name, func() {
				dir := ginkgo.GinkgoT().TempDir()
				path := filepath.Join(dir, "notes.md")
				original := "line one\nline two\nline three\n"
				writeTestFile(path, original)

				mgr := NewManager(config.Workspace{
					Root:         dir,
					CWD:          dir,
					EnableWrites: true,
					MaxFileBytes: 1 << 20,
				})
				mgr.MarkPathObserved("notes.md")

				// Most common edit_file footgun: model intends "insert after line N"
				// but uses the line-range form with blank content, which actually
				// REPLACES (and thus deletes) lines start-end. The guard should catch
				// this and direct the model to the substring-anchored form.
				result := mgr.Execute(context.Background(), ToolRequest{
					Tool:      "edit_file",
					Path:      "notes.md",
					StartLine: 2,
					EndLine:   2,
					Content:   tc.content,
				})
				gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected guard to reject blank-content line-range edit, got %#v", result))

				for _, want := range []string{"would DELETE", "old_text", "new_text"} {
					gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected guard error to mention %q, got: %s", want, result.Error))

				}

				if got := readTestFile(path); got != original {
					ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
				}
			})
		}
	})
	ginkgo.It("edit file content without anchor recovery prefers bounded source edits", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "main.go")
		original := "package main\n\nfunc main() {}\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("main.go")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:        "edit_file",
			Path:        "main.go",
			Content:     "package main\n\nfunc main() { println(\"ok\") }\n",
			Description: "update main behavior",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected malformed edit_file to be rejected"))

		for _, want := range []string{"bounded edit_file shape", "concrete description", "old_text", "start_line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected source recovery to mention %q, got:\n%s", want, result.Error))

		}
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("use write_file instead"), fmt.Sprintf("source recovery should not redirect to write_file:\n%s", result.Error))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("REPLACE THE WHOLE FILE"), fmt.Sprintf("source recovery should not redirect to write_file:\n%s", result.Error))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not mutate file\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file content without anchor recovery keeps whole file option for docs", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "notes.md")
		writeTestFile(path, "# Notes\n")

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("notes.md")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:        "edit_file",
			Path:        "notes.md",
			Content:     "# Updated\n",
			Description: "update notes",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected malformed edit_file to be rejected"))

		for _, want := range []string{"write_file", "concrete description", "missing/new file or non-source deliverable"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected doc recovery to mention %q, got:\n%s", want, result.Error))

		}
		for _, forbidden := range []string{"new, small", "REPLACE THE WHOLE FILE"} {
			gomega.Expect(result.Error).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("expected doc recovery to avoid stale whole-file guidance %q, got:\n%s", forbidden, result.Error))

		}
	})
	ginkgo.It("format tool result for model compacts context pack output", func() {
		largeBody := strings.Repeat(`{"path":"pkg/extra.go","reason":"extra candidate"},`, 200)
		result := ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/abc.json",
			Output: `{
			"pack_id":"abc",
			"artifact_path":".idiom/comprehension/views/abc.json",
			"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/workflows/productworkflow/coverage_gate_test.go"],"reasons":["related_evidence_without_source_owner"]},
			"candidate_files":[
				{"path":"internal/workflows/productworkflow/coverage_gate.go","role":"editable","reason":"coverage gate implementation"},
				{"path":"cmd/idiom/product_workflow.go","role":"editable","reason":"product tdd command wiring"},
				` + largeBody + `
				{"path":"pkg/final.go","reason":"tail"}
			],
			"candidate_symbols":[{"qualified_name":"EvaluateCoverage","path":"internal/workflows/productworkflow/coverage_gate.go","start_line":42,"end_line":90}],
			"tests":[{"command":"go test ./internal/workflows/productworkflow","path":"internal/workflows/productworkflow/coverage_gate_test.go","reason":"coverage gate regression tests"}]
		}`,
		}

		rendered := FormatToolResultForModel(result)
		for _, want := range []string{
			"context_pack_summary:",
			"pack_id: abc",
			"artifact_path: .idiom/comprehension/views/abc.json",
			"fresh: false",
			"packet_quality:",
			"localization_confidence: low",
			"source_owner_found: false",
			"internal/workflows/productworkflow/coverage_gate_test.go",
			"internal/workflows/productworkflow/coverage_gate.go",
			"cmd/idiom/product_workflow.go",
			"candidate_symbols:",
			"EvaluateCoverage",
			"go test ./internal/workflows/productworkflow",
			"tests:",
			"evidence_note: context_pack is localization evidence, not source-body evidence.",
		} {
			gomega.Expect(rendered).To(gomega.ContainSubstring(want), fmt.Sprintf("compacted context_pack result missing %q:\n%s", want, rendered))

		}
		// Without budget_tokens, all candidates are rendered (unbounded path).
		// The full largeBody must not leak as raw JSON, but individual entries
		// are present.
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring(largeBody), fmt.Sprintf("context_pack raw JSON leaked into compact output, len=%d", len(rendered)))

	})
	ginkgo.It("format tool result for model context pack budget enforcement", func() {
		// Build a result with many more files than budget allows, so the
		// renderer must truncate and report omitted counts.
		largeBody := strings.Repeat(`{"path":"pkg/extra.go","reason":"extra candidate"},`, 200)
		result := ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack with budget",
			Output: `{
			"pack_id":"budget-test",
			"artifact_path":".idiom/comprehension/views/budget.json",
			"budget_tokens":200,
			"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["pkg/main.go"],"reasons":["found_source_owner"]},
			"candidate_files":[
				{"path":"pkg/core.go","role":"editable","reason":"core logic"},
				` + largeBody + `
				{"path":"pkg/last.go","reason":"last"}
			],
			"candidate_symbols":[
				{"qualified_name":"Handle","path":"pkg/core.go","start_line":10}
			],
			"tests":[
				{"command":"go test ./pkg/...","path":"pkg/core_test.go","reason":"unit tests"}
			]
		}`,
		}

		rendered := FormatToolResultForModel(result)

		// Mandatory content must always be present regardless of budget.
		for _, want := range []string{
			"context_pack_summary:",
			"pack_id: budget-test",
			"fresh: false",
			"packet_quality:",
			"localization_confidence: high",
			"source_owner_found: true",
			"evidence_note: context_pack is localization evidence, not source-body evidence.",
		} {
			gomega.Expect(rendered).To(gomega.ContainSubstring(want), fmt.Sprintf("budget-compacted result missing %q:\n%s", want, rendered))

		}
		// Quality evidence paths must be present.
		gomega.Expect(rendered).To(gomega.ContainSubstring("pkg/main.go"), fmt.Sprintf("budget-compacted result missing quality evidence path:\n%s", rendered))

		// Budget enforcement: omitted counts must appear.
		gomega.Expect(rendered).To(gomega.ContainSubstring("omitted:"), fmt.Sprintf("budget-compacted result missing omitted section:\n%s", rendered))

		// The raw JSON must never spill into compact output.
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring(largeBody), fmt.Sprintf("raw largeBody leaked into budget-compacted output"))

		// With budget=200, most files should be omitted.
		gomega.Expect(rendered).To(gomega.ContainSubstring("omitted (rendered "), fmt.Sprintf("budget-compacted output missing omitted detail:\n%s", rendered))

		// Total output should be well under a generous bound for budget=200.
		gomega.Expect(len(rendered) > 2000).To(gomega.BeFalse(), fmt.Sprintf("budget-compacted output too large for budget=200, len=%d:\n%s", len(rendered), rendered))

	})
	ginkgo.It("edit file line range preserves trailing line boundary", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "notes.md")
		original := "line one\nline two\nline three\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("notes.md")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:        "edit_file",
			Path:        "notes.md",
			StartLine:   2,
			EndLine:     2,
			Content:     "replacement",
			Description: "replace one line without merging the following line",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("unexpected edit_file error: %#v", result))

		want := "line one\nreplacement\nline three\n"
		if got := readTestFile(path); got != want {
			ginkgo.Fail(fmt.Sprintf("line-range edit should preserve line boundary:\nwant %q\ngot  %q", want, got))
		}
	})
	ginkgo.It("edit file rejects line range with duplicated preceding context", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "notes.md")
		original := "setup one\nsetup two\nsetup three\n}\n\n}  // SUITE\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("notes.md")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "edit_file",
			Path:      "notes.md",
			StartLine: 4,
			EndLine:   6,
			Content: "setup one\nsetup two\nsetup three\n}\n\n" +
				"new case\n}\n\n}  // SUITE\n",
			Description: "insert a new case before the suite close",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected duplicated-context line-range edit to be rejected, got %#v", result))

		for _, want := range []string{"duplicate context", "old_text", "new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected guard error to mention %q, got: %s", want, result.Error))

		}

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file prefers exact replacement when line range metadata is also present", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "notes.md")
		original := "line one\nline two\nline three\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("notes.md")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "edit_file",
			Path:      "notes.md",
			StartLine: 2,
			EndLine:   2,
			OldText:   "line two\n",
			NewText:   "replacement two\n",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected exact replacement to win over line metadata, got: %s", result.Error))

		want := "line one\nreplacement two\nline three\n"
		if got := readTestFile(path); got != want {
			ginkgo.Fail(fmt.Sprintf("unexpected edited file\ngot:\n%s\nwant:\n%s", got, want))
		}
	})
	ginkgo.It("edit file rejects line range with new text but no old text", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "notes.md")
		original := "line one\nline two\nline three\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("notes.md")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "edit_file",
			Path:      "notes.md",
			StartLine: 2,
			EndLine:   2,
			NewText:   "replacement two\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected ambiguous line-range/new_text shape to be rejected"))

		for _, want := range []string{"content", "new_text", "old_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected error to mention %q, got: %s", want, result.Error))

		}

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file rejects invalid go before write", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "analysis_agents.go")
		original := "package app\n\nfunc ok() {}\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("analysis_agents.go")

		// Non-blank invalid Go content should bypass the blank-range deletion
		// guard and reach syntax preflight.
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "edit_file",
			Path:      "analysis_agents.go",
			StartLine: 1,
			EndLine:   1,
			Content:   "package\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected Go syntax preflight rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("preflight syntax validation failed"), fmt.Sprintf("expected Go syntax preflight rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("planned content around syntax error"), fmt.Sprintf("expected syntax error context, got %#v", result))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("invalid edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file reports case folded anchor mismatch", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "native_test.cpp")
		original := "TEST_CASE(\"CartSystem recovers route loss\") {\n    CHECK(true);\n}\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("native_test.cpp")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "native_test.cpp",
			OldText: "test_case(\"cartsystem recovers route loss\") {",
			NewText: "TEST_CASE(\"CartSystem recovers route loss\") {\n    CHECK_FALSE(false);",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected case-folded anchor guidance, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("case-insensitive match exists"), fmt.Sprintf("expected case-folded anchor guidance, got %#v", result))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file rejects truncated search anchor", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "catalog.go")
		original := "package main\n\nvar catalog = \"needle " + strings.Repeat("x", 800) + "\"\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("catalog.go")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "catalog.go",
			OldText: "var catalog = \"needle " + strings.Repeat("x", 120) + lineTruncationMarker,
			NewText: "var catalog = \"replacement\"\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected truncated search anchor to be rejected"))

		for _, want := range []string{"truncation marker", "search_content", "read_file", "start_line/end_line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected truncated-anchor error to mention %q, got %q", want, result.Error))

		}

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file reports ambiguous old text occurrence lines", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "matrix.cpp")
		original := "set_floor(30.0f);\nother();\nset_floor(30.0f);\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("matrix.cpp")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "matrix.cpp",
			OldText: "set_floor(30.0f);",
			NewText: "floor = 30.0f;",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected ambiguous old_text to be rejected"))

		for _, want := range []string{"matched 2 locations", "start_line/end_line", "replace_all=true"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected ambiguous-anchor error to mention %q, got %q", want, result.Error))

		}

		for _, want := range []string{"matching start line(s): 1, 3", "read_file", "matrix.cpp"} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("expected ambiguous-anchor output to mention %q, got %q", want, result.Output))

		}

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file requires evidence after syntax preflight failure", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "analysis_agents.go")
		original := "package app\n\nfunc ok() {}\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
			MaxFileBytes: 1 << 20,
		})
		mgr.MarkPathObserved("analysis_agents.go")

		bad := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "edit_file",
			Path:      "analysis_agents.go",
			StartLine: 1,
			EndLine:   1,
			Content:   "package\n",
		})
		gomega.Expect(bad.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected Go syntax preflight rejection, got %#v", bad))
		gomega.Expect(bad.Error).To(gomega.ContainSubstring("preflight syntax validation failed"), fmt.Sprintf("expected Go syntax preflight rejection, got %#v", bad))

		blocked := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "analysis_agents.go",
			OldText: "func ok() {}",
			NewText: "func okRenamed() {}",
		})
		gomega.Expect(blocked.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected recovery guard before next edit, got %#v", blocked))
		gomega.Expect(blocked.Error).To(gomega.ContainSubstring("previous syntax preflight failure"), fmt.Sprintf("expected recovery guard before next edit, got %#v", blocked))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("blocked edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}

		read := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "read_file",
			Path:      "analysis_agents.go",
			StartLine: 1,
			EndLine:   3,
		})
		gomega.Expect(read.Error).To(gomega.Equal(""), fmt.Sprintf("expected read_file to refresh evidence, got %#v", read))

		fixed := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "analysis_agents.go",
			OldText: "func ok() {}",
			NewText: "func okRenamed() {}",
		})
		gomega.Expect(fixed.Error).To(gomega.Equal(""), fmt.Sprintf("expected edit after evidence refresh to succeed, got %#v", fixed))

		if got := readTestFile(path); got != "package app\n\nfunc okRenamed() {}\n" {
			ginkgo.Fail(fmt.Sprintf("unexpected edited content:\n%s", got))
		}
	})
	ginkgo.It("write file and append reject invalid structured content", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "write_file",
			Path:    "bad.go",
			Content: "import \"fmt\"\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected invalid Go write rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("preflight syntax validation failed"), fmt.Sprintf("expected invalid Go write rejection, got %#v", result))

		if _, err := os.Stat(filepath.Join(dir, "bad.go")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("invalid Go write should not create file, stat err=%v", err))
		}

		jsonPath := filepath.Join(dir, "config.json")
		original := "{\"ok\":true}\n"
		writeTestFile(jsonPath, original)

		result = mgr.Execute(context.Background(), ToolRequest{
			Tool:    "append_file",
			Path:    "config.json",
			Content: "not json",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected invalid JSON append rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("preflight JSON validation failed"), fmt.Sprintf("expected invalid JSON append rejection, got %#v", result))

		if got := readTestFile(jsonPath); got != original {
			ginkgo.Fail(fmt.Sprintf("invalid append should not touch disk\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("append file preserves concurrent appends", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		const count = 64
		var wg sync.WaitGroup
		errs := make(chan string, count)

		for i := 0; i < count; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				result := mgr.Execute(context.Background(), ToolRequest{
					Tool:    ToolNameAppendFile,
					Path:    "events.log",
					Content: fmt.Sprintf("line-%02d\n", i),
				})
				if result.Error != "" {
					errs <- result.Error
				}
			}()
		}

		wg.Wait()
		close(errs)
		for err := range errs {
			ginkgo.Fail(fmt.Sprintf("append_file failed: %s", err))
		}

		lines := strings.Split(strings.TrimSpace(readTestFile(filepath.Join(dir, "events.log"))), "\n")
		gomega.Expect(lines).To(gomega.HaveLen(count), fmt.Sprintf("line count = %d, want %d; lines=%q", len(lines), count, lines))

		seen := map[string]bool{}
		for _, line := range lines {
			gomega.Expect(seen[line]).To(gomega.BeFalse(), fmt.Sprintf("duplicate appended line %q in %q", line, lines))

			seen[line] = true
		}
		for i := 0; i < count; i++ {
			line := fmt.Sprintf("line-%02d", i)
			gomega.Expect(seen[line]).To(gomega.BeTrue(), fmt.Sprintf("missing appended line %q in %q", line, lines))

		}
	})
	ginkgo.It("write file rejects completed planning artifact overwrite", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, ".planning", "atoms", "atom-a.md")
		original := "# Atom A\n\n**Status**: complete\n\nExisting accepted spec.\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "write_file",
			Path:    ".planning/atoms/atom-a.md",
			Content: "# Atom A\n\nReplacement draft.\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected completed artifact overwrite rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("completed planning artifact"), fmt.Sprintf("expected completed artifact overwrite rejection, got %#v", result))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("completed artifact should remain unchanged\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("edit file rejects new adjacent duplicate assignment", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "sim.cpp")
		original := "void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n"
		writeTestFile(path, original)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})
		mgr.MarkPathObserved("sim.cpp")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "sim.cpp",
			OldText: "    c.cultural_work_mult = 1.0f;\n",
			NewText: "    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected adjacent duplicate rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("adjacent duplicate code line"), fmt.Sprintf("expected adjacent duplicate rejection, got %#v", result))

		if got := readTestFile(path); got != original {
			ginkgo.Fail(fmt.Sprintf("duplicate rejection should leave file unchanged\ngot:\n%s\nwant:\n%s", got, original))
		}
	})
	ginkgo.It("shell rejects zero selected doctest command", func() {
		dir := ginkgo.GinkgoT().TempDir()
		script := filepath.Join(dir, "zero_tests.sh")
		writeTestFile(script, "#!/bin/sh\nprintf 'test cases: 0 | 0 passed | 0 failed\\n'\nexit 0\n")
		gomega.Expect(os.Chmod(script, 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			Shell:                 "/bin/sh",
			CommandTimeoutSeconds: 5,
			MaxOutputBytes:        65536,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "./zero_tests.sh -tc=Missing"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected zero-selected test rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("selected zero tests"), fmt.Sprintf("expected zero-selected test rejection, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("test cases: 0"), fmt.Sprintf("shell output should preserve command output for debugging, got %#v", result))

		mgr.cfg.MaxOutputBytes = 8

		truncated := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "./zero_tests.sh -tc=Missing"})
		gomega.Expect(truncated.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("zero-test detection must use raw output before truncation, got %#v", truncated))
		gomega.Expect(truncated.Error).To(gomega.ContainSubstring("selected zero tests"), fmt.Sprintf("zero-test detection must use raw output before truncation, got %#v", truncated))
		gomega.Expect(truncated.Output).To(gomega.ContainSubstring("[truncated]"), fmt.Sprintf("small output cap should still truncate displayed output, got %#v", truncated))

		goNoTests := filepath.Join(dir, "go_test_none.sh")
		writeTestFile(goNoTests, "#!/bin/sh\nprintf 'testing: warning: no tests to run\\nPASS\\n'\nexit 0\n")
		gomega.Expect(os.Chmod(goNoTests, 0o755)).To(gomega.Succeed())
		mgr.cfg.MaxOutputBytes = 65536
		noTests := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "./go_test_none.sh"})
		gomega.Expect(noTests.Error).To(gomega.ContainSubstring("selected zero tests"), fmt.Sprintf("Go no-tests-to-run signal must reject successful shell result, got %#v", noTests))

		control := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "printf 'test cases: 0 | 0 passed | 0 failed\\n'"})
		gomega.Expect(control.Error).To(gomega.Equal(""), fmt.Sprintf("non-test command with matching text should not be reclassified, got %#v", control))

	})
	ginkgo.It("go find function is implemented", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "widget.go"), `package sample

func helper() {}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_function", Pattern: "helper"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_find_function failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.Or(
			gomega.ContainSubstring("func helper"),
			gomega.ContainSubstring("function helper"),
		), fmt.Sprintf("expected function match in output, got:\n%s", result.Output))

	})
	ginkgo.It("go file outline reports declarations", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "widget.go"), `package sample

import "fmt"

type Model struct{}

func (m *Model) View() string {
	return fmt.Sprint("ok")
}

func helper() {}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_file_outline", Path: "widget.go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_file_outline failed: %#v", result))
		gomega.Expect(result.Summary).NotTo(gomega.ContainSubstring("go_move_declarations"), fmt.Sprintf("outline summary should stay neutral for explanatory flows, got: %s", result.Summary))

		for _, want := range []string{"Model.View", "helper", "imports:", "tags: rendering"} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("expected outline to contain %q, got:\n%s", want, result.Output))

		}
	})
	ginkgo.It("go file outline skips same file move recommendations", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "analysis_agents.go"), `package app

func analysisEvidence() {}

func renderView() {}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_file_outline", Path: "analysis_agents.go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_file_outline failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("destination: analysis_agents.go"), fmt.Sprintf("outline should not recommend moving declarations back into the source file:\nsummary: %s\noutput:\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).NotTo(gomega.ContainSubstring(`destination:"analysis_agents.go"`), fmt.Sprintf("outline should not recommend moving declarations back into the source file:\nsummary: %s\noutput:\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("destination: rendering.go"), fmt.Sprintf("expected useful cross-file move recommendation to remain, got:\n%s", result.Output))

	})
	ginkgo.It("go move declarations moves selected blocks and repairs imports", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "source.go"), `package sample

import (
	"fmt"
	"strings"
)

type Model struct{}

func (m *Model) View() string {
	return fmt.Sprint("ok")
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:         "go_move_declarations",
			Path:         "source.go",
			Destination:  "rendering.go",
			Declarations: []string{"Model.View"},
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_move_declarations failed: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.HaveLen(2), fmt.Sprintf("go_move_declarations changed paths = %#v", result.ChangedPaths))
		gomega.Expect(result.ChangedPaths[0]).To(gomega.Equal("source.go"), fmt.Sprintf("go_move_declarations changed paths = %#v", result.ChangedPaths))
		gomega.Expect(result.ChangedPaths[1]).To(gomega.Equal("rendering.go"), fmt.Sprintf("go_move_declarations changed paths = %#v", result.ChangedPaths))

		source := readTestFile(filepath.Join(dir, "source.go"))
		dest := readTestFile(filepath.Join(dir, "rendering.go"))
		gomega.Expect(source).NotTo(gomega.ContainSubstring("func (m *Model) View"), fmt.Sprintf("source still contains moved method:\n%s", source))
		gomega.Expect(source).To(gomega.ContainSubstring(`"strings"`), fmt.Sprintf("source imports were not repaired:\n%s", source))
		gomega.Expect(source).NotTo(gomega.ContainSubstring(`"fmt"`), fmt.Sprintf("source imports were not repaired:\n%s", source))
		gomega.Expect(dest).To(gomega.ContainSubstring("func (m *Model) View"), fmt.Sprintf("destination missing moved method or import:\n%s", dest))
		gomega.Expect(dest).To(gomega.ContainSubstring(`"fmt"`), fmt.Sprintf("destination missing moved method or import:\n%s", dest))
		gomega.Expect(dest).NotTo(gomega.ContainSubstring(`"strings"`), fmt.Sprintf("destination retained unused import:\n%s", dest))

	})
	ginkgo.It("go move declarations accepts responsibility tag pattern", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "source.go"), `package sample

import "fmt"

func renderView() string {
	return fmt.Sprint("ok")
}

func coreValue() int {
	return 1
}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:        "go_move_declarations",
			Path:        "source.go",
			Destination: "rendering.go",
			Pattern:     "tag:rendering",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_move_declarations tag pattern failed: %#v", result))

		source := readTestFile(filepath.Join(dir, "source.go"))
		dest := readTestFile(filepath.Join(dir, "rendering.go"))
		gomega.Expect(source).NotTo(gomega.ContainSubstring("renderView"), fmt.Sprintf("tag move did not move rendering declaration\nsource:\n%s\ndest:\n%s", source, dest))
		gomega.Expect(dest).To(gomega.ContainSubstring("renderView"), fmt.Sprintf("tag move did not move rendering declaration\nsource:\n%s\ndest:\n%s", source, dest))
		gomega.Expect(source).To(gomega.ContainSubstring("coreValue"), fmt.Sprintf("tag move should leave core declaration in source:\n%s", source))

	})
	ginkgo.It("go move declarations accepts batch moves", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "source.go"), `package sample

import (
	"fmt"
	"strings"
)

func renderView() string {
	return fmt.Sprint("ok")
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}

func coreValue() int {
	return 1
}
`)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "go_move_declarations",
			Path: "source.go",
			Moves: []GoDeclarationMove{
				{Destination: "rendering.go", Pattern: "tag:rendering"},
				{Destination: "task_memory.go", Declarations: []string{"normalize"}},
			},
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("go_move_declarations batch failed: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.HaveLen(3), fmt.Sprintf("go_move_declarations batch changed paths = %#v", result.ChangedPaths))
		gomega.Expect(result.ChangedPaths[0]).To(gomega.Equal("rendering.go"), fmt.Sprintf("go_move_declarations batch changed paths = %#v", result.ChangedPaths))
		gomega.Expect(result.ChangedPaths[1]).To(gomega.Equal("source.go"), fmt.Sprintf("go_move_declarations batch changed paths = %#v", result.ChangedPaths))
		gomega.Expect(result.ChangedPaths[2]).To(gomega.Equal("task_memory.go"), fmt.Sprintf("go_move_declarations batch changed paths = %#v", result.ChangedPaths))

		if source := readTestFile(filepath.Join(dir, "source.go")); strings.Contains(source, "renderView") || strings.Contains(source, "normalize") || !strings.Contains(source, "coreValue") {
			ginkgo.Fail(fmt.Sprintf("batch move left unexpected source content:\n%s", source))
		}

		if dest := readTestFile(filepath.Join(dir, "rendering.go")); !strings.Contains(dest, "renderView") {
			ginkgo.Fail(fmt.Sprintf("rendering destination missing moved declaration:\n%s", dest))
		}

		if dest := readTestFile(filepath.Join(dir, "task_memory.go")); !strings.Contains(dest, "normalize") {
			ginkgo.Fail(fmt.Sprintf("task memory destination missing moved declaration:\n%s", dest))
		}
	})
	ginkgo.It("list files skips noisy dirs from root listing", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for _, path := range []string{
			".git/config",
			".cache/model.bin",
			"internal/surfaces/tui/app/app.go",
			"README.md",
		} {
			full := filepath.Join(dir, path)
			gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(full, []byte("x\n"), 0o644)).To(gomega.Succeed())

		}

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
			EnableShell:         true,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: ".", Recursive: true, MaxDepth: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("list_files failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(".git/"), fmt.Sprintf("root listing should skip noisy directories, got:\n%s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(".cache/"), fmt.Sprintf("root listing should skip noisy directories, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/surfaces/tui/app/app.go"), fmt.Sprintf("expected source path in root listing, got:\n%s", result.Output))

	})
	ginkgo.Describe("shell rejects directory only commands", func() {
		newManager := func() *Manager {
			dir := ginkgo.GinkgoT().TempDir()
			return NewManager(config.Workspace{
				Root:                  dir,
				CWD:                   dir,
				Shell:                 "/bin/sh",
				CommandTimeoutSeconds: 5,
				EnableShell:           true,
			})
		}
		for _, command := range []string{"cd", "cd subdir", "cd subdir && cd .."} {
			ginkgo.It(command, func() {
				mgr := newManager()
				result := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: command})
				gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected directory-only shell rejection, got %#v", result))
				gomega.Expect(result.Error).To(gomega.ContainSubstring("do not persist"), fmt.Sprintf("expected directory-only shell rejection, got %#v", result))

			})
		}

		ginkgo.It("allows a directory change followed by a command", func() {
			result := newManager().Execute(context.Background(), ToolRequest{Tool: "shell", Command: "cd . && pwd"})
			gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected cd plus command to remain allowed, got %#v", result))
		})

	})
	ginkgo.It("context trace pack uses shell admission policy", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			Shell:                 "/bin/sh",
			CommandTimeoutSeconds: 5,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameContextTracePack, Command: "cd ."})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected context_trace_pack to reject directory-only shell command, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("do not persist"), fmt.Sprintf("expected context_trace_pack to reject directory-only shell command, got %#v", result))

	})
})

func writeTestFile(path string, content string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

func readTestFile(path string) string {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadFile(%q): %v", path, err))

	return string(data)
}

var _ = ginkgo.Describe("manager", func() {
	ginkgo.It("search files skips noisy dirs by default but allows explicit path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, ".claude", "worktrees", "HiddenWorktreeWidget.go"), "package hidden\n")
		writeTestFile(filepath.Join(dir, "internal", "surfaces", "tui", "app", "app.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "HiddenWorktree",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(".claude"), fmt.Sprintf("default search_files should skip .claude worktrees, got:\n%s", result.Output))

		explicit := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "HiddenWorktree",
			Path:    ".claude",
		})
		gomega.Expect(explicit.Error).To(gomega.Equal(""), fmt.Sprintf("explicit search_files failed: %#v", explicit))
		gomega.Expect(explicit.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("search_files should publish source-owner discovery facts, got %#v", explicit.Facts))
		gomega.Expect(explicit.Facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthSourceOwner), fmt.Sprintf("search_files should publish source-owner discovery facts, got %#v", explicit.Facts))
		gomega.Expect(explicit.Facts.SourceOwnerFound).To(gomega.BeTrue(), fmt.Sprintf("search_files should publish source-owner discovery facts, got %#v", explicit.Facts))
		gomega.Expect(slices.Contains(explicit.Facts.CandidatePaths, ".claude/worktrees/HiddenWorktreeWidget.go")).To(gomega.BeTrue(), fmt.Sprintf("search_files facts missing displayed candidate path: %#v", explicit.Facts.CandidatePaths))
		gomega.Expect(explicit.Output).To(gomega.ContainSubstring(".claude/worktrees/HiddenWorktreeWidget.go"), fmt.Sprintf("explicit .claude search should still work, got:\n%s", explicit.Output))

	})
	ginkgo.It("search files allows explicit planning dot dir path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, ".planning", "qwen-rename", "rca-brief.md"), "failure analysis\n")
		writeTestFile(filepath.Join(dir, "internal", "surfaces", "tui", "app", "app.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "rca-brief",
			Path:    ".planning",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("explicit .planning search failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(".planning/qwen-rename/rca-brief.md"), fmt.Sprintf("explicit .planning search should include nested files, got:\n%s", result.Output))

	})
	ginkgo.It("search files includes logs for log intent", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "logs", "llama-server.log"), "failed to bind\n")
		writeTestFile(filepath.Join(dir, "internal", "surfaces", "tui", "app", "app.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "*.log",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("logs/llama-server.log"), fmt.Sprintf("log-shaped search should include logs directory, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("modified "), fmt.Sprintf("log-shaped search should include recency metadata, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("size "), fmt.Sprintf("log-shaped search should include recency metadata, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("newest first"), fmt.Sprintf("log-shaped search should include recency metadata, summary=%q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("search files accepts glob when pattern omitted", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal", "tui", "chat", "input.go"), "package chat\n")
		writeTestFile(filepath.Join(dir, "tui", "chat", "root_input.go"), "package chat\n")
		writeTestFile(filepath.Join(dir, "internal", "runtime", "agent", "input.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "search_files",
			Glob: "**/tui/**/*.go",
			Path: ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("glob-only search_files should execute, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/tui/chat/input.go"), fmt.Sprintf("recursive glob should match nested tui Go file, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("tui/chat/root_input.go"), fmt.Sprintf("recursive glob should match root-level tui Go file, got:\n%s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("internal/runtime/agent/input.go"), fmt.Sprintf("recursive glob should not collapse to a broad input search, got:\n%s", result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Interpreted glob"), fmt.Sprintf("summary should explain glob interpretation, got %q", result.Summary))

		patternResult := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "**/tui/**/*.go",
			Path:    ".",
		})
		gomega.Expect(patternResult.Error).To(gomega.Equal(""), fmt.Sprintf("pattern recursive glob should execute, got %#v", patternResult))
		gomega.Expect(patternResult.Output).To(gomega.Equal(result.Output), fmt.Sprintf("pattern and glob recursive searches should return the same output:\npattern=%s\nglob=%s", patternResult.Output, result.Output))

	})
	ginkgo.It("search files glob only zero hit is search result not schema error", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal", "runtime", "agent", "input.go"), "package agent\n")
		writeTestFile(filepath.Join(dir, "docs", "tui-notes.md"), "tui notes\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "search_files",
			Glob: "**/tui/**/*.go",
			Path: ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("glob-only zero-hit should not be a schema error, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("No file matches"), fmt.Sprintf("zero-hit glob summary should be explicit, got %q", result.Summary))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Interpreted glob"), fmt.Sprintf("zero-hit glob summary should be explicit, got %q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("docs/tui-notes.md"), fmt.Sprintf("zero-hit glob should surface token-matched real paths before generic directories, got:\n%s", result.Output))

	})
	ginkgo.It("search files rejects query when pattern and glob omitted", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal", "surfaces", "tui", "app", "input.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:  "search_files",
			Query: "input",
			Path:  ".",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("query without pattern/glob should reject, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("pattern is required"), fmt.Sprintf("unexpected rejection: %#v", result))

	})
	ginkgo.It("search files ranks code like matches before noisy matches", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "docs", "chat-input.md"), "input docs\n")
		writeTestFile(filepath.Join(dir, "testdata", "fixtures", "input-layout.json"), "{}\n")
		writeTestFile(filepath.Join(dir, "internal", "surfaces", "tui", "app", "input.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "input",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))

		sourceIndex := strings.Index(result.Output, "internal/surfaces/tui/app/input.go")
		docsIndex := strings.Index(result.Output, "docs/chat-input.md")
		fixtureIndex := strings.Index(result.Output, "testdata/fixtures/input-layout.json")
		gomega.Expect(sourceIndex).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("expected source, docs, and fixture matches, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(docsIndex).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("expected source, docs, and fixture matches, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(fixtureIndex).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("expected source, docs, and fixture matches, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(sourceIndex).NotTo(gomega.BeNumerically(">", docsIndex), fmt.Sprintf("code-like path should rank before noisy matches, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(sourceIndex).NotTo(gomega.BeNumerically(">", fixtureIndex), fmt.Sprintf("code-like path should rank before noisy matches, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Code-like paths are ranked first"), fmt.Sprintf("summary should describe evidence shape, got %q", result.Summary))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("docs"), fmt.Sprintf("summary should describe evidence shape, got %q", result.Summary))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("fixtures"), fmt.Sprintf("summary should describe evidence shape, got %q", result.Summary))

	})
	ginkgo.It("search files reports no code like matches", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "docs", "tui-input.md"), "input docs\n")
		writeTestFile(filepath.Join(dir, "testdata", "scripted-contracts", "fixtures", "tui-input-layout-repo", "README.md"), "input fixture\n")
		writeTestFile(filepath.Join(dir, "testdata", "scripted-contracts", "fixtures", "tui-input-layout-repo", "scripts", "tui-input.json"), "{}\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "input",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("No code-like file matches"), fmt.Sprintf("summary should state that matches are noisy/non-code, got %q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("docs"), fmt.Sprintf("summary should state that matches are noisy/non-code, got %q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("fixtures"), fmt.Sprintf("summary should state that matches are noisy/non-code, got %q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("search files rejects when pattern glob and query omitted", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 100,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "search_files",
			Path: ".",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("missing pattern/glob/query should still reject clearly, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("pattern is required"), fmt.Sprintf("missing pattern/glob/query should still reject clearly, got %#v", result))

	})
	ginkgo.It("search files caps large log listings", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for i := 0; i < 30; i++ {
			writeTestFile(filepath.Join(dir,

				"logs", "conversations",

				fmt.Sprintf("recent-%02d.jsonl",
					i),
			), "{\"event\":\"x\"}\n",
			)
		}

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 128,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "logs/**/*",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("showing first 24"), fmt.Sprintf("expected capped log listing summary, got %q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("omitted 6 older log-like match(es)"), fmt.Sprintf("expected omitted-count marker, got output:\n%s", result.Output))

	})
	ginkgo.It("search files log intent sorts newest before display cap", func() {
		dir := ginkgo.GinkgoT().TempDir()
		oldTime := time.Date(2026, 5, 18, 14, 15, 0, 0, time.UTC)
		newTime := time.Date(2026, 5, 31, 12, 26, 0, 0, time.UTC)

		for i := 0; i < 30; i++ {
			path := filepath.Join(dir, "logs", "conversations", fmt.Sprintf("a-old-%02d.jsonl", i))
			writeTestFile(path, "{\"event\":\"old\"}\n")
			gomega.Expect(os.Chtimes(path, oldTime, oldTime)).To(gomega.Succeed())

		}

		newPath := filepath.Join(dir, "logs", "conversations", "z-new.jsonl")
		writeTestFile(newPath, "{\"event\":\"new\"}\n")
		gomega.Expect(os.Chtimes(newPath, newTime, newTime)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 3,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "logs/**/*",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files failed: %#v", result))

		firstLine := strings.Split(strings.TrimSpace(result.Output), "\n")[0]
		gomega.Expect(firstLine).To(gomega.ContainSubstring("logs/conversations/z-new.jsonl"), fmt.Sprintf("expected globally newest log first despite low directory cap, got first line %q from output:\n%s", firstLine, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Found 31 matching log-like file(s)"), fmt.Sprintf("expected truthful total and display cap after global log sort, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("omitted 7 older log-like match(es)"), fmt.Sprintf("expected truthful total and display cap after global log sort, summary=%q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("read file defaults to log tail", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, defaultLogReadLineCap+20)

		for i := 1; i <= defaultLogReadLineCap+20; i++ {
			lines = append(lines, fmt.Sprintf("line-%03d", i))
		}

		writeTestFile(filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			MaxFileBytes: 1 << 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "read_file",
			Path: "logs/server.log",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("1:line-001"), fmt.Sprintf("expected default log read to skip old head lines, got:\n%s", result.Output))

		wantLast := fmt.Sprintf("%d:line-%03d", defaultLogReadLineCap+20, defaultLogReadLineCap+20)
		gomega.Expect(result.Output).To(gomega.ContainSubstring(wantLast), fmt.Sprintf("expected recent log tail, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("recent log tail"), fmt.Sprintf("expected recent log tail, summary=%q output=\n%s", result.Summary, result.Output))

	})
	ginkgo.It("read file allows user global conversation log", func() {
		dir := ginkgo.GinkgoT().TempDir()
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			MaxFileBytes: 1 << 20,
		})

		for _, logPath := range []string{
			filepath.Join(home, ".idiom", "logs", "conversations", "session.jsonl"),
			filepath.Join(home, ".codex", "sessions", "2026", "06", "28", "rollout-2026-06-28T12-00-00-session.jsonl"),
			filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-06-28T12-00-00-session.jsonl"),
		} {
			writeTestFile(logPath, "line-001\nline-002\n")

			result := mgr.Execute(context.Background(), ToolRequest{
				Tool: ToolNameReadFile,
				Path: logPath,
			})
			gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file should allow user-global conversation log %q, got %#v", logPath, result))
			gomega.Expect(result.Output).To(gomega.ContainSubstring("line-002"), fmt.Sprintf("expected conversation log output for %q, summary=%q output=\n%s", logPath, result.Summary, result.Output))
			gomega.Expect(result.Summary).To(gomega.ContainSubstring(filepath.Base(logPath)), fmt.Sprintf("expected conversation log output for %q, summary=%q output=\n%s", logPath, result.Summary, result.Output))

		}
	})
	ginkgo.It("read file rejects other user global idiom files", func() {
		dir := ginkgo.GinkgoT().TempDir()
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)

		configPath := filepath.Join(home, ".idiom", "config.json")
		writeTestFile(configPath, "{}\n")

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			MaxFileBytes: 1 << 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile,
			Path: configPath,
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("read_file should reject non-log user-global .idiom path, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("escapes workspace root"), fmt.Sprintf("read_file should reject non-log user-global .idiom path, got %#v", result))

	})
	ginkgo.It("search files allows user global conversation log dirs", func() {
		dir := ginkgo.GinkgoT().TempDir()
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)

		idiomLog := filepath.Join(home, ".idiom", "logs", "conversations", "session.jsonl")
		codexSession := filepath.Join(home, ".codex", "sessions", "2026", "06", "28", "rollout-2026-06-28T12-00-00-session.jsonl")
		codexArchivedSession := filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-06-28T12-00-00-session.jsonl")
		for _, path := range []string{idiomLog, codexSession, codexArchivedSession} {
			writeTestFile(path, "{}\n")
		}

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 20,
		})

		for _, tc := range []struct {
			path string
			want string
		}{
			{path: filepath.Join(home, ".idiom", "logs", "conversations"), want: filepath.Base(idiomLog)},
			{path: filepath.Join(home, ".codex", "sessions"), want: filepath.Base(codexSession)},
			{path: filepath.Join(home, ".codex", "archived_sessions"), want: filepath.Base(codexArchivedSession)},
		} {
			result := mgr.Execute(context.Background(), ToolRequest{
				Tool:    ToolNameSearchFiles,
				Path:    tc.path,
				Pattern: ".jsonl",
			})
			gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files should allow global conversation log dir %q, got %#v", tc.path, result))
			gomega.Expect(result.Output).To(gomega.ContainSubstring(tc.want), fmt.Sprintf("search_files output for %q missing %q, summary=%q output=\n%s", tc.path, tc.want, result.Summary, result.Output))

			stats := mgr.Execute(context.Background(), ToolRequest{
				Tool:      ToolNameFileStats,
				Path:      tc.path,
				Recursive: true,
				Glob:      "*.jsonl",
			})
			gomega.Expect(stats.Error).To(gomega.Equal(""), fmt.Sprintf("file_stats should allow global conversation log dir %q, got %#v", tc.path, stats))
			gomega.Expect(stats.Output).To(gomega.ContainSubstring(tc.want), fmt.Sprintf("file_stats output for %q missing %q, summary=%q output=\n%s", tc.path, tc.want, stats.Summary, stats.Output))

		}
	})
	ginkgo.It("file stats line range reports byte and character counts", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "catalog.go")
		gomega.Expect(os.WriteFile(path, []byte("alpha\nconst Label = \"wide value\"\nomega\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      ToolNameFileStats,
			Path:      "catalog.go",
			StartLine: 2,
			EndLine:   2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("file_stats line range returned error: %s", result.Error))

		for _, want := range []string{
			`"start_line": 2`,
			`"end_line": 2`,
			`"line_bytes": 26`,
			`"line_bytes_with_newlines": 27`,
			`"line_characters": 26`,
		} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("file_stats output missing %q; summary=%q output=\n%s", want, result.Summary, result.Output))

		}
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("selected lines 2-2"), fmt.Sprintf("summary should include selected line range, got %q", result.Summary))

	})
	ginkgo.It("search files rejects other user global hidden dirs", func() {
		dir := ginkgo.GinkgoT().TempDir()
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)

		privatePath := filepath.Join(home, ".codex", "config.toml")
		writeTestFile(privatePath, "model = \"example\"\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSearchFiles,
			Path:    filepath.Join(home, ".codex"),
			Pattern: "config",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("search_files should reject non-log user-global .codex path, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("escapes workspace root"), fmt.Sprintf("search_files should reject non-log user-global .codex path, got %#v", result))

	})
	ginkgo.It("read file caps open ended log range", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, defaultLogReadLineCap+20)

		for i := 1; i <= defaultLogReadLineCap+20; i++ {
			lines = append(lines, fmt.Sprintf("line-%03d", i))
		}

		writeTestFile(filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			MaxFileBytes: 1 << 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "read_file",
			Path:      "logs/server.log",
			StartLine: 1,
			EndLine:   0,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("100:line-100"), fmt.Sprintf("expected open-ended log range cap, summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("open-ended log range capped"), fmt.Sprintf("expected open-ended log range cap, summary=%q output=\n%s", result.Summary, result.Output))

		if lines := strings.Count(result.Output, "\n") + 1; lines != defaultLogReadLineCap {
			ginkgo.Fail(fmt.Sprintf("expected %d output lines, got %d", defaultLogReadLineCap, lines))
		}
	})
	ginkgo.It("read file clamps an omitted-end log start beyond eof to the final line", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := []string{"line-001", "line-002", "line-003"}
		writeTestFile(filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			MaxFileBytes: 1 << 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "read_file",
			Path:      "logs/server.log",
			StartLine: 99990,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("expected log tail clamp instead of invalid range, got %#v", result))
		gomega.Expect(result.Output).To(gomega.Equal("3:line-003"), fmt.Sprintf("expected final-line output after clamp, got:\n%s", result.Output))

	})
	ginkgo.It("list files caps large output", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for i := 0; i < listFilesDisplayCap+20; i++ {
			writeTestFile(filepath.Join(dir,

				fmt.Sprintf("file-%03d.go",

					i)), "package main\n",
			)
		}

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: listFilesDisplayCap + 50,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "."})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("list_files failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("showing first 80"), fmt.Sprintf("expected capped list_files output, got summary=%q output=\n%s", result.Summary, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("more path(s) omitted"), fmt.Sprintf("expected capped list_files output, got summary=%q output=\n%s", result.Summary, result.Output))

		if lines := strings.Count(result.Output, "\n") + 1; lines > listFilesDisplayCap+1 {
			ginkgo.Fail(fmt.Sprintf("expected at most %d output lines, got %d:\n%s", listFilesDisplayCap+1, lines, result.Output))
		}
	})
	ginkgo.It("execute rejects path traversal and unsupported tools", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			CommandTimeoutSeconds: 5,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   100,
			EnableWrites:          true,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "../outside.txt"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected path traversal read to fail: %#v", result))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "definitely_not_a_tool"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected unsupported tool error, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("unsupported tool"), fmt.Sprintf("expected unsupported tool error, got %#v", result))

	})
	// Fix 1 (read-before-edit) — exercises docs/opencode-fixes-design.md §1.
	ginkgo.It("edit file requires prior observation of path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "bubble.go"), "package bubble\n")

		mgr := NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "bubble.go",
			OldText: "package bubble",
			NewText: "package bubble2",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected read-before-edit rejection on first edit, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("requires you to have observed"), fmt.Sprintf("expected read-before-edit rejection on first edit, got %#v", result))

		// A successful read_file marks the path observed.
		read := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "bubble.go"})
		gomega.Expect(read.Error).To(gomega.Equal(""), fmt.Sprintf("read_file unexpectedly failed: %#v", read))
		gomega.Expect(mgr.HasObservedPath("bubble.go")).To(gomega.BeTrue(), "read_file should have marked bubble.go observed")

		result = mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "bubble.go",
			OldText: "package bubble",
			NewText: "package bubble2",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("edit_file should succeed after read_file, got %#v", result))

	})
	// Fix 2 (fuzzy path suggestions) — exercises docs/opencode-fixes-design.md §2.
	ginkgo.It("edit file suggests nearest paths when file missing", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal/bubble/bubble.go"), "package bubble\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			EnableWrites:        true,
			MaxDirectoryEntries: 1024,
		})
		mgr.MarkPathObserved("bubble/bubble.go") // claim we "saw" the wrong path

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "edit_file",
			Path:    "bubble/bubble.go",
			OldText: "package bubble",
			NewText: "package bubble2",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected edit_file to fail on missing path, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/bubble/bubble.go"), fmt.Sprintf("expected nearest-path suggestion in output, got %#v", result))

	})
	// Extended Fix 2 — fuzzy suggestions must find sibling directories when the
	// user fabricates an entire parent directory (the failure mode we saw in
	// logs/conversations/20260420-210101.488.jsonl where the model invented
	// internal/llm/ in a repo that has no such package).
	ginkgo.It("find path suggestions surfaces sibling directories of missing ancestor", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")
		mustWriteOverview(filepath.Join(dir, "internal/surfaces/tui/app/app.go"), "package app\n")
		mustWriteOverview(filepath.Join(dir, "internal/capabilities/workspace/manager.go"), "package workspace\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
		})

		suggestions := mgr.findPathSuggestions("internal/llm/llm.go", 5)
		gomega.Expect(suggestions).NotTo(gomega.BeEmpty(), "expected at least one sibling directory hint when an entire parent is fabricated")

		joined := strings.Join(suggestions, " ")
		found := 0

		for _, wanted := range []string{"internal/runtime/", "internal/surfaces/", "internal/capabilities/"} {
			if strings.Contains(joined, wanted) {
				found++
			}
		}
		gomega.Expect(found < 2).To(gomega.BeFalse(), fmt.Sprintf("expected real sibling directories to appear among suggestions, got:\n%v", suggestions))

	})
	// Adoption fix D - when list_files is called on a fabricated directory,
	// the error must explicitly tell the model the directory does not exist
	// while keeping the payload descriptive rather than prescriptive.
	ginkgo.It("list files error names missing directory", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "internal/llm"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected list_files on fabricated dir to fail, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("does not exist in this workspace"), fmt.Sprintf("expected clear 'does not exist' framing, got error:\n%s", result.Error))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Do NOT retry"), fmt.Sprintf("expected descriptive path-miss error without retry directive, got error:\n%s", result.Error))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Do not retry"), fmt.Sprintf("expected descriptive path-miss error without retry directive, got error:\n%s", result.Error))

	})
	// Adoption fix D - read_file on a fabricated path whose PARENT directory is
	// also missing must call that out explicitly without prescribing recovery.
	ginkgo.It("read file error names missing parent directory when fabricated", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
			MaxFileBytes:        1 << 20,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/llm/llm.go"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected read_file on fabricated path to fail, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("parent directory"), fmt.Sprintf("expected 'parent directory ... does not exist' framing, got error:\n%s", result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("does not exist"), fmt.Sprintf("expected 'parent directory ... does not exist' framing, got error:\n%s", result.Error))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Do NOT retry"), fmt.Sprintf("expected descriptive parent-miss error without retry directive, got error:\n%s", result.Error))
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Do not retry"), fmt.Sprintf("expected descriptive parent-miss error without retry directive, got error:\n%s", result.Error))

	})
	// Extended Fix 2 — list_files and edit_file error paths also surface
	// "Did you mean" when a path is missing. Previously only read_file did.
	ginkgo.It("list files emits path suggestions on missing path", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "internal/llm"})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected list_files on fabricated dir to fail, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("Did you mean"), fmt.Sprintf("expected Did-you-mean output with real siblings, got %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/runtime"), fmt.Sprintf("expected Did-you-mean output with real siblings, got %#v", result))

	})
	// TestSearchFilesZeroHitSurfacesClosestPaths verifies the enrichment
	// behavior: when the pattern matches no files, the result carries the
	// closest real-path candidates so the model can switch to a targeted
	// read_file on a concrete path instead of searching again with another
	// invented name.
	ginkgo.It("search files zero hit surfaces closest paths", func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/runner.go"), "package agent\n")
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/tools.go"), "package agent\n")
		mustWriteOverview(filepath.Join(dir, "internal/runtime/agent/workbranch.go"), "package agent\n")

		mgr := NewManager(config.Workspace{
			Root:                dir,
			CWD:                 dir,
			MaxDirectoryEntries: 512,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_files",
			Pattern: "task_tool",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_files should not error on zero-hit: %#v", result))
		gomega.Expect(strings.ToLower(result.Summary)).To(gomega.ContainSubstring("no file matches"), fmt.Sprintf("expected zero-hit summary, got %q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/runtime/agent/tools.go"), fmt.Sprintf("expected closest-path suggestion to surface tools.go, got output: %q", result.Output))
		gomega.Expect(strings.ToLower(result.Output)).NotTo(gomega.ContainSubstring("read_file"), fmt.Sprintf("expected zero-hit output to stay descriptive rather than prescribing read_file, got: %q", result.Output))

	})
	// TestSearchContentZeroHitSuggestsPathsWhenPatternLooksLikeSymbol ensures
	// that when search_content gets no matches and the pattern is a bare
	// identifier (not a regex), we surface filename-match suggestions —
	// model often confuses "find the symbol" with "grep the symbol" and
	// the symbol's name lives in the filename.
	ginkgo.It("search content zero hit suggests paths when pattern looks like symbol", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/surfaces/tui/app/prompt_routing.go"), "package app\nfunc unrelated() {}\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "prompt_routing",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content should not error on zero-hit: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("prompt_routing.go"), fmt.Sprintf("expected filename-match suggestion, got output: %q", result.Output))

	})
	ginkgo.It("search content fixed strings finds regex syntax literally", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/runtime/workerruntime/catalog.go"), "package workerruntime\nvar _ = FinalCriticID{\"changed_paths_reported\"}\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:         "search_content",
			Pattern:      `FinalCriticID{`,
			Path:         ".",
			Glob:         "*.go",
			FixedStrings: true,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("fixed-string search_content should not treat pattern as regex: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`FinalCriticID{"changed_paths_reported"}`), fmt.Sprintf("expected literal regex-syntax match, got output: %q", result.Output))

	})
	ginkgo.It("search content skips noisy dirs by default", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/surfaces/tui/app/plan.go"), "package app\nfunc planPanel() {}\n")
		mustWriteOverview(filepath.Join(dir, "logs/conversations/recent.jsonl"), strings.Repeat(`{"content":"plan panel duplicated log context"}`, 200)+"\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "plan panel|planPanel",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("internal/surfaces/tui/app/plan.go"), fmt.Sprintf("expected source hit, got output: %q", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("logs/conversations"), fmt.Sprintf("default search should skip logs noise, got output: %q", result.Output))

		logResult := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "plan panel",
			Path:    "logs",
		})
		gomega.Expect(logResult.Error).To(gomega.Equal(""), fmt.Sprintf("explicit logs search failed: %#v", logResult))
		gomega.Expect(logResult.Output).To(gomega.ContainSubstring("logs/conversations/recent.jsonl"), fmt.Sprintf("explicit logs search should still work, got output: %q", logResult.Output))

	})
	ginkgo.It("search content prioritizes source files over tests", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal", "runtime", "agent", "runner.go"), "package agent\nfunc runChildTask() { /* delegate child spawn */ }\n")
		mustWriteOverview(filepath.Join(dir, "internal", "runtime", "agent", "runner_test.go"), "package agent\nfunc TestRunnerDelegate(t *testing.T) { /* task child */ }\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "delegate|child|spawn",
			Path:    "internal/runtime/agent",
			Glob:    "*.go",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", result))

		lines := strings.Split(result.Output, "\n")
		gomega.Expect(lines).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected source file hit to rank ahead of test file, got output: %q", result.Output))
		gomega.Expect(lines[0]).To(gomega.ContainSubstring("internal/runtime/agent/runner.go"), fmt.Sprintf("expected source file hit to rank ahead of test file, got output: %q", result.Output))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("search_content facts = %#v, want two untruncated hits", result.Facts))
		gomega.Expect(result.Facts.SearchHitCount).To(gomega.BeNumerically("==", 2), fmt.Sprintf("search_content facts = %#v, want two untruncated hits", result.Facts))
		gomega.Expect(result.Facts.SearchHighCardinality).To(gomega.BeFalse(), fmt.Sprintf("search_content facts = %#v, want two untruncated hits", result.Facts))

		if got, want := result.Facts.CandidatePaths, []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_test.go"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("search_content candidate facts = %#v, want ranked paths %#v", got, want))
		}
	})
	ginkgo.It("search content facts classify display cap as high cardinality", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		lines := []string{"package agent"}
		for i := 0; i < 50; i++ {
			lines = append(lines, fmt.Sprintf("func Target%d() {}", i))
		}
		mustWriteOverview(filepath.Join(dir, "internal", "runtime", "agent", "runner.go"), strings.Join(lines, "\n")+"\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "Target",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", result))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), "search_content facts were nil")
		gomega.Expect(result.Facts.SearchHitCount).To(gomega.BeNumerically("==", 50), fmt.Sprintf("search_content high-cardinality facts = %#v", result.Facts))
		gomega.Expect(result.Facts.SearchHighCardinality).To(gomega.BeTrue(), fmt.Sprintf("search_content high-cardinality facts = %#v", result.Facts))
		gomega.Expect(result.Facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthCandidateOnly), fmt.Sprintf("search_content high-cardinality facts = %#v", result.Facts))
		gomega.Expect(result.Facts.SourceOwnerFound).To(gomega.BeFalse(), fmt.Sprintf("search_content high-cardinality facts = %#v", result.Facts))

	})
	ginkgo.It("search content skips suite manifest by default but allows explicit path", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "testdata", "swe", "representative-suite.json"), `{"needle":"SuiteManifestOnlyWidget"}`+"\n")
		mustWriteOverview(filepath.Join(dir, "internal", "surfaces", "tui", "app", "banner.go"), "package app\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "SuiteManifestOnlyWidget",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", result))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("representative-suite.json"), fmt.Sprintf("default search should skip suite manifest, got output: %q", result.Output))

		explicit := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "SuiteManifestOnlyWidget",
			Path:    "testdata/swe/representative-suite.json",
		})
		gomega.Expect(explicit.Error).To(gomega.Equal(""), fmt.Sprintf("explicit suite manifest search failed: %#v", explicit))
		gomega.Expect(explicit.Output).To(gomega.ContainSubstring("representative-suite.json"), fmt.Sprintf("explicit suite manifest search should work, got output: %q", explicit.Output))

	})
	ginkgo.It("search content truncates long matching lines", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		mustWriteOverview(filepath.Join(dir, "internal/surfaces/tui/app/huge.go"), "package app\nvar huge = \"needle "+strings.Repeat("x", 800)+"\"\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "needle",
			Path:    ".",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(lineTruncationMarker), fmt.Sprintf("expected long matching line to be truncated, got output: %q", result.Output))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("search_content truncation facts = %#v, want one truncated hit", result.Facts))
		gomega.Expect(result.Facts.SearchHitCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("search_content truncation facts = %#v, want one truncated hit", result.Facts))
		gomega.Expect(result.Facts.SearchHighCardinality).To(gomega.BeFalse(), fmt.Sprintf("search_content truncation facts = %#v, want one truncated hit", result.Facts))

		for _, want := range []string{"displayed matching line(s) were truncated", "truncated output is not an exact old_text anchor", "read_file", "line-range edit shape"} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("expected truncation guidance to mention %q, got output: %q", want, result.Output))

		}
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("edit_file"), fmt.Sprintf("search_content truncation guidance should not name write tools, got output: %q", result.Output))
		gomega.Expect(len(result.Output) > 800).To(gomega.BeFalse(), fmt.Sprintf("expected compact search output, got %d chars: %q", len(result.Output), result.Output))

	})
	ginkgo.It("read file returns exact long line after search content truncates", func() {
		requireContentSearchBackend()

		dir := ginkgo.GinkgoT().TempDir()
		longLine := "var huge = \"needle " + strings.Repeat("x", 800) + "\""
		mustWriteOverview(filepath.Join(dir, "internal/surfaces/tui/app/huge.go"), "package app\n"+longLine+"\n")

		mgr := NewManager(config.Workspace{
			Root:                  dir,
			CWD:                   dir,
			MaxDirectoryEntries:   512,
			CommandTimeoutSeconds: 10,
		})
		search := mgr.Execute(context.Background(), ToolRequest{
			Tool:    "search_content",
			Pattern: "needle",
			Path:    ".",
		})
		gomega.Expect(search.Error).To(gomega.Equal(""), fmt.Sprintf("search_content failed: %#v", search))
		gomega.Expect(search.Output).To(gomega.ContainSubstring(lineTruncationMarker), fmt.Sprintf("test setup expected search output to truncate, got: %q", search.Output))

		read := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/huge.go",
			StartLine: 2,
			EndLine:   2,
		})
		gomega.Expect(read.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", read))
		gomega.Expect(read.Output).NotTo(gomega.ContainSubstring(lineTruncationMarker), fmt.Sprintf("explicit source read should return exact line without truncation, got: %q", read.Output))
		gomega.Expect(read.Output).To(gomega.ContainSubstring(longLine), fmt.Sprintf("explicit source read should include full old_text anchor, got %d chars: %q", len(read.Output), read.Output))

	})
	ginkgo.It("looks like bare identifier rejects regex shapes", func() {
		cases := []struct {
			in   string
			want bool
		}{
			{"Foo", true},
			{"foo_bar", true},
			{"internal/runtime/agent/runner.go", true},
			{"pkg.Foo", true},
			{"", false},
			{"x", false},        // too short
			{"foo.*bar", false}, // regex
			{"[a-z]+", false},
			{"a|b", false},
			{"^foo$", false},
		}
		for _, c := range cases {
			if got := looksLikeBareIdentifier(c.in); got != c.want {
				ginkgo.Fail(fmt.Sprintf("looksLikeBareIdentifier(%q) = %v, want %v", c.in, got, c.want))
			}
		}
	})
})

func mustWriteOverview(path, content string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

var _ = ginkgo.Describe("manager", func() {
	ginkgo.It("read file returns full large files when no range given", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, 700)

		for i := 0; i < 700; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i+1))
		}

		mustWriteOverview(filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("lines 1-700"), fmt.Sprintf("expected full range-less source read, summary=%q", result.Summary))
		gomega.Expect(result.Summary).NotTo(gomega.ContainSubstring("file has 700 lines total"), fmt.Sprintf("full source read should not include truncation hint; summary=%q", result.Summary))

	})
	ginkgo.It("read file honors explicit range for large files", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, 700)

		for i := 0; i < 700; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i+1))
		}

		mustWriteOverview(filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 1, EndLine: 700})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("lines 1-700"), fmt.Sprintf("explicit range should be preserved; summary=%q", result.Summary))
		gomega.Expect(result.Summary).NotTo(gomega.And(
			gomega.ContainSubstring("file has "),
			gomega.ContainSubstring("lines total"),
		), fmt.Sprintf("explicit range should not trigger truncation hint; summary=%q", result.Summary))

	})
	ginkgo.It("read file honors explicit range beyond max file bytes", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, 200)

		for i := 0; i < 200; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i+1))
		}

		mustWriteOverview(filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 80})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 150, EndLine: 152})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("lines 150-152"), fmt.Sprintf("explicit range should address lines beyond MaxFileBytes; summary=%q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("150:line 150"), fmt.Sprintf("expected late explicit range, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("152:line 152"), fmt.Sprintf("expected late explicit range, got:\n%s", result.Output))

	})
	ginkgo.It("read file clamps explicit range beyond eof", func() {
		dir := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, 50)

		for i := 0; i < 50; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i+1))
		}

		mustWriteOverview(filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 90, EndLine: 110})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_file failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("clamped to file bounds"), fmt.Sprintf("expected explicit out-of-range read to clamp, summary=%q", result.Summary))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("lines 30-50"), fmt.Sprintf("expected clamped tail window, summary=%q", result.Summary))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("30:line 30"), fmt.Sprintf("expected clamped output window, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("50:line 50"), fmt.Sprintf("expected clamped output window, got:\n%s", result.Output))

	})
})
