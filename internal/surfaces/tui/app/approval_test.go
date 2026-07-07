package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/modepolicy"
)

func TestRequestApprovalAutoDeniesInHeadlessMode(t *testing.T) {
	// Headless flow: SetJSONEventStream is wired, no TUI is running.
	// requestApproval used to block forever on a channel that only the
	// TUI thread filled. The auto-deny path returns false promptly and
	// emits an approval_auto_denied event so the audit trail captures
	// what happened.
	model := &Model{}

	var buf bytes.Buffer

	model.SetJSONEventStream(&buf, "test-session")

	done := make(chan bool, 1)
	go func() {
		done <- model.requestApproval(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"}, "implement")
	}()

	select {
	case got := <-done:
		if got {
			t.Fatalf("expected auto-deny in headless mode, got true")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("requestApproval blocked in headless mode; auto-deny path is broken")
	}

	emitted := buf.String()
	if !strings.Contains(emitted, "approval_auto_denied") {
		t.Fatalf("expected approval_auto_denied event in JSON stream, got: %s", emitted)
	}

	if !strings.Contains(emitted, `"tool":"edit_file"`) {
		t.Fatalf("event should name the tool, got: %s", emitted)
	}
}

func TestRequestApprovalAutoDeniesInNonInteractivePromptRunner(t *testing.T) {
	model := &Model{}
	model.SetNonInteractive(true)

	done := make(chan bool, 1)
	go func() {
		done <- model.requestApproval(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"}, "implement")
	}()

	select {
	case got := <-done:
		if got {
			t.Fatalf("expected auto-deny in non-interactive mode, got true")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("requestApproval blocked in non-interactive mode")
	}
}

func TestPreExecuteToolRequestConfigDenyOverridesModeAllow(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: ".idiom/deny-read-test.txt", Decision: "deny"},
				},
			},
		}),
	}
	model.workspace.SetPermissionRules(map[string][]toolpolicy.Rule{
		"read_file": {
			{Pattern: "*", Decision: toolpolicy.Allow},
		},
	})

	result := model.preExecuteToolRequest(1, workspace.ToolRequest{
		Tool: "read_file",
		Path: ".idiom/deny-read-test.txt",
	}, modepolicy.Mode{Name: "build"})
	if result == nil {
		t.Fatal("expected TUI pre-execute hook to block configured deny")
	}

	if result.Output != "" {
		t.Fatalf("deny result must not include file output, got %q", result.Output)
	}

	if !strings.Contains(result.Summary, "permission rules") {
		t.Fatalf("unexpected deny summary %q", result.Summary)
	}

	if !strings.Contains(result.Error, "deny read_file") {
		t.Fatalf("unexpected deny error %q", result.Error)
	}
}

func TestPreExecuteToolRequestReportsUnapprovedAskAsWriteApprovalRequired(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: t.TempDir()}),
	}
	model.SetJSONEventStream(&bytes.Buffer{}, "approval-required-test")
	model.workspace.SetProtectedWritePaths([]string{"prompts/base.md"})

	result := model.preExecuteToolRequest(1, workspace.ToolRequest{
		Tool: "edit_file",
		Path: "prompts/base.md",
	}, modepolicy.Mode{Name: "build"})
	if result == nil {
		t.Fatal("expected TUI pre-execute hook to require approval")
	}

	if !strings.Contains(result.Summary, "Write approval required") {
		t.Fatalf("unexpected approval summary %q", result.Summary)
	}

	if !strings.Contains(result.Error, "write approval required") {
		t.Fatalf("unexpected approval error %q", result.Error)
	}
}
