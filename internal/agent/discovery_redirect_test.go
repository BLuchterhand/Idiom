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
		`grep -rn "func.*Run"`,           // regex
		`grep -rn "Run" | head -20`,      // pipe
		`grep -rn "Run" > out.txt`,       // redirect
		`grep -rn "$VAR"`,                // variable
		`grep -E '^(foo|bar)$' main.go`,  // regex alternation
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
