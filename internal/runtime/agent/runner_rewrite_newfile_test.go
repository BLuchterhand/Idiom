package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

// TestRewriteReadFileForPlannedNewFileFiresOnRequiredPath verifies
// the Phase 7.3 / harness-fix-#7 contract: a "no such file" error
// on a read targeting a required_edit_path becomes an actionable
// nudge naming the whole-file shape on submit_plan, NOT a bare
// "file not found" the planner treats as a blocker.
func TestRewriteReadFileForPlannedNewFileFiresOnRequiredPath(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "cmd/idiom/init.go"}
	result := workspace.ToolResult{Tool: "read_file", Error: "no such file or directory"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"cmd/idiom/init.go", "cmd/idiom/main.go"},
	}
	got := rewriteMissingRequiredNewFileProbe(req, result, brief)

	if !strings.Contains(got.Error, "NEW FILE") {
		t.Fatalf("rewrite should call out the new-file shape; got: %s", got.Error)
	}

	if !strings.Contains(got.Error, "replace_all") {
		t.Fatalf("rewrite should mention replace_all; got: %s", got.Error)
	}

	if !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("rewrite should point at submit_plan; got: %s", got.Error)
	}

	if !strings.Contains(got.Error, "missing body evidence") {
		t.Fatalf("rewrite should point at the new-file body evidence contract; got: %s", got.Error)
	}
}

// TestRewriteReadFileForPlannedNewFileLeavesUnrelatedErrorsAlone
// verifies the gate's selectivity: errors on paths NOT in
// required_edit_paths pass through unchanged so the planner sees
// the original "did-you-mean" diagnostics.
func TestRewriteReadFileForPlannedNewFileLeavesUnrelatedErrorsAlone(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "some/other/path.go"}
	original := workspace.ToolResult{Tool: "read_file", Error: "no such file"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"cmd/idiom/init.go"},
	}
	got := rewriteMissingRequiredNewFileProbe(req, original, brief)

	if got.Error != original.Error {
		t.Fatalf("error on unrelated path should pass through; got rewrite: %s", got.Error)
	}
}

func TestRewriteMissingRequiredNewFileProbeFiresOnMissingParentOutline(t *testing.T) {
	req := workspace.ToolRequest{Tool: "go_file_outline", Path: "./internal/contracts/writerroute"}
	original := workspace.ToolResult{Tool: "go_file_outline", Error: "open /repo/internal/contracts/writerroute: no such file or directory"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{
			"internal/contracts/writerroute/decision.go",
			"internal/contracts/writerroute/decision_test.go",
			"internal/surfaces/tui/app/writer_route.go",
		},
	}
	got := rewriteMissingRequiredNewFileProbe(req, original, brief)

	for _, want := range []string{
		"Required new-file directory does not exist yet",
		"internal/contracts/writerroute/decision.go",
		"internal/contracts/writerroute/decision_test.go",
		"submit_plan",
		"replace_all",
		"Do NOT keep trying to inspect",
	} {
		if !strings.Contains(got.Summary+"\n"+got.Error, want) {
			t.Fatalf("rewritten result missing %q:\n%#v", want, got)
		}
	}

	if strings.Contains(got.Error, "internal/surfaces/tui/app/writer_route.go") {
		t.Fatalf("rewrite should only list required paths under the missing probe target, got: %s", got.Error)
	}
}

func TestUnavailableRequiredNewFileProbeNudgesMissingParent(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	req := workspace.ToolRequest{Tool: "go_file_outline", Path: "./internal/contracts/writerroute"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{
			"internal/contracts/writerroute/decision.go",
			"internal/surfaces/tui/app/writer_route.go",
		},
	}

	summary, errorText, ok := unavailableRequiredNewFileProbeNudge(req, brief, manager)

	if !ok {
		t.Fatal("expected unavailable required new-file probe nudge")
	}

	for _, want := range []string{
		"Required new-file directory does not exist yet",
		"internal/contracts/writerroute/decision.go",
		"go_file_outline",
		"submit_plan",
		"replace_all",
	} {
		if !strings.Contains(summary+"\n"+errorText, want) {
			t.Fatalf("nudge missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if strings.Contains(errorText, "internal/surfaces/tui/app/writer_route.go") {
		t.Fatalf("nudge should only list required paths under the missing probe target, got: %s", errorText)
	}
}

func TestUnavailableRequiredNewFileProbeDoesNotMaskExistingTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "contracts", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	req := workspace.ToolRequest{Tool: "go_file_outline", Path: "./internal/contracts/writerroute"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision.go"},
	}

	_, _, ok := unavailableRequiredNewFileProbeNudge(req, brief, manager)

	if ok {
		t.Fatal("existing target should keep the original unavailable-tool rejection")
	}
}

// TestRewriteMissingRequiredNewFileProbeIgnoresUnrelatedTools verifies
// the gate doesn't fire on unrelated tools' errors.
func TestRewriteMissingRequiredNewFileProbeIgnoresUnrelatedTools(t *testing.T) {
	req := workspace.ToolRequest{Tool: "search_content", Path: "cmd/idiom/init.go"}
	original := workspace.ToolResult{Tool: "search_content", Error: "no such file"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"cmd/idiom/init.go"},
	}
	got := rewriteMissingRequiredNewFileProbe(req, original, brief)

	if got.Error != original.Error {
		t.Fatalf("non-read_file tools should pass through; got: %s", got.Error)
	}
}
