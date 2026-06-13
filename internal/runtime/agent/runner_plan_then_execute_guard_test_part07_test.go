package agent

import (
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanExecuteUnscopedRejectedPlanCorrectionUsesLargeBudget(t *testing.T) {
	thinkingOff := false
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391", Output: "package productworkflow\n"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow.go lines 1-180", Output: "package main\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "edits[0]: edit would not change internal/workflows/productworkflow/coverage_gate.go; submit real edits, or answer `Decision: source-satisfied` with current-state evidence when no changes are needed"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("unscoped candidate correction should allow full textual submit_plan budget, got %d", opts.MaxTokens)
	}

	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("unscoped correction should expose only submit_plan, got tool_choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}

func TestPlanExecuteRejectedPlanCorrectionKeepsLargeBudgetForWholeFile(t *testing.T) {
	thinkingOff := false
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Correct the rejected submit_plan with a whole-file fixture replacement.",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate_report.go", "internal/workflows/productworkflow/coverage_gate_report_test.go", "internal/workflows/productworkflow/coverage_gate_fixture.json"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/workflows/productworkflow/coverage_gate_report.go lines 1-40"},
		{Tool: "read_file", Summary: "Read f2 internal/workflows/productworkflow/coverage_gate_report_test.go lines 1-40"},
		{Tool: "read_file", Summary: "Read f3 internal/workflows/productworkflow/coverage_gate_fixture.json lines 1-80"},
		{Tool: "submit_plan", Summary: "submit_plan rejected: invalid edit shape", Error: "replace_all requires a full file body for internal/workflows/productworkflow/coverage_gate_fixture.json"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("whole-file correction should preserve full submit_plan budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteTextualFallbackUsesLargeBudgetForFourExistingFiles(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteTextualSubmitPlanFallbackChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("textual fallback should use large four-file submit_plan budget, got %d", opts.MaxTokens)
	}

	if opts.Tools != nil || opts.ToolChoice != "" || opts.ForceNativeTools {
		t.Fatalf("textual fallback should disable native tool transport, got force=%v choice=%q tools=%#v", opts.ForceNativeTools, opts.ToolChoice, opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking || opts.PreserveThinking {
		t.Fatalf("textual fallback should force visible no-thinking output, got enable=%#v preserve=%v", opts.EnableThinking, opts.PreserveThinking)
	}

	if !opts.ResponseFormatJSON {
		t.Fatal("textual fallback should request JSON response format")
	}
}

func TestPlanExecuteRequiredPathSymbolReadRecoveryCapsPlannerBudget(t *testing.T) {
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
	}, []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}})

	if opts.PreserveThinking {
		t.Fatalf("symbol-read required-path recovery should stop preserving prior hidden reasoning")
	}

	if opts.MaxTokens != 768 {
		t.Fatalf("symbol-read required-path recovery MaxTokens = %d, want 768", opts.MaxTokens)
	}
}

func TestPlanExecuteRequiredPathAllSourcesReadRecoveryKeepsPlannerBudget(t *testing.T) {
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
	}, []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 400-460\n\n400:func TestProductTDDCoverageGate() {}\n",
		},
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("all-source recovery should force visible submit_plan output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("all-source recovery should stop preserving prior hidden reasoning")
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("all-source recovery MaxTokens = %d, want 4096", opts.MaxTokens)
	}
}

func TestPlanExecuteStrictVisibleActionOverridesThinkingFloor(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		ModelThinkingFloor: true,
		MaxTokens:          8192,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1396-1671\n1396:func runProductTDD() {}"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read TestProductLintFailsOnInvalidTDDCoverageTier in cmd/idiom/product_workflow_test.go lines 5300-5480\n5300:func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {}"},
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("strict plan-execute visible action should disable thinking even with a thinking-floor route, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("strict plan-execute visible action should not preserve hidden reasoning")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("strict plan-execute visible action should set request-level thinking kwargs")
	}

	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("strict plan-execute visible action should require submit_plan, got choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}

func TestPlanExecuteCandidateReadyKeepsUnreadCandidateReadTools(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          8192,
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
		AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
		Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
	}})

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("candidate readiness must keep read tools visible while an allowed candidate remains unread, got %#v", opts.Tools)
	}

	if opts.ToolChoice == "required" {
		t.Fatalf("candidate readiness must not require submit_plan before unread candidate source-satisfied evidence can be read")
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("candidate readiness should still force visible actions, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteContextPackCandidateReadyFiltersSubmitPlanOnly(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
			Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
		},
	}

	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          8192,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "context_pack"}},
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, false)

	if chatOptionsExposeTool(opts, "context_pack") || chatOptionsExposeTool(opts, "read_file") {
		t.Fatalf("selected context-pack candidate readiness should remove discovery tools, got %#v", opts.Tools)
	}

	if allowed := runnerPlanExecuteTextualAllowedTools(nil, brief, results); !slices.Equal(allowed, []string{"submit_plan"}) {
		t.Fatalf("selected context-pack candidate textual allowed tools = %#v, want submit_plan", allowed)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("selected context-pack candidate readiness should force visible terminal output, got %#v", opts.EnableThinking)
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("selected context-pack candidate readiness should cap terminal budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteCandidateWriteOnlyRecoveryForcesVisibleSubmitPlanAfterRead(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteCandidateWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
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
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"}})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("write-only plan-execute recovery should force visible submit_plan output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("write-only plan-execute recovery should drop prior hidden reasoning")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("write-only plan-execute recovery should force request-level thinking kwargs")
	}

	if opts.ToolChoice != "required" {
		t.Fatalf("write-only plan-execute recovery should force submit_plan tool choice, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("write-only plan-execute recovery should expose only submit_plan, got %#v", opts.Tools)
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("write-only plan-execute recovery should cap existing-file planner budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteRequiredWriteOnlyRecoveryForcesVisibleSubmitPlan(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteRequiredWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol"}})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("required-path recovery should force visible submit_plan output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("required-path recovery should drop prior hidden reasoning")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("required-path recovery should force request-level thinking kwargs")
	}

	if opts.ToolChoice != "required" {
		t.Fatalf("required-path recovery should force submit_plan tool choice, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("required-path recovery should expose only submit_plan, got %#v", opts.Tools)
	}

	if opts.MaxTokens != 4096 {
		t.Fatalf("required-path recovery should cap existing-file planner budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecutePostReadinessRecoveryAllowsOnlyAnchorRefreshBeforeCorrectedPlan(t *testing.T) {
	thinkingOn := true
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/workflows/productworkflow/coverage_gate.go lines 1-20"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("anchor-refresh recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("anchor-refresh recovery should expose only source-body read tools, got %#v", opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("anchor-refresh recovery should force visible/tool output over hidden thinking, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteRejectedOldTextRequiresAnchorReadyRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow_test.go: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-120 (total lines: 6834)"},
		rejected,
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-100 (total lines: 6834)"},
	}

	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("generic first-window refresh must not close a stale old_text anchor rejection")
	}

	results = append(results, workspace.ToolResult{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 5300-5360 (total lines: 6834)"})
	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("raw bounded source refresh of a very large Go file should not close stale-anchor refresh requirement")
	}

	results = append(results, workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read TestProductLintFailsOnInvalidTDDCoverageTier in cmd/idiom/product_workflow_test.go lines 5300-5360\n5300:func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {}"})
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("symbol/body source refresh should close stale-anchor refresh requirement")
	}
}

func TestPlanExecuteRejectedOldTextKeepsCppRawRangeInAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 1-180 (total lines: 1593)"},
		rejected,
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 181-360 (total lines: 1593)", Output: "181:void execute_production() {}\n360:}\n"},
	}

	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("bounded non-header C++ source windows should close a stale old_text anchor rejection")
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("Mode after bounded C++ range = %q, want submit correction/ready state; state=%+v", state.Mode, state)
	}
}

func TestPlanExecuteInvalidAnchorFallbackKeepsWeakRefreshPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 1-180 (total lines: 1593)"},
		rejected,
	}
	runner := &Runner{RuntimeBrief: brief}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("<<QUESTION>>: blocked", results)

	if !ok {
		t.Fatal("weak stale-anchor evidence should still produce a deterministic refresh request")
	}

	if req.Tool != "read_file" || req.Path != "native/src/simulation/production/production_execution.cpp" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		t.Fatalf("unexpected refresh request: %#v", req)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 181-360 (total lines: 1593)",
		Output:  "181:void execute_production() {}\n360:}\n",
	})
	if req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("<<QUESTION>>: blocked", results); ok {
		t.Fatalf("bounded non-header C++ refresh should close the deterministic refresh path, got %#v", req)
	}
}

func TestPlanExecuteMissingGroundingRefreshCompletesAfterBoundedReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/simulation/production/production_execution.cpp",
			"native/tests/test_quality_inventory_bindings.cpp",
		},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `native/src/simulation/production/production_execution.cpp`; missing source-body evidence for `native/tests/test_quality_inventory_bindings.cpp`.",
	}
	results := []workspace.ToolResult{
		rejected,
	}
	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0)
	assertEqualSet(t, paths, []string{
		"native/src/simulation/production/production_execution.cpp",
		"native/tests/test_quality_inventory_bindings.cpp",
	})

	results = append(results,
		workspace.ToolResult{Tool: "read_file", Summary: "Read f1 native/src/simulation/production/production_execution.cpp lines 181-260 (total lines: 1593)"},
		workspace.ToolResult{Tool: "read_file", Summary: "Read f2 native/tests/test_quality_inventory_bindings.cpp lines 1-160 (total lines: 260)"},
	)
	if paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0); len(paths) != 0 {
		t.Fatalf("bounded reads should close missing-grounding refresh paths, got %#v", paths)
	}

	if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "native/src/core/colony_sim.h",
		StartLine: 1,
		EndLine:   80,
	}, brief, results); !reject || summary != "Corrected submit_plan required" || strings.Contains(errorText, "still needs bounded source-body refresh") {
		t.Fatalf("post-refresh discovery should require submit correction without stale refresh paths, reject=%v summary=%q error=%q", reject, summary, errorText)
	}
}

func TestPlanExecuteRejectedOldTextAllowsRoutedRefreshAfterWeakRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 1-180 (total lines: 1593)"},
		rejected,
	}
	refreshReq := workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "native/src/simulation/production/production_execution.cpp",
		Capability: "read_symbol",
		Pattern:    "apply_quality_record",
	}

	if _, _, reject := planThenExecutePostReadinessDiscoveryRejection(refreshReq, brief, results); reject {
		t.Fatal("routed symbol/body refresh must remain allowed after a raw range leaves the stale anchor unresolved")
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowedForScope(refreshReq, brief.RequiredEditPaths, results) {
		t.Fatal("source-refresh allowance must permit routed symbol/body refresh after a raw range")
	}

	results = append(results, workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via cpp_read_symbol",
		Output:  "route_summary: Read apply_quality_record in native/src/simulation/production/production_execution.cpp lines 181-260\n181:void apply_quality_record() {}",
	})
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("routed symbol/body evidence should close stale-anchor refresh")
	}
}

func TestPlanExecuteRejectedMissingSourceGroundingRequiresRefreshForRejectedEditPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-120 (total lines: 2894)"},
		rejected,
	}

	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("missing source-body grounding rejection should enable a bounded refresh for the rejected edit path")
	}

	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 1)
	if len(paths) != 1 || paths[0] != "internal/workflows/productworkflow/tdd.go" {
		t.Fatalf("refresh paths = %#v, want rejected edit path", paths)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/workflows/productworkflow/tdd.go lines 1-50 (total lines: 120)",
	})
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("successful source read after the rejected plan should close missing-grounding refresh")
	}
}

func TestPlanExecuteRejectedMissingCoverageStaysInSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.txt`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "before\n"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)", Output: "before\n"},
		rejected,
	}

	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("plain missing required-path coverage should require submit_plan correction, not anchor refresh")
	}

	if paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, len(results)-1); len(paths) != 0 {
		t.Fatalf("missing coverage must not produce refresh paths, got %#v", paths)
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction; state=%+v", state.Mode, state)
	}

	assertEqualSet(t, state.ParserVisibleTools, []string{"submit_plan"})

	if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "b.txt",
		StartLine: 1,
		EndLine:   10,
	}, brief, results); !reject || summary != "Corrected submit_plan required" || !strings.Contains(errorText, "No further read/search/discovery action is currently allowed") {
		t.Fatalf("post-rejection read should be rejected as submit-correction, got reject=%v summary=%q error=%q", reject, summary, errorText)
	}
}

func TestPlanExecuteVariedRejectedSubmitPlanStreakForcesAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/src/simulation/world/collapse_event.cpp",
			"native/tests/test_collapse_event.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/world/collapse_event.h lines 1-40 (total lines: 40)", Output: "1:#pragma once\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit instead.",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: old_text contains placeholder marker \"todo:\"; submit concrete edit text.",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: invalid edit shape",
			Error:   "edits[2]: replace_all requires a full file body for native/tests/test_collapse_event.cpp.",
		},
	}

	if streak := planThenExecuteRejectedSubmitPlanStreak(results); len(streak) != 3 {
		t.Fatalf("streak length = %d, want 3", len(streak))
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode = %q, want anchor_refresh; state=%+v", state.Mode, state)
	}
	if !slices.Equal(state.RefreshPaths, []string{"native/src/simulation/world/collapse_event.h"}) {
		t.Fatalf("refresh paths = %#v, want unresolved existing-source rewrite path", state.RefreshPaths)
	}
	if !slices.Equal(state.ParserVisibleTools, planThenExecuteAnchorRefreshToolNames()) {
		t.Fatalf("parser tools = %#v, want anchor refresh tools", state.ParserVisibleTools)
	}
	if slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("anchor refresh must not expose submit_plan terminal option: %#v", state.TerminalOptions)
	}

	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		MaxTokens: 8192,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
		},
		ToolChoice: "auto",
	}, brief, results)
	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("anchor refresh retry budget = %d, want %d", opts.MaxTokens, planThenExecuteAnchorRefreshMaxTokens)
	}
	if !chatOptionsExposeAnyTool(opts, "read_file", "code_navigate") || chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		t.Fatalf("anchor refresh chat options must expose refresh tools only, got %#v", opts.Tools)
	}
}

func TestPlanExecutePathlessRejectedSubmitPlanStreakStaysSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)", Output: "package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-20 (total lines: 20)", Output: "package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: old_text contains placeholder marker \"... existing code ...\".",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: edit would not change a.go; submit real edits.",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: invalid edit shape",
			Error:   "edits[2]: replace_all requires a full file body for generated fixture.",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction; state=%+v", state.Mode, state)
	}
	if len(state.RefreshPaths) != 0 {
		t.Fatalf("pathless rejection streak must not invent refresh paths, got %#v", state.RefreshPaths)
	}
	if !slices.Equal(state.ParserVisibleTools, []string{"submit_plan"}) {
		t.Fatalf("parser tools = %#v, want submit_plan only", state.ParserVisibleTools)
	}
	if slices.Contains(state.AllowedActionClasses, actionSourceSatisfied) || slices.Contains(state.AllowedActionClasses, actionBlocker) {
		t.Fatalf("submit correction should not advertise terminal escapes: %#v", state.AllowedActionClasses)
	}
}

func TestPlanExecuteGroundingCorrectionReadyNudgeIsSubmitPlanOnly(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/tdd.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. " +
			"Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan. " +
			"If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
	}
	results := []workspace.ToolResult{
		rejected,
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/workflows/productworkflow/tdd.go lines 1-50 (total lines: 120)",
			Output:  "1:package productworkflow\n",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction; state=%+v", state.Mode, state)
	}

	if !slices.Equal(state.LastInvalidPaths, []string{"internal/workflows/productworkflow/tdd.go"}) {
		t.Fatalf("last invalid paths = %#v, want grounding path", state.LastInvalidPaths)
	}

	nudge := planThenExecuteReadyToSubmitNudge(brief, results)
	for _, want := range []string{
		"PLANNER SUBMIT-CORRECTION READY",
		"exactly one parser-readable `submit_plan`",
		"missing source-body evidence for `internal/workflows/productworkflow/tdd.go`",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("grounding correction nudge missing %q:\n%s", want, nudge)
		}
	}

	for _, forbidden := range []string{
		"Narrow exceptions only",
		"Use `Decision: source-satisfied`",
		"Use `<<QUESTION>>:`",
		"Read each existing edit path",
		"If the feature is already present",
		"if no concrete path can be identified",
	} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("grounding correction nudge advertised stale exit %q:\n%s", forbidden, nudge)
		}
	}
}

func TestPlanExecuteSubmitPlanDiagnosticsKeepExtensionlessPathsAndDropProtocolTokens(t *testing.T) {
	coverageRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "missing required edit path(s): `.gitignore`, `go.work`; invalid edit anchor(s): `Brewfile: old_text not found in current source`, `Cargo.lock: old_text not found in current source`. Use `Decision: source-satisfied` or `<<QUESTION>>:` only as generic exceptions.",
	}
	if got := sortedRunnerPathKeys(submitPlanMissingRequiredPaths(coverageRejected)); !slices.Equal(got, []string{".gitignore", "go.work"}) {
		t.Fatalf("missing required diagnostics should retain extensionless path, got %#v", got)
	}

	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(coverageRejected)); !slices.Equal(got, []string{"Brewfile", "Cargo.lock"}) {
		t.Fatalf("invalid anchor diagnostics should retain extensionless path, got %#v", got)
	}

	existingSourceRewriteRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(existingSourceRewriteRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		t.Fatalf("whole-file existing source rewrite should become an anchor-refresh path, got %#v", got)
	}

	lineRangeWholeBodyRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors",
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(lineRangeWholeBodyRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		t.Fatalf("whole-file line-range existing source rewrite should become an anchor-refresh path, got %#v", got)
	}

	lineRangeCollapseRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(lineRangeCollapseRejected)); !slices.Equal(got, []string{"native/src/core/colony_sim_tick.cpp"}) {
		t.Fatalf("line-range source collapse should become an anchor-refresh path, got %#v", got)
	}

	headerGuardRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h lines 1-1 starts a header guard before selecting the existing file prologue/include guard; use a bounded anchor that includes the current guard/include line",
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(headerGuardRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		t.Fatalf("header-guard line-range rejection should become an anchor-refresh path, got %#v", got)
	}

	groundingRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `.dockerignore`, `Pipfile`, `Rakefile`, `yarn.lock`. " +
			"Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan. " +
			"If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
	}
	got := sortedRunnerPathKeys(submitPlanMissingSourceGroundingPaths(groundingRejected))

	if !slices.Equal(got, []string{".dockerignore", "Pipfile", "Rakefile", "yarn.lock"}) {
		t.Fatalf("grounding diagnostics should retain extensionless paths and drop protocol tokens, got %#v", got)
	}

	for _, forbidden := range []string{"Decision", "<<QUESTION>>", "read_file", "code_navigate", "read_symbol", "extract_body"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("grounding diagnostics retained protocol token %q: %#v", forbidden, got)
		}
	}

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{".gitignore", "Brewfile"},
	}
	if got := candidateRecoveryScopePaths(brief, nil, 4); !slices.Equal(got, []string{".gitignore", "Brewfile"}) {
		t.Fatalf("candidate recovery scope should retain extensionless allowed paths, got %#v", got)
	}

	refreshScope := planThenExecuteRefreshScopePathsForRejectedPlan(brief, []workspace.ToolResult{groundingRejected}, submitPlanMissingSourceGroundingPaths(groundingRejected))
	sort.Strings(refreshScope)

	if !slices.Equal(refreshScope, []string{".dockerignore", ".gitignore", "Brewfile", "Pipfile", "Rakefile", "yarn.lock"}) {
		t.Fatalf("rejected-plan refresh scope should retain extensionless diagnostic paths, got %#v", refreshScope)
	}
}

func TestPlanExecuteRepeatedExistingSourceRewriteAfterRefreshStaysSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors",
		},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction after refreshed repeated invalid rewrite; state=%+v", state.Mode, state)
	}
	if len(state.RefreshPaths) != 0 {
		t.Fatalf("repeated invalid rewrite after refresh should not reopen anchor refresh, got %#v", state.RefreshPaths)
	}

	req := workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "native/src/simulation/world/collapse_event.h", StartLine: 1, EndLine: 63}
	if planThenExecuteRejectedPlanSourceRefreshAllowed(req, brief.RequiredEditPaths, results) {
		t.Fatalf("anchor refresh should not be allowed after corrected evidence already refreshed the invalid anchor")
	}
}

func TestPlanExecutePartialPriorRefreshDoesNotCloseLaterInvalidAnchor(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-180 (total lines: 2616)", Output: "1:#include <x>\n180:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f2 native/src/core/colony_sim_tick.cpp lines 880-1050 (total lines: 2616)", Output: "880:void tick() {}\n1050:}\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode = %q, want anchor_refresh after later invalid anchor with only partial prior refresh; state=%+v", state.Mode, state)
	}
	if !slices.Equal(state.RefreshPaths, []string{"native/src/core/colony_sim_tick.cpp"}) {
		t.Fatalf("refresh paths = %#v, want core source path", state.RefreshPaths)
	}
}

func TestPlanExecuteRepeatedUnsafeSubmitCorrectionAllowsPathBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/world/collapse_event.h"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction; state=%+v", state.Mode, state)
	}
	if !state.SubmitCorrectionBlockerAllowed {
		t.Fatalf("expected repeated unsafe submit correction to allow exact blocker: %+v", state)
	}
	if !slices.Equal(state.AllowedActionClasses, []string{actionSubmitPlan, actionBlocker}) {
		t.Fatalf("allowed actions = %#v, want submit_plan + blocker", state.AllowedActionClasses)
	}
	if !slices.Equal(state.TerminalResponseShapes, planExecuteTerminalSubmitPlanQuestion()) {
		t.Fatalf("terminal shapes = %#v, want submit_plan/question", state.TerminalResponseShapes)
	}

	runner := &Runner{RuntimeBrief: brief}
	terminalBrief := runner.planThenExecuteTerminalToolBrief(results)
	if !slices.Contains(terminalBrief.StopConditions, "exact_missing_body_anchor_blocker_returned") {
		t.Fatalf("terminal brief should allow exact body/anchor blocker stop condition, got %#v", terminalBrief.StopConditions)
	}
	if slices.Contains(terminalBrief.InvalidActions, "<<QUESTION>> after source evidence is sufficient for correction") {
		t.Fatalf("terminal brief still forbids all question blockers: %#v", terminalBrief.InvalidActions)
	}

	generic := "<<QUESTION>>: blocked for native/src/simulation/world/collapse_event.h."
	if got, rejected := runner.requiredPathCompletionRecoveryRejection(generic, results); !rejected || !strings.Contains(got.Error, "missing body/anchor") {
		t.Fatalf("generic submit-correction blocker should be rejected with body/anchor guidance, got rejected=%v result=%+v", rejected, got)
	}

	exact := "<<QUESTION>>: native/src/simulation/world/collapse_event.h is blocked because no safe edit body or missing anchor evidence is available for a bounded header update."
	if got, rejected := runner.requiredPathCompletionRecoveryRejection(exact, results); rejected {
		t.Fatalf("exact submit-correction body/anchor blocker should be accepted, got %+v", got)
	}
}

func TestPlanExecuteMixedUnsafeRewriteDoesNotBypassOtherInvalidAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
	}
	mixedRejection := "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`. " +
		"edits[1]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker"
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: mixedRejection},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: mixedRejection},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: mixedRejection},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode = %q, want anchor_refresh for non-whole-file invalid anchor; state=%+v", state.Mode, state)
	}
	if !slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		t.Fatalf("refresh paths = %#v, want unresolved core source path present", state.RefreshPaths)
	}
	if state.SubmitCorrectionBlockerAllowed {
		t.Fatalf("mixed rejection must not allow terminal blocker while another path still needs refresh: %+v", state)
	}
	if answer, ok := deterministicNoToolsAnswer("main", brief, results); ok {
		t.Fatalf("unresolved mixed invalid anchor must not synthesize terminal fallback:\n%s", answer)
	}
}

func TestPlanExecuteWrongPathRefreshKeepsAnchorRefreshNonTerminal(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f4 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{Tool: "read_file", Summary: "Required new-file path does not exist yet", Error: "path \"native/src/simulation/world/collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create."},
		{Tool: "read_file", Summary: "Read f3 native/src/simulation/world/collapse_event.h lines 1-63 (total lines: 63)", Output: "1:#pragma once\n63:};\n"},
		{Tool: "read_file", Summary: "Required new-file path does not exist yet", Error: "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create."},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths, no edits outside required_edit_paths, and coverage for every required_edit_snippets final-state snippet; invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`, `native/src/simulation/world/collapse_event.h: old_text not found in current source`.",
		},
		{
			Tool:    "read_file",
			Summary: "Bounded submit_plan anchor refresh required",
			Error:   "the previous `submit_plan` was rejected before it became a valid plan artifact, and the corrected plan still needs bounded source-body refresh for: native/src/core/colony_sim_tick.cpp. Do not call `submit_plan` until the remaining anchor refresh returns.",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("wrong-path refresh rejection must keep anchor-refresh mode; state=%+v", state)
	}
	if !slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		t.Fatalf("refresh paths = %#v, want unresolved core path", state.RefreshPaths)
	}
	if answer, ok := deterministicNoToolsAnswer("main", brief, results); ok {
		t.Fatalf("pending wrong-path refresh must not synthesize terminal fallback:\n%s", answer)
	}

	runner := &Runner{RuntimeBrief: brief}
	answer := "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged."
	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.BlockerKind != "plan_execute_anchor_refresh_pending" {
		t.Fatalf("final answer blocker = %q/%q, want anchor-refresh pending", acceptance.BlockerKind, acceptance.Reason)
	}
	if !runner.retryableFinalAnswerAcceptanceRejection(acceptance, results, 0, []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate}) {
		t.Fatalf("anchor-refresh pending final answer rejection should be retryable")
	}
}

func TestPlanExecuteAnchorRefreshProjectionUsesOnlyUnresolvedRejectedPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/simulation/economy/fluid_network_system.cpp",
			"native/src/simulation/economy/fluid_network_system.h",
			"native/src/simulation/machines/machine.cpp",
			"native/tests/test_fluid_brewery_cascade.cpp",
		},
	}
	rejection := "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): " +
		"`native/src/simulation/economy/fluid_network_system.h: old_text not found in current source`, " +
		"`native/src/simulation/machines/machine.cpp: old_text not found in current source`, " +
		"`native/tests/test_fluid_brewery_cascade.cpp: old_text not found in current source`."
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/economy/fluid_network_system.cpp lines 1-120 (total lines: 230)", Output: "1:#include <x>\n120:void a();\n"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/economy/fluid_network_system.h lines 1-120 (total lines: 120)", Output: "1:#pragma once\n120:};\n"},
		{Tool: "read_file", Summary: "Read f3 native/src/simulation/machines/machine.cpp lines 1-120 (total lines: 420)", Output: "1:#include <x>\n120:void a();\n"},
		{Tool: "read_file", Summary: "Read f4 native/tests/test_fluid_brewery_cascade.cpp lines 1-120 (total lines: 180)", Output: "1:#include <x>\n120:TEST_CASE();\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected: required edit path coverage mismatch", Error: rejection},
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/economy/fluid_network_system.h lines 1-120 (total lines: 120)", Output: "1:#pragma once\n120:};\n"},
		{Tool: "read_file", Summary: "Read f6 native/tests/test_fluid_brewery_cascade.cpp lines 1-120 (total lines: 180)", Output: "1:#include <x>\n120:TEST_CASE();\n"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	want := []string{"native/src/simulation/machines/machine.cpp"}
	if !slices.Equal(state.RefreshPaths, want) {
		t.Fatalf("refresh paths = %#v, want only unresolved rejected path %#v", state.RefreshPaths, want)
	}

	runner := &Runner{RuntimeBrief: brief}
	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "native/src/simulation/economy/fluid_network_system.h", StartLine: 1, EndLine: 120},
		results,
	)
	if !ok {
		t.Fatal("wrong-path refresh should redirect to remaining unresolved path")
	}
	if got := normalizeRunnerPath(redirected.Path); got != "native/src/simulation/machines/machine.cpp" {
		t.Fatalf("redirect path = %q, want remaining unresolved machine.cpp", got)
	}
}

func TestPlanExecuteRepeatedBoundedCppReadAfterRejectionCountsAsFresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-180 (total lines: 2616)", Output: "1:#include <x>\n180:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f2 native/src/core/colony_sim_tick.cpp lines 1-180 (total lines: 2616)", Output: "1:#include <x>\n180:void a();\n"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("post-rejection bounded C++ re-read should count as fresh source evidence; state=%+v", state)
	}
	if len(state.RefreshPaths) != 0 {
		t.Fatalf("refresh paths = %#v, want none after post-rejection re-read", state.RefreshPaths)
	}
}

func TestPlanExecuteWrongPathAnchorRefreshRedirectsToPendingPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/simulation/economy/fluid_network_system.h",
			"native/src/simulation/economy/fluid_network_system.cpp",
			"native/src/simulation/machines/machine.cpp",
			"native/tests/test_fluid_brewery_cascade.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/economy/fluid_network_system.h lines 1-82 (total lines: 82)", Output: "1:#pragma once\n82:};\n"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/economy/fluid_network_system.cpp lines 1-230 (total lines: 230)", Output: "1:#include <x>\n230:}\n"},
		{Tool: "read_file", Summary: "Read f3 native/src/simulation/machines/machine.cpp lines 1-300 (total lines: 600)", Output: "1:#include <x>\n300:}\n"},
		{Tool: "read_file", Summary: "Read f4 native/tests/test_fluid_brewery_cascade.cpp lines 1-110 (total lines: 110)", Output: "1:#include <x>\n110:}\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/simulation/economy/fluid_network_system.h: old_text not found in current source`, `native/src/simulation/economy/fluid_network_system.cpp: old_text not found in current source`, `native/src/simulation/machines/machine.cpp: old_text not found in current source`, `native/tests/test_fluid_brewery_cascade.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/economy/fluid_network_system.h lines 1-82 (total lines: 82)", Output: "1:#pragma once\n82:};\n"},
	}
	runner := &Runner{RuntimeBrief: brief}

	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "native/src/simulation/economy/fluid_network_system.h", StartLine: 1, EndLine: 120},
		results,
	)
	if !ok {
		t.Fatal("wrong-path anchor refresh should redirect instead of consuming a rejected tool turn")
	}
	if redirected.Path != "native/src/simulation/economy/fluid_network_system.cpp" || redirected.StartLine != 1 || redirected.EndLine != 230 {
		t.Fatalf("redirect = %#v, want next pending cpp refresh from known source extent", redirected)
	}

	results = append(results, workspace.ToolResult{Tool: "read_file", Summary: "Read f6 native/src/simulation/economy/fluid_network_system.cpp lines 1-230 (total lines: 230)", Output: "1:#include <x>\n230:}\n"})
	redirected, _, ok = runner.planThenExecuteRejectedPlanRefreshReadRedirect(
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "native/src/simulation/economy/fluid_network_system.cpp", StartLine: 101, EndLine: 230},
		results,
	)
	if !ok {
		t.Fatal("continuation read for a completed refresh path should redirect to next pending path")
	}
	if redirected.Path != "native/src/simulation/machines/machine.cpp" || redirected.StartLine != 301 || redirected.EndLine != 480 {
		t.Fatalf("redirect = %#v, want next pending machine refresh continuation", redirected)
	}
}

func TestPlanExecuteActiveAnchorRefreshReadGrantsWriteOnlyAllowance(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/simulation/economy/fluid_network_system.h",
			"native/src/simulation/economy/fluid_network_system.cpp",
			"native/src/simulation/machines/machine.cpp",
			"native/tests/test_fluid_brewery_cascade.cpp",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/simulation/economy/fluid_network_system.h lines 1-82 (total lines: 82)", Output: "1:#pragma once\n82:};\n"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/economy/fluid_network_system.cpp lines 1-230 (total lines: 230)", Output: "1:#include <x>\n230:}\n"},
		{Tool: "read_file", Summary: "Read f3 native/src/simulation/machines/machine.cpp lines 1-300 (total lines: 600)", Output: "1:#include <x>\n300:}\n"},
		{Tool: "read_file", Summary: "Read f4 native/tests/test_fluid_brewery_cascade.cpp lines 1-110 (total lines: 110)", Output: "1:#include <x>\n110:}\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/simulation/machines/machine.cpp: old_text not found in current source`.",
		},
	}
	runner := &Runner{RuntimeBrief: brief}
	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            "native/src/simulation/machines/machine.cpp",
		StartLine:       301,
		EndLine:         480,
		NamedGap:        "refresh current source evidence for the rejected submit_plan edit path",
		WhyNotAnswering: "need bounded source evidence before resubmitting the corrected plan",
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || !slices.Contains(state.RefreshPaths, req.Path) {
		t.Fatalf("state = %+v, want open anchor refresh for %s", state, req.Path)
	}
	if !runner.planThenExecuteActiveAnchorRefreshReadAllowed(req, results, 0) {
		t.Fatalf("active anchor-refresh read should receive the same write-only recovery allowance as a redirected refresh read")
	}
	if runner.planThenExecuteActiveAnchorRefreshReadAllowed(req, results, planThenExecuteReadySourceRefreshLimit) {
		t.Fatalf("active anchor-refresh read should honor the shared source-refresh turn cap")
	}
	directAllowanceResults := slices.Clone(results[:3])
	directAllowanceResults = append(directAllowanceResults, results[4])
	if reason, ok := runner.planThenExecuteDirectSourceRefreshAllowed(req, directAllowanceResults, 0); !ok || reason != "active anchor-refresh path" {
		t.Fatalf("direct source-refresh allowance = %q, %v; want active anchor-refresh path", reason, ok)
	}
	if _, ok := runner.planThenExecuteAnchorRefreshWriteOnlyRejection(req, results); !ok {
		t.Fatal("anchor-refresh write-only rejection surface should be open before the allowance is granted")
	}
	if summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("required-path gate rejected allowed anchor refresh: %s\n%s", summary, errorText)
	}
	if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("post-readiness gate rejected allowed anchor refresh: %s\n%s", summary, errorText)
	}
}

func TestPlanExecuteRepeatedNoopProofCorrectionAllowsPathBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/tests/test_collapse_event.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_collapse_event.cpp lines 1-1", Error: "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence."},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 7; submit a concrete state assertion or report a blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 6; submit a concrete state assertion or report a blocker",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction; state=%+v", state.Mode, state)
	}
	if !state.SubmitCorrectionBlockerAllowed {
		t.Fatalf("expected repeated no-op proof correction to allow exact blocker: %+v", state)
	}

	runner := &Runner{RuntimeBrief: brief}
	exact := "<<QUESTION>>: native/src/core/colony_sim_tick.cpp and native/tests/test_collapse_event.cpp are blocked because gathered evidence lacks a safe edit body or missing anchor evidence for the required collapse dispatch/test changes."
	if got, rejected := runner.requiredPathCompletionRecoveryRejection(exact, results); rejected {
		t.Fatalf("exact no-op proof body/anchor blocker should be accepted, got %+v", got)
	}
}

func TestPlanExecuteNoopProofBeforeThresholdKeepsPendingRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 7; submit a concrete state assertion or report a blocker",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.SubmitCorrectionBlockerAllowed {
		t.Fatalf("blocker must not be allowed before repeated-rejection threshold: %+v", state)
	}

	runner := &Runner{RuntimeBrief: brief}
	generic := "<<QUESTION>>: native/src/core/colony_sim_tick.cpp is blocked."
	if got, rejected := runner.requiredPathCompletionRecoveryRejection(generic, results); !rejected || !strings.Contains(got.Error, "Submit-correction mode has exactly one valid outcome: parser-readable submit_plan") {
		t.Fatalf("generic blocker before threshold should be rejected with submit_plan correction guidance, rejected=%v result=%+v", rejected, got)
	}
}

func TestPlanExecutePathlessNoopProofDoesNotSuppressUnresolvedStreakRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 200-260 would collapse 61 non-empty source lines to 20; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 7; submit a concrete state assertion or report a blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 6; submit a concrete state assertion or report a blocker",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode = %q, want anchor_refresh while earlier pathful collapse rejection is unresolved; state=%+v", state.Mode, state)
	}
	if !slices.Equal(state.RefreshPaths, []string{"native/src/core/colony_sim_tick.cpp"}) {
		t.Fatalf("refresh paths = %#v, want unresolved core source path", state.RefreshPaths)
	}
}

func TestPlanExecuteCollapseRangeReadClosesRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /Users/ben/Documents/GitHub/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		},
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void after();\n"},
	}

	rejected := results[0]
	if paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0); len(paths) != 0 {
		t.Fatalf("bounded read covering collapse diagnostic lines should close refresh, got %#v", paths)
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		t.Fatalf("state should not keep covered collapse refresh path: %+v", state)
	}
}

func TestPlanExecuteGroundingPostReadinessRejectionSanitizesStaleRecoveryText(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/tdd.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. " +
				"Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan. " +
				"If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Target in internal/workflows/productworkflow/tdd.go lines 1-50\n1:package productworkflow\n",
		},
	}

	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/tdd.go",
		StartLine: 1,
		EndLine:   50,
	}, brief, results)
	if !reject {
		t.Fatal("extra read after grounding refresh should be rejected")
	}

	if summary != "Corrected submit_plan required" {
		t.Fatalf("summary = %q, want Corrected submit_plan required", summary)
	}

	for _, want := range []string{
		"missing source-body evidence for `internal/workflows/productworkflow/tdd.go`",
		"submit corrected edits now",
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("grounding post-readiness rejection missing %q:\n%s", want, errorText)
		}
	}

	for _, forbidden := range []string{
		"Read each existing edit path",
		"If the feature is already present",
		"if no concrete path can be identified",
		"Use `Decision: source-satisfied`",
		"Use `<<QUESTION>>:`",
	} {
		if strings.Contains(errorText, forbidden) {
			t.Fatalf("grounding post-readiness rejection replayed stale text %q:\n%s", forbidden, errorText)
		}
	}
}

func TestPlanExecuteInvalidAnchorAllowsTargetedRefreshAfterGenericRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-120 (total lines: 6834)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow_test.go: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-100 (total lines: 6834)"},
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow_test.go",
		Capability: "read_symbol",
		Pattern:    "TestProductLintFailsOnInvalidTDDCoverageTier",
	}

	if _, _, reject := planThenExecuteRequiredPathDiscoveryRejection(req, brief, results); reject {
		t.Fatal("required-path discovery guard must allow targeted symbol/body anchor refresh after a generic stale-anchor refresh")
	}

	if _, _, reject := planThenExecutePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatal("post-readiness discovery guard must allow targeted symbol/body anchor refresh until anchor-ready evidence exists")
	}
}

func TestPlanExecuteAnchorRefreshPhaseHintsDoNotForbidRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read TestProductLintFailsOnInvalidTDDCoverageTier in cmd/idiom/product_workflow_test.go lines 5300-5360\n5300:func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {}"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow_test.go: old_text not found in current source`.",
		},
	}
	got := runnerBriefWithPhaseHints(brief, results)
	joinedNext := strings.Join(got.RequiredNext, "\n")
	joinedInvalid := strings.Join(got.InvalidActions, "\n")
	joinedPolicy := strings.Join(got.SearchPolicy, "\n")

	if !strings.Contains(joinedNext, "refresh bounded source-body evidence") {
		t.Fatalf("phase hints should require bounded anchor refresh, got next=%#v", got.RequiredNext)
	}

	if strings.Contains(joinedInvalid, "read/search/source-refresh") || strings.Contains(joinedPolicy, "no read/search/discovery action is valid") {
		t.Fatalf("anchor-refresh phase must not retain submit-ready no-read authority, invalid=%#v policy=%#v", got.InvalidActions, got.SearchPolicy)
	}
}

func TestPlanExecuteMLXTerminalSubmitPlanUsesTextualSurface(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")

	runner := &Runner{
		Client: llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}

	if runner.supportsTerminalSubmitPlanNativeTools(results) {
		t.Fatal("MLX should use textual submit_plan at terminal readiness instead of forced native tool_choice=required")
	}
}

func TestPlanExecuteTerminalNativeSubmitPlanDoesNotClampAnchorRefresh(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/workflows/productworkflow/coverage_gate.go lines 1-20"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true)
	runner := &Runner{
		Client:       llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: brief,
	}

	if runner.supportsTerminalSubmitPlanNativeTools(results) {
		t.Fatalf("MLX terminal native submit_plan should stand down while rejected plan needs source refresh")
	}

	if opts.ForceNativeTools || chatOptionsExposeTool(opts, "submit_plan") || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || opts.ToolChoice != "required" {
		t.Fatalf("MLX source-refresh recovery should remain source-read-only, got force=%v tools=%#v choice=%q", opts.ForceNativeTools, opts.Tools, opts.ToolChoice)
	}

	nativeRunner := &Runner{
		Client:       llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendLlama),
		RuntimeBrief: brief,
	}
	if nativeRunner.supportsTerminalSubmitPlanNativeTools(results) {
		t.Fatalf("native submit_plan should also stand down while rejected plan needs source refresh")
	}
}

func TestPlanExecuteSubmitReadyClearsStaleReadOnlyToolSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim.h lines 1-80 (total lines: 120)"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_quality_inventory_bindings.cpp lines 1-180 (total lines: 355)"},
		{Tool: "history_range", Summary: "Read git history for native/src/core/colony_sim.h lines 1-80"},
		{Tool: "history_range", Summary: "Read git history for native/tests/test_quality_inventory_bindings.cpp lines 1-180"},
	}

	if planThenExecuteReadyToSubmitNudge(brief, results) == "" {
		t.Fatal("test setup must be terminal submit-ready")
	}

	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
		},
		ToolChoice: "required",
	}, brief, results, true)
	if chatOptionsExposeTool(opts, "read_file") || chatOptionsExposeTool(opts, "code_navigate") || opts.ToolChoice != "" || opts.ForceNativeTools || !opts.ResponseFormatJSON {
		t.Fatalf("submit-ready textual fallback must clear stale read tools, got force=%v json=%v choice=%q tools=%#v", opts.ForceNativeTools, opts.ResponseFormatJSON, opts.ToolChoice, opts.Tools)
	}

	if opts.MaxTokens != planThenExecuteRecoveryTokenLimit(brief, results) {
		t.Fatalf("submit-ready textual fallback should keep terminal synthesis budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteSourceRefreshRecoveryClearsStaleSubmitOnlySurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-80 (total lines: 120)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/coverage_gate_test.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
		},
	}
	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice: "auto",
	}, brief, results, true)

	if chatOptionsExposeTool(opts, "submit_plan") || opts.ToolChoice != "" || opts.ForceNativeTools || opts.ResponseFormatJSON {
		t.Fatalf("source-refresh recovery must clear stale submit-only surface, got force=%v json=%v choice=%q tools=%#v", opts.ForceNativeTools, opts.ResponseFormatJSON, opts.ToolChoice, opts.Tools)
	}

	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("source-refresh recovery should clamp retry budget to %d tokens, got %d", planThenExecuteAnchorRefreshMaxTokens, opts.MaxTokens)
	}

	opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		MaxTokens:  768,
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
		ToolChoice: "auto",
	}, brief, results, true)
	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("source-refresh recovery should raise inherited short retry budget to %d tokens, got %d", planThenExecuteAnchorRefreshMaxTokens, opts.MaxTokens)
	}
}

func TestPlanExecuteCompletionGateRecoveryPreservesAnchorRefreshTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim.h lines 1-80 (total lines: 120)"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_quality_inventory_bindings.cpp lines 1-180 (total lines: 355)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must cover every required_edit_paths entry; missing required edit path(s): `native/src/core/colony_sim.h`; invalid edit anchor(s): `native/tests/test_quality_inventory_bindings.cpp: old_text not found in current source`.",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true)

	if opts.ForceNativeTools || opts.ResponseFormatJSON {
		t.Fatalf("anchor-refresh completion recovery should keep normal source-refresh transport, got force=%v json=%v", opts.ForceNativeTools, opts.ResponseFormatJSON)
	}

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "submit_plan") || opts.ToolChoice != "required" {
		t.Fatalf("completion-gate recovery must preserve anchor-refresh tools only, got choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}

	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("completion-gate anchor refresh should clamp retry budget to %d tokens, got %d", planThenExecuteAnchorRefreshMaxTokens, opts.MaxTokens)
	}
}

func TestPlanExecuteLaterInvalidAnchorRequiresFreshRefreshDespitePriorCppWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 200-260 (total lines: 2700)"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/world/collapse_event.h lines 1-80 (total lines: 80)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan rejected: invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim_tick.cpp lines 51-230 (total lines: 2700)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0] path \"native/src/core/colony_sim_tick.cpp\": source line-range edit for native/src/core/colony_sim_tick.cpp lines 1-1 expands the selected range but does not preserve any selected anchor line; for insertions, use old_text/new_text or replace the anchor line(s) with unchanged anchor line(s) plus new content",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan rejected: invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`, `native/src/simulation/world/collapse_event.h: old_text not found in current source`.",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("later invalid anchor should require fresh anchor refresh, state=%#v", state)
	}
	if !slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		t.Fatalf("latest invalid core anchor should remain refreshable; refresh paths=%#v state=%#v", state.RefreshPaths, state)
	}
	if !submitPlanRejectionHasInvalidEditShape(results[4]) {
		t.Fatalf("line-range anchor preservation rejection should be classified as invalid edit shape")
	}
}
