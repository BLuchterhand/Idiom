package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanExecutePostReadinessRejectsSourceReadBeforeHistory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}

	summary, errText, reject := planThenExecutePostReadinessDiscoveryRejection(req, brief, results)
	if !reject {
		t.Fatalf("source-ready but history-pending phase should reject additional source reads")
	}

	for _, want := range []string{"Git history evidence required", "history_range", "history_symbol", "Do not perform additional source reads"} {
		if !strings.Contains(summary+"\n"+errText, want) {
			t.Fatalf("history-pending rejection missing %q, got %q: %s", want, summary, errText)
		}
	}
}

func TestPlanExecuteSourceReadBeforeHistoryRedirectsToHistoryRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 10-20 (total lines: 40)",
			Output:  "10:package a\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 b.go lines 30-45 (total lines: 80)",
			Output:  "30:package b\n",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "b.go",
		StartLine: 30,
		EndLine:   45,
	}

	redirected, _, ok := runner.sourceReadBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"})
	if !ok {
		t.Fatal("expected stale source read in history-pending phase to redirect to history evidence")
	}

	if redirected.Tool != "history_range" || redirected.Path != "b.go" || redirected.StartLine != 30 || redirected.EndLine != 45 {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
}

func TestPlanExecuteSourceReadBeforeHistoryDoesNotBypassUnreadRequiredSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 20)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: "b.go",
	}

	if redirected, _, ok := runner.sourceReadBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"}); ok {
		t.Fatalf("must not redirect to history while required source body remains unread: %#v", redirected)
	}
}

func TestPlanExecuteSubmitBeforeHistoryRedirectsToHistoryRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "old",
			NewText: "new",
			Purpose: "update",
		}},
	}

	redirected, _, ok := runner.submitPlanBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"})
	if !ok {
		t.Fatal("expected submit_plan-before-history redirect")
	}

	if redirected.Tool != "history_range" || redirected.Path != "a.go" || redirected.StartLine != 1 || redirected.EndLine != 10 {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
}

func TestPlanExecuteSubmitBeforeHistoryDoesNotRedirectWhenNotApplicable(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
			Output:  "1:package a\n",
		},
		gitHistoryNotApplicableResult("a.go"),
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "old",
			NewText: "new",
			Purpose: "update",
		}},
	}

	state := DerivePlanExecutePlanningState(runner.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode after path-scoped history_not_applicable = %q, want submit_ready", state.Mode)
	}

	if redirected, _, ok := runner.submitPlanBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"}); ok {
		t.Fatalf("submit_plan must not redirect to history after path-scoped history_not_applicable: %#v", redirected)
	}

	if guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, results); guard.Intercepted {
		t.Fatalf("submit_plan guard should accept path-scoped history_not_applicable: %#v", guard.Result)
	}
}

func TestPlanExecuteSubmitBeforeHistoryDoesNotBypassUnreadRequiredSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "old a", NewText: "new a", Purpose: "update a"},
			{Path: "b.go", OldText: "old b", NewText: "new b", Purpose: "update b"},
		},
	}

	if redirected, _, ok := runner.submitPlanBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"}); ok {
		t.Fatalf("submit_plan must not redirect to history while a required path is unread: %#v", redirected)
	}
}

func TestImplementationWriteBeforeHistoryRedirectsToHistoryRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       "implementation",
			RequiredEvidence: []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "a.go",
		OldText:     "old",
		NewText:     "new",
		Description: "update value after checking history",
	}

	redirected, _, ok := runner.writeBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"})
	if !ok {
		t.Fatal("expected write-before-history redirect")
	}

	if redirected.Tool != "history_range" || redirected.Path != "a.go" || redirected.StartLine != 1 || redirected.EndLine != 10 {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
}

func TestPlanExecuteSubmitPlanExactAnchorGroundsUnscopedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "return 1",
			NewText: "return 2",
			Purpose: "change value",
		}},
		Rationale: "Change value.",
	}
	guard := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, nil)

	if guard.Intercepted {
		t.Fatalf("exact current-source anchor should ground unscoped submit_plan edit, got %#v", guard)
	}
}

func TestPlanThenExecuteRepeatedSymbolReadRemainsModelOwnedBeforeBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/types.go lines 1-120 (total lines: 200)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read CoverageGateResult in internal/workflows/productworkflow/types.go lines 20-40\n20:type CoverageGateResult struct {}\n",
		},
	}

	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/types.go",
		Pattern:    "CoverageGateFinding",
		NamedGap:   "need more context",
	}
	state := DerivePlanExecutePlanningActionState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, req)

	if len(state.LowValueActions) != 0 {
		t.Fatalf("repeated symbol read should remain model-owned before the candidate-read budget is exhausted, got %#v", state.LowValueActions)
	}
}

func TestPlanThenExecuteManyCandidateReadsRemainModelOwned(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"},{"path":"internal/workflows/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-120 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/workflows/productworkflow/coverage_gate_test.go lines 1-80 (total lines: 80)",
			Output:  "1:package productworkflow\n",
		},
	}

	for i := 0; i < 11; i++ {
		results = append(results, workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Symbol in internal/workflows/productworkflow/coverage_gate.go lines 20-40\n20:func Symbol() {}\n",
		})
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate source evidence should permit submit_plan once all surfaced candidates are read")
	}

	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Pattern:    "AnotherSymbol",
		NamedGap:   "inspect another helper",
	}
	state := DerivePlanExecutePlanningActionState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, req)

	if len(state.LowValueActions) != 0 {
		t.Fatalf("many successful source reads must not create a fixed-budget low-value action, got %#v", state.LowValueActions)
	}
}

func TestPlanExecuteManyCandidateReadsDoNotBypassUnreadCandidateSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
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
			Output:  "1:package pkg\n",
		},
	}

	for i := 0; i < 11; i++ {
		results = append(results, workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Helper in pkg/feature.go lines 20-40\n20:func Helper() {}\n",
		})
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("many source reads must not collapse unscoped candidate scope to submit_plan-only while a surfaced candidate remains unread")
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got == "" {
		t.Fatal("many source reads should still emit submit-ready guidance without requiring every unscoped candidate")
	} else if !strings.Contains(got, "Allowed/candidate paths are not coverage obligations") {
		t.Fatalf("readiness nudge should preserve open-candidate semantics:\n%s", got)
	}
}

func TestPlanExecuteRunnerRepeatedCandidateReadDoesNotRecordRunnerRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4,"named_gap":"need more context"}`,
		"<<QUESTION>>: no concrete selected edit path is required for this low-value-read regression fixture.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		sawLowValue     bool
		eventRoles      []string
		modelCallEvents []map[string]any
		turnSummary     struct {
			RejectedActions          int `json:"rejected_actions"`
			CompletionGateRejections int `json:"completion_gate_rejections"`
		}
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		OnEvent: func(event RunEvent) {
			eventRoles = append(eventRoles, event.Role)
			switch event.Role {
			case "plan_then_execute_low_value_action":
				sawLowValue = true
			case "model_call":
				var payload map[string]any

				if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
					t.Fatalf("decode model_call: %v", err)
				}

				modelCallEvents = append(modelCallEvents, payload)
			case "turn_summary":
				if err := json.Unmarshal([]byte(event.Content), &turnSummary); err != nil {
					t.Fatalf("decode turn_summary: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a no-op"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}

	if sawLowValue {
		t.Fatalf("repeated candidate read should remain model-owned before budget exhaustion; saw events %#v", eventRoles)
	}

	if len(modelCallEvents) == 0 {
		t.Fatal("expected model_call telemetry")
	}

	for _, key := range []string{"backend_kind", "transport_mode", "parser_contract", "response_format", "max_tokens", "thinking_policy", "supports_native_tools"} {
		if _, ok := modelCallEvents[0][key]; !ok {
			t.Fatalf("model_call telemetry missing %q: %#v", key, modelCallEvents[0])
		}
	}

	if turnSummary.RejectedActions != 0 || turnSummary.CompletionGateRejections != 0 {
		t.Fatalf("low-value read must not count as runner rejection, got %#v", turnSummary)
	}
}

func TestPlanExecuteRunnerPostGateMissingReadTargetUsesRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n\nfunc Other() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4}`,
		"Done.",
		`{"tool":"read_file"}`,
		"<<QUESTION>>: Cannot draft a safe plan because the exact missing edit-safety fact was not specified for an unread path.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		sawLowValue          bool
		sawReadTargetFailure bool
		sawDoomLoop          bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.go", "b.go"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "plan_then_execute_low_value_action":
				sawLowValue = true

				if !strings.Contains(event.Content, "candidate_read_missing_target") {
					t.Fatalf("low-value event should name missing target reason: %s", event.Content)
				}
			case "tool_result":
				if strings.Contains(event.Content, "read_file requires id or path") {
					sawReadTargetFailure = true
				}
			case "doom_loop_guard":
				sawDoomLoop = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a safe change"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}

	if !sawLowValue {
		t.Fatal("expected missing-target read after completion gate to use low-value recovery")
	}

	if sawReadTargetFailure {
		t.Fatal("missing-target read should be recovered before workspace manager execution")
	}

	if sawDoomLoop {
		t.Fatal("missing-target recovery should not reach doom-loop guard")
	}
}

func TestPlanExecuteRunnerPreSourceMissingCodeNavigateTargetStaysModelOwned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		`{"tool":"code_navigate"}`,
		"<<QUESTION>>: Cannot choose a safe plan because the requested navigation omitted a concrete path, capability, or symbol target.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		sawCandidateRead                bool
		sawWorkspaceCodeNavigateFailure bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "tool_result":
				if strings.Contains(event.Content, "capability is required") || strings.Contains(event.Content, "path is required") {
					sawWorkspaceCodeNavigateFailure = true
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a safe change"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, func(event ToolResultEvent) {
		if event.Request.Tool == "read_file" && event.Request.Path == "a.go" && event.Result.Error == "" {
			sawCandidateRead = true
		}

		if event.Request.Tool == "code_navigate" && (strings.Contains(event.Result.Error, "capability is required") || strings.Contains(event.Result.Error, "path is required")) {
			sawWorkspaceCodeNavigateFailure = true
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}

	if sawCandidateRead {
		t.Fatal("targetless code_navigate must not be rewritten into a host-selected candidate read")
	}

	if sawWorkspaceCodeNavigateFailure {
		t.Fatal("targetless code_navigate should be recovered before workspace manager execution")
	}
}

func TestPlanThenExecuteCandidateReadFileIDIsConcreteTarget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-4 (total lines: 4)",
		Output:  "1:package a\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		ID:        "f3",
		StartLine: 1,
		EndLine:   80,
		NamedGap:  "inspect discovered file handle before selecting the plan edit",
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})

	if lowValue && reason == "candidate_read_missing_target" {
		t.Fatalf("read_file id target must not be classified as missing target:\n%s", prompt)
	}
}

func TestPlanThenExecuteCandidateCodeNavigateWorkspaceSymbolTargetIsConcrete(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "workspace_symbol",
		Pattern:    "Value",
		NamedGap:   "locate candidate symbol before selecting edit path",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-4 (total lines: 4)",
		Output:  "1:package a\n",
	}}

	if planThenExecuteReadRequestMissingConcreteTarget(req) {
		t.Fatal("workspace_symbol code_navigate with pattern should be treated as a concrete symbol target")
	}

	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue && reason == "candidate_read_missing_target" {
		t.Fatalf("workspace_symbol code_navigate with pattern must not be classified as missing target:\n%s", prompt)
	}
}

func TestPlanExecuteMissingTargetReadRedirectDoesNotSelectCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"},{"path":"cmd/idiom/audit.go"}]}`,
	}}

	if req, raw, ok := runner.planThenExecuteMissingTargetReadRedirect(workspace.ToolRequest{Tool: "read_file"}, results); ok {
		t.Fatalf("targetless read_file must stay model-owned instead of redirecting to packet candidate, req=%#v raw=%s", req, raw)
	}
}

func TestPlanExecuteMissingTargetReadRedirectSkipsRequiredSourceScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"required.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"other.go"}]}`,
	}}

	if req, _, ok := runner.planThenExecuteMissingTargetReadRedirect(workspace.ToolRequest{Tool: "read_file"}, results); ok {
		t.Fatalf("required source scope must not redirect targetless read_file to packet candidate: %#v", req)
	}
}

func TestPlanExecuteNoWriteRecoveryLeavesNextCandidateModelOwned(t *testing.T) {
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
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}

	if read := readPathsFromResults(results); len(read) == 0 {
		t.Fatalf("fixture should expose code_navigate source-body evidence, got %#v", read)
	}

	if unread := unreadCandidateRecoveryScopePathsForRecovery(runner.RuntimeBrief, results, 4); len(unread) == 0 {
		t.Fatalf("fixture should expose an unread context-pack candidate, got none")
	}

	if req, raw, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results); ok {
		t.Fatalf("plan-execute unread candidate recovery must not host-select the next path, req=%#v raw=%s", req, raw)
	}
}

func TestPlanThenExecuteCandidatePostReadinessSamePathReadRemainsModelOwned(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {}\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 3636,
		EndLine:   3678,
		NamedGap:  "inspect the helper body again",
	}
	if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("same candidate path reread should not be a hard rejection, got %q: %s", summary, errorText)
	}

	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue {
		t.Fatalf("same candidate path reread should remain model-owned before budget exhaustion, reason=%q prompt=\n%s", reason, prompt)
	}
}

func TestPlanThenExecuteCandidatePostReadinessDifferentPathDoesNotRequireNamedGap(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "cmd/idiom/product_workflow.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"},{"path":"cmd/idiom/product_workflow.go"}]}`,
	}, {
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1500,
		EndLine:   1650,
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})

	if lowValue {
		t.Fatalf("different candidate read should not require a harness-inferred named gap before budget exhaustion, reason=%q prompt=\n%s", reason, prompt)
	}

	req.NamedGap = "exact coverage-gate call site before wiring authority preflight"
	req.WhyNotAnswering = "need the current command path before drafting the edit"

	if _, _, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); lowValue {
		t.Fatal("different candidate read with a named edit-safety gap should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSamePathContinuationRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 126,
		EndLine:   250,
	}, brief, results)
	if reject {
		t.Fatal("bounded unread same-path continuation should remain available for larger candidate files")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSmallFullReadAfterPartialRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/coverage_gate.go",
	}, brief, results)
	if reject {
		t.Fatal("full-file read of a small partially read candidate should be treated as continuation, not duplicate evidence")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsUnboundedSmallPartialSamePathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/workflows/productworkflow/validate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/validate.go lines 300-350 (total lines: 391)",
		Output:  "300:func ValidateProductWorkflow() {}\n",
	}}

	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/validate.go",
	}
	if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("unbounded reread of a small partially read candidate should not hard reject, got %q: %s", summary, errorText)
	}

	if _, _, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); lowValue {
		t.Fatal("unbounded reread of a partially read candidate should remain available as same-path continuation")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsPriorReadContinuationOutsideCurrentCandidateSet(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/workflows/productworkflow/validate.go lines 1-300 (total lines: 1449)",
			Output:  "1:package productworkflow\n",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 125,
		EndLine:   250,
	}, brief, results)
	if reject {
		t.Fatal("bounded continuation of a previously read path should not depend on current candidate ranking")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSymbolReadOnPreviouslyReadPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/workflows/productworkflow/validate.go lines 1-300 (total lines: 1449)",
			Output:  "1:package productworkflow\n",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/validate.go",
		Pattern:    "ValidateADRCrossLinks",
	}, brief, results)
	if reject {
		t.Fatal("symbol-body read on a previously read candidate path should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessRejectsNonBodyNavigationOnReadyPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "find_symbol",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Pattern:    "CoverageGateFinding",
	}, brief, results)
	if !reject {
		t.Fatal("non-body navigation on an already sourced candidate should be rejected after readiness")
	}

	if summary != "Plan-execute candidate source already ready" {
		t.Fatalf("unexpected summary %q", summary)
	}

	for _, want := range []string{"already has source-body evidence", "submit_plan", "bounded source-body continuation", "<<QUESTION>>"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanExecuteSubmitOnlySurfaceDoesNotReopenHiddenNavigation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:   PlanThenExecutePlannerTaskFamily,
		AllowedTools: []string{"code_navigate", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Candidate source already ready",
		Code:    planThenExecuteResultCandidateSourceReady,
		Error:   "terminal planner recovery active",
	}}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Pattern:    "CoverageGate",
	}

	allowed := runnerPlanExecuteTextualAllowedToolsForRequest([]string{"submit_plan"}, brief, results, req)
	if len(allowed) != 1 || allowed[0] != "submit_plan" {
		t.Fatalf("submit-only terminal surface reopened hidden navigation: %#v", allowed)
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsDifferentNamedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {}\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
		NamedGap:  "exact current gate signature before deciding whether this second candidate needs an edit",
	}, brief, results)
	if reject {
		t.Fatal("a different unread allowed candidate with a concrete named gap should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsDifferentBoundedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-4",
		Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "pkg/feature_test.go",
		StartLine: 1,
		EndLine:   4,
	}, brief, results)
	if reject {
		t.Fatal("a different unread allowed candidate with a bounded range should remain available for source-satisfied evidence")
	}
}

func TestPlanThenExecuteCandidatePostGateRejectsOutOfScopeRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 900-1080\n900:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/rendering.go",
		StartLine: 1,
		EndLine:   180,
	}, brief, results)

	if !reject {
		t.Fatal("post-gate hallucinated read outside explicit allowed candidates should be rejected")
	}

	if summary != "Plan-execute post-gate read outside candidate scope" {
		t.Fatalf("unexpected summary %q", summary)
	}

	for _, want := range []string{"completion gate", "not an explicit allowed candidate path", "cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go", "submit_plan"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}
