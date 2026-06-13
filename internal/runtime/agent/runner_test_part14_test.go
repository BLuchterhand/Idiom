package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerWorkspaceToolRulesDenyBeforePolicyAllow(t *testing.T) {
	root := t.TempDir()
	secret := "SECRET_SENTINEL_DO_NOT_LEAK"

	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"secret.txt\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"secret.txt\"}\n```",
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
		permissionEvent  string
		allowEvent       string
		completionStatus string
		leaked           bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: "secret.txt", Decision: "deny"},
				},
			},
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "permission_decision" {
				if strings.Contains(event.Content, `"decision":"deny"`) {
					permissionEvent = event.Content
				}

				if strings.Contains(event.Content, `"decision":"allow"`) {
					allowEvent = event.Content
				}
			}

			if event.Role == "completion_status" {
				completionStatus = event.Content
			}

			if strings.Contains(event.Content, secret) {
				leaked = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "read secret"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "workspace `tool_rules` deny `read_file`") {
		t.Fatalf("unexpected answer %q", answer)
	}

	if requests != 1 {
		t.Fatalf("workspace deny should close before a second model call, got %d requests", requests)
	}

	if permissionEvent == "" || !strings.Contains(permissionEvent, "workspace.tool_rules") {
		t.Fatalf("expected workspace tool_rules deny event, got %q", permissionEvent)
	}

	if !strings.Contains(completionStatus, `"blocker_kind":"workspace_tool_rules_deny"`) {
		t.Fatalf("expected workspace tool_rules completion status, got %q", completionStatus)
	}

	if allowEvent != "" {
		t.Fatalf("workspace deny must not emit allow permission event: %s", allowEvent)
	}

	if leaked {
		t.Fatal("deny path leaked sentinel content")
	}
}

func TestRunnerRejectsToolOutsideCurrentSurfaceBeforeExecution(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		"```tool\n{\"tool\":\"write_file\",\"path\":\"blocked.txt\",\"content\":\"should not be written\",\"description\":\"attempt disallowed write\"}\n```",
		"Blocked by the current tool surface.",
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	var rejected bool

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "answer without writes"}}, llama.ChatOptions{}, nil, nil, func(event ToolResultEvent) {
		if event.Request.Tool == "write_file" && strings.Contains(event.Result.Summary, "Tool not available in current runtime contract") {
			rejected = true
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "Blocked by the current tool surface") {
		t.Fatalf("unexpected answer %q", answer)
	}

	if !rejected {
		t.Fatal("expected runner to reject the disallowed tool at the current surface gate")
	}

	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("disallowed write executed, stat err=%v", err)
	}
}

func TestRunnerCreatesCheckpointBeforeFirstWrite(t *testing.T) {
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
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Changed README.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var checkpointEvent string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy:           RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "checkpoint" {
				checkpointEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "Changed README") {
		t.Fatalf("unexpected answer %q", answer)
	}

	var checkpoint GitCheckpoint

	if err := json.Unmarshal([]byte(checkpointEvent), &checkpoint); err != nil {
		t.Fatalf("decode checkpoint %q: %v", checkpointEvent, err)
	}

	if checkpoint.Head == "" || checkpoint.Dirty {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}
}

func TestToolResultMadeProgressClassifiesUsefulAndStalledResults(t *testing.T) {
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "read_file"}, workspace.ToolResult{Tool: "read_file", Summary: "Read file"}) {
		t.Fatal("read_file summary should count as progress")
	}

	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "task_ledger"}, workspace.ToolResult{Tool: "task_ledger", Summary: "Task ledger updated", Output: "- [completed] one: done"}) {
		t.Fatal("task_ledger output should count as progress")
	}

	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"}) {
		t.Fatal("successful declaration move should count as progress")
	}

	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "apply_patch"}, workspace.ToolResult{Tool: "apply_patch", Summary: "Applied patch with 1 op(s)"}) {
		t.Fatal("successful patch should count as progress")
	}

	if toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "No-op declaration move"}) {
		t.Fatal("no-op declaration move should not count as progress")
	}

	if toolResultMadeProgress(workspace.ToolRequest{Tool: "search_content"}, workspace.ToolResult{Tool: "search_content", Error: "failed"}) {
		t.Fatal("failed tools should not count as progress")
	}

	patchPaths := strings.Join(changedPathsFromToolResult(workspace.ToolResult{
		Tool:   "apply_patch",
		Output: "add docs/example.md (8 bytes)\nupdate internal/surfaces/tui/app/app.go\nupdate+move old.go -> new.go",
	}), "\n")
	for _, want := range []string{"docs/example.md", "internal/surfaces/tui/app/app.go", "old.go", "new.go"} {
		if !strings.Contains(patchPaths, want) {
			t.Fatalf("expected patch changed paths to include %q, got %q", want, patchPaths)
		}
	}
}

func TestSynthesizeFallbackAnswerUsesNarrowEvidenceAndFinalBlocker(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("explore", []workspace.ToolResult{
		{Tool: "list_files", Summary: "listed files", Output: "README.md\ninternal/surfaces/tui/app/app.go"},
		{Tool: "search_content", Summary: "found route", Output: "internal/surfaces/tui/app/app.go:42: route"},
		{Tool: "shell", Summary: "bad shell", Error: "not allowed"},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}

	for _, want := range []string{"Partial findings from this turn", "Verified context:", "found route", "Final blocker:", "bad shell: not allowed", "Recommended next step:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "listed files") {
		t.Fatalf("broad list_files result should not be preferred as narrowing evidence:\n%s", answer)
	}
}

func TestSynthesizeMutationFallbackAnswerDropsErrorsSupersededByLaterWrite(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}, []workspace.ToolResult{
		{
			Tool:    "edit_file",
			Summary: "Missing write description",
			Error:   "edit_file requires a description field",
		},
		{
			Tool:    "edit_file",
			Summary: "Edited src/example.h",
		},
	})

	if !strings.Contains(answer, "src/example.h") {
		t.Fatalf("expected changed path in fallback, got:\n%s", answer)
	}

	if strings.Contains(answer, "Missing write description") || strings.Contains(answer, "Blocker:") {
		t.Fatalf("expected superseded edit error to be omitted, got:\n%s", answer)
	}

	if !strings.Contains(answer, "workspace writes are still pending verification") {
		t.Fatalf("expected verification blocker to remain, got:\n%s", answer)
	}
}

func TestMutationDeterministicFallbackStartsAfterSuccessfulWrite(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	pendingResults := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited README.md"},
	}

	if !shouldPreferDeterministicNoToolsAnswer("main", brief, pendingResults) {
		t.Fatal("mutation fallback should be preferred after a successful write")
	}

	verifiedResults := append(pendingResults, workspace.ToolResult{
		Tool:    "shell",
		Summary: "Ran shell command",
		Output:  "$ git diff --check -- README.md\n",
	})
	if !shouldPreferDeterministicNoToolsAnswer("main", brief, verifiedResults) {
		t.Fatal("mutation fallback should be preferred after changed paths and successful verification")
	}
}

func TestMutationDeterministicFallbackStartsForWriteReadyRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		RequiredEditPaths:    []string{"native/tests/test_energy.cpp"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read native/src/energy.h"}}

	if !shouldPreferDeterministicNoToolsAnswer("main", brief, results) {
		t.Fatal("write-ready mutation fallback should be preferred once tools are disabled, even before a successful write")
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic no-tools answer")
	}

	for _, want := range []string{"<<QUESTION>>: Implementation incomplete", "Changed paths: none", "native/tests/test_energy.cpp"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected deterministic blocker to contain %q, got:\n%s", want, answer)
		}
	}

	runner := &Runner{RuntimeBrief: brief}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "required_edit_paths_remaining" || reason != "required_edit_paths_remaining" {
		t.Fatalf("mutation fallback should preserve required-path blocker classification, got (%q, %q):\n%s", kind, reason, answer)
	}

	if strings.Contains(answer, "not enough supported evidence") {
		t.Fatalf("mutation fallback must not be rewritten as read-only uncertainty:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackPreservesAnchorRefreshContract(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "plan_then_execute",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.txt: old_text not found in current source`, `b.txt: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read a.txt lines 1-10 (total lines: 10)"},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic plan-execute fallback")
	}

	for _, want := range []string{"Plan-execute anchor refresh is still required", "b.txt", "bounded read_file or code_navigate"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("plan-execute fallback missing %q:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "Implementation incomplete") || strings.Contains(answer, "Changed paths: none") {
		t.Fatalf("plan-execute fallback must not use direct implementation blocker:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackInfersPlannerContractFromTranscript(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.txt: old_text not found in current source`, `b.txt: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read a.txt lines 2-4 (total lines: 10)"},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic plan-execute fallback inferred from transcript")
	}

	for _, want := range []string{"Plan-execute anchor refresh is still required", "b.txt"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("inferred plan-execute fallback missing %q:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "- a.txt") {
		t.Fatalf("already refreshed path should not remain in anchor-refresh fallback:\n%s", answer)
	}

	if strings.Contains(answer, "Implementation incomplete") || strings.Contains(answer, "Changed paths: none") {
		t.Fatalf("transcript-inferred plan-execute fallback must not use direct implementation blocker:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackDoesNotInferFromReadOutputOnly(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		RequiredEditPaths:    []string{"internal/runtime/agent/runner_fallback.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_fallback.go lines 1-120 (total lines: 260)",
			Output:  "submit_plan source_satisfied_paths required edit path bounded source-body refresh",
		},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic mutation fallback")
	}

	if !strings.Contains(answer, "<<QUESTION>>: Implementation incomplete") {
		t.Fatalf("ordinary implementation task should keep mutation fallback, got:\n%s", answer)
	}

	if strings.Contains(answer, "Plan-execute") {
		t.Fatalf("read output alone must not infer plan-execute fallback:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackPreservesSubmitCorrectionContract(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "plan_then_execute",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: edit would not change b.txt; for b.txt: remove no-op edit; use `source_satisfied_paths`: [\"b.txt\"] if final; use `Decision: source-satisfied` only if all paths are final; else submit real edit",
		},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic plan-execute fallback")
	}

	for _, want := range []string{"Plan-execute submit correction is still required", "submit_plan", "source_satisfied_paths"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("plan-execute submit-correction fallback missing %q:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "Implementation incomplete") || strings.Contains(answer, "Changed paths: none") {
		t.Fatalf("plan-execute fallback must not use direct implementation blocker:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackAllowsExactBlockerWhenCorrectionBodiesUnsafe(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "plan_then_execute",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/tests/test_collapse_event.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)"},
		{Tool: "read_file", Summary: "Required new-file path does not exist yet", Error: "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence."},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{Tool: "read_file", Summary: "Read native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 7; submit a concrete state assertion or report a blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 6; submit a concrete state assertion or report a blocker",
		},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic plan-execute fallback")
	}

	for _, want := range []string{"<<QUESTION>>:", "Path-level blockers:", "native/src/core/colony_sim_tick.cpp", "native/tests/test_collapse_event.cpp", "safe executable edit body", "bounded anchor"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("plan-execute unsafe-correction fallback missing %q:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"source_satisfied_paths", "Next valid action"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("unsafe-correction fallback should produce a concrete blocker, got stale fallback text %q:\n%s", forbidden, answer)
		}
	}

	if !strings.Contains(answer, "Last rejected submit_plan:") {
		t.Fatalf("unsafe-correction fallback should not advertise source_satisfied_paths as the fallback terminal option:\n%s", answer)
	}
}

func TestPlanThenExecuteFallbackBlockerUsesCoveragePathsNotDirectoryScopes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "plan_then_execute",
		RequiredEditPaths: []string{"cmd/idiom", "cmd/idiom/product_workflow.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read cmd/idiom/product_workflow.go lines 1-120 (total lines: 240)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: line-range replacement for /repo/cmd/idiom/product_workflow.go lines 1-240 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /repo/cmd/idiom/product_workflow.go; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /repo/cmd/idiom/product_workflow.go; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
	}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic plan-execute fallback")
	}

	if !strings.Contains(answer, "cmd/idiom/product_workflow.go") {
		t.Fatalf("fallback should name concrete rejected child path:\n%s", answer)
	}
	if strings.Contains(answer, "- cmd/idiom:") {
		t.Fatalf("fallback must not report directory scope as file blocker:\n%s", answer)
	}
}

func TestSynthesizeMutationFallbackReportsMissingRequiredPathsAndPlannerOwnedVerification(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
		RequiredEditPaths:    []string{"native/src/example.h", "native/src/example.cpp"},
	}, []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
	})

	for _, want := range []string{
		"Changed paths:",
		"native/src/example.cpp",
		"Required edit paths not changed:",
		"native/src/example.h",
		"pending lane sanity; build/test verification is planner-owned",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected mutation fallback to include %q, got:\n%s", want, answer)
		}
	}
}

func TestSynthesizeMutationFallbackReportsPatchHygieneOnlyForPlannerOwnedSourceEdits(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}, []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/src/example.cpp\n"},
	})

	for _, want := range []string{
		"Verification: git diff --check -- native/src/example.cpp passed (patch hygiene only); source-aware lane sanity still pending; planner-owned build/test verification not run by lane.",
		"native/src/example.cpp",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
}

func TestHideExactAnchorReadToolsRequiresWriteFirst(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	hidden := map[string]bool{}
	exactHidden := map[string]bool{}

	hideExactAnchorReadTools(hidden, exactHidden, manager)

	for _, tool := range []string{"read_file", "context_status", "context_query", "shell", "git_diff", "pwd"} {
		if !hidden[tool] || !exactHidden[tool] {
			t.Fatalf("expected exact-anchor pre-write mode to hide %s; hidden=%v exact=%v", tool, hidden[tool], exactHidden[tool])
		}
	}

	for _, tool := range []string{"edit_file", "write_file", "append_file", "apply_patch"} {
		if hidden[tool] {
			t.Fatalf("expected exact-anchor pre-write mode to allow %s", tool)
		}
	}
}

func TestExactAnchorPreWriteVisibleBriefAdvertisesWriteOnlySurface(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"exact_anchor_implementation"},
		AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "shell", "task_ledger", "harness_feedback"},
		RequiredTools:     []string{"edit_file_or_apply_patch", "verification_shell"},
		RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
		TargetRefs:        []string{"scripts/ui/depot_panel.gd"},
	}, nil)

	for _, forbidden := range []string{"read_file", "search_content", "shell", "task_ledger", "harness_feedback"} {
		if slices.Contains(brief.AllowedTools, forbidden) {
			t.Fatalf("exact-anchor pre-write brief advertised %s in allowed tools: %#v", forbidden, brief.AllowedTools)
		}

		manifest, err := json.Marshal(brief.ToolManifest)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}

		if strings.Contains(string(manifest), fmt.Sprintf(`"tool":"%s"`, forbidden)) {
			t.Fatalf("exact-anchor pre-write manifest advertised %s: %s", forbidden, manifest)
		}
	}

	for _, allowed := range []string{"edit_file", "apply_patch"} {
		if !slices.Contains(brief.AllowedTools, allowed) {
			t.Fatalf("exact-anchor pre-write brief should keep %s, got %#v", allowed, brief.AllowedTools)
		}
	}

	if !reflect.DeepEqual(brief.RequiredTools, []string{"edit_file_or_apply_patch"}) {
		t.Fatalf("exact-anchor pre-write required tools = %#v, want edit only", brief.RequiredTools)
	}

	if !slices.Contains(brief.InvalidActions, "read/search/shell before first exact-anchor write") {
		t.Fatalf("expected explicit invalid pre-write read/search/shell action, got %#v", brief.InvalidActions)
	}
}

func TestWriteReadyPreWriteVisibleBriefAdvertisesScopedSurface(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"implementation_packet_write_ready"},
		AllowedTools:      []string{"read_file", "search_content", "code_navigate", "edit_file", "write_file", "apply_patch", "task_ledger", "harness_feedback"},
		RequiredTools:     []string{"edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
		RequiredEditPaths: []string{"native/src/simulation/economy/energy.cpp"},
		TargetPacket:      []string{"verified_current_state"},
		EvidencePaths:     []string{"native/src/simulation/economy/energy.cpp"},
	}, nil)

	for _, forbidden := range []string{"search_content", "task_ledger", "harness_feedback"} {
		if slices.Contains(brief.AllowedTools, forbidden) {
			t.Fatalf("write-ready pre-write brief advertised broad tool %s in allowed tools: %#v", forbidden, brief.AllowedTools)
		}
	}

	for _, allowed := range []string{"read_file", "code_navigate", "edit_file", "write_file", "apply_patch"} {
		if !slices.Contains(brief.AllowedTools, allowed) {
			t.Fatalf("write-ready pre-write brief should keep scoped tool %s, got %#v", allowed, brief.AllowedTools)
		}
	}

	if !reflect.DeepEqual(brief.RequiredTools, []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}) {
		t.Fatalf("write-ready pre-write required tools = %#v, want scoped edit/read tools", brief.RequiredTools)
	}

	invalid := strings.Join(brief.InvalidActions, "\n")
	if !strings.Contains(invalid, "task_ledger or broad discovery") || !strings.Contains(invalid, "outside required edit paths") {
		t.Fatalf("expected explicit invalid broad/out-of-scope actions, got %#v", brief.InvalidActions)
	}
}

func TestControlRailsExactAnchorPreWriteDoesNotAdvertiseReadShell(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "shell", "task_ledger"},
			RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
		},
	}

	rails := runner.controlRails(nil)
	for _, forbidden := range []string{"read_file", "search_content", "shell", "task_ledger"} {
		if slices.Contains(rails.AllowedTools, forbidden) {
			t.Fatalf("exact-anchor pre-write control rails advertised %s: %#v", forbidden, rails.AllowedTools)
		}
	}

	for _, allowed := range []string{"edit_file", "apply_patch"} {
		if !slices.Contains(rails.AllowedTools, allowed) {
			t.Fatalf("exact-anchor pre-write control rails should keep %s, got %#v", allowed, rails.AllowedTools)
		}
	}

	if !reflect.DeepEqual(rails.RequiredTools, []string{"edit_file_or_apply_patch"}) {
		t.Fatalf("exact-anchor pre-write control rails required tools = %#v, want edit only", rails.RequiredTools)
	}
}

func TestWorkspaceWriteResultCompletedRequiresErrorFreeWrite(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}
	if !workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "Edited README.md"}) {
		t.Fatal("successful edit_file result should complete the exact-anchor write phase")
	}

	if !workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "No-op edit for README.md"}) {
		t.Fatal("error-free no-op edit should complete the exact-anchor write phase so verification/closeout can proceed")
	}

	if workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "old_text did not appear"}) {
		t.Fatal("failed edit_file result must not release exact-anchor read tools")
	}

	if workspaceWriteResultCompleted(workspace.ToolRequest{Tool: "read_file", Path: "README.md"}, workspace.ToolResult{Tool: "read_file", Summary: "Read README.md"}) {
		t.Fatal("read tools must not complete the exact-anchor write phase")
	}
}

func TestExactAnchorWriteFailureKeepsReadToolsHidden(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"edit_file","path":"target.txt","old_text":"missing\n","new_text":"replacement\n","description":"replace supplied exact anchor"}`,
		`{"tool":"read_file","path":"target.txt","start_line":1,"end_line":1}`,
		"<<QUESTION>>: supplied exact anchor `missing` did not match target.txt; no workspace change was made.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.MarkPathObserved("target.txt")

	var (
		events  []RunEvent
		results []workspace.ToolResult
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.TrimSpace(answer) == "" {
		t.Fatal("expected non-empty final answer after failed exact-anchor write")
	}

	for _, event := range events {
		if event.Role == "tool_hidden" && strings.Contains(event.Content, "restored after successful exact-anchor write") {
			t.Fatalf("read tools should not be restored after failed exact-anchor write; events=%v", eventRolesFor(events))
		}
	}

	foundRetainedHidden := false
	foundHiddenRead := false

	for _, event := range events {
		if event.Role == "tool_hidden" && strings.Contains(event.Content, "remain hidden after failed exact-anchor write") {
			foundRetainedHidden = true
		}
	}

	for _, result := range results {
		if result.Tool == "read_file" && result.Summary == "Tool hidden by active contract" {
			foundHiddenRead = true
		}
	}

	if !foundRetainedHidden {
		t.Fatalf("expected explicit telemetry that read tools stayed hidden; events=%v", eventRolesFor(events))
	}

	if !foundHiddenRead {
		t.Fatalf("expected attempted read to remain hidden after failed write; results=%#v", results)
	}

	content, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(content) != "actual\n" {
		t.Fatalf("failed exact-anchor edit should not change file, got %q", content)
	}
}

func TestExactAnchorRepeatedHiddenToolsReturnTypedBlocker(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		`{"tool":"read_file","path":"target.txt"}`,
		`{"tool":"read_file","path":"target.txt"}`,
		`{"tool":"shell","command":"cat target.txt"}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request after deterministic exact-anchor blocker")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var completion runnerCompletionStatus

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>:") || !strings.Contains(answer, "No workspace change was made") {
		t.Fatalf("expected deterministic exact-anchor blocker, got %q", answer)
	}

	if completion.Status != "blocked" || completion.BlockerKind != "exact_anchor_prewrite_hidden_tool_repeated" || !completion.RequiresEdit {
		t.Fatalf("unexpected completion status: %#v", completion)
	}

	if requests != len(replies) {
		t.Fatalf("expected deterministic blocker without final-answer model call, got %d request(s)", requests)
	}
}

func TestExactAnchorRejectedWritesAfterRecoveryReturnTypedBlocker(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"edit_file","path":"target.txt","description":"empty write shape"}`,
		`{"tool":"edit_file","path":"target.txt","old_text":"missing anchor","new_text":"replacement\n","description":"bad anchor"}`,
		`{"tool":"edit_file","path":"target.txt","start_line":1,"end_line":1,"content":"","description":"bad empty line edit"}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request after deterministic exact-anchor write blocker")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var completion runnerCompletionStatus

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.MarkPathObserved("target.txt")

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>:") ||
		!strings.Contains(answer, "repeated edit_file write attempts") ||
		!strings.Contains(answer, "empty or whitespace-only content") {

		t.Fatalf("expected deterministic repeated-write blocker, got %q", answer)
	}

	if completion.Status != "blocked" || completion.BlockerKind != "exact_anchor_write_failure_repeated" || !completion.RequiresEdit {
		t.Fatalf("unexpected completion status: %#v", completion)
	}

	if requests != len(replies) {
		t.Fatalf("expected deterministic blocker without extra model call, got %d request(s)", requests)
	}

	content, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(content) != "actual\n" {
		t.Fatalf("rejected exact-anchor writes should not change file, got %q", content)
	}
}

func TestSynthesizeFallbackAnswerPrefersSemanticEvidenceOverRedirects(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("child:explorer", []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/capabilities/workspace/tool_schemas.go to go_file_outline",
			Output:  "candidate_request: {\"tool\":\"go_file_outline\",\"path\":\"internal/capabilities/workspace/tool_schemas.go\"}\nrelated_capability: read_symbol via go_read_symbol.",
		},
		{
			Tool:    "go_file_outline",
			Summary: "Outlined 7 top-level declaration(s) in internal/capabilities/workspace/tool_schemas.go.",
			Output:  "package workspace\ndeclarations:\n- baseToolDefinitions lines 69-743",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read baseToolDefinitions in internal/capabilities/workspace/tool_schemas.go lines 69-89",
			Output:  "69:func baseToolDefinitions() []ToolDefinition {\n80:\t\t\tName:        \"task\"",
		},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}

	if strings.Contains(answer, "Redirected to go_file_outline") || strings.Contains(answer, "Try: {\"tool\"") {
		t.Fatalf("expected redirect chatter to be omitted from fallback packet, got:\n%s", answer)
	}

	for _, want := range []string{"internal/capabilities/workspace/tool_schemas.go", "baseToolDefinitions", "Name:        \"task\""} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected semantic evidence %q in fallback packet, got:\n%s", want, answer)
		}
	}
}

// Adoption fix B (OpenCode alignment) — the forced-completion fallback must
// NOT prefix the reply with apologetic "did not complete cleanly" wording.
// Users read that as a system error; the same content reframed as
// "Partial findings from this turn" presents the evidence honestly.
// Guards against the failure surfaced in
// logs/conversations/20260421-053044.479.jsonl where the fallback answer
// was an apology instead of a useful partial report.
func TestSynthesizeFallbackAnswerOmitsApologeticPrefix(t *testing.T) {
	for _, agent := range []string{"explore", "main"} {
		answer, ok := synthesizeFallbackAnswer(agent, []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/app.go lines 1-50", Output: "package app\n"},
		})
		if !ok {
			t.Fatalf("expected fallback answer for agent %q", agent)
		}

		if !strings.Contains(answer, "Partial findings from this turn") {
			t.Fatalf("expected %q fallback to lead with partial-findings framing, got:\n%s", agent, answer)
		}

		if strings.Contains(answer, "did not complete cleanly") {
			t.Fatalf("expected %q fallback to drop apologetic wording, got:\n%s", agent, answer)
		}
	}
}

func TestImmediateBudgetFallbackAnswerUsesExistingEvidence(t *testing.T) {
	answer, ok := immediateBudgetFallbackAnswer("main", workerruntime.WorkerRuntimeBrief{
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "How does this harness utilize sub-agents?",
	}, "Post-child named-gap contract is exhausted. Tools are now disabled.", []workspace.ToolResult{
		{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: How does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/runtime/agent/runner.go`\n- `internal/runtime/agent/runner_child_tasks.go`\n\nKey findings:\n- The harness uses the `task` tool in internal/runtime/agent/runner.go to launch a read-only child runner implemented by internal/runtime/agent/runner_child_tasks.go.\n",
		},
		{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-20", Output: "func (r *Runner) runChildTask(...) {}"},
	})
	if !ok {
		t.Fatal("expected immediate fallback answer")
	}

	for _, want := range []string{"The answer is grounded in", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"Source: delegated read-only evidence packet.", "Inspected paths:", "Key findings:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected fallback to avoid internal packet section %q, got:\n%s", forbidden, answer)
		}
	}

	if strings.Contains(answer, "Best grounded answer from the gathered evidence") {
		t.Fatalf("expected evidence answer without fallback banner, got:\n%s", answer)
	}
}

func TestAnnotateScopedFallbackAnswerAddsObjectiveForChildPacketFallback(t *testing.T) {
	answer := annotateScopedFallbackAnswer("Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Found task hooks", "Can you tell me how this harness uses subagents?")
	if !strings.Contains(answer, "Question in scope: Can you tell me how this harness uses subagents?") {
		t.Fatalf("expected scoped question prefix, got:\n%s", answer)
	}

	if !strings.Contains(answer, "Source: delegated read-only evidence packet.") {
		t.Fatalf("expected delegated read-only packet source line, got:\n%s", answer)
	}
}

func TestChildPacketContainsGroundedAnswerEvidenceRequiresRealEvidence(t *testing.T) {
	good := "Source-body evidence paths:\n- internal/runtime/agent/runner_child_tasks.go\n\nInspected paths: internal/runtime/agent/runner.go, internal/runtime/agent/runner_child_tasks.go\nKey findings: the harness uses the task tool to launch an isolated read-only child runner.\nConfidence: high\n"
	if !childPacketContainsGroundedAnswerEvidence("", good) {
		t.Fatal("expected grounded child packet evidence to be recognized")
	}

	goodSubagent := "Source-body evidence paths:\n- internal/runtime/agent/runner_child_tasks.go\n- internal/runtime/agent/runner.go\n\nInspected paths: internal/runtime/agent/runner.go, internal/runtime/agent/runner_child_tasks.go\nKey findings: the harness uses the task tool to launch subagents as child runners and return their packets to the parent runner.\nConfidence: high\n"
	if !childPacketContainsGroundedAnswerEvidence("Can you tell me how this harness uses subagents?", goodSubagent) {
		t.Fatal("expected objective-relevant subagent child packet to be recognized")
	}

	offObjectiveSubagent := "Source-body evidence paths:\n- internal/runtime/agent/runner.go\n- internal/repository/comprehension/result_metadata.go\n\nInspected paths: internal/runtime/agent/runner.go, internal/repository/comprehension/result_metadata.go\nKey findings: the runner reads ChatMessage values and stores context metadata.\nConfidence: high\n"
	if childPacketContainsGroundedAnswerEvidence("Can you tell me how this harness uses subagents?", offObjectiveSubagent) {
		t.Fatal("expected off-objective child packet to be rejected for subagent objective")
	}

	offObjectiveDiscovery := "Source-body evidence paths:\n- internal/capabilities/workspace/tool_schema_export.go\n- internal/capabilities/workspace/tool_schemas.go\n\nInspected paths: internal/capabilities/workspace/tool_schema_export.go, internal/capabilities/workspace/tool_schemas.go\nKey findings: tool schema export serializes available discovery tool schemas and returns JSON metadata.\nConfidence: high\n"
	if childPacketContainsGroundedAnswerEvidence("What can we add to our discovery tools in an effort at increasing their overall usefulness?", offObjectiveDiscovery) {
		t.Fatal("expected generic tool/schema packet to be rejected for discovery usefulness objective")
	}

	partial := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nSource-body evidence paths:\n- internal/runtime/agent/runner.go\n\nVerified context:\n- Read internal/runtime/agent/runner.go lines 100-140\n- Found internal/surfaces/tui/app/turn_planner.go references\n"
	if !childPacketContainsGroundedAnswerEvidence("", partial) {
		t.Fatal("expected partial packet with grounded file evidence to be recognized")
	}

	bad := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Redirected to go_file_outline\ncandidate_request: {\"tool\":\"go_file_outline\",\"path\":\"internal/capabilities/workspace/tool_schemas.go\"}\nrelated_capability: read_symbol via go_read_symbol.\n"
	if childPacketContainsGroundedAnswerEvidence("", bad) {
		t.Fatal("expected redirect packet without grounded evidence to be rejected")
	}

	fixtureOnly := "Inspected paths: testdata/swe/fixtures/prompt-injection-repo/README.md, codex/README.md\nKey findings: child packet ready."
	if childPacketContainsGroundedAnswerEvidence("", fixtureOnly) {
		t.Fatal("expected fixture/docs-only child packet to be rejected for non-doc objectives")
	}

	artifactOnly := "Source-body evidence paths:\n- .idiom/comprehension/views/context.json\n\nInspected paths: .idiom/comprehension/views/context.json\nKey findings: context cache inspected."
	if childPacketContainsGroundedAnswerEvidence("", artifactOnly) {
		t.Fatal("expected generated artifact child packet to be rejected")
	}

	docsOnly := "Source-body evidence paths:\n- README.md\n\nInspected paths: README.md\nKey findings: documentation entrypoint found."
	if !childPacketContainsGroundedAnswerEvidence("Update the README documentation.", docsOnly) {
		t.Fatal("expected docs objective to accept documentation-only child packet")
	}
}

func TestNormalizeDelegatedReadOnlyPacketStripsTransientHandles(t *testing.T) {
	packet := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read f23 internal/runtime/agent/runner_child_tasks.go lines 1-40\n- The caller is internal/runtime/agent/runner.go:574 and the composer lives in internal/runtime/agent/answer_composer.go``\nf23 internal/runtime/agent/runner_child_tasks.go\nf7 README.md\n"
	got := normalizeDelegatedReadOnlyPacket(packet)

	if strings.Contains(got, "f23 ") || strings.Contains(got, "f7 ") {
		t.Fatalf("expected transient handles to be stripped, got:\n%s", got)
	}

	if strings.Contains(got, "internal/runtime/agent/runner.go:574") || strings.Contains(got, "internal/runtime/agent/answer_composer.go``") {
		t.Fatalf("expected packet normalization to strip speculative line citations and malformed quotes, got:\n%s", got)
	}

	markdownPathLine := "- `internal/runtime/agent/runner_child_tasks.go`"
	if normalizedLine := normalizeDelegatedReadOnlyPacket(markdownPathLine); normalizedLine != markdownPathLine {
		t.Fatalf("expected packet normalization to preserve markdown path citations, got %q", normalizedLine)
	}

	for _, want := range []string{"internal/runtime/agent/runner_child_tasks.go", "internal/runtime/agent/runner.go", "internal/runtime/agent/answer_composer.go", "README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected normalized packet to retain %q, got:\n%s", want, got)
		}
	}
}
