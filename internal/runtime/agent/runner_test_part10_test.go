package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestEvidenceFirstTargetReadRefreshHasNoWindowBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 221-360", Output: "doc excerpt 2"},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	})
	if !ok {
		t.Fatal("expected third open-ended target read to be redirected to the next narrow window")
	}

	if redirected.StartLine != 361 || redirected.EndLine != 500 {
		t.Fatalf("unexpected follow-up redirect request: %#v", redirected)
	}

	if _, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "docs/frontier-routing-implementation-plan.md",
		StartLine: 240,
		EndLine:   260,
	}); ok {
		t.Fatal("expected precise target read to remain allowed after prior target windows")
	}
}

func TestEvidenceFirstTargetInspectionEncouragesWritesWithoutReadBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}

	if !evidenceFirstTargetInspectionActive(brief, evidence) {
		t.Fatal("expected target inspection guidance to activate after target evidence")
	}

	if redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	}); !ok || redirected.StartLine != 360 {
		t.Fatalf("expected further target reads to stay available as bounded follow-ups, got %#v ok=%v", redirected, ok)
	}
}

func TestRunnerBriefWithPhaseHintsPromotesTargetInspectionPhase(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	results := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}

	live := runnerBriefWithPhaseHints(brief, results)
	if got := evidenceFirstMutationPhase(live, results); got != "target_inspection" {
		t.Fatalf("phase = %q, want target_inspection", got)
	}

	required := strings.Join(live.RequiredNext, "\n")
	if !strings.Contains(required, "make the edit in the named target once the change is supported") {
		t.Fatalf("expected target-inspection write guidance, got %#v", live.RequiredNext)
	}

	invalid := strings.Join(live.InvalidActions, "\n")
	if !strings.Contains(invalid, "source discovery outside the named target during target inspection") {
		t.Fatalf("expected target-inspection invalid action guidance, got %#v", live.InvalidActions)
	}
}

func TestRunnerPhaseHintsUseCapabilityLanguageForPromptSurfaces(t *testing.T) {
	debugLogSelection := workspace.ToolResult{
		Tool:   "search_files",
		Output: "f1 logs/conversations/2026-05-13.jsonl\n",
	}
	debugLogRead := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/2026-05-13.jsonl lines 1-80",
		Output:  "decision trace",
	}
	nonTargetEvidence := workspace.ToolResult{
		Tool:    "go_read_symbol",
		Summary: "Read Worker.resolve in internal/surfaces/tui/app/worker.go lines 10-40",
		Output:  "func resolve() {}",
	}
	targetEvidence := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read target.go lines 1-80",
		Output:  "package target",
	}

	cases := []struct {
		name    string
		brief   workerruntime.WorkerRuntimeBrief
		results []workspace.ToolResult
	}{
		{
			name:    "debug log tail",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
			results: []workspace.ToolResult{debugLogSelection},
		},
		{
			name:    "selected log analysis",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
			results: []workspace.ToolResult{debugLogSelection, debugLogRead},
		},
		{
			name:  "source evidence",
			brief: workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
		},
		{
			name:    "target entry",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
			results: []workspace.ToolResult{nonTargetEvidence},
		},
		{
			name:    "target inspection",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
			results: []workspace.ToolResult{nonTargetEvidence, targetEvidence},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := runnerBriefWithPhaseHints(tc.brief, tc.results)
			promptSurface := strings.Join(append(append(append([]string{}, live.RequiredNext...), live.InvalidActions...), live.SearchPolicy...), "\n")

			for _, forbidden := range []string{
				"read the newest selected log tail " + "now",
				"one targeted " + "search_content",
				"using " + "search_content" + " or a narrow " + "read_file",
				"another blind " + "read_file",
				"open-ended " + "read_file",
			} {
				if strings.Contains(promptSurface, forbidden) {
					t.Fatalf("phase hint prompt surface still contains tool strategy prescription %q:\n%s", forbidden, promptSurface)
				}
			}

			for _, rawTool := range []string{"search_content", "read_file"} {
				if strings.Contains(promptSurface, rawTool) {
					t.Fatalf("phase hint prompt surface should use capability/contract language, found %q in:\n%s", rawTool, promptSurface)
				}
			}
		})
	}
}

func TestRunnerPromptSourceDoesNotContainRemovedToolPrescriptions(t *testing.T) {
	removedPromptSurfacePrescriptions := []string{
		"read the newest selected log tail " + "now",
		"prefer one " + "discovery_query first",
		"otherwise stay inside the selected log with one targeted " + "search_content before answering",
		"continue reading only for a named missing target fact, using " + "search_content or a narrow " + "read_file scoped to the named target",
	}
	rawProfileToolMenuTerms := []string{
		"code_navigate",
		"*_find_symbol",
		"_find_symbol",
		"*_read_symbol",
		"_read_symbol",
		"*_references",
		"_references",
		"read_file",
		"project wrappers",
	}

	checks := []struct {
		path      string
		forbidden []string
	}{
		{path: filepath.Join(agentTestRepoRoot(t), "prompts", "profiles", "claude-qwen-worker.md"), forbidden: rawProfileToolMenuTerms},
	}

	runnerSources, err := filepath.Glob(filepath.Join(agentTestRepoRoot(t), "internal", "runtime", "agent", "runner*.go"))
	if err != nil {
		t.Fatalf("glob runner sources: %v", err)
	}

	if len(runnerSources) == 0 {
		t.Fatal("expected runner source files")
	}

	for _, path := range runnerSources {
		checks = append(checks, struct {
			path      string
			forbidden []string
		}{path: path, forbidden: removedPromptSurfacePrescriptions})
	}

	for _, check := range checks {
		t.Run(check.path, func(t *testing.T) {
			body, err := os.ReadFile(check.path)
			if err != nil {
				t.Fatalf("read %s: %v", check.path, err)
			}

			source := string(body)
			for _, forbidden := range check.forbidden {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s still contains removed prompt-surface prescription %q", check.path, forbidden)
				}
			}
		})
	}
}

func TestRunnerBriefWithPhaseHintsPromotesRequiredPathWriteReadiness(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/system.cpp", "native/tests/test_system.cpp"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
			Path:    "native/src/system.cpp",
			Content: "anchor",
		}},
		RequiredEvidence:     []string{"required_edit_paths_covered_or_blocked"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	live := runnerBriefWithPhaseHints(brief, nil)
	if !slices.Contains(live.Constraints, "implementation_packet_write_ready") {
		t.Fatalf("expected write-ready constraint, got %#v", live.Constraints)
	}

	required := strings.Join(live.RequiredNext, "\n")
	if !strings.Contains(required, "resolve a required path") ||
		!strings.Contains(required, "semantic/context/declared project discovery") ||
		!strings.Contains(required, "exact path-specific blocker") {

		t.Fatalf("expected scoped required-path guidance, got %#v", live.RequiredNext)
	}

	invalid := strings.Join(live.InvalidActions, "\n")
	if !strings.Contains(invalid, "orientation read/search") || !strings.Contains(invalid, "outside required edit paths") {
		t.Fatalf("expected scoped-read invalid action, got %#v", live.InvalidActions)
	}

	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/system.cpp"},
	}

	live = runnerBriefWithPhaseHints(brief, results)

	if !slices.Contains(live.Constraints, "implementation_required_paths_remaining") {
		t.Fatalf("expected remaining-path constraint, got %#v", live.Constraints)
	}

	if !strings.Contains(strings.Join(live.RequiredNext, "\n"), "remaining required path") {
		t.Fatalf("expected remaining-path guidance, got %#v", live.RequiredNext)
	}
}

func TestRunnerBriefWithPhaseHintsOmitsWriteReadyConstraintWithoutAnchors(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/system.cpp"},
		RequiredEvidence:     []string{"required_edit_paths_covered_or_blocked"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	live := runnerBriefWithPhaseHints(brief, nil)
	if slices.Contains(live.Constraints, "implementation_packet_write_ready") {
		t.Fatalf("write-ready constraint should be omitted when brief lacks anchors, got %#v", live.Constraints)
	}

	if runtimeBriefDefinesWriteReadyReadContract(live) {
		t.Fatalf("write-ready read contract should be inactive without anchors, brief = %#v", live)
	}
}

func TestBuildRunnerNextActionStateIncludesEvidenceFirstPhaseHints(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		Objective:  "Explore the routing flow and update the plan doc",
	}
	results := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}
	req := workspace.ToolRequest{Tool: "list_files", Path: "internal/surfaces/tui/app"}
	result := workspace.ToolResult{
		Tool:    "list_files",
		Summary: "Listed 38 path(s).",
	}

	state := buildRunnerNextActionState("main", 17, "tool_result", req, result, results, false, brief)
	if state.Phase != "target_inspection" {
		t.Fatalf("phase = %q, want target_inspection", state.Phase)
	}

	actions := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(actions, "make the edit in the named target once the change is supported") {
		t.Fatalf("expected target-inspection edit guidance, got %#v", state.NextValidActions)
	}

	invalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(invalid, "source discovery outside the named target during target inspection") {
		t.Fatalf("expected target-inspection invalid guidance, got %#v", state.InvalidNextActions)
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesDiffHidingAndReleaseCommands(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	deniedShell := []string{
		"git add README.md",
		"git commit -m child",
		"git reset --hard",
		"git push origin HEAD",
		"gh pr create",
		"gh pr merge 1",
		"gh release create v1",
		"npm publish",
		"pnpm publish",
		"docker push image",
		"kubectl apply -f deploy.yaml",
	}

	for _, command := range deniedShell {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("task decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}); decision.Decision != PermissionAllow {
		t.Fatalf("edit_file decision = %q, want allow", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "go test ./..."}); decision.Decision != PermissionAllow {
		t.Fatalf("go test decision = %q, want allow", decision.Decision)
	}
}

func TestWorkbranchChildTaskPermissionPolicyDefaultDeniesUnknownTools(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	if decision := policy.Decide(workspace.ToolRequest{Tool: "unknown_tool", Path: "."}); decision.Decision != PermissionDeny {
		t.Fatalf("unknown tool decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "rm -rf /tmp/x"}); decision.Decision != PermissionDeny {
		t.Fatalf("arbitrary shell decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "git clone https://example.com/repo"}); decision.Decision != PermissionDeny {
		t.Fatalf("git clone decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "git push"}); decision.Decision != PermissionDeny {
		t.Fatalf("git push decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "git statusfoo"}); decision.Decision != PermissionDeny {
		t.Fatalf("git statusfoo decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "pwdx"}); decision.Decision != PermissionDeny {
		t.Fatalf("pwdx decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "cat"}); decision.Decision != PermissionDeny {
		t.Fatalf("bare cat decision = %q, want deny", decision.Decision)
	}
}

func TestWorkbranchChildTaskPermissionPolicyAllowsStructuredWriteTools(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	writeTools := []string{
		"edit_file",
		"write_file",
		"apply_patch",
		"mkdir",
		"append_file",
		"go_move_declarations",
	}

	for _, tool := range writeTools {
		if decision := policy.Decide(workspace.ToolRequest{Tool: tool, Path: "test.go"}); decision.Decision != PermissionAllow {
			t.Fatalf("%q decision = %q, want allow", tool, decision.Decision)
		}
	}
}

func TestWorkbranchChildTaskPermissionPolicyAllowsSafeVerificationShell(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	safeCommands := []string{
		"go test ./...",
		"go list ./...",
		"git status",
		"git diff",
		"git show HEAD",
		"git grep func",
		"pwd",
		"ls -la",
		"rg pattern",
		"grep -r foo .",
		"cat README.md",
		"head -20 file.go",
		"tail -20 file.go",
		"wc -l file.go",
	}

	for _, command := range safeCommands {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionAllow {
			t.Fatalf("%q decision = %q, want allow", command, decision.Decision)
		}
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesGitCParentEscape(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	parentEscapeCommands := []string{
		"git -C /workspace/idiom status",
		"git -C /some/other/repo diff",
		"git -C ../parent status",
		"git -C / status",
	}

	for _, command := range parentEscapeCommands {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesChainedShellEscapes(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	commands := []string{
		"go test ./... && rm -rf /tmp/workbranch-escape",
		"git status; git push origin HEAD",
		"git status\nrm -rf /tmp/workbranch-escape",
		"go test ./...\ntouch /tmp/workbranch-escape",
		"git diff | sh",
		"git show HEAD > /tmp/patch",
		"cat README.md $(touch /tmp/workbranch-escape)",
		"grep foo README.md `touch /tmp/workbranch-escape`",
	}

	for _, command := range commands {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesMutatingAllowedCommandOptions(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	commands := []string{
		"go env -w GOPROXY=off",
		"go env\t-w GOPROXY=off",
		"go env -u GOPROXY",
		"go env  -u GOPROXY",
		"go env GOPATH",
		"find . -delete",
		"find . -exec touch /tmp/workbranch-escape {} +",
		"awk 'BEGIN { system(\"touch /tmp/workbranch-escape\") }'",
		"sed -n -i '1p' file.go",
		"git diff --output=/tmp/patch",
		"git show HEAD --output=/tmp/patch",
	}

	for _, command := range commands {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesNestedDelegation(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("nested task decision = %q, want deny", decision.Decision)
	}

	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "explore more"}); decision.Decision != PermissionDeny {
		t.Fatalf("nested explore task decision = %q, want deny", decision.Decision)
	}
}

func TestRunnerExecutesWorkbranchWriteChildTaskWithoutMutatingParent(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")

	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	replies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change README greeting\"}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting with child-authored version\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
		"Parent inspected child patch.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	var childPatch string

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate isolated write"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "task" {
				childPatch = event.Result.Output
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read parent README: %v", err)
	}

	if string(data) != "hello\n" {
		t.Fatalf("parent workspace was mutated: %q", data)
	}

	if !strings.Contains(childPatch, "README.md") || !strings.Contains(childPatch, "hello from child") {
		t.Fatalf("expected child patch output, got:\n%s", childPatch)
	}
}

func TestRunnerRoutesWorkbranchChildOntoImplementerClient(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")

	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	// Planner replies: delegate, then final summary after child returns.
	plannerReplies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change greeting\"}\n```",
		"Parent inspected child patch.",
	}
	plannerHits := 0
	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if plannerHits >= len(plannerReplies) {
			t.Fatalf("planner received unexpected extra request")
		}

		reply := plannerReplies[plannerHits]
		plannerHits++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer plannerServer.Close()

	// Implementer replies: drive the child through ledger, read, edit, final.
	implReplies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
	}
	implHits := 0
	implServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if implHits >= len(implReplies) {
			t.Fatalf("implementer received unexpected extra request")
		}

		reply := implReplies[implHits]
		implHits++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer implServer.Close()

	runner := &Runner{
		Client:            llama.NewClient(plannerServer.URL, 2*time.Second),
		ImplementerClient: llama.NewClient(implServer.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	if plannerHits != len(plannerReplies) {
		t.Fatalf("planner hit count = %d, want %d", plannerHits, len(plannerReplies))
	}

	if implHits == 0 {
		t.Fatal("expected implementer client to receive child-task requests, got none")
	}
}

func TestRunnerReplaysFirstRepeatedToolRequestThenRejectsExactLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"Stopped after repeated loops.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		replayedSummary string
		rejectedSummary string
		budgetEvent     string
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 2,
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "budget_ledger":
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayedSummary = event.Result.Summary
			}

			if strings.Contains(event.Result.Summary, "Repeated tool request rejected") {
				rejectedSummary = event.Result.Summary
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// DOOM_LOOP guard (threshold=3) trips on the third identical pwd call,
	// records the rejection, then forces a no-tools closeout so callers do
	// not receive an empty final answer.
	if requests < 3 {
		t.Fatalf("expected at least 3 model requests before doom-loop guard, got %d", requests)
	}

	if replayedSummary == "" {
		t.Fatal("expected second identical pwd to replay prior evidence before the guard trips")
	}

	_ = rejectedSummary
	_ = answer

	var ledger runnerBudgetLedger

	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}

	if ledger.StopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %#v", ledger)
	}

	if answer != "Stopped after repeated loops." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

func TestRepeatedSearchFilesRejectsAfterReplayBeforeDoomLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/input.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"search_files","pattern":"input","path":"."}`,
		`{"tool":"search_files","pattern":"input","path":"."}`,
		`{"tool":"search_files","pattern":"input","path":"."}`,
		"Stopped after exact search replay rejection.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		replayed bool
		rejected bool
		events   []RunEvent
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "find input"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "search_files" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}

			if event.Result.Tool == "search_files" && strings.Contains(event.Result.Summary, "Repeated tool request rejected") {
				rejected = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !replayed {
		t.Fatal("expected the second exact search_files request to replay prior evidence")
	}

	if !rejected {
		t.Fatal("expected the third exact search_files request to reject the exhausted replay")
	}

	for _, event := range events {
		if event.Role == "doom_loop_guard" || strings.Contains(event.Content, "doom_loop_guard") {
			t.Fatalf("exact search replay exhaustion should reject before doom-loop approval path, events=%v", eventRolesFor(events))
		}
	}

	if answer != "Stopped after exact search replay rejection." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected three tool requests plus final answer, got %d", requests)
	}
}

func TestReadOnlyDirectAnswerClosesAfterAnswerableExactSearchReplay(t *testing.T) {
	root := t.TempDir()
	docPath := "internal/documentviewer/document-viewer.go"

	if err := writeTestFile(root, docPath, strings.Join([]string{
		"package documentviewer",
		"",
		"const DocumentViewerBehavior = \"document viewer renders approved design documents\"",
	}, "\n")); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		fmt.Sprintf(`{"tool":"read_file","path":%q}`, docPath),
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		events   []RunEvent
		replayed bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What does internal/documentviewer/document-viewer.go say about the document viewer?",
			AllowedTools:      []string{"read_file", "search_files"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What does internal/documentviewer/document-viewer.go say about the document viewer?"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "search_files" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !replayed && requests != 0 {
		t.Fatalf("expected exact search replay or prefetch closeout; events=%s", formatRunEventsForTest(events))
	}

	// With prefetch, the host-owned closeout may answer directly (0 requests)
	// from the prefetched evidence instead of needing search replay.
	if requests != 3 && requests != 0 {
		t.Fatalf("expected 0 (prefetch closeout) or 3 (replay) requests; got %d; events=%s", requests, formatRunEventsForTest(events))
	}

	if !strings.Contains(answer, docPath) ||
		!strings.Contains(strings.ToLower(answer), "document viewer") ||
		!strings.Contains(answer, "approved design documents") {

		t.Fatalf("host closeout answer should preserve the inspected document evidence, got:\n%s", answer)
	}

	for _, event := range events {
		if event.Role != "budget_ledger" {
			continue
		}

		var ledger runnerBudgetLedger

		if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
			t.Fatalf("decode budget ledger %q: %v", event.Content, err)
		}

		if ledger.StopReason != "host_read_only_closeout" && ledger.StopReason != "read_only_prefetch_host_closeout" {
			t.Fatalf("expected host_read_only_closeout or read_only_prefetch_host_closeout, got %#v", ledger)
		}

		if ledger.RejectedActions != 0 {
			t.Fatalf("exact replay closeout should not accumulate rejected actions, got %#v", ledger)
		}

		return
	}

	t.Fatalf("expected budget ledger; events=%s", formatRunEventsForTest(events))
}

func TestRunnerRepeatedBroadInspectionStillHitsExploreGuard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"Stopped after broad-inspection guard.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 4,
		},
	}

	var (
		broadGuarded bool
		replayed     bool
	)

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(event.Result.Summary, "Additional broad inspection rejected") {
				broadGuarded = true
			}

			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Stopped after broad-inspection guard." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if !broadGuarded {
		t.Fatal("expected repeated broad inspection to hit the explore guard")
	}

	if replayed {
		t.Fatal("repeated broad inspection must not bypass explore guard through replay")
	}
}
