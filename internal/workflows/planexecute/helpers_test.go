package planexecute

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmitExecutorPreflightFailure(t *testing.T) {
	mismatch := &PlanEditPreflightMismatch{
		Index:  2,
		Path:   "pkg/target.go",
		Reason: "outside allowed scope",
	}

	t.Run("default event fields", func(t *testing.T) {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, true, mismatch, "allowed_edit_paths pkg/target.go", nil)

		event := decodeSinglePlanExecuteEventForTest(t, stdout.String())
		if event["type"] != "idiom.plan_execute.preflight_failed" {
			t.Fatalf("type = %v", event["type"])
		}
		if event["edit_index"] != float64(2) || event["path"] != "pkg/target.go" || event["reason"] != "outside allowed scope" {
			t.Fatalf("event fields = %+v", event)
		}
	})

	t.Run("custom event fields", func(t *testing.T) {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, true, mismatch, "approval/scope pkg/target.go", map[string]any{
			"edit_index":        mismatch.Index,
			"path":              "pkg/target.go",
			"reason":            mismatch.Reason,
			"handoff_id":        "handoff-1",
			"approval_scope_id": "approval-1",
		})

		event := decodeSinglePlanExecuteEventForTest(t, stdout.String())
		if event["handoff_id"] != "handoff-1" || event["approval_scope_id"] != "approval-1" {
			t.Fatalf("custom event fields = %+v", event)
		}
	})

	t.Run("human output", func(t *testing.T) {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, false, mismatch, "edit 3/4 pkg/target.go", nil)

		if got, want := stdout.String(), "  preflight FAILED on edit 3/4 pkg/target.go: outside allowed scope\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}

func decodeSinglePlanExecuteEventForTest(t *testing.T, output string) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one event line, got %d: %q", len(lines), output)
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	return event
}
