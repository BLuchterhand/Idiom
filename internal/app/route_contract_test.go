package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/llama"
)

func TestRouteContractClassifierUnavailableNaturalLanguageFallsBackToConversation(t *testing.T) {
	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
	}

	plan := m.classifyTurnRoute(context.Background(), "Can we shorten the displayed model label to just the alias?", agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if plan.Family != "conversation" || plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("fallback route = %#v, want conversation/main/direct_answer", plan)
	}
	if contract.Source != routeSourceConservativeFallback {
		t.Fatalf("source = %q, want conservative fallback", contract.Source)
	}
	if contract.MutationAuthority != routeMutationAuthorityNone || contract.MutationCapable {
		t.Fatalf("fallback natural language must not grant mutation authority: %#v", contract)
	}
}

func TestRouteContractClassifierImplementationOwnsMutationAuthority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
			}},
		})
	}))
	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}

	plan := m.classifyTurnRoute(context.Background(), "Shorten the displayed model label to just the alias.", agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if contract.Source != routeSourceClassifier {
		t.Fatalf("source = %q, want classifier", contract.Source)
	}
	if contract.Family != "implementation" || contract.Lane != "implement" || contract.Contract != "implementation_result" {
		t.Fatalf("projection lost route fields: %#v", contract)
	}
	if contract.MutationAuthority != routeMutationAuthorityClassifierOwned || !contract.MutationCapable {
		t.Fatalf("classifier implementation route should own mutation authority: %#v", contract)
	}
}

func TestRouteContractTypedCommandActivatesExplicitMutationAuthority(t *testing.T) {
	contract := projectRouteContract(turnRoutePlan{
		Family:      "implementation",
		RouteSource: routeSourceTypedCommand,
	}, agent.BuildMode())

	if contract.Source != routeSourceTypedCommand {
		t.Fatalf("source = %q, want typed command", contract.Source)
	}
	if contract.Family != "implementation" || contract.Lane != "implement" || contract.Contract != "implementation_result" {
		t.Fatalf("projection lost typed command route fields: %#v", contract)
	}
	if contract.MutationAuthority != routeMutationAuthorityExplicit || !contract.MutationCapable {
		t.Fatalf("typed command should grant explicit mutation authority: %#v", contract)
	}
}

func TestRouteContractProductionTypedCommandPlansActivateExplicitMutationAuthority(t *testing.T) {
	m := &Model{}
	implement := m.implementCommandExecutionPlan("Update the label.")
	implementContract := projectRouteContract(implement.route, implement.mode)
	if implementContract.Source != routeSourceTypedCommand || implementContract.MutationAuthority != routeMutationAuthorityExplicit {
		t.Fatalf("implement command contract = %#v, want typed explicit mutation authority", implementContract)
	}

	planExecute := m.planExecuteCommandExecutionPlan("Update the label.")
	planExecuteContract := projectRouteContract(planExecute.route, planExecute.mode)
	if planExecuteContract.Source != routeSourceTypedCommand || planExecuteContract.MutationAuthority != routeMutationAuthorityExplicit {
		t.Fatalf("plan-execute command contract = %#v, want typed explicit mutation authority", planExecuteContract)
	}
}

func TestRouteContractPromptWordingAndPathShapeDoNotGrantMutationAuthority(t *testing.T) {
	prompt := "Implement the change. Required edit paths: internal/app/route_contract.go, internal/app/route_contract_test.go."
	plan := fallbackTurnRoute(prompt, agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if contract.Source != routeSourceConservativeFallback {
		t.Fatalf("source = %q, want conservative fallback", contract.Source)
	}
	if contract.Family != "conversation" || contract.Lane != "main" || contract.Contract != "direct_answer" {
		t.Fatalf("fallback prompt with path-shaped wording should stay conversation: %#v", contract)
	}
	if contract.MutationAuthority != routeMutationAuthorityNone || contract.MutationCapable {
		t.Fatalf("prompt wording/path shape must not grant mutation authority: %#v", contract)
	}
}
