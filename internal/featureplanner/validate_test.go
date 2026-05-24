package featureplanner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidatePlanRequiresTDDBeforeImplementation(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].TestFirst.Commands = nil

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "missing test_first.commands") {
		t.Fatalf("expected missing TDD command error, got %v", err)
	}
}

func TestBuildPlannerPromptAdvertisesFlatSubmitFeaturePlanSchema(t *testing.T) {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()))

	for _, want := range []string{
		`return the FeaturePlan object itself`,
		`Do not nest the plan under a plan, payload, arguments, or input field`,
		`do not return both arrays empty`,
		`"tool":"submit_feature_plan"`,
		`"top_level_scope"`,
		`"tdd_contract"`,
		`"planned_tdd_snapshot"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildImplementorPromptRejectsUnsupportedEditTestClaims(t *testing.T) {
	plan := validPlan()
	prompt := BuildImplementorPrompt(FeatureAtomState{FeatureAtomPlan: plan.Atoms[0]}, plan)

	for _, want := range []string{
		`question_for_planner`,
		`Decision: source-satisfied`,
		`Do not claim files were edited or tests were run`,
		`unsupported edit/test claims are rejected as recovery-open`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementor prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestApplyPlannerOutputAcceptsPlanAndQueuesAtoms(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	parsed := ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}

	events := ApplyPlannerOutput(&state, parsed, time.Unix(2, 0).UTC())

	if state.Status != FeatureStateDispatching {
		t.Fatalf("state.Status = %q, want dispatching", state.Status)
	}
	if len(state.Atoms) != 1 || state.Atoms[0].State != AtomStateQueued {
		t.Fatalf("unexpected atoms: %+v", state.Atoms)
	}
	if len(events) != 6 || events[0].Type != "idiom.feature_planner.plan_accepted" || events[4].Type != "idiom.feature_planner.tdd_pre_execution_checked" || events[len(events)-1].Type != "idiom.feature_planner.dispatch_ready" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if len(state.TDDPreExecutionEvidence) != 1 || state.TDDPreExecutionEvidence[0].EventID != "evt-tdd-1" {
		t.Fatalf("expected pre-dispatch TDD evidence ledger, got %+v", state.TDDPreExecutionEvidence)
	}
}

func TestRunDispatchesAtomAfterAcceptedTDDPlan(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	calls := 0
	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		calls++
		switch taskFamily {
		case "feature_planner_planner":
			return mustPlanJSON(t, validPlan()), nil
		case "feature_planner_implementor":
			return "Implemented atom.", nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want planner + implementor", calls)
	}
	if result.State.Status != FeatureStateComplete {
		t.Fatalf("state.Status = %q, want complete", result.State.Status)
	}
	if len(result.Events) != 8 {
		t.Fatalf("events = %+v, want plan review sequence, dispatch, complete", result.Events)
	}
	if len(result.State.ActiveRuns) != 0 {
		t.Fatalf("active runs should be folded after terminal atom: %+v", result.State.ActiveRuns)
	}
}

func TestDispatchBlocksWhenTDDEvidenceLedgerIsMissing(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Plan = validPlan()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: state.Plan.Atoms[0],
		State:           AtomStateQueued,
	}}

	events := DispatchReadyAtoms(&state, 1, func(_ string, _ string) (string, error) {
		t.Fatal("implementor must not run without harness-owned TDD evidence")
		return "", nil
	})

	if state.Atoms[0].State != AtomStateBlockedMissingTDD {
		t.Fatalf("atom state = %q, want blocked missing TDD", state.Atoms[0].State)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.tdd_pre_execution_blocked" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !containsString(state.Blockers, "tdd_pre_execution_evidence.evt-tdd-1") {
		t.Fatalf("missing ledger blocker not recorded: %+v", state.Blockers)
	}
}

func TestDispatchWaitsForAtomDependencies(t *testing.T) {
	plan := validPlan()
	dependent := plan.Atoms[0]
	dependent.ID = "atom-dependent"
	dependent.AtomID = "atom-dependent"
	dependent.Dependencies = []string{"atom-base"}
	dependent.TestFirst.PreExecutionEventIDs = []string{"evt-dependent"}
	dependent.TestFirst.CoverageGateEventID = "evt-dependent"
	dependent.PlannedTDDSnapshot.SnapshotID = "snap-dependent"
	dependent.PlannedTDDSnapshot.SnapshotDigest = "sha256:dependent"
	dependent.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-dependent"
	base := plan.Atoms[0]
	base.ID = "atom-base"
	base.AtomID = "atom-base"
	base.TestFirst.PreExecutionEventIDs = []string{"evt-base"}
	base.TestFirst.CoverageGateEventID = "evt-base"
	base.PlannedTDDSnapshot.SnapshotID = "snap-base"
	base.PlannedTDDSnapshot.SnapshotDigest = "sha256:base"
	base.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-base"
	plan.Atoms = []FeatureAtomPlan{dependent, base}
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var prompts []string
	events := DispatchReadyAtoms(&state, 1, func(prompt string, taskFamily string) (string, error) {
		prompts = append(prompts, prompt)
		return "Implemented dependency.", nil
	})

	if len(prompts) != 1 || !strings.Contains(prompts[0], `"id": "atom-base"`) {
		t.Fatalf("expected only dependency atom to dispatch first, prompts=%#v", prompts)
	}
	if state.Atoms[0].State != AtomStateQueued {
		t.Fatalf("dependent atom should remain queued until dependency completes: %+v", state.Atoms[0])
	}
	if state.Atoms[1].State != AtomStateComplete {
		t.Fatalf("base atom should complete: %+v events=%+v", state.Atoms[1], events)
	}
}

func TestValidateDurablePlanRejectsInvalidDependencies(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		plan := validPlan()
		plan.Atoms[0].Dependencies = []string{"missing"}

		err := ValidateDurablePlan(plan)
		if err == nil || !strings.Contains(err.Error(), "unknown atom") {
			t.Fatalf("expected unknown dependency error, got %v", err)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		plan := validPlan()
		second := plan.Atoms[0]
		second.ID = "atom-2"
		second.AtomID = "atom-2"
		second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
		second.TestFirst.CoverageGateEventID = "evt-tdd-2"
		second.PlannedTDDSnapshot.SnapshotID = "snap-tdd-2"
		second.PlannedTDDSnapshot.SnapshotDigest = "sha256:test2"
		second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
		plan.Atoms[0].Dependencies = []string{"atom-2"}
		second.Dependencies = []string{"atom-1"}
		plan.Atoms = append(plan.Atoms, second)

		err := ValidateDurablePlan(plan)
		if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
			t.Fatalf("expected dependency cycle error, got %v", err)
		}
	})
}

func TestRunKeepsImplementorQuestionAsPlannerRepairState(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_planner" {
			return mustPlanJSON(t, validPlan()), nil
		}
		return `{"tool":"question_for_planner","question_summary":"Which label text should win?","blocking_claim":"ambiguous target"}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want question-open", result.State.Status)
	}
	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("open questions = %+v", result.State.OpenQuestions)
	}
	if result.State.Atoms[0].State != AtomStateQuestionForPlanner {
		t.Fatalf("atom state = %q", result.State.Atoms[0].State)
	}
	if result.State.OpenQuestions[0].QuestionKind == "" || result.State.OpenQuestions[0].AuthorityRequired == "" {
		t.Fatalf("planner question was not normalized: %+v", result.State.OpenQuestions[0])
	}
}

func TestImplementorAtomJSONDoesNotOpenPlannerQuestionOrComplete(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	atom := &state.Atoms[0]
	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.ActiveRuns = []ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        atom.ID,
		LaneSessionID: "atom-1-run-1",
		State:         "running",
	}}
	raw, err := json.Marshal(atom.FeatureAtomPlan)
	if err != nil {
		t.Fatalf("marshal atom: %v", err)
	}

	events := ApplyImplementorOutput(&state, atom, string(raw))

	if atom.State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open; events=%+v", atom.State, events)
	}
	if len(state.OpenQuestions) != 0 {
		t.Fatalf("atom JSON should not open planner question: %+v", state.OpenQuestions)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.unverified_completion_rejected" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if containsString(state.Continuation.CompletedAtoms, atom.ID) {
		t.Fatalf("atom JSON echo must not complete the atom: %+v", state.Continuation)
	}
}

func TestApplyImplementorOutputRejectsUnobservedEditAndTestClaims(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	atom := &state.Atoms[0]
	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.Status = FeatureStateDispatching
	state.ActiveRuns = []ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        atom.ID,
		LaneSessionID: "atom-1-run-1",
		State:         "running",
	}}

	events := ApplyImplementorOutput(&state, atom, `Implemented & TDD-verified.

**Paths Edited**
- internal/labels/labels.go
- internal/labels/labels_test.go

**Verification**
- go test ./internal/labels/... passed locally`)

	if atom.State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open; events=%+v", atom.State, events)
	}
	if state.Status != FeatureStateRecoveryOpen {
		t.Fatalf("feature status = %q, want recovery-open", state.Status)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.unverified_completion_rejected" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded after rejected completion: %+v", state.ActiveRuns)
	}
	if containsString(state.Continuation.CompletedAtoms, atom.ID) {
		t.Fatalf("rejected atom must not be recorded complete: %+v", state.Continuation)
	}
	if !containsString(state.Continuation.BlockedAtoms, atom.ID) {
		t.Fatalf("rejected atom should be resumable as blocked: %+v", state.Continuation)
	}
}

func TestRunResumesDispatchingStateWithoutReplacingPlan(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	if len(events) == 0 {
		t.Fatal("expected initial plan events")
	}
	state.Atoms[0].State = AtomStateQueued

	result, err := Run(state, RunOptions{Prompt: "resume", MaxPlanningIterations: 3}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_planner" {
			t.Fatal("resume from dispatching must not ask planner for a fresh plan")
		}
		return "Implemented resumed atom.", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStateComplete {
		t.Fatalf("status = %q, want complete", result.State.Status)
	}
	if result.State.Plan.Summary != "Add feature" {
		t.Fatalf("plan was replaced: %+v", result.State.Plan)
	}
}

func TestRunRejectsPlainAnswerForNonOperatorQuestion(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	result, err := Run(state, RunOptions{Prompt: "Just do it", ResumeAnswer: "Just do it"}, func(_ string, _ string) (string, error) {
		t.Fatal("plain answer without required authority must not continue to planner")
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("status = %q, want planning question", result.State.Status)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "idiom.feature_planner.planner_question_answer_rejected" {
		t.Fatalf("unexpected events: %+v", result.Events)
	}
}

func TestRunAcceptsTypedAnswerForRequiredAuthority(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}
	typedAnswer := `{"answer":"Use docs/feature.md acceptance criteria.","answer_source_kind":"accepted_source","evidence_refs":["docs/feature.md#acceptance"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(_ string, taskFamily string) (string, error) {
		switch taskFamily {
		case "feature_planner_planner":
			return mustPlanJSON(t, validPlan()), nil
		case "feature_planner_implementor":
			return "Implemented after typed answer.", nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.OpenQuestions[0].AnswerSourceKind != "accepted_source" || len(result.State.OpenQuestions[0].EvidenceRefs) != 1 {
		t.Fatalf("typed authority was not recorded: %+v", result.State.OpenQuestions[0])
	}
	if result.State.Status == FeatureStatePlanningQuestion {
		t.Fatalf("typed answer should allow planning to continue")
	}
}

func validPlan() FeaturePlan {
	return FeaturePlan{
		SchemaVersion: FeaturePlanSchemaVersion,
		FeatureID:     "feature-test",
		Tool:          ToolSubmitFeaturePlan,
		SourceTask: SourceTask{
			Kind:    "operator_prompt",
			Summary: "build feature",
		},
		Summary: "Add feature",
		TopLevelScope: TopLevelScope{
			Objective:       "Add feature",
			SuccessCriteria: []string{"works"},
		},
		SDDContract: SDDContract{
			Refs: []string{"REQ-1"},
		},
		TDDContract: TDDContract{
			CoverageTier:                 "contract_high",
			RequiredBeforeImplementation: true,
			Obligations: []TDDObligation{{
				ID:             "TDD-1",
				Behavior:       "Short label changes",
				Command:        "go test ./...",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []FeatureAtomPlan{{
			ID:                "atom-1",
			Title:             "Short label",
			Summary:           "Change label",
			RequiredEditPaths: []string{"internal/labels/labels.go"},
			SDDRefs:           []string{"REQ-1"},
			TestFirst: TestFirstPlan{
				ObligationRefs:             []string{"TDD-1"},
				Commands:                   []string{"go test ./..."},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/labels/labels_test.go"},
				PreExecutionEventIDs:       []string{"evt-tdd-1"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-tdd-1",
			},
			PlannedTDDSnapshot: PlannedTDDSnapshot{
				SnapshotID:                   "snap-tdd-1",
				SnapshotDigest:               "sha256:test",
				ObligationIDs:                []string{"TDD-1"},
				TestCommands:                 []string{"go test ./..."},
				TestArtifactPaths:            []string{"internal/labels/labels_test.go"},
				ExpectedPreExecutionResults:  []string{"red-confirmed"},
				CreatedBeforeDispatchEventID: "evt-tdd-1",
			},
		}},
	}
}

func mustPlanJSON(t *testing.T, plan FeaturePlan) string {
	t.Helper()
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return string(body)
}
