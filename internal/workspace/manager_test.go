package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/semantic"
)

func TestGoReadSymbolWindowUsesSymbolExtentWhenAvailable(t *testing.T) {
	// Symbol declared at lines 16-132 (e.g. Runner.runChildTask): the
	// returned window should start at 16 and run for the full body when
	// it fits under the cap. Previous bug used match.Line - 12 / +20, which returned
	// imports for any function declared past line 12. Live run
	// 20260425-161904 exposed this when go_read_symbol on
	// `Runner.runChildTask` returned lines 4-36 — the imports.
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 0, 0)
	if start != 16 {
		t.Fatalf("expected start=16, got %d", start)
	}
	if end != 132 {
		t.Fatalf("expected end to include the full symbol body, got %d (start=%d)", end, start)
	}
}

func TestSourceSanityFallsBackWhenLanguageDiagnosticsUnavailable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "native", "tests", "test_energy.cpp")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("TEST_CASE(\"broken\" {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: "native/tests/test_energy.cpp"})

	if result.Tool != "source_sanity" {
		t.Fatalf("tool = %q, want source_sanity", result.Tool)
	}
	if result.Error == "" {
		t.Fatalf("expected generic fallback to catch malformed source, got %#v", result)
	}
	if !strings.Contains(result.Output, "cpp_diagnostics unavailable") || !strings.Contains(result.Output, "generic source sanity failed") {
		t.Fatalf("expected diagnostics-unavailable note and generic fallback in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Error, "unclosed") && !strings.Contains(result.Error, "mismatched") {
		t.Fatalf("expected delimiter failure, got %q", result.Error)
	}
}

func TestGoDefinitionNoMatchIsEmptyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nomatch\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pkg", "feature.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package pkg\n\nfunc Present() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "definition",
		Path:       "pkg/feature.go",
		Pattern:    "Missing",
	})

	if result.Error != "" {
		t.Fatalf("symbol no-match should be empty discovery evidence, got error %q", result.Error)
	}
	if !strings.Contains(result.Output, "Go location query found no matching symbol") || !strings.Contains(result.Output, `no Go symbols matched "Missing"`) {
		t.Fatalf("unexpected no-match result: %#v", result)
	}
}

func TestSourceSanityRoutesKnownLanguagesThroughDiagnosticsTool(t *testing.T) {
	if got := sourceSanityDiagnosticToolForPath("native/tests/test_energy.cpp"); got != "cpp_diagnostics" {
		t.Fatalf("cpp source_sanity tool = %q, want cpp_diagnostics", got)
	}
	if got := sourceSanityDiagnosticToolForPath("internal/agent/runner.go"); got != "go_diagnostics" {
		t.Fatalf("go source_sanity tool = %q, want go_diagnostics", got)
	}
	if got := sourceSanityDiagnosticToolForPath("scripts/demo.gd"); got != "" {
		t.Fatalf("gdscript source_sanity tool = %q, want generic fallback", got)
	}
	if !sourceSanityCoversPath("scripts/demo.gd") {
		t.Fatal("source_sanity should cover gdscript through generic fallback")
	}
}

func TestSourceSanityCompilesGoTestFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/source-sanity\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "pkg", "feature.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := filepath.Join(root, "pkg", "feature_test.go")
	if err := os.WriteFile(testSource, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tmissingHelper()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 30})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("pkg", "feature_test.go")})

	if result.Error == "" {
		t.Fatalf("expected source_sanity to fail on _test.go compile error, got %#v", result)
	}
	if !strings.Contains(result.Error, "go test compile failed") || !strings.Contains(result.Error, "missingHelper") {
		t.Fatalf("expected go test compile diagnostics, output=\n%s\nerror=\n%s", result.Output, result.Error)
	}
}

func TestSourceSanityCatchesGDScriptFunctionWithoutBody(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "scripts", "panel_test.gd")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("extends Node\n\nfunc _assert_gte(value, minimum, label):\n\nfunc _run_case():\n\tpass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("scripts", "panel_test.gd")})

	if result.Error == "" {
		t.Fatalf("expected gdscript structural sanity failure, got %#v", result)
	}
	if !strings.Contains(result.Error, "GDScript function declaration has no indented body") {
		t.Fatalf("expected missing-body diagnostic, got %q", result.Error)
	}
}

func TestSourceSanityAcceptsGDScriptFunctionBody(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "scripts", "panel_test.gd")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("extends Node\n\nfunc _assert_gte(value, minimum, label):\n\tif value < minimum:\n\t\tpush_error(label)\n\nfunc _run_case():\n\tpass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "source_sanity", Path: filepath.Join("scripts", "panel_test.gd")})

	if result.Error != "" {
		t.Fatalf("expected gdscript structural sanity to pass, got error=%v output=\n%s", result.Error, result.Output)
	}
}

func TestCodeNavigateRoutesByPathLanguage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "pkg", "runner.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       filepath.Join("pkg", "runner.go"),
		Pattern:    "Run",
	})

	if result.Tool != "code_navigate" {
		t.Fatalf("tool = %q, want code_navigate", result.Tool)
	}
	if result.Error != "" {
		t.Fatalf("code_navigate returned error: %v\n%s", result.Error, result.Output)
	}
	if !strings.Contains(result.Summary, "go_read_symbol") || !strings.Contains(result.Output, "routed_tool: go_read_symbol") {
		t.Fatalf("expected Go read-symbol route, got summary=%q output=\n%s", result.Summary, result.Output)
	}
	if !strings.Contains(result.Output, "func Run() string") {
		t.Fatalf("expected symbol body in output, got:\n%s", result.Output)
	}
}

func TestCodeNavigatePathLanguageBeatsConflictingOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "pkg", "runner.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       filepath.Join("pkg", "runner.go"),
		Language:   "python",
		Pattern:    "Run",
	})

	if result.Error != "" {
		t.Fatalf("code_navigate returned error: %v\n%s", result.Error, result.Output)
	}
	if !strings.Contains(result.Summary, "go_read_symbol") || !strings.Contains(result.Output, "language: go") {
		t.Fatalf("expected path-derived Go route to beat python override, got summary=%q output=\n%s", result.Summary, result.Output)
	}
}

func TestCodeNavigateGenericFallbackForUnregisteredSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nav\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "scripts", "player.gd")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("extends Node\n\nfunc _ready():\n\tpass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "definition",
		Path:       filepath.Join("scripts", "player.gd"),
		Pattern:    "_ready",
	})

	if result.Error != "" {
		t.Fatalf("code_navigate fallback returned error: %v\n%s", result.Error, result.Output)
	}
	if !strings.Contains(result.Summary, "generic fallback") || !strings.Contains(result.Output, "routed_tool: generic_source_text") {
		t.Fatalf("expected generic fallback, got summary=%q output=\n%s", result.Summary, result.Output)
	}
	if !strings.Contains(result.Output, "func _ready():") {
		t.Fatalf("expected fallback match in output, got:\n%s", result.Output)
	}
}

func TestGoReadSymbolWindowHonorsExplicitRequest(t *testing.T) {
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 50, 70)
	if start != 50 || end != 70 {
		t.Fatalf("expected explicit window to be honored, got %d-%d", start, end)
	}
}

func TestGoReadSymbolWindowFallsBackToCenteredWindow(t *testing.T) {
	// SymbolMatch with no extent (legacy index entry): fall back to the
	// old centered window so we don't regress.
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 200}, 0, 0)
	if start != 188 {
		t.Fatalf("expected fallback start=188 (200-12), got %d", start)
	}
	if end != 220 {
		t.Fatalf("expected fallback end=220 (200+20), got %d", end)
	}
}

func TestGoReadSymbolWindowCapsAtMaxLines(t *testing.T) {
	// A 500-line function should still be capped.
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 516}, 0, 0)
	if start != 16 {
		t.Fatalf("expected start=16, got %d", start)
	}
	if end != 135 {
		t.Fatalf("expected end capped at 120-line window (16+119), got %d", end)
	}
}

func TestGoReadSymbolIgnoresMaxFileBytesForTargetedLines(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/target\n\ngo 1.22\n")
	var b strings.Builder
	b.WriteString("package test\n\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "// filler %03d keeps the target beyond MaxFileBytes\n", i)
	}
	b.WriteString("func TargetSymbol() string {\n\treturn \"ok\"\n}\n")
	writeTestFile(t, filepath.Join(dir, "target.go"), b.String())

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 128})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_read_symbol", Pattern: "TargetSymbol"})
	if result.Error != "" {
		t.Fatalf("go_read_symbol returned error: %v", result.Error)
	}
	if !strings.Contains(result.Output, "func TargetSymbol() string") {
		t.Fatalf("expected targeted symbol body beyond MaxFileBytes, got summary=%q output=\n%s", result.Summary, result.Output)
	}
}

func TestGoWorkspaceSymbolFallsBackAfterGoplsFailure(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "worker.go"), "package test\n\nfunc ExampleWorker() {}\n")
	gopls := filepath.Join(dir, "fake-gopls.sh")
	writeTestFile(t, gopls, "#!/bin/sh\necho simulated gopls failure >&2\nexit 1\n")
	if err := os.Chmod(gopls, 0o755); err != nil {
		t.Fatalf("chmod fake gopls: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
	mgr.goplsChecked = true
	mgr.goplsPath = gopls

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_workspace_symbol", Pattern: "ExampleWorker"})
	if result.Error != "" {
		t.Fatalf("expected semantic fallback, got %#v", result)
	}
	if !strings.Contains(result.Summary, "semantic fallback after gopls failure") {
		t.Fatalf("expected fallback summary, got %#v", result)
	}
	if !strings.Contains(result.Output, "ExampleWorker") {
		t.Fatalf("expected fallback output to mention symbol, got %#v", result)
	}
}

func TestGoWorkspaceSymbolFallsBackAfterEmptyGoplsResult(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "worker.go"), "package test\n\nfunc ExampleWorker() {}\n")
	gopls := filepath.Join(dir, "fake-gopls.sh")
	writeTestFile(t, gopls, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(gopls, 0o755); err != nil {
		t.Fatalf("chmod fake gopls: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, CommandTimeoutSeconds: 5})
	mgr.goplsChecked = true
	mgr.goplsPath = gopls

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_workspace_symbol", Pattern: "func ExampleWorker"})
	if result.Error != "" {
		t.Fatalf("expected semantic fallback, got %#v", result)
	}
	if !strings.Contains(result.Summary, "semantic fallback after empty gopls result") {
		t.Fatalf("expected empty-result fallback summary, got %#v", result)
	}
	if !strings.Contains(result.Output, "ExampleWorker") {
		t.Fatalf("expected fallback output to mention symbol, got %#v", result)
	}
}

func TestEnvWithCanonicalPWDOverridesLogicalPWD(t *testing.T) {
	dir := t.TempDir()
	env := envWithCanonicalPWD([]string{"PATH=/bin", "PWD=/tmp/Logical"}, dir, []string{"PWD=/tmp/Other"})
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "PWD=") {
			count++
			if got := strings.TrimPrefix(entry, "PWD="); got != config.CanonicalizeExistingPath(dir) {
				t.Fatalf("expected canonical PWD %q, got %q", config.CanonicalizeExistingPath(dir), got)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one PWD entry, got %d in %#v", count, env)
	}
}

func TestSearchContentMissingScopeReturnsGracefulMiss(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "internal", "agent", "runner.go"), "package agent\n\nfunc ExampleWorker() {}\n")
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "search_content", Pattern: "ExampleWorker", Path: "missing/runner.go"})
	if result.Error != "" {
		t.Fatalf("expected graceful missing-scope miss, got %#v", result)
	}
	if !strings.Contains(result.Summary, "Search scope not found") {
		t.Fatalf("expected missing-scope summary, got %#v", result)
	}
}

func testWorkspaceConfig() config.Workspace {
	return config.Workspace{
		Root:                  filepath.Clean("../.."),
		CWD:                   filepath.Clean("../.."),
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	}
}

func TestGoDiagnosticTargetsIncludesWorkspaceRootFiles(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())

	files, err := mgr.goDiagnosticTargets(ToolRequest{Tool: "go_diagnostics", Path: "."})
	if err != nil {
		t.Fatalf("goDiagnosticTargets returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected workspace-root Go diagnostics to include indexed files")
	}
}

func TestInstructionsPreferStructuredNarrowing(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root: dir,
		CWD:  dir,
	})
	instructions := mgr.Instructions()
	for _, want := range []string{
		"Resolve a path-discovery capability, discovered handle, or structured symbol/navigation route",
		"narrow with structured language-aware routes or discovered handles first",
		"either answer directly or take one exact evidence step tied to a named gap",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("expected instructions to contain %q, got:\n%s", want, instructions)
		}
	}
	if strings.Contains(instructions, "search_files / search_content / read_file instead of repeating broad listings") {
		t.Fatalf("instructions should not prefer generic text tools after orientation:\n%s", instructions)
	}
}

func TestExploreInstructionsPreferStructuredNarrowing(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root: dir,
		CWD:  dir,
	})
	if err := mgr.SetMode(ModeExplore); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	instructions := mgr.Instructions()
	for _, want := range []string{
		"narrow with structured language-aware routes or discovered handles first",
		"Continue only for one exact missing claim and one exact probe",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("expected explore instructions to contain %q, got:\n%s", want, instructions)
		}
	}
	if strings.Contains(instructions, "search_files / search_content / read_file on discovered handles") {
		t.Fatalf("explore instructions should not prefer generic text tools first:\n%s", instructions)
	}
}

func TestResolveReadPathMissingFileSuggestsStructuredDiscovery(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root: dir,
		CWD:  dir,
	})
	_, _, err := mgr.resolveReadTarget(ToolRequest{Tool: "read_file", Path: "missing.go"})
	if err == nil {
		t.Fatal("expected missing read path error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "structured symbol/navigation") {
		t.Fatalf("expected missing path error to mention structured symbol/navigation capabilities, got %q", msg)
	}
}

func TestResolveReadTargetPrefersExplicitPathOverStaleHandle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wanted.txt"), []byte("wanted\n"), 0o644); err != nil {
		t.Fatalf("write wanted: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
	mgr.PrimeDiscoveredPaths([]string{"old.txt"})
	oldPath, err := mgr.ResolveHandlePath("f1")
	if err != nil {
		t.Fatalf("ResolveHandlePath f1: %v", err)
	}
	if filepath.Base(oldPath) != "old.txt" {
		t.Fatalf("test setup expected f1 to point at old.txt, got %q", oldPath)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", ID: "f1", Path: "wanted.txt"})
	if result.Error != "" {
		t.Fatalf("read_file should prefer explicit path over stale handle, got %#v", result)
	}
	if !strings.Contains(result.Output, "wanted") || strings.Contains(result.Output, "old") {
		t.Fatalf("read_file should read explicit path content, got %#v", result)
	}
}

func TestExecuteReadWriteAndExploreWriteRejection(t *testing.T) {
	dir := t.TempDir()
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
	if result.Error != "" {
		t.Fatalf("write_file failed: %#v", result)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "notes", "todo.txt")); err != nil || string(data) != "hello\nworld\n" {
		t.Fatalf("unexpected written file data=%q err=%v", string(data), err)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
	if result.Error != "" || !strings.Contains(result.Output, "2:world") {
		t.Fatalf("expected exact existing raw path read to auto-discover, got %#v", result)
	}
	mgr.PrimeDiscoveredPaths([]string{"notes/todo.txt"})
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "notes/todo.txt", StartLine: 2, EndLine: 2})
	if result.Error != "" || !strings.Contains(result.Output, "2:world") {
		t.Fatalf("read_file did not return requested line: %#v", result)
	}

	if err := mgr.SetMode(ModeExplore); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "write_file", Path: "blocked.txt", Content: "nope"})
	if result.Error == "" || !strings.Contains(result.Error, "explore mode is read-only") {
		t.Fatalf("expected explore write rejection, got %#v", result)
	}
}

func TestReadFileLineCountMatchesSubmitPlanLineRange(t *testing.T) {
	dir := t.TempDir()
	body := "package sample\n\nfunc Value() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "value.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir})
	read := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "value.go"})
	if read.Error != "" {
		t.Fatalf("read_file failed: %#v", read)
	}
	if !strings.Contains(read.Summary, "total lines: 5") || strings.Contains(read.Output, "\n6:") {
		t.Fatalf("read_file should report editable line spans without a synthetic trailing blank, got summary=%q output=\n%s", read.Summary, read.Output)
	}
	plan := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "update value",
		Edits: []PlanEdit{{
			Path:      "value.go",
			StartLine: 3,
			EndLine:   5,
			Content:   "func Value() int {\n\treturn 2\n}\n",
			Purpose:   "update returned value",
		}},
	})
	if plan.Error != "" {
		t.Fatalf("submit_plan should accept the line range advertised by read_file, got: %s", plan.Error)
	}

	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o644); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	empty := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "empty.txt"})
	if empty.Error != "" {
		t.Fatalf("empty read_file failed: %#v", empty)
	}
	if !strings.Contains(empty.Summary, "total lines: 0") || strings.Contains(empty.Output, "1:") {
		t.Fatalf("empty read_file should not advertise editable line 1, got summary=%q output=%q", empty.Summary, empty.Output)
	}
}

func TestUnsupportedToolErrorSuggestsClosestNames(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root: dir,
		CWD:  dir,
	})

	// Typo of "edit_file" — error should suggest the right name back.
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "edit_files",
		Path: "anything.md",
	})
	if result.Error == "" {
		t.Fatalf("expected unsupported-tool error, got %#v", result)
	}
	for _, want := range []string{"unsupported tool", "edit_file"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected error to contain %q, got: %s", want, result.Error)
		}
	}
}

func TestNotFoundRecoveryHintMentionsParentEntries(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root: dir,
		CWD:  dir,
	})
	// Create a real file in a subdir so the hint has something to suggest.
	subdir := filepath.Join(dir, "notes")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestFile(t, filepath.Join(subdir, "real.md"), "ok\n")

	// Ask to read a NEAR-MISS path — error should mention the real file.
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "read_file",
		Path: "notes/reel.md", // typo of real.md
	})
	if result.Error == "" {
		t.Fatalf("expected read_file not-found error, got %#v", result)
	}
	for _, want := range []string{"was not found", "real.md"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected error to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestEditFileRejectsEmptyContentLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	original := "line one\nline two\nline three\n"
	writeTestFile(t, path, original)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
		MaxFileBytes: 1 << 20,
	})
	mgr.MarkPathObserved("notes.md")

	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "newline", content: "\n"},
		{name: "spaces", content: "   \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			if result.Error == "" {
				t.Fatalf("expected guard to reject blank-content line-range edit, got %#v", result)
			}
			for _, want := range []string{"would DELETE", "old_text", "new_text"} {
				if !strings.Contains(result.Error, want) {
					t.Fatalf("expected guard error to mention %q, got: %s", want, result.Error)
				}
			}
			if got := readTestFile(t, path); got != original {
				t.Fatalf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
			}
		})
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("restore original: %v", err)
		}
	}
}

func TestEditFileContentWithoutAnchorRecoveryPrefersBoundedSourceEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc main() {}\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" {
		t.Fatalf("expected malformed edit_file to be rejected")
	}
	for _, want := range []string{"bounded edit_file shape", "concrete description", "old_text", "start_line"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected source recovery to mention %q, got:\n%s", want, result.Error)
		}
	}
	if strings.Contains(result.Error, "use write_file instead") || strings.Contains(result.Error, "REPLACE THE WHOLE FILE") {
		t.Fatalf("source recovery should not redirect to write_file:\n%s", result.Error)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("rejected edit should not mutate file\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFileContentWithoutAnchorRecoveryKeepsWholeFileOptionForDocs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	writeTestFile(t, path, "# Notes\n")
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
	if result.Error == "" {
		t.Fatalf("expected malformed edit_file to be rejected")
	}
	for _, want := range []string{"write_file", "concrete description", "new, small, or non-source deliverable"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected doc recovery to mention %q, got:\n%s", want, result.Error)
		}
	}
}

func TestFormatToolResultForModelCompactsContextPackOutput(t *testing.T) {
	largeBody := strings.Repeat(`{"path":"pkg/extra.go","reason":"extra candidate"},`, 200)
	result := ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/abc.json",
		Output: `{
			"pack_id":"abc",
			"artifact_path":".idiom/comprehension/views/abc.json",
			"candidate_files":[
				{"path":"internal/productworkflow/coverage_gate.go","role":"editable","reason":"coverage gate implementation"},
				{"path":"cmd/idiom/product_workflow.go","role":"editable","reason":"product tdd command wiring"},
				` + largeBody + `
				{"path":"pkg/final.go","reason":"tail"}
			],
			"candidate_symbols":[{"qualified_name":"EvaluateCoverage","path":"internal/productworkflow/coverage_gate.go","start_line":42,"end_line":90}],
			"tests":[{"command":"go test ./internal/productworkflow","path":"internal/productworkflow/coverage_gate_test.go","reason":"coverage gate regression tests"}]
		}`,
	}

	rendered := FormatToolResultForModel(result)
	for _, want := range []string{
		"context_pack_summary:",
		"pack_id: abc",
		"internal/productworkflow/coverage_gate.go",
		"cmd/idiom/product_workflow.go",
		"candidate_symbols:",
		"EvaluateCoverage",
		"go test ./internal/productworkflow",
		"tests:",
		"evidence_note: context_pack is localization evidence, not source-body evidence.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("compacted context_pack result missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, largeBody) || len(rendered) > 3000 {
		t.Fatalf("context_pack output was not compacted enough, len=%d", len(rendered))
	}
}

func TestEditFileLineRangePreservesTrailingLineBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	original := "line one\nline two\nline three\n"
	writeTestFile(t, path, original)
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
	if result.Error != "" {
		t.Fatalf("unexpected edit_file error: %#v", result)
	}

	want := "line one\nreplacement\nline three\n"
	if got := readTestFile(t, path); got != want {
		t.Fatalf("line-range edit should preserve line boundary:\nwant %q\ngot  %q", want, got)
	}
}

func TestEditFileRejectsLineRangeWithDuplicatedPrecedingContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	original := "setup one\nsetup two\nsetup three\n}\n\n}  // SUITE\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" {
		t.Fatalf("expected duplicated-context line-range edit to be rejected, got %#v", result)
	}
	for _, want := range []string{"duplicate context", "old_text", "new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected guard error to mention %q, got: %s", want, result.Error)
		}
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFilePrefersExactReplacementWhenLineRangeMetadataIsAlsoPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	original := "line one\nline two\nline three\n"
	writeTestFile(t, path, original)
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
	if result.Error != "" {
		t.Fatalf("expected exact replacement to win over line metadata, got: %s", result.Error)
	}
	want := "line one\nreplacement two\nline three\n"
	if got := readTestFile(t, path); got != want {
		t.Fatalf("unexpected edited file\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestEditFileRejectsLineRangeWithNewTextButNoOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	original := "line one\nline two\nline three\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" {
		t.Fatalf("expected ambiguous line-range/new_text shape to be rejected")
	}
	for _, want := range []string{"content", "new_text", "old_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected error to mention %q, got: %s", want, result.Error)
		}
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFileRejectsInvalidGoBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analysis_agents.go")
	original := "package app\n\nfunc ok() {}\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" || !strings.Contains(result.Error, "preflight syntax validation failed") {
		t.Fatalf("expected Go syntax preflight rejection, got %#v", result)
	}
	if !strings.Contains(result.Error, "planned content around syntax error") {
		t.Fatalf("expected syntax error context, got %#v", result)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("invalid edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFileReportsCaseFoldedAnchorMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "native_test.cpp")
	original := "TEST_CASE(\"CartSystem recovers route loss\") {\n    CHECK(true);\n}\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" || !strings.Contains(result.Error, "case-insensitive match exists") {
		t.Fatalf("expected case-folded anchor guidance, got %#v", result)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("rejected edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFileRequiresEvidenceAfterSyntaxPreflightFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analysis_agents.go")
	original := "package app\n\nfunc ok() {}\n"
	writeTestFile(t, path, original)
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
	if bad.Error == "" || !strings.Contains(bad.Error, "preflight syntax validation failed") {
		t.Fatalf("expected Go syntax preflight rejection, got %#v", bad)
	}

	blocked := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "edit_file",
		Path:    "analysis_agents.go",
		OldText: "func ok() {}",
		NewText: "func okRenamed() {}",
	})
	if blocked.Error == "" || !strings.Contains(blocked.Error, "previous syntax preflight failure") {
		t.Fatalf("expected recovery guard before next edit, got %#v", blocked)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("blocked edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}

	read := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "read_file",
		Path:      "analysis_agents.go",
		StartLine: 1,
		EndLine:   3,
	})
	if read.Error != "" {
		t.Fatalf("expected read_file to refresh evidence, got %#v", read)
	}

	fixed := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "edit_file",
		Path:    "analysis_agents.go",
		OldText: "func ok() {}",
		NewText: "func okRenamed() {}",
	})
	if fixed.Error != "" {
		t.Fatalf("expected edit after evidence refresh to succeed, got %#v", fixed)
	}
	if got := readTestFile(t, path); got != "package app\n\nfunc okRenamed() {}\n" {
		t.Fatalf("unexpected edited content:\n%s", got)
	}
}

func TestWriteFileAndAppendRejectInvalidStructuredContent(t *testing.T) {
	dir := t.TempDir()
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
	if result.Error == "" || !strings.Contains(result.Error, "preflight syntax validation failed") {
		t.Fatalf("expected invalid Go write rejection, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.go")); !os.IsNotExist(err) {
		t.Fatalf("invalid Go write should not create file, stat err=%v", err)
	}

	jsonPath := filepath.Join(dir, "config.json")
	original := "{\"ok\":true}\n"
	writeTestFile(t, jsonPath, original)
	result = mgr.Execute(context.Background(), ToolRequest{
		Tool:    "append_file",
		Path:    "config.json",
		Content: "not json",
	})
	if result.Error == "" || !strings.Contains(result.Error, "preflight JSON validation failed") {
		t.Fatalf("expected invalid JSON append rejection, got %#v", result)
	}
	if got := readTestFile(t, jsonPath); got != original {
		t.Fatalf("invalid append should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestWriteFileRejectsCompletedPlanningArtifactOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".planning", "atoms", "atom-a.md")
	original := "# Atom A\n\n**Status**: complete\n\nExisting accepted spec.\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" || !strings.Contains(result.Error, "completed planning artifact") {
		t.Fatalf("expected completed artifact overwrite rejection, got %#v", result)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("completed artifact should remain unchanged\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestEditFileRejectsNewAdjacentDuplicateAssignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sim.cpp")
	original := "void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n"
	writeTestFile(t, path, original)
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
	if result.Error == "" || !strings.Contains(result.Error, "adjacent duplicate code line") {
		t.Fatalf("expected adjacent duplicate rejection, got %#v", result)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("duplicate rejection should leave file unchanged\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestShellRejectsZeroSelectedDoctestCommand(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "zero_tests.sh")
	writeTestFile(t, script, "#!/bin/sh\nprintf 'test cases: 0 | 0 passed | 0 failed\\n'\nexit 0\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatalf("chmod zero_tests.sh: %v", err)
	}
	mgr := NewManager(config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		EnableShell:           true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "./zero_tests.sh -tc=Missing"})
	if result.Error == "" || !strings.Contains(result.Error, "selected zero tests") {
		t.Fatalf("expected zero-selected test rejection, got %#v", result)
	}
	if !strings.Contains(result.Output, "test cases: 0") {
		t.Fatalf("shell output should preserve command output for debugging, got %#v", result)
	}

	mgr.cfg.MaxOutputBytes = 8
	truncated := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "./zero_tests.sh -tc=Missing"})
	if truncated.Error == "" || !strings.Contains(truncated.Error, "selected zero tests") {
		t.Fatalf("zero-test detection must use raw output before truncation, got %#v", truncated)
	}
	if !strings.Contains(truncated.Output, "[truncated]") {
		t.Fatalf("small output cap should still truncate displayed output, got %#v", truncated)
	}

	control := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "printf 'test cases: 0 | 0 passed | 0 failed\\n'"})
	if control.Error != "" {
		t.Fatalf("non-test command with matching text should not be reclassified, got %#v", control)
	}
}

func TestGoFindFunctionIsImplemented(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "widget.go"), `package sample

func helper() {}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_function", Pattern: "helper"})
	if result.Error != "" {
		t.Fatalf("go_find_function failed: %#v", result)
	}
	if !strings.Contains(result.Output, "func helper") && !strings.Contains(result.Output, "function helper") {
		t.Fatalf("expected function match in output, got:\n%s", result.Output)
	}
}

func TestGoFileOutlineReportsDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "widget.go"), `package sample

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
	if result.Error != "" {
		t.Fatalf("go_file_outline failed: %#v", result)
	}
	if strings.Contains(result.Summary, "go_move_declarations") {
		t.Fatalf("outline summary should stay neutral for explanatory flows, got: %s", result.Summary)
	}
	for _, want := range []string{"Model.View", "helper", "imports:", "tags: rendering"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected outline to contain %q, got:\n%s", want, result.Output)
		}
	}
}

func TestGoFileOutlineSkipsSameFileMoveRecommendations(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "analysis_agents.go"), `package app

func analysisEvidence() {}

func renderView() {}
`)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "go_file_outline", Path: "analysis_agents.go"})
	if result.Error != "" {
		t.Fatalf("go_file_outline failed: %#v", result)
	}
	if strings.Contains(result.Output, "destination: analysis_agents.go") || strings.Contains(result.Summary, `destination:"analysis_agents.go"`) {
		t.Fatalf("outline should not recommend moving declarations back into the source file:\nsummary: %s\noutput:\n%s", result.Summary, result.Output)
	}
	if !strings.Contains(result.Output, "destination: rendering.go") {
		t.Fatalf("expected useful cross-file move recommendation to remain, got:\n%s", result.Output)
	}
}

func TestGoMoveDeclarationsMovesSelectedBlocksAndRepairsImports(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

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
	if result.Error != "" {
		t.Fatalf("go_move_declarations failed: %#v", result)
	}

	source := readTestFile(t, filepath.Join(dir, "source.go"))
	dest := readTestFile(t, filepath.Join(dir, "rendering.go"))
	if strings.Contains(source, "func (m *Model) View") {
		t.Fatalf("source still contains moved method:\n%s", source)
	}
	if !strings.Contains(source, `"strings"`) || strings.Contains(source, `"fmt"`) {
		t.Fatalf("source imports were not repaired:\n%s", source)
	}
	if !strings.Contains(dest, "func (m *Model) View") || !strings.Contains(dest, `"fmt"`) {
		t.Fatalf("destination missing moved method or import:\n%s", dest)
	}
	if strings.Contains(dest, `"strings"`) {
		t.Fatalf("destination retained unused import:\n%s", dest)
	}
}

func TestGoMoveDeclarationsAcceptsResponsibilityTagPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

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
	if result.Error != "" {
		t.Fatalf("go_move_declarations tag pattern failed: %#v", result)
	}
	source := readTestFile(t, filepath.Join(dir, "source.go"))
	dest := readTestFile(t, filepath.Join(dir, "rendering.go"))
	if strings.Contains(source, "renderView") || !strings.Contains(dest, "renderView") {
		t.Fatalf("tag move did not move rendering declaration\nsource:\n%s\ndest:\n%s", source, dest)
	}
	if !strings.Contains(source, "coreValue") {
		t.Fatalf("tag move should leave core declaration in source:\n%s", source)
	}
}

func TestGoMoveDeclarationsAcceptsBatchMoves(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "source.go"), `package sample

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
	if result.Error != "" {
		t.Fatalf("go_move_declarations batch failed: %#v", result)
	}
	if source := readTestFile(t, filepath.Join(dir, "source.go")); strings.Contains(source, "renderView") || strings.Contains(source, "normalize") || !strings.Contains(source, "coreValue") {
		t.Fatalf("batch move left unexpected source content:\n%s", source)
	}
	if dest := readTestFile(t, filepath.Join(dir, "rendering.go")); !strings.Contains(dest, "renderView") {
		t.Fatalf("rendering destination missing moved declaration:\n%s", dest)
	}
	if dest := readTestFile(t, filepath.Join(dir, "task_memory.go")); !strings.Contains(dest, "normalize") {
		t.Fatalf("task memory destination missing moved declaration:\n%s", dest)
	}
}

func TestListFilesSkipsNoisyDirsFromRootListing(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{
		".git/config",
		".cache/model.bin",
		"internal/app/app.go",
		"README.md",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 100,
		EnableShell:         true,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: ".", Recursive: true, MaxDepth: 3})
	if result.Error != "" {
		t.Fatalf("list_files failed: %#v", result)
	}
	if strings.Contains(result.Output, ".git/") || strings.Contains(result.Output, ".cache/") {
		t.Fatalf("root listing should skip noisy directories, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "internal/app/app.go") {
		t.Fatalf("expected source path in root listing, got:\n%s", result.Output)
	}
}

func TestShellRejectsDirectoryOnlyCommands(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		EnableShell:           true,
	})

	for _, command := range []string{"cd", "cd subdir", "cd subdir && cd .."} {
		t.Run(command, func(t *testing.T) {
			result := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: command})
			if result.Error == "" || !strings.Contains(result.Error, "do not persist") {
				t.Fatalf("expected directory-only shell rejection, got %#v", result)
			}
		})
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "shell", Command: "cd . && pwd"})
	if result.Error != "" {
		t.Fatalf("expected cd plus command to remain allowed, got %#v", result)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func TestSearchFilesSkipsNoisyDirsByDefaultButAllowsExplicitPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ".claude", "worktrees", "HiddenWorktreeWidget.go"), "package hidden\n")
	writeTestFile(t, filepath.Join(dir, "internal", "app", "app.go"), "package app\n")

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
	if result.Error != "" {
		t.Fatalf("search_files failed: %#v", result)
	}
	if strings.Contains(result.Output, ".claude") {
		t.Fatalf("default search_files should skip .claude worktrees, got:\n%s", result.Output)
	}

	explicit := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "search_files",
		Pattern: "HiddenWorktree",
		Path:    ".claude",
	})
	if explicit.Error != "" {
		t.Fatalf("explicit search_files failed: %#v", explicit)
	}
	if !strings.Contains(explicit.Output, ".claude/worktrees/HiddenWorktreeWidget.go") {
		t.Fatalf("explicit .claude search should still work, got:\n%s", explicit.Output)
	}
}

func TestSearchFilesIncludesLogsForLogIntent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "logs", "llama-server.log"), "failed to bind\n")
	writeTestFile(t, filepath.Join(dir, "internal", "app", "app.go"), "package app\n")

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
	if result.Error != "" {
		t.Fatalf("search_files failed: %#v", result)
	}
	if !strings.Contains(result.Output, "logs/llama-server.log") {
		t.Fatalf("log-shaped search should include logs directory, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "modified ") || !strings.Contains(result.Output, "size ") || !strings.Contains(result.Summary, "newest first") {
		t.Fatalf("log-shaped search should include recency metadata, summary=%q output=\n%s", result.Summary, result.Output)
	}
}

func TestSearchFilesCapsLargeLogListings(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		writeTestFile(t, filepath.Join(dir, "logs", "conversations", fmt.Sprintf("recent-%02d.jsonl", i)), "{\"event\":\"x\"}\n")
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
	if result.Error != "" {
		t.Fatalf("search_files failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "showing first 24") {
		t.Fatalf("expected capped log listing summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Output, "omitted 6 older log-like match(es)") {
		t.Fatalf("expected omitted-count marker, got output:\n%s", result.Output)
	}
}

func TestReadFileDefaultsToLogTail(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, defaultLogReadLineCap+20)
	for i := 1; i <= defaultLogReadLineCap+20; i++ {
		lines = append(lines, fmt.Sprintf("line-%03d", i))
	}
	writeTestFile(t, filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		MaxFileBytes: 1 << 20,
	})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "read_file",
		Path: "logs/server.log",
	})
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if strings.Contains(result.Output, "1:line-001") {
		t.Fatalf("expected default log read to skip old head lines, got:\n%s", result.Output)
	}
	wantLast := fmt.Sprintf("%d:line-%03d", defaultLogReadLineCap+20, defaultLogReadLineCap+20)
	if !strings.Contains(result.Output, wantLast) || !strings.Contains(result.Summary, "recent log tail") {
		t.Fatalf("expected recent log tail, summary=%q output=\n%s", result.Summary, result.Output)
	}
}

func TestReadFileCapsOpenEndedLogRange(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, defaultLogReadLineCap+20)
	for i := 1; i <= defaultLogReadLineCap+20; i++ {
		lines = append(lines, fmt.Sprintf("line-%03d", i))
	}
	writeTestFile(t, filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

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
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if strings.Contains(result.Output, "100:line-100") || !strings.Contains(result.Summary, "open-ended log range capped") {
		t.Fatalf("expected open-ended log range cap, summary=%q output=\n%s", result.Summary, result.Output)
	}
	if lines := strings.Count(result.Output, "\n") + 1; lines != defaultLogReadLineCap {
		t.Fatalf("expected %d output lines, got %d", defaultLogReadLineCap, lines)
	}
}

func TestReadFileClampsLogTailStartBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"line-001", "line-002", "line-003"}
	writeTestFile(t, filepath.Join(dir, "logs", "server.log"), strings.Join(lines, "\n"))

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
	if result.Error != "" {
		t.Fatalf("expected log tail clamp instead of invalid range, got %#v", result)
	}
	if !strings.Contains(result.Output, "1:line-001") || !strings.Contains(result.Output, "3:line-003") {
		t.Fatalf("expected log tail output after clamp, got:\n%s", result.Output)
	}
}

func TestListFilesCapsLargeOutput(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < listFilesDisplayCap+20; i++ {
		writeTestFile(t, filepath.Join(dir, fmt.Sprintf("file-%03d.go", i)), "package main\n")
	}
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: listFilesDisplayCap + 50,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "."})
	if result.Error != "" {
		t.Fatalf("list_files failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "showing first 80") || !strings.Contains(result.Output, "more path(s) omitted") {
		t.Fatalf("expected capped list_files output, got summary=%q output=\n%s", result.Summary, result.Output)
	}
	if lines := strings.Count(result.Output, "\n") + 1; lines > listFilesDisplayCap+1 {
		t.Fatalf("expected at most %d output lines, got %d:\n%s", listFilesDisplayCap+1, lines, result.Output)
	}
}

func TestExecuteRejectsPathTraversalAndUnsupportedTools(t *testing.T) {
	dir := t.TempDir()
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
	if result.Error == "" {
		t.Fatalf("expected path traversal read to fail: %#v", result)
	}
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "definitely_not_a_tool"})
	if result.Error == "" || !strings.Contains(result.Error, "unsupported tool") {
		t.Fatalf("expected unsupported tool error, got %#v", result)
	}
}

// Fix 1 (read-before-edit) — exercises docs/opencode-fixes-design.md §1.
func TestEditFileRequiresPriorObservationOfPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "bubble.go"), "package bubble\n")
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
	if result.Error == "" || !strings.Contains(result.Error, "requires you to have observed") {
		t.Fatalf("expected read-before-edit rejection on first edit, got %#v", result)
	}

	// A successful read_file marks the path observed.
	read := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "bubble.go"})
	if read.Error != "" {
		t.Fatalf("read_file unexpectedly failed: %#v", read)
	}
	if !mgr.HasObservedPath("bubble.go") {
		t.Fatal("read_file should have marked bubble.go observed")
	}

	result = mgr.Execute(context.Background(), ToolRequest{
		Tool:    "edit_file",
		Path:    "bubble.go",
		OldText: "package bubble",
		NewText: "package bubble2",
	})
	if result.Error != "" {
		t.Fatalf("edit_file should succeed after read_file, got %#v", result)
	}
}

// Fix 2 (fuzzy path suggestions) — exercises docs/opencode-fixes-design.md §2.
func TestEditFileSuggestsNearestPathsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "internal/bubble/bubble.go"), "package bubble\n")
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
	if result.Error == "" {
		t.Fatalf("expected edit_file to fail on missing path, got %#v", result)
	}
	if !strings.Contains(result.Output, "internal/bubble/bubble.go") {
		t.Fatalf("expected nearest-path suggestion in output, got %#v", result)
	}
}

// Extended Fix 2 — fuzzy suggestions must find sibling directories when the
// user fabricates an entire parent directory (the failure mode we saw in
// logs/conversations/20260420-210101.488.jsonl where the model invented
// internal/llm/ in a repo that has no such package).
func TestFindPathSuggestionsSurfacesSiblingDirectoriesOfMissingAncestor(t *testing.T) {
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/runner.go"), "package agent\n")
	mustWriteOverview(t, filepath.Join(dir, "internal/app/app.go"), "package app\n")
	mustWriteOverview(t, filepath.Join(dir, "internal/workspace/manager.go"), "package workspace\n")
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 512,
	})

	suggestions := mgr.findPathSuggestions("internal/llm/llm.go", 5)
	if len(suggestions) == 0 {
		t.Fatal("expected at least one sibling directory hint when an entire parent is fabricated")
	}
	joined := strings.Join(suggestions, " ")
	found := 0
	for _, wanted := range []string{"internal/agent/", "internal/app/", "internal/workspace/"} {
		if strings.Contains(joined, wanted) {
			found++
		}
	}
	if found < 2 {
		t.Fatalf("expected real sibling directories to appear among suggestions, got:\n%v", suggestions)
	}
}

// Adoption fix D - when list_files is called on a fabricated directory,
// the error must explicitly tell the model the directory does not exist
// while keeping the payload descriptive rather than prescriptive.
func TestListFilesErrorNamesMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/runner.go"), "package agent\n")
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 512,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "internal/llm"})
	if result.Error == "" {
		t.Fatalf("expected list_files on fabricated dir to fail, got %#v", result)
	}
	if !strings.Contains(result.Error, "does not exist in this workspace") {
		t.Fatalf("expected clear 'does not exist' framing, got error:\n%s", result.Error)
	}
	if strings.Contains(result.Error, "Do NOT retry") || strings.Contains(result.Error, "Do not retry") {
		t.Fatalf("expected descriptive path-miss error without retry directive, got error:\n%s", result.Error)
	}
}

// Adoption fix D - read_file on a fabricated path whose PARENT directory is
// also missing must call that out explicitly without prescribing recovery.
func TestReadFileErrorNamesMissingParentDirectoryWhenFabricated(t *testing.T) {
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/runner.go"), "package agent\n")
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 512,
		MaxFileBytes:        1 << 20,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "internal/llm/llm.go"})
	if result.Error == "" {
		t.Fatalf("expected read_file on fabricated path to fail, got %#v", result)
	}
	if !strings.Contains(result.Error, "parent directory") || !strings.Contains(result.Error, "does not exist") {
		t.Fatalf("expected 'parent directory ... does not exist' framing, got error:\n%s", result.Error)
	}
	if strings.Contains(result.Error, "Do NOT retry") || strings.Contains(result.Error, "Do not retry") {
		t.Fatalf("expected descriptive parent-miss error without retry directive, got error:\n%s", result.Error)
	}
}

// Extended Fix 2 — list_files and edit_file error paths also surface
// "Did you mean" when a path is missing. Previously only read_file did.
func TestListFilesEmitsPathSuggestionsOnMissingPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/runner.go"), "package agent\n")
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 512,
	})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "list_files", Path: "internal/llm"})
	if result.Error == "" {
		t.Fatalf("expected list_files on fabricated dir to fail, got %#v", result)
	}
	if !strings.Contains(result.Output, "Did you mean") || !strings.Contains(result.Output, "internal/agent") {
		t.Fatalf("expected Did-you-mean output with real siblings, got %#v", result)
	}
}

// TestSearchFilesZeroHitSurfacesClosestPaths verifies the enrichment
// behavior: when the pattern matches no files, the result carries the
// closest real-path candidates so the model can switch to a targeted
// read_file on a concrete path instead of searching again with another
// invented name.
func TestSearchFilesZeroHitSurfacesClosestPaths(t *testing.T) {
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/runner.go"), "package agent\n")
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/tools.go"), "package agent\n")
	mustWriteOverview(t, filepath.Join(dir, "internal/agent/workbranch.go"), "package agent\n")
	mgr := NewManager(config.Workspace{
		Root:                dir,
		CWD:                 dir,
		MaxDirectoryEntries: 512,
	})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "search_files",
		Pattern: "task_tool",
	})
	if result.Error != "" {
		t.Fatalf("search_files should not error on zero-hit: %#v", result)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "no file matches") {
		t.Fatalf("expected zero-hit summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Output, "internal/agent/tools.go") {
		t.Fatalf("expected closest-path suggestion to surface tools.go, got output: %q", result.Output)
	}
	if strings.Contains(strings.ToLower(result.Output), "read_file") {
		t.Fatalf("expected zero-hit output to stay descriptive rather than prescribing read_file, got: %q", result.Output)
	}
}

// TestSearchContentZeroHitSuggestsPathsWhenPatternLooksLikeSymbol ensures
// that when search_content gets no matches and the pattern is a bare
// identifier (not a regex), we surface filename-match suggestions —
// model often confuses "find the symbol" with "grep the symbol" and
// the symbol's name lives in the filename.
func TestSearchContentZeroHitSuggestsPathsWhenPatternLooksLikeSymbol(t *testing.T) {
	if !hasContentSearchBackend() {
		t.Skip("no supported content search backend installed on this machine")
	}
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/app/prompt_routing.go"), "package app\nfunc unrelated() {}\n")
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
	if result.Error != "" {
		t.Fatalf("search_content should not error on zero-hit: %#v", result)
	}
	if !strings.Contains(result.Output, "prompt_routing.go") {
		t.Fatalf("expected filename-match suggestion, got output: %q", result.Output)
	}
}

func TestSearchContentSkipsNoisyDirsByDefault(t *testing.T) {
	if !hasContentSearchBackend() {
		t.Skip("no supported content search backend installed on this machine")
	}
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/app/plan.go"), "package app\nfunc planPanel() {}\n")
	mustWriteOverview(t, filepath.Join(dir, "logs/conversations/recent.jsonl"), strings.Repeat(`{"content":"plan panel duplicated log context"}`, 200)+"\n")

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
	if result.Error != "" {
		t.Fatalf("search_content failed: %#v", result)
	}
	if !strings.Contains(result.Output, "internal/app/plan.go") {
		t.Fatalf("expected source hit, got output: %q", result.Output)
	}
	if strings.Contains(result.Output, "logs/conversations") {
		t.Fatalf("default search should skip logs noise, got output: %q", result.Output)
	}

	logResult := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "search_content",
		Pattern: "plan panel",
		Path:    "logs",
	})
	if logResult.Error != "" {
		t.Fatalf("explicit logs search failed: %#v", logResult)
	}
	if !strings.Contains(logResult.Output, "logs/conversations/recent.jsonl") {
		t.Fatalf("explicit logs search should still work, got output: %q", logResult.Output)
	}
}

func TestSearchContentPrioritizesSourceFilesOverTests(t *testing.T) {
	if !hasContentSearchBackend() {
		t.Skip("no supported content search backend installed on this machine")
	}
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal", "agent", "runner.go"), "package agent\nfunc runChildTask() { /* delegate child spawn */ }\n")
	mustWriteOverview(t, filepath.Join(dir, "internal", "agent", "runner_test.go"), "package agent\nfunc TestRunnerDelegate(t *testing.T) { /* task child */ }\n")

	mgr := NewManager(config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		MaxDirectoryEntries:   512,
		CommandTimeoutSeconds: 10,
	})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "search_content",
		Pattern: "delegate|child|spawn",
		Path:    "internal/agent",
		Glob:    "*.go",
	})
	if result.Error != "" {
		t.Fatalf("search_content failed: %#v", result)
	}
	lines := strings.Split(result.Output, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "internal/agent/runner.go") {
		t.Fatalf("expected source file hit to rank ahead of test file, got output: %q", result.Output)
	}
}

func TestSearchContentSkipsSuiteManifestByDefaultButAllowsExplicitPath(t *testing.T) {
	if !hasContentSearchBackend() {
		t.Skip("no supported content search backend installed on this machine")
	}
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "testdata", "swe", "representative-suite.json"), `{"needle":"SuiteManifestOnlyWidget"}`+"\n")
	mustWriteOverview(t, filepath.Join(dir, "internal", "app", "banner.go"), "package app\n")

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
	if result.Error != "" {
		t.Fatalf("search_content failed: %#v", result)
	}
	if strings.Contains(result.Output, "representative-suite.json") {
		t.Fatalf("default search should skip suite manifest, got output: %q", result.Output)
	}

	explicit := mgr.Execute(context.Background(), ToolRequest{
		Tool:    "search_content",
		Pattern: "SuiteManifestOnlyWidget",
		Path:    "testdata/swe/representative-suite.json",
	})
	if explicit.Error != "" {
		t.Fatalf("explicit suite manifest search failed: %#v", explicit)
	}
	if !strings.Contains(explicit.Output, "representative-suite.json") {
		t.Fatalf("explicit suite manifest search should work, got output: %q", explicit.Output)
	}
}

func TestSearchContentTruncatesLongMatchingLines(t *testing.T) {
	if !hasContentSearchBackend() {
		t.Skip("no supported content search backend installed on this machine")
	}
	dir := t.TempDir()
	mustWriteOverview(t, filepath.Join(dir, "internal/app/huge.go"), "package app\nvar huge = \"needle "+strings.Repeat("x", 800)+"\"\n")
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
	if result.Error != "" {
		t.Fatalf("search_content failed: %#v", result)
	}
	if !strings.Contains(result.Output, "...[line truncated]") {
		t.Fatalf("expected long matching line to be truncated, got output: %q", result.Output)
	}
	if len(result.Output) > 800 {
		t.Fatalf("expected compact search output, got %d chars: %q", len(result.Output), result.Output)
	}
}

func TestLooksLikeBareIdentifierRejectsRegexShapes(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Foo", true},
		{"foo_bar", true},
		{"internal/agent/runner.go", true},
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
			t.Errorf("looksLikeBareIdentifier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func mustWriteOverview(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestReadFileReturnsFullLargeFilesWhenNoRangeGiven(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	mustWriteOverview(t, filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt"})
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "lines 1-700") {
		t.Fatalf("expected full range-less source read, summary=%q", result.Summary)
	}
	if strings.Contains(result.Summary, "file has 700 lines total") {
		t.Fatalf("full source read should not include truncation hint; summary=%q", result.Summary)
	}
}

func TestReadFileHonorsExplicitRangeForLargeFiles(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	mustWriteOverview(t, filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 1, EndLine: 700})
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "lines 1-700") {
		t.Fatalf("explicit range should be preserved; summary=%q", result.Summary)
	}
	if strings.Contains(result.Summary, "file has ") && strings.Contains(result.Summary, "lines total") {
		t.Fatalf("explicit range should not trigger truncation hint; summary=%q", result.Summary)
	}
}

func TestReadFileHonorsExplicitRangeBeyondMaxFileBytes(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	mustWriteOverview(t, filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 80})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 150, EndLine: 152})
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "lines 150-152") {
		t.Fatalf("explicit range should address lines beyond MaxFileBytes; summary=%q", result.Summary)
	}
	if !strings.Contains(result.Output, "150:line 150") || !strings.Contains(result.Output, "152:line 152") {
		t.Fatalf("expected late explicit range, got:\n%s", result.Output)
	}
}

func TestReadFileClampsExplicitRangeBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	mustWriteOverview(t, filepath.Join(dir, "big.txt"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: dir, CWD: dir, MaxFileBytes: 1 << 20})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "big.txt", StartLine: 90, EndLine: 110})
	if result.Error != "" {
		t.Fatalf("read_file failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "clamped to file bounds") {
		t.Fatalf("expected explicit out-of-range read to clamp, summary=%q", result.Summary)
	}
	if !strings.Contains(result.Summary, "lines 30-50") {
		t.Fatalf("expected clamped tail window, summary=%q", result.Summary)
	}
	if !strings.Contains(result.Output, "30:line 30") || !strings.Contains(result.Output, "50:line 50") {
		t.Fatalf("expected clamped output window, got:\n%s", result.Output)
	}
}
