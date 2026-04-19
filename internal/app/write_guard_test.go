package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

func testWorkspaceConfig() config.Workspace {
	return config.Workspace{
		Root:                  "../..",
		CWD:                   "../..",
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	}
}

func TestClassifyPromptExecutionPlanRoutesShellOnlyCreateToImplement(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Create a new file shell-note.txt containing the text hello by using shell commands only.",
		mgr,
	)
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected implement lane, got %q", plan.lane)
	}
	if plan.reason != promptRouteReasonDirectImplementation {
		t.Fatalf("expected direct implement route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesLocateThenImplementForMixedPrompt(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Can we identify what renders the \"working...\" text on the top banner of the bubbletea harness and remove it?",
		mgr,
	)
	if plan.lane != analysisLaneLocate {
		t.Fatalf("expected locate lane, got %q", plan.lane)
	}
	if plan.followupLane != analysisLaneImplement {
		t.Fatal("expected mixed locate/edit prompt to continue into implement lane")
	}
	if plan.reason != promptRouteReasonLocateRequest {
		t.Fatalf("expected locate route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesUILiteralRemovalThroughLocate(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		`please remove the "waiting..." text from the banner`,
		mgr,
	)
	if plan.lane != analysisLaneLocate {
		t.Fatalf("expected locate lane, got %q", plan.lane)
	}
	if plan.followupLane != analysisLaneImplement {
		t.Fatal("expected UI literal removal prompt to continue into implement lane")
	}
}

func TestClassifyPromptExecutionPlanRoutesArchitectureQuestionToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"How does this harness support Golang? Is it extensible to support other languages in the same way?",
		mgr,
	)
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected architecture lane, got %q", plan.lane)
	}
	if plan.followupLane != "" {
		t.Fatalf("expected no follow-up lane, got %q", plan.followupLane)
	}
	if plan.reason != promptRouteReasonArchitectureRequest {
		t.Fatalf("expected architecture route reason, got %q", plan.reason)
	}
	if plan.contract != answerContractArchitecture {
		t.Fatalf("expected architecture answer contract, got %q", plan.contract)
	}
}

func TestClassifyPromptExecutionPlanRoutesLanguageSupportQuestionsToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	cases := []string{
		"Which languages does this harness support, and how extensible is that architecture?",
		"In this project, how are C++ and Go language capabilities routed through the harness?",
		"How should prompt and docs capability support be organized in this repo?",
	}
	for _, prompt := range cases {
		plan := classifyPromptExecutionPlan(prompt, mgr)
		if plan.lane != analysisLaneArchitecture {
			t.Fatalf("expected architecture lane for %q, got %q", prompt, plan.lane)
		}
		if plan.reason != promptRouteReasonArchitectureRequest {
			t.Fatalf("expected architecture reason for %q, got %q", prompt, plan.reason)
		}
		if plan.contract != answerContractArchitecture {
			t.Fatalf("expected architecture contract for %q, got %q", prompt, plan.contract)
		}
	}
}

func TestClassifyPromptExecutionPlanRoutesStructureOptimizationPromptToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Please identify any optimizations we can make to this codebase's structure and boundaries.",
		mgr,
	)
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected architecture lane, got %q", plan.lane)
	}
	if plan.reason != promptRouteReasonArchitectureRequest {
		t.Fatalf("expected architecture route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesArchitecturalImprovementsToArchitecture(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Can you identify architectural improvements we can make?",
		mgr,
	)
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected architecture lane, got %q", plan.lane)
	}
	if plan.reason != promptRouteReasonArchitectureRequest {
		t.Fatalf("expected architecture route reason, got %q", plan.reason)
	}
	if plan.contract != answerContractArchitecture {
		t.Fatalf("expected architecture answer contract, got %q", plan.contract)
	}
}

func TestClassifyPromptExecutionPlanRoutesArchitectureThenImplementPrompt(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Analyze this codebase's structure, identify the best package boundary split, and then implement it.",
		mgr,
	)
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("expected architecture lane, got %q", plan.lane)
	}
	if plan.followupLane != analysisLaneImplement {
		t.Fatalf("expected implement follow-up, got %q", plan.followupLane)
	}
	if plan.reason != promptRouteReasonArchitectureRequest {
		t.Fatalf("expected architecture route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesReviewPromptToReviewLane(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Please review this harness for bugs and regressions.",
		mgr,
	)
	if plan.lane != analysisLaneReview {
		t.Fatalf("expected review lane, got %q", plan.lane)
	}
	if plan.reason != promptRouteReasonReviewRequest {
		t.Fatalf("expected review route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesCleanupQuestionToReviewLane(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"what can we clean up about this codebase?",
		mgr,
	)
	if plan.lane != analysisLaneReview {
		t.Fatalf("expected review lane, got %q", plan.lane)
	}
	if plan.followupLane != "" {
		t.Fatalf("expected no follow-up lane, got %q", plan.followupLane)
	}
	if plan.reason != promptRouteReasonReviewRequest {
		t.Fatalf("expected review route reason, got %q", plan.reason)
	}
}

func TestClassifyPromptExecutionPlanRoutesPlainQuestionToMain(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"What is the default request timeout in this app?",
		mgr,
	)
	if plan.lane != analysisLaneMain {
		t.Fatalf("expected main lane, got %q", plan.lane)
	}
	if plan.followupLane != "" {
		t.Fatalf("expected no follow-up lane, got %q", plan.followupLane)
	}
	if plan.reason != promptRouteReasonDirectConversation {
		t.Fatalf("expected direct conversation route reason, got %q", plan.reason)
	}
}

func TestBuildAnalysisTaskMemoryCapturesEvidenceAndGaps(t *testing.T) {
	memory := buildAnalysisTaskMemory(
		"Review routing and continue if needed.",
		analysisLaneReview,
		"Inspected internal/app/app.go but no bounded actionable finding was confirmed; not enough evidence to edit.",
		[]string{"internal/app/app.go"},
		"resume review with narrower evidence",
	)
	if !taskMemoryResumable(memory) {
		t.Fatalf("expected analysis memory to be resumable: %#v", memory)
	}
	if memory.Kind != "investigation" || memory.Lane != string(analysisLaneReview) {
		t.Fatalf("unexpected memory identity: %#v", memory)
	}
	if !testContainsFold(memory.ChosenTargets, "internal/app/app.go") {
		t.Fatalf("expected chosen target, got %#v", memory.ChosenTargets)
	}
	if !testContainsFold(memory.AttemptedSearches, "validated target internal/app/app.go") {
		t.Fatalf("expected attempted evidence, got %#v", memory.AttemptedSearches)
	}
	if !testContainsFold(memory.OpenGaps, "no bounded actionable finding was confirmed") {
		t.Fatalf("expected review open gap, got %#v", memory.OpenGaps)
	}
}

func TestBuildRepairTaskMemoryCapturesFailedVerification(t *testing.T) {
	verify := implementationVerification{
		failed:           true,
		commands:         []string{"go test ./internal/app"},
		expectedFailures: []string{"routing regressions"},
		results: []workspace.ToolResult{{
			Tool:  "shell",
			Error: "exit status 1",
		}},
	}
	memory := buildRepairTaskMemory(
		"Fix routing.",
		[]string{"internal/app/app.go"},
		verify,
		implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}},
	)
	if memory.Kind != "partial_repair" || memory.Lane != string(analysisLaneImplement) {
		t.Fatalf("unexpected repair memory identity: %#v", memory)
	}
	if !testContainsPrefixFold(memory.VerificationHistory, "go test ./internal/app -> failed") {
		t.Fatalf("expected failed verification history, got %#v", memory.VerificationHistory)
	}
	if !testContainsFold(memory.NextActions, "repair failing verification before widening implementation scope") {
		t.Fatalf("expected repair next action, got %#v", memory.NextActions)
	}
}

func TestTaskMemoryPersistenceInvalidatesStaleTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	saveDir := filepath.Join(root, "transcripts")
	m := &Model{
		cfg: config.Config{
			Chat: config.Chat{SaveDir: saveDir},
			Workspace: config.Workspace{
				Root: root,
				CWD:  root,
			},
		},
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	memory := &longHorizonTaskMemory{
		Active:        true,
		Kind:          "investigation",
		Prompt:        "Continue target investigation",
		Lane:          string(analysisLaneExplore),
		ChosenTargets: []string{"target.go"},
		NextActions:   []string{"resume from target.go"},
	}
	m.rememberTaskMemory(memory, "test persist")
	if m.taskMemory == nil {
		t.Fatal("expected task memory to persist in model")
	}
	reloaded := &Model{
		cfg:       m.cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	reloaded.loadPersistedTaskMemory()
	if reloaded.taskMemory == nil || !testContainsFold(reloaded.taskMemory.ChosenTargets, "target.go") {
		t.Fatalf("expected persisted task memory to reload, got %#v", reloaded.taskMemory)
	}
	if err := os.WriteFile(target, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("modify target: %v", err)
	}
	stale := &Model{
		cfg:       m.cfg,
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	stale.loadPersistedTaskMemory()
	if stale.taskMemory != nil {
		t.Fatalf("expected stale task memory to be cleared, got %#v", stale.taskMemory)
	}
}

func TestBuildTaskMemoryResumePromptAvoidsRepeatingBroadSearch(t *testing.T) {
	prompt := buildTaskMemoryResumePrompt(&longHorizonTaskMemory{
		Active:            true,
		Kind:              "investigation",
		Prompt:            "Find the routing bug.",
		Lane:              string(analysisLaneReview),
		OpenGaps:          []string{"confirm candidate path"},
		AttemptedSearches: []string{"validated target internal/app/app.go"},
		ChosenTargets:     []string{"internal/app/app.go"},
		NextActions:       []string{"read the focused function"},
	}, "continue")
	for _, want := range []string{"Original request: Find the routing bug.", "Already attempted:", "Open evidence gaps:", "Do not repeat broad search"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected resume prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestFrontierControllerSnapshots(t *testing.T) {
	plan := promptExecutionPlan{
		lane:   analysisLaneArchitecture,
		reason: promptRouteReasonArchitectureRequest,
	}
	ambiguity := buildRouteAmbiguitySnapshot("Analyze the architecture and then implement the best refactor.", plan)
	if !ambiguity.Ambiguous || !testContainsFold(ambiguity.Signals, "architecture") || !testContainsFold(ambiguity.Signals, "implement") {
		t.Fatalf("expected ambiguous architecture/implement route, got %#v", ambiguity)
	}

	delta := evidenceDeltaPaths([]string{"internal/app/app.go", "internal/eval/eval.go"}, []string{"internal/app/app.go"})
	if !testContainsFold(delta, "internal/eval/eval.go") || evidenceMarginalValue(delta, 0, 1) != "medium" {
		t.Fatalf("unexpected evidence delta: paths=%#v value=%s", delta, evidenceMarginalValue(delta, 0, 1))
	}

	readiness := analysisReadinessAssessment{
		Lane:          string(analysisLaneReview),
		Ready:         false,
		Confidence:    "low",
		Reason:        "response explicitly reports insufficient evidence",
		EvidencePaths: []string{"internal/app/app.go"},
	}
	claim := claimConfidenceFromReadiness(readiness)
	if claim.Confidence != "low" || len(claim.UnresolvedUncertainty) == 0 {
		t.Fatalf("expected unresolved low-confidence claim snapshot, got %#v", claim)
	}
}

func TestAnalysisFinalAnswerCriticCoversNonImplementationContracts(t *testing.T) {
	locate := critiqueAnalysisFinalAnswer("I found it.", analysisLaneLocate, nil)
	if locate.Passed {
		t.Fatalf("expected locate critic to reject pathless answer: %#v", locate)
	}
	architecture := critiqueAnalysisFinalAnswer("Architecture: internal/app/app.go is the hotspot for package boundary work.", analysisLaneArchitecture, []string{"internal/app/app.go"})
	if !architecture.Passed {
		t.Fatalf("expected grounded architecture answer to pass, got %#v", architecture)
	}
}

func TestVerificationFeedbackFromResult(t *testing.T) {
	plan := verificationPlan{
		Commands:   []string{"go test ./internal/app"},
		Coverage:   "targeted test verification",
		Confidence: "medium",
		RiskLevel:  "high",
	}
	verify := implementationVerification{
		failed:   true,
		commands: []string{"go test ./internal/app"},
		results: []workspace.ToolResult{{
			Tool:  "shell",
			Error: "exit status 1",
		}},
	}
	feedback := verificationFeedbackFromResult(plan, verify)
	if !testContainsFold(feedback.FailedCommands, "go test ./internal/app") || !strings.Contains(feedback.LearnedRecommendation, "repair") {
		t.Fatalf("expected failed verification feedback, got %#v", feedback)
	}
}

func TestClassifyPromptExecutionPlanAssignsAnswerContractsForEveryLane(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	cases := []struct {
		name     string
		prompt   string
		lane     analysisLane
		contract answerContract
	}{
		{
			name:     "main",
			prompt:   "What is the default request timeout in this app?",
			lane:     analysisLaneMain,
			contract: answerContractDirectConversation,
		},
		{
			name:     "locate",
			prompt:   `Find where the "working..." banner is rendered.`,
			lane:     analysisLaneLocate,
			contract: answerContractLocateTargets,
		},
		{
			name:     "explore",
			prompt:   "Explore the codebase routing flow.",
			lane:     analysisLaneExplore,
			contract: answerContractGroundedExplanation,
		},
		{
			name:     "review",
			prompt:   "Review the codebase for regressions.",
			lane:     analysisLaneReview,
			contract: answerContractReviewFindings,
		},
		{
			name:     "architecture",
			prompt:   "Analyze this codebase architecture and package boundaries.",
			lane:     analysisLaneArchitecture,
			contract: answerContractArchitecture,
		},
		{
			name:     "implement",
			prompt:   "Create a new file notes.txt containing hello.",
			lane:     analysisLaneImplement,
			contract: answerContractImplementation,
		},
	}
	for _, tc := range cases {
		plan := classifyPromptExecutionPlan(tc.prompt, mgr)
		if plan.lane != tc.lane {
			t.Fatalf("%s: expected lane %q, got %q", tc.name, tc.lane, plan.lane)
		}
		if plan.contract != tc.contract {
			t.Fatalf("%s: expected contract %q, got %q", tc.name, tc.contract, plan.contract)
		}
	}
}

func TestAnalysisTransitionAllowsImplementAfterExplore(t *testing.T) {
	plan := promptExecutionPlan{
		lane:         analysisLaneExplore,
		followupLane: analysisLaneImplement,
		reason:       promptRouteReasonExploreRequest,
	}
	next, ok := analysisTransition(plan, analysisLaneExplore)
	if !ok {
		t.Fatal("expected explore plan to transition")
	}
	if next != analysisLaneImplement {
		t.Fatalf("expected implement follow-up, got %q", next)
	}
}

func TestAnalysisTransitionRejectsMismatchedCompletedLane(t *testing.T) {
	plan := promptExecutionPlan{
		lane:         analysisLaneReview,
		followupLane: analysisLaneImplement,
		reason:       promptRouteReasonReviewRequest,
	}
	if next, ok := analysisTransition(plan, analysisLaneExplore); ok || next != "" {
		t.Fatalf("expected mismatched lane transition to be rejected, got next=%q ok=%v", next, ok)
	}
}

func TestAnalysisTransitionAllowsImplementAfterArchitecture(t *testing.T) {
	plan := promptExecutionPlan{
		lane:         analysisLaneArchitecture,
		followupLane: analysisLaneImplement,
		reason:       promptRouteReasonArchitectureRequest,
	}
	next, ok := analysisTransition(plan, analysisLaneArchitecture)
	if !ok {
		t.Fatal("expected architecture plan to transition")
	}
	if next != analysisLaneImplement {
		t.Fatalf("expected implement follow-up, got %q", next)
	}
}

func TestAnalysisReplanRecommendationDetectsStructuralReviewOutput(t *testing.T) {
	summary := "Top structural opportunity: internal/app/app.go has broad orchestration responsibilities and should be split across clearer boundaries."
	target, reason, ok := analysisReplanRecommendation(analysisLaneReview, summary)
	if !ok {
		t.Fatal("expected replan recommendation")
	}
	if target != analysisLaneArchitecture {
		t.Fatalf("expected architecture replan target, got %q", target)
	}
	if !strings.Contains(reason, "structural evidence") {
		t.Fatalf("unexpected replan reason: %q", reason)
	}
}

func TestAnalysisReplanRecommendationRequiresGroundedPath(t *testing.T) {
	summary := "Top structural opportunity: the app has broad orchestration responsibilities and should be split."
	if target, reason, ok := analysisReplanRecommendation(analysisLaneReview, summary); ok || target != "" || reason != "" {
		t.Fatalf("expected ungrounded replan to be rejected, got target=%q reason=%q ok=%v", target, reason, ok)
	}
}

func TestAnalysisReplanRecommendationUsesEvidenceLedgerPaths(t *testing.T) {
	summary := "Top structural opportunity: broad orchestration responsibilities should be split across clearer boundaries."
	target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneReview, summary, []string{"internal/app/app.go"})
	if !ok {
		t.Fatal("expected evidence-grounded replan recommendation")
	}
	if target != analysisLaneArchitecture {
		t.Fatalf("expected architecture replan target, got %q", target)
	}
	if !strings.Contains(reason, "structural evidence") {
		t.Fatalf("unexpected replan reason: %q", reason)
	}
}

func TestAppendAnalysisReplanRecommendation(t *testing.T) {
	summary := appendAnalysisReplanRecommendation("Reviewed evidence.", analysisLaneArchitecture, "review produced structural evidence")
	for _, want := range []string{"Reviewed evidence.", "Replan recommendation:", "next lane: architecture", "review produced structural evidence"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected replan summary to include %q, got:\n%s", want, summary)
		}
	}
}

func TestBuildReplanPromptCarriesPriorEvidence(t *testing.T) {
	prompt := buildReplanPrompt("review the codebase", analysisReplanRecommendationState{
		FromLane: analysisLaneReview,
		ToLane:   analysisLaneArchitecture,
		Reason:   "review produced structural evidence",
		Summary:  "internal/app/app.go has broad responsibilities",
	})
	for _, want := range []string{"Execute the prior replan recommendation", "Original request: review the codebase", "Recommended lane: architecture", "internal/app/app.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected replan prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestDefaultExecutionPlanSummaryUsesArchitectureCopy(t *testing.T) {
	plan := promptExecutionPlan{
		lane:   analysisLaneArchitecture,
		reason: promptRouteReasonArchitectureRequest,
	}
	if got := defaultExecutionPlanSummary(plan); !strings.Contains(strings.ToLower(got), "architecture hotspots") {
		t.Fatalf("expected architecture plan summary, got %q", got)
	}
}

func TestFormatPromptExecutionPlanIncludesAnswerContract(t *testing.T) {
	plan := promptExecutionPlan{
		lane:   analysisLaneReview,
		reason: promptRouteReasonReviewRequest,
	}
	got := formatPromptExecutionPlan(plan)
	for _, want := range []string{"initial=review", "reason=review_request", "contract=review_findings"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected formatted route plan to include %q, got %q", want, got)
		}
	}
}

func TestRenderLaneContractPolicyIncludesExpectedAndForbiddenRules(t *testing.T) {
	policy := renderLaneContractPolicy(analysisLaneExplore)
	for _, want := range []string{"Answer contract: grounded_explanation", "major files or packages", "what was verified", "what is inferred", "Forbidden:", "claims without cited paths"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("expected explore contract policy to include %q, got:\n%s", want, policy)
		}
	}
}

func TestTaskPromptsIncludeLaneContractPolicy(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{name: "explore", prompt: buildExploreTaskPrompt("explain routing"), want: "Answer contract: grounded_explanation"},
		{name: "review", prompt: buildReviewTaskPrompt("review routing"), want: "Answer contract: review_findings"},
		{name: "architecture", prompt: buildArchitectureTaskPrompt("analyze structure"), want: "Answer contract: architecture_assessment"},
		{name: "implement", prompt: buildImplementTaskPrompt("change routing"), want: "Answer contract: implementation_result"},
	}
	for _, tc := range cases {
		if !strings.Contains(tc.prompt, tc.want) {
			t.Fatalf("%s prompt missing %q:\n%s", tc.name, tc.want, tc.prompt)
		}
	}
}

func TestResetExecutionPanelCapturesRouteMetadata(t *testing.T) {
	m := &Model{}
	plan := promptExecutionPlan{
		lane:         analysisLaneExplore,
		followupLane: analysisLaneImplement,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       promptRouteReasonExploreRequest,
	}
	m.resetExecutionPanel("Investigate this and then fix it.", plan)
	if m.executionPanel.Lane != analysisLaneExplore {
		t.Fatalf("expected execution panel lane to be explore, got %q", m.executionPanel.Lane)
	}
	if m.executionPanel.FollowupLane != analysisLaneImplement {
		t.Fatalf("expected follow-up lane implement, got %q", m.executionPanel.FollowupLane)
	}
	if m.executionPanel.RouteReason != promptRouteReasonExploreRequest {
		t.Fatalf("expected route reason explore_request, got %q", m.executionPanel.RouteReason)
	}
	if m.executionPanel.Contract != answerContractGroundedExplanation {
		t.Fatalf("expected grounded explanation contract, got %q", m.executionPanel.Contract)
	}
	if !strings.Contains(m.executionPanel.Summary, "implementation pass") {
		t.Fatalf("unexpected execution panel summary: %q", m.executionPanel.Summary)
	}
}

func TestExecutionPanelViewIncludesRouteMetadata(t *testing.T) {
	m := &Model{
		width:  120,
		height: 40,
	}
	m.executionPanel = executionPanelState{
		Request:      "Investigate the codebase",
		Lane:         analysisLaneReview,
		FollowupLane: analysisLaneImplement,
		RouteReason:  promptRouteReasonReviewRequest,
		Contract:     answerContractReviewFindings,
		Summary:      "Reviewing bounded evidence before the implementation pass.",
	}
	got := m.executionPanelView(40, 20)
	for _, want := range []string{"lane: review", "followup: implement", "route: review_request", "contract: review_findings"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected execution panel to include %q, got:\n%s", want, got)
		}
	}
}

func TestExecutionPanelViewIncludesVerificationRiskMetadata(t *testing.T) {
	m := &Model{
		width:  120,
		height: 40,
	}
	m.executionPanel = executionPanelState{
		Verification: verificationPlan{
			Commands:         []string{"go build ./...", "go test -vet=off ./internal/app"},
			Coverage:         "targeted package verification",
			Confidence:       "medium",
			RiskLevel:        "high",
			ExpectedFailures: []string{"compile errors", "routing or prompt assembly regressions"},
			RepairPolicy:     "repair targeted failures, then consider full-suite escalation if behavior changed",
		},
	}

	got := m.executionPanelView(80, 24)
	normalized := strings.Join(strings.Fields(got), " ")
	for _, want := range []string{
		"risk: high",
		"expected failures: compile errors, routing or prompt assembly regressions",
		"repair: repair targeted failures",
	} {
		want = strings.Join(strings.Fields(want), " ")
		if !strings.Contains(normalized, want) {
			t.Fatalf("expected execution panel to include %q, got:\n%s", want, got)
		}
	}
}

func TestExecutionPanelViewIncludesRefactorStageMetadata(t *testing.T) {
	m := &Model{
		width:  120,
		height: 40,
		lastJournal: &executionJournal{
			Summary: "Applied a bounded structural stage.",
			Paths:   []string{"internal/app/app.go"},
			Refactor: &structuralRefactorStage{
				Active:       true,
				Stage:        1,
				FocusPath:    "internal/app/app.go",
				Remaining:    []string{"internal/workspace/manager.go"},
				NextStep:     "review verification, then continue the structural refactor with internal/workspace/manager.go",
				Verification: "high (targeted package verification)",
			},
		},
	}
	got := m.executionPanelView(80, 24)
	normalized := strings.Join(strings.Fields(got), " ")
	for _, want := range []string{
		"refactor stage 1: internal/app/app.go",
		"next: review verification, then continue the structural refactor",
		"internal/workspace/manager.go",
		"rollback: /undo",
	} {
		want = strings.Join(strings.Fields(want), " ")
		if !strings.Contains(normalized, want) {
			t.Fatalf("expected execution panel to include %q, got:\n%s", want, got)
		}
	}
}

func TestExecutionPanelViewIncludesPersistedRefactorStage(t *testing.T) {
	m := &Model{
		width:  120,
		height: 40,
		refactorStage: &structuralRefactorStage{
			Stage:     2,
			FocusPath: "internal/workspace/manager.go",
			Remaining: []string{"internal/agent/runner.go"},
			NextStep:  "review verification, then continue the structural refactor with internal/agent/runner.go",
		},
	}
	got := m.executionPanelView(80, 24)
	normalized := strings.Join(strings.Fields(got), " ")
	for _, want := range []string{
		"Refactor",
		"stage 2: internal/workspace/manager.go",
		"next: review verification, then continue the structural refactor",
		"resume: /resume",
	} {
		want = strings.Join(strings.Fields(want), " ")
		if !strings.Contains(normalized, want) {
			t.Fatalf("expected execution panel to include %q, got:\n%s", want, got)
		}
	}
}

func TestAnalysisRetryRequirementUsesLaneSpecificLanguage(t *testing.T) {
	review := analysisRetryRequirement(analysisLaneReview, []string{"internal/app/app.go"})
	if !strings.Contains(review, "review summary") || !strings.Contains(review, "candidate path") {
		t.Fatalf("unexpected review retry requirement: %q", review)
	}

	architecture := analysisRetryRequirement(analysisLaneArchitecture, []string{"internal/app/app.go"})
	if !strings.Contains(architecture, "architecture summary") || !strings.Contains(architecture, "hotspot path") {
		t.Fatalf("unexpected architecture retry requirement: %q", architecture)
	}
	if !strings.Contains(strings.ToLower(architecture), "structure") {
		t.Fatalf("expected architecture retry requirement to mention structure, got %q", architecture)
	}
	explore := analysisRetryRequirement(analysisLaneExplore, []string{"internal/app/app.go"})
	for _, want := range []string{"explore summary", "evidence path", "verified", "inferred"} {
		if !strings.Contains(strings.ToLower(explore), strings.ToLower(want)) {
			t.Fatalf("expected explore retry requirement to include %q, got %q", want, explore)
		}
	}
}

func TestAnalysisReplyAcceptedUsesLaneSpecificContracts(t *testing.T) {
	architectureReply := "Top structural opportunity: internal/app/app.go has broad orchestration responsibilities and should be split across clearer boundaries."
	if !analysisReplyAccepted(architectureReply, []string{"internal/app/app.go"}, analysisLaneArchitecture) {
		t.Fatalf("expected architecture reply to be accepted: %q", architectureReply)
	}

	structureOnlyReply := "Top opportunity: internal/app/app.go has broad responsibilities and should be split across clearer boundaries."
	if analysisReplyAccepted(structureOnlyReply, []string{"internal/app/app.go"}, analysisLaneReview) {
		t.Fatalf("expected review contract to reject structure-only reply: %q", structureOnlyReply)
	}

	reviewReply := "Finding: internal/app/app.go is the strongest cleanup candidate because the evidence clusters there and confidence is medium."
	if !analysisReplyAccepted(reviewReply, []string{"internal/app/app.go"}, analysisLaneReview) {
		t.Fatalf("expected review reply to be accepted: %q", reviewReply)
	}

	exploreReply := "Verified evidence: internal/app/app.go renders the shell. Inference: internal/workspace/manager.go likely owns the tool boundary."
	if !analysisReplyAccepted(exploreReply, []string{"internal/app/app.go"}, analysisLaneExplore) {
		t.Fatalf("expected grounded explore reply to be accepted: %q", exploreReply)
	}

	genericExploreReply := "This repository is organized well and the behavior is probably straightforward."
	if analysisReplyAccepted(genericExploreReply, []string{"internal/app/app.go"}, analysisLaneExplore) {
		t.Fatalf("expected explore contract to reject ungrounded reply: %q", genericExploreReply)
	}
}

func TestAnalysisReplyAcceptedUsesEvidenceLedgerGrounding(t *testing.T) {
	ledger := evidenceLedger{VerifiedPaths: []string{"internal/app/app.go"}}
	grounded := "Verified evidence: internal/app/app.go owns the routing surface. Inference: split orchestration from rendering."
	if !analysisReplyAcceptedWithEvidenceLedger(grounded, nil, ledger, analysisLaneExplore) {
		t.Fatalf("expected ledger-grounded explore reply to be accepted: %q", grounded)
	}

	ungrounded := "Verified evidence: the routing surface is broad. Inference: split orchestration from rendering."
	if analysisReplyAcceptedWithEvidenceLedger(ungrounded, nil, ledger, analysisLaneExplore) {
		t.Fatalf("expected ledger-grounded acceptance to reject missing path: %q", ungrounded)
	}
}

func TestAnalysisReadinessRejectsLowConfidenceDespiteGrounding(t *testing.T) {
	ledger := evidenceLedger{VerifiedPaths: []string{"internal/app/app.go"}}
	reply := "Finding: internal/app/app.go is probably relevant, but I am not confident and need more evidence."
	readiness := analysisReadinessAssessmentForReply(reply, nil, ledger, analysisLaneReview, true, "retry")
	if readiness.Ready {
		t.Fatalf("expected low-confidence grounded reply to fail readiness: %#v", readiness)
	}
	if readiness.Confidence != "low" || readiness.Action != "retry" {
		t.Fatalf("unexpected low-confidence readiness assessment: %#v", readiness)
	}

	ready := analysisReadinessAssessmentForReply(
		"Finding: internal/app/app.go is the strongest cleanup candidate because the evidence confirms the routing issue.",
		nil,
		ledger,
		analysisLaneReview,
		true,
		"retry",
	)
	if !ready.Ready || ready.Action != "answer" || ready.Confidence != "high" {
		t.Fatalf("expected grounded confident reply to be ready: %#v", ready)
	}
}

func TestBuildAnalysisDeepeningResultCarriesRetryEvidence(t *testing.T) {
	result := buildAnalysisDeepeningResult(
		"review routing",
		analysisLaneReview,
		1,
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/app.go lines 1-120",
			Output:  "internal/app/app.go: route plan",
		}},
		[]string{"internal/app/app.go"},
		"readiness gate failed; retry with wider candidate reads",
	)
	if !result.Attempted || result.Exhausted {
		t.Fatalf("expected successful deepening result, got %#v", result)
	}
	if result.Action != "retry_with_evidence" || result.Lane != string(analysisLaneReview) {
		t.Fatalf("unexpected deepening metadata: %#v", result)
	}
	if !containsString(result.EvidencePaths, "internal/app/app.go") {
		t.Fatalf("expected evidence path in deepening result, got %#v", result.EvidencePaths)
	}
	if len(result.Messages) < 2 {
		t.Fatalf("expected model messages with deepening evidence, got %#v", result.Messages)
	}
	if !containsString(result.Ledger.Tools, "readiness_deepener") {
		t.Fatalf("expected readiness deepener tool marker, got %#v", result.Ledger.Tools)
	}
}

func TestBuildEffortPolicySnapshotReflectsLaneAndThoroughness(t *testing.T) {
	policy := buildEffortPolicySnapshot(promptExecutionPlan{
		lane:         analysisLaneReview,
		followupLane: analysisLaneImplement,
		thoroughness: workspace.ThoroughnessThorough,
	}, 12, 90, 2048, -1)
	if policy.Level != "thorough" || policy.Lane != string(analysisLaneReview) {
		t.Fatalf("unexpected effort policy identity: %#v", policy)
	}
	if policy.AnalysisToolBudget != exploreToolBudget(12) || policy.ImplementToolBudget != implementToolBudget(12) {
		t.Fatalf("unexpected effort budgets: %#v", policy)
	}
	if policy.DeepeningPasses != 2 || policy.ConfidenceFloor != "high_or_exhausted_budget" {
		t.Fatalf("unexpected frontier readiness budget policy: %#v", policy)
	}
	if policy.MaxOutputTokens != 2048 || policy.ReasoningBudget != -1 {
		t.Fatalf("unexpected token budget policy: %#v", policy)
	}
}

func TestExploreEvidencePathsExtractsBootstrapPaths(t *testing.T) {
	paths := exploreEvidencePaths([]workspace.ToolResult{
		{
			Tool:    "list_files",
			Summary: "Listed source files",
			Output:  "f1 internal/app/app.go\nf2 README.md\ninternal/workspace/",
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines",
			Output:  "f3 internal/workspace/manager.go:42:func NewManager",
		},
		{
			Tool:    "read_file",
			Summary: "Read f4 internal/agent/runner.go lines 1-120",
		},
	})
	for _, want := range []string{"internal/app/app.go", "README.md", "internal/workspace/manager.go", "internal/agent/runner.go"} {
		if !containsString(paths, want) {
			t.Fatalf("expected evidence paths to include %q, got %#v", want, paths)
		}
	}
	if containsString(paths, "internal/workspace") {
		t.Fatalf("did not expect directory path in evidence paths: %#v", paths)
	}
}

func TestBuildEvidenceLedgerCapturesToolsPathsAndGaps(t *testing.T) {
	ledger := buildEvidenceLedger("How does routing work?", []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines",
			Output:  "f1 internal/app/app.go:42:func classifyPromptExecutionPlan",
		},
		{
			Tool:  "read_file",
			Error: "path has not been discovered yet",
		},
	})
	if !containsString(ledger.VerifiedPaths, "internal/app/app.go") {
		t.Fatalf("expected verified path in evidence ledger, got %#v", ledger)
	}
	if !containsString(ledger.Tools, "search_content") || !containsString(ledger.Tools, "read_file") {
		t.Fatalf("expected tools in evidence ledger, got %#v", ledger)
	}
	if len(ledger.Errors) != 1 || !strings.Contains(ledger.Errors[0], "path has not been discovered") {
		t.Fatalf("expected tool error in evidence ledger, got %#v", ledger)
	}
	if len(ledger.InferredGaps) == 0 {
		t.Fatalf("expected inferred gap in evidence ledger, got %#v", ledger)
	}
	rendered := renderEvidenceLedger(ledger)
	for _, want := range []string{"Evidence ledger:", "verified paths: internal/app/app.go", "tools: search_content, read_file", "inferred gaps:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered evidence ledger to include %q, got:\n%s", want, rendered)
		}
	}
}

func TestDetectLanguageCapabilitiesFindsGoCppShellAndConfig(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                   "module example.com/project\n",
		"internal/app/app.go":      "package app\n",
		"internal/app/app_test.go": "package app\n",
		"native/main.cpp":          "int main() { return 0; }\n",
		"scripts/run.sh":           "#!/bin/sh\n",
		"config.json":              "{}\n",
		"docs/plan.md":             "# Plan\n",
		"prompts/coding.md":        "Be precise.\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	rendered := renderLanguageCapabilityRegistry(detectLanguageCapabilities(root))
	for _, want := range []string{"- go tools:", "go_find_symbol", "- c/c++ tools:", "cpp_find_symbol", "- shell tools:", "- config tools:", "- docs tools:", "- prompts tools:", "- tests tools:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected language capability registry to include %q, got:\n%s", want, rendered)
		}
	}
}

func TestAppendLanguageCapabilityRegistry(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	profile := appendLanguageCapabilityRegistry("Repository profile.", root)
	if !strings.Contains(profile, "Repository profile.") || !strings.Contains(profile, "Language capability registry:") || !strings.Contains(profile, "go_overview") {
		t.Fatalf("unexpected profile with language registry:\n%s", profile)
	}
}

func TestBuildExploreFallbackSummaryUsesGroundedEvidence(t *testing.T) {
	summary := buildExploreFallbackSummary("How does routing work?", exploreBootstrap{
		results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 internal/app/app.go lines 1-120",
			},
		},
	})
	for _, want := range []string{"Grounded exploration summary", "Verified evidence", "internal/app/app.go", "Inference", "Next validation"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected explore fallback to include %q, got:\n%s", want, summary)
		}
	}
	if !strings.Contains(summary, "Evidence ledger:") {
		t.Fatalf("expected explore fallback to include evidence ledger, got:\n%s", summary)
	}
	if !analysisReplyAccepted(summary, []string{"internal/app/app.go"}, analysisLaneExplore) {
		t.Fatalf("expected explore fallback to satisfy explore contract, got:\n%s", summary)
	}
}

func TestBuildReviewFallbackSummaryIncludesEvidenceLedger(t *testing.T) {
	ledger := deterministicEvidenceLedger(
		"Please review the runtime routing.",
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/app.go lines 1-120",
		}},
		[]string{"internal/app/app.go"},
		"review_candidate_ranker",
	)
	summary := buildReviewFallbackSummary(
		"Please review the runtime routing.",
		[]reviewCandidate{{
			Path:    "internal/app/app.go",
			Score:   90,
			Reasons: []string{"large orchestration surface"},
		}},
		"- internal/app/app.go (score 90): large orchestration surface",
		"Indexed 12 Go files",
		"",
		ledger,
	)
	for _, want := range []string{"Evidence ledger:", "verified paths: internal/app/app.go", "review_candidate_ranker", "Most likely finding"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected review fallback to include %q, got:\n%s", want, summary)
		}
	}
	if !strings.Contains(summary, "Deterministic review assessment") || !strings.Contains(summary, "Confidence:") {
		t.Fatalf("expected review fallback to state deterministic confidence, got:\n%s", summary)
	}
}

func TestBuildArchitectureFallbackSummaryUsesStructuralLanguage(t *testing.T) {
	ledger := deterministicEvidenceLedger(
		"Please identify structural opportunities in this codebase.",
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/app.go lines 1-220",
		}},
		[]string{"internal/app/app.go"},
		"architecture_hotspot_ranker",
	)
	summary := buildArchitectureFallbackSummary(
		"Please identify structural opportunities in this codebase.",
		[]architectureHotspot{{
			Path:             "internal/app/app.go",
			Package:          "app",
			Score:            99,
			FuncsMethods:     28,
			Types:            5,
			Imports:          12,
			LocalImports:     6,
			ImporterFiles:    8,
			PackageFiles:     6,
			BroadSurface:     true,
			Responsibilities: []string{"ui", "routing", "workspace"},
			Reasons:          []string{"28 funcs/methods", "package app spans 6 files"},
		}},
		"- internal/app/app.go (score 99, package app): 28 funcs/methods | coupling: fan-out 6 local imports, fan-in 8 importer files | responsibilities: ui, routing, workspace | direction: split orchestration/state handling from helper and rendering concerns",
		"Indexed 12 Go files",
		"",
		ledger,
	)
	if strings.Contains(summary, "Most likely finding") {
		t.Fatalf("expected architecture fallback to avoid review wording, got %q", summary)
	}
	for _, want := range []string{"Deterministic architecture assessment", "Evidence ledger:", "architecture_hotspot_ranker", "Confidence:", "high:", "Structural baseline", "Highest-value structural opportunities", "internal/app/app.go", "suggested direction"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected architecture fallback to include %q, got:\n%s", want, summary)
		}
	}
}

func TestDeterministicArchitectureAnswerAcceptsHighConfidenceFallback(t *testing.T) {
	ledger := deterministicEvidenceLedger(
		"Please identify structural opportunities in this codebase.",
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/app.go lines 1-220",
		}},
		[]string{"internal/app/app.go", "internal/workspace/manager.go"},
		"architecture_hotspot_ranker",
	)
	summary := buildArchitectureFallbackSummary(
		"Please identify structural opportunities in this codebase.",
		[]architectureHotspot{{
			Path:             "internal/app/app.go",
			Package:          "app",
			Score:            99,
			FuncsMethods:     28,
			Types:            5,
			Imports:          12,
			LocalImports:     6,
			ImporterFiles:    8,
			PackageFiles:     6,
			BroadSurface:     true,
			Responsibilities: []string{"ui", "routing", "workspace"},
			Reasons:          []string{"28 funcs/methods", "package app spans 6 files"},
		}},
		"- internal/app/app.go (score 99, package app): 28 funcs/methods | coupling: fan-out 6 local imports, fan-in 8 importer files | responsibilities: ui, routing, workspace | direction: split orchestration/state handling from helper and rendering concerns",
		"Indexed 12 Go files",
		"",
		ledger,
	)
	answer, readiness, ok := deterministicArchitectureAnswer(summary, []string{"internal/app/app.go"}, ledger, analysisLaneArchitecture)
	if !ok {
		t.Fatalf("expected deterministic architecture answer to be accepted; readiness=%+v", readiness)
	}
	if answer != summary {
		t.Fatal("expected helper to return the fallback summary unchanged")
	}
	if !readiness.Ready || readiness.Confidence != "high" || readiness.Action != "answer" {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}
	if _, _, ok := deterministicArchitectureAnswer(summary, []string{"internal/app/app.go"}, ledger, analysisLaneReview); ok {
		t.Fatal("expected deterministic shortcut to be architecture-only")
	}
}

func TestArchitectureRelatedPatternForPathUsesFileAndPackageTerms(t *testing.T) {
	if got := architectureRelatedPatternForPath("internal/app/impact_analysis.go"); got != "impact_analysis app" {
		t.Fatalf("unexpected related pattern %q", got)
	}
	if got := architectureRelatedPatternForPath(""); got == "" {
		t.Fatal("expected fallback related pattern")
	}
}

func TestDeepenArchitectureEvidenceUsesValidRelatedFileSearch(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	result, err := (&Model{}).deepenArchitectureEvidence(
		context.Background(),
		mgr,
		"identify architectural improvements",
		[]architectureHotspot{{Path: "internal/app/app.go"}},
		analysisDeepeningRequest{Attempt: 1},
	)
	if err != nil {
		t.Fatalf("deepenArchitectureEvidence: %v", err)
	}
	for _, message := range result.Messages {
		if strings.Contains(message.Content, "pattern is required") {
			t.Fatalf("go_related_files should receive a pattern, got message:\n%s", message.Content)
		}
	}
}

func TestArchitectureFallbackUsesLanguageCapabilityRegistry(t *testing.T) {
	repoProfile := "Repository profile.\n\nLanguage capability registry:\n- c/c++ tools: cpp_overview, cpp_find_symbol | signals: native/main.cpp, native/app.hpp\n- tests tools: targeted regression checks | signals: native/tests/run_tests.py"
	summary := buildArchitectureFallbackSummary(
		"Please identify structural opportunities in the native layer.",
		[]architectureHotspot{{
			Path:    "native/main.cpp",
			Package: "native",
			Score:   80,
			Reasons: []string{"broad native entrypoint"},
		}},
		"- native/main.cpp (score 80): broad native entrypoint",
		repoProfile,
		"",
		deterministicEvidenceLedger("native layer", nil, []string{"native/main.cpp"}, "architecture_hotspot_ranker"),
	)
	for _, want := range []string{"Detected capability surface:", "c/c++", "tests", "Highest-value C/C++ structural opportunities"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected language-aware architecture fallback to include %q, got:\n%s", want, summary)
		}
	}
}

func TestRankArchitectureHotspotsPrefersLargeInternalFiles(t *testing.T) {
	idx := &semantic.GoIndex{
		Module: "example.com/project",
		Files: []semantic.GoFile{
			{
				Path:    "internal/app/app.go",
				Package: "app",
				Funcs:   20,
				Methods: 6,
				Types:   5,
				Imports: []string{"example.com/project/internal/workspace", "example.com/project/internal/agent", "example.com/project/internal/config", "a", "b", "c", "d", "e", "f"},
				Symbols: []semantic.GoSymbol{
					{Name: "renderExecutionPanel"},
					{Name: "classifyPromptExecutionPlan"},
					{Name: "runImplementAgent"},
					{Name: "rememberExecutionJournal"},
				},
				SourceDir: "internal",
			},
			{
				Path:      "cmd/qwen-bubbles/main.go",
				Package:   "main",
				Funcs:     2,
				Methods:   0,
				Types:     0,
				Imports:   []string{"fmt"},
				HasMain:   true,
				SourceDir: "cmd",
			},
		},
		Package: map[string]semantic.GoPackage{
			"internal/app":     {Name: "app", Files: 6, Funcs: 30, Methods: 8, Types: 7},
			"cmd/qwen-bubbles": {Name: "main", Files: 1, Funcs: 2, Methods: 0, Types: 0},
		},
	}

	hotspots := rankArchitectureHotspots("Please identify structural opportunities in this codebase.", idx, 2)
	if len(hotspots) == 0 {
		t.Fatal("expected architecture hotspots")
	}
	if hotspots[0].Path != "internal/app/app.go" {
		t.Fatalf("expected internal app hotspot first, got %#v", hotspots)
	}
	board := buildArchitectureHotspotBoard(hotspots)
	if !strings.Contains(board, "direction:") {
		t.Fatalf("expected hotspot board to include refactor direction, got:\n%s", board)
	}
	if !strings.Contains(board, "coupling:") {
		t.Fatalf("expected hotspot board to include coupling metadata, got:\n%s", board)
	}
	if !strings.Contains(board, "fan-out") || !strings.Contains(board, "fan-in") {
		t.Fatalf("expected hotspot board to include fan-in/fan-out labels, got:\n%s", board)
	}
	if !strings.Contains(board, "responsibilities:") {
		t.Fatalf("expected hotspot board to include responsibility metadata, got:\n%s", board)
	}
}

func TestArchitectureResponsibilitySignalsDetectMixedSurface(t *testing.T) {
	signals := architectureResponsibilitySignals(semantic.GoFile{
		Path:    "internal/app/app.go",
		Imports: []string{"github.com/charmbracelet/lipgloss", "example.com/project/internal/workspace", "example.com/project/internal/agent"},
		Symbols: []semantic.GoSymbol{
			{Name: "renderPanel"},
			{Name: "classifyPromptExecutionPlan"},
			{Name: "rollbackLastJournalAsync"},
		},
	})
	for _, want := range []string{"ui", "routing", "workspace", "agent", "state"} {
		if !containsString(signals, want) {
			t.Fatalf("expected responsibility signal %q, got %#v", want, signals)
		}
	}
}

func TestLocateFallbackPatternsIncludesHeaderHintsForBannerPrompt(t *testing.T) {
	patterns := locateFallbackPatterns(`please remove the "waiting..." text from the banner`, "waiting...")
	if !containsString(patterns, "header") {
		t.Fatalf("expected header fallback pattern, got %#v", patterns)
	}
	if !containsString(patterns, "banner") {
		t.Fatalf("expected banner fallback pattern, got %#v", patterns)
	}
}

func TestLocateLiteralSearchPatternEscapesRegexMeta(t *testing.T) {
	pattern := locateLiteralSearchPattern("thinking...")
	if pattern != "thinking\\.\\.\\." {
		t.Fatalf("expected escaped literal pattern, got %q", pattern)
	}
}

func TestParseLocateCandidatesIgnoresTestFiles(t *testing.T) {
	output := strings.Join([]string{
		`f1 internal/app/write_guard_test.go:53:        "please remove the \"waiting...\" text from the banner",`,
		`f2 internal/app/app.go:475:        header += workingStyle.Render(m.spinner.View() + " Qwen is working" + m.workingDots())`,
	}, "\n")

	candidates := parseLocateCandidates(output)
	if len(candidates) != 1 {
		t.Fatalf("expected only the non-test locate candidate, got %#v", candidates)
	}
	if candidates[0].Path != "internal/app/app.go" || candidates[0].Line != 475 {
		t.Fatalf("unexpected surviving candidate: %#v", candidates[0])
	}
}

func TestRenderLocateSummaryIncludesContractShape(t *testing.T) {
	summary := renderLocateSummary(`remove "working..." from the top banner`, locateOutcome{
		Literal: "working...",
		Best: locateCandidate{
			Path:    "internal/app/app.go",
			Line:    475,
			Match:   "header += workingStyle.Render(...)",
			Snippet: "475:header += workingStyle.Render(...)",
			Region:  "header",
		},
	})
	for _, want := range []string{"Answer contract: locate_targets", "Best target:", "`internal/app/app.go:475`", "Snippet:", "region: header"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected locate summary to include %q, got:\n%s", want, summary)
		}
	}
	if !analysisReplyAccepted(summary, []string{"internal/app/app.go"}, analysisLaneLocate) {
		t.Fatalf("expected locate summary to satisfy locate contract, got:\n%s", summary)
	}
}

func TestAnalysisReplyAcceptedRejectsUngroundedLocateSummary(t *testing.T) {
	reply := "The code is probably in the UI layer."
	if analysisReplyAccepted(reply, []string{"internal/app/app.go"}, analysisLaneLocate) {
		t.Fatalf("expected locate contract to reject ungrounded summary: %q", reply)
	}
}

func TestSamplingSummaryIncludesThinkingState(t *testing.T) {
	m := &Model{cfg: config.Config{Chat: config.Chat{
		Temperature:       0.6,
		TopP:              0.95,
		TopK:              20,
		MinP:              0,
		PresencePenalty:   0,
		RepetitionPenalty: 1.0,
		PreserveThinking:  false,
		MaxTokens:         8192,
	}}}

	if got := m.samplingSummary(); !strings.Contains(got, "thinking=auto") {
		t.Fatalf("expected auto thinking summary, got %q", got)
	}

	on := true
	m.cfg.Chat.EnableThinking = cloneBoolPtr(&on)
	if got := m.samplingSummary(); !strings.Contains(got, "thinking=on") {
		t.Fatalf("expected thinking=on summary, got %q", got)
	}

	off := false
	m.cfg.Chat.EnableThinking = cloneBoolPtr(&off)
	if got := m.samplingSummary(); !strings.Contains(got, "thinking=off") {
		t.Fatalf("expected thinking=off summary, got %q", got)
	}
}

func TestTraceSummaryCompactTracksHiddenEvents(t *testing.T) {
	m := &Model{traceMode: traceDisplayCompact}
	if got := m.traceSummary(); got != "compact (quiet)" {
		t.Fatalf("expected quiet compact trace summary, got %q", got)
	}
	m.recordCompactTrace("edit_file | Edited config.json lines 44-44\noutput:\nreplaced lines: 44-44")
	got := m.traceSummary()
	if !strings.Contains(got, "compact (1 hidden; last: edit_file | Edited config.json lines 44-44)") {
		t.Fatalf("unexpected compact trace summary: %q", got)
	}
	m.resetCompactTrace()
	if got := m.traceSummary(); got != "compact (quiet)" {
		t.Fatalf("expected compact trace reset to quiet, got %q", got)
	}
}

func TestHandleSlashCommandTraceMode(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig()), traceMode: traceDisplayCompact}
	if quit := m.handleSlashCommand("/trace detail"); quit {
		t.Fatal("did not expect /trace to request quit")
	}
	if m.traceMode != traceDisplayDetail {
		t.Fatalf("expected trace mode detail, got %q", m.traceMode)
	}
	if quit := m.handleSlashCommand("/trace compact"); quit {
		t.Fatal("did not expect /trace to request quit")
	}
	if m.traceMode != traceDisplayCompact {
		t.Fatalf("expected trace mode compact, got %q", m.traceMode)
	}
}

func TestHandleSlashCommandPanelToggle(t *testing.T) {
	m := &Model{
		workspace:    workspace.NewManager(testWorkspaceConfig()),
		panelVisible: true,
		width:        120,
		height:       40,
		input:        textarea.New(),
	}
	if quit := m.handleSlashCommand("/panel hide"); quit {
		t.Fatal("did not expect /panel hide to request quit")
	}
	if m.panelVisible {
		t.Fatal("expected panel to be hidden")
	}
	if got := m.status; got != "Execution panel collapsed." {
		t.Fatalf("unexpected /panel hide status: %q", got)
	}
	if quit := m.handleSlashCommand("/panel show"); quit {
		t.Fatal("did not expect /panel show to request quit")
	}
	if !m.panelVisible {
		t.Fatal("expected panel to be shown")
	}
	if got := m.status; got != "Execution panel expanded." {
		t.Fatalf("unexpected /panel show status: %q", got)
	}
}

func TestBodyLayoutUsesOneThirdWidthForExecutionPanel(t *testing.T) {
	m := &Model{panelVisible: true, width: 120}
	transcriptWidth, panelWidth, show := m.bodyLayout()
	if !show {
		t.Fatal("expected panel to render at width 120")
	}
	if transcriptWidth != 79 || panelWidth != 40 {
		t.Fatalf("unexpected body layout widths: transcript=%d panel=%d", transcriptWidth, panelWidth)
	}
}

func TestBodyLayoutAutoHidesPanelWhenTerminalIsTooNarrow(t *testing.T) {
	m := &Model{panelVisible: true, width: 70}
	transcriptWidth, panelWidth, show := m.bodyLayout()
	if show {
		t.Fatal("expected panel to auto-hide on narrow width")
	}
	if transcriptWidth != 70 || panelWidth != 0 {
		t.Fatalf("unexpected narrow layout widths: transcript=%d panel=%d", transcriptWidth, panelWidth)
	}
	if got := m.panelSummary(); got != "expanded (auto-hidden on narrow width)" {
		t.Fatalf("unexpected panel summary: %q", got)
	}
}

func TestDisplayPathUsesWorkspaceRelativeFormForAbsolutePaths(t *testing.T) {
	root := filepath.Clean("/tmp/qwen-bubbles-observability")
	m := &Model{workspace: workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          filepath.Join(root, "internal"),
		EnableWrites: true,
	})}

	if got := m.displayPath(root); got != "." {
		t.Fatalf("expected workspace root to render as '.', got %q", got)
	}
	if got := m.displayPath(filepath.Join(root, "logs", "conversations", "session.jsonl")); got != "./logs/conversations/session.jsonl" {
		t.Fatalf("expected workspace-relative display path, got %q", got)
	}
	if got := m.displayPath(filepath.Join("/tmp", "outside.txt")); got != "/tmp/outside.txt" {
		t.Fatalf("expected outside path to stay absolute, got %q", got)
	}
}

func TestWorkspaceSummaryUsesRelativePaths(t *testing.T) {
	root := filepath.Clean("/tmp/qwen-bubbles-observability")
	m := &Model{workspace: workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          filepath.Join(root, "internal", "app"),
		EnableWrites: true,
	})}

	if got := m.workspaceSummary(); got != "root=. cwd=./internal/app" {
		t.Fatalf("unexpected workspace summary: %q", got)
	}
}

func TestHandleSlashCommandPwdShowsRelativeWorkspacePath(t *testing.T) {
	root := filepath.Clean("/tmp/qwen-bubbles-observability")
	m := &Model{workspace: workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          filepath.Join(root, "internal", "app"),
		EnableWrites: true,
	})}

	if quit := m.handleSlashCommand("/pwd"); quit {
		t.Fatal("did not expect /pwd to request quit")
	}
	if got := m.status; got != "Current workspace directory: ./internal/app" {
		t.Fatalf("unexpected /pwd status: %q", got)
	}
}

func TestHandleSlashCommandDebuglogShowsRelativePath(t *testing.T) {
	root := filepath.Clean("/tmp/qwen-bubbles-observability")
	m := &Model{
		workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		cfg:          config.Config{Chat: config.Chat{DebugLogMode: "redacted"}},
		debugLogPath: filepath.Join(root, "logs", "conversations", "session.jsonl"),
	}

	if quit := m.handleSlashCommand("/debuglog"); quit {
		t.Fatal("did not expect /debuglog to request quit")
	}
	if got := m.status; got != "Debug log: ./logs/conversations/session.jsonl | mode: redacted" {
		t.Fatalf("unexpected /debuglog status: %q", got)
	}
}

func TestImplementationVerificationPlanTargetsLocalGoPackageFirst(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := implementationVerificationPlan(mgr, []string{"internal/eval/eval.go"})
	if !containsString(plan.Commands, "go build ./...") {
		t.Fatalf("expected build command in verification plan, got %#v", plan.Commands)
	}
	if !containsString(plan.Commands, "go test -vet=off ./internal/eval") {
		t.Fatalf("expected targeted package test in verification plan, got %#v", plan.Commands)
	}
	if containsString(plan.Commands, "go test -vet=off ./...") {
		t.Fatalf("did not expect full-suite escalation for local eval package change, got %#v", plan.Commands)
	}
	if plan.Confidence != "high" {
		t.Fatalf("expected high confidence for targeted package+importer verification, got %q", plan.Confidence)
	}
	if plan.Coverage != "targeted package and importer verification" {
		t.Fatalf("unexpected coverage summary: %q", plan.Coverage)
	}
	if plan.RiskLevel != "medium" {
		t.Fatalf("expected medium risk for local Go package change, got %q", plan.RiskLevel)
	}
	for _, want := range []string{"compile errors", "package test regressions"} {
		if !containsString(plan.ExpectedFailures, want) {
			t.Fatalf("expected failure mode %q in verification plan, got %#v", want, plan.ExpectedFailures)
		}
	}
	if !strings.Contains(plan.RepairPolicy, "targeted or importer failures") {
		t.Fatalf("expected targeted repair policy, got %q", plan.RepairPolicy)
	}
}

func TestImplementationVerificationPlanEscalatesCoreGoChangeToFullSuite(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := implementationVerificationPlan(mgr, []string{"internal/app/app.go"})
	if !containsString(plan.Commands, "go test -vet=off ./internal/app") {
		t.Fatalf("expected targeted app package test in verification plan, got %#v", plan.Commands)
	}
	if !containsString(plan.Commands, "go test -vet=off ./...") {
		t.Fatalf("expected full-suite escalation for core app change, got %#v", plan.Commands)
	}
	if !plan.UsesFullSuite {
		t.Fatal("expected full-suite flag for core app change")
	}
	if plan.Confidence != "high" {
		t.Fatalf("expected high confidence for full-suite escalation, got %q", plan.Confidence)
	}
	if plan.Coverage != "full-suite escalation" {
		t.Fatalf("unexpected coverage summary: %q", plan.Coverage)
	}
	if plan.RiskLevel != "high" {
		t.Fatalf("expected high risk for core app change, got %q", plan.RiskLevel)
	}
	if !containsString(plan.ExpectedFailures, "routing or prompt assembly regressions") {
		t.Fatalf("expected routing/prompt failure mode, got %#v", plan.ExpectedFailures)
	}
	if !strings.Contains(plan.RepairPolicy, "full-suite") {
		t.Fatalf("expected full-suite repair policy, got %q", plan.RepairPolicy)
	}
}

func TestImplementationVerificationPlanAddsShellSyntaxCheck(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := implementationVerificationPlan(mgr, []string{"scripts/run-harness.sh"})
	if !containsString(plan.Commands, "bash -n scripts/run-harness.sh") {
		t.Fatalf("expected shell syntax check in verification plan, got %#v", plan.Commands)
	}
	if plan.Confidence != "high" || plan.Coverage != "full-suite escalation" {
		t.Fatalf("expected high-confidence full-suite verification for script changes, got confidence=%q coverage=%q", plan.Confidence, plan.Coverage)
	}
	if plan.RiskLevel != "high" {
		t.Fatalf("expected high risk for script change with full-suite escalation, got %q", plan.RiskLevel)
	}
	if !containsString(plan.ExpectedFailures, "shell syntax errors") {
		t.Fatalf("expected shell failure mode, got %#v", plan.ExpectedFailures)
	}
}

func TestImplementationVerificationPlanMarksDocsOnlyAsLowRisk(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := implementationVerificationPlan(mgr, []string{"docs/frontier-routing-implementation-plan.md"})
	if plan.RiskLevel != "low" {
		t.Fatalf("expected low risk for docs-only change, got %q", plan.RiskLevel)
	}
	if plan.Coverage != "compile-only verification" || plan.Confidence != "low" {
		t.Fatalf("expected compile-only smoke coverage for docs-only change, got coverage=%q confidence=%q", plan.Coverage, plan.Confidence)
	}
	if !strings.Contains(plan.RepairPolicy, "smoke coverage") {
		t.Fatalf("expected smoke-coverage repair policy, got %q", plan.RepairPolicy)
	}
}

func TestExecutionJournalRecorderCapturesExistingFileBeforeEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	recorder := newExecutionJournalRecorder(mgr, "update note")

	if err := recorder.CaptureRequest(workspace.ToolRequest{Tool: "edit_file", Path: "note.txt"}); err != nil {
		t.Fatalf("capture request: %v", err)
	}
	journal := recorder.Finalize([]string{"note.txt"}, "updated note")
	if journal == nil {
		t.Fatal("expected execution journal")
	}
	snapshot, ok := journal.Snapshots["note.txt"]
	if !ok {
		t.Fatalf("expected snapshot for note.txt, got %#v", journal.Snapshots)
	}
	if !snapshot.Exists {
		t.Fatal("expected existing snapshot")
	}
	if string(snapshot.Content) != "before\n" {
		t.Fatalf("expected original content, got %q", string(snapshot.Content))
	}
}

func TestApplyExecutionRollbackRestoresPreviousFileContents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	recorder := newExecutionJournalRecorder(mgr, "update note")
	if err := recorder.CaptureRequest(workspace.ToolRequest{Tool: "edit_file", Path: "note.txt"}); err != nil {
		t.Fatalf("capture request: %v", err)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	journal := recorder.Finalize([]string{"note.txt"}, "updated note")
	if journal == nil {
		t.Fatal("expected execution journal")
	}
	if err := applyExecutionRollback(mgr, journal); err != nil {
		t.Fatalf("apply rollback: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "before\n" {
		t.Fatalf("expected restored content, got %q", string(content))
	}
}

func TestApplyExecutionRollbackRemovesCreatedFile(t *testing.T) {
	root := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	recorder := newExecutionJournalRecorder(mgr, "create note")
	if err := recorder.CaptureRequest(workspace.ToolRequest{Tool: "write_file", Path: "note.txt"}); err != nil {
		t.Fatalf("capture request: %v", err)
	}
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("created\n"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	journal := recorder.Finalize([]string{"note.txt"}, "created note")
	if journal == nil {
		t.Fatal("expected execution journal")
	}
	if err := applyExecutionRollback(mgr, journal); err != nil {
		t.Fatalf("apply rollback: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected created file to be removed, stat err=%v", err)
	}
}

func TestHandleSlashCommandUndoWithoutJournal(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}

	if quit := m.handleSlashCommand("/undo"); quit {
		t.Fatal("did not expect /undo to request quit")
	}
	if got := m.status; got != "No rollback journal is available for the last bounded change." {
		t.Fatalf("unexpected /undo status: %q", got)
	}
}

func TestHandleSlashCommandResumeWithoutRefactorStage(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}

	if quit := m.handleSlashCommand("/resume"); quit {
		t.Fatal("did not expect /resume to request quit")
	}
	if got := m.status; got != "No resumable investigation or repair task is available." {
		t.Fatalf("unexpected /resume status: %q", got)
	}
}

func TestHandleSlashCommandResumeRejectsUnknownArgument(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}

	if quit := m.handleSlashCommand("/resume everything"); quit {
		t.Fatal("did not expect /resume to request quit")
	}
	if got := m.status; got != "Usage: /resume [refactor|task]" {
		t.Fatalf("unexpected /resume status: %q", got)
	}
}

func TestHandleSlashCommandReplanWithoutRecommendation(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}

	if quit := m.handleSlashCommand("/replan"); quit {
		t.Fatal("did not expect /replan to request quit")
	}
	if got := m.status; got != "No replan recommendation is available." {
		t.Fatalf("unexpected /replan status: %q", got)
	}
}

func TestHandleSlashCommandReplanRejectsArguments(t *testing.T) {
	m := &Model{workspace: workspace.NewManager(testWorkspaceConfig())}

	if quit := m.handleSlashCommand("/replan now"); quit {
		t.Fatal("did not expect /replan to request quit")
	}
	if got := m.status; got != "Usage: /replan" {
		t.Fatalf("unexpected /replan status: %q", got)
	}
}

func TestPhaseCardViewRendersLaneStateAndDetail(t *testing.T) {
	m := &Model{
		phaseCard: phaseCardState{
			Lane:   analysisLaneImplement,
			Phase:  "verify",
			State:  phaseStateDone,
			Detail: "Verification passed with 2 successful check(s).",
			At:     time.Now().Add(-3 * time.Second),
		},
	}

	got := m.phaseCardView()
	if !strings.Contains(got, "phase: implement / verify [done |") {
		t.Fatalf("unexpected phase card header: %q", got)
	}
	if !strings.Contains(got, "Verification passed with 2 successful check(s).") {
		t.Fatalf("unexpected phase card detail: %q", got)
	}
}

func TestUpdateWriteApprovalProposalSetsBlockedPhaseCard(t *testing.T) {
	m := &Model{}
	proposal := writeApprovalProposal{Paths: []string{"config.json"}}

	next, _ := m.Update(writeApprovalProposalMsg{proposal: proposal})
	updated, ok := next.(*Model)
	if !ok {
		t.Fatalf("expected *Model, got %T", next)
	}
	if updated.phaseCard.Lane != analysisLaneImplement || updated.phaseCard.Phase != "approval" || updated.phaseCard.State != phaseStateBlocked {
		t.Fatalf("unexpected phase card after approval proposal: %#v", updated.phaseCard)
	}
	if !strings.Contains(updated.phaseCard.Detail, "config.json") {
		t.Fatalf("expected phase card detail to mention scope, got %q", updated.phaseCard.Detail)
	}
}

func TestAnalysisSummaryTargetsExtractLocateReference(t *testing.T) {
	history := []llama.ChatMessage{{
		Role: "system",
		Content: "[locate subagent summary]\n" +
			"Use this as initial orientation, but verify before editing.\n\n" +
			"The top-of-window \"working...\" banner is in `internal/app/app.go:475`.\n\n" +
			"Separate similar match:\n- chat placeholder: `internal/app/app.go:4571`",
	}}

	targets := analysisSummaryTargets(history)
	if len(targets) == 0 {
		t.Fatal("expected locate summary history to produce implementation targets")
	}
	if targets[0].Path != "internal/app/app.go" || targets[0].Line != 475 {
		t.Fatalf("unexpected first locate target: %#v", targets[0])
	}
}

func TestImplementationTargetPathsIncludesLocateSummaryPath(t *testing.T) {
	history := []llama.ChatMessage{{
		Role: "system",
		Content: "[locate subagent summary]\n" +
			"The top-of-window \"working...\" banner is in `internal/app/app.go:475`.",
	}}

	paths := implementationTargetPaths("Please identify the logic that creates the \"working...\" logic in the banner and remove it.", history)
	if !containsString(paths, "internal/app/app.go") {
		t.Fatalf("expected locate summary path to seed implementation targets, got %#v", paths)
	}
}

func TestImplementationTargetPathsIncludesAppForUIEditPrompt(t *testing.T) {
	paths := implementationTargetPaths("Can we add a small bit of padding underneath the banner?", nil)
	if !containsString(paths, "internal/app/app.go") {
		t.Fatalf("expected UI edit prompt to seed internal/app/app.go, got %#v", paths)
	}
}

func TestPromptMentionedWorkspacePathsSupportsAtAnchors(t *testing.T) {
	paths := promptMentionedWorkspacePaths("Update @internal/app/app.go to match @internal/llama/client.go where useful.")
	if !containsString(paths, "internal/app/app.go") {
		t.Fatalf("expected @anchor path to be extracted, got %#v", paths)
	}
	if !containsString(paths, "internal/llama/client.go") {
		t.Fatalf("expected second @anchor path to be extracted, got %#v", paths)
	}
}

func TestBuildImplementationTargetPacketCapturesAnalogyPaths(t *testing.T) {
	packet := buildImplementationTargetPacket("Update @internal/app/app.go so its status rendering works like @internal/llama/client.go.", nil)
	if !containsString(packet.PrimaryPaths, "internal/app/app.go") {
		t.Fatalf("expected primary target path, got %#v", packet.PrimaryPaths)
	}
	if !containsString(packet.AnalogyPaths, "internal/llama/client.go") {
		t.Fatalf("expected analogy reference path, got %#v", packet.AnalogyPaths)
	}
}

func TestBuildImplementationTargetPacketCapturesSymbolHints(t *testing.T) {
	packet := buildImplementationTargetPacket("Update Model.bootstrapImplementationEvidence in @internal/app/app.go to match runImplementAgentWithGrant.", nil)
	if !containsString(packet.SymbolHints, "Model.bootstrapImplementationEvidence") {
		t.Fatalf("expected method symbol hint, got %#v", packet.SymbolHints)
	}
	if !containsString(packet.SymbolHints, "runImplementAgentWithGrant") {
		t.Fatalf("expected function symbol hint, got %#v", packet.SymbolHints)
	}
}

func TestEnrichImplementationTargetPacketResolvesSymbolAnchors(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	packet := enrichImplementationTargetPacket(mgr, buildImplementationTargetPacket("Update Model.bootstrapImplementationEvidence in @internal/app/app.go.", nil))
	found := false
	for _, symbol := range packet.Symbols {
		if symbol.Path == "internal/app/app.go" && implementationQualifiedSymbol(symbol) == "Model.bootstrapImplementationEvidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected semantic symbol anchor in enriched packet, got %#v", packet.Symbols)
	}
}

func TestBuildImplementationTargetPacketHarvestsArchitectureSummaryHints(t *testing.T) {
	history := appendAnalysisSummary(nil, "architecture",
		"Refactor stage: 2\n"+
			"Top structural opportunities:\n"+
			"- internal/app/app.go should be split across clearer orchestration boundaries.\n"+
			"- internal/workspace/manager.go should separate generic workspace operations from language-specific tooling.\n"+
			"- Suggested direction: extract a narrower workspace interface before moving responsibilities.",
	)
	packet := buildImplementationTargetPacket("Analyze the structure and then implement the first split.", history)
	if packet.SourceLane != "architecture" {
		t.Fatalf("expected architecture source lane, got %q", packet.SourceLane)
	}
	if packet.RefactorStage != 2 {
		t.Fatalf("expected refactor stage 2, got %d", packet.RefactorStage)
	}
	for _, want := range []string{"internal/app/app.go", "internal/workspace/manager.go"} {
		if !containsString(packet.PrimaryPaths, want) {
			t.Fatalf("expected architecture summary path %q in primary targets, got %#v", want, packet.PrimaryPaths)
		}
	}
	if len(packet.StrategyHints) == 0 {
		t.Fatalf("expected structural strategy hints, got %#v", packet)
	}
	if !strings.Contains(strings.Join(packet.StrategyHints, "\n"), "Suggested direction") {
		t.Fatalf("expected structural strategy direction in packet, got %#v", packet.StrategyHints)
	}
}

func TestRenderImplementationTargetPacketIncludesAnchorsAndAnalogies(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:       "architecture",
		PrimaryPaths:     []string{"internal/app/app.go"},
		Anchors:          []implementationTarget{{Path: "internal/app/app.go", Line: 475}},
		AnalogyPaths:     []string{"internal/llama/client.go"},
		LiteralFragments: []string{"working..."},
		LocationHints:    []string{"banner"},
		StrategyHints:    []string{"split orchestration/state handling from helper and rendering concerns"},
	}
	rendered := renderImplementationTargetPacket(packet)
	if !strings.Contains(rendered, "derived from architecture summary") {
		t.Fatalf("expected source lane in target packet, got %q", rendered)
	}
	if !strings.Contains(rendered, "primary files: internal/app/app.go") {
		t.Fatalf("expected primary file in target packet, got %q", rendered)
	}
	if !strings.Contains(rendered, "anchored lines: internal/app/app.go:475") {
		t.Fatalf("expected anchor in target packet, got %q", rendered)
	}
	if !strings.Contains(rendered, "analogous references: internal/llama/client.go") {
		t.Fatalf("expected analogy in target packet, got %q", rendered)
	}
	if !strings.Contains(rendered, "structural hints: split orchestration/state handling from helper and rendering concerns") {
		t.Fatalf("expected structural hint in target packet, got %q", rendered)
	}
	if !strings.Contains(rendered, "structural stage: stage 1 edits internal/app/app.go") {
		t.Fatalf("expected structural stage hint in target packet, got %q", rendered)
	}
}

func TestClassifyPromptExecutionPlanRoutesUIEditPromptToImplement(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	plan := classifyPromptExecutionPlan(
		"Can we add a small bit of padding underneath the banner?",
		mgr,
	)
	if plan.lane != analysisLaneImplement {
		t.Fatalf("expected implement lane for UI edit prompt, got %q", plan.lane)
	}
}

func TestBootstrapImplementationEvidenceSeedsBannerRegionForUIEditPrompt(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Can we add a small bit of padding underneath the banner?",
		implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}},
		nil,
	)
	if len(bootstrap.results) == 0 {
		t.Fatal("expected bootstrap evidence for UI edit prompt")
	}
	foundRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/app.go") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Fatalf("expected seeded read_file result for internal/app/app.go, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceDiscoversExplicitPrimaryPathBeforeRead(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Please edit @internal/app/app.go to add a small bit of padding underneath the banner.",
		implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}},
		nil,
	)
	if len(bootstrap.results) < 2 {
		t.Fatalf("expected discovery and read bootstrap results, got %#v", bootstrap.results)
	}
	foundSearch := false
	foundRead := false
	for _, result := range bootstrap.results {
		switch result.Tool {
		case "search_files":
			if strings.Contains(result.Output, "internal/app/app.go") {
				foundSearch = true
			}
		case "read_file":
			if strings.Contains(result.Summary, "internal/app/app.go") {
				foundRead = true
			}
		}
	}
	if !foundSearch {
		t.Fatalf("expected explicit primary path bootstrap to discover internal/app/app.go first, got %#v", bootstrap.results)
	}
	if !foundRead {
		t.Fatalf("expected explicit primary path bootstrap to read internal/app/app.go, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceReadsAnchorWindowFromTargetPacket(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		`remove the "working..." text from the banner`,
		implementationTargetPacket{
			PrimaryPaths: []string{"internal/app/app.go"},
			Anchors:      []implementationTarget{{Path: "internal/app/app.go", Line: 475}},
		},
		nil,
	)
	if len(bootstrap.results) == 0 {
		t.Fatal("expected anchored bootstrap evidence")
	}
	foundRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/app.go") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Fatalf("expected anchored bootstrap evidence to include a read_file result, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceUsesArchitectureSummaryDerivedTargets(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}
	history := appendAnalysisSummary(nil, "architecture",
		"Top structural opportunities:\n"+
			"- internal/app/app.go should be split across clearer orchestration boundaries.\n"+
			"- internal/workspace/manager.go should separate generic workspace operations from language-specific tooling.",
	)
	packet := buildImplementationTargetPacket("Analyze the structure and then implement the first split.", history)
	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Analyze the structure and then implement the first split.",
		packet,
		history,
	)
	if len(bootstrap.results) == 0 {
		t.Fatal("expected bootstrap evidence from architecture-derived targets")
	}
	foundPrimaryRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/app.go") {
			foundPrimaryRead = true
			break
		}
	}
	if !foundPrimaryRead {
		t.Fatalf("expected architecture-derived primary path to be read, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceReadsSymbolAnchorFirst(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Update Model.bootstrapImplementationEvidence in @internal/app/app.go.",
		implementationTargetPacket{
			PrimaryPaths: []string{"internal/app/app.go"},
			Symbols: []semantic.SymbolMatch{{
				Path:      "internal/app/app.go",
				Name:      "bootstrapImplementationEvidence",
				Receiver:  "Model",
				Line:      1614,
				StartLine: 1614,
				EndLine:   1710,
			}},
		},
		nil,
	)
	if len(bootstrap.results) == 0 {
		t.Fatal("expected symbol-anchored bootstrap evidence")
	}
	foundRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/app.go") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Fatalf("expected symbol bootstrap evidence to include a read_file result, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceReadsAnalogyPath(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Update @internal/app/app.go to behave like @internal/llama/client.go.",
		implementationTargetPacket{
			PrimaryPaths: []string{"internal/app/app.go"},
			AnalogyPaths: []string{"internal/llama/client.go"},
		},
		nil,
	)
	foundAnalogyRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/llama/client.go") {
			foundAnalogyRead = true
			break
		}
	}
	if !foundAnalogyRead {
		t.Fatalf("expected analogy read in bootstrap results, got %#v", bootstrap.results)
	}
}

func TestBootstrapImplementationEvidenceReadsMultiplePrimaryPaths(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	m := &Model{}

	bootstrap := m.bootstrapImplementationEvidence(
		context.Background(),
		mgr,
		"Please update @config.json and @config.example.json together.",
		implementationTargetPacket{PrimaryPaths: []string{"config.json", "config.example.json"}},
		nil,
	)
	readCount := 0
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && (strings.Contains(result.Summary, "config.json") || strings.Contains(result.Summary, "config.example.json")) {
			readCount++
		}
	}
	if readCount < 2 {
		t.Fatalf("expected bootstrap to read both explicit primary files, got %#v", bootstrap.results)
	}
}

func TestImplementTargetGuardBlocksBroadDiscoveryBeforeTargetUse(t *testing.T) {
	guard := newImplementTargetGuard(nil, implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}}, nil)
	result := guard.PreExecute(workspace.ToolRequest{Tool: "list_files", Path: "."})
	if result == nil {
		t.Fatal("expected target guard to block broad list_files before target consumption")
	}
	if !strings.Contains(result.Error, "target files") {
		t.Fatalf("unexpected target-guard error: %q", result.Error)
	}
}

func TestImplementTargetGuardAllowsTargetReadAndThenBroadDiscovery(t *testing.T) {
	guard := newImplementTargetGuard(nil, implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}}, nil)
	if result := guard.PreExecute(workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"}); result != nil {
		t.Fatalf("expected target read to pass, got %#v", result)
	}
	if result := guard.PreExecute(workspace.ToolRequest{Tool: "list_files", Path: "."}); result != nil {
		t.Fatalf("expected broad discovery after consuming target, got %#v", result)
	}
}

func TestImplementTargetGuardBlocksRedundantFullFileReadAfterBootstrap(t *testing.T) {
	guard := newImplementTargetGuard(
		nil,
		implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/app.go lines 469-478",
		}},
	)
	result := guard.PreExecute(workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"})
	if result == nil {
		t.Fatal("expected bootstrapped full-file reread to be blocked")
	}
	if !strings.Contains(result.Error, "focused read of this file") {
		t.Fatalf("unexpected reread error: %q", result.Error)
	}
}

func TestImplementTargetGuardRedirectsBroadDiscoveryToTargetRead(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	guard := newImplementTargetGuard(mgr, implementationTargetPacket{
		PrimaryPaths: []string{"internal/app/app.go"},
		Anchors:      []implementationTarget{{Path: "internal/app/app.go", Line: 2112}},
	}, nil)

	result := guard.PreExecute(workspace.ToolRequest{Tool: "list_files", Path: "."})
	if result == nil {
		t.Fatal("expected redirected read result")
	}
	if result.Error != "" {
		t.Fatalf("expected successful redirected read, got %#v", result)
	}
	if result.Tool != "read_file" {
		t.Fatalf("expected redirected tool to be read_file, got %q", result.Tool)
	}
	if !strings.Contains(result.Summary, "redirected list_files to read_file") {
		t.Fatalf("unexpected redirected summary: %q", result.Summary)
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Fatalf("expected redirected read output to be non-empty, got %#v", result)
	}
}

func TestImplementTargetGuardRedirectsBroadSearchToScopedSearchContent(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	guard := newImplementTargetGuard(mgr, implementationTargetPacket{
		PrimaryPaths: []string{"internal/app/app.go"},
	}, nil)

	result := guard.PreExecute(workspace.ToolRequest{
		Tool:    "search_files",
		Path:    ".",
		Pattern: "runImplementAgent",
	})
	if result == nil {
		t.Fatal("expected redirected scoped search result")
	}
	if result.Error != "" {
		t.Fatalf("expected successful redirected search, got %#v", result)
	}
	if result.Tool != "search_content" {
		t.Fatalf("expected redirected tool to be search_content, got %q", result.Tool)
	}
	if !strings.Contains(result.Summary, "redirected search_files to search_content") {
		t.Fatalf("unexpected redirected search summary: %q", result.Summary)
	}
	if !strings.Contains(result.Output, "runImplementAgent") {
		t.Fatalf("expected redirected search output to mention runImplementAgent, got %q", result.Output)
	}
}

func TestFocusedRewriteWindowUsesBootstrappedReadSnippet(t *testing.T) {
	path, startLine, endLine, snippet, ok := focusedRewriteWindow(
		implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}},
		implementationBootstrap{
			results: []workspace.ToolResult{{
				Tool:    "read_file",
				Summary: "Read f1 internal/app/app.go lines 469-478",
				Output:  "469:\t\tserverState = m.spinner.View() + \" booting\"\n470:\t}\n471:\n472:\theader := titleStyle.Render(m.cfg.AppName) +",
			}},
		},
	)
	if !ok {
		t.Fatal("expected focused rewrite window to be extracted from bootstrap results")
	}
	if path != "internal/app/app.go" || startLine != 469 || endLine != 478 {
		t.Fatalf("unexpected focused rewrite window: %s %d-%d", path, startLine, endLine)
	}
	if !strings.Contains(snippet, "header := titleStyle.Render") {
		t.Fatalf("unexpected focused rewrite snippet: %q", snippet)
	}
}

func TestParseStructuredExecutionPlanParsesFencedJSON(t *testing.T) {
	reply := "```json\n{\"summary\":\"apply banner tweak\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"internal/app/app.go\",\"start_line\":475,\"end_line\":475,\"content\":\"header += \\\"x\\\"\",\"reason\":\"tweak banner\"}]}\n```"
	plan, err := parseStructuredExecutionPlan(reply)
	if err != nil {
		t.Fatalf("expected structured execution plan to parse, got %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected one structured op, got %#v", plan.Operations)
	}
	if plan.Operations[0].Path != "internal/app/app.go" {
		t.Fatalf("unexpected structured execution path: %#v", plan.Operations[0])
	}
}

func TestValidateStructuredExecutionEditRejectsNonTargetPath(t *testing.T) {
	packet := implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}}
	result := validateStructuredExecutionEdit(packet, workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "internal/llama/client.go",
		StartLine: 10,
		EndLine:   10,
		Content:   "replacement",
	})
	if result == nil {
		t.Fatal("expected non-target structured edit to be rejected")
	}
	if !strings.Contains(result.Error, "target packet primary files") {
		t.Fatalf("unexpected validation error: %q", result.Error)
	}
}

func TestValidateStructuredExecutionPlanRejectsOverBroadStructuralPlan(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go", "internal/workspace/manager.go"},
		StrategyHints: []string{"extract a narrower workspace interface before moving responsibilities"},
	}
	plan := structuredExecutionPlan{
		Operations: []structuredExecutionEdit{
			{Tool: "edit_file", Path: "internal/app/app.go", Reason: "extract interface boundary"},
			{Tool: "edit_file", Path: "internal/workspace/manager.go", Reason: "separate responsibilities"},
			{Tool: "edit_file", Path: "internal/agent/runner.go", Reason: "move orchestration"},
		},
	}
	if err := validateStructuredExecutionPlan(packet, plan); err == nil {
		t.Fatal("expected structural plan with >2 operations to be rejected")
	}
}

func TestValidateStructuredExecutionPlanRejectsMissingStructuralReason(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go"},
		StrategyHints: []string{"split orchestration/state handling from helper and rendering concerns"},
	}
	plan := structuredExecutionPlan{
		Operations: []structuredExecutionEdit{
			{Tool: "edit_file", Path: "internal/app/app.go", Reason: "update code"},
		},
	}
	if err := validateStructuredExecutionPlan(packet, plan); err == nil {
		t.Fatal("expected structural plan without structural reason to be rejected")
	}
}

func TestValidateStructuredExecutionPlanAcceptsGroundedStructuralReason(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go"},
		StrategyHints: []string{"split orchestration/state handling from helper and rendering concerns"},
	}
	plan := structuredExecutionPlan{
		Operations: []structuredExecutionEdit{
			{Tool: "edit_file", Path: "internal/app/app.go", Reason: "split orchestration and rendering responsibilities into a narrower boundary"},
		},
	}
	if err := validateStructuredExecutionPlan(packet, plan); err != nil {
		t.Fatalf("expected grounded structural plan to be accepted, got %v", err)
	}
}

func TestValidateStructuredExecutionPlanRejectsUnstagedMultiFileStructuralPlan(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go", "internal/workspace/manager.go"},
		StrategyHints: []string{"split orchestration/state handling from helper and rendering concerns"},
	}
	plan := structuredExecutionPlan{
		Operations: []structuredExecutionEdit{
			{Tool: "edit_file", Path: "internal/app/app.go", Reason: "split orchestration responsibilities"},
			{Tool: "edit_file", Path: "internal/workspace/manager.go", Reason: "separate workspace responsibilities"},
		},
	}
	if err := validateStructuredExecutionPlan(packet, plan); err == nil {
		t.Fatal("expected unstaged multi-file structural plan to be rejected")
	}
}

func TestValidateStructuredExecutionPlanAllowsExplicitCrossFileStructuralStage(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go", "internal/workspace/manager.go"},
		StrategyHints: []string{"extract a cross-file workspace interface between app and workspace manager"},
	}
	plan := structuredExecutionPlan{
		Operations: []structuredExecutionEdit{
			{Tool: "edit_file", Path: "internal/app/app.go", Reason: "extract cross-file interface boundary for app orchestration"},
			{Tool: "edit_file", Path: "internal/workspace/manager.go", Reason: "move workspace boundary behind the extracted interface"},
		},
	}
	if err := validateStructuredExecutionPlan(packet, plan); err != nil {
		t.Fatalf("expected explicit cross-file structural stage to be accepted, got %v", err)
	}
}

func TestBuildStructuralRefactorStageRecordsNextStage(t *testing.T) {
	packet := implementationTargetPacket{
		SourceLane:    "architecture",
		PrimaryPaths:  []string{"internal/app/app.go", "internal/workspace/manager.go"},
		StrategyHints: []string{"split orchestration/state handling from helper and rendering concerns"},
	}
	verify := implementationVerification{
		coverage:   "targeted package verification",
		confidence: "high",
	}
	stage := buildStructuralRefactorStage(packet, []string{"internal/app/app.go"}, verify)
	if stage == nil {
		t.Fatal("expected structural refactor stage")
	}
	if stage.Stage != 1 {
		t.Fatalf("expected stage 1, got %#v", stage)
	}
	if stage.FocusPath != "internal/app/app.go" {
		t.Fatalf("unexpected focus path: %#v", stage)
	}
	if !containsString(stage.Remaining, "internal/workspace/manager.go") {
		t.Fatalf("expected remaining path, got %#v", stage.Remaining)
	}
	if !strings.Contains(stage.NextStep, "internal/workspace/manager.go") {
		t.Fatalf("expected next step to mention remaining path, got %q", stage.NextStep)
	}
	if stage.Verification != "high (targeted package verification)" {
		t.Fatalf("unexpected verification summary: %q", stage.Verification)
	}
}

func TestComposeImplementationSummaryIncludesRefactorStage(t *testing.T) {
	stage := &structuralRefactorStage{
		Active:    true,
		Stage:     1,
		FocusPath: "internal/app/app.go",
		Remaining: []string{"internal/workspace/manager.go"},
		NextStep:  "review verification, then continue the structural refactor with internal/workspace/manager.go",
	}
	summary := composeImplementationSummary(
		"Applied stage one.",
		[]string{"internal/app/app.go"},
		implementationVerification{},
		stage,
	)
	for _, want := range []string{"Refactor stage:", "stage: 1", "remaining: internal/workspace/manager.go", "next: review verification"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected implementation summary to include %q, got:\n%s", want, summary)
		}
	}
}

func TestComposeImplementationSummaryIncludesVerificationRiskContract(t *testing.T) {
	summary := composeImplementationSummary(
		"Applied bounded edit.",
		[]string{"internal/app/app.go"},
		implementationVerification{
			commands:         []string{"go build ./...", "go test -vet=off ./internal/app"},
			results:          []workspace.ToolResult{{Tool: "shell"}, {Tool: "shell"}},
			coverage:         "targeted package verification",
			confidence:       "medium",
			riskLevel:        "high",
			expectedFailures: []string{"compile errors", "routing or prompt assembly regressions"},
			repairPolicy:     "repair targeted failures, then consider full-suite escalation if behavior changed",
		},
		nil,
	)
	for _, want := range []string{
		"Verification:",
		"confidence: medium (targeted package verification)",
		"risk: high",
		"expected failure modes: compile errors, routing or prompt assembly regressions",
		"repair policy: repair targeted failures",
		"`go test -vet=off ./internal/app`: passed",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected implementation summary to include %q, got:\n%s", want, summary)
		}
	}
}

func TestFinalAnswerCriticRequiresImplementationSections(t *testing.T) {
	verify := implementationVerification{
		commands:   []string{"go test ./internal/app"},
		results:    []workspace.ToolResult{{Tool: "shell"}},
		riskLevel:  "high",
		confidence: "medium",
	}
	critic := critiqueImplementationFinalAnswer("Applied bounded edit.", []string{"internal/app/app.go"}, verify)
	if critic.Passed {
		t.Fatalf("expected critic to reject missing sections, got %#v", critic)
	}
	joined := strings.Join(critic.Findings, "\n")
	for _, want := range []string{"missing changed files section", "missing verification section", "missing verification command", "missing verification risk"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected critic finding %q, got %#v", want, critic.Findings)
		}
	}
}

func TestFinalAnswerCriticRepairsUnsafeImplementationSummary(t *testing.T) {
	verify := implementationVerification{
		commands:  []string{"go test ./internal/app"},
		results:   []workspace.ToolResult{{Tool: "shell", Error: "failed"}},
		failed:    true,
		riskLevel: "high",
	}
	model := &Model{}
	repaired := model.applyFinalAnswerCritic(
		"All tests passed.",
		[]string{"internal/app/app.go"},
		verify,
		nil,
	)
	for _, want := range []string{"Changed files:", "internal/app/app.go", "Verification:", "`go test ./internal/app`: failed", "risk: high"} {
		if !strings.Contains(repaired, want) {
			t.Fatalf("expected repaired summary to include %q, got:\n%s", want, repaired)
		}
	}
	if strings.Contains(repaired, "All tests passed") {
		t.Fatalf("expected unsafe model claim to be removed, got:\n%s", repaired)
	}
	if !critiqueImplementationFinalAnswer(repaired, []string{"internal/app/app.go"}, verify).Passed {
		t.Fatalf("expected repaired summary to pass critic, got:\n%s", repaired)
	}
}

func TestLooksLikeResumeStructuralRefactorPrompt(t *testing.T) {
	if !looksLikeResumeStructuralRefactorPrompt("continue the refactor") {
		t.Fatal("expected continue refactor prompt to be recognized")
	}
	if !looksLikeResumeStructuralRefactorPrompt("resume the next architecture stage") {
		t.Fatal("expected resume architecture stage prompt to be recognized")
	}
	if looksLikeResumeStructuralRefactorPrompt("continue explaining this function") {
		t.Fatal("did not expect generic continue prompt to resume refactor")
	}
}

func TestBuildStructuralRefactorResumePromptAndSummary(t *testing.T) {
	stage := &structuralRefactorStage{
		Stage:     1,
		FocusPath: "internal/app/app.go",
		Changed:   []string{"internal/app/app.go"},
		Remaining: []string{"internal/workspace/manager.go"},
		Strategy:  []string{"extract a narrower workspace interface before moving responsibilities"},
		NextStep:  "review verification, then continue the structural refactor with internal/workspace/manager.go",
	}
	prompt := buildStructuralRefactorResumePrompt(stage, "continue")
	for _, want := range []string{
		"stage 2",
		"Previous stage focused on @internal/app/app.go",
		"Next bounded target: @internal/workspace/manager.go",
		"Edit the next bounded target first",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected resume prompt to include %q, got:\n%s", want, prompt)
		}
	}
	summary := buildStructuralRefactorResumeAnalysisSummary(stage)
	for _, want := range []string{"Refactor stage: 2", "internal/workspace/manager.go", "Suggested direction"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected resume summary to include %q, got:\n%s", want, summary)
		}
	}
	packet := buildImplementationTargetPacket(prompt, appendAnalysisSummary(nil, "architecture", summary))
	if packet.RefactorStage != 2 {
		t.Fatalf("expected resumed packet stage 2, got %#v", packet)
	}
	if !containsString(packet.PrimaryPaths, "internal/workspace/manager.go") {
		t.Fatalf("expected resumed packet target, got %#v", packet.PrimaryPaths)
	}
}

func TestPersistedStructuralRefactorStageSurvivesNewModel(t *testing.T) {
	root := t.TempDir()
	saveDir := t.TempDir()
	cfg := config.Default()
	cfg.Chat.SaveDir = saveDir
	cfg.Workspace = config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	}
	mgr := workspace.NewManager(cfg.Workspace)
	m := &Model{cfg: cfg, workspace: mgr}
	stage := &structuralRefactorStage{
		Stage:     1,
		FocusPath: "internal/app/app.go",
		Changed:   []string{"internal/app/app.go"},
		Remaining: []string{"internal/workspace/manager.go"},
		Strategy:  []string{"extract a narrower workspace interface before moving responsibilities"},
		NextStep:  "review verification, then continue the structural refactor with internal/workspace/manager.go",
	}

	m.persistStructuralRefactorStage(stage)
	if _, err := os.Stat(m.structuralRefactorStageStatePath()); err != nil {
		t.Fatalf("expected persisted refactor stage file: %v", err)
	}

	reloaded := &Model{cfg: cfg, workspace: workspace.NewManager(cfg.Workspace)}
	reloaded.loadPersistedStructuralRefactorStage()
	if !reloaded.canResumeStructuralRefactor() {
		t.Fatal("expected persisted refactor stage to be resumable")
	}
	if got := reloaded.currentStructuralRefactorStage(); got == nil || got.Stage != 1 || !containsString(got.Remaining, "internal/workspace/manager.go") {
		t.Fatalf("unexpected persisted refactor stage: %#v", got)
	}
}

func TestPersistedStructuralRefactorStageIgnoresOtherWorkspace(t *testing.T) {
	saveDir := t.TempDir()
	cfg := config.Default()
	cfg.Chat.SaveDir = saveDir
	cfg.Workspace = config.Workspace{
		Root:         t.TempDir(),
		CWD:          t.TempDir(),
		EnableWrites: true,
	}
	m := &Model{cfg: cfg, workspace: workspace.NewManager(cfg.Workspace)}
	m.persistStructuralRefactorStage(&structuralRefactorStage{
		Stage:     1,
		FocusPath: "internal/app/app.go",
		Remaining: []string{"internal/workspace/manager.go"},
		NextStep:  "review verification, then continue the structural refactor with internal/workspace/manager.go",
	})

	otherCfg := cfg
	otherRoot := t.TempDir()
	otherCfg.Workspace.Root = otherRoot
	otherCfg.Workspace.CWD = otherRoot
	reloaded := &Model{cfg: otherCfg, workspace: workspace.NewManager(otherCfg.Workspace)}
	reloaded.loadPersistedStructuralRefactorStage()
	if reloaded.canResumeStructuralRefactor() {
		t.Fatalf("did not expect refactor stage from another workspace to load: %#v", reloaded.currentStructuralRefactorStage())
	}
}

func TestPersistedStructuralRefactorStageClearsNonResumableStage(t *testing.T) {
	saveDir := t.TempDir()
	cfg := config.Default()
	cfg.Chat.SaveDir = saveDir
	cfg.Workspace = testWorkspaceConfig()
	m := &Model{cfg: cfg, workspace: workspace.NewManager(cfg.Workspace)}

	m.persistStructuralRefactorStage(&structuralRefactorStage{
		Stage:     1,
		FocusPath: "internal/app/app.go",
		Remaining: []string{"internal/workspace/manager.go"},
		NextStep:  "review verification, then continue the structural refactor with internal/workspace/manager.go",
	})
	m.persistStructuralRefactorStage(&structuralRefactorStage{
		Stage:     2,
		FocusPath: "internal/workspace/manager.go",
		NextStep:  "review verification, then decide whether another bounded structural stage is needed",
	})
	if _, err := os.Stat(m.structuralRefactorStageStatePath()); !os.IsNotExist(err) {
		t.Fatalf("expected non-resumable stage to clear persisted file, stat err=%v", err)
	}
}

func TestValidateStructuredExecutionEditAcceptsTargetedLineEdit(t *testing.T) {
	packet := implementationTargetPacket{PrimaryPaths: []string{"internal/app/app.go"}}
	result := validateStructuredExecutionEdit(packet, workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "internal/app/app.go",
		StartLine: 10,
		EndLine:   10,
		Content:   "replacement",
	})
	if result != nil {
		t.Fatalf("expected target structured edit to pass, got %#v", result)
	}
}

func TestValidateFocusedStructuredExecutionEditAcceptsWindowScopedLineEdit(t *testing.T) {
	result := validateFocusedStructuredExecutionEdit(
		"internal/app/app.go",
		471,
		489,
		"header := titleStyle.Render(...)\nreturn lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)",
		workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      "internal/app/app.go",
			StartLine: 489,
			EndLine:   489,
			Content:   "return lipgloss.JoinVertical(lipgloss.Left, header, \"\", body, m.input.View(), footer)",
		},
	)
	if result != nil {
		t.Fatalf("expected focused structured edit to pass, got %#v", result)
	}
}

func TestValidateFocusedStructuredExecutionEditRejectsOutOfWindowLineEdit(t *testing.T) {
	result := validateFocusedStructuredExecutionEdit(
		"internal/app/app.go",
		471,
		489,
		"header := titleStyle.Render(...)\nreturn lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)",
		workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      "internal/app/app.go",
			StartLine: 490,
			EndLine:   490,
			Content:   "replacement",
		},
	)
	if result == nil {
		t.Fatal("expected out-of-window focused structured edit to be rejected")
	}
	if !strings.Contains(result.Error, "must stay within lines 471-489") {
		t.Fatalf("unexpected focused structured edit error: %q", result.Error)
	}
}

func TestValidateFocusedStructuredExecutionEditRejectsOldTextOutsideSnippet(t *testing.T) {
	result := validateFocusedStructuredExecutionEdit(
		"internal/app/app.go",
		471,
		489,
		"header := titleStyle.Render(...)\nreturn lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)",
		workspace.ToolRequest{
			Tool:    "edit_file",
			Path:    "internal/app/app.go",
			OldText: "missing marker",
			NewText: "replacement",
		},
	)
	if result == nil {
		t.Fatal("expected focused structured edit with missing old_text to be rejected")
	}
	if !strings.Contains(result.Error, "old_text must appear in the bootstrapped window") {
		t.Fatalf("unexpected focused structured edit error: %q", result.Error)
	}
}

func TestPreflightStructuredExecutionRequestRejectsInvalidGoEdit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write temp go file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	err := preflightStructuredExecutionRequest(mgr, workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "main.go",
		StartLine: 4,
		EndLine:   4,
		Content:   "\tprintln(\"hi\"\n",
	})
	if err == nil {
		t.Fatal("expected invalid go edit to fail preflight syntax validation")
	}
	if !strings.Contains(err.Error(), "preflight syntax validation failed") {
		t.Fatalf("unexpected preflight error: %v", err)
	}
}

func TestPreflightStructuredExecutionRequestAcceptsValidGoEdit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write temp go file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	err := preflightStructuredExecutionRequest(mgr, workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "main.go",
		StartLine: 4,
		EndLine:   4,
		Content:   "\tfmt.Println(\"hi\")\n",
	})
	if err != nil {
		t.Fatalf("expected valid go edit to pass preflight, got %v", err)
	}
}

func TestPreflightStructuredExecutionPlanRejectsBrokenMultiFileGoPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc a() {\n\tprintln(\"a\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main\n\nfunc b() {\n\tprintln(\"b\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	err := preflightStructuredExecutionPlan(mgr, []workspace.ToolRequest{
		{Tool: "edit_file", Path: "a.go", StartLine: 4, EndLine: 4, Content: "\tprintln(\"a\")\n"},
		{Tool: "edit_file", Path: "b.go", StartLine: 4, EndLine: 4, Content: "\tprintln(\"b\"\n"},
	})
	if err == nil {
		t.Fatal("expected broken multi-file go plan to fail preflight")
	}
	if !strings.Contains(err.Error(), "preflight syntax validation failed") {
		t.Fatalf("unexpected multi-file preflight error: %v", err)
	}
}

func TestPreflightStructuredExecutionPlanAcceptsBoundedMultiFilePlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{\n  \"max_tool_steps\": 8\n}\n"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.example.json"), []byte("{\n  \"max_tool_steps\": 8\n}\n"), 0o644); err != nil {
		t.Fatalf("write config.example.json: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	err := preflightStructuredExecutionPlan(mgr, []workspace.ToolRequest{
		{Tool: "edit_file", Path: "config.json", OldText: "\"max_tool_steps\": 8", NewText: "\"max_tool_steps\": 9"},
		{Tool: "edit_file", Path: "config.example.json", OldText: "\"max_tool_steps\": 8", NewText: "\"max_tool_steps\": 9"},
	})
	if err != nil {
		t.Fatalf("expected bounded multi-file plan to pass preflight, got %v", err)
	}
}

func TestConfigFieldUpdateTarget(t *testing.T) {
	field, value, ok := configFieldUpdateTarget("Please update @config.json and @config.example.json so max_tool_steps is 9 in both files.")
	if !ok {
		t.Fatal("expected config field update target to be detected")
	}
	if field != "max_tool_steps" || value != "9" {
		t.Fatalf("unexpected config field target: %q %q", field, value)
	}
}

func TestReplaceJSONFieldLine(t *testing.T) {
	replacement, ok := replaceJSONFieldLine(`  "max_tool_steps": 8,`, "max_tool_steps", "9")
	if !ok {
		t.Fatal("expected json field line replacement")
	}
	if replacement != "  \"max_tool_steps\": 9,\n" {
		t.Fatalf("unexpected replacement: %q", replacement)
	}
}

func TestPreflightStructuredExecutionRequestRejectsInvalidJSONEdit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{\n  \"max_tool_steps\": 8\n}\n"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	err := preflightStructuredExecutionRequest(mgr, workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "config.json",
		StartLine: 2,
		EndLine:   2,
		Content:   "  \"max_tool_steps\":,\n",
	})
	if err == nil {
		t.Fatal("expected invalid json edit to fail preflight")
	}
	if !strings.Contains(err.Error(), "preflight JSON validation failed") {
		t.Fatalf("unexpected json preflight error: %v", err)
	}
}

func TestLooksLikeUISpacingPrompt(t *testing.T) {
	if !looksLikeUISpacingPrompt("Can we add a small bit of padding underneath the banner?") {
		t.Fatal("expected banner padding prompt to be recognized as a UI spacing request")
	}
	if looksLikeUISpacingPrompt("Please rename the banner state variable.") {
		t.Fatal("did not expect non-spacing banner prompt to match")
	}
}

func TestDirectUISpacingReplacementWrapsBodyLine(t *testing.T) {
	line, replacement, ok := directUISpacingReplacement("474:\theader := titleStyle.Render(m.cfg.AppName) +\n476:\tbody := m.viewport.View()\n491:\treturn lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)", 474, 491)
	if !ok {
		t.Fatal("expected direct UI spacing replacement to find the body line")
	}
	if line != 476 {
		t.Fatalf("expected body line 476, got %d", line)
	}
	if !strings.Contains(replacement, "MarginTop(1).Render(m.viewport.View())") {
		t.Fatalf("unexpected UI spacing replacement: %q", replacement)
	}
}

func TestParseUnusedVariableDiagnostics(t *testing.T) {
	verify := implementationVerification{
		results: []workspace.ToolResult{{
			Tool:   "shell",
			Error:  "command failed: exit status 1",
			Output: "$ go build ./...\n# qwen-bubbles/internal/app\ninternal/app/app.go:460:2: declared and not used: workingStyle",
		}},
		failed: true,
	}

	diagnostics := parseUnusedVariableDiagnostics(verify)
	if len(diagnostics) != 1 {
		t.Fatalf("expected one unused-variable diagnostic, got %#v", diagnostics)
	}
	if diagnostics[0].Path != "internal/app/app.go" || diagnostics[0].Line != 460 || diagnostics[0].Name != "workingStyle" {
		t.Fatalf("unexpected unused-variable diagnostic: %#v", diagnostics[0])
	}
}

func TestLooksLikeUnusedVariableDeclaration(t *testing.T) {
	if !looksLikeUnusedVariableDeclaration("workingStyle := lipgloss.NewStyle()", "workingStyle") {
		t.Fatal("expected := declaration to be recognized as removable unused variable")
	}
	if !looksLikeUnusedVariableDeclaration("var workingStyle = lipgloss.NewStyle()", "workingStyle") {
		t.Fatal("expected var declaration to be recognized as removable unused variable")
	}
	if looksLikeUnusedVariableDeclaration("header += workingStyle.Render(...)", "workingStyle") {
		t.Fatal("did not expect usage line to be treated as removable declaration")
	}
}

func TestLooksLikeSingleLineConditionalWrapper(t *testing.T) {
	if !looksLikeSingleLineConditionalWrapper("if m.streaming {", "header += workingStyle.Render(...)", "}") {
		t.Fatal("expected single-line conditional wrapper to be recognized")
	}
	if looksLikeSingleLineConditionalWrapper("header := titleStyle.Render(...)", "header += workingStyle.Render(...)", "}") {
		t.Fatal("did not expect non-conditional prefix to be recognized as wrapper")
	}
}

func TestMainWriteGuardBlocksMutatingShellInGuardedMode(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newMainWriteGuard(mgr, "Create a new file shell-note.txt containing the text hello by using shell commands only.")
	result := guard.PreExecute(workspace.ToolRequest{
		Tool:    "shell",
		Command: `echo "hello" > shell-note.txt`,
	})
	if result == nil {
		t.Fatal("expected guarded main lane to block mutating shell command")
	}
	if !strings.Contains(result.Error, "shell-only mutations") {
		t.Fatalf("expected shell-only guard error, got %q", result.Error)
	}
}

func TestImplementWriteGuardBlocksShellOnlyWriteFile(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newImplementWriteGuard(mgr, "Create a new file shell-note.txt containing the text hello by using shell commands only.")
	result := guard.PreExecute(workspace.ToolRequest{
		Tool:    "write_file",
		Path:    "shell-note.txt",
		Content: "hello\n",
	})
	if result == nil {
		t.Fatal("expected guarded implement lane to block write_file for shell-only request")
	}
	if !strings.Contains(result.Error, "shell-only mutation request") {
		t.Fatalf("expected shell-only guarded error, got %q", result.Error)
	}
}

func TestBuildWriteApprovalProposalForProtectedPath(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newImplementWriteGuard(mgr, "Edit scripts/run-harness.sh to add a comment.")
	req := workspace.ToolRequest{
		Tool: "edit_file",
		Path: "scripts/run-harness.sh",
	}
	blocked := guard.PreExecute(req)
	if blocked == nil {
		t.Fatal("expected protected path edit to be blocked in guarded mode")
	}

	proposal := buildWriteApprovalProposal(context.Background(), mgr, "Edit scripts/run-harness.sh to add a comment.", req, guard, *blocked)
	if proposal == nil {
		t.Fatal("expected protected path block to produce a write approval proposal")
	}
	if proposal.Kind != writeApprovalProtectedPath {
		t.Fatalf("expected protected path proposal, got %q", proposal.Kind)
	}
	if len(proposal.Paths) != 1 || proposal.Paths[0] != "scripts/run-harness.sh" {
		t.Fatalf("unexpected proposal paths: %#v", proposal.Paths)
	}
	if len(proposal.ImpactSummary) == 0 {
		t.Fatal("expected protected path proposal to include impact summary lines")
	}
	if proposal.Risk.rank() < writeApprovalRiskHigh.rank() {
		t.Fatalf("expected protected path script proposal to be high risk, got %q", proposal.Risk)
	}
	if !strings.Contains(proposal.renderSummary(), "Assessed risk:") {
		t.Fatal("expected rendered approval summary to include risk section")
	}
	if len(proposal.DeepAnalysis) == 0 {
		t.Fatal("expected protected path proposal to include deep analysis at high risk")
	}
	if len(proposal.ApprovalNotes) == 0 {
		t.Fatal("expected protected path proposal to include approval guidance at high risk")
	}
	rendered := proposal.renderSummary()
	if !strings.Contains(rendered, "Deep analysis:") {
		t.Fatal("expected rendered summary to include deep analysis section")
	}
	if !strings.Contains(rendered, "Approval guidance:") {
		t.Fatal("expected rendered summary to include approval guidance section")
	}
	if len(proposal.VerifySteps) == 0 {
		t.Fatal("expected protected path proposal to include verification guidance")
	}
	if !containsString(proposal.VerifySteps, "bash -n scripts/run-harness.sh") {
		t.Fatalf("expected shell syntax verification, got %#v", proposal.VerifySteps)
	}
	if proposal.VerifyRisk == "" {
		t.Fatal("expected protected path proposal to include verification risk")
	}
	if !containsString(proposal.VerifyExpected, "shell syntax errors") {
		t.Fatalf("expected shell verification failure mode, got %#v", proposal.VerifyExpected)
	}
	if strings.TrimSpace(proposal.VerifyRepair) == "" {
		t.Fatal("expected protected path proposal to include verification repair policy")
	}
	rendered = proposal.renderSummary()
	for _, want := range []string{"Verification risk contract:", "expected failure modes:", "repair policy:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered approval summary to include %q, got:\n%s", want, rendered)
		}
	}
}

func TestBuildWriteApprovalProposalForProtectedPathLivePromptIncludesDeepSections(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "In scripts/run-harness.sh, change \"set -euo pipefail\" to \"set -euo pipefail # approval packet smoke test\" and verify."
	guard := newImplementWriteGuard(mgr, prompt)
	req := workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "scripts/run-harness.sh",
		StartLine: 2,
		EndLine:   2,
		Content:   "set -euo pipefail # approval packet smoke test",
	}
	blocked := guard.PreExecute(req)
	if blocked == nil {
		t.Fatal("expected protected live prompt edit to be blocked in guarded mode")
	}

	proposal := buildWriteApprovalProposal(context.Background(), mgr, prompt, req, guard, *blocked)
	if proposal == nil {
		t.Fatal("expected live protected path prompt to produce a write approval proposal")
	}
	rendered := proposal.renderSummary()
	if !strings.Contains(rendered, "Deep analysis:") {
		t.Fatalf("expected live prompt summary to include deep analysis, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Approval guidance:") {
		t.Fatalf("expected live prompt summary to include approval guidance, got:\n%s", rendered)
	}
}

func TestBuildWriteApprovalProposalForProtectedWriteSetIncludesFullScope(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	prompt := "Please update @config.json and @config.example.json so max_tool_steps is 9 in both files."
	guard := newImplementWriteGuard(mgr, prompt)

	proposal := buildWriteApprovalProposalForPaths(
		context.Background(),
		mgr,
		prompt,
		"edit_file",
		[]string{"config.json", "config.example.json"},
		guard,
	)
	if proposal == nil {
		t.Fatal("expected protected multi-file write set to produce a write approval proposal")
	}
	if proposal.Kind != writeApprovalProtectedPath {
		t.Fatalf("expected protected path proposal, got %q", proposal.Kind)
	}
	if len(proposal.Paths) != 2 || !containsString(proposal.Paths, "config.json") || !containsString(proposal.Paths, "config.example.json") {
		t.Fatalf("expected full config scope in proposal, got %#v", proposal.Paths)
	}
	rendered := proposal.renderSummary()
	if !strings.Contains(rendered, "- config.json") || !strings.Contains(rendered, "- config.example.json") {
		t.Fatalf("expected rendered summary to include both protected paths, got:\n%s", rendered)
	}
}

func TestBuildWriteApprovalProposalForRequestsIncludesEditPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{\n  \"max_tool_steps\": 8,\n  \"other\": true\n}\n"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.example.json"), []byte("{\n  \"max_tool_steps\": 8,\n  \"other\": true\n}\n"), 0o644); err != nil {
		t.Fatalf("write config.example.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module approvalpreview\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         dir,
		CWD:          dir,
		EnableShell:  true,
		EnableWrites: true,
	})
	prompt := "Please update @config.json and @config.example.json so max_tool_steps is 9 in both files."
	guard := newImplementWriteGuard(mgr, prompt)
	reqs := []workspace.ToolRequest{
		{Tool: "edit_file", Path: "config.json", StartLine: 2, EndLine: 2, Content: `  "max_tool_steps": 9,` + "\n"},
		{Tool: "edit_file", Path: "config.example.json", StartLine: 2, EndLine: 2, Content: `  "max_tool_steps": 9,` + "\n"},
	}
	proposal := buildWriteApprovalProposalForRequests(context.Background(), mgr, prompt, guard, reqs)
	if proposal == nil {
		t.Fatal("expected request-scoped approval proposal")
	}
	if len(proposal.PlannedEdits) != 2 {
		t.Fatalf("expected edit previews for both files, got %#v", proposal.PlannedEdits)
	}
	if len(proposal.PreviewHunks) != 2 {
		t.Fatalf("expected diff previews for both files, got %#v", proposal.PreviewHunks)
	}
	rendered := proposal.renderSummary()
	if !strings.Contains(rendered, "Proposed edits:") {
		t.Fatalf("expected rendered summary to include proposed edits, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Diff preview:") || !strings.Contains(rendered, "```diff") {
		t.Fatalf("expected rendered summary to include diff previews, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Verification confidence:") {
		t.Fatalf("expected rendered summary to include verification confidence, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "config.json:2-2") || !strings.Contains(rendered, "config.example.json:2-2") {
		t.Fatalf("expected rendered summary to include both edit previews, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "@@ -2,1 +2,1 @@") {
		t.Fatalf("expected rendered summary to include hunk headers, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "-  \"max_tool_steps\": 8,") || !strings.Contains(rendered, "+  \"max_tool_steps\": 9,") {
		t.Fatalf("expected rendered summary to include line-level diff content, got:\n%s", rendered)
	}
}

func TestBuildWriteApprovalProposalForBroadScope(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newImplementWriteGuard(mgr, "Refactor these files together.")
	guard.writePaths["internal/app/app.go"] = struct{}{}
	guard.writePaths["internal/agent/runner.go"] = struct{}{}
	guard.writePaths["internal/workspace/manager.go"] = struct{}{}
	req := workspace.ToolRequest{
		Tool: "edit_file",
		Path: "README.md",
	}
	blocked := guard.PreExecute(req)
	if blocked == nil {
		t.Fatal("expected fourth file edit to be blocked in guarded mode")
	}

	proposal := buildWriteApprovalProposal(context.Background(), mgr, "Refactor these files together.", req, guard, *blocked)
	if proposal == nil {
		t.Fatal("expected broad scope block to produce a write approval proposal")
	}
	if proposal.Kind != writeApprovalBroadScope {
		t.Fatalf("expected broad scope proposal, got %q", proposal.Kind)
	}
	if len(proposal.Paths) != 4 {
		t.Fatalf("expected four approved paths, got %#v", proposal.Paths)
	}
	if len(proposal.VerifySteps) == 0 {
		t.Fatal("expected broad scope proposal to include verification guidance")
	}
	if proposal.Risk.rank() < writeApprovalRiskHigh.rank() {
		t.Fatalf("expected broad scope proposal to be high risk or above, got %q", proposal.Risk)
	}
	if len(proposal.ApprovalNotes) == 0 {
		t.Fatal("expected broad scope proposal to include approval guidance")
	}
}

func TestWriteApprovalGrantAllowsExactApprovedPathOnly(t *testing.T) {
	proposal := writeApprovalProposal{
		Prompt: "Edit scripts/run-harness.sh to add a comment.",
		Tool:   "edit_file",
		Kind:   writeApprovalProtectedPath,
		Paths:  []string{"scripts/run-harness.sh"},
	}
	grant := proposal.grant()
	guard := &implementWriteGuard{
		policy:     workspace.WritePolicyGuarded,
		writePaths: make(map[string]struct{}),
		grant:      grant,
	}

	if blocked := guard.guardPathScope("edit_file", "scripts/run-harness.sh"); blocked != nil {
		t.Fatalf("expected approved protected path to be allowed, got %v", blocked)
	}
	if blocked := guard.guardPathScope("edit_file", "scripts/other.sh"); blocked == nil {
		t.Fatal("expected unapproved sibling path to remain blocked")
	}
	if blocked := guard.guardPathScope("write_file", "scripts/run-harness.sh"); blocked == nil {
		t.Fatal("expected different tool kind on approved path to remain blocked")
	}
}

func TestBuildWriteApprovalProposalIncludesGoBlastRadius(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newImplementWriteGuard(mgr, "Edit internal/app/app.go around runImplementAgent and verify.")
	guard.writePaths["internal/agent/runner.go"] = struct{}{}
	guard.writePaths["internal/workspace/manager.go"] = struct{}{}
	guard.writePaths["internal/config/config.go"] = struct{}{}
	req := workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      "internal/app/app.go",
		StartLine: 1031,
		EndLine:   1031,
	}
	blocked := guard.PreExecute(req)
	if blocked == nil {
		t.Fatal("expected broad guarded write scope to be blocked")
	}

	proposal := buildWriteApprovalProposal(context.Background(), mgr, "Edit internal/app/app.go around runImplementAgent and verify.", req, guard, *blocked)
	if proposal == nil {
		t.Fatal("expected Go-file proposal")
	}
	if len(proposal.BlastRadius) == 0 {
		t.Fatal("expected Go-file proposal to include blast-radius lines")
	}
	if len(proposal.AffectedTests) == 0 {
		t.Fatal("expected Go-file proposal to include affected test surfaces")
	}
	if len(proposal.DeepAnalysis) == 0 {
		t.Fatal("expected Go-file proposal to include deep analysis at high risk")
	}
	if !containsString(proposal.ChangeKinds, "core-go-code") {
		t.Fatalf("expected Go-file proposal to include core-go-code classification, got %#v", proposal.ChangeKinds)
	}
}

func TestBuildWriteApprovalProposalForPromptFileIncludesBaselineRisk(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())

	guard := newImplementWriteGuard(mgr, "Edit prompts/base.md to change the baseline behavior wording.")
	req := workspace.ToolRequest{
		Tool: "edit_file",
		Path: "prompts/base.md",
	}
	blocked := guard.PreExecute(req)
	if blocked == nil {
		t.Fatal("expected prompt file edit to be blocked in guarded mode")
	}

	proposal := buildWriteApprovalProposal(context.Background(), mgr, "Edit prompts/base.md to change the baseline behavior wording.", req, guard, *blocked)
	if proposal == nil {
		t.Fatal("expected prompt edit to produce a proposal")
	}
	if proposal.Risk.rank() < writeApprovalRiskHigh.rank() {
		t.Fatalf("expected prompt proposal to be high risk, got %q", proposal.Risk)
	}
	if !containsString(proposal.ChangeKinds, "baseline-behavior") {
		t.Fatalf("expected baseline-behavior change class, got %#v", proposal.ChangeKinds)
	}
	if len(proposal.AffectedTests) == 0 {
		t.Fatal("expected prompt proposal to include affected test surfaces")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func testContainsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func testContainsPrefixFold(items []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item)), target) {
			return true
		}
	}
	return false
}
