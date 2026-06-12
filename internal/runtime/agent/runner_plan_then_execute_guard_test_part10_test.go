package agent

import (
	"context"
	"encoding/json"
	"io"
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

func TestUnscopedImplementationWriteReadyAllowsBoundedSamePathContinuation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "update session pane scrolling",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/surfaces/tui/app/sessions.go lines 1-100 (total lines: 418)",
			Output:  "package app\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/sessions.go",
			StartLine: 100,
			EndLine:   200,
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("expected bounded same-path continuation to be allowed after partial source read")
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/sessions.go",
			StartLine: 1,
			EndLine:   80,
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("covered same-path range should not be allowed")
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/sessions.go",
			StartLine: 1,
			EndLine:   500,
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("oversized same-path continuation should not be allowed")
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/sessions.go",
			StartLine: 419,
			EndLine:   500,
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("same-path continuation past known EOF should not be allowed")
	}

	atContinuationLimit := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f3 internal/surfaces/tui/app/sessions.go lines 100-200 (total lines: 418)",
		Output:  "package app\n",
	}, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f4 internal/surfaces/tui/app/sessions.go lines 201-300 (total lines: 418)",
		Output:  "package app\n",
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/surfaces/tui/app/sessions.go",
			StartLine: 301,
			EndLine:   400,
		},
		atContinuationLimit,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("same-path continuation should stop once the per-path read range cap is reached")
	}
}

func TestUnscopedImplementationWriteReadyAllowsRedirectedSourceFollowup(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "update session pane scrolling",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol", Pattern: "SessionList"},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("expected exact redirected source-body follow-up to satisfy the pending source-read obligation")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            "internal/surfaces/tui/app/sessions.go",
			Capability:      "file_outline",
			NamedGap:        "declarations needed before selecting source-body evidence",
			WhyNotAnswering: "need symbol names for the redirected source-body follow-up",
		},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("expected one exact redirected file_outline follow-up to be allowed before a symbol is known")
	}

	for name, req := range map[string]workspace.ToolRequest{
		"missing source-body capability": {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go"},
		"references":                     {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "references", Pattern: "sessions.viewport"},
		"read symbol missing pattern":    {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, implementationTargetWriteReadySourceReadAllowanceLimit) {
			t.Fatalf("%s should not satisfy the pending redirected source-body obligation", name)
		}
	}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "references", Pattern: "sessions.viewport"},
		results,
	)
	if !reject {
		t.Fatal("non-source-body code_navigate follow-up should be rejected while a pending redirect remains")
	}

	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"capability=read_symbol", "pattern/symbol", "Do not use references"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("pending redirect recovery missing executable source-body guidance %q:\n%s", want, joined)
		}
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/other.go"},
		results,
		0,
	) {

		t.Fatal("redirected source-body allowance must stay scoped to the redirected path")
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/other.go", StartLine: 1, EndLine: 80},
		results,
		0,
	) {

		t.Fatal("new source reads should wait while a redirected source-read obligation is pending")
	}

	withSourceBody := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read SessionList in internal/surfaces/tui/app/sessions.go lines 1-8",
			"1:func SessionList() []Session {",
			"2:\treturn nil",
			"3:}",
		}, "\n"),
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go"},
		withSourceBody,
		0,
	) {

		t.Fatal("redirected follow-up should close once source-body evidence exists for that path")
	}

	withOutline := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"language: go",
			"routed_tool: go_file_outline",
			"route_summary: Outlined 1 top-level declaration(s) in internal/surfaces/tui/app/sessions.go.",
			"declarations:",
			"- SessionList lines 1-8",
		}, "\n"),
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            "internal/surfaces/tui/app/sessions.go",
			Capability:      "file_outline",
			NamedGap:        "declarations needed before selecting source-body evidence",
			WhyNotAnswering: "need symbol names for the redirected source-body follow-up",
		},
		withOutline,
		0,
	) {

		t.Fatal("redirected file_outline progress should be single-use")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol", Pattern: "SessionList"},
		withOutline,
		0,
	) {

		t.Fatal("source-body follow-up should remain allowed after file_outline progress")
	}
}

func TestUnscopedImplementationWriteReadyAllowsTargetedLocalizationSearch(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix session pane cursor scrolling",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:     "search_content",
			Path:     "internal/surfaces/tui/app",
			Pattern:  "type sessionsModel",
			NamedGap: "find related session model state before editing",
		},
		results,
		0,
	) {

		t.Fatal("expected targeted source localization search to be allowed after one plausible source-body read")
	}

	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:     "search_content",
			Pattern:  "type sessionsModel",
			NamedGap: "find related session model state before editing",
		},
		results,
		0,
	) {

		t.Fatal("expected narrow code-shaped search pattern to be allowed even without a path")
	}

	for name, req := range map[string]workspace.ToolRequest{
		"empty search":        {Tool: "search_content", Path: "internal/surfaces/tui/app", NamedGap: "find related state"},
		"short search":        {Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "ui", NamedGap: "find related state"},
		"missing named gap":   {Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "type sessionsModel"},
		"broad directory":     {Tool: "search_content", Path: "internal", Pattern: "type sessionsModel", NamedGap: "find related state"},
		"repo-wide search":    {Tool: "search_content", Path: ".", Pattern: "type sessionsModel", NamedGap: "find related state"},
		"generic code search": {Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "return", NamedGap: "find related state"},
		"all-caps search":     {Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "TODO", NamedGap: "find related state"},
		"already sourced":     {Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "package app", NamedGap: "find related state"},
		"search files":        {Tool: "search_files", Pattern: "sessions", NamedGap: "find related state"},
		"generic navigation":  {Tool: "code_navigate", Path: "internal/surfaces/tui/app", Capability: "file_outline"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, 0) {
			t.Fatalf("%s should not receive write-ready targeted localization allowance", name)
		}
	}

	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "search_content", Path: "internal/surfaces/tui/app", Pattern: "type sessionsModel"},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {

		t.Fatal("targeted localization search should stop at the write-ready source expansion allowance")
	}
}

func TestCompletionGateRetryLimitAllowsTwoTextualRequiredPathRecoveries(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")

	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	if got := runner.completionGateRetryLimit(nil); got != 2 {
		t.Fatalf("textual required-path recovery retry limit = %d, want 2", got)
	}

	if !runner.textualRequiredPathCompletionRecoveryApplies(nil) {
		t.Fatal("expected textual required-path recovery to apply")
	}
}

func TestCompletionGateRetryLimitAllowsPostWriteVerificationRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "completion_gate", Summary: "Mutation completion gate rejected final answer", Error: "no successful workspace write"},
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/model_swap.go"},
	}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		t.Fatalf("post-write verification recovery retry limit = %d, want 2", got)
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatal("expected post-write verification recovery prompt")
	}

	if hideReads {
		t.Fatal("post-write verification recovery should not hide reads through this prompt")
	}

	for _, want := range []string{
		"post-write verification",
		"parser-readable verification action",
		"language-appropriate verification",
		"internal/surfaces/tui/app/model_swap.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected recovery prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestCompletionGateRetryLimitCutsOverPlanExecuteSubmitPlanRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read B in b.go lines 1-10\n\n1:func B() {}\n"},
	}

	if got := runner.completionGateRetryLimit(results); got != 1 {
		t.Fatalf("plan-execute submit_plan recovery retry limit = %d, want 1", got)
	}
}

func TestCompletionGateRetryLimitAllowsSubmitCorrectionRepeatRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		t.Fatalf("submit-correction retry limit = %d, want 2", got)
	}
}

func TestPlanExecuteLoopBudgetRecoveryKeepsUnreadCandidateReadsAvailable(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote candidates",
			Output:  `{"candidate_files":[{"path":"a.go"},{"path":"b.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-20 (total lines: 20)",
			Output:  "1:package demo\n",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "a.go", Pattern: "type Foo", Resolution: "definition"},
		results,
		"the model repeated code_navigate with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		t.Fatal("expected plan-execute loop-budget recovery")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("loop-budget recovery after source evidence should force terminal planner outcomes, got:\n%s", recovery.Prompt)
	}

	for _, want := range []string{
		"The planner already has source evidence",
		"do not call more read, search, navigation, or verification tools",
		"parser-readable `submit_plan`",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("expected recovery prompt to contain %q:\n%s", want, recovery.Prompt)
		}
	}
}

func TestPlanExecuteSubmitCorrectionLoopBudgetRecoveryIsSubmitPlanOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "b.go"},
		results,
		"read_file repeated after submit_plan coverage rejection",
		false,
	)
	if !ok {
		t.Fatal("expected submit-correction loop-budget recovery")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("submit-correction loop recovery should force terminal-only surface:\n%s", recovery.Prompt)
	}

	for _, want := range []string{
		"Submit-correction mode is already ready",
		"exactly one parser-readable `submit_plan`",
		"required edit path: a.go",
		"required edit path: b.go",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("submit-correction loop recovery missing %q:\n%s", want, recovery.Prompt)
		}
	}

	for _, forbidden := range []string{
		"exactly one terminal outcome",
		"`Decision: source-satisfied` citing",
		"`<<QUESTION>>:` naming",
		"current-state evidence",
	} {
		if strings.Contains(recovery.Prompt, forbidden) {
			t.Fatalf("submit-correction loop recovery advertised stale exit %q:\n%s", forbidden, recovery.Prompt)
		}
	}
}

func TestPlanExecuteIgnoredLoopBudgetRecoveryForcesTerminalPlannerOutcome(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote candidates",
			Output:  `{"candidate_files":[{"path":"a.go"},{"path":"b.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-20 (total lines: 20)",
			Output:  "1:package demo\n",
		},
	}

	recovery, ok := runner.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "a.go", Pattern: "type Foo", Resolution: "definition"},
		results,
		"the model repeated code_navigate with the same arguments after a loop-budget recovery",
		false,
	)
	if !ok {
		t.Fatal("expected ignored loop-budget recovery")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("ignored loop recovery should force terminal-only surface:\n%s", recovery.Prompt)
	}

	for _, want := range []string{
		"repeated code_navigate after a loop-budget recovery",
		"Read/search/navigation tools are disabled",
		"submit_plan",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("expected recovery prompt to contain %q:\n%s", want, recovery.Prompt)
		}
	}
}

func TestPlanExecuteSubmitCorrectionIgnoredLoopBudgetRecoveryIsSubmitPlanOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	recovery, ok := runner.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "b.go"},
		results,
		"read_file repeated after submit_plan coverage rejection",
		false,
	)
	if !ok {
		t.Fatal("expected ignored submit-correction loop-budget recovery")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("ignored submit-correction loop recovery should force terminal-only surface:\n%s", recovery.Prompt)
	}

	for _, want := range []string{
		"Submit-correction mode is already ready",
		"exactly one parser-readable `submit_plan`",
		"required edit path: a.go",
		"required edit path: b.go",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("ignored submit-correction loop recovery missing %q:\n%s", want, recovery.Prompt)
		}
	}

	for _, forbidden := range []string{
		"exactly one terminal planner outcome",
		"`Decision: source-satisfied` citing",
		"`<<QUESTION>>:` naming",
		"current-state evidence",
	} {
		if strings.Contains(recovery.Prompt, forbidden) {
			t.Fatalf("ignored submit-correction loop recovery advertised stale exit %q:\n%s", forbidden, recovery.Prompt)
		}
	}
}

func TestCompletionGateRejectsUnresolvedProcessIntent(t *testing.T) {
	got, rejected := unresolvedProcessIntentCompletionRejection("I'll search the codebase and inspect the TUI next.")
	if !rejected {
		t.Fatalf("expected process narration to be rejected")
	}

	if got.Tool != "completion_gate" {
		t.Fatalf("rejection should be attributed to completion gate, got %#v", got)
	}

	if !strings.Contains(got.Error, "Issue the needed tool call now") {
		t.Fatalf("rejection should point at action, got %q", got.Error)
	}

	if _, rejected := unresolvedProcessIntentCompletionRejection("I cannot proceed because the workspace is read-only."); rejected {
		t.Fatalf("concrete blocker answers should not be classified as unresolved process intent")
	}
}

func TestFinalAnswerBlockerKindExemptsPlannerPhaseWithSubmitPlan(t *testing.T) {
	// Post-answer blocker check: in the plan-execute planner phase,
	// successful submit_plan IS the deliverable — the executor phase
	// (run by the orchestrator after this runner exits) produces the
	// actual file changes. Without this exemption the blocker check
	// sees zero changed_paths and overrides the model's final answer
	// with a "required_edit_paths_remaining" narrative, even after the
	// model submitted a correct plan.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	kind, reason := runner.finalAnswerBlockerKind("Plan submitted with 2 edits.", results)

	if kind != "" || reason != "" {
		t.Fatalf("planner phase with successful submit_plan must not trigger the blocker check; got kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindStillRejectsImplementationLaneWithoutEdits(t *testing.T) {
	// Sanity: the plan-execute exemption must not weaken the post-answer
	// blocker for normal implementation lanes.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	kind, reason := runner.finalAnswerBlockerKind("Done.", nil)

	if kind != "required_edit_paths_remaining" || reason != "required_edit_paths_remaining" {
		t.Fatalf("implementation lane with no edits must trigger blocker; got kind=%q reason=%q", kind, reason)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeFiresWhenAllPathsRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results)

	if got == "" {
		t.Fatalf("expected nudge once every required path has been read; got empty string")
	}

	if !strings.Contains(got, "submit_plan") {
		t.Fatalf("nudge must point at submit_plan; got %q", got)
	}

	if !strings.Contains(got, "substantial inline body or explicit replacement edit shape") {
		t.Fatalf("nudge must explain required new-file body evidence; got %q", got)
	}

	if !strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("nudge must offer the QUESTION escape hatch; got %q", got)
	}

	if !strings.Contains(got, "source-satisfied") {
		t.Fatalf("nudge must offer the source-satisfied closeout; got %q", got)
	}

	if !strings.Contains(got, "Do not read/search/discovery again before the first valid `submit_plan`") {
		t.Fatalf("nudge must close reads after readiness; got %q", got)
	}

	if strings.Contains(got, "Only read again") {
		t.Fatalf("nudge must not retain stale optional-read language; got %q", got)
	}
}

func TestPlanThenExecuteLargeGenericHeaderReadIsNotSourceReady(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"huge.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read huge.go lines 1-180 (bounded large required-path source; file has 1200 lines total; full range-less read omitted)",
		Output:  "Bounded source packet for huge.go\n- file_lines: 1200\n- returned_lines: 1-180\nSource lines:\n1:package huge",
	}}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("generic first-window read of a large file must not reach planner readiness:\n%s", got)
	}

	noTotalResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read huge.go lines 1-180",
		Output:  "1:package huge",
	}}
	if got := planThenExecuteReadyToSubmitNudge(brief, noTotalResults); got != "" {
		t.Fatalf("generic first-window read without total line count must not reach planner readiness:\n%s", got)
	}

	largeHeaderResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read huge.go lines 1-300 (total lines: 6796)",
		Output:  "1:package huge",
	}}
	if got := planThenExecuteReadyToSubmitNudge(brief, largeHeaderResults); got != "" {
		t.Fatalf("generic 300-line header read of a very large file must not reach planner readiness:\n%s", got)
	}

	midFileRawRangeResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read huge.go lines 301-600 (total lines: 6796)",
		Output:  "301:func Helper() {}",
	}}
	if got := planThenExecuteReadyToSubmitNudge(brief, midFileRawRangeResults); got != "" {
		t.Fatalf("raw bounded read_file range of a very large Go file must not reach planner readiness without symbol/body routing:\n%s", got)
	}

	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	if _, ok := unread["huge.go"]; !ok {
		t.Fatalf("huge.go should remain pending after generic header window: %#v", unread)
	}

	nudge := planThenExecuteUnreadRequiredSourceNudge(brief, results)
	for _, want := range []string{"huge.go", "Current evidence state: body_window", "Do not call `submit_plan`"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("unread-source nudge missing %q:\n%s", want, nudge)
		}
	}

	readiness := runnerReadinessState("main", brief, results)
	joined := strings.Join(readiness.OpenObligations, "\n")

	if !strings.Contains(joined, "pending_required_source_anchors") || !strings.Contains(joined, "huge.go(body_window") {
		t.Fatalf("readiness should expose pending source anchor without source excerpts, got %#v", readiness.OpenObligations)
	}
}

func TestPlanThenExecuteSmallWholeFileReadIsSourceReady(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"small.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read small.go lines 1-24 (file has 24 lines total)",
		Output:  "1:package small\n2:\n3:func Value() int { return 1 }",
	}}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); !strings.Contains(got, "PLANNER READINESS REACHED") {
		t.Fatalf("small whole-file read should reach readiness, got:\n%s", got)
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) != 0 {
		t.Fatalf("small whole-file read should satisfy source readiness, got %#v", unread)
	}
}

func TestPlanThenExecuteSymbolBodyReadIsSourceReady(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"internal/runtime/agent/runner.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: Read Runner.Run in internal/runtime/agent/runner.go lines 88-210\n\n88:func (r *Runner) Run() {}",
	}}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); !strings.Contains(got, "PLANNER READINESS REACHED") {
		t.Fatalf("read_symbol body should reach readiness, got:\n%s", got)
	}
}

func TestPlanThenExecuteOutlineAndSearchDoNotSatisfyRequiredSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"pkg/feature.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  "pkg/feature.go:88: func Feature() bool",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nlanguage: go\nrouted_tool: go_file_outline\nroute_summary: Outlined 2 top-level declaration(s) in pkg/feature.go.",
		},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("outline/search evidence should not reach readiness:\n%s", got)
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) != 1 {
		t.Fatalf("required source should remain pending after outline/search, got %#v", unread)
	}
}

func TestPlanExecuteTextualSubmitPlanRecoveryForcesVisibleOutput(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"}}
	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        2048,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, brief, results, false)

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("textual submit_plan recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if !opts.ChatTemplateKwargs {
		t.Fatal("textual submit_plan recovery should send request-level no-thinking kwargs")
	}

	if opts.PreserveThinking {
		t.Fatalf("textual submit_plan recovery should not preserve prior reasoning")
	}

	if len(opts.Tools) != 0 || opts.ToolChoice != "" {
		t.Fatalf("textual submit_plan recovery must not depend on native tools/tool_choice, got tools=%#v choice=%q", opts.Tools, opts.ToolChoice)
	}

	if !opts.ResponseFormatJSON {
		t.Fatal("textual submit_plan recovery should request JSON-object response format")
	}
}

func TestPlanExecuteMLXReadinessRecoveryUsesTextHistoryAndStrictNoThinking(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go"}`,
	}

	type requestPayload struct {
		Tools              []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice         string                          `json:"tool_choice"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
			Preserve       bool  `json:"preserve_thinking"`
			ThinkingBudget int   `json:"thinking_budget"`
		} `json:"chat_template_kwargs,omitempty"`
		ResponseFormat map[string]string `json:"response_format,omitempty"`
		Messages       []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id,omitempty"`
			ToolCalls  []any  `json:"tool_calls,omitempty"`
		} `json:"messages"`
	}

	var (
		requests        int
		recoveryPayload requestPayload
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies)+1 {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 1 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}

			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id":   "call_submit_plan",
							"type": "function",
							"function": map[string]any{
								"name":      "submit_plan",
								"arguments": `{"rationale":"Update value.","edits":[{"path":"a.go","old_text":"return 1","new_text":"return 2","purpose":"change returned value"}]}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			})

			return
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
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "required",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(recoveryPayload.Tools) != 0 || recoveryPayload.ToolChoice != "" {
		t.Fatalf("MLX terminal recovery should stay textual instead of forcing native submit_plan, got tools=%#v choice=%q", recoveryPayload.Tools, recoveryPayload.ToolChoice)
	}

	if recoveryPayload.ResponseFormat["type"] != "json_object" {
		t.Fatalf("MLX textual terminal recovery should request JSON response format, got %#v", recoveryPayload.ResponseFormat)
	}

	if recoveryPayload.ChatTemplateKwargs == nil ||
		recoveryPayload.ChatTemplateKwargs.EnableThinking == nil ||
		*recoveryPayload.ChatTemplateKwargs.EnableThinking ||
		recoveryPayload.ChatTemplateKwargs.Preserve {

		t.Fatalf("MLX recovery should force no-thinking visible output, got %#v", recoveryPayload.ChatTemplateKwargs)
	}

	var (
		sawTextualReadAction      bool
		sawReadEvidence           bool
		sawTextualTerminalSurface bool
	)

	for _, msg := range recoveryPayload.Messages {
		if len(msg.ToolCalls) > 0 || strings.TrimSpace(msg.ToolCallID) != "" || msg.Role == "tool" {
			t.Fatalf("MLX terminal recovery history should not contain stale native tool-call messages: %#v", recoveryPayload.Messages)
		}

		if msg.Role == "system" {
			for _, stale := range []string{
				"REQUIRED SOURCE READ PENDING",
				"Next action MUST be exactly one visible source-body tool call",
				"your `read_file` call was redirected",
				"your `code_navigate` call was redirected",
			} {
				if strings.Contains(msg.Content, stale) {
					t.Fatalf("MLX terminal recovery should drop stale pre-read routing nudge %q:\n%s", stale, msg.Content)
				}
			}
		}

		if msg.Role == "assistant" && strings.Contains(msg.Content, `"tool":"read_file"`) && strings.Contains(msg.Content, `"path":"a.go"`) {
			sawTextualReadAction = true
		}

		if msg.Role == "system" && strings.Contains(msg.Content, "Read f1 a.go") {
			sawReadEvidence = true
		}

		if msg.Role == "system" && strings.Contains(msg.Content, "# Terminal Plan-Execute Textual Tool Surface") {
			sawTextualTerminalSurface = true

			if !strings.Contains(msg.Content, "Textual-tool backends must emit exactly one visible JSON object") &&
				!strings.Contains(msg.Content, "Parser-readable submit_plan contract") {

				t.Fatalf("terminal textual surface should advertise submit_plan JSON:\n%s", msg.Content)
			}

			for _, stale := range []string{
				`"tool":"read_file"`,
				`"tool":"code_navigate"`,
				"- read_file ",
				"- code_navigate ",
				`"tool":"search_content"`,
			} {
				if strings.Contains(msg.Content, stale) {
					t.Fatalf("terminal textual surface retained stale read/search example %q:\n%s", stale, msg.Content)
				}
			}
		}
	}

	if sawTextualReadAction {
		t.Fatalf("MLX terminal recovery should prune prior read_file action JSON, got messages %#v", recoveryPayload.Messages)
	}

	if !sawReadEvidence {
		t.Fatalf("MLX terminal recovery should keep read evidence while pruning action JSON, got messages %#v", recoveryPayload.Messages)
	}

	if !sawTextualTerminalSurface {
		t.Fatalf("MLX terminal recovery should replace stale read-capable scaffolding with textual submit_plan-only surface, got messages %#v", recoveryPayload.Messages)
	}

	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected submit_plan success, got:\n%s", answer)
	}
}
