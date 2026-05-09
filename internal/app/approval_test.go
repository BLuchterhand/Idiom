package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"idiom/internal/workspace"
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
