package featureplanner

import (
	"encoding/json"
	"errors"
	"idiom/internal/platform/failures"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestReplayErrorClassifiesReplayInvariantViolations(t *testing.T) {
	state := durableState()

	state.Revision = 3

	_, err := Replay(state, []Event{{
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
	}})
	if err == nil {
		t.Fatal("expected replay error")
	}

	var replayErr ReplayError

	if !errors.As(err, &replayErr) {
		t.Fatalf("expected ReplayError, got %v", err)
	}

	if got := failures.CodeOf(err); got != failures.FailedPrecondition {
		t.Fatalf("CodeOf(err) = %v, want %v", got, failures.FailedPrecondition)
	}

	if got := failures.ReasonOf(err); got != ReplayInvariantViolation {
		t.Fatalf("ReasonOf(err) = %v, want %v", got, ReplayInvariantViolation)
	}

	if !strings.Contains(err.Error(), "expected feature_revision_before") {
		t.Fatalf("existing replay error message changed: %v", err)
	}
}

func TestReplayPreservesMalformedAuthorityDeltaForDurableValidation(t *testing.T) {
	state := durableState()

	bundle, err := Replay(state, []Event{{
		SchemaVersion:         FeatureEventSchemaVersion,
		EventID:               "evt-authority",
		IdempotencyKey:        "feature-one/rev-0/authority",
		Type:                  EventPlannerQuestionAnswered,
		SessionID:             "session",
		FeatureID:             "feature-one",
		FeatureRevisionBefore: 0,
		FeatureRevisionAfter:  1,
		Fields: map[string]any{
			"authority_delta": map[string]any{
				"authority_kind": "new_file_authorization",
				"affected_paths": []string{"pkg/new_file.go"},
			},
		},
	}})
	if err != nil {
		t.Fatalf("replay should preserve malformed authority delta for validation, got %v", err)
	}

	if len(bundle.State.AuthorityDeltas) != 1 {
		t.Fatalf("authority deltas were dropped: %#v", bundle.State.AuthorityDeltas)
	}

	err = ValidateDurableState(bundle.State)
	if err == nil || !strings.Contains(err.Error(), "authority delta 0 missing question_id") {
		t.Fatalf("expected durable authority delta validation error, got %v", err)
	}
}

func TestReplayPreservesNewFileAuthorityForContractRecompile(t *testing.T) {
	state := durableState()
	state.Plan = durablePlan()
	state.Plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	state.Plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	state.Plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	bundle, err := Replay(state, []Event{{
		SchemaVersion:         FeatureEventSchemaVersion,
		EventID:               "evt-authority",
		IdempotencyKey:        "feature-one/rev-0/authority",
		Type:                  EventPlannerQuestionAnswered,
		SessionID:             "session",
		FeatureID:             "feature-one",
		FeatureRevisionBefore: 0,
		FeatureRevisionAfter:  1,
		Fields: map[string]any{
			"authority_delta": map[string]any{
				"question_id":     "q-001-new-file-authorization",
				"authority_kind":  "operator_approval",
				"evidence_refs":   []string{"planner_question:q-001"},
				"affected_paths":  []string{"pkg/new_helper.go", "pkg/new_helper_test.go"},
				"affected_fields": []string{"new_file_authorization"},
				"expected":        "missing new files require explicit operator authorization",
				"actual":          "operator authorized creating new helper and test files",
				"revision":        float64(1),
				"created_at":      "2026-06-12T19:00:00Z",
			},
		},
	}})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}

	contract := CompilePlanAuthorityContract(bundle.State.Plan, bundle.State.AuthorityDeltas)
	got := []string{}
	for _, file := range contract.AuthorizedNewFiles {
		got = append(got, file.Path)
	}

	if !slices.Equal(got, []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}) {
		t.Fatalf("replayed authority did not recompile authorized new files: %#v", got)
	}
}

func TestReplaySupersedesPlannerQuestionByRevision(t *testing.T) {
	state := durableState()
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.Blockers = []string{
		`planner question "q-source" requires typed accepted_source authority answer`,
		"source fact blocker remains",
	}
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts",
		AuthorityRequired: "accepted_source",
	}}

	bundle, err := Replay(state, []Event{{
		SchemaVersion:         FeatureEventSchemaVersion,
		EventID:               "evt-supersede",
		IdempotencyKey:        "feature-one/rev-0/question-supersede",
		Type:                  EventPlannerQuestionSupersededByRevision,
		SessionID:             "session",
		FeatureID:             "feature-one",
		FeatureRevisionBefore: 0,
		FeatureRevisionAfter:  1,
		Fields: map[string]any{
			"question_id": "q-source",
		},
	}})
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}

	if bundle.State.Status != FeatureStatePlanning || bundle.State.PlanningLoop.State != FeatureStatePlanning {
		t.Fatalf("superseded question should resume planning, got status=%q loop=%q", bundle.State.Status, bundle.State.PlanningLoop.State)
	}
	if len(bundle.State.OpenQuestions) != 1 ||
		bundle.State.OpenQuestions[0].Status != "answered" ||
		bundle.State.OpenQuestions[0].AnswerSourceKind != "plan_revision" ||
		!slices.Contains(bundle.State.OpenQuestions[0].EvidenceRefs, "operator_plan_revision") {
		t.Fatalf("question was not replayed as superseded plan revision: %+v", bundle.State.OpenQuestions)
	}
	if len(bundle.State.AuthorityDeltas) != 0 {
		t.Fatalf("plan revision replay must not mint authority deltas: %+v", bundle.State.AuthorityDeltas)
	}
	if containsString(bundle.State.Blockers, `planner question "q-source" requires typed accepted_source authority answer`) {
		t.Fatalf("superseded question blocker should be cleared: %+v", bundle.State.Blockers)
	}
	if !containsString(bundle.State.Blockers, "source fact blocker remains") {
		t.Fatalf("unrelated blocker should remain: %+v", bundle.State.Blockers)
	}
}

func TestSaveLoadBundleUsesStateAuthorityForAcceptedSourceSignatures(t *testing.T) {
	plan := durablePlan()
	plan.Atoms[0].Prompt = "Use FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) before posting fallback water haul."

	state := durableState()
	state.Plan = plan
	state.AuthorityDeltas = []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []SourceAuthorityFact{{
			Path:      "native/src/simulation/economy/fluid_network_system.h",
			Symbol:    "FluidNetworkSystem::find_connected_cistern_with_volume",
			Signature: "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
		}},
		Expected:  "helper signature needs accepted source evidence",
		Actual:    "accepted source fact approved for fluid network helper usage",
		Revision:  1,
		CreatedAt: "2026-06-13T12:36:17Z",
	}}

	dir := t.TempDir()
	if err := SaveBundle(dir, Bundle{State: state, Plan: plan}); err != nil {
		t.Fatalf("SaveBundle should use state authority deltas for durable plan validation: %v", err)
	}

	loaded, err := LoadBundle(dir)
	if err != nil {
		t.Fatalf("LoadBundle should use state authority deltas for durable plan validation: %v", err)
	}
	if len(loaded.State.AuthorityDeltas) != 1 {
		t.Fatalf("authority deltas were not preserved: %+v", loaded.State.AuthorityDeltas)
	}
	if len(loaded.State.AuthorityDeltas[0].SourceFacts) != 1 {
		t.Fatalf("source facts were not preserved: %+v", loaded.State.AuthorityDeltas[0])
	}
}

func TestApplyPlannerOutputStampsCanonicalPlanArtifactAndApproval(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	plan := durablePlan()
	plan.Artifact = FeaturePlanArtifact{}
	plan.Approval = FeaturePlanApproval{}

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))

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
	if err := ValidateDurablePlan(state.Plan); err != nil {
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
	if approved.Fields["plan_digest"] != state.Plan.Artifact.Digest ||
		approved.Fields["plan_revision"] != state.Plan.Artifact.Revision ||
		approved.Fields["workspace_fingerprint"] != state.Plan.Artifact.WorkspaceFingerprint {
		t.Fatalf("approval event does not carry artifact binding: %+v", approved.Fields)
	}
}

func TestValidateDurablePlanRejectsApprovalAfterPlanDigestDrift(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	plan := durablePlan()
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))

	state.Plan.TopLevelScope.SuccessCriteria = append(state.Plan.TopLevelScope.SuccessCriteria, "unapproved new criterion")

	err := ValidateDurablePlan(state.Plan)
	if err == nil || !strings.Contains(err.Error(), "does not match canonical digest") {
		t.Fatalf("expected digest drift rejection, got %v", err)
	}
}

func TestValidateDurableStateRequiresVerificationClosureForApprovedTerminalAtom(t *testing.T) {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime(t, "2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
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
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
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
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime(t, "2026-06-22T19:01:00Z"))
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
				Scope:                "internal/workflows/featureplanner",
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
				AfterState:    ActiveRunStateRunning,
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
				BeforeState:   ActiveRunStateRunning,
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

func TestReplayAppliesProductSourceSatisfiedCloseout(t *testing.T) {
	state := durableState()

	state.Status = FeatureStateDispatching
	state.PlanningLoop.State = FeatureStateDispatching
	state.OpenQuestions = []PlannerQuestion{{ID: "q-1", Status: "open", QuestionSummary: "stale question"}}
	state.PlanningLoop.OpenQuestionIDs = []string{"q-1"}
	state.OpenRecoveryPackets = []string{"recovery.json"}
	state.ActiveRuns = []ActiveRun{{
		RunID:         "run-1",
		AtomID:        "atom-1",
		LaneSessionID: "lane-1",
		State:         ActiveRunStateRunning,
	}}

	bundle, err := Replay(state, []Event{{
		SchemaVersion: FeatureEventSchemaVersion,
		EventID:       "evt-product-source-satisfied",
		Type:          "idiom.feature_planner.product_source_satisfied",
		SessionID:     "session",
		FeatureTo:     FeatureStateSourceSatisfied,
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        "atom-1",
			LaneSessionID: "lane-1",
			BeforeState:   ActiveRunStateRunning,
			AfterState:    FeatureStateSourceSatisfied,
		}},
	}})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}

	if bundle.State.Status != FeatureStateSourceSatisfied || bundle.State.PlanningLoop.State != FeatureStateSourceSatisfied {
		t.Fatalf("expected source-satisfied replay state, got status=%q loop=%q", bundle.State.Status, bundle.State.PlanningLoop.State)
	}

	if len(bundle.State.ActiveRuns) != 0 || len(bundle.State.OpenQuestions) != 0 || len(bundle.State.OpenRecoveryPackets) != 0 || len(bundle.State.PlanningLoop.OpenQuestionIDs) != 0 {
		t.Fatalf("source-satisfied replay should clear terminal-incompatible work: %#v", bundle.State)
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
