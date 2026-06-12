package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestCompletionGateRejectsRawToolResultTranscript(t *testing.T) {
	result, rejected := rawToolResultTranscriptCompletionRejection("tool: read_file\nsummary: Read internal/surfaces/tui/app/model_swap.go lines 70-91\nstatus: ok\noutput:\n70:\t\tm.openModelPopup()")
	if !rejected {
		t.Fatal("expected raw tool result transcript to be rejected")
	}

	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "internal tool-result transcript") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestRunnerRepairsRawToolResultTranscriptFinalAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/model_swap.go", "package app\n\nfunc modelAlias() string { return \"qwen\" }\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"internal/surfaces/tui/app/model_swap.go\",\"start_line\":1,\"end_line\":3}\n```",
		"tool: read_file\nsummary: Read internal/surfaces/tui/app/model_swap.go lines 1-3\nstatus: ok\noutput:\n1:package app\n2:\n3:func modelAlias() string { return \"qwen\" }",
		"The inspected code path is `internal/surfaces/tui/app/model_swap.go`; the current evidence shows the model alias helper there, but this read-only turn did not change files.",
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
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
			Objective:         "answer from the inspected model display code",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "Can we shorten this to the alias?"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "tool:") ||
		strings.Contains(strings.ToLower(answer), "\nsummary:") ||
		strings.Contains(strings.ToLower(answer), "\noutput:") {

		t.Fatalf("runner returned raw tool transcript:\n%s", answer)
	}

	if !strings.Contains(answer, "read-only turn did not change files") {
		t.Fatalf("expected repaired prose answer, got:\n%s", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected raw transcript rejection and repair retry, got %d request(s)", requests)
	}
}

func TestForcedCompletionRepairsToolRequestBeforeDeterministicEvidenceFallback(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner_child_tasks.go", "package agent\n\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents by delegating read-only exploration through the task pathway, then folding the child result back into the parent runner as evidence for the final answer."
	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner_child_tasks.go","named_gap":"body of runChildTask in internal/runtime/agent/runner_child_tasks.go","why_not_answering":"need one anchored read before answering"}`,
		`{"tool":"go_read_symbol","path":"internal/runtime/agent/runner_child_tasks.go","pattern":"Runner.runChildTask","named_gap":"read runChildTask before answering"}`,
		repaired,
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

	var modelCallEvents []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness utilize sub-agents?",
			PermissionProfile: "read_only_workspace",
			Constraints:       []string{"child_packet_available"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: how does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nSource-body evidence paths:\n- `internal/runtime/agent/runner_child_tasks.go`\n\nPrimary files: `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_child_tasks.go`.\n\nKey findings:\n- The parent runner uses sub-agents by delegating exploration to a child task and using the child output as evidence.",
		}},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness utilize sub-agents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(strings.ToLower(answer), "sub-agent") && !strings.Contains(strings.ToLower(answer), "subagent") {
		t.Fatalf("expected fallback/model synthesis to preserve the child-task finding, got:\n%s", answer)
	}

	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Primary files:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected cleaned deterministic prose instead of packet metadata %q:\n%s", forbidden, answer)
		}
	}

	if requests > 3 {
		t.Fatalf("expected deterministic fallback to avoid invalid-final retry, got %d requests", requests)
	}

	if len(modelCallEvents) == 0 {
		t.Fatal("expected model call telemetry for bounded fallback recovery")
	}
}

func TestForcedCompletionRepairsRepeatedToolRequestsWithFinalSynthesis(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner_child_tasks.go", "package agent\n\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents by launching a bounded read-only child runner through `internal/runtime/agent/runner_child_tasks.go`, then returning that child result to the parent as evidence for the final response."
	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner_child_tasks.go","named_gap":"body of runChildTask in internal/runtime/agent/runner_child_tasks.go","why_not_answering":"need one anchored read before answering"}`,
		`{"tool":"go_read_symbol","path":"internal/runtime/agent/runner_child_tasks.go","pattern":"Runner.runChildTask","named_gap":"read runChildTask before answering"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner_child_tasks.go","start_line":1,"end_line":80,"named_gap":"read runChildTask before answering","why_not_answering":"need another probe"}`,
		repaired,
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

	var modelCallEvents []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness utilize sub-agents?",
			PermissionProfile: "read_only_workspace",
			Constraints:       []string{"child_packet_available"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Source-body evidence paths:\n- `internal/runtime/agent/runner_child_tasks.go`\n\nThe strongest implementation evidence is in `internal/runtime/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents`.\n\nWhat this establishes:\n- Text anchors: Found sub-agent child runner implementation paths.",
		}},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness utilize sub-agents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "internal/runtime/agent/runner_child_tasks.go") {
		t.Fatalf("expected deterministic fallback to preserve the relevant implementation surface, got:\n%s", answer)
	}

	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Primary files:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected cleaned fallback prose instead of internal evidence framing %q:\n%s", forbidden, answer)
		}
	}

	if requests > 2 {
		t.Fatalf("expected deterministic source-backed fallback to avoid invalid-final retry, got %d requests", requests)
	}

	if slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"invalid_final_answer_retry"`) }) {
		t.Fatalf("did not expect invalid_final_answer_retry when source-backed fallback is available, got %#v", modelCallEvents)
	}
}

func TestForcedCompletionModelOwnedAnswerForReadOnlyArchitecture(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner_child_tasks.go", "package agent\n\nfunc seed() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents through `Runner.runChildTask` in `internal/runtime/agent/runner_child_tasks.go`. That path normalizes the delegated prompt, creates a child `Runner` with a bounded read-only policy and child-scoped permissions, runs the child task, and then returns the child answer/results to the parent as grounded evidence for the final response."
	replies := []string{
		repaired,
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

	var modelCallEvents []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness make use of sub-agents?",
			PermissionProfile: "read_only_workspace",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "discovery_query",
				Summary: "Ran architecture_boundary discovery query with 4 layer(s)",
				Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found child-runner implementation paths.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
			},
			{
				Tool:    "go_read_symbol",
				Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
				Output:  "func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n\tprompt = normalizeReadOnlyChildPrompt(prompt)\n\tprompt = r.augmentReadOnlyChildPrompt(prompt)\n\tchild := &Runner{\n\t\tClient: r.Client,\n\t\tWorkspace: r.Workspace,\n\t\tPolicy: childPolicy,\n\t\tAgentName: \"child:\" + agentName,\n\t\tPermissionPolicy: readOnlyChildTaskPermissionPolicy(),\n\t\tDisableTaskTool: true,\n\t}\n\tanswer, err := child.Run(ctx, []llama.ChatMessage{{Role: \"user\", Content: prompt}}, opts, nil, nil, nil)\n\tanswer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)\n\treturn workspace.ToolResult{Tool: \"task\", Summary: \"Child task completed\", Output: strings.TrimSpace(answer)}\n}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness make use of sub-agents?"}},
		llama.ChatOptions{},
		runner.SeedResults,
		"Post-readiness named-gap contract is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	for _, want := range []string{
		"`Runner.runChildTask`",
		"`internal/runtime/agent/runner_child_tasks.go`",
		"normalizes the delegated prompt",
		"child `Runner`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected source-backed synthesis to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"Source: delegated read-only evidence packet", "Inspected paths:", "The strongest implementation evidence is in", "What this establishes:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected answer without scaffold or delegated packet metadata %q:\n%s", forbidden, answer)
		}
	}

	if slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"fallback_final_answer_retry"`) }) {
		t.Fatalf("did not expect fallback_final_answer_retry for acceptable generic evidence synthesis, got %#v", modelCallEvents)
	}

	if requests == 0 {
		t.Fatalf("expected model-owned forced completion, not deterministic host fallback")
	}
}

func TestDeterministicNoToolsAnswerDoesNotKeepGroundedReadOnlyAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain what ShortLabel returns from internal/labels/labels.go.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/labels/labels.go lines 1-4",
		Output: strings.Join([]string{
			"package labels",
			"",
			"func ShortLabel() string {",
			"\treturn \"Save changes\"",
			"}",
		}, "\n"),
	}}
	answer, ok := deterministicNoToolsAnswer("main", brief, results)

	if ok {
		t.Fatalf("grounded read-only evidence must remain model-owned, got host answer:\n%s", answer)
	}
}

func TestPresentableFallbackCandidatePreservesChildDelegatedPacket(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "delegated_read_only",
		Contract:          "grounded_evidence",
		Objective:         "Explain how this harness uses child agents.",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 1-80",
		Output:  "package agent\nfunc runChildTask() {}\n",
	}}
	childAnswer := "The answer is grounded in `internal/runtime/agent/runner_child_tasks.go`.\n\nWhat this establishes:\n- The child runner returns a delegated evidence packet to the parent after bounded read-only inspection."

	answer, ok := presentableFallbackCandidate("child:explorer", brief, results, childAnswer)
	if !ok {
		t.Fatal("expected child delegated fallback packet to remain presentable")
	}

	if answer != childAnswer {
		t.Fatalf("expected child delegated fallback packet to be preserved, got:\n%s", answer)
	}

	for _, forbidden := range []string{"not enough supported evidence", "<<QUESTION>>", "concrete user-facing finding"} {
		if strings.Contains(strings.ToLower(answer), strings.ToLower(forbidden)) {
			t.Fatalf("child delegated fallback should not be rewritten as main-lane uncertainty %q:\n%s", forbidden, answer)
		}
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryStillViolatesCompletionGate(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":200}`,
		"The file `internal/labels/labels.go` contains `ShortLabel()` returning `\"Save changes\"`.\n\nTo determine whether it can be shortened, I need one more probe:\n\n{\"tool\":\"search_content\",\"pattern\":\"ShortLabel\",\"path\":\".\"}",
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Can we shorten the save label in internal/labels/labels.go?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can we shorten the save label in internal/labels/labels.go?"}},
		llama.ChatOptions{},
		results,
		"The conversation lane has enough bounded evidence. Tools are now disabled. Answer the user's actual question now.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if answer == replies[1] || strings.Contains(answer, `"tool"`) || strings.Contains(answer, "I need one more probe") {
		t.Fatalf("retry answer with an unexecuted tool request should not be accepted:\n%s", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected forced-completion retry before fallback, used %d/%d replies", requests, len(replies))
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryReturnsSourceInventory(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	badInventoryAnswer := "For `" + objective + "`, `internal/surfaces/tui/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`."
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": badInventoryAnswer},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/surfaces/tui/app/app.go lines 1-57 (total lines: 57)",
		Output: strings.Join([]string{
			"1:package app",
			"17:func truncateText(s string, max int) string {",
			"18:\tif len(s) <= max {",
			"19:\t\treturn s",
			"20:\t}",
			"21:\treturn s[:max]",
			"42:func normalizeHistoryRole(value string) string {",
			"43:\tif value == \"\" {",
			"44:\t\treturn \"User\"",
			"45:\t}",
			"46:\treturn strings.ToUpper(value[:1]) + value[1:]",
			"47:}",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"Repeated post-readiness follow-up requests omitted the exact missing claim. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if answer == badInventoryAnswer || strings.Contains(answer, "shows defines") {
		t.Fatalf("source-inventory retry answer escaped forced completion:\n%s", answer)
	}

	if strings.Contains(answer, "normalizeHistoryRole` returns") {
		t.Fatalf("deterministic fallback reintroduced helper-function inventory:\n%s", answer)
	}

	if strings.Contains(answer, "internal/surfaces/tui/app/app.go") && strings.Contains(answer, "normalizeHistoryRole") {
		t.Fatalf("forced-completion fallback should not synthesize source inventory, got:\n%s", answer)
	}

	if requests != 1 {
		t.Fatalf("expected one forced-completion model call, not deterministic host fallback; got %d", requests)
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryReturnsInspectedBodyInventory(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	badInventoryAnswer := "`Model.handleKeyPress` in `internal/surfaces/tui/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`."
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": badInventoryAnswer},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-141",
			"",
			"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
			"44:\t\tcase tea.KeyUp:",
			"49:\t\tcase tea.KeyDown:",
			"69:\t\tcase tea.KeyUp:",
			"76:\t\tcase tea.KeyDown:",
			"83:\t\tcase tea.KeyEnter:",
			"151:\t\tm.appendHistoryMessage(\"user\", text, nil)",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"Repeated post-readiness follow-up requests omitted the exact missing claim. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if answer == badInventoryAnswer || strings.Contains(answer, "inspected body shows") {
		t.Fatalf("inspected-body inventory retry answer escaped forced completion:\n%s", answer)
	}

	if strings.Contains(answer, "internal/surfaces/tui/app/update_handlers.go") && strings.Contains(answer, "appendHistoryMessage") {
		t.Fatalf("forced-completion fallback should not synthesize source inventory, got:\n%s", answer)
	}

	if requests != 1 {
		t.Fatalf("expected one forced-completion model call, not deterministic host fallback; got %d", requests)
	}
}

func TestForcedCompletionRepairReturnDoesNotLeakSourceInventory(t *testing.T) {
	objective := "Can we enable up and down arrows to recall old user messages in the TUI?"
	replies := []string{
		"Question: Can we enable up and down arrows to recall old user messages in the TUI?\n\nThe inspected TUI key handler in `internal/surfaces/tui/app/update_handlers.go` already handles `tea.KeyUp` and `tea.KeyDown`, so the implementation needs to route those keys through user-message history recall when the input is focused.",
		"`Model.handleKeyPress` in `internal/surfaces/tui/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, calls `renderHistoryPanelContent`, and handles `tea.KeyUp` plus `tea.KeyDown`.",
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-141",
			"",
			"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
			"44:\t\tcase tea.KeyUp:",
			"49:\t\tcase tea.KeyDown:",
			"83:\t\tcase tea.KeyEnter:",
			"151:\t\tm.appendHistoryMessage(\"user\", text, nil)",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"The conversation lane has enough bounded evidence. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if strings.Contains(answer, "clearest inspected implementation anchor") || strings.Contains(answer, "inspected body shows") {
		t.Fatalf("repair-path source inventory escaped forced completion:\n%s", answer)
	}

	if !strings.Contains(answer, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("fallback should retain inspected evidence path, got:\n%s", answer)
	}

	if requests != 2 {
		t.Fatalf("expected decoration repair path to use both model replies, used %d", requests)
	}
}

func TestDeterministicNoToolsAnswerRejectsCodeNavigateInventoryFallback(t *testing.T) {
	objective := "How can we enable the up and down arrow keys to iterate between previously sent user messages so they can be resent or edited before sending?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		Objective:         objective,
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"code_navigate", "context_query"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.LoadHistoryFromSession in internal/surfaces/tui/app/sessions.go lines 1-20",
			"",
			"func (m *Model) LoadHistoryFromSession(path string) error {",
			"\tif m == nil {",
			"\t\treturn fmt.Errorf(\"model is nil\")",
			"\t}",
			"\tpath = strings.TrimSpace(path)",
			"\tif path == \"\" {",
			"\t\treturn fmt.Errorf(\"session path is required\")",
			"\t}",
			"\treturn replaySessionLog(m, path)",
			"}",
		}, "\n"),
	}}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if ok {
		t.Fatalf("code-navigation read-only evidence must not produce deterministic host answer:\n%s", answer)
	}
}

func TestForcedCompletionRequestForcesVisibleTextOnlyOutput(t *testing.T) {
	thinkingOn := true

	var sawRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools              []any `json:"tools,omitempty"`
			ToolChoice         any   `json:"tool_choice,omitempty"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		sawRequest = true

		if len(body.Tools) != 0 || body.ToolChoice != nil {
			t.Fatalf("forced completion should be text-only, got tools=%#v tool_choice=%#v", body.Tools, body.ToolChoice)
		}

		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {

			t.Fatalf("forced completion should disable hidden thinking, got %#v", body.ChatTemplateKwargs)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "The evidence shows the answer."},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Answer from gathered evidence.",
			PermissionProfile: "read_only_workspace",
		},
	}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Answer from gathered evidence."}},
		llama.ChatOptions{
			Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
			ToolChoice:       "auto",
			EnableThinking:   &thinkingOn,
			PreserveThinking: true,
			MaxTokens:        8192,
		},
		nil,
		"Tools are now disabled. Answer from gathered evidence.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}

	if !sawRequest {
		t.Fatal("expected forced-completion model request")
	}

	if !strings.Contains(answer, "evidence") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
}

func TestReadOnlyEvidenceAvailableMainLoopForcesVisibleOutputButKeepsTools(t *testing.T) {
	thinkingOn := true

	var sawRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools              []any `json:"tools,omitempty"`
			ToolChoice         any   `json:"tool_choice,omitempty"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		sawRequest = true

		if len(body.Tools) == 0 {
			t.Fatalf("evidence-available terminal mode should keep read/search tools available")
		}

		if body.ToolChoice == nil {
			t.Fatalf("evidence-available terminal mode should keep tool choice available")
		}

		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {

			t.Fatalf("evidence-available terminal mode should force visible output, got %#v", body.ChatTemplateKwargs)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "runner.go contains package agent."},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What is in internal/runtime/agent/runner.go?",
			AllowedTools:      []string{"read_file", "search_content"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner.go lines 1-20",
			Output:  "1:package agent\n2:\n3:func RunExample() {}\n",
		}},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is in internal/runtime/agent/runner.go?"}},
		llama.ChatOptions{
			Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
			ToolChoice:       "auto",
			EnableThinking:   &thinkingOn,
			PreserveThinking: true,
			MaxTokens:        8192,
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !sawRequest {
		t.Fatal("expected main-loop model request")
	}

	if !strings.Contains(answer, "runner.go") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
}

func TestEmptyAnswerRetryForcesVisibleOutputAndStripsReasoning(t *testing.T) {
	thinkingOn := true

	var sawRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content,omitempty"`
			} `json:"messages"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		sawRequest = true

		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {

			t.Fatalf("empty-answer retry should disable hidden thinking, got %#v", body.ChatTemplateKwargs)
		}

		for _, message := range body.Messages {
			if strings.Contains(message.ReasoningContent, "hidden scratchpad") ||
				strings.Contains(message.Content, "hidden scratchpad") {

				t.Fatalf("empty-answer retry leaked prior hidden reasoning in request: %#v", body.Messages)
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Visible retry answer from evidence."},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Answer from gathered evidence.",
			PermissionProfile: "read_only_workspace",
		},
	}

	answer, err := runner.ensureNonEmptyAnswer(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Answer from gathered evidence."}},
		llama.ChatMessage{Role: "assistant", ReasoningContent: "hidden scratchpad that must not be resent"},
		llama.ChatOptions{EnableThinking: &thinkingOn, PreserveThinking: true, MaxTokens: 8192},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ensureNonEmptyAnswer: %v", err)
	}

	if !sawRequest {
		t.Fatal("expected empty-answer retry request")
	}

	if !strings.Contains(answer, "Visible retry answer") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
}

func TestBudgetClosureRunEmitsAcceptanceSignals(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "line one\nline two\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":2}`,
		"README.md currently has two lines and no sub-agent behavior in it.",
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

	var roles []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "What is in README.md?",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is in README.md?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "two lines") && !(strings.Contains(answer, "line one") && strings.Contains(answer, "line two")) {
		t.Fatalf("expected forced completion answer, got %q", answer)
	}

	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}

	if requests != len(replies) {
		t.Fatalf("expected one tool turn plus one budget-closure answer, used %d/%d replies", requests, len(replies))
	}
}
