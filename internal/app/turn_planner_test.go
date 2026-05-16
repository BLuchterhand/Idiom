package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestFallbackTurnRouteRecognizesDebugLogs(t *testing.T) {
	plan := fallbackTurnRoute("Check our most recent logs and explain why it failed.", agent.BuildMode())
	if plan.Family != "debug_logs" {
		t.Fatalf("family = %q, want debug_logs", plan.Family)
	}
	if plan.InitialLane != "explore" || plan.Contract != "grounded_explanation" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanRoutesNonForensicDebugLogFeatureQuestionToArchitecture(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "What does the debug log redacted mode do?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsArchitectureForProjectFeatureQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "architecture"}, "What does the debug log redacted mode do?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanUsesFallbackInsteadOfHardcodedDirectQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "Run the tests and report failures.", agent.BuildMode())
	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}
	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesCodeExplanationAsArchitecture(t *testing.T) {
	plan := fallbackTurnRoute("How does this harness use sub-agents?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesGDScriptEdits(t *testing.T) {
	plan := fallbackTurnRoute("Update the panel script in ui/status_panel.gd.", agent.BuildMode())
	if plan.Family != "gdscript_edit" {
		t.Fatalf("family = %q, want gdscript_edit", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesStartWorkingOnNamedDocAsDocsEdit(t *testing.T) {
	plan := fallbackTurnRoute("can you please start working on docs/writer-route-decision-report-only-plan.md?", agent.BuildMode())
	if plan.Family != "docs_edit" {
		t.Fatalf("family = %q, want docs_edit", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanDowngradesImplementationLikeExplanationQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Explain how this harness uses sub-agents.", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestRunPromptOnceDispatchesStartWorkingPlanDocToPlanExecute(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | replace fixture text |",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var chatRequests atomic.Int64
	var plannerPromptRequests atomic.Int64
	var sawRequiredScope atomic.Bool
	var sawReviewPrompt atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}
			chatRequests.Add(1)
			requestText := string(body)
			reply := ""
			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				if strings.Contains(requestText, `"enable_thinking":false`) {
					t.Fatalf("review request should not inherit candidate-scope recovery thinking override:\n%s", requestText)
				}
				sawReviewPrompt.Store(true)
				reply = "Decision: pass\n\nVerification Checklist:\n- target.txt contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				if strings.Contains(requestText, `"enable_thinking":false`) {
					t.Fatalf("initial in-process plan-execute request forced thinking off:\n%s", requestText)
				}
				plannerPromptRequests.Add(1)
				if !strings.Contains(requestText, "replace fixture text") {
					t.Fatalf("planner request did not include referenced plan document body:\n%s", requestText)
				}
				for _, want := range []string{
					"Required `submit_plan` coverage for this run",
					"## Files to Modify",
					"- target.txt",
				} {
					if !strings.Contains(requestText, want) {
						t.Fatalf("planner request missing required edit-scope contract %q:\n%s", want, requestText)
					}
				}
				for _, forbidden := range []string{
					"Allowed `submit_plan` edit scope for this run",
					"No required_edit_paths list is active",
					"## Candidate Edit Scope From Target References",
				} {
					if strings.Contains(requestText, forbidden) {
						t.Fatalf("planner request still used candidate-scope contract %q:\n%s", forbidden, requestText)
					}
				}
				sawRequiredScope.Store(true)
				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Update target.txt from the referenced plan document.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace the target fixture text"}]}`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	thinkingOn := true
	cfg.Chat.EnableThinking = &thinkingOn
	cfg.Chat.PreserveThinking = true
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	answer, err := model.RunPromptOnce(context.Background(), "can you please start working on docs/plan.md?")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}
	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new\n" {
		t.Fatalf("target content = %q, want new line", string(got))
	}
	planArtifact := loadOnlyTimestampedPlanArtifactForTest(t, root)
	if planArtifact.SchemaVersion != workspace.PlanArtifactSchemaVersion {
		t.Fatalf("plan artifact schema = %q, want %q", planArtifact.SchemaVersion, workspace.PlanArtifactSchemaVersion)
	}
	if planArtifact.SessionID == "" {
		t.Fatalf("plan artifact should carry the plan-execute session id")
	}
	if len(planArtifact.Edits) != 1 || planArtifact.Edits[0].Path != "target.txt" {
		t.Fatalf("plan artifact edits = %#v, want one target.txt edit", planArtifact.Edits)
	}
	if !strings.Contains(answer, "plan-then-execute summary") {
		t.Fatalf("expected plan-execute summary answer, got %q", answer)
	}
	if plannerPromptRequests.Load() != 1 {
		t.Fatalf("expected one initial planner request, got %d", plannerPromptRequests.Load())
	}
	if !sawRequiredScope.Load() {
		t.Fatalf("expected TUI plan-doc scope to become active required_edit_paths")
	}
	if !sawReviewPrompt.Load() {
		t.Fatalf("expected review-repair prompt after executor phase")
	}
	if chatRequests.Load() < 2 {
		t.Fatalf("expected planner and review chat requests, got %d", chatRequests.Load())
	}
}

func loadOnlyTimestampedPlanArtifactForTest(t *testing.T, root string) workspace.PlanArtifact {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "*.plan.json"))
	if err != nil {
		t.Fatalf("glob plan artifacts: %v", err)
	}
	var stamped []string
	for _, path := range paths {
		if filepath.Base(path) == "last.plan.json" {
			continue
		}
		stamped = append(stamped, path)
	}
	if len(stamped) != 1 {
		t.Fatalf("expected one timestamped plan artifact under .idiom/plans, got %d: %v", len(stamped), stamped)
	}
	artifact, err := workspace.LoadPlanArtifact(stamped[0])
	if err != nil {
		t.Fatalf("load plan artifact %s: %v", stamped[0], err)
	}
	return artifact
}

func TestComposeImplementationBriefPromotesPlanDocumentScopeToRequiredPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	planDoc := strings.Join([]string{
		"# Handoff",
		"",
		"## Files to Modify",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | Do the important thing |",
		"",
		"## Implementation Steps",
		"",
		"1. Preserve this body text for the planner.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "handoff.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{"docs/handoff.md"}),
	}
	brief := composeImplementationBrief("start working on docs/handoff.md", plan, cfg)
	if !strings.Contains(brief, "## Files to Modify\n\n- target.txt") {
		t.Fatalf("brief missing active required edit scope:\n%s", brief)
	}
	for _, want := range []string{
		"## Referenced Plan Documents",
		"### docs/handoff.md",
		"> ## Files to Modify",
		"> | target.txt | Do the important thing |",
		"> 1. Preserve this body text for the planner.",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	if got := requiredEditPathsFromPrompt(brief); !slices.Contains(got, "target.txt") {
		t.Fatalf("plan document scope must become active required edit paths, got %#v\n%s", got, brief)
	}
}

func runtimeBriefForTest(targetRefs []string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{TargetRefs: targetRefs}
}

func TestCorrectTurnRoutePlanUpgradesVerificationOnlyWithEditIntent(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "verification_only"}, "Modify exactly one file and do not run tests.", agent.BuildMode())
	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteKeepsEditAtomWithAcceptanceCommandsAsImplementation(t *testing.T) {
	prompt := `
# IMPLEMENTATION REQUEST - EDIT FILES

The user explicitly requests repository file edits using apply_patch, edit_file,
or write_file.

## Files To Modify

| File | What |
|---|---|
| src/simulation/job_queue.cpp | Post/cancel loader jobs. |

## Required Behavior

- Modify the implementation so loader jobs are deduplicated.

## Acceptance

` + "```bash" + `
python3 scripts/run_tests.py --build-only
./build/tests/job_queue_tests --case JobQueue
git diff --check
` + "```" + `
`
	plan := fallbackTurnRoute(prompt, agent.BuildMode())
	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsAnalysisContinuationReadOnly(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Continue your investigation until you have a confident response.", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.FollowupLane != "" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestShouldPreferFallbackRoutePreventsModelDowngradeToDirectQuestion(t *testing.T) {
	fallback := fallbackTurnRoute("Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?", agent.BuildMode())
	if fallback.Family != "debug_logs" {
		t.Fatalf("fallback family = %q, want debug_logs", fallback.Family)
	}
	planned := normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, agent.BuildMode())
	if !shouldPreferFallbackRoute(fallback, planned) {
		t.Fatal("expected specialized fallback to override a generic direct-question model route")
	}
}

func TestClassifyTurnRouteLetsModelUpgradeDirectQuestionFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"verification_only","initial_lane":"main","reason":"verification_request","contract":"direct_answer"}`},
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
	plan := m.classifyTurnRoute(context.Background(), "Can you verify that this still builds?", agent.BuildMode())
	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}
	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestClassifyTurnRouteSkipsModelForDecisiveFallback(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"direct_question"}`},
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
	plan := m.classifyTurnRoute(context.Background(), "Update the README docs for the new workflow.", agent.BuildMode())
	if plan.Family != "docs_edit" {
		t.Fatalf("family = %q, want docs_edit", plan.Family)
	}
	if calls.Load() != 0 {
		t.Fatalf("deterministic route should skip classifier model call, got %d calls", calls.Load())
	}
}

func TestTurnRoutePlanRuntimeBriefForDirectQuestionUsesReadOnlyWorkspace(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "edit_file", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "direct_question"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	if len(brief.AllowedTools) == 0 {
		t.Fatalf("direct question should advertise read-only tools, got %#v", brief.AllowedTools)
	}
	if len(brief.ReadinessGate) != 0 || !slices.Contains(brief.FinalCritic, "answers_actual_user_request") {
		t.Fatalf("expected direct question to use no readiness gate and a final answer critic, got %#v", brief)
	}
}

func TestTurnRoutePlanRuntimeBriefForVerificationOnly(t *testing.T) {
	available := []string{"task", "pwd", "read_file", "search_files", "search_content", "shell", "edit_file", "write_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "verification_only"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_verification_shell" {
		t.Fatalf("permission = %q, want read_only_verification_shell", brief.PermissionProfile)
	}
	if !strings.Contains(strings.Join(brief.AllowedTools, ","), "shell") {
		t.Fatalf("expected shell in allowed tools, got %#v", brief.AllowedTools)
	}
	for _, tool := range brief.AllowedTools {
		if tool == "edit_file" || tool == "write_file" || tool == "pwd" {
			t.Fatalf("unexpected write tool %q in allowed tools %#v", tool, brief.AllowedTools)
		}
	}
	if brief.VerificationContract != "not_required_for_read_only_lane" {
		t.Fatalf("verification contract = %q", brief.VerificationContract)
	}
}

func TestTurnRoutePlanRuntimeBriefForDebugLogsOmitsPwd(t *testing.T) {
	available := []string{"pwd", "read_file", "search_files", "search_content", "edit_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "debug_logs"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	for _, tool := range brief.AllowedTools {
		if tool == "pwd" || tool == "edit_file" {
			t.Fatalf("unexpected tool %q in debug_logs allowed tools %#v", tool, brief.AllowedTools)
		}
	}
	if !strings.Contains(strings.Join(brief.AllowedTools, ","), "search_files") {
		t.Fatalf("expected search_files in allowed tools, got %#v", brief.AllowedTools)
	}
	if len(brief.ReadinessGate) == 0 || len(brief.FinalCritic) == 0 {
		t.Fatalf("expected debug logs to keep operational gates, got %#v", brief)
	}
}

func TestTurnRoutePlanRuntimeBriefForArchitectureUsesShapingOnly(t *testing.T) {
	available := []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "architecture"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	if len(brief.ReadinessGate) == 0 || len(brief.FinalCritic) == 0 {
		t.Fatalf("expected architecture analysis to keep a closure gate and final critic, got %#v", brief)
	}
}
