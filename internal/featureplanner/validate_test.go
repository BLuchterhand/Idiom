package featureplanner

import (
	"encoding/json"
	"fmt"
	"slices"
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

func TestValidatePlanProductWorkflowContextRejectsUnknownRefs(t *testing.T) {
	plan := validPlan()
	plan.SDDContract.PRDRequirementRefs = []string{"REQ-404"}
	plan.SDDContract.ADRRefs = []string{"ADR-001"}
	plan.SDDContract.Refs = nil
	plan.Atoms[0].SDDRefs = []string{"REQ-404"}
	plan.TDDContract.Obligations[0].ID = "TDD-404"
	plan.Atoms[0].TDDObligations = []string{"TDD-404"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-404"}
	brief := ProductWorkflowBrief{
		CoverageGatePassed: true,
		PRDRequirements:    []PRDRequirementBrief{{ID: "REQ-001"}},
		AcceptedADRIDs:     []string{"ADR-001"},
		TDDObligations:     []TDDObligationBrief{{ID: "TDD-001"}},
		FilesToModify:      []string{"internal/labels/labels.go"},
	}

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected product context validation error")
	}
	for _, want := range []string{"REQ-404", "TDD-404"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func TestRunRejectsProductPlanWithUnknownArtifactRefs(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.SDDContract.PRDRequirementRefs = []string{"REQ-999"}
	plan.SDDContract.ADRRefs = []string{"ADR-001"}
	plan.Atoms[0].SDDRefs = []string{"REQ-999"}
	plan.TDDContract.Obligations[0].ID = "TDD-001"
	plan.Atoms[0].TDDObligations = []string{"TDD-001"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-001"}
	plan.Atoms[0].TestFirst.PreExecutionEventIDs = []string{"evt-tdd-1"}
	plan.Atoms[0].PlannedTDDSnapshot.ObligationIDs = []string{"TDD-001"}
	brief := &ProductWorkflowBrief{
		CoverageGatePassed: true,
		PRDRequirements:    []PRDRequirementBrief{{ID: "REQ-001"}},
		AcceptedADRIDs:     []string{"ADR-001"},
		TDDObligations:     []TDDObligationBrief{{ID: "TDD-001"}},
		FilesToModify:      []string{"internal/labels/labels.go"},
	}

	result, err := Run(state, RunOptions{Prompt: "build feature", ProductWorkflowBrief: brief}, func(_ string, taskFamily string) (string, error) {
		if taskFamily != "feature_planner_planner" {
			t.Fatalf("implementor must not run after product context rejection")
		}
		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair", result.State.Status)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "idiom.feature_planner.product_context_blocked" {
		t.Fatalf("unexpected events: %+v", result.Events)
	}
}

func TestNormalizeProductPlanRemovesContextOnlyDocsFromAllowedEditScope(t *testing.T) {
	plan := validProductContextPlan()
	plan.Atoms[0].AllowedEditPaths = []string{
		"internal/labels/labels.go",
		"internal/labels/labels_test.go",
		"internal/labels/labels_extra.go",
		"adrs/ADR-draft.md",
		".planning/campaign-status.md",
	}
	brief := validProductWorkflowBrief()
	brief.AcceptedADRPaths = []string{"adrs/ADR-draft.md"}
	brief.FilesToModify = append(brief.FilesToModify, "internal/labels/labels_extra.go")

	normalized := NormalizePlanProductWorkflowContext(plan, brief)
	if err := ValidatePlanProductWorkflowContext(normalized, brief); err != nil {
		t.Fatalf("normalized plan should validate: %v", err)
	}
	allowed := normalized.Atoms[0].AllowedEditPaths
	for _, forbidden := range []string{"adrs/ADR-draft.md", ".planning/campaign-status.md"} {
		if containsString(allowed, forbidden) {
			t.Fatalf("context-only doc %q leaked into allowed edit scope: %#v", forbidden, allowed)
		}
	}
	for _, want := range []string{"internal/labels/labels_extra.go"} {
		if !containsString(allowed, want) {
			t.Fatalf("grounded allowed edit path %q was not preserved: %#v", want, allowed)
		}
	}
	for _, forbidden := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go"} {
		if containsString(allowed, forbidden) {
			t.Fatalf("required edit path %q leaked into allowed scope: %#v", forbidden, allowed)
		}
	}
}

func TestNormalizeProductPlanPromotesGroundedTDDArtifactsToRequiredScope(t *testing.T) {
	plan := validProductContextPlan()
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}
	brief := validProductWorkflowBrief()

	normalized := NormalizePlanProductWorkflowContext(plan, brief)
	if err := ValidatePlanProductWorkflowContext(normalized, brief); err != nil {
		t.Fatalf("normalized plan should validate: %v", err)
	}
	required := normalized.Atoms[0].RequiredEditPaths
	for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go"} {
		if !containsString(required, want) {
			t.Fatalf("required TDD edit path %q missing from required scope: %#v", want, required)
		}
	}
	if containsString(normalized.Atoms[0].AllowedEditPaths, "internal/labels/labels_test.go") {
		t.Fatalf("promoted TDD edit path must not remain optional allowed scope: %#v", normalized.Atoms[0].AllowedEditPaths)
	}
}

func TestBuildPlannerPromptHidesSubmitShapeWhenProductBriefHasBlockers(t *testing.T) {
	brief := validProductWorkflowBrief()
	brief.Blockers = []string{"implementation-affecting claim lacks resolved evidence"}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)

	if strings.Contains(prompt, `Required submit_feature_plan shape`) || strings.Contains(prompt, `"tool":"submit_feature_plan"`) {
		t.Fatalf("prompt must not advertise submit_feature_plan while product blockers are open:\n%s", prompt)
	}
	if !strings.Contains(prompt, "unresolved blockers") || !strings.Contains(prompt, "ask_planner_question") {
		t.Fatalf("prompt should direct blocker repair through planner question:\n%s", prompt)
	}
}

func TestBuildPlannerPromptProductExampleUsesNonAuthoritativePlaceholders(t *testing.T) {
	brief := validProductWorkflowBrief()
	brief.FilesToModify = []string{"internal/labels/labels.go"}
	brief.ContextEvidencePaths = []string{"internal/labels/labels.go"}
	brief.AcceptedADRPaths = []string{"docs/product-workflows/efforts/started/test/adrs/ADR-0001.md"}
	brief.RequiredBehavior = []string{"ShortLabel keeps product-approved text"}
	brief.Acceptance = []string{"ShortLabel is covered by TDD-1"}
	brief.Decisions = []string{"Use existing labels package"}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)

	for _, want := range []string{
		"Accepted ADR paths:",
		"Required behavior:",
		"Acceptance:",
		"Accepted decisions:",
		"Source/context evidence paths:",
		"required_before: implementation",
		`required_before to the exact scalar string "implementation"`,
		"planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs",
		"use-only-product-grounded-source-or-required-tdd-test-paths",
		"use-only-product-grounded-optional-edit-paths",
		"The schema example path placeholders are not authority",
		"Repo grounding is required:",
		"context evidence only",
		"allowed_edit_paths is still edit scope",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("product prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "internal/example/source.go") {
		t.Fatalf("product prompt must not contain copyable fake source paths:\n%s", prompt)
	}
	if strings.Contains(prompt, `"path_test.go"`) {
		t.Fatalf("product prompt must not contain copyable fake test artifact paths:\n%s", prompt)
	}
	exampleStart := strings.Index(prompt, "Required submit_feature_plan shape:")
	if exampleStart < 0 {
		t.Fatalf("product prompt missing submit_feature_plan shape:\n%s", prompt)
	}
	schemaExample := prompt[exampleStart:]
	if strings.Contains(schemaExample, "go test ./...") {
		t.Fatalf("product prompt schema example must not contain language-specific test command placeholders:\n%s", prompt)
	}
}

func TestBuildPlannerPromptStandaloneUsesLanguageNeutralPlaceholders(t *testing.T) {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)

	for _, forbidden := range []string{"internal/example/source.go", "internal/example/source_test.go", "path_test.go", "go test ./..."} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("standalone prompt must not contain language-specific placeholder %q:\n%s", forbidden, prompt)
		}
	}
	for _, want := range []string{"project/source-file.ext", "project/test-file.ext", "project-specific-test-command"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("standalone prompt missing neutral placeholder %q:\n%s", want, prompt)
		}
	}
	for _, want := range []string{
		`required_before to the scalar string "implementation"`,
		"planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("standalone prompt missing schema invariant %q:\n%s", want, prompt)
		}
	}
}

func TestParsePlannerOutputCoercesSingletonRequiredBeforeArray(t *testing.T) {
	raw := strings.Replace(mustPlanJSON(t, validPlan()), `"required_before":"implementation"`, `"required_before":["implementation"]`, 1)

	parsed, err := ParsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("ParsePlannerOutput returned error: %v", err)
	}
	if got := parsed.Plan.TDDContract.Obligations[0].RequiredBefore; got != "implementation" {
		t.Fatalf("RequiredBefore = %q, want implementation", got)
	}
}

func TestParsePlannerOutputCoercesScalarAcceptanceString(t *testing.T) {
	raw := strings.Replace(
		mustPlanJSON(t, validPlan()),
		`"refs":["REQ-1"]`,
		`"refs":["REQ-1"],"acceptance":"REQ-1 metric: works.; REQ-1 testability: covered."`,
		1,
	)

	parsed, err := ParsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("ParsePlannerOutput returned error: %v", err)
	}
	want := []string{"REQ-1 metric: works.", "REQ-1 testability: covered."}
	if got := parsed.Plan.SDDContract.Acceptance; !slices.Equal(got, want) {
		t.Fatalf("Acceptance = %#v, want %#v", got, want)
	}
}

func TestValidatePlanProductWorkflowContextRejectsUngroundedTestArtifacts(t *testing.T) {
	plan := validProductContextPlan()
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"path_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"path_test.go"}
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected ungrounded test artifact validation error")
	}
	if !strings.Contains(err.Error(), "test artifact path") || !strings.Contains(err.Error(), "path_test.go") {
		t.Fatalf("validation error should identify ungrounded test artifact path, got %v", err)
	}
}

func TestBuildPlannerPromptAdvertisesFlatSubmitFeaturePlanSchema(t *testing.T) {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)

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
	plan.Atoms[0].Prompt = "Use exact old_text/new_text anchors from the atom brief."
	prompt := BuildImplementorPrompt(FeatureAtomState{FeatureAtomPlan: plan.Atoms[0]}, plan)

	for _, want := range []string{
		`This atom brief is consumed by plan-execute`,
		`Plan-execute owns the terminal response protocol`,
		`do not emit feature-planner completion JSON`,
		`Do not claim files were edited or tests were run`,
		`unsupported edit/test claims are rejected as recovery-open`,
		`Requested verification commands for executor phase:`,
		"Required verification command: run `go test ./...` after edits.",
		`Atom implementation brief:`,
		`Use exact old_text/new_text anchors from the atom brief.`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementor prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, stale := range []string{
		`"tool":"question_for_planner"`,
		`You may answer with JSON`,
	} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("implementor prompt retained feature-planner terminal surface %q:\n%s", stale, prompt)
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

func TestDispatchRecordsPlanExecuteLaneFamily(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != "feature_planner_implementor" {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","summary":"go test ./internal/featureplanner passed"}`, nil
	})

	if len(events) != 2 {
		t.Fatalf("events = %+v, want dispatch and complete", events)
	}
	dispatch := events[0]
	if dispatch.Type != "idiom.feature_planner.atom_dispatched" || len(dispatch.ActiveRunDeltas) != 1 {
		t.Fatalf("unexpected dispatch event: %+v", dispatch)
	}
	if dispatch.ActiveRunDeltas[0].LaneFamily != "plan-execute" {
		t.Fatalf("lane family = %q, want plan-execute", dispatch.ActiveRunDeltas[0].LaneFamily)
	}
	complete := events[1]
	if got := fmt.Sprint(complete.Fields["lane_family"]); got != "plan-execute" {
		t.Fatalf("completion lane family = %q, want plan-execute; event=%+v", got, complete)
	}
	if got := fmt.Sprint(complete.Fields["nested_status"]); got != "completed" {
		t.Fatalf("nested status = %q, want completed; event=%+v", got, complete)
	}
}

func TestDispatchRejectsUnsupportedImplementorJSONTool(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != "feature_planner_implementor" {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		return `{"tool":"plan_execute_complete","summary":"atom implemented"}`, nil
	})

	if state.Status != FeatureStateRecoveryOpen {
		t.Fatalf("state.Status = %q, want recovery-open; events=%+v", state.Status, events)
	}
	if state.Atoms[0].State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open", state.Atoms[0].State)
	}
	if len(state.Atoms[0].Blockers) == 0 || !strings.Contains(state.Atoms[0].Blockers[0], "unsupported completion tool") {
		t.Fatalf("missing unsupported-tool blocker: %+v", state.Atoms[0].Blockers)
	}
}

func TestDispatchRejectsMalformedOrEmptyImplementorJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "malformed", raw: `{"summary":`, want: "malformed JSON completion envelope"},
		{name: "empty tool", raw: `{"summary":"implemented"}`, want: "JSON without a supported tool"},
		{name: "incomplete question summary", raw: `{"question_summary":"blocked"}`, want: "JSON without a supported tool"},
		{name: "incomplete blocking claim", raw: `{"blocking_claim":"needs decision"}`, want: "JSON without a supported tool"},
		{name: "empty tool question-shaped", raw: `{"question_summary":"blocked","blocking_claim":"needs decision"}`, want: "JSON without a supported tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
			ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

			events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
				if taskFamily != "feature_planner_implementor" {
					t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
				}
				return tt.raw, nil
			})

			if state.Status != FeatureStateRecoveryOpen {
				t.Fatalf("state.Status = %q, want recovery-open; events=%+v", state.Status, events)
			}
			if state.Atoms[0].State != AtomStateRecoveryOpen {
				t.Fatalf("atom state = %q, want recovery-open", state.Atoms[0].State)
			}
			if len(state.Atoms[0].Blockers) == 0 || !strings.Contains(state.Atoms[0].Blockers[0], tt.want) {
				t.Fatalf("missing blocker %q: %+v", tt.want, state.Atoms[0].Blockers)
			}
		})
	}
}

func TestDispatchStopsAfterBlockedAtomWithoutSyntheticSiblingRuns(t *testing.T) {
	plan := validPlan()
	second := plan.Atoms[0]
	second.ID = "atom-2"
	second.AtomID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels2.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snap-tdd-2"
	second.PlannedTDDSnapshot.SnapshotDigest = "sha256:test2"
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var implementorCalls int
	events := DispatchReadyAtoms(&state, 5, func(_ string, taskFamily string) (string, error) {
		if taskFamily != "feature_planner_implementor" {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		implementorCalls++
		return `{"tool":"question_for_planner","question_summary":"first atom blocked","blocking_claim":"needs product decision"}`, nil
	})

	if implementorCalls != 1 {
		t.Fatalf("expected dispatch to stop after first blocked child, got %d calls", implementorCalls)
	}
	if state.Atoms[0].State != AtomStateQuestionForPlanner {
		t.Fatalf("first atom state = %q, want question-for-planner", state.Atoms[0].State)
	}
	if state.Atoms[1].State != AtomStateQueued {
		t.Fatalf("sibling atom should remain queued, got %q", state.Atoms[1].State)
	}
	for _, event := range events {
		if event.AtomID == "atom-2" && event.Type == "idiom.feature_planner.atom_dispatched" {
			t.Fatalf("sibling atom must not be synthetically dispatched after blocker: %+v", events)
		}
	}
}

func TestDispatchStopsAfterPreDispatchTDDBlocker(t *testing.T) {
	plan := validPlan()
	second := plan.Atoms[0]
	second.ID = "atom-2"
	second.AtomID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels2.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snap-tdd-2"
	second.PlannedTDDSnapshot.SnapshotDigest = "sha256:test2"
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	state.TDDPreExecutionEvidence = nil

	var implementorCalls int
	events := DispatchReadyAtoms(&state, 5, func(_ string, _ string) (string, error) {
		implementorCalls++
		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","summary":"go test ./internal/featureplanner passed"}`, nil
	})

	if implementorCalls != 0 {
		t.Fatalf("implementor must not run after pre-dispatch TDD blocker, got %d call(s)", implementorCalls)
	}
	if state.Atoms[0].State != AtomStateBlockedMissingTDD {
		t.Fatalf("first atom state = %q, want blocked-missing-tdd", state.Atoms[0].State)
	}
	if state.Atoms[1].State != AtomStateQueued {
		t.Fatalf("sibling atom should remain queued, got %q", state.Atoms[1].State)
	}
	for _, event := range events {
		if event.AtomID == "atom-2" && event.Type == "idiom.feature_planner.atom_dispatched" {
			t.Fatalf("sibling atom must not dispatch after pre-dispatch blocker: %+v", events)
		}
	}
}

func TestDispatchDoesNotCompletePlanExecuteQuestion(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != "feature_planner_implementor" {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		return `{"tool":"question_for_planner","lane_family":"plan-execute","question_summary":"Plan-execute could not produce a plan artifact.","blocking_claim":"plan-execute did not complete the atom implementation contract"}`, nil
	})

	if state.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question-open; events=%+v", state.Status, events)
	}
	if len(state.OpenQuestions) != 1 {
		t.Fatalf("open questions = %+v", state.OpenQuestions)
	}
	if state.Atoms[0].State != AtomStateQuestionForPlanner {
		t.Fatalf("atom state = %q, want question-for-planner", state.Atoms[0].State)
	}
	for _, event := range events {
		if event.Type == "idiom.feature_planner.atom_completed" {
			t.Fatalf("blocked plan-execute output must not complete atom: %+v", events)
		}
	}
}

func TestApplyImplementorOutputQuestionPrefixOpensPlannerQuestion(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	atom := &state.Atoms[0]
	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.ActiveRuns = []ActiveRun{{RunID: "atom-1-run-1", AtomID: atom.ID, State: "running"}}

	events := ApplyImplementorOutput(&state, atom, "<<QUESTION>>: Plan-execute could not produce a plan artifact.")

	if state.Status != FeatureStatePlanningQuestion || atom.State != AtomStateQuestionForPlanner {
		t.Fatalf("question prefix should open planner question, state=%q atom=%q events=%+v", state.Status, atom.State, events)
	}
	if len(state.OpenQuestions) != 1 || !strings.Contains(state.OpenQuestions[0].QuestionSummary, "Plan-execute") {
		t.Fatalf("unexpected open questions: %+v", state.OpenQuestions)
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

func TestApplyImplementorOutputAcceptsObservedPlanExecuteCompletion(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	atom := &state.Atoms[0]
	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.ActiveRuns = []ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        atom.ID,
		LaneSessionID: "atom-1-run-1",
		LaneFamily:    "plan-execute",
		State:         "running",
	}}

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"complete","review_status":"skipped","review_repair_skipped":true,"progress_path":".idiom/plan-execute/session/state.json","summary":"Edited files and go test ./internal/featureplanner passed"}`)

	if atom.State != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v", atom.State, atom.Blockers)
	}
	if state.Status != FeatureStateComplete {
		t.Fatalf("feature state = %q, want complete", state.Status)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := fmt.Sprint(events[0].Fields["progress_path"]); got != ".idiom/plan-execute/session/state.json" {
		t.Fatalf("progress_path = %q, want delegated plan-execute path", got)
	}
	if got := fmt.Sprint(events[0].Fields["nested_review_status"]); got != "skipped" {
		t.Fatalf("nested review status = %q, want skipped", got)
	}
	if got := events[0].Fields["review_repair_skipped"]; got != true {
		t.Fatalf("review_repair_skipped = %v, want true", got)
	}
}

func TestApplyImplementorOutputAcceptsNestedPlanExecuteCompletionStatus(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","plan_execute":{"lane_family":"plan-execute","status":"completed","phase":"review_repair","review_status":"completed","progress_path":".idiom/plan-execute/session/state.json"}}`)

	if atom.State != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := fmt.Sprint(events[0].Fields["nested_status"]); got != "completed" {
		t.Fatalf("nested status = %q, want completed; fields=%+v", got, events[0].Fields)
	}
	if got := fmt.Sprint(events[0].Fields["nested_phase"]); got != "review_repair" {
		t.Fatalf("nested phase = %q, want review_repair; fields=%+v", got, events[0].Fields)
	}
}

func TestApplyImplementorOutputRejectsPlanExecuteCompletionWithoutNestedStatus(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","progress_path":".idiom/plan-execute/session/state.json","summary":"nested output without typed status"}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "missing required nested plan-execute status")
	if got := fmt.Sprint(events[0].Fields["nested_status"]); got != "" {
		t.Fatalf("nested_status = %q, want empty observable field", got)
	}
}

func TestApplyImplementorOutputAcceptsDegradedPlanExecuteReview(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"degraded","phase":"review_repair","review_status":"degraded","progress_path":".idiom/plan-execute/session/state.json","summary":"review repair degraded"}`)

	if atom.State != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := fmt.Sprint(events[0].Fields["nested_status"]); got != "degraded" {
		t.Fatalf("nested_status = %q, want degraded", got)
	}
	if got := fmt.Sprint(events[0].Fields["nested_review_status"]); got != "degraded" {
		t.Fatalf("nested_review_status = %q, want degraded", got)
	}
}

func TestApplyImplementorOutputRejectsPlanExecuteModelError(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"review_repair","review_status":"review_model_error","failure_kind":"review_model_error","failure_reason":"model returned malformed review"}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute reported failure details")
	if got := fmt.Sprint(events[0].Fields["nested_failure_kind"]); got != "review_model_error" {
		t.Fatalf("nested_failure_kind = %q, want review_model_error", got)
	}
	if containsString(state.Continuation.CompletedAtoms, atom.ID) {
		t.Fatalf("model-error nested completion must not record completed atom: %+v", state.Continuation)
	}
}

func TestApplyImplementorOutputPromotesNestedPlanExecuteFailureDetailToBlocker(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: target.txt source evidence unavailable","progress_path":".idiom/plan-execute/sessions/pe.json"}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute reported failure details: planner_blocked - <<QUESTION>>: target.txt source evidence unavailable")
	if len(atom.Blockers) == 0 || !strings.Contains(atom.Blockers[0], "target.txt source evidence unavailable") {
		t.Fatalf("atom blocker should preserve nested failure detail, blockers=%+v", atom.Blockers)
	}
	if got := fmt.Sprint(events[0].Fields["nested_failure_kind"]); got != "planner_blocked" {
		t.Fatalf("nested_failure_kind = %q, want planner_blocked", got)
	}
	if got := fmt.Sprint(events[0].Fields["progress_path"]); got != ".idiom/plan-execute/sessions/pe.json" {
		t.Fatalf("progress_path = %q, want child progress state", got)
	}
}

func TestApplyImplementorOutputRejectsImplicitSkippedReview(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"complete","review_status":"skipped"}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute skipped review is not explicitly marked by design")
}

func implementingAtomForCompletionTest(t *testing.T) (FeatureState, *FeatureAtomState) {
	t.Helper()
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
		LaneFamily:    "plan-execute",
		State:         "running",
	}}
	return state, atom
}

func assertRejectedPlanExecuteCompletion(t *testing.T, state FeatureState, atom *FeatureAtomState, events []Event, wantMessage string) {
	t.Helper()
	if atom.State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open; events=%+v", atom.State, events)
	}
	if state.Status != FeatureStateRecoveryOpen {
		t.Fatalf("feature state = %q, want recovery-open", state.Status)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.plan_execute_completion_rejected" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(events[0].Message, wantMessage) {
		t.Fatalf("message = %q, want %q", events[0].Message, wantMessage)
	}
	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded after rejected completion: %+v", state.ActiveRuns)
	}
	if containsString(state.Continuation.CompletedAtoms, atom.ID) {
		t.Fatalf("rejected atom must not be recorded complete: %+v", state.Continuation)
	}
	if !containsString(state.Continuation.BlockedAtoms, atom.ID) {
		t.Fatalf("rejected atom should be blocked: %+v", state.Continuation)
	}
}

func TestRunReconcilesActiveRunsBeforeResumeActiveRun(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	result, err := Run(state, RunOptions{
		Prompt:                "resume",
		MaxPlanningIterations: 3,
		ReconcileActiveRuns: func(state *FeatureState) []Event {
			return ApplyImplementorOutput(state, &state.Atoms[0], `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: target.txt source evidence unavailable","progress_path":".idiom/plan-execute/sessions/pe.json"}`)
		},
	}, func(_ string, taskFamily string) (string, error) {
		t.Fatalf("runner should not be called after active-run reconciliation, taskFamily=%s", taskFamily)
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStateRecoveryOpen {
		t.Fatalf("feature state = %q, want recovery-open", result.State.Status)
	}
	if len(result.State.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded by reconciliation: %+v", result.State.ActiveRuns)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "idiom.feature_planner.plan_execute_completion_rejected" {
		t.Fatalf("expected reconciled terminal event, got %+v", result.Events)
	}
	if !containsString(result.State.Continuation.BlockedAtoms, atom.ID) {
		t.Fatalf("atom should be blocked after reconciliation: %+v", result.State.Continuation)
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

func validProductContextPlan() FeaturePlan {
	plan := validPlan()
	plan.SDDContract.Refs = nil
	plan.SDDContract.PRDRequirementRefs = []string{"REQ-1"}
	plan.SDDContract.ADRRefs = []string{"ADR-1"}
	plan.Atoms[0].SDDRefs = []string{"REQ-1", "ADR-1"}
	plan.Atoms[0].TDDObligations = []string{"TDD-1"}
	return plan
}

func validProductWorkflowBrief() ProductWorkflowBrief {
	return ProductWorkflowBrief{
		CoverageGatePassed: true,
		PRDRequirements: []PRDRequirementBrief{{
			ID:          "REQ-1",
			Obligations: []string{"TDD-1"},
		}},
		AcceptedADRIDs: []string{"ADR-1"},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Behavior:       "Short label changes",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
		CoverageTier:         "contract_high",
		FilesToModify:        []string{"internal/labels/labels.go", "internal/labels/labels_test.go"},
		ContextEvidencePaths: []string{"internal/labels/labels.go"},
		Tests:                []string{"go test ./..."},
	}
}
