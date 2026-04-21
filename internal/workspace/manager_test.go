package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
)

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

func TestEditFileRejectsInvalidGoBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analysis_agents.go")
	original := "package app\n\nfunc ok() {}\n"
	writeTestFile(t, path, original)
	mgr := NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableWrites: true,
	})
	mgr.MarkPathObserved("analysis_agents.go")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "edit_file",
		Path:      "analysis_agents.go",
		StartLine: 1,
		EndLine:   1,
		Content:   "",
	})
	if result.Error == "" || !strings.Contains(result.Error, "preflight syntax validation failed") {
		t.Fatalf("expected Go syntax preflight rejection, got %#v", result)
	}
	if got := readTestFile(t, path); got != original {
		t.Fatalf("invalid edit should not touch disk\ngot:\n%s\nwant:\n%s", got, original)
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
	if result.Error == "" || !strings.Contains(result.Error, "requires you to have read") {
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

// Adoption fix D — when list_files is called on a fabricated directory,
// the error must explicitly tell the model the directory does not exist
// and forbid probing children of that path. Guards against the failure
// loop in logs/conversations/20260421-053044.479.jsonl where
// list_files(internal/llm) failed and the model then probed
// internal/llm/llm.go and internal/llm/llm_provider.go.
func TestListFilesErrorNamesMissingDirectoryAndForbidsChildProbes(t *testing.T) {
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
	if !strings.Contains(result.Error, "Do NOT retry with children") {
		t.Fatalf("expected explicit 'do not retry children' instruction, got error:\n%s", result.Error)
	}
}

// Adoption fix D — read_file on a fabricated path whose PARENT directory is
// also missing must call that out explicitly so the model stops probing
// siblings of the phantom directory.
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
	if !strings.Contains(result.Error, "Do NOT retry sibling files") {
		t.Fatalf("expected explicit 'do not retry siblings' instruction, got error:\n%s", result.Error)
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

func mustWriteOverview(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Review-lane read cap: when no explicit line range is given, large files
// must be truncated and the summary must tell the model how to continue.
// Prevents the "read full 1720-line runner.go into context" failure mode
// documented in the review of logs/conversations/20260420-210101.488.jsonl.
func TestReadFileCapsLargeFilesWhenNoRangeGiven(t *testing.T) {
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
	if !strings.Contains(result.Summary, fmt.Sprintf("lines 1-%d", defaultLargeReadLineCap)) {
		t.Fatalf("expected soft cap to truncate to lines 1-%d, summary=%q", defaultLargeReadLineCap, result.Summary)
	}
	if !strings.Contains(result.Summary, "file has 700 lines total") {
		t.Fatalf("expected hint to state full file size; summary=%q", result.Summary)
	}
}

// The cap must NOT apply when the caller explicitly asked for a larger
// range — users can still opt in to full-file reads.
func TestReadFileHonorsExplicitRangeOverSoftCap(t *testing.T) {
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
		t.Fatalf("explicit range must override soft cap; summary=%q", result.Summary)
	}
	if strings.Contains(result.Summary, "file has ") && strings.Contains(result.Summary, "lines total") {
		t.Fatalf("explicit range should not trigger truncation hint; summary=%q", result.Summary)
	}
}
