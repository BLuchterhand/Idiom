package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plancontract "idiom/internal/contracts/plan"
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
		if event["type"] != plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPreflightFailed) {
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

func TestEmitPlanExecuteJSONEventReturnsMarshalError(t *testing.T) {
	var stdout bytes.Buffer
	err := emitPlanExecuteJSONEvent(&stdout, true, plancontract.PlanExecuteEventVerification, map[string]any{
		"bad": func() {},
	})
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on marshal failure", stdout.String())
	}
}

func TestEmitPlanExecuteJSONEventRejectsUnknownEventType(t *testing.T) {
	var stdout bytes.Buffer
	err := emitPlanExecuteJSONEvent(&stdout, true, plancontract.PlanExecuteEventType("typo_event"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown plan-execute event type") {
		t.Fatalf("expected unknown event type error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on unknown event type", stdout.String())
	}
}

func TestEmitPlanExecuteInvalidJSONEventRejectsUnknownEventType(t *testing.T) {
	var stdout bytes.Buffer
	err := emitPlanExecuteInvalidJSONEvent(&stdout, true, plancontract.PlanExecuteEventType("typo_event"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown plan-execute invalid event type") {
		t.Fatalf("expected unknown invalid event type error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on unknown invalid event type", stdout.String())
	}
}

func TestRunPlanExecuteVerificationCommandTimesOut(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module timeoutverify\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "slow"), 0o755); err != nil {
		t.Fatalf("mkdir slow: %v", err)
	}
	testSource := `package slow

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(5 * time.Second)
}
`
	if err := os.WriteFile(filepath.Join(root, "slow", "slow_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatalf("write slow test: %v", err)
	}

	check := runPlanExecuteVerificationCommandWithTimeout(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test ./slow",
		Argv:    []string{"go", "test", "./slow"},
	}, 200*time.Millisecond)

	if check.Status != "failed" {
		t.Fatalf("status = %q, want failed", check.Status)
	}
	if !strings.Contains(check.Error, "timed out") {
		t.Fatalf("error = %q, want timeout", check.Error)
	}
}

func TestRunPlanExecuteVerificationCommandUsesWorkingDir(t *testing.T) {
	root := t.TempDir()
	writePlanExecuteVerificationModule(t, root, "workingdir")
	writePlanExecuteVerificationFile(t, root, "pkg/pkg_test.go", `package pkg

import "testing"

func TestPass(t *testing.T) {}
`)

	check := runPlanExecuteVerificationCommandWithTimeout(context.Background(), root, plancontract.VerificationCommand{
		Command:    "go test -count=1 .",
		WorkingDir: "pkg",
	}, 10*time.Second)

	if check.Status != plancontract.PhaseEvidenceStatusOK {
		t.Fatalf("status = %q, want ok; check=%#v", check.Status, check)
	}
	if check.WorkingDir != "pkg" {
		t.Fatalf("WorkingDir = %q, want pkg", check.WorkingDir)
	}
	if !strings.Contains(check.Summary, "ok") {
		t.Fatalf("summary should contain go test output, got %q", check.Summary)
	}
}

func TestRunPlanExecuteVerificationCommandRejectsUnsafeWorkingDir(t *testing.T) {
	root := t.TempDir()
	writePlanExecuteVerificationModule(t, root, "unsafeworkdir")

	check := runPlanExecuteVerificationCommandWithTimeout(context.Background(), root, plancontract.VerificationCommand{
		Command:    "go test ./...",
		WorkingDir: "../escape",
	}, 10*time.Second)

	if check.Status != plancontract.PhaseEvidenceStatusFailed {
		t.Fatalf("status = %q, want failed", check.Status)
	}
	if check.Error != "unsafe verification working_dir" {
		t.Fatalf("error = %q, want unsafe verification working_dir", check.Error)
	}
}

func TestRunPlanExecuteVerificationCommandFailureUsesCombinedOutput(t *testing.T) {
	root := t.TempDir()
	writePlanExecuteVerificationModule(t, root, "failureoutput")
	writePlanExecuteVerificationFile(t, root, "pkg/pkg_test.go", `package pkg

import (
	"fmt"
	"os"
	"testing"
)

func TestFail(t *testing.T) {
	fmt.Println("STDOUT_MARKER")
	fmt.Fprintln(os.Stderr, "STDERR_MARKER")
	t.Fatal("FAIL_MARKER")
}
`)

	check := runPlanExecuteVerificationCommandWithTimeout(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test -count=1 ./pkg",
	}, 10*time.Second)

	if check.Status != plancontract.PhaseEvidenceStatusFailed {
		t.Fatalf("status = %q, want failed", check.Status)
	}
	for _, want := range []string{"STDOUT_MARKER", "STDERR_MARKER", "FAIL_MARKER"} {
		if !strings.Contains(check.Error, want) {
			t.Fatalf("error should include %q from combined output, got %q", want, check.Error)
		}
	}
	if !strings.Contains(check.Summary, "exit status") {
		t.Fatalf("summary should preserve process error, got %q", check.Summary)
	}
}

func TestRunPlanExecuteVerificationCommandTruncatesOutput(t *testing.T) {
	root := t.TempDir()
	writePlanExecuteVerificationModule(t, root, "truncateverify")
	writePlanExecuteVerificationFile(t, root, "pkg/pkg_test.go", `package pkg

import (
	"strings"
	"testing"
)

func TestChatty(t *testing.T) {
	t.Log(strings.Repeat("x", 13000))
}
`)

	check := runPlanExecuteVerificationCommandWithTimeout(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test -count=1 -v ./pkg",
	}, 10*time.Second)

	if check.Status != plancontract.PhaseEvidenceStatusOK {
		t.Fatalf("status = %q, want ok; check=%#v", check.Status, check)
	}
	if !strings.Contains(check.Summary, "[truncated") {
		t.Fatalf("summary should include truncation marker, len=%d summary tail=%q", len(check.Summary), check.Summary[max(0, len(check.Summary)-80):])
	}
}

func writePlanExecuteVerificationModule(t *testing.T, root string, module string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func writePlanExecuteVerificationFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
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
