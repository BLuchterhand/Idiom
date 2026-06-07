package planexecute

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPlanExecuteTerminalCompletionRecordClassifiesProgressStateBranches(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		state       ProgressState
		input       PlanExecuteTerminalCompletionInput
		wantClass   PlanExecuteTerminalClass
		wantOutcome PlanExecuteTerminalOutcome
	}{
		{
			name: "completed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = ProgressPhaseComplete
				state.Status = ProgressStatusCompleted
			}),
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeCompleted,
		},
		{
			name: "source satisfied explicit input",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = ProgressPhaseComplete
				state.Status = ProgressStatusCompleted
			}),
			input: PlanExecuteTerminalCompletionInput{
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: []string{"pkg/feature.go", "pkg/feature.go", " "},
				CompletedAt:                  now,
			},
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeSourceSatisfied,
		},
		{
			name: "review skipped by design",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkSkippedReview()
			}),
			wantClass:   PlanExecuteTerminalClassSuccess,
			wantOutcome: PlanExecuteTerminalOutcomeReviewSkippedByDesign,
		},
		{
			name: "review degraded",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: "degraded (review unavailable)"}, nil)
			}),
			wantClass:   PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome: PlanExecuteTerminalOutcomeReviewDegraded,
		},
		{
			name: "blocked",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: "blocked: missing approval"}, nil)
			}),
			wantClass:   PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome: PlanExecuteTerminalOutcomeBlocked,
		},
		{
			name: "failed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed("planner_failed", "planner model unavailable")
			}),
			wantClass:   PlanExecuteTerminalClassFailed,
			wantOutcome: PlanExecuteTerminalOutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := NewPlanExecuteTerminalCompletionRecord(tt.state, tt.input)
			if record.TerminalClass != tt.wantClass || record.Outcome != tt.wantOutcome {
				t.Fatalf("terminal classification = class %q outcome %q, want class %q outcome %q; record=%#v", record.TerminalClass, record.Outcome, tt.wantClass, tt.wantOutcome, record)
			}
			if record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion || record.Type != PlanExecuteTerminalCompletionRecordType {
				t.Fatalf("record schema/type missing: %#v", record)
			}
		})
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordDefaultsAndTrims(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		SessionID:                    " session-1 ",
		Status:                       " complete ",
		SourceSatisfiedEvidencePaths: []string{" a.go ", "", "a.go", "../escape.go"},
		ChangedPaths:                 []string{` b.go `, `b.go`, `pkg\feature.go`, "pkg/./bad.go"},
	})

	if record.SchemaVersion != PlanExecuteTerminalCompletionSchemaVersion || record.Type != PlanExecuteTerminalCompletionRecordType {
		t.Fatalf("normalizer should fill schema/type, got %#v", record)
	}
	if record.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want trimmed session-1", record.SessionID)
	}
	if record.TerminalClass != PlanExecuteTerminalClassSuccess || record.Outcome != PlanExecuteTerminalOutcomeCompleted {
		t.Fatalf("classification = class %q outcome %q, want completed/completed", record.TerminalClass, record.Outcome)
	}
	if len(record.SourceSatisfiedEvidencePaths) != 1 || record.SourceSatisfiedEvidencePaths[0] != "a.go" {
		t.Fatalf("source evidence paths not normalized: %#v", record.SourceSatisfiedEvidencePaths)
	}
	if len(record.ChangedPaths) != 2 || record.ChangedPaths[0] != "b.go" || record.ChangedPaths[1] != "pkg/feature.go" {
		t.Fatalf("changed paths not normalized: %#v", record.ChangedPaths)
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordReclassifiesContradictions(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		TerminalClass: PlanExecuteTerminalClassSuccess,
		Outcome:       PlanExecuteTerminalOutcomeCompleted,
		Status:        ProgressStatusCompleted,
		FailureKind:   "review_model_error",
		FailureReason: "review model returned invalid JSON",
	})

	if record.TerminalClass != PlanExecuteTerminalClassFailed || record.Outcome != PlanExecuteTerminalOutcomeFailed {
		t.Fatalf("classification = class %q outcome %q, want failed/failed", record.TerminalClass, record.Outcome)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromFieldsNormalizesStatePath(t *testing.T) {
	record := PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"state_path":            ".idiom/plan-execute/sessions/field-test.json",
		"status":                "completed",
		"review_status":         "skipped",
		"review_repair_skipped": true,
		"changed_paths":         []any{"pkg/feature.go", "pkg/feature.go"},
	})

	if record.ProgressPath != ".idiom/plan-execute/sessions/field-test.json" {
		t.Fatalf("ProgressPath = %q, want state_path fallback", record.ProgressPath)
	}
	if record.TerminalClass != PlanExecuteTerminalClassSuccess || record.Outcome != PlanExecuteTerminalOutcomeReviewSkippedByDesign {
		t.Fatalf("classification = class %q outcome %q, want skipped-by-design success", record.TerminalClass, record.Outcome)
	}
	if len(record.ChangedPaths) != 1 || record.ChangedPaths[0] != "pkg/feature.go" {
		t.Fatalf("ChangedPaths = %#v, want deduped path", record.ChangedPaths)
	}
}

func TestPlanExecuteTerminalCompletionRecordFromProgressStateRequiresTerminalState(t *testing.T) {
	running := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = ProgressStatusRunning
		state.Phase = ProgressPhaseExecutor
	})
	if _, ok := PlanExecuteTerminalCompletionRecordFromProgressState(running, PlanExecuteTerminalCompletionInput{}); ok {
		t.Fatalf("running progress must not synthesize terminal completion")
	}

	completed := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = ProgressStatusCompleted
		state.Phase = ProgressPhaseComplete
	})
	record, ok := PlanExecuteTerminalCompletionRecordFromProgressState(completed, PlanExecuteTerminalCompletionInput{ProgressPath: "progress.json"})
	if !ok {
		t.Fatalf("completed progress should synthesize terminal completion")
	}
	if record.ProgressPath != "progress.json" || record.TerminalClass != PlanExecuteTerminalClassSuccess {
		t.Fatalf("record = %#v, want terminal success with progress path", record)
	}
}

func TestNormalizePlanExecuteTerminalCompletionRecordKeepsDegradedOutcomeIdempotent(t *testing.T) {
	record := NormalizePlanExecuteTerminalCompletionRecord(PlanExecuteTerminalCompletionRecord{
		Status:       ProgressStatusDegraded,
		ReviewStatus: "degraded (review unavailable)",
		FailureKind:  "review_repair_degraded",
	})
	record = NormalizePlanExecuteTerminalCompletionRecord(record)

	if record.TerminalClass != PlanExecuteTerminalClassRecoveryRequired || record.Outcome != PlanExecuteTerminalOutcomeReviewDegraded {
		t.Fatalf("degraded terminal record was not idempotent: %#v", record)
	}
}

func TestSavePlanExecuteTerminalCompletionRecordRoundTrips(t *testing.T) {
	root := t.TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Status = ProgressStatusBlocked
			state.FailureKind = "review_repair_blocked"
			state.FailureReason = "review repair remained blocked"
			state.SetWriteAllowedPaths([]string{"logs/telemetry/run.jsonl", "logs/telemetry/run.jsonl"})
		}),
		PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/terminal_completion_test.json",
			ExitCode:     1,
			FailureID:    "failure-123",
			RecordPath:   ".idiom/recovery/failures/failure-123.json",
			PacketPath:   ".idiom/recovery/packets/failure-123.json",
			NextAction:   "review_decomposition",
		},
	)
	path, err := SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}
	if want := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", "terminal_completion_test.terminal.json")); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	loaded, loadedPath, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "terminal_completion_test")
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err)
	}
	if loadedPath != path {
		t.Fatalf("loaded path = %q, want %q", loadedPath, path)
	}
	if loaded.TerminalClass != PlanExecuteTerminalClassRecoveryRequired || loaded.Outcome != PlanExecuteTerminalOutcomeBlocked {
		t.Fatalf("loaded classification = class %q outcome %q", loaded.TerminalClass, loaded.Outcome)
	}
	if loaded.FailureID != "failure-123" || loaded.PacketPath == "" || loaded.NextAction != "review_decomposition" {
		t.Fatalf("loaded recovery metadata incomplete: %#v", loaded)
	}
	if len(loaded.WriteAllowedPaths) != 1 || loaded.WriteAllowedPaths[0] != "logs/telemetry/run.jsonl" {
		t.Fatalf("write allowed paths not normalized: %#v", loaded.WriteAllowedPaths)
	}
}

func progressStateForTerminalCompletionTest(mutator func(*ProgressState)) ProgressState {
	state := NewProgressState("terminal_completion_test", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.ChangedPaths = []string{"pkg/feature.go"}
	mutator(&state)
	return state
}
