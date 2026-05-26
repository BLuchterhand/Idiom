package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

// newCppTestWorkspace builds a workspace the C++ detector recognizes
// (one .cpp file + compile_commands.json). Used to drive C++-side
// redirect tests without pulling in the full cpp_tools_test fixtures.
func newCppTestWorkspace(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	// Pad past the small-file redirect threshold so the outline redirect
	// actually fires. The redirect deliberately skips files that are
	// cheap to read whole; tests that exercise the redirect path need
	// files large enough to trip the threshold.
	if err := os.WriteFile(filepath.Join(root, "src", "main.cpp"), []byte("int main(){return 0;}\n"+strings.Repeat("\n", 500)), 0o644); err != nil {
		t.Fatalf("main.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "compile_commands.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("compile_commands.json: %v", err)
	}
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

// newEmptyWorkspace returns a workspace with no language markers. The
// Go detector requires go.mod and the C++ detector requires either
// compile_commands.json or a C/C++ source file — neither is present.
func newEmptyWorkspace(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# empty\n"), 0o644); err != nil {
		t.Fatalf("README.md: %v", err)
	}
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

// newGoTestWorkspace builds a throwaway Go workspace (go.mod + one .go
// file) so the language detector activates. Used by the routing tests
// below to drive matchRedirectRule against a real Manager.
func newGoTestWorkspace(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	// Pad past the small-file redirect threshold so the outline redirect
	// actually fires. The redirect deliberately skips files that are
	// cheap to read whole; tests that exercise the redirect path need
	// files large enough to trip the threshold.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"+strings.Repeat("\n", 500)), 0o644); err != nil {
		t.Fatalf("main.go: %v", err)
	}
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

func newGoWorkspaceWithLargeTestFile(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	testPath := filepath.Join(root, "cmd", "idiom", "product_workflow_test.go")
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}
	body := "package main\n\nfunc TestProductWorkflow(t *testing.T) {}\n" + strings.Repeat("\n", smallFileRedirectThreshold+25)
	if err := os.WriteFile(testPath, []byte(body), 0o644); err != nil {
		t.Fatalf("product_workflow_test.go: %v", err)
	}
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

func newTestWorkspaceWithFiles(t *testing.T, files map[string]string) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

func TestReadFileRedirectsLargeTestFileToOutline(t *testing.T) {
	ws := newGoWorkspaceWithLargeTestFile(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow_test.go",
	}, ws)
	if !decision.ShouldRedirect {
		t.Fatal("expected large unbounded test-file read to redirect")
	}
	if decision.Capability != workspace.CapFileOutline {
		t.Fatalf("capability: want %s, got %s", workspace.CapFileOutline, decision.Capability)
	}
	if !strings.Contains(decision.Result.Output, `"tool":"go_file_outline"`) {
		t.Fatalf("expected outline candidate request, got:\n%s", decision.Result.Output)
	}
}

// TestSearchContentRoutesByPathScope locks in the decision: when the
// model already scoped the search to a path, index-backed find_symbol
// should win; with no path, workspace_symbol (gopls-backed) should.
// This is the fix for the bug the e2e session surfaced where
// workspace_symbol was always preferred, silently dropping the model's
// path scope.
func TestSearchContentRoutesByPathScope(t *testing.T) {
	ws := newGoTestWorkspace(t)

	cases := []struct {
		name    string
		req     workspace.ToolRequest
		wantCap workspace.Capability
	}{
		{
			name:    "no path scope prefers workspace_symbol",
			req:     workspace.ToolRequest{Tool: "search_content", Pattern: "Runner", Glob: "*.go"},
			wantCap: workspace.CapWorkspaceSymbol,
		},
		{
			name:    "explicit path scope prefers find_symbol",
			req:     workspace.ToolRequest{Tool: "search_content", Pattern: "Runner", Glob: "*.go", Path: "internal/agent"},
			wantCap: workspace.CapFindSymbol,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(tc.req, ws)
			if !decision.ShouldRedirect {
				t.Fatalf("expected redirect, got none")
			}
			if decision.Capability != tc.wantCap {
				t.Fatalf("capability: want %s, got %s", tc.wantCap, decision.Capability)
			}
		})
	}
}

func TestSearchContentRedirectStaysSoftAfterRepeatedClass(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	requests := []workspace.ToolRequest{
		{Tool: "search_content", Pattern: "Runner", Glob: "*.go"},
		{Tool: "search_content", Pattern: "RunPolicy", Glob: "*.go"},
		{Tool: "search_content", Pattern: "ToolRequest", Glob: "*.go"},
	}
	for i, req := range requests {
		decision := d.Consider(req, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("request %d: expected redirect", i+1)
		}
		if decision.HardReject {
			t.Fatalf("request %d: search_content redirect should remain a soft nudge", i+1)
		}
	}
}

func TestReadFileOutlineRedirectStaysSoftAfterRepeatedClass(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":   "module example.test\n",
		"a.go":     "package main\nfunc A() {}\n" + strings.Repeat("\n", smallFileRedirectThreshold+10),
		"b.go":     "package main\nfunc B() {}\n" + strings.Repeat("\n", smallFileRedirectThreshold+10),
		"c.go":     "package main\nfunc C() {}\n" + strings.Repeat("\n", smallFileRedirectThreshold+10),
		"extra.go": "package main\nfunc Extra() {}\n" + strings.Repeat("\n", smallFileRedirectThreshold+10),
	})
	d := NewDiscoveryRedirect()
	requests := []workspace.ToolRequest{
		{Tool: "read_file", Path: "a.go"},
		{Tool: "read_file", Path: "b.go"},
		{Tool: "read_file", Path: "c.go"},
		{Tool: "read_file", Path: "extra.go"},
	}
	for i, req := range requests {
		decision := d.Consider(req, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("request %d: expected redirect", i+1)
		}
		if decision.HardReject {
			t.Fatalf("request %d: read_file redirect should remain a soft nudge", i+1)
		}
		if strings.TrimSpace(decision.Result.Error) != "" {
			t.Fatalf("request %d: soft redirect should not surface as an error: %#v", i+1, decision.Result)
		}
		if i >= 1 && !strings.Contains(decision.Result.Output, "Redirect #") {
			t.Fatalf("request %d: repeated redirect should still carry explicit guidance:\n%s", i+1, decision.Result.Output)
		}
	}
}

// TestRedirectSkipsInactiveLanguage ensures a request that looks like
// it matches a language (by extension) but targets a workspace where
// that language is not active does not redirect. The deterministic
// tool would fail — feeding that failure to the model is worse than
// letting the generic call through.
func TestRedirectSkipsInactiveLanguage(t *testing.T) {
	ws := newEmptyWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: "main.go"}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("expected no redirect in a non-Go workspace, got one for %s", decision.Language)
	}
}

func TestReadFileRedirectSkipsMissingPath(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/gate.go"}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("missing read_file path should flow to path resolution, got redirect to %s", decision.Capability)
	}
}

// TestRedirectSkipsUnknownExtension ensures read_file on a file whose
// extension is unmapped (e.g. Markdown, plain text) never redirects.
func TestRedirectSkipsUnknownExtension(t *testing.T) {
	ws := newGoTestWorkspace(t)
	for _, path := range []string{"README.md", "LICENSE", "notes.txt", "data.json"} {
		t.Run(path, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: path}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("%s should not redirect, got one to %s", path, decision.Capability)
			}
		})
	}
}

// TestRedirectSkipsNonIdentifierPattern ensures search_content with a
// regex or non-identifier pattern does not redirect — workspace_symbol
// and find_symbol are symbol-name tools, not general regex search.
func TestRedirectSkipsNonIdentifierPattern(t *testing.T) {
	ws := newGoTestWorkspace(t)
	patterns := []string{
		`foo.*bar`, // wildcard regex (not a declaration shape)
		`Runner\b`, // word-boundary regex
		`a b`,      // space
		`x`,        // too short (< 2 chars)
		`call(x)`,  // parens
	}
	// Note: `func\s+Run`, `func.*Run`, and `type Foo` now legitimately
	// redirect — they are Go declaration shapes in disguise and are
	// covered by TestSearchContentRedirectsGoSyntaxPattern below.
	for _, p := range patterns {
		t.Run(p, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: p, Glob: "*.go"}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("pattern %q should not redirect", p)
			}
		})
	}
}

func TestSearchContentSkipsLowercaseLiteralTerm(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "banner", Glob: "*.go", Path: "internal/app"}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("lowercase literal UI/domain term should run as text search, got redirect to %s", decision.Capability)
	}
}

func TestSearchContentRedirectsSymbolShapedBareIdentifier(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "headerModelLabel", Glob: "*.go", Path: "internal/app"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("symbol-shaped identifier should redirect to structured symbol search")
	}
	if decision.Capability != workspace.CapFindSymbol {
		t.Fatalf("capability: want find_symbol, got %s", decision.Capability)
	}
}

// TestRedirectSkipsGenericGlob ensures search_content with no glob or
// a language-agnostic glob doesn't fire — we can't pick a language
// without a concrete extension.
func TestRedirectSkipsGenericGlob(t *testing.T) {
	ws := newGoTestWorkspace(t)
	for _, glob := range []string{"", "*", "**/*"} {
		t.Run("glob="+glob, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "Runner", Glob: glob}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("glob %q should not redirect", glob)
			}
		})
	}
}

func TestSearchContentSkipsConcreteFileIdentifier(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "edit_file", Path: "main.go"}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("exact-file identifier search should run as text search, got %s", decision.Capability)
	}
}

// TestRedirectHandlesBraceExpansionGlob ensures `*.{cpp,h}` and
// similar multi-extension globs resolve to the right language.
func TestRedirectHandlesBraceExpansionGlob(t *testing.T) {
	ws := newCppTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "EntityManager", Glob: "*.{cpp,h}"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("brace-expansion glob should redirect to cpp")
	}
	if decision.Language != "cpp" {
		t.Fatalf("language: want cpp, got %s", decision.Language)
	}
}

// TestRedirectRoutesCppReadFile mirrors TestDiscoveryRedirectOnReadFileCodePath
// but on the C++ side — closes the symmetry gap where every redirect
// test was Go-only.
func TestRedirectRoutesCppReadFile(t *testing.T) {
	ws := newCppTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: "src/main.cpp"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("expected redirect for .cpp read")
	}
	if decision.Language != "cpp" {
		t.Fatalf("language: want cpp, got %s", decision.Language)
	}
	if decision.Capability != workspace.CapFileOutline {
		t.Fatalf("capability: want file_outline, got %s", decision.Capability)
	}
}

func TestRedirectSkipsSmallTestLikeReadFileOutline(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"native/tests/test_item_lifecycle_system.cpp": strings.Repeat("TEST_CASE(\"[REC-027] captures previous band\") {}\n", 20),
		"pkg/widget_test.go":                          strings.Repeat("func TestWidget(t *testing.T) {}\n", 20),
	})

	for _, path := range []string{
		"native/tests/test_item_lifecycle_system.cpp",
		"pkg/widget_test.go",
	} {
		t.Run(path, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: path}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("small test-like source read should execute directly, got redirect to %s", decision.Capability)
			}
		})
	}
}

func TestRedirectRoutesReadFileHandle(t *testing.T) {
	ws := newGoTestWorkspace(t)
	ws.PrimeDiscoveredPaths([]string{"main.go"})
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", ID: "f1"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("expected redirect for unbounded handle read")
	}
	if decision.Language != "go" {
		t.Fatalf("language: want go, got %s", decision.Language)
	}
	if decision.Capability != workspace.CapFileOutline {
		t.Fatalf("capability: want file_outline, got %s", decision.Capability)
	}
	if !strings.Contains(decision.Result.Output, `"path":"main.go"`) {
		t.Fatalf("result should resolve the handle to a path: %q", decision.Result.Output)
	}
}

// TestRedirectResultNamesReplacementTool ensures the synthetic
// ToolResult names the replacement adapter while framing it as a
// capability contract, not a raw next-tool command.
func TestRedirectResultNamesReplacementTool(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: "main.go"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("expected redirect")
	}
	output := decision.Result.Output
	if !strings.Contains(output, "go_file_outline") {
		t.Fatalf("result output missing replacement tool name: %q", output)
	}
	if !strings.Contains(output, `"path":"main.go"`) {
		t.Fatalf("result output missing example request path: %q", output)
	}
	if !strings.Contains(output, "direct_tool_fallback") {
		t.Fatalf("result output should mention fallback behavior: %q", output)
	}
	if strings.Contains(output, "Try:") || strings.Contains(output, "Next step:") {
		t.Fatalf("redirect output should avoid imperative strategy menu text: %q", output)
	}
}

func TestRedirectResultUsesCodeNavigateWhenDirectToolHidden(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	d.SetAllowedTools([]string{"search_content", "read_file", "code_navigate", "submit_plan"})

	decision := d.Consider(workspace.ToolRequest{Tool: "read_file", Path: "main.go"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("expected redirect")
	}
	output := decision.Result.Output
	if !strings.Contains(output, `"tool":"code_navigate"`) || !strings.Contains(output, `"capability":"file_outline"`) {
		t.Fatalf("redirect should point at code_navigate file_outline candidate, got: %q", output)
	}
	if strings.Contains(output, `"tool":"go_file_outline"`) || strings.Contains(output, "via go_read_symbol") {
		t.Fatalf("redirect leaked hidden direct Go tools into candidate guidance: %q", output)
	}
}

// TestSearchContentRedirectsGoSyntaxPattern covers the case the analyzer
// surfaced from live logs: `func.*Foo`, `func Foo(`, and `type Foo`
// patterns are Go declaration shapes and should redirect to the symbol
// capability even when no glob is set — those patterns are
// unambiguously Go.
func TestSearchContentRedirectsGoSyntaxPattern(t *testing.T) {
	ws := newGoTestWorkspace(t)
	cases := []struct {
		name    string
		pattern string
	}{
		{"func_name", `func Runner`},
		{"func_receiver", `func (r *Runner) Run`},
		{"func_wildcard", `func.*handleSlashCommand`},
		{"func_regex_escape", `func\s+Run`},
		{"type_decl", `type Runner`},
		{"const_decl", `const DefaultLimit`},
		{"var_decl", `var globalRunner`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: tc.pattern}, ws)
			if !decision.ShouldRedirect {
				t.Fatalf("pattern %q should redirect", tc.pattern)
			}
			if decision.Language != "go" {
				t.Fatalf("language: want go, got %s", decision.Language)
			}
		})
	}
}

func TestSearchContentRedirectExtractsDeclarationIdentifier(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "type ToolRequest struct", Path: "internal/workspace"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("declaration search should redirect")
	}
	if !strings.Contains(decision.Result.Output, `"pattern":"ToolRequest"`) {
		t.Fatalf("redirect should pass only the symbol identifier, got: %q", decision.Result.Output)
	}
	if !strings.Contains(decision.Result.Output, `"path":"internal/workspace"`) {
		t.Fatalf("redirect should preserve path scope for find_symbol, got: %q", decision.Result.Output)
	}
	if strings.Contains(decision.Result.Output, `"type ToolRequest struct"`) {
		t.Fatalf("redirect should not pass the whole declaration as the symbol pattern: %q", decision.Result.Output)
	}
}

func TestSearchContentWorkspaceSymbolRedirectExtractsDeclarationIdentifier(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()

	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "type ToolRequest struct"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("declaration search should redirect")
	}
	if decision.Capability != workspace.CapWorkspaceSymbol {
		t.Fatalf("capability: want workspace_symbol, got %s", decision.Capability)
	}
	if !strings.Contains(decision.Result.Output, `"pattern":"ToolRequest"`) {
		t.Fatalf("redirect should pass only the symbol identifier, got: %q", decision.Result.Output)
	}
	if strings.Contains(decision.Result.Output, `"type ToolRequest struct"`) {
		t.Fatalf("redirect should not pass the whole declaration as the symbol pattern: %q", decision.Result.Output)
	}
}

// TestSearchContentRedirectsCppSyntaxPattern mirrors the Go-syntax test
// for C++: class/namespace/enum/template declaration shapes should
// redirect without needing a glob hint.
func TestSearchContentRedirectsCppSyntaxPattern(t *testing.T) {
	ws := newCppTestWorkspace(t)
	cases := []struct {
		name    string
		pattern string
	}{
		{"class_decl", `class EntityManager`},
		{"class_wildcard", `class.*EntityManager`},
		{"struct_decl", `struct EntityManager`},
		{"struct_wildcard", `struct.*EntityManager`},
		{"namespace_decl", `namespace engine`},
		{"enum_decl", `enum Color`},
		{"enum_class_decl", `enum class Color`},
		{"union_decl", `union EntityValue`},
		{"union_wildcard", `union.*EntityValue`},
		{"template_class", `template<typename T> class Container`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: tc.pattern}, ws)
			if !decision.ShouldRedirect {
				t.Fatalf("pattern %q should redirect", tc.pattern)
			}
			if decision.Language != "cpp" {
				t.Fatalf("language: want cpp, got %s", decision.Language)
			}
		})
	}
}

func TestSearchContentSkipsAmbiguousSyntaxInPolyglotWorkspace(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"compile_commands.json": "[]\n",
		"src/widget.cpp":        "class Widget {};\n",
		"pom.xml":               "<project></project>\n",
		"src/Widget.java":       "public class Widget {}\n",
	})
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "class Widget"}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("ambiguous class syntax should not redirect, got %s", decision.Language)
	}
}

func TestSearchContentPathDisambiguatesAmbiguousSyntax(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"compile_commands.json": "[]\n",
		"src/widget.cpp":        "class Widget {};\n",
		"pom.xml":               "<project></project>\n",
		"src/Widget.java":       "public class Widget {}\n",
	})
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: "class Widget", Path: "src/Widget.java"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("path-scoped Java class search should redirect")
	}
	if decision.Language != "java" {
		t.Fatalf("language: want java, got %s", decision.Language)
	}
}

func TestStructuredToolDetectionUsesLanguageRegistry(t *testing.T) {
	for _, tool := range []string{"python_overview", "ts_workspace_symbol", "rust_read_symbol", "java_references"} {
		t.Run(tool, func(t *testing.T) {
			if !isStructuredTool(tool) {
				t.Fatalf("%s should be classified as a structured language tool", tool)
			}
		})
	}
}
