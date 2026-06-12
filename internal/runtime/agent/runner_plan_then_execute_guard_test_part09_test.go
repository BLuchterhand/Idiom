package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestCompletionGateRecoveryPromptFallsBackAfterEmptyContextPack(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "0 candidates", Output: `{"candidate_files":[],"candidate_symbols":[],"tests":[]}`},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}

	if !strings.Contains(prompt, "bounded fallback discovery phase") {
		t.Fatalf("empty context_pack should trigger fallback discovery:\n%s", prompt)
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("empty context_pack recovery should not ask for another context_pack call:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryChatOptionsPreservesDirectMutationModelMode(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "context"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("direct mutation recovery should preserve thinking mode, got %#v", opts.EnableThinking)
	}

	if !opts.PreserveThinking {
		t.Fatalf("direct mutation recovery should preserve hidden-reasoning policy")
	}

	if opts.ChatTemplateKwargs {
		t.Fatalf("direct mutation recovery should not force request-level chat_template_kwargs")
	}

	if opts.MaxTokens != 2048 {
		t.Fatalf("direct mutation recovery MaxTokens = %d, want 2048", opts.MaxTokens)
	}

	if opts.ToolChoice != "auto" {
		t.Fatalf("direct mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice)
	}
}

func TestCompletionGateRecoveryChatOptionsForcesVisibleToolAfterConcreteRead(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/rendering.go", Output: "rightInfo := statusStyle.Render(...) "}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("post-read mutation recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("post-read mutation recovery should not preserve hidden reasoning")
	}

	if opts.ChatTemplateKwargs {
		t.Fatalf("post-read mutation recovery should not force request-level chat_template_kwargs")
	}

	if opts.ToolChoice != "auto" {
		t.Fatalf("post-read mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice)
	}

	if opts.MaxTokens != 1024 {
		t.Fatalf("post-read mutation recovery MaxTokens = %d, want 1024", opts.MaxTokens)
	}
}

func TestCompletionGateRecoveryChatOptionsPreservesCurrentThinkingForDirectRequiredPaths(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "write_file"}},
		},
		ToolChoice: "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-220"},
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("direct required-path recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("direct required-path recovery should drop prior hidden reasoning")
	}

	if opts.ChatTemplateKwargs {
		t.Fatalf("direct required-path recovery should not force request-level no-thinking kwargs")
	}

	if opts.ToolChoice != "auto" {
		t.Fatalf("direct required-path recovery should allow text blockers alongside tools, got %q", opts.ToolChoice)
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("direct required-path recovery MaxTokens = %d, want 4096", opts.MaxTokens)
	}
}

func TestCompletionGateRecoveryChatOptionsForcesVisibleTextualActionWhenAnchorsReady(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read a_test.go lines 1-40 (total lines: 40)", Output: "1:package a"},
	}, false)

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("textual required-path write recovery should disable hidden thinking after anchors are ready, got %#v", opts.EnableThinking)
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("textual required-path write recovery should send request-level chat template kwargs")
	}

	if opts.PreserveThinking {
		t.Fatalf("textual required-path write recovery should not preserve prior hidden reasoning")
	}

	if opts.ToolChoice != "auto" {
		t.Fatalf("textual backend should keep terminal-aware auto tool choice, got %q", opts.ToolChoice)
	}

	if opts.ResponseFormatJSON {
		t.Fatalf("implementation recovery should not force JSON response format; only textual tool terminal contracts should")
	}
}

func TestImplementationRequiredPathHiddenOnlyRecoveryStallDisablesThinkingAfterRetry(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-4 (total lines: 4)",
		Output:  "1:package main\n2:\n3:func main() {}\n",
	}}
	reply := llama.ChatMessage{ReasoningContent: "I can make the edit now."}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 0, false) {
		t.Fatal("first hidden-only recovery should keep bounded thinking available")
	}

	if !implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 1, false) {
		t.Fatal("second hidden-only anchor-ready textual recovery should disable hidden thinking")
	}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, llama.ChatMessage{Content: `{"tool":"edit_file"}`}, 1, false) {
		t.Fatal("visible content should not be treated as hidden-only stall")
	}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 1, true) {
		t.Fatal("native tool backends should not use textual hidden-only fallback")
	}
}

func TestMutationHiddenOnlyNoWriteTerminalRecoveryRequiresConcreteSourceEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
	}
	reply := llama.ChatMessage{ReasoningContent: "I know the edit now."}
	sourceRead := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-3 (total lines: 3)",
		Output:  "1:package a\n2:\n3:func A() {}\n",
	}}

	if !mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 1, false) {
		t.Fatal("hidden-only textual retry after concrete source read and no writes should enter terminal write/blocker recovery")
	}

	outlineOnly := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "capability: file_outline\nroute_summary: Outlined 1 top-level declaration(s) in a.go.",
	}}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, outlineOnly, reply, 1, false) {
		t.Fatal("outline-only evidence must not satisfy concrete source-body recovery precondition")
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 0, false) {
		t.Fatal("first completion-gate rejection should keep normal bounded recovery")
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 1, true) {
		t.Fatal("native tool backends should not use textual hidden-only terminal recovery")
	}

	plannerBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Contract:   "implementation_result",
	}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(plannerBrief, sourceRead, reply, 1, false) {
		t.Fatal("plan-execute planner recovery must keep submit_plan/source-satisfied/question terminal contract")
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, append(sourceRead, workspace.ToolResult{Tool: "edit_file", Summary: "Edited a.go"}), reply, 1, false) {
		t.Fatal("successful writes should disable no-write terminal recovery")
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, llama.ChatMessage{Content: `{"tool":"edit_file"}`, ReasoningContent: "edit"}, 1, false) {
		t.Fatal("visible content should not be treated as a hidden-only stall")
	}
}

func TestHiddenOnlyNoWriteTerminalRecoveryPromptDoesNotChoosePatchShape(t *testing.T) {
	prompt := hiddenOnlyNoWriteTerminalRecoveryPrompt()
	for _, want := range []string{
		"Hidden thinking is disabled",
		"Read/search/discovery tools are hidden",
		"parser-readable write tool call",
		"`<<QUESTION>>:`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("terminal recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, notWant := range []string{
		`"tool":"edit_file"`,
		`"tool":"apply_patch"`,
		"old_text",
		"new_text",
		"*** Begin Patch",
		"candidate/path.go",
	} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("terminal recovery prompt should not choose an edit path or patch shape (%q):\n%s", notWant, prompt)
		}
	}
}

func TestSalvageToolRequestFromEmptyReasoning(t *testing.T) {
	runner := &Runner{}
	req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "I should inspect it.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"internal/surfaces/tui/app/rendering.go\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"render branch\",\"why_not_answering\":\"need source\"}\n```",
	}, nil)

	if !ok {
		t.Fatalf("expected reasoning tool request to be salvaged")
	}

	if req.Tool != "read_file" || req.Path != "internal/surfaces/tui/app/rendering.go" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsPlanExecuteSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "I have the anchor now.\n\n```tool\n" +
			`{"tool":"submit_plan","rationale":"Replace the banner model path with the configured alias.","edits":[{"path":"internal/surfaces/tui/app/rendering.go","old_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.modelID, \"unknown\"))","new_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.currentAliasForRole(\"writer\"), \"unknown\"))","purpose":"Display the model alias in the TUI banner instead of the raw model path."}]}` +
			"\n```",
	}, nil); ok {
		t.Fatalf("hidden-reasoning submit_plan must not execute; require visible/native tool call, got %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsSubmitPlanOutsidePlanSubmission(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
		},
	}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"Plan.","edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace text"}]}` +
			"\n```",
	}, nil); ok {
		t.Fatalf("submit_plan salvage must stay scoped to plan-submission phases, got %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsSubmitPlanPlaceholders(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"why these candidate path edits satisfy the objective","edits":[{"path":"candidate/path.go","old_text":"exact current text","new_text":"replacement text","purpose":"one sentence"}]}` +
			"\n```",
	}, nil); ok {
		t.Fatalf("placeholder submit_plan request should not be salvaged: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsPlaceholders(t *testing.T) {
	runner := &Runner{}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "```tool\n{\"tool\":\"edit_file\",\"path\":\"candidate/path.go\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\"}\n```",
	}, nil); ok {
		t.Fatalf("placeholder request should not be salvaged: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsMutatingToolsWithoutRequiredScope(t *testing.T) {
	runner := &Runner{}
	for _, reasoning := range []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"a.txt\",\"old_text\":\"old\",\"new_text\":\"new\"}\n```",
		"```tool\n{\"tool\":\"write_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"append_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"apply_patch\",\"content\":\"*** Begin Patch\\n*** Update File: a.txt\\n@@\\n-old\\n+new\\n*** End Patch\\n\"}\n```",
	} {
		if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
			Content:          "",
			ReasoningContent: reasoning,
		}, nil); ok {
			t.Fatalf("hidden-reasoning mutating request should not be salvaged: %#v", req)
		}
	}
}

func TestDeterministicNoWriteRecoveryReadsSearchContentCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line.",
		Output:  "internal/surfaces/tui/app/rendering.go:50: banner",
	}})

	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}

	if req.Tool != "read_file" || req.Path != "internal/surfaces/tui/app/rendering.go" {
		t.Fatalf("unexpected recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryRanksUISourceOverSchemasAndComments(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`f33 internal/capabilities/workspace/tool_schemas.go:120: "model alias"`,
			`f6 internal/surfaces/tui/app/approval.go:51:// invariant that the banner always matches a real request`,
			`f8 internal/surfaces/tui/app/rendering.go:50: if banner := m.errorBanner(); banner != "" {`,
			`f8 internal/surfaces/tui/app/rendering.go:51: footerLines = append(footerLines, errorStyle.Render(banner))`,
			`f3 internal/frontier/locate.go:169: if ContainsAny(normalized, "top", "header", "banner") {`,
		}, "\n"),
	}})

	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}

	if req.Tool != "read_file" || req.Path != "internal/surfaces/tui/app/rendering.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryPrefersObjectiveSpecificApplyPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         syntheticPolicyGuardApplyObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output:  syntheticPolicyGuardApplySearchOutput(),
	}})

	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}

	if req.Tool != "read_file" || req.Path != "internal/policy/guard_apply.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryDoesNotReadUnreadBetterCandidateAfterFirstRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         syntheticPolicyGuardApplyObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  syntheticPolicyGuardApplySearchOutput(),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/policy/guard.go lines 1-180",
			Output:  "type PolicyGuardPlan struct{}",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results)

	if ok {
		t.Fatalf("candidate evidence already exists; deterministic recovery must not force another candidate read, got %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryKeepsNonGoSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the banner model path with the actual model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`f1 web/src/components/Banner.tsx:42: <HeaderBanner modelAlias={activeAlias} />`,
			`f2 docs/banner.md:4: banner model alias notes`,
		}, "\n"),
	}})

	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}

	if req.Tool != "read_file" || req.Path != "web/src/components/Banner.tsx" {
		t.Fatalf("unexpected non-Go recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoverySearchesAfterContextPackOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoverySearch([]workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "packet",
		Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/model_popup.go"}]}`,
	}})

	if !ok {
		t.Fatalf("expected deterministic search recovery")
	}

	if req.Tool != "search_content" || !strings.Contains(req.Pattern, "banner") {
		t.Fatalf("unexpected recovery search request: %#v raw=%s", req, raw)
	}

	if strings.Contains(req.Pattern, "model alias") || strings.Contains(req.Pattern, "alias") {
		t.Fatalf("recovery search should avoid broad replacement terms, got pattern %q", req.Pattern)
	}
}

func TestDeterministicNoWriteRecoverySearchesAfterFailedContextPack(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoverySearch([]workspace.ToolResult{{
		Tool:  "context_pack",
		Error: "context index unavailable",
	}})

	if !ok {
		t.Fatalf("expected deterministic search recovery after failed context_pack")
	}

	if req.Tool != "search_content" || !strings.Contains(req.Pattern, "banner") {
		t.Fatalf("unexpected recovery search request after failed context_pack: %#v raw=%s", req, raw)
	}
}

func TestUnscopedContextPackCandidateReadRecoveryAllowsStructuredCandidateRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	summary, errText, prompt, rejected := runner.unscopedContextPackCandidateReadRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/model_popup.go"},
		[]workspace.ToolResult{{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/surfaces/tui/app/model_popup.go"}]}`}},
	)

	if rejected {
		t.Fatalf("structured context_pack candidate read should be allowed, got summary=%q err=%q prompt=%q", summary, errText, prompt)
	}
}

func TestUnscopedContextPackCandidateReadRecoveryRejectsUngroundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	summary, errText, prompt, rejected := runner.unscopedContextPackCandidateReadRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/model_popup.go"},
		[]workspace.ToolResult{{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/surfaces/tui/app/other.go"}]}`}},
	)

	if !rejected {
		t.Fatalf("expected context-pack-only read to be rejected")
	}

	for _, want := range []string{
		"Context packet candidate read rejected",
		"context_pack has not established an exact edit target",
		"bounded fallback discovery action",
		"literal words, symbols, or behavior",
	} {
		if !strings.Contains(summary+"\n"+errText+"\n"+prompt, want) {
			t.Fatalf("expected recovery text to contain %q, got summary=%q err=%q prompt=%q", want, summary, errText, prompt)
		}
	}

	for _, forbidden := range []string{"```tool", `"tool":"search_content"`, "requested behavior|target symbol|current source"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("recovery text must not include copyable placeholder/template %q: %s", forbidden, prompt)
		}
	}
}

func TestCompletionGateRetryLimitAllowsTwoUnscopedNoWriteRecoveries(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{Tool: "search_content", Summary: "No content matches"}}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		t.Fatalf("unscoped no-write recovery retry limit = %d, want 2", got)
	}

	if !runner.unscopedNoWriteCompletionRecoveryApplies(results) {
		t.Fatalf("expected unscoped no-write recovery to apply")
	}
}

func TestUnscopedNoWriteRecoveryRequiresSourceBodyBeforeWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/surfaces/tui/app/banner.go"}]}`},
		{Tool: "completion_gate", Summary: "Mutation completion gate rejected final answer", Error: "no successful workspace write"},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatal("expected unscoped no-write recovery prompt")
	}

	if strings.Contains(prompt, "call one write tool now") {
		t.Fatalf("prompt should not tell the model to write before source-body evidence, got:\n%s", prompt)
	}

	for _, want := range []string{"source-body evidence", "exact replacement shape", "named_gap"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestUnscopedNoWriteRecoveryDoesNotAutoReadAfterWriteReadyGate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "packet",
			Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 docs/design-tdd-coverage.md lines 1-180 (total lines: 815)",
			Output:  "Phase 1: EARS And BDD Coverage Checks\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/workflows/productworkflow/coverage_gate.go lines 1-180 (total lines: 552)",
			Output:  "func EvaluateCoverageGate() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in unscoped implementation write-ready state")
	}

	if req, raw, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results); ok {
		t.Fatalf("write-ready recovery must not inject another candidate read after no-write gate, got %#v raw=%s", req, raw)
	}
}

func TestUnscopedImplementationWriteReadyRejectsNonWriteTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-120 (total lines: 552)",
		Output:  "func EvaluateCoverageGate() {}\n",
	}, {
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
	)
	if !reject {
		t.Fatal("expected non-write tool to be rejected once unscoped implementation is write-ready")
	}

	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"target-write-ready", "parser-readable write", "<<QUESTION>>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}

	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "edit_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
	); reject {
		t.Fatal("write tools must remain available in target-write-ready recovery")
	}
}

func TestUnscopedImplementationWriteReadyDoesNotBlockOnSupersededRedirect(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix the session pane cursor scrolling",
			PermissionProfile: "guarded_workspace_write",
			TargetPacket: []string{
				"candidate file: internal/surfaces/tui/app/implementation_dispatch.go",
				"candidate file: internal/surfaces/tui/app/sessions_panel.go",
			},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/implementation_dispatch.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/implementation_dispatch.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in implementation write-ready state after plausible source-body evidence")
	}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/model_state.go"},
		results,
	)
	if !reject {
		t.Fatal("expected unrelated navigation to be rejected after write-ready")
	}

	joined := summary + " " + errorText + " " + prompt
	if strings.Contains(joined, "pending redirected source-body obligation") ||
		strings.Contains(joined, "implementation_dispatch.go via code_navigate") {

		t.Fatalf("stale redirect should not dominate write-ready recovery after plausible source body was read: %q", joined)
	}

	for _, want := range []string{"Source-body evidence is already available", "internal/surfaces/tui/app/sessions_panel.go", "parser-readable write"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}
}

func TestUnscopedImplementationWrongTargetReadDoesNotRejectFurtherLocalization(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/surfaces/tui/app/app.go` (read_only_evidence): weak packet candidate",
				"- `internal/runtime/agent/phase_contract.go` (editable): matched task terms",
				"- `internal/runtime/agent/runner.go` (editable): matched task terms",
			},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/surfaces/tui/app/app.go lines 1-55 (total lines: 55)",
		Output:  "package app\n\ntype Model struct{}\n",
	}}

	if implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("non-plausible first source read should not lock implementation into write-ready")
	}

	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "context_pack", Task: "find session panel scrolling code"},
		results,
	); reject {
		t.Fatal("target-source localization must remain available after a non-plausible first source read")
	}
}

func TestUnscopedImplementationWriteReadyRejectsNonWriteAfterSymbolBody(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-8",
				"1:func EvaluateCoverageGate() bool {",
				"2:\treturn true",
				"3:}",
			}, "\n"),
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("symbol-body evidence should put unscoped implementation into write-ready state")
	}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "docs/obsidian-export/docs/design-prd-adr-brief-research-workflow.md"},
		results,
	)
	if !reject {
		t.Fatal("expected broad write-ready read to be rejected after symbol-body source evidence")
	}

	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"target-write-ready", "parser-readable write", "<<QUESTION>>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}
}

func TestUnscopedImplementationWriteReadyAllowsOneSamePathSourceRefreshAfterSymbolBody(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-8",
				"1:func EvaluateCoverageGate() bool {",
				"2:\treturn true",
				"3:}",
			}, "\n"),
		},
	}

	if !runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
		0,
	) {

		t.Fatal("expected one same-path source refresh to be allowed after symbol-body evidence")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/workflows/productworkflow/coverage_gate_test.go",
			StartLine: 1,
			EndLine:   80,
			NamedGap:  "source-body evidence for newly selected implementation target",
		},
		results,
		0,
	) {

		t.Fatal("expected one bounded new-target source read to be allowed after a different path made the turn write-ready")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool: "read_file",
			Path: "internal/workflows/productworkflow/coverage_gate_test.go",
		},
		results,
		1,
	) {

		t.Fatal("expected rangeless new-target source read to remain available after one unrelated write-ready source expansion")
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/workflows/productworkflow/coverage_gate_test.go",
			StartLine: 1,
			EndLine:   80,
			NamedGap:  "source-body evidence for newly selected implementation target",
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("new-target source reads should stop at the write-ready source expansion allowance")
	}

	for name, req := range map[string]workspace.ToolRequest{
		"design doc path": {Tool: "read_file", Path: "docs/obsidian-export/docs/design-prd-adr-brief-research-workflow.md"},
		"search":          {Tool: "search_content", Pattern: "coverage"},
		"write":           {Tool: "edit_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, 0) {
			t.Fatalf("%s should not receive the source-read allowance", name)
		}
	}

	if runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
		1,
	) {

		t.Fatal("source-refresh allowance must be capped at one use")
	}

	withFullRead := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-8 (total lines: 8)",
		Output:  "1:func EvaluateCoverageGate() bool {\n2:\treturn true\n3:}\n",
	})
	if runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		withFullRead,
		0,
	) {

		t.Fatal("same-path source refresh should not be allowed after a full read_file result")
	}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		withFullRead,
	)
	if !reject {
		t.Fatal("range-less full reread after a full source read should be rejected as non-progress")
	}

	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"target-write-ready", "internal/workflows/productworkflow/coverage_gate.go", "already available", "parser-readable write"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected full-reread recovery text to contain %q, got %q", want, joined)
		}
	}

	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/workflows/productworkflow/coverage_gate.go", Capability: "file_outline"},
		withFullRead,
	); !reject {
		t.Fatal("same-path navigation after a full source read should be rejected as non-progress")
	}

	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go", StartLine: 4, EndLine: 8},
		withFullRead,
	); !reject {
		t.Fatal("new ranged reads after a full source read should be rejected in write-ready state")
	}
}
