package recovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildPlanExecuteFailureRecordClassifiesPlannerBlocked(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-1",
		ProgressStatePath: ".idiom/plan-execute/sessions/session-1.json",
		Phase:             "planning",
		Status:            "failed",
		FailureKind:       "planner_blocked",
		FailureReason:     "<<QUESTION>>: docs need a human answer",
		RequiredEditPaths: []string{"docs/design.md"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	if record.FailureID != "plan-execute-session-1" {
		t.Fatalf("failure id=%q", record.FailureID)
	}
	if record.Failure.Class != FailureClassPlannerBlocked {
		t.Fatalf("failure class=%q", record.Failure.Class)
	}
	if record.NextAction != NextActionAnswerBlocker {
		t.Fatalf("next action=%q", record.NextAction)
	}
	if len(record.Failure.EvidenceRefs) == 0 || record.Failure.EvidenceRefs[0].Kind != "progress_state" {
		t.Fatalf("expected progress state evidence, got %#v", record.Failure.EvidenceRefs)
	}
}

func TestWritePlanExecuteFailureRecordPreservesCreatedAt(t *testing.T) {
	root := t.TempDir()
	input := PlanExecuteFailureInput{
		SessionID:     "session-2",
		Phase:         "planning",
		Status:        "failed",
		FailureKind:   "plan_artifact_missing",
		FailureReason: "no plan artifact",
	}
	firstTime := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	first, path, err := WritePlanExecuteFailureRecord(root, input, firstTime)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if path != filepath.ToSlash(filepath.Join(".idiom", "recovery", "plan-execute-session-2", "failure.json")) {
		t.Fatalf("record path=%q", path)
	}
	secondTime := firstTime.Add(time.Hour)
	second, _, err := WritePlanExecuteFailureRecord(root, input, secondTime)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if second.CreatedAt != first.CreatedAt {
		t.Fatalf("created_at changed from %q to %q", first.CreatedAt, second.CreatedAt)
	}
	if second.UpdatedAt == first.UpdatedAt {
		t.Fatalf("updated_at did not change")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
		t.Fatalf("stat failure record: %v", err)
	}
}

func TestClassifyPlanExecuteFailureRecognizesBudgetExhaustion(t *testing.T) {
	got := ClassifyPlanExecuteFailure("planner_failed", "completion reached max_tokens before submit_plan", "")
	if got != FailureClassBudgetExhausted {
		t.Fatalf("class=%q", got)
	}
}
