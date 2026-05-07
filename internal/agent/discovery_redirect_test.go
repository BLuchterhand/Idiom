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

// TestRedirectResultNamesReplacementTool ensures the synthetic
// ToolResult the model actually sees names the replacement tool and
// shows an example call — the redirect is useless if the model can't
// figure out what to try next from the result alone.
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
	if !strings.Contains(output, "fall") && !strings.Contains(output, "retry") {
		t.Fatalf("result output should mention fallback/retry path: %q", output)
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

func TestSearchContentRedirectsAdditionalLanguageSyntaxPatterns(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		pattern string
		want    string
	}{
		{
			name: "python_class",
			files: map[string]string{
				"pyproject.toml": "[project]\nname='x'\n",
				"pkg/model.py":   "class Widget:\n    pass\n",
			},
			pattern: "class Widget",
			want:    "python",
		},
		{
			name: "typescript_interface",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export interface Widget {}\n",
			},
			pattern: "export interface Widget",
			want:    "typescript",
		},
		{
			name: "rust_struct",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub struct Widget;\n",
			},
			pattern: "pub struct Widget",
			want:    "rust",
		},
		{
			name: "java_record",
			files: map[string]string{
				"pom.xml":              "<project></project>\n",
				"src/main/Widget.java": "public record Widget() {}\n",
			},
			pattern: "public record Widget",
			want:    "java",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := newTestWorkspaceWithFiles(t, tc.files)
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: tc.pattern}, ws)
			if !decision.ShouldRedirect {
				t.Fatalf("pattern %q should redirect", tc.pattern)
			}
			if decision.Language != tc.want {
				t.Fatalf("language: want %s, got %s", tc.want, decision.Language)
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

// TestShellGrepIdentifierRedirectsInCppWorkspace confirms the shell-grep
// rewrite fires with cpp_references / cpp_workspace_symbol when the
// active language is C++.
func TestShellGrepIdentifierRedirectsInCppWorkspace(t *testing.T) {
	ws := newCppTestWorkspace(t)
	t.Run("scoped_to_cpp_file_uses_references", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -n EntityManager src/main.cpp`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("scoped grep should redirect")
		}
		if decision.Language != "cpp" {
			t.Fatalf("language: want cpp, got %s", decision.Language)
		}
		if decision.Capability != workspace.CapReferences {
			t.Fatalf("capability: want references, got %s", decision.Capability)
		}
		if !strings.Contains(decision.Result.Output, "cpp_references") {
			t.Fatalf("should name cpp_references: %q", decision.Result.Output)
		}
	})
	t.Run("unscoped_uses_cpp_workspace_symbol", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -rn EntityManager`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("recursive grep should redirect")
		}
		if decision.Language != "cpp" {
			t.Fatalf("language: want cpp, got %s", decision.Language)
		}
	})
}

// TestShellSedRangeRedirectsOnCppFile confirms the sed→read_file rewrite
// is language-agnostic — the read_file tool itself is generic.
func TestShellSedRangeRedirectsOnCppFile(t *testing.T) {
	ws := newCppTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `sed -n '10,40p' src/main.cpp`}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("sed on cpp file should redirect")
	}
	out := decision.Result.Output
	if !strings.Contains(out, `"tool":"read_file"`) || !strings.Contains(out, "src/main.cpp") {
		t.Fatalf("result should redirect to read_file on the cpp file: %q", out)
	}
}

// TestShellSedRangeRedirectsToReadFile covers the 15-hit cluster from
// live logs: `sed -n 'A,Bp' FILE` is literally read_file with bounds.
func TestShellSedRangeRedirectsToReadFile(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: "sed -n '100,150p' internal/agent/runner.go"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("sed range read should redirect")
	}
	out := decision.Result.Output
	if !strings.Contains(out, `"tool":"read_file"`) {
		t.Fatalf("result should name read_file: %q", out)
	}
	if !strings.Contains(out, `"start_line":100`) || !strings.Contains(out, `"end_line":150`) {
		t.Fatalf("result should carry the bounds: %q", out)
	}
	if !strings.Contains(out, "internal/agent/runner.go") {
		t.Fatalf("result should carry the path: %q", out)
	}
}

// TestShellSedSkipsInPlaceEdits ensures destructive sed forms don't get
// silently rewritten — only the read-only `-n 'A,Bp'` shape is safe.
func TestShellSedSkipsInPlaceEdits(t *testing.T) {
	ws := newGoTestWorkspace(t)
	for _, cmd := range []string{
		"sed -i 's/foo/bar/' main.go",
		"sed -e '1d' main.go",
		"sed -n '1,20p' main.go | head -5",
	} {
		t.Run(cmd, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: cmd}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("shell command %q should not redirect to read_file", cmd)
			}
		})
	}
}

// TestShellGrepIdentifierRedirects covers the largest shell cluster —
// `grep -n "Ident" file` and `grep -rn "Ident"` — routing to references
// (scoped) or workspace_symbol (broad).
func TestShellGrepIdentifierRedirects(t *testing.T) {
	ws := newGoTestWorkspace(t)
	t.Run("with_file_scope_uses_references", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -n RunPolicy main.go`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("scoped grep should redirect")
		}
		if decision.Capability != workspace.CapReferences {
			t.Fatalf("capability: want references, got %s", decision.Capability)
		}
		if !strings.Contains(decision.Result.Output, "go_references") {
			t.Fatalf("should name go_references: %q", decision.Result.Output)
		}
	})
	t.Run("workspace_wide_uses_workspace_symbol", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -rn RunPolicy`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("recursive grep should redirect")
		}
		if decision.Capability != workspace.CapWorkspaceSymbol {
			t.Fatalf("capability: want workspace_symbol, got %s", decision.Capability)
		}
	})
}

// TestShellGrepSkipsRegexAndPipes ensures we only rewrite the safe
// identifier shape — never a pipeline, redirect, or regex pattern.
func TestShellGrepSkipsRegexAndPipes(t *testing.T) {
	ws := newGoTestWorkspace(t)
	for _, cmd := range []string{
		`grep -rn "func.*Run"`,          // regex
		`grep -rn "Run" | head -20`,     // pipe
		`grep -rn "Run" > out.txt`,      // redirect
		`grep -rn "$VAR"`,               // variable
		`grep -E '^(foo|bar)$' main.go`, // regex alternation
	} {
		t.Run(cmd, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: cmd}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("shell command %q should not redirect", cmd)
			}
		})
	}
}

func TestShellGrepSkipsAmbiguousUnscopedPolyglotSearch(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":                "module example.test\n",
		"main.go":               "package main\nfunc Widget() {}\n",
		"pyproject.toml":        "[project]\nname='x'\n",
		"pkg/widget.py":         "def Widget():\n    pass\n",
		"compile_commands.json": "[]\n",
		"src/widget.cpp":        "class Widget {};\n",
	})
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -rn Widget`}, ws)
	if decision.ShouldRedirect {
		t.Fatalf("unscoped grep in polyglot workspace should not pick %s", decision.Language)
	}
}

func TestShellGrepUsesIncludeGlobForLanguage(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":         "module example.test\n",
		"main.go":        "package main\nfunc Widget() {}\n",
		"pyproject.toml": "[project]\nname='x'\n",
		"pkg/widget.py":  "def Widget():\n    pass\n",
	})
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -rn --include=*.py Widget`}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("include-scoped grep should redirect")
	}
	if decision.Language != "python" {
		t.Fatalf("language: want python, got %s", decision.Language)
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

// TestShellRedirectFingerprintPerCommand ensures each distinct shell
// command gets its own one-shot — before the fingerprint fix, every
// shell call in a turn shared "shell|||" and only the first redirected.
func TestShellRedirectFingerprintPerCommand(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	first := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `sed -n '1,20p' a.go`}, ws)
	if !first.ShouldRedirect {
		t.Fatalf("first shell redirect should fire")
	}
	second := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `sed -n '30,50p' b.go`}, ws)
	if !second.ShouldRedirect {
		t.Fatalf("second shell redirect (different command) should also fire")
	}
	// But the same command a second time must fall through as the fallback.
	third := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `sed -n '1,20p' a.go`}, ws)
	if third.ShouldRedirect {
		t.Fatalf("identical shell command should not redirect twice")
	}
}
