package app

import (
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/workspace"
)

func testDecisionModeModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	return &Model{
		workspace: workspace.NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
		}),
	}
}

func TestAgentDecisionModeForcesPlanLaneReadOnly(t *testing.T) {
	m := testDecisionModeModel(t)
	m.agentMode = agentDecisionModePlan
	plan := m.classifyPromptExecutionPlan("Please implement a refactor in internal/app/app.go.")
	if plan.lane != analysisLaneArchitecture {
		t.Fatalf("lane = %q, want architecture", plan.lane)
	}
	if plan.followupLane != "" {
		t.Fatalf("followup lane = %q, want empty read-only plan mode", plan.followupLane)
	}
	if !plan.intent.WantsPlanOnly || plan.intent.WantsEdits {
		t.Fatalf("expected plan-only intent, got %#v", plan.intent)
	}
	if plan.agentMode != agentDecisionModePlan || plan.intent.AgentMode != string(agentDecisionModePlan) {
		t.Fatalf("expected agent mode propagated, plan=%q intent=%q", plan.agentMode, plan.intent.AgentMode)
	}
}

func TestAgentDecisionModeBuildPreservesVerificationOnlyBoundary(t *testing.T) {
	m := testDecisionModeModel(t)
	m.agentMode = agentDecisionModeBuild
	plan := m.classifyPromptExecutionPlan("Run go test ./internal/app and report the result only. Do not edit files.")
	if plan.lane == analysisLaneImplement {
		t.Fatalf("build mode should not override explicit read-only verification prompt: %#v", plan)
	}
	if !containsString(plan.intent.Vetoes, "agent_build_mode_preserved_user_read_only_boundary") {
		t.Fatalf("expected read-only boundary veto, got %#v", plan.intent.Vetoes)
	}
}

func TestWorkflowCommandPromptTemplates(t *testing.T) {
	for _, name := range []string{"latest-log", "implement-plan", "verify", "frontier-audit", "opencode-audit"} {
		prompt, ok := workflowCommandPrompt(name)
		if !ok || strings.TrimSpace(prompt) == "" {
			t.Fatalf("expected workflow %q to resolve, got ok=%t prompt=%q", name, ok, prompt)
		}
	}
	if _, ok := workflowCommandPrompt("unknown"); ok {
		t.Fatal("unexpected unknown workflow template")
	}
}

func TestDurablePlanArtifactCarriesApprovalScope(t *testing.T) {
	summary := "Split internal/app/app.go into internal/app/routing.go and keep README.md as evidence."
	rendered := renderDurablePlanArtifact(
		"Refactor internal/app/app.go.",
		analysisLaneArchitecture,
		summary,
		[]string{"internal/app/app.go", "README.md"},
		"run go test ./internal/app",
	)
	for _, want := range []string{"Approval scope:", "Plan artifact schema:", "\"approval_scope\"", "allowed_destinations"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered artifact missing %q:\n%s", want, rendered)
		}
	}
	allowed := durablePlanAllowedDestinations(rendered)
	if !containsString(allowed, "internal/app/routing.go") {
		t.Fatalf("expected allowed destination from plan artifact, got %#v", allowed)
	}
	memory := &longHorizonTaskMemory{Kind: "durable_plan", Summary: rendered}
	resume := buildTaskResumeContext(memory, "implement this plan")
	if !containsString(resume.AllowedDestinations, "internal/app/routing.go") {
		t.Fatalf("expected resume context allowed destination, got %#v", resume.AllowedDestinations)
	}
}

func TestDurablePlanArtifactDoesNotPromoteEvidenceLedgerToDestinations(t *testing.T) {
	summary := strings.Join([]string{
		"Focused architecture plan.",
		"",
		"Evidence ledger:",
		"- verified paths: internal/app/analysis_agents.go, internal/eval/eval.go, internal/eval/swe_suite.go",
		"",
		"Focused refactor plan:",
		"- Target: internal/app/analysis_agents.go",
		"- Stage 1 scope: edit only this existing Go file; do not create files or move declarations.",
	}, "\n")
	rendered := renderDurablePlanArtifact(
		"Prepare a refactor plan for internal/app/analysis_agents.go.",
		analysisLaneArchitecture,
		summary,
		[]string{"internal/app/analysis_agents.go"},
		"run go test ./internal/app",
	)
	allowed := durablePlanAllowedDestinations(rendered)
	if len(allowed) != 0 {
		t.Fatalf("evidence ledger paths must not become allowed destinations, got %#v", allowed)
	}
}

func TestImplementationTargetPacketAllowsPlanApprovedDestination(t *testing.T) {
	guard := newImplementTargetGuard(nil, implementationTargetPacket{
		PrimaryPaths:        []string{"internal/app/app.go"},
		EvidencePaths:       []string{"internal/app/routing.go"},
		AllowedDestinations: []string{"internal/app/routing.go"},
		ForbiddenPaths:      []string{"internal/app/forbidden.go"},
	}, nil)
	if !guard.pathEditableAllowed("internal/app/routing.go") {
		t.Fatal("expected plan-approved destination to be editable")
	}
	if guard.pathPrimaryAllowed("internal/app/routing.go") {
		t.Fatal("plan-approved destination must not be treated as a declaration-move source")
	}
	if block := guard.blockWriteOutsideEditableTargets(workspace.ToolRequest{Tool: "go_move_declarations", Path: "internal/app/routing.go", Destination: "internal/app/new.go"}); block == nil {
		t.Fatal("expected declaration move source outside primary targets to be blocked")
	}
}
