package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("route contract classifier unavailable natural language falls back to conversation", func() {
	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
	}

	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Can we shorten the displayed model label to just the alias?", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("fallback route = %#v, want conversation/main/direct_answer", plan))
	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("fallback route = %#v, want conversation/main/direct_answer", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("fallback route = %#v, want conversation/main/direct_answer", plan))

	gomega.Expect(contract.Source).To(gomega.Equal(routeSourceConservativeFallback), fmt.Sprintf("source = %q, want conservative fallback", contract.Source))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityNone), fmt.Sprintf("fallback natural language must not grant mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeFalse(), fmt.Sprintf("fallback natural language must not grant mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract fallback concrete browser creation activates implementation", func() {
	plan := fallbackTurnRoute("Let’s make this feel like a cozy little personal place I can open in a browser. Choose the smallest useful first step.", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Family).To(gomega.Equal("implementation"), fmt.Sprintf("browser creation prompt should route to implementation, got %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("implement"), fmt.Sprintf("browser creation prompt should route to implementation, got %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("browser creation prompt should route to implementation, got %#v", contract))

	gomega.Expect(contract.Source).To(gomega.Equal(routeSourceConservativeFallback), fmt.Sprintf("source = %q, want conservative fallback", contract.Source))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("browser creation fallback should grant guarded mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeTrue(), fmt.Sprintf("browser creation fallback should grant guarded mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract fallback creation question stays conversation", func() {
	plan := fallbackTurnRoute("How would we make this into a cozy browser journal without editing files?", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Family).To(gomega.Equal("conversation"), fmt.Sprintf("read-only creation question should stay conversation, got %#v", contract))
	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityNone), fmt.Sprintf("read-only creation question should stay conversation, got %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeFalse(), fmt.Sprintf("read-only creation question should stay conversation, got %#v", contract))
})

var _ = ginkgo.It("route contract classifier implementation activates guarded mutation authority", func() {
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

	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Shorten the displayed model label to just the alias.", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Source).To(gomega.Equal(routeSourceClassifier), fmt.Sprintf("source = %q, want classifier", contract.Source))

	gomega.Expect(contract.Family).To(gomega.Equal("implementation"), fmt.Sprintf("projection lost route fields: %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("implement"), fmt.Sprintf("projection lost route fields: %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("projection lost route fields: %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("classifier implementation route should activate guarded mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeTrue(), fmt.Sprintf("classifier implementation route should activate guarded mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract typed command activates explicit mutation authority", func() {
	contract := projectRouteContract(turnRoutePlan{
		Family:      "implementation",
		RouteSource: routeSourceTypedCommand,
	}, modepolicy.BuildMode())

	gomega.Expect(contract.Source).To(gomega.Equal(routeSourceTypedCommand), fmt.Sprintf("source = %q, want typed command", contract.Source))

	gomega.Expect(contract.Family).To(gomega.Equal("implementation"), fmt.Sprintf("projection lost typed command route fields: %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("implement"), fmt.Sprintf("projection lost typed command route fields: %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("projection lost typed command route fields: %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("typed command should grant explicit mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeTrue(), fmt.Sprintf("typed command should grant explicit mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract production typed command plans activate explicit mutation authority", func() {
	m := &Model{}
	execution := newDetachedPromptExecutionSession(context.Background(), m)
	implement := execution.implementCommandExecutionPlan("Update the label.")
	implementContract := projectRouteContract(implement.route, implement.mode)

	gomega.Expect(implementContract.Source).To(gomega.Equal(routeSourceTypedCommand), fmt.Sprintf("implement command contract = %#v, want typed explicit mutation authority", implementContract))
	gomega.Expect(implementContract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("implement command contract = %#v, want typed explicit mutation authority", implementContract))

	planExecute := execution.planExecuteCommandExecutionPlan("Update the label.")
	planExecuteContract := projectRouteContract(planExecute.route, planExecute.mode)

	gomega.Expect(planExecuteContract.Source).To(gomega.Equal(routeSourceTypedCommand), fmt.Sprintf("plan-execute command contract = %#v, want typed explicit mutation authority", planExecuteContract))
	gomega.Expect(planExecuteContract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("plan-execute command contract = %#v, want typed explicit mutation authority", planExecuteContract))
})

var _ = ginkgo.It("route contract prompt wording and path shape do not grant mutation authority", func() {
	prompt := "Implement the change. Required edit paths: internal/surfaces/tui/app/route_contract.go, internal/surfaces/tui/app/route_contract_test.go."
	plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Source).To(gomega.Equal(routeSourceConservativeFallback), fmt.Sprintf("source = %q, want conservative fallback", contract.Source))

	gomega.Expect(contract.Family).To(gomega.Equal("conversation"), fmt.Sprintf("fallback prompt with path-shaped wording should stay conversation: %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("main"), fmt.Sprintf("fallback prompt with path-shaped wording should stay conversation: %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("fallback prompt with path-shaped wording should stay conversation: %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityNone), fmt.Sprintf("prompt wording/path shape must not grant mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeFalse(), fmt.Sprintf("prompt wording/path shape must not grant mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract outcome corrects post completion defect report to implementation", func() {
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

	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "that didnt quite fix it - the cursor still goes off the screen.", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Family).To(gomega.Equal("implementation"), fmt.Sprintf("post-completion defect report should be corrected to implementation, got %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("implement"), fmt.Sprintf("post-completion defect report should be corrected to implementation, got %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("post-completion defect report should be corrected to implementation, got %#v", contract))

	gomega.Expect(contract.RequestedOutcome).To(gomega.Equal("change_workspace"), fmt.Sprintf("outcome metadata was not preserved: %#v", contract))
	gomega.Expect(contract.WriteIntent).To(gomega.Equal("yes"), fmt.Sprintf("outcome metadata was not preserved: %#v", contract))
	gomega.Expect(contract.ContinuationTarget).To(gomega.Equal("prior_change"), fmt.Sprintf("outcome metadata was not preserved: %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityExplicit), fmt.Sprintf("corrected classifier route should activate guarded mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeTrue(), fmt.Sprintf("corrected classifier route should activate guarded mutation authority: %#v", contract))
})

var _ = ginkgo.It("route contract read only constraint beats implementation outcome", func() {
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

	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Before changing anything, why didn't that fix it?", modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Family).To(gomega.Equal("conversation"), fmt.Sprintf("explicit read-only question should be corrected to conversation, got %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("main"), fmt.Sprintf("explicit read-only question should be corrected to conversation, got %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("explicit read-only question should be corrected to conversation, got %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityNone), fmt.Sprintf("read-only constraint must not grant mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeFalse(), fmt.Sprintf("read-only constraint must not grant mutation authority: %#v", contract))

	gomega.Expect(contract.ReadOnlyConstraint).To(gomega.BeTrue(), fmt.Sprintf("read-only metadata was not preserved: %#v", contract))
	gomega.Expect(contract.WriteIntent).To(gomega.Equal("no"), fmt.Sprintf("read-only metadata was not preserved: %#v", contract))
})

var _ = ginkgo.It("route contract evidence report prompt beats misclassified implementation", func() {
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

	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())
	contract := projectRouteContract(plan, modepolicy.BuildMode())

	gomega.Expect(contract.Family).To(gomega.Equal("conversation"), fmt.Sprintf("evidence-report prompt should be corrected to conversation, got %#v", contract))
	gomega.Expect(contract.Lane).To(gomega.Equal("main"), fmt.Sprintf("evidence-report prompt should be corrected to conversation, got %#v", contract))
	gomega.Expect(contract.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("evidence-report prompt should be corrected to conversation, got %#v", contract))

	gomega.Expect(contract.MutationAuthority).To(gomega.Equal(routeMutationAuthorityNone), fmt.Sprintf("evidence-report prompt must not grant mutation authority: %#v", contract))
	gomega.Expect(contract.MutationCapable).To(gomega.BeFalse(), fmt.Sprintf("evidence-report prompt must not grant mutation authority: %#v", contract))

	gomega.Expect(contract.RequestedOutcome).To(gomega.Equal("diagnose"), fmt.Sprintf("evidence-report outcome metadata not corrected: %#v", contract))
	gomega.Expect(contract.WriteIntent).To(gomega.Equal("no"), fmt.Sprintf("evidence-report outcome metadata not corrected: %#v", contract))
})

var _ = ginkgo.It("correct turn route plan keeps debug and fix implementation", func() {
	prompt := "Please debug and fix the issue where resizing the TUI mid-answer makes the conversation non-scrollable."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
		Family:           "investigate_then_implement",
		RequestedOutcome: "diagnose_then_change",
		WriteIntent:      "yes",
	}, prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("investigate_then_implement"), fmt.Sprintf("debug-and-fix prompt should stay implementation-shaped, got %#v", plan))
	gomega.Expect(plan.InitialLane).To(gomega.Equal("explore"), fmt.Sprintf("debug-and-fix prompt should stay implementation-shaped, got %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.Equal("implement"), fmt.Sprintf("debug-and-fix prompt should stay implementation-shaped, got %#v", plan))

	gomega.Expect(plan.WriteIntent).To(gomega.Equal("yes"), fmt.Sprintf("debug-and-fix metadata lost: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("debug-and-fix metadata lost: %#v", plan))
})

var _ = ginkgo.It("evidence report prompt mutation guard uses whole words", func() {
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
		}, prompt, modepolicy.BuildMode())

		gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("descriptive RCA wording should not be treated as mutation intent for %q, got %#v", prompt, plan))
		gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("descriptive RCA wording should not be treated as mutation intent for %q, got %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("descriptive RCA wording should not be treated as mutation intent for %q, got %#v", prompt, plan))

		gomega.Expect(plan.WriteIntent).To(gomega.Equal("no"), fmt.Sprintf("evidence-report metadata not corrected for %q: %#v", prompt, plan))
		gomega.Expect(plan.RequestedOutcome).To(gomega.Equal("diagnose"), fmt.Sprintf("evidence-report metadata not corrected for %q: %#v", prompt, plan))
	}
})

var _ = ginkgo.It("evidence report prompt does not override explicit followup mutation", func() {
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
		}, prompt, modepolicy.BuildMode())

		gomega.Expect(plan.Family).To(gomega.Equal("investigate_then_implement"), fmt.Sprintf("explicit follow-up mutation should stay implementation-shaped for %q, got %#v", prompt, plan))
		gomega.Expect(plan.InitialLane).To(gomega.Equal("explore"), fmt.Sprintf("explicit follow-up mutation should stay implementation-shaped for %q, got %#v", prompt, plan))
		gomega.Expect(plan.FollowupLane).To(gomega.Equal("implement"), fmt.Sprintf("explicit follow-up mutation should stay implementation-shaped for %q, got %#v", prompt, plan))

		gomega.Expect(plan.WriteIntent).To(gomega.Equal("yes"), fmt.Sprintf("explicit follow-up mutation metadata lost for %q: %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("explicit follow-up mutation metadata lost for %q: %#v", prompt, plan))
	}
})
