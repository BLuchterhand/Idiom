package agent

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
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanExecuteNativeNoToolCallFallbackStaysTextualAfterGateRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	type requestPayload struct {
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}

	var (
		requests        int
		fallbackPayload requestPayload
		postGatePayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request %d: %v\n%s", requests, err, string(body))
		}

		switch requests {
		case 0:
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go"}`}}}})
		case 1:
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
					"id": "call_submit", "type": "function",
					"function": map[string]any{"name": "submit_plan", "arguments": `{"rationale":"incomplete","edits":[]}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
		case 2:
			requests++

			http.Error(w, `{"detail":"tool_choice='required' was set but the model did not produce any tool calls."}`, http.StatusBadRequest)
		case 3:
			fallbackPayload = payload
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "I will submit the corrected plan now."}}}})
		case 4:
			postGatePayload = payload
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"submit_plan","rationale":"Update value.","edits":[{"path":"a.go","old_text":"return 1","new_text":"return 2","purpose":"change returned value"}]}`}}}})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "required",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fallbackPayload.Tools) != 0 || fallbackPayload.ToolChoice != "" {
		t.Fatalf("native no-call fallback request should be textual, got tools=%#v choice=%q", fallbackPayload.Tools, fallbackPayload.ToolChoice)
	}

	if len(postGatePayload.Tools) != 0 || postGatePayload.ToolChoice != "" {
		t.Fatalf("post-gate recovery after textual fallback must stay textual, got tools=%#v choice=%q", postGatePayload.Tools, postGatePayload.ToolChoice)
	}

	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected submit_plan success, got:\n%s", answer)
	}
}

func TestPlanExecuteMLXSubmitPlanAfterRefreshPromptMatchesNativeTransport(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	type requestPayload struct {
		Messages   []llama.ChatMessage             `json:"messages"`
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}

	var (
		requests               int
		correctedSubmitPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request %d: %v\n%s", requests, err, string(body))
		}

		switch requests {
		case 0:
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go","start_line":1,"end_line":6}`}}}})
		case 1:
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
					"id": "call_bad_submit", "type": "function",
					"function": map[string]any{"name": "submit_plan", "arguments": `{"rationale":"Bad anchor should require refresh.","edits":[{"path":"a.go","old_text":"return missing","new_text":"return 2","purpose":"change returned value"}]}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
		case 2:
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go","start_line":1,"end_line":6}`}}}})
		case 3:
			correctedSubmitPayload = payload
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"submit_plan","rationale":"Corrected after refresh.","edits":[{"path":"a.go","old_text":"return 1","new_text":"return 2","purpose":"change returned value"}]}`}}}})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{
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

	if correctedSubmitPayload.ToolChoice != "" || len(correctedSubmitPayload.Tools) != 0 {
		t.Fatalf("post-refresh MLX request should stay textual, got tools=%#v choice=%q", correctedSubmitPayload.Tools, correctedSubmitPayload.ToolChoice)
	}

	var promptText strings.Builder

	for _, msg := range correctedSubmitPayload.Messages {
		promptText.WriteString(msg.Content)
		promptText.WriteByte('\n')
	}

	joined := promptText.String()
	for _, forbidden := range []string{"native `submit_plan`", "Native `submit_plan`"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("post-refresh textual request leaked native guidance %q:\n%s", forbidden, joined)
		}
	}

	for _, want := range []string{"Textual-tool backends must emit exactly one visible JSON object", "no prose, markdown commentary"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("post-refresh textual request missing %q:\n%s", want, joined)
		}
	}

	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected submit_plan success, got:\n%s", answer)
	}
}

func TestPlanThenExecuteUnreadRequiredSourceNudgeFiresAfterOutlineOnlyPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 33 top-level declaration(s) in cmd/idiom/product_workflow_test.go.\n",
		},
	}

	got := planThenExecuteUnreadRequiredSourceNudge(brief, results)
	if got == "" {
		t.Fatalf("expected unread required source nudge")
	}

	for _, want := range []string{
		"cmd/idiom/product_workflow_test.go",
		"read_symbol",
		"start_line",
		"Do not call `submit_plan`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanThenExecuteUnreadRequiredSourceChatOptionsCapsNextCall(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}
	thinkingOn := true
	opts := planThenExecuteUnreadRequiredSourceChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
	}, brief, results)

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("unread-source nudge should force thinking off for the next short tool call")
	}

	if opts.PreserveThinking {
		t.Fatalf("unread-source nudge should not preserve prior hidden reasoning")
	}

	if opts.MaxTokens != 768 {
		t.Fatalf("MaxTokens = %d, want 768", opts.MaxTokens)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeWaitsForFullCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10"},
		// c.txt has not been read — the nudge must wait.
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must NOT fire while a required path is unread; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeFiresForCandidateScopeRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"},
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatalf("expected candidate-scope readiness nudge after a read")
	}

	for _, want := range []string{
		"at least one selected candidate edit path has source evidence",
		"internal/surfaces/tui/app/rendering.go",
		"submit_plan",
		"Decision: source-satisfied",
		"<<QUESTION>>",
		"Hidden reasoning is not executable evidence",
		"Allowed/candidate paths are not coverage obligations",
		"Do not reread a candidate path that already has source-body evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate-scope nudge missing %q: %q", want, got)
		}
	}

	if strings.Contains(got, "Only read/search again") {
		t.Fatalf("candidate-scope nudge must not retain stale optional-read language: %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeDoesNotRequireUnreadUnscopedCandidates(t *testing.T) {
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
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverage in internal/workflows/productworkflow/coverage_gate.go lines 20-80\n\n20:func EvaluateCoverage() {}\n",
		},
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatal("unscoped context candidates should receive submit-ready guidance after a selected source-body read")
	}

	for _, want := range []string{
		"coverage_gate.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
		"submit_plan",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate readiness nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeIgnoresUnselectedEvidenceOnlyRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("evidence-only unscoped reads must not receive submit-ready nudge without selected candidate path, got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeDoesNotRequireAllAllowedCandidates(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
		RequiredEditPaths: nil,
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {\n3637:\treturn `{}`\n3638:}\n",
	}}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatal("candidate-only allowed scope must become submit-ready after one anchor-ready candidate read")
	}

	if !strings.Contains(got, "cmd/idiom/product_workflow_test.go") || !strings.Contains(got, "Allowed/candidate paths are not coverage obligations") {
		t.Fatalf("nudge should cite the read path and not promote allowed paths to required coverage:\n%s", got)
	}

	if strings.Contains(got, "cmd/idiom/product_workflow.go") || strings.Contains(got, "internal/workflows/productworkflow/coverage_gate.go") {
		t.Fatalf("nudge should not demand unread allowed candidates as coverage obligations:\n%s", got)
	}
}

func TestPlanExecutePlanningStateKeepsCandidatesOutOfRequiredCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-40 (total lines: 40)",
		Output:  "1:package pkg\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAllowedCandidate {
		t.Fatalf("mode = %q, want allowed_candidate", state.Mode)
	}

	if len(state.RequiredCoveragePaths) != 0 {
		t.Fatalf("allowed candidates must not become required coverage: %#v", state.RequiredCoveragePaths)
	}

	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		t.Fatalf("selected edit paths should come from source evidence, got %#v", state.SelectedEditPaths)
	}

	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("terminal options should include submit_plan once selected source evidence exists: %#v", state.TerminalOptions)
	}
}

func TestPlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/types.go lines 1-40 (total lines: 40)",
		Output:  "1:package productworkflow\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if len(state.RequiredCoveragePaths) != 0 {
		t.Fatalf("unexpected required coverage paths: %#v", state.RequiredCoveragePaths)
	}

	if len(state.SelectedEditPaths) != 0 {
		t.Fatalf("evidence-only paths must not become selected edit paths: %#v", state.SelectedEditPaths)
	}

	if slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan must not be advertised without concrete selected edit paths: %#v", state.TerminalOptions)
	}
}

func TestPlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPathViaContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/other.go lines 1-40 (total lines: 40)",
			Output:  "1:package pkg\n",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if len(state.SelectedEditPaths) != 0 {
		t.Fatalf("context-pack candidates must not make unrelated evidence-only paths selected: %#v", state.SelectedEditPaths)
	}

	if slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan must not be advertised without a selected edit path: %#v", state.TerminalOptions)
	}

	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(brief, results, false)
	if strings.Contains(skeleton, "pkg/feature.go") || strings.Contains(skeleton, "pkg/other.go") {
		t.Fatalf("skeleton must not promote context-pack or evidence-only paths:\n%s", skeleton)
	}

	if !strings.Contains(skeleton, "No concrete selected edit path") {
		t.Fatalf("skeleton should report no safe selected path:\n%s", skeleton)
	}
}

func TestPlanExecutePlanningStateSelectsContextPackCandidateWithSourceEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-40 (total lines: 40)",
			Output:  "1:package pkg\n",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		t.Fatalf("selected edit paths should be context-pack candidates with source evidence, got %#v", state.SelectedEditPaths)
	}

	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan should be available once a typed candidate has source evidence: %#v", state.TerminalOptions)
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("typed context-pack candidate with source evidence should allow terminal submit_plan surface")
	}
}

func TestPlanExecutePlanningStateCorrectionSelectsInvalidArtifactPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `b.go: old_text not found in current source`.",
		},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read Target in b.go lines 1-20\n1:package b"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction", state.Mode)
	}

	if !slices.Equal(state.RequiredCoveragePaths, []string{"a.go", "b.go"}) {
		t.Fatalf("required coverage paths changed unexpectedly: %#v", state.RequiredCoveragePaths)
	}

	if !slices.Equal(state.LastInvalidPaths, []string{"b.go"}) {
		t.Fatalf("last invalid paths = %#v, want b.go", state.LastInvalidPaths)
	}

	if !slices.Equal(state.SelectedEditPaths, []string{"a.go", "b.go"}) {
		t.Fatalf("correction selected edit paths must preserve required coverage, got %#v", state.SelectedEditPaths)
	}

	if !slices.Equal(planThenExecuteSubmitPlanSkeletonPaths(state), []string{"a.go", "b.go"}) {
		t.Fatalf("correction skeleton paths must preserve required coverage, got %#v", planThenExecuteSubmitPlanSkeletonPaths(state))
	}

	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(brief, results, false)
	if !strings.Contains(skeleton, "required edit path: b.go") {
		t.Fatalf("correction skeleton should include invalid path b.go:\n%s", skeleton)
	}

	if !strings.Contains(skeleton, "required edit path: a.go") {
		t.Fatalf("correction skeleton should preserve all required paths:\n%s", skeleton)
	}

	if !slices.Equal(state.TerminalResponseShapes, []string{"submit_plan"}) {
		t.Fatalf("submit correction must expose only submit_plan, got %#v", state.TerminalResponseShapes)
	}

	if slices.Contains(state.AllowedActionClasses, actionSourceSatisfied) || slices.Contains(state.AllowedActionClasses, actionBlocker) {
		t.Fatalf("submit correction should not advertise escape hatches after sufficient source evidence: %#v", state.AllowedActionClasses)
	}
}

func TestPlanExecuteSubmitCorrectionRejectsSourceSatisfiedCloseout(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
		},
	}
	answer := "Decision: source-satisfied\n- a.go: requested final state is already present.\n- b.go: requested final state is already present."

	if !planThenExecuteSubmitCorrectionActive(runner.RuntimeBrief, results) {
		t.Fatal("expected submit-correction mode")
	}

	if runner.planThenExecuteSourceSatisfiedAnswerCovers(answer, results) {
		t.Fatal("submit-correction must not accept source-satisfied closeout after a rejected submit_plan")
	}

	blocker := "<<QUESTION>>: exact blocker: a.go and b.go cannot be updated from the rejected submit_plan evidence."
	result, rejected := runner.requiredPathCompletionRecoveryRejection(blocker, results)

	if !rejected {
		t.Fatal("submit-correction must reject blocker closeout after a rejected submit_plan")
	}

	if !strings.Contains(result.Error, "exactly one valid outcome") || !strings.Contains(result.Error, "submit_plan") {
		t.Fatalf("submit-correction blocker rejection should force submit_plan, got %#v", result)
	}
}

func TestPlanExecuteSourceSatisfiedSubmitPlanRejectionDoesNotEnterSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	runner := &Runner{RuntimeBrief: brief}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-1 (total lines: 1)", Output: "package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-1 (total lines: 1)", Output: "package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}
	answer := "Decision: source-satisfied\n- a.go: requested final state is already present.\n- b.go: requested final state is already present."

	if planThenExecuteSubmitCorrectionActive(brief, results) {
		t.Fatal("source-satisfied terminal-shape rejection should not enter submit-correction mode")
	}

	if !runner.planThenExecuteSourceSatisfiedAnswerCovers(answer, results) {
		t.Fatal("explicit source-satisfied answer should remain available after typed terminal-shape rejection")
	}

	afterNoopRejection := append([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: edit would not change a.go; submit real edits, or answer `Decision: source-satisfied` with current-state evidence when no changes are needed",
		},
	}, results[2])
	state := DerivePlanExecutePlanningState(brief, afterNoopRejection, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode after source-satisfied rejection following no-op = %q, want submit_ready", state.Mode)
	}

	if planThenExecuteSubmitCorrectionActive(brief, afterNoopRejection) {
		t.Fatal("source-satisfied terminal-shape rejection after no-op should exit submit-correction mode")
	}

	if !slices.Contains(state.TerminalResponseShapes, "Decision: source-satisfied") {
		t.Fatalf("source-satisfied answer shape should be restored: %#v", state.TerminalResponseShapes)
	}
}

func TestPlanExecuteSourceSatisfiedRationaleRejectionDoesNotEnterSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	runner := &Runner{RuntimeBrief: brief}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied rationale",
			Error:   "submit_plan rationale says the requested final state is already present or that edits are placeholders. Do not submit verification/placeholder edits; answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact blocker.",
		},
	}
	answer := "Decision: source-satisfied\n- a.go: requested final state is already present.\n- b.go: requested final state is already present."

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode = %q, want submit_ready", state.Mode)
	}

	if planThenExecuteSubmitCorrectionActive(brief, results) {
		t.Fatal("source-satisfied rationale rejection should not enter submit-correction mode")
	}

	if !slices.Contains(state.TerminalResponseShapes, "Decision: source-satisfied") {
		t.Fatalf("source-satisfied answer shape should remain available: %#v", state.TerminalResponseShapes)
	}

	if !runner.planThenExecuteSourceSatisfiedAnswerCovers(answer, results) {
		t.Fatal("explicit source-satisfied answer should remain available after source-satisfied rationale rejection")
	}
}

func TestPlanExecuteSubmitCorrectionTerminalBriefIsSubmitPlanOnly(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
		},
	}

	brief := runner.planThenExecuteTerminalToolBrief(results)
	if !slices.Contains(brief.ReadinessGate, "submit_plan_correction_required") {
		t.Fatalf("submit-correction terminal brief missing strict readiness: %#v", brief.ReadinessGate)
	}

	joinedNext := strings.Join(brief.RequiredNext, "\n")
	if !strings.Contains(joinedNext, "corrected submit_plan") {
		t.Fatalf("submit-correction terminal brief missing strict next action: %#v", brief.RequiredNext)
	}

	for _, forbidden := range []string{"submit_plan_or_source_satisfied_or_blocker_reported", "source_satisfied_or_blocker_only_with_per_path_evidence"} {
		if slices.Contains(brief.ReadinessGate, forbidden) || slices.Contains(brief.FinalCritic, forbidden) {
			t.Fatalf("submit-correction terminal brief advertised stale exit %q: readiness=%#v final=%#v", forbidden, brief.ReadinessGate, brief.FinalCritic)
		}
	}

	for _, forbidden := range []string{"Decision: source-satisfied with per-path evidence", "submit_plan covering every required_edit_path with real edits or source_satisfied_paths entries, Decision: source-satisfied"} {
		if strings.Contains(joinedNext, forbidden) || strings.Contains(strings.Join(brief.InvalidActions, "\n"), "final answer before submit_plan succeeds unless it is a source-satisfied closeout") {
			t.Fatalf("submit-correction terminal brief retained stale next/invalid guidance %q: next=%#v invalid=%#v", forbidden, brief.RequiredNext, brief.InvalidActions)
		}
	}
}

func TestPlanExecuteSubmitCorrectionChecklistNamesNoopSourceSatisfiedPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if !strings.Contains(checklist, "a.txt=must-keep-valid-coverage") {
		t.Fatalf("checklist should preserve valid prior coverage for unaffected path:\n%s", checklist)
	}

	if !strings.Contains(checklist, "b.txt=use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit") {
		t.Fatalf("checklist should steer no-op path to source_satisfied_paths or a real edit:\n%s", checklist)
	}
}

func TestPlanExecuteSubmitCorrectionChecklistParsesSequenceNoopPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change b.txt after applying prior plan edits; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if strings.Contains(checklist, "after applying prior plan edits") {
		t.Fatalf("checklist parsed extra prose as path:\n%s", checklist)
	}

	if !strings.Contains(checklist, "b.txt=use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit") {
		t.Fatalf("checklist should identify no-op path from sequence preflight:\n%s", checklist)
	}
}

func TestPlanExecuteSubmitCorrectionChecklistCoversPathlessNoop(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if !strings.Contains(checklist, "a.txt=must-have-real-edit-or-source_satisfied_paths-entry-no-noop") ||
		!strings.Contains(checklist, "b.txt=must-have-real-edit-or-source_satisfied_paths-entry-no-noop") {

		t.Fatalf("pathless no-op should force every required path away from no-op edits:\n%s", checklist)
	}
}

func TestPlanExecutePlanningStateDerivesTextualJSONTerminalTransport(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}, PlanExecutePlanningTransport{
		TextualToolParser:  true,
		TerminalOnly:       true,
		ResponseFormatJSON: true,
		EffectiveMaxTokens: 4096,
		TimeoutMillis:      300000,
	})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode = %q, want submit_ready", state.Mode)
	}

	if state.TransportMode != PlanExecuteTransportModeTextualJSON {
		t.Fatalf("transport mode = %q, want textual_json_terminal", state.TransportMode)
	}

	if !state.RuntimePolicy.ResponseFormatJSON || state.RuntimePolicy.EffectiveMaxTokens != 4096 || state.RuntimePolicy.TimeoutMillis != 300000 {
		t.Fatalf("runtime policy not preserved: %#v", state.RuntimePolicy)
	}
}

func TestPlanExecuteDuplicateRejectedPlanStaysInSubmitCorrection(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: edit would introduce adjacent duplicate code line 7 in b.go: \"const ready = true\"",
		},
	}, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction", state.Mode)
	}

	if !slices.Contains(state.InvalidActionClasses, actionSourceSatisfied) {
		t.Fatalf("source-satisfied should be invalid after an untyped duplicate/no-op rejection: %#v", state.InvalidActionClasses)
	}
}

func TestPlanExecutePlanningStateKeepsRequiredSourceBeforeHistoryUntilAllRequiredPathsRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Fatalf("mode = %q, want required_source while b.go is unread", state.Mode)
	}

	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			t.Fatalf("required-source phase missing %s: %#v", want, state.ParserVisibleTools)
		}
	}

	for _, hidden := range []string{"history_range", "history_symbol", "submit_plan"} {
		if slices.Contains(state.ParserVisibleTools, hidden) {
			t.Fatalf("required-source phase should not expose %s before all required paths are read: %#v", hidden, state.ParserVisibleTools)
		}
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            brief,
		Results:          results,
		BaseAllowedTools: brief.AllowedTools,
	})
	if contract.PhaseID != turnPhaseRequiredSource {
		t.Fatalf("phase = %q, want %q", contract.PhaseID, turnPhaseRequiredSource)
	}

	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(contract.AllowedTools, want) {
			t.Fatalf("required-source contract missing %s: %#v", want, contract.AllowedTools)
		}
	}

	for _, hidden := range []string{"history_range", "history_symbol", "submit_plan"} {
		if slices.Contains(contract.AllowedTools, hidden) {
			t.Fatalf("required-source contract should not expose %s before all required paths are read: %#v", hidden, contract.AllowedTools)
		}
	}

	if !slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) {
		t.Fatalf("required-source contract should keep read allowed: %#v", contract.ActionPolicy.AllowedActionClasses)
	}

	if slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan) {
		t.Fatalf("required-source contract should not allow submit_plan action class: %#v", contract.ActionPolicy.AllowedActionClasses)
	}

	withAllSource := append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 b.go lines 1-10 (total lines: 10)",
		Output:  "1:package b\n",
	})

	state = DerivePlanExecutePlanningState(brief, withAllSource, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode after all required source reads = %q, want history_pending", state.Mode)
	}

	for _, want := range []string{"a.go", "b.go"} {
		if !slices.Contains(state.SelectedEditPaths, want) {
			t.Fatalf("history-pending selected paths missing %s: %#v", want, state.SelectedEditPaths)
		}
	}
}

func TestPlanExecutePlanningStateRequiresHistoryAfterSourceEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode = %q, want history_pending", state.Mode)
	}

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("history-pending phase must not advertise submit_plan before git history evidence: %#v", state.ParserVisibleTools)
	}

	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			t.Fatalf("history-pending phase missing %s: %#v", want, state.ParserVisibleTools)
		}
	}

	if slices.Contains(state.ParserVisibleTools, "read_file") {
		t.Fatalf("history-pending phase must not advertise read_file after source evidence is satisfied: %#v", state.ParserVisibleTools)
	}

	if !slices.Contains(state.AllowedActionClasses, actionRead) {
		t.Fatalf("history-pending phase should expose history tools as read actions: %#v", state.AllowedActionClasses)
	}

	if slices.Contains(state.AllowedActionClasses, "history") {
		t.Fatalf("history-pending phase should not advertise non-canonical history action class: %#v", state.AllowedActionClasses)
	}

	if slices.Contains(state.InvalidActionClasses, actionRead) {
		t.Fatalf("history-pending phase should not invalidate read action class for history tools: %#v", state.InvalidActionClasses)
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            brief,
		Results:          results,
		BaseAllowedTools: brief.AllowedTools,
	})
	if contract.PhaseID != turnPhaseHistoryPending {
		t.Fatalf("phase = %q, want %q", contract.PhaseID, turnPhaseHistoryPending)
	}

	if slices.Contains(contract.AllowedTools, "submit_plan") {
		t.Fatalf("history-pending contract must not advertise submit_plan before git history evidence: %#v", contract.AllowedTools)
	}

	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(contract.AllowedTools, want) {
			t.Fatalf("history-pending contract missing %s: %#v", want, contract.AllowedTools)
		}
	}

	if slices.Contains(contract.AllowedTools, "read_file") {
		t.Fatalf("history-pending contract must not advertise read_file after source evidence is satisfied: %#v", contract.AllowedTools)
	}

	if slices.Contains(contract.TerminalOutcomes, "submit_plan") {
		t.Fatalf("history-pending contract must not list submit_plan as a terminal outcome: %#v", contract.TerminalOutcomes)
	}

	if !slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) {
		t.Fatalf("history-pending contract should expose history tools as read actions: %#v", contract.ActionPolicy.AllowedActionClasses)
	}

	if slices.Contains(contract.ActionPolicy.AllowedActionClasses, "history") {
		t.Fatalf("history-pending contract should not advertise non-canonical history action class: %#v", contract.ActionPolicy.AllowedActionClasses)
	}

	if slices.Contains(contract.RuntimeCall.ExpressibleActionClasses, "history") {
		t.Fatalf("history-pending runtime contract should only use canonical action classes: %#v", contract.RuntimeCall.ExpressibleActionClasses)
	}

	textualContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(textualContract.AllowedTools, want) {
			t.Fatalf("textual history-pending contract missing %s: %#v", want, textualContract.AllowedTools)
		}
	}

	if slices.Contains(textualContract.AllowedTools, "read_file") {
		t.Fatalf("textual history-pending contract must not advertise read_file after source evidence is satisfied: %#v", textualContract.AllowedTools)
	}

	if slices.Contains(textualContract.AllowedTools, "submit_plan") {
		t.Fatalf("textual history-pending contract must hide submit_plan before git history evidence: %#v", textualContract.AllowedTools)
	}

	withHistory := append(results, workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for a.go lines 1-10",
		Output:  "commit abc123\n\n    explain current behavior",
	})

	state = DerivePlanExecutePlanningState(brief, withHistory, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode after history = %q, want submit_ready", state.Mode)
	}

	if !slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("submit_ready phase should expose submit_plan after history: %#v", state.ParserVisibleTools)
	}
}

func TestPlanExecutePartialRequiredHistoryDoesNotAdvertiseSubmitPlanReadiness(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{Tool: "history_range", Summary: "Read git history for a.go lines 1-10", Output: "commit abc123\n\n    explain current behavior"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode with one required path still missing history = %q, want history_pending", state.Mode)
	}

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("partial history must not advertise submit_plan: %#v", state.ParserVisibleTools)
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("partial history must not emit submit-readiness nudge:\n%s", got)
	}

	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("partial history must not be submit-plan-only ready")
	}

	pending := planThenExecuteHistoryPendingPaths(brief, results, nil)
	assertEqualSet(t, pending, []string{"b.go"})

	runner := &Runner{RuntimeBrief: brief}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		t.Fatal("history-pending completion rejection should produce recovery guidance")
	}

	if hideReads {
		t.Fatalf("history-pending recovery must keep history tools available, prompt:\n%s", prompt)
	}

	for _, want := range []string{"history-pending", "history_range", "b.go", "Do not call `submit_plan` yet"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("history recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecutePostReadinessAllowsRequiredHistoryRead(t *testing.T) {
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
		Tool:      "history_range",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}

	if summary, errText, reject := planThenExecutePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("history read after source evidence should remain allowed, got %q: %s", summary, errText)
	}
}
