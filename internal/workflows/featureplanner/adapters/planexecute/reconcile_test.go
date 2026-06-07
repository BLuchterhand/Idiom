package planexecute

import (
	"testing"
	"time"

	"idiom/internal/workflows/featureplanner"
	coreplanexecute "idiom/internal/workflows/planexecute"
)

func TestReconcileActiveRunsFoldsBoundTerminalCompletion(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())
	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
		CompletedAt:  time.Now().UTC(),
	})
	if _, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", state.Atoms[0].State, state.Atoms[0].Blockers, events)
	}
	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active runs should be folded: %+v", state.ActiveRuns)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("events = %+v, want atom_completed", events)
	}
}

func TestReconcileActiveRunsIgnoresUnboundOverlappingTerminalCompletion(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("other-run", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())
	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/other-run.json",
		CompletedAt:  time.Now().UTC(),
	})
	if _, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateImplementing {
		t.Fatalf("atom state = %q, want still implementing; events=%+v", state.Atoms[0].State, events)
	}
	if len(state.ActiveRuns) != 1 {
		t.Fatalf("active run should remain: %+v", state.ActiveRuns)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none for unbound session", events)
	}
}

func TestReconcileActiveRunsIgnoresUnboundOverlappingTerminalProgress(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("generated-session", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())
	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	if _, err := coreplanexecute.SaveProgressState(root, &progress); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateImplementing {
		t.Fatalf("atom state = %q, want still implementing; events=%+v", state.Atoms[0].State, events)
	}
	if len(state.ActiveRuns) != 1 {
		t.Fatalf("active run should remain: %+v", state.ActiveRuns)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none for unbound session", events)
	}
}

func TestStringSetsOverlapUsesScopePathNormalization(t *testing.T) {
	if !stringSetsOverlap([]string{`pkg\feature.go`}, []string{"./pkg/feature.go"}) {
		t.Fatalf("expected overlap to use canonical scope path normalization")
	}
	if stringSetsOverlap([]string{"../pkg/feature.go"}, []string{"pkg/feature.go"}) {
		t.Fatalf("escaped paths must not overlap canonical workspace paths")
	}
}

func featureStateWithActivePlanExecuteRunForTest(runID string) featureplanner.FeatureState {
	started := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	return featureplanner.FeatureState{
		SessionID: "feature-session",
		Status:    featureplanner.FeatureStateDispatching,
		Atoms: []featureplanner.FeatureAtomState{{
			FeatureAtomPlan: featureplanner.FeatureAtomPlan{
				ID:                "atom-1",
				RequiredEditPaths: []string{"pkg/feature.go"},
			},
			State:     featureplanner.AtomStateImplementing,
			LastRunID: runID,
		}},
		ActiveRuns: []featureplanner.ActiveRun{{
			RunID:         runID,
			AtomID:        "atom-1",
			LaneSessionID: runID,
			LaneFamily:    "plan-execute",
			State:         "running",
			StartedAt:     started,
		}},
	}
}
