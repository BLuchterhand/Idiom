package frontier

import (
	"strings"
	"testing"

	"qwen-bubbles/internal/agent"
)

func TestBuildWorkerRuntimeBriefCarriesRailsTargetsAndToolManifest(t *testing.T) {
	plan := ClassifyPromptPlan(PromptPlanInput{
		Prompt:              "Implement the routing refactor in internal/app/prompt_routing.go",
		WorkspaceEnabled:    true,
		WorkspaceNormal:     true,
		DefaultThoroughness: "medium",
	})
	rails := BuildControlRails(ControlRailInput{
		Prompt:  "Implement the routing refactor in internal/app/prompt_routing.go",
		Profile: BuildRouteProfile("Implement the routing refactor in internal/app/prompt_routing.go"),
		Plan:    ControlRailPlan{Lane: plan.Lane, Contract: plan.Contract, Intent: plan.Intent},
	})
	brief := BuildWorkerRuntimeBrief(WorkerRuntimeBriefInput{
		Objective: "Implement the routing refactor in internal/app/prompt_routing.go",
		Rails:     rails,
		TargetPacket: TargetPacket{
			PrimaryPaths: []string{"internal/app/prompt_routing.go"},
		},
		VerificationPlan: []string{"go test ./internal/app"},
		RunBudget:        RunPolicyBudgetBrief(agent.RunPolicy{MaxToolSteps: 12, MaxModelCalls: 18}),
	})

	if brief.Lane != string(LaneImplement) || brief.TaskFamily != "implementation" {
		t.Fatalf("brief = %#v, want implementation runtime brief", brief)
	}
	if !containsString(brief.TargetRefs, "internal/app/prompt_routing.go") {
		t.Fatalf("target refs = %#v, want prompt_routing.go", brief.TargetRefs)
	}
	if len(brief.ToolManifest) == 0 || !strings.Contains(brief.ToolManifest[0].Request, `"tool"`) {
		t.Fatalf("expected typed tool manifest, got %#v", brief.ToolManifest)
	}
	if !containsString(brief.VerificationPlan, "go test ./internal/app") {
		t.Fatalf("verification plan = %#v", brief.VerificationPlan)
	}
}

func TestImplementationCompletionGateAndRunPolicyAreFrontierOwned(t *testing.T) {
	policy := ImplementRunPolicyForRequest(12, "split internal/app/app.go into cohesive files", TargetPacket{
		PrimaryPaths:  []string{"internal/app/app.go"},
		StrategyHints: []string{"structural refactor"},
	})
	if policy.MaxToolSteps != 0 || policy.MaxModelCalls == 0 {
		t.Fatalf("policy = %#v, want model-decided structural policy", policy)
	}

	var changed []string
	gate := ImplementationCompletionGate("implement the change", func() []string { return changed })
	rejected := gate(agent.CompletionGateInput{Reply: "Done.", Results: nil})
	if rejected.Accept {
		t.Fatalf("gate accepted implementation final without changed paths or terminal reason: %#v", rejected)
	}
	changed = []string{"internal/app/app.go"}
	accepted := gate(agent.CompletionGateInput{Reply: "Done.", Results: nil})
	if !accepted.Accept {
		t.Fatalf("gate rejected implementation final after changed paths: %#v", accepted)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
