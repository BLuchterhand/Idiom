package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestPlanExecuteRecoveryForcesSubmitPlanOnlyAfterRejectedPlan(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update only one path so the required-scope gate rejects it.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Update every required path after the required-scope gate rejected the incomplete plan.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}

	type requestPayload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}

	var (
		requests        int
		recoveryPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	var toolTrace []string

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolTrace = append(toolTrace, "request "+event.Request.Tool+": "+event.Raw)
	}, func(event ToolResultEvent) {
		toolTrace = append(toolTrace, "result "+event.Request.Tool+": "+event.Result.Summary+" | "+event.Result.Error)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after rejected submit_plan, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}

	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after rejected submit_plan, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRejectedPlanAllowsSourceSatisfiedCoverageCorrection(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update only one path so the required-scope gate rejects it.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Correct the rejected plan: a.txt needs a real edit while b.txt already contains the requested final state from the source evidence.","source_satisfied_paths":["b.txt"],"edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
	}

	var (
		requests  int
		toolTrace []string
	)

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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolTrace = append(toolTrace, "request "+event.Request.Tool+": "+event.Raw)
	}, func(event ToolResultEvent) {
		toolTrace = append(toolTrace, "result "+event.Request.Tool+": "+event.Result.Summary+" | "+event.Result.Error)
	})
	if err != nil {
		t.Fatalf("Run: %v\ntrace:\n%s", err, strings.Join(toolTrace, "\n"))
	}

	if requests != len(replies) {
		t.Fatalf("expected corrected mixed-coverage submit_plan, got %d request(s)\ntrace:\n%s", requests, strings.Join(toolTrace, "\n"))
	}

	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected successful mixed coverage plan summary, got:\n%s\ntrace:\n%s", answer, strings.Join(toolTrace, "\n"))
	}
}

func TestPlanExecuteSubmitPlanSourceSatisfiedEvidenceBridgeRejectsBodyWindow(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []workspace.PlanEdit{{
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
			Purpose: "replace a fixture",
		}},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f2 b.txt lines 1-200 (total lines: 1000)",
		Output:  "b.txt\n" + strings.Repeat("body\n", 10),
	}})

	if bridged.PlanArtifact == nil {
		t.Fatalf("expected legacy submit_plan to be normalized into an artifact")
	}

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			t.Fatalf("body-window source evidence must not be durable source_satisfied proof: %#v", bridged.PlanArtifact.Evidence)
		}
	}
}

func TestPlanExecuteSubmitPlanSourceSatisfiedBridgeStripsModelSuppliedEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		PlanArtifact: &workspace.PlanArtifact{
			Task: workspace.PlanArtifactTask{Summary: "mixed source-satisfied coverage"},
			Contract: workspace.PlanArtifactContract{
				Kind:              "plan_then_execute",
				StrategyOwner:     "model",
				ContractOwner:     "harness",
				PermissionProfile: "plan_then_execute",
				VerificationOwner: "planner",
			},
			Paths: workspace.PlanArtifactPaths{Required: []string{"a.txt", "b.txt"}},
			Evidence: []workspace.PlanArtifactEvidence{{
				Kind: "read_file",
				Path: "b.txt",
			}},
			Edits: []workspace.PlanEdit{{
				Path:    "a.txt",
				OldText: "before\n",
				NewText: "after\n",
				Purpose: "replace a fixture",
			}},
			VerificationObligations: []workspace.PlanArtifactVerificationObligation{{
				Kind:     "executor_lane_sanity",
				Owner:    "planner",
				Status:   "open",
				Required: true,
			}},
		},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f2 b.txt lines 1-200 (total lines: 1000)",
		Output:  "b.txt\n" + strings.Repeat("body\n", 10),
	}})

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			t.Fatalf("model-supplied source_satisfied evidence must be replaced by harness-observed anchor-ready evidence, got %#v", bridged.PlanArtifact.Evidence)
		}
	}
}

func TestPlanExecuteSubmitPlanSourceSatisfiedBridgeRejectsSnippetOnlyEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
			{Path: "b.txt", Content: "already final"},
		},
	}}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []workspace.PlanEdit{{
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
			Purpose: "replace a fixture",
		}},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, nil)

	if bridged.PlanArtifact == nil {
		t.Fatalf("expected legacy submit_plan to be normalized into an artifact")
	}

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			t.Fatalf("required_edit_snippet must not be durable source_satisfied proof without observed source evidence: %#v", bridged.PlanArtifact.Evidence)
		}
	}
}

func TestPlanExecuteSubmitPlanCoverageAcceptsArtifactSourceSatisfiedPaths(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &workspace.PlanArtifact{
			Paths: workspace.PlanArtifactPaths{SourceSatisfied: []string{"b.txt"}},
			Edits: []workspace.PlanEdit{
				{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "replace a fixture"},
			},
		},
	}

	if result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); result.Intercepted {
		t.Fatalf("artifact paths.source_satisfied should satisfy required coverage, got %#v", result.Result)
	}
}

func TestPlanExecuteSubmitPlanCoverageAcceptsSourceSatisfiedRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "b.txt", Content: "already final"},
			},
		},
	}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before\n", NewText: "after\n", Purpose: "replace a fixture"},
		},
	}

	if result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); result.Intercepted {
		t.Fatalf("source_satisfied_paths should satisfy required snippet coverage from current source, got %#v", result.Result)
	}
}

func TestPlanExecuteSubmitPlanCoverageAcceptsArtifactSourceSatisfiedRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "b.txt", Content: "already final"},
			},
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &workspace.PlanArtifact{
			Paths: workspace.PlanArtifactPaths{SourceSatisfied: []string{"b.txt"}},
			Edits: []workspace.PlanEdit{
				{Path: "a.txt", OldText: "before\n", NewText: "after\n", Purpose: "replace a fixture"},
			},
		},
	}

	if result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); result.Intercepted {
		t.Fatalf("artifact source_satisfied paths should satisfy required snippet coverage from current source, got %#v", result.Result)
	}
}

func TestPlanExecuteSubmitPlanCoverageRejectsSourceSatisfiedMissingRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("not final\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{ID: "b_final", Path: "b.txt", Content: "already final"},
			},
		},
	}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before\n", NewText: "after\n", Purpose: "replace a fixture"},
		},
	}

	result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req)
	if !result.Intercepted || !strings.Contains(result.Result.Error, "missing required edit snippet(s): `b_final`") {
		t.Fatalf("expected missing required snippet rejection, got %#v", result)
	}
}

func TestPlanExecuteSubmitPlanCoverageRejectsOutOfScopeSourceSatisfiedPaths(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}}
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		SourceSatisfiedPaths: []string{"outside.txt"},
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "replace a fixture"},
		},
	}

	result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req)
	if !result.Intercepted || !strings.Contains(result.Result.Error, "path(s) outside required_edit_paths: `outside.txt`") {
		t.Fatalf("expected out-of-scope source_satisfied_paths rejection, got %#v", result)
	}
}

func TestPlanExecuteRejectedMissingCoverageRequiresCorrectedSubmitPlan(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update only one path so the guard rejects missing required edit coverage.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Correct the rejected coverage mismatch and cover every required path.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after b\n","purpose":"replace b fixture"}]}`,
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected corrected submit_plan after rejected post-refresh read, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecutePartialNoopSubmitPlanFallsThroughToCorrection(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update one file but accidentally include a no-op for the second required path.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"before\n","purpose":"replace b fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Correct the rejected no-op so every required path has a real edit.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after b\n","purpose":"replace b fixture"}]}`,
	}

	var (
		requests              int
		sawSubmitPlanRequired bool
	)

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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_short_circuit" {
				t.Fatalf("partial no-op submit_plan must not source-satisfy every required path: %s", event.Content)
			}

			if event.Role == "plan_then_execute_submit_plan_required" {
				sawSubmitPlanRequired = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected recovery to request a corrected submit_plan, got %d request(s)", requests)
	}

	if !sawSubmitPlanRequired {
		t.Fatalf("expected submit-correction recovery event")
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteCandidateRejectedPlanDoesNotAcceptGenericBlockedProse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"submit_plan","rationale":"Use a stale candidate anchor so the plan is rejected.","edits":[{"path":"a.txt","old_text":"missing\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		"The planner is blocked because candidate source is already ready but no plan was submitted.",
		"<<QUESTION>>: exact blocker: current source anchor for a.txt could not be recovered after the rejected submit_plan.",
	}

	var (
		requests                int
		sawDeterministicRefresh bool
	)

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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_recovery_auto_grounding" {
				sawDeterministicRefresh = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan candidate edit"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("generic blocked prose should have been rejected and retried, got %d request(s)", requests)
	}

	if !sawDeterministicRefresh {
		t.Fatal("expected generic blocked prose to trigger deterministic anchor refresh")
	}

	if !strings.Contains(answer, "exact blocker") {
		t.Fatalf("expected final typed blocker, got:\n%s", answer)
	}
}

func TestPlanExecuteLargeInsertAfterLineRecoveryNamesLineRangeAlternative(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	large := strings.Repeat("inserted line\n", 360)
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		fmt.Sprintf(`{"tool":"submit_plan","rationale":"Use an oversized insertion so schema recovery must name line-range replacement.","edits":[{"path":"a.txt","insert_after_line":1,"content":%q,"purpose":"insert a large block"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`, large),
		fmt.Sprintf(`{"tool":"submit_plan","rationale":"Correct large insertion with line-range replacement.","edits":[{"path":"a.txt","start_line":1,"end_line":1,"content":%q,"purpose":"replace anchor with anchor plus inserted block"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`, "before\n"+large),
	}

	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}

	var (
		requests        int
		recoveryPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after large insert rejection, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}

	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after large insert rejection, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}

	var correction string

	for _, message := range recoveryPayload.Messages {
		if strings.Contains(message.Content, "do not repeat `insert_after_line`") ||
			strings.Contains(message.Content, "Do not repeat `insert_after_line`") {

			correction = message.Content
			break
		}
	}

	if correction == "" {
		t.Fatalf("large insertion recovery prompt missing from request messages: %#v", recoveryPayload.Messages)
	}

	for _, want := range []string{"do not repeat `insert_after_line`", "start_line", "end_line", "unchanged anchor line"} {
		if !strings.Contains(correction, want) {
			t.Fatalf("large insertion recovery prompt missing %q:\n%s", want, correction)
		}
	}

	if strings.Contains(correction, `"insert_after_line":42`) {
		t.Fatalf("large insertion recovery should not render generic insertion example:\n%s", correction)
	}

	for _, forbidden := range []string{"<<QUESTION>>", "path-specific blocker"} {
		if strings.Contains(correction, forbidden) {
			t.Fatalf("large insertion submit-correction prompt advertised stale exit %q:\n%s", forbidden, correction)
		}
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRecoveryForcesAnchorRefreshAfterInvalidSubmitPlan(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Use a stale anchor so recovery must refresh source before another plan.","edits":[{"path":"a.txt","old_text":"missing\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","rationale":"Update every required path after refreshing the stale anchor.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}

	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}

	var (
		requests        int
		recoveryPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode anchor recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("anchor recovery request should require a parser-readable source refresh, got %q", recoveryPayload.ToolChoice)
	}

	var exposed []string

	for _, tool := range recoveryPayload.Tools {
		exposed = append(exposed, tool.Function.Name)
	}

	slices.Sort(exposed)

	if !slices.Equal(exposed, []string{"code_navigate", "read_file"}) {
		t.Fatalf("anchor recovery should expose only source refresh tools, got %#v", recoveryPayload.Tools)
	}

	var recoveryText strings.Builder

	for _, message := range recoveryPayload.Messages {
		recoveryText.WriteString(message.Role)
		recoveryText.WriteString(": ")
		recoveryText.WriteString(message.Content)
		recoveryText.WriteString("\n")
	}

	for _, want := range []string{"anchor-refresh phase", "bounded source-body request for one of those paths", "Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`"} {
		if !strings.Contains(recoveryText.String(), want) {
			t.Fatalf("anchor recovery request missing %q:\n%s", want, recoveryText.String())
		}
	}

	if strings.Contains(recoveryText.String(), "path_specific_blocker_reported") {
		t.Fatalf("anchor recovery request must not advertise blocker stop condition:\n%s", recoveryText.String())
	}

	if strings.Contains(recoveryText.String(), "The next visible action must be a corrected parser-readable `submit_plan` request") {
		t.Fatalf("anchor recovery request must not also demand corrected submit_plan before refresh:\n%s", recoveryText.String())
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRepairsStringifiedSubmitPlanEditsBeforeValidation(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad schema: edits is stringified JSON.","edits":"[{\"path\":\"a.txt\",\"old_text\":\"before\\n\",\"new_text\":\"after\\n\",\"purpose\":\"replace a fixture\"},{\"path\":\"b.txt\",\"old_text\":\"before\\n\",\"new_text\":\"after\\n\",\"purpose\":\"replace b fixture\"}]"}`,
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("valid stringified edits should be repaired without another model request, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected repaired plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteParseRecoveryForcesSubmitPlanOnlyAfterUnrepairableStringifiedEdits(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad schema: edits is a non-JSON string.","edits":"not json"}`,
		`{"tool":"submit_plan","rationale":"Corrected schema after parse recovery.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}

	type requestPayload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice         string `json:"tool_choice"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
		} `json:"chat_template_kwargs,omitempty"`
	}

	var (
		requests         int
		recoveryPayload  requestPayload
		sawParseRecovery bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_submit_plan_parse_recovery" {
				sawParseRecovery = true

				if !strings.Contains(event.Content, "Do not call read/search/discovery") {
					t.Fatalf("parse recovery prompt should block renewed discovery, got:\n%s", event.Content)
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !sawParseRecovery {
		t.Fatalf("expected plan_then_execute_submit_plan_parse_recovery event")
	}

	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after unrepairable stringified-edits parse failure, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}

	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after unrepairable stringified-edits parse failure, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}

	if recoveryPayload.ChatTemplateKwargs == nil || recoveryPayload.ChatTemplateKwargs.EnableThinking == nil || *recoveryPayload.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("parse recovery should force visible submit_plan output over hidden thinking, got %#v", recoveryPayload.ChatTemplateKwargs)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}
