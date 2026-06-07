package featureplanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/taskfamily"
)

func TestValidatePlanRequiresTDDBeforeImplementation(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].TestFirst.Commands = nil

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "missing test_first.commands") {
		t.Fatalf("expected missing TDD command error, got %v", err)
	}
}

func TestValidateDurableStateTreatsSourceSatisfiedAsTerminal(t *testing.T) {
	state := NewState("source-satisfied-session", "already done", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStateSourceSatisfied
	state.PlanningLoop.State = FeatureStateSourceSatisfied
	state.OpenQuestions = []PlannerQuestion{{ID: "q-1", Status: "open", QuestionSummary: "stale question"}}

	err := ValidateDurableState(state)
	if err == nil || !strings.Contains(err.Error(), "terminal feature state has open planner questions") {
		t.Fatalf("source-satisfied state should reject terminal-incompatible open questions, got %v", err)
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

func TestValidatePlanProductWorkflowContextAllowsPendingTDDVerificationDispatch(t *testing.T) {
	plan := validProductContextPlan()
	brief := validProductWorkflowBrief()
	brief.CoverageGatePassed = false
	brief.CoverageBlockers = []string{"expected all 1 TDD obligations to have verification evidence"}

	if err := ValidatePlanProductWorkflowContext(plan, brief); err != nil {
		t.Fatalf("pending TDD verification evidence should not block implementation dispatch: %v", err)
	}
}

func TestValidatePlanProductWorkflowContextRejectsNonTDDCoverageBlocker(t *testing.T) {
	plan := validProductContextPlan()
	brief := validProductWorkflowBrief()
	brief.CoverageGatePassed = false
	brief.CoverageBlockers = []string{"expected EARS requirements to map to evidence"}

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected non-TDD coverage blocker to reject feature plan")
	}
	for _, want := range []string{"coverage gate is blocked", "expected EARS requirements"} {
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
		if taskFamily != string(taskfamily.FeaturePlannerPlanner) {
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
	required := normalized.Atoms[0].RequiredEditPaths
	for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go", "internal/labels/labels_extra.go"} {
		if !containsString(required, want) {
			t.Fatalf("Files to modify path %q was not promoted to required scope: %#v", want, required)
		}
	}
	for _, forbidden := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go", "internal/labels/labels_extra.go"} {
		if containsString(allowed, forbidden) {
			t.Fatalf("required edit path %q leaked into allowed scope: %#v", forbidden, allowed)
		}
	}
}

func TestValidateProductPlanRejectsContextEvidenceAsEditScope(t *testing.T) {
	plan := validProductContextPlan()
	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go", "internal/context/context.go"}
	brief := validProductWorkflowBrief()
	brief.FilesToModify = []string{"internal/labels/labels.go"}
	brief.ContextEvidencePaths = []string{"internal/context/context.go"}

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected context evidence path in edit scope to reject product plan")
	}
	if !strings.Contains(err.Error(), "internal/context/context.go") ||
		!strings.Contains(err.Error(), "files_to_modify") {
		t.Fatalf("validation error should identify context evidence as non-edit scope, got %v", err)
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

func TestValidateProductPlanRequiresFilesToModifyCoverageAcrossAtoms(t *testing.T) {
	plan := validProductContextPlan()
	second := plan.Atoms[0]
	second.ID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels_test.go"}
	plan.Atoms = append(plan.Atoms, second)
	brief := validProductWorkflowBrief()
	brief.FilesToModify = append(brief.FilesToModify, "internal/labels/labels_extra.go")

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected missing Files to modify coverage to reject product plan")
	}
	if !strings.Contains(err.Error(), "internal/labels/labels_extra.go") ||
		!strings.Contains(err.Error(), "atom.required_edit_paths") {
		t.Fatalf("validation error should identify missing required Files to modify coverage, got %v", err)
	}
}

func TestValidateProductPlanRejectsDemotedFilesToModifyBeforeNormalization(t *testing.T) {
	plan := validProductContextPlan()
	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected demoted Files to modify path to reject product plan")
	}
	if !strings.Contains(err.Error(), "internal/labels/labels_test.go") ||
		!strings.Contains(err.Error(), "not omitted or demoted to allowed_edit_paths") {
		t.Fatalf("validation error should identify demoted Files to modify path, got %v", err)
	}
}

func TestValidateProductPlanRejectsFilesToModifyAllowedInAnotherAtom(t *testing.T) {
	plan := validProductContextPlan()
	second := plan.Atoms[0]
	second.ID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	second.AllowedEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms = append(plan.Atoms, second)
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief)
	if err == nil {
		t.Fatal("expected Files to modify path in allowed scope to reject product plan")
	}
	if !strings.Contains(err.Error(), "internal/labels/labels.go") ||
		!strings.Contains(err.Error(), "must not appear in atom.allowed_edit_paths") {
		t.Fatalf("validation error should identify Files to modify allowed-scope demotion, got %v", err)
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
		"Every path listed under Files to modify is required implementation scope",
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

func TestBuildPlannerPromptAdvertisesSourceSatisfiedNoEditGate(t *testing.T) {
	prompt := BuildPlannerPrompt(
		"Please first check whether the existing source already satisfies this. If it is already satisfied, report source-satisfied and do not edit.",
		NewState("session", "check feature", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		"Valid tools: submit_feature_plan, source_satisfied, or ask_planner_question.",
		`"tool":"source_satisfied"`,
		"preserve that gate",
		"Do not rewrite that request into unconditional implementation work.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing source-satisfied contract %q:\n%s", want, prompt)
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

func TestParsePlannerOutputAcceptsQuestionAlias(t *testing.T) {
	parsed, err := ParsePlannerOutput(`{"tool":"ask_planner_question","question":"Which source paths should be used for the tooltip?"}`)
	if err != nil {
		t.Fatalf("ParsePlannerOutput returned error: %v", err)
	}
	if parsed.Kind != ToolAskPlannerQuestion {
		t.Fatalf("kind = %q, want ask_planner_question", parsed.Kind)
	}
	if parsed.Question.QuestionSummary != "Which source paths should be used for the tooltip?" {
		t.Fatalf("question summary not populated from alias: %+v", parsed.Question)
	}
}

func TestParsePlannerOutputAcceptsSourceSatisfiedCloseout(t *testing.T) {
	raw := `{"tool":"source_satisfied","summary":"tooltip already shows contributors","reason":"current helper contains the score breakdown","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"]}`

	parsed, err := ParsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("ParsePlannerOutput returned error: %v", err)
	}
	if parsed.Kind != ToolSourceSatisfied {
		t.Fatalf("kind = %q, want source_satisfied", parsed.Kind)
	}
	if parsed.SourceSatisfied.Summary != "tooltip already shows contributors" || !slices.Equal(parsed.SourceSatisfied.EvidencePaths, []string{"scripts/ui/ui_helpers.gd"}) {
		t.Fatalf("unexpected source-satisfied payload: %+v", parsed.SourceSatisfied)
	}
}

func TestRunSourceSatisfiedCloseoutSkipsAtomDispatch(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "scripts/ui/ui_helpers.gd"), "func build_item_detail_tooltip(): pass\n")
	mkdirAndWriteFile(t, filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")
	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(taskfamily.FeaturePlannerImplementor) {
			t.Fatal("source-satisfied planner closeout must not dispatch an atom")
		}
		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"]}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStateSourceSatisfied {
		t.Fatalf("state.Status = %q, want source-satisfied; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.State.Atoms) != 0 || len(result.State.ActiveRuns) != 0 {
		t.Fatalf("source-satisfied closeout should not create dispatch state: atoms=%+v active=%+v", result.State.Atoms, result.State.ActiveRuns)
	}
	if len(result.Events) != 1 || result.Events[0].Type != EventPlannerSourceSatisfied {
		t.Fatalf("events = %+v, want planner source-satisfied event", result.Events)
	}
}

func TestRunRejectsSourceSatisfiedMissingWorkspaceEvidence(t *testing.T) {
	root := t.TempDir()
	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(taskfamily.FeaturePlannerImplementor) {
			t.Fatal("invalid source-satisfied closeout must not dispatch an atom")
		}
		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["missing.gd"]}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair", result.State.Status)
	}
	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "missing.gd") {
		t.Fatalf("blocker should name missing evidence path: %+v", result.State.Blockers)
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

func TestValidatePlanStandaloneWorkspaceContextRejectsMissingInventedPaths(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()
	plan.Atoms[0].RequiredEditPaths = []string{"src/ui/economy_masterwork_tooltip.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	mkdirAndWriteFile(t, filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot: root,
		Prompt:        "Add score breakdown to the existing tooltip.",
	})
	if err == nil {
		t.Fatal("expected missing standalone path validation error")
	}
	for _, want := range []string{
		"src/ui/economy_masterwork_tooltip.gd",
		"missing from the workspace",
		"not authorized as a new file",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func TestValidatePlanStandaloneWorkspaceContextAllowsExplicitNewFileAndTestCreation(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()
	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot: root,
		Prompt:        "Create a new helper with the following content and add regression tests.",
	}); err != nil {
		t.Fatalf("explicit new-file/test prompt should be allowed, got %v", err)
	}
}

func TestRunRejectsStandaloneMissingWorkspacePathBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()
	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].AllowedEditPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	mkdirAndWriteFile(t, filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                "Add score breakdown to the existing tooltip.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(taskfamily.FeaturePlannerImplementor) {
			t.Fatal("standalone workspace validation must block before dispatch")
		}
		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.State.Atoms) != 0 {
		t.Fatalf("invalid standalone plan should not compile atoms: %+v", result.State.Atoms)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "idiom.feature_planner.standalone_context_blocked" {
		t.Fatalf("expected standalone context blocked event, got %+v", result.Events)
	}
	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "project/scripts/economy/score_breakdown.gd") {
		t.Fatalf("blocker should name invented path: %+v", result.State.Blockers)
	}
}

func TestApplyImplementorOutputPreservesRecoveryPacketForMissingPathFailure(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{
		"tool":"plan_execute_completed",
		"lane_family":"plan-execute",
		"status":"failed",
		"phase":"planning",
		"failure_kind":"missing_edit_path_without_new_file_authorization",
		"failure_reason":"plan would create missing edit path without new-file authorization",
		"failure_id":"failure-123",
		"record_path":".idiom/recovery/failures/failure-123.json",
		"packet_path":".idiom/recovery/packets/failure-123.json",
		"next_valid_action":"review_decomposition",
		"progress_path":".idiom/plan-execute/sessions/pe.json"
	}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "missing_edit_path_without_new_file_authorization")
	if !containsString(state.OpenRecoveryPackets, ".idiom/recovery/packets/failure-123.json") {
		t.Fatalf("recovery packet path was not retained: %+v", state.OpenRecoveryPackets)
	}
	fields := events[0].Fields
	for key, want := range map[string]any{
		"failure_id":        "failure-123",
		"record_path":       ".idiom/recovery/failures/failure-123.json",
		"packet_path":       ".idiom/recovery/packets/failure-123.json",
		"next_valid_action": "review_decomposition",
	} {
		if got := fields[key]; got != want {
			t.Fatalf("event field %s = %#v, want %#v; fields=%+v", key, got, want, fields)
		}
	}
}

func TestBuildPlannerPromptAdvertisesFlatSubmitFeaturePlanSchema(t *testing.T) {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)

	for _, want := range []string{
		`return the FeaturePlan object itself`,
		`Do not nest the plan under a plan, payload, arguments, or input field`,
		`"tool":"ask_planner_question"`,
		`"question_summary"`,
		`"blocking_claim"`,
		`"requested_decision"`,
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

func TestRunOpensNormalizedPlannerQuestionFromQuestionAlias(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(_ string, taskFamily string) (string, error) {
		if taskFamily != string(taskfamily.FeaturePlannerPlanner) {
			t.Fatalf("task family = %q, want planner", taskFamily)
		}
		return `{"tool":"ask_planner_question","question":"Please provide tooltip source paths."}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("open questions = %+v", result.State.OpenQuestions)
	}
	question := result.State.OpenQuestions[0]
	if question.QuestionSummary != "Please provide tooltip source paths." ||
		question.BlockingClaim == "" ||
		question.RequestedDecision == "" ||
		question.AuthorityRequired == "" ||
		question.RequiredBefore == "" {
		t.Fatalf("question was not normalized: %+v", question)
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
		case string(taskfamily.FeaturePlannerPlanner):
			return mustPlanJSON(t, validPlan()), nil
		case string(taskfamily.FeaturePlannerImplementor):
			return testPlanExecuteCompletedJSON(), nil
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
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
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

func TestDispatchUsesPlanExecuteSafeRunIDForUnsafeAtomID(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.Atoms[0].ID = "atoms/1.3c"
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	var prompt string

	events := DispatchReadyAtoms(&state, 1, func(gotPrompt string, _ string) (string, error) {
		prompt = gotPrompt
		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","summary":"done"}`, nil
	})

	if len(events) != 2 {
		t.Fatalf("events = %+v, want dispatch and complete", events)
	}
	runID := events[0].ActiveRunDeltas[0].LaneSessionID
	if runID == "" || strings.ContainsAny(runID, "/.") {
		t.Fatalf("run id = %q, want session-safe id", runID)
	}
	if !strings.Contains(prompt, "Plan-execute session id: "+runID) {
		t.Fatalf("implementor prompt missing bound session id %q:\n%s", runID, prompt)
	}
	if state.Atoms[0].LastRunID != runID {
		t.Fatalf("atom LastRunID = %q, want %q", state.Atoms[0].LastRunID, runID)
	}
}

func TestDispatchRejectsRawSourceSatisfiedWithoutTypedPlanExecuteEvidence(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		return "Decision: source-satisfied\n- internal/feature.go: already implemented.", nil
	})

	if state.Status != FeatureStateRecoveryOpen {
		t.Fatalf("state.Status = %q, want recovery-open; events=%+v", state.Status, events)
	}
	if state.Atoms[0].State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open", state.Atoms[0].State)
	}
	if len(events) != 2 || events[1].Type != "idiom.feature_planner.unverified_completion_rejected" {
		t.Fatalf("events = %+v, want dispatch then unverified completion rejection", events)
	}
	if len(state.Atoms[0].Blockers) == 0 || !strings.Contains(state.Atoms[0].Blockers[0], "typed \"plan_execute_completed\"") {
		t.Fatalf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers)
	}
}

func TestDispatchRejectsRawProseWithoutTypedPlanExecuteEvidence(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}
		return "Implemented atom.", nil
	})

	if state.Status != FeatureStateRecoveryOpen {
		t.Fatalf("state.Status = %q, want recovery-open; events=%+v", state.Status, events)
	}
	if state.Atoms[0].State != AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open", state.Atoms[0].State)
	}
	if len(events) != 2 || events[1].Type != "idiom.feature_planner.unverified_completion_rejected" {
		t.Fatalf("events = %+v, want dispatch then unverified completion rejection", events)
	}
	if len(state.Atoms[0].Blockers) == 0 || !strings.Contains(state.Atoms[0].Blockers[0], "raw text without typed \"plan_execute_completed\"") {
		t.Fatalf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers)
	}
}

func TestDispatchRejectsUnsupportedImplementorJSONTool(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
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
				if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
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
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
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
		if taskFamily != string(taskfamily.FeaturePlannerImplementor) {
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
		return testPlanExecuteCompletedJSON(), nil
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
		if taskFamily == string(taskfamily.FeaturePlannerPlanner) {
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

func TestApplyImplementorOutputUsesNestedPlanExecuteTerminalClass(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","plan_execute":{"lane_family":"plan-execute","status":"completed","terminal_class":"recovery_required","outcome":"review_degraded","phase":"review_repair","review_status":"degraded","progress_path":".idiom/plan-execute/session/state.json"}}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute terminal class requires recovery")
	if got := fmt.Sprint(events[0].Fields["terminal_class"]); got != "recovery_required" {
		t.Fatalf("terminal_class = %q, want recovery_required; fields=%+v", got, events[0].Fields)
	}
}

func TestApplyImplementorOutputPreservesPlanExecuteSourceSatisfiedAtomState(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"source-satisfied","phase":"complete","progress_path":".idiom/plan-execute/session/state.json"}`)

	if atom.State != AtomStateSourceSatisfied {
		t.Fatalf("atom state = %q, want source-satisfied; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}
	if state.Status != FeatureStateComplete {
		t.Fatalf("feature state = %q, want complete", state.Status)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_source_satisfied" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := fmt.Sprint(events[0].Fields["outcome"]); got != "source_satisfied" {
		t.Fatalf("outcome = %q, want source_satisfied; fields=%+v", got, events[0].Fields)
	}
	if got := events[0].AtomTo; got != AtomStateSourceSatisfied {
		t.Fatalf("event AtomTo = %q, want source-satisfied", got)
	}
}

func TestApplyImplementorOutputAcceptsOutcomeOnlySourceSatisfiedCompletion(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","outcome":"source_satisfied","phase":"complete","progress_path":".idiom/plan-execute/session/state.json"}`)

	if atom.State != AtomStateSourceSatisfied {
		t.Fatalf("atom state = %q, want source-satisfied; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_source_satisfied" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := fmt.Sprint(events[0].Fields["nested_status"]); got != "source-satisfied" {
		t.Fatalf("nested_status = %q, want source-satisfied; fields=%+v", got, events[0].Fields)
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

func TestApplyImplementorOutputRejectsDegradedPlanExecuteReview(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"degraded","phase":"review_repair","review_status":"degraded","progress_path":".idiom/plan-execute/session/state.json","summary":"review repair degraded"}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute status is not success-shaped")
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

func TestApplyImplementorOutputPrefersNestedReviewRepairFailureOverPlanningFallback(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact","progress_path":".idiom/plan-execute/sessions/fallback.json","plan_execute":{"lane_family":"plan-execute","status":"blocked","phase":"review_repair","review_status":"blocked","failure_kind":"review_repair_blocked","failure_reason":"review repair remained blocked","progress_path":".idiom/plan-execute/sessions/active.json"}}`)

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute reported failure details: review_repair_blocked - review repair remained blocked")
	if got := fmt.Sprint(events[0].Fields["nested_failure_kind"]); got != "review_repair_blocked" {
		t.Fatalf("nested_failure_kind = %q, want review_repair_blocked; fields=%+v", got, events[0].Fields)
	}
	if got := fmt.Sprint(events[0].Fields["nested_phase"]); got != "review_repair" {
		t.Fatalf("nested_phase = %q, want review_repair; fields=%+v", got, events[0].Fields)
	}
	if got := fmt.Sprint(events[0].Fields["progress_path"]); got != ".idiom/plan-execute/sessions/active.json" {
		t.Fatalf("progress_path = %q, want active progress state; fields=%+v", got, events[0].Fields)
	}
	if strings.Contains(events[0].Message, "plan_artifact_missing") || strings.Contains(events[0].Message, "planning/no plan artifact") {
		t.Fatalf("stale planning fallback leaked into rejection message: %q", events[0].Message)
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
		if taskFamily == string(taskfamily.FeaturePlannerPlanner) {
			t.Fatal("resume from dispatching must not ask planner for a fresh plan")
		}
		return testPlanExecuteCompletedJSON(), nil
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
		case string(taskfamily.FeaturePlannerPlanner):
			return mustPlanJSON(t, validPlan()), nil
		case string(taskfamily.FeaturePlannerImplementor):
			return testPlanExecuteCompletedJSON(), nil
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
			RequiredEditPaths: []string{"internal/labels/labels.go", "internal/labels/labels_test.go"},
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

func testPlanExecuteCompletedJSON() string {
	return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"complete","review_status":"completed","completion_source":"test"}`
}

func mkdirAndWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
