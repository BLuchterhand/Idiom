package agent

import (
	"context"
	"encoding/json"
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

func TestForcedCompletionUsesDeterministicMutationFallback(t *testing.T) {
	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Changed paths:\n- src/example.cpp\n\nVerification: git diff --check passed."},
			}},
		})
	}))

	defer server.Close()

	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			Constraints:          []string{"planner_owned_verification"},
			RequiredEditPaths:    []string{"src/example.h", "src/example.cpp"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited src/example.cpp"}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "implement the requested patch"}},
		llama.ChatOptions{},
		results,
		"Tools are now disabled.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if requests != 0 {
		t.Fatalf("expected deterministic mutation fallback without model call, got %d request(s)", requests)
	}

	for _, want := range []string{
		"<<QUESTION>>: Implementation incomplete",
		"src/example.cpp",
		"Required edit paths not changed:",
		"src/example.h",
		"pending lane sanity; build/test verification is planner-owned",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected deterministic mutation answer to include %q, got:\n%s", want, answer)
		}
	}

	if strings.Contains(strings.ToLower(answer), "passed") {
		t.Fatalf("deterministic mutation fallback should not claim unrun verification passed:\n%s", answer)
	}
}

func TestToolStepBudgetPartialRequiredEditsEmitsBlockedCompletion(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before a\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	if err := writeTestFile(root, "b.txt", "before b\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`},
			}},
		})
	}))

	defer server.Close()

	var (
		critic     map[string]any
		completion runnerCompletionStatus
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "final_answer_critic":
				if err := json.Unmarshal([]byte(event.Content), &critic); err != nil {
					t.Fatalf("decode critic: %v", err)
				}
			case "completion_status":
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>: Implementation incomplete") || !strings.Contains(answer, "b.txt") {
		t.Fatalf("expected visible incomplete-implementation blocker, got %q", answer)
	}

	if passed, _ := critic["passed"].(bool); passed {
		t.Fatalf("final answer critic should reject partial required edits, got %#v", critic)
	}

	if completion.Status != "blocked" || completion.BlockerKind != "required_edit_paths_remaining" {
		t.Fatalf("expected blocked completion for missing required path, got %#v", completion)
	}

	if requests != 1 {
		t.Fatalf("expected one model tool request before deterministic budget closeout, got %d", requests)
	}
}

func TestModelErrorAfterPartialEditReturnsTypedBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": `{"tool":"write_file","path":"a.txt","content":"after\n","description":"partial required edit"}`},
				}},
			})

			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	defer server.Close()

	var completion runnerCompletionStatus

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run should return typed blocker instead of raw model error: %v", err)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>: Local model endpoint failed after workspace edits") {
		t.Fatalf("expected model-error typed blocker, got %q", answer)
	}

	for _, want := range []string{"a.txt", "b.txt", "Required edit paths not changed"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker to mention %q, got %q", want, answer)
		}
	}

	if completion.Status != "blocked" || completion.BlockerKind != "model_error_after_partial_edit" || !completion.EditAttempted {
		t.Fatalf("unexpected completion status: %#v", completion)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}

	if requests != 2 {
		t.Fatalf("expected write request then EOF model request, got %d requests", requests)
	}
}

// TestRequiredEditPathReadinessAllowsNoGapInScopeRead was renamed
// from RejectsNoGapReadWithoutTargetPacket after harness fix #4
// (commit 6fe6bce) dropped the named_gap requirement for in-scope
// reads. The new contract: a read targeting a required_edit_path
// is always allowed regardless of named_gap, because the safety
// reason ("verify the anchor on a path we're about to edit") is
// implicit. Out-of-scope reads still get blocked — that's the
// protection the gate is meant to provide. This test now confirms
// the in-scope no-gap read succeeds rather than triggers recovery.
func TestRequiredEditPathReadinessAllowsNoGapInScopeRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "seed\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"write_file","path":"a.txt","content":"changed\n","description":"make required edit"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	var (
		recoveryPrompt  string
		successfulReads int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableWrites:          true,
			CommandTimeoutSeconds: 5,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				Path:    "a.txt",
				Content: "changed",
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_read_recovery" {
				recoveryPrompt = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if recoveryPrompt != "" {
		t.Fatalf("in-scope no-gap read should NOT trigger scoped recovery after fix #4; got: %q", recoveryPrompt)
	}

	if successfulReads != 1 {
		t.Fatalf("in-scope no-gap read should succeed (in-scope reads are always allowed); got %d successful read(s)", successfulReads)
	}

	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report required path, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "changed\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}
}

// TestWriteReadyPacketReadWithoutNamedGapAllowsInScopeRead was
// renamed from UsesScopedRecovery after harness fix #4 (commit
// 6fe6bce) dropped the named_gap requirement for in-scope reads.
// The new contract: a read targeting a required_edit_path is
// always allowed regardless of named_gap. This test now confirms
// the recovery does NOT fire for an in-scope no-gap read.
func TestWriteReadyPacketReadWithoutNamedGapAllowsInScopeRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	var (
		sawRecovery     bool
		successfulReads int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_read_recovery" {
				sawRecovery = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sawRecovery {
		t.Fatalf("in-scope no-gap read should NOT trigger scoped recovery after fix #4")
	}

	if successfulReads != 1 {
		t.Fatalf("in-scope no-gap read should succeed; got %d successful read(s)", successfulReads)
	}

	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationLastChanceHidesReadsBeforeModelCallBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\nmore\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	requests := 0
	thirdRequestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []llama.Tool `json:"tools,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		requests++
		switch requests {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`}}},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.txt","start_line":2,"end_line":2}`}}},
			})
		case 3:
			thirdRequestChecked = true

			var toolNames []string

			for _, tool := range body.Tools {
				toolNames = append(toolNames, tool.Function.Name)
			}

			for _, forbidden := range []string{"read_file", "search_content", "code_navigate", "context_query"} {
				if slices.Contains(toolNames, forbidden) {
					t.Fatalf("last-chance scoped implementation call advertised non-write tool %q: %#v", forbidden, toolNames)
				}
			}

			if !slices.Contains(toolNames, "write_file") && !slices.Contains(toolNames, "edit_file") && !slices.Contains(toolNames, "apply_patch") {
				t.Fatalf("last-chance scoped implementation call must retain write tools, got %#v", toolNames)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": "<<QUESTION>>: no safe edit for a.txt without a current replacement anchor"}}},
			})
		default:
			t.Fatalf("unexpected extra chat request %d", requests)
		}
	}))

	defer server.Close()

	var sawLastChance bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8, MaxModelCalls: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			AllowedTools:         []string{"read_file", "search_content", "code_navigate", "write_file", "edit_file", "apply_patch"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_last_chance_write_only" {
				sawLastChance = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !thirdRequestChecked {
		t.Fatal("expected third model call to exercise last-chance write-only surface")
	}

	if !sawLastChance {
		t.Fatal("expected implementation_scoped_last_chance_write_only event")
	}

	if !strings.Contains(answer, "<<QUESTION>>") || !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected typed blocker answer for a.txt, got %q", answer)
	}

	if requests != 3 {
		t.Fatalf("expected exactly three model requests, got %d", requests)
	}
}

func TestWriteReadyPacketRepeatedEmptyPathReadReturnsTypedBlocker(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":""}`,
		`{"tool":"read_file","path":""}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"should not be reached after repeated scoped-read recovery"}`,
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

	var (
		completion           runnerCompletionStatus
		scopedReadRejections int
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
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
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
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Summary == "Scoped implementation read rejected" {
				scopedReadRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "scoped implementation recovery rejected the same read_file request twice") {
		t.Fatalf("expected repeated scoped-read blocker, got %q", answer)
	}

	if completion.Status != "blocked" || completion.BlockerKind != "scoped_implementation_read_repeated" {
		t.Fatalf("expected scoped-read blocker completion status, got %#v", completion)
	}

	if scopedReadRejections != 2 {
		t.Fatalf("expected two scoped-read rejections before corrected write, got %d", scopedReadRejections)
	}

	if requests != 2 {
		t.Fatalf("expected blocker before write, got %d requests", requests)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "before\n" {
		t.Fatalf("scoped-read blocker should not mutate a.txt, got %q", content)
	}
}

func TestWriteReadyPacketRejectsTaskLedgerDetourBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"task_ledger","todos":[{"id":"read-targets","content":"Read target files","status":"in_progress"},{"id":"edit-a","content":"Edit a.txt","status":"pending"}]}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	var (
		sawRecovery             bool
		sawScopedToolRecovery   bool
		successfulLedgerUpdates int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_write_only_recovery" {
				sawRecovery = true
			}

			if event.Role == "implementation_scoped_tool_recovery" {
				sawScopedToolRecovery = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "task_ledger" && event.Result.Error == "" {
				successfulLedgerUpdates++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sawRecovery {
		t.Fatalf("did not expect write-only recovery after task_ledger detour")
	}

	if !sawScopedToolRecovery {
		t.Fatalf("expected scoped tool recovery after task_ledger detour")
	}

	if successfulLedgerUpdates != 0 {
		t.Fatalf("task_ledger should be rejected before the first required-path write, got %d successful update(s)", successfulLedgerUpdates)
	}

	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationHiddenBroadToolFallsThroughAfterOneRecovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"search_content","path":".","pattern":"before","named_gap":"find implementation context","why_not_answering":"need context"}`,
		`{"tool":"search_content","path":".","pattern":"after","named_gap":"find implementation context","why_not_answering":"need context"}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	var (
		scopedRecoveryEvents int
		hiddenToolResults    int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_tool_recovery" {
				scopedRecoveryEvents++
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Summary == "Tool hidden by active contract" {
				hiddenToolResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if scopedRecoveryEvents != 1 {
		t.Fatalf("expected exactly one scoped recovery before hidden-tool handling, got %d", scopedRecoveryEvents)
	}

	if hiddenToolResults != 1 {
		t.Fatalf("expected repeated broad tool to fall through to hidden-tool handling, got %d hidden result(s)", hiddenToolResults)
	}

	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}

	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestWriteReadyPacketAllowsNamedGapProbeBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1,"named_gap":"verify the replacement line in a.txt before editing","why_not_answering":"the edit would be unsafe if this anchor is stale"}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	var (
		sawRecovery     bool
		successfulReads int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_write_only_recovery" {
				sawRecovery = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sawRecovery {
		t.Fatalf("did not expect write-only recovery after named prewrite probe")
	}

	if successfulReads != 1 {
		t.Fatalf("named prewrite read should execute as bounded source refresh, got %d successful read(s)", successfulReads)
	}

	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestWriteReadyPacketAllowsReadOnCompletedRequiredPathWithGap(t *testing.T) {
	// Multi-file scenario: a.txt then b.txt are required. Model writes a.txt,
	// then reads a.txt (now a completed path) with named_gap to set up the
	// b.txt write. Pre-fix this read was rejected because the path dropped
	// out of remainingRequiredEditPaths after the first write. Post-fix the
	// read scope is the brief's full required_edit_paths set, so reads on
	// completed paths within the working set are allowed (when they include
	// named_gap).
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1,"named_gap":"verify the line a.txt now exposes so b.txt can mirror its structure","why_not_answering":"b.txt's edit must mirror the freshly-landed a.txt content"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request: requests=%d", requests)
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
		rejectedReads   int
		successfulReads int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt", "b.txt"},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				if event.Result.Error == "" {
					successfulReads++
				} else {
					rejectedReads++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rejectedReads != 0 {
		t.Fatalf("expected the named-gap read on a.txt (a completed required path) to pass, got %d rejected reads", rejectedReads)
	}

	if successfulReads != 1 {
		t.Fatalf("expected one successful read on a.txt after the first write, got %d", successfulReads)
	}

	for path, want := range map[string]string{"a.txt": "after a\n", "b.txt": "after b\n"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", path, string(got), want)
		}
	}
}

func TestTerminalBlockedStatusSupersedesPrefinishCompletedStatus(t *testing.T) {
	if !terminalBlockedStatusShouldEmit(true, "completed", "", "transport_protocol_output") {
		t.Fatal("terminal blocked status must supersede a pre-finish completed status")
	}

	if terminalBlockedStatusShouldEmit(true, "blocked", "source_grounding_unmet", "transport_protocol_output") {
		t.Fatal("terminal blocked status should not overwrite an already-blocked local status")
	}
}
