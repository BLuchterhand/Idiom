package frontier

import (
	"testing"

	"qwen-bubbles/internal/workspace"
)

func TestEffortPolicySourceOfTruth(t *testing.T) {
	policy := BuildEffortPolicySnapshot(EffortPlan{
		Lane:         LaneLocate,
		FollowupLane: LaneImplement,
	}, 20, 30, 4096, 2048)

	if policy.AnalysisToolBudget != ExploreToolBudget(20) {
		t.Fatalf("AnalysisToolBudget = %d, want %d", policy.AnalysisToolBudget, ExploreToolBudget(20))
	}
	if policy.ImplementToolBudget != ImplementToolBudget(20) {
		t.Fatalf("ImplementToolBudget = %d, want %d", policy.ImplementToolBudget, ImplementToolBudget(20))
	}
	if policy.ConfidenceFloor != "medium" {
		t.Fatalf("ConfidenceFloor = %q, want medium", policy.ConfidenceFloor)
	}
}

func TestRoutingSourceOfTruth(t *testing.T) {
	lane, reason := InitialRoute(RouteProfile{
		WantsArchitecture: true,
		WantsPlanOnly:     true,
	})
	if lane != LaneArchitecture || reason != RouteReasonArchitectureRequest {
		t.Fatalf("InitialRoute = (%q, %q), want architecture route", lane, reason)
	}
	if followup := FollowupRoute(RouteProfile{WantsImplementation: true}, LaneArchitecture); followup != LaneImplement {
		t.Fatalf("FollowupRoute = %q, want implement", followup)
	}
}

func TestBuildRouteProfileClassifiesArchitectureRequest(t *testing.T) {
	profile := BuildRouteProfile("can you identify an opportunity to refactor this codebase? Specifically to more appropriately match the frontier architecture?")
	lane, reason := InitialRoute(profile)

	if !profile.WantsArchitecture || !profile.WantsExploration {
		t.Fatalf("profile = %#v, want architecture exploration profile", profile)
	}
	if lane != LaneArchitecture || reason != RouteReasonArchitectureRequest {
		t.Fatalf("InitialRoute = (%q, %q), want architecture route", lane, reason)
	}
}

func TestClassifyVerificationFailureRecognizesGoFailures(t *testing.T) {
	result := workspace.ToolResult{
		Tool:   "shell",
		Output: "# qwen-bubbles/internal/app\ninternal/app/app.go:12:2: declared and not used: value\n",
		Error:  "exit status 1",
	}
	classes := ClassifyVerificationFailure("go test -vet=off ./internal/app", result)
	if !containsString(classes, "go_unused_variable") {
		t.Fatalf("expected go_unused_variable class, got %#v", classes)
	}
}

func TestClassifyVerificationFailurePrioritizesShellSyntax(t *testing.T) {
	result := workspace.ToolResult{
		Tool:   "shell",
		Output: "scripts/check.sh: line 2: syntax error near unexpected token `fi'\n",
		Error:  "exit status 2",
	}
	classes := ClassifyVerificationFailure("bash -n scripts/check.sh", result)
	if !containsString(classes, "shell_syntax_error") {
		t.Fatalf("expected shell_syntax_error class, got %#v", classes)
	}
	if containsString(classes, "syntax_error") || containsString(classes, "go_syntax_error") {
		t.Fatalf("expected shell syntax to avoid generic syntax classes, got %#v", classes)
	}
}

func TestContextualRouteUsesRecentArchitectureContext(t *testing.T) {
	recent := "Architecture hotspot: internal/app/prompt_routing.go\nRecommended next step: validate this option before editing."
	decision, ok := ContextualRoute(ContextualRouteInput{
		Prompt:              "validate that option",
		Recent:              recent,
		DefaultThoroughness: "thorough",
	})
	if !ok {
		t.Fatal("ContextualRoute did not detect architecture validation follow-up")
	}
	if decision.Plan.Lane != LaneArchitecture || decision.Plan.Reason != RouteReasonArchitectureRequest {
		t.Fatalf("decision = %#v, want architecture route", decision)
	}
	if decision.Thoroughness != "thorough" {
		t.Fatalf("Thoroughness = %q, want thorough", decision.Thoroughness)
	}
}

func TestContextualRouteUsesRecentImplementationContext(t *testing.T) {
	recent := "Architecture hotspot: internal/app/prompt_routing.go\nRecommended implementation order: split one responsibility, then implement."
	decision, ok := ContextualRoute(ContextualRouteInput{
		Prompt: "go ahead",
		Recent: recent,
	})
	if !ok {
		t.Fatal("ContextualRoute did not detect implementation follow-up")
	}
	if decision.Plan.Lane != LaneImplement || decision.Plan.Reason != RouteReasonDirectImplementation {
		t.Fatalf("decision = %#v, want implementation route", decision)
	}
}

func TestIntentDecisionAndVetoesComeFromFrontier(t *testing.T) {
	profile := BuildRouteProfile("prepare a refactor plan for internal/app/prompt_routing.go without changing code")
	plan := ApplyIntentVetoes(IntentPlan{
		Lane:         LaneImplement,
		FollowupLane: LaneImplement,
		Reason:       RouteReasonDirectImplementation,
	}, profile)
	decision := BuildIntentDecision("prepare a refactor plan for internal/app/prompt_routing.go without changing code", profile, "", plan)

	if plan.Lane != LaneArchitecture || plan.FollowupLane != "" {
		t.Fatalf("plan = %#v, want architecture with no followup", plan)
	}
	if decision.Intent != "plan_architecture" || decision.WantsEdits {
		t.Fatalf("decision = %#v, want plan_architecture without edits", decision)
	}
}
