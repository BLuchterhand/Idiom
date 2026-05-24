package featureplanner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateDurablePlanRequiresPreDispatchBaseline(t *testing.T) {
	plan := durablePlan()
	plan.Atoms[0].TestFirst.PreExecutionEventIDs = nil

	err := ValidateDurablePlan(plan)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "test_first.pre_execution_event_ids") {
		t.Fatalf("expected pre-execution event error, got %v", err)
	}
}

func TestValidateDurablePlanRejectsMismatchedTDDSnapshot(t *testing.T) {
	plan := durablePlan()
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"go test ./wrong"}

	err := ValidateDurablePlan(plan)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "planned_tdd_snapshot.test_commands") {
		t.Fatalf("expected snapshot command mismatch, got %v", err)
	}
}

func TestReplayRejectsStaleFeatureRevision(t *testing.T) {
	state := durableState()
	state.Revision = 3
	_, err := Replay(state, []Event{
		{
			SchemaVersion:         FeatureEventSchemaVersion,
			EventID:               "evt-stale",
			IdempotencyKey:        "feature-one/rev-0/dispatch",
			Type:                  "idiom.feature.atom_started",
			SessionID:             "session",
			FeatureID:             "feature-one",
			FeatureRevisionBefore: 0,
			FeatureRevisionAfter:  1,
			FeatureFrom:           FeatureStateReady,
			FeatureTo:             FeatureStateDispatching,
		},
	})
	var replayErr ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}
}

func TestReplayAppliesDurableDispatchEvent(t *testing.T) {
	state := durableState()
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateDispatchable,
	}}
	events := []Event{
		{
			SchemaVersion:         FeatureEventSchemaVersion,
			EventID:               "evt-dispatch",
			IdempotencyKey:        "feature-one/rev-0/dispatch",
			Type:                  "idiom.feature.atom_started",
			SessionID:             "session",
			FeatureID:             "feature-one",
			FeatureRevisionBefore: 0,
			FeatureRevisionAfter:  1,
			FeatureFrom:           FeatureStateReady,
			FeatureTo:             FeatureStateDispatching,
			AtomID:                "atom-1",
			AtomFrom:              AtomStateDispatchable,
			AtomTo:                AtomStateDispatching,
			Action:                "dispatch-atom",
			Actor:                 "orchestrator",
			InvariantID:           InvariantDispatchAuthority,
			ActiveRunDeltas: []ActiveRunDelta{{
				Op:            "insert",
				AtomID:        "atom-1",
				LaneSessionID: "lane-1",
				LaneFamily:    "plan-execute",
				AfterState:    "dispatching",
				ProgressPath:  ".idiom/plan-execute/sessions/lane-1.json",
				StartedAt:     "2026-05-24T21:00:00Z",
			}},
			LeaseDeltas: []LeaseDelta{{
				Op:                   "acquire",
				LeaseID:              "lease-1",
				AfterFencingRevision: 1,
				LeaseKind:            "path",
				Scope:                "internal/featureplanner",
				OwnerFeatureID:       "feature-one",
				OwnerAtomID:          "atom-1",
				OwnerSession:         "lane-1",
			}},
		},
	}

	bundle, err := Replay(state, events)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if bundle.State.Revision != 1 || bundle.State.Status != FeatureStateDispatching {
		t.Fatalf("unexpected replay state: %#v", bundle.State)
	}
	if got := bundle.State.Atoms[0].State; got != AtomStateDispatching {
		t.Fatalf("atom state = %q, want dispatching", got)
	}
	if len(bundle.State.ActiveRuns) != 1 || bundle.State.ActiveRuns[0].LaneSessionID != "lane-1" {
		t.Fatalf("active runs not folded: %#v", bundle.State.ActiveRuns)
	}
	if len(bundle.State.Leases) != 1 || bundle.State.Leases[0].FencingRevision != 1 {
		t.Fatalf("leases not folded: %#v", bundle.State.Leases)
	}
}

func TestReplayAppliesFeaturePlannerDispatchAndCompletionDeltas(t *testing.T) {
	state := durableState()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateQueued,
	}}
	events := []Event{
		{
			SchemaVersion: FeatureEventSchemaVersion,
			EventID:       "evt-feature-dispatch",
			Type:          "idiom.feature_planner.atom_dispatched",
			SessionID:     "session",
			AtomID:        "atom-1",
			AtomFrom:      AtomStateQueued,
			AtomTo:        AtomStateImplementing,
			AtomDeltas:    []AtomDelta{{Op: "update", AtomID: "atom-1", AtomFrom: AtomStateQueued, AtomTo: AtomStateImplementing}},
			ActiveRunDeltas: []ActiveRunDelta{{
				Op:            "insert",
				AtomID:        "atom-1",
				LaneSessionID: "atom-1-run-1",
				LaneFamily:    "feature-planner-implementor",
				AfterState:    "running",
				StartedAt:     "2026-05-24T21:00:00Z",
			}},
		},
		{
			SchemaVersion: FeatureEventSchemaVersion,
			EventID:       "evt-feature-complete",
			Type:          "idiom.feature_planner.atom_completed",
			SessionID:     "session",
			AtomID:        "atom-1",
			AtomFrom:      AtomStateImplementing,
			AtomTo:        AtomStateComplete,
			AtomDeltas:    []AtomDelta{{Op: "update", AtomID: "atom-1", AtomFrom: AtomStateImplementing, AtomTo: AtomStateComplete}},
			ActiveRunDeltas: []ActiveRunDelta{{
				Op:            "fold_terminal",
				AtomID:        "atom-1",
				LaneSessionID: "atom-1-run-1",
				BeforeState:   "running",
				AfterState:    AtomStateComplete,
			}},
		},
	}

	bundle, err := Replay(state, events)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if got := bundle.State.Atoms[0].State; got != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete", got)
	}
	if bundle.State.Status != FeatureStateComplete {
		t.Fatalf("feature state = %q, want complete", bundle.State.Status)
	}
	if len(bundle.State.ActiveRuns) != 0 {
		t.Fatalf("active run should fold terminal: %+v", bundle.State.ActiveRuns)
	}
}

func TestReplayRejectsMismatchedAtomDeltaFromState(t *testing.T) {
	state := durableState()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateQueued,
	}}

	_, err := Replay(state, []Event{{
		SchemaVersion: FeatureEventSchemaVersion,
		EventID:       "evt-stale-atom",
		Type:          "idiom.feature_planner.atom_completed",
		SessionID:     "session",
		AtomID:        "atom-1",
		AtomFrom:      AtomStateImplementing,
		AtomTo:        AtomStateComplete,
		AtomDeltas:    []AtomDelta{{Op: "update", AtomID: "atom-1", AtomFrom: AtomStateImplementing, AtomTo: AtomStateComplete}},
	}})

	var replayErr ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}
	if !strings.Contains(err.Error(), "got folded state") {
		t.Fatalf("expected folded atom state mismatch, got %v", err)
	}
}

func TestReplayRejectsMalformedPreExecutionTDDEvidence(t *testing.T) {
	state := durableState()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateQueued,
	}}

	_, err := Replay(state, []Event{{
		SchemaVersion: FeatureEventSchemaVersion,
		Type:          "idiom.feature_planner.tdd_pre_execution_checked",
		SessionID:     "session",
		AtomID:        "atom-1",
		Fields: map[string]any{
			"commands": []any{"go test ./..."},
		},
	}})

	var replayErr ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid pre-execution TDD evidence") {
		t.Fatalf("expected TDD evidence replay rejection, got %v", err)
	}
}

func TestReplayRejectsUnknownSingularAtomEvent(t *testing.T) {
	state := durableState()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateQueued,
	}}

	_, err := Replay(state, []Event{{
		SchemaVersion: FeatureEventSchemaVersion,
		EventID:       "evt-unknown-atom",
		Type:          "idiom.feature.atom_progressed",
		SessionID:     "session",
		AtomID:        "atom-missing",
		AtomTo:        AtomStateDispatching,
	}})

	var replayErr ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}
	if !strings.Contains(err.Error(), "unknown atom") {
		t.Fatalf("expected unknown atom replay rejection, got %v", err)
	}
}

func TestReplayRejectsInvalidSingularAtomTo(t *testing.T) {
	state := durableState()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: durablePlan().Atoms[0],
		State:           AtomStateQueued,
	}}

	_, err := Replay(state, []Event{{
		SchemaVersion: FeatureEventSchemaVersion,
		EventID:       "evt-invalid-atom-to",
		Type:          "idiom.feature.atom_progressed",
		SessionID:     "session",
		AtomID:        "atom-1",
		AtomTo:        "invented",
	}})

	var replayErr ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid atom_to") {
		t.Fatalf("expected invalid atom_to replay rejection, got %v", err)
	}
}

func TestValidateDurableStateRejectsUnknownStatus(t *testing.T) {
	state := durableState()
	state.Status = "invented"

	err := ValidateDurableState(state)
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("expected unknown status validation error, got %v", err)
	}
}

func TestSaveLoadBundleRoundTrip(t *testing.T) {
	bundle := Bundle{
		State:  durableState(),
		Plan:   durablePlan(),
		Events: []Event{{Type: "idiom.feature.created", SessionID: "session"}},
	}

	dir := t.TempDir()
	if err := SaveBundle(dir, bundle); err != nil {
		t.Fatalf("save bundle: %v", err)
	}
	loaded, err := LoadBundle(dir)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if !reflect.DeepEqual(bundle, loaded) {
		t.Fatalf("round trip mismatch\nwant %#v\ngot %#v", bundle, loaded)
	}
}

func TestSaveStateAndAppendEventsRoundTripDurablePaths(t *testing.T) {
	state := durableState()
	state.SchemaVersion = SchemaVersion
	state.FeatureID = ""
	state.Status = FeatureStatePlanning

	dir := t.TempDir()
	eventsPath, err := AppendEvents(dir, state.SessionID, []Event{{Type: "idiom.feature.created", SessionID: state.SessionID}})
	if err != nil {
		t.Fatalf("append events: %v", err)
	}
	state.EventsPath = eventsPath
	statePath, err := SaveState(dir, &state)
	if err != nil {
		t.Fatalf("save state: %v", err)
	}
	if statePath == "" || eventsPath == "" {
		t.Fatalf("expected durable paths, state=%q events=%q", statePath, eventsPath)
	}
	events, loadedEventsPath, err := LoadEvents(dir, state.SessionID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if loadedEventsPath != eventsPath || len(events) != 1 {
		t.Fatalf("unexpected loaded events path=%q events=%+v", loadedEventsPath, events)
	}
}

func TestLoadStateRejectsInvalidDurableStatus(t *testing.T) {
	state := durableState()
	state.SchemaVersion = SchemaVersion
	state.FeatureID = ""
	state.Status = "invented"

	dir := t.TempDir()
	fullPath, _, err := statePath(dir, state.SessionID)
	if err != nil {
		t.Fatalf("state path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	_, _, err = LoadState(dir, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("expected invalid status load error, got %v", err)
	}
}

func durableState() FeatureState {
	return FeatureState{
		SchemaVersion:    FeatureStateSchemaVersion,
		FeatureID:        "feature-one",
		Title:            "Feature One",
		SessionID:        "session",
		Status:           FeatureStateReady,
		Revision:         0,
		ContextFreshness: "fresh",
		PlanPath:         DefaultFeaturePlanName,
		EventsPath:       DefaultEventLogName,
	}
}

func durablePlan() FeaturePlan {
	return FeaturePlan{
		SchemaVersion: FeaturePlanSchemaVersion,
		FeatureID:     "feature-one",
		Tool:          ToolSubmitFeaturePlan,
		SourceTask: SourceTask{
			Kind:    "design_doc",
			Path:    "docs/design-feature-planner-orchestration.md",
			Summary: "Implement durable feature planner domain package.",
		},
		Summary: "Add feature planner",
		TopLevelScope: TopLevelScope{
			Objective:       "Add feature planner",
			SuccessCriteria: []string{"durable contracts exist"},
		},
		SDDContract: SDDContract{
			Refs:       []string{"REQ-1"},
			Acceptance: []string{"durable contracts exist"},
		},
		TDDContract: TDDContract{
			CoverageTier:                 "unit",
			RequiredBeforeImplementation: true,
			Obligations: []TDDObligation{{
				ID:             "TDD-1",
				Behavior:       "Durable replay validates revisions",
				Command:        "go test ./internal/featureplanner",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []FeatureAtomPlan{{
			SchemaVersion:     FeatureAtomSchemaVersion,
			FeatureID:         "feature-one",
			ID:                "atom-1",
			AtomID:            "atom-1",
			Title:             "Durable replay",
			Summary:           "Add replay and storage",
			RequiredEditPaths: []string{"internal/featureplanner"},
			EvidencePaths:     []string{"docs/design-feature-planner-orchestration.md"},
			SDDRefs:           []string{"REQ-1"},
			TDDObligations:    []string{"TDD-1"},
			RequiredTests:     []string{"go test ./internal/featureplanner"},
			TestFirst: TestFirstPlan{
				ObligationRefs:             []string{"TDD-1"},
				Commands:                   []string{"go test ./internal/featureplanner"},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/featureplanner/durable_test.go"},
				PreExecutionEventIDs:       []string{"evt-tdd-1"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-tdd-coverage",
			},
			PlannedTDDSnapshot: PlannedTDDSnapshot{
				SnapshotID:                   "snap-1",
				SnapshotDigest:               "sha256:abc",
				ObligationIDs:                []string{"TDD-1"},
				TestCommands:                 []string{"go test ./internal/featureplanner"},
				TestArtifactPaths:            []string{"internal/featureplanner/durable_test.go"},
				ExpectedPreExecutionResults:  []string{"red-confirmed"},
				CreatedBeforeDispatchEventID: "evt-tdd-1",
			},
			RuntimeEnvelope: RuntimeEnvelope{
				ContractID:   "feature/atom-1/handoff",
				ProtocolHash: "sha256:protocol",
				QueueMode:    "gated",
			},
			RecoveryPolicyRef: "harness.recovery.default.v1",
		}},
	}
}
