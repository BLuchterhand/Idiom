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

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestIsDescribedWriteToolCoversDiffProducingSubset(t *testing.T) {
	described := []string{"edit_file", "write_file", "append_file", "apply_patch"}
	for _, tool := range described {
		if !isDescribedWriteTool(tool) {
			t.Errorf("expected %q to require a description, got false", tool)
		}
	}
	// mkdir and go_move_declarations are write tools but not diff-producing
	// in a way a prose description would help — the first creates an empty
	// directory, the second's intent is already structured in its args.
	exempt := []string{"mkdir", "go_move_declarations", "read_file", "shell", "search_content"}
	for _, tool := range exempt {
		if isDescribedWriteTool(tool) {
			t.Errorf("expected %q to be exempt from description requirement, got true", tool)
		}
	}
}

func TestCheckWriteDescriptionSkipsNonDescribedTools(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "foo.go"}
	if got := checkWriteDescription(req); got != nil {
		t.Fatalf("read_file should be skipped, got guard result: %#v", got)
	}
	req = workspace.ToolRequest{Tool: "mkdir", Path: "foo"}
	if got := checkWriteDescription(req); got != nil {
		t.Fatalf("mkdir should be skipped, got guard result: %#v", got)
	}
}

func TestCheckWriteDescriptionPassesWhenDescriptionPresent(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Content: "pkg", Description: "rename package"}
	if got := checkWriteDescription(req); got != nil {
		t.Fatalf("edit_file with description should pass, got guard result: %#v", got)
	}
}

func TestCheckWriteDescriptionTreatsWhitespaceAsMissing(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "   \t\n "}
	got := checkWriteDescription(req)
	if got == nil {
		t.Fatal("whitespace-only description should be rejected")
	}
	if !strings.Contains(got.Error, "requires a \"description\"") {
		t.Errorf("guard error should explain requirement, got: %q", got.Error)
	}
}

func TestCheckWriteDescriptionRejectsEachDescribedTool(t *testing.T) {
	for _, tool := range []string{"edit_file", "write_file", "append_file", "apply_patch"} {
		req := workspace.ToolRequest{Tool: tool, Path: "x.go"}
		got := checkWriteDescription(req)
		if got == nil {
			t.Errorf("%s without description should be rejected", tool)
			continue
		}
		if got.Tool != tool {
			t.Errorf("%s: guard Tool = %q, want %q", tool, got.Tool, tool)
		}
		if got.Summary != "Missing write description" {
			t.Errorf("%s: guard Summary = %q", tool, got.Summary)
		}
		// The redirect-style nudge must name the offending tool and include
		// a non-copyable "Try:" reshaping so the model can fix and retry.
		if !strings.Contains(got.Error, tool) {
			t.Errorf("%s: guard error should mention the tool name, got: %q", tool, got.Error)
		}
		if !strings.Contains(got.Error, "Try:") {
			t.Errorf("%s: guard error should include a 'Try:' example, got: %q", tool, got.Error)
		}
		if !strings.Contains(got.Error, "executes normally") {
			t.Errorf("%s: guard error should promise normal execution after retry, got: %q", tool, got.Error)
		}
	}
}

func TestCheckWriteDescriptionTryGuidanceAvoidsCopyablePlaceholders(t *testing.T) {
	req := workspace.ToolRequest{Tool: "apply_patch"}
	got := checkWriteDescription(req)
	if got == nil {
		t.Fatal("apply_patch without description should be rejected")
	}
	for _, forbidden := range []string{`"path":""`, `"<one sentence`, `"description":"`} {
		if strings.Contains(got.Error, forbidden) {
			t.Errorf("write description Try guidance must not include copyable placeholder %q, got: %q", forbidden, got.Error)
		}
	}
}

func TestCheckWriteDescriptionEditFileGuidanceNamesSameArguments(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "pkg/foo.go"}
	got := checkWriteDescription(req)
	if got == nil {
		t.Fatal("edit_file without description should be rejected")
	}
	if !strings.Contains(got.Error, "same concrete tool arguments") {
		t.Errorf("edit_file Try guidance should tell the model to preserve concrete arguments, got: %q", got.Error)
	}
}

// TestRunnerRejectsEditWithoutDescription locks in that the guard fires
// through the runner and surfaces as a structured ToolResultEvent.
func TestRunnerRejectsEditWithoutDescription(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"Stopped without description.",
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

	var guarded *workspace.ToolResult
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit without description"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			if strings.Contains(ev.Result.Error, "requires a \"description\"") {
				copy := ev.Result
				guarded = &copy
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if guarded == nil {
		t.Fatal("expected guard to fire as a ToolResultEvent")
	}
	if guarded.Tool != "edit_file" {
		t.Errorf("guard ToolResult.Tool = %q", guarded.Tool)
	}
	// The file must NOT have been mutated — the guard fires before Execute.
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read readme: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("file was mutated despite rejected write: %q", data)
	}
}

func TestScopedImplementationConcreteMissingWriteDescriptionSynthesizesBeforeRetry(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("a.txt")
	var sawValidatorDecision, sawSynthesized bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "dynamic_validator_decision" {
				sawValidatorDecision = true
			}
			if event.Role == "write_description_repair_synthesized" {
				sawSynthesized = true
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
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report repaired write, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected corrected write and final answer, got %d requests", requests)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("synthesized missing-description write should mutate file, got %q", data)
	}
	if !sawValidatorDecision || !sawSynthesized {
		t.Fatalf("expected validator and synthesized events, saw decision=%v synthesized=%v", sawValidatorDecision, sawSynthesized)
	}
}

func TestScopedImplementationMissingWriteDescriptionSynthesizesDescription(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("a.txt")
	var sawSynthesized bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "write_description_repair_synthesized" {
				sawSynthesized = true
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
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report repaired write, got %q", answer)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("synthesized-description write should mutate file, got %q", data)
	}
	if !sawSynthesized {
		t.Fatal("expected write_description_repair_synthesized event")
	}
}

func TestWriteDescriptionRepairSynthesisRejectsUnsafeOrOutOfScopeWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"allowed.txt"},
		},
	}
	tests := []struct {
		name         string
		req          workspace.ToolRequest
		repairActive bool
	}{
		{
			name:         "non concrete edit",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "edit_file", Path: "allowed.txt"},
		},
		{
			name:         "ambiguous old text only edit",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "edit_file", Path: "allowed.txt", OldText: "before"},
		},
		{
			name:         "placeholder path",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "edit_file", Path: "<path>", OldText: "before", NewText: "after"},
		},
		{
			name:         "empty path",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "write_file", Content: "after\n"},
		},
		{
			name:         "malformed patch",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "apply_patch", Content: "*** Begin Patch\n*** End Patch\n"},
		},
		{
			name:         "out of required scope",
			repairActive: true,
			req:          workspace.ToolRequest{Tool: "edit_file", Path: "other.txt", OldText: "before", NewText: "after"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if repaired, raw, ok := runner.writeDescriptionRepairSynthesizedRequest(tt.req, tt.repairActive); ok {
				t.Fatalf("unexpected synthesis: repaired=%#v raw=%q", repaired, raw)
			}
		})
	}
	nonMutationRunner := &Runner{}
	for _, repairActive := range []bool{false, true} {
		if repaired, raw, ok := nonMutationRunner.writeDescriptionRepairSynthesizedRequest(workspace.ToolRequest{Tool: "edit_file", Path: "allowed.txt", OldText: "before", NewText: "after"}, repairActive); ok {
			t.Fatalf("unexpected non-mutation synthesis with repairActive=%v: repaired=%#v raw=%q", repairActive, repaired, raw)
		}
	}
	if _, _, ok := runner.writeDescriptionRepairSynthesizedRequest(workspace.ToolRequest{Tool: "edit_file", Path: "allowed.txt", OldText: "before", NewText: "after"}, false); !ok {
		t.Fatal("expected implementation-lane concrete write to synthesize missing description on first pass")
	}
}

func TestScopedImplementationSynthesizedWriteStillRequiresSourceEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
		"<<QUESTION>>: Cannot edit a.txt before source-body evidence is available.",
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

	var sawSynthesized, sawSourceRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "write_description_repair_synthesized" {
				sawSynthesized = true
			}
		},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "a.txt") {
				sawSourceRead = true
			}
		},
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawSynthesized || !sawSourceRead {
		t.Fatalf("expected synthesized write then source-evidence read redirect, synthesized=%v sourceRead=%v", sawSynthesized, sawSourceRead)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(data) != "before\n" {
		t.Fatalf("write should not execute before source evidence, got %q", data)
	}
}

func TestScopedImplementationSynthesizedWriteStillHonorsPermissionDeny(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
		"<<QUESTION>>: Cannot edit a.txt because the permission policy denied edit_file for that path.",
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

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("a.txt")
	var sawSynthesized, sawPermissionDeny bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			EvidencePaths:        []string{"a.txt"},
		},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
			Rules: []ToolPermissionRule{{
				Tool:        "edit_file",
				PathPattern: "a.txt",
				Decision:    PermissionDeny,
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "write_description_repair_synthesized" {
				sawSynthesized = true
			}
			if event.Role == "permission_decision" && strings.Contains(event.Content, `"decision":"deny"`) {
				sawPermissionDeny = true
			}
		},
	}
	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "Update a.txt."}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawSynthesized || !sawPermissionDeny {
		t.Fatalf("expected synthesized write then permission denial, synthesized=%v permissionDeny=%v", sawSynthesized, sawPermissionDeny)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(data) != "before\n" {
		t.Fatalf("permission-denied synthesized write should not mutate file, got %q", data)
	}
}

func TestScopedImplementationMissingWriteDescriptionRepeatedDetourReturnsTypedBlocker(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"a.txt"}`,
		`{"tool":"search_content","path":".","pattern":"before"}`,
		`{"tool":"search_content","path":".","pattern":"still before"}`,
		`{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after","description":"write after passive missing-description rejection"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
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

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("a.txt")
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {},
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
	if !strings.Contains(answer, "write-description repair rejected repeated non-write detours") {
		t.Fatalf("expected typed detour blocker, got %q", answer)
	}
	if requests != 3 {
		t.Fatalf("expected blocker before corrected write, got %d requests", requests)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(data) != "before\n" {
		t.Fatalf("detour blocker should not mutate file, got %q", data)
	}
}

func TestScopedImplementationWriteDescriptionRepairClearsForTargetPath(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "alpha\n"); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := writeTestFile(root, "b.txt", "before\n"); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"b.txt","old_text":"before","new_text":"after"}`,
		`{"tool":"read_file","path":"a.txt","named_gap":"confirm first required file before writing it"}`,
		`{"tool":"edit_file","path":"a.txt","old_text":"alpha","new_text":"done","description":"update the first required file after the second file repair is complete"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
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

	var sawReadSuccess bool
	var readResult workspace.ToolResult
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("b.txt")
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			EvidencePaths:        []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both files."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Request.Path == "a.txt" {
				readResult = event.Result
				if strings.TrimSpace(event.Result.Error) == "" {
					sawReadSuccess = true
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawReadSuccess {
		t.Fatalf("expected scoped read for another required path after target repair to succeed, got result %#v", readResult)
	}
	if !strings.Contains(answer, "a.txt") || !strings.Contains(answer, "b.txt") {
		t.Fatalf("expected final answer to report both paths, got %q", answer)
	}
	for path, want := range map[string]string{"a.txt": "done\n", "b.txt": "after\n"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", path, data, want)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected full synth/read/edit/final sequence, got %d requests", requests)
	}
}

// TestRunnerAllowsEditWithDescription verifies the happy path — once the
// description is present the edit executes and mutates the file.
func TestRunnerAllowsEditWithDescription(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Done.",
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

	var lastReq workspace.ToolRequest
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	if err := ws.SetWritePolicy(workspace.WritePolicyAuto); err != nil {
		t.Fatalf("set write policy: %v", err)
	}
	// Satisfy the read-before-edit invariant for edit_file.
	ws.MarkPathObserved("README.md")
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			if ev.Request.Tool == "edit_file" {
				lastReq = ev.Request
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lastReq.Description != "change greeting" {
		t.Fatalf("Request.Description not propagated through event: %#v", lastReq)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "bye\n" {
		t.Fatalf("file not mutated: %q", data)
	}
}

// TestRunnerGuardFiresAfterPermissionDeny ensures that when a tool is
// denied by policy, the permission decision wins and the description
// guard does not redundantly nag. This is a correctness check on the
// order we chose: PreExecute (permissions) before description guard.
func TestRunnerGuardFiresAfterPermissionDeny(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		// No description — if the guard ran first, the result would be a
		// description-missing error instead of a permission denial.
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"Denied.",
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

	var firstResult workspace.ToolResult
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
			Rules: []ToolPermissionRule{{
				Tool:        "edit_file",
				PathPattern: "README.md",
				Decision:    PermissionDeny,
			}},
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			if firstResult.Tool == "" && ev.Result.Tool == "edit_file" {
				firstResult = ev.Result
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(firstResult.Error, "requires a \"description\"") {
		t.Fatalf("description guard fired before permission check: %q", firstResult.Error)
	}
}

// TestRunnerBailsAfterTwoNonConcreteMissingDescriptions locks in the
// safety valve for missing-description writes that cannot be safely
// synthesized: the runner stops the loop rather than letting the guard
// fire indefinitely. Concrete in-scope writes are covered separately by
// TestScopedImplementationMissingWriteDescriptionSynthesizesDescription.
func TestRunnerBailsAfterTwoNonConcreteMissingDescriptions(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeTestFile(root, "OTHER.md", "hello\n"); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	// Use distinct args on each call so the "repeated tool request" guard
	// doesn't pre-empt the description guard.
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"OTHER.md\"}\n```",
		// If the bail never triggers, the loop will ask for a third reply.
		"Gave up on description.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request: bail did not fire")
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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit without description"}},
		llama.ChatOptions{},
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "write_description_budget_exhausted" {
		t.Fatalf("expected stop_reason=write_description_budget_exhausted, got %#v", ledger)
	}
	if !ledger.BudgetExhausted {
		t.Fatalf("expected budget_exhausted=true on write-description bail, got %#v", ledger)
	}
}

// TestNoEllipsisInTryExamples verifies that no Try: example contains an
// ellipsis placeholder — the model needs valid JSON to retry reliably.
func TestWriteDescriptionNoEllipsisInTryExamples(t *testing.T) {
	tools := []string{"edit_file", "write_file", "append_file", "apply_patch"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			req := workspace.ToolRequest{Tool: tool}
			got := checkWriteDescription(req)
			t.Log(got.Error)
			if strings.Contains(got.Error, "...") {
				t.Errorf("Try example should not contain ellipsis, got: %q", got.Error)
			}
		})
	}
}

func TestWriteDescriptionGuidancePreservesConcreteArgumentsInProse(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:    "edit_file",
		Path:    "foo.go",
		OldText: "old stuff",
		NewText: "new stuff",
	}
	got := checkWriteDescription(req)
	t.Log(got.Error)
	if !strings.Contains(got.Error, "same concrete tool arguments") {
		t.Errorf("Try guidance should preserve existing concrete arguments by instruction, got: %q", got.Error)
	}
}

func TestWriteDescriptionTryGuidanceIsNotCopyableJSON(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:    "edit_file",
		Path:    "foo.go",
		OldText: "old",
		NewText: "new",
	}
	got := checkWriteDescription(req)
	t.Log(got.Error)
	for _, forbidden := range []string{`{"tool"`, `"old_text"`, `"new_text"`, `"description":`} {
		if strings.Contains(got.Error, forbidden) {
			t.Errorf("Try guidance should not include copyable JSON field %q: %q", forbidden, got.Error)
		}
	}
}
