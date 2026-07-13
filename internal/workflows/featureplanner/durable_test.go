package featureplanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("validate durable plan requires pre dispatch baseline", func() {
	plan := durablePlan()

	plan.Atoms[0].TestFirst.PreExecutionEventIDs = nil

	err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected validation error"))
	gomega.Expect(strings.Contains(err.Error(), "test_first.pre_execution_event_ids")).To(gomega.BeTrue(), fmt.Sprintf("expected pre-execution event error, got %v", err))

})

func mustParseFeaturePlannerTime(value string) time.Time {
	ginkgo.GinkgoHelper()
	parsed, err := time.Parse(time.RFC3339, value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse time %q: %v", value, err))

	return parsed
}

var _ = ginkgo.It("validate durable plan rejects mismatched TDD snapshot", func() {
	plan := durablePlan()

	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"go test ./wrong"}

	err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected validation error"))
	gomega.Expect(strings.Contains(err.Error(), "planned_tdd_snapshot.test_commands")).To(gomega.BeTrue(), fmt.Sprintf("expected snapshot command mismatch, got %v", err))

})
var _ = ginkgo.It("apply planner output stamps canonical plan artifact and approval", func() {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime("2026-06-22T19:00:00Z"))
	plan := durablePlan()
	plan.Artifact = FeaturePlanArtifact{}
	plan.Approval = FeaturePlanApproval{}

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime("2026-06-22T19:01:00Z"))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateDispatching), fmt.Sprintf("state status = %q, want dispatching; blockers=%v events=%+v", state.Status, state.Blockers, events))
	gomega.Expect(state.PlannerRevision).To(gomega.Equal(1), fmt.Sprintf("planner revision not stamped: state=%d artifact=%d", state.PlannerRevision, state.Plan.Artifact.Revision))
	gomega.Expect(state.Plan.Artifact.Revision).To(gomega.Equal(1), fmt.Sprintf("planner revision not stamped: state=%d artifact=%d", state.PlannerRevision, state.Plan.Artifact.Revision))
	gomega.Expect(state.Plan.Artifact.ArtifactID).NotTo(gomega.Equal(""), fmt.Sprintf("artifact metadata incomplete: %+v", state.Plan.Artifact))
	gomega.Expect(state.Plan.Artifact.Digest).NotTo(gomega.Equal(""), fmt.Sprintf("artifact metadata incomplete: %+v", state.Plan.Artifact))
	gomega.Expect(state.Plan.Artifact.WorkspaceFingerprint).NotTo(gomega.Equal(""), fmt.Sprintf("artifact metadata incomplete: %+v", state.Plan.Artifact))
	gomega.Expect(state.Plan.Approval.Status).To(gomega.Equal("approved"), fmt.Sprintf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact))
	gomega.Expect(state.Plan.Approval.Mode).To(gomega.Equal("harness_policy"), fmt.Sprintf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact))
	gomega.Expect(state.Plan.Approval.PlanDigest).To(gomega.Equal(state.Plan.Artifact.Digest), fmt.Sprintf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact))
	gomega.Expect(state.Plan.Approval.PlanRevision).To(gomega.Equal(state.Plan.Artifact.Revision), fmt.Sprintf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact))
	gomega.Expect(state.Plan.Approval.WorkspaceFingerprint).To(gomega.Equal(state.Plan.Artifact.WorkspaceFingerprint), fmt.Sprintf("approval is not bound to artifact: approval=%+v artifact=%+v", state.Plan.Approval, state.Plan.Artifact))
	{

		err := ValidateDurablePlanWithContract(state.Plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("stamped plan should validate: %v", err))
	}

	var approved Event
	for _, event := range events {
		if event.Type == "idiom.feature_planner.plan_approved" {
			approved = event
			break
		}
	}
	gomega.Expect(approved.EventID).To(gomega.Equal(state.Plan.Approval.EventID), fmt.Sprintf("approval event id = %q, want %q", approved.EventID, state.Plan.Approval.EventID))
	gomega.Expect(approved.Fields[featureplannercontract.EventFieldPlanDigest]).To(gomega.Equal(state.Plan.Artifact.Digest), fmt.Sprintf("approval event does not carry artifact binding: %+v", approved.Fields))
	gomega.Expect(approved.Fields[featureplannercontract.EventFieldPlanRevision]).To(gomega.Equal(state.Plan.Artifact.Revision), fmt.Sprintf("approval event does not carry artifact binding: %+v", approved.Fields))
	gomega.Expect(approved.Fields[featureplannercontract.EventFieldWorkspaceFingerprint]).To(gomega.Equal(state.Plan.Artifact.WorkspaceFingerprint), fmt.Sprintf("approval event does not carry artifact binding: %+v", approved.Fields))

})
var _ = ginkgo.It("validate durable plan rejects approval after plan digest drift", func() {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime("2026-06-22T19:00:00Z"))
	plan := durablePlan()
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, mustParseFeaturePlannerTime("2026-06-22T19:01:00Z"))

	state.Plan.TopLevelScope.SuccessCriteria = append(state.Plan.TopLevelScope.SuccessCriteria, "unapproved new criterion")

	err := ValidateDurablePlanWithContract(state.Plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected digest drift rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("does not match canonical digest"), fmt.Sprintf("expected digest drift rejection, got %v", err))

})
var _ = ginkgo.It("validate durable state requires verification closure for approved terminal atom", func() {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime("2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime("2026-06-22T19:01:00Z"))
	state.Atoms[0].State = AtomStateComplete
	state.Status = FeatureStateComplete
	state.ActiveRuns = nil
	state.VerificationClosures = nil

	err := ValidateDurableState(state)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected durable state validation error"))
	gomega.Expect(strings.Contains(err.Error(), "without verification closure")).To(gomega.BeTrue(), fmt.Sprintf("expected missing verification closure error, got %v", err))

})
var _ = ginkgo.It("evaluate release readiness blocks open verification obligations", func() {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime("2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime("2026-06-22T19:01:00Z"))
	state.Status = FeatureStateComplete
	state.Atoms[0].State = AtomStateComplete
	state.ActiveRuns = nil
	state.VerificationClosures = nil

	decision := EvaluateReleaseReadiness(state, "2026-06-22T19:02:00Z")
	gomega.Expect(decision.Status).To(gomega.Equal(ReleaseReadinessStatusBlocked), fmt.Sprintf("release readiness should block missing closure, got %+v", decision))
	gomega.Expect(containsString(decision.Blockers, "verification closure missing for atom: atom-1")).To(gomega.BeTrue(), fmt.Sprintf("release readiness should block missing closure, got %+v", decision))

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
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected impossible release readiness rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("release readiness cannot be ready with blockers"), fmt.Sprintf("expected impossible release readiness rejection, got %v", err))

})
var _ = ginkgo.It("validate durable state requires release decision for complete state", func() {
	state := NewState("session", "Implement durable feature planner.", "", 3, mustParseFeaturePlannerTime("2026-06-22T19:00:00Z"))
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: durablePlan()}, mustParseFeaturePlannerTime("2026-06-22T19:01:00Z"))
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
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing release readiness rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing release readiness decision"), fmt.Sprintf("expected missing release readiness rejection, got %v", err))

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
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown release closure rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown verification closure"), fmt.Sprintf("expected unknown release closure rejection, got %v", err))

})
var _ = ginkgo.It("validate durable state rejects unknown status", func() {
	state := durableState()

	state.Status = "invented"

	err := ValidateDurableState(state)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown status validation error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown status"), fmt.Sprintf("expected unknown status validation error, got %v", err))

})
var _ = ginkgo.It("save state and append events round trip durable paths", func() {
	state := durableState()

	state.SchemaVersion = SchemaVersion
	state.FeatureID = ""
	state.Status = FeatureStatePlanning

	dir := ginkgo.GinkgoT().TempDir()

	eventsPath, err := AppendEvents(dir, state.SessionID, []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventStart), SessionID: state.SessionID}})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("append events: %v", err))

	state.EventsPath = eventsPath

	statePath, err := SaveState(dir, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save state: %v", err))
	gomega.Expect(statePath).NotTo(gomega.Equal(""), fmt.Sprintf("expected durable paths, state=%q events=%q", statePath, eventsPath))
	gomega.Expect(eventsPath).NotTo(gomega.Equal(""), fmt.Sprintf("expected durable paths, state=%q events=%q", statePath, eventsPath))
	{

		_, err := os.Stat(filepath.Join(dir, statePath+".tmp"))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("SaveState should not leave predictable temp file, stat err=%v", err))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("SaveState should not leave predictable temp file, stat err=%v", err))
	}

	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(eventsPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read events: %v", err))

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	gomega.Expect(len(lines)).To(gomega.Equal(1), fmt.Sprintf("unexpected event log body: %q", string(body)))

	var event Event
	{
		err := json.Unmarshal([]byte(lines[0]), &event)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode event log line: %v", err))
	}
	gomega.Expect(event.Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventStart)), fmt.Sprintf("unexpected stored event: %+v", event))
	gomega.Expect(event.SessionID).To(gomega.Equal(state.SessionID), fmt.Sprintf("unexpected stored event: %+v", event))

})
var _ = ginkgo.It("feature planner state paths reject dot session i ds", func() {
	root := ginkgo.GinkgoT().TempDir()
	for _, sessionID := range []string{".", "..", "nested/session", `nested\session`, "a/../b"} {
		{
			_, _, err := statePath(root, sessionID)
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("statePath accepted invalid session id %q", sessionID))
		}
		{

			_, _, err := EventLogPath(root, sessionID)
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("EventLogPath accepted invalid session id %q", sessionID))
		}

	}
})
var _ = ginkgo.It("load state rejects invalid durable status", func() {
	state := durableState()

	state.SchemaVersion = SchemaVersion
	state.FeatureID = ""
	state.Status = "invented"

	dir := ginkgo.GinkgoT().TempDir()

	fullPath, _, err := statePath(dir, state.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("state path: %v", err))
	{

		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir state dir: %v", err))
	}

	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal state: %v", err))
	{

		err := os.WriteFile(fullPath, body, 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write state: %v", err))
	}

	_, _, err = LoadState(dir, state.SessionID)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid status load error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown status"), fmt.Sprintf("expected invalid status load error, got %v", err))

})
var _ = ginkgo.It("load state rejects embedded session mismatch", func() {
	state := durableState()
	state.SchemaVersion = SchemaVersion
	requestedSessionID := state.SessionID
	state.SessionID = "other-session"

	dir := ginkgo.GinkgoT().TempDir()

	fullPath, _, err := statePath(dir, requestedSessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("state path: %v", err))
	{

		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir state dir: %v", err))
	}

	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal state: %v", err))
	{

		err := os.WriteFile(fullPath, body, 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write state: %v", err))
	}

	_, _, err = LoadState(dir, requestedSessionID)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected session mismatch load error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("does not match requested session"), fmt.Sprintf("expected session mismatch load error, got %v", err))

})

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
