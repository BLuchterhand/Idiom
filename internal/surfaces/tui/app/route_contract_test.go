package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
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

func TestRouteContractClassifierImplementationRequiresMutationConfirmation(t *testing.T) {
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

	if contract.MutationAuthority != routeMutationAuthorityConfirmationRequired || contract.MutationCapable {
		t.Fatalf("classifier implementation route should require explicit confirmation: %#v", contract)
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
	prompt := "Implement the change. Required edit paths: internal/surfaces/tui/app/route_contract.go, internal/surfaces/tui/app/route_contract_test.go."
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

func TestRouteContractOutcomeCorrectsPostCompletionDefectReportToImplementation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{
					"family":"debug_logs",
					"initial_lane":"explore",
					"contract":"grounded_explanation",
					"requested_outcome":"change_workspace",
					"write_intent":"yes",
					"read_only_constraint":false,
					"continuation_target":"prior_change",
					"reason_codes":["post_completion_defect_report"]
				}`},
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

	plan := m.classifyTurnRoute(context.Background(), "that didnt quite fix it - the cursor still goes off the screen.", agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if contract.Family != "implementation" || contract.Lane != "implement" || contract.Contract != "implementation_result" {
		t.Fatalf("post-completion defect report should be corrected to implementation, got %#v", contract)
	}

	if contract.RequestedOutcome != "change_workspace" || contract.WriteIntent != "yes" || contract.ContinuationTarget != "prior_change" {
		t.Fatalf("outcome metadata was not preserved: %#v", contract)
	}

	if contract.MutationAuthority != routeMutationAuthorityConfirmationRequired || contract.MutationCapable {
		t.Fatalf("corrected classifier route should require explicit confirmation: %#v", contract)
	}
}

func TestRouteContractReadOnlyConstraintBeatsImplementationOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{
					"family":"implementation",
					"initial_lane":"implement",
					"contract":"implementation_result",
					"requested_outcome":"answer",
					"write_intent":"no",
					"read_only_constraint":true,
					"continuation_target":"prior_change",
					"reason_codes":["user_asked_why_only"]
				}`},
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

	plan := m.classifyTurnRoute(context.Background(), "Before changing anything, why didn't that fix it?", agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if contract.Family != "conversation" || contract.Lane != "main" || contract.Contract != "direct_answer" {
		t.Fatalf("explicit read-only question should be corrected to conversation, got %#v", contract)
	}

	if contract.MutationAuthority != routeMutationAuthorityNone || contract.MutationCapable {
		t.Fatalf("read-only constraint must not grant mutation authority: %#v", contract)
	}

	if !contract.ReadOnlyConstraint || contract.WriteIntent != "no" {
		t.Fatalf("read-only metadata was not preserved: %#v", contract)
	}
}

func TestRouteContractEvidenceReportPromptBeatsMisclassifiedImplementation(t *testing.T) {
	const prompt = "you are a research assistant for another agentic process; please provide structured evidence in the form of files, line numbers, and relevant symbols. We are trying to debug an issue where resizing the TUI mid-answer caused the conversation to become non-scrollable. Please debug and present your findings."

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{
					"family":"investigate_then_implement",
					"initial_lane":"explore",
					"followup_lane":"implement",
					"contract":"implementation_result",
					"requested_outcome":"diagnose_then_change",
					"write_intent":"yes",
					"read_only_constraint":false,
					"reason_codes":["debug_request","ui_fix"]
				}`},
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

	plan := m.classifyTurnRoute(context.Background(), prompt, agent.BuildMode())
	contract := projectRouteContract(plan, agent.BuildMode())

	if contract.Family != "conversation" || contract.Lane != "main" || contract.Contract != "direct_answer" {
		t.Fatalf("evidence-report prompt should be corrected to conversation, got %#v", contract)
	}

	if contract.MutationAuthority != routeMutationAuthorityNone || contract.MutationCapable {
		t.Fatalf("evidence-report prompt must not grant mutation authority: %#v", contract)
	}

	if contract.RequestedOutcome != "diagnose" || contract.WriteIntent != "no" {
		t.Fatalf("evidence-report outcome metadata not corrected: %#v", contract)
	}
}

func TestCorrectTurnRoutePlanKeepsDebugAndFixImplementation(t *testing.T) {
	prompt := "Please debug and fix the issue where resizing the TUI mid-answer makes the conversation non-scrollable."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
		Family:           "investigate_then_implement",
		RequestedOutcome: "diagnose_then_change",
		WriteIntent:      "yes",
	}, prompt, agent.BuildMode())

	if plan.Family != "investigate_then_implement" || plan.InitialLane != "explore" || plan.FollowupLane != "implement" {
		t.Fatalf("debug-and-fix prompt should stay implementation-shaped, got %#v", plan)
	}

	if plan.WriteIntent != "yes" || plan.Contract != "implementation_result" {
		t.Fatalf("debug-and-fix metadata lost: %#v", plan)
	}
}

func TestEvidenceReportPromptMutationGuardUsesWholeWords(t *testing.T) {
	prompts := []string{
		"You are a research assistant; debug this fixture failure and present findings with structured evidence.",
		"You are a research assistant; debug what changed in the resize path and present findings with line numbers.",
		"You are a research assistant; debug the change in the resize path and present findings with line numbers.",
		"You are a research assistant; research status migration apply failures and present findings with structured evidence.",
		"You are a research assistant; research how to fix the resize issue and present findings with structured evidence.",
		"You are a research assistant; debug this issue and write a structured evidence report with line numbers.",
		"You are a research assistant; debug resize and update handling and present findings with line numbers.",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
			Family:           "investigate_then_implement",
			RequestedOutcome: "diagnose_then_change",
			WriteIntent:      "yes",
		}, prompt, agent.BuildMode())

		if plan.Family != "conversation" || plan.InitialLane != "main" || plan.Contract != "direct_answer" {
			t.Fatalf("descriptive RCA wording should not be treated as mutation intent for %q, got %#v", prompt, plan)
		}

		if plan.WriteIntent != "no" || plan.RequestedOutcome != "diagnose" {
			t.Fatalf("evidence-report metadata not corrected for %q: %#v", prompt, plan)
		}
	}
}

func TestEvidenceReportPromptDoesNotOverrideExplicitFollowupMutation(t *testing.T) {
	prompts := []string{
		"Please debug and fix the resize issue and present findings with structured evidence.",
		"Act as a research assistant, debug this issue, present findings, then make the changes.",
		"Act as a research assistant, debug this issue, present findings, then refactor the handler.",
		"Act as a research assistant, debug this issue, present findings, then add regression tests.",
		"Act as a research assistant, debug this issue, present findings, then add a regression test.",
		"Act as a research assistant, debug this issue, present findings, then write regression tests.",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
			Family:           "investigate_then_implement",
			RequestedOutcome: "diagnose_then_change",
			WriteIntent:      "yes",
		}, prompt, agent.BuildMode())

		if plan.Family != "investigate_then_implement" || plan.InitialLane != "explore" || plan.FollowupLane != "implement" {
			t.Fatalf("explicit follow-up mutation should stay implementation-shaped for %q, got %#v", prompt, plan)
		}

		if plan.WriteIntent != "yes" || plan.Contract != "implementation_result" {
			t.Fatalf("explicit follow-up mutation metadata lost for %q: %#v", prompt, plan)
		}
	}
}
