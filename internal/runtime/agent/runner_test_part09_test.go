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
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestHiddenOnlyImplementationTurnAutoReadsUnreadRequiredPath(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []map[string]string{
		{"content": `{"tool":"read_file","path":"a.txt"}`},
		{"reasoning_content": `I need the current contents of b.txt before editing it.`},
		{"content": `{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`},
		{"content": `{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`},
		{"content": "Done. Updated both required files."},
	}

	var (
		requests      int
		autoReadEvent string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": reply,
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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_recovery_auto_required_read" {
				autoReadEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected hidden-only turn to auto-read without an extra model retry, got %d request(s)", requests)
	}

	if !strings.Contains(autoReadEvent, "b.txt") {
		t.Fatalf("expected auto required read event for b.txt, got %q", autoReadEvent)
	}

	if !strings.Contains(answer, "Done") {
		t.Fatalf("unexpected answer: %s", answer)
	}

	for path, want := range map[string]string{"a.txt": "after a\n", "b.txt": "after b\n"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, string(got), want)
		}
	}
}

func TestImplementationRequiredPathRecoveryFollowsGenericCodeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicImplementationRequiredPathRead([]workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
		Output:  "1:package main",
	}})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("generic code header read should deterministically advance to file_outline, got %#v ok=%v", req, ok)
	}

	if strings.Contains(req.NamedGap, "plan-execute") || strings.Contains(req.WhyNotAnswering, "submit_plan") {
		t.Fatalf("direct implementation follow-up must not leak plan-execute wording: %#v", req)
	}

	if !strings.Contains(req.NamedGap, "required implementation edit path") {
		t.Fatalf("direct implementation follow-up should name implementation source evidence, got %#v", req)
	}

	req, _, ok = runner.deterministicImplementationRequiredPathRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
			Output:  "1:package main",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductTDD lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Pattern != "runProductTDD" {
		t.Fatalf("outline should deterministically advance to best matching source-body symbol, got %#v ok=%v", req, ok)
	}

	if strings.Contains(req.NamedGap, "plan-execute") || strings.Contains(req.WhyNotAnswering, "submit_plan") {
		t.Fatalf("direct implementation symbol follow-up must not leak plan-execute wording: %#v", req)
	}
}

func TestImplementationRequiredPathRecoveryAutoReadsAfterNonToolProse(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("I need to inspect the function before editing.", []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
		Output:  "1:package main",
	}})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" {
		t.Fatalf("non-tool prose while anchors are missing should advance bounded source evidence, got %#v ok=%v", req, ok)
	}

	req, _, ok = runner.deterministicImplementationRejectedRequiredPathRead("I found the coverage gate surface and will update the product workflow.", []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-338 (total lines: 338)",
		Output:  "1:package productworkflow",
	}})
	if !ok || req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("ordinary rejected non-tool prose should advance to next unread source required path, got %#v ok=%v", req, ok)
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("<<QUESTION>>: cmd/idiom/product_workflow.go has a stale anchor.", nil); ok {
		t.Fatal("path-specific blocker answers should not be converted into deterministic reads")
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("Blocked: I cannot safely edit without the generated fixture.", nil); ok {
		t.Fatal("concrete blocker answers should not be converted into deterministic reads")
	}
}

func TestImplementationRequiredPathRecoveryDoesNotAutoReadNonSourceDeliverables(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Update the release notes.",
			RequiredEditPaths: []string{"docs/release-notes.md"},
		},
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("I updated the release-note content.", nil); ok {
		t.Fatal("non-source deliverable prose should remain available to markdown/blocker handling instead of forcing a source read")
	}
}

func TestImplementationRequiredPathRecoveryPromptSuggestsNextSourceBodyRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt(
		[]string{"cmd/idiom/product_workflow.go"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
			Output:  "1:package main",
		}},
		false,
	)
	if !strings.Contains(prompt, `"tool":"code_navigate"`) || !strings.Contains(prompt, `"capability":"file_outline"`) {
		t.Fatalf("prompt should suggest the next source-body evidence request instead of repeating the header read:\n%s", prompt)
	}

	if strings.Contains(prompt, `"tool":"read_file","path":"cmd/idiom/product_workflow.go","start_line":1,"end_line":220`) {
		t.Fatalf("prompt should not repeat the already-insufficient header read:\n%s", prompt)
	}

	if strings.Contains(prompt, "submit_plan") || strings.Contains(prompt, "plan-execute") {
		t.Fatalf("implementation recovery prompt should not leak plan-execute wording:\n%s", prompt)
	}
}

func TestImplementationRequiredPathRecoveryPromptShowsTextualEditShapeWhenAnchorsReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n"); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt(
		[]string{"a.go"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read a.go lines 1-1 (total lines: 1)",
			Output:  "1:package main",
		}},
		false,
	)
	for _, want := range []string{"`edit_file`", "real required path", "Do not copy placeholders"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("anchor-ready textual recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, `"tool":"read_file"`) {
		t.Fatalf("anchor-ready recovery prompt should not suggest another read:\n%s", prompt)
	}
}

func TestTextualRequiredPathRecoveryForcesVisibleActionAfterAnchorRead(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")

	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":20,"named_gap":"current text before edit","why_not_answering":"need source evidence"}`,
		"<<QUESTION>>: a.txt cannot be edited safely from the available evidence.",
	}

	var (
		requests                      int
		secondRequestDisablesThinking bool
		visibleModeEvent              string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		var body struct {
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
				ThinkingBudget int   `json:"thinking_budget"`
			} `json:"chat_template_kwargs,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if requests == 1 {
			secondRequestDisablesThinking = body.ChatTemplateKwargs != nil &&
				body.ChatTemplateKwargs.EnableThinking != nil &&
				!*body.ChatTemplateKwargs.EnableThinking &&
				!body.ChatTemplateKwargs.Preserve
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
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
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
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "required_edit_paths_visible_write_mode" {
				visibleModeEvent = event.Content
			}
		},
	}

	_, err := runner.Run(
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

	if !secondRequestDisablesThinking {
		t.Fatal("second textual request should disable hidden thinking after required-path anchor evidence is ready")
	}

	if !strings.Contains(visibleModeEvent, "disable hidden thinking") {
		t.Fatalf("expected visible write mode event, got %q", visibleModeEvent)
	}
}

func TestVisibleToolRequestProseSalvageExecutesExplicitToolObject(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`I will read the required file now: {"tool":"read_file","path":"a.txt","start_line":1,"end_line":20,"named_gap":"current text before edit","why_not_answering":"need source evidence"}`,
		"<<QUESTION>>: a.txt cannot be edited safely from the available evidence.",
	}

	var (
		requests     int
		salvageEvent string
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
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
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
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}

	_, err := runner.Run(
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

	if requests != len(replies) {
		t.Fatalf("expected salvaged read plus final answer, got %d request(s)", requests)
	}

	if !strings.Contains(salvageEvent, "read_file") {
		t.Fatalf("expected visible tool salvage event for read_file, got %q", salvageEvent)
	}
}

func TestVisibleSubmitPlanProseSalvageAllowedAtTerminalReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
	}
	reply := strings.Join([]string{
		"I have enough evidence now, so I will submit the complete plan as the required visible tool request. The prose here should not make the harness drop the structured object when it is otherwise valid.",
		`{"tool":"submit_plan","rationale":"Update both required files.","edits":[{"path":"a.go","old_text":"package a","new_text":"package a\n// updated","purpose":"update a"},{"path":"b.go","old_text":"package b","new_text":"package b\n// updated","purpose":"update b"}]}`,
	}, "\n")

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected visible submit_plan JSON embedded in prose to be salvaged at terminal readiness")
	}

	if req.Tool != "submit_plan" || !strings.Contains(raw, `"edits"`) || len(req.Edits) != 2 {
		t.Fatalf("unexpected salvaged submit_plan: req=%#v raw=%s", req, raw)
	}

	if hiddenReq, _, hidden := runner.salvageToolRequestFromReasoning(llama.ChatMessage{ReasoningContent: reply}, results); hidden {
		t.Fatalf("hidden reasoning must not salvage submit_plan, got %#v", hiddenReq)
	}
}

func TestVisibleSubmitPlanMissingToolSalvageAllowedForOpenCandidateSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-20 (total lines: 200)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	reply := `{"rationale":"Reuse canonical product authority validation in the coverage gate.","edits":[{"path":"internal/workflows/productworkflow/coverage_gate.go","old_text":"func EvaluateCoverageGate() {}","new_text":"func EvaluateCoverageGate() { validateProductAuthority() }","purpose":"call the canonical authority validation before returning coverage results"}]}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected missing-tool submit_plan JSON to be salvaged after open candidate source evidence")
	}

	if req.Tool != "submit_plan" || len(req.Edits) != 1 || req.Edits[0].Path != "internal/workflows/productworkflow/coverage_gate.go" {
		t.Fatalf("unexpected salvaged submit_plan: req=%#v raw=%s", req, raw)
	}

	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("salvaged raw request must include inferred submit_plan tool, got %s", raw)
	}
}

func TestVisibleSubmitPlanProseSalvageRejectsSchemaExamplePlaceholders(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-20 (total lines: 200)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	reply := `{"tool":"submit_plan","purpose":"submit a structured executable edit plan (plan-then-execute mode only).","request":"{\"tool\":\"submit_plan\",\"rationale\":\"one paragraph on the overall plan\",\"edits\":[{\"path\":\"relative/file\",\"old_text\":\"exact current substring\",\"new_text\":\"complete replacement text\",\"purpose\":\"why this edit\"}]}"}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("schema/example submit_plan must not be salvaged, req=%#v raw=%s", req, raw)
	}
}

func TestVisibleSubmitPlanMissingToolSalvageRequiresSourceEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	reply := `{"rationale":"Update target.","edits":[{"path":"target.go","old_text":"old","new_text":"new","purpose":"update target"}]}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, nil); ok {
		t.Fatalf("missing-tool submit_plan salvage must require source evidence, req=%#v raw=%s", req, raw)
	}
}

func TestVisibleSubmitPlanTerminalBypassDoesNotWidenNonSubmitSalvage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-20 (total lines: 20)",
		Output:  "1:package a",
	}}
	reply := `Here is context, but this is not a tool-action response: {"tool":"read_file","path":"a.go","start_line":1,"end_line":20}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("terminal submit-plan bypass must not salvage non-submit prose JSON, req=%#v raw=%s", req, raw)
	}
}

func TestRequiredPathRecoveryForExistingSourceFilesAdvertisesBoundedWritesOnly(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n"); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	if err := writeTestFile(root, "b_test.go", "package main\n"); err != nil {
		t.Fatalf("write b_test.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b_test.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt([]string{"a.go", "b_test.go"}, nil, false)
	for _, want := range []string{"existing source/test edits", "`edit_file` with `old_text`+`new_text`", "`start_line`+`end_line`+`content`", "`apply_patch`", "one coherent multi-file patch"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected bounded recovery prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{"call one write capability now for a remaining required path: `edit_file`, `write_file`, `append_file`, or `apply_patch`", "Use real current text, complete replacement text, or a valid patch"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("bounded source recovery prompt should not advertise broad write surface %q:\n%s", forbidden, prompt)
		}
	}

	if !strings.Contains(prompt, "Do not use `write_file` or `append_file` for existing source/test edits") {
		t.Fatalf("expected explicit write_file/append_file prohibition, got:\n%s", prompt)
	}
}

func TestRequiredPathRecoveryKeepsBroadWritesForDocDeliverables(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "notes.md", "# Notes\n"); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"notes.md"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt([]string{"notes.md"}, nil, false)
	if !strings.Contains(prompt, "`edit_file`, `write_file`, `append_file`, or `apply_patch`") {
		t.Fatalf("doc deliverable recovery should keep broad write options, got:\n%s", prompt)
	}
}

func TestRequiredPathCompletionRecoveryAcceptsPathSpecificBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	pathBlocker := "<<QUESTION>>: Blocker for b.txt: cannot safely edit because the provided anchor is stale."
	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`,
		"Done.",
		pathBlocker,
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

	var (
		completion            runnerCompletionStatus
		repeatedBlockerStatus bool
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
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "completion_status" {
				return
			}

			if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
				t.Fatalf("decode completion status: %v", err)
			}

			if completion.BlockerKind == "completion_gate_required_paths_remaining_repeated" {
				repeatedBlockerStatus = true
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

	if answer != pathBlocker {
		t.Fatalf("expected path-specific blocker answer to be accepted, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected one recovery retry, got %d request(s)", requests)
	}

	if repeatedBlockerStatus {
		t.Fatalf("path-specific blocker should not be replaced by repeated completion-gate blocker")
	}

	if completion.BlockerKind != "required_edit_paths_remaining" || !slices.Contains(completion.ChangedPaths, "a.txt") {
		t.Fatalf("expected accepted blocker status for remaining path, got %#v", completion)
	}
}

func TestDecorateFinalAnswerCanonicalizesMissingRequiredPathSuccessClaim(t *testing.T) {
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Implemented the requested changes in a.txt and b.txt.",
		StopReason: "model_final",
		UserFacing: true,
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		Results: []workspace.ToolResult{
			{Tool: "write_file", Summary: "Wrote a.txt"},
		},
	})

	for _, want := range []string{"<<QUESTION>>:", "b.txt", "Changed path recorded: a.txt"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected canonical missing-path blocker to contain %q, got %q", want, answer)
		}
	}

	if strings.Contains(answer, "Implemented the requested changes") {
		t.Fatalf("misleading success claim should be replaced, got %q", answer)
	}
}

func TestDecorateFinalAnswerPreservesConcreteMissingPathBlocker(t *testing.T) {
	runner := &Runner{}
	blocker := "Blocked: b.txt cannot be edited safely because the current anchor is stale."
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     blocker,
		StopReason: "model_final",
		UserFacing: true,
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		Results: []workspace.ToolResult{
			{Tool: "write_file", Summary: "Wrote a.txt"},
		},
	})

	if answer != blocker {
		t.Fatalf("expected concrete blocker to be preserved, got %q", answer)
	}
}

func TestPlannerCompletionGateResolvesPlanCoverageAfterSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)

	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}

	recorder.emitContractStarted(brief)

	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry executor workspace write obligations: %#v", recorder.obligations)
	}

	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:         autonomy.GateCompletion,
		Decision:     autonomy.PolicyAllow,
		ReasonCode:   "plan_submitted_short_circuit",
		ChangedPaths: nil,
	}, refs)

	coverageIDs := plannerCoverageObligationsByPathForTest(t, recorder)
	for _, path := range brief.RequiredEditPaths {
		id := coverageIDs[path]
		if id == "" {
			t.Fatalf("missing planner coverage obligation for %s in %#v", path, recorder.obligations)
		}

		if !slices.Contains(resolved, id) {
			t.Fatalf("planner submit_plan should resolve plan coverage obligation %s; resolved=%#v open=%#v", id, resolved, open)
		}

		if slices.Contains(open, id) {
			t.Fatalf("planner submit_plan should not leave plan coverage obligation %s open; resolved=%#v open=%#v", id, resolved, open)
		}
	}

	finalAnswerID := obligationIDByKindForTest(t, recorder, autonomy.ObligationFinalAnswer)
	if !slices.Contains(resolved, finalAnswerID) {
		t.Fatalf("planner short-circuit should still resolve final-answer/plan artifact closeout, resolved=%#v", resolved)
	}
}

func TestPlannerCompletionGateResolvesSourceSatisfiedRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)

	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}

	recorder.emitContractStarted(brief)

	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry executor workspace write obligations: %#v", recorder.obligations)
	}

	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:       autonomy.GateCompletion,
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "plan_source_satisfied_short_circuit",
		TargetRefs: []string{"a.txt:pending", "b.txt:pending"},
	}, refs)

	coverageIDs := plannerCoverageObligationsByPathForTest(t, recorder)
	for _, path := range brief.RequiredEditPaths {
		id := coverageIDs[path]
		if id == "" {
			t.Fatalf("missing planner coverage obligation for %s in %#v", path, recorder.obligations)
		}

		if !slices.Contains(resolved, id) {
			t.Fatalf("source-satisfied closeout should resolve plan coverage obligation %s; resolved=%#v open=%#v", id, resolved, open)
		}

		if slices.Contains(open, id) {
			t.Fatalf("source-satisfied closeout should not leave plan coverage obligation %s open; resolved=%#v open=%#v", id, resolved, open)
		}
	}
}

func TestPlannerCompletionGateResolvesUnscopedSourceSatisfiedFinalOutcome(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)

	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}

	recorder.emitContractStarted(brief)

	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry generic executor write obligation: %#v", recorder.obligations)
	}

	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:       autonomy.GateCompletion,
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "plan_source_satisfied_short_circuit",
	}, refs)

	finalAnswerID := obligationIDByKindForTest(t, recorder, autonomy.ObligationFinalAnswer)
	if !slices.Contains(resolved, finalAnswerID) {
		t.Fatalf("unscoped source-satisfied closeout should resolve planner final outcome obligation; resolved=%#v open=%#v", resolved, open)
	}

	if slices.Contains(open, finalAnswerID) {
		t.Fatalf("unscoped source-satisfied closeout should not leave planner final outcome obligation open; resolved=%#v open=%#v", resolved, open)
	}
}

func plannerCoverageObligationsByPathForTest(t *testing.T, recorder *runnerAutonomyRecorder) map[string]string {
	t.Helper()

	ids := make(map[string]string)

	for _, obligation := range recorder.obligations {
		if obligation.Kind != autonomy.ObligationEvidence || obligation.RequiredBy != "submit_plan_required_edit_coverage" {
			continue
		}

		ids[obligation.TargetRef] = obligation.ObligationID
	}

	return ids
}

func obligationIDByKindForTest(t *testing.T, recorder *runnerAutonomyRecorder, kind autonomy.ObligationKind) string {
	t.Helper()

	for _, obligation := range recorder.obligations {
		if obligation.Kind == kind {
			return obligation.ObligationID
		}
	}

	t.Fatalf("missing obligation kind %q in %#v", kind, recorder.obligations)

	return ""
}

func TestMutationFallbackAnswerReportsChangedPathsAndVerification(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md", Output: "hello\n"},
		{Tool: "edit_file", Summary: "Edited README.md"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- README.md\n"},
	}

	answer, ok := synthesizeNoToolsFallbackAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected mutation fallback answer")
	}

	if strings.Contains(answer, "Partial findings from this turn") {
		t.Fatalf("mutation fallback should not use read-only partial-findings framing, got:\n%s", answer)
	}

	for _, want := range []string{
		"Changed paths:",
		"- README.md",
		"Verification: git diff --check -- README.md passed",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected mutation fallback to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestMutationFallbackAnswerDropsStaleHarnessBlockerAfterLaterVerification(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited README.md"},
		{
			Tool:    "read_file",
			Summary: "Verification required after workspace edits",
			Error:   "the workspace has 2 successful write(s) since the last verification.",
		},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- README.md\n"},
	}

	answer, ok := synthesizeNoToolsFallbackAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected mutation fallback answer")
	}

	if strings.Contains(answer, "Blocker:") ||
		strings.Contains(answer, "Verification required after workspace edits") {

		t.Fatalf("stale harness rejection should not survive later verification, got:\n%s", answer)
	}

	if !strings.Contains(answer, "Verification: git diff --check -- README.md passed") {
		t.Fatalf("expected later successful verification, got:\n%s", answer)
	}
}

func TestPrematureTargetReadRejectionRequiresConcreteNonTargetInspection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"}

	broadEvidence := []workspace.ToolResult{
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols with gopls", Output: "internal/surfaces/tui/app/prompt_routing.go:10 func buildPromptExecutionPlan"},
	}
	if _, _, reject := prematureTargetReadRejection(req, broadEvidence, brief); !reject {
		t.Fatal("expected broad workspace-symbol evidence to be insufficient for a large target-file read")
	}

	concreteEvidence := []workspace.ToolResult{
		{Tool: "go_file_outline", Summary: "Outlined 4 top-level declaration(s) in internal/surfaces/tui/app/prompt_routing.go.", Output: "package app"},
	}
	if _, _, reject := prematureTargetReadRejection(req, concreteEvidence, brief); reject {
		t.Fatal("expected concrete non-target file inspection to unlock the target-file read")
	}
}

func TestEvidenceFirstTargetReadRedirectStartsBoundedTargetReadAfterConcreteEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: "classifyTurnRoute"})
	if !ok {
		t.Fatal("expected post-evidence source exploration to redirect into the target file")
	}

	if redirected.Tool != "read_file" || redirected.Path != "docs/frontier-routing-implementation-plan.md" || redirected.StartLine != 1 || redirected.EndLine != 220 {
		t.Fatalf("unexpected redirect request: %#v", redirected)
	}

	alreadyRead := append([]workspace.ToolResult(nil), evidence...)

	alreadyRead = append(alreadyRead, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220",
		Output:  "doc excerpt",
	})

	if _, ok := evidenceFirstTargetReadRedirect(brief, alreadyRead, workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: "classifyTurnRoute"}); ok {
		t.Fatal("did not expect redirect after the target file had already been read")
	}
}

func TestEvidenceFirstTargetReadUnlockClearsStaleTargetReadDeadEnd(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f11 internal/surfaces/tui/app/prompt_routing.go lines 1-207", Output: "func ..."},
	}

	if !evidenceFirstTargetReadUnlocked(brief, evidence) {
		t.Fatal("expected concrete non-target evidence to unlock the target-read phase")
	}

	rejectedActions := map[string]int{
		toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"}): 1,
		"search_content:internal/surfaces/tui/app": 1,
	}
	clearEvidenceFirstTargetReadActionClasses(rejectedActions, brief)

	if got := rejectedActions[toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"})]; got != 0 {
		t.Fatalf("expected target read dead-end state to clear, got %d", got)
	}

	if got := rejectedActions["search_content:internal/surfaces/tui/app"]; got != 1 {
		t.Fatalf("expected unrelated rejected action to remain, got %d", got)
	}
}

func TestEvidenceFirstTargetReadRedirectBoundsFollowupTargetWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/surfaces/tui/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	})
	if !ok {
		t.Fatal("expected second open-ended target read to be bounded")
	}

	if redirected.Tool != "read_file" || redirected.Path != "docs/frontier-routing-implementation-plan.md" || redirected.StartLine != 221 || redirected.EndLine != 360 {
		t.Fatalf("unexpected follow-up redirect request: %#v", redirected)
	}
}
