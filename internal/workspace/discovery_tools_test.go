package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestGenericDiscoveryToolsAreAdvertised(t *testing.T) {
	tools := ToolManifestTools(ToolManifestToolOptions{})
	for _, want := range []string{
		"discovery_query",
		"structural_search",
		"dependency_graph",
		"history_range",
		"history_symbol",
		"blame_range",
		"ctags_symbols",
		"semgrep_scan",
		"joern_scan",
	} {
		if !slices.Contains(tools, want) {
			t.Fatalf("expected manifest tools to include %q, got %#v", want, tools)
		}
	}

	defs := BuildToolDefinitions(tools)
	var names []string
	for _, def := range defs {
		names = append(names, def.Name)
	}
	for _, want := range []string{"discovery_query", "structural_search", "dependency_graph", "history_range", "blame_range"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected tool definitions to include %q, got %#v", want, names)
		}
	}
}

func TestReadToolSchemasAdvertisePostReadinessGapFields(t *testing.T) {
	defs := BuildToolDefinitions([]string{"search_content", "read_file"})
	for _, want := range []string{"search_content", "read_file"} {
		var matched bool
		for _, def := range defs {
			if def.Name != want {
				continue
			}
			matched = true
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal([]byte(def.ParametersJSON), &schema); err != nil {
				t.Fatalf("unmarshal schema for %s: %v", want, err)
			}
			if _, ok := schema.Properties["named_gap"]; !ok {
				t.Fatalf("expected %s schema to advertise named_gap, got %#v", want, schema.Properties)
			}
			if _, ok := schema.Properties["why_not_answering"]; !ok {
				t.Fatalf("expected %s schema to advertise why_not_answering, got %#v", want, schema.Properties)
			}
		}
		if !matched {
			t.Fatalf("missing tool definition for %s", want)
		}
	}
}

func TestEditFileSchemaDoesNotRequireContentAlone(t *testing.T) {
	defs := BuildToolDefinitions([]string{"edit_file"})
	if len(defs) != 1 {
		t.Fatalf("expected one edit_file definition, got %#v", defs)
	}
	def := defs[0]
	if !strings.Contains(def.Description, "old_text+new_text") ||
		!strings.Contains(def.Description, "start_line+end_line+content") {
		t.Fatalf("edit_file description should name both valid shapes, got %q", def.Description)
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(def.ParametersJSON), &schema); err != nil {
		t.Fatalf("unmarshal edit_file schema: %v", err)
	}

	required := map[string]bool{}
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, want := range []string{"path", "description"} {
		if !required[want] {
			t.Fatalf("expected edit_file required to include %q, got %#v", want, schema.Required)
		}
	}
	for _, notRequired := range []string{"content", "old_text", "new_text", "start_line", "end_line"} {
		if required[notRequired] {
			t.Fatalf("edit_file should not require %q by itself, got %#v", notRequired, schema.Required)
		}
	}
	for _, want := range []string{"old_text", "new_text", "start_line", "end_line", "content"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("expected edit_file property %q, got %#v", want, schema.Properties)
		}
	}
}

func TestDependencyGraphExtractsTypeScriptImportsWithoutMadge(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "src", "main.ts"), `import {x} from "./lib";
const fs = require("fs");
export { y } from "./other";
`)
	writeWorkspaceFile(t, filepath.Join(root, "src", "lib.ts"), `export const x = 1;`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
	target := filepath.Join(root, "src")
	result := mgr.regexDependencyGraph(target, "typescript", []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, tsImportRe)
	if result.Error != "" {
		t.Fatalf("dependency_graph returned error: %v\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"src/main.ts -> ./lib", "src/main.ts -> ./other", "src/main.ts -> fs"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected dependency graph to include %q, got:\n%s", want, result.Output)
		}
	}
}

func TestDependencyGraphExtractsPythonImportsWithoutPydeps(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "main.py"), `import os, sys
from pkg.helpers import run
`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
	target := filepath.Join(root, "pkg")
	result := mgr.regexDependencyGraph(target, "python", []string{".py", ".pyi"}, pythonImportRe)
	if result.Error != "" {
		t.Fatalf("dependency_graph returned error: %v\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"pkg/main.py -> os", "pkg/main.py -> sys", "pkg/main.py -> pkg.helpers"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected dependency graph to include %q, got:\n%s", want, result.Output)
		}
	}
}

func TestHistoryAndBlameToolsUseGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a + b\n}\n")
	runTestCommand(t, root, "git", "add", "sample.go")
	runTestCommand(t, root, "git", "commit", "-m", "initial")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a - b\n}\n")
	runTestCommand(t, root, "git", "commit", "-am", "change add")

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})

	history := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 3, EndLine: 5})
	if history.Error != "" {
		t.Fatalf("history_range returned error: %v\n%s", history.Error, history.Output)
	}
	if !strings.Contains(history.Output, "change add") || !strings.Contains(history.Output, "initial") {
		t.Fatalf("expected git log -L output to include both commits, got:\n%s", history.Output)
	}

	symbolHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "add"})
	if symbolHistory.Error != "" {
		t.Fatalf("history_symbol returned error: %v\n%s", symbolHistory.Error, symbolHistory.Output)
	}
	if !strings.Contains(symbolHistory.Output, "change add") || !strings.Contains(symbolHistory.Output, "initial") {
		t.Fatalf("expected git log -L symbol output to include both commits, got:\n%s", symbolHistory.Output)
	}

	blame := mgr.Execute(context.Background(), ToolRequest{Tool: "blame_range", Path: "sample.go", StartLine: 4, EndLine: 4})
	if blame.Error != "" {
		t.Fatalf("blame_range returned error: %v\n%s", blame.Error, blame.Output)
	}
	if !strings.Contains(blame.Output, "return a - b") {
		t.Fatalf("expected blame output for changed line, got:\n%s", blame.Output)
	}
}

func TestStructuralSearchRejectsMissingPattern(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "structural_search", Path: "."})
	if result.Error == "" || !strings.Contains(result.Error, "pattern is required") {
		t.Fatalf("expected missing-pattern error, got %#v", result)
	}
}

func TestOptionalExternalDiscoveryToolsReportUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	mgr := NewManager(testWorkspaceConfig())

	for _, req := range []ToolRequest{
		{Tool: "structural_search", Path: ".", Pattern: "$X"},
		{Tool: "ctags_symbols", Path: "."},
		{Tool: "semgrep_scan", Path: "."},
		{Tool: "joern_scan", Path: "."},
	} {
		result := mgr.Execute(context.Background(), req)
		if result.Error == "" || !strings.Contains(result.Summary, "unavailable") {
			t.Fatalf("expected %s to report unavailable, got %#v", req.Tool, result)
		}
	}
}

func writeWorkspaceFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func runTestCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(output))
	}
}
