package featureplanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
)

func TestValidateDurablePlanRequiresPreDispatchBaseline(t *testing.T) {
	plan := durablePlan()

	plan.Atoms[0].TestFirst.PreExecutionEventIDs = nil

	err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
	if err == nil {
		t.Fatal("expected validation error")
	}

	if !strings.Contains(err.Error(), "test_first.pre_execution_event_ids") {
		t.Fatalf("expected pre-execution event error, got %v", err)
	}
}

func mustParseFeaturePlannerTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func TestValidateDurablePlanRejectsMismatchedTDDSnapshot(t *testing.T) {
	plan := durablePlan()

	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"go test ./wrong"}

	err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
	if err == nil {
		t.Fatal("expected validation error")
	}

	if !strings.Contains(err.Error(), "planned_tdd_snapshot.test_commands") {
		t.Fatalf("expected snapshot command mismatch, got %v", err)
	}
}

func TestApplyPlannerOutputStampsCanonicalPlanArtifactAndApproval(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	plan := durablePlan()
	plan.Artifact = FeaturePlanArtifact{}
	plan.Approval = FeaturePlanApproval{}

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))

	if state.Status != FeatureStateDispatching {
		t.Fatalf("state status = %q, want dispatching; blockers=%v events=%+v", state.Status, state.Blockers, events)
	}
	if state.PlannerRevision != 1 || state.Plan.Artifact.Revision != 1 {
		t.Fatalf("planner revision not stamped: state=%d artifact=%d", state.PlannerRevision, state.Plan.Artifact.Revision)
	}
	if state.Plan.Artifact.ArtifactID == "" || state.Plan.Artifact.Digest == "" || state.Plan.Artifact.WorkspaceFingerprint == "" {
		t.Fatalf("artifact metadata incomplete: %+v", state.Plan.Artifact)
	}
	if state.Plan.Approval.Status != "approved" ||
		state.Plan.Approval.Mode != "harness_policy" ||
		state.Plan.Approval.PlanDigest != state.Plan.Artifact.Digest ||
		state.Plan.Approval.PlanRevision != state.Plan.Artifact.Revision ||
		state.Plan.Approval.WorkspaceFingerprint != state.Plan.Artifact.WorkspaceFingerprint {
		t.Fatalf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact)
	}
	if err := ValidateDurablePlanWithContract(state.Plan, plancontract.PlanExecutionContract{}); err != nil {
		t.Fatalf("stamped plan should validate: %v", err)
	}

	var approved Event
	for _, event := range events {
		if event.Type == "idiom.feature_planner.plan_approved" {
			approved = event
			break
		}
	}
	if approved.EventID != state.Plan.Approval.EventID {
		t.Fatalf("approval event id = %q, want %q", approved.EventID, state.Plan.Approval.EventID)
	}
	if approved.Fields[featureplannercontract.EventFieldPlanDigest] != state.Plan.Artifact.Digest ||
		approved.Fields[featureplannercontract.EventFieldPlanRevision] != state.Plan.Artifact.Revision ||
		approved.Fields[featureplannercontract.EventFieldWorkspaceFingerprint] != state.Plan.Artifact.WorkspaceFingerprint {
		t.Fatalf("approval event does not carry artifact binding: %+v", approved.Fields)
	}
}

func TestValidateDurablePlanRejectsApprovalAfterPlanDigestDrift(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	plan := durablePlan()
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))

	state.Plan.TopLevelScope.SuccessCriteria = append(state.Plan.TopLevelScope.SuccessCriteria, "unapproved new criterion")

	err := ValidateDurablePlanWithContract(state.Plan, plancontract.PlanExecutionContract{})
	if err == nil || !strings.Contains(err.Error(), "does not match canonical digest") {
		t.Fatalf("expected digest drift rejection, got %v", err)
	}
}

func TestValidateDurableStateRequiresVerificationClosureForApprovedTerminalAtom(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
	state.Atoms[0].State = AtomStateComplete
	state.Status = FeatureStateComplete
	state.ActiveRuns = nil
	state.VerificationClosures = nil

	err := ValidateDurableState(state)
	if err == nil {
		t.Fatal("expected durable state validation error")
	}
	if !strings.Contains(err.Error(), "without verification closure") {
		t.Fatalf("expected missing verification closure error, got %v", err)
	}
}

func TestEvaluateReleaseReadinessBlocksOpenVerificationObligations(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
	state.Status = FeatureStateComplete
	state.Atoms[0].State = AtomStateComplete
	state.ActiveRuns = nil
	state.VerificationClosures = nil

	decision := EvaluateReleaseReadiness(state, "2026-06-22T19:02:00Z")
	if decision.Status != "blocked" || !containsString(decision.Blockers, "verification closure missing for atom: atom-1") {
		t.Fatalf("release readiness should block missing closure, got %+v", decision)
	}

	state.ReleaseReadiness = ReleaseReadinessDecision{
		Status:               "ready",
		EventID:              "release-feature-one-r1",
		PlanArtifactID:       state.Plan.Artifact.ArtifactID,
		PlanRevision:         state.Plan.Artifact.Revision,
		PlanDigest:           state.Plan.Artifact.Digest,
		WorkspaceFingerprint: state.Plan.Artifact.WorkspaceFingerprint,
		ApprovalEventID:      state.Plan.Approval.EventID,
		Blockers:             []string{"verification closure missing for atom: atom-1"},
	}
	err := ValidateDurableState(state)
	if err == nil || !strings.Contains(err.Error(), "release readiness cannot be ready with blockers") {
		t.Fatalf("expected impossible release readiness rejection, got %v", err)
	}
}

func TestValidateDurableStateRequiresReleaseDecisionForCompleteState(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
	state.Status = FeatureStateComplete
	state.Atoms[0].State = AtomStateComplete
	state.ActiveRuns = nil
	state.VerificationClosures = []VerificationClosure{{
		EventID:              "verification-feature-one-atom-1-r1",
		AtomID:               "atom-1",
		Status:               "passed",
		PlanArtifactID:       state.Plan.Artifact.ArtifactID,
		PlanRevision:         state.Plan.Artifact.Revision,
		PlanDigest:           state.Plan.Artifact.Digest,
		ApprovalEventID:      state.Plan.Approval.EventID,
		WorkspaceFingerprint: state.Plan.Artifact.WorkspaceFingerprint,
		RequiredTestResults:  []VerificationCommandResult{{Command: "go test ./internal/workflows/featureplanner", Status: "passed", Passed: true}},
		TDDCommandResults:    []VerificationCommandResult{{Command: "go test ./internal/workflows/featureplanner", Status: "passed", Passed: true, TestArtifactPaths: []string{"internal/workflows/featureplanner/durable_test.go"}}},
	}}

	err := ValidateDurableState(state)
	if err == nil || !strings.Contains(err.Error(), "missing release readiness decision") {
		t.Fatalf("expected missing release readiness rejection, got %v", err)
	}

	state.ReleaseReadiness = ReleaseReadinessDecision{
		Status:                      "ready",
		EventID:                     "release-feature-one-r1",
		PlanArtifactID:              state.Plan.Artifact.ArtifactID,
		PlanRevision:                state.Plan.Artifact.Revision,
		PlanDigest:                  state.Plan.Artifact.Digest,
		WorkspaceFingerprint:        state.Plan.Artifact.WorkspaceFingerprint,
		ApprovalEventID:             state.Plan.Approval.EventID,
		VerificationClosureEventIDs: []string{"unknown-closure"},
	}
	err = ValidateDurableState(state)
	if err == nil || !strings.Contains(err.Error(), "unknown verification closure") {
		t.Fatalf("expected unknown release closure rejection, got %v", err)
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

func TestSaveStateAndAppendEventsRoundTripDurablePaths(t *testing.T) {
	state := durableState()

	state.SchemaVersion = SchemaVersion
	state.FeatureID = ""
	state.Status = FeatureStatePlanning

	dir := t.TempDir()

	eventsPath, err := AppendEvents(dir, state.SessionID, []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventStart), SessionID: state.SessionID}})
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
	if _, err := os.Stat(filepath.Join(dir, statePath+".tmp")); err == nil || !os.IsNotExist(err) {
		t.Fatalf("SaveState should not leave predictable temp file, stat err=%v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(eventsPath)))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 1 {
		t.Fatalf("unexpected event log body: %q", string(body))
	}
	var event Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode event log line: %v", err)
	}
	if event.Type != featureplannercontract.EventName(featureplannercontract.EventStart) || event.SessionID != state.SessionID {
		t.Fatalf("unexpected stored event: %+v", event)
	}
}

func TestFeaturePlannerStatePathsRejectDotSessionIDs(t *testing.T) {
	root := t.TempDir()
	for _, sessionID := range []string{".", "..", "nested/session", `nested\session`, "a/../b"} {
		if _, _, err := statePath(root, sessionID); err == nil {
			t.Fatalf("statePath accepted invalid session id %q", sessionID)
		}
		if _, _, err := EventLogPath(root, sessionID); err == nil {
			t.Fatalf("EventLogPath accepted invalid session id %q", sessionID)
		}
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

func TestLoadStateRejectsEmbeddedSessionMismatch(t *testing.T) {
	state := durableState()
	state.SchemaVersion = SchemaVersion
	requestedSessionID := state.SessionID
	state.SessionID = "other-session"

	dir := t.TempDir()

	fullPath, _, err := statePath(dir, requestedSessionID)
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

	_, _, err = LoadState(dir, requestedSessionID)
	if err == nil || !strings.Contains(err.Error(), "does not match requested session") {
		t.Fatalf("expected session mismatch load error, got %v", err)
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
		Tool:          string(featureplannercontract.ToolSubmitFeaturePlan),
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
				Command:        "go test ./internal/workflows/featureplanner",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []FeatureAtomPlan{{
			SchemaVersion: FeatureAtomSchemaVersion,
			FeatureID:     "feature-one",
			ID:            "atom-1",
			AtomID:        "atom-1",
			Title:         "Durable replay",
			Summary:       "Add replay and storage",
			RequiredEditPaths: []string{
				"internal/workflows/featureplanner",
				"internal/workflows/featureplanner/durable_test.go",
			},
			EvidencePaths:  []string{"docs/design-feature-planner-orchestration.md"},
			SDDRefs:        []string{"REQ-1"},
			TDDObligations: []string{"TDD-1"},
			RequiredTests:  []string{"go test ./internal/workflows/featureplanner"},
			TestFirst: TestFirstPlan{
				ObligationRefs:             []string{"TDD-1"},
				Commands:                   []string{"go test ./internal/workflows/featureplanner"},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/workflows/featureplanner/durable_test.go"},
				PreExecutionEventIDs:       []string{"evt-tdd-1"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-tdd-coverage",
			},
			PlannedTDDSnapshot: PlannedTDDSnapshot{
				SnapshotID:                   "snapshot-durable-0001",
				SnapshotDigest:               "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ObligationIDs:                []string{"TDD-1"},
				TestCommands:                 []string{"go test ./internal/workflows/featureplanner"},
				TestArtifactPaths:            []string{"internal/workflows/featureplanner/durable_test.go"},
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
