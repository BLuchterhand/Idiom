package frontier

import (
	"strings"
	"testing"
)

func TestClassifyPromptPlanOwnsProfileAndInitialRoute(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Can you identify a refactor opportunity to better match the frontier architecture?",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
	})

	if plan.Lane != LaneArchitecture || plan.Reason != RouteReasonArchitectureRequest {
		t.Fatalf("plan = %#v, want architecture route", plan)
	}
	if plan.Contract != AnswerContractArchitecture {
		t.Fatalf("Contract = %q, want architecture contract", plan.Contract)
	}
	if plan.TaskFamily != "architecture" || plan.PermissionProfile == "" {
		t.Fatalf("frontier finalization did not attach task family/permission profile: %#v", plan)
	}
	if plan.Intent.Source != "frontier_structured_intent" || plan.Intent.SelectedLane != string(LaneArchitecture) {
		t.Fatalf("intent = %#v, want frontier-owned selected lane", plan.Intent)
	}
}

func TestClassifyPromptPlanUsesContextualRoute(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "validate that option",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "thorough",
		Recent:              "Architecture hotspot: internal/app/prompt_routing.go\nRecommended next step: validate this option before editing.",
	})

	if plan.Lane != LaneArchitecture || plan.Thoroughness != "thorough" {
		t.Fatalf("plan = %#v, want contextual architecture plan with thoroughness", plan)
	}
}

func TestClassifyPromptPlanKeepsMemoryNotesConversational(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Remember that any later docs write must touch README.md only.",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
	})

	if plan.Lane != LaneMain || plan.Intent.WantsEdits {
		t.Fatalf("plan = %#v, memory note should not trigger implementation", plan)
	}
}

func TestClassifyPromptPlanAppliesAgentModeInFrontier(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Please implement a refactor in internal/app/app.go.",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
		AgentMode:           "plan",
	})

	if plan.Lane != LaneArchitecture || plan.FollowupLane != "" || plan.AgentMode != "plan" {
		t.Fatalf("plan = %#v, want architecture plan-mode route", plan)
	}
	if !plan.Intent.WantsPlanOnly || plan.Intent.WantsEdits {
		t.Fatalf("intent = %#v, want plan-only without edits", plan.Intent)
	}
	if !containsString(plan.Intent.Vetoes, "agent_plan_mode_blocks_edits") {
		t.Fatalf("vetoes = %#v, want agent plan-mode veto", plan.Intent.Vetoes)
	}
}

func TestClassifyPromptPlanBuildModePreservesReadOnlyBoundary(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Run go test ./internal/app and report the result only. Do not edit files.",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
		AgentMode:           "build",
	})

	if plan.Lane == LaneImplement {
		t.Fatalf("plan = %#v, build mode should not override explicit read-only verification prompt", plan)
	}
	if !containsString(plan.Intent.Vetoes, "agent_build_mode_preserved_user_read_only_boundary") {
		t.Fatalf("vetoes = %#v, want read-only preservation veto", plan.Intent.Vetoes)
	}
}

func TestClassifyPromptPlanAppliesWorkflowStateInFrontier(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "build it",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
		Workflow: PromptWorkflowState{
			Active:           true,
			Kind:             "durable_plan",
			Phase:            "awaiting_approval",
			HasApprovalScope: true,
		},
	})

	if plan.Lane != LaneImplement || plan.Reason != RouteReasonDirectImplementation || plan.WorkflowPhase != "building" {
		t.Fatalf("plan = %#v, want workflow approval to route to implementation", plan)
	}
	if !plan.Intent.WantsEdits || plan.Intent.WantsPlanOnly {
		t.Fatalf("intent = %#v, want workflow approval edit intent", plan.Intent)
	}
}

func TestClassifyPromptPlanWorkflowApprovalRequiresScope(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "build it",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
		Workflow: PromptWorkflowState{
			Active: true,
			Kind:   "durable_plan",
			Phase:  "awaiting_approval",
		},
	})

	if plan.Lane == LaneImplement {
		t.Fatalf("plan = %#v, workflow approval without scope must not route to implementation", plan)
	}
}

func TestBuildTaskMemoryPromptPlanOwnsResumeRouting(t *testing.T) {
	plan := BuildTaskMemoryPromptPlan(TaskMemoryResumeInput{
		Active:     true,
		Kind:       "durable_plan",
		Lane:       string(LaneArchitecture),
		UserPrompt: "implement this plan",
	}, "medium")

	if plan.Lane != LaneImplement || plan.Reason != RouteReasonDirectImplementation {
		t.Fatalf("plan = %#v, want approved durable plan to route to implementation", plan)
	}
	if !plan.Intent.WantsEdits || plan.Intent.WantsPlanOnly {
		t.Fatalf("intent = %#v, want edit intent after task-memory approval", plan.Intent)
	}

	plan = BuildTaskMemoryPromptPlan(TaskMemoryResumeInput{
		Active:     true,
		Kind:       "durable_plan",
		Lane:       string(LaneArchitecture),
		UserPrompt: "continue validating the plan",
	}, "medium")
	if plan.Lane != LaneArchitecture || plan.Intent.WantsEdits {
		t.Fatalf("plan = %#v, want unapproved durable plan to stay read-only architecture", plan)
	}
}

func TestReconcilePromptPlanWithVerifierOwnsSafetyPolicy(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Prepare a plan to update internal/app/analysis_agents.go. Do not edit files.",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
	})
	verifier := NormalizeIntentVerifierResult(IntentVerifierResult{
		PrimaryIntent:   "implement",
		WantsEdits:      true,
		RecommendedLane: "implement",
		Confidence:      "high",
	})

	result := ReconcilePromptPlanWithVerifier("Prepare a plan to update internal/app/analysis_agents.go. Do not edit files.", plan, verifier, "")
	if result.Plan.Lane == LaneImplement || result.Plan.FollowupLane == LaneImplement {
		t.Fatalf("reconciled plan = %#v, want read-only lane preserved", result.Plan)
	}
	if !result.SafetyOverride {
		t.Fatalf("SafetyOverride = false, want verifier implementation recommendation rejected")
	}
	if !strings.Contains(strings.Join(result.Notes, "\n"), "ignored verifier implementation recommendation") {
		t.Fatalf("notes = %#v, want safety note", result.Notes)
	}
}

func TestReconcilePromptPlanPreservesBoundedCodeSmellReview(t *testing.T) {
	prompt := "Can you identify an opportunity to clean up the code base? Any small code smells?"
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              prompt,
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
	})
	if plan.Lane != LaneReview {
		t.Fatalf("test setup expected host review route, got %#v", plan)
	}
	verifier := NormalizeIntentVerifierResult(IntentVerifierResult{
		PrimaryIntent:       "plan_architecture",
		WantsEdits:          false,
		WantsPlanOnly:       false,
		RecommendedLane:     string(LaneArchitecture),
		RecommendedContract: string(AnswerContractArchitecture),
		TaskFamily:          "architecture",
		Confidence:          "high",
	})

	result := ReconcilePromptPlanWithVerifier(prompt, plan, verifier, "")
	if result.Plan.Lane != LaneReview || result.Plan.Contract != AnswerContractReviewFindings {
		t.Fatalf("reconciled plan = %#v notes=%#v, want bounded review preserved", result.Plan, result.Notes)
	}
	if !strings.Contains(strings.Join(result.Notes, "\n"), "preserved host review lane") {
		t.Fatalf("notes = %#v, want bounded review preservation note", result.Notes)
	}
}

func TestRouteVerifierPromptAndParserAreFrontierOwned(t *testing.T) {
	system := BuildPromptIntentVerifierSystemPrompt()
	if !strings.Contains(system, "narrow route classifier") || !strings.Contains(system, "recommended_lane") {
		t.Fatalf("unexpected verifier system prompt:\n%s", system)
	}

	result, err := ParseIntentVerifierResult(`<think>ignore</think>{"primary_intent":"plan-architecture","recommended_lane":"architecture","risk":"high","confidence":"low","target_refs":"internal/app/app.go"}`)
	if err != nil {
		t.Fatalf("ParseIntentVerifierResult: %v", err)
	}
	normalized := NormalizeIntentVerifierResult(result)
	if normalized.PrimaryIntent != "plan_architecture" || normalized.RecommendedLane != string(LaneArchitecture) {
		t.Fatalf("normalized = %#v, want architecture result", normalized)
	}
	if len(normalized.TargetRefs) != 1 || normalized.TargetRefs[0] != "internal/app/app.go" {
		t.Fatalf("TargetRefs = %#v, want decoded path", normalized.TargetRefs)
	}
}
