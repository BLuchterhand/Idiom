package agent

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPlanExecuteTextualAllowedToolsKeepsHistoryWhenCandidateReadyNeedsHistory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go"},
		RequiredEvidence: []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:     []string{"history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)",
			Output:  "1:package pkg\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "arbitrary candidate terminal nudge",
			Error:   "candidate/allowed scope already has anchor-ready source evidence. Submit one parser-readable submit_plan.",
		},
	}

	allowed := runnerPlanExecuteTextualAllowedTools([]string{"history_range", "history_symbol", "submit_plan"}, brief, results)
	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("candidate-ready history-pending surface missing %s: %#v", want, allowed)
		}
	}

	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate-ready history-pending surface must hold submit_plan until history evidence is present: %#v", allowed)
	}

	narrowed := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, results)
	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(narrowed, want) {
			t.Fatalf("candidate-ready history-pending surface should restore %s after submit-only narrowing: %#v", want, narrowed)
		}
	}

	if slices.Contains(narrowed, "submit_plan") {
		t.Fatalf("candidate-ready history-pending surface must not preserve submit-only narrowing before history evidence: %#v", narrowed)
	}
}

func TestPlanExecuteTextualAllowedToolsRestrictsToHistoryToolsWhenHistoryPending(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"pkg/feature.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)",
			Output:  "1:package pkg\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "required path has source evidence",
			Error:   "candidate/allowed scope already has anchor-ready source evidence. Submit one parser-readable submit_plan.",
		},
	}

	allowed := runnerPlanExecuteTextualAllowedTools([]string{"history_range", "history_symbol", "submit_plan"}, brief, results)
	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("history-pending surface should preserve %s for git history evidence: %#v", want, allowed)
		}
	}

	for _, forbidden := range []string{"read_file", "code_navigate"} {
		if slices.Contains(allowed, forbidden) {
			t.Fatalf("history-pending surface must not expose source-read tool %s after source evidence is satisfied: %#v", forbidden, allowed)
		}
	}

	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("history-pending surface must hold submit_plan until history evidence is present: %#v", allowed)
	}
}

func TestPlanExecuteCandidateReadyResultUsesTypedCodeBeforeSummary(t *testing.T) {
	result := workspace.ToolResult{
		Code:    planThenExecuteResultCandidateSourceReady,
		Summary: "not the legacy summary",
		Error:   "candidate scope is ready",
	}
	if !planThenExecuteCandidateSourceReadyResult(result) {
		t.Fatal("typed candidate-source-ready code should be authoritative even with arbitrary summary text")
	}

	if !planThenExecuteCandidateSourceReadyRejectionSeen([]workspace.ToolResult{result}) {
		t.Fatal("typed candidate-source-ready code should drive candidate-ready rejection detection")
	}
}

func TestPlanExecuteCandidateTerminalSubmitPlanWaitsForUnreadAllowedCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	if planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-4"},
	}) {

		t.Fatal("candidate scope with unread allowed candidates should not collapse textual surface to submit_plan-only")
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-4"},
		{Tool: "read_file", Summary: "Read f2 pkg/feature_test.go lines 1-4"},
	}) {

		t.Fatal("candidate scope should become terminal once every allowed candidate has source evidence")
	}
}

func TestPlanExecuteOpenCandidateTerminalDoesNotCollapseAfterOneRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/example/target.go lines 1-80"},
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate source without allowed paths should not collapse to submit_plan-only after one read")
	}

	if !planThenExecuteOpenCandidateSourceCanSubmit(brief, results) {
		t.Fatal("unscoped candidate source with source evidence should allow submit_plan while keeping read/search available")
	}
}

func TestPlanExecuteOpenCandidateTerminalWaitsForUnreadContextCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate scope with unread context candidates should not collapse to submit-plan-only")
	}

	if !planThenExecuteOpenCandidateSourceCanSubmit(brief, results) {
		t.Fatal("unscoped candidate source with source evidence should still allow submit_plan while reads remain visible")
	}
}

func TestPlanExecuteCompletionRecoveryKeepsReadsForUnreadContextCandidate(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	})

	if opts.ToolChoice == "required" {
		t.Fatalf("completion recovery with unread context candidates must not force terminal submit_plan mode")
	}

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "search_content") || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("completion recovery should keep read/search and submit_plan available, got %#v", opts.Tools)
	}
}

func TestPlanExecuteUnreadCandidateRecoveryDropsMissingFileEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/analysis_agents.go"},{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-80 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/analysis_agents.go failed",
			Error:   "file not found: cmd/idiom/analysis_agents.go",
		},
	}

	unread := unreadCandidateRecoveryScopePathsForRecovery(brief, results, 8)
	if slices.Contains(unread, "cmd/idiom/analysis_agents.go") {
		t.Fatalf("missing candidate path should not stay in unread recovery list: %#v", unread)
	}
}

func TestPlanExecuteLoopBudgetRecoveryAfterSymbolMissKeepsUnreadCandidatePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"},{"path":"internal/productcmd/product_workflow.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"CoverageGate\"",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/workflows/productworkflow/coverage_gate.go", Capability: "read_symbol", Symbol: "CoverageGate", Pattern: "CoverageGate"},
		results,
		"the model repeated code_navigate with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		t.Fatal("expected loop recovery to stay model-owned on unread candidate paths after a symbol miss")
	}

	if recovery.TerminalOnly {
		t.Fatalf("symbol miss without source body should not force terminal-only recovery:\n%s", recovery.Prompt)
	}

	for _, want := range []string{
		"Unread candidate path(s):",
		"internal/productcmd/product_workflow.go",
		"internal/workflows/productworkflow/coverage_gate.go",
		"one bounded source-body read/search for a model-selected unread candidate path",
		"a bounded read on that same candidate path is valid",
		"Do not call the same tool with the same arguments again",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, recovery.Prompt)
		}
	}
}

func TestPlanExecuteLoopBudgetRecoveryDoesNotUseUnreadCandidateEscapeAfterSourceBody(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack",
			Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"},{"path":"internal/productcmd/product_workflow.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-80 (total lines: 320)",
			Output:  "1:package productworkflow\n2:func EvaluateCoverageGate() {}\n",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
		"the model repeated read_file with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		t.Fatal("source-body evidence should still get terminal loop recovery")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("source-body evidence plus unread candidates should not become a generic read escape:\n%s", recovery.Prompt)
	}
}

func TestPlanExecuteUnscopedEmptySubmitPlanRejectionReopensCandidateSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n2:\n3:func Label() string { return \"long label\" }\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "Candidate source already ready",
			Error:   "candidate source evidence is ready for a terminal planner outcome",
		},
		{
			Tool:    "submit_plan",
			Code:    workspace.SubmitPlanEmptyEditsCode,
			Summary: "submit_plan rejected",
			Error:   "submit_plan requires at least one edit in the edits array",
		},
	}

	if !planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		t.Fatal("empty submit_plan rejection should be detected from the typed result code")
	}

	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection must not keep the planner in submit-plan-only mode")
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection must reopen candidate source/search surface")
	}

	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection is salvage, not submit-plan-only correction")
	}

	allowed := runnerPlanExecuteTextualAllowedTools([]string{"context_pack", "read_file", "code_navigate", "submit_plan"}, brief, results)
	if slices.Equal(allowed, []string{"submit_plan"}) {
		t.Fatalf("textual allowed tools collapsed to submit_plan after empty rejection: %#v", allowed)
	}

	if !slices.Contains(allowed, "read_file") || !slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate recovery should keep both source evidence and non-empty submit_plan available, got %#v", allowed)
	}
}

func TestPlanExecuteUnscopedEmptySubmitPlanRejectionRequiresTypedCode(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "submit_plan requires at least one edit in the edits array",
	}}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		t.Fatal("empty submit_plan salvage must be driven by typed result code, not diagnostic prose")
	}
}

func TestPlanExecuteRunnerReopensCandidateSurfaceAfterEmptySubmitPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Label() string { return \"Long descriptive label\" }\n"), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a_test.go"), []byte("package a\n\nfunc TestLabel(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}

	replies := []string{
		`{"tool":"context_pack","task":"shorten the label returned by a.go and keep tests aligned","mode":"plan","limit":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":20}`,
		`{"tool":"submit_plan","rationale":"drafting from current candidate evidence","edits":[]}`,
		`{"tool":"read_file","path":"a_test.go","start_line":1,"end_line":20,"named_gap":"check adjacent test expectation before finalizing the edit"}`,
		`{"tool":"submit_plan","rationale":"Shorten the label using current source evidence.","edits":[{"path":"a.go","old_text":"return \"Long descriptive label\"","new_text":"return \"Short label\"","purpose":"shorten the user-visible label"}]}`,
	}

	var (
		requests     int
		chatRequests []map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		var payload map[string]any

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}

		chatRequests = append(chatRequests, payload)

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
		sawEmptyRecovery  bool
		sawEmptyRejection bool
		sawPostEmptyRead  bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_empty_submit_plan_recovery" {
				sawEmptyRecovery = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "shorten the label and keep tests aligned"}},
		llama.ChatOptions{MaxTokens: 2048},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "submit_plan" && event.Result.Code == workspace.SubmitPlanEmptyEditsCode {
				sawEmptyRejection = true
			}

			if sawEmptyRejection && event.Request.Tool == "read_file" && event.Request.Path == "a_test.go" && strings.TrimSpace(event.Result.Error) == "" {
				sawPostEmptyRead = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected full empty-plan recovery sequence, got %d request(s)", requests)
	}

	if !sawEmptyRecovery {
		t.Fatal("runner did not emit empty submit_plan recovery event")
	}

	if !sawPostEmptyRead {
		t.Fatal("runner did not execute a bounded candidate read after empty submit_plan rejection")
	}

	if len(chatRequests) < 4 {
		t.Fatalf("expected at least four model calls, got %d", len(chatRequests))
	}

	afterEmptyRecovery := chatRequests[3]
	if choice, ok := afterEmptyRecovery["tool_choice"].(string); ok && choice == "required" {
		t.Fatalf("empty-plan recovery must not preserve terminal required tool_choice: %#v", afterEmptyRecovery["tool_choice"])
	}

	if responseFormat, ok := afterEmptyRecovery["response_format"].(map[string]any); ok && responseFormat["type"] == "json_object" {
		t.Fatalf("empty-plan recovery must not preserve terminal JSON response_format: %#v", responseFormat)
	}

	if !chatRequestToolListContains(afterEmptyRecovery, "read_file") || !chatRequestToolListContains(afterEmptyRecovery, "submit_plan") {
		t.Fatalf("empty-plan recovery request should expose candidate tools plus submit_plan, got tools=%#v", afterEmptyRecovery["tools"])
	}

	if chatRequestMessagesContain(afterEmptyRecovery, "# Terminal Plan-Execute Native Tool Surface") ||
		chatRequestMessagesContain(afterEmptyRecovery, "# Terminal Plan-Execute Textual Tool Surface") {

		t.Fatalf("empty-plan recovery request retained stale terminal surface scaffolding")
	}

	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected final non-empty submit_plan to be accepted, got:\n%s", answer)
	}
}

func TestPlanExecuteRunnerRecordsUnavailableEmptySubmitPlanBeforeHistory(t *testing.T) {
	root := t.TempDir()
	writeRunnerTestFile(t, root, "a.go", "package a\n\nfunc Label() string { return \"Long descriptive label\" }\n")
	writeRunnerTestFile(t, root, "a_test.go", "package a\n\nfunc TestLabel(t *testing.T) {}\n")
	initRunnerTestGitRepo(t, root)

	replies := []string{
		`{"tool":"context_pack","task":"shorten the label returned by a.go and keep tests aligned","mode":"plan","limit":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":20}`,
		`{"tool":"submit_plan","rationale":"empty draft before history should still be typed as empty submit recovery","edits":[]}`,
		`{"tool":"read_file","path":"a_test.go","start_line":1,"end_line":20,"named_gap":"check adjacent test expectation before finalizing the edit"}`,
		`{"tool":"submit_plan","rationale":"Shorten the label using current source evidence.","edits":[{"path":"a.go","old_text":"return \"Long descriptive label\"","new_text":"return \"Short label\"","purpose":"shorten the user-visible label"}]}`,
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		var payload map[string]any

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode chat request: %v", err)
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
		sawEmptyRecovery       bool
		sawEmptyRejection      bool
		sawPostEmptyRead       bool
		sawHistoryBeforeSubmit bool
		sawAcceptedSubmit      bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_empty_submit_plan_recovery" {
				sawEmptyRecovery = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "shorten the label and keep tests aligned"}},
		llama.ChatOptions{MaxTokens: 2048},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "submit_plan" && event.Result.Code == workspace.SubmitPlanEmptyEditsCode {
				sawEmptyRejection = true
			}

			if sawEmptyRejection && event.Request.Tool == "read_file" && event.Request.Path == "a_test.go" && strings.TrimSpace(event.Result.Error) == "" {
				sawPostEmptyRead = true
			}

			if event.Request.Tool == "history_range" && strings.TrimSpace(event.Result.Error) == "" {
				sawHistoryBeforeSubmit = true
			}

			if event.Request.Tool == "submit_plan" && strings.TrimSpace(event.Result.Error) == "" {
				sawAcceptedSubmit = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected full empty-plan recovery sequence, got %d request(s)", requests)
	}

	if !sawEmptyRecovery {
		t.Fatal("runner did not emit empty submit_plan recovery event")
	}

	if !sawPostEmptyRead {
		t.Fatal("runner did not execute a bounded candidate read after empty submit_plan rejection")
	}

	if !sawHistoryBeforeSubmit {
		t.Fatal("non-empty submit_plan should be redirected through git history before acceptance")
	}

	if !sawAcceptedSubmit {
		t.Fatal("expected final non-empty submit_plan to be accepted")
	}

	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected final non-empty submit_plan to be accepted, got:\n%s", answer)
	}
}

func chatRequestToolListContains(payload map[string]any, name string) bool {
	tools, ok := payload["tools"].([]any)
	if !ok {
		return false
	}

	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}

		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}

		if function["name"] == name {
			return true
		}
	}

	return false
}

func chatRequestMessagesContain(payload map[string]any, needle string) bool {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return false
	}

	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}

		if strings.Contains(fmt.Sprint(message["content"]), needle) {
			return true
		}
	}

	return false
}

func TestPlanExecuteSubmitReadyFailureFeedbackDoesNotSuggestGenericProbe(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}
	rejected := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Plan-execute submit_plan required",
		Error:   "read_file is not available because the plan-execute planner is source-ready and exposes only submit_plan",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)"},
		rejected,
	}

	state := buildRunnerNextActionState("main", 3, "tool_rejected", req, rejected, results, false, brief)
	joinedState := strings.Join(state.NextValidActions, "\n")

	if !strings.Contains(joinedState, "source_satisfied_paths") {
		t.Fatalf("submit-ready next_action_state should point at submit_plan, got %#v", state.NextValidActions)
	}

	if strings.Contains(joinedState, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready next_action_state must not suggest another probe: %#v", state.NextValidActions)
	}

	if strings.Contains(joinedState, "answer from the current evidence") {
		t.Fatalf("submit-ready next_action_state must not include generic answer/probe fallback: %#v", state.NextValidActions)
	}

	packet := buildRunnerFailureFeedbackPacket("main", 3, "tool_rejected", req, rejected, results, false, brief)
	joinedFeedback := strings.Join(packet.ValidNext, "\n")

	if !strings.Contains(joinedFeedback, "source_satisfied_paths") {
		t.Fatalf("submit-ready failure feedback should point at submit_plan, got %#v", packet.ValidNext)
	}

	if strings.Contains(joinedFeedback, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready failure feedback must not suggest another probe: %#v", packet.ValidNext)
	}

	if strings.Contains(joinedFeedback, "answer from the current evidence") {
		t.Fatalf("submit-ready failure feedback must not include generic answer/probe fallback: %#v", packet.ValidNext)
	}

	if strings.Contains(packet.ResultError, "Use an available tool") {
		t.Fatalf("submit-ready rejection should not use generic allowed-tool wording: %q", packet.ResultError)
	}
}

func TestPlanExecuteSubmitReadyStateDoesNotAdvertiseReadProbes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredNext: []string{
			"for existing required paths, satisfy source-body obligations before submit_plan unless the packet is exact-plan-ready/source-satisfied; then call submit_plan, answer Decision: source-satisfied with per-path evidence, or report <<QUESTION>>",
		},
		SearchPolicy: []string{
			"treat required_edit_paths plus packet evidence as submit_plan scope; packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
			"use reads only for exact anchor confirmation before submit_plan",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Run in b.go lines 10-30\nfunc Run() {}\n",
		},
	}

	readyBrief := runnerBriefWithRequiredEditPathHints(brief, results)
	joinedNext := strings.Join(readyBrief.RequiredNext, "\n")
	joinedPolicy := strings.Join(readyBrief.SearchPolicy, "\n")

	if !strings.Contains(joinedNext, "source_satisfied_paths") {
		t.Fatalf("submit-ready brief should keep terminal submit_plan action, got %#v", readyBrief.RequiredNext)
	}

	if !strings.Contains(joinedPolicy, "no read/search/discovery action is valid after submit_plan readiness") {
		t.Fatalf("submit-ready brief should add terminal no-read policy, got %#v", readyBrief.SearchPolicy)
	}

	for _, forbidden := range []string{
		"source-body obligations before submit_plan",
		"use reads only for exact anchor confirmation",
		"raw source reads are only for exact anchor confirmation",
	} {
		if strings.Contains(joinedNext+"\n"+joinedPolicy, forbidden) {
			t.Fatalf("submit-ready brief leaked stale source-read phrase %q\nnext=%#v\npolicy=%#v", forbidden, readyBrief.RequiredNext, readyBrief.SearchPolicy)
		}
	}

	lastReq := workspace.ToolRequest{Tool: "code_navigate", Path: "b.go", Capability: "read_symbol", Pattern: "Run"}
	lastResult := results[len(results)-1]
	state := buildRunnerNextActionState("main", 2, "tool_result", lastReq, lastResult, results, false, brief)
	joinedState := strings.Join(state.NextValidActions, "\n")

	if !strings.Contains(joinedState, "source_satisfied_paths") {
		t.Fatalf("submit-ready next_action_state should point at submit_plan, got %#v", state.NextValidActions)
	}

	for _, forbidden := range []string{
		"one exact missing claim",
		"narrow range read",
		"exact-text search",
		"answer if the inspected body",
		"source-body obligations before submit_plan",
	} {
		if strings.Contains(joinedState, forbidden) {
			t.Fatalf("submit-ready next_action_state leaked read/probe action %q: %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestPlanExecuteSubmitReadyParseRecoveryBlocksFurtherReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}
	runner := &Runner{RuntimeBrief: brief}
	sourceReady := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)"},
	}
	raw := `{"tool":"submit_plan","rationale":"Update coverage gate","edits":[{"path":"a.go""start_line":1}]}`
	prompt, ok := runner.planThenExecuteSubmitPlanParseRecoveryPrompt(raw, errors.New(`invalid character '"' after object key:value pair`), sourceReady)

	if !ok {
		t.Fatalf("expected submit-ready malformed JSON to trigger submit_plan parse recovery")
	}

	if !strings.Contains(prompt, "malformed") || !strings.Contains(prompt, "Do not call read/search/discovery") {
		t.Fatalf("expected terminal parse recovery prompt, got %q", prompt)
	}

	afterParse := append(append([]workspace.ToolResult{}, sourceReady...), workspace.ToolResult{
		Tool:    "tool_parse",
		Summary: "Malformed tool request rejected",
		Error:   "decode tool request",
		Output:  raw,
	})
	if !planThenExecuteSubmitReadyParseFailureSeen(brief, afterParse) {
		t.Fatal("expected submit-ready parse failure to be remembered")
	}

	if runner.planThenExecuteReadySourceRefreshAllowed(workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}, afterParse, 0) {
		t.Fatal("post-readiness source refresh must not reopen after malformed submit_plan JSON")
	}

	afterRejectedPlan := append(append([]workspace.ToolResult{}, afterParse...), workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `a.go: old_text not found in current source`.",
	})
	if !runner.planThenExecuteReadySourceRefreshAllowed(workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}, afterRejectedPlan, 0) {
		t.Fatal("later invalid-anchor submit_plan rejection should reopen its advertised bounded source refresh despite an earlier malformed submit_plan")
	}
}

func TestPlanExecuteStringifiedSubmitPlanParseFailureEmitsRecoveryEventWithoutPrompt(t *testing.T) {
	var events []RunEvent

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	raw := `{"tool":"submit_plan","rationale":"Bad stringified edits.","edits":"not json"}`
	parseErr := errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits of type []workspace.PlanEdit")

	if prompt, ok := runner.planThenExecuteSubmitPlanParseRecoveryPrompt(raw, parseErr, nil); ok {
		t.Fatalf("fixture should exercise event fallback without prompt injection, got %q", prompt)
	}

	runner.emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw, parseErr)

	for _, event := range events {
		if event.Role == "plan_then_execute_submit_plan_parse_recovery" {
			if strings.Contains(event.Content, "Do not call read/search/discovery") {
				t.Fatalf("fallback event must not reuse submit-ready prompt directives, got %q", event.Content)
			}

			return
		}
	}

	t.Fatalf("expected plan_then_execute_submit_plan_parse_recovery event, got %#v", events)
}

func TestPlanExecuteTerminalTextualHistoryPrunesReadToolRequests(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "system", Content: "base"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
		{Role: "system", Content: "Read a.go lines 1-20"},
		{Role: "assistant", Content: `{"tool":"code_navigate","path":"a.go","capability":"read_symbol","pattern":"Run"}`},
		{Role: "assistant", Content: `{"tool":"submit_plan","rationale":"ok","edits":[]}`},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go""start_line":1}`},
	}

	pruned, dropped := prunePlanThenExecuteTextualToolRequestHistory(messages)
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3; pruned=%#v", dropped, pruned)
	}

	joined := ""
	for _, message := range pruned {
		joined += message.Content + "\n"
	}

	if strings.Contains(joined, `"tool":"read_file"`) || strings.Contains(joined, `"tool":"code_navigate"`) {
		t.Fatalf("terminal history should drop prior read/navigation JSON requests, got:\n%s", joined)
	}

	if !strings.Contains(joined, "Read a.go lines") {
		t.Fatalf("terminal history must keep tool result evidence, got:\n%s", joined)
	}

	if !strings.Contains(joined, `"tool":"submit_plan"`) {
		t.Fatalf("terminal history should keep submit_plan attempts for correction, got:\n%s", joined)
	}
}

func TestPlanExecuteReadyToSubmitRaisesBudgetAfterSourceReadCap(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        768,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 5257-5270"},
	})

	if opts.MaxTokens != 4096 {
		t.Fatalf("ready-to-submit should raise inherited source-read budget to planner budget, got %d", opts.MaxTokens)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit should keep thinking disabled after source-read cap, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteReadyToSubmitUsesLargeBudgetForFourExistingFiles(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("ready-to-submit should use large budget for four-file textual submit_plan output, got %d", opts.MaxTokens)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit should keep thinking disabled for submit_plan-only multi-file synthesis, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteReadyToSubmitKeepsBoundedBudgetForThreeExistingFiles(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"},
	})

	if opts.MaxTokens != 8192 {
		t.Fatalf("ready-to-submit should keep bounded budget for three existing files, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteParseRecoveryUsesLargeBudgetForFourRequiredPaths(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteRequiredWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        8192,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"},
		{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request: unexpected end of JSON input"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("four-path parse recovery should raise submit_plan retry budget above truncation ceiling, got %d", opts.MaxTokens)
	}

	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("parse recovery should still expose only submit_plan, got tool_choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}

func TestPlanExecuteReadyToSubmitKeepsLargeBudgetForNewFileOrWholeFile(t *testing.T) {
	thinkingOff := false

	for name, brief := range map[string]workerruntime.WorkerRuntimeBrief{
		"new-file objective": {
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Create new file internal/workflows/productworkflow/coverage_gate_report.go",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate_report.go", "internal/workflows/productworkflow/coverage_gate_report_test.go"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "internal/workflows/productworkflow/coverage_gate_report.go", Content: "package productworkflow\n"},
				{Path: "internal/workflows/productworkflow/coverage_gate_report_test.go", Content: "package productworkflow\n"},
			},
		},
		"whole-file objective": {
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Replace whole-file contents for generated coverage report fixtures",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate_report.go", "internal/workflows/productworkflow/coverage_gate_report_test.go"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "internal/workflows/productworkflow/coverage_gate_report.go", Content: "package productworkflow\n"},
				{Path: "internal/workflows/productworkflow/coverage_gate_report_test.go", Content: "package productworkflow\n"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
				EnableThinking:   &thinkingOff,
				PreserveThinking: false,
				MaxTokens:        4096,
				Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
				ToolChoice:       "required",
			}, brief, []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read f1 internal/workflows/productworkflow/coverage_gate_report.go", Error: "path is a NEW FILE and does not exist yet"},
				{Tool: "read_file", Summary: "Read f2 internal/workflows/productworkflow/coverage_gate_report_test.go", Error: "path is a NEW FILE and does not exist yet"},
			})

			if opts.MaxTokens != 16384 {
				t.Fatalf("large-body/new-file submit_plan should preserve full budget, got %d", opts.MaxTokens)
			}
		})
	}
}

func TestPlanExecuteRejectedPlanCorrectionUsesLargeBudgetForFourExistingFiles(t *testing.T) {
	thinkingOff := false
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"},
		{Tool: "submit_plan", Summary: "submit_plan rejected: required edit path coverage mismatch", Error: "missing required edit path(s): `cmd/idiom/product_workflow_test.go`, `internal/workflows/productworkflow/coverage_gate.go`, `internal/workflows/productworkflow/coverage_gate_test.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; placeholder edit body path(s): `cmd/idiom/product_workflow.go: old_text contains placeholder marker \"... existing code ...\"`"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1396-1671"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("correction-only four-file submit_plan recovery should use large budget, got %d", opts.MaxTokens)
	}

	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("correction-only recovery should expose only submit_plan, got tool_choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}
