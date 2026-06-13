package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/contracts/autonomy"
)

const (
	validTDDSnapshotDigest    = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	validTDDSnapshotDigestTwo = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

func TestValidatePlanRequiresTDDBeforeImplementation(t *testing.T) {
	plan := validPlan()

	plan.Atoms[0].TestFirst.Commands = nil

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "missing test_first.commands") {
		t.Fatalf("expected missing TDD command error, got %v", err)
	}
}

func TestValidatePlanRequiresTDDArtifactPathsInRequiredEditScope(t *testing.T) {
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}

	err := ValidatePlan(plan)
	if err == nil {
		t.Fatal("expected demoted TDD test artifact path to reject feature plan")
	}

	if !strings.Contains(err.Error(), "internal/labels/labels_test.go") ||
		!strings.Contains(err.Error(), "must be covered by atom.required_edit_paths") {

		t.Fatalf("validation error should identify demoted TDD test path, got %v", err)
	}
}

func TestValidatePlanRejectsDemotedImplementationPathsRequiredByPlanAndAtom(t *testing.T) {
	plan := validPlan()
	plan.Summary = "Implement CollapseEvent struct and dispatch helper, integrate into colony_sim_tick.cpp, and add focused unit tests."
	plan.TopLevelScope.Objective = "Introduce a CollapseEvent helper to encapsulate collapse dispatch logic, reducing coupling in colony_sim_tick.cpp."
	plan.TopLevelScope.SuccessCriteria = []string{
		"CollapseEvent struct and dispatch helper are implemented in native/src/simulation/world/collapse_event.cpp and declared in native/src/simulation/world/collapse_event.h.",
		"colony_sim_tick.cpp uses CollapseEvent::dispatch for collapse events.",
		"Unit tests in native/tests/test_collapse_event.cpp verify CollapseEvent behavior.",
	}
	plan.Atoms[0].Summary = "Create CollapseEvent struct in collapse_event.h and implementation in collapse_event.cpp with a static dispatch method."
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = []string{
		"native/src/simulation/world/collapse_event.h",
		"native/src/core/colony_sim_tick.cpp",
	}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].Prompt = "First, check if the work is already satisfied. Otherwise, implement CollapseEvent struct in collapse_event.h and collapse_event.cpp with a static dispatch method, update colony_sim_tick.cpp to use it, and write unit tests in test_collapse_event.cpp."

	err := ValidatePlan(plan)
	if err == nil {
		t.Fatal("expected demoted implementation paths to reject feature plan")
	}

	for _, want := range []string{
		`native/src/simulation/world/collapse_event.h`,
		`native/src/core/colony_sim_tick.cpp`,
		`required by both top-level success criteria and atom implementation text`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func TestValidatePlanKeepsOptionalAllowedImplementationPathAllowed(t *testing.T) {
	plan := validPlan()
	plan.TopLevelScope.SuccessCriteria = []string{
		"Primary behavior is implemented in internal/labels/labels.go.",
		"internal/labels/labels_extra.go may be updated if needed for shared helper cleanup.",
	}
	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_extra.go"}
	plan.Atoms[0].Prompt = "Update internal/labels/labels.go. You may update internal/labels/labels_extra.go if needed."

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("optional allowed path should remain valid: %v", err)
	}
}

func TestNewStateStoresBoundedPromptSummaryForResume(t *testing.T) {
	state := NewState("session", "Implement CollapseEvent helper.\n\nKeep it scoped.", "", 3, time.Unix(1, 0).UTC())

	if state.PromptSummary != "Implement CollapseEvent helper. Keep it scoped." {
		t.Fatalf("PromptSummary = %q", state.PromptSummary)
	}

	longState := NewState("session", strings.Repeat("x", 2500), "", 3, time.Unix(1, 0).UTC())
	if len([]rune(longState.PromptSummary)) != 2000 {
		t.Fatalf("long prompt summary length = %d, want 2000", len([]rune(longState.PromptSummary)))
	}
}

func TestValidatePlanAcceptsProductSDDRefs(t *testing.T) {
	plan := validProductContextPlan()

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("product PRD/ADR refs should satisfy the generic SDD contract: %v", err)
	}
}

func TestValidatePlanAcceptsStandaloneAcceptanceIDSDDRefs(t *testing.T) {
	plan := validPlan()
	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"SDD-COLLAPSE-EVENT-1"}

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("standalone acceptance ID should satisfy atom sdd_refs: %v", err)
	}
}

func TestValidatePlanRejectsUndeclaredAtomSDDRefs(t *testing.T) {
	plan := validPlan()
	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"REQ-001"}

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "is not declared by sdd_contract") {
		t.Fatalf("expected undeclared sdd_refs rejection, got %v", err)
	}
}

func TestValidatePlanRejectsBlankAtomSDDRefs(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].SDDRefs = []string{""}

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "missing sdd_refs") || !strings.Contains(err.Error(), "must not be blank") {
		t.Fatalf("expected blank sdd_refs rejection, got %v", err)
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

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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

	if err := ValidatePlanProductWorkflowContext(plan, brief, ""); err != nil {
		t.Fatalf("pending TDD verification evidence should not block implementation dispatch: %v", err)
	}
}

func TestValidatePlanProductWorkflowContextRejectsNonTDDCoverageBlocker(t *testing.T) {
	plan := validProductContextPlan()
	brief := validProductWorkflowBrief()

	brief.CoverageGatePassed = false
	brief.CoverageBlockers = []string{"expected EARS requirements to map to evidence"}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerPlanner) {
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

	normalized := NormalizePlanProductWorkflowContext(plan, brief, "")
	if err := ValidatePlanProductWorkflowContext(normalized, brief, ""); err != nil {
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

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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

	normalized := NormalizePlanProductWorkflowContext(plan, brief, "")
	if err := ValidatePlanProductWorkflowContext(normalized, brief, ""); err != nil {
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

func TestValidateProductPlanUsesExplicitWorkspaceRootForPackageGrounding(t *testing.T) {
	root, plan, brief := workspaceRootProductPlanFixture(t)

	if err := ValidatePlanProductWorkflowContext(plan, brief, root); err != nil {
		t.Fatalf("explicit workspace root should ground sibling package scope: %v", err)
	}

	normalized := NormalizePlanProductWorkflowContext(plan, brief, root)
	if !containsString(normalized.Atoms[0].AllowedEditPaths, "pkg/generated_helper.go") {
		t.Fatalf("expected sibling package scope to remain allowed, got %#v", normalized.Atoms[0].AllowedEditPaths)
	}
}

func TestRunProductPlanUsesWorkspaceRootForPackageGrounding(t *testing.T) {
	root, plan, brief := workspaceRootProductPlanFixture(t)
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:               "build feature",
		ProductWorkflowBrief: &brief,
		WorkspaceRoot:        root,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStateComplete {
		t.Fatalf("state.Status = %q, want complete; blockers=%+v events=%+v", result.State.Status, result.State.Blockers, result.Events)
	}
}

func workspaceRootProductPlanFixture(t *testing.T) (string, FeaturePlan, ProductWorkflowBrief) {
	t.Helper()

	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "pkg/feature.go"), "package pkg\n")
	mkdirAndWriteFile(t, filepath.Join(root, "pkg/feature_test.go"), "package pkg\n")

	plan := validProductContextPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/feature.go", "pkg/feature_test.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"pkg/generated_helper.go"}
	plan.Atoms[0].TestFirst.Commands = []string{"go test ./pkg"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/feature_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"go test ./pkg"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/feature_test.go"}
	plan.TDDContract.Obligations[0].Command = "go test ./pkg"

	brief := validProductWorkflowBrief()

	brief.FilesToModify = []string{"pkg/feature.go", "pkg/feature_test.go"}
	brief.ContextEvidencePaths = []string{"pkg/feature.go"}
	brief.Tests = []string{"go test ./pkg"}
	brief.TDDObligations[0].Command = "go test ./pkg"

	return root, plan, brief
}

func TestValidateProductPlanRequiresFilesToModifyCoverageAcrossAtoms(t *testing.T) {
	plan := validProductContextPlan()
	second := plan.Atoms[0]

	second.ID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels_test.go"}
	plan.Atoms = append(plan.Atoms, second)

	brief := validProductWorkflowBrief()

	brief.FilesToModify = append(brief.FilesToModify, "internal/labels/labels_extra.go")

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	if err == nil {
		t.Fatal("expected missing Files to modify coverage to reject product plan")
	}

	if !strings.Contains(err.Error(), "internal/labels/labels_extra.go") ||
		!strings.Contains(err.Error(), "atom.required_edit_paths") {

		t.Fatalf("validation error should identify missing required Files to modify coverage, got %v", err)
	}
}

func TestValidateProductPlanRejectsCommandNotAuthorizedByProductTDD(t *testing.T) {
	root, plan, brief := workspaceRootProductPlanFixture(t)

	plan.Atoms[0].TestFirst.Commands = []string{"cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanProductWorkflowContext(plan, brief, root)
	if err == nil || !strings.Contains(err.Error(), "not one of the source/product TDD commands") {
		t.Fatalf("expected product TDD command provenance rejection, got %v", err)
	}
}

func TestValidateProductPlanRejectsTopLevelTDDCommandNotAuthorizedByProductTDD(t *testing.T) {
	root, plan, brief := workspaceRootProductPlanFixture(t)

	plan.TDDContract.Obligations[0].Command = "cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"

	err := ValidatePlanProductWorkflowContext(plan, brief, root)
	if err == nil || !strings.Contains(err.Error(), "not the source/product TDD command") {
		t.Fatalf("expected top-level product TDD command provenance rejection, got %v", err)
	}
}

func TestValidateProductPlanRejectsDemotedFilesToModifyBeforeNormalization(t *testing.T) {
	plan := validProductContextPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}

	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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

	for _, forbidden := range []string{
		`"snapshot_id":"snap-tdd-`,
		`"snapshot_id":"placeholder`,
		`"snapshot_digest":"sha256:..."`,
		`"snapshot_digest":"sha256:placeholder`,
	} {
		if strings.Contains(schemaExample, forbidden) {
			t.Fatalf("product prompt schema example contains validator-rejected TDD snapshot placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestBuildPlannerPromptStandaloneUsesLanguageNeutralPlaceholders(t *testing.T) {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)
	schemaStart := strings.Index(prompt, "Required submit_feature_plan shape:")
	if schemaStart < 0 {
		t.Fatalf("standalone prompt missing schema section:\n%s", prompt)
	}
	schemaEnd := strings.Index(prompt[schemaStart:], "Set tdd_contract.obligations")
	if schemaEnd < 0 {
		t.Fatalf("standalone prompt missing post-schema contract text:\n%s", prompt)
	}
	schemaExample := prompt[schemaStart : schemaStart+schemaEnd]

	for _, forbidden := range []string{"internal/example/source.go", "internal/example/source_test.go", "path_test.go", "go test ./...", "cmake --build", "ctest", "pytest", "make test"} {
		if strings.Contains(schemaExample, forbidden) {
			t.Fatalf("standalone schema example must not contain language-specific placeholder %q:\n%s", forbidden, schemaExample)
		}
	}

	for _, want := range []string{"project/source-file.ext", "project/test-file.ext", "project-specific-test-command", "SDD-ACCEPTANCE-1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("standalone prompt missing neutral placeholder %q:\n%s", want, prompt)
		}
	}

	for _, want := range []string{
		`required_before to the scalar string "implementation"`,
		"planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs",
		"include every test_first.test_artifact_paths entry in required_edit_paths",
		"Define stable standalone acceptance IDs",
		"use those IDs in atom.sdd_refs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("standalone prompt missing schema invariant %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		`"snapshot_id":"snap-tdd-`,
		`"snapshot_id":"placeholder`,
		`"snapshot_digest":"sha256:..."`,
		`"snapshot_digest":"sha256:placeholder`,
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("standalone prompt schema example contains validator-rejected TDD snapshot placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestBuildPlannerPromptIncludesPreviousPlanningBlockers(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningRepair
	state.PlanningLoop.State = FeatureStatePlanningRepair
	state.Blockers = []string{"atom 0 missing sdd_refs"}

	prompt := BuildPlannerPrompt("repair the feature plan", state, nil)

	for _, want := range []string{
		"Previous planner blockers that must be repaired before resubmission",
		"atom 0 missing sdd_refs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing blocker context %q:\n%s", want, prompt)
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
		`"anchors":[`,
		"path names alone are not proof",
		"preserve that gate",
		"Do not rewrite that request into unconditional implementation work.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing source-satisfied contract %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPlannerPromptAdvertisesTypedPlannerQuestionAuthority(t *testing.T) {
	prompt := BuildPlannerPrompt(
		"Implement scoped atom.",
		NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		`"tool":"ask_planner_question"`,
		`"affected_paths":["repo/path"]`,
		`"affected_fields":["new_file_authorization"]`,
		`authority_required="accepted_source"`,
		`affected_fields=["source_localization"]`,
		`do not label source-evidence questions as new_file_authorization`,
		"paths inside requested_decision are explanatory text only and do not grant authority",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing typed planner-question authority contract %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPlannerPromptForbidsStandaloneVerificationAndSignatureInvention(t *testing.T) {
	prompt := BuildPlannerPrompt(
		"Implement a source-localized atom.",
		NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		"Do not invent project build systems or verification commands.",
		"ask_planner_question for the verification command",
		"Command names mentioned in this instruction are prohibited examples, not supplied project commands",
		"Do not encode guessed function signatures or call shapes in atom prompts.",
		"require exact signature/source verification first",
		"block on signature mismatch rather than inventing arguments",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing standalone invention guard %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPlannerPromptCarriesAcceptedSourceAuthorityConstraints(t *testing.T) {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.AuthorityDeltas = []PlanAuthorityDelta{{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		Actual:         "source-verified signature is EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}

	prompt := BuildPlannerPrompt("continue after accepted-source answer", state, nil)

	for _, want := range []string{
		"Accepted source authority constraints:",
		"source-verified signature is EntityID FluidNetworkSystem::find_connected_cistern_with_volume",
		"Use only these accepted source facts for helper signatures and source-localized call shapes.",
		"Remove or generalize any helper/method call in atom prompts that is not explicitly covered here",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planner prompt missing accepted-source constraint %q:\n%s", want, prompt)
		}
	}
}

func TestFormatStateSummaryShowsTypedAnswerFormatForAcceptedSourceQuestion(t *testing.T) {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionSummary:   "Provide accepted source facts",
		AuthorityRequired: "accepted_source",
		Status:            "open",
	}}

	summary := FormatStateSummary(state, ".idiom/feature-planner/session/state.json")

	for _, want := range []string{
		"question: Provide accepted source facts",
		`answer_format: JSON object with answer, answer_source_kind="accepted_source", and evidence_refs`,
		"next: answer_planner_question",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("state summary missing typed-answer guidance %q:\n%s", want, summary)
		}
	}
}

func TestValidatePlanRejectsPromptPathOutsideDeclaredScope(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Edit internal/labels/labels.go and wire native/src/simulation/machines/machine.cpp for recipe start."

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "native/src/simulation/machines/machine.cpp") || !strings.Contains(err.Error(), "outside required_edit_paths") {
		t.Fatalf("expected prompt path scope rejection, got %v", err)
	}
}

func TestValidatePlanAllowsPromptPathDeclaredByVerificationCommand(t *testing.T) {
	plan := validPlan()
	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	plan.Atoms[0].Prompt = "Verify with: " + command
	plan.Atoms[0].TestFirst.Commands = []string{command}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{command}

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("prompt command paths declared by test commands should pass: %v", err)
	}
}

func TestValidatePlanRejectsGuessedHelperCallWithoutSignatureEvidence(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Call find_connected_cistern_with_volume(eid) before posting fallback water haul."

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "find_connected_cistern_with_volume(eid)") || !strings.Contains(err.Error(), "source-verified signature") {
		t.Fatalf("expected guessed helper-call rejection, got %v", err)
	}

	plan.Atoms[0].Prompt = "Current source evidence: exact signature is EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required). Call it only with verified arguments."
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("source-verified signature prompt should pass: %v", err)
	}
}

func TestValidatePlanAllowsMultipleSourceVerifiedSignaturesAfterMarker(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Source-verified signatures:\n- MachineSystem::try_consume_inputs(EntityID machine_id, const Recipe& recipe)\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n\nCall find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required)."

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("all signatures after source-verified marker should satisfy helper calls: %v", err)
	}
}

func TestValidatePlanDoesNotLetSignatureMarkerSelfAuthorizeLaterCalls(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Source-verified signatures:\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n- Call missing_helper(eid) before posting fallback water haul.\n\nCall missing_helper(eid) before posting fallback water haul."

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "missing_helper(eid)") {
		t.Fatalf("signature marker must not authorize later prose calls, got %v", err)
	}
}

func TestValidatePlanDoesNotTreatNegatedSignatureMarkerAsAuthority(t *testing.T) {
	for _, prompt := range []string{
		"No source-verified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"unverified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"not verified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"source-verified signature is not missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"Current source evidence: exact signature is missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt

		err := ValidatePlan(plan)
		if err == nil || !strings.Contains(err.Error(), "missing_helper(eid)") {
			t.Fatalf("negated signature marker must not authorize helper calls, got %v for prompt %q", err, prompt)
		}
	}
}

func TestPromptSignatureDeclarationsAllowLanguageNeutralSuffixes(t *testing.T) {
	for _, prompt := range []string{
		"Source-verified signatures:\n- func renderLabel(name string) string\n\nCall renderLabel(name).",
		"Source-verified signatures:\n- fn helper(x: i32) -> bool\n\nCall helper(x).",
		"Source-verified signatures:\n- helper(x: string): boolean\n\nCall helper(x).",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt

		if err := ValidatePlan(plan); err != nil {
			t.Fatalf("language-neutral source signature should authorize call, got %v for prompt %q", err, prompt)
		}
	}
}

func TestValidatePlanDoesNotTreatBareCallBulletAsSignatureDeclaration(t *testing.T) {
	for _, bullet := range []string{
		"- missing_helper(eid)",
		"- missing_helper(eid) before fallback",
		"- missing_helper(eid): before fallback",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = "Source-verified signatures:\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n" + bullet + "\n\nCall missing_helper(eid) before posting fallback water haul."

		err := ValidatePlan(plan)
		if err == nil || !strings.Contains(err.Error(), "missing_helper(eid)") {
			t.Fatalf("bare call-shaped bullet must not authorize helper calls, got %v for bullet %q", err, bullet)
		}
	}
}

func TestValidatePlanRejectsGuessedHelperCallDespiteUnrelatedSourceEvidence(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Current source evidence: labels.go contains render_label(). Then call find_connected_cistern_with_volume(eid) before posting fallback water haul."

	err := ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "find_connected_cistern_with_volume(eid)") {
		t.Fatalf("expected unrelated source evidence to not authorize guessed helper call, got %v", err)
	}
}

func TestProductSourceFactRequiresAcceptedSourceAuthority(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Call find_connected_cistern_with_volume(eid) before posting fallback water haul."
	brief := validProductWorkflowBrief()
	brief.SourceFacts = []SourceFactBrief{{
		Path:      "internal/labels/labels.go",
		Symbol:    "find_connected_cistern_with_volume",
		Signature: "EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
	}}

	err := errors.Join(validateAtomPromptVerifiedCalls("atom 0", plan.Atoms[0], newAtomPromptGrounding(plan.Atoms[0], &brief))...)
	if err == nil || !strings.Contains(err.Error(), "find_connected_cistern_with_volume(eid)") {
		t.Fatalf("source fact without accepted authority should not satisfy helper call, got %v", err)
	}

	brief.SourceFacts[0].AuthorityRef = "accepted-source:q-1"
	if err := errors.Join(validateAtomPromptVerifiedCalls("atom 0", plan.Atoms[0], newAtomPromptGrounding(plan.Atoms[0], &brief))...); err != nil {
		t.Fatalf("accepted source fact should satisfy helper call: %v", err)
	}
}

func TestFeaturePlannerDoesNotTreatFailsRedAsHelperCall(t *testing.T) {
	if plannerCallNeedsSignatureEvidence("fails", "red") {
		t.Fatal("plain prose fails(red) must not require source signature evidence")
	}
	if !plannerCallNeedsSignatureEvidence("fail", "reason") {
		t.Fatal("real fail(reason) helper call should still require source signature evidence")
	}
}

func TestFeaturePlannerDoesNotTreatParentheticalExamplesAsHelperCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "water", args: "e.g., recipe.requiresWater()"},
		{name: "job", args: "i.e., return true without scheduling haul"},
		{name: "ID", args: "e.g., from machine component"},
	} {
		if plannerCallNeedsSignatureEvidence(tc.name, tc.args) {
			t.Fatalf("%s(%s) prose must not require source signature evidence", tc.name, tc.args)
		}
	}

	if !plannerCallNeedsSignatureEvidence("find_connected_cistern_with_volume", "network_id, volume_required") {
		t.Fatal("real helper call with concrete args must still require source signature evidence")
	}
	if plannerCallNeedsSignatureEvidence("cover:", "1") {
		t.Fatal("list prose cover:(1) must not require source signature evidence")
	}
}

func TestValidatePlanAllowsParentheticalExamplesInAtomPrompt(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].Prompt = "When recipe needs water(e.g., recipe.requiresWater()), skip the haul job(i.e., return true without scheduling haul) using the machine ID(e.g., from machine component)."

	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("parenthetical examples must not be treated as helper calls: %v", err)
	}
}

func TestNormalizePlanPromptDeclarationsPromotesEvidenceOnlyPaths(t *testing.T) {
	plan := validPlan()
	plan.Atoms[0].EvidencePaths = nil
	plan.Atoms[0].Prompt = "Evidence-only path (do not edit): native/src/core/components.h"

	normalized := NormalizePlanPromptDeclarations(plan)
	if !slices.Contains(normalized.Atoms[0].EvidencePaths, "native/src/core/components.h") {
		t.Fatalf("evidence-only prompt path was not promoted: %+v", normalized.Atoms[0].EvidencePaths)
	}
}

func TestBuildPlannerPromptRendersSourceFactAnchors(t *testing.T) {
	brief := validProductWorkflowBrief()
	brief.SourceFacts = []SourceFactBrief{{
		Path:         "internal/labels/labels.go",
		Symbol:       "ShortLabel",
		Anchor:       `func ShortLabel() string`,
		AuthorityRef: "accepted-source:q-1",
	}}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)

	if !strings.Contains(prompt, "anchor=func ShortLabel() string") {
		t.Fatalf("planner prompt must render source fact anchors:\n%s", prompt)
	}
}

func TestRepoPathsMentionedInTextIgnoresLabelsAndShorthand(t *testing.T) {
	text := "Write/Update MachineSystem using fluid_network_system.h/.cpp and native/src/core/components.h."

	got := repoPathsMentionedInText(text)
	if slices.Contains(got, "Write/Update") || slices.Contains(got, "fluid_network_system.h/.cpp") {
		t.Fatalf("fake or shorthand paths should be ignored, got %#v", got)
	}
	if !slices.Contains(got, "native/src/core/components.h") {
		t.Fatalf("real repo path should be retained, got %#v", got)
	}
}

func TestCommandReferencedWorkspacePathsUsesCommandCWD(t *testing.T) {
	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	got := commandReferencedWorkspacePaths(command)

	for _, want := range []string{"native/tests", "bin/colony_tests", "native/tests/bin/colony_tests"} {
		if !slices.Contains(got, want) {
			t.Fatalf("command paths missing %q: %#v", want, got)
		}
	}
}

func TestValidateStandaloneWorkspaceUsesAcceptedSourceGroundingForFET33CShape(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void MachineSystem::tick_normal(float dt) {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/economy/fluid_network_system.h"), "EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const;\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/economy/fluid_network_system.cpp"), "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const { return INVALID_ENTITY; }\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/core/components.h"), "struct CisternComponent { int32_t stored; };\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "DefaultEnvironment()\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"brewery fluid cascade\") { CHECK(true); }\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755); err != nil {
		t.Fatalf("chmod colony_tests: %v", err)
	}

	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	plan := validPlan()
	plan.TopLevelScope.SuccessCriteria = []string{"FET 3.3c consumes water from a connected cistern before water hauling."}
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].EvidencePaths = []string{
		"native/src/simulation/economy/fluid_network_system.h",
		"native/src/simulation/economy/fluid_network_system.cpp",
		"native/src/core/components.h",
	}
	plan.Atoms[0].TestFirst.Commands = []string{command}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{command}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].Prompt = "Write/Update FET 3.3c. Use fluid_network_system.h/.cpp context and native/src/core/components.h. Call find_connected_cistern_with_volume(network_id, volume_required) only after source verification."

	contract := StampPlanAuthorityContract(&plan, []PlanAuthorityDelta{{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		Expected:       "Need source-localized helper signature.",
		Actual:         "Accepted source: EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const in native/src/simulation/economy/fluid_network_system.h.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}})

	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "find_connected_cistern_with_volume(network_id, volume_required)") {
		t.Fatalf("base plan should require source signature before accepted-source grounding, got %v", err)
	}
	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root, Contract: contract}); err != nil {
		t.Fatalf("accepted source grounding should satisfy standalone workspace context: %v", err)
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

func TestValidatePlannerQuestionRejectsPathAuthorityWithoutAffectedPaths(t *testing.T) {
	question := PlannerQuestion{
		ID:                "q-new-file",
		QuestionSummary:   "Authorize new helper",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper requires operator approval.",
		RequestedDecision: "Approve creating pkg/new_helper.go.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		Status:            "open",
		AffectedFields:    []string{"new_file_authorization"},
	}

	err := ValidatePlannerQuestion(question)
	if err == nil || !strings.Contains(err.Error(), "path-level authority requires affected_paths") {
		t.Fatalf("expected affected_paths validation error, got %v", err)
	}
}

func TestValidatePlannerQuestionSourceLocalizationIsNotPathAuthority(t *testing.T) {
	sourceQuestion := PlannerQuestion{
		ID:                "q-source",
		QuestionSummary:   "Provide source-localized scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Need exact source evidence.",
		RequestedDecision: "Provide exact existing source/test paths.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
		AffectedFields:    []string{"source_localization"},
	}

	if err := ValidatePlannerQuestion(sourceQuestion); err != nil {
		t.Fatalf("source localization should not require affected_paths: %v", err)
	}

	sourceQuestion.AffectedFields = []string{"new_file_authorization"}
	if err := ValidatePlannerQuestion(sourceQuestion); err == nil || !strings.Contains(err.Error(), "path-level authority requires affected_paths") {
		t.Fatalf("new-file authorization should require affected_paths, got %v", err)
	}
}

func TestParsePlannerOutputAcceptsSourceSatisfiedCloseout(t *testing.T) {
	raw := `{"tool":"source_satisfied","summary":"tooltip already shows contributors","reason":"current helper contains the score breakdown","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"}]}`

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
	mkdirAndWriteFile(t, filepath.Join(root, "scripts/ui/ui_helpers.gd"), "func build_item_detail_tooltip():\n\tprint(\"Quality contributors:\")\n")
	mkdirAndWriteFile(t, filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("source-satisfied planner closeout must not dispatch an atom")
		}

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"},{"path":"tests/e2e/suites/ui_overhaul_suite.gd","text":"extends RefCounted"}]}`, nil
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
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("invalid source-satisfied closeout must not dispatch an atom")
		}

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["missing.gd"],"anchors":[{"path":"missing.gd","text":"Quality contributors:"}]}`, nil
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

func TestRunRejectsSourceSatisfiedWithoutAnchors(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "scripts/ui/ui_helpers.gd"), "Quality contributors:\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("invalid source-satisfied closeout must not dispatch an atom")
		}

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"]}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "source_satisfied missing anchors") {
		t.Fatalf("blocker should require anchors: %+v", result.State.Blockers)
	}
}

func TestRunRejectsSourceSatisfiedMissingAnchorText(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "scripts/ui/ui_helpers.gd"), "func build_item_detail_tooltip(): pass\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("invalid source-satisfied closeout must not dispatch an atom")
		}

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"}]}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "anchor text not found") {
		t.Fatalf("blocker should require present anchor text: %+v", result.State.Blockers)
	}
}

func TestRunPreservesSourceSatisfiedGateInDispatchedAtomPrompt(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(t, filepath.Join(root, "scripts/ui/ui_helpers.gd"), "Quality contributors:\n")

	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].Prompt = "Implement score breakdown logic in the item tooltip helper."

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	var implementorPrompt string

	result, err := Run(state, RunOptions{
		Prompt:                "Please first check whether the existing areas already satisfy the atom. If it is already satisfied, report source-satisfied and do not edit.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			implementorPrompt = prompt
			return testPlanExecuteSourceSatisfiedJSONForPrompt(prompt), nil
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStateComplete {
		t.Fatalf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers)
	}

	for _, want := range []string{"Before editing, verify", "Decision: source-satisfied", "do not edit"} {
		if !strings.Contains(implementorPrompt, want) {
			t.Fatalf("implementor prompt missing preserved gate %q:\n%s", want, implementorPrompt)
		}
	}

	if !strings.Contains(result.State.Atoms[0].Prompt, "Decision: source-satisfied") {
		t.Fatalf("persisted atom prompt did not preserve source-satisfied gate: %q", result.State.Atoms[0].Prompt)
	}
}

func TestValidatePlanProductWorkflowContextRejectsUngroundedTestArtifacts(t *testing.T) {
	plan := validProductContextPlan()

	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"path_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"path_test.go"}

	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
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

	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:     "q-new-files",
		AuthorityKind:  "new_file_authorization",
		EvidenceRefs:   []string{"operator:q-new-files"},
		AffectedPaths:  []string{"pkg/new_helper.go", "pkg/new_helper_test.go"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "new files require explicit typed authority",
		Actual:         "operator authorized the new helper and test files",
		Revision:       1,
		CreatedAt:      time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot: root,
		Contract:      contract,
	}); err != nil {
		t.Fatalf("typed new-file/test authority should be allowed, got %v", err)
	}
}

func TestCompilePlanAuthorityContractDoesNotAuthorizeNewFileFromPathOnlyDelta(t *testing.T) {
	plan := validPlan()
	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:    "q-new-files",
		AuthorityKind: "operator_approval",
		EvidenceRefs:  []string{"operator:q-new-files"},
		AffectedPaths: []string{"pkg/new_helper.go"},
		Expected:      "new files require explicit typed authority",
		Actual:        "operator approved text mentioning pkg/new_helper.go",
		Revision:      1,
		CreatedAt:     time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})

	if len(contract.AuthorizedNewFiles) != 0 {
		t.Fatalf("path-only authority delta must not authorize new files: %+v", contract.AuthorizedNewFiles)
	}
}

func TestValidatePlanStandaloneWorkspaceContextDoesNotExistenceGateTDDMetadata(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")

	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"internal/workflows/productworkflow/coverage_gate_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"internal/workflows/productworkflow/coverage_gate_test.go"}

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot: root,
	}); err != nil {
		t.Fatalf("standalone validation should not require TDD artifact metadata to exist in target workspace: %v", err)
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
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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

func TestRunOpensLocalizationQuestionForPreparedAtomSelectionWithInventedPaths(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Summary = "Implement Atom 1.1: Add CollapseEvent handling to ColonyService with TDD and SDD contracts."
	plan.TopLevelScope.Objective = "Implement CollapseEvent handling in ColonyService."
	plan.Atoms[0].Title = "Implement CollapseEvent handling in ColonyService"
	plan.Atoms[0].RequiredEditPaths = []string{
		"src/services/colony_service.py",
		"tests/services/test_colony_service.py",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/services/test_colony_service.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/services/test_colony_service.py"}

	state := NewState("session", "pick a prepared atom", "", 3, time.Unix(1, 0).UTC())
	prompt := "As product owner: pick one genuinely unsatisfied, bounded Colony atom from the prepared next-atom candidates. Before dispatching implementation, verify current source and planning state. Favor small source-backed atoms with explicit tests."

	result, err := Run(state, RunOptions{
		Prompt:                prompt,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("standalone workspace validation must block before dispatch")
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.Events) != 2 || result.Events[0].Type != "idiom.feature_planner.standalone_context_blocked" || result.Events[1].Type != "idiom.feature_planner.planner_question_opened" {
		t.Fatalf("expected rejected-plan then localization question events, got %+v", result.Events)
	}
	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("expected one localization question, got %+v", result.State.OpenQuestions)
	}

	question := result.State.OpenQuestions[0]
	if question.AuthorityRequired != "accepted_source" || containsString(question.AffectedFields, "new_file_authorization") {
		t.Fatalf("localization question must not authorize invented files: %+v", question)
	}
	if !containsString(question.AffectedFields, "source_localization") {
		t.Fatalf("localization question missing source_localization field: %+v", question)
	}
	if !strings.Contains(question.RequestedDecision, "source-verified atom candidate") ||
		!strings.Contains(question.RequestedDecision, "src/services/colony_service.py") {
		t.Fatalf("question should request real source localization and preserve rejected paths diagnostically: %+v", question)
	}
}

func TestStandaloneUnverifiedHelperCallOpensAcceptedSourceQuestion(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n")

	plan := validPlan()
	plan.Atoms[0].Prompt = "Use accepted source localization: call find_connected_cistern_with_volume(network_id, volume_required) from internal/labels/labels.go before dispatch."
	plan.Atoms[0].EvidencePaths = []string{"internal/labels/labels.go"}

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                "Build the atom from accepted source localization and source verification. If helper signatures are not source-verified, ask for accepted source facts before dispatch.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("standalone source-signature validation must block before dispatch")
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.Events) != 2 || result.Events[0].Type != "idiom.feature_planner.standalone_context_blocked" || result.Events[1].Type != "idiom.feature_planner.planner_question_opened" {
		t.Fatalf("expected rejected-plan then source question events, got %+v", result.Events)
	}
	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("expected one open question, got %+v", result.State.OpenQuestions)
	}

	question := result.State.OpenQuestions[0]
	if question.AuthorityRequired != "accepted_source" || !containsString(question.AffectedFields, "source_localization") {
		t.Fatalf("signature question must request accepted source localization: %+v", question)
	}
	if !strings.Contains(question.RequestedDecision, "exact current helper signatures") {
		t.Fatalf("question should request source-verified signatures: %+v", question)
	}
}

func TestValidateStandaloneWorkspaceRejectsInventedCMakeVerificationCommand(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/storage/stockpile_system.cpp"), "void scan() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/SConstruct"), "# scons project\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/storage/stockpile_system.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "no CMake project evidence") {
		t.Fatalf("expected invented CMake command rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsUnsupportedVerificationCommandFamily(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"custom-test-runner ./internal/labels"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "unsupported command family") {
		t.Fatalf("expected unsupported command family rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceAcceptsRepoBackedSConsVerificationCommand(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"scons tests=yes target=template_debug"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root}); err != nil {
		t.Fatalf("repo-backed scons command should pass: %v", err)
	}
}

func TestValidateStandaloneWorkspaceAcceptsRepoBackedSConsAndLocalTestBinary(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755); err != nil {
		t.Fatalf("chmod fixture binary: %v", err)
	}
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root}); err != nil {
		t.Fatalf("repo-backed scons plus local test binary command should pass: %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsMissingLocalTestBinary(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "unsupported command family") {
		t.Fatalf("expected missing local test binary rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsNonExecutableLocalCommandPath(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "unsupported command family") {
		t.Fatalf("expected non-executable local command rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceAcceptsExecutableDotSlashLocalCommandPath(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755); err != nil {
		t.Fatalf("chmod fixture binary: %v", err)
	}
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root}); err != nil {
		t.Fatalf("executable ./ local command should pass: %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsNonExecutableDotSlashLocalCommandPath(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(t, filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "missing or non-executable local command") {
		t.Fatalf("expected non-executable ./ local command rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsInvalidCommandDirectory(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"cd ../outside && scons -j10"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "invalid workspace directory") {
		t.Fatalf("expected invalid cd rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsAbsoluteLocalCommandPath(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"/tmp/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "unsupported command family") {
		t.Fatalf("expected absolute command path rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceRejectsSkippedCommentOnlyRedConfirmedTest(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), `package labels

// Tests are SKIP-marked pending FET 3.3c.
// TODO: assert cistern decrement.
`)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "comment-only") || !strings.Contains(err.Error(), "red-confirmed") {
		t.Fatalf("expected skipped/comment-only red-confirmed test rejection, got %v", err)
	}
}

func TestValidateStandaloneWorkspaceIgnoresAssertionTokensInComments(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(t, filepath.Join(root, "internal/labels/labels_test.go"), `package labels

// add t.Fatal here when this is implemented
`)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "comment-only") {
		t.Fatalf("expected comment assertion token to be ignored, got %v", err)
	}
}

func TestRunDoesNotTreatOrdinaryCandidateFeatureAsLocalizationRepair(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Summary = "Add candidate scoring"
	plan.TopLevelScope.Objective = "Add candidate scoring"
	plan.Atoms[0].Title = "Add candidate scoring"
	plan.Atoms[0].RequiredEditPaths = []string{"src/recruiting/candidate_scoring.py"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/test_candidate_scoring.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/test_candidate_scoring.py"}

	state := NewState("session", "add candidate scoring", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add candidate scoring to the recruiting workflow.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
	if len(result.Events) != 1 || result.Events[0].Type != "idiom.feature_planner.standalone_context_blocked" {
		t.Fatalf("expected standalone context blocked only, got %+v", result.Events)
	}
	if len(result.State.OpenQuestions) != 0 {
		t.Fatalf("ordinary candidate feature must not open localization question: %+v", result.State.OpenQuestions)
	}
}

func TestRunPrefersExplicitNewFileAuthorizationOverLocalizationQuestion(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}

	mkdirAndWriteFile(t, filepath.Join(root, "native/src/core/colony_sim_tick.cpp"), "// tick\n")

	state := NewState("session", "pick a prepared atom", "", 3, time.Unix(1, 0).UTC())
	prompt := "Pick one prepared next-atom candidate. If any required new source or test file is missing, ask me for explicit product-owner authorization with the exact affected paths before dispatching implementation."

	result, err := Run(state, RunOptions{
		Prompt:                prompt,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("standalone workspace validation must block before dispatch")
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers)
	}
	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("expected one open question, got %+v", result.State.OpenQuestions)
	}
	question := result.State.OpenQuestions[0]
	if question.AuthorityRequired != "operator_approval" || !containsString(question.AffectedFields, "new_file_authorization") {
		t.Fatalf("explicit new-file prompt should keep new-file authorization precedence: %+v", question)
	}
	if containsString(question.AffectedFields, "source_localization") {
		t.Fatalf("explicit new-file prompt should not become localization question: %+v", question)
	}
}

func TestRunOpensQuestionForExplicitMissingNewFileAuthorizationPrompt(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}

	mkdirAndWriteFile(t, filepath.Join(root, "native/src/core/colony_sim_tick.cpp"), "// tick\n")

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent. If any required new source or test file is missing, ask me for explicit product-owner authorization with the exact affected paths before dispatching implementation.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("standalone workspace validation must block before dispatch")
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers)
	}

	if len(result.State.Atoms) != 0 {
		t.Fatalf("invalid standalone plan should not compile atoms: %+v", result.State.Atoms)
	}

	if len(result.Events) != 2 || result.Events[0].Type != "idiom.feature_planner.standalone_context_blocked" || result.Events[1].Type != "idiom.feature_planner.planner_question_opened" {
		t.Fatalf("expected rejected-plan then question events, got %+v", result.Events)
	}

	if len(result.State.RejectedPlanCandidates) != 1 {
		t.Fatalf("expected rejected plan candidate, got %+v", result.State.RejectedPlanCandidates)
	}

	if len(result.State.OpenQuestions) != 1 {
		t.Fatalf("expected one open question, got %+v", result.State.OpenQuestions)
	}
	question := result.State.OpenQuestions[0]
	if question.Status != "open" || question.AuthorityRequired != "operator_approval" || !containsString(question.AffectedFields, "new_file_authorization") {
		t.Fatalf("unexpected authorization question: %+v", question)
	}
	if !containsString(question.AffectedPaths, "native/src/simulation/world/collapse_event.cpp") ||
		!containsString(question.AffectedPaths, "native/tests/test_collapse_event.cpp") ||
		containsString(question.AffectedPaths, "native/src/core/colony_sim_tick.cpp") {
		t.Fatalf("question should request exact missing path authorization only: %+v", question)
	}
}

func TestAnswerOpenQuestionClearsAuthorizedNewFileBlockers(t *testing.T) {
	blocker := "atom 0 edit path \"native/src/simulation/world/collapse_event.cpp\" is missing from the workspace and is not authorized as a new file; use ask_planner_question or source-satisfied evidence with existing paths instead of inventing paths: missing standalone new-file authorization\natom 0 edit path \"native/tests/test_collapse_event.cpp\" is missing from the workspace and is not authorized as a new file; use ask_planner_question or source-satisfied evidence with existing paths instead of inventing paths: missing standalone new-file authorization\nmissing new-file authorization for: native/src/simulation/world/collapse_event.cpp, native/tests/test_collapse_event.cpp"
	other := "nested plan-execute reported failure details: planner_blocked"
	state := FeatureState{
		SessionID: "feature-session",
		Status:    FeatureStatePlanningQuestion,
		Blockers:  []string{blocker, other},
		Atoms: []FeatureAtomState{{
			FeatureAtomPlan: FeatureAtomPlan{ID: "atom-1"},
			State:           AtomStateRecoveryOpen,
			Blockers:        []string{blocker, other},
		}},
		OpenQuestions: []PlannerQuestion{{
			ID:                "q-001",
			QuestionSummary:   "Authorize new files",
			BlockingClaim:     "new files need approval",
			QuestionKind:      "external_answer_required",
			AuthorityRequired: "operator_approval",
			AffectedPaths:     []string{"native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
			AffectedFields:    []string{"new_file_authorization"},
			Status:            "open",
		}},
	}

	events := answerOpenQuestion(&state, "Approved. You may create native/src/simulation/world/collapse_event.cpp and native/tests/test_collapse_event.cpp.")
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.planner_question_answered" {
		t.Fatalf("unexpected answer events: %+v", events)
	}
	if slices.Contains(state.Blockers, blocker) || slices.Contains(state.Atoms[0].Blockers, blocker) {
		t.Fatalf("authorized new-file blocker should be cleared, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers)
	}
	if !slices.Contains(state.Blockers, other) || !slices.Contains(state.Atoms[0].Blockers, other) {
		t.Fatalf("unrelated blockers should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers)
	}
}

func TestAnswerOpenQuestionDenialDoesNotClearNewFileBlockers(t *testing.T) {
	blocker := "missing new-file authorization for: native/src/simulation/world/collapse_event.cpp, native/tests/test_collapse_event.cpp"
	state := FeatureState{
		SessionID: "feature-session",
		Status:    FeatureStatePlanningQuestion,
		Blockers:  []string{blocker},
		Atoms: []FeatureAtomState{{
			FeatureAtomPlan: FeatureAtomPlan{ID: "atom-1"},
			State:           AtomStateRecoveryOpen,
			Blockers:        []string{blocker},
		}},
		OpenQuestions: []PlannerQuestion{{
			ID:                "q-001",
			QuestionSummary:   "Authorize new files",
			BlockingClaim:     "new files need approval",
			QuestionKind:      "external_answer_required",
			AuthorityRequired: "operator_approval",
			AffectedPaths:     []string{"native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
			AffectedFields:    []string{"new_file_authorization"},
			Status:            "open",
		}},
	}

	events := answerOpenQuestion(&state, "No, do not create native/src/simulation/world/collapse_event.cpp or native/tests/test_collapse_event.cpp.")
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.planner_question_answered" {
		t.Fatalf("unexpected answer events: %+v", events)
	}
	if !slices.Contains(state.Blockers, blocker) || !slices.Contains(state.Atoms[0].Blockers, blocker) {
		t.Fatalf("denied new-file blocker should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers)
	}
	if deltaAuthorizesNewFile(state.AuthorityDeltas[0]) {
		t.Fatalf("denial answer must not compile as new-file authorization: %+v", state.AuthorityDeltas[0])
	}
}

func TestNewFileAuthorizationDenialPhrasesWinOverApprovalWords(t *testing.T) {
	base := PlanAuthorityDelta{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		AffectedPaths:  []string{"native/src/simulation/world/collapse_event.cpp"},
		AffectedFields: []string{"new_file_authorization"},
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}

	for _, actual := range []string{
		"No authorization is approved for these files.",
		"This is not allowed.",
		"No approval; do not create the files.",
		"Do not approve creating native/src/simulation/world/collapse_event.cpp.",
		"Don't approve creating native/src/simulation/world/collapse_event.cpp.",
		"Don't authorize native/src/simulation/world/collapse_event.cpp.",
	} {
		delta := base
		delta.Actual = actual
		if deltaAuthorizesNewFile(delta) {
			t.Fatalf("denial phrase compiled as authorization: %q", actual)
		}
	}

	approved := base
	approved.Actual = "Approved. You may create exactly native/src/simulation/world/collapse_event.cpp."
	if !deltaAuthorizesNewFile(approved) {
		t.Fatalf("explicit approval should compile as new-file authorization")
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
		`"affected_paths"`,
		`"affected_fields"`,
		`paths inside requested_decision are explanatory text only and do not grant authority`,
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerPlanner) {
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

func TestBuildImplementorPromptCarriesTypedPlanNewFileAuthority(t *testing.T) {
	plan := validPlan()
	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:     "q-new-file",
		AuthorityKind:  "new_file_authorization",
		EvidenceRefs:   []string{"operator:q-new-file"},
		AffectedPaths:  []string{"pkg/new_helper.go"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "new helper requires explicit typed authority",
		Actual:         "operator authorized the helper",
		Revision:       1,
		CreatedAt:      time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})
	prompt := BuildImplementorPromptFromContract(FeatureAtomState{FeatureAtomPlan: plan.Atoms[0]}, plan, contract)

	if !strings.Contains(prompt, "Typed plan execution contract:") {
		t.Fatalf("implementor prompt missing typed plan execution contract:\n%s", prompt)
	}

	if got := PlanExecuteAuthorizedNewFilesFromImplementorPrompt(prompt); !slices.Equal(got, []string{"pkg/new_helper.go"}) {
		t.Fatalf("authorized new files = %#v", got)
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

func TestApplyPlannerOutputStampsTDDSnapshotIdentity(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.Atoms[0].PlannedTDDSnapshot.SnapshotID = "model-authored-snapshot"
	plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	if len(events) == 0 || state.Status != FeatureStateDispatching {
		t.Fatalf("expected accepted plan, status=%q events=%+v blockers=%v", state.Status, events, state.Blockers)
	}

	gotSnapshot := state.Plan.Atoms[0].PlannedTDDSnapshot
	if gotSnapshot.SnapshotID == "model-authored-snapshot" {
		t.Fatal("model-authored snapshot_id was not replaced by harness stamp")
	}

	if gotSnapshot.SnapshotDigest == "sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatal("model-authored snapshot_digest was not replaced by harness stamp")
	}

	if !strings.HasPrefix(gotSnapshot.SnapshotID, "tdd-snap-session-atom-1") {
		t.Fatalf("snapshot_id = %q, want harness-stamped session/atom id", gotSnapshot.SnapshotID)
	}

	if reason := invalidPlannedTDDSnapshotDigestReason(gotSnapshot.SnapshotDigest); reason != "" {
		t.Fatalf("harness snapshot_digest invalid: %s (%q)", reason, gotSnapshot.SnapshotDigest)
	}

	if len(state.TDDPreExecutionEvidence) != 1 {
		t.Fatalf("expected one TDD evidence row, got %+v", state.TDDPreExecutionEvidence)
	}

	if state.TDDPreExecutionEvidence[0].SnapshotID != gotSnapshot.SnapshotID ||
		state.TDDPreExecutionEvidence[0].SnapshotDigest != gotSnapshot.SnapshotDigest {
		t.Fatalf("evidence did not use harness-stamped snapshot identity, evidence=%+v snapshot=%+v", state.TDDPreExecutionEvidence[0], gotSnapshot)
	}
}

func TestComputePlannedTDDSnapshotDigestIsDeterministic(t *testing.T) {
	first := PlannedTDDSnapshot{
		ObligationIDs:                []string{"TDD-2", "TDD-1"},
		TestCommands:                 []string{"go test ./b", "go test ./a"},
		TestArtifactPaths:            []string{"b_test.go", "a_test.go"},
		ExpectedPreExecutionResults:  []string{"red-confirmed"},
		CreatedBeforeDispatchEventID: "evt-tdd-1",
	}
	second := PlannedTDDSnapshot{
		ObligationIDs:                []string{"TDD-1", "TDD-2"},
		TestCommands:                 []string{"go test ./a", "go test ./b"},
		TestArtifactPaths:            []string{"a_test.go", "b_test.go"},
		ExpectedPreExecutionResults:  []string{"red-confirmed"},
		CreatedBeforeDispatchEventID: "evt-tdd-1",
	}

	first.SnapshotID = "model-value-one"
	first.SnapshotDigest = "sha256:" + strings.Repeat("1", 64)
	second.SnapshotID = "model-value-two"
	second.SnapshotDigest = "sha256:" + strings.Repeat("2", 64)

	got := computePlannedTDDSnapshotDigest(first)
	if got != computePlannedTDDSnapshotDigest(second) {
		t.Fatalf("digest should be stable across list order and snapshot identity fields")
	}

	if reason := invalidPlannedTDDSnapshotDigestReason(got); reason != "" {
		t.Fatalf("digest is not valid sha256 format: %s (%q)", reason, got)
	}
}

func TestApplyPlannerOutputInvalidDurablePlanDoesNotReplaceCommittedPlan(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	accepted := validPlan()
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: accepted}, time.Unix(2, 0).UTC())
	committedID := state.Plan.Atoms[0].ID

	invalid := validPlan()
	invalid.Atoms[0].ID = "invalid-candidate"
	invalid.Atoms[0].TestFirst.Commands = nil
	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: invalid}, time.Unix(3, 0).UTC())

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.tdd_pre_execution_blocked" {
		t.Fatalf("expected blocked event, got %+v", events)
	}

	if state.Plan.Atoms[0].ID != committedID {
		t.Fatalf("invalid candidate replaced committed plan: got atom %q want %q", state.Plan.Atoms[0].ID, committedID)
	}
}

func TestApplyPlannerOutputUsesAcceptedSourceAuthorityForDurablePromptCalls(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.AuthorityDeltas = []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		Expected:       "helper signature needs accepted source evidence",
		Actual:         "source-verified signature in native/src/simulation/economy/fluid_network_system.h is EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const.",
		Revision:       1,
		CreatedAt:      time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}}

	plan := validPlan()
	plan.Atoms[0].Prompt = "Use FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) before posting fallback water haul."

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(3, 0).UTC())
	if len(events) == 1 && events[0].Type == "idiom.feature_planner.tdd_pre_execution_blocked" {
		t.Fatalf("durable validation ignored accepted source authority: %+v", events[0])
	}
	if state.Status != FeatureStateDispatching {
		t.Fatalf("status = %q, want dispatching; events=%+v blockers=%+v", state.Status, events, state.Blockers)
	}
}

func TestApplyPlannerOutputRejectsDemotedImplementationScopeBeforeDispatch(t *testing.T) {
	state := NewState("session", "build collapse event helper", "", 1, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.Summary = "Implement CollapseEvent struct and dispatch helper, integrate into colony_sim_tick.cpp, and add focused unit tests."
	plan.TopLevelScope.SuccessCriteria = []string{
		"CollapseEvent helper is declared in native/src/simulation/world/collapse_event.h.",
		"colony_sim_tick.cpp uses CollapseEvent::dispatch for collapse events.",
	}
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = []string{
		"native/src/simulation/world/collapse_event.h",
		"native/src/core/colony_sim_tick.cpp",
	}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].Prompt = "Implement collapse_event.h declarations and update colony_sim_tick.cpp to use CollapseEvent::dispatch."

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	if len(events) != 1 || events[0].Type != "idiom.feature_planner.tdd_pre_execution_blocked" {
		t.Fatalf("expected durable plan rejection, got status=%s events=%+v", state.Status, events)
	}

	if state.Status != FeatureStatePlanningRepair || len(state.Atoms) != 0 {
		t.Fatalf("invalid demoted scope must not queue atoms, status=%s atoms=%+v", state.Status, state.Atoms)
	}
}

func TestRunDispatchesAtomAfterAcceptedTDDPlan(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	calls := 0

	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		calls++

		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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

func TestRunUsesAnsweredPlannerQuestionForStandaloneNewFileAuthorization(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Approve creating the helper and test files.",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/new_helper.go", "pkg/new_helper_test.go"},
		AffectedFields:    []string{"new_file_authorization"},
	}}

	calls := 0
	var implementorPrompt string

	result, err := Run(state, RunOptions{
		Prompt:                "Implement scoped atom from the approved spec.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		calls++

		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			implementorPrompt = prompt
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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

	if result.State.Status == FeatureStatePlanningRepair {
		t.Fatalf("answered operator authorization should satisfy missing new-file validation, blockers=%+v", result.State.Blockers)
	}

	if result.State.Status != FeatureStateComplete {
		t.Fatalf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers)
	}

	if len(result.State.OpenQuestions) != 1 || result.State.OpenQuestions[0].Status != "answered" {
		t.Fatalf("expected question to be answered, got %+v", result.State.OpenQuestions)
	}

	var derived *PlanAuthorityDelta
	for i := range result.State.AuthorityDeltas {
		if result.State.AuthorityDeltas[i].QuestionID == "q-001" {
			derived = &result.State.AuthorityDeltas[i]
			break
		}
	}
	if derived == nil {
		t.Fatalf("expected typed planner-question authority delta, got %+v", result.State.AuthorityDeltas)
	}

	want := []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	if !slices.Equal(derived.AffectedPaths, want) {
		t.Fatalf("derived affected paths = %#v, want %#v", derived.AffectedPaths, want)
	}
	if !slices.Equal(derived.AffectedFields, []string{"new_file_authorization"}) {
		t.Fatalf("derived affected fields = %#v, want new_file_authorization", derived.AffectedFields)
	}
	if got := PlanExecuteAuthorizedNewFilesFromImplementorPrompt(implementorPrompt); !slices.Equal(got, want) {
		t.Fatalf("authorized new files = %#v, want %#v", got, want)
	}
}

func TestRunUsesAnsweredNewFileAuthorizationWithStandaloneAcceptanceID(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"SDD-COLLAPSE-EVENT-1"}
	plan.Atoms[0].RequiredEditPaths = []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}

	state := newQuestionStateForNewFiles([]string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"})

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent helper.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPlanAndPrompt(plan, prompt), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStateComplete {
		t.Fatalf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers)
	}
}

func TestRunRecordsRejectedCandidateForMissingSDDRefsAfterAuthorization(t *testing.T) {
	root := t.TempDir()
	mkdirAndWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = nil
	plan.Atoms[0].RequiredEditPaths = []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}

	state := newQuestionStateForNewFiles([]string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"})

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent helper.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(_ string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatal("invalid plan must not dispatch implementor")
		}

		return mustPlanJSON(t, plan), nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning-repair", result.State.Status)
	}
	if len(result.State.RejectedPlanCandidates) != 1 {
		t.Fatalf("rejected candidates = %+v, want one", result.State.RejectedPlanCandidates)
	}

	rejected := result.State.RejectedPlanCandidates[0]
	if rejected.Plan.Atoms[0].ID != plan.Atoms[0].ID || rejected.PlanHash == "" {
		t.Fatalf("rejected candidate did not preserve submitted plan/hash: %+v", rejected)
	}
	if !strings.Contains(rejected.Diagnostic, "missing sdd_refs") {
		t.Fatalf("diagnostic should name missing sdd_refs: %+v", rejected)
	}
	lastEvent := result.Events[len(result.Events)-1]
	if lastEvent.Type != "idiom.feature_planner.tdd_pre_execution_blocked" || lastEvent.Fields["rejected_plan_sha256"] == "" {
		t.Fatalf("event should include rejected plan hash fields: %+v", result.Events)
	}
}

func newQuestionStateForNewFiles(paths []string) FeatureState {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Approve creating the helper and test files.",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"new_file_authorization"},
	}}
	return state
}

func TestRunDoesNotCompileRequestedDecisionPathsAsNewFileAuthorization(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Authorize creating missing files: pkg/new_helper.go and pkg/new_helper_test.go.",
		RequiredBefore:    "plan_approval",
	}}

	result, err := Run(state, RunOptions{
		Prompt:                "Approved.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning_repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file") {
		t.Fatalf("expected new-file authorization blocker, got %+v", result.State.Blockers)
	}

	for _, delta := range result.State.AuthorityDeltas {
		if len(delta.AffectedPaths) > 0 || slices.Contains(delta.AffectedFields, "new_file_authorization") {
			t.Fatalf("requested_decision prose must not mint typed path authority, got %+v", result.State.AuthorityDeltas)
		}
	}
}

func TestRunDoesNotCompileGenericResumeAnswerAsNewFileAuthorization(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}

	state := NewState("session", "add score breakdown", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Clarify implementation approval",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequiredBefore:    "plan_approval",
	}}

	result, err := Run(state, RunOptions{
		Prompt:                "Continue.",
		ResumeAnswer:          "Approved to add score breakdown to the existing tooltip.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning_repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file") {
		t.Fatalf("expected new-file authorization blocker, got %+v", result.State.Blockers)
	}

	for _, delta := range result.State.AuthorityDeltas {
		if slices.Contains(delta.AffectedFields, "new_file_authorization") || len(delta.AffectedPaths) > 0 {
			t.Fatalf("generic resume answer must not mint path-level new-file authority, got %+v", result.State.AuthorityDeltas)
		}
	}
}

func TestRunRejectsExplicitOperatorPromptForStandaloneNewFileAuthorization(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/greeting/greeting.go", "internal/greeting/greeting_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"internal/greeting/greeting_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"internal/greeting/greeting_test.go"}

	state := NewState("session", "add greeting package", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add an internal/greeting package with Hello function.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning_repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file") {
		t.Fatalf("expected new-file authorization blocker, got %+v", result.State.Blockers)
	}

	if len(result.State.AuthorityDeltas) != 0 {
		t.Fatalf("operator prompt prose must not mint new-file authority, got %+v", result.State.AuthorityDeltas)
	}
}

func TestRunRejectsGenericAddPromptForStandaloneImplementationNewFile(t *testing.T) {
	root := t.TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}

	state := NewState("session", "add score breakdown", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add score breakdown to the existing tooltip.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, plan), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningRepair {
		t.Fatalf("state.Status = %q, want planning_repair", result.State.Status)
	}

	if !strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file") {
		t.Fatalf("expected new-file authorization blocker, got %+v", result.State.Blockers)
	}

	if len(result.State.AuthorityDeltas) != 0 {
		t.Fatalf("generic add prompt must not mint new-file authority, got %+v", result.State.AuthorityDeltas)
	}
}

func TestDispatchRecordsPlanExecuteLaneFamily(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(prompt string, taskFamily string) (string, error) {
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			t.Fatalf("task family = %q, want feature_planner_implementor callback", taskFamily)
		}

		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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
		return testPlanExecuteCompletedJSONForPrompt(gotPrompt), nil
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
				if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
	second.RequiredEditPaths = []string{"internal/labels/labels2.go", "internal/labels/labels_test.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
	second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var implementorCalls int

	events := DispatchReadyAtoms(&state, 5, func(_ string, taskFamily string) (string, error) {
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
	second.RequiredEditPaths = []string{"internal/labels/labels2.go", "internal/labels/labels_test.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
	second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	state.TDDPreExecutionEvidence = nil

	var implementorCalls int

	events := DispatchReadyAtoms(&state, 5, func(prompt string, _ string) (string, error) {
		implementorCalls++
		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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
		if taskFamily != string(autonomy.TaskFamilyFeaturePlannerImplementor) {
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
	dependent.PlannedTDDSnapshot.SnapshotID = "snapshot-dependent-0001"
	dependent.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigest
	dependent.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-dependent"

	base := plan.Atoms[0]

	base.ID = "atom-base"
	base.AtomID = "atom-base"
	base.TestFirst.PreExecutionEventIDs = []string{"evt-base"}
	base.TestFirst.CoverageGateEventID = "evt-base"
	base.PlannedTDDSnapshot.SnapshotID = "snapshot-base-0001"
	base.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	base.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-base"
	plan.Atoms = []FeatureAtomPlan{dependent, base}

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var prompts []string

	events := DispatchReadyAtoms(&state, 1, func(prompt string, taskFamily string) (string, error) {
		prompts = append(prompts, prompt)
		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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
		second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
		second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
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

func TestValidateDurablePlanAcceptsFullSHA256TDDSnapshotDigest(t *testing.T) {
	plan := validPlan()

	plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = "sha256:" + strings.Repeat("a", 64)

	if err := ValidateDurablePlan(plan); err != nil {
		t.Fatalf("valid lowercase SHA-256 TDD snapshot digest should validate: %v", err)
	}
}

func TestValidateDurablePlanRejectsPlaceholderTDDSnapshotValues(t *testing.T) {
	tests := []struct {
		name     string
		snapshot PlannedTDDSnapshot
		want     []string
	}{
		{
			name: "ellipsis digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:...",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "ellipsis placeholder"},
		},
		{
			name: "placeholder digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:placeholder0000000000000000000000000000000000000000000000000",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "placeholder token \"placeholder\""},
		},
		{
			name: "short digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:abc123",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "got 6 characters after sha256:"},
		},
		{
			name: "non hex digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:" + strings.Repeat("0", 63) + "g",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "non-lowercase-hex"},
		},
		{
			name: "fake snapshot id",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snap-tdd-1",
				SnapshotDigest: validTDDSnapshotDigest,
			},
			want: []string{"planned_tdd_snapshot.snapshot_id", "snap-tdd-*"},
		},
		{
			name: "ellipsis snapshot id",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-...",
				SnapshotDigest: validTDDSnapshotDigest,
			},
			want: []string{"planned_tdd_snapshot.snapshot_id", "ellipsis placeholder"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := validPlan()

			plan.Atoms[0].PlannedTDDSnapshot.SnapshotID = tt.snapshot.SnapshotID
			plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = tt.snapshot.SnapshotDigest

			err := ValidateDurablePlan(plan)
			if err == nil {
				t.Fatal("expected placeholder TDD snapshot validation error")
			}

			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("validation error missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestValidateDurablePlanRejectsMismatchedTDDSnapshotEventID(t *testing.T) {
	plan := validPlan()

	plan.Atoms[0].PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-missing"

	err := ValidateDurablePlan(plan)
	if err == nil {
		t.Fatal("expected mismatched TDD snapshot event validation error")
	}

	for _, want := range []string{"mismatched durable pre-dispatch TDD fields", "planned_tdd_snapshot.created_before_dispatch_event_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func TestRunKeepsImplementorQuestionAsPlannerRepairState(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerPlanner) {
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

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom))

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

func TestApplyImplementorOutputBindsActiveAuthorityForScopeOnlyPlanExecuteCompletion(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	if err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	payload["scope_contract_id"] = "child-local-scope"
	payload["scope_contract_hash"] = "child-local-scope-hash"
	delete(payload, "contract_id")
	delete(payload, "contract_hash")
	delete(payload, "snapshot_hash")
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	if atom.State != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("unexpected events: %+v", events)
	}

	if got := fmt.Sprint(events[0].Fields["contract_id"]); got != atom.RuntimeEnvelope.ContractID {
		t.Fatalf("contract_id = %q, want active atom contract %q; fields=%+v", got, atom.RuntimeEnvelope.ContractID, events[0].Fields)
	}

	if got := fmt.Sprint(events[0].Fields["snapshot_hash"]); got != atom.RuntimeEnvelope.ProtocolHash {
		t.Fatalf("snapshot_hash = %q, want active snapshot %q; fields=%+v", got, atom.RuntimeEnvelope.ProtocolHash, events[0].Fields)
	}

	if got := fmt.Sprint(events[0].Fields["scope_contract_id"]); got != "child-local-scope" {
		t.Fatalf("scope_contract_id = %q, want preserved child scope; fields=%+v", got, events[0].Fields)
	}
}

func TestApplyImplementorOutputRejectsConflictingParentPlanExecuteIdentity(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	if err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	payload["contract_id"] = "wrong-parent-contract"
	payload["snapshot_hash"] = atom.RuntimeEnvelope.ProtocolHash

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "plan-execute terminal completion conflicts with the active plan contract snapshot")
}

func TestApplyImplementorOutputRejectsSkippedReviewCompletionMissingSourceSanity(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	if err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	delete(payload, "source_sanity")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "nested plan-execute terminal validation failed")

	if !strings.Contains(events[0].Message, "source_sanity_missing:internal/labels/labels.go") {
		t.Fatalf("rejection did not name missing source_sanity: %+v", events[0])
	}
}

func TestApplyImplementorOutputAcceptsLegacyContractHashAsSnapshotAlias(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	if err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	delete(payload, "snapshot_hash")

	payload["contract_hash"] = atom.RuntimeEnvelope.ProtocolHash
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	if atom.State != AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events)
	}

	if got := fmt.Sprint(events[0].Fields["snapshot_hash"]); got != atom.RuntimeEnvelope.ProtocolHash {
		t.Fatalf("snapshot_hash = %q, want legacy contract_hash normalized to active snapshot; fields=%+v", got, events[0].Fields)
	}

	if _, ok := events[0].Fields["contract_hash"]; ok {
		t.Fatalf("contract_hash should not remain a parallel authority field; fields=%+v", events[0].Fields)
	}
}

func TestApplyImplementorOutputRejectsConflictingLegacyContractHashAlias(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	if err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	payload["snapshot_hash"] = atom.RuntimeEnvelope.ProtocolHash
	payload["contract_hash"] = "wrong-legacy-snapshot"

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "plan-execute terminal completion conflicts with the active plan contract snapshot")
}

func TestApplyImplementorOutputAcceptsNestedPlanExecuteCompletionStatus(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, testNestedPlanExecuteCompletedJSONForAtom(*atom))

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

func TestApplyImplementorOutputRejectsMissingAcceptanceEvidenceIDs(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)

	nested := payload["plan_execute"].(map[string]any)
	delete(nested, "acceptance_evidence_ids")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "missing acceptance_evidence_ids")
}

func TestApplyImplementorOutputRejectsUntrustedAcceptanceEvidenceIDs(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)

	nested := payload["plan_execute"].(map[string]any)

	nested["completion_source"] = "headless_plan_execute_returned"
	nested["acceptance_evidence_trusted"] = true

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "acceptance_evidence_ids are not backed by trusted plan-execute terminal evidence")
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

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteSourceSatisfiedJSONForAtom(*atom))

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

func TestApplyImplementorOutputRejectsPathOnlyPlanExecuteSourceSatisfied(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	delete(payload, "source_satisfied_evidence")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "source-satisfied completion missing typed source_satisfied_evidence refs")
}

func TestApplyImplementorOutputRejectsSourceSatisfiedWithChangedPaths(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["changed_paths"] = atom.RequiredEditPaths

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "source-satisfied completion must not include changed_paths")
}

func TestApplyImplementorOutputRejectsOutOfScopeSourceSatisfiedPath(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence_paths"] = append([]string{"pkg/out_of_scope.go"}, atom.RequiredEditPaths...)
	payload["source_satisfied_evidence"] = testPlanExecuteSourceSatisfiedEvidenceRefs([]string{"pkg/out_of_scope.go"})

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "source-satisfied completion includes paths outside required edit scope")
}

func TestApplyImplementorOutputRejectsSourceSatisfiedWithoutRequiredScope(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	state.Plan.Atoms[0].RequiredEditPaths = nil
	state.Plan.Atoms[0].AllowedEditPaths = []string{"pkg/allowed.go"}
	state.Plan.Atoms[0].TestFirst.TestArtifactPaths = nil
	state.Plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = nil
	StampPlanAuthorityContract(&state.Plan, state.AuthorityDeltas)

	atom.FeatureAtomPlan = state.Plan.Atoms[0]

	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence_paths"] = []string{"pkg/allowed.go"}
	payload["source_satisfied_evidence"] = testPlanExecuteSourceSatisfiedEvidenceRefs([]string{"pkg/allowed.go"})
	payload["required_edit_paths"] = []string{}

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "source-satisfied completion missing required edit scope")
}

func TestApplyImplementorOutputRejectsUnsupportedSourceSatisfiedEvidenceKind(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence"] = []map[string]string{{"path": atom.RequiredEditPaths[0], "kind": "path_name_only"}}

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(t, state, atom, events, "source-satisfied completion missing typed source_satisfied_evidence refs")
}

func TestApplyImplementorOutputAcceptsOutcomeOnlySourceSatisfiedCompletion(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteOutcomeOnlySourceSatisfiedJSONForAtom(*atom))

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

func TestApplyImplementorOutputPreservesPlanExecuteRetryNextAction(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)
	state.ActiveRuns = []ActiveRun{{RunID: "atom-1-run-1", AtomID: atom.ID, LaneSessionID: "atom-1-run-1", State: "running"}}
	atom.LastRunID = "atom-1-run-1"

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe.json"}`)

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.plan_execute_retry_pending" {
		t.Fatalf("retryable plan-execute completion should become retry-pending event, got state=%s atom=%s events=%+v", state.Status, atom.State, events)
	}
	if state.Status == FeatureStateRecoveryOpen || atom.State == AtomStateRecoveryOpen {
		t.Fatalf("retryable plan-execute completion must not open product recovery: state=%s atom=%s", state.Status, atom.State)
	}
	if state.Continuation.ResumeAction != "retry_plan_execute" || state.Continuation.ResumeAtomID != atom.ID {
		t.Fatalf("continuation = %+v, want retry_plan_execute for %s", state.Continuation, atom.ID)
	}
	if len(state.ActiveRuns) != 0 {
		t.Fatalf("failed active run should be folded so durable reconciliation does not replay it: %+v", state.ActiveRuns)
	}

	if got := fmt.Sprint(events[0].Fields["next_valid_action"]); got != "retry_plan_execute" {
		t.Fatalf("next_valid_action = %q, want retry_plan_execute; fields=%+v", got, events[0].Fields)
	}

	if got := fmt.Sprint(events[0].Fields["nested_failure_kind"]); got != "planner_blocked" {
		t.Fatalf("nested_failure_kind = %q, want planner_blocked", got)
	}
}

func TestRunResumesPlanExecuteRetryPendingAtom(t *testing.T) {
	state, atom := implementingAtomForCompletionTest(t)

	ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe.json"}`)

	var taskFamilies []string
	var prompts []string
	result, err := Run(state, RunOptions{Prompt: "resume", MaxAtoms: 1}, func(prompt string, taskFamily string) (string, error) {
		taskFamilies = append(taskFamilies, taskFamily)
		prompts = append(prompts, prompt)

		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: still refreshing","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe-retry.json"}`, nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !slices.Equal(taskFamilies, []string{string(autonomy.TaskFamilyFeaturePlannerImplementor)}) {
		t.Fatalf("task families = %#v, want only implementor retry dispatch", taskFamilies)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], atom.ID) {
		t.Fatalf("retry dispatch should rebuild implementor prompt for atom %s, got %#v", atom.ID, prompts)
	}
	if len(result.Events) < 2 || result.Events[0].Type != "idiom.feature_planner.plan_execute_retry_resumed" {
		t.Fatalf("events should start with retry resume then dispatch, got %+v", result.Events)
	}
	if result.State.Continuation.ResumeAction != "retry_plan_execute" || result.State.Continuation.ResumeAtomID != atom.ID {
		t.Fatalf("retry failure should remain retry-pending after resumed attempt, continuation=%+v", result.State.Continuation)
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

	result, err := Run(state, RunOptions{Prompt: "resume", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerPlanner) {
			t.Fatal("resume from dispatching must not ask planner for a fresh plan")
		}

		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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
	state.Blockers = []string{
		`planner question "q-source" requires typed accepted_source authority answer`,
		"unrelated blocker remains",
	}
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

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
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

	if containsString(result.State.Blockers, `planner question "q-source" requires typed accepted_source authority answer`) {
		t.Fatalf("accepted typed answer should clear stale question blocker: %+v", result.State.Blockers)
	}

	if result.State.Status == FeatureStatePlanningQuestion {
		t.Fatalf("typed answer should allow planning to continue")
	}
}

func TestRunRejectsTypedAnswerWithMismatchedAuthorityKind(t *testing.T) {
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

	typedAnswer := `{"answer":"Use docs/feature.md acceptance criteria.","answer_source_kind":"accepted_source","authority_kind":"operator_approval","evidence_refs":["docs/feature.md#acceptance"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(_ string, _ string) (string, error) {
		t.Fatal("mismatched authority_kind must not continue to planner")

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.Status != FeatureStatePlanningQuestion {
		t.Fatalf("status = %q, want planning question", result.State.Status)
	}
	if len(result.State.AuthorityDeltas) != 0 {
		t.Fatalf("mismatched authority_kind must not mint authority deltas: %+v", result.State.AuthorityDeltas)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Message, `requires authority_kind "accepted_source"`) {
		t.Fatalf("unexpected rejection event: %+v", result.Events)
	}
}

func TestClearAnsweredPlannerQuestionBlockersPreservesUnrelatedBlockers(t *testing.T) {
	state := FeatureState{
		Blockers: []string{
			`planner question "q-source" requires typed accepted_source authority answer`,
			`planner question "q-other" requires typed accepted_source authority answer`,
			"unrelated blocker remains",
		},
		Atoms: []FeatureAtomState{{
			Blockers: []string{
				`planner question "q-source" requires typed accepted_source authority answer`,
				"atom blocker remains",
			},
		}},
	}

	clearAnsweredPlannerQuestionBlockers(&state, "q-source")

	if containsString(state.Blockers, `planner question "q-source" requires typed accepted_source authority answer`) ||
		containsString(state.Atoms[0].Blockers, `planner question "q-source" requires typed accepted_source authority answer`) {
		t.Fatalf("answered question blocker was not cleared: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers)
	}
	for _, want := range []string{
		`planner question "q-other" requires typed accepted_source authority answer`,
		"unrelated blocker remains",
	} {
		if !containsString(state.Blockers, want) {
			t.Fatalf("unrelated state blocker %q should remain: %+v", want, state.Blockers)
		}
	}
	if !containsString(state.Atoms[0].Blockers, "atom blocker remains") {
		t.Fatalf("unrelated atom blocker should remain: %+v", state.Atoms[0].Blockers)
	}
}

func TestRunAcceptsTypedAnswerForAtomLocalEvidenceQuestion(t *testing.T) {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-local",
		QuestionKind:      "missing_source_localization",
		QuestionSummary:   "Which source anchors prove this atom is current?",
		BlockingClaim:     "Planning needs exact local source evidence.",
		RequestedDecision: "Provide exact local source anchors.",
		AuthorityRequired: "atom_local_evidence",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	typedAnswer := `{"answer":"Use the exact current source anchors.","answer_source_kind":"atom_local_evidence","evidence_refs":["native/src/system.cpp#anchor"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(t, validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		default:
			t.Fatalf("unexpected task family %q", taskFamily)
		}

		return "", nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.State.OpenQuestions[0].AnswerSourceKind != "atom_local_evidence" || len(result.State.OpenQuestions[0].EvidenceRefs) != 1 {
		t.Fatalf("typed atom-local evidence was not recorded: %+v", result.State.OpenQuestions[0])
	}

	if result.State.Status == FeatureStatePlanningQuestion {
		t.Fatalf("typed atom-local evidence answer should allow planning to continue")
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
				SnapshotID:                   "snapshot-evidence-0001",
				SnapshotDigest:               validTDDSnapshotDigest,
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

func testPlanExecuteCompletedJSONForPrompt(prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	sourceSanity := testSourceSanityEvidenceForPaths(paths)
	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 contractID,
		"snapshot_hash":               snapshotHash,
		"changed_paths":               paths,
		"required_edit_paths":         paths,
		"source_sanity":               sourceSanity,
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForPlanAndPrompt(plan FeaturePlan, prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	sourceSanity := testSourceSanityEvidenceForPaths(paths)
	contract := CompilePlanAuthorityContract(plan, nil)

	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 contractID,
		"snapshot_hash":               snapshotHash,
		"changed_paths":               paths,
		"required_edit_paths":         paths,
		"source_sanity":               sourceSanity,
		"acceptance_evidence_ids":     requiredAcceptanceEvidenceIDs(contract.Atoms[0]),
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteSourceSatisfiedJSONForPrompt(prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	payload := map[string]any{
		"tool":                            "plan_execute_completed",
		"lane_family":                     "plan-execute",
		"status":                          "source-satisfied",
		"phase":                           "complete",
		"contract_id":                     contractID,
		"snapshot_hash":                   snapshotHash,
		"source_satisfied_evidence_paths": paths,
		"source_satisfied_evidence":       testPlanExecuteSourceSatisfiedEvidenceRefs(paths),
		"required_edit_paths":             paths,
		"acceptance_evidence_ids":         []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted":     true,
		"completion_source":               "durable_plan_execute_terminal_completion",
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForAtom(atom FeatureAtomState) string {
	sourceSanity := testSourceSanityEvidenceForPaths(atom.RequiredEditPaths)
	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":               atom.RuntimeEnvelope.ProtocolHash,
		"changed_paths":               atom.RequiredEditPaths,
		"required_edit_paths":         atom.RequiredEditPaths,
		"source_sanity":               sourceSanity,
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForAtomWithReviewSkipped(atom FeatureAtomState) string {
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtom(atom)), &payload)
	payload["review_status"] = "skipped"
	payload["review_repair_skipped"] = true
	payload["progress_path"] = ".idiom/plan-execute/session/state.json"
	payload["summary"] = "Edited files and ran required verification"

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testNestedPlanExecuteCompletedJSONForAtom(atom FeatureAtomState) string {
	sourceSanity := testSourceSanityEvidenceForPaths(atom.RequiredEditPaths)
	nested := trustPlanExecuteCompletionForTest(map[string]any{
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "review_repair",
		"review_status":               "completed",
		"progress_path":               ".idiom/plan-execute/session/state.json",
		"contract_id":                 atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":               atom.RuntimeEnvelope.ProtocolHash,
		"changed_paths":               atom.RequiredEditPaths,
		"required_edit_paths":         atom.RequiredEditPaths,
		"source_sanity":               sourceSanity,
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
		"completion_source":           "durable_plan_execute_terminal_completion",
	})

	return mustJSONMap(map[string]any{
		"tool":         "plan_execute_completed",
		"plan_execute": nested,
	})
}

func testSourceSanityEvidenceForPaths(paths []string) []map[string]string {
	sourceSanity := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, ".go") {
			sourceSanity = append(sourceSanity, map[string]string{"kind": "source_sanity", "path": path, "status": "ok"})
		}
	}

	return sourceSanity
}

func testPlanExecuteSourceSatisfiedJSONForAtom(atom FeatureAtomState) string {
	payload := map[string]any{
		"tool":                            "plan_execute_completed",
		"lane_family":                     "plan-execute",
		"status":                          "source-satisfied",
		"phase":                           "complete",
		"progress_path":                   ".idiom/plan-execute/session/state.json",
		"contract_id":                     atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":                   atom.RuntimeEnvelope.ProtocolHash,
		"source_satisfied_evidence_paths": atom.RequiredEditPaths,
		"source_satisfied_evidence":       testPlanExecuteSourceSatisfiedEvidenceRefs(atom.RequiredEditPaths),
		"required_edit_paths":             atom.RequiredEditPaths,
		"acceptance_evidence_ids":         []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted":     true,
		"completion_source":               "durable_plan_execute_terminal_completion",
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func trustPlanExecuteCompletionForTest(fields map[string]any) map[string]any {
	TrustPlanExecuteCompletionFields(fields)

	return fields
}

func testPlanExecuteSourceSatisfiedEvidenceRefs(paths []string) []map[string]string {
	refs := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		refs = append(refs, map[string]string{"path": path, "kind": "harness_observed_source"})
	}

	return refs
}

func testPlanExecuteOutcomeOnlySourceSatisfiedJSONForAtom(atom FeatureAtomState) string {
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(atom)), &payload)
	delete(payload, "status")

	payload["outcome"] = "source_satisfied"

	return mustJSONMap(payload)
}

func testPlanExecuteContractFromPrompt(prompt string) (string, string, []string) {
	var (
		contractID   string
		snapshotHash string
		paths        []string
	)

	inRequired := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "Active plan contract id:"):
			contractID = strings.TrimSpace(strings.TrimPrefix(line, "Active plan contract id:"))
		case strings.HasPrefix(line, "Active plan contract snapshot hash:"):
			snapshotHash = strings.TrimSpace(strings.TrimPrefix(line, "Active plan contract snapshot hash:"))
		case line == "required_edit_paths:":
			inRequired = true
		case inRequired && strings.HasPrefix(line, "- "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case inRequired && line == "":
			inRequired = false
		case inRequired && strings.HasSuffix(line, ":"):
			inRequired = false
		}
	}

	return contractID, snapshotHash, normalizedStringList(paths)
}

func mustJSONMap(payload map[string]any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	return string(body)
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
