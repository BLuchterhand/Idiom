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

func TestPathlessReadSymbolAfterOutlineInheritsOutlinePath(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "capability: file_outline\nroute_summary: Outlined internal/runtime/agent/runner_child_tasks.go",
	}}
	req := workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Symbol: "Runner.runChildTask"}
	rewritten, raw, reason, ok := pathlessReadSymbolAfterOutlineRewriteRequest(req, results)

	if !ok {
		t.Fatal("expected pathless read_symbol to inherit latest outline path")
	}

	if rewritten.Path != "internal/runtime/agent/runner_child_tasks.go" || rewritten.Pattern != "Runner.runChildTask" {
		t.Fatalf("unexpected rewrite: %#v", rewritten)
	}

	if !strings.Contains(raw, `"path":"internal/runtime/agent/runner_child_tasks.go"`) || reason == "" {
		t.Fatalf("expected raw path rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestRepeatedFileOutlineUpgradesToReadSymbol(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 2 top-level declaration(s) in internal/runtime/agent/runner_child_tasks.go.",
			"declarations:",
			"- runChildTask lines 16-140 tags: delegation",
			"- childTaskOptions lines 142-180 tags: support",
		}, "\n"),
	}}
	req := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/runtime/agent/runner_child_tasks.go", Capability: "file_outline"}
	rewritten, raw, reason, ok := repeatedFileOutlineToReadSymbolRewriteRequest(req, results, workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "delegated_read_only",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	})

	if !ok {
		t.Fatal("expected repeated outline to upgrade to read_symbol")
	}

	if rewritten.Capability != "read_symbol" || rewritten.Pattern != "runChildTask" || rewritten.Path != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("unexpected outline upgrade: %#v", rewritten)
	}

	if !strings.Contains(raw, `"capability":"read_symbol"`) || reason == "" {
		t.Fatalf("expected raw read_symbol rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestReadOnlyDefinitionProbeUpgradesToReadSymbol(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/evidence/transcript/debuglog.go",
		Capability: "find_definition",
		Symbol:     "DebugLogModeRedacted",
	}
	rewritten, raw, reason, ok := readOnlyDefinitionToReadSymbolRewriteRequest(req, workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	})

	if !ok {
		t.Fatal("expected read-only definition probe to upgrade to read_symbol")
	}

	if rewritten.Capability != "read_symbol" || rewritten.Pattern != "DebugLogModeRedacted" || rewritten.Path != "internal/evidence/transcript/debuglog.go" {
		t.Fatalf("unexpected definition rewrite: %#v", rewritten)
	}

	if !strings.Contains(raw, `"capability":"read_symbol"`) || reason == "" {
		t.Fatalf("expected raw read_symbol rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestSafeVisibleToolRequestAllowsConcreteDiscoveryQuery(t *testing.T) {
	runner := &Runner{}
	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Query:   "subagent orchestration",
		Path:    ".",
	}, nil) {

		t.Fatal("concrete discovery_query should be safe visible recovery")
	}

	if runner.safeVisibleToolRequest(workspace.ToolRequest{Tool: "discovery_query"}, nil) {
		t.Fatal("empty discovery_query should not be safe visible recovery")
	}

	if runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "all",
		Path:    ".",
	}, nil) {

		t.Fatal("invalid discovery_query purpose must not be safe visible recovery")
	}

	if runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:  "discovery_query",
		Query: "subagent orchestration",
		Path:  ".",
	}, nil) {

		t.Fatal("arbitrary query text must not be salvaged as a discovery_query purpose")
	}

	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:  "discovery_query",
		Query: "architecture",
		Path:  ".",
	}, nil) {

		t.Fatal("recognized discovery_query purpose aliases should be safe visible recovery")
	}

	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "Run a deterministic layered discovery recipe to identify subagent definitions and orchestration patterns.",
		Path:    ".",
	}, nil) {

		t.Fatal("descriptive architecture discovery purpose should be canonicalized for safe visible recovery")
	}
}

func TestVisibleToolSalvageRepairsDelegatedReadOnlyDiscoveryQueryWithoutTopicScope(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	reply := `{"tool":"discovery_query","query":"discovery tool usefulness","path":"."}`
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, nil)

	if !ok {
		t.Fatal("expected delegated discovery query JSON to salvage through repair")
	}

	if req.Tool != "discovery_query" || req.Purpose != "architecture_boundary" || req.Path != "." {
		t.Fatalf("expected repaired architecture discovery query to keep generic scope, got %#v", req)
	}

	if strings.Contains(req.Pattern, "answer[-_ ]?composer") || strings.Contains(req.Pattern, "synthesizeDelegatedReadOnlyAnswer") {
		t.Fatalf("expected repaired query not to target answer synthesis boundary, got %q", req.Pattern)
	}

	if !strings.Contains(raw, `"purpose":"architecture_boundary"`) || !strings.Contains(raw, `"path":"."`) {
		t.Fatalf("expected raw repaired request, got %s", raw)
	}
}

func TestDelegatedChildFallbackSourceReadUsesDiscoveredEvidencePath(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod": "module example.test\n",
		"internal/runtime/agent/answer_composer.go": "package agent\n\nfunc ComposeDiscoveryAnswer() string { return \"ok\" }\n",
		"internal/runtime/agent/runner.go":          "package agent\n\nfunc Run() {}\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery paths","output_excerpt":"internal/runtime/agent/runner.go:1: package agent"}]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)

	if !ok {
		t.Fatal("expected fallback source read")
	}

	if req.Path != "internal/runtime/agent/runner.go" || result.Tool != "read_file" {
		t.Fatalf("expected fallback to read discovered evidence path, req=%#v result=%#v", req, result)
	}

	if !readFileResultHasConcreteSourceBody(result) {
		t.Fatalf("fallback result should contain source body, got %#v", result)
	}
}

func TestDelegatedChildFallbackSourceReadDoesNotUseQuestionClassWithoutDiscoveryPath(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod": "module example.test\n",
		"internal/runtime/agent/answer_composer.go": "package agent\n\nfunc ComposeDiscoveryAnswer() string { return \"ok\" }\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"dependency_graph","summary":"Collected graph","output_excerpt":"no file paths"}]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)

	if ok {
		t.Fatalf("expected no fallback read from question-class hardcoding, req=%#v result=%#v", req, result)
	}
}

func TestDelegatedChildFallbackSourceReadUsesPlanCandidateRanking(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":                           "module example.test\n",
		"internal/runtime/agent/errors.go": "package agent\n\ntype RunnerError string\n",
		"internal/runtime/agent/runner_child_tasks.go": "package agent\n\nfunc (r *Runner) runChildTask() {}\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"target_listing","summary":"Listed internal/runtime/agent.","output_excerpt":"internal/runtime/agent/errors.go\ninternal/runtime/agent/final_answer_acceptance.go"},` +
			`{"name":"text_anchor_search","summary":"Found delegated child orchestration implementation paths.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask()"}` +
			`]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "Explain how this harness uses subagents and child task orchestration.", results)

	if !ok {
		t.Fatal("expected fallback source read from objective-relevant discovery anchor")
	}

	if req.Path != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected plan-ranked child task source path, req=%#v result=%#v", req, result)
	}
}

func TestBuildRunnerNextActionStateUsesClosureConstraintsForSuccessfulPostChildSearch(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nSource-body evidence paths:\n- `internal/runtime/agent/runner.go`\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths:\n- `internal/runtime/agent/runner.go`\n- `internal/runtime/agent/answer_composer.go`\n",
		},
		{
			Tool:    "search_content",
			Summary: "Found 12 matching line(s) (showing first 12).",
			Output:  "internal/runtime/agent/answer_composer.go:73:synthesizeDelegatedReadOnlyAnswer\ninternal/runtime/agent/runner.go:648:runChildTask\n",
		},
	}
	state := buildRunnerNextActionState(
		"main",
		5,
		"tool_result",
		workspace.ToolRequest{Tool: "search_content", Pattern: "harness|sub.?agent"},
		results[1],
		results,
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)

	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure constraint, got %#v", state)
	}

	for _, forbidden := range []string{
		"edit_file if the target is already clear",
		"targeted verification evidence",
		"update task_ledger or answer with the concrete blocker",
	} {
		if slices.Contains(state.NextValidActions, forbidden) {
			t.Fatalf("expected closure-constrained next actions to omit %q, got %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestBuildRunnerNextActionStatePreservesDurableChildPacketClosureAfterSuccessfulOutline(t *testing.T) {
	state := buildRunnerNextActionState(
		"main",
		6,
		"tool_result",
		workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/runtime/agent/runner.go"},
		workspace.ToolResult{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/runtime/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		},
		[]workspace.ToolResult{{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/runtime/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		}},
		false,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:    "architecture",
			Objective:     "Explain how this harness uses subagents.",
			Constraints:   []string{"child_packet_available"},
			EvidencePaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		},
	)
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected durable child-packet closure constraint, got %#v", state)
	}

	for _, forbidden := range []string{
		"read-symbol capability with one outlined declaration name",
		"references or calls-into capability on a named declaration",
		"go_move_declarations",
	} {
		if slices.Contains(state.NextValidActions, forbidden) {
			t.Fatalf("expected durable closure state to omit %q, got %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestDelegatedReadOnlySecondaryTestReadRedirectsToImplementation(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how sub-agents are invoked and executed in this harness.\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_test.go", StartLine: 3140, EndLine: 3220},
	})

	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected secondary test evidence to redirect to implementation search, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected redirected search to target sibling implementation file, got %#v", decision.Request)
	}

	for _, want := range []string{"sub[-_ ]?agent", "invoke", "execute"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected redirected implementation search pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}

	for _, forbidden := range []string{"delegate", "task"} {
		if strings.Contains(decision.Request.Pattern, forbidden) {
			t.Fatalf("expected redirected implementation search pattern not to inject scenario terms %q, got %q", forbidden, decision.Request.Pattern)
		}
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

// Fix 7 (real subagent spawning via the task tool) — exercises
// docs/opencode-fixes-design.md §7. Verifies that the parent's `task` call
// launches an isolated child Runner, that the child actually drives the
// model loop (tool call plus answer), and that the child's intermediate
// messages do not pollute the parent's conversation.
func TestRunnerTaskToolSpawnsIsolatedReadOnlyChild(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "src/widget.go", "package widget\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"scout","mode":"read_only","prompt":"list .go files under src/"}`,
		`{"tool":"list_files","path":"src"}`,
		`{"tool":"read_file","path":"src/widget.go","start_line":1,"end_line":1}`,
		"The delegated probe read the Go source file under src and found that it declares the widget package, so the requested read-only scan located a concrete Go file.",
		"The scout reported that the delegated probe found a Go source file declaring the widget package.",
		"The scout reported that the delegated probe found a Go source file declaring the widget package.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "The scout reported that the delegated probe found a Go source file declaring the widget package."
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

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
	})

	var (
		childRequests, childResults int
		events                      []string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_gate" || strings.Contains(event.Role, "critic") || strings.Contains(event.Role, "readiness") {
				events = append(events, event.Role+": "+event.Content)
			}

			switch event.Role {
			case "child_tool_request":
				childRequests++
			case "child_tool_result":
				childResults++
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate a read-only probe then report"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v; events=%v", err, events)
	}

	if !strings.Contains(answer, "scout") || !strings.Contains(answer, "widget package") {
		t.Fatalf("unexpected parent answer: %q; events=%v", answer, events)
	}

	if childRequests == 0 || childResults == 0 {
		t.Fatalf("expected isolated child tool events to fire (requests=%d results=%d)", childRequests, childResults)
	}

	if requests < 5 || requests > 8 {
		t.Fatalf("expected parent spawn, child steps, and bounded parent final synthesis; got %d requests", requests)
	}
}

func TestRunnerRequiresChildTaskForBroadEvidenceReads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/runtime/agent: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		`{"tool":"search_files","pattern":"runner","path":"."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":3}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/runtime/agent/runner.go for performance evidence and report paths."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":3}`,
		"No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium.",
		"No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0

	var roles []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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
		rejected                  bool
		sawChildTaskResult        bool
		parentModelCallAfterChild bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}

			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Review performance risks in this workspace."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(event.Result.Summary, "Broad exploration after delegation nudge blocked") {
				rejected = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium." {
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}

	if !rejected {
		t.Fatal("expected broad exploration to be rejected after the delegation nudge")
	}

	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
	}

	if requests != len(replies) {
		t.Fatalf("expected parent to take one post-child decision turn, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerReturnsChildPacketBeforeBroadRediscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/runtime/agent: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		`{"tool":"search_files","pattern":"runner","path":"."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":3}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect performance evidence in internal/runtime/agent/runner.go and report paths."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":3}`,
		"No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium.",
		"No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0

	var roles []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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
		sawChildTaskResult        bool
		parentModelCallAfterChild bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}

			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Review performance risks in this workspace."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "No performance risk stands out in internal/runtime/agent/runner.go from the delegated evidence. Confidence: medium." {
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}

	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
	}

	if requests != len(replies) {
		t.Fatalf("expected parent to take one post-child decision turn, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerReturnsFirstReadOnlyChildPacketWithoutSecondDelegation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/runtime/agent: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/runtime/agent/runner.go and summarize."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":3}`,
		"internal/runtime/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium.",
		"internal/runtime/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium.",
	}
	requests := 0

	var roles []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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
		sawChildTaskResult        bool
		parentModelCallAfterChild bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}

			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how this harness delegates work."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "internal/runtime/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium." {
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}

	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
	}

	if requests != len(replies) {
		t.Fatalf("expected parent to decide after the child packet, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerDirectAnswerReturnsChildPacketForParentSynthesis(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "capabilities", "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "capabilities", "workspace", "tool_manifest.go"), []byte("package workspace\n\nfunc TaskToolManifestSummary() string {\n\treturn \"subagents are wired through task tool manifest entries for delegated child work\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write tool manifest: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/capabilities/workspace/tool_manifest.go and explain how subagents are wired."}`,
		`{"tool":"search_files","pattern":"*tool_manifest*"}`,
		`{"tool":"read_file","path":"internal/capabilities/workspace/tool_manifest.go","start_line":1,"end_line":20}`,
		"Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read internal/capabilities/workspace/tool_manifest.go lines 1-20\n- Found task tool manifest entries for delegated child work\n",
		"Subagents are wired through task tool manifest entries for delegated child work in internal/capabilities/workspace/tool_manifest.go. Confidence: medium.",
	}
	requests := 0

	var (
		roles        []string
		eventDetails []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v details=%v", requests, roles, eventDetails)
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
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			Objective:         "Explain how subagents work in this harness.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "read_file"},
		},
	}

	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
		switch event.Role {
		case "failure_feedback", "next_action_state", "readiness_check", "final_answer_critic", "control_rails_enforcement":
			eventDetails = append(eventDetails, event.Role+": "+event.Content)
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how subagents work in this harness."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lowerAnswer := strings.ToLower(answer)
	for _, want := range []string{
		"subagents are wired through",
		"task tool",
		"internal/capabilities/workspace/tool_manifest.go",
	} {
		if !strings.Contains(lowerAnswer, strings.ToLower(want)) {
			t.Fatalf("expected direct answer to include %q, got %q", want, answer)
		}
	}

	if strings.Contains(strings.ToLower(answer), "partial findings from this turn") {
		t.Fatalf("expected parent synthesis instead of raw child packet, got %q", answer)
	}

	if strings.Contains(answer, "f23 ") || strings.Contains(answer, "f1 ") {
		t.Fatalf("unexpected answer: %q", answer)
	}

	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}

	if requests < 4 || requests > len(replies) {
		t.Fatalf("expected parent model synthesis from child source evidence, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerArchitectureReturnsChildPacketForParentSynthesis(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner_child_tasks.go"), []byte("package agent\n\nfunc runChildTask() { _ = \"delegated child runners are created from task requests\" }\n"), 0o644); err != nil {
		t.Fatalf("write runner child tasks: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/runtime/agent/runner_child_tasks.go and explain how delegated child runners are created."}`,
		`{"tool":"search_files","pattern":"*runner_child_tasks*","path":"internal/runtime/agent"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner_child_tasks.go","start_line":1,"end_line":20}`,
		"Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read f23 internal/runtime/agent/runner_child_tasks.go lines 1-20\n- Found task child runner creation in internal/runtime/agent/runner_child_tasks.go\n",
		"Delegated child runners are created in internal/runtime/agent/runner_child_tasks.go and invoked from the parent runner loop. Confidence: medium.",
	}
	requests := 0

	var eventDetails []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; details=%v", requests, eventDetails)
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
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how delegated child runners are created in this harness.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "read_file"},
		},
	}

	var roles []string

	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
		switch event.Role {
		case "failure_feedback", "next_action_state", "readiness_check", "final_answer_critic", "control_rails_enforcement":
			eventDetails = append(eventDetails, event.Role+": "+event.Content)
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how delegated child runners are created in this harness."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(answer, "f23 ") {
		t.Fatalf("expected normalized packet in final answer, got %q", answer)
	}

	if strings.Contains(strings.ToLower(answer), "partial findings from this turn") {
		t.Fatalf("expected parent synthesis instead of raw child packet, got %q", answer)
	}

	if !strings.Contains(answer, "runner_child_tasks.go") {
		t.Fatalf("unexpected answer: %q", answer)
	}

	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}

	if requests < 4 || requests > len(replies) {
		t.Fatalf("expected parent model synthesis from delegated child source evidence, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerChildDiscoveryPacketLetsChildDecideFollowupAfterDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte("package agent\n\nfunc runChildTask() { _ = \"subagents task delegate\" }\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner_child_tasks.go"), []byte("package agent\n\nfunc childInstructions() { _ = \"subagents task delegate\" }\n"), 0o644); err != nil {
		t.Fatalf("write runner_child_tasks.go: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Explain how this harness uses subagents."}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","named_gap":"inspect subagent delegation source after discovery","why_not_answering":"need source-body evidence for the discovered delegation anchor"}`,
		"The harness uses subagents through the task tool flow anchored in `internal/runtime/agent/runner.go`; the source body shows the delegated child task handoff and the child packet names `internal/runtime/agent/runner_child_tasks.go`. Confidence: high.",
		"The harness uses subagents through the task tool, primarily in internal/runtime/agent/runner.go and internal/runtime/agent/runner_child_tasks.go. Confidence: high.",
		"The answer is grounded in `internal/runtime/agent/runner.go`. Use those findings as the source of truth for `Explain how this harness uses subagents?`.\n\nWhat this establishes:\n- The source body shows the child task delegation anchor.",
		"The answer is grounded in `internal/runtime/agent/runner.go`. Use those findings as the source of truth for `Explain how this harness uses subagents?`.\n\nWhat this establishes:\n- The source body shows the child task delegation anchor.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
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

	var roles []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "architecture",
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses subagents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_content", "read_file", "go_file_outline"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed"},
		},
	}

	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how this harness uses subagents."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, want := range []string{"subagents", "internal/runtime/agent/runner_child_tasks.go", "internal/runtime/agent/runner.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected model-directed read-only answer to contain %q, got %q", want, answer)
		}
	}

	if strings.HasPrefix(answer, "<<QUESTION>>") {
		t.Fatalf("expected model-directed read-only answer, got blocker %q", answer)
	}

	if strings.Contains(answer, "task tool") && !strings.Contains(answer, "internal/runtime/agent/runner.go") {
		t.Fatalf("unexpected answer: %q", answer)
	}

	for _, forbidden := range []string{`"layers_run"`, "Partial findings from this turn", "Discovery packet:", "Relevant source hits:", "Evidence used:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("unexpected answer with %q: %q", forbidden, answer)
		}
	}

	_ = roles

	if requests < 4 {
		t.Fatalf("expected parent to honor model follow-up after child packet, used only %d request(s)", requests)
	}

	if requests > len(replies) {
		t.Fatalf("parent used too many replies, used %d/%d", requests, len(replies))
	}
}

func TestDelegatedReadOnlyAnswerReadyAfterDiscoveryQuery(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Discovery query complete",
		Output:  `{"artifact_path":".codex/discovery/impact-analysis.json","evidence":[{"path":"internal/runtime/agent/runner.go","excerpt":"task child delegated_read_only"},{"path":"internal/runtime/agent/runner_child_tasks.go","excerpt":"child task runner"}]}`,
	}}
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Objective:         "Explain child discovery.",
			PermissionProfile: "read_only_workspace",
		},
	}

	// A lone discovery_query should NOT mark readiness — the model
	// must also read at least one source body before the harness
	// restricts further reads.
	ready := runner.delegatedReadOnlyAnswerReadyAfterResult(
		workspace.ToolRequest{Tool: "discovery_query"},
		results[0],
		results,
	)
	if ready {
		t.Fatalf("discovery_query alone should not mark readiness without a source body read")
	}

	// After discovery_query + two source body reads, readiness should fire.
	resultsWithRead := append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read file internal/runtime/agent/runner.go lines 1-200",
		Output:  "package agent\nfunc runChildTask() {}",
	}, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read file internal/runtime/agent/runner_child_tasks.go lines 1-100",
		Output:  "package agent\nfunc handleChildTask() {}",
	})

	ready = runner.delegatedReadOnlyAnswerReadyAfterResult(
		workspace.ToolRequest{Tool: "read_file"},
		resultsWithRead[2],
		resultsWithRead,
	)

	if !ready {
		t.Fatalf("discovery_query plus source body reads should mark readiness")
	}

	mutationRunner := *runner

	mutationRunner.RuntimeBrief.Contract = "implementation"

	if mutationRunner.delegatedReadOnlyAnswerReadyAfterResult(workspace.ToolRequest{Tool: "discovery_query"}, results[0], results) {
		t.Fatalf("did not expect answer-ready shortcut for mutation-required child")
	}

	failingResult := results[0]

	failingResult.Error = "discovery failed"

	if runner.delegatedReadOnlyAnswerReadyAfterResult(workspace.ToolRequest{Tool: "discovery_query"}, failingResult, []workspace.ToolResult{failingResult}) {
		t.Fatalf("did not expect answer-ready shortcut after failed discovery")
	}
}
