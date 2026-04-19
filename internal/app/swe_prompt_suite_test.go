package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

type representativeSWEPromptCase struct {
	name         string
	prompt       string
	lane         analysisLane
	followupLane analysisLane
	reason       promptRouteReason
	contract     answerContract
	thoroughness workspace.ExploreThoroughness
}

func representativeSWEPromptSuite() []representativeSWEPromptCase {
	return []representativeSWEPromptCase{
		{
			name:     "direct question",
			prompt:   "What does the debug log redacted mode do?",
			lane:     analysisLaneMain,
			reason:   promptRouteReasonDirectConversation,
			contract: answerContractDirectConversation,
		},
		{
			name:     "run verification command",
			prompt:   "Run the Go test suite and report any failures.",
			lane:     analysisLaneMain,
			reason:   promptRouteReasonDirectConversation,
			contract: answerContractDirectConversation,
		},
		{
			name:     "locate code",
			prompt:   `Find where the "Qwen is working" banner is rendered.`,
			lane:     analysisLaneLocate,
			reason:   promptRouteReasonLocateRequest,
			contract: answerContractLocateTargets,
		},
		{
			name:         "locate then edit",
			prompt:       `Find where the "Qwen is working" banner is rendered and remove it.`,
			lane:         analysisLaneLocate,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonLocateRequest,
			contract:     answerContractLocateTargets,
		},
		{
			name:         "explore then implement",
			prompt:       "Explore the routing flow in this codebase and then update docs/frontier-routing-implementation-plan.md with what you find.",
			lane:         analysisLaneExplore,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonExploreRequest,
			contract:     answerContractGroundedExplanation,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "deep investigate then fix",
			prompt:       "Do a thorough investigation of the guarded write flow in this codebase, then update the implementation if a gap is confirmed.",
			lane:         analysisLaneExplore,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonExploreRequest,
			contract:     answerContractGroundedExplanation,
			thoroughness: workspace.ThoroughnessThorough,
		},
		{
			name:         "latest logs failure inspection",
			prompt:       "Check the most recent logs and tell me why this failed.",
			lane:         analysisLaneExplore,
			reason:       promptRouteReasonExploreRequest,
			contract:     answerContractGroundedExplanation,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "longer search when confidence is low",
			prompt:       "Investigate this codebase for hidden routing bugs. Do not stop early if you are not super confident.",
			lane:         analysisLaneReview,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessThorough,
		},
		{
			name:         "review bugs then fix",
			prompt:       "Review this codebase for regressions and apply the fix if you find a bounded issue.",
			lane:         analysisLaneReview,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "security audit",
			prompt:       "Audit this repo for security vulnerabilities.",
			lane:         analysisLaneReview,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "performance review",
			prompt:       "Review this codebase for performance regressions.",
			lane:         analysisLaneReview,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "cleanup review",
			prompt:       "Identify one code smell in this codebase.",
			lane:         analysisLaneReview,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "architecture assessment",
			prompt:       "Analyze package boundaries and ownership in this codebase.",
			lane:         analysisLaneArchitecture,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "architecture improvements",
			prompt:       "Can you identify architectural improvements we can make?",
			lane:         analysisLaneArchitecture,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "optimize our architecture",
			prompt:       "please identify opportunities to optimize our architecture",
			lane:         analysisLaneArchitecture,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "optimization opportunity discovery shorthand",
			prompt:       "Discovery optimization opportunities.",
			lane:         analysisLaneArchitecture,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "language capability architecture",
			prompt:       "Which languages does this harness support, and how extensible is that architecture?",
			lane:         analysisLaneArchitecture,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "architecture then implement",
			prompt:       "Analyze this codebase's structure, identify the best package boundary split, and then implement it.",
			lane:         analysisLaneArchitecture,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractArchitecture,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "review then implement",
			prompt:       "Review this codebase for regressions and apply the fix if you find a bounded issue.",
			lane:         analysisLaneReview,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonReviewRequest,
			contract:     answerContractReviewFindings,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:         "explore then docs update",
			prompt:       "Explore the routing flow first, then update docs/frontier-routing-implementation-plan.md with what you find.",
			lane:         analysisLaneExplore,
			followupLane: analysisLaneImplement,
			reason:       promptRouteReasonExploreRequest,
			contract:     answerContractGroundedExplanation,
			thoroughness: workspace.ThoroughnessMedium,
		},
		{
			name:     "concrete architecture refactor",
			prompt:   "Using the prior architecture recommendation, refactor internal/app/app.go in one shot and split existing code into cohesive new files.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "targeted code edit",
			prompt:   "Update the code in internal/app/app.go to improve the execution panel label.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "add regression test",
			prompt:   "Add regression tests in internal/app/write_guard_test.go for route plan rendering.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "config edit",
			prompt:   "Update config.example.json so max_tool_steps is 16.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "script edit",
			prompt:   "Edit scripts/run-harness.sh to print the selected profile before launching.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "documentation edit",
			prompt:   "Update README.md with a short troubleshooting note for debug logs.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "plan a refactor read-only",
			prompt:   "Plan a refactor for internal/app/analysis_agents.go.",
			lane:     analysisLaneArchitecture,
			reason:   promptRouteReasonArchitectureRequest,
			contract: answerContractArchitecture,
		},
		{
			name:     "prompt behavior edit",
			prompt:   "Edit prompts/base.md to clarify the answer style guidance.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
		{
			name:     "shell-only mutation",
			prompt:   "Create a new file scratch-note.txt containing hello by using shell commands only.",
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractImplementation,
		},
	}
}

func TestDeterministicControlRailsForDebugLogs(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Check the latest logs and explain why this failed.", mgr)
	rails := buildDeterministicControlRails("Check the latest logs and explain why this failed.", plan)
	if rails.TaskFamily != "debug_logs" {
		t.Fatalf("expected debug_logs family, got %#v", rails)
	}
	if rails.Lane != "explore" || rails.Contract != string(answerContractGroundedExplanation) {
		t.Fatalf("expected explore grounded contract, got %#v", rails)
	}
	if rails.PermissionProfile != "read_only_workspace" {
		t.Fatalf("expected read-only permission profile, got %#v", rails)
	}
	for _, want := range []string{"log_record", "failure_output", "timestamp_or_latest_selection"} {
		if !testContainsFold(rails.RequiredEvidence, want) {
			t.Fatalf("expected required evidence %q, got %#v", want, rails.RequiredEvidence)
		}
	}
	if !testContainsFold(rails.RequiredTools, "log_inspection") {
		t.Fatalf("expected log inspection precondition, got %#v", rails.RequiredTools)
	}
	if !testContainsFold(rails.SearchPolicy, "start_with_most_recent_log") {
		t.Fatalf("expected latest-log search policy, got %#v", rails.SearchPolicy)
	}
	if !rails.FailureToEval {
		t.Fatalf("expected failure-to-eval capture for failure prompt, got %#v", rails)
	}
}

func TestDeterministicControlRailsForImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Update internal/app/app.go to improve route plan rendering.", mgr)
	rails := buildDeterministicControlRails("Update internal/app/app.go to improve route plan rendering.", plan)
	if rails.TaskFamily != "implementation" || rails.PermissionProfile != "guarded_workspace_writes" {
		t.Fatalf("unexpected implementation rails: %#v", rails)
	}
	for _, want := range []string{"changed_files_or_noop_reason", "verification_result", "risk_contract"} {
		if !testContainsFold(rails.RequiredEvidence, want) {
			t.Fatalf("expected implementation evidence %q, got %#v", want, rails.RequiredEvidence)
		}
	}
	if rails.VerificationContract != "tests_passed_or_failed_or_skipped_with_blocker" {
		t.Fatalf("expected verification contract, got %#v", rails)
	}
	if !testContainsFold(rails.FinalCritic, "changed_files_section") || !testContainsFold(rails.FinalCritic, "verification_section") {
		t.Fatalf("expected implementation final critic checks, got %#v", rails.FinalCritic)
	}
}

func TestDeterministicControlRailsForVerificationOnlyAllowsShellChecks(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Run the Go test suite and report any failures."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	rails := buildDeterministicControlRails(prompt, plan)
	if rails.TaskFamily != "verification_only" {
		t.Fatalf("expected verification_only family, got %#v", rails)
	}
	if rails.PermissionProfile != "read_only_verification_shell" {
		t.Fatalf("expected verification shell permission profile, got %#v", rails)
	}
	if !testContainsFold(rails.RequiredTools, "shell") {
		t.Fatalf("expected shell as required verification tool, got %#v", rails.RequiredTools)
	}
	if testContainsFold(rails.ForbiddenTools, "shell") {
		t.Fatalf("verification-only rails must not forbid shell checks, got %#v", rails.ForbiddenTools)
	}
	if controlRailPreExecute(rails, workspace.ToolRequest{Tool: "shell", Command: "go test ./..."}) != nil {
		t.Fatalf("expected verification shell command to pass rails")
	}
	if blocked := controlRailPreExecute(rails, workspace.ToolRequest{Tool: "shell", Command: "rm -rf /tmp/example"}); blocked == nil {
		t.Fatalf("expected mutating shell command to be blocked")
	}
}

func TestControlRailRuntimeInstructionForVerificationOnlyRequiresShellStatus(t *testing.T) {
	rails := buildDeterministicControlRails("Run the Go test suite and report any failures.", promptExecutionPlan{
		lane:       analysisLaneMain,
		taskFamily: "verification_only",
	})
	instruction := controlRailRuntimeInstruction(rails)
	for _, want := range []string{
		"verification-only task",
		"run one read-only shell verification command",
		"report pass/fail/skipped status",
		"Do not edit files",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("expected runtime instruction to include %q, got:\n%s", want, instruction)
		}
	}
}

func TestControlRailCompletionGateRejectsVerificationOnlyWithoutShell(t *testing.T) {
	rails := buildDeterministicControlRails("can you please check if your previous changes made it through?", promptExecutionPlan{
		lane:       analysisLaneMain,
		taskFamily: "verification_only",
	})
	gate := controlRailCompletionGate(rails)
	if gate == nil {
		t.Fatal("expected verification-only completion gate")
	}
	result := gate(agent.CompletionGateInput{
		Reply: "The previous changes appear to be present.",
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read internal/app/analysis_agents.go"},
			{Tool: "read_file", Summary: "Read internal/app/analysis_agents.go (replayed prior evidence)"},
		},
	})
	if result.Accept {
		t.Fatalf("expected gate to reject final answer without shell verification")
	}
	if !strings.Contains(result.Error, "shell verification command") {
		t.Fatalf("expected actionable shell verification error, got %#v", result)
	}
}

func TestControlRailCompletionGateAcceptsFailedShellVerificationWithStatus(t *testing.T) {
	rails := buildDeterministicControlRails("Run the Go test suite and report any failures.", promptExecutionPlan{
		lane:       analysisLaneMain,
		taskFamily: "verification_only",
	})
	gate := controlRailCompletionGate(rails)
	result := gate(agent.CompletionGateInput{
		Reply: "Verification failed: `go test ./...` exited with an error. Failure output: package qwen-bubbles/internal/app failed.",
		Results: []workspace.ToolResult{
			{
				Tool:    "shell",
				Summary: "Ran shell command in .",
				Output:  "$ go test ./...\nFAIL\tqwen-bubbles/internal/app",
				Error:   "exit status 1",
			},
		},
	})
	if !result.Accept {
		t.Fatalf("expected failed shell verification with explicit status to pass gate, got %#v", result)
	}
}

func TestControlRailCompletionGateRejectsVerificationAnswerWithoutStatusAfterShell(t *testing.T) {
	rails := buildDeterministicControlRails("Run the Go test suite and report any failures.", promptExecutionPlan{
		lane:       analysisLaneMain,
		taskFamily: "verification_only",
	})
	gate := controlRailCompletionGate(rails)
	result := gate(agent.CompletionGateInput{
		Reply: "I ran `go test ./...` and checked the output.",
		Results: []workspace.ToolResult{
			{
				Tool:    "shell",
				Summary: "Ran shell command in .",
				Output:  "$ go test ./...\nok\tqwen-bubbles/internal/app",
			},
		},
	})
	if result.Accept {
		t.Fatalf("expected gate to reject shell verification answer without explicit pass/fail status")
	}
	if !strings.Contains(result.Error, "pass/fail/skipped") {
		t.Fatalf("expected status-specific rejection, got %#v", result)
	}
}

func TestDeterministicControlRailsPreferImplementationFamilyForFailureFix(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Fix internal/app/app.go because it failed."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	rails := buildDeterministicControlRails(prompt, plan)
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected implementation lane, got %q", plan.lane)
	}
	if rails.TaskFamily != "implementation" {
		t.Fatalf("expected implementation rails despite failure wording, got %#v", rails)
	}
}

func TestContextualRouteValidatesPriorArchitectureOption(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(testWorkspaceConfig()),
		messages: []llama.ChatMessage{
			{
				Role: "assistant",
				Content: "Architecture hotspot board:\n" +
					"- internal/app/analysis_agents.go: split orchestration/state handling from helper and rendering concerns\n\n" +
					"Recommended next step:\n" +
					"- validate the top hotspot boundaries and split one responsibility at a time.",
			},
		},
	}
	plan := model.classifyPromptExecutionPlan("please validate the analysis_agents.go option")
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected contextual architecture validation route, got %#v", plan)
	}
	if plan.reason != promptRouteReasonArchitectureRequest || plan.contract != answerContractArchitecture {
		t.Fatalf("expected architecture route contract, got %#v", plan)
	}
}

func TestContextualRouteExecutesPriorArchitectureRecommendation(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(testWorkspaceConfig()),
		messages: []llama.ChatMessage{
			{
				Role: "assistant",
				Content: "Specific optimization opportunities for internal/app/analysis_agents.go.\n\n" +
					"Recommended Implementation Order:\n" +
					"1. Extract state management.\n" +
					"2. Create prompt generation boundaries.\n" +
					"3. Extract output formatting.\n\n" +
					"Expected benefits: clearer architecture and safer refactor boundaries.",
			},
		},
	}
	plan := model.classifyPromptExecutionPlan("do it")
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected terse follow-up to route to implementation with prior recommendation context, got %#v", plan)
	}
	if plan.reason != promptRouteReasonDirectImplementation || plan.contract != answerContractImplementation {
		t.Fatalf("expected direct implementation contract, got %#v", plan)
	}
}

func TestContextualRouteAddsAllPriorArchitectureRecommendation(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(testWorkspaceConfig()),
		messages: []llama.ChatMessage{
			{
				Role: "assistant",
				Content: "Specific optimization opportunities for internal/app/analysis_agents.go.\n\n" +
					"Recommended Implementation Order:\n" +
					"1. Extract state management.\n" +
					"2. Create prompt generation boundaries.\n" +
					"3. Extract output formatting.\n\n" +
					"Expected benefits: clearer architecture and safer refactor boundaries.",
			},
		},
	}
	plan := model.classifyPromptExecutionPlan("add it all")
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected terse add-it-all follow-up to route to implementation with prior recommendation context, got %#v", plan)
	}
	if plan.reason != promptRouteReasonDirectImplementation || plan.contract != answerContractImplementation {
		t.Fatalf("expected direct implementation contract, got %#v", plan)
	}
}

func TestContextualRoutePlansPriorArchitectureRecommendationReadOnly(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(testWorkspaceConfig()),
		messages: []llama.ChatMessage{
			{
				Role: "assistant",
				Content: "Architecture hotspot board:\n" +
					"- internal/app/analysis_agents.go: split orchestration/state handling from helper and rendering concerns\n\n" +
					"Recommended next step:\n" +
					"- validate the top hotspot boundaries and split one responsibility at a time.",
			},
		},
	}
	plan := model.classifyPromptExecutionPlan("The analysis_agents.go file, can you prepare an plan to update this one")
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected plan-only architecture follow-up to stay read-only, got %#v", plan)
	}
	if plan.reason != promptRouteReasonArchitectureRequest || plan.contract != answerContractArchitecture {
		t.Fatalf("expected architecture contract, got %#v", plan)
	}
}

func TestContextualRouteDoesNotImplementBareDoItWithoutContext(t *testing.T) {
	model := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}
	plan := model.classifyPromptExecutionPlan("do it")
	if plan.lane != analysisLaneMain {
		t.Fatalf("expected bare do-it without actionable context to remain direct conversation, got %#v", plan)
	}
}

func TestPlanOnlyPromptDoesNotRouteToImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Prepare a plan to update internal/app/analysis_agents.go.", mgr)
	if plan.lane == analysisLaneImplement {
		t.Fatalf("plan-only prompt should not route to implementation, got %#v", plan)
	}
}

func TestRefactorPlanOnlyPromptDoesNotRouteToImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Can you prepare a refactor plan for internal/app/analysis_agents.go?", mgr)
	if plan.lane == analysisLaneImplement {
		t.Fatalf("refactor plan-only prompt should not route to implementation, got %#v", plan)
	}
	if plan.lane != analysisLaneArchitecture || plan.contract != answerContractArchitecture {
		t.Fatalf("refactor plan-only prompt should route to architecture, got %#v", plan)
	}
	if !plan.intent.WantsPlanOnly || !testContainsFold(plan.intent.Vetoes, "plan_only_blocks_edits") {
		t.Fatalf("expected structured plan-only intent veto, got %#v", plan.intent)
	}
	if plan.intent.Confidence == "" || plan.intent.Risk == "" {
		t.Fatalf("expected intent confidence and risk snapshot, got %#v", plan.intent)
	}
}

func TestPlanARefactorPromptDoesNotRouteToImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompts := []string{
		"Plan a refactor for internal/app/analysis_agents.go.",
		"Please plan the refactor for internal/app/analysis_agents.go.",
		"Plan a refactoring for internal/app/analysis_agents.go.",
	}
	for _, prompt := range prompts {
		plan := classifyPromptExecutionPlan(prompt, mgr)
		if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
			t.Fatalf("plan-a-refactor prompt should remain read-only, prompt=%q plan=%#v", prompt, plan)
		}
		if plan.lane != analysisLaneArchitecture || plan.contract != answerContractArchitecture {
			t.Fatalf("plan-a-refactor prompt should route to architecture, prompt=%q plan=%#v", prompt, plan)
		}
		if !plan.intent.WantsPlanOnly || !testContainsFold(plan.intent.Vetoes, "plan_only_blocks_edits") {
			t.Fatalf("expected plan-only veto, prompt=%q intent=%#v", prompt, plan.intent)
		}
	}
}

func TestOptimizationOpportunityDiscoveryRoutesToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompts := []string{
		"Discovery optimization opportunities.",
		"Discover optimization opportunities.",
		"Identify optimization opportunities in the harness.",
		"Explore this Go codebase and identify one bounded optimization/refactor opportunity that can be implemented by editing only one existing git-clean Go file. Avoid files already modified in git status. Prepare a concrete refactor plan only; do not edit files yet.",
	}
	for _, prompt := range prompts {
		plan := classifyPromptExecutionPlan(prompt, mgr)
		if plan.lane != analysisLaneArchitecture {
			t.Fatalf("optimization-opportunity discovery should route architecture, prompt=%q plan=%#v", prompt, plan)
		}
		if plan.followupLane != "" {
			t.Fatalf("optimization-opportunity discovery should not auto-implement, prompt=%q plan=%#v", prompt, plan)
		}
	}
}

func TestThreeTurnOpportunityPlanPerformRefactorWorkflowRoutesStably(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	discovery := classifyPromptExecutionPlan("Discovery optimization opportunities.", mgr)
	if discovery.lane != analysisLaneArchitecture || discovery.followupLane != "" {
		t.Fatalf("discovery should be read-only architecture, got %#v", discovery)
	}
	planning := classifyPromptExecutionPlan("Plan a refactor for internal/app/analysis_agents.go.", mgr)
	if planning.lane != analysisLaneArchitecture || planning.followupLane != "" || !planning.intent.WantsPlanOnly {
		t.Fatalf("planning turn should be durable-plan architecture, got %#v", planning)
	}
	memory := buildAnalysisTaskMemory(
		"Plan a refactor for internal/app/analysis_agents.go.",
		analysisLaneArchitecture,
		"Focused refactor plan:\n- Target: internal/app/analysis_agents.go\n- Evidence: internal/app/implementation.go\n\nVerification:\n- go test ./internal/app",
		[]string{"internal/app/analysis_agents.go", "internal/app/implementation.go"},
		"run go test ./internal/app",
	)
	memory = decorateDurablePlanArtifactMemory(memory, memory.Prompt, analysisLaneArchitecture, memory.Summary, []string{"internal/app/analysis_agents.go", "internal/app/implementation.go"}, "run go test ./internal/app")
	if !shouldResumeTaskMemoryPrompt("Perform the refactor.", memory) {
		t.Fatal("perform-the-refactor should approve and resume the durable plan")
	}
	if shouldResumeTaskMemoryPrompt("Do not perform the refactor yet.", memory) {
		t.Fatal("negative perform-the-refactor phrasing must not approve the durable plan")
	}
	if got := laneForTaskMemoryWithPrompt(memory, "Perform the refactor."); got != analysisLaneImplement {
		t.Fatalf("perform-the-refactor should switch durable plan to implementation, got %q", got)
	}
	resumePrompt := buildTaskMemoryResumePrompt(memory, "Perform the refactor.")
	if !strings.Contains(resumePrompt, "Plan approval: explicit approval detected") {
		t.Fatalf("expected explicit build handoff in resume prompt, got:\n%s", resumePrompt)
	}
}

func TestTypedWorkflowStateTracksDiscoveryPlanApproval(t *testing.T) {
	model := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}
	model.rememberAnalysisTaskMemory(
		"Discovery optimization opportunities.",
		analysisLaneArchitecture,
		"Architecture hotspot board:\n- internal/app/analysis_agents.go: split orchestration/state handling from helper and rendering concerns.",
		[]string{"internal/app/analysis_agents.go"},
		"prepare a refactor plan for internal/app/analysis_agents.go",
	)
	state := model.currentWorkflowState()
	if state == nil || state.Kind != agentWorkflowKindOptimizationRefactor || state.Phase != agentWorkflowPhaseDiscovered {
		t.Fatalf("expected discovered optimization workflow state, got %#v", state)
	}

	plan := model.classifyPromptExecutionPlan("Plan a refactor for internal/app/analysis_agents.go.")
	if plan.lane != analysisLaneArchitecture || plan.followupLane != "" {
		t.Fatalf("workflow planning follow-up should remain read-only architecture, got %#v", plan)
	}
	if plan.workflowKind != agentWorkflowKindOptimizationRefactor || plan.workflowPhase != agentWorkflowPhasePlanning {
		t.Fatalf("expected route plan to expose workflow planning state, got %#v", plan)
	}

	model.rememberAnalysisTaskMemory(
		"Plan a refactor for internal/app/analysis_agents.go.",
		analysisLaneArchitecture,
		"Focused refactor plan:\n- Target: internal/app/analysis_agents.go\n- Evidence: internal/app/implementation.go\n\nVerification:\n- go test ./internal/app",
		[]string{"internal/app/analysis_agents.go", "internal/app/implementation.go"},
		"run go test ./internal/app",
	)
	state = model.currentWorkflowState()
	if state == nil || state.Phase != agentWorkflowPhaseAwaitingApproval {
		t.Fatalf("expected durable workflow plan awaiting approval, got %#v", state)
	}
	if !testContainsFold(state.EditableTargets, "internal/app/analysis_agents.go") {
		t.Fatalf("expected editable target from durable plan, got %#v", state.EditableTargets)
	}
	if !testContainsFold(state.ReadOnlyEvidence, "internal/app/implementation.go") {
		t.Fatalf("expected read-only evidence from durable plan, got %#v", state.ReadOnlyEvidence)
	}
	if !shouldResumeWorkflowPrompt("Perform the refactor.", state) {
		t.Fatal("expected perform-the-refactor to resume typed workflow state")
	}
	if shouldResumeWorkflowPrompt("Do not perform the refactor yet.", state) {
		t.Fatal("negative approval phrasing must not resume typed workflow state")
	}

	build := model.classifyPromptExecutionPlan("Perform the refactor.")
	if build.lane != analysisLaneImplement || !build.intent.WantsEdits {
		t.Fatalf("approved workflow prompt should route to implementation, got %#v", build)
	}
	if build.workflowKind != agentWorkflowKindOptimizationRefactor || build.workflowPhase != agentWorkflowPhaseBuilding {
		t.Fatalf("expected build route to carry workflow build state, got %#v", build)
	}
	resume := buildWorkflowResumePrompt(state, "Perform the refactor.")
	for _, want := range []string{"Resume the typed agent workflow", "Workflow-approved editable targets", "internal/app/analysis_agents.go", "Build-switch reminder"} {
		if !strings.Contains(resume, want) {
			t.Fatalf("expected workflow resume prompt to include %q, got:\n%s", want, resume)
		}
	}
	ctx := buildWorkflowResumeContext(state)
	if !testContainsFold(ctx.ApprovedEditTargets, "internal/app/analysis_agents.go") {
		t.Fatalf("expected typed resume context approved target, got %#v", ctx)
	}
	if testContainsFold(ctx.ReadOnlyEvidencePaths, "internal/app/analysis_agents.go") {
		t.Fatalf("editable target must not be duplicated as read-only evidence, got %#v", ctx.ReadOnlyEvidencePaths)
	}
}

func TestWorkflowStatePersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	saveDir := filepath.Join(root, "transcripts")
	cfg := config.Config{
		Chat: config.Chat{SaveDir: saveDir},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	}
	model := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	model.rememberWorkflowState(&agentWorkflowState{
		Active:          true,
		Kind:            agentWorkflowKindOptimizationRefactor,
		Phase:           agentWorkflowPhaseAwaitingApproval,
		Objective:       "Plan a refactor for target.go.",
		EditableTargets: []string{"target.go"},
		Verification:    []string{"go test ./..."},
	}, "test persist")
	if model.currentWorkflowState() == nil {
		t.Fatal("expected workflow state to persist in model")
	}
	if len(model.currentWorkflowState().FileFingerprints) == 0 {
		t.Fatalf("expected persisted workflow state to include target fingerprints, got %#v", model.currentWorkflowState())
	}

	reloaded := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	reloaded.loadPersistedWorkflowState()
	if reloaded.currentWorkflowState() == nil || !testContainsFold(reloaded.currentWorkflowState().EditableTargets, "target.go") {
		t.Fatalf("expected persisted workflow state to reload, got %#v", reloaded.workflowState)
	}

	unfingerprinted := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	stale, reason := unfingerprinted.workflowStateStaleOnLoad(&agentWorkflowState{
		Active:          true,
		Kind:            agentWorkflowKindOptimizationRefactor,
		Phase:           agentWorkflowPhaseAwaitingApproval,
		EditableTargets: []string{"target.go"},
	})
	if !stale || !strings.Contains(reason, "predates target fingerprints") {
		t.Fatalf("expected unfingerprinted workflow state to require replanning, stale=%t reason=%q", stale, reason)
	}

	if err := os.WriteFile(target, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("modify target: %v", err)
	}
	staleModel := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	staleModel.loadPersistedWorkflowState()
	if staleModel.currentWorkflowState() != nil {
		t.Fatalf("expected stale workflow state to be cleared after target changed, got %#v", staleModel.currentWorkflowState())
	}
}

func TestGenericDurablePlanWorkflowKindDoesNotMasqueradeAsOptimizationRefactor(t *testing.T) {
	model := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}
	model.rememberWorkflowFromTaskMemory(
		"Create an implementation plan for docs/api.md.",
		analysisLaneArchitecture,
		&longHorizonTaskMemory{
			Active:        true,
			Kind:          "durable_plan",
			Prompt:        "Create an implementation plan for docs/api.md.",
			Lane:          string(analysisLaneArchitecture),
			Summary:       "Durable plan artifact\nTarget: docs/api.md\nVerification:\n- go test ./...",
			ChosenTargets: []string{"docs/api.md"},
			NextActions:   []string{"wait for approval before editing docs/api.md"},
		},
		[]string{"docs/api.md"},
		"wait for approval before editing docs/api.md",
	)
	state := model.currentWorkflowState()
	if state == nil {
		t.Fatal("expected generic durable plan to create typed workflow state")
	}
	if state.Kind != agentWorkflowKindDurablePlan {
		t.Fatalf("expected generic durable plan workflow kind, got %#v", state)
	}
	if state.Kind == agentWorkflowKindOptimizationRefactor {
		t.Fatalf("generic durable plan must not inherit optimization-refactor semantics, got %#v", state)
	}
}

func TestWorkflowApprovalRequiresAwaitingApprovalScope(t *testing.T) {
	state := &agentWorkflowState{
		Active:          true,
		Kind:            agentWorkflowKindOptimizationRefactor,
		Phase:           agentWorkflowPhasePlanning,
		EditableTargets: []string{"internal/app/analysis_agents.go"},
	}
	if shouldResumeWorkflowPrompt("Perform the refactor.", state) {
		t.Fatal("planning-phase workflow must not resume implementation before durable approval state exists")
	}

	state.Phase = agentWorkflowPhaseAwaitingApproval
	state.EditableTargets = nil
	if shouldResumeWorkflowPrompt("Perform the refactor.", state) {
		t.Fatal("approval prompt must not resume workflow without editable targets or allowed destinations")
	}

	state.EditableTargets = []string{"internal/app/analysis_agents.go"}
	if !shouldResumeWorkflowPrompt("Perform the refactor.", state) {
		t.Fatal("awaiting-approval workflow with scoped targets should resume implementation")
	}
}

func TestWorkflowImplementationAcceptedRequiresNewVerifiedJournal(t *testing.T) {
	state := &agentWorkflowState{
		Active: true,
		Kind:   agentWorkflowKindOptimizationRefactor,
		Phase:  agentWorkflowPhaseBuilding,
	}
	previous := &executionJournal{
		Paths:              []string{"internal/app/workflow_state.go"},
		Summary:            "Updated workflow state.",
		VerificationPassed: true,
	}
	model := &Model{lastJournal: previous}
	if model.workflowImplementationAccepted(state, previous) {
		t.Fatal("workflow completion must not accept an old journal from before the implementation run")
	}

	model.lastJournal = &executionJournal{
		Paths:   []string{"internal/app/workflow_state.go"},
		Summary: "Updated workflow state.",
	}
	if model.workflowImplementationAccepted(state, previous) {
		t.Fatal("workflow completion must require verification evidence")
	}

	model.lastJournal = &executionJournal{
		Paths:              []string{"internal/app/workflow_state.go"},
		Summary:            "Control-rail gaps: verification_contract_unresolved.",
		VerificationPassed: true,
	}
	if model.workflowImplementationAccepted(state, previous) {
		t.Fatal("workflow completion must reject control-rail gap summaries")
	}

	model.lastJournal = &executionJournal{
		Paths:              []string{"internal/app/workflow_state.go"},
		Summary:            "Updated workflow state and verified the focused app tests.",
		VerificationPassed: true,
	}
	if !model.workflowImplementationAccepted(state, previous) {
		t.Fatal("workflow completion should accept a new journal with changed paths and passed verification")
	}

	model.lastJournal = &executionJournal{
		Summary:            "No file edits were needed: the approved refactor is already present.\n\nVerification:\n- `go test ./...`: passed",
		VerificationPassed: true,
	}
	if !model.workflowImplementationAccepted(state, previous) {
		t.Fatal("workflow completion should accept a verified no-op journal")
	}
}

func TestWorkflowStateIncludedInClassifierMemory(t *testing.T) {
	model := &Model{
		workflowState: &agentWorkflowState{
			Active:          true,
			Kind:            agentWorkflowKindOptimizationRefactor,
			Phase:           agentWorkflowPhaseAwaitingApproval,
			Objective:       "Plan a refactor for internal/app/analysis_agents.go.",
			EditableTargets: []string{"internal/app/analysis_agents.go"},
			Verification:    []string{"go test ./internal/app"},
		},
	}
	memory := model.renderRoutingMemoryForVerifier()
	for _, want := range []string{"Persisted workflow state", "awaiting_approval", "internal/app/analysis_agents.go", "go test ./internal/app"} {
		if !strings.Contains(memory, want) {
			t.Fatalf("expected classifier routing memory to include %q, got:\n%s", want, memory)
		}
	}
}

func TestPerformRefactorUsesRecentPlanContextWhenMemoryIsUnavailable(t *testing.T) {
	model := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}
	model.messages = []llama.ChatMessage{
		{Role: "assistant", Content: "Focused refactor plan.\nTarget: internal/app/analysis_agents.go\nRecommended implementation order: split one responsibility, then run go test ./internal/app."},
	}
	plan := model.classifyPromptExecutionPlan("Perform the refactor.")
	if plan.lane != analysisLaneImplement {
		t.Fatalf("perform-the-refactor should use recent focused plan context, got %#v", plan)
	}
	if plan.reason != promptRouteReasonDirectImplementation || !plan.intent.WantsEdits {
		t.Fatalf("expected direct implementation intent from recent plan context, got %#v", plan)
	}
}

func TestRefactoringPlanOnlyPromptDoesNotNeedVerifierRescue(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Can you prepare a refactoring plan for the analysis_agents.go recommendation?", mgr)
	if plan.lane != analysisLaneArchitecture || plan.followupLane == analysisLaneImplement {
		t.Fatalf("refactoring-plan prompt should route directly to read-only architecture, got %#v", plan)
	}
	if !plan.intent.WantsPlanOnly || plan.intent.WantsEdits {
		t.Fatalf("expected plan-only intent without edits, got %#v", plan.intent)
	}
	if plan.taskFamily != "architecture" || plan.permissionProfile != "read_only_workspace" {
		t.Fatalf("expected architecture rails metadata, got %#v", plan)
	}
}

func TestWorkPlanPromptDoesNotRouteToImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Can you propose a work plan for the analysis_agents.go refactor?", mgr)
	if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
		t.Fatalf("work-plan prompt should not route to implementation, got %#v", plan)
	}
	if plan.lane != analysisLaneArchitecture || plan.contract != answerContractArchitecture {
		t.Fatalf("work-plan prompt should route to architecture, got %#v", plan)
	}
	if !plan.intent.WantsPlanOnly || !testContainsFold(plan.intent.Vetoes, "plan_only_blocks_edits") {
		t.Fatalf("expected work-plan prompt to carry plan-only intent veto, got %#v", plan.intent)
	}
}

func TestApproachRefactorPromptDoesNotRouteToImplementation(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	plan := classifyPromptExecutionPlan("Please work out an approach to refactoring the analysis_agents.go file as per your recommendation.", mgr)
	if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
		t.Fatalf("approach-only refactor prompt should not route to implementation, got %#v", plan)
	}
	if plan.lane != analysisLaneArchitecture || plan.contract != answerContractArchitecture {
		t.Fatalf("approach-only refactor prompt should route to architecture, got %#v", plan)
	}
	if !plan.intent.WantsPlanOnly || !testContainsFold(plan.intent.Vetoes, "plan_only_blocks_edits") {
		t.Fatalf("expected structured plan-only intent veto, got %#v", plan.intent)
	}
}

func TestLatestLogAndFixRoutesToDebugThenImplement(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Check the latest log and fix the issue."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	if plan.lane != analysisLaneExplore || plan.followupLane != analysisLaneImplement {
		t.Fatalf("expected latest-log fix prompt to route explore then implement, got %#v", plan)
	}
	if !plan.intent.WantsEdits {
		t.Fatalf("expected edit intent for latest-log fix prompt, got %#v", plan.intent)
	}
	rails := buildDeterministicControlRails(prompt, plan)
	if rails.TaskFamily != "debug_logs" || rails.PermissionProfile != "analysis_read_only_then_guarded_writes" {
		t.Fatalf("expected debug-log rails with implementation handoff, got %#v", rails)
	}
	if !testContainsFold(rails.RequiredTools, "implementation_handoff") {
		t.Fatalf("expected implementation handoff tool requirement, got %#v", rails.RequiredTools)
	}
}

func TestStructuredIntentBoundaryBlocksReleaseActions(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Update README.md, but don't push anything until I review."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	rails := buildDeterministicControlRails(prompt, plan)
	if !testContainsFold(rails.UserBoundaries, "no_push") {
		t.Fatalf("expected no-push boundary in rails, got %#v", rails)
	}
	if rails.Intent == "" || rails.IntentConfidence == "" || rails.Risk == "" {
		t.Fatalf("expected structured intent fields in rails, got %#v", rails)
	}
	blocked := controlRailPreExecute(rails, workspace.ToolRequest{Tool: "shell", Command: "git push origin HEAD"})
	if blocked == nil {
		t.Fatalf("expected no-push boundary to block git push")
	}
}

func TestStructuredIntentCarriesRecentConversationBoundaries(t *testing.T) {
	model := &Model{
		workspace: workspace.NewManager(testWorkspaceConfig()),
		messages: []llama.ChatMessage{{
			Role:    "user",
			Content: "Do not push anything until I review.",
		}},
	}
	plan := model.classifyPromptExecutionPlan("Update README.md with the latest usage note.")
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected implementation request, got %#v", plan)
	}
	if !testContainsFold(plan.intent.UserBoundaries, "no_push") {
		t.Fatalf("expected recent no-push boundary to be carried into intent, got %#v", plan.intent)
	}
	rails := buildDeterministicControlRails("Update README.md with the latest usage note.", plan)
	if blocked := controlRailPreExecute(rails, workspace.ToolRequest{Tool: "shell", Command: "git push origin HEAD"}); blocked == nil {
		t.Fatalf("expected recent no-push boundary to block git push")
	}
}

func TestControlRailPermissionPolicyDeniesDirectConversationShell(t *testing.T) {
	rails := buildDeterministicControlRails("What does the app do?", promptExecutionPlan{
		lane:     analysisLaneMain,
		contract: answerContractForLane(analysisLaneMain),
		intent: promptIntentDecision{
			Intent:     "direct_answer",
			Risk:       "low",
			Confidence: "high",
		},
	})
	policy := controlRailPermissionPolicy(rails)
	decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "go test ./..."})
	if decision.Decision != "deny" {
		t.Fatalf("expected direct conversation shell to be denied by policy, got %#v", decision)
	}
	workspaceDecision := policy.Decide(workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/app/analysis_agents.go"})
	if workspaceDecision.Decision != "deny" {
		t.Fatalf("expected direct conversation workspace inspection to be denied by policy, got %#v", workspaceDecision)
	}
	ledgerDecision := policy.Decide(workspace.ToolRequest{Tool: "task_ledger"})
	if ledgerDecision.Decision != "deny" {
		t.Fatalf("expected direct conversation task ledger to be denied by policy, got %#v", ledgerDecision)
	}
}

func TestIntentVerifierRunsForMixedFailureImplementationPrompt(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected verifier path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"primary_intent\":\"implement\",\"secondary_intents\":[\"debug_logs\"],\"wants_edits\":true,\"wants_plan_only\":false,\"risk\":\"medium\",\"confidence\":\"high\",\"reason\":\"The user asked to fix a file; failure wording is context.\",\"recommended_lane\":\"implement\",\"recommended_followup_lane\":\"\",\"required_boundaries\":[]}"}}]}`))
	}))
	defer server.Close()

	model := &Model{
		cfg:       config.Default(),
		client:    llama.NewClient(server.URL, time.Second),
		workspace: workspace.NewManager(testWorkspaceConfig()),
	}
	prompt := "Please fix internal/app/app.go because it failed."
	plan := model.classifyPromptExecutionPlan(prompt)
	rails := buildDeterministicControlRails(prompt, plan)
	if reasons := promptIntentVerifierReasons(plan, rails); len(reasons) == 0 {
		t.Fatalf("expected mixed failure implementation prompt to trigger verifier; plan=%#v rails=%#v", plan, rails)
	}
	reconciled, ok := model.verifyAndReconcilePromptPlan(context.Background(), prompt, nil, llama.ChatOptions{Model: "test", MaxTokens: 128}, plan, rails)
	if !ok || !called {
		t.Fatalf("expected verifier to run, ok=%t called=%t", ok, called)
	}
	if reconciled.lane != analysisLaneImplement {
		t.Fatalf("failure wording must not downgrade explicit implementation, got %#v", reconciled)
	}
	finalRails := buildDeterministicControlRails(prompt, reconciled)
	if finalRails.TaskFamily != "implementation" {
		t.Fatalf("expected implementation rails after verifier reconciliation, got %#v", finalRails)
	}
}

func TestRouteClassifierUsesConfiguredModelAndDurableTaskMemory(t *testing.T) {
	var requestedModel string
	var taskPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model    string              `json:"model"`
			Messages []llama.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode classifier request: %v", err)
		}
		requestedModel = payload.Model
		if len(payload.Messages) > 1 {
			taskPrompt = payload.Messages[1].Content
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"primary_intent\":\"implement\",\"wants_edits\":true,\"wants_plan_only\":false,\"approval_detected\":true,\"risk\":\"medium\",\"confidence\":\"high\",\"reason\":\"User approved the active durable plan.\",\"recommended_lane\":\"implement\",\"recommended_contract\":\"implementation_result\",\"target_refs\":[\"internal/app/analysis_agents.go\"]}"}}]}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = server.URL
	cfg.RouteClassifier.Model = "tiny-route-9b"
	cfg.RouteClassifier.AlwaysOn = true
	model := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(testWorkspaceConfig()),
		taskMemory: &longHorizonTaskMemory{
			Active:        true,
			Kind:          "durable_plan",
			Prompt:        "Prepare a refactor plan for analysis_agents.go.",
			Lane:          string(analysisLaneArchitecture),
			Summary:       "Durable plan artifact\n\nProposed plan:\n- Target: internal/app/analysis_agents.go",
			ChosenTargets: []string{"internal/app/analysis_agents.go"},
			NextActions:   []string{"wait for explicit approval or a clear plan-exit instruction before implementation"},
		},
	}
	prompt := "Let's commence."
	plan := model.classifyPromptExecutionPlan(prompt)
	if plan.lane != analysisLaneMain {
		t.Fatalf("test setup expected deterministic route to start at main, got %#v", plan)
	}
	reconciled, ok := model.verifyAndReconcilePromptPlan(context.Background(), prompt, nil, llama.ChatOptions{Model: "large-model", MaxTokens: 8192}, plan, buildDeterministicControlRails(prompt, plan))
	if !ok {
		t.Fatal("expected route classifier to run")
	}
	if requestedModel != "tiny-route-9b" {
		t.Fatalf("expected configured route-classifier model, got %q", requestedModel)
	}
	if !strings.Contains(taskPrompt, "Persisted task memory") || !strings.Contains(taskPrompt, "internal/app/analysis_agents.go") {
		t.Fatalf("expected active task memory in classifier prompt, got:\n%s", taskPrompt)
	}
	if reconciled.lane != analysisLaneImplement || reconciled.contract != answerContractImplementation {
		t.Fatalf("expected classifier durable-plan approval to route implement, got %#v", reconciled)
	}
	if !reconciled.intent.WantsEdits {
		t.Fatalf("expected classifier approval to carry edit intent, got %#v", reconciled.intent)
	}
}

func TestRouteClassifierDiscoversModelFromSeparateBaseURL(t *testing.T) {
	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen3.5-9b-q8"}]}`))
		case "/v1/chat/completions":
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode classifier request: %v", err)
			}
			requestedModel = payload.Model
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"primary_intent\":\"direct_answer\",\"wants_edits\":false,\"wants_plan_only\":false,\"risk\":\"low\",\"confidence\":\"high\",\"recommended_lane\":\"main\"}"}}]}`))
		default:
			t.Fatalf("unexpected classifier path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = server.URL
	cfg.RouteClassifier.Model = ""
	cfg.RouteClassifier.AlwaysOn = true
	model := &Model{cfg: cfg, workspace: workspace.NewManager(testWorkspaceConfig())}
	prompt := "What does this app do?"
	plan := model.classifyPromptExecutionPlan(prompt)
	if _, ok := model.verifyAndReconcilePromptPlan(context.Background(), prompt, nil, llama.ChatOptions{Model: "large-model", MaxTokens: 8192}, plan, buildDeterministicControlRails(prompt, plan)); !ok {
		t.Fatal("expected route classifier to run")
	}
	if requestedModel != "qwen3.5-9b-q8" {
		t.Fatalf("expected classifier to use discovered model id, got %q", requestedModel)
	}
}

func TestRouteClassifierHealthFailureIsDebouncedAndRecovers(t *testing.T) {
	model := &Model{
		cfg:    config.Default(),
		events: make(chan tea.Msg, 4),
	}
	model.cfg.RouteClassifier.BaseURL = "http://127.0.0.1:11437"
	model.noteRouteClassifierFailure(context.DeadlineExceeded)
	if !model.routeClassifierOfflineNotified {
		t.Fatal("expected route classifier offline state")
	}
	if len(model.events) != 1 {
		t.Fatalf("expected one debounced health event, got %d", len(model.events))
	}
	model.noteRouteClassifierFailure(context.Canceled)
	if len(model.events) != 1 {
		t.Fatalf("expected repeated classifier failures to be debounced, got %d events", len(model.events))
	}
	model.noteRouteClassifierHealthy()
	if model.routeClassifierOfflineNotified {
		t.Fatal("expected classifier health recovery to clear offline state")
	}
}

func TestRouteClassifierAutoStartsLocalServerOnDemand(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	stub := filepath.Join(dir, "classifier-stub.sh")
	if err := os.WriteFile(stub, []byte(fmt.Sprintf("#!/bin/sh\n/usr/bin/touch %q\n", started)), 0o755); err != nil {
		t.Fatalf("write classifier stub: %v", err)
	}
	modelPath := filepath.Join(dir, "classifier.gguf")
	if err := os.WriteFile(modelPath, []byte("stub model"), 0o644); err != nil {
		t.Fatalf("write classifier model: %v", err)
	}

	modelCalls := 0
	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelCalls++
			if _, err := os.Stat(started); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"local-route-classifier"}]}`))
		case "/v1/chat/completions":
			chatCalls++
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"primary_intent\":\"direct_answer\",\"wants_edits\":false,\"wants_plan_only\":false,\"risk\":\"low\",\"confidence\":\"high\",\"recommended_lane\":\"main\"}"}}]}`))
		default:
			t.Fatalf("unexpected classifier path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = server.URL
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.Binary = stub
	cfg.RouteClassifier.ServerModel = modelPath
	cfg.RouteClassifier.StartTimeoutSeconds = 2
	cfg.RouteClassifier.TimeoutSeconds = 2
	model := &Model{
		cfg:       cfg,
		workspace: workspace.NewManager(testWorkspaceConfig()),
		events:    make(chan tea.Msg, 4),
	}
	defer model.Close()

	prompt := "What does this app do?"
	plan := model.classifyPromptExecutionPlan(prompt)
	if _, ok := model.verifyAndReconcilePromptPlan(context.Background(), prompt, nil, llama.ChatOptions{Model: "large-model", MaxTokens: 8192}, plan, buildDeterministicControlRails(prompt, plan)); !ok {
		t.Fatal("expected route classifier to run after auto-start")
	}
	if !model.managedRouteClassifier {
		t.Fatal("expected route classifier process to be marked managed")
	}
	if chatCalls != 1 {
		t.Fatalf("expected classifier chat call after auto-start, got %d", chatCalls)
	}
}

func TestRouteClassifierAutoStartFailsFastWhenModelPathMissing(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "classifier-stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write classifier stub: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = server.URL
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.Binary = stub
	cfg.RouteClassifier.ServerModel = filepath.Join(dir, "missing.gguf")
	cfg.RouteClassifier.StartTimeoutSeconds = 1
	model := New(cfg)

	err := model.ensureRouteClassifierReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "route classifier model is not readable") {
		t.Fatalf("expected clear missing-model error, got %v", err)
	}
	if model.managedRouteClassifier {
		t.Fatal("missing model must not mark classifier as managed")
	}
}

func TestRouteClassifierAutoStartFailsFastWhenBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "classifier.gguf")
	if err := os.WriteFile(modelPath, []byte("stub model"), 0o644); err != nil {
		t.Fatalf("write classifier model: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = server.URL
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.Binary = filepath.Join(dir, "missing-llama-server")
	cfg.RouteClassifier.ServerModel = modelPath
	cfg.RouteClassifier.StartTimeoutSeconds = 1
	model := New(cfg)

	err := model.ensureRouteClassifierReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "route classifier binary is not readable") {
		t.Fatalf("expected clear missing-binary error, got %v", err)
	}
	if model.managedRouteClassifier {
		t.Fatal("missing binary must not mark classifier as managed")
	}
}

func TestPromptIntentVerifierPromptIsStrictForSmallClassifier(t *testing.T) {
	system := buildPromptIntentVerifierSystemPrompt()
	for _, want := range []string{
		"Return exactly one JSON object",
		"Required JSON shape:",
		"Allowed primary_intent values:",
		"Failure wording inside a fix request is context",
		"Durable-plan approvals",
		`Prompt: "check the latest log and fix it"`,
		`"recommended_lane":"implement"`,
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("expected classifier system prompt to contain %q, got:\n%s", want, system)
		}
	}
	if strings.Contains(strings.ToLower(system), "markdown") && !strings.Contains(system, "no markdown") {
		t.Fatalf("classifier prompt mentions markdown without forbidding it:\n%s", system)
	}

	task := buildPromptIntentVerifierTaskPrompt(
		"check the latest log and fix it",
		[]llama.ChatMessage{{Role: "user", Content: "it failed"}},
		promptExecutionPlan{
			lane:     analysisLaneImplement,
			contract: answerContractImplementation,
			intent:   promptIntentDecision{Intent: "implementation", WantsEdits: true, Confidence: "medium"},
		},
		deterministicControlRailsSnapshot{Lane: "implement", Contract: "implementation_result", TaskFamily: "implementation"},
		[]string{"route_classifier_enabled"},
		"Persisted task memory: none",
	)
	for _, want := range []string{
		"Return exactly one JSON object",
		"Recent conversation excerpt:",
		"Active task memory:",
		"Host route snapshot:",
		"route_classifier_enabled",
	} {
		if !strings.Contains(task, want) {
			t.Fatalf("expected classifier task prompt to contain %q, got:\n%s", want, task)
		}
	}
}

func TestParsePromptIntentVerifierResultToleratesSmallModelShapeDrift(t *testing.T) {
	result, err := parsePromptIntentVerifierResult(`<think>

</think>

{"primary_intent":"implement","secondary_intents":"debug_logs","wants_edits":true,"wants_plan_only":false,"needs_verification":true,"risk":"medium","confidence":"high","recommended_lane":"implement","recommended_contract":"implementation_result","target_refs":"latest log","required_boundaries":"no_push"}`)
	if err != nil {
		t.Fatalf("parse verifier result: %v", err)
	}
	result = normalizePromptIntentVerifierResult(result)
	if result.PrimaryIntent != "implement" || result.RecommendedLane != string(analysisLaneImplement) {
		t.Fatalf("expected implementation classifier result, got %#v", result)
	}
	if !containsString(result.TargetRefs, "latest log") || !containsString(result.SecondaryIntents, "debug_logs") || !containsString(result.RequiredBoundaries, "no_push") {
		t.Fatalf("expected string fields to normalize into slices, got %#v", result)
	}
}

func TestLiveRouteClassifierPromptSmoke(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("QWEN_LIVE_ROUTE_CLASSIFIER_URL"))
	if baseURL == "" {
		t.Skip("set QWEN_LIVE_ROUTE_CLASSIFIER_URL to run live classifier smoke")
	}
	cfg := config.Default()
	cfg.RouteClassifier.BaseURL = baseURL
	cfg.RouteClassifier.AutoStartServer = false
	cfg.RouteClassifier.Model = strings.TrimSpace(os.Getenv("QWEN_LIVE_ROUTE_CLASSIFIER_MODEL"))
	cfg.RouteClassifier.TimeoutSeconds = 60
	model := &Model{cfg: cfg, workspace: workspace.NewManager(testWorkspaceConfig())}
	prompt := "check the latest log and fix it"
	plan := model.classifyPromptExecutionPlan(prompt)
	verifier, err := model.runPromptIntentVerifier(
		context.Background(),
		prompt,
		nil,
		llama.ChatOptions{Model: "large-model", MaxTokens: 8192},
		plan,
		buildDeterministicControlRails(prompt, plan),
		[]string{"route_classifier_enabled", "lane_task_family_conflict"},
	)
	if err != nil {
		t.Fatalf("live classifier verifier failed: %v", err)
	}
	verifier = normalizePromptIntentVerifierResult(verifier)
	if !verifier.WantsEdits || verifier.RecommendedLane != string(analysisLaneImplement) || verifier.RecommendedContract != string(answerContractImplementation) {
		t.Fatalf("expected live classifier to preserve fix intent as implementation, got %#v", verifier)
	}
}

func TestIntentVerifierCannotOverridePlanOnlySafetyVeto(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Can you prepare a refactor plan for internal/app/analysis_agents.go?"
	plan := classifyPromptExecutionPlan(prompt, mgr)
	verifier := promptIntentVerifierResult{
		PrimaryIntent:    "implement",
		WantsEdits:       true,
		WantsPlanOnly:    false,
		Risk:             "medium",
		Confidence:       "high",
		RecommendedLane:  "implement",
		SecondaryIntents: []string{"plan_architecture"},
	}
	reconciled, _, safetyOverride, notes := reconcilePromptPlanWithVerifier(prompt, plan, normalizePromptIntentVerifierResult(verifier))
	if !safetyOverride {
		t.Fatalf("expected safety override for plan-only prompt, notes=%#v", notes)
	}
	if reconciled.lane == analysisLaneImplement || reconciled.followupLane == analysisLaneImplement {
		t.Fatalf("verifier must not override plan-only edit veto, got %#v", reconciled)
	}
	if !testContainsFold(reconciled.intent.Vetoes, "plan_only_blocks_edits") {
		t.Fatalf("expected host plan-only veto to remain, got %#v", reconciled.intent)
	}
}

func TestIntentVerifierDurablePlanApprovalClearsStalePlanOnlyVeto(t *testing.T) {
	memory := &longHorizonTaskMemory{
		Active:      true,
		Kind:        "durable_plan",
		Lane:        string(analysisLaneArchitecture),
		Prompt:      "Can you prepare a refactor plan for internal/app/analysis_agents.go?",
		NextActions: []string{"wait for explicit approval or a clear plan-exit instruction before implementation"},
	}
	prompt := buildTaskMemoryResumePrompt(memory, "Please begin")
	plan := promptExecutionPlan{
		lane:     analysisLaneImplement,
		reason:   promptRouteReasonDirectImplementation,
		contract: answerContractImplementation,
		intent: promptIntentDecision{
			Intent:        "implement",
			SelectedLane:  string(analysisLaneImplement),
			WantsEdits:    true,
			WantsPlanOnly: true,
			Vetoes:        []string{"plan_only_blocks_edits", "read_only_boundary_blocks_edits"},
		},
	}
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:    "implement",
		WantsEdits:       true,
		WantsPlanOnly:    false,
		ApprovalDetected: true,
		Risk:             "medium",
		Confidence:       "high",
		RecommendedLane:  string(analysisLaneImplement),
	})
	reconciled, changed, safetyOverride, notes := reconcilePromptPlanWithVerifier(prompt, plan, verifier, memory)
	if !changed {
		t.Fatalf("expected durable approval reconciliation to change stale intent, notes=%#v", notes)
	}
	if safetyOverride {
		t.Fatalf("durable plan approval should clear stale edit veto, notes=%#v", notes)
	}
	if reconciled.lane != analysisLaneImplement || reconciled.contract != answerContractImplementation {
		t.Fatalf("expected implementation route after approval, got %#v notes=%#v", reconciled, notes)
	}
	if !reconciled.intent.WantsEdits || reconciled.intent.WantsPlanOnly {
		t.Fatalf("expected edits allowed and plan-only cleared, got %#v notes=%#v", reconciled.intent, notes)
	}
	if testContainsFold(reconciled.intent.Vetoes, "plan_only_blocks_edits") || testContainsFold(reconciled.intent.Vetoes, "read_only_boundary_blocks_edits") {
		t.Fatalf("expected stale plan/read-only vetoes removed, got %#v", reconciled.intent.Vetoes)
	}
}

func TestIntentVerifierPrimaryIntentCanUpgradeMainPlanToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Please work out an approach for the routing decision tree."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	if plan.lane != analysisLaneMain || !plan.intent.WantsPlanOnly {
		t.Fatalf("test setup expected a main-lane plan-only host route, got %#v", plan)
	}
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:   "plan_architecture",
		WantsEdits:      false,
		WantsPlanOnly:   true,
		Risk:            "low",
		Confidence:      "high",
		RecommendedLane: "main",
	})
	reconciled, changed, safetyOverride, notes := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if !changed {
		t.Fatalf("expected verifier primary intent to change the route, notes=%#v", notes)
	}
	if safetyOverride {
		t.Fatalf("read-only architecture upgrade should not be a safety override, notes=%#v", notes)
	}
	if reconciled.lane != analysisLaneArchitecture || reconciled.contract != answerContractArchitecture {
		t.Fatalf("expected architecture route from verifier primary intent, got %#v notes=%#v", reconciled, notes)
	}
	if reconciled.followupLane == analysisLaneImplement || reconciled.intent.WantsEdits {
		t.Fatalf("plan-only verifier route must remain read-only, got %#v", reconciled)
	}
}

func TestIntentVerifierPreservesHostArchitectureWhenClassifierUsesExploreIntent(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "can you recommend some parts of the codebase we can architecturally optimize for long-term maintainability"
	plan := classifyPromptExecutionPlan(prompt, mgr)
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("test setup expected host architecture route, got %#v", plan)
	}
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:     "explore_codebase",
		SecondaryIntents:  []string{"plan_architecture"},
		WantsEdits:        false,
		WantsPlanOnly:     false,
		Risk:              "low",
		Confidence:        "high",
		RecommendedLane:   "explore",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
	})
	reconciled, changed, safetyOverride, notes := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if safetyOverride {
		t.Fatalf("architecture preservation should not be a safety override, notes=%#v", notes)
	}
	if !changed {
		t.Fatalf("expected intent metadata to be reconciled, notes=%#v", notes)
	}
	if reconciled.lane != analysisLaneArchitecture || reconciled.contract != answerContractArchitecture {
		t.Fatalf("expected host architecture lane to be preserved, got %#v notes=%#v", reconciled, notes)
	}
	if reconciled.intent.Intent != "plan_architecture" {
		t.Fatalf("expected architecture intent after preserving lane, got %#v notes=%#v", reconciled.intent, notes)
	}
	if !strings.Contains(strings.ToLower(strings.Join(notes, "\n")), "preserved host architecture lane") {
		t.Fatalf("expected preservation note, got %#v", notes)
	}
}

func TestArchitectureControlRailsRejectBudgetFailureWithoutRecommendations(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "can you recommend some parts of the codebase we can architecturally optimize for long-term maintainability"
	plan := classifyPromptExecutionPlan(prompt, mgr)
	rails := buildDeterministicControlRails(prompt, plan)
	answer := "Exploration did not complete cleanly.\n\nFinal blocker:\n- repeated dead-end action rejected."
	enforcement := controlRailsEnforcementForAnswer(rails, answer, []string{"internal/app/app.go"}, nil, false)
	if enforcement.Passed {
		t.Fatalf("expected architecture rails to reject blocker-only answer, got %#v", enforcement)
	}
	if !testContainsFold(enforcement.Violations, "architecture_recommendations_missing_after_budget_failure") {
		t.Fatalf("expected architecture recommendation violation, got %#v", enforcement)
	}
}

func TestArchitectureControlRailsRejectTargetDrift(t *testing.T) {
	rails := deterministicControlRailsSnapshot{
		Lane:       string(analysisLaneArchitecture),
		Contract:   string(answerContractArchitecture),
		TaskFamily: "architecture",
		TargetRefs: []string{"internal/workspace/manager.go"},
	}
	answer := "Architecture recommendation: split internal/app/app.go into a smaller router and planner."
	enforcement := controlRailsEnforcementForAnswer(rails, answer, []string{"internal/app/app.go"}, nil, false)
	if enforcement.Passed {
		t.Fatalf("expected target drift to fail control rails, got %#v", enforcement)
	}
	if !testContainsFold(enforcement.Violations, "target_ref_missing_from_evidence_or_answer:internal/workspace/manager.go") {
		t.Fatalf("expected missing target-ref violation, got %#v", enforcement.Violations)
	}
}

func TestIntentVerifierCanAddUserBoundary(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Update README.md with the latest usage note, but don't push it."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:      "implement",
		WantsEdits:         true,
		Risk:               "medium",
		Confidence:         "high",
		RecommendedLane:    "implement",
		RequiredBoundaries: []string{"do_not_push"},
	})
	reconciled, changed, _, _ := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if !changed {
		t.Fatalf("expected verifier boundary to change plan intent")
	}
	if !testContainsFold(reconciled.intent.UserBoundaries, "no_push") {
		t.Fatalf("expected verifier no-push boundary, got %#v", reconciled.intent)
	}
	rails := buildDeterministicControlRails(prompt, reconciled)
	if blocked := controlRailPreExecute(rails, workspace.ToolRequest{Tool: "shell", Command: "git push origin HEAD"}); blocked == nil {
		t.Fatalf("expected verifier-added no-push boundary to block git push")
	}
}

func TestIntentVerifierTargetRefsAreRetainedForFollowupPlanning(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Can you prepare a refactor plan for this file?"
	plan := classifyPromptExecutionPlan(prompt, mgr)
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:       "plan_architecture",
		WantsPlanOnly:       true,
		Risk:                "medium",
		Confidence:          "high",
		RecommendedLane:     string(analysisLaneArchitecture),
		RecommendedContract: string(answerContractArchitecture),
		TaskFamily:          "architecture",
		TargetRefs:          []string{"internal/workspace/manager.go"},
	})
	reconciled, changed, _, _ := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if !changed {
		t.Fatalf("expected target-ref reconciliation to be marked changed")
	}
	if reconciled.lane != analysisLaneArchitecture {
		t.Fatalf("expected architecture lane, got %#v", reconciled)
	}
	if !testContainsFold(reconciled.intent.TargetRefs, "internal/workspace/manager.go") {
		t.Fatalf("expected verifier target ref to be retained, got %#v", reconciled.intent.TargetRefs)
	}
	rails := buildDeterministicControlRails(prompt, reconciled)
	if !testContainsFold(rails.TargetRefs, "internal/workspace/manager.go") {
		t.Fatalf("expected target ref in control rails, got %#v", rails.TargetRefs)
	}
}

func TestIntentVerifierTargetRefsIgnoredForOpenEndedOpportunityDiscovery(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Explore this Go codebase and identify one bounded optimization/refactor opportunity that can be implemented by editing only one existing git-clean Go file. Avoid files already modified in git status. Prepare a concrete refactor plan only; do not edit files yet."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:       "plan_architecture",
		WantsPlanOnly:       true,
		Risk:                "low",
		Confidence:          "high",
		RecommendedLane:     string(analysisLaneArchitecture),
		RecommendedContract: string(answerContractArchitecture),
		TaskFamily:          "architecture",
		TargetRefs:          []string{"internal/app/implementation.go", "internal/agent/runner.go"},
	})
	reconciled, _, _, notes := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if len(reconciled.intent.TargetRefs) != 0 {
		t.Fatalf("expected open-ended opportunity prompt to ignore verifier target refs, got %#v", reconciled.intent.TargetRefs)
	}
	if !testContainsFold(notes, "ignored verifier target refs for open-ended opportunity discovery") {
		t.Fatalf("expected ignored-target note, got %#v", notes)
	}
}

func TestIntentVerifierDoesNotEraseHostEditIntent(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Update README.md with the latest usage note."
	plan := classifyPromptExecutionPlan(prompt, mgr)
	if !plan.intent.WantsEdits {
		t.Fatalf("expected host route to carry edit intent, got %#v", plan)
	}
	verifier := normalizePromptIntentVerifierResult(promptIntentVerifierResult{
		PrimaryIntent:   "implement",
		WantsEdits:      false,
		Risk:            "medium",
		Confidence:      "high",
		RecommendedLane: "implement",
	})
	reconciled, _, _, _ := reconcilePromptPlanWithVerifier(prompt, plan, verifier)
	if !reconciled.intent.WantsEdits {
		t.Fatalf("verifier under-reporting must not erase host edit intent, got %#v", reconciled)
	}
	if reconciled.taskFamily != "implementation" || reconciled.permissionProfile != "guarded_workspace_writes" {
		t.Fatalf("expected canonical implementation metadata, got %#v", reconciled)
	}
}

func TestIntentVerifierEditIntentOnlyChangeIsRetained(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "What does the app do?"
	plan := classifyPromptExecutionPlan(prompt, mgr)
	if plan.intent.WantsEdits {
		t.Fatalf("test setup expected no host edit intent, got %#v", plan)
	}
	reconciled, changed, _, _ := reconcilePromptPlanWithVerifier(prompt, plan, promptIntentVerifierResult{
		WantsEdits: true,
	})
	if !changed {
		t.Fatalf("expected edit-intent-only reconciliation to be marked changed")
	}
	if !reconciled.intent.WantsEdits {
		t.Fatalf("expected verifier edit intent to be retained, got %#v", reconciled.intent)
	}
}

func TestControlRailsFinalAnswerRepairsMissingVerificationContract(t *testing.T) {
	rails := buildDeterministicControlRails("Fix internal/app/app.go", promptExecutionPlan{
		lane:     analysisLaneImplement,
		contract: answerContractForLane(analysisLaneImplement),
	})
	enforcement := controlRailsEnforcementForAnswer(rails, "Changed files:\n- internal/app/app.go", nil, []string{"internal/app/app.go"}, false)
	if enforcement.Passed {
		t.Fatalf("expected missing verification contract to fail rails")
	}
	if !testContainsFold(enforcement.Violations, "verification_contract_unresolved") {
		t.Fatalf("expected verification violation, got %#v", enforcement.Violations)
	}
	repaired := repairControlRailsAnswer("Changed files:\n- internal/app/app.go", enforcement)
	if !strings.Contains(repaired, "Control-rail gaps:") || !strings.Contains(repaired, "verification_contract_unresolved") {
		t.Fatalf("expected repaired control-rail answer, got %q", repaired)
	}
}

func TestControlRailsAcceptsNoFileEditNoopEvidence(t *testing.T) {
	rails := buildDeterministicControlRails("Fix internal/app/app.go", promptExecutionPlan{
		lane:     analysisLaneImplement,
		contract: answerContractForLane(analysisLaneImplement),
	})
	answer := "No file edits were needed: the requested change is already present.\n\nVerification:\n- `go test ./...`: passed"
	enforcement := controlRailsEnforcementForAnswer(rails, answer, nil, nil, true)
	if !enforcement.Passed {
		t.Fatalf("expected no-op implementation evidence to pass rails, got %#v", enforcement)
	}
}

func TestNormalizePromptPlanThoroughnessDefaultsAnalysisLanes(t *testing.T) {
	model := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}
	for _, lane := range []analysisLane{analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture} {
		plan := model.normalizePromptPlanThoroughness(promptExecutionPlan{lane: lane})
		if plan.thoroughness != workspace.ThoroughnessMedium {
			t.Fatalf("expected medium thoroughness for lane %q, got %q", lane, plan.thoroughness)
		}
	}
	plan := model.normalizePromptPlanThoroughness(promptExecutionPlan{lane: analysisLaneImplement})
	if plan.thoroughness != "" {
		t.Fatalf("implementation lane should not get thoroughness, got %q", plan.thoroughness)
	}
}

func TestRepresentativeSWEPromptSuiteRoutesTypicalTasks(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	for _, tc := range representativeSWEPromptSuite() {
		t.Run(tc.name, func(t *testing.T) {
			plan := classifyPromptExecutionPlan(tc.prompt, mgr)
			if plan.lane != tc.lane {
				t.Fatalf("expected lane %q, got %q for prompt %q", tc.lane, plan.lane, tc.prompt)
			}
			if plan.followupLane != tc.followupLane {
				t.Fatalf("expected follow-up lane %q, got %q for prompt %q", tc.followupLane, plan.followupLane, tc.prompt)
			}
			if plan.reason != tc.reason {
				t.Fatalf("expected reason %q, got %q for prompt %q", tc.reason, plan.reason, tc.prompt)
			}
			if plan.contract != tc.contract {
				t.Fatalf("expected contract %q, got %q for prompt %q", tc.contract, plan.contract, tc.prompt)
			}
			if tc.thoroughness != "" && plan.thoroughness != tc.thoroughness {
				t.Fatalf("expected thoroughness %q, got %q for prompt %q", tc.thoroughness, plan.thoroughness, tc.prompt)
			}
		})
	}
}

func TestRoutePlansMaintainPolicyInvariants(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompts := []string{
		"What does the debug log redacted mode do?",
		"Check the latest logs and explain why this failed.",
		"Check the latest log and fix the issue.",
		"Run the Go test suite and report any failures.",
		"Prepare a plan to update internal/app/analysis_agents.go.",
		"Can you prepare a refactor plan for internal/app/analysis_agents.go?",
		"Please work out an approach to refactoring the analysis_agents.go file as per your recommendation.",
		"Update README.md with the latest usage note, but don't push it.",
		"Fix internal/app/app.go because it failed.",
		"Analyze this codebase architecture and package boundaries.",
	}
	for _, prompt := range prompts {
		t.Run(prompt, func(t *testing.T) {
			plan := classifyPromptExecutionPlan(prompt, mgr)
			if plan.contract != answerContractForLane(plan.lane) {
				t.Fatalf("contract drift: lane=%q contract=%q expected=%q plan=%#v", plan.lane, plan.contract, answerContractForLane(plan.lane), plan)
			}
			if plan.intent.SelectedLane != string(plan.lane) {
				t.Fatalf("intent selected lane drift: prompt=%q plan=%#v", prompt, plan)
			}
			if plan.intent.FollowupLane != string(plan.followupLane) {
				t.Fatalf("intent follow-up lane drift: prompt=%q plan=%#v", prompt, plan)
			}
			if (plan.intent.WantsPlanOnly || plan.intent.WantsVerify) && (plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement) {
				t.Fatalf("read-only intent must not carry implementation route: prompt=%q plan=%#v", prompt, plan)
			}
			rails := buildDeterministicControlRails(prompt, plan)
			if rails.Lane != string(plan.lane) || rails.Contract != string(plan.contract) {
				t.Fatalf("control rails drift from route plan: prompt=%q rails=%#v plan=%#v", prompt, rails, plan)
			}
			if plan.taskFamily == "" || rails.TaskFamily != plan.taskFamily {
				t.Fatalf("task family drift: prompt=%q rails=%#v plan=%#v", prompt, rails, plan)
			}
			if plan.permissionProfile == "" || rails.PermissionProfile != plan.permissionProfile {
				t.Fatalf("permission profile drift: prompt=%q rails=%#v plan=%#v", prompt, rails, plan)
			}
		})
	}
}
