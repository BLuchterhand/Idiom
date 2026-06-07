package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
)

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

// TestShellSedRangeRedirectsOnCppFile confirms the sed-to-read_file rewrite
// is language-agnostic; the read_file tool itself is generic.
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
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: "sed -n '100,150p' internal/runtime/agent/runner.go"}, ws)
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
	if !strings.Contains(out, "internal/runtime/agent/runner.go") {
		t.Fatalf("result should carry the path: %q", out)
	}
}

// TestShellSedSkipsInPlaceEdits ensures destructive sed forms don't get
// silently rewritten; only the read-only `-n 'A,Bp'` shape is safe.
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

func TestShellWCRedirectsToFileStats(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: "wc -l internal/runtime/agent/runner.go"}, ws)
	if !decision.ShouldRedirect {
		t.Fatalf("wc file count should redirect")
	}
	if decision.Capability != workspace.Capability("file_stats") {
		t.Fatalf("capability: want file_stats, got %s", decision.Capability)
	}
	if !strings.Contains(decision.Result.Output, `"tool":"file_stats"`) || !strings.Contains(decision.Result.Output, "internal/runtime/agent/runner.go") {
		t.Fatalf("result should redirect to file_stats with the path: %q", decision.Result.Output)
	}
}

func TestShellWCSkipsPipelinesAndMultipleFiles(t *testing.T) {
	ws := newGoTestWorkspace(t)
	for _, cmd := range []string{
		"wc -l internal/runtime/agent/runner.go internal/surfaces/tui/app/app.go",
		"find . -type f | wc -l",
		"wc -L internal/runtime/agent/runner.go",
	} {
		t.Run(cmd, func(t *testing.T) {
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: cmd}, ws)
			if decision.ShouldRedirect {
				t.Fatalf("shell command %q should not redirect to file_stats", cmd)
			}
		})
	}
}

// TestShellGrepIdentifierRedirects covers the largest shell cluster:
// `grep -n "Ident" file` and `grep -rn "Ident"` route to references
// when scoped and workspace_symbol when broad.
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
	t.Run("quoted_declaration_extracts_symbol", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -n 'type ToolRequest struct' internal/capabilities/workspace/manager.go`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("declaration grep should redirect")
		}
		if decision.Capability != workspace.CapReferences {
			t.Fatalf("capability: want references, got %s", decision.Capability)
		}
		if !strings.Contains(decision.Result.Output, `"pattern":"ToolRequest"`) {
			t.Fatalf("should extract the declaration symbol: %q", decision.Result.Output)
		}
	})
	t.Run("identifier_alternation_with_late_include", func(t *testing.T) {
		d := NewDiscoveryRedirect()
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: `grep -rn 'parseToolCall\|ParseToolRequest' internal/ --include='*.go'`}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("identifier alternation grep should redirect")
		}
		if decision.Language != "go" {
			t.Fatalf("language: want go, got %s", decision.Language)
		}
		if !strings.Contains(decision.Result.Output, `"pattern":"parseToolCall"`) {
			t.Fatalf("should start from the first identifier alternative: %q", decision.Result.Output)
		}
	})
}

// TestShellGrepSkipsRegexAndPipes ensures we only rewrite the safe
// identifier shape, never a pipeline, redirect, or regex pattern.
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

// TestShellRedirectFingerprintPerCommand ensures each distinct shell
// command gets its own one-shot; before the fingerprint fix, every
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

func TestShellRedirectHardRejectsThirdDistinctCommandInClass(t *testing.T) {
	ws := newGoTestWorkspace(t)
	d := NewDiscoveryRedirect()
	commands := []string{
		`sed -n '1,20p' a.go`,
		`sed -n '30,50p' b.go`,
		`sed -n '60,80p' c.go`,
	}

	for i, command := range commands {
		decision := d.Consider(workspace.ToolRequest{Tool: "shell", Command: command}, ws)
		if !decision.ShouldRedirect {
			t.Fatalf("command %d should redirect", i+1)
		}
		if i < len(commands)-1 && decision.HardReject {
			t.Fatalf("command %d should still be a soft redirect", i+1)
		}
		if i == len(commands)-1 && !decision.HardReject {
			t.Fatalf("third distinct command in class should hard-reject")
		}
		if decision.HardReject && decision.Result.Error == "" {
			t.Fatalf("hard reject should put guidance in Error")
		}
		if decision.HardReject && decision.Result.Output != "" {
			t.Fatalf("hard reject should clear Output, got %q", decision.Result.Output)
		}
	}
}
