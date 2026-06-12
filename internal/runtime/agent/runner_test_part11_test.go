package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerEmitsRecoveryNudgeBeforeGivingUpOnRepeatedBroadInspection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Answered from prior evidence after nudge.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
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
			MaxNoProgressTurns: 6,
		},
	}

	var stopReason string

	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger

			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "identify small refactor opportunities"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// DOOM_LOOP guard (threshold=3) catches the third identical list_files
	// call before the rejection_recovery_nudge path is reached, then forces
	// a no-tools closeout.
	if stopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %q", stopReason)
	}

	if answer != "Answered from prior evidence after nudge." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

func TestRunnerStopsExploreAfterRepeatedMissingPathProbes(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Use README.md and the bootstrapped evidence; no more path probes are needed.",
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

	var ledger runnerBudgetLedger

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}

			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Use README.md and the bootstrapped evidence; no more path probes are needed." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if requests != 3 {
		t.Fatalf("expected forced completion after two missing path probes, got %d requests", requests)
	}

	if ledger.StopReason != "missing_path_probe_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("expected missing-path budget ledger, got %#v", ledger)
	}
}

func TestRunnerAllowsMissingPathProbeAfterProgress(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":20}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Completed without forced missing-path stop.",
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

	var ledger runnerBudgetLedger

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}

			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Completed without forced missing-path stop." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected all scripted model requests, got %d", requests)
	}

	if ledger.StopReason != "model_final" || ledger.BudgetExhausted {
		t.Fatalf("expected ordinary model final after non-consecutive missing path probes, got %#v", ledger)
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
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "no task_ledger") {
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

// TestTaskLedgerCompletionGatePlanningExit locks in the planning-mode
// escape hatch: when the user asked only for a plan, the model
// should be able to submit the ledger and reply with an
// explicit planning-exit phrase without the gate forcing it to
// execute every item. Before this fix the model would get stuck
// in a retry loop (or worse, start executing items it was never
// asked to execute).
func TestTaskLedgerCompletionGatePlanningExit(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "pending"},
			{ID: "b", Content: "add tests for X", Status: "pending"},
		},
	})

	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	// Plain final answer with unfinished items → gate rejects.
	if _, rejected := state.completionRejection("here is what I'd change"); !rejected {
		t.Fatalf("gate should reject a final answer with unfinished items and no planning exit")
	}

	// Planning-exit phrase → gate allows the final answer through.
	planningReplies := []string{
		"Plan ready — awaiting approval before I execute the items.",
		"Plan complete. Let me know if you'd like me to proceed.",
		"Planning only — please confirm which items to execute.",
		"Awaiting approval on the above plan.",
	}
	for _, reply := range planningReplies {
		if _, rejected := state.completionRejection(reply); rejected {
			t.Errorf("planning-exit reply should pass the gate, rejected: %q", reply)
		}
	}
}

// TestTaskLedgerCompletionGateStillRejectsVagueReplies guards against
// the planning-exit loophole swallowing genuinely-incomplete replies.
// A reply that doesn't contain any recognized terminal phrase must
// still be rejected when items are pending — otherwise the gate
// loses its value.
func TestTaskLedgerCompletionGateStillRejectsVagueReplies(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "in_progress"},
		},
	})

	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	vague := []string{
		"Done.",
		"Here's what I found.",
		"I've looked at the code.",
	}
	for _, reply := range vague {
		if _, rejected := state.completionRejection(reply); !rejected {
			t.Errorf("vague reply should still be rejected: %q", reply)
		}
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

	var (
		requests           int
		sawCompactedPrompt bool
	)

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
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 4, "total_tokens": 15},
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

	if ledger.PromptTokens != 11 || ledger.CompletionTokens != 4 || ledger.TotalTokens != 15 {
		t.Fatalf("expected token usage in budget ledger, got %#v", ledger)
	}
}

func TestRunnerRejectsUnexecutedToolJSONAsFinalAnswer(t *testing.T) {
	var (
		requests      int
		secondRequest string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 2 {
			secondRequest = string(body)
		}

		reply := "The implementation needs to be rerouted; no workspace lookup has run."
		if requests == 1 {
			reply = "Let me start by understanding the task and locating the relevant code.\n\n" +
				`{"task":"implement docs/writer-route-decision-report-only-plan.md","mode":"plan","limit":12}`
		}

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
			Root: t.TempDir(),
			CWD:  ".",
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "read_file", "search_content"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "start implementing docs/writer-route-decision-report-only-plan.md"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests < 2 {
		t.Fatalf("expected retry after tool-shaped final answer rejection, got %d request(s)", requests)
	}

	if strings.Contains(answer, `"task":"implement docs/writer-route-decision-report-only-plan.md"`) {
		t.Fatalf("runner accepted raw tool-shaped JSON as final answer: %q", answer)
	}

	if !strings.Contains(secondRequest, "Unexecuted tool request rejected as final answer") {
		t.Fatalf("retry request did not include completion-gate rejection context:\n%s", secondRequest)
	}
}

func TestRunnerExecutesBareJSONMissingToolAliasBeforeCompletionGate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		reply := "I read `internal/example/target.go`; the source body contains the package declaration `package example`."

		if requests == 1 {
			reply = `{"path":"internal/example/target.go","start_line":1,"end_line":1}`
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	root := t.TempDir()
	target := filepath.Join(root, "internal", "example", "target.go")

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	if err := os.WriteFile(target, []byte("package example\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "inspect internal/example/target.go"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected one tool execution followed by answer, got %d request(s), answer=%q", requests, answer)
	}

	if !strings.Contains(answer, "internal/example/target.go") || !strings.Contains(answer, "package example") {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestRunnerSalvagesVisibleCodeNavigateJSONMissingTool(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"read_symbol","path":"cmd/idiom/product_workflow.go","pattern":"runProductBrief"}`,
		nil,
	)

	if !ok {
		t.Fatal("expected visible code_navigate payload missing tool field to be salvaged")
	}

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/product_workflow.go" || req.Pattern != "runProductBrief" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("salvaged raw request must include inferred tool, got %s", raw)
	}
}

func TestRunnerSalvagesExplicitVisibleToolJSONAfterCompletionGateRecovery(t *testing.T) {
	runner := &Runner{}
	reply := strings.Join([]string{
		"I'll start by exploring the relevant source and tests to understand the current implementation and then plan the changes.",
		"",
		"tool: code_navigate",
		"capability: read_symbol",
		"path: cmd/idiom/product_evidence.go",
		"symbol: p912EvaluateCoverageGate",
		"",
		`{"tool":"code_navigate","capability":"read_symbol","path":"cmd/idiom/product_evidence.go","symbol":"p912EvaluateCoverageGate"}`,
	}, "\n")
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Plan-execute completion gate rejected final answer",
		Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
	}}

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected explicit JSON tool request to be salvaged after completion-gate recovery")
	}

	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/product_evidence.go" || req.Pattern != "p912EvaluateCoverageGate" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("expected canonical raw tool JSON, got %s", raw)
	}
}

func TestRunnerSalvagesVisibleNestedEditFileJSONAfterCompletionGateRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "1:package app\n2:func updateSessionsPanel() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}
	nested := `{"path":"internal/surfaces/tui/app/sessions_panel.go","old_text":"func updateSessionsPanel() {}","new_text":"func updateSessionsPanel() { renderSessionsPanelContent() }","description":"keep selected session visible"}`
	reply := `{"tool":"edit_file","request":` + strconv.Quote(nested) + `}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected visible nested edit_file request to be salvaged after completion-gate recovery")
	}

	if req.Tool != "edit_file" || req.Path != "internal/surfaces/tui/app/sessions_panel.go" || req.OldText != "func updateSessionsPanel() {}" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"edit_file"`) || !strings.Contains(raw, `"request"`) {
		t.Fatalf("expected raw nested edit_file JSON, got %s", raw)
	}
}

func TestRunnerRejectsVisibleNestedEditFileJSONWithoutSourceEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}
	nested := `{"path":"internal/surfaces/tui/app/sessions_panel.go","old_text":"func updateSessionsPanel() {}","new_text":"func updateSessionsPanel() { renderSessionsPanelContent() }","description":"keep selected session visible"}`
	reply := `{"tool":"edit_file","request":` + strconv.Quote(nested) + `}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("visible edit salvage must require source evidence for unscoped implementation writes, req=%#v raw=%s", req, raw)
	}
}

func TestUnscopedImplementationWriteReadyNonWriteLoopStopsBeforeModelBudget(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write labels.go: %v", err)
	}

	// First reply is a source-body read to establish evidence.
	// Remaining 19 are read_file on the same path without the named_gap
	// marker, which should be rejected as non-write after the source
	// refresh allowance (3) is exhausted.
	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20,"named_gap":"current target source before edit","why_not_answering":"need source before writing"}`,
	}
	for i := 0; i < 20; i++ {
		replies = append(replies, `{"tool":"read_file","path":"internal/labels/labels.go"}`)
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
		Policy: RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 20, MaxModelCalls: 40},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update ShortLabel in internal/labels/labels.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected write-ready loop blocker with <<QUESTION>>, got: %q", answer)
	}
}
func TestRunnerSalvagesVisibleLineToolRequest(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(strings.Join([]string{
		"tool: read_file",
		"path: internal/workflows/productworkflow/coverage_gate.go",
		"start_line: 1",
		"end_line: 130",
	}, "\n"), nil)

	if !ok {
		t.Fatal("expected visible key-value tool request to be salvaged")
	}

	if req.Tool != "read_file" || req.Path != "internal/workflows/productworkflow/coverage_gate.go" || req.StartLine != 1 || req.EndLine != 130 {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"start_line":1`) {
		t.Fatalf("salvaged raw request must be canonical JSON, got %s", raw)
	}
}

func TestPlanExecuteSalvagesVisibleMissingTargetReadForTypedRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-120",
		Output:  "1:package productworkflow\n",
	}}
	reply := "Let me start by understanding the current state.\n\n" + `{"tool":"read_file"}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected plan-execute targetless read_file to be salvaged for typed recovery")
	}

	if req.Tool != "read_file" || strings.TrimSpace(req.Path) != "" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) {
		t.Fatalf("salvaged raw request must preserve read_file request, got %s", raw)
	}

	reason, _, lowValue := planThenExecuteCandidateLowValueAction(req, runner.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if !lowValue || reason != "candidate_read_missing_target" {
		t.Fatalf("expected typed missing-target recovery, got lowValue=%v reason=%q", lowValue, reason)
	}
}

func TestRunnerDoesNotSalvageNarrativeLineText(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(strings.Join([]string{
		"status: blocked",
		"reason: more information is needed",
	}, "\n"), nil)

	if ok {
		t.Fatal("ordinary key-value answer text without a tool must not be salvaged")
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectBoundsUnreadCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	req, raw, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/coverage_gate_test.go",
	}, results)
	if !ok {
		t.Fatal("expected unread candidate range-less read to be rewritten to a bounded source window")
	}

	if req.StartLine != 1 || req.EndLine != 180 || req.Path != "internal/workflows/productworkflow/coverage_gate_test.go" {
		t.Fatalf("unexpected redirected request: %#v", req)
	}

	if !strings.Contains(raw, `"start_line":1`) || !strings.Contains(raw, `"end_line":180`) {
		t.Fatalf("redirect raw should be canonical bounded JSON, got %s", raw)
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectUsesContextPackScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "context_pack",
			Output: `{"candidate_files":[{"path":"internal/workflows/productworkflow/scope.go"}],"tests":[{"path":"internal/workflows/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
	}

	req, _, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/scope.go",
	}, results)
	if !ok {
		t.Fatal("expected context-pack candidate path to be eligible for bounded range-less read rewrite")
	}

	if req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected redirect range: %#v", req)
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectSkipsAlreadyReadCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	if req, raw, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/coverage_gate.go",
	}, results); ok {
		t.Fatalf("already read candidate should not be rewritten, req=%#v raw=%s", req, raw)
	}
}

func TestRunnerDoesNotSalvageCapabilityShapedAnswerJSON(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"summarize","language":"go","path":"cmd/idiom/main.go","confidence":"high"}`,
		nil,
	)

	if ok {
		t.Fatal("ordinary capability-shaped answer JSON should not be salvaged as code_navigate")
	}
}
