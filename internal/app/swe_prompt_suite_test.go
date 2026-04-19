package app

import (
	"strings"
	"testing"

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
