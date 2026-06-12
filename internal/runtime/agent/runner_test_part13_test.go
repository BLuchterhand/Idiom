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

func TestRunnerEmptyImplementationReplyAutoReadsSingleEvidencePath(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		"",
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
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
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
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

	if got := countEventsByRole(events, "completion_recovery_auto_evidence_read"); got != 1 {
		t.Fatalf("expected one deterministic evidence read event, got %d events=%v", got, eventRolesFor(events))
	}

	for _, result := range results {
		if result.Tool == "completion_gate" {
			t.Fatalf("empty implementation reply should recover through a bounded read before completion-gate rejection, results=%#v", results)
		}
	}

	if len(results) == 0 || results[0].Tool != "read_file" || results[0].Error != "" {
		t.Fatalf("expected first tool result to be read_file evidence, got %#v", results)
	}

	got, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if !strings.Contains(string(got), `return "Save"`) || strings.Contains(string(got), "Save changes") {
		t.Fatalf("expected source edit to complete after evidence recovery:\n%s", got)
	}
}

func TestDeterministicImplementationEvidencePathReadRequiresSingleEvidencePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
	}
	req, _, ok := runner.deterministicImplementationEvidencePathRead(nil)

	if !ok {
		t.Fatal("expected single evidence path to produce a bounded read")
	}

	if req.Tool != "read_file" || req.Path != "internal/labels/labels.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected read request: %#v", req)
	}

	runner.RuntimeBrief.EvidencePaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	if _, _, ok := runner.deterministicImplementationEvidencePathRead(nil); ok {
		t.Fatal("must not choose between multiple evidence paths")
	}

	runner.RuntimeBrief.TaskFamily = PlanThenExecutePlannerTaskFamily
	runner.RuntimeBrief.EvidencePaths = []string{"internal/labels/labels.go"}

	if _, _, ok := runner.deterministicImplementationEvidencePathRead(nil); ok {
		t.Fatal("plan-execute planner target choice should stay with planner/capability contracts")
	}
}

func TestRunnerSalvagesPathOnlyFileOutlineJSONMissingTool(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"file_outline","path":"cmd/idiom/product_workflow.go"}`,
		nil,
	)

	if !ok {
		t.Fatal("expected visible file_outline payload missing tool field to be salvaged")
	}

	if req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"file_outline"`) {
		t.Fatalf("salvaged raw request must include inferred code_navigate request, got %s", raw)
	}
}

func TestRunnerExecutesCanonicalCodeNavigateForRepeatedRangeLessReadAfterOutline(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "cmd/idiom/product_workflow.go", "package main\n\nfunc runProductWorkflow() string {\n\treturn \"ok\"\n}\n"); err != nil {
		t.Fatalf("write source: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"cmd/idiom/product_workflow.go"}`,
		"Done.",
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
		toolRequests []ToolRequestEvent
		toolResults  []ToolResultEvent
		rewriteEvent string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
				Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed file_outline via go_file_outline",
				Output: strings.Join([]string{
					"capability: file_outline",
					"route_summary: Outlined 1 top-level declaration(s) in cmd/idiom/product_workflow.go.",
					"declarations:",
					"- runProductWorkflow lines 3-5 tags: implementation",
				}, "\n"),
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				rewriteEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update product workflow"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			toolResults = append(toolResults, event)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if len(toolRequests) != 1 {
		t.Fatalf("expected one executed tool request, got %#v", toolRequests)
	}

	gotReq := toolRequests[0].Request
	if gotReq.Tool != "code_navigate" || gotReq.Capability != "read_symbol" || gotReq.Pattern != "runProductWorkflow" {
		t.Fatalf("expected canonical code_navigate read_symbol request, got %#v", gotReq)
	}

	if !strings.Contains(toolRequests[0].Raw, `"tool":"code_navigate"`) || !strings.Contains(toolRequests[0].Raw, `"capability":"read_symbol"`) {
		t.Fatalf("onToolRequest raw should be canonical rewritten JSON, got %s", toolRequests[0].Raw)
	}

	if !strings.Contains(rewriteEvent, "repeated_rangeless_read_file_bounded_source") {
		t.Fatalf("expected repeated range-less read rewrite event, got %q", rewriteEvent)
	}

	for _, event := range toolResults {
		if event.Result.Error != "" || strings.Contains(strings.ToLower(event.Result.Summary), "reject") {
			t.Fatalf("redirected request should execute without rejection, got %#v", event)
		}
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestRunnerExecutesCapabilityOnlyCodeNavigateJSONMissingTool(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {}\n"); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"capability":"file_outline","path":"main.go"}`,
		"Done.",
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
		toolRequests []ToolRequestEvent
		salvageEvent string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "outline main"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if len(toolRequests) != 1 {
		t.Fatalf("expected one salvaged tool request, got %#v", toolRequests)
	}

	gotReq := toolRequests[0].Request
	if gotReq.Tool != "code_navigate" || gotReq.Capability != "file_outline" || gotReq.Path != "main.go" {
		t.Fatalf("expected inferred code_navigate file_outline request, got %#v", gotReq)
	}

	if !strings.Contains(toolRequests[0].Raw, `"tool":"code_navigate"`) {
		t.Fatalf("canonical raw request should include inferred tool, got %s", toolRequests[0].Raw)
	}

	if !strings.Contains(salvageEvent, "code_navigate") {
		t.Fatalf("expected visible code_navigate salvage event, got %q", salvageEvent)
	}
}

func TestRunnerSalvagesMalformedXMLWrapperBeforeToolParse(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "alpha\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`<function=read_file>I'll use {"tool":"read_file","path":"a.txt"}</function broken>`,
		"Done.",
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
		toolRequests     []ToolRequestEvent
		toolParseResults int
		salvageEvent     string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read a.txt"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if toolParseResults != 0 {
		t.Fatalf("recoverable malformed XML should be salvaged before tool_parse, got %d tool_parse result(s)", toolParseResults)
	}

	if len(toolRequests) != 1 || toolRequests[0].Request.Tool != "read_file" || toolRequests[0].Request.Path != "a.txt" {
		t.Fatalf("expected salvaged read_file request, got %#v", toolRequests)
	}

	if !strings.Contains(salvageEvent, "before recording tool_parse") {
		t.Fatalf("expected parse-error salvage event, got %q", salvageEvent)
	}
}

func TestRunnerSalvagesMalformedReadOnlyJSONPrefixBeforeToolParse(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\n\nfunc Run() {}\n"); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":2 - inspect the source before answering`,
		"Evidence comes from bounded source reads.",
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
		toolRequests     []ToolRequestEvent
		toolParseResults int
		salvageEvent     string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "What is the role of evidence in this harness?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is the role of evidence in this harness?"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Evidence comes from bounded source reads." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if toolParseResults != 0 {
		t.Fatalf("recoverable malformed read-only JSON should be salvaged before tool_parse, got %d tool_parse result(s)", toolParseResults)
	}

	if len(toolRequests) != 1 {
		t.Fatalf("expected one salvaged tool request, got %#v", toolRequests)
	}

	req := toolRequests[0].Request
	if req.Tool != "read_file" || req.Path != "internal/runtime/agent/runner.go" || req.StartLine != 1 || req.EndLine != 2 {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}

	if !strings.Contains(salvageEvent, "before recording tool_parse") || !strings.Contains(salvageEvent, "read_file") {
		t.Fatalf("expected visible read_file salvage event, got %q", salvageEvent)
	}
}

func TestParseMalformedVisibleReadOnlyToolJSONPrefixExtractsSafeFields(t *testing.T) {
	req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(
		`{"tool":"search_content","pattern":"Evidence","path":"internal/runtime/agent","limit":"5","named_gap":"find evidence source" - search first`,
	)
	if !ok {
		t.Fatal("expected malformed read-only JSON prefix to be recoverable")
	}

	if req.Tool != "search_content" || req.Pattern != "Evidence" || req.Path != "internal/runtime/agent" || req.Limit != 5 || req.NamedGap != "find evidence source" {
		t.Fatalf("unexpected recovered request: %#v", req)
	}

	if !strings.Contains(raw, `"tool":"search_content"`) || !strings.Contains(raw, `"pattern":"Evidence"`) {
		t.Fatalf("expected canonical raw request, got %s", raw)
	}
}

func TestParseMalformedVisibleReadOnlyToolJSONPrefixRejectsWriteTools(t *testing.T) {
	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(
		`{"tool":"edit_file","path":"README.md","old_text":"old","new_text":"new"} - edit it`,
	); ok {
		t.Fatalf("malformed write JSON must not be recovered as read-only request: req=%#v raw=%s", req, raw)
	}
}

func TestRunnerDoesNotSalvageMalformedWriteJSONPrefixInReadOnlyMode(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "old\n"); err != nil {
		t.Fatalf("write README: %v", err)
	}

	replies := []string{
		`{"tool":"edit_file","path":"README.md","old_text":"old","new_text":"new" - apply this edit`,
		"<<QUESTION>>: Blocked by malformed write request.",
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
		toolRequests     []ToolRequestEvent
		toolParseResults int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: false,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain the README.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain the README."}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Blocked by malformed write request." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if toolParseResults != 1 {
		t.Fatalf("malformed write JSON must remain a tool_parse rejection, got %d", toolParseResults)
	}

	if len(toolRequests) != 0 {
		t.Fatalf("malformed write JSON must not be salvaged into a tool request, got %#v", toolRequests)
	}

	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	if string(data) != "old\n" {
		t.Fatalf("read-only malformed write changed workspace: %q", string(data))
	}
}

func TestRunnerConcreteReadSymbolEvidenceDoesNotRestartRangeLessReadLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "cmd/idiom/product_workflow.go", "package main\n\nfunc runProductWorkflow() string {\n\treturn \"ok\"\n}\n"); err != nil {
		t.Fatalf("write source: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"cmd/idiom/product_workflow.go"}`,
		"Done.",
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
		toolRequests []ToolRequestEvent
		rewriteEvent string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
				Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed file_outline via go_file_outline",
				Output: strings.Join([]string{
					"capability: file_outline",
					"route_summary: Outlined 1 top-level declaration(s) in cmd/idiom/product_workflow.go.",
					"declarations:",
					"- runProductWorkflow lines 3-5 tags: implementation",
				}, "\n"),
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed read_symbol via go_read_symbol",
				Output: strings.Join([]string{
					"capability: read_symbol",
					"route_summary: Read runProductWorkflow in cmd/idiom/product_workflow.go lines 3-5",
					"func runProductWorkflow() string {",
					"\treturn \"ok\"",
					"}",
				}, "\n"),
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				rewriteEvent = event.Content
			}
		},
	}

	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update product workflow"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		nil,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(toolRequests) != 1 {
		t.Fatalf("expected one model-requested tool execution, got %#v", toolRequests)
	}

	if toolRequests[0].Request.Tool != "read_file" {
		t.Fatalf("concrete read_symbol evidence should not restart code_navigate loop, got %#v", toolRequests[0].Request)
	}

	if strings.Contains(rewriteEvent, "repeated_rangeless_read_file_bounded_source") {
		t.Fatalf("did not expect repeated range-less read rewrite after concrete source evidence, got %q", rewriteEvent)
	}
}

func TestLooksLikeTextualToolCallRejectsLineOrientedToolPayload(t *testing.T) {
	payload := "tool: code_navigate\ncapability: read_symbol\npath: internal/surfaces/tui/app/turn_planner.go\npattern: classifyTurnRoute"
	if !looksLikeTextualToolCall(payload) {
		t.Fatalf("expected line-oriented tool payload to be rejected")
	}

	contextPayload := "tool: context_pack\ntask: plan a bounded change to ShortLabel in internal/labels/labels.go\nmode: plan\nlimit: 8"
	if !looksLikeTextualToolCall(contextPayload) {
		t.Fatalf("expected context-pack line-oriented tool payload to be rejected")
	}

	multilineJSONPayload := "tool: read_file\n{\n  \"tool\": \"read_file\",\n  \"path\": \"internal/labels/labels.go\"\n}"
	if !looksLikeTextualToolCall(multilineJSONPayload) {
		t.Fatalf("expected multiline JSON line-oriented tool payload to be rejected")
	}

	if looksLikeTextualToolCall("Tool: route classifier\n\nThis section explains the classifier in prose.") {
		t.Fatalf("prose heading should not be rejected as a line-oriented tool payload")
	}

	// <invoke> XML tool-call markup detection
	if !looksLikeTextualToolCall("<invoke name=\"context_pack\">") {
		t.Fatalf("expected <invoke> XML to be detected as textual tool call")
	}

	if !looksLikeTextualToolCall("</invoke>") {
		t.Fatalf("expected </invoke> XML to be detected as textual tool call")
	}

	if !looksLikeTextualToolCall("<invoke") {
		t.Fatalf("expected partial <invoke to be detected as textual tool call")
	}

	if !looksLikeTextualToolCall("<invoke>\n<invoke name=\"search_files\">\n</invoke>") {
		t.Fatalf("expected invoke XML block to be detected as textual tool call")
	}
}

// Test that janus_tool_calls XML is detected as textual tool call
func TestLooksLikeTextualToolCallDetectsJanusToolCalls(t *testing.T) {
	// The exact format from the live conversation log
	livePayload := `<janus_tool_calls>
<janus_invoke name="code_navigate">
<janus_parameter name="capability" string="true">find_symbol</janus_parameter>
<janus_parameter name="named_gap" string="true">Find PlanThenExecutePlannerTaskFamily constant</janus_parameter>
<janus_parameter name="path" string="true">internal/runtime/agent</janus_parameter>
</janus_invoke>
</janus_tool_calls>`
	if !looksLikeTextualToolCall(livePayload) {
		t.Fatal("expected janus_tool_calls XML to be detected as textual tool call")
	}

	if !looksLikeTextualToolCall("<janus_tool_calls>") {
		t.Fatal("expected bare janus_tool_calls open tag to be detected")
	}

	if !looksLikeTextualToolCall("</janus_tool_calls>") {
		t.Fatal("expected bare janus_tool_calls close tag to be detected")
	}

	if !looksLikeTextualToolCall("<janus_invoke name=\"code_navigate\">") {
		t.Fatal("expected janus_invoke open tag to be detected")
	}

	if !looksLikeTextualToolCall("</janus_invoke>") {
		t.Fatal("expected janus_invoke close tag to be detected")
	}
}

// Test that n_tool_calls XML (DeepSeek's native tool-call markup when
// forced into text-only mode) is detected as textual tool call.
func TestLooksLikeTextualToolCallDetectsNToolCalls(t *testing.T) {
	payload := `<n_tool_calls>
<n_invoke name="code_navigate">
<n_parameter name="capability" string="true">find_symbol</n_parameter>
<n_parameter name="named_gap" string="true">Find constant</n_parameter>
<n_parameter name="path" string="true">internal/runtime/agent</n_parameter>
</n_invoke>
</n_tool_calls>`
	if !looksLikeTextualToolCall(payload) {
		t.Fatal("expected n_tool_calls XML to be detected as textual tool call")
	}

	if !looksLikeTextualToolCall("<n_tool_calls>") {
		t.Fatal("expected bare n_tool_calls open tag to be detected")
	}

	if !looksLikeTextualToolCall("</n_tool_calls>") {
		t.Fatal("expected bare n_tool_calls close tag to be detected")
	}

	if !looksLikeTextualToolCall("<n_invoke name=\"read_file\">") {
		t.Fatal("expected n_invoke open tag to be detected")
	}

	if !looksLikeTextualToolCall("</n_invoke>") {
		t.Fatal("expected n_invoke close tag to be detected")
	}

	if !looksLikeTextualToolCall("<n_parameter name=\"path\" string=\"true\">foo</n_parameter>") {
		t.Fatal("expected n_parameter tag to be detected")
	}
}

func TestIsTextualToolCallExportedAPI(t *testing.T) {
	// Verify the exported API that the presentation layer uses
	if !IsTextualToolCall("<invoke name=\"context_pack\">") {
		t.Fatal("IsTextualToolCall should detect invoke XML")
	}

	if !IsTextualToolCall("<tool_call>{\"tool\":\"read\"}</tool_call>") {
		t.Fatal("IsTextualToolCall should detect tool_call XML")
	}

	if IsTextualToolCall("The lane transition works by returning to main state after implementation.") {
		t.Fatal("IsTextualToolCall should not flag normal prose")
	}
}

func TestFinalAnswerNeedsDecorationRejectsStructuredAnswerEnvelope(t *testing.T) {
	answer := `{"answer":["The route classifier chooses the workflow family."],"verification":["internal/surfaces/tui/app/turn_planner.go"]}`
	if !finalAnswerNeedsDecoration(answer) {
		t.Fatal("expected structured answer envelope to need decoration")
	}
}

func TestUnexecutedToolJSONDetectionDoesNotRejectGenericJSON(t *testing.T) {
	for _, reply := range []string{
		`{"items":["one","two"]}`,
		`{"command":"make build"}`,
		`{"prompt":"Summarize this file"}`,
		"Here is an explanatory JSON example for documentation:\n\n```json\n" +
			`{"tool":"search_files","pattern":"*.jsonl","max_depth":2}` + "\n```\n\n" +
			"That example is not a tool call being requested now.",
	} {
		if looksLikeUnexecutedToolRequestAnswer(reply) {
			t.Fatalf("generic JSON should not be treated as an unexecuted tool request: %s", reply)
		}
	}

	if !looksLikeUnexecutedToolRequestAnswer(`{"task":"implement docs/writer-route-decision-report-only-plan.md","mode":"plan","limit":12}`) {
		t.Fatalf("context_pack-shaped JSON should still be rejected as an unexecuted tool request")
	}
}

func TestUnexecutedToolJSONDetectionRejectsLongProcessProse(t *testing.T) {
	reply := "The search returned no results, which is unexpected given the previous tool result showed 400 log files. Let me try a different approach to find the latest conversation logs.\n\n" +
		`{"tool":"search_files","pattern":"*.jsonl","max_depth":2}`
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		t.Fatal("expected long process prose with embedded tool JSON to be rejected")
	}

	result, rejected := (&Runner{}).unexecutedToolRequestCompletionRejection(reply)
	if !rejected || result.Tool != "completion_gate" || !strings.Contains(result.Summary, "Unexecuted tool request") {
		t.Fatalf("unexpected completion rejection: rejected=%v result=%#v", rejected, result)
	}
}

func TestRunnerCountsForcedCompletionUsageInBudgetLedger(t *testing.T) {
	replies := []map[string]any{
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "```tool\n{\"tool\":\"pwd\"}\n```"},
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		},
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Forced final."},
			}},
			"usage": map[string]int{"prompt_tokens": 13, "completion_tokens": 3, "total_tokens": 16},
		},
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(reply)
	}))

	defer server.Close()

	var budgetEvent string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "pwd then stop"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Forced final." {
		t.Fatalf("unexpected answer %q", answer)
	}

	var ledger runnerBudgetLedger

	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}

	if ledger.StopReason != "tool_step_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("unexpected budget state: %#v", ledger)
	}

	if ledger.UsedModelCalls != 2 || ledger.PromptTokens != 18 || ledger.CompletionTokens != 5 || ledger.TotalTokens != 23 {
		t.Fatalf("expected forced completion usage in budget ledger, got %#v", ledger)
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
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
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
