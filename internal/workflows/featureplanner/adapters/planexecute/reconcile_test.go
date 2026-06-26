package planexecute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/featureplanner"
	coreplanexecute "idiom/internal/workflows/planexecute"
)

func TestReconcileActiveRunsFoldsBoundTerminalCompletion(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	progress.Review.Status = coreplanexecute.ProgressStatusCompleted
	progress.Executor.SourceSanity = []coreplanexecute.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}

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

	if len(events) != 2 ||
		events[0].Type != "idiom.feature_planner.atom_completed" ||
		events[1].Type != "idiom.feature_planner.release_blocked" {
		t.Fatalf("events = %+v, want atom_completed and release_blocked", events)
	}
}

func TestReconcileActiveRunsOverlaysActiveParentAuthorityForDivergentTerminalScope(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	run := state.ActiveRuns[0]
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	progress.ScopeContractID = "child-scope-contract"
	progress.ScopeContractHash = "child-scope-hash"
	markProgressTerminalEvidenceForTest(&progress)

	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
		CompletedAt:  time.Now().UTC(),
	})

	completionPath, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	payload := encodeTerminalCompletion(record, completionPath, run)
	fields := decodeCompletionPayloadForTest(t, payload)

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldContractID); got != run.InvocationContractID {
		t.Fatalf("contract_id = %q, want active invocation %q; payload=%s", got, run.InvocationContractID, payload)
	}

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldSnapshotHash); got != run.RuntimeEnvelopeID {
		t.Fatalf("snapshot_hash = %q, want active snapshot %q; payload=%s", got, run.RuntimeEnvelopeID, payload)
	}

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldScopeContractID); got != "child-scope-contract" {
		t.Fatalf("scope_contract_id = %q, want preserved child scope; payload=%s", got, payload)
	}

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldScopeContractHash); got != "child-scope-hash" {
		t.Fatalf("scope_contract_hash = %q, want preserved child scope hash; payload=%s", got, payload)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", state.Atoms[0].State, state.Atoms[0].Blockers, events)
	}

	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active runs should be folded: %+v", state.ActiveRuns)
	}
}

func TestEncodeTerminalCompletionDoesNotTrustFailedAcceptanceEvidence(t *testing.T) {
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	run := state.ActiveRuns[0]
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusFailed
	progress.ChangedPaths = []string{"pkg/feature.go"}

	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath:          ".idiom/plan-execute/sessions/atom-1-run-1.json",
		AcceptanceEvidenceIDs: []string{"req-1"},
		CompletedAt:           time.Now().UTC(),
	})

	record.TerminalClass = coreplanexecute.PlanExecuteTerminalClassFailed
	record.Outcome = coreplanexecute.PlanExecuteTerminalOutcomeFailed

	payload := encodeTerminalCompletion(record, ".idiom/plan-execute/completions/atom-1-run-1.json", run)
	fields := decodeCompletionPayloadForTest(t, payload)

	if fields[featureplanner.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted] == true {
		t.Fatalf("failed terminal completion should not trust acceptance evidence: %s", payload)
	}
}

func TestReconcileActiveRunsOverlaysActiveParentAuthorityForDivergentProgressScope(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	run := state.ActiveRuns[0]
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	progress.ScopeContractID = "child-progress-scope-contract"
	progress.ScopeContractHash = "child-progress-scope-hash"
	markProgressTerminalEvidenceForTest(&progress)

	if _, err := coreplanexecute.SaveProgressState(root, &progress); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	payload := encodeProgressCompletion(progress, ".idiom/plan-execute/sessions/atom-1-run-1.json", run)
	fields := decodeCompletionPayloadForTest(t, payload)

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldContractID); got != run.InvocationContractID {
		t.Fatalf("contract_id = %q, want active invocation %q; payload=%s", got, run.InvocationContractID, payload)
	}

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldSnapshotHash); got != run.RuntimeEnvelopeID {
		t.Fatalf("snapshot_hash = %q, want active snapshot %q; payload=%s", got, run.RuntimeEnvelopeID, payload)
	}

	if got := featureplanner.CompletionFieldString(fields, featureplanner.PlanExecuteCompletionFieldScopeContractID); got != "child-progress-scope-contract" {
		t.Fatalf("scope_contract_id = %q, want preserved child scope; payload=%s", got, payload)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", state.Atoms[0].State, state.Atoms[0].Blockers, events)
	}

	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active runs should be folded: %+v", state.ActiveRuns)
	}
}

func TestReconcileActiveRunsRejectsStaleOrMissingTimestampTerminalSidecar(t *testing.T) {
	for _, tt := range []struct {
		name     string
		mutateTS func(*coreplanexecute.PlanExecuteTerminalCompletionRecord)
	}{
		{
			name: "missing ts",
			mutateTS: func(record *coreplanexecute.PlanExecuteTerminalCompletionRecord) {
				record.TS = ""
			},
		},
		{
			name: "unparseable ts",
			mutateTS: func(record *coreplanexecute.PlanExecuteTerminalCompletionRecord) {
				record.TS = "not-a-timestamp"
			},
		},
		{
			name: "stale ts",
			mutateTS: func(record *coreplanexecute.PlanExecuteTerminalCompletionRecord) {
				record.TS = time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
			progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

			progress.Phase = coreplanexecute.ProgressPhaseComplete
			progress.Status = coreplanexecute.ProgressStatusCompleted
			progress.ChangedPaths = []string{"pkg/feature.go"}
			markProgressTerminalEvidenceForTest(&progress)

			record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
				ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
				CompletedAt:  time.Now().UTC(),
			})
			tt.mutateTS(&record)

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
				t.Fatalf("events = %+v, want none for stale or invalid terminal sidecar", events)
			}
		})
	}
}

func TestReconcileActiveRunsPreservesExplicitWrongParentAuthorityForRejection(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	markProgressTerminalEvidenceForTest(&progress)

	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
		CompletedAt:  time.Now().UTC(),
	})

	completionPath, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	overlayTerminalCompletionJSONForTest(t, root, completionPath, map[string]any{
		"contract_id": "wrong-parent-contract",
	})

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open; blockers=%+v events=%+v", state.Atoms[0].State, state.Atoms[0].Blockers, events)
	}

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.plan_execute_completion_rejected" {
		t.Fatalf("events = %+v, want plan_execute_completion_rejected", events)
	}

	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded after rejection: %+v", state.ActiveRuns)
	}
}

func TestReconcileActiveRunsRejectsWrongLegacyContractHashAfterOverlay(t *testing.T) {
	root := t.TempDir()
	state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
	progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	progress.Phase = coreplanexecute.ProgressPhaseComplete
	progress.Status = coreplanexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	markProgressTerminalEvidenceForTest(&progress)

	record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
		CompletedAt:  time.Now().UTC(),
	})

	record.ContractHash = "wrong-legacy-snapshot"

	if _, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	events := ReconcileActiveRuns(&state, root)

	if state.Atoms[0].State != featureplanner.AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open; blockers=%+v events=%+v", state.Atoms[0].State, state.Atoms[0].Blockers, events)
	}

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.plan_execute_completion_rejected" {
		t.Fatalf("events = %+v, want plan_execute_completion_rejected", events)
	}

	if !strings.Contains(events[0].Message, "conflicts with the active plan contract snapshot") {
		t.Fatalf("event did not surface legacy contract_hash conflict: %+v", events[0])
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
	plan := featureplanner.FeaturePlan{
		FeatureID: "feature-session",
		Atoms: []featureplanner.FeatureAtomPlan{{
			ID:                "atom-1",
			RequiredEditPaths: []string{"pkg/feature.go"},
		}},
	}
	contract := featureplanner.StampPlanAuthorityContract(&plan, nil)
	atomContract, _ := contractAtomForTest(contract, "atom-1")

	return featureplanner.FeatureState{
		SessionID: "feature-session",
		Status:    featureplanner.FeatureStateDispatching,
		Plan:      plan,
		Atoms: []featureplanner.FeatureAtomState{{
			FeatureAtomPlan: plan.Atoms[0],
			State:           featureplanner.AtomStateImplementing,
			LastRunID:       runID,
		}},
		ActiveRuns: []featureplanner.ActiveRun{{
			RunID:                runID,
			AtomID:               "atom-1",
			LaneSessionID:        runID,
			LaneFamily:           featureplanner.PlanExecuteCompletionLaneFamily,
			InvocationContractID: atomContract.Scope.ContractID,
			RuntimeEnvelopeID:    contract.ContractHash,
			State:                featureplanner.ActiveRunStateRunning,
			StartedAt:            started,
		}},
	}
}

func contractAtomForTest(contract plancontract.PlanExecutionContract, atomID string) (plancontract.AtomExecutionContract, bool) {
	for _, atom := range contract.Atoms {
		if atom.AtomID == atomID {
			return atom, true
		}
	}

	return plancontract.AtomExecutionContract{}, false
}

func markProgressTerminalEvidenceForTest(progress *coreplanexecute.ProgressState) {
	progress.Review.Status = coreplanexecute.ProgressStatusCompleted
	progress.Executor.SourceSanity = []coreplanexecute.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
}

func decodeCompletionPayloadForTest(t *testing.T, payload string) map[string]any {
	t.Helper()

	var fields map[string]any

	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("decode completion payload: %v\npayload=%s", err, payload)
	}

	return fields
}

func overlayTerminalCompletionJSONForTest(t *testing.T, root string, relPath string, overlay map[string]any) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relPath))

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read terminal completion: %v", err)
	}

	var fields map[string]any

	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode terminal completion: %v", err)
	}

	for key, value := range overlay {
		fields[key] = value
	}

	body, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatalf("encode terminal completion: %v", err)
	}

	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("write terminal completion: %v", err)
	}
}
