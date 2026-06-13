package agent

import (
	"context"
	"encoding/json"
	"io"
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

func TestPlanExecuteRepeatedInvalidSubmitPlanGetsStrictCorrectionRetry(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	invalidPlan := `{"tool":"submit_plan","rationale":"No edit entries yet.","edits":[]}`
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		invalidPlan,
		invalidPlan,
		`{"tool":"submit_plan","rationale":"Corrected after strict repeat recovery.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}

	var (
		requests            int
		sawRepeatCorrection bool
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
			if event.Role == "plan_then_execute_repeated_submit_plan_correction" {
				sawRepeatCorrection = true

				if !strings.Contains(event.Content, "read/search/discovery") {
					t.Fatalf("repeat correction prompt should forbid renewed discovery:\n%s", event.Content)
				}

				if strings.Contains(event.Content, "Only use `Decision: source-satisfied`") || strings.Contains(event.Content, "Only use `<<QUESTION>>:`") {
					t.Fatalf("repeat correction prompt should not advertise stale terminal exits:\n%s", event.Content)
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

	if !sawRepeatCorrection {
		t.Fatalf("expected repeated invalid submit_plan correction event")
	}

	if requests != len(replies) {
		t.Fatalf("expected corrected plan after repeated invalid submit_plan, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteSubmitCorrectionWrongReadUsesStrictRunnerBranch(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Missing required path coverage.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		`{"tool":"read_file","path":"new_file.txt"}`,
		`{"tool":"submit_plan","rationale":"Corrected after strict wrong-tool recovery.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}

	var (
		requests                 int
		sawStrictWrongToolPrompt bool
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
			if event.Role != "plan_then_execute_submit_plan_required" {
				return
			}

			if strings.Contains(event.Content, "submit-correction mode") {
				sawStrictWrongToolPrompt = true

				for _, forbidden := range []string{
					"report a concrete blocker",
					"Answer from gathered evidence",
					"NEW FILE you must create",
					"Only use `<<QUESTION>>:`",
					"Only use `Decision: source-satisfied`",
				} {
					if strings.Contains(event.Content, forbidden) {
						t.Fatalf("strict submit-correction prompt leaked fallback %q:\n%s", forbidden, event.Content)
					}
				}
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

	if !sawStrictWrongToolPrompt {
		t.Fatalf("expected strict submit-correction wrong-tool prompt")
	}

	if requests != len(replies) {
		t.Fatalf("expected corrected plan after wrong-tool recovery, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRepeatedSubmitPlanExhaustedRejectionStaysSubmitPlanOnly(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `a.txt`, `b.txt`.",
		},
	}
	last := results[len(results)-1]
	summary, errorText, ok := runner.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(workspace.ToolRequest{Tool: "submit_plan"}, &last, results)

	if !ok {
		t.Fatal("expected exhausted repeated submit_plan rejection")
	}

	if summary != "Repeated invalid submit_plan rejected" {
		t.Fatalf("summary = %q", summary)
	}

	for _, forbidden := range []string{"Choose a materially different tool", "concrete blocker", "target-scope update"} {
		if strings.Contains(errorText, forbidden) {
			t.Fatalf("submit-correction replay rejection leaked generic fallback %q:\n%s", forbidden, errorText)
		}
	}

	if !strings.Contains(errorText, "only valid next action is a corrected parser-readable `submit_plan`") {
		t.Fatalf("submit-correction replay rejection should stay submit_plan-only:\n%s", errorText)
	}
}

func TestPlanExecuteSubmitCorrectionHiddenOnlyPromptIsSubmitPlanOnly(t *testing.T) {
	prompt := planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt("Corrected submit_plan required.")

	for _, want := range []string{"exactly one visible parser-readable `submit_plan`", "Do not answer `Decision: source-satisfied`", "do not answer `<<QUESTION>>:`", "do not call read/search/discovery"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("hidden-only submit-correction prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{"visible `<<QUESTION>>:` blocker", "or a visible `<<QUESTION>>:`", "tool JSON or"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("hidden-only submit-correction prompt advertised stale exit %q:\n%s", forbidden, prompt)
		}
	}
}

func TestPlanExecuteSubmitCorrectionWrongToolRejectionHasNoFallbackExits(t *testing.T) {
	summary, errorText, prompt := planThenExecuteSubmitCorrectionWrongToolRejection("read_file", false)
	joined := strings.Join([]string{summary, errorText, prompt}, "\n")

	for _, want := range []string{
		"Corrected submit_plan required",
		"submit-correction mode exposes only `submit_plan`",
		"exactly one parser-readable `submit_plan`",
		"No terminal source-satisfied/question/prose fallback is valid",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("submit-correction wrong-tool rejection missing %q:\n%s", want, joined)
		}
	}

	for _, forbidden := range []string{
		"answer from gathered evidence",
		"report a concrete blocker",
		"Use `Decision: source-satisfied`",
		"Use `<<QUESTION>>:`",
		"Only use `<<QUESTION>>:`",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("submit-correction wrong-tool rejection advertised stale exit %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteSubmitCorrectionCurrentSurfaceRejectsWrongRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.txt`.",
		},
	}

	summary, errorText, prompt, reject := planThenExecuteSubmitCorrectionCurrentSurfaceRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "new_file.txt"},
		brief,
		results,
	)
	if !reject {
		t.Fatal("expected submit-correction current-surface rejection")
	}

	joined := strings.Join([]string{summary, errorText, prompt}, "\n")
	for _, want := range []string{
		"Corrected submit_plan required",
		"submit-correction mode exposes only `submit_plan`",
		"exactly one parser-readable `submit_plan`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("submit-correction current-surface rejection missing %q:\n%s", want, joined)
		}
	}

	for _, forbidden := range []string{
		"Tool not available in current runtime contract",
		"NEW FILE you must create",
		"report a concrete blocker",
		"Use `<<QUESTION>>:`",
		"Use `Decision: source-satisfied`",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("submit-correction current-surface rejection advertised stale fallback %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteSubmitCorrectionChatOptionsExposeOnlySubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-2 (total lines: 2)", Output: "before\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.txt`.",
		},
	}

	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: workspace.ToolNameReadFile}},
			{Type: "function", Function: llama.ToolFunction{Name: workspace.ToolNameSubmitPlan}},
		},
		ToolChoice: "auto",
	}, brief, results, true)

	if !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		t.Fatalf("submit-correction should expose submit_plan, got %#v", opts.Tools)
	}

	if chatOptionsExposeTool(opts, workspace.ToolNameReadFile) {
		t.Fatalf("submit-correction must not re-expose read_file, got tools=%#v choice=%q", opts.Tools, opts.ToolChoice)
	}

	if opts.ToolChoice != "required" {
		t.Fatalf("submit-correction native tool choice = %q, want required", opts.ToolChoice)
	}
}

func TestPlanExecuteSyntaxRejectedSubmitPlanAllowsRefreshThenCorrectedPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc value() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad line-range edit should fail syntax preflight.","edits":[{"path":"a.go","start_line":3,"end_line":3,"content":"func broken {\n","purpose":"bad source edit"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b"}]}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":5}`,
		`{"tool":"submit_plan","rationale":"Corrected after source refresh.","edits":[{"path":"a.go","old_text":"\treturn 1\n","new_text":"\treturn 2\n","purpose":"replace return value"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b"}]}`,
	}

	type requestPayload struct {
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}

	var (
		requests       int
		extraRequests  int
		refreshPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			extraRequests++

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": "<<QUESTION>>: unexpected extra request after corrected plan."},
				}},
			})

			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &refreshPayload); err != nil {
				t.Fatalf("decode refresh request: %v\n%s", err, string(body))
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

	var events []string

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
			RequiredEditPaths: []string{"a.go", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_result" || strings.Contains(event.Role, "plan_then_execute") || strings.Contains(event.Role, "submit_plan") || strings.Contains(event.Role, "completion") {
				events = append(events, event.Role+": "+event.Content)
			}
		},
	}

	var toolTrace []string

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
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

	if len(refreshPayload.Tools) != 2 || !requestPayloadExposesTool(refreshPayload.Tools, "read_file") || !requestPayloadExposesTool(refreshPayload.Tools, "code_navigate") || requestPayloadExposesTool(refreshPayload.Tools, "submit_plan") {
		t.Fatalf("syntax preflight recovery should expose only bounded source-refresh tools, got %#v", refreshPayload.Tools)
	}

	if refreshPayload.ToolChoice != "required" {
		t.Fatalf("syntax preflight recovery should require a tool call, got %q", refreshPayload.ToolChoice)
	}

	if extraRequests != 0 {
		t.Fatalf("unexpected extra chat request after corrected plan; tools:\n%s\nevents:\n%s\nanswer:\n%s", strings.Join(toolTrace, "\n"), strings.Join(events, "\n"), answer)
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s\ntools:\n%s\nevents:\n%s", answer, strings.Join(toolTrace, "\n"), strings.Join(events, "\n"))
	}
}

type planExecuteRequestToolPayload struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

func requestPayloadExposesTool(tools []planExecuteRequestToolPayload, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}

	return false
}

func TestPlanExecuteRecoveryRetriesAfterNonSpecificSubmitPlanBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		"Done.",
		`{"tool":"read_file","path":"a.txt"}`,
		"Still done.",
		"<<QUESTION>>: blocked by missing safe edit body.\n- a.txt: no safe edit body is available.\n- b.txt: no safe edit body is available.",
	}

	var (
		requests                            int
		recoveryPrompts                     int
		sawRepeatedSubmitPlanMissingBlocker bool
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
			switch event.Role {
			case "completion_gate_recovery":
				recoveryPrompts++
			case "completion_status":
				if strings.Contains(event.Content, "completion_gate_submit_plan_missing_repeated") {
					sawRepeatedSubmitPlanMissingBlocker = true
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests >= len(replies) {
		t.Fatalf("expected recovery to cut over before exhausting scripted retries, got %d request(s)", requests)
	}

	if recoveryPrompts < 1 {
		t.Fatalf("expected one completion recovery prompt before cutover, got %d", recoveryPrompts)
	}

	if !sawRepeatedSubmitPlanMissingBlocker {
		t.Fatalf("expected repeated submit-plan-missing completion status")
	}

	if !strings.Contains(answer, "required edit path") {
		t.Fatalf("expected deterministic required-path blocker answer after recovery cutover, got:\n%s", answer)
	}
}

func TestPlanExecuteCompletionBlockerKindDistinguishesInvalidSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-2 (total lines: 2)", Output: "before a\n"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-2 (total lines: 2)", Output: "before b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths",
		},
	}

	if got := runner.completionGateRepeatedBlockerKind(results); got != "completion_gate_submit_plan_invalid_repeated" {
		t.Fatalf("completion blocker kind = %q, want submit-plan invalid", got)
	}
}

func TestImplementationScopedSurfaceActiveForRequiredPathMutation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/review.md"},
	}
	if !implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("required-path mutation packets should use scoped discovery/edit tools even without target-packet anchors")
	}

	brief.RequiredEditPaths = nil
	if implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("scoped implementation surface must stay inactive without required edit paths")
	}
}

func TestScopedImplementationDiscoveryLookupAllowsSemanticAndDeclaredProjectTools(t *testing.T) {
	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "lookup", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
	}
	runner.Workspace.SetExternalToolsRegistry(reg)

	allowed := []workspace.ToolRequest{
		{Tool: "cpp_find_symbol", Pattern: "FormativeEventType"},
		{Tool: "context_query", Query: "FormativeEventType owner", Resolution: "symbol"},
		{Tool: "lookup", Query: "FormativeEventType"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/simulation/creatures/formative_events.h"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent"},
		{Tool: "discovery_query", Purpose: "trace_code", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent"},
		{Tool: "structural_search", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent", Language: "cpp"},
	}
	for _, req := range allowed {
		if !runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to allow %#v", req)
		}
	}

	rejected := []workspace.ToolRequest{
		{Tool: "search_content", Pattern: "FormativeEventType"},
		{Tool: "queue_run", Query: "FormativeEventType"},
		{Tool: "semgrep_scan", Path: "."},
		{Tool: "cpp_find_symbol", Pattern: "*"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/core/components.h"},
		{Tool: "structural_search", Pattern: "enum class FormativeEventType", Path: "native/src"},
		{Tool: "structural_search", Pattern: "if $COND { $BODY }", Path: "native/src/simulation/creatures", Language: "cpp"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation", Pattern: "ValidateFormativeEvent"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation/core", Pattern: "ValidateFormativeEvent"},
	}
	for _, req := range rejected {
		if runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to reject %#v", req)
		}
	}
}

func TestDocDeliverableRecoveryNudgesScopeCorrectionAfterOutOfScopeRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:    "structural_search",
		Path:    "native/src",
		Pattern: "enum class FormativeEventType",
	}
	_, _, prompt, hideReads, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, nil)

	if !rejected {
		t.Fatalf("expected out-of-scope read to be rejected")
	}

	if hideReads {
		t.Fatalf("doc deliverable out-of-scope recovery should not activate write-only read hiding")
	}

	for _, want := range []string{"Treat this as scope evidence", "scope-correction", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryRecordsEvidenceGapWithoutHidingReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)

	if !ok {
		t.Fatalf("expected completion recovery prompt")
	}

	if hideReads {
		t.Fatalf("doc deliverable completion recovery should not hide reads")
	}

	for _, want := range []string{"non-source deliverable", "deliverable-write contract", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryStateClosesTowardWriteOrBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file", "code_navigate"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	result := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}
	state := buildRunnerNextActionState("main", 3, "completion_gate_rejected", workspace.ToolRequest{Tool: "completion_gate"}, result, []workspace.ToolResult{result}, false, brief)

	joinedValid := strings.Join(state.NextValidActions, "\n")
	for _, want := range []string{"deliverable-write capability", ".idiom/refine/example/iter-2/draft.md", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected next valid actions to close toward %q, got %#v", want, state.NextValidActions)
		}
	}

	if strings.Contains(joinedValid, "code_navigate") || strings.Contains(joinedValid, "read_file") {
		t.Fatalf("post-gate scratchpad closeout should not advertise new discovery as a normal next action: %#v", state.NextValidActions)
	}

	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "new source discovery") {
		t.Fatalf("expected invalid actions to call out discovery after prose-only completion, got %#v", state.InvalidNextActions)
	}

	joinedEvidence := strings.Join(state.EvidenceStatus, "\n")
	if !strings.Contains(joinedEvidence, "non-source deliverable still unwritten") {
		t.Fatalf("expected evidence status to carry closeout state, got %#v", state.EvidenceStatus)
	}
}

func TestScratchpadDeliverableCloseoutRejectsNonWriteTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}
	summary, errorText, prompt, target, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/example.cpp"},
		results,
	)

	if !reject {
		t.Fatalf("post-completion scratchpad closeout should reject non-write tools")
	}

	joined := summary + " " + errorText + " " + prompt + " " + target
	for _, want := range []string{"Scratchpad deliverable closeout", "read_file", ".idiom/refine/example/iter-2/draft.md", "deliverable-write capability", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got summary=%q error=%q prompt=%q target=%q", want, summary, errorText, prompt, target)
		}
	}

	if _, _, _, _, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		results,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad closeout")
	}
}

func TestScratchpadDeliverablePrewriteRejectsSourceDiscoveryButAllowsScratchpadReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths: []string{
				".idiom/refine/example/iter-1/draft.md",
				".idiom/refine/example/iter-1/review.md",
				"native/src/simulation/creatures/formative_events.h",
			},
		},
	}

	summary, errorText, prompt, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/simulation/creatures/formative_events.h"},
		nil,
	)
	if !reject {
		t.Fatalf("expected source read to be redirected to scratchpad write")
	}

	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"Scratchpad deliverable source discovery", "deliverable-write contract", "Source verification pending", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}

	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "context_query", Query: "FormativeEventType"},
		nil,
	); !reject {
		t.Fatalf("expected broad/pathless discovery to be redirected")
	}

	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: ".idiom/refine/example/iter-1/review.md"},
		nil,
	); reject {
		t.Fatalf("scratchpad input reads should remain available")
	}

	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		nil,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad deliverables")
	}
}

func TestScopedImplementationReadScopeDoesNotExpandFromDynamicEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "native/src/core/components.h",
		NamedGap: "context query mentioned this candidate path",
	}
	results := []workspace.ToolResult{{
		Tool:   "context_query",
		Output: "candidate: native/src/core/components.h:42 AppraisalResult",
	}}
	_, _, prompt, _, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, results)

	if !rejected {
		t.Fatalf("dynamic tool-result evidence must not expand scratchpad read scope")
	}

	if !strings.Contains(prompt, "scope evidence") {
		t.Fatalf("expected scope evidence prompt, got %q", prompt)
	}
}

func TestScratchpadDeliverableFailedEvidenceNudgesDraftedGap(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
		},
	}
	prompt, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		[]workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"}},
	)

	if !ok {
		t.Fatalf("expected failed optional evidence to nudge scratchpad gap recording")
	}

	for _, want := range []string{"treat that as an evidence fact", "rather than repairing the lookup", "next action should be either writing", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestScratchpadDeliverableFailureFeedbackNarrowsToWriteOrQuestion(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	req := workspace.ToolRequest{
		Tool:    "read_file",
		Path:    "native/src/core/components.h",
		Pattern: "AppraisalResult",
	}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Scoped implementation read rejected",
		Error:   "outside the required edit paths and explicit evidence paths",
	}
	packet := buildRunnerFailureFeedbackPacket("main", 3, "tool_rejected", req, result, []workspace.ToolResult{result}, false, brief)
	joinedRequired := strings.Join(packet.RequiredNext, "\n")

	if !strings.Contains(joinedRequired, "record the failed") {
		t.Fatalf("expected required_next to record failed evidence, got %#v", packet.RequiredNext)
	}

	joinedValid := strings.Join(packet.ValidNext, "\n")
	for _, want := range []string{"write the non-source deliverable", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected valid_next to contain %q, got %#v", want, packet.ValidNext)
		}
	}

	joinedEvidence := strings.Join(packet.EvidenceStatus, "\n")
	for _, want := range []string{"closed_negative_evidence", "read_file", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joinedEvidence, want) {
			t.Fatalf("expected evidence_status to contain %q, got %#v", want, packet.EvidenceStatus)
		}
	}
}

func TestScratchpadDeliverableFailedEvidenceKeepsOriginalFailure(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
	}
	result, ok := scratchpadDeliverableFailedEvidenceResult(brief, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		{Tool: "read_file", Summary: "Scratchpad evidence follow-up rejected", Error: "A previous optional evidence lookup failed"},
	})

	if !ok {
		t.Fatalf("expected original failed evidence result")
	}

	if result.Tool != "code_navigate" || strings.Contains(result.Error, "previous optional evidence") {
		t.Fatalf("expected original failure, got %#v", result)
	}
}

func TestScratchpadDeliverableFailedEvidenceIgnoresSourceImplementation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"native/src/example.cpp"},
		},
	}
	if _, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		nil,
	); ok {
		t.Fatalf("source implementation failures should keep the normal recovery path")
	}
}

func TestPlanExecuteNonToolPlannerTextTriggersRequiredSourceRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
		},
	}
	req, _, ok := runner.deterministicPlanExecuteRejectedRequiredSourceRead("I can make this change.", nil)

	if !ok {
		t.Fatalf("expected deterministic source read for non-tool planner text before required source evidence")
	}

	if req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("unexpected deterministic read request: %#v", req)
	}
}

func TestPlanExecuteNonToolPlannerQuestionDoesNotAutoRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	if _, _, ok := runner.deterministicPlanExecuteRejectedRequiredSourceRead("<<QUESTION>>: missing permission", nil); ok {
		t.Fatalf("typed blocker questions should not be replaced by deterministic reads")
	}
}
