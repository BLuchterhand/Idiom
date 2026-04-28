package app

import (
	"strings"
	"testing"

	"idiom/internal/agent"
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

func TestFallbackTurnRouteRecognizesCodeExplanationAsArchitecture(t *testing.T) {
	plan := fallbackTurnRoute("How does this harness use sub-agents?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestTurnRoutePlanRuntimeBriefForDirectQuestionUsesConversationOnly(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "edit_file", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "direct_question"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "conversation_only" {
		t.Fatalf("permission = %q, want conversation_only", brief.PermissionProfile)
	}
	if len(brief.AllowedTools) != 0 {
		t.Fatalf("direct question should not advertise tools in conversation-only mode, got %#v", brief.AllowedTools)
	}
	if len(brief.ReadinessGate) != 0 || !containsAppString(brief.FinalCritic, "answers_actual_user_request") {
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

func containsAppString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
