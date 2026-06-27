package planexecute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plancontract "idiom/internal/contracts/plan"
)

func TestTryLoadProgressStateRejectsSessionMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("actual-session", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SessionID = "other-session"

	fullPath, _, err := progressStatePath(root, "actual-session")
	if err != nil {
		t.Fatalf("progressStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, found, err := TryLoadProgressState(root, "actual-session")
	if err == nil || found {
		t.Fatalf("expected session mismatch error and found=false, found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "does not match requested session") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTryLoadProgressStateRejectsUnknownLifecycleStatus(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("bad-status", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Status = plancontract.PlanExecuteProgressStatus("typo")

	writeProgressStateFixture(t, root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err == nil || found {
		t.Fatalf("expected invalid status error and found=false, found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "invalid plan-execute progress status") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTryLoadProgressStateAcceptsMissingSchemaVersion(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("legacy-schema", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SchemaVersion = ""
	writeProgressStateFixture(t, root, state)

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("expected legacy progress state to load, found=%v err=%v", found, err)
	}
	if loaded.SchemaVersion != ProgressStateSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", loaded.SchemaVersion, ProgressStateSchemaVersion)
	}
}

func TestTryLoadProgressStateRejectsUnsupportedSchemaVersion(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("future-schema", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SchemaVersion = "idiom.plan_execute.progress.v99"
	writeProgressStateFixture(t, root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err == nil || found {
		t.Fatalf("expected unsupported schema error and found=false, found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "unsupported plan-execute progress schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTryLoadProgressStateRejectsImpossiblePhaseStatus(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("bad-lifecycle", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhasePlanning
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	writeProgressStateFixture(t, root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	if err == nil || found {
		t.Fatalf("expected invalid lifecycle error and found=false, found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "invalid plan-execute progress lifecycle") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveProgressStateRejectsUnknownLifecyclePhase(t *testing.T) {
	state := NewProgressState("bad-phase", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhase("typo")

	if _, err := SaveProgressState(t.TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress phase") {
		t.Fatalf("expected invalid phase error, got %v", err)
	}
}

func TestSaveProgressStateRejectsUnknownLifecycleStatus(t *testing.T) {
	state := NewProgressState("bad-status-save", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Status = plancontract.PlanExecuteProgressStatus("typo")

	if _, err := SaveProgressState(t.TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress status") {
		t.Fatalf("expected invalid status error, got %v", err)
	}
}

func TestSaveProgressStateRejectsImpossiblePhaseStatus(t *testing.T) {
	state := NewProgressState("bad-lifecycle-save", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhasePlanning
	state.Status = plancontract.PlanExecuteProgressStatusCompleted

	if _, err := SaveProgressState(t.TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress lifecycle") {
		t.Fatalf("expected invalid lifecycle error, got %v", err)
	}
}

func TestMarkReviewCompleteMergesRepairChangedPaths(t *testing.T) {
	state := NewProgressState("session", "prompt", "", []string{"a.go"}, nil, time.Now())
	state.ChangedPaths = []string{"a.go"}

	state.MarkReviewComplete(ReviewRepairPhaseReport{
		Status:               plancontract.PlanExecuteReviewStatusRepaired,
		RepairPaths:          []string{"b.go"},
		CombinedChangedPaths: []string{"a.go", "b.go"},
	}, nil)

	if got := strings.Join(state.ChangedPaths, ","); got != "a.go,b.go" {
		t.Fatalf("ChangedPaths = %q, want a.go,b.go", got)
	}
}

func writeProgressStateFixture(t *testing.T, root string, state ProgressState) {
	t.Helper()

	fullPath, _, err := progressStatePath(root, state.SessionID)
	if err != nil {
		t.Fatalf("progressStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestMarkReviewCompletePersistsTypedDegradedFailureKind(t *testing.T) {
	state := NewProgressState("session", "prompt", "", nil, nil, time.Now())

	state.MarkReviewComplete(ReviewRepairPhaseReport{
		Status:       plancontract.PlanExecuteReviewStatusDegraded,
		StatusDetail: "review unavailable",
		ReasonClass:  "backend_timeout",
	}, nil)

	if state.FailureKind != plancontract.PlanExecuteFailureReviewRepairDegraded {
		t.Fatalf("FailureKind = %q, want %q", state.FailureKind, plancontract.PlanExecuteFailureReviewRepairDegraded)
	}
	if state.Review.ReasonClass != "backend_timeout" {
		t.Fatalf("Review.ReasonClass = %q", state.Review.ReasonClass)
	}
}
