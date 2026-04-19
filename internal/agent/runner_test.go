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

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

func TestToolActionClassNormalizesRepeatedDeadEndKeys(t *testing.T) {
	cases := []struct {
		req  workspace.ToolRequest
		want string
	}{
		{req: workspace.ToolRequest{Tool: "list_files"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "list_files", Path: "internal"}, want: "list_files:internal"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "rg route internal/app"}, want: "shell_narrow:rg route internal/app"},
		{req: workspace.ToolRequest{Tool: "read_file", ID: "f12"}, want: "read_file:f12"},
		{req: workspace.ToolRequest{Tool: "go_find_symbol", Pattern: "classifyPromptExecutionPlan"}, want: "go_find_symbol:classifypromptexecutionplan"},
		{req: workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/app/app.go"}, want: "go_file_outline:internal/app/app.go"},
		{req: workspace.ToolRequest{Tool: "go_move_declarations", Path: "internal/app/app.go", Destination: "internal/app/rendering.go"}, want: "go_move_declarations:internal/app/app.go->internal/app/rendering.go"},
	}
	for _, tc := range cases {
		if got := toolActionClass(tc.req); got != tc.want {
			t.Fatalf("toolActionClass(%#v) = %q, want %q", tc.req, got, tc.want)
		}
	}
}

func writeTestFile(root string, path string, content string) error {
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func TestShouldRejectProseAfterGoOutlineRequiresMoveBeforeAnswer(t *testing.T) {
	results := []workspace.ToolResult{{Tool: "go_file_outline", Summary: "Outlined internal/app/app.go"}}
	if !shouldRejectProseAfterGoOutline("Done.", results) {
		t.Fatal("expected normal prose after outline to be rejected before any declaration move")
	}
	if shouldRejectProseAfterGoOutline("Done.", append(results, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"})) {
		t.Fatal("expected prose to be allowed after a declaration move")
	}
	if shouldRejectProseAfterGoOutline("", results) {
		t.Fatal("empty reply should not be handled as prose-after-outline")
	}
}

func TestRecommendedGoMoveAfterOutlineParsesToolSuggestion(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "go_file_outline",
		Summary: "Outlined 2 top-level declaration(s) in source.go.",
		Output:  "recommended structural move batches:\n- tag: rendering destination: rendering.go\n  tool: {\"tool\":\"go_move_declarations\",\"path\":\"source.go\",\"destination\":\"rendering.go\",\"pattern\":\"tag:rendering\"}",
	}}
	req, ok := recommendedGoMoveAfterOutline(workspace.ToolRequest{Tool: "read_file", Path: "source.go"}, results)
	if !ok {
		t.Fatal("expected recommended go_move_declarations request")
	}
	if req.Tool != "go_move_declarations" || req.Path != "source.go" || req.Destination != "rendering.go" || req.Pattern != "tag:rendering" {
		t.Fatalf("unexpected recommended request: %#v", req)
	}
}

func TestRecommendedGoMoveAfterOutlineHandlesBroadRediscovery(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "go_file_outline",
		Summary: "Outlined 2 top-level declaration(s) in source.go.",
		Output:  "recommended structural move batches:\n- tag: rendering destination: rendering.go\n  tool: {\"tool\":\"go_move_declarations\",\"path\":\"source.go\",\"destination\":\"rendering.go\",\"pattern\":\"tag:rendering\"}",
	}}
	req, ok := recommendedGoMoveAfterOutline(workspace.ToolRequest{Tool: "list_files", Path: ".", Recursive: true}, results)
	if !ok {
		t.Fatal("expected broad rediscovery to reuse recommended go_move_declarations request")
	}
	if req.Tool != "go_move_declarations" || req.Path != "source.go" || req.Destination != "rendering.go" {
		t.Fatalf("unexpected recommended request: %#v", req)
	}
}

func TestRecommendedGoMoveAfterOutlineRespectsPreExecuteGuard(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "go_file_outline",
		Summary: "Outlined 2 top-level declaration(s) in source.go.",
		Output:  "recommended structural move batches:\n- tag: implementation destination: implementation.go\n  tool: {\"tool\":\"go_move_declarations\",\"path\":\"source.go\",\"destination\":\"implementation.go\",\"pattern\":\"tag:implementation\"}",
	}}
	runner := &Runner{
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool == "go_move_declarations" && req.Destination == "implementation.go" {
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Implementation target packet blocked write outside approved targets",
					Error:   "implementation.go is not an approved editable target",
				}
			}
			return nil
		},
	}
	req, guarded, ok := runner.recommendedGoMoveAfterOutline(workspace.ToolRequest{Tool: "go_file_outline", Path: "source.go"}, results, 2)
	if !ok {
		t.Fatal("expected recommended move to be found")
	}
	if req.Tool != "go_move_declarations" || req.Destination != "implementation.go" {
		t.Fatalf("unexpected recommended request: %#v", req)
	}
	if guarded == nil || !strings.Contains(guarded.Error, "not an approved editable target") {
		t.Fatalf("expected pre-execute guard result, got %#v", guarded)
	}
}

func TestShouldRejectProseAfterGoOutlineAllowsConcreteFailedMoveBlocker(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "go_file_outline", Summary: "Outlined source.go"},
		{Tool: "go_move_declarations", Summary: "Recommended declaration move rejected by guard", Error: "implementation.go is not an approved editable target"},
	}
	if shouldRejectProseAfterGoOutline("Blocked: destination is not approved by the current target scope.", results) {
		t.Fatal("expected concrete failed-move blocker to be allowed")
	}
}

func TestRunnerExecutesRecommendedGoMoveAfterIgnoredOutline(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "source.go", `package sample

import "fmt"

func renderView() string {
	return fmt.Sprint("ok")
}

func coreValue() int {
	return 1
}
`); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"go_file_outline\",\"path\":\"source.go\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"source.go\"}\n```",
		"Moved the rendering declarations.",
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

	var moved bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "split rendering out of source.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "go_move_declarations" && event.Result.Error == "" {
				moved = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !moved {
		t.Fatal("expected deterministic go_move_declarations fallback to execute")
	}
	if answer != "Moved the rendering declarations." {
		t.Fatalf("unexpected answer %q", answer)
	}
	rendering, err := os.ReadFile(filepath.Join(root, "rendering.go"))
	if err != nil {
		t.Fatalf("expected rendering.go to be created: %v", err)
	}
	if !strings.Contains(string(rendering), "renderView") {
		t.Fatalf("expected moved renderView in rendering.go, got:\n%s", rendering)
	}
}

func TestRunnerExecutesRecommendedGoMoveAfterBroadRediscovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "source.go", `package sample

import "fmt"

func renderView() string {
	return fmt.Sprint("ok")
}

func coreValue() int {
	return 1
}
`); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"go_file_outline\",\"path\":\"source.go\"}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".\",\"recursive\":true}\n```",
		"Moved after outline.",
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

	var sawBroadResult bool
	var moved bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "split rendering out of source.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "list_files" {
				sawBroadResult = true
			}
			if event.Result.Tool == "go_move_declarations" && event.Result.Error == "" {
				moved = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sawBroadResult {
		t.Fatal("expected broad rediscovery request to be replaced before execution")
	}
	if !moved {
		t.Fatal("expected deterministic go_move_declarations fallback to execute")
	}
	if answer != "Moved after outline." {
		t.Fatalf("unexpected answer %q", answer)
	}
}

func TestRunnerReplaysFirstRepeatedToolRequestAndStopsOnNoProgressBudget(t *testing.T) {
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

	var replayedSummary string
	var budgetEvent string
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
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Stopped after repeated loops." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
	if replayedSummary == "" {
		t.Fatal("expected repeated tool request to replay prior evidence before stopping")
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "no_progress_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: "."})
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

	var broadGuarded bool
	var replayed bool
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

func TestRunnerUnboundedPolicyLetsCompletionGateDriveDone(t *testing.T) {
	replies := []string{"premature final", "accepted final"}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
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

	rejections := 0
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 3,
		},
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			if rejections == 0 {
				rejections++
				return CompletionGateResult{
					Summary: "done gate rejected",
					Error:   "continue once",
				}
			}
			return CompletionGateResult{Accept: true}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work until done"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "accepted final" {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 2 || rejections != 1 {
		t.Fatalf("expected one gate rejection and two model calls, requests=%d rejections=%d", requests, rejections)
	}
}

func TestRunnerTaskLedgerBlocksFinalUntilRequiredItemsComplete(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"Done too early.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after ledger completion.",
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

	var gateRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform a complex refactor"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "unfinished required work") {
				gateRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after ledger completion." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
	if gateRejections != 1 {
		t.Fatalf("expected one task-ledger completion rejection, got %d", gateRejections)
	}
}

func TestRunnerTaskLedgerRequiredBeforeFinalForComplexWork(t *testing.T) {
	replies := []string{
		"Done without a ledger.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"blocked\",\"content\":\"Apply the requested change\",\"status\":\"blocked\",\"evidence\":\"write approval is required\"}]}\n```",
		"Blocked: write approval is required before I can apply the requested change.",
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

	var missingLedgerRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform complex work"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "no task_ledger has been created") {
				missingLedgerRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked: write approval") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if missingLedgerRejections != 1 {
		t.Fatalf("expected missing-ledger rejection, got %d", missingLedgerRejections)
	}
}

func TestTaskLedgerRejectsInvalidUpdates(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{})
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "First active task", Status: "in_progress"},
			{ID: "two", Content: "Second active task", Status: "in_progress"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "only one") {
		t.Fatalf("expected multiple-active rejection, got %#v", result)
	}

	result = state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "Use a bad status", Status: "started"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "unsupported status") {
		t.Fatalf("expected status rejection, got %#v", result)
	}
}

func TestRunnerCompactsLongTurnHistoryWithTaskLedgerState(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"in_progress\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after compaction.",
	}
	var requests int
	var sawCompactedPrompt bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []llama.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, msg := range payload.Messages {
			if strings.Contains(msg.Content, "[runner context compaction]") &&
				strings.Contains(msg.Content, "Task ledger:") &&
				strings.Contains(msg.Content, "verify: Run verification") {
				sawCompactedPrompt = true
			}
		}
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

	var sawCompactionEvent bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxContextMessages: 4,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "context_compaction" {
				sawCompactionEvent = true
				if !strings.Contains(event.Content, "runner_task_state") {
					t.Fatalf("expected runner task state compaction event, got %s", event.Content)
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "perform complex work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after compaction." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !sawCompactedPrompt {
		t.Fatal("expected later model request to include compacted runner state")
	}
	if !sawCompactionEvent {
		t.Fatal("expected context_compaction run event")
	}
}

func TestRunnerEmitsBudgetLedgerForModelFinal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
		})
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "answer"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_final" || ledger.BudgetExhausted || ledger.UsedModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
}

func TestRunnerModelCallBudgetStopsWithLedger(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"work\",\"content\":\"Do work\",\"status\":\"in_progress\"}]}\n```",
		"Budget exhausted with work still in progress.",
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

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxModelCalls:      1,
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Budget exhausted") {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_call_budget_exhausted" || !ledger.BudgetExhausted || ledger.MaxModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
}

func TestRunnerPermissionPolicyDeniesToolBeforeExecution(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"Blocked by permission policy.",
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

	var permissionEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
			Rules: []ToolPermissionRule{{
				Tool:        "edit_file",
				PathPattern: "README.md",
				Decision:    PermissionDeny,
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "permission_decision" && strings.Contains(event.Content, `"decision":"deny"`) {
				permissionEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked by permission") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if permissionEvent == "" {
		t.Fatal("expected deny permission decision event")
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
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
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
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "No-op declaration move"}) {
		t.Fatal("no-op declaration move should not count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "search_content"}, workspace.ToolResult{Tool: "search_content", Error: "failed"}) {
		t.Fatal("failed tools should not count as progress")
	}
}

func TestSynthesizeFallbackAnswerUsesNarrowEvidenceAndFinalBlocker(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("explore", []workspace.ToolResult{
		{Tool: "list_files", Summary: "listed files", Output: "README.md\ninternal/app/app.go"},
		{Tool: "search_content", Summary: "found route", Output: "internal/app/app.go:42: route"},
		{Tool: "shell", Summary: "bad shell", Error: "not allowed"},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}
	for _, want := range []string{"Exploration did not complete cleanly", "Verified context:", "found route", "Final blocker:", "bad shell: not allowed", "Recommended next step:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "listed files") {
		t.Fatalf("broad list_files result should not be preferred as narrowing evidence:\n%s", answer)
	}
}

func TestCompactOutputRespectsLineBudget(t *testing.T) {
	got := compactOutput("a\nb\nc", 2)
	if got != "a\nb\n..." {
		t.Fatalf("unexpected compact output %q", got)
	}
	if got := compactOutput("a\nb", 0); got != "a\nb" {
		t.Fatalf("expected zero budget to preserve output, got %q", got)
	}
}
