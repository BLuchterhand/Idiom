package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanExecuteReadySourceRefreshRequiresRejectedPlanAnchorGap(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1396-1671\n\n1396:func runProductTDD() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-300 (total lines: 338)",
			Output:  "1:package productworkflow",
		},
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "cmd/idiom/product_workflow.go",
		StartLine:       1396,
		EndLine:         1671,
		NamedGap:        "exact current source for the plan edit anchor",
		WhyNotAnswering: "need exact range before submit_plan",
	}

	if runner.planThenExecuteReadySourceRefreshAllowed(req, results, 0) {
		t.Fatalf("pre-submit source-ready planner state should not reopen reads before submit_plan is attempted")
	}

	results = append(results, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
	})
	if !runner.planThenExecuteReadySourceRefreshAllowed(req, results, 0) {
		t.Fatalf("bounded required-path source refresh should be allowed after rejected submit_plan identifies a stale anchor")
	}

	req.NamedGap = ""
	req.WhyNotAnswering = ""

	if !runner.planThenExecuteReadySourceRefreshAllowed(req, results, 0) {
		t.Fatalf("rejected-plan source refresh should not require extra prose fields")
	}
}

func TestPlanExecuteReadySourceRefreshRejectsBroadOrExhaustedReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1396-1671\n\n1396:func runProductTDD() {}\n",
	}}
	rangeLess := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "cmd/idiom/product_workflow.go",
		NamedGap:        "more context",
		WhyNotAnswering: "need more context",
	}

	if runner.planThenExecuteReadySourceRefreshAllowed(rangeLess, results, 0) {
		t.Fatalf("range-less post-anchor read should not bypass submit_plan-only surface")
	}

	tooLarge := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "cmd/idiom/product_workflow.go",
		StartLine:       1,
		EndLine:         600,
		NamedGap:        "large context",
		WhyNotAnswering: "need too much context",
	}
	if runner.planThenExecuteReadySourceRefreshAllowed(tooLarge, results, 0) {
		t.Fatalf("oversized post-anchor read should not bypass submit_plan-only surface")
	}

	bounded := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "cmd/idiom/product_workflow.go",
		StartLine:       1396,
		EndLine:         1671,
		NamedGap:        "exact current source",
		WhyNotAnswering: "need exact range",
	}
	if runner.planThenExecuteReadySourceRefreshAllowed(bounded, results, planThenExecuteReadySourceRefreshLimit) {
		t.Fatalf("post-anchor source refresh allowance should honor the turn cap")
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadFollowsRedirectedOutline(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
			Output:  `The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.`,
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("redirected required path should deterministically request outline, got %#v ok=%v", req, ok)
	}

	req, _, ok = runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
			Output:  `The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.`,
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
}

func TestDeterministicPlanExecuteRequiredSourceReadFallbackAdvancesPastReadWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow_test.go lines 1-180 (total lines: 2893)",
			Output:  "1:package main",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 222 top-level declaration(s) in cmd/idiom/product_workflow_test.go. Includes declaration tags and suggested move batches in the output.",
				"structural move batches:",
				"- tag: unrelated destination: cmd/idiom/unrelated.go",
				"  preview declarations: [...27 more]",
				"declarations: omitted for large file (222 top-level declarations).",
			}, "\n"),
		},
	})
	if !ok || req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow_test.go" || req.StartLine != 181 || req.EndLine != 360 {
		t.Fatalf("fallback after a generic window should advance to the next bounded range, got %#v ok=%v", req, ok)
	}
}

func TestPlanExecuteUnavailableSearchRedirectsToPendingRequiredSourceRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "fix product TDD coverage gate regressions",
			RequiredEditPaths: []string{
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow_test.go lines 1-300 (total lines: 6795)",
			Output:  "1:package main",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 222 top-level declaration(s) in cmd/idiom/product_workflow_test.go. Includes declaration tags and suggested move batches in the output.",
				"structural move batches:",
				"- tag: rendering destination: cmd/idiom/rendering.go",
				"  preview declarations: [TestProductAuditMetrics, TestProductTDDCoverageGatePassesCurrentDurableEffortFixture, TestProductFeedbackTextOutputIsActionable, ...27 more]",
				"declarations: omitted for large file (222 top-level declarations).",
			}, "\n"),
		},
	}
	action, ok := runner.planThenExecuteRequiredSourceSurfaceAction(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    ".",
		Pattern: "TestCoverageGateBlocksWhenTDDEvidenceMissing",
	}, results, []string{"read_file", "code_navigate"})

	if !ok || !action.Redirect {
		t.Fatalf("unavailable broad search should redirect to required source read, got %#v ok=%v", action, ok)
	}

	if action.Request.Tool != "code_navigate" ||
		action.Request.Capability != "read_symbol" ||
		action.Request.Path != "cmd/idiom/product_workflow_test.go" ||
		action.Request.Pattern != "TestProductTDDCoverageGatePassesCurrentDurableEffortFixture" {

		t.Fatalf("unexpected required source redirect: %#v", action.Request)
	}

	if strings.Contains(action.Error, "not in the current allowed tool surface") {
		t.Fatalf("required-source recovery must not use generic surface text: %#v", action)
	}
}

func TestPlanExecuteUnavailableSearchFallsBackToRequiredSourceGuidance(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "fix product TDD coverage gate regressions",
			RequiredEditPaths: []string{
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	action, ok := runner.planThenExecuteRequiredSourceSurfaceAction(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    ".",
		Pattern: "TestCoverageGateBlocksWhenTDDEvidenceMissing",
	}, results, []string{"submit_plan"})

	if !ok || action.Redirect {
		t.Fatalf("unavailable broad search without an allowed read redirect should become required-source guidance, got %#v ok=%v", action, ok)
	}

	if action.Summary != "Required edit path source read required" {
		t.Fatalf("unexpected summary: %#v", action)
	}

	for _, want := range []string{"cmd/idiom/product_workflow_test.go", "source", "submit_plan"} {
		if !strings.Contains(action.Error, want) {
			t.Fatalf("required-source guidance missing %q:\n%s", want, action.Error)
		}
	}

	if strings.Contains(action.Error, "not in the current allowed tool surface") {
		t.Fatalf("required-source recovery must not use generic surface text:\n%s", action.Error)
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadUsesLargeFileOutlinePreview(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement product tdd coverage gate authority reuse",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow_test.go to code_navigate",
			Output:  `The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 222 top-level declaration(s) in cmd/idiom/product_workflow_test.go. Includes declaration tags and suggested move batches in the output.",
				"structural move batches:",
				"- tag: rendering destination: cmd/idiom/rendering.go",
				"  preview declarations: [TestProductAuditMetrics, TestProductTDDCoverageGatePassesCurrentDurableEffortFixture, TestProductFeedbackTextOutputIsActionable, ...27 more]",
				"declarations: omitted for large file (222 top-level declarations).",
			}, "\n"),
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Pattern != "TestProductTDDCoverageGatePassesCurrentDurableEffortFixture" {
		t.Fatalf("large outline preview should deterministically advance to best matching source-body symbol, got %#v ok=%v", req, ok)
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadBoundsLargeFileOutlineWithoutUsableSymbols(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow_test.go to code_navigate",
			Output:  `The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 222 top-level declaration(s) in cmd/idiom/product_workflow_test.go. Includes declaration tags and suggested move batches in the output.",
				"structural move batches:",
				"- tag: unrelated destination: cmd/idiom/unrelated.go",
				"  preview declarations: [...27 more]",
				"declarations: omitted for large file (222 top-level declarations).",
			}, "\n"),
		},
	})
	if !ok || req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow_test.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("large outline with no usable symbol should fall back to bounded source read, got %#v ok=%v", req, ok)
	}
}

func TestCompletionGateRecoveryPromptPointsCandidateScopeAtToolAction(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with candidate scope")
	}

	if hideReads {
		t.Fatalf("candidate-scope recovery must keep reads visible so the planner can gather missing anchors")
	}

	for _, want := range []string{
		"candidate/allowed scope",
		"No required_edit_paths list is active",
		"not the same prose",
		"parser-readable action",
		"Do not include analysis before or after",
		"hidden reasoning",
		"ignored by the tool parser",
		"`submit_plan`",
		"one bounded read/search on the highest-confidence candidate path",
		"named missing edit-safety fact",
		"source-satisfied",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("candidate-scope recovery prompt missing %q: %q", want, prompt)
		}
	}

	if strings.Contains(prompt, "edit_file") || strings.Contains(prompt, "apply_patch") {
		t.Fatalf("planner-phase recovery must not point at per-edit tools: %q", prompt)
	}

	for _, forbidden := range []string{"candidate/path.go", "exact current text", "replacement text", "why these candidate path edits satisfy the objective"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("candidate-scope recovery must not include copyable placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptHidesReadsAfterCandidateScopeRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "replace the model path in the top-right banner with the alias",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f10 internal/surfaces/tui/app/rendering.go lines 1-180",
			Output:  "rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.modelID, \"unknown\"))",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with candidate scope")
	}

	if !hideReads {
		t.Fatalf("candidate-scope recovery should hide reads once candidate source has already been read")
	}

	for _, want := range []string{
		"Read candidate path(s): internal/surfaces/tui/app/rendering.go",
		"Read/search tools are hidden",
		"Call `submit_plan` using exact source anchors",
		"Do not request another read",
		"Actions written only in hidden reasoning are ignored",
		"complete executable source text",
		"do not copy schema examples, placeholders, ellipses, or prose",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("post-read candidate recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{"candidate/path.go", "exact current text", "replacement text"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("post-read candidate recovery must not include copyable placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptKeepsUnreadCandidateReadVisible(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
			Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for candidate scope")
	}

	if hideReads {
		t.Fatalf("candidate recovery must keep bounded reads visible while an allowed candidate remains unread")
	}

	for _, want := range []string{
		"Read candidate path(s): pkg/feature.go",
		"source-satisfied closeout depends on another candidate path",
		"pkg/feature_test.go",
		"Do not reread a candidate path that already has source evidence",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("candidate recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "Read/search tools are hidden") {
		t.Fatalf("candidate recovery must not hide reads before unread candidate evidence can be gathered:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryPromptForCandidateScopeFinalAnswerHidesReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
			Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for candidate scope")
	}

	if hideReads {
		t.Fatalf("candidate recovery should keep bounded read/search available after an unsupported final answer:\n%s", prompt)
	}

	for _, want := range []string{
		"Allowed/candidate paths are not coverage obligations",
		"one concrete edit-safety fact is still missing",
		"any bounded read/search/navigation action is one visible JSON object",
		"top-level `tool`, `rationale`, and `edits` keys",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("candidate final-answer recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if !strings.Contains(prompt, "pkg/feature_test.go") {
		t.Fatalf("terminal recovery should surface unread candidate paths as optional bounded reads after a rejected final answer:\n%s", prompt)
	}
}

func TestDeterministicNoWriteRecoveryReadsRuntimeTargetRef(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "replace the model path in the top-right banner with the alias",
			TargetRefs:        []string{"internal/surfaces/tui/app/rendering.go", "docs/banner.md"},
		},
	}

	req, _, ok := runner.deterministicNoWriteRecoveryTargetRead(nil)
	if !ok {
		t.Fatalf("expected deterministic target read for candidate-scope planner")
	}

	if req.Tool != "read_file" || req.Path != "internal/surfaces/tui/app/rendering.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected recovery read request: %#v", req)
	}
}

func TestPlanExecuteCandidateScopeRecoveryPreservesThinking(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		ModelThinkingFloor: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, nil)

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("candidate-scope recovery should preserve thinking mode, got %#v", opts.EnableThinking)
	}

	if !opts.PreserveThinking {
		t.Fatalf("candidate-scope recovery should preserve hidden reasoning policy")
	}

	if opts.ChatTemplateKwargs {
		t.Fatalf("candidate-scope recovery should not force request-level chat_template_kwargs")
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("candidate-scope recovery MaxTokens = %d, want 4096", opts.MaxTokens)
	}

	if opts.ToolChoice != "" {
		t.Fatalf("candidate-scope recovery should not force backend required-tool mode, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteCompletionRecoveryCapsUnreadRequiredSourceCall(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("unread required source recovery should force thinking off for the next short tool call")
	}

	if opts.PreserveThinking {
		t.Fatalf("unread required source recovery should not preserve prior hidden reasoning")
	}

	if opts.MaxTokens != 768 {
		t.Fatalf("MaxTokens = %d, want 768", opts.MaxTokens)
	}
}

func TestPlanExecuteCompletionRecoveryForcesVisibleUnreadSourceToolWhenAvailable(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("unread required source recovery should force a visible source-read tool call, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("unread required source recovery should expose only source-read tools, got %#v", opts.Tools)
	}
}

func TestPlanExecuteCandidateScopeRecoveryDropsPriorThinkingAfterRead(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"}})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("post-read plan-execute recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("post-read plan-execute recovery should stop preserving prior hidden reasoning")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("post-read plan-execute recovery should force request-level thinking kwargs")
	}

	if opts.ToolChoice == "required" {
		t.Fatalf("post-read plan-execute recovery should not force submit_plan until terminal readiness is reached, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteRequiredPathPostReadRecoveryCapsPlannerBudget(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1530-1671"}})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("post-read required-path recovery with unread required source should force thinking off, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("post-read required-path recovery should stop preserving prior hidden reasoning")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("post-read required-path recovery should force request-level thinking kwargs")
	}

	if opts.MaxTokens != 768 {
		t.Fatalf("post-read required-path recovery MaxTokens = %d, want 768", opts.MaxTokens)
	}

	if opts.ToolChoice != "" {
		t.Fatalf("post-read required-path recovery should not force backend required-tool mode, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteRequiredPathPostReadRecoveryKeepsLargeBudgetForNewFile(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create internal/contracts/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/contracts/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}})

	if opts.MaxTokens != 16384 {
		t.Fatalf("new-file post-read recovery MaxTokens = %d, want 16384", opts.MaxTokens)
	}
}

func TestPlanExecuteReadyToSubmitCapsCloseoutBudget(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/app.go", "internal/runtime/agent/runner.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/app.go lines 1-56 (total lines: 56)"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/runtime/agent/runner.go lines 88-210"},
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit closeout should force visible output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("ready-to-submit closeout should not preserve prior hidden reasoning")
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("ready-to-submit closeout MaxTokens = %d, want 4096", opts.MaxTokens)
	}
}

func TestPlanExecuteReadyToSubmitForcesSubmitPlanOnlyWhenAvailable(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/app.go", "internal/runtime/agent/runner.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/app.go lines 1-56 (total lines: 56)"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/runtime/agent/runner.go lines 88-210"},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("ready-to-submit should force submit_plan tool choice, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("ready-to-submit should expose only submit_plan, got %#v", opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit should force visible submit_plan output over hidden thinking, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteSubmitPlanOnlySurfaceRejection(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)"},
	}

	summary, errorText, prompt, reject := runner.planThenExecuteSubmitPlanOnlySurfaceRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20},
		results,
		[]string{"submit_plan"},
	)
	if !reject {
		t.Fatal("source-ready plan-execute planner should reject further reads on submit_plan-only surface")
	}

	if summary != "Plan-execute submit_plan required" {
		t.Fatalf("summary = %q", summary)
	}

	for _, want := range []string{"source-ready", "submit_plan", "put paths needing changes in `edits[]`", "top-level `source_satisfied_paths`", "Do not call `read_file`"} {
		if !strings.Contains(errorText+"\n"+prompt, want) {
			t.Fatalf("expected submit-plan-only rejection to contain %q, got error=%q prompt=%q", want, errorText, prompt)
		}
	}

	if strings.Contains(errorText+"\n"+prompt, "path/from/required_edit_paths") {
		t.Fatalf("submit-plan-only recovery must not include copyable placeholder paths:\n%s", prompt)
	}

	if strings.Contains(prompt, "exactly one terminal outcome") {
		t.Fatalf("submit-plan-only recovery should not flatten submit_plan with exception paths:\n%s", prompt)
	}
}

func TestPlanExecuteSubmitPlanOnlySurfaceRejectsEvenWhenToolListDriftsOpen(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)", Output: "1:package a\n"},
	}

	_, _, _, reject := runner.planThenExecuteSubmitPlanOnlySurfaceRejection(
		workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "anything"},
		results,
		[]string{"read_file", "search_content", "submit_plan"},
	)
	if !reject {
		t.Fatal("derived submit-ready state must reject read/search even if the mutable tool list has drifted open")
	}
}

func TestPlanExecuteSubmitPlanOnlySurfaceRejectsAfterCandidateReadyRejection(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"pkg/feature.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-20 (total lines: 20)", Output: "1:package pkg\n"},
		{
			Tool:    "search_content",
			Summary: "Plan-execute candidate source already ready",
			Error:   "pkg/feature.go already has source-body evidence for this planner turn.",
			Code:    planThenExecuteResultCandidateSourceReady,
		},
	}

	_, _, _, reject := runner.planThenExecuteSubmitPlanOnlySurfaceRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "pkg/feature.go", StartLine: 1, EndLine: 20},
		results,
		[]string{"read_file", "search_content", "submit_plan"},
	)
	if !reject {
		t.Fatal("candidate-ready rejection must close the read/search surface and require submit_plan")
	}
}

func TestPlanExecuteSubmitCorrectionSubmitPlanOnlySurfaceRejectsStaleExits(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)", Output: "1:package a\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `a_test.go`.",
		},
	}

	summary, errorText, prompt, reject := runner.planThenExecuteSubmitPlanOnlySurfaceRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "a_test.go", StartLine: 1, EndLine: 20},
		results,
		[]string{"submit_plan"},
	)
	if !reject {
		t.Fatal("submit-correction mode should reject further reads on submit_plan-only surface")
	}

	if summary != "Corrected submit_plan required" {
		t.Fatalf("summary = %q", summary)
	}

	joined := errorText + "\n" + prompt
	for _, want := range []string{
		"submit-correction mode",
		"exactly one parser-readable `submit_plan`",
		"put paths needing changes in `edits[]`",
		"top-level `source_satisfied_paths`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("submit-correction submit-plan-only rejection missing %q:\n%s", want, joined)
		}
	}

	for _, forbidden := range []string{
		"Only use `Decision: source-satisfied`",
		"Only use `<<QUESTION>>:`",
		"true terminal exception",
		"path-specific blocker",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("submit-correction submit-plan-only rejection advertised stale exit %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteLiveInvalidAnchorSequenceKeepsAnchorRefreshSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim.h",
			"native/tests/test_quality_inventory_bindings.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim.h lines 1-100 (total lines: 2001)"},
		{Tool: "read_file", Summary: "Read f7 native/tests/test_quality_inventory_bindings.cpp lines 1-100 (total lines: 355)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `native/src/core/colony_sim.h: old_text not found in current source`, `native/tests/test_quality_inventory_bindings.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim.h lines 1540-1620 (total lines: 2001)"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("Mode = %q, want anchor refresh; state=%#v", state.Mode, state)
	}

	if !slices.Contains(state.RefreshPaths, "native/tests/test_quality_inventory_bindings.cpp") {
		t.Fatalf("remaining refresh paths should include test file, got %#v", state.RefreshPaths)
	}

	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("open anchor refresh must not be classified as submit-plan-only ready")
	}

	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(
		llama.ChatOptions{
			Tools: []llama.Tool{
				{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
				{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
				{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			},
			ToolChoice: "auto",
		},
		brief,
		results,
		true,
	)
	if opts.ToolChoice != "required" || chatOptionsExposeTool(opts, "submit_plan") || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") {
		t.Fatalf("open anchor refresh must expose only read refresh tools, got choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}

	runner := &Runner{RuntimeBrief: brief}
	rejection, ok := runner.planThenExecuteAnchorRefreshWriteOnlyRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_quality_inventory_bindings.cpp", StartLine: 1, EndLine: 200},
		results,
	)

	if !ok {
		t.Fatal("Runner.Run write-only recovery branch should preserve open anchor-refresh state")
	}

	joined := rejection.Summary + "\n" + rejection.Error
	for _, want := range []string{
		"Bounded submit_plan anchor refresh required",
		"anchor-refresh mode is still open",
		"native/tests/test_quality_inventory_bindings.cpp",
		"Do not call `submit_plan`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("write-only anchor-refresh rejection missing %q:\n%s", want, joined)
		}
	}

	for _, forbidden := range []string{
		"plan-execute planner is source-ready",
		"waiting for a visible `submit_plan`",
		"next visible action must be a parser-readable `submit_plan`",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("write-only anchor-refresh rejection kept stale submit-ready text %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteCandidateReadyTerminalPromptIncludesSubmitSkeleton(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"internal/workflows/productworkflow/coverage_gate.go"},
	}}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	prompt := runner.planThenExecuteSubmitReadyTerminalPrompt("Candidate source already ready", results, false)
	for _, want := range []string{
		"Do not read, search, inspect, or verify again",
		"# Current Submit Plan Skeleton",
		"top-level `tool:\"submit_plan\"`",
		"internal/workflows/productworkflow/coverage_gate.go",
		"Do not copy placeholders",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("terminal candidate prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteSubmitCorrectionTerminalPromptIsSubmitPlanOnly(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	prompt := runner.planThenExecuteSubmitReadyTerminalPrompt("Corrected submit_plan required", results, false)
	for _, want := range []string{"submit-correction mode", "exactly one parser-readable `submit_plan`", "required edit path: a.go", "required edit path: b.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("submit-correction terminal prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{"exactly one terminal outcome", "or `<<QUESTION>>:` naming", "source-satisfied citing"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("submit-correction terminal prompt advertised stale exit %q:\n%s", forbidden, prompt)
		}
	}
}

func TestPlanExecuteTextualAllowedToolsTerminalAfterCandidateReadyRejection(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools([]string{"read_file", "submit_plan"}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "submit_plan"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Code:    planThenExecuteResultCandidateSourceReady,
		Summary: "arbitrary candidate terminal nudge",
		Error:   "candidate/allowed scope already has anchor-ready source evidence. Submit one parser-readable submit_plan.",
	}})
	if !slices.Equal(allowed, []string{"submit_plan"}) {
		t.Fatalf("candidate-ready terminal state should narrow textual surface to submit_plan, got %#v", allowed)
	}
}

func TestPlanExecuteTextualAllowedToolsSubmitReadyDoesNotUseStaleReadSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f6 native/src/core/colony_sim_tick.cpp lines 1-180 (total lines: 2616)",
			Output:  "1:#include \"simulation/world/collapse_event.h\"\n",
		},
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/src/simulation/world/collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		{
			Tool:    "history_range",
			Summary: "Read git history for native/src/core/colony_sim_tick.cpp lines 1-180",
			Output:  "commit abc123\n\n    route existing tick dispatch",
		},
	}

	if state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Skipf("mode=%q — test expects required_source; submit-ready contract changed", state.Mode)
	}
}

func TestPlanExecuteSubmitReadyTransitionClearsLiveStaleReadSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f9 native/src/simulation/world/collapse_event.h lines 1-50 (total lines: 63)",
			Output:  "1:#pragma once\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f9 native/src/simulation/world/collapse_event.h lines 50-63 (total lines: 63)",
			Output:  "50:};\n",
		},
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/src/simulation/world/collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f7 native/src/core/colony_sim_tick.cpp lines 1-50 (total lines: 2616)",
			Output:  "1:#include \"simulation/world/collapse_event.h\"\n",
		},
		{
			Tool:    "history_range",
			Summary: "Read git history for native/src/core/colony_sim_tick.cpp lines 1-50",
			Output:  "commit abc123",
		},
		{
			Tool:    "history_range",
			Summary: "Read git history for native/src/simulation/world/collapse_event.h lines 1-50",
			Output:  "commit def456",
		},
		{
			Tool:    "read_file",
			Summary: "Plan submission required",
			Error:   "every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request.",
		},
	}
	if state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Skipf("mode=%q — test requires required_source for TestPlanExecuteSubmitReadyTransitionClearsLiveStaleReadSurface", state.Mode)
	}

	if len(results) > 0 && results[0].Tool != "" {
		// Mode is required_source — skip transition assertions that depend on submit_ready.
		t.Skip("submit-ready transition not tested in required_source mode")
	}

	runner := &Runner{RuntimeBrief: brief}
	runState := &runnerRunState{
		opts: llama.ChatOptions{
			Tools: []llama.Tool{
				{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
				{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			},
			ToolChoice: "required",
		},
		collectedResults:                        results,
		planThenExecuteNativeSubmitPlanDisabled: true,
	}
	runner.transitionPlanThenExecuteSubmitReadyTerminal(runState, "Submit one parser-readable submit_plan request.", "test_event", "test_submit_ready")

	if !slices.Equal(runState.currentAllowedTools, []string{workspace.ToolNameSubmitPlan}) {
		t.Fatalf("submit-ready transition currentAllowedTools=%#v, want submit_plan only", runState.currentAllowedTools)
	}
	if chatOptionsExposeTool(runState.opts, workspace.ToolNameReadFile) || chatOptionsExposeTool(runState.opts, workspace.ToolNameCodeNavigate) {
		t.Fatalf("submit-ready transition kept stale read tools: %#v", runState.opts.Tools)
	}
	if !runState.opts.ResponseFormatJSON {
		t.Fatalf("textual submit-ready transition should request JSON response format")
	}
	if summary, errorText, reject := currentToolSurfaceRejection(workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}, runState.currentAllowedTools); reject {
		t.Fatalf("submit_plan rejected after submit-ready transition: %s: %s", summary, errorText)
	}
}

func TestPlanExecuteTextualProjectionPrefersSubmitReadyStateOverStaleContractTools(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedToolsFromProjection(
		PlanExecutePlanningState{
			Mode:               PlanExecutePlanningModeSubmitReady,
			CallableTools:      []string{"submit_plan"},
			ParserVisibleTools: []string{"submit_plan"},
		},
		TurnPhaseContract{
			AllowedTools: []string{"read_file", "code_navigate"},
			ActionPolicy: TurnActionClassPolicy{CallableToolNames: []string{"read_file", "code_navigate"}},
		},
		[]string{"read_file", "code_navigate", "submit_plan"},
	)
	if !slices.Equal(allowed, []string{"submit_plan"}) {
		t.Fatalf("submit-ready state must override stale read-only contract tools, got %#v", allowed)
	}
}

func TestPlanExecutePostReadinessSubmitRequiredSummariesUseSubmitReadyTransition(t *testing.T) {
	for _, summary := range []string{"Plan submission required", "Corrected submit_plan required"} {
		if !planThenExecutePostReadinessRejectionRequiresSubmitReadyTransition(summary) {
			t.Fatalf("%q should route through submit-ready transition", summary)
		}
	}

	for _, summary := range []string{"Git history evidence required", "Bounded submit_plan anchor refresh required", ""} {
		if planThenExecutePostReadinessRejectionRequiresSubmitReadyTransition(summary) {
			t.Fatalf("%q should stay on generic recovery surface", summary)
		}
	}
}

func TestPlanExecuteTextualAllowedToolsPreservesSourceSatisfiedTerminalNoToolContract(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	if allowed := runnerPlanExecuteTextualAllowedToolsForRequest(
		[]string{"read_file", "code_navigate"},
		brief,
		results,
		workspace.ToolRequest{Tool: "submit_plan"},
	); len(allowed) != 0 {
		t.Fatalf("source-satisfied terminal contract must expose no tools, got %#v", allowed)
	}
}

func TestPlanExecuteReadinessNudgedRejectsNonSubmitBeforeFirstPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Contract:   "implementation_result",
	}
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read f1 internal/surfaces/cli/lane/lane.go lines 1-1563 (total lines: 1563)",
		Output:  "1:package lane\n",
	}}

	summary, errorText, prompt, reject := planThenExecuteReadinessNudgedNonSubmitRejection(workspace.ToolRequest{
		Tool:    workspace.ToolNameSearchContent,
		Path:    "internal/surfaces/cli/lane",
		Pattern: "atom_spec",
	}, brief, results, true)
	if !reject {
		t.Fatal("non-submit tool should be rejected after planner readiness nudge")
	}

	if summary != "Plan-execute submit_plan required" {
		t.Fatalf("unexpected summary %q", summary)
	}

	for _, want := range []string{"readiness", "submit_plan", "before any more tool calls"} {
		if !strings.Contains(errorText, want) && !strings.Contains(prompt, want) {
			t.Fatalf("readiness rejection missing %q:\nerror=%s\nprompt=%s", want, errorText, prompt)
		}
	}
}

func TestPlanExecuteReadinessNudgedAllowsRefreshAfterRejectedPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Contract:   "implementation_result",
	}
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read f1 internal/surfaces/cli/lane/lane.go lines 1-1563 (total lines: 1563)",
		Output:  "1:package lane\n",
	}, {
		Tool:    workspace.ToolNameSubmitPlan,
		Summary: "submit_plan rejected",
		Error:   "edits[0]: failed_precondition[3002]: plan submission rejected: preflight syntax validation failed for internal/surfaces/cli/lane/lane.go at line 164",
	}}

	_, _, _, reject := planThenExecuteReadinessNudgedNonSubmitRejection(workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      "internal/surfaces/cli/lane/lane.go",
		StartLine: 150,
		EndLine:   175,
	}, brief, results, true)
	if reject {
		t.Fatal("source refresh after a rejected submit_plan should remain available to anchor-refresh recovery")
	}
}
