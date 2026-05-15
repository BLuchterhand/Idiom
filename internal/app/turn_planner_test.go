package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/llama"
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

func TestCorrectTurnRoutePlanDowngradesImplementationLikeExplanationQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Explain how this harness uses sub-agents.", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
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
