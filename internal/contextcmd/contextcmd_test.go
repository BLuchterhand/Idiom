package contextcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/workspace"
)

func TestResolveTaskRejectsTaskAndTaskFile(t *testing.T) {
	_, err := resolveTask("inline", filepath.Join(t.TempDir(), "task.txt"))
	if err == nil {
		t.Fatal("expected task/task-file conflict")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveTaskReadsTaskFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(path, []byte("  inspect context graph  \n"), 0o644); err != nil {
		t.Fatalf("write task file: %v", err)
	}

	got, err := resolveTask("", path)
	if err != nil {
		t.Fatalf("resolve task: %v", err)
	}
	if got != "inspect context graph" {
		t.Fatalf("resolveTask() = %q", got)
	}
}

func TestResultBytesPrefersToolOutput(t *testing.T) {
	got, err := resultBytes(workspace.ToolResult{
		Tool:   "context_status",
		Output: "{\"fresh\":true}\n\n",
	})
	if err != nil {
		t.Fatalf("resultBytes: %v", err)
	}
	if string(got) != "{\"fresh\":true}\n" {
		t.Fatalf("unexpected output bytes: %q", string(got))
	}
}
