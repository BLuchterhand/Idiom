package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/platform/config"
)

func TestTuiReadApprovalDeciderAutoDeniesInHeadlessMode(t *testing.T) {
	model := &Model{}

	var buf bytes.Buffer
	model.SetJSONEventStream(&buf, "test-session")

	decider := newTuiReadApprovalDecider(model)

	done := make(chan readapproval.Decision, 1)
	go func() {
		decision, err := decider.DecideRead(context.Background(), readapproval.Request{
			SessionID: "test-session",
			Path:      "docs/design-intent-loop-mutation-cycle.md",
			Tool:      "read_file",
			Lane:      "plan-then-execute-planner",
		})
		if err != nil {
			t.Errorf("DecideRead returned err: %v", err)
			return
		}
		done <- decision
	}()

	select {
	case decision := <-done:
		if decision.Outcome != readapproval.OutcomeDenied {
			t.Fatalf("expected headless mode to auto-deny, got %v", decision.Outcome)
		}
		if decision.Path != "docs/design-intent-loop-mutation-cycle.md" {
			t.Fatalf("expected decision to carry the requested path, got %q", decision.Path)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DecideRead blocked in headless mode; auto-deny path is broken")
	}

	emitted := buf.String()
	if !strings.Contains(emitted, "read_approval_requested") {
		t.Fatalf("expected read_approval_requested event in JSON stream, got: %s", emitted)
	}
	if strings.Contains(emitted, "read_approval_decision") {
		t.Fatalf("decider should not emit read_approval_decision; gate owns decision provenance, got: %s", emitted)
	}
	if !strings.Contains(emitted, `"path":"docs/design-intent-loop-mutation-cycle.md"`) {
		t.Fatalf("event should name the requested path, got: %s", emitted)
	}
}

func TestTuiReadApprovalDeciderHandlesNilModel(t *testing.T) {
	decider := newTuiReadApprovalDecider(nil)

	decision, err := decider.DecideRead(context.Background(), readapproval.Request{
		SessionID: "s",
		Path:      "docs/foo.md",
	})
	if err != readapproval.ErrNoDecider {
		t.Fatalf("expected ErrNoDecider, got %v", err)
	}
	if decision.Outcome != readapproval.OutcomeDenied {
		t.Fatalf("expected OutcomeDenied, got %v", decision.Outcome)
	}
	if decision.Reason != readapproval.ReasonNoDeciderWired {
		t.Fatalf("expected ReasonNoDeciderWired, got %q", decision.Reason)
	}
}

func TestNewReadApprovalGateReturnsNilForNilModel(t *testing.T) {
	if got := (*Model)(nil).newReadApprovalGate(); got != nil {
		t.Fatalf("expected nil gate for nil model, got %+v", got)
	}
}

func TestNewReadApprovalGateWiresDeciderAndCache(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	gate := model.newReadApprovalGate()
	if gate == nil {
		t.Fatal("expected non-nil gate for non-nil model")
	}
	if gate.Decider == nil {
		t.Fatal("expected gate to wire a Decider")
	}
	if gate.Cache == nil {
		t.Fatal("expected gate to wire a Cache when workspace is available")
	}
}
