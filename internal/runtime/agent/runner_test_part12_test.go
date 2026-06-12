package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestRunnerDoesNotSalvageExplicitUnknownCodeNavigateCapabilityJSON(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(
		`{"tool":"code_navigate","capability":"summarize","path":"cmd/idiom/main.go"}`,
		nil,
	)

	if ok {
		t.Fatal("explicit code_navigate with unknown capability should not be salvaged")
	}
}

func TestRunnerTargetSourceManifestHidesVerificationTools(t *testing.T) {
	var payload struct {
		Tools []llama.Tool `json:"tools,omitempty"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "<<QUESTION>>: Cannot edit because no concrete target is resolvable."},
			}},
		})
	}))

	defer server.Close()

	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
	}

	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "update behavior"}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, "read_file") || !chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, "edit_file") {
		t.Fatalf("target-source manifest should expose read/write tools, got %#v", payload.Tools)
	}

	for _, forbidden := range []string{"source_sanity", "shell"} {
		if chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, forbidden) {
			t.Fatalf("target-source manifest should hide %s before writes, got %#v", forbidden, payload.Tools)
		}
	}
}

func TestTargetSourceTextualProtocolHidesVerificationTools(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")

	runner := &Runner{Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX)}
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity", "shell"},
	}, nil)

	prompt := runner.textualToolProtocolPrompt(brief)

	for _, want := range []string{"- read_file", "- edit_file"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("target-source textual protocol missing %s:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{"source_sanity", "shell", "verification_shell"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("target-source textual protocol advertised %s before write:\n%s", forbidden, prompt)
		}
	}
}

func TestRunnerTargetSourceRejectsWriteBeforeSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.go", "package main\n\nconst value = \"before\"\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"edit_file","path":"target.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"read_file","path":"target.go","start_line":1,"end_line":20,"named_gap":"confirm source before edit retry"}`,
		"Done. Changed target.go; verification not required for this test.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Done. Changed target.go; verification not required for this test."
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

	var redirectedRead bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "search_content", "edit_file"},
			VerificationContract: "not_required_for_text_mutations",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "target.go") {
			redirectedRead = true
		}
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update target.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !redirectedRead {
		t.Fatal("expected target-source write-before-evidence to redirect to a bounded read")
	}

	got, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if !strings.Contains(string(got), "after") {
		t.Fatalf("deferred write was not replayed after source-body evidence:\n%s", got)
	}
}

func TestRunnerTargetSourceRejectsExistingWriteFileBeforeSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.go", "package main\n\nconst value = \"before\"\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"write_file","path":"target.go","content":"package main\n\nconst value = \"after\"\n","description":"replace file"}`,
		`{"tool":"read_file","path":"target.go","start_line":1,"end_line":20,"named_gap":"confirm source before write retry"}`,
		"Done. Changed target.go; verification not required for this test.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Done. Changed target.go; verification not required for this test."
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

	var redirectedRead bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "search_content", "write_file"},
			VerificationContract: "not_required_for_text_mutations",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "target.go") {
			redirectedRead = true
		}
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update target.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !redirectedRead {
		t.Fatal("expected target-source write_file before evidence to redirect to a bounded read")
	}

	got, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if !strings.Contains(string(got), "after") {
		t.Fatalf("deferred write_file was not replayed after source-body evidence:\n%s", got)
	}
}

func TestImplementationTargetSourceWriteBeforeEvidenceRedirectsToBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file"},
		},
	}
	req := workspace.ToolRequest{
		Tool:    "edit_file",
		Path:    "internal/labels/labels.go",
		OldText: `return "Save changes"`,
		NewText: `return "Save"`,
	}

	redirected, raw, ok := runner.implementationTargetSourceWriteBeforeEvidenceRedirect(req, nil, []string{"read_file", "edit_file"})
	if !ok {
		t.Fatal("expected write-before-evidence to redirect to a bounded read of the model-selected target")
	}

	if redirected.Tool != "read_file" || redirected.Path != "internal/labels/labels.go" || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}

	if !strings.Contains(raw, `"named_gap":"source-body evidence before applying the requested write"`) {
		t.Fatalf("redirect raw missing named gap: %s", raw)
	}
}

func TestImplementationDeferredWriteReplayRequiresAnchorCoverage(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "internal/labels/labels.go",
		OldText:     `return "Save changes"`,
		NewText:     `return "Save"`,
		Description: "shorten label",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "internal/labels/labels.go")

	if deferred == nil {
		t.Fatal("expected deferred write")
	}

	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func Other() string { return \"Cancel\" }\n",
	}}
	repeatedRead := workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go", StartLine: 1, EndLine: 20}

	if replayReq, raw, replay := deferred.replayAfterEvidence(repeatedRead, results, []string{"read_file", "edit_file"}); replay {
		t.Fatalf("must not replay when source evidence does not cover old_text, got req=%#v raw=%q", replayReq, raw)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	})
	if replayReq, _, replay := deferred.replayAfterEvidence(repeatedRead, results, []string{"read_file", "edit_file"}); !replay || replayReq.Tool != "edit_file" {
		t.Fatalf("expected replay only after anchor-covered source evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestImplementationDeferredWriteReplayRequiresHistoryWhenMarked(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "internal/labels/labels.go",
		OldText:     `return "Save changes"`,
		NewText:     `return "Save"`,
		Description: "shorten label",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "internal/labels/labels.go")

	if deferred == nil {
		t.Fatal("expected deferred write")
	}

	deferred.RequiresHistory = true

	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	verifyReq := workspace.ToolRequest{Tool: "source_sanity", Path: "internal/labels/labels.go"}

	if replayReq, raw, replay := deferred.replayAfterEvidence(verifyReq, results, []string{"source_sanity", "edit_file"}); replay {
		t.Fatalf("must not replay before history evidence, got req=%#v raw=%q", replayReq, raw)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for internal/labels/labels.go lines 1-5",
		Output:  "commit abc123\n\n    Preserve current label behavior",
	})
	if replayReq, _, replay := deferred.replayAfterEvidence(verifyReq, results, []string{"source_sanity", "edit_file"}); !replay || replayReq.Tool != "edit_file" {
		t.Fatalf("expected replay after source and history evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestImplementationDeferredAppendReplayRequiresFullFileEvidence(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "append_file",
		Path:        "notes.txt",
		Content:     "\nafter\n",
		Description: "append note",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "notes.txt")

	if deferred == nil {
		t.Fatal("expected deferred append")
	}

	repeatedRead := workspace.ToolRequest{Tool: "read_file", Path: "notes.txt", StartLine: 1, EndLine: 180}
	partial := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 notes.txt lines 1-180 (total lines: 300)",
		Output:  "1:before\n",
	}}

	if replayReq, raw, replay := deferred.replayAfterEvidence(repeatedRead, partial, []string{"read_file", "append_file"}); replay {
		t.Fatalf("must not replay append after partial source evidence, got req=%#v raw=%q", replayReq, raw)
	}

	full := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 notes.txt lines 1-3 (total lines: 3)",
		Output:  "1:before\n2:middle\n3:end\n",
	}}
	if replayReq, _, replay := deferred.replayAfterEvidence(repeatedRead, full, []string{"read_file", "append_file"}); !replay || replayReq.Tool != "append_file" {
		t.Fatalf("expected append replay only after full-file source evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestRunnerTargetSourceRejectsWriteAfterReadingDifferentPath(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n\nconst a = \"read\"\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	if err := writeTestFile(root, "b.go", "package main\n\nconst b = \"before\"\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		"<<QUESTION>>: Cannot write b.go before source-body evidence for b.go.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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

	var redirectedRead bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "b.go") {
			redirectedRead = true
		}
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update b.go after checking a.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !redirectedRead {
		t.Fatal("expected target-source write after reading a different path to redirect to a bounded read of the write target")
	}

	got, err := os.ReadFile(filepath.Join(root, "b.go"))
	if err != nil {
		t.Fatalf("read b: %v", err)
	}

	if strings.Contains(string(got), "after") {
		t.Fatalf("write executed after reading a different path:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsNewTargetReadAfterDifferentPathWriteRejection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/recovery\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "a.go", "package main\n\nconst a = \"read\"\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	if err := writeTestFile(root, "b.go", "package main\n\nconst b = \"before\"\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: b.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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

	var sawRedirectedTargetRead bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 7, MaxNoProgressTurns: 7},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && event.Request.Path == "b.go" && strings.Contains(event.Result.Summary, "b.go") {
			sawRedirectedTargetRead = true
		}
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update b.go after checking a.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !sawRedirectedTargetRead {
		t.Fatal("expected first b.go write-before-evidence to redirect to b.go source-body evidence")
	}

	got, err := os.ReadFile(filepath.Join(root, "b.go"))
	if err != nil {
		t.Fatalf("read b: %v", err)
	}

	if !strings.Contains(string(got), "after") || strings.Contains(string(got), "before") {
		t.Fatalf("expected second write to execute after b.go source-body evidence:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsRedirectedSourceFollowupAfterWriteReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/session\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/surfaces/tui/app/sessions_panel.go", "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n"); err != nil {
		t.Fatalf("write sessions_panel: %v", err)
	}

	if err := writeTestFile(root, "internal/surfaces/tui/app/sessions.go", "package app\n\nfunc ScrollOffset() int {\n\treturn 0\n}\n"); err != nil {
		t.Fatalf("write sessions: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions_panel.go","start_line":1,"end_line":20,"named_gap":"session panel source before editing scroll behavior"}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions.go"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/surfaces/tui/app/sessions.go","pattern":"ScrollOffset"}`,
		`{"tool":"edit_file","path":"internal/surfaces/tui/app/sessions.go","old_text":"return 0","new_text":"return 1","description":"advance scroll offset when selection reaches the pane edge"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/surfaces/tui/app/sessions.go\n\nVerification: go test ./... passed.",
	}
	requests := 0

	var trace []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d\n%s", requests, strings.Join(trace, "\n"))
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
		sawRedirectAllowance       bool
		rejectedRedirectedFollowup bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		PreExecute: func(_ int, req workspace.ToolRequest) *workspace.ToolResult {
			switch strings.TrimSpace(req.Tool) {
			case "read_file":
				if normalizeRunnerPath(req.Path) == "internal/surfaces/tui/app/sessions.go" && req.StartLine <= 0 && req.EndLine <= 0 {
					return &workspace.ToolResult{
						Tool:    "read_file",
						Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
						Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"internal/surfaces/tui/app/sessions.go","language":"go"}`,
					}
				}
			case "code_navigate":
				if normalizeRunnerPath(req.Path) == "internal/surfaces/tui/app/sessions.go" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed read_symbol via go_read_symbol",
						Output: strings.Join([]string{
							"capability: read_symbol",
							"language: go",
							"routed_tool: go_read_symbol",
							"route_summary: Read ScrollOffset in internal/surfaces/tui/app/sessions.go lines 3-5",
							"3:func ScrollOffset() int {",
							"4:\treturn 0",
							"5:}",
						}, "\n"),
					}
				}
			}

			return nil
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "implementation_target_write_ready_source_refresh_allowed",
				"implementation_target_write_ready_recovery",
				"post_write_verification_required",
				"completion_gate_recovery":
				trace = append(trace, "event "+event.Role+": "+event.Content)
			}

			if event.Role == "implementation_target_write_ready_source_refresh_allowed" &&
				strings.Contains(event.Content, "code_navigate") {

				sawRedirectAllowance = true
			}
		},
	}
	onToolResult := func(event ToolResultEvent) {
		trace = append(trace, fmt.Sprintf("tool %s %s => %s err=%s", event.Request.Tool, event.Request.Path, event.Result.Summary, event.Result.Error))
		if event.Request.Tool == "code_navigate" &&
			event.Request.Path == "internal/surfaces/tui/app/sessions.go" &&
			strings.Contains(event.Result.Summary, "rejected") {

			rejectedRedirectedFollowup = true
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "fix session panel scrolling"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(answer, "target-write-ready recovery rejected") {
		t.Fatalf("runner returned write-ready blocker:\n%s", answer)
	}

	if rejectedRedirectedFollowup {
		t.Fatal("redirected code_navigate follow-up was rejected, recreating the latest write-ready loop")
	}

	if !sawRedirectAllowance {
		t.Fatal("expected write-ready allowance event for redirected code_navigate source follow-up")
	}

	got, err := os.ReadFile(filepath.Join(root, "internal/surfaces/tui/app/sessions.go"))
	if err != nil {
		t.Fatalf("read sessions.go: %v", err)
	}

	if !strings.Contains(string(got), "return 1") || strings.Contains(string(got), "return 0") {
		t.Fatalf("expected edit after redirected source-body follow-up:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsRedirectedOutlineThenSymbolAfterWriteReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/session\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/surfaces/tui/app/sessions_panel.go", "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n"); err != nil {
		t.Fatalf("write sessions_panel: %v", err)
	}

	if err := writeTestFile(root, "internal/surfaces/tui/app/sessions.go", "package app\n\nfunc ScrollOffset() int {\n\treturn 0\n}\n"); err != nil {
		t.Fatalf("write sessions: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions_panel.go","start_line":1,"end_line":20,"named_gap":"session panel source before editing scroll behavior"}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions.go"}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions.go"}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/sessions.go"}`,
		`{"tool":"edit_file","path":"internal/surfaces/tui/app/sessions.go","old_text":"return 0","new_text":"return 1","description":"advance scroll offset when selection reaches the pane edge"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/surfaces/tui/app/sessions.go\n\nVerification: go test ./... passed.",
	}
	requests := 0

	var trace []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d\n%s", requests, strings.Join(trace, "\n"))
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
		sawOutlineRewrite          bool
		sawReadSymbolRewrite       bool
		rejectedRedirectedFollowup bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix session pane ScrollOffset behavior",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 9, MaxNoProgressTurns: 9},
		PreExecute: func(_ int, req workspace.ToolRequest) *workspace.ToolResult {
			switch strings.TrimSpace(req.Tool) {
			case "read_file":
				if normalizeRunnerPath(req.Path) == "internal/surfaces/tui/app/sessions.go" && req.StartLine <= 0 && req.EndLine <= 0 {
					return &workspace.ToolResult{
						Tool:    "read_file",
						Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
						Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"internal/surfaces/tui/app/sessions.go","language":"go"}`,
					}
				}
			case "code_navigate":
				if normalizeRunnerPath(req.Path) == "internal/surfaces/tui/app/sessions.go" && req.Capability == "file_outline" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed file_outline via go_file_outline",
						Output: strings.Join([]string{
							"capability: file_outline",
							"language: go",
							"routed_tool: go_file_outline",
							"route_summary: Outlined 1 top-level declaration(s) in internal/surfaces/tui/app/sessions.go.",
							"declarations:",
							"- ScrollOffset lines 3-5 tags: implementation",
						}, "\n"),
					}
				}

				if normalizeRunnerPath(req.Path) == "internal/surfaces/tui/app/sessions.go" && req.Capability == "read_symbol" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed read_symbol via go_read_symbol",
						Output: strings.Join([]string{
							"capability: read_symbol",
							"language: go",
							"routed_tool: go_read_symbol",
							"route_summary: Read ScrollOffset in internal/surfaces/tui/app/sessions.go lines 3-5",
							"3:func ScrollOffset() int {",
							"4:\treturn 0",
							"5:}",
						}, "\n"),
					}
				}
			}

			return nil
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				trace = append(trace, "rewrite: "+event.Content)
				if strings.Contains(event.Content, `file_outline`) {
					sawOutlineRewrite = true
				}

				if strings.Contains(event.Content, `read_symbol`) {
					sawReadSymbolRewrite = true
				}
			}

			if event.Role == "implementation_target_write_ready_recovery" {
				trace = append(trace, "recovery: "+event.Content)
			}
		},
	}
	onToolResult := func(event ToolResultEvent) {
		trace = append(trace, fmt.Sprintf("tool %s %s => %s err=%s", event.Request.Tool, event.Request.Path, event.Result.Summary, event.Result.Error))
		if event.Request.Tool == "code_navigate" &&
			event.Request.Path == "internal/surfaces/tui/app/sessions.go" &&
			strings.Contains(event.Result.Summary, "rejected") {

			rejectedRedirectedFollowup = true
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "fix session panel scrolling"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(answer, "target-write-ready recovery rejected") {
		t.Fatalf("runner returned write-ready blocker:\n%s\ntrace:\n%s", answer, strings.Join(trace, "\n"))
	}

	if !sawOutlineRewrite || !sawReadSymbolRewrite {
		t.Fatalf("expected repeated read_file redirects to outline then read_symbol, outline=%v read_symbol=%v\n%s", sawOutlineRewrite, sawReadSymbolRewrite, strings.Join(trace, "\n"))
	}

	if rejectedRedirectedFollowup {
		t.Fatalf("redirected code_navigate follow-up was rejected:\n%s", strings.Join(trace, "\n"))
	}

	got, err := os.ReadFile(filepath.Join(root, "internal/surfaces/tui/app/sessions.go"))
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}

	if !strings.Contains(string(got), "return 1") {
		t.Fatalf("expected edit after redirected outline/read_symbol follow-up:\n%s\ntrace:\n%s", got, strings.Join(trace, "\n"))
	}
}

func TestRunnerTargetSourceAllowsSameWriteAfterSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	edit := `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`
	replies := []string{
		edit,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20,"named_gap":"confirm exact ShortLabel source before editing"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		edit,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed."
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
		events  []RunEvent
		results []workspace.ToolResult
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell"},
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update internal/labels/labels.go"}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("source-backed write retry must not be blocked as stale rejection, got %d events=%v", got, eventRolesFor(events))
	}

	seenInitialReadRedirect := false
	seenVerification := false

	for _, result := range results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/labels/labels.go") {
			seenInitialReadRedirect = true
		}

		if result.Tool == "shell" && strings.TrimSpace(result.Error) == "" {
			seenVerification = true
		}
	}

	if !seenInitialReadRedirect {
		t.Fatal("expected first write-before-evidence to redirect to source-body evidence")
	}

	if !seenVerification {
		t.Fatalf("expected language verification command to run after accepted edit, results=%#v", results)
	}

	got, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if !strings.Contains(string(got), `return "Save"`) || strings.Contains(string(got), "Save changes") {
		t.Fatalf("expected second write to execute after source-body evidence:\n%s", got)
	}
}

func TestRunnerPostWriteVerificationRecoversRepeatedInvalidToolThroughSourceSanity(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":6,"named_gap":"current ShortLabel source before editing"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save\"","new_text":"return \"Save\"","description":"retry same label change"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":6}`,
		"Changed paths: internal/labels/labels.go\n\nVerification: Source sanity passed for internal/labels/labels.go.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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
		events  []RunEvent
		results []workspace.ToolResult
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update internal/labels/labels.go"}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("post-write invalid tool recovery must not force a pending-verification answer, got %d events=%v", got, eventRolesFor(events))
	}

	sawRewrite := false

	for _, event := range events {
		if event.Role == "tool_request_rewrite" && strings.Contains(event.Content, `"rewritten_tool":"source_sanity"`) {
			sawRewrite = true
		}
	}

	if !sawRewrite {
		t.Fatalf("expected repeated invalid verification tool to be rewritten to source_sanity, events=%v", eventRolesFor(events))
	}

	sawSourceSanity := false

	for _, result := range results {
		if result.Tool == "source_sanity" && result.Error == "" {
			sawSourceSanity = true
		}
	}

	if !sawSourceSanity {
		t.Fatalf("expected source_sanity recovery result, got %#v", results)
	}

	if !strings.Contains(answer, "Verification: Source sanity passed") {
		t.Fatalf("expected verified final answer, got:\n%s", answer)
	}
}
